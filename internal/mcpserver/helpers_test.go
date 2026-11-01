package mcpserver

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const (
	testIssuer   = "https://gw.example.test"
	testEmail    = "lucas@example.test"
	testPassword = "correct horse battery staple"
	callback     = "http://127.0.0.1:53682/callback"
)

var sharedSandbox *sandbox.Executor

func TestMain(m *testing.M) {
	exec, err := sandbox.NewExecutor()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
	sharedSandbox = exec
	code := m.Run()
	_ = exec.Close()
	os.Exit(code)
}

// aesSealer is a test oauth.Sealer (the real one lives in the vault package).
type aesSealer struct{ aead cipher.AEAD }

func newSealer(t *testing.T) *aesSealer {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	block, err := aes.NewCipher(key)
	must(t, err)
	aead, err := cipher.NewGCM(block)
	must(t, err)
	return &aesSealer{aead: aead}
}

func (s *aesSealer) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (s *aesSealer) Open(ct, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(ct) < n {
		return nil, errors.New("short ciphertext")
	}
	return s.aead.Open(nil, ct[:n], ct[n:], aad)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	must(t, err)
}

// newFakeUpstream is an MCP server over httptest with one tool.
func newFakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "get_item", Description: "Fetch an item by id", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any
			_ = json.Unmarshal(req.Params.Arguments, &args)
			body := fmt.Sprintf(`{"id":%q,"name":"widget"}`, args["id"])
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// env is a real oauth.Server + codemode.Service + Handler over a throwaway database.
type env struct {
	t        *testing.T
	pool     *pgxpool.Pool
	oauth    *oauth.Server
	oauthMux *http.ServeMux
	handler  *Handler
	srv      *httptest.Server
	userID   string
	serverID string
}

func newEnv(t *testing.T, o Options) *env {
	t.Helper()
	ctx := context.Background()
	pool := storetest.New(t)
	up := newFakeUpstream(t)
	e := &env{t: t, pool: pool}

	hash, err := oauth.HashPassword(testPassword)
	must(t, err)
	must(t, pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text`, testEmail, hash).Scan(&e.userID))
	must(t, pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('acme', $1, 'none') RETURNING id::text`, up.URL).Scan(&e.serverID))
	exec(t, pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level, status)
		VALUES ($1::uuid, 'get_item', 'Fetch an item by id', 'h', 'read', 'active')`, e.serverID)
	exec(t, pool, `INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid, $2::uuid, 'read')`, e.userID, e.serverID)

	e.oauth, err = oauth.New(ctx, oauth.Options{PublicURL: testIssuer, Pool: pool, Sealer: newSealer(t), Logger: slog.New(slog.DiscardHandler)})
	must(t, err)
	e.oauthMux = http.NewServeMux()
	e.oauth.Routes(e.oauthMux)

	pol, err := policy.New(ctx, pool)
	must(t, err)
	rec := audit.New(pool, audit.Options{FlushInterval: 20 * time.Millisecond})
	t.Cleanup(func() { _ = rec.Close(context.Background()) })
	svc, err := codemode.New(codemode.Options{
		Pool: pool, Sandbox: sharedSandbox, Policy: pol, Upstream: upstream.New(), Audit: rec,
		PublicURL: testIssuer, Logger: slog.New(slog.DiscardHandler),
	})
	must(t, err)

	o.OAuth, o.Codemode = e.oauth, svc
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	e.handler, err = New(o)
	must(t, err)
	e.srv = httptest.NewServer(e.handler)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.oauthMux.ServeHTTP(w, r)
	return w
}

var hiddenField = regexp.MustCompile(`name="(csrf_token|request)" value="([^"]*)"`)

func hiddenFields(t *testing.T, w *httptest.ResponseRecorder) url.Values {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	out := url.Values{}
	for _, m := range hiddenField.FindAllStringSubmatch(w.Body.String(), -1) {
		out.Set(m[1], m[2])
	}
	return out
}

func (e *env) form(path string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return e.do(r)
}

// issueToken runs the real public OAuth flow (DCR, login, consent, code exchange) and returns an
// access token whose client granted level on the acme server ("none" grants nothing).
func (e *env) issueToken(level string) string {
	t := e.t
	t.Helper()
	reg := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(
		`{"client_name":"test client","redirect_uris":["http://127.0.0.1/callback"],"token_endpoint_auth_method":"none"}`))
	reg.Header.Set("Content-Type", "application/json")
	regResp := e.do(reg)
	if regResp.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", regResp.Code, regResp.Body.String())
	}
	var client struct {
		ClientID string `json:"client_id"`
	}
	must(t, json.Unmarshal(regResp.Body.Bytes(), &client))

	sum := sha256.Sum256([]byte("verifier-0123456789-0123456789-0123456789-abcdef"))
	verifier := "verifier-0123456789-0123456789-0123456789-abcdef"
	q := url.Values{
		"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {callback},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"s"}, "resource": {testIssuer + "/mcp"}, "scope": {oauth.ScopeMCP},
	}
	page := e.do(httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil))
	cookies := page.Result().Cookies()
	login := hiddenFields(t, page)
	login.Set("action", "login")
	login.Set("email", testEmail)
	login.Set("password", testPassword)
	consentPage := e.form("/authorize", login, cookies)
	cookies = append(cookies, consentPage.Result().Cookies()...)
	consent := hiddenFields(t, consentPage)
	consent.Set("action", "consent")
	consent.Set("level_"+e.serverID, level)
	done := e.form("/authorize", consent, cookies)
	if done.Code != http.StatusSeeOther {
		t.Fatalf("consent: %d %s", done.Code, done.Body.String())
	}
	loc, err := url.Parse(done.Header().Get("Location"))
	must(t, err)

	tok := e.form("/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier},
		"client_id": {client.ClientID}, "redirect_uri": {callback}, "resource": {testIssuer + "/mcp"},
	}, nil)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if tok.Code != http.StatusOK {
		t.Fatalf("token: %d %s", tok.Code, tok.Body.String())
	}
	must(t, json.Unmarshal(tok.Body.Bytes(), &resp))
	return resp.AccessToken
}

// connect opens an SDK client session against the handler with the given bearer token.
func (e *env) connect(token string) *mcp.ClientSession {
	e.t.Helper()
	hc := &http.Client{Transport: bearerTransport{token: token}}
	ct := &mcp.StreamableClientTransport{Endpoint: e.srv.URL, HTTPClient: hc, DisableStandaloneSSE: true}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	must(e.t, err)
	e.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// rawRequest is a hand-built POST to the handler, for the cases the SDK client cannot produce.
type rawRequest struct {
	token   string
	headers map[string]string
	body    string
	query   string
}

const metaVersion = `"_meta":{"io.modelcontextprotocol/protocolVersion":"` + protocolVersion +
	`","io.modelcontextprotocol/clientInfo":{"name":"raw","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}`

const listBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaVersion + `}}`

func callBody(name, args string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `,` + metaVersion + `}}`
}

func post(t *testing.T, h http.Handler, rr rawRequest) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp"+rr.query, bytes.NewReader([]byte(rr.body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Mcp-Protocol-Version", protocolVersion)
	if rr.token != "" {
		r.Header.Set("Authorization", "Bearer "+rr.token)
	}
	for k, v := range rr.headers {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func bodyOf(w *httptest.ResponseRecorder) string {
	b, _ := io.ReadAll(w.Result().Body)
	return string(b)
}

// fakeAuth is a TokenValidator seam for cases a real oauth.Server cannot produce.
type fakeAuth struct {
	claims oauth.Claims
	err    error
}

func (f fakeAuth) ValidateAccessToken(context.Context, string) (oauth.Claims, error) {
	return f.claims, f.err
}
func (fakeAuth) WWWAuthenticate() string { return `Bearer resource_metadata="x"` }
func (fakeAuth) Issuer() string          { return testIssuer }

// fakeTools is a ToolService seam.
type fakeTools struct {
	searchErr, executeErr error
	out                   json.RawMessage
	desc                  func(policy.Subject) (string, string)
}

func (f fakeTools) Search(context.Context, policy.Subject, string) (codemode.SearchResult, error) {
	return codemode.SearchResult{}, f.searchErr
}

func (f fakeTools) Execute(context.Context, policy.Subject, string) (json.RawMessage, error) {
	return f.out, f.executeErr
}

func (f fakeTools) ToolDescriptions(_ context.Context, s policy.Subject) (string, string) {
	if f.desc != nil {
		return f.desc(s)
	}
	return "search desc", "execute desc"
}

// handlerTransport routes SDK client requests straight into a handler (no network).
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	return w.Result(), nil
}

func newGet() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer t")
	r.Header.Set("Mcp-Protocol-Version", protocolVersion)
	return r
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
