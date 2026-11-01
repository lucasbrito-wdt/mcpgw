package upstreamauth

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testFetcher is the test-only hook: loopback is allowed and the httptest certificate is
// trusted. It lives in a _test.go file so no production path can build it.
func testFetcher(srv *httptest.Server, timeout time.Duration) *fetcher {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return newGuardedFetcher(func(a netip.Addr) bool { return a.IsLoopback() },
		&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, timeout)
}

type codeInfo struct {
	challenge, redirectURI, resource, clientID string
}

// fakeAS is an upstream MCP server (protected resource) and its authorization server in one
// TLS server. It enforces PKCE, redirect_uri, resource, single-use codes and refresh rotation.
type fakeAS struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	codes    map[string]codeInfo
	refresh  map[string]bool
	clients  map[string]string // client id -> secret ("" = public)
	seq      int
	tokenLog []url.Values

	registerCalls atomic.Int32
	refreshCalls  atomic.Int32

	// knobs
	issSupported   bool
	cimd           bool
	noRegistration bool
	omitS256       bool
	noChallenge    bool
	oidcOnly       bool
	prmResource    string
	expiresIn      int64
	rotate         bool
	refreshDelay   time.Duration
	refreshStatus  int
	redirectURI    string
}

func newFakeAS(t *testing.T) *fakeAS {
	f := &fakeAS{t: t, codes: map[string]codeInfo{}, refresh: map[string]bool{}, clients: map[string]string{},
		expiresIn: 3600, rotate: true}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mcp", f.handleMCP)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", f.handlePRM)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", f.handleASMeta)
	mux.HandleFunc("GET /.well-known/openid-configuration", f.handleASMeta)
	mux.HandleFunc("POST /register", f.handleRegister)
	mux.HandleFunc("POST /token", f.handleToken)
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAS) mcpURL() string { return f.srv.URL + "/mcp" }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeAS) handleMCP(w http.ResponseWriter, _ *http.Request) {
	if !f.noChallenge {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(
			`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp", scope="mcp.read"`, f.srv.URL))
	}
	w.WriteHeader(http.StatusUnauthorized)
}

func (f *fakeAS) handlePRM(w http.ResponseWriter, _ *http.Request) {
	resource := f.mcpURL()
	if f.prmResource != "" {
		resource = f.prmResource
	}
	writeJSON(w, 200, map[string]any{"resource": resource, "authorization_servers": []string{f.srv.URL},
		"scopes_supported": []string{"mcp.read", "mcp.write"}})
}

func (f *fakeAS) handleASMeta(w http.ResponseWriter, r *http.Request) {
	if f.oidcOnly && strings.Contains(r.URL.Path, "oauth-authorization-server") {
		http.NotFound(w, r)
		return
	}
	methods := []string{"S256"}
	if f.omitS256 {
		methods = []string{"plain"}
	}
	meta := map[string]any{
		"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
		"response_types_supported": []string{"code"}, "code_challenge_methods_supported": methods,
		"client_id_metadata_document_supported":          f.cimd,
		"authorization_response_iss_parameter_supported": f.issSupported,
	}
	if !f.noRegistration {
		meta["registration_endpoint"] = f.srv.URL + "/register"
	}
	writeJSON(w, 200, meta)
}

func (f *fakeAS) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if len(req.RedirectURIs) != 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid_redirect_uri"})
		return
	}
	n := f.registerCalls.Add(1)
	id := fmt.Sprintf("dcr-%d", n)
	f.mu.Lock()
	f.clients[id] = ""
	f.mu.Unlock()
	writeJSON(w, 201, map[string]string{"client_id": id})
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize plays the browser + AS: it validates the authorization request and returns the
// query the AS would append to the callback.
func (f *fakeAS) authorize(authURL string) url.Values {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, f.srv.URL+"/authorize") {
		f.t.Fatalf("unexpected authorization URL %q", authURL)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" {
		f.t.Fatalf("bad authorization request: %v", q)
	}
	if q.Get("resource") != f.mcpURL() {
		f.t.Fatalf("resource = %q, want %q", q.Get("resource"), f.mcpURL())
	}
	f.mu.Lock()
	f.seq++
	code := fmt.Sprintf("code-%d", f.seq)
	f.codes[code] = codeInfo{challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri"),
		resource: q.Get("resource"), clientID: q.Get("client_id")}
	f.mu.Unlock()
	out := url.Values{"code": {code}, "state": {q.Get("state")}}
	if f.issSupported {
		out.Set("iss", f.srv.URL)
	}
	return out
}

func (f *fakeAS) clientAuth(r *http.Request) (string, bool) {
	id, secret, hasBasic := r.BasicAuth()
	if hasBasic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id = r.PostForm.Get("client_id")
	}
	f.mu.Lock()
	want, known := f.clients[id]
	f.mu.Unlock()
	if !known && !strings.HasPrefix(id, "https://gateway.test/") {
		return "", false
	}
	return id, want == "" || (hasBasic && secret == want)
}

func (f *fakeAS) handleToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	f.tokenLog = append(f.tokenLog, r.PostForm)
	f.mu.Unlock()
	clientID, ok := f.clientAuth(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		f.grantCode(w, r, clientID)
	case "refresh_token":
		f.grantRefresh(w, r)
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (f *fakeAS) issue(rotate bool) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	resp := map[string]any{"access_token": fmt.Sprintf("at-%d", f.seq), "token_type": "Bearer", "expires_in": f.expiresIn}
	if rotate {
		rt := fmt.Sprintf("rt-%d", f.seq)
		f.refresh[rt] = true
		resp["refresh_token"] = rt
	}
	return resp
}

func (f *fakeAS) grantCode(w http.ResponseWriter, r *http.Request, clientID string) {
	f.mu.Lock()
	info, found := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code")) // single use
	f.mu.Unlock()
	switch {
	case !found, info.clientID != clientID, info.redirectURI != r.PostForm.Get("redirect_uri"),
		info.challenge != s256(r.PostForm.Get("code_verifier")), r.PostForm.Get("resource") != f.mcpURL():
		writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "secret detail rt-leak"})
		return
	}
	writeJSON(w, 200, f.issue(true))
}

func (f *fakeAS) grantRefresh(w http.ResponseWriter, r *http.Request) {
	f.refreshCalls.Add(1)
	time.Sleep(f.refreshDelay)
	if f.refreshStatus != 0 {
		writeJSON(w, f.refreshStatus, map[string]string{"error": "server_error"})
		return
	}
	rt := r.PostForm.Get("refresh_token")
	f.mu.Lock()
	valid := f.refresh[rt]
	if valid && f.rotate {
		delete(f.refresh, rt) // reuse of a rotated token must fail
	}
	f.mu.Unlock()
	if !valid || r.PostForm.Get("resource") != f.mcpURL() {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	writeJSON(w, 200, f.issue(f.rotate))
}
