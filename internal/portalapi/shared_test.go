package portalapi_test

import (
	"strings"
	"testing"
)

func TestOnlyAdminCreatesShared(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "read")
	h.ceiling(h.userC, "gh", "read")
	a := h.loginAs(h.emailA)
	body := map[string]any{"token": goodShared, "shared": true, "password": h.passwordA}
	if res := h.put(a, "/portal/api/connections/gh/token", body); res.StatusCode != 403 || errorCode(t, res) != "forbidden" {
		t.Fatal("non-admin wrote shared")
	}
	if res := h.post(a, "/portal/api/connections/gh/oauth", map[string]any{"shared": true, "password": h.passwordA}); res.StatusCode != 403 {
		t.Fatal("non-admin started shared oauth")
	}
	if res := h.do(a, "DELETE", "/portal/api/connections/gh?shared=1", map[string]any{"password": h.passwordA}, reqOpt{}); res.StatusCode != 403 {
		t.Fatal("non-admin removed shared")
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials WHERE shared`); n != 0 {
		t.Fatal("shared row created")
	}

	c := h.loginAs(h.emailC)
	body = map[string]any{"token": goodShared, "shared": true, "password": "wrong password!!"}
	if res := h.put(c, "/portal/api/connections/gh/token", body); res.StatusCode != 403 || errorCode(t, res) != "reauth_required" {
		t.Fatal("password not checked")
	}
	body["password"] = h.adminPassword
	res := h.put(c, "/portal/api/connections/gh/token", body)
	if rb := readAll(res); res.StatusCode != 200 || !strings.Contains(rb, `"last4":"5678"`) || strings.Contains(rb, "good-shared") {
		t.Fatalf("admin: %d %s", res.StatusCode, rb)
	}
	if h.count(`SELECT count(*) FROM upstream_credentials WHERE shared AND user_id IS NULL AND connected_by = $1::uuid`, h.userC) != 1 {
		t.Fatal("row")
	}
	if h.count(`SELECT count(*) FROM admin_events WHERE action = 'connection.set_shared' AND actor_user_id = $1::uuid AND details::text NOT LIKE '%good-shared%'`, h.userC) != 1 {
		t.Fatal("audit")
	}
	if !strings.Contains(readAll(h.get(a, "/portal/api/connections/gh")), `"connection":"shared"`) {
		t.Fatal("A does not see the shared connection")
	}
	// A sees the shared connection but none of its details; the admin gets shared_last4.
	if ab := readAll(h.get(a, "/portal/api/connections/gh")); strings.Contains(ab, "5678") {
		t.Fatalf("non-admin sees shared details: %s", ab)
	}
	if cb := decode(t, h.get(c, "/portal/api/connections/gh")); cb["shared_last4"] != "5678" || cb["shared_available"] != true {
		t.Fatalf("%v", cb)
	}
}

func TestAdminRemovesShared(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", "good-")
	h.ceiling(h.userC, "gh", "read")
	c := h.loginAs(h.emailC)
	h.put(c, "/portal/api/connections/gh/token", map[string]any{"token": goodShared, "shared": true, "password": h.adminPassword})
	if res := h.do(c, "DELETE", "/portal/api/connections/gh?shared=1", map[string]any{"password": "nope nope nope"}, reqOpt{}); res.StatusCode != 403 {
		t.Fatal(res.StatusCode)
	}
	if res := h.do(c, "DELETE", "/portal/api/connections/gh?shared=1", map[string]any{"password": h.adminPassword}, reqOpt{}); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if h.count(`SELECT count(*) FROM upstream_credentials WHERE shared`) != 0 ||
		h.count(`SELECT count(*) FROM admin_events WHERE action = 'connection.remove_shared'`) != 1 {
		t.Fatal("row or audit")
	}
}

func TestAdminSharedOAuthStart(t *testing.T) {
	h := newHarness(t)
	h.server("notion", "oauth")
	h.ceiling(h.userC, "notion", "read")
	c := h.loginAs(h.emailC)
	if res := h.post(c, "/portal/api/connections/notion/oauth", map[string]any{"shared": true, "password": "nope nope nope"}); res.StatusCode != 403 {
		t.Fatal(res.StatusCode)
	}
	m := decode(t, h.post(c, "/portal/api/connections/notion/oauth", map[string]any{"shared": true, "password": h.adminPassword}))
	if u, _ := m["redirect_url"].(string); !strings.HasPrefix(u, publicURL+"/connect/notion?shared=") {
		t.Fatalf("%v", m)
	}
}

func TestPortalShowsConnectionInEffect(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "read")
	h.ceiling(h.userC, "gh", "read")
	a, c := h.loginAs(h.emailA), h.loginAs(h.emailC)
	h.put(c, "/portal/api/connections/gh/token", map[string]any{"token": goodShared, "shared": true, "password": h.adminPassword})
	in := func() string { return decode(t, h.get(a, "/portal/api/connections/gh"))["connection"].(string) }
	if in() != "shared" {
		t.Fatal(in())
	}
	h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken})
	if in() != "personal" {
		t.Fatal(in())
	}
	h.del(a, "/portal/api/connections/gh", nil)
	if in() != "shared" || h.count(`SELECT count(*) FROM upstream_credentials WHERE shared`) != 1 {
		t.Fatal("shared row must survive the personal disconnect")
	}
}
