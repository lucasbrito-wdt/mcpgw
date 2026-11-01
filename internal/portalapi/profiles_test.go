package portalapi_test

import (
	"fmt"
	"strings"
	"testing"
)

type profileRow struct{ name, status, last4 string }

func profilesOf(t *testing.T, item any) []profileRow {
	t.Helper()
	var out []profileRow
	for _, p := range item.(map[string]any)["profiles"].([]any) {
		m := p.(map[string]any)
		last4, _ := m["last4"].(string)
		out = append(out, profileRow{m["profile"].(string), m["status"].(string), last4})
	}
	return out
}

func TestPortalListsConnectionsPerProfile(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("cf", "good-")
	h.ceiling(h.userA, "cf", "read")

	if got := profilesOf(t, decode(t, h.get(a, "/portal/api/connections"))["items"].([]any)[0]); len(got) != 0 {
		t.Fatalf("profiles before connecting: %v", got)
	}
	for _, path := range []string{"/token", "/token?profile=acme", "/token?profile=globex"} {
		if res := h.put(a, "/portal/api/connections/cf"+path, map[string]string{"token": goodToken}); res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, res.StatusCode, readAll(res))
		}
	}

	m := decode(t, h.get(a, "/portal/api/connections"))
	items := m["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("server listed %d times", len(items))
	}
	if sum := m["summary"].(map[string]any); sum["connected"] != float64(1) {
		t.Fatalf("summary %v", sum)
	}
	want := []profileRow{{"default", "connected", "1234"}, {"acme", "connected", "1234"}, {"globex", "connected", "1234"}}
	if got := profilesOf(t, items[0]); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("profiles = %v", got)
	}
	detail := decode(t, h.get(a, "/portal/api/connections/cf"))
	if got := profilesOf(t, detail); len(got) != 3 || got[0].name != "default" {
		t.Fatalf("detail profiles = %v", got)
	}
	if strings.Contains(readAll(h.get(a, "/portal/api/connections")), "good-token") {
		t.Fatal("token leaked")
	}
}

func TestPortalNamedProfileOnlyStillListsOnce(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("cf", "")
	h.ceiling(h.userA, "cf", "read")
	h.put(a, "/portal/api/connections/cf/token?profile=globex", map[string]string{"token": goodToken})
	m := decode(t, h.get(a, "/portal/api/connections"))
	items := m["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["status"] != "connected" {
		t.Fatalf("%v", items)
	}
}

func TestPortalConnectAndDisconnectPerProfile(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("cf", "good-")
	h.ceiling(h.userA, "cf", "read")
	put := func(profile, token string) int {
		return h.put(a, "/portal/api/connections/cf/token?profile="+profile, map[string]string{"token": token}).StatusCode
	}
	if put("one", goodToken) != 200 || put("two", goodToken) != 200 {
		t.Fatal("connect")
	}
	// Connecting an existing name replaces it: still two rows.
	if put("one", "good-token-9999") != 200 {
		t.Fatal("replace")
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials WHERE profile = 'one' AND last4 = '9999'`); n != 1 {
		t.Fatalf("replace did not update: %d", n)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 2 {
		t.Fatalf("rows: %d", n)
	}

	if res := h.del(a, "/portal/api/connections/cf?profile=one", nil); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	var left string
	if err := h.row(`SELECT profile FROM upstream_credentials`).Scan(&left); err != nil || left != "two" {
		t.Fatalf("left %q %v", left, err)
	}
	// Without ?profile= the default profile is the target (nothing to delete here, not an error).
	if res := h.del(a, "/portal/api/connections/cf", nil); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 1 {
		t.Fatalf("default delete touched a named profile: %d", n)
	}
}

func TestPortalInvalidProfileIs400(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("cf", "")
	h.server("notion", "oauth")
	h.ceiling(h.userA, "cf", "read")
	h.ceiling(h.userA, "notion", "read")
	for _, bad := range []string{"Upper", "1abc", "a%20b", strings.Repeat("a", 33)} {
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"PUT", "/portal/api/connections/cf/token?profile=" + bad, map[string]string{"token": goodToken}},
			{"DELETE", "/portal/api/connections/cf?profile=" + bad, nil},
			{"POST", "/portal/api/connections/notion/oauth?profile=" + bad, map[string]any{}},
		} {
			res := h.do(a, c.method, c.path, c.body, reqOpt{})
			if res.StatusCode != 400 || errorCode(t, res) != "invalid_profile" {
				t.Fatalf("%s %s: %d", c.method, c.path, res.StatusCode)
			}
		}
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatal("stored")
	}
}

func TestPortalOAuthStartCarriesProfile(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.server("notion", "oauth")
	h.ceiling(h.userA, "notion", "read")
	url := func(q string) string {
		res := h.post(a, "/portal/api/connections/notion/oauth"+q, map[string]any{})
		if res.StatusCode != 200 {
			t.Fatalf("%d %s", res.StatusCode, readAll(res))
		}
		return decode(t, res)["redirect_url"].(string)
	}
	if got := url(""); strings.Contains(got, "profile=") || !strings.HasSuffix(got, "/connect/notion") {
		t.Fatalf("default url %q", got)
	}
	if got := url("?profile=globex"); !strings.HasSuffix(got, "/connect/notion?profile=globex") {
		t.Fatalf("named url %q", got)
	}
}

func TestBulkDisconnectRemovesAllProfiles(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "")
	h.headerServer("lin", "")
	h.ceiling(h.userA, "gh", "read")
	h.ceiling(h.userA, "lin", "read")
	for _, path := range []string{"/token?profile=one", "/token?profile=two"} {
		if res := h.put(a, "/portal/api/connections/gh"+path, map[string]string{"token": goodToken}); res.StatusCode != 200 && res.StatusCode != 204 {
			t.Fatal(path, res.StatusCode)
		}
	}
	m := decode(t, h.post(a, "/portal/api/connections/disconnect", map[string]any{"slugs": []string{"gh", "lin"}}))
	if fmt.Sprint(m["disconnected"]) != "[gh]" || fmt.Sprint(m["skipped"]) != "[]" {
		t.Fatalf("%v", m)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials WHERE user_id = $1::uuid`, h.userA); n != 0 {
		t.Fatalf("%d profile rows kept", n)
	}
}
