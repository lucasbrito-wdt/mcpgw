package upstreamauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestIsPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "::1": false, "10.0.0.1": false, "172.16.5.4": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false,
		"fe80::1": false, "fc00::1": false, "::ffff:10.0.0.1": false, "::ffff:127.0.0.1": false,
		"64:ff9b::a00:1": false, "2002:a00:1::": false, "198.18.0.1": false,
	} {
		if got := isPublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestProductionFetcherBlocksBadTargets(t *testing.T) {
	live := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer live.Close()
	f := newFetcher()
	for _, tt := range []struct {
		name, url string
		want      error
	}{
		{"plain http", "http://example.com/.well-known/x", errNotHTTPS},
		{"ftp", "ftp://example.com/x", errNotHTTPS},
		{"no scheme", "example.com/x", errNotHTTPS},
		{"userinfo", "https://user:pw@example.com/x", nil},
		{"live loopback server", live.URL + "/x", errBlockedAddress},
		{"localhost name", "https://localhost/x", errBlockedAddress},
		{"ipv6 loopback", "https://[::1]/x", errBlockedAddress},
		{"rfc1918", "https://10.0.0.1/x", errBlockedAddress},
		{"rfc1918 192", "https://192.168.1.1/x", errBlockedAddress},
		{"cloud metadata", "https://169.254.169.254/latest/meta-data", errBlockedAddress},
		{"ipv4 mapped", "https://[::ffff:10.0.0.1]/x", errBlockedAddress},
		{"cgnat", "https://100.64.0.1/x", errBlockedAddress},
		{"unspecified", "https://0.0.0.0/x", errBlockedAddress},
		{"decimal ip", "https://2130706433/x", errBlockedAddress},
		{"hex ip", "https://0x7f.1/x", errBlockedAddress},
		{"short ip", "https://127.1/x", errBlockedAddress},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := f.getJSON(ctx, tt.url, metadataMaxBytes)
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFetcherRedirectsAndLimits(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://169.254.169.254/latest", http.StatusFound)
	})
	mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/x", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"a":1}`))
	})
	mux.HandleFunc("/hop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, io.LimitReader(zeroReader{}, metadataMaxBytes+10))
	})
	mux.HandleFunc("/text", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(2 * time.Second)
	})
	mux.HandleFunc("POST /post-redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok", http.StatusTemporaryRedirect)
	})
	srv = httptest.NewTLSServer(mux)
	defer srv.Close()
	f := testFetcher(srv, 600*time.Millisecond)
	ctx := context.Background()

	for _, tt := range []struct {
		path string
		want error // nil = any error
	}{
		{"/to-metadata", errBlockedAddress},
		{"/to-http", errNotHTTPS},
		{"/loop", nil},
		{"/big", errTooLarge},
		{"/text", errBadContentType},
	} {
		if _, err := f.getJSON(ctx, srv.URL+tt.path, metadataMaxBytes); err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
			t.Errorf("%s: err = %v, want %v", tt.path, err, tt.want)
		}
	}
	if body, err := f.getJSON(ctx, srv.URL+"/hop", metadataMaxBytes); err != nil || string(body) != `{"a":1}`+"\n" && string(body) != `{"a":1}` {
		t.Errorf("same-host redirect: %q, %v", body, err)
	}

	start := time.Now()
	if _, err := f.getJSON(ctx, srv.URL+"/slow", metadataMaxBytes); err == nil || time.Since(start) > 1500*time.Millisecond {
		t.Errorf("slow body: err = %v after %v", err, time.Since(start))
	}

	resp, err := f.post(ctx, srv.URL+"/post-redirect", "application/x-www-form-urlencoded", strings.NewReader("code=secret"), nil)
	if err != nil || resp.status != http.StatusTemporaryRedirect {
		t.Errorf("POST must not follow redirects: %+v, %v", resp, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestScrubTransportError(t *testing.T) {
	err := scrubTransportError(errors.New(`Get "https://x/?code=SECRET": boom`))
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatal("transport error not scrubbed")
	}
}

func TestResourceCanonicalization(t *testing.T) {
	for in, want := range map[string]string{
		"https://Example.com/mcp":      "https://example.com/mcp",
		"https://example.com/":         "https://example.com",
		"https://example.com/mcp#frag": "https://example.com/mcp",
		"https://example.com/mcp?x=1":  "https://example.com/mcp",
	} {
		if got, err := canonicalResource(in); err != nil || got != want {
			t.Errorf("canonicalResource(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := canonicalResource("http://example.com/mcp"); err == nil {
		t.Error("http resource accepted")
	}
}
