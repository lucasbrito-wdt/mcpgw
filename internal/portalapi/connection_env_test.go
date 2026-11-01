package portalapi_test

import (
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// portainerWithConnectionEnv is a stdio header server declaring PORTAINER_URL (fixed default) and
// PORTAINER_TEAM (no default) as per-connection variables.
func (h *harness) portainerWithConnectionEnv() {
	h.t.Helper()
	h.stdioHeaderServer("portainer")
	id := h.serverID("portainer")
	sealed, err := upstream.SealStdioEnv(h.vault, id, map[string]string{"PORTAINER_URL": "https://default.example"})
	if err != nil {
		h.t.Fatal(err)
	}
	h.exec(`UPDATE upstream_servers SET stdio_env_ciphertext = $2, connection_env = '{PORTAINER_URL,PORTAINER_TEAM}'
		WHERE id = $1::uuid`, id, sealed)
	h.ceiling(h.userA, "portainer", "read")
}

func TestPortalConnectionEnvContract(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.portainerWithConnectionEnv()
	const vars = `"connection_env":[{"name":"PORTAINER_URL","has_default":true},{"name":"PORTAINER_TEAM","has_default":false}]`

	res := h.put(a, "/portal/api/connections/portainer/token?profile=globex", map[string]any{"token": "ptr_globex_0001",
		"env": map[string]string{"PORTAINER_URL": "https://globex-secret.example", "PORTAINER_TEAM": "team-secret-z"}})
	if res.StatusCode != 200 {
		t.Fatalf("put: %d %s", res.StatusCode, readAll(res))
	}
	for _, path := range []string{"/portal/api/connections", "/portal/api/connections/portainer"} {
		body := readAll(h.get(a, path))
		if !strings.Contains(body, vars) || !strings.Contains(body, `"env_set":["PORTAINER_TEAM","PORTAINER_URL"]`) {
			t.Fatalf("%s: %s", path, body)
		}
		if strings.Contains(body, "globex-secret") || strings.Contains(body, "team-secret") || strings.Contains(body, "default.example") {
			t.Fatalf("%s leaked an env value: %s", path, body)
		}
	}

	// Env-only update keeps the token; it needs an existing connection.
	res = h.put(a, "/portal/api/connections/portainer/token?profile=globex", map[string]any{"env": map[string]string{"PORTAINER_TEAM": "t2"}})
	if body := readAll(res); res.StatusCode != 200 || !strings.Contains(body, `"last4":"0001"`) {
		t.Fatalf("env-only: %d %s", res.StatusCode, body)
	}
	if body := readAll(h.get(a, "/portal/api/connections/portainer")); !strings.Contains(body, `"env_set":["PORTAINER_TEAM"]`) {
		t.Fatalf("after env-only: %s", body)
	}
	res = h.put(a, "/portal/api/connections/portainer/token?profile=other", map[string]any{"env": map[string]string{"PORTAINER_TEAM": "t"}})
	if body := readAll(res); res.StatusCode != 422 || !strings.Contains(body, `"token"`) {
		t.Fatalf("env-only on a new profile: %d %s", res.StatusCode, body)
	}
}

func TestPortalConnectionEnvRejections(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.portainerWithConnectionEnv()
	cases := map[string]struct {
		env   map[string]string
		field string
	}{
		"unknown":          {map[string]string{"PORTAINER_TEAM": "t", "OTHER": "x"}, "env.OTHER"},
		"empty value":      {map[string]string{"PORTAINER_TEAM": ""}, "env.PORTAINER_TEAM"},
		"too long":         {map[string]string{"PORTAINER_TEAM": strings.Repeat("a", 2049)}, "env.PORTAINER_TEAM"},
		"required missing": {map[string]string{"PORTAINER_URL": "https://x.example"}, "env.PORTAINER_TEAM"},
		"no env, new row":  {nil, "env.PORTAINER_TEAM"},
	}
	for name, c := range cases {
		body := map[string]any{"token": "ptr_candidate_01"}
		if c.env != nil {
			body["env"] = c.env
		}
		res := h.put(a, "/portal/api/connections/portainer/token", body)
		if got := readAll(res); res.StatusCode != 400 || !strings.Contains(got, `"`+c.field+`"`) {
			t.Errorf("%s: %d %s", name, res.StatusCode, got)
		}
	}
	if n := h.count(`SELECT count(*) FROM upstream_credentials`); n != 0 {
		t.Fatalf("rejected writes stored %d rows", n)
	}
}

func TestPortalServerWithoutConnectionEnv(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("cf", "")
	h.ceiling(h.userA, "cf", "read")
	h.put(a, "/portal/api/connections/cf/token", map[string]string{"token": goodToken})
	body := readAll(h.get(a, "/portal/api/connections"))
	if !strings.Contains(body, `"connection_env":[]`) || !strings.Contains(body, `"env_set":[]`) {
		t.Fatal(body)
	}
	res := h.put(a, "/portal/api/connections/cf/token", map[string]any{"token": goodToken, "env": map[string]string{"X": "1"}})
	if res.StatusCode != 400 {
		t.Fatalf("env on a server without connection_env: %d", res.StatusCode)
	}
}
