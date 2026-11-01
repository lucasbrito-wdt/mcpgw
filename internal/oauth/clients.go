package oauth

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	authMethodNone         = "none"
	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"
	appTypeNative          = "native"
	appTypeWeb             = "web"
	kindCIMD               = "cimd"
	kindDCR                = "dcr"

	maxRedirectURIs   = 20
	maxRedirectURILen = 2048
	maxClientNameLen  = 200
)

var (
	errUnknownClient    = errors.New("unknown client")
	errInvalidRedirect  = errors.New("invalid redirect_uri")
	forbiddenURISchemes = []string{"javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss"}
)

// ClientMetadata is the subset of RFC 7591 / CIMD client metadata the server uses.
type ClientMetadata struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	ApplicationType         string   `json:"application_type,omitempty"`
	SoftwareID              string   `json:"software_id,omitempty"`
	SoftwareVersion         string   `json:"software_version,omitempty"`
}

// Client is a registered (DCR) or metadata-document (CIMD) client. All clients are public.
type Client struct {
	ClientID string
	Kind     string
	Metadata ClientMetadata
}

// normalize validates metadata for a public client and fills defaults.
func (m *ClientMetadata) normalize() error {
	m.ClientName = strings.TrimSpace(m.ClientName)
	if utf8.RuneCountInString(m.ClientName) > maxClientNameLen {
		return errors.New("client_name too long")
	}
	if m.TokenEndpointAuthMethod == "" {
		m.TokenEndpointAuthMethod = authMethodNone
	}
	if m.TokenEndpointAuthMethod != authMethodNone {
		return errors.New(`only token_endpoint_auth_method "none" is supported`)
	}
	for _, g := range m.GrantTypes {
		if g != grantAuthorizationCode && g != grantRefreshToken {
			return fmt.Errorf("unsupported grant_type %q", g)
		}
	}
	if len(m.GrantTypes) > 0 && !slices.Contains(m.GrantTypes, grantAuthorizationCode) {
		return errors.New("grant_types must include authorization_code")
	}
	for _, rt := range m.ResponseTypes {
		if rt != "code" {
			return fmt.Errorf("unsupported response_type %q", rt)
		}
	}
	return m.normalizeRedirects()
}

func (m *ClientMetadata) normalizeRedirects() error {
	if len(m.RedirectURIs) == 0 || len(m.RedirectURIs) > maxRedirectURIs {
		return fmt.Errorf("%w: between 1 and %d redirect_uris are required", errInvalidRedirect, maxRedirectURIs)
	}
	switch m.ApplicationType {
	case "":
		m.ApplicationType = inferApplicationType(m.RedirectURIs)
	case appTypeNative, appTypeWeb:
	default:
		return fmt.Errorf("unsupported application_type %q", m.ApplicationType)
	}
	for _, raw := range m.RedirectURIs {
		if err := validateRedirectURI(raw, m.ApplicationType); err != nil {
			return fmt.Errorf("%w %q: %v", errInvalidRedirect, raw, err)
		}
	}
	return nil
}

// allowsRefresh is true unless the client explicitly limited its grant types.
func (m *ClientMetadata) allowsRefresh() bool {
	return len(m.GrantTypes) == 0 || slices.Contains(m.GrantTypes, grantRefreshToken)
}

func inferApplicationType(redirects []string) string {
	for _, raw := range redirects {
		if u, err := url.Parse(raw); err == nil && u.Scheme != "https" {
			return appTypeNative
		}
	}
	return appTypeWeb
}

// validateRedirectURI enforces the OIDC/RFC 8252 split: web clients use https on non-local
// hosts; native clients may also use http loopback or a private-use scheme.
func validateRedirectURI(raw, appType string) error {
	if len(raw) > maxRedirectURILen {
		return errors.New("too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || !u.IsAbs() {
		return errors.New("must be an absolute URI")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") || u.User != nil {
		return errors.New("must not contain a fragment or userinfo")
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme == "https":
		if u.Host == "" {
			return errors.New("https redirect needs a host")
		}
		if appType == appTypeWeb && isLoopbackHost(u.Hostname()) {
			return errors.New("web clients cannot redirect to localhost")
		}
	case scheme == "http":
		if !isLoopbackHost(u.Hostname()) {
			return errors.New("http is only allowed for loopback redirects")
		}
		if appType != appTypeNative {
			return errors.New("loopback redirects require application_type native")
		}
	case slices.Contains(forbiddenURISchemes, scheme):
		return fmt.Errorf("scheme %q is not allowed", scheme)
	default:
		if appType != appTypeNative {
			return errors.New("private-use schemes require application_type native")
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
}

// matchRedirect reports whether requested is registered. Exact string match, except that a
// registered http loopback redirect matches the same URI on any port (RFC 8252 section 7.3).
func (c *Client) matchRedirect(requested string) bool {
	for _, registered := range c.Metadata.RedirectURIs {
		if requested == registered || loopbackMatch(registered, requested) {
			return true
		}
	}
	return false
}

func loopbackMatch(registered, requested string) bool {
	reg, err1 := url.Parse(registered)
	req, err2 := url.Parse(requested)
	if err1 != nil || err2 != nil || reg.Scheme != "http" || req.Scheme != "http" {
		return false
	}
	if !isLoopbackHost(reg.Hostname()) || req.Hostname() != reg.Hostname() {
		return false
	}
	if req.User != nil || req.Fragment != "" || strings.Contains(requested, "#") {
		return false
	}
	if port := req.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return req.EscapedPath() == reg.EscapedPath() && req.RawQuery == reg.RawQuery
}

// displayName is the client name for the consent page, falling back to the client_id.
func (c *Client) displayName() string {
	if c.Metadata.ClientName != "" {
		return c.Metadata.ClientName
	}
	return c.ClientID
}

// idHost is the host that vouches for the client identity (CIMD only).
func (c *Client) idHost() string {
	if c.Kind != kindCIMD {
		return ""
	}
	u, err := url.Parse(c.ClientID)
	if err != nil {
		return ""
	}
	return u.Host
}
