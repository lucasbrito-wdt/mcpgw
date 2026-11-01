package adminapi_test

import (
	"strings"
	"testing"
)

func TestPatchStdioEnv(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	if res := h.post(c, "/admin/api/servers", portainerBody()); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	patch := map[string]any{"stdio_env": map[string]any{
		"set": map[string]string{"PORTAINER_TIMEOUT": "very-secret-120"}, "unset": []string{"PORTAINER_MCP_TRANSPORT"}}}
	res := h.patch(c, "/admin/api/servers/portainer", patch)
	if body := readAll(res); res.StatusCode != 200 || strings.Contains(body, "very-secret-120") {
		t.Fatalf("patch: %d %s", res.StatusCode, body)
	}
	got := readAll(h.get(c, "/admin/api/servers/portainer"))
	if !strings.Contains(got, `"stdio_env_names":["PORTAINER_TIMEOUT","PORTAINER_URL"]`) || strings.Contains(got, "very-secret-120") {
		t.Fatal(got)
	}
}

func TestPatchStdioEnvValidation(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	if res := h.post(c, "/admin/api/servers", portainerBody()); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	for name, env := range map[string]map[string]any{
		"credential name": {"set": map[string]string{"PORTAINER_API_KEY": "v"}},
		"empty value":     {"set": map[string]string{"A": ""}},
		"newline":         {"set": map[string]string{"A": "a\nb"}},
		"empty edit":      {},
	} {
		res := h.patch(c, "/admin/api/servers/portainer", map[string]any{"stdio_env": env})
		if body := readAll(res); res.StatusCode != 422 || !strings.Contains(body, `"stdio_env"`) {
			t.Fatalf("%s: %d %s", name, res.StatusCode, body)
		}
	}
}

func TestPatchStdioEnvRequiresReauth(t *testing.T) {
	h := newHarness(t)
	if res := h.post(h.loginReauthed(h.adminEmail), "/admin/api/servers", portainerBody()); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	res := h.patch(h.login(h.adminEmail), "/admin/api/servers/portainer", map[string]any{"stdio_env": map[string]any{"set": map[string]string{"A": "v"}}})
	if res.StatusCode != 403 {
		t.Fatalf("status %d", res.StatusCode)
	}
}
