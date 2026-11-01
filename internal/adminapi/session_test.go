package adminapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSessionRejectsNonAdmin(t *testing.T) {
	h := newHarness(t)
	res := h.post(nil, "/admin/api/session", map[string]string{"email": h.userEmail, "password": testPassword})
	if res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
	nonAdmin := readAll(res)
	if !strings.Contains(nonAdmin, `"unauthorized"`) {
		t.Fatal(nonAdmin)
	}
	if len(res.Cookies()) != 0 {
		t.Fatal("non-admin got cookies")
	}
	wrong := readAll(h.post(nil, "/admin/api/session", map[string]string{"email": h.adminEmail, "password": "wrong password!!"}))
	if wrong != nonAdmin {
		t.Fatalf("account oracle: %q vs %q", wrong, nonAdmin)
	}
}

func TestLoginSetsStrictCookies(t *testing.T) {
	h := newHarness(t)
	res := h.post(nil, "/admin/api/session", map[string]string{"email": h.adminEmail, "password": testPassword})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", res.StatusCode)
	}
	seen := map[string]*http.Cookie{}
	for _, c := range res.Cookies() {
		seen[c.Name] = c
	}
	sess, csrf := seen["mcpgw_admin"], seen["mcpgw_admin_csrf"]
	if sess == nil || csrf == nil {
		t.Fatalf("cookies: %v", res.Cookies())
	}
	if !sess.HttpOnly || csrf.HttpOnly {
		t.Fatal("session must be HttpOnly, csrf readable")
	}
	for _, c := range []*http.Cookie{sess, csrf} {
		if c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 0 {
			t.Fatalf("cookie %s: %+v", c.Name, c)
		}
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing Cache-Control: no-store")
	}
}

func TestLoginRejectsForeignOrigin(t *testing.T) {
	h := newHarness(t)
	res := h.do(nil, http.MethodPost, "/admin/api/session",
		map[string]string{"email": h.adminEmail, "password": testPassword}, reqOpt{origin: "https://evil.example"})
	if res.StatusCode != 403 || errorCode(t, res) != "csrf" {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestDemotedAdminLosesAccess(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	if res := h.get(c, "/admin/api/me"); res.StatusCode != 200 || !strings.Contains(readAll(res), h.adminEmail) {
		t.Fatalf("status %d", res.StatusCode)
	}
	h.exec(`UPDATE users SET is_admin = false WHERE id = $1`, h.adminID)
	if res := h.get(c, "/admin/api/me"); res.StatusCode != 403 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestWriteRequiresCSRF(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	body := map[string]any{"email": "n@example.com", "password": testPassword}
	res := h.do(c, http.MethodPost, "/admin/api/users", body, reqOpt{noCSRF: true})
	if res.StatusCode != 403 || errorCode(t, res) != "csrf" {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	res = h.do(c, http.MethodPost, "/admin/api/users", body, reqOpt{origin: "https://evil.example"})
	if res.StatusCode != 403 || errorCode(t, res) != "csrf" {
		t.Fatalf("foreign origin: %d", res.StatusCode)
	}
	if n := h.count(`SELECT count(*) FROM users WHERE email = 'n@example.com'`); n != 0 {
		t.Fatal("rejected write ran")
	}
	if res := h.post(c, "/admin/api/users", body); res.StatusCode != 201 {
		t.Fatalf("valid: %d %s", res.StatusCode, readAll(res))
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.clock.Advance(31 * time.Minute)
	if res := h.get(c, "/admin/api/me"); res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestSessionAbsoluteExpiry(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	for elapsed := 20 * time.Minute; elapsed < 8*time.Hour; elapsed += 20 * time.Minute {
		h.clock.Advance(20 * time.Minute)
		if res := h.get(c, "/admin/api/me"); res.StatusCode != 200 {
			t.Fatalf("at %s: status %d", elapsed, res.StatusCode)
		}
	}
	h.clock.Advance(21 * time.Minute) // 8h01 since login
	if res := h.get(c, "/admin/api/me"); res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestSessionSlidesOnUse(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	for range 2 {
		h.clock.Advance(25 * time.Minute)
		if res := h.get(c, "/admin/api/me"); res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
	}
}

func TestLogoutClearsCookies(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	res := h.do(c, http.MethodDelete, "/admin/api/session", nil, reqOpt{})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", res.StatusCode)
	}
	cleared := 0
	for _, ck := range res.Cookies() {
		if (ck.Name == "mcpgw_admin" || ck.Name == "mcpgw_admin_csrf") && ck.MaxAge < 0 {
			cleared++
		}
	}
	if cleared != 2 {
		t.Fatalf("cookies: %v", res.Cookies())
	}
	if res := h.get(c, "/admin/api/me"); res.StatusCode != 401 {
		t.Fatalf("after logout: %d", res.StatusCode)
	}
}

func TestTamperedSessionRejected(t *testing.T) {
	h := newHarness(t)
	jar := h.login(h.adminEmail).Jar
	c := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/admin/api/me", nil)
	for _, ck := range jar.Cookies(req.URL) {
		if ck.Name == "mcpgw_admin" {
			b := []byte(ck.Value)
			if i := len(b) / 2; b[i] == 'A' {
				b[i] = 'B'
			} else {
				b[i] = 'A'
			}
			ck.Value = string(b)
		}
		req.AddCookie(ck)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	h := newHarness(t)
	if res := h.get(nil, "/admin/api/nope"); res.StatusCode != 401 {
		t.Fatalf("anonymous: %d", res.StatusCode)
	}
	c := h.login(h.adminEmail)
	res := h.get(c, "/admin/api/nope")
	if res.StatusCode != 404 || errorCode(t, res) != "not_found" {
		t.Fatalf("status %d", res.StatusCode)
	}
}
