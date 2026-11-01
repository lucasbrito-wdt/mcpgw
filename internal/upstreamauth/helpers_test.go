package upstreamauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const testUserHeader = "X-Test-User"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t        *testing.T
	pool     *pgxpool.Pool
	vault    *vault.Vault
	fake     *fakeAS
	mgr      *Manager
	mux      *http.ServeMux
	clock    *clock
	userA    string
	userB    string
	serverID string
}

func newEnv(t *testing.T) *env { return newEnvWithPublicURL(t, "http://localhost:8787") }

func newEnvWithPublicURL(t *testing.T, publicURL string) *env {
	t.Helper()
	pool := storetest.New(t)
	v, err := vault.New([]byte("0123456789abcdef0123456789abcdef"), "k1")
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeAS(t)
	mgr, err := New(Options{Pool: pool, Vault: v, PublicURL: publicURL,
		User: func(r *http.Request) (string, bool) {
			id := r.Header.Get(testUserHeader)
			return id, id != ""
		}})
	if err != nil {
		t.Fatal(err)
	}
	mgr.http = testFetcher(fake.srv, 5*time.Second)
	clk := &clock{t: time.Now()}
	mgr.now = clk.Now
	mux := http.NewServeMux()
	mgr.Routes(mux)
	e := &env{t: t, pool: pool, vault: v, fake: fake, mgr: mgr, mux: mux, clock: clk}
	e.userA = e.insertUser("a@example.com")
	e.userB = e.insertUser("b@example.com")
	e.serverID = e.insertServer("alpha", fake.mcpURL(), AuthKindOAuth)
	e.grant(e.userA, e.serverID)
	e.grant(e.userB, e.serverID)
	return e
}

// grant gives the user a read ceiling on the server (required to connect an account to it).
func (e *env) grant(userID, serverID string) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid, $2::uuid, 'read')`, userID, serverID)
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) insertUser(email string) string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`, email).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) insertServer(slug, serverURL, kind string) string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(context.Background(),
		`INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ($1, $2, $3) RETURNING id::text`,
		slug, serverURL, kind).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) get(userID, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if userID != "" {
		req.Header.Set(testUserHeader, userID)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// startFlow hits /connect/{slug} and returns the authorization URL the user is sent to.
func (e *env) startFlow(userID, slug string) string {
	e.t.Helper()
	rec := e.get(userID, "/connect/"+slug)
	if rec.Code != http.StatusFound {
		e.t.Fatalf("connect: status %d body %s", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

func (e *env) callback(userID string, q url.Values) *httptest.ResponseRecorder {
	return e.get(userID, "/connect/callback?"+q.Encode())
}

// connect runs the whole flow for userID and requires success.
func (e *env) connect(userID, slug string) {
	e.t.Helper()
	rec := e.callback(userID, e.fake.authorize(e.startFlow(userID, slug)))
	if rec.Code != http.StatusSeeOther {
		e.t.Fatalf("callback: status %d body %s", rec.Code, rec.Body.String())
	}
}

func (e *env) credentialCount() int {
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM upstream_credentials`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}
