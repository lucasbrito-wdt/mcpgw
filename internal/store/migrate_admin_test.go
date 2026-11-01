package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

func TestAdminEventsTableAcceptsAuditRow(t *testing.T) {
	pool := storetest.New(t)
	_, err := pool.Exec(context.Background(), `INSERT INTO admin_events (actor_user_id, action, target, details, ip)
		VALUES (uuidv7(), 'server.set_header', 'github', '{"name":"X-Api-Key"}', '127.0.0.1')`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestHeaderServerConfig(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind, header_name, header_prefix, header_format_hint, header_help_url, category)
		VALUES ('gh', 'https://gh.example/mcp', 'header', 'Authorization', 'Bearer ', 'github_pat_', 'https://github.com/settings/tokens', 'dev')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE upstream_servers SET header_help_url = 'http://x.example' WHERE slug = 'gh'`); err == nil {
		t.Fatal("non-https help url accepted")
	}
	if _, err = pool.Exec(ctx, `UPDATE upstream_servers SET category = 'Dev Tools!' WHERE slug = 'gh'`); err == nil {
		t.Fatal("invalid category accepted")
	}
	// legacy value still accepted during the expand phase
	if _, err = pool.Exec(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('old', 'https://o.example/mcp', 'static_header')`); err != nil {
		t.Fatal(err)
	}
}

// seedCredentialFixture inserts users A and B plus server S and returns their ids.
func seedCredentialFixture(t *testing.T, pool *pgxpool.Pool) (userA, userB, server string) {
	t.Helper()
	ctx := context.Background()
	for email, dst := range map[string]*string{"a@example.com": &userA, "b@example.com": &userB} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, password_hash) VALUES ($1, 'h') RETURNING id::text`, email).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('s', 'https://s.example/mcp', 'header') RETURNING id::text`).Scan(&server); err != nil {
		t.Fatal(err)
	}
	return userA, userB, server
}

func TestSharedAndPersonalUniqueness(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	a, b, s := seedCredentialFixture(t, pool)

	personal := func(user string) error {
		_, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, access_ciphertext, key_id)
			VALUES ($1, $2, '\x01', 'k1')`, s, user)
		return err
	}
	shared := func(connectedBy string) error {
		_, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, shared, connected_by, access_ciphertext, key_id)
			VALUES ($1, true, $2, '\x01', 'k1')`, s, connectedBy)
		return err
	}

	if err := personal(a); err != nil {
		t.Fatal(err)
	}
	if err := personal(a); err == nil {
		t.Fatal("duplicate personal (server, user) accepted")
	}
	if err := personal(b); err != nil {
		t.Fatal(err)
	}
	if err := shared(a); err != nil {
		t.Fatal(err)
	}
	if err := shared(a); err == nil {
		t.Fatal("second shared row for the same server accepted")
	}

	if _, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, access_ciphertext, key_id)
		VALUES ($1, '\x01', 'k1')`, s); err == nil {
		t.Fatal("personal row without user_id accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, shared, access_ciphertext, key_id)
		VALUES ($1, $2, true, '\x01', 'k1')`, s, a); err == nil {
		t.Fatal("shared row with user_id accepted")
	}
}

func TestInactiveCredentialHoldsNoSecret(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	a, _, s := seedCredentialFixture(t, pool)

	if _, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, status, key_id)
		VALUES ($1, $2, 'active', 'k1')`, s, a); err == nil {
		t.Fatal("active row without access_ciphertext accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, status, access_ciphertext, key_id)
		VALUES ($1, $2, 'revoked_by_admin', '\x01', 'k1')`, s, a); err == nil {
		t.Fatal("revoked row holding access_ciphertext accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, status, revoked_reason, key_id)
		VALUES ($1, $2, 'revoked_by_admin', 'saiu do time', 'k1')`, s, a); err != nil {
		t.Fatal(err)
	}
}

func TestStdioCredentialEnvConstraints(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	insert := func(slug, authKind string, credentialEnv any) error {
		_, err := pool.Exec(ctx, `WITH n AS (SELECT uuidv7() AS id)
			INSERT INTO upstream_servers (id, slug, url, auth_kind, transport, stdio_launcher, docker_image, credential_env)
			SELECT id, $1, 'stdio://' || id, $2, 'stdio', 'docker', 'portainer/portainer-mcp:2.45.3', $3 FROM n`,
			slug, authKind, credentialEnv)
		return err
	}
	if err := insert("oknone", "none", nil); err != nil {
		t.Fatalf("stdio none: %v", err)
	}
	if err := insert("okheader", "header", "PORTAINER_API_KEY"); err != nil {
		t.Fatalf("stdio header with credential_env: %v", err)
	}
	for name, err := range map[string]error{
		"header without credential_env": insert("a", "header", nil),
		"none with credential_env":      insert("b", "none", "PORTAINER_API_KEY"),
		"oauth":                         insert("c", "oauth", "PORTAINER_API_KEY"),
		"lowercase name":                insert("d", "header", "portainer_key"),
		"leading digit":                 insert("e", "header", "1KEY"),
		"too long":                      insert("f", "header", "K"+strings.Repeat("A", 64)),
	} {
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	_, err := pool.Exec(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind, header_name, credential_env)
		VALUES ('http', 'https://h.example/mcp', 'header', 'Authorization', 'API_KEY')`)
	if err == nil {
		t.Error("http server with credential_env accepted")
	}
}
