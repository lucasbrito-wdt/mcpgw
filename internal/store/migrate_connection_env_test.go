package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

func TestConnectionEnvConstraints(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	insert := func(slug string, credentialEnv any, connectionEnv []string) error {
		authKind := "header"
		if credentialEnv == nil {
			authKind = "none"
		}
		_, err := pool.Exec(ctx, `WITH n AS (SELECT uuidv7() AS id)
			INSERT INTO upstream_servers (id, slug, url, auth_kind, transport, stdio_launcher, docker_image, credential_env, connection_env)
			SELECT id, $1, 'stdio://' || id, $2, 'stdio', 'docker', 'portainer/portainer-mcp:2.45.3', $3, $4 FROM n`,
			slug, authKind, credentialEnv, connectionEnv)
		return err
	}
	if err := insert("ok", "PORTAINER_API_KEY", []string{"PORTAINER_URL", "PORTAINER_TLS"}); err != nil {
		t.Fatalf("valid connection_env: %v", err)
	}
	if err := insert("empty", nil, []string{}); err != nil {
		t.Fatalf("no connection_env: %v", err)
	}
	many := make([]string, 17)
	for i := range many {
		many[i] = "V" + strings.Repeat("A", i)
	}
	for name, err := range map[string]error{
		"without credential_env": insert("a", nil, []string{"PORTAINER_URL"}),
		"includes credential":    insert("b", "PORTAINER_API_KEY", []string{"PORTAINER_API_KEY"}),
		"lowercase":              insert("c", "PORTAINER_API_KEY", []string{"portainer_url"}),
		"duplicate":              insert("d", "PORTAINER_API_KEY", []string{"X", "X"}),
		"empty name":             insert("e", "PORTAINER_API_KEY", []string{""}),
		"too many":               insert("f", "PORTAINER_API_KEY", many),
	} {
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('h', 'https://h.example/mcp', 'none')`); err != nil {
		t.Fatalf("http server default connection_env: %v", err)
	}
}

func TestRevokedCredentialHoldsNoEnv(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	a, _, s := seedCredentialFixture(t, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, status, revoked_reason, key_id, env_ciphertext)
		VALUES ($1, $2, 'revoked_by_admin', 'x', 'k1', '\x01')`, s, a); err == nil {
		t.Fatal("revoked row holding env_ciphertext accepted")
	}
}
