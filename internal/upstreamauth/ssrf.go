package upstreamauth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	fetchTimeout       = 10 * time.Second
	fetchMaxRedirects  = 3
	fetchMaxHeaderSize = 16 << 10
	metadataMaxBytes   = 256 << 10
	tokenMaxBytes      = 64 << 10
)

var (
	errBlockedAddress = errors.New("destination address is not public")
	errNotHTTPS       = errors.New("only https URLs are allowed")
	errTooLarge       = errors.New("response body too large")
	errBadContentType = errors.New("unexpected response content type")
)

// statusError carries a non-success HTTP status without exposing the body.
type statusError struct{ Code int }

func (e *statusError) Error() string { return fmt.Sprintf("unexpected HTTP status %d", e.Code) }

// blockedPrefixes are non-public ranges no upstream fetch may reach (loopback, RFC 1918, CGNAT,
// link-local incl. cloud metadata, documentation, multicast, reserved, and IPv6 translation
// prefixes that can embed private IPv4 addresses).
var blockedPrefixes = mustParsePrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
	"2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
)

func mustParsePrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// isPublicAddr reports whether a is a globally routable unicast address.
func isPublicAddr(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if !a.IsValid() || !a.IsGlobalUnicast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// fetcher performs every outbound request of the package (probe, discovery, registration, token).
// The address guard runs in the dialer's Control hook on the resolved IP of every connection,
// including those opened for redirects, so it also defeats DNS rebinding.
type fetcher struct {
	client *http.Client
}

// newFetcher is the only constructor production code uses: public addresses, system trust store.
// Tests build a fetcher with another policy through newGuardedFetcher in a _test.go file.
func newFetcher() *fetcher { return newGuardedFetcher(isPublicAddr, nil, fetchTimeout) }

func newGuardedFetcher(allowAddr func(netip.Addr) bool, tlsConfig *tls.Config, timeout time.Duration) *fetcher {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !allowAddr(ap.Addr()) {
				return errBlockedAddress
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                  nil, // a proxy would hide the real destination from the guard
		DialContext:            dialer.DialContext,
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		MaxResponseHeaderBytes: fetchMaxHeaderSize,
		MaxIdleConns:           16,
		IdleConnTimeout:        30 * time.Second,
		ForceAttemptHTTP2:      true,
	}
	return &fetcher{client: &http.Client{
		Transport: transport,
		Timeout:   timeout, // covers the body read too
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.Method != http.MethodGet {
				return http.ErrUseLastResponse // never replay credentials or codes to a redirect target
			}
			if len(via) >= fetchMaxRedirects {
				return errors.New("too many redirects")
			}
			return checkHTTPS(req.URL)
		},
	}}
}

func checkHTTPS(u *url.URL) error {
	if u.Scheme != "https" {
		return errNotHTTPS
	}
	if u.Hostname() == "" || u.User != nil {
		return errors.New("URL must have a host and no userinfo")
	}
	return nil
}

func parseHTTPSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("malformed URL")
	}
	if err := checkHTTPS(u); err != nil {
		return nil, err
	}
	return u, nil
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// do sends req and reads at most maxBytes of body; a larger body is an error, never truncated.
func (f *fetcher) do(req *http.Request, maxBytes int64) (*response, error) {
	if err := checkHTTPS(req.URL); err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, scrubTransportError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, scrubTransportError(err)
	}
	if int64(len(body)) > maxBytes {
		return nil, errTooLarge
	}
	return &response{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// getJSON GETs rawURL and returns the body of a 200 application/json response.
func (f *fetcher) getJSON(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := parseHTTPSURL(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.do(req, maxBytes)
	if err != nil {
		return nil, err
	}
	if resp.status != http.StatusOK {
		return nil, &statusError{Code: resp.status}
	}
	if err := requireJSON(resp.header); err != nil {
		return nil, err
	}
	return resp.body, nil
}

// post sends a POST with the given content type and returns the response whatever its status
// (OAuth errors arrive as 400 with a JSON body that callers interpret).
func (f *fetcher) post(ctx context.Context, rawURL, contentType string, body io.Reader, setup func(*http.Request)) (*response, error) {
	u, err := parseHTTPSURL(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if setup != nil {
		setup(req)
	}
	return f.do(req, tokenMaxBytes)
}

// probe sends an unauthenticated GET and returns the WWW-Authenticate headers of a 401.
func (f *fetcher) probe(ctx context.Context, rawURL string) []string {
	u, err := parseHTTPSURL(rawURL)
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil
	}
	resp, err := f.do(req, metadataMaxBytes)
	if err != nil || resp.status != http.StatusUnauthorized {
		return nil
	}
	return resp.header.Values("WWW-Authenticate")
}

func requireJSON(h http.Header) error {
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || (mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
		return errBadContentType
	}
	return nil
}

// scrubTransportError reduces a transport error to a category: net/http errors embed the URL,
// and nothing coming from the network may end up in logs or user-facing errors verbatim.
func scrubTransportError(err error) error {
	switch {
	case errors.Is(err, errBlockedAddress):
		return errBlockedAddress
	case errors.Is(err, errNotHTTPS):
		return errNotHTTPS
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return errors.New("network request failed")
	}
}
