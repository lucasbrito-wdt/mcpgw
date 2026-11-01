package upstream

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
)

// guardedTransport injects the credential into every request, bounds what is read from each
// response and rewrites inbound JSON-RPC messages around SDK interop gaps (see sanitizeMessage).
// It also knows the secret header values so that error strings can be scrubbed. One instance
// belongs to one session (and so to one credential).
type guardedTransport struct {
	base *http.Transport
	cred Credential
	// origin is the configured server origin (scheme://host:port). Requests to any other origin
	// are refused before the credential is applied: the legacy SSE `endpoint` event is chosen by
	// the upstream and may name an arbitrary host.
	origin string
	// bodyLimit bounds the bytes of one response body (0: unbounded, for the long-lived legacy
	// SSE stream). lineLimit bounds one event-stream line.
	bodyLimit int64
	lineLimit int
	// wireLimit bounds a buffered application/json body even when bodyLimit is 0.
	wireLimit int64

	exceeded atomic.Bool
	// rejected is the last HTTP error status seen (transport auto-detection uses it).
	rejected atomic.Int32
}

func (g *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// An unset origin never equals originOf (always non-empty): fail closed.
	if g.origin == "" || originOf(req.URL) != g.origin {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, ErrCrossOrigin
	}
	req = req.Clone(req.Context())
	if g.cred != nil {
		g.cred.Apply(req)
	}
	resp, err := g.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		g.rejected.Store(int32(resp.StatusCode))
	}
	var body io.ReadCloser = resp.Body
	if g.bodyLimit > 0 {
		body = &limitedBody{rc: body, remaining: g.bodyLimit, onExceeded: g.markExceeded}
	}
	switch mediaType(resp.Header.Get("Content-Type")) {
	case "application/json":
		body = &jsonBody{rc: body, limit: g.wireLimit, onExceeded: g.markExceeded}
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
	case "text/event-stream":
		body = newLineFilter(body, g.lineLimit, sanitizeEventLine, g.markExceeded)
	}
	resp.Body = body
	return resp, nil
}

// originOf returns scheme://host:port of u in canonical form (lower case, default port made
// explicit), so that equivalent spellings of one origin compare equal.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func (g *guardedTransport) markExceeded() { g.exceeded.Store(true) }

func mediaType(contentType string) string {
	mt, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(mt))
}

type limitedBody struct {
	rc         io.ReadCloser
	remaining  int64
	onExceeded func()
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		b.onExceeded()
		return 0, ErrResponseTooLarge
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.rc.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

// secrets returns every header value the credential sets (and token parts), for scrubbing.
func (g *guardedTransport) secrets() []string { return credentialValues(g.cred) }

func (g *guardedTransport) sanitize(err error) error {
	return scrubError(err, g.exceeded.Load(), g.secrets())
}

// credentialValues returns the header values a credential sets, plus the token part of
// "Scheme token" values.
func credentialValues(cred Credential) []string {
	if cred == nil {
		return nil
	}
	if ec, ok := cred.(EnvCredential); ok {
		return append([]string{ec.Value}, slices.Collect(maps.Values(ec.Env))...)
	}
	probe, _ := http.NewRequest(http.MethodGet, "http://probe.invalid/", nil)
	cred.Apply(probe)
	var out []string
	for _, vs := range probe.Header {
		for _, v := range vs {
			out = append(out, v)
			if _, tok, ok := strings.Cut(v, " "); ok {
				out = append(out, tok)
			}
		}
	}
	return out
}

// scrubError maps errors to ones that never contain credential material or URL secrets.
func scrubError(err error, tooLarge bool, secrets []string) error {
	if err == nil {
		return nil
	}
	if tooLarge || errors.Is(err, ErrResponseTooLarge) {
		return ErrResponseTooLarge
	}
	var toolErr *ToolError
	var statusErr *HTTPStatusError
	if errors.As(err, &toolErr) || errors.As(err, &statusErr) || errors.Is(err, ErrInputRequired) {
		return err
	}
	msg := RedactURLs(err)
	for _, s := range secrets {
		if len(s) >= 4 {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return &sanitizedError{msg: msg, cause: stripSecrets(err)}
}

// sanitizedError keeps context/deadline matching via Is without exposing the wrapped chain text.
type sanitizedError struct {
	msg   string
	cause error
}

func (e *sanitizedError) Error() string { return e.msg }

func (e *sanitizedError) Is(target error) bool {
	return e.cause != nil && errors.Is(e.cause, target)
}

// stripSecrets keeps only well-known sentinel errors from the chain.
func stripSecrets(err error) error {
	for _, s := range []error{context.Canceled, context.DeadlineExceeded, ErrPoolExhausted, ErrUnknownEndpoint, ErrClosed} {
		if errors.Is(err, s) {
			return s
		}
	}
	return nil
}

const (
	redactedURLPart = "REDACTED"
	redactedURL     = "[redacted-url]"
)

// RedactURLs returns err.Error() with the URL of every *url.Error in the chain reduced to
// scheme://host[:port] (see redactURL). net/http errors embed the full request URL, and servers
// that authenticate with a path segment or ?api_key=... would otherwise leak it to whoever sees
// the message.
func RedactURLs(err error) string {
	msg := err.Error()
	var urlErrs []*url.Error
	for e := err; e != nil; {
		var ue *url.Error
		if !errors.As(e, &ue) {
			break
		}
		urlErrs = append(urlErrs, ue)
		e = ue.Err
	}
	// Longest URL first: a URL that is a prefix of another must not corrupt the longer match.
	slices.SortStableFunc(urlErrs, func(a, b *url.Error) int { return len(b.URL) - len(a.URL) })
	for _, ue := range urlErrs {
		red := redactURL(ue.URL)
		if red == ue.URL {
			continue
		}
		// url.Error quotes the URL with %q; replace that form first so the plain pass cannot
		// break the quoting.
		msg = strings.ReplaceAll(msg, strconv.Quote(ue.URL), strconv.Quote(red))
		msg = strings.ReplaceAll(msg, ue.URL, red)
	}
	return msg
}

// redactURL keeps only scheme://host[:port] (enough to tell servers apart): userinfo, path,
// fragment and the whole query are dropped, the query replaced by a REDACTED marker. Path
// segments and query keys can carry secrets as much as query values. Anything without a host
// (unparsable, relative, opaque) is replaced entirely. The result is a fixed point.
func redactURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return redactedURL
	}
	out := u.Scheme + "://" + u.Host
	if u.RawQuery != "" || u.ForceQuery {
		out += "?" + redactedURLPart
	}
	return out
}
