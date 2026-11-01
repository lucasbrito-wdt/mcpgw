// Command fakeupstream is a stateless Streamable HTTP MCP server for load tests. It exposes
// get_item (instant) and slow_item (sleeps -delay), both read-only in the gateway's catalog.
//
//	fakeupstream -addr 127.0.0.1:9100 -delay 20ms
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9100", "listen address")
	delay := flag.Duration("delay", 20*time.Millisecond, "latency of slow_item")
	flag.Parse()

	s := mcp.NewServer(&mcp.Implementation{Name: "fakeupstream", Version: "1"}, nil)
	item := func(wait time.Duration) mcp.ToolHandler {
		return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any
			_ = json.Unmarshal(req.Params.Arguments, &args)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
			}
			body := fmt.Sprintf(`{"id":%q,"name":"widget"}`, fmt.Sprint(args["id"]))
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}, nil
		}
	}
	schema := map[string]any{"type": "object"}
	s.AddTool(&mcp.Tool{Name: "get_item", Description: "Fetch an item by id", InputSchema: schema}, item(0))
	s.AddTool(&mcp.Tool{Name: "slow_item", Description: "Fetch an item by id, slowly", InputSchema: schema}, item(*delay))

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	log.Printf("fakeupstream listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, h))
}
