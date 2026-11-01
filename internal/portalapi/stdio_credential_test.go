package portalapi_test

import (
	"strings"
	"testing"
)

// stdioHeaderServer adds an active docker stdio server whose users paste their own token,
// delivered as PORTAINER_API_KEY.
func (h *harness) stdioHeaderServer(slug string) {
	h.t.Helper()
	h.exec(`WITH n AS (SELECT uuidv7() AS id)
		INSERT INTO upstream_servers (id, slug, url, auth_kind, transport, stdio_launcher, docker_image, credential_env)
		SELECT id, $1, 'stdio://' || id, 'header', 'stdio', 'docker', 'portainer/portainer-mcp:2.45.3', 'PORTAINER_API_KEY' FROM n`, slug)
}

func TestStdioHeaderServerInPortal(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.stdioHeaderServer("portainer")
	h.ceiling(h.userA, "portainer", "read")
	h.headerServer("gh", "")
	h.ceiling(h.userA, "gh", "read")

	list := readAll(h.get(a, "/portal/api/connections?auth=header"))
	if !strings.Contains(list, `"slug":"portainer"`) || !strings.Contains(list, `"credential_env":"PORTAINER_API_KEY"`) ||
		!strings.Contains(list, `"credential_env":null`) || !strings.Contains(list, `"not_connected"`) {
		t.Fatal(list)
	}
	res := h.put(a, "/portal/api/connections/portainer/token?profile=ops", map[string]string{"token": "ptr_alice_ops_1234"})
	if body := readAll(res); res.StatusCode != 200 || !strings.Contains(body, `"last4":"1234"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	detail := readAll(h.get(a, "/portal/api/connections/portainer"))
	if !strings.Contains(detail, `"credential_env":"PORTAINER_API_KEY"`) || !strings.Contains(detail, `"ops"`) ||
		strings.Contains(detail, "ptr_alice") {
		t.Fatal(detail)
	}
}

func TestStdioTokenTestUnavailable(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.stdioHeaderServer("portainer")
	h.ceiling(h.userA, "portainer", "read")
	res := h.post(a, "/portal/api/connections/portainer/token/test", map[string]string{"token": "ptr_candidate_1234"})
	if res.StatusCode != 422 || errorCode(t, res) != "test_unavailable" {
		t.Fatalf("status %d", res.StatusCode)
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatal("test stored a row")
	}
}
