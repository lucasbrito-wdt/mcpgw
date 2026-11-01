package upstreamauth

import (
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestStaticHeaderMigratedToShared(t *testing.T) {
	e := newEnv(t)
	id := e.insertServer("keyed", "https://keyed.example/mcp", "static_header")
	if err := e.mgr.SetStaticHeader(ctx, e.pool, id, "X-Api-Key", "k-12345"); err != nil {
		t.Fatal(err)
	}
	n, err := e.mgr.MigrateLegacyHeaders(ctx)
	if err != nil || n != 1 {
		t.Fatalf("migrated %d, %v", n, err)
	}
	var kind, name string
	var legacy []byte
	var shared int
	e.pool.QueryRow(ctx, `SELECT auth_kind, header_name, static_header_ciphertext FROM upstream_servers WHERE id = $1::uuid`, id).Scan(&kind, &name, &legacy)
	e.pool.QueryRow(ctx, `SELECT count(*) FROM upstream_credentials WHERE server_id = $1::uuid AND shared AND user_id IS NULL AND last4 = '2345'`, id).Scan(&shared)
	if kind != "header" || name != "X-Api-Key" || legacy != nil || shared != 1 {
		t.Fatalf("kind=%s name=%s legacy=%v shared=%d", kind, name, legacy != nil, shared)
	}
	cred, err := e.mgr.CredentialFor(ctx, id, e.userA)
	if err != nil || cred != (upstream.StaticHeader{Name: "X-Api-Key", Value: "k-12345"}) {
		t.Fatalf("%v %v", cred, err)
	}
	if n, _ := e.mgr.MigrateLegacyHeaders(ctx); n != 0 {
		t.Fatal("not idempotent")
	}
}

func TestMigrateLegacyHeadersSkipsUnopenableValue(t *testing.T) {
	e := newEnv(t)
	bad := e.insertServer("bad", "https://bad.example/mcp", "static_header")
	good := e.insertServer("good", "https://good.example/mcp", "static_header")
	for _, id := range []string{bad, good} {
		if err := e.mgr.SetStaticHeader(ctx, e.pool, id, "X-Api-Key", "k-12345"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_servers SET static_header_ciphertext = '\x00010203'::bytea WHERE id = $1::uuid`, bad); err != nil {
		t.Fatal(err)
	}
	if n, err := e.mgr.MigrateLegacyHeaders(ctx); err != nil || n != 1 {
		t.Fatalf("migrated %d, %v", n, err)
	}
	var left bool
	e.pool.QueryRow(ctx, `SELECT static_header_ciphertext IS NOT NULL FROM upstream_servers WHERE id = $1::uuid`, bad).Scan(&left)
	if !left || e.credentialCount() != 1 {
		t.Fatalf("bad server must keep its legacy value; rows=%d", e.credentialCount())
	}
}
