package upstream

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const recordDetectionTimeout = 5 * time.Second

type detectionEntry struct {
	d     Detection
	stale bool // the session was lost: probe again instead of trusting the stored detection
}

// dialSession opens a session for key: with the remembered transport and era when known (no
// probing), else by detection. A remembered choice that no longer works is re-detected once.
func (c *Client) dialSession(ctx context.Context, key sessionKey, cred Credential) (dialed, error) {
	ep, err := c.resolver.Resolve(ctx, key.endpoint)
	if err != nil {
		return dialed{}, scrubError(err, false, credentialValues(cred))
	}
	if ep.Transport == TransportStdio && ep.Stdio == nil {
		return dialed{}, errors.New("stdio upstream without a process spec")
	}
	if known := c.detection(key.endpoint, ep.Detected); applies(ep.Transport, known.Transport) {
		d, _, err := c.dialTransport(ctx, ep, cred, known.Transport, known.ProtocolVersion)
		if err == nil || ctx.Err() != nil {
			return d, err
		}
	}
	return c.detect(ctx, key.endpoint, ep, cred)
}

// applies reports whether a remembered transport may be used for a configured one.
func applies(configured, known TransportKind) bool {
	if known == "" {
		return false
	}
	if configured == TransportAuto {
		return known == TransportStreamable || known == TransportSSE
	}
	return configured == known
}

// detect tries the candidate transports in order. The SDK itself tries the modern
// server/discover first and falls back to the legacy initialize handshake. Streamable HTTP
// falls back to legacy SSE only when the server rejected the request with a status that
// means "not this protocol" (never on auth or network errors).
func (c *Client) detect(ctx context.Context, endpoint string, ep Endpoint, cred Credential) (dialed, error) {
	var firstErr error
	for _, kind := range candidates(ep) {
		d, status, err := c.dialTransport(ctx, ep, cred, kind, "")
		if err == nil {
			c.remember(endpoint, ep.Detected, Detection{Transport: kind, ProtocolVersion: protocolVersion(d.cs)})
			return d, nil
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return dialed{}, &HTTPStatusError{Status: status}
		}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil || !wrongProtocolStatus(status) {
			break
		}
	}
	return dialed{}, firstErr
}

func candidates(ep Endpoint) []TransportKind {
	if ep.Transport != TransportAuto {
		return []TransportKind{ep.Transport}
	}
	if strings.HasSuffix(strings.TrimRight(ep.URL, "/"), "/sse") {
		return []TransportKind{TransportSSE, TransportStreamable}
	}
	return []TransportKind{TransportStreamable, TransportSSE}
}

func wrongProtocolStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed,
		http.StatusNotAcceptable, http.StatusUnsupportedMediaType:
		return true
	}
	return false
}

func protocolVersion(cs *mcp.ClientSession) string {
	if r := cs.InitializeResult(); r != nil {
		return r.ProtocolVersion
	}
	return ""
}

// dialTransport connects one transport. status is the last HTTP error status seen (0 if none).
func (c *Client) dialTransport(ctx context.Context, ep Endpoint, cred Credential, kind TransportKind, version string) (d dialed, status int, err error) {
	var transport mcp.Transport
	rejected := func() int { return 0 }
	switch kind {
	case TransportStdio:
		spec, err := withCredentialEnv(*ep.Stdio, cred)
		if err != nil {
			return dialed{}, 0, err
		}
		transport, d.sanitize, d.overflowed = c.stdioTransport(spec)
		d.secrets = stdioSecrets(spec)
	case TransportStreamable, TransportSSE:
		rt := c.httpGuard(ep.URL, cred, kind)
		transport, d.sanitize, d.overflowed = c.httpTransport(ep.URL, rt, kind), rt.sanitize, rt.exceeded.Load
		d.secrets = rt.secrets()
		rejected = func() int { return int(rt.rejected.Load()) }
	default:
		return dialed{}, 0, errors.New("unsupported upstream transport " + string(kind))
	}
	cs, err := c.mcp.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		return dialed{}, rejected(), d.sanitize(err)
	}
	d.cs = cs
	return d, 0, nil
}

func (c *Client) stdioTransport(spec StdioSpec) (mcp.Transport, func(error) error, func() bool) {
	exceeded := &atomic.Bool{}
	secrets := stdioSecrets(spec)
	t := &stdioTransport{
		spec: spec, dockerPath: c.dockerPath, grace: c.terminateGrace,
		lineLimit: int(c.wireLimit()), onTooLarge: func() { exceeded.Store(true) },
	}
	return t, func(err error) error { return scrubError(err, exceeded.Load(), secrets) }, exceeded.Load
}

// withCredentialEnv returns spec with an EnvCredential applied to a copy of its fixed environment:
// first the connection's own values (each overrides the fixed default of the same name), then the
// token. A stdio server takes no other kind of credential, and the token never overrides a fixed
// variable. The merged spec is validated again when the process starts.
func withCredentialEnv(spec StdioSpec, cred Credential) (StdioSpec, error) {
	if cred == nil {
		return spec, nil
	}
	ec, ok := cred.(EnvCredential)
	if !ok {
		return spec, errors.New("stdio upstream accepts only an environment credential")
	}
	if _, fixed := spec.Env[ec.Name]; fixed {
		return spec, errors.New("stdio: credential variable collides with the fixed environment")
	}
	if _, set := ec.Env[ec.Name]; set {
		return spec, errors.New("stdio: connection environment sets the credential variable")
	}
	env := make(map[string]string, len(spec.Env)+len(ec.Env)+1)
	maps.Copy(env, spec.Env)
	maps.Copy(env, ec.Env)
	env[ec.Name] = ec.Value
	spec.Env = env
	return spec, nil
}

func stdioSecrets(spec StdioSpec) []string {
	secrets := make([]string, 0, len(spec.Env))
	for _, v := range spec.Env {
		secrets = append(secrets, v)
	}
	return secrets
}

// httpGuard builds the per-session request guard. The legacy SSE GET stream lives as long as
// the session, so only its events are bounded, not the whole body. An unparsable server URL
// leaves the origin empty, which makes the guard refuse every request (fail closed).
func (c *Client) httpGuard(serverURL string, cred Credential, kind TransportKind) *guardedTransport {
	rt := &guardedTransport{base: c.transport, cred: cred, lineLimit: int(c.wireLimit()), wireLimit: c.wireLimit()}
	if u, err := url.Parse(serverURL); err == nil && u.Host != "" {
		rt.origin = originOf(u)
	}
	if kind == TransportStreamable {
		rt.bodyLimit = c.wireLimit()
	}
	return rt
}

func (c *Client) httpTransport(endpoint string, rt *guardedTransport, kind TransportKind) mcp.Transport {
	httpClient := &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	maxEvent := int(c.wireLimit())
	if kind == TransportSSE {
		return &mcp.SSEClientTransport{Endpoint: endpoint, HTTPClient: httpClient, MaxEventSize: maxEvent}
	}
	return &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
		MaxEventSize:         maxEvent,
	}
}

// detection returns the in-memory detection for endpoint, falling back to the stored one.
func (c *Client) detection(endpoint string, stored Detection) Detection {
	c.detMu.Lock()
	defer c.detMu.Unlock()
	e, ok := c.detections[endpoint]
	switch {
	case !ok:
		return stored
	case e.stale:
		return Detection{}
	default:
		return e.d
	}
}

// forgetDetection makes the next session of endpoint probe again (called on session loss).
func (c *Client) forgetDetection(endpoint string) {
	c.detMu.Lock()
	defer c.detMu.Unlock()
	c.detections[endpoint] = detectionEntry{stale: true}
}

// remember keeps a detection in memory and persists it through the resolver when it changed.
func (c *Client) remember(endpoint string, stored, d Detection) {
	c.detMu.Lock()
	c.detections[endpoint] = detectionEntry{d: d}
	c.detMu.Unlock()
	if d == stored {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), recordDetectionTimeout)
	defer cancel()
	if err := c.resolver.RecordDetection(ctx, endpoint, d); err != nil {
		c.log.Warn("upstream: record detection", "transport", d.Transport, "error", RedactURLs(err))
	}
}
