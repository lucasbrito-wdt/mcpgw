package upstreamauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const discoveryTTL = 10 * time.Minute

// ErrDiscovery is returned when the upstream's OAuth metadata is missing or fails validation.
var ErrDiscovery = errors.New("upstream OAuth discovery failed")

// Discovery is the validated outcome of RFC 9728 + RFC 8414 discovery for one upstream server.
type Discovery struct {
	// Resource is the canonical RFC 8707 resource indicator (the upstream MCP server URL).
	Resource string
	// Scope is the space-separated scope to request: WWW-Authenticate scope, else the
	// protected resource's scopes_supported, else empty.
	Scope string
	// Meta is the authorization server metadata; Meta.Issuer is the trusted issuer.
	Meta *oauthex.AuthServerMeta
}

type discoveryCache struct {
	mu      sync.Mutex
	entries map[string]discoveryEntry
}

type discoveryEntry struct {
	d       *Discovery
	expires time.Time
}

func (c *discoveryCache) get(key string, now time.Time) (*Discovery, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !now.Before(e.expires) {
		return nil, false
	}
	return e.d, true
}

func (c *discoveryCache) put(key string, d *Discovery, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]discoveryEntry)
	}
	c.entries[key] = discoveryEntry{d: d, expires: now.Add(discoveryTTL)}
}

// Discover resolves the authorization server of the MCP server at serverURL. wwwAuthenticate
// optionally carries the WWW-Authenticate headers of a 401 the caller already received; when
// empty the result may come from the cache, else the server is probed with an unauthenticated
// GET and the well-known URIs are tried.
func (m *Manager) Discover(ctx context.Context, serverURL string, wwwAuthenticate []string) (*Discovery, error) {
	resource, err := canonicalResource(serverURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDiscovery, err)
	}
	if len(wwwAuthenticate) == 0 {
		if d, ok := m.disc.get(resource, m.now()); ok {
			return d, nil
		}
		wwwAuthenticate = m.http.probe(ctx, resource)
	}
	d, err := m.discover(ctx, resource, wwwAuthenticate)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDiscovery, err)
	}
	m.disc.put(resource, d, m.now())
	return d, nil
}

func (m *Manager) discover(ctx context.Context, resource string, wwwAuthenticate []string) (*Discovery, error) {
	hint, challengeScope := parseChallenge(wwwAuthenticate)
	prm, err := m.fetchResourceMetadata(ctx, resource, hint)
	if err != nil {
		return nil, err
	}
	issuer, err := firstHTTPSIssuer(prm.AuthorizationServers)
	if err != nil {
		return nil, err
	}
	meta, err := m.fetchAuthServerMetadata(ctx, issuer)
	if err != nil {
		return nil, err
	}
	scope := challengeScope
	if scope == "" {
		scope = strings.Join(prm.ScopesSupported, " ")
	}
	return &Discovery{Resource: resource, Scope: scope, Meta: meta}, nil
}

// parseChallenge extracts resource_metadata and scope from the Bearer challenge, if any.
func parseChallenge(headers []string) (metadataURL, scope string) {
	challenges, err := oauthex.ParseWWWAuthenticate(headers)
	if err != nil {
		return "", ""
	}
	for _, c := range challenges {
		if c.Scheme == "bearer" {
			return c.Params["resource_metadata"], c.Params["scope"]
		}
	}
	return "", ""
}

func (m *Manager) fetchResourceMetadata(ctx context.Context, resource, hint string) (*oauthex.ProtectedResourceMetadata, error) {
	var candidates []string
	if hint != "" {
		candidates = append(candidates, hint)
	}
	candidates = append(candidates, wellKnownURLs("oauth-protected-resource", resource, false)...)
	var lastErr error
	for _, u := range candidates {
		var prm oauthex.ProtectedResourceMetadata
		if err := m.getMetadata(ctx, u, &prm); err != nil {
			lastErr = err
			continue
		}
		if !sameURL(prm.Resource, resource) {
			lastErr = errors.New("protected resource metadata does not match the server URL")
			continue
		}
		return &prm, nil
	}
	return nil, fmt.Errorf("protected resource metadata unavailable: %w", lastErr)
}

func (m *Manager) fetchAuthServerMetadata(ctx context.Context, issuer string) (*oauthex.AuthServerMeta, error) {
	var lastErr error
	for _, u := range authServerMetadataURLs(issuer) {
		var meta oauthex.AuthServerMeta
		if err := m.getMetadata(ctx, u, &meta); err != nil {
			lastErr = err
			continue
		}
		if err := validateAuthServerMeta(&meta, issuer); err != nil {
			return nil, err
		}
		return &meta, nil
	}
	return nil, fmt.Errorf("authorization server metadata unavailable: %w", lastErr)
}

func (m *Manager) getMetadata(ctx context.Context, rawURL string, v any) error {
	body, err := m.http.getJSON(ctx, rawURL, metadataMaxBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return errors.New("malformed metadata document")
	}
	return nil
}

func validateAuthServerMeta(meta *oauthex.AuthServerMeta, issuer string) error {
	if !sameURL(meta.Issuer, issuer) {
		return errors.New("authorization server issuer mismatch")
	}
	for _, e := range []string{meta.AuthorizationEndpoint, meta.TokenEndpoint} {
		if _, err := parseHTTPSURL(e); err != nil {
			return errors.New("authorization server endpoints must be https URLs")
		}
	}
	if meta.RegistrationEndpoint != "" {
		if _, err := parseHTTPSURL(meta.RegistrationEndpoint); err != nil {
			return errors.New("registration endpoint must be an https URL")
		}
	}
	if !slices.Contains(meta.CodeChallengeMethodsSupported, "S256") {
		return errors.New("authorization server does not advertise PKCE S256")
	}
	return nil
}

func firstHTTPSIssuer(servers []string) (string, error) {
	for _, s := range servers {
		if _, err := parseHTTPSURL(s); err == nil {
			return s, nil
		}
	}
	return "", errors.New("no https authorization server advertised")
}

// authServerMetadataURLs lists the RFC 8414 / OIDC discovery candidates for an issuer.
func authServerMetadataURLs(issuer string) []string {
	urls := wellKnownURLs("oauth-authorization-server", issuer, true)
	urls = append(urls, wellKnownURLs("openid-configuration", issuer, true)...)
	if u, err := url.Parse(issuer); err == nil && strings.Trim(u.Path, "/") != "" {
		// OIDC Discovery 1.0: the well-known suffix is appended to the issuer path.
		urls = append(urls, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration")
	}
	return urls
}

// wellKnownURLs builds the path-inserted well-known URL (RFC 8414 / RFC 9728) and, for resources,
// the root fallback. For issuers only the path-inserted form applies.
func wellKnownURLs(suffix, raw string, issuer bool) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	path := strings.TrimRight(u.Path, "/")
	base := url.URL{Scheme: u.Scheme, Host: u.Host}
	inserted := base.String() + "/.well-known/" + suffix + path
	if path == "" || issuer {
		return []string{inserted}
	}
	return []string{inserted, base.String() + "/.well-known/" + suffix}
}

// canonicalResource normalizes an upstream URL into the RFC 8707 resource indicator:
// https, lowercase host, no fragment, no query, no trailing slash on an empty path.
func canonicalResource(raw string) (string, error) {
	u, err := parseHTTPSURL(raw)
	if err != nil {
		return "", err
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment, u.RawQuery = "", ""
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}

// sameURL compares URLs tolerating a single trailing slash and host case.
func sameURL(a, b string) bool {
	norm := func(s string) string {
		u, err := url.Parse(s)
		if err != nil {
			return s
		}
		u.Host = strings.ToLower(u.Host)
		return strings.TrimSuffix(u.String(), "/")
	}
	return norm(a) == norm(b)
}
