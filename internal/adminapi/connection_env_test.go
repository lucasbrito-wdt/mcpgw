package adminapi_test

import (
	"strings"
	"testing"
)

func TestAddStdioServerWithConnectionEnv(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	body := portainerBody()
	body["connection_env"] = []string{"PORTAINER_URL"}
	if res := h.post(c, "/admin/api/servers", body); res.StatusCode != 201 {
		t.Fatalf("add: %d %s", res.StatusCode, readAll(res))
	}
	got := readAll(h.get(c, "/admin/api/servers/portainer"))
	if !strings.Contains(got, `"connection_env":["PORTAINER_URL"]`) || strings.Contains(got, "manager.example") {
		t.Fatal(got)
	}
}

func TestPatchConnectionEnv(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	if res := h.post(c, "/admin/api/servers", portainerBody()); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	if got := readAll(h.get(c, "/admin/api/servers/portainer")); !strings.Contains(got, `"connection_env":[]`) {
		t.Fatal(got)
	}
	res := h.patch(c, "/admin/api/servers/portainer", map[string]any{"connection_env": []string{"PORTAINER_URL", "PORTAINER_TEAM"}})
	if res.StatusCode != 200 {
		t.Fatalf("patch: %d %s", res.StatusCode, readAll(res))
	}
	if got := readAll(h.get(c, "/admin/api/servers/portainer")); !strings.Contains(got, `"connection_env":["PORTAINER_URL","PORTAINER_TEAM"]`) {
		t.Fatal(got)
	}
	res = h.patch(c, "/admin/api/servers/portainer", map[string]any{"connection_env": []string{"PORTAINER_API_KEY"}})
	if body := readAll(res); res.StatusCode != 422 || !strings.Contains(body, `"connection_env"`) {
		t.Fatalf("credential name: %d %s", res.StatusCode, body)
	}
}

func TestPatchConnectionEnvRequiresReauth(t *testing.T) {
	h := newHarness(t)
	if res := h.post(h.loginReauthed(h.adminEmail), "/admin/api/servers", portainerBody()); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	res := h.patch(h.login(h.adminEmail), "/admin/api/servers/portainer", map[string]any{"connection_env": []string{"PORTAINER_URL"}})
	if res.StatusCode != 403 {
		t.Fatalf("status %d", res.StatusCode)
	}
}
