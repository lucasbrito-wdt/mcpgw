package portalapi_test

import (
	"strings"
	"testing"
)

func TestPortalConnectionURLContract(t *testing.T) {
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("kafka", "")
	h.exec(`UPDATE upstream_servers SET connection_url = true WHERE slug = 'kafka'`)
	h.ceiling(h.userA, "kafka", "read")

	res := h.put(a, "/portal/api/connections/kafka/token", map[string]any{"token": goodToken})
	if body := readAll(res); res.StatusCode != 422 || !strings.Contains(body, `"url"`) {
		t.Fatalf("create without url: %d %s", res.StatusCode, body)
	}
	res = h.post(a, "/portal/api/connections/kafka/token/test", map[string]any{"token": goodToken, "url": "https://169.254.169.254/mcp"})
	if body := readAll(res); res.StatusCode != 422 || !strings.Contains(body, `"url"`) {
		t.Fatalf("test with metadata url: %d %s", res.StatusCode, body)
	}
	res = h.post(a, "/portal/api/connections/kafka/token/test", map[string]any{"token": goodToken, "url": "https://kafka-a.example.app/mcp"})
	if body := readAll(res); res.StatusCode != 200 || !strings.Contains(body, "get_issue") {
		t.Fatalf("test: %d %s", res.StatusCode, body)
	}
	res = h.put(a, "/portal/api/connections/kafka/token", map[string]any{"token": goodToken, "url": "https://kafka-a.example.app/mcp?k=v"})
	if body := readAll(res); res.StatusCode != 200 {
		t.Fatalf("put: %d %s", res.StatusCode, body)
	}
	// An empty url keeps the saved one.
	res = h.put(a, "/portal/api/connections/kafka/token", map[string]any{"token": goodToken})
	if body := readAll(res); res.StatusCode != 200 {
		t.Fatalf("put keeping url: %d %s", res.StatusCode, body)
	}
	for _, path := range []string{"/portal/api/connections", "/portal/api/connections/kafka"} {
		body := readAll(h.get(a, path))
		if !strings.Contains(body, `"connection_url":true`) || !strings.Contains(body, `"url_host":"kafka-a.example.app"`) {
			t.Fatalf("%s: %s", path, body)
		}
		if strings.Contains(body, "k=v") {
			t.Fatalf("%s exposed the full url: %s", path, body)
		}
	}
	var stored string
	if err := h.row(`SELECT url FROM upstream_credentials`).Scan(&stored); err != nil || stored != "https://kafka-a.example.app/mcp?k=v" {
		t.Fatalf("stored = %q, %v", stored, err)
	}
}
