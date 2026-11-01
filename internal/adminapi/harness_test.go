package adminapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/adminapi"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const (
	testPassword = "a long enough password"
	publicURL    = "http://127.0.0.1"
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

type harness struct {
	t                   *testing.T
	pool                *pgxpool.Pool
	srv                 *httptest.Server
	oauth               *oauth.Server
	clock               *fakeClock
	adminID, adminEmail string
	userID, userEmail   string
}

// reqOpt tweaks one request: noCSRF drops the X-CSRF-Token header, origin overrides Origin,
// lang sets Accept-Language.
type reqOpt struct {
	noCSRF bool
	origin string
	lang   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	v, err := vault.New(key, "k1")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, pool: storetest.New(t), clock: &fakeClock{now: time.Now()}}
	h.oauth, err = oauth.New(ctx, oauth.Options{PublicURL: publicURL, Pool: h.pool, Sealer: v, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := upstreamauth.New(upstreamauth.Options{
		Pool: h.pool, Vault: v, PublicURL: publicURL,
		User: func(*http.Request) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := adminapi.New(adminapi.Options{
		Pool: h.pool, PublicURL: publicURL, Sealer: v, Login: h.oauth.Login, Now: h.clock.Now,
		Admin: admin.Options{
			Pool:        h.pool,
			Credentials: func() (*upstreamauth.Manager, error) { return mgr, nil },
			Vault:       func() (*vault.Vault, error) { return v, nil },
			Lister:      upstream.New(),
			Grants:      h.oauth,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Routes(mux)
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	h.adminEmail, h.userEmail = "admin@example.com", "user@example.com"
	h.adminID = h.createUser(h.adminEmail, true)
	h.userID = h.createUser(h.userEmail, false)
	return h
}

func (h *harness) createUser(email string, isAdmin bool) string {
	h.t.Helper()
	hash, err := oauth.HashPassword(testPassword)
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

// login returns a client whose cookie jar holds a fresh admin session.
func (h *harness) login(email string) *http.Client {
	h.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res := h.post(c, "/admin/api/session", map[string]string{"email": email, "password": testPassword})
	if res.StatusCode != http.StatusNoContent {
		h.t.Fatalf("login %s: %d %s", email, res.StatusCode, readAll(res))
	}
	return c
}

func (h *harness) csrfToken(c *http.Client) string {
	u, _ := url.Parse(h.srv.URL)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == "mcpgw_admin_csrf" {
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
	if opt.lang != "" {
		req.Header.Set("Accept-Language", opt.lang)
	}
	if c.Jar != nil && !opt.noCSRF {
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

func (h *harness) patch(c *http.Client, path string, body any) *http.Response {
	return h.do(c, http.MethodPatch, path, body, reqOpt{})
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

func readAll(res *http.Response) string {
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func errorCode(t *testing.T, res *http.Response) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	body := readAll(res)
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("not an error envelope: %s", body)
	}
	return env.Error.Code
}
