package admin_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

type fakeRevoker struct{ families []string }

func (f *fakeRevoker) RevokeFamily(_ context.Context, id string) error {
	f.families = append(f.families, id)
	return nil
}

func (h *harness) exec(query string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(ctx, query, args...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) headerServer(slug string, cfg admin.HeaderConfig) string {
	h.t.Helper()
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: slug, URL: "https://" + slug + ".example/mcp", AuthKind: "header", Header: &cfg}); err != nil {
		h.t.Fatal(err)
	}
	var id string
	if err := h.pool.QueryRow(ctx, `SELECT id::text FROM upstream_servers WHERE slug = $1`, slug).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *harness) lastEvent() admin.Event {
	h.t.Helper()
	if len(h.events) == 0 {
		h.t.Fatal("no audit event")
	}
	return h.events[len(h.events)-1]
}

func TestOverviewCountsDenies24h(t *testing.T) {
	h := newHarness(t)
	h.exec(`SELECT create_audit_partition(now()::date)`)
	h.exec(`SELECT create_audit_partition((now() - interval '25 hours')::date)`)
	uid := "00000000-0000-0000-0000-000000000001"
	for _, ago := range []string{"0", "1 hour", "25 hours"} {
		h.exec(`INSERT INTO audit_events (ts, user_id, client_id, server_slug, tool_name, decision)
			VALUES (now() - $1::interval, $2::uuid, 'c', 's', 't', 'deny')`, ago, uid)
	}
	h.server("a")
	h.server("b")
	if err := h.svc.SetStatus(ctx, "b", "disabled"); err != nil {
		t.Fatal(err)
	}
	o, err := h.svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Denies24h != 2 || o.Servers != 1 || o.ServersDisabled != 1 {
		t.Fatalf("overview = %+v", o)
	}
}

func TestExecutionListTruncatesCode(t *testing.T) {
	h := newHarness(t)
	u := h.user("e@example.com")
	long := strings.Repeat("x", 5000)
	h.exec(`INSERT INTO executions (user_id, client_id, kind, code, code_hash) VALUES ($1::uuid, 'c', 'execute', $2, 'h')`, u.ID, long)
	page, err := h.svc.ListExecutions(ctx, admin.ExecutionFilter{}, admin.PageQuery{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	ex := page.Items[0]
	if len(ex.Code) != 2048 || !ex.CodeTruncated || ex.Email != "e@example.com" {
		t.Fatalf("len=%d truncated=%v email=%q", len(ex.Code), ex.CodeTruncated, ex.Email)
	}
	full, _, err := h.svc.GetExecution(ctx, ex.ID)
	if err != nil || full.Code != long || full.CodeTruncated {
		t.Fatalf("detail: len=%d err=%v", len(full.Code), err)
	}
	h.exec(`UPDATE executions SET code = repeat('é', 1500)`) // 3000 bytes, cut must not split a rune
	page, _ = h.svc.ListExecutions(ctx, admin.ExecutionFilter{}, admin.PageQuery{})
	if c := page.Items[0].Code; len(c) > 2048 || strings.ContainsRune(c, '�') {
		t.Fatalf("split rune: len=%d", len(c))
	}
}

func TestRevokeConsentSetsRevokedAt(t *testing.T) {
	h := newHarness(t)
	u := h.user("c@example.com")
	h.server("srv")
	h.exec(`INSERT INTO oauth_clients (client_id, registration_kind, metadata) VALUES ('cli', 'dcr', '{"client_name":"My Agent"}')`)
	h.exec(`INSERT INTO consents (user_id, client_id, server_id, level)
		SELECT $1::uuid, 'cli', id, 'read' FROM upstream_servers WHERE slug = 'srv'`, u.ID)
	page, err := h.svc.ListConsents(ctx, admin.ConsentFilter{UserID: u.ID}, admin.PageQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ClientName != "My Agent" || page.Items[0].ServerSlug != "srv" {
		t.Fatalf("consents = %+v, %v", page, err)
	}
	if err := h.svc.RevokeConsent(ctx, page.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT count(*) FROM consents WHERE revoked_at IS NOT NULL`); n != 1 {
		t.Fatalf("revoked rows = %d", n)
	}
	if ev := h.lastEvent(); ev.Action != "consent.revoke" || ev.Target != page.Items[0].ID {
		t.Fatalf("event = %+v", ev)
	}
	if err := h.svc.RevokeConsent(ctx, page.Items[0].ID); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("second revoke = %v", err)
	}
	clients, err := h.svc.ListClients(ctx, admin.PageQuery{})
	if err != nil || len(clients.Items) != 1 || clients.Items[0].Name != "My Agent" || clients.Items[0].ActiveConsents != 0 {
		t.Fatalf("clients = %+v, %v", clients, err)
	}
}

func TestRevokeGrantCallsRevoker(t *testing.T) {
	h := newHarness(t)
	u := h.user("g@example.com")
	rev := &fakeRevoker{}
	var events []admin.Event
	svc := admin.New(admin.Options{Pool: h.pool, Grants: rev, Audit: func(_ context.Context, _ pgx.Tx, ev admin.Event) error {
		events = append(events, ev)
		return nil
	}})
	h.exec(`INSERT INTO oauth_clients (client_id, registration_kind) VALUES ('cli', 'dcr')`)
	family := "00000000-0000-0000-0000-0000000000f1"
	h.exec(`INSERT INTO oauth_refresh_tokens (token_hash, family_id, client_id, user_id, expires_at)
		VALUES ('t1', $1::uuid, 'cli', $2::uuid, now() + interval '1 day')`, family, u.ID)
	grants, err := svc.ListGrants(ctx, "", admin.PageQuery{})
	if err != nil || len(grants.Items) != 1 || grants.Items[0].FamilyID != family || grants.Items[0].Email != "g@example.com" {
		t.Fatalf("grants = %+v, %v", grants, err)
	}
	if err := svc.RevokeGrant(ctx, family); err != nil {
		t.Fatal(err)
	}
	if len(rev.families) != 1 || rev.families[0] != family {
		t.Fatalf("revoker got %v", rev.families)
	}
	if len(events) != 1 || events[0].Action != "grant.revoke" || events[0].Target != family {
		t.Fatalf("events = %+v", events)
	}
	if err := svc.RevokeGrant(ctx, "00000000-0000-0000-0000-0000000000f2"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown family = %v", err)
	}
}

func TestListConnectionsStatuses(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", admin.HeaderConfig{Name: "Authorization"})
	h.server("plain")
	var ids [4]string
	for i := range ids {
		u := h.user(fmt.Sprintf("u%d@example.com", i))
		ids[i] = u.ID
		if err := h.svc.SetCeiling(ctx, u.ID, "gh", policy.LevelRead); err != nil {
			t.Fatal(err)
		}
	}
	cred := `INSERT INTO upstream_credentials (server_id, user_id, access_ciphertext, key_id, expires_at, status)
		SELECT id, $1::uuid, $2, 'k1', $3, $4 FROM upstream_servers WHERE slug = 'gh'`
	var soon = time.Now().Add(48 * time.Hour)
	h.exec(cred, ids[0], []byte{1}, nil, "active")
	h.exec(cred, ids[1], []byte{1}, soon, "active")
	h.exec(cred, ids[2], nil, nil, "refresh_failed")

	servers, page, err := h.svc.ListConnections(ctx, admin.ConnectionFilter{}, admin.PageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Slug != "gh" {
		t.Fatalf("servers = %+v", servers)
	}
	sc := servers[0]
	if sc.Connected != 1 || sc.Expiring != 1 || sc.RefreshFailed != 1 || sc.NotConnected != 1 || sc.UsingShared != 0 || sc.Shared != nil {
		t.Fatalf("counts = %+v", sc)
	}
	got := map[string]string{}
	for _, c := range page.Items {
		got[c.Email] = c.Status
	}
	want := map[string]string{"u0@example.com": "connected", "u1@example.com": "expiring", "u2@example.com": "refresh_failed", "u3@example.com": "not_connected"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("statuses = %v", got)
	}

	if _, err := h.svc.SetSharedHeaderToken(ctx, "gh", ids[0], "Bearer shared-1234"); err != nil {
		t.Fatal(err)
	}
	servers, page, err = h.svc.ListConnections(ctx, admin.ConnectionFilter{Server: "gh", Status: "shared"}, admin.PageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	sc = servers[0]
	if sc.UsingShared != 1 || sc.NotConnected != 0 || sc.Shared == nil || sc.Shared.Last4 != "1234" || sc.Shared.ConnectedByEmail != "u0@example.com" {
		t.Fatalf("after shared: %+v shared=%+v", sc, sc.Shared)
	}
	if len(page.Items) != 1 || page.Items[0].Email != "u3@example.com" {
		t.Fatalf("shared filter page = %+v", page.Items)
	}
}

func TestRevokeConnectionRequiresReason(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", admin.HeaderConfig{Name: "Authorization"})
	for _, reason := range []string{"", strings.Repeat("r", 501)} {
		var ie *admin.InvalidError
		if err := h.svc.RevokeConnection(ctx, "gh", "", reason); !errors.As(err, &ie) || ie.Field != "reason" {
			t.Fatalf("reason %d chars: %v", len(reason), err)
		}
	}
	if err := h.svc.RevokeConnection(ctx, "gh", "", "Vazou"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("no shared row: %v", err)
	}
}

func TestSharedHeaderTokenAudited(t *testing.T) {
	h := newHarness(t)
	admin1 := h.user("adm@example.com")
	h.headerServer("gh", admin.HeaderConfig{Name: "Authorization"})
	last4, err := h.svc.SetSharedHeaderToken(ctx, "gh", admin1.ID, "Bearer s3cr3t-9999")
	if err != nil || last4 != "9999" {
		t.Fatalf("last4=%q err=%v", last4, err)
	}
	var userNull bool
	var by *string
	if err := h.pool.QueryRow(ctx, `SELECT user_id IS NULL, connected_by::text FROM upstream_credentials WHERE shared`).Scan(&userNull, &by); err != nil {
		t.Fatal(err)
	}
	if !userNull || by == nil || *by != admin1.ID {
		t.Fatalf("shared row: userNull=%v by=%v", userNull, by)
	}
	ev := h.lastEvent()
	if ev.Action != "connection.set_shared" || ev.Target != "gh" || strings.Contains(fmt.Sprint(ev.Details), "s3cr3t") || ev.Details["last4"] != "9999" {
		t.Fatalf("event = %+v", ev)
	}
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "oa", URL: "https://oa.example/mcp", AuthKind: "oauth"}); err != nil {
		t.Fatal(err)
	}
	var ie *admin.InvalidError
	if _, err := h.svc.SetSharedHeaderToken(ctx, "oa", admin1.ID, "tok"); !errors.As(err, &ie) || ie.Field != "auth_kind" {
		t.Fatalf("oauth server: %v", err)
	}
	if _, err := h.svc.SetSharedHeaderToken(ctx, "gh", "", ""); !errors.As(err, &ie) || ie.Field != "token" {
		t.Fatalf("empty token: %v", err)
	}
	if _, err := h.svc.SetSharedHeaderToken(ctx, "gh", "", "cli-token-0001"); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials WHERE shared AND connected_by IS NULL`); n != 1 {
		t.Fatalf("CLI write must leave connected_by NULL, rows=%d", n)
	}
}

func TestRemoveSharedConnection(t *testing.T) {
	h := newHarness(t)
	u := h.user("u@example.com")
	h.headerServer("gh", admin.HeaderConfig{Name: "Authorization"})
	h.exec(`INSERT INTO upstream_credentials (server_id, user_id, access_ciphertext, key_id)
		SELECT id, $1::uuid, '\x01', 'k1' FROM upstream_servers WHERE slug = 'gh'`, u.ID)
	if _, err := h.svc.SetSharedHeaderToken(ctx, "gh", u.ID, "abc-token"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RemoveSharedConnection(ctx, "gh"); err != nil {
		t.Fatal(err)
	}
	if ev := h.lastEvent(); ev.Action != "connection.remove_shared" || ev.Target != "gh" {
		t.Fatalf("event = %+v", ev)
	}
	if h.count(`SELECT count(*) FROM upstream_credentials WHERE shared`) != 0 || h.count(`SELECT count(*) FROM upstream_credentials WHERE NOT shared`) != 1 {
		t.Fatal("shared row not removed, or personal row touched")
	}
	if err := h.svc.RemoveSharedConnection(ctx, "gh"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("second remove = %v", err)
	}
}

func TestRevokeSharedConnection(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", admin.HeaderConfig{Name: "Authorization"})
	if _, err := h.svc.SetSharedHeaderToken(ctx, "gh", "", "abc-token"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RevokeConnection(ctx, "gh", "", "Vazou"); err != nil {
		t.Fatal(err)
	}
	var status string
	var nulls bool
	if err := h.pool.QueryRow(ctx, `SELECT status, access_ciphertext IS NULL FROM upstream_credentials WHERE shared`).Scan(&status, &nulls); err != nil {
		t.Fatal(err)
	}
	if status != "revoked_by_admin" || !nulls {
		t.Fatalf("status=%s nulls=%v", status, nulls)
	}
	ev := h.lastEvent()
	if ev.Action != "connection.revoke" || ev.Details["shared"] != true || ev.Details["reason"] != "Vazou" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestPrefixNotDoubled(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", admin.HeaderConfig{Name: "Authorization"})
	update := func(slug string) error {
		return h.svc.UpdateServer(ctx, slug, admin.ServerUpdate{Header: &admin.HeaderConfig{Name: "Authorization", Prefix: "Bearer "}})
	}
	prefix := func(slug string) string {
		var p string
		if err := h.pool.QueryRow(ctx, `SELECT header_prefix FROM upstream_servers WHERE slug = $1`, slug).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := h.svc.SetSharedHeaderToken(ctx, "gh", "", "Bearer abc"); err != nil {
		t.Fatal(err)
	}
	before := len(h.events)
	if err := update("gh"); !errors.Is(err, admin.ErrPrefixInValue) {
		t.Fatalf("update = %v", err)
	}
	if prefix("gh") != "" || len(h.events) != before {
		t.Fatalf("guard must change nothing: prefix=%q events=%d/%d", prefix("gh"), len(h.events), before)
	}
	if _, err := h.svc.SetSharedHeaderToken(ctx, "gh", "", "abc-token"); err != nil {
		t.Fatal(err)
	}
	if err := update("gh"); err != nil || prefix("gh") != "Bearer " {
		t.Fatalf("clean value: err=%v prefix=%q", err, prefix("gh"))
	}
	h.headerServer("nos", admin.HeaderConfig{Name: "Authorization"})
	if err := update("nos"); err != nil || prefix("nos") != "Bearer " {
		t.Fatalf("no shared row: err=%v", err)
	}
}

func TestPagesAreKeyset(t *testing.T) {
	h := newHarness(t)
	actor := "00000000-0000-0000-0000-0000000000a1"
	for i := 0; i < 3; i++ {
		h.exec(`INSERT INTO admin_events (actor_user_id, action, target, details) VALUES ($1::uuid, 'server.add', $2, '{"k":1}')`, actor, fmt.Sprint("t", i))
	}
	first, err := h.svc.ListAdminEvents(ctx, "", admin.PageQuery{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if first.Items[0].Target != "t2" || first.Items[0].Details["k"] != float64(1) {
		t.Fatalf("order/details: %+v", first.Items[0])
	}
	second, err := h.svc.ListAdminEvents(ctx, "", admin.PageQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].Target != "t0" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	var ie *admin.InvalidError
	if _, err := h.svc.ListAdminEvents(ctx, "", admin.PageQuery{Cursor: "nope"}); !errors.As(err, &ie) || ie.Field != "cursor" {
		t.Fatalf("bad cursor = %v", err)
	}
}

func TestListConnectionsOneRowPerProfile(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", admin.HeaderConfig{Name: "Authorization"})
	u := h.user("multi@example.com")
	if err := h.svc.SetCeiling(ctx, u.ID, "gh", policy.LevelRead); err != nil {
		t.Fatal(err)
	}
	cred := `INSERT INTO upstream_credentials (server_id, user_id, profile, access_ciphertext, key_id, status)
		SELECT id, $1::uuid, $2, $3, 'k1', 'active' FROM upstream_servers WHERE slug = 'gh'`
	for _, profile := range []string{"default", "acme", "globex"} {
		h.exec(cred, u.ID, profile, []byte{1})
	}

	servers, page, err := h.svc.ListConnections(ctx, admin.ConnectionFilter{}, admin.PageQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if servers[0].Connected != 3 || servers[0].NotConnected != 0 {
		t.Fatalf("counts = %+v", servers[0])
	}
	var got []string
	for len(page.Items) > 0 {
		for _, c := range page.Items {
			got = append(got, c.Profile)
		}
		if page.NextCursor == "" {
			break
		}
		if _, page, err = h.svc.ListConnections(ctx, admin.ConnectionFilter{}, admin.PageQuery{Limit: 2, Cursor: page.NextCursor}); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(got) != "[acme default globex]" {
		t.Fatalf("profiles across pages = %v", got)
	}
}
