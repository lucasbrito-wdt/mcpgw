package codemode_test

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const (
	workToken   = "token-work-0123456789"
	homeToken   = "token-home-0123456789"
	sharedToken = "token-shared-0123456789"
	mineToken   = "token-mine-0123456789"
)

// newProfileFixture turns acme into a header server whose credentials come from a real
// upstreamauth.Manager, and adds echo_args plus echo_profile (a tool with its own profile property).
func newProfileFixture(t *testing.T) (*fixture, *upstreamauth.Manager) {
	t.Helper()
	f := newFixture(t, setup{consent: "read"})
	exec(t, f.pool, `UPDATE upstream_servers SET auth_kind = 'header', header_name = 'Authorization', header_prefix = 'Bearer '
		WHERE id = $1::uuid`, f.serverID)
	exec(t, f.pool, `INSERT INTO tools (server_id, name, schema_hash, trust_level, input_schema) VALUES
		($1::uuid, 'echo_args', 'h', 'read', '{"type":"object","properties":{"id":{"type":"string"}}}'),
		($1::uuid, 'echo_profile', 'h', 'read', '{"type":"object","properties":{"profile":{"type":"string"}}}')`, f.serverID)

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	v, err := vault.New(key, "k1")
	must(t, err)
	mgr, err := upstreamauth.New(upstreamauth.Options{
		Pool: f.pool, Vault: v, PublicURL: "https://gw.example.com",
		User: func(*http.Request) (string, bool) { return "", false },
	})
	must(t, err)
	f.svc, err = codemode.New(codemode.Options{
		Pool: f.pool, Sandbox: sharedSandbox, Policy: f.pol, Upstream: upstream.New(), Credentials: mgr,
		Audit: f.rec, PublicURL: "https://gw.example.com/",
	})
	must(t, err)
	return f, mgr
}

func (f *fixture) setProfileToken(mgr *upstreamauth.Manager, profile, token string) {
	f.t.Helper()
	_, err := mgr.SetProfileToken(context.Background(), f.pool, f.serverID, f.subj.UserID, profile, token)
	must(f.t, err)
}

func (f *fixture) lastAuth() string {
	f.up.mu.Lock()
	defer f.up.mu.Unlock()
	return f.up.auths[len(f.up.auths)-1]
}

// grantOther gives the user read access to the auth_kind none server "other" and a get_item tool on it.
func (f *fixture) grantOther() {
	f.t.Helper()
	var otherID string
	must(f.t, f.pool.QueryRow(context.Background(), `SELECT id::text FROM upstream_servers WHERE slug = 'other'`).Scan(&otherID))
	exec(f.t, f.pool, `INSERT INTO tools (server_id, name, schema_hash, trust_level) VALUES ($1::uuid, 'get_item', 'h', 'read')`, otherID)
	exec(f.t, f.pool, `INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid, $2::uuid, 'read')`, f.subj.UserID, otherID)
	exec(f.t, f.pool, `INSERT INTO consents (user_id, client_id, server_id, level) VALUES ($1::uuid, $2, $3::uuid, 'read')`,
		f.subj.UserID, clientID, otherID)
}

func TestSearchDeclaresProfileOnlyForAuthServers(t *testing.T) {
	f, _ := newProfileFixture(t)
	f.grantOther()
	ctx := context.Background()

	res, err := f.svc.Search(ctx, f.subj, "echo")
	must(t, err)
	if !strings.Contains(res.Declarations, "function echo_args(args?: {") || !strings.Contains(res.Declarations, "mcpgw_profile?: string") ||
		strings.Count(res.Declarations, "profile?: string") != 3 { // echo_args: profile; echo_profile: profile + mcpgw_profile
		t.Fatalf("auth server declarations:\n%s", res.Declarations)
	}
	res, err = f.svc.Search(ctx, f.subj, "get_item")
	must(t, err)
	open, _, _ := strings.Cut(strings.SplitN(res.Declarations, "declare namespace other", 2)[1], "}\n")
	if !strings.Contains(res.Declarations, "declare namespace other") || strings.Contains(open, "profile") {
		t.Fatalf("auth none declarations:\n%s", res.Declarations)
	}
}

func TestCallUsesNamedProfile(t *testing.T) {
	f, mgr := newProfileFixture(t)
	f.setProfileToken(mgr, "work", workToken)
	f.setProfileToken(mgr, "home", homeToken)

	for profile, token := range map[string]string{"work": workToken, "home": homeToken} {
		out, err := f.execute(`async () => await tools.acme.echo_args({id: "1", profile: "` + profile + `"})`)
		must(t, err)
		if string(out) != `{"id":"1"}` {
			t.Fatalf("%s: upstream received %s", profile, out)
		}
		if got := f.lastAuth(); got != "Bearer "+token {
			t.Fatalf("%s: upstream auth = %q", profile, got)
		}
	}

	// A tool with its own profile property keeps it; the connection is picked with mcpgw_profile.
	out, err := f.execute(`async () => await tools.acme.echo_profile({profile: "upstream-value", mcpgw_profile: "work"})`)
	must(t, err)
	if string(out) != `{"profile":"upstream-value"}` || f.lastAuth() != "Bearer "+workToken {
		t.Fatalf("collision: out=%s auth=%q", out, f.lastAuth())
	}
}

func TestCallWithoutProfileUsesDefault(t *testing.T) {
	f, mgr := newProfileFixture(t)
	ctx := context.Background()
	f.setProfileToken(mgr, "work", workToken)
	_, err := mgr.SetToken(ctx, f.pool, f.serverID, "", sharedToken, true)
	must(t, err)

	call := `async () => await tools.acme.echo_args({id: "1"})`
	_, err = f.execute(call)
	must(t, err)
	if got := f.lastAuth(); got != "Bearer "+sharedToken {
		t.Fatalf("without a personal default the shared connection is used, got %q", got)
	}

	_, err = mgr.SetToken(ctx, f.pool, f.serverID, f.subj.UserID, mineToken, false)
	must(t, err)
	_, err = f.execute(call)
	must(t, err)
	if got := f.lastAuth(); got != "Bearer "+mineToken {
		t.Fatalf("the personal default wins over shared, got %q", got)
	}
}

func TestUnknownProfileDeniedWithConnectedList(t *testing.T) {
	f, mgr := newProfileFixture(t)
	f.setProfileToken(mgr, "work", workToken)
	f.setProfileToken(mgr, "home", homeToken)

	_, err := f.execute(`async () => await tools.acme.echo_args({id: "1", profile: "globex"})`)
	if err == nil || !strings.Contains(err.Error(), `"globex"`) || !strings.Contains(err.Error(), "home, work") ||
		!strings.Contains(err.Error(), "https://gw.example.com/") {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{`"Bad Name"`, `5`, `null`, `""`} {
		_, err = f.execute(`async () => await tools.acme.echo_args({profile: ` + bad + `})`)
		if err == nil || !strings.Contains(err.Error(), "profile") {
			t.Fatalf("profile %s: err = %v", bad, err)
		}
	}
	ev := f.events()
	if len(ev) != 5 || ev[0].Decision != "deny" || ev[0].Reason != "not_connected" {
		t.Fatalf("events = %+v", ev)
	}
	for _, e := range ev[1:] {
		if e.Decision != "deny" || e.Reason != "invalid_profile" {
			t.Fatalf("events = %+v", ev)
		}
	}
	f.up.mu.Lock()
	defer f.up.mu.Unlock()
	if len(f.up.auths) != 0 {
		t.Fatalf("denied calls reached the upstream: %d", len(f.up.auths))
	}
}

func TestAuditRecordsProfile(t *testing.T) {
	f, mgr := newProfileFixture(t)
	f.grantOther()
	f.setProfileToken(mgr, "work", workToken)
	f.setProfileToken(mgr, "default", mineToken)

	_, err := f.execute(`async () => {
		await tools.acme.echo_args({id: "1", profile: "work"});
		await tools.acme.echo_args({id: "2"});
		await tools.other.get_item({id: "3"});
	}`)
	must(t, err)
	must(t, f.rec.Close(context.Background()))
	rows, err := f.pool.Query(context.Background(), `SELECT server_slug, COALESCE(profile, '<null>'), args_redacted::text
		FROM audit_events ORDER BY ts, id`)
	must(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var server, profile, args string
		must(t, rows.Scan(&server, &profile, &args))
		if strings.Contains(args, "work") {
			t.Fatalf("profile argument reached the audited args: %s", args)
		}
		got = append(got, server+":"+profile)
	}
	if want := "acme:work,acme:default,other:<null>"; strings.Join(got, ",") != want {
		t.Fatalf("audit profiles = %v, want %s", got, want)
	}
}

func TestMissingDefaultListsNamedProfiles(t *testing.T) {
	f, mgr := newProfileFixture(t)
	f.setProfileToken(mgr, "work", workToken)
	f.setProfileToken(mgr, "home", homeToken)

	_, err := f.execute(`async () => await tools.acme.echo_args({id: "1"})`)
	if err == nil || !strings.Contains(err.Error(), "not_connected") || !strings.Contains(err.Error(), "home, work") ||
		!strings.Contains(err.Error(), "profile") {
		t.Fatalf("err = %v", err)
	}
	if ev := f.events(); len(ev) != 1 || ev[0].Decision != "deny" || ev[0].Reason != "not_connected" {
		t.Fatalf("events = %+v", ev)
	}
}
