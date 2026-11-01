package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestServerSetConnectionEnv(t *testing.T) {
	h := newAdminHarness(t, map[string]string{"PORTAINER_URL_SRC": "https://manager.example"})
	h.mustRun("server add --slug portainer --transport stdio --docker-image portainer/portainer-mcp:2.45.3 --network " +
		"--env PORTAINER_URL=PORTAINER_URL_SRC --credential-env PORTAINER_API_KEY")
	connectionEnv := func() []string {
		var names []string
		if err := h.env.pool.QueryRow(context.Background(), `SELECT connection_env FROM upstream_servers WHERE slug = 'portainer'`).
			Scan(&names); err != nil {
			t.Fatal(err)
		}
		return names
	}
	h.mustRun("server set-connection-env --slug portainer --name PORTAINER_URL")
	if got := connectionEnv(); !slices.Equal(got, []string{"PORTAINER_URL"}) {
		t.Fatalf("connection_env = %v", got)
	}
	for _, args := range []string{
		"server set-connection-env --slug portainer",
		"server set-connection-env --slug portainer --name A --clear",
		"server set-connection-env --slug portainer --name PORTAINER_API_KEY",
		"server set-connection-env --slug missing --name A",
	} {
		if _, err := h.run(args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
	h.mustRun("server set-connection-env --slug portainer --clear")
	if got := connectionEnv(); len(got) != 0 {
		t.Fatalf("not cleared: %v", got)
	}
}

func TestServerSetConnectionURL(t *testing.T) {
	h := newAdminHarness(t, nil)
	h.mustRun("server add --slug kafka --url https://kafka.example/mcp --auth header")
	enabled := func() bool {
		var on bool
		if err := h.env.pool.QueryRow(context.Background(), `SELECT connection_url FROM upstream_servers WHERE slug = 'kafka'`).
			Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}
	h.mustRun("server set-connection-url --slug kafka")
	if !enabled() {
		t.Fatal("not enabled")
	}
	for _, args := range []string{"server set-connection-url", "server set-connection-url --slug missing"} {
		if _, err := h.run(args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
	h.mustRun("server set-connection-url --slug kafka --off")
	if enabled() {
		t.Fatal("not disabled")
	}
}

func TestServerSetEnv(t *testing.T) {
	h := newAdminHarness(t, map[string]string{"URL_SRC": "https://manager.example", "TLS_SRC": "1", "EMPTY_SRC": ""})
	h.mustRun("server add --slug portainer --transport stdio --docker-image portainer/portainer-mcp:2.45.3 --network " +
		"--env PORTAINER_URL=URL_SRC --env PORTAINER_MCP_TRANSPORT=TLS_SRC --credential-env PORTAINER_API_KEY")
	var before []byte
	read := func() []byte {
		var sealed []byte
		if err := h.env.pool.QueryRow(context.Background(), `SELECT stdio_env_ciphertext FROM upstream_servers WHERE slug = 'portainer'`).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	before = read()
	out := h.mustRun("server set-env --slug portainer --env PORTAINER_TLS_VERIFY=TLS_SRC --unset PORTAINER_MCP_TRANSPORT")
	if strings.Contains(out, "manager.example") || bytes.Equal(before, read()) {
		t.Fatalf("not applied or leaked: %q", out)
	}
	d, err := h.env.service().GetServer(context.Background(), "portainer")
	if err != nil || !slices.Equal(d.StdioEnvNames, []string{"PORTAINER_TLS_VERIFY", "PORTAINER_URL"}) {
		t.Fatalf("names = %v, %v", d.StdioEnvNames, err)
	}
	for _, args := range []string{
		"server set-env --slug portainer",
		"server set-env --env A=TLS_SRC",
		"server set-env --slug portainer --env A=MISSING_SRC",
		"server set-env --slug portainer --env A=EMPTY_SRC",
		"server set-env --slug portainer --env A",
		"server set-env --slug portainer --env A=TLS_SRC --env A=TLS_SRC",
		"server set-env --slug portainer --env PORTAINER_API_KEY=TLS_SRC",
		"server set-env --slug missing --env A=TLS_SRC",
	} {
		if _, err := h.run(args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
}

func TestServerSetHeaderFormatOnOAuthServer(t *testing.T) {
	h := newAdminHarness(t, nil)
	ctx := context.Background()
	h.mustRun("server add --slug fc --url https://fc.example/mcp --auth oauth")
	header := func() (name *string, hint *string) {
		if err := h.env.pool.QueryRow(ctx, `SELECT header_name, header_format_hint FROM upstream_servers WHERE slug = 'fc'`).Scan(&name, &hint); err != nil {
			t.Fatal(err)
		}
		return name, hint
	}
	h.mustRun("server set-header-format --slug fc --name x-fc-key --format-hint fc-")
	if name, hint := header(); name == nil || *name != "x-fc-key" || hint == nil || *hint != "fc-" {
		t.Fatalf("header = %v %v", name, hint)
	}
	for _, args := range []string{
		"server set-header-format --slug fc --clear --name x-fc-key",
		"server set-header-format --clear",
		"server set-header-format --slug acct --clear",
	} {
		if _, err := h.run(args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
	_, err := h.env.pool.Exec(ctx, `INSERT INTO users (email, password_hash) VALUES ('t@example.com', 'x')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.env.pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, access_ciphertext, key_id, method)
		SELECT s.id, u.id, '\x00', 'k1', 'token' FROM upstream_servers s, users u WHERE s.slug = 'fc' AND u.email = 't@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.run("server set-header-format --slug fc --clear"); err == nil || !strings.Contains(err.Error(), "pasted token") {
		t.Fatalf("clear with a token connection: %v", err)
	}
	if _, err := h.env.pool.Exec(ctx, `DELETE FROM upstream_credentials`); err != nil {
		t.Fatal(err)
	}
	h.mustRun("server set-header-format --slug fc --clear")
	if name, hint := header(); name != nil || hint != nil {
		t.Fatalf("not cleared: %v %v", name, hint)
	}
}
