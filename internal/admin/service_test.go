package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestWriteRunsAuditInSameTransaction(t *testing.T) {
	h := newHarness(t)
	h.audit = func(ctx context.Context, tx pgx.Tx, ev admin.Event) error { return errors.New("audit down") }
	_, err := h.svc.CreateUser(ctx, admin.CreateUserInput{Email: "a@example.com", Password: goodPassword})
	if err == nil {
		t.Fatal("write succeeded although audit failed")
	}
	if n := h.count(`SELECT count(*) FROM users`); n != 0 {
		t.Fatalf("user committed without audit: %d", n)
	}
}

func TestEveryWriteIsAuditedWithoutSecrets(t *testing.T) {
	h := newHarness(t)
	u := h.user("a@example.com")
	h.user("b@example.com")
	if err := h.svc.ResetPassword(ctx, u.ID, "another long password"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetAdmin(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	h.server("srv")
	if err := h.svc.SetOAuthClient(ctx, "srv", "cid", "very-secret"); err == nil {
		t.Fatal("oauth client accepted on auth none server")
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "acct", URL: "https://acct.example/mcp", AuthKind: "oauth"}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetOAuthClient(ctx, "acct", "cid", "very-secret"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetCeiling(ctx, u.ID, "srv", policy.LevelRead); err != nil {
		t.Fatal(err)
	}
	want := []string{"user.create", "user.create", "user.reset_password", "user.set_admin", "server.add", "server.add", "server.set_oauth_client", "ceiling.set"}
	if len(h.events) != len(want) {
		t.Fatalf("events = %d, want %d: %+v", len(h.events), len(want), h.events)
	}
	for i, ev := range h.events {
		if ev.Action != want[i] {
			t.Errorf("event %d action = %q, want %q", i, ev.Action, want[i])
		}
		for k, v := range ev.Details {
			if s, ok := v.(string); ok && (s == "very-secret" || s == goodPassword || s == "another long password") {
				t.Errorf("event %s leaks a secret in details[%s]", ev.Action, k)
			}
		}
	}
	if got := h.events[6].Details["secret_set"]; got != true {
		t.Errorf("secret_set = %v", got)
	}
}

func TestCreateUser(t *testing.T) {
	h := newHarness(t)
	u := h.user("Lucas@Example.com")
	if u.Email != "lucas@example.com" || u.ID == "" {
		t.Fatalf("user = %+v", u)
	}
	_, err := h.svc.CreateUser(ctx, admin.CreateUserInput{Email: "lucas@example.com", Password: goodPassword})
	if !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	var ie *admin.InvalidError
	_, err = h.svc.CreateUser(ctx, admin.CreateUserInput{Email: "b@example.com", Password: "short"})
	if !errors.As(err, &ie) || ie.Field != "password" {
		t.Fatalf("weak password: %v", err)
	}
	users, err := h.svc.ListUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("users = %v, %v", users, err)
	}
	id, err := h.svc.UserIDByEmail(ctx, " LUCAS@example.com")
	if err != nil || id != u.ID {
		t.Fatalf("UserIDByEmail = %q, %v", id, err)
	}
	if _, err := h.svc.UserIDByEmail(ctx, "ghost@example.com"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("ghost: %v", err)
	}
}

func TestSetAdminRefusesLastAdmin(t *testing.T) {
	h := newHarness(t)
	a := h.user("a@example.com")
	b := h.user("b@example.com")
	if err := h.svc.SetAdmin(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetAdmin(ctx, a.ID, false); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("last admin demoted: %v", err)
	}
	if err := h.svc.SetAdmin(ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetAdmin(ctx, a.ID, false); err != nil {
		t.Fatalf("demotion with two admins: %v", err)
	}
	if err := h.svc.SetAdmin(ctx, "00000000-0000-7000-8000-000000000000", true); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if err := h.svc.SetAdmin(ctx, "not-a-uuid", true); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("malformed id: %v", err)
	}
}

func TestAddServerRejectsHostLauncherByDefault(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "local", Transport: "stdio", AuthKind: "none",
		Stdio: &admin.StdioInput{Launcher: "host", Command: "/usr/bin/true"}})
	var ie *admin.InvalidError
	if !errors.Is(err, admin.ErrInvalid) || !errors.As(err, &ie) || ie.Field != "stdio_launcher" {
		t.Fatalf("err = %v", err)
	}
	if n := h.count(`SELECT count(*) FROM upstream_servers`); n != 0 {
		t.Fatalf("server stored: %d", n)
	}
}

func TestAddServerHostLauncherWhenAllowed(t *testing.T) {
	h := newHarness(t)
	svc := admin.New(admin.Options{Pool: h.pool, AllowHostLauncher: true})
	s, err := svc.AddServer(ctx, admin.AddServerInput{Slug: "local", Transport: "stdio", AuthKind: "none",
		Stdio: &admin.StdioInput{Launcher: "host", Command: "/usr/bin/true"}})
	if err != nil || s.Transport != "stdio" {
		t.Fatalf("summary = %+v, %v", s, err)
	}
}

func TestAddStdioDockerSealsEnv(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "dk", Transport: "stdio", AuthKind: "none",
		Stdio: &admin.StdioInput{Image: "ghcr.io/acme/mcp:1", Env: map[string]string{"TOKEN": "env-secret-value"}}})
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := h.pool.QueryRow(ctx, `SELECT stdio_env_ciphertext FROM upstream_servers WHERE slug = 'dk'`).Scan(&sealed); err != nil || len(sealed) == 0 {
		t.Fatalf("sealed = %d bytes, %v", len(sealed), err)
	}
	for _, ev := range h.events {
		if m, _ := ev.Details["env"].(map[string]string); m != nil {
			t.Fatal("env in audit details")
		}
	}
}

func TestAddServerHeaderRequiresHTTPSHelpURL(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "keyed", URL: "https://k.example/mcp", AuthKind: "header",
		Header: &admin.HeaderConfig{Name: "X-Api-Key", HelpURL: "http://docs.example.com"}})
	var ie *admin.InvalidError
	if !errors.As(err, &ie) || ie.Field != "header_help_url" {
		t.Fatalf("err = %v", err)
	}
}

func TestAddServerHeaderConfigRoundTrip(t *testing.T) {
	h := newHarness(t)
	cfg := admin.HeaderConfig{Name: "Authorization", Prefix: "Bearer ", FormatHint: "ghp_", HelpURL: "https://docs.example.com/t"}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "gh", URL: "https://gh.example/mcp", AuthKind: "header", Category: "dev", Header: &cfg}); err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.GetServer(ctx, "gh")
	if err != nil {
		t.Fatal(err)
	}
	if d.Header == nil || *d.Header != cfg || d.Category != "dev" || d.AuthKind != "header" || d.HasSharedConnection {
		t.Fatalf("detail = %+v header=%+v", d, d.Header)
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "plain", URL: "http://127.0.0.1:1/mcp", AuthKind: "none", Header: &cfg}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("header config on auth none: %v", err)
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "bad", URL: "https://b.example/mcp", AuthKind: "header", Header: &admin.HeaderConfig{Name: "Bad Name"}}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("bad header name: %v", err)
	}
}

func TestAddServerNormalizesStaticHeader(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "keyed", URL: "https://k.example/mcp", AuthKind: "static_header"}); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := h.pool.QueryRow(ctx, `SELECT auth_kind FROM upstream_servers WHERE slug = 'keyed'`).Scan(&kind); err != nil || kind != "header" {
		t.Fatalf("stored auth_kind = %q, %v", kind, err)
	}
	// An expand-phase row still holding static_header is reported as header.
	if _, err := h.pool.Exec(ctx, `UPDATE upstream_servers SET auth_kind = 'static_header', static_header_name = 'X-K', static_header_ciphertext = '\x01', header_name = 'X-K' WHERE slug = 'keyed'`); err != nil {
		t.Fatal(err)
	}
	list, err := h.svc.ListServers(ctx)
	if err != nil || len(list) != 1 || list[0].AuthKind != "header" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if d, err := h.svc.GetServer(ctx, "keyed"); err != nil || d.AuthKind != "header" || d.Header == nil || d.Header.Name != "X-K" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
}

func TestHasSharedConnectionCountsLegacyAndSharedRows(t *testing.T) {
	h := newHarness(t)
	h.server("a")
	h.server("b")
	if _, err := h.pool.Exec(ctx, `UPDATE upstream_servers SET auth_kind = 'static_header', static_header_name = 'X-K', static_header_ciphertext = '\x01' WHERE slug = 'a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, shared, access_ciphertext, key_id)
		SELECT id, true, '\x01', 'k1' FROM upstream_servers WHERE slug = 'b'`); err != nil {
		t.Fatal(err)
	}
	list, err := h.svc.ListServers(ctx)
	if err != nil || len(list) != 2 || !list[0].HasSharedConnection || !list[1].HasSharedConnection {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestAddServerRejectsBadCategory(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "s", URL: "http://127.0.0.1:1/mcp", AuthKind: "none", Category: "Dev Tools"})
	var ie *admin.InvalidError
	if !errors.As(err, &ie) || ie.Field != "category" {
		t.Fatalf("err = %v", err)
	}
}

func TestReservedSlugIsInvalid(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "callback", URL: "http://127.0.0.1:1/mcp", AuthKind: "none"})
	var ie *admin.InvalidError
	if !errors.As(err, &ie) || ie.Field != "slug" {
		t.Fatalf("err = %v", err)
	}
}

func TestAddServerRules(t *testing.T) {
	h := newHarness(t)
	h.server("fake")
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "fake", URL: "http://x.example", AuthKind: "none"}); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("duplicate slug: %v", err)
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "bad", URL: "http://x.example", AuthKind: "oauth"}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("http oauth: %v", err)
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "bad", URL: "http://x.example", AuthKind: "none", Transport: "carrier_pigeon"}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("bad transport: %v", err)
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "bad", Transport: "stdio", AuthKind: "oauth", Stdio: &admin.StdioInput{Image: "x/y:1"}}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("stdio with oauth: %v", err)
	}
	s, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "auto", URL: "http://127.0.0.1:9998/mcp", AuthKind: "none", AutoApproveNewTools: true})
	if err != nil || !s.AutoApproveNewTools || s.Transport != "auto" {
		t.Fatalf("summary = %+v, %v", s, err)
	}
}

func TestServerStatusTrustAndUpdate(t *testing.T) {
	h := newHarness(t)
	h.server("fake")
	if err := h.svc.SetStatus(ctx, "fake", "disabled"); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.svc.GetServer(ctx, "fake"); d.Status != "disabled" {
		t.Fatalf("status = %s", d.Status)
	}
	if err := h.svc.SetStatus(ctx, "nope", "active"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := h.svc.SetStatus(ctx, "fake", "weird"); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("bad status: %v", err)
	}
	yes := true
	if _, err := h.svc.SetTrust(ctx, "fake", admin.TrustInput{}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("empty trust: %v", err)
	}
	if _, err := h.svc.SetTrust(ctx, "fake", admin.TrustInput{Annotations: &yes, AutoApproveNewTools: &yes}); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.svc.GetServer(ctx, "fake"); !d.TrustAnnotations || !d.AutoApproveNewTools {
		t.Fatalf("trust = %+v", d.ServerSummary)
	}

	cat := "docs"
	if err := h.svc.UpdateServer(ctx, "fake", admin.ServerUpdate{Category: &cat}); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.svc.GetServer(ctx, "fake"); d.Category != "docs" {
		t.Fatalf("category = %q", d.Category)
	}
	if err := h.svc.UpdateServer(ctx, "fake", admin.ServerUpdate{Header: &admin.HeaderConfig{Name: "X-K"}}); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("header config on auth none: %v", err)
	}
	empty := ""
	if err := h.svc.UpdateServer(ctx, "fake", admin.ServerUpdate{Category: &empty}); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.svc.GetServer(ctx, "fake"); d.Category != "" {
		t.Fatalf("category not cleared: %q", d.Category)
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "keyed", URL: "https://k.example/mcp", AuthKind: "header"}); err != nil {
		t.Fatal(err)
	}
	cfg := admin.HeaderConfig{Name: "X-Api-Key", Prefix: "Token ", HelpURL: "https://docs.example.com"}
	if err := h.svc.UpdateServer(ctx, "keyed", admin.ServerUpdate{Header: &cfg}); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.svc.GetServer(ctx, "keyed"); d.Header == nil || *d.Header != cfg {
		t.Fatalf("header = %+v", d.Header)
	}
	last := h.events[len(h.events)-1]
	if last.Action != "server.update" || last.Target != "keyed" {
		t.Fatalf("last event = %+v", last)
	}
}

func TestCeilings(t *testing.T) {
	h := newHarness(t)
	u := h.user("lucas@example.com")
	h.server("fake")
	if err := h.svc.SetCeiling(ctx, u.ID, "fake", policy.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetCeiling(ctx, u.ID, "fake", policy.LevelRead); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.ListCeilings(ctx, admin.CeilingFilter{Email: "LUCAS@example.com"})
	if err != nil || len(got) != 1 || got[0].Level != "read" || got[0].ServerSlug != "fake" || got[0].UserID != u.ID {
		t.Fatalf("ceilings = %+v, %v", got, err)
	}
	if err := h.svc.SetCeiling(ctx, u.ID, "fake", policy.LevelNone); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.svc.ListCeilings(ctx, admin.CeilingFilter{UserID: u.ID}); len(got) != 0 {
		t.Fatalf("ceiling not removed: %+v", got)
	}
	if err := h.svc.SetCeiling(ctx, "00000000-0000-7000-8000-000000000000", "fake", policy.LevelRead); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if err := h.svc.SetCeiling(ctx, u.ID, "nope", policy.LevelRead); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}
}

func syncTwice(t *testing.T, h *harness, slug string, tools []upstream.Tool) {
	t.Helper()
	h.lister.tools = tools
	if _, err := h.svc.Sync(ctx, slug, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSyncToolsAndLevels(t *testing.T) {
	h := newHarness(t)
	h.server("every")
	syncTwice(t, h, "every", []upstream.Tool{{Name: "get_item", Description: "Get"}})
	h.lister.tools[0].Description = "Get v2"
	rep, err := h.svc.Sync(ctx, "every", "")
	if err != nil || len(rep.Changed) != 1 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	tools, err := h.svc.ListTools(ctx, admin.ToolFilter{Server: "every", Status: "pending_approval"})
	if err != nil || len(tools) != 1 || tools[0].PendingChanges[0] != "description" || len(tools[0].Hash) != 64 {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	d, err := h.svc.GetTool(ctx, "every", "get_item")
	if err != nil || d.Description != "Get v2" || d.InputSchema == "" || d.ApprovedHash == "" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	if _, err := h.svc.GetTool(ctx, "every", "nope"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown tool: %v", err)
	}
	if err := h.svc.SetToolLevel(ctx, "every", "get_item", policy.LevelDestructive); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.svc.GetTool(ctx, "every", "get_item"); d.Level != "destructive" || !d.Overridden {
		t.Fatalf("level = %+v", d.Tool)
	}
	if err := h.svc.SetToolLevel(ctx, "every", "nope", policy.LevelRead); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown tool level: %v", err)
	}
	if err := h.svc.SetToolLevel(ctx, "every", "get_item", policy.LevelNone); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("level none: %v", err)
	}
}

func TestApproveToolStaleHashReturnsCurrentHash(t *testing.T) {
	h := newHarness(t)
	h.server("every")
	syncTwice(t, h, "every", []upstream.Tool{{Name: "get_item", Description: "Get"}})
	h.lister.tools[0].Description = "Get v2"
	if _, err := h.svc.Sync(ctx, "every", ""); err != nil {
		t.Fatal(err)
	}
	var full string
	if err := h.pool.QueryRow(ctx, `SELECT schema_hash FROM tools WHERE name = 'get_item'`).Scan(&full); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.ApproveTool(ctx, "every", "get_item", "000000000000")
	var stale *admin.StaleHashError
	if !errors.As(err, &stale) || stale.Current != full[:12] || !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if n := h.count(`SELECT count(*) FROM tools WHERE status = 'pending_approval'`); n != 1 {
		t.Fatal("stale approval changed the tool")
	}
	got, err := h.svc.ApproveTool(ctx, "every", "get_item", full[:12])
	if err != nil || len(got) != 1 {
		t.Fatalf("approve = %v, %v", got, err)
	}
	if _, err := h.svc.ApproveTool(ctx, "every", "get_item", ""); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("not pending: %v", err)
	}
	if _, err := h.svc.ApproveTool(ctx, "every", "nope", full[:12]); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := h.svc.ApproveTool(ctx, "every", "get_item", "xyz"); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("bad hash: %v", err)
	}
}

func TestApproveAll(t *testing.T) {
	h := newHarness(t)
	h.server("every")
	syncTwice(t, h, "every", []upstream.Tool{{Name: "a", Description: "A"}})
	h.lister.tools = append(h.lister.tools, upstream.Tool{Name: "b", Description: "B"})
	if _, err := h.svc.Sync(ctx, "every", ""); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.ApproveAll(ctx, "every")
	if err != nil || len(got) != 1 || got[0] != "b" {
		t.Fatalf("approved = %v, %v", got, err)
	}
	if got, err := h.svc.ApproveAll(ctx, "every"); err != nil || len(got) != 0 {
		t.Fatalf("second approve = %v, %v", got, err)
	}
}

func TestSyncOAuthNeedsUser(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "acct", URL: "https://acct.example/mcp", AuthKind: "oauth"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Sync(ctx, "acct", "")
	var ie *admin.InvalidError
	if !errors.As(err, &ie) || ie.Field != "as" {
		t.Fatalf("err = %v", err)
	}
	if _, err := h.svc.Sync(ctx, "nope", ""); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}
}

func TestSyncAuditFailureRollsBackTools(t *testing.T) {
	h := newHarness(t)
	h.server("every")
	h.audit = func(context.Context, pgx.Tx, admin.Event) error { return errors.New("audit down") }
	h.lister.tools = []upstream.Tool{{Name: "a", Description: "A"}}
	if _, err := h.svc.Sync(ctx, "every", ""); err == nil {
		t.Fatal("sync succeeded although audit failed")
	}
	if n := h.count(`SELECT count(*) FROM tools`); n != 0 {
		t.Fatalf("tools committed without audit: %d", n)
	}
}

func TestRemoveServerDeletesDependents(t *testing.T) {
	h := newHarness(t)
	h.server("doomed")
	h.server("kept")
	u := h.user("rm@example.com")
	for _, slug := range []string{"doomed", "kept"} {
		for _, q := range []string{
			`INSERT INTO tools (server_id, name, schema_hash, trust_level) SELECT id, 't', 'h', 'read' FROM upstream_servers WHERE slug = $1 AND $2::uuid IS NOT NULL`,
			`INSERT INTO upstream_credentials (server_id, user_id, access_ciphertext, key_id) SELECT id, $2::uuid, '\x00', 'k1' FROM upstream_servers WHERE slug = $1`,
			`INSERT INTO trust_ceilings (user_id, server_id, max_level) SELECT $2::uuid, id, 'read' FROM upstream_servers WHERE slug = $1`,
		} {
			if _, err := h.pool.Exec(ctx, q, slug, u.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	d, err := h.svc.ServerDependents(ctx, "doomed")
	if err != nil || d.Tools != 1 || d.Credentials != 1 {
		t.Fatalf("dependents = %+v, %v", d, err)
	}
	h.events = nil
	if err := h.svc.RemoveServer(ctx, "doomed"); err != nil {
		t.Fatal(err)
	}
	if len(h.events) != 1 || h.events[0].Action != "server.remove" || h.events[0].Target != "doomed" {
		t.Fatalf("events = %+v", h.events)
	}
	for _, table := range []string{"tools", "upstream_credentials", "trust_ceilings"} {
		if n := h.count(`SELECT count(*) FROM ` + table); n != 1 {
			t.Fatalf("%s rows = %d, want 1 (only the kept server)", table, n)
		}
	}
	if n := h.count(`SELECT count(*) FROM upstream_servers WHERE slug = 'kept'`); n != 1 {
		t.Fatal("other server removed")
	}
}

func TestRemoveServerUnknownSlugIsNotFound(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.RemoveServer(ctx, "nope"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := h.svc.ServerDependents(ctx, "nope"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("dependents err = %v", err)
	}
}
