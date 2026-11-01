package adminapi_test

import (
	"strings"
	"testing"
)

func portainerBody() map[string]any {
	return map[string]any{"slug": "portainer", "transport": "stdio", "stdio_launcher": "docker",
		"stdio_image": "portainer/portainer-mcp:2.45.3", "stdio_network": true, "credential_env": "PORTAINER_API_KEY",
		"stdio_env": []map[string]string{
			{"name": "PORTAINER_URL", "value": "https://manager.example"},
			{"name": "PORTAINER_MCP_TRANSPORT", "value": "stdio"},
		}}
}

func TestAddStdioServerWithCredentialEnv(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	res := h.post(c, "/admin/api/servers", portainerBody())
	created := decodeBody[map[string]any](t, res)
	if res.StatusCode != 201 || created["auth_kind"] != "header" || created["credential_env"] != "PORTAINER_API_KEY" {
		t.Fatalf("%d %v", res.StatusCode, created)
	}
	res = h.get(c, "/admin/api/servers/portainer")
	body := readAll(res)
	if res.StatusCode != 200 || strings.Contains(body, "manager.example") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	for _, want := range []string{`"credential_env":"PORTAINER_API_KEY"`, `"stdio_network":true`,
		`"stdio_env_names":["PORTAINER_MCP_TRANSPORT","PORTAINER_URL"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
}

func TestAddStdioServerEnvRequiresReauth(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	body := portainerBody()
	delete(body, "transport")
	if res := h.post(c, "/admin/api/servers", body); res.StatusCode == 201 {
		t.Fatal("stdio server created without reauth")
	}
}

func TestAddStdioServerRejectsDuplicateEnv(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	body := portainerBody()
	body["stdio_env"] = []map[string]string{{"name": "A", "value": "1"}, {"name": "A", "value": "2"}}
	if res := h.post(c, "/admin/api/servers", body); res.StatusCode != 422 || !strings.Contains(readAll(res), `"stdio_env"`) {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestListServersExposesCredentialEnv(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	if res := h.post(c, "/admin/api/servers", portainerBody()); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	h.addServer(c, "plain", "none")
	body := readAll(h.get(c, "/admin/api/servers"))
	if !strings.Contains(body, `"credential_env":"PORTAINER_API_KEY"`) || !strings.Contains(body, `"credential_env":null`) {
		t.Fatal(body)
	}
}

func TestSyncStdioServerWithoutCredential(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	if res := h.post(c, "/admin/api/servers", portainerBody()); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	res := h.post(c, "/admin/api/servers/portainer/sync", nil)
	if res.StatusCode != 422 || !strings.Contains(readAll(res), `"credential"`) {
		t.Fatalf("status %d", res.StatusCode)
	}
	res = h.post(c, "/admin/api/servers/portainer/test", nil)
	if body := readAll(res); res.StatusCode != 200 || !strings.Contains(body, `"requires_auth":true`) {
		t.Fatalf("probe %d %s", res.StatusCode, body)
	}
}
