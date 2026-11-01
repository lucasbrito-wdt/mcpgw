package main

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

func webTestConfig(t *testing.T, pool *pgxpool.Pool) *config.Config {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		DatabaseURL: pool.Config().ConnString(),
		ListenAddr:  "127.0.0.1:0",
		PublicURL:   "http://127.0.0.1",
		MasterKey:   key,
	}
}

func startWebGateway(t *testing.T, cfg *config.Config) *gateway {
	t.Helper()
	gw, err := newGateway(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.Close(context.Background()) })
	return gw
}

func noRedirectGet(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestGatewayMountsAdminAndPortal(t *testing.T) {
	gw := startWebGateway(t, webTestConfig(t, storetest.New(t)))
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()
	cases := []struct {
		path string
		want int
	}{
		{"/admin/api/me", 401},
		{"/portal/api/me", 401},
		{"/admin/api/nope", 401}, // the adminapi catch-all is behind requireAdmin
		{"/admin", 308},
		{"/portal", 308},
		{"/healthz", 200},
	}
	for _, c := range cases {
		if res := noRedirectGet(t, srv.URL+c.path); res.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", c.path, res.StatusCode, c.want)
		}
	}
	// /admin/ answers 200 with the built UI or 503 with the placeholder; both carry the CSP header.
	res := noRedirectGet(t, srv.URL+"/admin/")
	if res.StatusCode != 200 && res.StatusCode != 503 {
		t.Fatalf("/admin/: %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("no CSP")
	}
}

func TestGatewayMigratesLegacyHeaders(t *testing.T) {
	pool := storetest.New(t)
	cfg := webTestConfig(t, pool)
	ctx := context.Background()

	v, err := vault.New(cfg.MasterKey, vaultKeyID)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := upstreamauth.New(upstreamauth.Options{
		Pool: pool, Vault: v, PublicURL: cfg.PublicURL,
		User: func(*http.Request) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('keyed', 'https://keyed.example/mcp', 'static_header') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetStaticHeader(ctx, pool, id, "X-Api-Key", "k-12345"); err != nil {
		t.Fatal(err)
	}

	check := func(label string) {
		t.Helper()
		var kind string
		var legacy bool
		var shared int
		pool.QueryRow(ctx, `SELECT auth_kind, static_header_ciphertext IS NOT NULL FROM upstream_servers WHERE id = $1::uuid`, id).Scan(&kind, &legacy)
		pool.QueryRow(ctx, `SELECT count(*) FROM upstream_credentials WHERE server_id = $1::uuid AND shared`, id).Scan(&shared)
		if kind != "header" || legacy || shared != 1 {
			t.Fatalf("%s: kind=%s legacy=%v shared=%d", label, kind, legacy, shared)
		}
	}
	startWebGateway(t, cfg)
	check("first start")
	startWebGateway(t, cfg)
	check("second start")
}
