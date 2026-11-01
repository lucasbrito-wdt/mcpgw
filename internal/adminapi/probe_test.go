package adminapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newSecretMCP is an upstream MCP server with one tool that accepts only the shared test secret.
func newSecretMCP(t *testing.T) *httptest.Server {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "probe-upstream", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "get_item", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
		})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secretValue {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type probeBody struct {
	OK           bool    `json:"ok"`
	Status       *int    `json:"status"`
	RequiresAuth bool    `json:"requires_auth"`
	Tools        *int    `json:"tools"`
	Error        *string `json:"error"`
}

func (h *harness) addHeaderServerAt(c *http.Client, slug, url string) {
	h.t.Helper()
	body := map[string]any{"slug": slug, "url": url, "auth_kind": "header", "transport": "auto",
		"header": map[string]string{"name": "Authorization", "prefix": "Bearer "}}
	if res := h.post(c, "/admin/api/servers", body); res.StatusCode != 201 {
		h.t.Fatalf("add server: %d %s", res.StatusCode, readAll(res))
	}
}

func TestProbeStoredServerOKAndRequiresAuthWithoutWriting(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	up := newSecretMCP(t)
	h.addHeaderServerAt(c, "withkey", up.URL)
	h.setShared(c, "withkey")
	h.addHeaderServerAt(c, "nokey", up.URL)
	tools := h.count(`SELECT count(*) FROM tools`)
	events := h.count(`SELECT count(*) FROM admin_events`)
	servers := h.count(`SELECT count(*) FROM upstream_servers`)
	synced := h.count(`SELECT count(*) FROM upstream_servers WHERE catalog_synced_at IS NOT NULL`)

	var ok probeBody
	res := h.post(c, "/admin/api/servers/withkey/test", nil)
	body := readAll(res)
	if res.StatusCode != 200 || strings.Contains(body, secretValue) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	decodeInto(t, h.post(c, "/admin/api/servers/withkey/test", nil), &ok)
	if !ok.OK || ok.Tools == nil || *ok.Tools != 1 || ok.Error != nil || ok.RequiresAuth {
		t.Fatalf("%+v", ok)
	}

	var denied probeBody
	decodeInto(t, h.post(c, "/admin/api/servers/nokey/test", nil), &denied)
	if denied.OK || !denied.RequiresAuth || denied.Status == nil || *denied.Status != 401 || denied.Tools != nil || denied.Error == nil {
		t.Fatalf("%+v", denied)
	}

	if got := h.count(`SELECT count(*) FROM tools`); got != tools {
		t.Errorf("tools %d -> %d", tools, got)
	}
	if got := h.count(`SELECT count(*) FROM admin_events`); got != events {
		t.Errorf("admin_events %d -> %d", events, got)
	}
	if got := h.count(`SELECT count(*) FROM upstream_servers`); got != servers {
		t.Errorf("upstream_servers %d -> %d", servers, got)
	}
	if got := h.count(`SELECT count(*) FROM upstream_servers WHERE catalog_synced_at IS NOT NULL`); got != synced {
		t.Errorf("synced %d -> %d", synced, got)
	}
}

func TestProbeUnknownSlugIs404(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	if res := h.post(c, "/admin/api/servers/ghost/test", nil); res.StatusCode != 404 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestProbeURL(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	up := newSecretMCP(t)
	var got probeBody
	res := h.post(c, "/admin/api/servers/test", map[string]string{"url": up.URL, "transport": "auto"})
	decodeInto(t, res, &got)
	if res.StatusCode != 200 || got.OK || !got.RequiresAuth {
		t.Fatalf("%d %+v", res.StatusCode, got)
	}
	for name, body := range map[string]map[string]string{
		"stdio":   {"url": up.URL, "transport": "stdio"},
		"bad url": {"url": "ftp://x", "transport": "auto"},
		"unknown": {"url": up.URL, "transport": "carrier-pigeon"},
	} {
		if res := h.post(c, "/admin/api/servers/test", body); res.StatusCode != 422 {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
	}
}
