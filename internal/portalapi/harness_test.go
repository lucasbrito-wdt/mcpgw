package portalapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/portalapi"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const (
	publicURL    = "http://127.0.0.1"
	goodToken    = "good-token-1234"
	goodShared   = "good-shared-5678"
	csrfCookieNm = "mcpgw_portal_csrf"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeUpstream is a header MCP upstream: it accepts only the two known tokens.
type fakeUpstream struct{}

func (fakeUpstream) ListTools(_ context.Context, _ string, cred upstream.Credential) ([]upstream.Tool, upstream.CacheHint, error) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	cred.Apply(req)
	if req.Header.Get("Authorization") == "" && req.Header.Get("X-Alt-Key") == goodToken {
		return []upstream.Tool{{Name: "get_issue"}}, upstream.CacheHint{}, nil
	}
	switch req.Header.Get("Authorization") {
	case "Bearer " + goodToken, "Bearer " + goodShared:
		return []upstream.Tool{{Name: "get_issue"}, {Name: "create_issue"}}, upstream.CacheHint{}, nil
	}
	return nil, upstream.CacheHint{}, &upstream.HTTPStatusError{Status: 401}
}

// swapHandler lets a test capture the logs of an already built API.
type swapHandler struct {
	mu *sync.Mutex
	h  *slog.Handler
}

func (s swapHandler) Enabled(ctx context.Context, l slog.Level) bool { return (*s.h).Enabled(ctx, l) }
func (s swapHandler) Handle(ctx context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (*s.h).Handle(ctx, r)
}
func (s swapHandler) WithAttrs(a []slog.Attr) slog.Handler { return (*s.h).WithAttrs(a) }
func (s swapHandler) WithGroup(n string) slog.Handler      { return (*s.h).WithGroup(n) }

type harness struct {
	t     *testing.T
	pool  *pgxpool.Pool
	srv   *httptest.Server
	oauth *oauth.Server
	mgr   *upstreamauth.Manager
	vault *vault.Vault
	clock *fakeClock

	passwordA, adminPassword string
	emailA, emailB, emailC   string
	userA, userB, userC      string

	logMu sync.Mutex
	log   slog.Handler
}

// reqOpt tweaks one request: noCSRF drops X-CSRF-Token, csrf overrides it, origin overrides Origin.
type reqOpt struct {
	noCSRF bool
	csrf   string
	origin string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	v, err := vault.New(key, "k1")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, pool: storetest.New(t), vault: v, clock: &fakeClock{now: time.Now()},
		passwordA: "a long enough password", adminPassword: "an admin long password",
		emailA: "a@example.com", emailB: "b@example.com", emailC: "c@example.com",
		log: slog.NewTextHandler(io.Discard, nil)}
	h.oauth, err = oauth.New(context.Background(), oauth.Options{PublicURL: publicURL, Pool: h.pool, Sealer: v, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	h.mgr, err = upstreamauth.New(upstreamauth.Options{
		Pool: h.pool, Vault: v, PublicURL: publicURL,
		User: func(*http.Request) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := portalapi.New(portalapi.Options{
		Pool: h.pool, PublicURL: publicURL, Sealer: v, OAuth: h.oauth,
		Credentials: h.mgr, Lister: fakeUpstream{},
		Logger: slog.New(swapHandler{mu: &h.logMu, h: &h.log}),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Routes(mux)
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	h.userA = h.createUser(h.emailA, h.passwordA, false)
	h.userB = h.createUser(h.emailB, h.passwordA, false)
	h.userC = h.createUser(h.emailC, h.adminPassword, true)
	return h
}

// setLogger redirects the API logger (and the default one) to l for the rest of the test.
func (h *harness) setLogger(l *slog.Logger) {
	h.logMu.Lock()
	h.log = l.Handler()
	h.logMu.Unlock()
	prev := slog.Default()
	slog.SetDefault(l)
	h.t.Cleanup(func() { slog.SetDefault(prev) })
}

func (h *harness) createUser(email, password string, isAdmin bool) string {
	h.t.Helper()
	hash, err := oauth.HashPassword(password)
	if err != nil {
		h.t.Fatal(err)
	}
	var id string
	err = h.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, is_admin) VALUES ($1, $2, $3) RETURNING id::text`,
		email, hash, isAdmin).Scan(&id)
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

// loginAs returns a client holding a portal session and the CSRF cookie from GET /me.
func (h *harness) loginAs(email string) *http.Client {
	h.t.Helper()
	password := h.passwordA
	if email == h.emailC {
		password = h.adminPassword
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if res := h.post(c, "/portal/api/session", map[string]string{"email": email, "password": password}); res.StatusCode != http.StatusNoContent {
		h.t.Fatalf("login %s: %d %s", email, res.StatusCode, readAll(res))
	}
	if res := h.get(c, "/portal/api/me"); res.StatusCode != http.StatusOK {
		h.t.Fatalf("me %s: %d", email, res.StatusCode)
	}
	return c
}

func (h *harness) csrfToken(c *http.Client) string {
	u, _ := url.Parse(h.srv.URL)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == csrfCookieNm {
			return ck.Value
		}
	}
	return ""
}

func (h *harness) do(c *http.Client, method, path string, body any, opt reqOpt) *http.Response {
	h.t.Helper()
	if c == nil {
		c = http.DefaultClient
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rd)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	origin := publicURL
	if opt.origin != "" {
		origin = opt.origin
	}
	req.Header.Set("Origin", origin)
	switch {
	case opt.csrf != "":
		req.Header.Set("X-CSRF-Token", opt.csrf)
	case c.Jar != nil && !opt.noCSRF:
		req.Header.Set("X-CSRF-Token", h.csrfToken(c))
	}
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func (h *harness) get(c *http.Client, path string) *http.Response {
	return h.do(c, http.MethodGet, path, nil, reqOpt{})
}
func (h *harness) post(c *http.Client, path string, body any) *http.Response {
	return h.do(c, http.MethodPost, path, body, reqOpt{})
}
func (h *harness) put(c *http.Client, path string, body any) *http.Response {
	return h.do(c, http.MethodPut, path, body, reqOpt{})
}
func (h *harness) del(c *http.Client, path string, body any) *http.Response {
	return h.do(c, http.MethodDelete, path, body, reqOpt{})
}

func (h *harness) exec(query string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), query, args...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) row(query string, args ...any) pgx.Row {
	return h.pool.QueryRow(context.Background(), query, args...)
}

func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.row(query, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) serverID(slug string) string {
	h.t.Helper()
	var id string
	if err := h.row(`SELECT id::text FROM upstream_servers WHERE slug = $1`, slug).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// headerServer adds an active header server (Authorization: Bearer <token>) with a format hint prefix.
func (h *harness) headerServer(slug, hint string) {
	h.t.Helper()
	h.exec(`INSERT INTO upstream_servers (slug, url, auth_kind, header_name, header_prefix, header_format_hint, header_help_url, category)
		VALUES ($1, 'https://up.example.com/mcp', 'header', 'Authorization', 'Bearer ', NULLIF($2, ''),
		        'https://up.example.com/tokens', 'dev')`, slug, hint)
}

func (h *harness) server(slug, authKind string) {
	h.t.Helper()
	h.exec(`INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ($1, 'https://up.example.com/mcp', $2)`, slug, authKind)
}

func (h *harness) ceiling(userID, slug, level string) {
	h.t.Helper()
	h.exec(`INSERT INTO trust_ceilings (user_id, server_id, max_level)
		VALUES ($1::uuid, (SELECT id FROM upstream_servers WHERE slug = $2), $3)`, userID, slug, level)
}

func (h *harness) tool(slug, name, level string) {
	h.t.Helper()
	h.exec(`INSERT INTO tools (server_id, name, schema_hash, trust_level)
		VALUES ((SELECT id FROM upstream_servers WHERE slug = $1), $2, 'h', $3)`, slug, name, level)
}

func readAll(res *http.Response) string {
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

// decode reads a JSON response body into a generic map.
func decode(t *testing.T, res *http.Response) map[string]any {
	t.Helper()
	body := readAll(res)
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not JSON (%d): %s", res.StatusCode, body)
	}
	return m
}

func errorCode(t *testing.T, res *http.Response) string {
	t.Helper()
	e, _ := decode(t, res)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// slugs lists the item slugs of a /connections response.
func slugs(t *testing.T, m map[string]any) []string {
	t.Helper()
	items, _ := m["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprint(it.(map[string]any)["slug"]))
	}
	return out
}
