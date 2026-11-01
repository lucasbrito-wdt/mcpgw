// Command fakeupstream is a tiny MCP server for the Playwright smoke: /mcp is open, /auth/mcp
// answers 401 unless Authorization equals "Bearer " + -token.
package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type itemArgs struct {
	ID string `json:"id" jsonschema:"item id"`
}

type listArgs struct{}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func newServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fakeupstream", Version: "0.0.1"}, nil)
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "get_item", Description: "Returns one item by id.", Annotations: ro},
		func(_ context.Context, _ *mcp.CallToolRequest, in itemArgs) (*mcp.CallToolResult, any, error) {
			return text("item " + in.ID), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_items", Description: "Lists all items.", Annotations: ro},
		func(_ context.Context, _ *mcp.CallToolRequest, _ listArgs) (*mcp.CallToolResult, any, error) {
			return text("item-1, item-2"), nil, nil
		})
	return s
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18790", "listen address")
	token := flag.String("token", "", "token accepted on /auth/mcp (required)")
	flag.Parse()
	if *token == "" {
		log.Fatal("-token is required")
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return newServer() }, nil)
	want := []byte("Bearer " + *token)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/auth/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	log.Fatal(http.ListenAndServe(*addr, mux))
}
