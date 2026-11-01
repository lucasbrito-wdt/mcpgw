package adminapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCannotDemoteLastAdmin(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	res := h.patch(c, "/admin/api/users/"+h.adminID, map[string]any{"is_admin": false})
	if res.StatusCode != 409 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if body := readAll(res); !strings.Contains(body, "cannot remove the last admin") {
		t.Fatal(body)
	}
}

// TestErrorLocalizedByAcceptLanguage: the same error is EN without Accept-Language and PT with it.
func TestErrorLocalizedByAcceptLanguage(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	for lang, want := range map[string]string{"": "cannot remove the last admin", "pt-BR": "não é possível remover o último admin"} {
		res := h.do(c, http.MethodPatch, "/admin/api/users/"+h.adminID, map[string]any{"is_admin": false}, reqOpt{lang: lang})
		if body := readAll(res); res.StatusCode != 409 || !strings.Contains(body, want) {
			t.Fatalf("lang %q: %d %s", lang, res.StatusCode, body)
		}
	}
}

func TestPromoteUser(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	res := h.patch(c, "/admin/api/users/"+h.userID, map[string]any{"is_admin": true})
	if res.StatusCode != 200 {
		t.Fatalf("status %d %s", res.StatusCode, readAll(res))
	}
	var u struct {
		ID      string `json:"id"`
		IsAdmin bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil || u.ID != h.userID || !u.IsAdmin {
		t.Fatalf("%+v %v", u, err)
	}
	if res := h.patch(c, "/admin/api/users/"+h.userID, map[string]any{}); res.StatusCode != 422 {
		t.Fatalf("missing is_admin: %d", res.StatusCode)
	}
}

func TestWritesAreAudited(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	res := h.post(c, "/admin/api/users", map[string]any{"email": "n@example.com", "password": "a long enough pw"})
	if res.StatusCode != 201 {
		t.Fatalf("status %d %s", res.StatusCode, readAll(res))
	}
	if body := readAll(res); strings.Contains(body, "long enough") || strings.Contains(body, "password") {
		t.Fatalf("password echoed: %s", body)
	}
	var action, target, actor, ip string
	var details []byte
	err := h.row(`SELECT action, target, actor_user_id::text, details, ip FROM admin_events`).Scan(&action, &target, &actor, &details, &ip)
	if err != nil {
		t.Fatal(err)
	}
	if action != "user.create" || target != "n@example.com" || actor != h.adminID || ip == "" {
		t.Fatalf("%s %s %s %q", action, target, actor, ip)
	}
	if bytes.Contains(details, []byte("long enough")) {
		t.Fatal("password in details")
	}
	if res := h.post(c, "/admin/api/users", map[string]any{"email": "n@example.com", "password": "a long enough pw"}); res.StatusCode != 409 {
		t.Fatalf("duplicate: %d", res.StatusCode)
	}
	if n := h.count(`SELECT count(*) FROM admin_events`); n != 1 {
		t.Fatalf("events %d", n)
	}
}

func TestListUsers(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	res := h.get(c, "/admin/api/users?limit=1")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var page struct {
		Items []struct {
			Email string `json:"email"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	body := readAll(res)
	if strings.Contains(body, "password") {
		t.Fatal("password_hash exposed")
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("%s", body)
	}
	first := page.Items[0].Email
	res = h.get(c, "/admin/api/users?limit=1&cursor="+page.NextCursor)
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil || len(page.Items) != 1 || page.Items[0].Email == first || page.NextCursor != "" {
		t.Fatalf("page 2: %+v %v", page, err)
	}
}

func TestResetPasswordRequiresReauth(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	path := "/admin/api/users/" + h.userID + "/password"
	newPw := map[string]string{"password": "another long password"}
	res := h.post(c, path, newPw)
	if res.StatusCode != 403 || errorCode(t, res) != "reauth_required" {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res := h.post(c, "/admin/api/session/reauth", map[string]string{"password": "wrong password!!"}); res.StatusCode != 401 {
		t.Fatalf("wrong reauth: %d", res.StatusCode)
	}
	if res := h.post(c, "/admin/api/session/reauth", map[string]string{"password": testPassword}); res.StatusCode != 204 {
		t.Fatalf("reauth: %d %s", res.StatusCode, readAll(res))
	}
	if res := h.post(c, path, newPw); res.StatusCode != 204 {
		t.Fatalf("after reauth: %d %s", res.StatusCode, readAll(res))
	}
	var details []byte
	if err := h.row(`SELECT details FROM admin_events WHERE action = 'user.reset_password'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(details, []byte("another")) {
		t.Fatal("password in details")
	}
	h.clock.Advance(6 * time.Minute) // 6 min: reauth window over, session still alive
	if res := h.post(c, path, newPw); res.StatusCode != 403 {
		t.Fatalf("stale reauth: %d", res.StatusCode)
	}
}
