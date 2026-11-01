package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestIsPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "127.9.9.9": false, "10.1.2.3": false, "172.16.0.1": false, "172.31.255.255": false,
		"192.168.1.1": false, "169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false,
		"192.0.2.10": false, "198.18.0.1": false, "224.0.0.1": false, "255.255.255.255": false, "240.0.0.1": false,
		"::1": false, "::": false, "fe80::1": false, "fc00::1": false, "fd12:3456::1": false, "ff02::1": false,
		"::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false, "64:ff9b::a00:1": false, "2002:a00:1::1": false,
		"2001:db8::1": false, "::ffff:8.8.8.8": true,
	}
	for raw, want := range cases {
		if got := isPublicAddr(netip.MustParseAddr(raw)); got != want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestValidateCIMDURL(t *testing.T) {
	cases := map[string]bool{
		"https://app.example.com/client.json":    true,
		"https://app.example.com/oauth/meta?v=1": true,
		"http://app.example.com/client.json":     false,
		"https://app.example.com":                false,
		"https://app.example.com/":               false,
		"https://user:pw@app.example.com/c.json": false,
		"https://app.example.com/c.json#frag":    false,
		"https://app.example.com/a/../c.json":    false,
		"ftp://app.example.com/c.json":           false,
	}
	for raw, ok := range cases {
		if err := validateCIMDURL(raw); (err == nil) != ok {
			t.Errorf("validateCIMDURL(%s) err=%v, want ok=%v", raw, err, ok)
		}
	}
}

func TestCIMDFetchGuard(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	loopback := e.srv.clients.fetcher
	production := newCIMDFetcher()
	e.docs.Store("/big.json", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"client_id":"` + strings.Repeat("a", cimdMaxBytes) + `"}`))
	}))
	e.docs.Store("/to-private", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://10.0.0.1/client.json", http.StatusFound)
	}))
	e.docs.Store("/to-http", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/client.json", http.StatusFound)
	}))
	e.docs.Store("/to-ok", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/client.json", http.StatusFound)
	}))

	cases := []struct {
		name    string
		fetcher *cimdFetcher
		url     string
		wantErr error
	}{
		{"production guard blocks loopback test server", production, e.clientID, errBlockedAddress},
		{"production guard blocks localhost after DNS", production, "https://localhost:1/client.json", errBlockedAddress},
		{"production guard blocks metadata IP", production, "https://169.254.169.254/latest", errBlockedAddress},
		{"production guard blocks private literal", production, "https://10.0.0.1/client.json", errBlockedAddress},
		{"production guard blocks mapped loopback", production, "https://[::ffff:127.0.0.1]/client.json", errBlockedAddress},
		{"non-https rejected", loopback, strings.Replace(e.clientID, "https", "http", 1), errNotHTTPS},
		{"redirect to private blocked", loopback, e.cimd.URL + "/to-private", errBlockedAddress},
		{"redirect to http blocked", loopback, e.cimd.URL + "/to-http", errNotHTTPS},
		{"oversized document", loopback, e.cimd.URL + "/big.json", errTooLarge},
		{"redirect to allowed host follows", loopback, e.cimd.URL + "/to-ok", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, _, err := tc.fetcher.fetch(ctx, tc.url)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCIMDDocumentValidation(t *testing.T) {
	e := newTestEnv(t)
	mismatch := e.cimd.URL + "/mismatch.json"
	e.docs.Store("/mismatch.json", ClientMetadata{ClientID: e.clientID, ClientName: "x", RedirectURIs: []string{e.redirect}})
	secret := e.cimd.URL + "/secret.json"
	e.docs.Store("/secret.json", ClientMetadata{ClientID: secret, ClientName: "x", RedirectURIs: []string{e.redirect},
		TokenEndpointAuthMethod: "client_secret_basic"})
	noName := e.cimd.URL + "/noname.json"
	e.docs.Store("/noname.json", ClientMetadata{ClientID: noName, RedirectURIs: []string{e.redirect}})
	badRedirect := e.cimd.URL + "/redirect.json"
	e.docs.Store("/redirect.json", ClientMetadata{ClientID: badRedirect, ClientName: "x",
		RedirectURIs: []string{"javascript:alert(1)"}})
	for _, id := range []string{mismatch, secret, noName, badRedirect} {
		if _, err := e.srv.clients.resolve(context.Background(), id, "test"); err == nil {
			t.Errorf("%s: expected rejection", id)
		}
	}
	c, err := e.srv.clients.resolve(context.Background(), e.clientID, "test")
	if err != nil || c.Kind != kindCIMD || c.Metadata.ApplicationType != appTypeNative {
		t.Fatalf("valid document: %+v %v", c, err)
	}
	var fetchedAt *time.Time
	e.must(e.pool.QueryRow(context.Background(), `SELECT fetched_at FROM oauth_clients WHERE client_id = $1`, e.clientID).Scan(&fetchedAt))
	if fetchedAt == nil {
		t.Fatal("CIMD client must be persisted with fetched_at")
	}
}

func TestCIMDCacheExpiry(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	if _, err := e.srv.clients.resolve(ctx, e.clientID, "test"); err != nil {
		t.Fatal(err)
	}
	e.docs.Store("/client.json", ClientMetadata{ClientID: e.clientID, ClientName: "Renamed", RedirectURIs: []string{e.redirect}})
	c, _ := e.srv.clients.resolve(ctx, e.clientID, "test")
	if c.Metadata.ClientName != "Claude Code (teste)" {
		t.Fatal("cached document must be served before expiry")
	}
	e.clock.Advance(cimdDefaultTTL + time.Second)
	c, err := e.srv.clients.resolve(ctx, e.clientID, "test")
	if err != nil || c.Metadata.ClientName != "Renamed" {
		t.Fatalf("document must be refetched after expiry: %+v %v", c, err)
	}
}

func TestCacheTTL(t *testing.T) {
	cases := map[string]time.Duration{
		"":                                     cimdDefaultTTL,
		"max-age=600":                          10 * time.Minute,
		"max-age=1":                            cimdMinCacheTTL,
		"max-age=99999999":                     cimdMaxCacheTTL,
		"no-store":                             cimdMinCacheTTL,
		"public, max-age=120, must-revalidate": 2 * time.Minute,
	}
	for header, want := range cases {
		if got := cacheTTL(header); got != want {
			t.Errorf("cacheTTL(%q) = %v, want %v", header, got, want)
		}
	}
}
