package admin_test

import (
	"context"
	"crypto/rand"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const goodPassword = "a long enough password"

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	svc    *admin.Service
	vault  *vault.Vault
	lister *staticLister
	audit  func(ctx context.Context, tx pgx.Tx, ev admin.Event) error
	events []admin.Event
}

// staticLister serves a fixed tool list as catalog.Lister.
type staticLister struct{ tools []upstream.Tool }

func (l *staticLister) ListTools(context.Context, string, upstream.Credential) ([]upstream.Tool, upstream.CacheHint, error) {
	return l.tools, upstream.CacheHint{}, nil
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	v, err := vault.New(key, "k1")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, pool: storetest.New(t), lister: &staticLister{}, vault: v}
	mgr, err := upstreamauth.New(upstreamauth.Options{
		Pool: h.pool, Vault: v, PublicURL: "http://localhost:8787",
		User: func(*http.Request) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc = admin.New(admin.Options{
		Pool:        h.pool,
		Credentials: func() (*upstreamauth.Manager, error) { return mgr, nil },
		Vault:       func() (*vault.Vault, error) { return v, nil },
		Lister:      h.lister,
		Audit: func(ctx context.Context, tx pgx.Tx, ev admin.Event) error {
			if h.audit != nil {
				return h.audit(ctx, tx, ev)
			}
			h.events = append(h.events, ev)
			return nil
		},
	})
	return h
}

func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) user(email string) admin.User {
	h.t.Helper()
	u, err := h.svc.CreateUser(context.Background(), admin.CreateUserInput{Email: email, Password: goodPassword})
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) server(slug string) {
	h.t.Helper()
	_, err := h.svc.AddServer(context.Background(), admin.AddServerInput{Slug: slug, URL: "http://127.0.0.1:9999/mcp", AuthKind: "none"})
	if err != nil {
		h.t.Fatal(err)
	}
}

var ctx = context.Background()
