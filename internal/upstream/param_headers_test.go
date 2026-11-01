package upstream_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// Servers like GitHub's annotate arguments with x-mcp-header and reject a tools/call that lacks the
// matching Mcp-Param-* header (SEP-2243). The gateway calls from its own catalog without listing
// first, so the client must still send the header.
func TestCallToolSendsParamHeaders(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "param-headers", Version: "1"}, nil)
	schema := json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"owner"}}}`)
	s.AddTool(&mcp.Tool{Name: "get_repo", InputSchema: schema},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Method") == "tools/call" {
			mu.Lock()
			seen = append(seen, r.Header.Get("Mcp-Param-owner"))
			mu.Unlock()
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	c := upstream.New()
	t.Cleanup(c.Close)
	if _, err := c.CallTool(context.Background(), srv.URL, nil, "get_repo", json.RawMessage(`{"owner":"octo"}`)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "octo" {
		t.Fatalf("Mcp-Param-owner on tools/call = %q, want [octo]", seen)
	}
}
