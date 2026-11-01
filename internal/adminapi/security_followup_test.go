package adminapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// loginReauthed logs in and confirms the password, for tests of privileged writes.
func (h *harness) loginReauthed(email string) *http.Client {
	h.t.Helper()
	c := h.login(email)
	h.reauth(c)
	return c
}

func TestPrivilegedWritesRequireReauth(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	usersBefore := h.count(`SELECT count(*) FROM users`)
	stdio := map[string]any{"slug": "fs", "transport": "stdio", "stdio_launcher": "docker", "stdio_image": "ghcr.io/example/fs:1",
		"stdio_command": "serve", "auth_kind": "none"}
	attempts := []struct {
		name, method, path string
		body               map[string]any
	}{
		{"create admin", "POST", "/admin/api/users", map[string]any{"email": "new@example.com", "password": testPassword, "is_admin": true}},
		{"promote", "PATCH", "/admin/api/users/" + h.userID, map[string]any{"is_admin": true}},
		{"stdio server", "POST", "/admin/api/servers", stdio},
	}
	do := func(method, path string, body map[string]any) int {
		if method == "PATCH" {
			return h.patch(c, path, body).StatusCode
		}
		res := h.post(c, path, body)
		if res.StatusCode == 403 && !strings.Contains(readAll(res), `"reauth_required"`) {
			t.Fatal("403 is not reauth_required")
		}
		return res.StatusCode
	}
	for _, a := range attempts {
		if got := do(a.method, a.path, a.body); got != 403 {
			t.Fatalf("%s without reauth: %d", a.name, got)
		}
	}
	if h.count(`SELECT count(*) FROM users`) != usersBefore || h.count(`SELECT count(*) FROM users WHERE is_admin`) != 1 ||
		h.count(`SELECT count(*) FROM upstream_servers`) != 0 {
		t.Fatal("a write happened without reauth")
	}
	// Non-privileged writes need no reauth.
	if got := do("POST", "/admin/api/users", map[string]any{"email": "plain@example.com", "password": testPassword}); got != 201 {
		t.Fatalf("plain create: %d", got)
	}
	h.addServer(c, "httpsrv", "none")
	h.reauth(c)
	for _, a := range attempts {
		if got := do(a.method, a.path, a.body); got/100 != 2 {
			t.Fatalf("%s after reauth: %d", a.name, got)
		}
	}
	// Demote needs no reauth even once the window is gone.
	h.clock.Advance(6 * 60 * 1e9)
	if got := do("PATCH", "/admin/api/users/"+h.userID, map[string]any{"is_admin": false}); got != 200 {
		t.Fatalf("demote: %d", got)
	}
}

func TestSharedRemovalRequiresReauth(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "linear", "header")
	h.setShared(c, "linear")
	h.clock.Advance(6 * 60 * 1e9)
	for _, path := range []string{"/admin/api/servers/linear/shared", "/admin/api/connections/linear/shared"} {
		if res := h.del(c, path, nil); res.StatusCode != 403 || !strings.Contains(readAll(res), `"reauth_required"`) {
			t.Fatalf("%s without reauth: %d", path, res.StatusCode)
		}
		if h.count(`SELECT count(*) FROM upstream_credentials WHERE shared AND status = 'active'`) != 1 {
			t.Fatalf("%s changed the shared row", path)
		}
	}
	h.reauth(c)
	if res := h.del(c, "/admin/api/servers/linear/shared", nil); res.StatusCode != 204 {
		t.Fatalf("after reauth: %d", res.StatusCode)
	}
}

func TestOAuthClientIDChangeKeepsSecret(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "notion", "oauth")
	h.reauth(c)
	if res := h.put(c, "/admin/api/servers/notion/oauth-client", map[string]string{"client_id": "abc", "client_secret": "shh"}); res.StatusCode != 204 {
		t.Fatalf("set: %d", res.StatusCode)
	}
	read := func() (ct []byte) {
		if err := h.row(`SELECT oauth_client_secret_ciphertext FROM upstream_servers WHERE slug = 'notion'`).Scan(&ct); err != nil {
			t.Fatal(err)
		}
		return ct
	}
	before := read()
	if len(before) == 0 {
		t.Fatal("secret not stored")
	}
	if res := h.put(c, "/admin/api/servers/notion/oauth-client", map[string]string{"client_id": "def"}); res.StatusCode != 204 {
		t.Fatalf("id only: %d", res.StatusCode)
	}
	if string(read()) != string(before) {
		t.Fatal("omitting the secret changed the stored ciphertext")
	}
}
