package upstream_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

func newVault(t *testing.T) *vault.Vault {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	v, err := vault.New(key, "k1")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// insertStdioServer stores a host-launched stdio server with a sealed environment.
func insertStdioServer(t *testing.T, pool *pgxpool.Pool, v *vault.Vault, spec upstream.StdioSpec) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if spec.Args == nil {
		spec.Args = []string{}
	}
	err := pool.QueryRow(ctx, `WITH n AS (SELECT uuidv7() AS id)
		INSERT INTO upstream_servers (id, slug, url, auth_kind, transport, stdio_launcher, stdio_command, stdio_args)
		SELECT id, 'local', 'stdio://' || id, 'none', 'stdio', $1, $2, $3 FROM n RETURNING id::text`,
		string(spec.Launcher), spec.Command, spec.Args).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := upstream.SealStdioEnv(v, id, spec.Env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE upstream_servers SET stdio_env_ciphertext = $2 WHERE id = $1`, id, ct); err != nil {
		t.Fatal(err)
	}
	return upstream.StdioKeyPrefix + id
}

func TestDBResolverStdioEndToEnd(t *testing.T) {
	pool := storetest.New(t)
	v := newVault(t)
	spec := stdioSpec(t, "legacy", map[string]string{"API_TOKEN": "sealed-secret-1"})
	key := insertStdioServer(t, pool, v, spec)

	var ct []byte
	if err := pool.QueryRow(context.Background(), `SELECT stdio_env_ciphertext FROM upstream_servers`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("sealed-secret-1")) {
		t.Fatal("environment stored in clear")
	}

	r := upstream.NewDBResolver(pool, v)
	ep, err := r.Resolve(context.Background(), key)
	if err != nil || ep.Stdio == nil || ep.Stdio.Env["API_TOKEN"] != "sealed-secret-1" || ep.Stdio.Command != spec.Command {
		t.Fatalf("resolve: %+v %v", ep, err)
	}

	c := upstream.New(upstream.WithResolver(r))
	defer c.Close()
	if got := callText(t, c, context.Background(), key, "echo", `{"text":"via db"}`); got != "via db" {
		t.Fatalf("echo = %q", got)
	}
	var transport, version string
	err = pool.QueryRow(context.Background(), `SELECT detected_transport, detected_protocol_version FROM upstream_servers`).Scan(&transport, &version)
	if err != nil || transport != "stdio" || version != legacyVersion {
		t.Fatalf("stored detection = %q %q (%v)", transport, version, err)
	}
	ep, _ = r.Resolve(context.Background(), key)
	if ep.Detected != (upstream.Detection{Transport: upstream.TransportStdio, ProtocolVersion: legacyVersion}) {
		t.Fatalf("detection not resolved back: %+v", ep.Detected)
	}
}

func TestDBResolverEnvBoundToRow(t *testing.T) {
	pool := storetest.New(t)
	v := newVault(t)
	keyA := insertStdioServer(t, pool, v, stdioSpec(t, "modern", map[string]string{"T": "a"}))
	_, err := pool.Exec(context.Background(), `INSERT INTO upstream_servers (slug, url, auth_kind, transport, stdio_launcher, stdio_command, stdio_env_ciphertext)
		SELECT 'copy', 'stdio://copy', 'none', 'stdio', 'host', stdio_command, stdio_env_ciphertext FROM upstream_servers WHERE url = $1`, keyA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upstream.NewDBResolver(pool, v).Resolve(context.Background(), "stdio://copy"); err == nil {
		t.Fatal("ciphertext copied to another row opened")
	}
}

func TestDBResolverHTTPRows(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind, transport) VALUES ('legacy', 'http://h.example/sse', 'none', 'sse')`)
	if err != nil {
		t.Fatal(err)
	}
	r := upstream.NewDBResolver(pool, nil)
	ep, err := r.Resolve(ctx, "http://h.example/sse")
	if err != nil || ep.Transport != upstream.TransportSSE || ep.URL != "http://h.example/sse" {
		t.Fatalf("resolve: %+v %v", ep, err)
	}
	if ep, err := r.Resolve(ctx, "https://unregistered.example/mcp"); err != nil || ep.Transport != upstream.TransportAuto {
		t.Fatalf("unregistered http: %+v %v", ep, err)
	}
	if _, err := r.Resolve(ctx, "stdio://missing"); !errors.Is(err, upstream.ErrUnknownEndpoint) {
		t.Fatalf("missing stdio: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind, transport, stdio_launcher, docker_image)
		VALUES ('bad', 'http://x.example', 'none', 'auto', 'docker', 'img')`); err == nil {
		t.Fatal("stdio fields accepted on an HTTP row")
	}
}
