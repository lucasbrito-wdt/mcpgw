package portalapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// rawRequest sends a request with an exact Origin: withOrigin=false omits the header entirely.
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

var badOrigins = map[string]struct {
	with   bool
	origin string
}{
	"null origin":    {true, "null"},
	"missing origin": {false, ""},
}

func TestWriteRejectsNullOrMissingOrigin(t *testing.T) {
	h := newHarness(t)
	c := h.loginAs(h.emailA)
	const path = "/portal/api/connections/disconnect"

	if res := rawRequest(h, c, http.MethodPost, path, `{}`, true, publicURL); res.StatusCode == http.StatusForbidden {
		t.Fatalf("control with valid Origin was rejected: %s", readAll(res))
	}
	for name, tc := range badOrigins {
		t.Run(name, func(t *testing.T) {
			res := rawRequest(h, c, http.MethodPost, path, `{}`, tc.with, tc.origin)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", res.StatusCode)
			}
			if code := errorCode(t, res); code != "csrf" {
				t.Fatalf("code = %q, want csrf", code)
			}
		})
	}
}

func TestLoginAndLogoutRejectNullOrMissingOrigin(t *testing.T) {
	h := newHarness(t)
	body := `{"email":"` + h.emailA + `","password":"` + h.passwordA + `"}`
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/portal/api/session"},
		{http.MethodDelete, "/portal/api/session"},
	} {
		for name, tc := range badOrigins {
			t.Run(route.method+" "+name, func(t *testing.T) {
				res := rawRequest(h, http.DefaultClient, route.method, route.path, body, tc.with, tc.origin)
				if res.StatusCode != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", res.StatusCode)
				}
				if len(res.Cookies()) != 0 {
					t.Fatalf("rejected request set cookies: %v", res.Cookies())
				}
				if code := errorCode(t, res); code != "csrf" {
					t.Fatalf("code = %q, want csrf", code)
				}
			})
		}
	}
}
