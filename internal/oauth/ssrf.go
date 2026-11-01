package oauth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	cimdMaxBytes      = 64 << 10
	cimdTimeout       = 5 * time.Second
	cimdMaxRedirects  = 3
	cimdMinCacheTTL   = time.Minute
	cimdDefaultTTL    = time.Hour
	cimdMaxCacheTTL   = 24 * time.Hour
	cimdMaxHeaderSize = 16 << 10
)

var (
	errBlockedAddress = errors.New("destination address is not public")
	errNotHTTPS       = errors.New("only https URLs are allowed")
	errTooLarge       = errors.New("metadata document too large")
)

// blockedPrefixes are non-public ranges a metadata fetch must never reach (loopback,
// RFC 1918, CGNAT, link-local, documentation, benchmarking, multicast, reserved, and IPv6
// translation prefixes that can embed private IPv4 addresses).
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

// cimdFetcher downloads Client ID Metadata Documents through an SSRF guard. The guard runs
// in the dialer's Control hook, i.e. on the resolved IP of every connection, including the
// ones opened for redirects, which also defeats DNS rebinding between check and use.
type cimdFetcher struct {
	client *http.Client
}

// newCIMDFetcher is the only constructor production code uses: public addresses only and
// the system trust store. Tests build a fetcher with a different policy in a _test.go file.
func newCIMDFetcher() *cimdFetcher { return newGuardedFetcher(isPublicAddr, nil) }

func newGuardedFetcher(allowAddr func(netip.Addr) bool, tlsConfig *tls.Config) *cimdFetcher {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	dialer := &net.Dialer{
		Timeout: cimdTimeout,
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
		TLSHandshakeTimeout:    cimdTimeout,
		ResponseHeaderTimeout:  cimdTimeout,
		MaxResponseHeaderBytes: cimdMaxHeaderSize,
		MaxIdleConns:           16,
		IdleConnTimeout:        30 * time.Second,
		ForceAttemptHTTP2:      true,
	}
	return &cimdFetcher{client: &http.Client{
		Transport: transport,
		Timeout:   cimdTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= cimdMaxRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errNotHTTPS
			}
			return nil
		},
	}}
}

// fetch returns the document body and how long it may be cached.
func (f *cimdFetcher) fetch(ctx context.Context, rawURL string) ([]byte, time.Duration, error) {
	if err := validateCIMDURL(rawURL); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch client metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("fetch client metadata: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read client metadata: %w", err)
	}
	if len(body) > cimdMaxBytes {
		return nil, 0, errTooLarge
	}
	return body, cacheTTL(resp.Header.Get("Cache-Control")), nil
}

// validateCIMDURL applies the CIMD client_id URL rules: https, a host, a non-root path,
// no userinfo, no fragment, no dot segments.
func validateCIMDURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" {
		return errNotHTTPS
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") {
		return errors.New("client_id URL must have a host and no userinfo or fragment")
	}
	if u.Path == "" || u.Path == "/" {
		return errors.New("client_id URL must have a path")
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if seg == "." || seg == ".." {
			return errors.New("client_id URL must not contain dot segments")
		}
	}
	return nil
}

func isCIMDClientID(clientID string) bool { return strings.HasPrefix(clientID, "https://") }

// cacheTTL honors Cache-Control max-age within [cimdMinCacheTTL, cimdMaxCacheTTL].
func cacheTTL(header string) time.Duration {
	ttl := cimdDefaultTTL
	for _, directive := range strings.Split(header, ",") {
		directive = strings.ToLower(strings.TrimSpace(directive))
		if directive == "no-store" || directive == "no-cache" {
			return cimdMinCacheTTL
		}
		if v, ok := strings.CutPrefix(directive, "max-age="); ok {
			if secs, err := strconv.Atoi(v); err == nil {
				ttl = time.Duration(secs) * time.Second
			}
		}
	}
	return min(max(ttl, cimdMinCacheTTL), cimdMaxCacheTTL)
}
