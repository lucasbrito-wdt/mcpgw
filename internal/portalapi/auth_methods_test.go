package portalapi_test

import (
	"fmt"
	"testing"
)

// tokenOAuthServer adds an oauth server that also accepts a token in X-Alt-Key.
func (h *harness) tokenOAuthServer(slug string) {
	h.t.Helper()
	h.exec(`INSERT INTO upstream_servers (slug, url, auth_kind, header_name, header_prefix, header_format_hint, header_help_url)
		VALUES ($1, 'https://up.example.com/mcp', 'oauth', 'X-Alt-Key', '', 'good-', 'https://up.example.com/tokens')`, slug)
}

func itemBySlug(t *testing.T, m map[string]any, slug string) map[string]any {
	t.Helper()
	for _, it := range m["items"].([]any) {
		if item := it.(map[string]any); item["slug"] == slug {
			return item
		}
	}
	t.Fatalf("slug %s not listed", slug)
	return nil
}

func TestAuthMethods(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.tokenOAuthServer("both")
	h.server("oauthonly", "oauth")
	h.headerServer("tokenonly", "")
	h.server("open", "none")
	for _, s := range []string{"both", "oauthonly", "tokenonly", "open"} {
		h.ceiling(h.userA, s, "read")
	}
	list := decode(t, h.get(a, "/portal/api/connections"))
	for slug, want := range map[string]string{"both": "[oauth token]", "oauthonly": "[oauth]", "tokenonly": "[token]", "open": "[]"} {
		if got := fmt.Sprint(itemBySlug(t, list, slug)["auth_methods"]); got != want {
			t.Errorf("list %s auth_methods = %s, want %s", slug, got, want)
		}
		if got := fmt.Sprint(decode(t, h.get(a, "/portal/api/connections/"+slug))["auth_methods"]); got != want {
			t.Errorf("detail %s auth_methods = %s, want %s", slug, got, want)
		}
	}
	d := decode(t, h.get(a, "/portal/api/connections/both"))
	if d["format_hint"] != "good-" || d["help_url"] != "https://up.example.com/tokens" {
		t.Fatalf("detail = %v", d)
	}
}

func TestTokenOnOAuthServer(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.tokenOAuthServer("both")
	h.server("oauthonly", "oauth")
	h.ceiling(h.userA, "both", "read")
	h.ceiling(h.userA, "oauthonly", "read")

	if res := h.post(a, "/portal/api/connections/both/token/test", map[string]string{"token": goodToken}); res.StatusCode != 200 {
		t.Fatalf("test token: %d %s", res.StatusCode, readAll(res))
	}
	if res := h.post(a, "/portal/api/connections/both/token/test", map[string]string{"token": "good-wrong"}); res.StatusCode != 422 {
		t.Fatalf("wrong token test: %d", res.StatusCode)
	}
	if res := h.put(a, "/portal/api/connections/both/token?profile=work", map[string]string{"token": goodToken}); res.StatusCode != 200 {
		t.Fatalf("put token: %d %s", res.StatusCode, readAll(res))
	}
	if got := h.count(`SELECT count(*) FROM upstream_credentials WHERE profile = 'work' AND method = 'token' AND user_id = $1::uuid`, h.userA); got != 1 {
		t.Fatalf("token rows = %d", got)
	}
	m := decode(t, h.get(a, "/portal/api/connections/both"))
	profiles := m["profiles"].([]any)
	if len(profiles) != 1 || profiles[0].(map[string]any)["method"] != "token" {
		t.Fatalf("profiles = %v", profiles)
	}

	for _, probe := range []func() (int, string){
		func() (int, string) {
			r := h.put(a, "/portal/api/connections/oauthonly/token", map[string]string{"token": goodToken})
			return r.StatusCode, errorCode(t, r)
		},
		func() (int, string) {
			r := h.post(a, "/portal/api/connections/oauthonly/token/test", map[string]string{"token": goodToken})
			return r.StatusCode, errorCode(t, r)
		},
	} {
		if status, code := probe(); status != 422 || code != "token_not_supported" {
			t.Errorf("oauth without alternative: %d %s", status, code)
		}
	}
}

func TestProfileMethodResolvesLegacy(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.server("oa", "oauth")
	h.headerServer("hd", "")
	for _, s := range []string{"oa", "hd"} {
		h.ceiling(h.userA, s, "read")
		h.exec(`INSERT INTO upstream_credentials (server_id, user_id, access_ciphertext, key_id)
			VALUES ((SELECT id FROM upstream_servers WHERE slug = $1), $2::uuid, '\x00', 'k1')`, s, h.userA)
	}
	for slug, want := range map[string]string{"oa": "oauth", "hd": "token"} {
		p := decode(t, h.get(a, "/portal/api/connections/"+slug))["profiles"].([]any)
		if len(p) != 1 || p[0].(map[string]any)["method"] != want {
			t.Errorf("%s profiles = %v, want method %s", slug, p, want)
		}
	}
}
