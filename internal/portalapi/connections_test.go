package portalapi_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestUserTokenWriteOnly(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "read")
	var logs bytes.Buffer
	h.setLogger(slog.New(slog.NewTextHandler(&logs, nil)))

	res := h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken})
	if body := readAll(res); res.StatusCode != 200 || !strings.Contains(body, `"last4":"1234"`) || strings.Contains(body, "good-token") {
		t.Fatal(body)
	}
	body := readAll(h.get(a, "/portal/api/connections/gh"))
	if strings.Contains(body, "good-token") || !strings.Contains(body, `"last4":"1234"`) {
		t.Fatal(body)
	}
	for _, p := range []string{"/portal/api/connections", "/portal/api/me"} {
		if strings.Contains(readAll(h.get(a, p)), "good-token") {
			t.Fatal(p)
		}
	}
	if strings.Contains(logs.String(), "good-token") {
		t.Fatal("token logged")
	}
}

func TestTokenTestRejectedNotStored(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "read")

	res := h.post(a, "/portal/api/connections/gh/token/test", map[string]string{"token": "good-wrong"})
	body := readAll(res)
	if res.StatusCode != 422 || !strings.Contains(body, `"upstream_rejected"`) || !strings.Contains(body, `"status":401`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatalf("stored %d rows", n)
	}

	ok := decode(t, h.post(a, "/portal/api/connections/gh/token/test", map[string]string{"token": goodToken}))
	if fmt.Sprint(ok["tools"]) != "[get_issue create_issue]" {
		t.Fatalf("%v", ok)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatal("test stored a row")
	}

	res = h.post(a, "/portal/api/connections/gh/token/test", map[string]string{"token": "other-token"})
	if res.StatusCode != 422 || !strings.Contains(readAll(res), "good-") {
		t.Fatal("format hint not reported")
	}
}

func TestTokenFormatRejectedOnPut(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "read")
	res := h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": "bad-token-0000"})
	body := readAll(res)
	if res.StatusCode != 422 || !strings.Contains(body, `"token"`) || strings.Contains(body, "bad-token") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatal("stored")
	}
}

func TestPortalCannotTouchOtherUser(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", "good-")
	h.headerServer("lin", "good-")
	h.ceiling(h.userB, "gh", "read")
	h.ceiling(h.userA, "lin", "read")
	b := h.loginAs(h.emailB)
	if res := h.put(b, "/portal/api/connections/gh/token", map[string]string{"token": goodToken}); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	a := h.loginAs(h.emailA)

	type probe struct{ method, path string }
	for _, p := range []probe{{"GET", ""}, {"DELETE", ""}, {"PUT", "/token"}, {"POST", "/token/test"}, {"POST", "/oauth"}} {
		var body any
		if p.method != "GET" && p.method != "DELETE" {
			body = map[string]any{"token": goodToken}
		}
		gh := h.do(a, p.method, "/portal/api/connections/gh"+p.path, body, reqOpt{})
		nope := h.do(a, p.method, "/portal/api/connections/nope"+p.path, body, reqOpt{})
		ghBody, nopeBody := readAll(gh), readAll(nope)
		if gh.StatusCode != 404 || ghBody != nopeBody {
			t.Fatalf("%s %s: %d %q vs %q", p.method, p.path, gh.StatusCode, ghBody, nopeBody)
		}
	}
	var last4 string
	if err := h.row(`SELECT last4 FROM upstream_credentials WHERE user_id = $1::uuid`, h.userB).Scan(&last4); err != nil || last4 != "1234" {
		t.Fatalf("B's row damaged: %q %v", last4, err)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestPortalListsOnlyGrantedServers(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "")
	h.headerServer("lin", "")
	h.headerServer("off", "")
	h.ceiling(h.userA, "gh", "write")
	h.ceiling(h.userA, "off", "read")
	h.ceiling(h.userB, "lin", "read")
	h.exec(`UPDATE upstream_servers SET status = 'disabled' WHERE slug = 'off'`)
	h.tool("gh", "get_issue", "read")
	h.tool("gh", "create_issue", "write")

	m := decode(t, h.get(a, "/portal/api/connections"))
	if got := fmt.Sprint(slugs(t, m)); got != "[gh]" {
		t.Fatal(got)
	}
	item := m["items"].([]any)[0].(map[string]any)
	if item["name"] != "Gh" || item["level"] != "write" || item["tool_count"] != float64(2) ||
		item["status"] != "not_connected" || item["connection"] != "none" || item["auth_kind"] != "header" ||
		item["category"] != "dev" || item["account_label"] != nil || item["last_used_at"] != nil {
		t.Fatalf("%v", item)
	}
	if m["next_cursor"] != nil && m["next_cursor"] != "" {
		t.Fatalf("cursor: %v", m["next_cursor"])
	}
}

func TestPortalConnectionsPagination(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	for i := 0; i < 60; i++ {
		slug := fmt.Sprintf("s%02d", i)
		h.headerServer(slug, "")
		h.ceiling(h.userA, slug, "read")
	}
	var all []string
	cursor := ""
	for page, want := range []int{25, 25, 10} {
		path := "/portal/api/connections?limit=25"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		m := decode(t, h.get(a, path))
		got := slugs(t, m)
		if len(got) != want {
			t.Fatalf("page %d: %d items", page, len(got))
		}
		if sum := m["summary"].(map[string]any); sum["not_connected"] != float64(60) {
			t.Fatalf("summary %v", sum)
		}
		all = append(all, got...)
		cursor, _ = m["next_cursor"].(string)
		if (page < 2) != (cursor != "") {
			t.Fatalf("page %d cursor %q", page, cursor)
		}
	}
	for i, s := range all {
		if s != fmt.Sprintf("s%02d", i) {
			t.Fatalf("order: %v", all)
		}
	}
	if got := slugs(t, decode(t, h.get(a, "/portal/api/connections?q=s1"))); len(got) != 10 { // s10..s19: no server is called plain "s1"
		t.Fatalf("q=s1: %v", got)
	}
	m := decode(t, h.get(a, "/portal/api/connections?status=not_connected&limit=200"))
	if len(slugs(t, m)) != 60 {
		t.Fatal("status filter")
	}
}

func TestPortalFilters(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "")
	h.headerServer("a_b", "")
	h.headerServer("axb", "")
	h.server("open", "none")
	h.server("notion", "oauth")
	h.exec(`UPDATE upstream_servers SET category = 'docs' WHERE slug = 'notion'`)
	h.exec(`UPDATE upstream_servers SET auth_kind = 'static_header' WHERE slug = 'axb'`)
	for _, s := range []string{"gh", "a_b", "axb", "open", "notion"} {
		h.ceiling(h.userA, s, "read")
	}
	list := func(q string) string { return fmt.Sprint(slugs(t, decode(t, h.get(a, "/portal/api/connections?"+q)))) }
	if got := list("q=a_b"); got != "[a_b]" { // _ is literal, not a wildcard
		t.Fatal(got)
	}
	if got := list("q=%25"); got != "[]" {
		t.Fatal(got)
	}
	if got := list("auth=header"); got != "[a_b axb gh]" {
		t.Fatal(got)
	}
	if got := list("auth=oauth"); got != "[notion]" {
		t.Fatal(got)
	}
	if got := list("category=docs"); got != "[notion]" {
		t.Fatal(got)
	}
	if got := list("status=ready"); got != "[open]" {
		t.Fatal(got)
	}
	for _, bad := range []string{"status=nope", "auth=nope", "sort=nope", "limit=x", "cursor=%25%25"} {
		if res := h.get(a, "/portal/api/connections?"+bad); res.StatusCode != 422 {
			t.Fatalf("%s: %d", bad, res.StatusCode)
		}
	}
}

func TestPortalSortByLastUsed(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	for _, s := range []string{"aa", "bb", "cc", "dd"} {
		h.headerServer(s, "")
		h.ceiling(h.userA, s, "read")
		h.put(a, "/portal/api/connections/"+s+"/token", map[string]string{"token": "tok-" + s + "-1234"})
	}
	h.exec(`UPDATE upstream_credentials SET last_used_at = now() - interval '1 hour' WHERE server_id = (SELECT id FROM upstream_servers WHERE slug = 'bb')`)
	h.exec(`UPDATE upstream_credentials SET last_used_at = now() WHERE server_id = (SELECT id FROM upstream_servers WHERE slug = 'dd')`)
	var all []string
	cursor := ""
	for i := 0; i < 4; i++ {
		path := "/portal/api/connections?sort=last_used&limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		m := decode(t, h.get(a, path))
		all = append(all, slugs(t, m)...)
		cursor, _ = m["next_cursor"].(string)
	}
	if fmt.Sprint(all) != "[dd bb aa cc]" || cursor != "" {
		t.Fatalf("%v cursor=%q", all, cursor)
	}
}

func TestPortalStatusOfRow(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "")
	h.server("notion", "oauth")
	h.ceiling(h.userA, "gh", "read")
	h.ceiling(h.userA, "notion", "read")
	h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken})
	status := func(slug string) string {
		return decode(t, h.get(a, "/portal/api/connections/"+slug))["status"].(string)
	}
	if s := status("gh"); s != "connected" {
		t.Fatal(s)
	}
	h.exec(`UPDATE upstream_credentials SET expires_at = now() + interval '2 days'`)
	if s := status("gh"); s != "expiring" {
		t.Fatal(s)
	}
	h.exec(`UPDATE upstream_credentials SET expires_at = now() - interval '1 day'`)
	if s := status("gh"); s != "expired" {
		t.Fatal(s)
	}
	// An OAuth grant with a refresh token renews itself: never expired.
	h.exec(`UPDATE upstream_credentials SET refresh_ciphertext = '\x01'::bytea`)
	if s := status("gh"); s != "connected" {
		t.Fatal(s)
	}
	h.exec(`UPDATE upstream_credentials SET status = 'refresh_failed', access_ciphertext = NULL, refresh_ciphertext = NULL`)
	if s := status("gh"); s != "refresh_failed" {
		t.Fatal(s)
	}
	if s := status("notion"); s != "not_connected" {
		t.Fatal(s)
	}
}

func TestBulkDisconnectLimit(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "")
	h.headerServer("lin", "")
	h.ceiling(h.userA, "gh", "read")
	h.ceiling(h.userB, "lin", "read")
	h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken})
	h.exec(`INSERT INTO upstream_credentials (server_id, user_id, shared, access_ciphertext, key_id, last4, status)
		VALUES ((SELECT id FROM upstream_servers WHERE slug = 'lin'), $1::uuid, false, '\x01', 'k1', '9999', 'active')`, h.userB)
	h.exec(`INSERT INTO upstream_credentials (server_id, user_id, shared, access_ciphertext, key_id, last4, status)
		VALUES ((SELECT id FROM upstream_servers WHERE slug = 'gh'), NULL, true, '\x01', 'k1', '8888', 'active')`)

	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("x%d", i)
	}
	if res := h.post(a, "/portal/api/connections/disconnect", map[string]any{"slugs": many}); res.StatusCode != 422 {
		t.Fatal(res.StatusCode)
	}
	if res := h.post(a, "/portal/api/connections/disconnect", map[string]any{"slugs": []string{}}); res.StatusCode != 422 {
		t.Fatal(res.StatusCode)
	}
	m := decode(t, h.post(a, "/portal/api/connections/disconnect", map[string]any{"slugs": []string{"gh", "lin", "nope", "gh"}}))
	if fmt.Sprint(m["disconnected"]) != "[gh]" || fmt.Sprint(m["skipped"]) != "[lin nope]" {
		t.Fatalf("%v", m)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials WHERE user_id = $1::uuid`, h.userA); n != 0 {
		t.Fatal("A's row kept")
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials WHERE user_id = $1::uuid`, h.userB); n != 1 {
		t.Fatal("B's row deleted")
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials WHERE shared`); n != 1 {
		t.Fatal("shared row deleted by bulk disconnect")
	}
}

func TestDisconnectOwnConnection(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "")
	h.ceiling(h.userA, "gh", "read")
	h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken})
	if res := h.del(a, "/portal/api/connections/gh", nil); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatal("row kept")
	}
	if res := h.del(a, "/portal/api/connections/gh", nil); res.StatusCode != 204 {
		t.Fatal("second disconnect is idempotent", res.StatusCode)
	}
}

func TestOAuthStart(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.server("notion", "oauth")
	h.headerServer("gh", "")
	h.ceiling(h.userA, "notion", "read")
	h.ceiling(h.userA, "gh", "read")
	m := decode(t, h.post(a, "/portal/api/connections/notion/oauth", map[string]any{}))
	if m["redirect_url"] != publicURL+"/connect/notion" {
		t.Fatalf("%v", m)
	}
	if res := h.post(a, "/portal/api/connections/gh/oauth", map[string]any{}); res.StatusCode != 404 {
		t.Fatal("oauth start on a header server:", res.StatusCode)
	}
}

func TestPortalDetail(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "write")
	h.tool("gh", "get_issue", "read")
	h.tool("gh", "create_issue", "write")
	h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken})
	m := decode(t, h.get(a, "/portal/api/connections/gh"))
	if m["name"] != "Gh" || m["help_url"] != "https://up.example.com/tokens" || m["format_hint"] != "good-" ||
		m["shared_available"] != false || m["revoked_reason"] != nil || m["expires_at"] != nil {
		t.Fatalf("%v", m)
	}
	if _, ok := m["shared_last4"]; ok {
		t.Fatal("shared_last4 leaked to a non-admin")
	}
	if fmt.Sprint(m["tools"]) != "[map[level:write name:create_issue] map[level:read name:get_issue]]" {
		t.Fatalf("tools %v", m["tools"])
	}
	tl := m["timeline"].([]any)
	if len(tl) == 0 || tl[0].(map[string]any)["event"] != "connected" {
		t.Fatalf("timeline %v", tl)
	}
}
