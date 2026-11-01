package oauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

const (
	testIssuer   = "https://gw.example.test"
	testPassword = "correct horse battery staple"
	testEmail    = "lucas@example.test"
)

// aesSealer is a test Sealer (the real one lives in the vault package).
type aesSealer struct{ aead cipher.AEAD }

func newAESSealer(t *testing.T) *aesSealer {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &aesSealer{aead: aead}
}

func (s *aesSealer) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (s *aesSealer) Open(ciphertext, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(ciphertext) < n {
		return nil, errors.New("short ciphertext")
	}
	return s.aead.Open(nil, ciphertext[:n], ciphertext[n:], aad)
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newLoopbackFetcher is the SSRF test hook: it exists only in this _test.go file, so no
// production code path can build a fetcher that reaches loopback or trusts a test CA.
func newLoopbackFetcher(srv *httptest.Server) *cimdFetcher {
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return newGuardedFetcher(func(a netip.Addr) bool { return a.IsLoopback() },
		&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
}

var (
	userHashOnce sync.Once
	userHash     string
)

type testEnv struct {
	t         *testing.T
	pool      *pgxpool.Pool
	srv       *Server
	mux       *http.ServeMux
	clock     *testClock
	cimd      *httptest.Server
	clientID  string
	redirect  string
	userID    string
	serverIDs map[string]string // slug -> id
	docs      sync.Map          // path -> served document
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{t: t, pool: storetest.New(t), clock: &testClock{t: time.Now()}, serverIDs: map[string]string{}}
	e.cimd = httptest.NewTLSServer(http.HandlerFunc(e.serveDoc))
	t.Cleanup(e.cimd.Close)
	e.clientID = e.cimd.URL + "/client.json"
	e.redirect = "http://127.0.0.1/callback"
	e.docs.Store("/client.json", ClientMetadata{
		ClientID: e.clientID, ClientName: "Claude Code (teste)", RedirectURIs: []string{e.redirect},
		GrantTypes: []string{grantAuthorizationCode, grantRefreshToken}, TokenEndpointAuthMethod: authMethodNone,
	})
	srv, err := New(context.Background(), Options{PublicURL: testIssuer, Pool: e.pool, Sealer: newAESSealer(t), Now: e.clock.Now, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	srv.clients.fetcher = newLoopbackFetcher(e.cimd)
	e.srv = srv
	e.mux = http.NewServeMux()
	srv.Routes(e.mux)
	e.seed()
	return e
}

func (e *testEnv) serveDoc(w http.ResponseWriter, r *http.Request) {
	doc, ok := e.docs.Load(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if h, ok := doc.(http.HandlerFunc); ok {
		h(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

// seed creates the user, three upstream servers and ceilings on two of them.
func (e *testEnv) seed() {
	userHashOnce.Do(func() {
		var err error
		if userHash, err = HashPassword(testPassword); err != nil {
			panic(err)
		}
	})
	ctx := context.Background()
	e.must(e.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text`,
		testEmail, userHash).Scan(&e.userID))
	for _, slug := range []string{"github", "notion", "secret"} {
		var id string
		e.must(e.pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ($1, $2, 'none')
			RETURNING id::text`, slug, "https://"+slug+".example.test/mcp").Scan(&id))
		e.serverIDs[slug] = id
	}
	for slug, level := range map[string]string{"github": "write", "notion": "read"} {
		_, err := e.pool.Exec(ctx, `INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1, $2, $3)`,
			e.userID, e.serverIDs[slug], level)
		e.must(err)
	}
}

func (e *testEnv) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

type pkce struct{ verifier, challenge string }

func newPKCE() pkce {
	v, _ := randomToken(32)
	sum := sha256.Sum256([]byte(v))
	return pkce{verifier: v, challenge: base64.RawURLEncoding.EncodeToString(sum[:])}
}

func (e *testEnv) authorizeParams(p pkce) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {e.clientID}, "redirect_uri": {"http://127.0.0.1:53682/callback"},
		"code_challenge": {p.challenge}, "code_challenge_method": {"S256"}, "state": {"xyz-state"},
		"resource": {testIssuer + "/mcp"}, "scope": {ScopeMCP},
	}
}

func (e *testEnv) getAuthorize(q url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return e.do(r)
}

func (e *testEnv) postForm(path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return e.do(newFormRequest(path, form, cookies...))
}

// sessionCookie logs the test user in without paying for argon2id.
func (e *testEnv) sessionCookie() *http.Cookie {
	w := httptest.NewRecorder()
	e.must(e.srv.startSession(w, e.userID))
	return cookieNamed(e.t, w, sessionCookie)
}

func cookieNamed(t *testing.T, w *httptest.ResponseRecorder, base string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if strings.HasSuffix(c.Name, base) {
			return c
		}
	}
	t.Fatalf("cookie %s not set", base)
	return nil
}

var hiddenField = regexp.MustCompile(`name="(csrf_token|request)" value="([^"]*)"`)

// formState extracts the hidden fields of a login or consent page.
func formState(t *testing.T, w *httptest.ResponseRecorder) url.Values {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	out := url.Values{}
	for _, m := range hiddenField.FindAllStringSubmatch(w.Body.String(), -1) {
		out.Set(m[1], m[2])
	}
	if out.Get(csrfField) == "" || out.Get(requestField) == "" {
		t.Fatalf("hidden fields missing in page: %s", w.Body.String())
	}
	return out
}

// consentPage opens /authorize with a session and returns the form state and cookies.
func (e *testEnv) consentPage(p pkce) (url.Values, []*http.Cookie) {
	sess := e.sessionCookie()
	w := e.getAuthorize(e.authorizeParams(p), sess)
	return formState(e.t, w), []*http.Cookie{sess, cookieNamed(e.t, w, csrfCookie)}
}

// authorizeCode runs authorize + consent and returns the code from the redirect.
func (e *testEnv) authorizeCode(p pkce) string {
	form, cookies := e.consentPage(p)
	form.Set("action", actionConsent)
	form.Set(levelFieldPrefix+e.serverIDs["github"], "write")
	form.Set(levelFieldPrefix+e.serverIDs["notion"], "read")
	w := e.postForm("/authorize", form, cookies...)
	return redirectParams(e.t, w, http.StatusSeeOther).Get("code")
}

func redirectParams(t *testing.T, w *httptest.ResponseRecorder, status int) url.Values {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func (e *testEnv) exchange(code, verifier, redirect string) *httptest.ResponseRecorder {
	return e.postForm("/token", url.Values{
		"grant_type": {grantAuthorizationCode}, "code": {code}, "code_verifier": {verifier},
		"client_id": {e.clientID}, "redirect_uri": {redirect}, "resource": {testIssuer + "/mcp"},
	})
}

func (e *testEnv) refreshWith(token string) *httptest.ResponseRecorder {
	return e.postForm("/token", url.Values{
		"grant_type": {grantRefreshToken}, "refresh_token": {token}, "client_id": {e.clientID},
	})
}

func decodeTokens(t *testing.T, w *httptest.ResponseRecorder) tokenResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("token status %d: %s", w.Code, w.Body.String())
	}
	var resp tokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func oauthErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var te tokenError
	_ = json.Unmarshal(w.Body.Bytes(), &te)
	return te.Error
}
