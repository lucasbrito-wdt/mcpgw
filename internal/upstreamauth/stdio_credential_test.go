package upstreamauth

import (
	"errors"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// insertStdioHeaderServer is a docker stdio server whose users paste their own token, delivered as
// PORTAINER_API_KEY. The header prefix is set to prove it is not applied to environment values.
func (e *env) insertStdioHeaderServer(slug string) string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(ctx, `WITH n AS (SELECT uuidv7() AS id)
		INSERT INTO upstream_servers (id, slug, url, auth_kind, transport, stdio_launcher, docker_image, credential_env, header_prefix)
		SELECT id, $1, 'stdio://' || id, 'header', 'stdio', 'docker', 'portainer/portainer-mcp:2.45.3', 'PORTAINER_API_KEY', 'Bearer '
		FROM n RETURNING id::text`, slug).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func envValue(t *testing.T, cred upstream.Credential, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	ec, ok := cred.(upstream.EnvCredential)
	if !ok || ec.Name != "PORTAINER_API_KEY" {
		t.Fatalf("credential is %T %+v", cred, cred)
	}
	return ec.Value
}

func TestStdioHeaderServerResolvesEnvCredential(t *testing.T) {
	e := newEnv(t)
	id := e.insertStdioHeaderServer("portainer")
	if _, err := e.mgr.CredentialFor(ctx, id, e.userA); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("no token: %v", err)
	}
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "ptr_alice_default", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, id, e.userA, "ops", "ptr_alice_ops_01"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userB, "ptr_bob_default", false); err != nil {
		t.Fatal(err)
	}
	cred, err := e.mgr.CredentialFor(ctx, id, e.userA)
	if got := envValue(t, cred, err); got != "ptr_alice_default" {
		t.Fatalf("alice default = %q (raw token, no prefix)", got)
	}
	cred, err = e.mgr.ProfileCredentialFor(ctx, id, e.userA, "ops")
	if got := envValue(t, cred, err); got != "ptr_alice_ops_01" {
		t.Fatalf("alice ops = %q", got)
	}
	cred, err = e.mgr.CredentialFor(ctx, id, e.userB)
	if got := envValue(t, cred, err); got != "ptr_bob_default" {
		t.Fatalf("bob = %q", got)
	}
}

func TestStdioTokenTestIsUnsupported(t *testing.T) {
	e := newEnv(t)
	id := e.insertStdioHeaderServer("portainer")
	lister := upstream.New()
	defer lister.Close()
	if _, err := e.mgr.TestToken(ctx, lister, id, e.userA, "ptr_candidate_1"); !errors.Is(err, ErrTokenTestUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
