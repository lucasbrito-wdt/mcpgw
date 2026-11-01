package upstreamauth

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// insertPortainer is a stdio header server declaring PORTAINER_URL (with a fixed default) and
// PORTAINER_TEAM (no default) as per-connection variables.
func (e *env) insertPortainer() string {
	e.t.Helper()
	id := e.insertStdioHeaderServer("portainer")
	sealed, err := upstream.SealStdioEnv(e.vault, id, map[string]string{"PORTAINER_URL": "https://default.example"})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_servers SET stdio_env_ciphertext = $2,
		connection_env = '{PORTAINER_URL,PORTAINER_TEAM}' WHERE id = $1`, id, sealed); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// envOf checks a resolved credential: envOf(t)(mgr.CredentialFor(...)).
func envOf(t *testing.T) func(upstream.Credential, error) upstream.EnvCredential {
	return func(cred upstream.Credential, err error) upstream.EnvCredential {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		ec, ok := cred.(upstream.EnvCredential)
		if !ok {
			t.Fatalf("credential is %T", cred)
		}
		return ec
	}
}

func TestConnectionEnvPerProfile(t *testing.T) {
	e := newEnv(t)
	id := e.insertPortainer()
	writes := []ConnectionInput{
		{ServerID: id, ActorID: e.userA, Profile: DefaultProfile, Token: "ptr_acme_000001",
			Env: map[string]string{"PORTAINER_TEAM": "a"}},
		{ServerID: id, ActorID: e.userA, Profile: "globex", Token: "ptr_globex_0001",
			Env: map[string]string{"PORTAINER_URL": "https://globex.example", "PORTAINER_TEAM": "z"}},
	}
	for _, in := range writes {
		if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
			t.Fatal(err)
		}
	}
	def := envOf(t)(e.mgr.CredentialFor(ctx, id, e.userA))
	if def.Value != "ptr_acme_000001" || def.Env["PORTAINER_TEAM"] != "a" {
		t.Fatalf("default connection = %v", def.Env)
	}
	if _, set := def.Env["PORTAINER_URL"]; set {
		t.Fatal("unset variable must fall back to the fixed default, not be overridden")
	}
	globex := envOf(t)(e.mgr.ProfileCredentialFor(ctx, id, e.userA, "globex"))
	if globex.Value != "ptr_globex_0001" || globex.Env["PORTAINER_URL"] != "https://globex.example" {
		t.Fatalf("globex connection = %v", globex.Env)
	}

	vars, err := e.mgr.ConnectionVars(ctx, e.pool, id)
	if err != nil {
		t.Fatal(err)
	}
	want := []ConnectionVar{{Name: "PORTAINER_URL", HasDefault: true}, {Name: "PORTAINER_TEAM"}}
	if !slices.Equal(vars, want) {
		t.Fatalf("vars = %+v", vars)
	}
	var ct []byte
	if err := e.pool.QueryRow(ctx, `SELECT env_ciphertext FROM upstream_credentials WHERE profile = 'globex'`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	names, err := e.mgr.ConnectionEnvSet(id, e.userA, "globex", ct)
	if err != nil || !slices.Equal(names, []string{"PORTAINER_TEAM", "PORTAINER_URL"}) {
		t.Fatalf("env set = %v, %v", names, err)
	}
	if _, err := e.mgr.ConnectionEnvSet(id, e.userA, DefaultProfile, ct); err == nil {
		t.Fatal("env sealed for one profile opened under another")
	}
}

func TestConnectionEnvValidation(t *testing.T) {
	e := newEnv(t)
	id := e.insertPortainer()
	base := ConnectionInput{ServerID: id, ActorID: e.userA, Profile: DefaultProfile, Token: "ptr_acme_000001"}
	cases := map[string]map[string]string{
		"unknown name":     {"PORTAINER_TEAM": "a", "OTHER": "x"},
		"empty value":      {"PORTAINER_TEAM": ""},
		"newline":          {"PORTAINER_TEAM": "a\nb"},
		"too long":         {"PORTAINER_TEAM": strings.Repeat("a", 2049)},
		"required missing": {"PORTAINER_URL": "https://x.example"},
		"credential name":  {"PORTAINER_TEAM": "a", "PORTAINER_API_KEY": "x"},
	}
	for name, envIn := range cases {
		in := base
		in.Env = envIn
		var envErr *EnvError
		if _, err := e.mgr.SetConnection(ctx, e.pool, in); !errors.As(err, &envErr) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A new connection without env misses the required PORTAINER_TEAM.
	var envErr *EnvError
	if _, err := e.mgr.SetConnection(ctx, e.pool, base); !errors.As(err, &envErr) || envErr.Name != "PORTAINER_TEAM" {
		t.Fatalf("new row without env: %v", err)
	}
}

func TestConnectionEnvUpdateKeepsToken(t *testing.T) {
	e := newEnv(t)
	id := e.insertPortainer()
	in := ConnectionInput{ServerID: id, ActorID: e.userA, Profile: "ops", Env: map[string]string{"PORTAINER_TEAM": "a"}}
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); !errors.Is(err, ErrTokenRequired) {
		t.Fatalf("env-only write on a missing row: %v", err)
	}
	in.Token = "ptr_ops_0000001"
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
		t.Fatal(err)
	}
	in.Token, in.Env = "", map[string]string{"PORTAINER_TEAM": "b"}
	last4, err := e.mgr.SetConnection(ctx, e.pool, in)
	if err != nil || last4 != "0001" {
		t.Fatalf("env-only write: %q %v", last4, err)
	}
	got := envOf(t)(e.mgr.ProfileCredentialFor(ctx, id, e.userA, "ops"))
	if got.Value != "ptr_ops_0000001" || got.Env["PORTAINER_TEAM"] != "b" {
		t.Fatalf("after env-only write: %v", got.Env)
	}
	// Re-sending the token without env keeps the stored values.
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, id, e.userA, "ops", "ptr_ops_0000002"); err != nil {
		t.Fatal(err)
	}
	got = envOf(t)(e.mgr.ProfileCredentialFor(ctx, id, e.userA, "ops"))
	if got.Value != "ptr_ops_0000002" || got.Env["PORTAINER_TEAM"] != "b" {
		t.Fatalf("token rotation lost the env: %v", got.Env)
	}
}

func TestConnectionEnvSharedAndRevoke(t *testing.T) {
	e := newEnv(t)
	id := e.insertPortainer()
	in := ConnectionInput{ServerID: id, Shared: true, Token: "ptr_shared_0001",
		Env: map[string]string{"PORTAINER_TEAM": "s", "PORTAINER_URL": "https://shared.example"}}
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
		t.Fatal(err)
	}
	got := envOf(t)(e.mgr.CredentialFor(ctx, id, e.userB))
	if got.Env["PORTAINER_URL"] != "https://shared.example" {
		t.Fatalf("shared env = %v", got.Env)
	}
	if err := e.mgr.RevokeConnection(ctx, e.pool, id, "", "saiu"); err != nil {
		t.Fatal(err)
	}
	var kept bool
	if err := e.pool.QueryRow(ctx, `SELECT env_ciphertext IS NOT NULL FROM upstream_credentials WHERE shared`).Scan(&kept); err != nil || kept {
		t.Fatalf("revoked row kept its env: %v", err)
	}
}
