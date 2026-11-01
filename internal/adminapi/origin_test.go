package adminapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// rawRequest sends a write with an exact Origin: withOrigin=false omits the header entirely.
func rawRequest(h *harness, c *http.Client, method, path, body string, withOrigin bool, origin string) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if withOrigin {
		req.Header.Set("Origin", origin)
	}
	if c.Jar != nil {
		req.Header.Set("X-CSRF-Token", h.csrfToken(c))
	}
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestWriteRejectsNullOrMissingOrigin(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	const path = "/admin/api/session/reauth"
	body := `{"password":"` + testPassword + `"}`

	if res := rawRequest(h, c, http.MethodPost, path, body, true, publicURL); res.StatusCode != http.StatusNoContent {
		t.Fatalf("control with valid Origin: %d", res.StatusCode)
	}
	cases := map[string]struct {
		with   bool
		origin string
	}{
		"null origin":    {true, "null"},
		"missing origin": {false, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := rawRequest(h, c, http.MethodPost, path, body, tc.with, tc.origin)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", res.StatusCode)
			}
			if code := errorCode(t, res); code != "csrf" {
				t.Fatalf("code = %q, want csrf", code)
			}
		})
	}
}

func TestLoginRejectsNullOrMissingOrigin(t *testing.T) {
	h := newHarness(t)
	body := `{"email":"` + h.adminEmail + `","password":"` + testPassword + `"}`
	cases := map[string]struct {
		with   bool
		origin string
	}{
		"null origin":    {true, "null"},
		"missing origin": {false, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := rawRequest(h, http.DefaultClient, http.MethodPost, "/admin/api/session", body, tc.with, tc.origin)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", res.StatusCode)
			}
			if len(res.Cookies()) != 0 {
				t.Fatalf("rejected login set cookies: %v", res.Cookies())
			}
			if code := errorCode(t, res); code != "csrf" {
				t.Fatalf("code = %q, want csrf", code)
			}
		})
	}
}
