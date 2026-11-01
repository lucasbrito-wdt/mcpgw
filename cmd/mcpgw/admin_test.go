package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

type adminHarness struct {
	t   *testing.T
	env *adminEnv
	out *bytes.Buffer
}

func newAdminHarness(t *testing.T, vars map[string]string) *adminHarness {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	out := &bytes.Buffer{}
	h := &adminHarness{t: t, out: out}
	h.env = &adminEnv{
		pool: storetest.New(t), cfg: &config.Config{PublicURL: "http://localhost:8787", MasterKey: key},
		out: out, in: strings.NewReader(""), getenv: func(k string) string { return vars[k] },
	}
	return h
}

func (h *adminHarness) run(args string) (string, error) {
	h.t.Helper()
	h.out.Reset()
	err := adminDispatch(context.Background(), h.env, strings.Fields(args))
	return h.out.String(), err
}

func (h *adminHarness) mustRun(args string) string {
	h.t.Helper()
	out, err := h.run(args)
	if err != nil {
		h.t.Fatalf("%s: %v", args, err)
	}
	return out
}

func TestAdminWorkflow(t *testing.T) {
	h := newAdminHarness(t, map[string]string{
		envAdminPassword: "a long enough password", "HDR": "secret-value", "CLIENT_SECRET": "cs-1",
	})

	h.mustRun("user create --email Lucas@Example.com")
	if _, err := h.run("user create --email lucas@example.com"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate user: %v", err)
	}
	if out := h.mustRun("user list"); !strings.Contains(out, "lucas@example.com") {
		t.Fatalf("user list = %q", out)
	}

	h.mustRun("server add --slug fake --url http://127.0.0.1:9999/mcp --auth none")
	h.mustRun("server add --slug keyed --url https://keyed.example/mcp --auth static_header")
	h.mustRun("server add --slug acct --url https://acct.example/mcp --auth oauth")
	if _, err := h.run("server add --slug fake --url http://x.example --auth none"); err == nil {
		t.Fatal("duplicate slug accepted")
	}
	if _, err := h.run("server add --slug bad --url http://x.example --auth oauth"); err == nil {
		t.Fatal("http oauth accepted")
	}

	h.mustRun("server set-header --slug keyed --name X-Api-Key --value-env HDR")
	if _, err := h.run("server set-header --slug fake --name X-Api-Key --value-env HDR"); err == nil {
		t.Fatal("set-header on auth none accepted")
	}
	if _, err := h.run("server set-header --slug keyed --name X-Api-Key --value-env MISSING"); err == nil {
		t.Fatal("empty env var accepted")
	}
	h.mustRun("server set-oauth-client --slug acct --client-id cid --secret-env CLIENT_SECRET")
	var sealed []byte
	if err := h.env.pool.QueryRow(context.Background(), `SELECT access_ciphertext FROM upstream_credentials WHERE shared AND server_id = (SELECT id FROM upstream_servers WHERE slug = 'keyed')`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("secret-value")) {
		t.Fatal("static header not sealed")
	}

	h.mustRun("server disable --slug fake")
	if out := h.mustRun("server list"); !strings.Contains(out, "disabled") {
		t.Fatalf("server list = %q", out)
	}
	h.mustRun("server enable --slug fake")
	if _, err := h.run("server enable --slug nope"); err == nil {
		t.Fatal("enable of unknown server accepted")
	}

	h.mustRun("ceiling set --email lucas@example.com --slug fake --level write")
	h.mustRun("ceiling set --email lucas@example.com --slug fake --level read")
	if out := h.mustRun("ceiling list --email lucas@example.com"); !strings.Contains(out, "read") || strings.Contains(out, "write") {
		t.Fatalf("ceiling list = %q", out)
	}
	h.mustRun("ceiling set --email lucas@example.com --slug fake --level none")
	if out := h.mustRun("ceiling list"); !strings.Contains(out, "No records") {
		t.Fatalf("ceiling not removed: %q", out)
	}
	if _, err := h.run("ceiling set --email ghost@example.com --slug fake --level read"); err == nil {
		t.Fatal("unknown user accepted")
	}

	_, err := h.env.pool.Exec(context.Background(), `INSERT INTO tools (server_id, name, schema_hash, trust_level)
		SELECT id, 'do_thing', 'h', 'read' FROM upstream_servers WHERE slug = 'fake'`)
	if err != nil {
		t.Fatal(err)
	}
	h.mustRun("tool level --slug fake --tool do_thing --level destructive")
	if out := h.mustRun("tool list --slug fake"); !strings.Contains(out, "destructive") || !strings.Contains(out, "manual") {
		t.Fatalf("tool list = %q", out)
	}
	if _, err := h.run("tool level --slug fake --tool missing --level read"); err == nil {
		t.Fatal("unknown tool accepted")
	}
	if _, err := h.run("tool level --slug fake --tool do_thing --level none"); err == nil {
		t.Fatal("level none accepted for a tool")
	}

	if _, err := h.run("catalog sync --slug acct"); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("oauth sync without --as: %v", err)
	}
}

func TestUserCreatePasswordSources(t *testing.T) {
	h := newAdminHarness(t, nil)
	h.env.in = strings.NewReader("short\n")
	if _, err := h.run("user create --email a@example.com"); err == nil || !strings.Contains(err.Error(), "weak password") {
		t.Fatalf("weak password: %v", err)
	}
	h.env.in = strings.NewReader("a long enough password\n")
	h.mustRun("user create --email a@example.com")
	h.env.in = strings.NewReader("")
	if _, err := h.run("user create --email b@example.com"); err == nil {
		t.Fatal("missing password accepted")
	}
}

func TestServerAddTransports(t *testing.T) {
	h := newAdminHarness(t, map[string]string{"GH_TOKEN": "ghp-sealed-value"})
	ctx := context.Background()

	h.mustRun("server add --slug legacy --url https://legacy.example/sse --auth none --transport sse")
	h.mustRun("server add --slug fs --transport stdio --auth none --docker-image mcp/filesystem:1 --arg /data --arg --ro --env TOKEN=GH_TOKEN")
	h.mustRun("server add --slug local --transport stdio --auth none --host-process --command /usr/local/bin/mcp-local")

	var id, url, launcher, image string
	var args []string
	var sealed []byte
	err := h.env.pool.QueryRow(ctx, `SELECT id::text, url, stdio_launcher, docker_image, stdio_args, stdio_env_ciphertext
		FROM upstream_servers WHERE slug = 'fs'`).Scan(&id, &url, &launcher, &image, &args, &sealed)
	if err != nil {
		t.Fatal(err)
	}
	if url != "stdio://"+id || launcher != "docker" || image != "mcp/filesystem:1" || strings.Join(args, " ") != "/data --ro" {
		t.Fatalf("row: %s %s %s %v", url, launcher, image, args)
	}
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("ghp-sealed-value")) {
		t.Fatal("stdio environment missing or stored in clear")
	}
	var hostSealed []byte
	if err := h.env.pool.QueryRow(ctx, `SELECT stdio_env_ciphertext FROM upstream_servers WHERE slug = 'local'`).Scan(&hostSealed); err != nil || hostSealed != nil {
		t.Fatalf("host row: %v %v", hostSealed, err)
	}
	if out := h.mustRun("server list"); !strings.Contains(out, "TRANSPORT") || !strings.Contains(out, "sse") || !strings.Contains(out, "stdio") {
		t.Fatalf("server list = %q", out)
	}

	rejected := []string{
		"server add --slug a --transport stdio --auth none --host-process --command mcp-local",
		"server add --slug b --transport stdio --auth oauth --docker-image x",
		"server add --slug c --transport stdio --auth none",
		"server add --slug d --transport stdio --auth none --docker-image x --url http://x.example",
		"server add --slug e --transport stdio --auth none --docker-image --privileged",
		"server add --slug f --transport stdio --auth none --docker-image x --env DOCKER_HOST=GH_TOKEN",
		"server add --slug g --transport stdio --auth none --docker-image x --env TOKEN=MISSING",
		"server add --slug h --transport stdio --auth none --host-process --command /bin/x --network",
		"server add --slug i --url https://x.example --auth none --docker-image x",
		"server add --slug j --url https://x.example --auth none --transport websocket",
	}
	for _, args := range rejected {
		if _, err := h.run(args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
	var n int
	if err := h.env.pool.QueryRow(ctx, `SELECT count(*) FROM upstream_servers`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("rows = %d (%v)", n, err)
	}
}

func TestServerAddCredentialEnv(t *testing.T) {
	h := newAdminHarness(t, map[string]string{"PORTAINER_URL_SRC": "https://manager.example"})
	h.mustRun("server add --slug portainer --transport stdio --docker-image portainer/portainer-mcp:2.45.3 --network " +
		"--env PORTAINER_URL=PORTAINER_URL_SRC --credential-env PORTAINER_API_KEY")
	var authKind, credentialEnv string
	var network bool
	err := h.env.pool.QueryRow(context.Background(), `SELECT auth_kind, credential_env, stdio_network
		FROM upstream_servers WHERE slug = 'portainer'`).Scan(&authKind, &credentialEnv, &network)
	if err != nil || authKind != "header" || credentialEnv != "PORTAINER_API_KEY" || !network {
		t.Fatalf("row: %s %s %v %v", authKind, credentialEnv, network, err)
	}
	for _, args := range []string{
		"server add --slug a --transport stdio --auth none --docker-image x --credential-env API_KEY",
		"server add --slug b --transport stdio --docker-image x --credential-env api_key",
		"server add --slug c --transport stdio --docker-image x --env PORTAINER_URL=PORTAINER_URL_SRC --credential-env PORTAINER_URL",
		"server add --slug d --url https://x.example --auth header --credential-env API_KEY",
	} {
		if _, err := h.run(args); err == nil {
			t.Errorf("accepted: %s", args)
		}
	}
}

// staticLister serves a fixed tool list as catalog.Lister.
type staticLister struct{ tools []upstream.Tool }

func (l *staticLister) ListTools(context.Context, string, upstream.Credential) ([]upstream.Tool, upstream.CacheHint, error) {
	return l.tools, upstream.CacheHint{}, nil
}

// toolListRow returns the columns of tool in a `tool list` table: server, tool, level, origin,
// status, changes, hash.
func toolListRow(table, tool string) []string {
	for _, line := range strings.Split(table, "\n") {
		if f := strings.Fields(line); len(f) == 7 && f[1] == tool {
			return f
		}
	}
	return nil
}

func hasToolState(table, tool, level, status, changes string) bool {
	f := toolListRow(table, tool)
	return f != nil && (level == "" || f[2] == level) && f[4] == status && f[5] == changes
}

func TestAdminCatalogTrust(t *testing.T) {
	h := newAdminHarness(t, nil)
	readOnly := upstream.Annotations{ReadOnlyHint: true}
	lister := &staticLister{tools: []upstream.Tool{
		{Name: "trigger-log-messages", Description: "Emit logs", Annotations: readOnly},
		{Name: "get_item", Description: "Get"},
	}}
	h.env.lister = lister
	h.mustRun("server add --slug every --url http://127.0.0.1:9999/mcp --auth none")
	h.mustRun("server add --slug auto --url http://127.0.0.1:9998/mcp --auth none --auto-approve-new-tools")

	if out := h.mustRun("catalog sync --slug every"); !strings.Contains(out, "first sync") {
		t.Fatalf("first sync output = %q", out)
	}
	if out := h.mustRun("tool list --slug every"); !hasToolState(out, "trigger-log-messages", "write", "active", "-") {
		t.Fatalf("untrusted readOnlyHint lowered the level:\n%s", out)
	}

	out := h.mustRun("server set-trust --slug every --annotations=true")
	if !strings.Contains(out, "1 tools with recalculated level") {
		t.Fatalf("set-trust output = %q", out)
	}
	if out := h.mustRun("tool list --slug every"); !hasToolState(out, "trigger-log-messages", "read", "active", "-") {
		t.Fatalf("trusted readOnlyHint ignored:\n%s", out)
	}
	if _, err := h.run("server set-trust --slug every"); err == nil {
		t.Fatal("set-trust without settings accepted")
	}

	lister.tools[1].Description = "Get v2"
	lister.tools = append(lister.tools, upstream.Tool{Name: "create_item", Description: "Create"})
	out = h.mustRun("catalog sync --slug every")
	if !strings.Contains(out, "2 awaiting approval") || !strings.Contains(out, "changed: get_item (description)") {
		t.Fatalf("sync output = %q", out)
	}
	list := h.mustRun("tool list --slug every")
	if !hasToolState(list, "get_item", "read", "pending_approval", "description") ||
		!hasToolState(list, "create_item", "write", "pending_approval", "new") {
		t.Fatalf("tool list = %q", list)
	}

	if _, err := h.run("tool approve --slug every"); err == nil {
		t.Fatal("approve without --tool or --all accepted")
	}
	if _, err := h.run("tool approve --slug every --tool trigger-log-messages"); err == nil {
		t.Fatal("approving an active tool accepted")
	}
	h.mustRun("tool approve --slug every --tool get_item")
	if out := h.mustRun("tool approve --slug every --all"); !strings.Contains(out, "create_item") {
		t.Fatalf("approve all output = %q", out)
	}
	if list := h.mustRun("tool list --slug every"); strings.Contains(list, "pending_approval") {
		t.Fatalf("still pending after approve:\n%s", list)
	}

	h.mustRun("catalog sync --slug auto")
	lister.tools = append(lister.tools, upstream.Tool{Name: "update_item", Description: "Update"})
	h.mustRun("catalog sync --slug auto")
	if list := h.mustRun("tool list --slug auto"); !hasToolState(list, "update_item", "write", "active", "-") {
		t.Fatalf("auto-approve ignored:\n%s", list)
	}
	h.mustRun("server set-trust --slug auto --auto-approve-new-tools=false")
	lister.tools = append(lister.tools, upstream.Tool{Name: "patch_item", Description: "Patch"})
	h.mustRun("catalog sync --slug auto")
	if list := h.mustRun("tool list --slug auto"); !hasToolState(list, "patch_item", "", "pending_approval", "new") {
		t.Fatalf("auto-approve not turned off:\n%s", list)
	}
}

func TestAdminToolShowAndApproveHash(t *testing.T) {
	h := newAdminHarness(t, nil)
	lister := &staticLister{tools: []upstream.Tool{{Name: "get_item", Description: "Get"}}}
	h.env.lister = lister
	h.mustRun("server add --slug every --url http://127.0.0.1:9999/mcp --auth none")
	h.mustRun("catalog sync --slug every")
	lister.tools[0].Description = "Get v2 reviewed"
	h.mustRun("catalog sync --slug every")

	show := h.mustRun("tool show --slug every --tool get_item")
	for _, want := range []string{"Status: pending_approval", "Pending changes: description", "Get v2 reviewed", "Input schema:", "Annotations:"} {
		if !strings.Contains(show, want) {
			t.Fatalf("tool show missing %q:\n%s", want, show)
		}
	}
	if _, err := h.run("tool show --slug every --tool nope"); err == nil {
		t.Fatal("tool show of an unknown tool accepted")
	}

	hash := toolListRow(h.mustRun("tool list --slug every"), "get_item")[6]
	if len(hash) != 12 || !strings.Contains(show, "Hash: "+hash) {
		t.Fatalf("short hash %q not a prefix of shown hash:\n%s", hash, show)
	}
	for name, args := range map[string]string{
		"hash with --all": "tool approve --slug every --all --hash " + hash,
		"too short":       "tool approve --slug every --tool get_item --hash " + hash[:8],
		"not hex":         "tool approve --slug every --tool get_item --hash zzzzzzzzzzzz",
		"stale hash":      "tool approve --slug every --tool get_item --hash 000000000000",
		"unknown tool":    "tool approve --slug every --tool nope --hash " + hash,
		"non-hex suffix":  "tool approve --slug every --tool get_item --hash " + hash[:11] + "x",
	} {
		if _, err := h.run(args); err == nil {
			t.Errorf("%s: approve accepted", name)
		}
	}
	if list := h.mustRun("tool list --slug every"); !hasToolState(list, "get_item", "", "pending_approval", "description") {
		t.Fatalf("refused approve changed the tool:\n%s", list)
	}
	h.mustRun("tool approve --slug every --tool get_item --hash " + strings.ToUpper(hash))
	if list := h.mustRun("tool list --slug every"); !hasToolState(list, "get_item", "", "active", "-") {
		t.Fatalf("hash approve did not activate:\n%s", list)
	}
}

func TestAdminServerRemoveRequiresYes(t *testing.T) {
	h := newAdminHarness(t, nil)
	h.mustRun("server add --slug gone --url http://127.0.0.1:9999/mcp --auth none")
	out, err := h.run("server remove --slug gone")
	if err == nil || !strings.Contains(err.Error(), "--yes") || !strings.Contains(out, "gone") {
		t.Fatalf("remove without --yes: out=%q err=%v", out, err)
	}
	if !strings.Contains(h.mustRun("server list"), "gone") {
		t.Fatal("server removed without --yes")
	}
	if out := h.mustRun("server remove --slug gone --yes"); !strings.Contains(out, "Server gone removed.") {
		t.Fatalf("out = %q", out)
	}
	if _, err := h.run("server remove --slug gone --yes"); err == nil {
		t.Fatal("remove of unknown server accepted")
	}
}
