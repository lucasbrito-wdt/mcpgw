package portalapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPortalLoginSetsSession(t *testing.T) {
	h := newHarness(t)
	if res := h.post(nil, "/portal/api/session", map[string]string{"email": h.emailA, "password": "wrong password!!"}); res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	a := h.loginAs(h.emailA) // a non-admin is welcome
	m := decode(t, h.get(a, "/portal/api/me"))
	if m["email"] != h.emailA || m["is_admin"] != false {
		t.Fatalf("%v", m)
	}
	if res := h.do(nil, http.MethodPost, "/portal/api/session", map[string]string{"email": h.emailA, "password": h.passwordA}, reqOpt{origin: "http://evil.example"}); res.StatusCode != 403 {
		t.Fatalf("foreign origin login: %d", res.StatusCode)
	}
	if res := h.del(a, "/portal/api/session", nil); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if res := h.get(a, "/portal/api/me"); res.StatusCode != 401 {
		t.Fatalf("after logout: %d", res.StatusCode)
	}
}

func TestPortalRequiresSession(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/portal/api/me", "/portal/api/connections", "/portal/api/connections/gh", "/portal/api/nope"} {
		res := h.get(nil, p)
		if res.StatusCode != 401 || errorCode(t, res) != "unauthorized" {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
	if res := h.put(nil, "/portal/api/connections/gh/token", map[string]string{"token": "x"}); res.StatusCode != 401 {
		t.Fatal("write without session:", res.StatusCode)
	}
}

func TestPortalWriteRequiresCSRF(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "read")
	h.ceiling(h.userB, "gh", "read")
	a, b := h.loginAs(h.emailA), h.loginAs(h.emailB)
	body := map[string]string{"token": goodToken}
	path := "/portal/api/connections/gh/token"

	for name, opt := range map[string]reqOpt{
		"no header":       {noCSRF: true},
		"foreign origin":  {origin: "http://evil.example"},
		"wrong token":     {csrf: "AAAA"},
		"other user csrf": {csrf: h.csrfToken(b)},
	} {
		res := h.do(a, http.MethodPut, path, body, opt)
		if res.StatusCode != 403 || errorCode(t, res) != "csrf" {
			t.Fatalf("%s: %d", name, res.StatusCode)
		}
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatal("a rejected write stored a row")
	}
	// A's CSRF cookie replayed with B's session: header == cookie is not enough, the uid must match.
	res := h.do(b, http.MethodPut, path, body, reqOpt{csrf: h.csrfToken(a)})
	if res.StatusCode != 403 {
		t.Fatalf("cross-user csrf: %d", res.StatusCode)
	}
	if res := h.put(a, path, body); res.StatusCode != 200 {
		t.Fatalf("valid write: %d", res.StatusCode)
	}
}

func TestPortalSessionSlides(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	for i := 0; i < 3; i++ {
		h.clock.Advance(29 * time.Minute)
		if res := h.get(a, "/portal/api/me"); res.StatusCode != 200 {
			t.Fatalf("step %d: %d", i, res.StatusCode)
		}
	}
	h.clock.Advance(31 * time.Minute)
	if res := h.get(a, "/portal/api/me"); res.StatusCode != 401 {
		t.Fatalf("idle: %d", res.StatusCode)
	}
}

func TestRemovedUserLosesAccess(t *testing.T) {
	h := newHarness(t)
	h.headerServer("gh", "")
	h.ceiling(h.userA, "gh", "read")
	a := h.loginAs(h.emailA)
	h.exec(`DELETE FROM users WHERE id = $1::uuid`, h.userA)
	for _, p := range []string{"/portal/api/me", "/portal/api/connections", "/portal/api/connections/gh"} {
		if res := h.get(a, p); res.StatusCode != 401 {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
	if res := h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken}); res.StatusCode != 401 {
		t.Fatalf("write: %d", res.StatusCode)
	}
}

func TestCSRFCookieIsOpaque(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	tok := h.csrfToken(a)
	if tok == "" || strings.Contains(tok, h.userA) {
		t.Fatalf("csrf cookie must be sealed, got %q", tok)
	}
}
