package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

const (
	toolSearch  = "search"
	toolExecute = "execute"

	genericToolError = "Internal error: the request could not be completed. Try again later."
	busyToolError    = "Too many scripts are running right now (the gateway or your account is at its concurrency limit). Wait a few seconds and retry."
)

type searchInput struct {
	Query string `json:"query" jsonschema:"keywords describing the tools you need, for example: issues github"`
}

type executeInput struct {
	Code string `json:"code" jsonschema:"an async arrow function that calls tools, for example: async () => { return await tools.server.tool({}); }"`
}

// scriptErrors are sandbox failures caused by the script itself; their messages are written for
// the model. Anything else (runtime faults, closed executor, unknown errors) is reported generically.
var scriptErrors = []error{
	sandbox.ErrScript, sandbox.ErrTimeout, sandbox.ErrMemoryLimit, sandbox.ErrTooManyCalls,
	sandbox.ErrResultTooLarge, sandbox.ErrForbiddenSyntax, sandbox.ErrNotSerializable, sandbox.ErrNeverSettles,
}

// newServer builds the per-request MCP server. Tool descriptions depend on the subject, so the
// server (cheap: schemas come from the shared SchemaCache) is built for every request.
func (h *Handler) newServer(ctx context.Context, subj policy.Subject) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "mcp-gateway", Version: "1"}, &mcp.ServerOptions{
		Logger:                    h.log,
		SchemaCache:               h.schemas,
		SupportedProtocolVersions: []string{protocolVersion},
		SetCacheable:              setCacheable,
	})
	searchDesc, executeDesc := h.tools.ToolDescriptions(ctx, subj)
	mcp.AddTool(srv, &mcp.Tool{Name: toolSearch, Description: searchDesc, Annotations: readOnlyAnnotations()}, h.searchTool(subj))
	mcp.AddTool(srv, &mcp.Tool{Name: toolExecute, Description: executeDesc}, h.executeTool(subj))
	return srv
}

func readOnlyAnnotations() *mcp.ToolAnnotations {
	open := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &open}
}

// setCacheable marks every cacheable result private (it depends on the user) and gives
// tools/list a short TTL so consent changes show up within seconds.
func setCacheable(_ context.Context, req mcp.Request, c *mcp.Cacheable) {
	c.CacheScope = "private"
	if _, ok := req.(*mcp.ListToolsRequest); ok {
		c.TTLMs = int(toolsListTTL.Milliseconds())
	}
}

func (h *Handler) searchTool(subj policy.Subject) mcp.ToolHandlerFor[searchInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		res, err := h.tools.Search(ctx, subj, in.Query)
		if err != nil {
			return h.toolFailure(ctx, toolSearch, err)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: formatSearch(res)}}}, res, nil
	}
}

func (h *Handler) executeTool(subj policy.Subject) mcp.ToolHandlerFor[executeInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in executeInput) (*mcp.CallToolResult, any, error) {
		out, err := h.tools.Execute(ctx, subj, in.Code)
		if err != nil {
			return h.toolFailure(ctx, toolExecute, err)
		}
		if len(out) == 0 {
			out = json.RawMessage("null")
		}
		return nil, out, nil
	}
}

// toolFailure maps a codemode error to a tool result. A canceled request gets no result (the
// client is gone); script errors carry their message; everything else is generic.
func (h *Handler) toolFailure(ctx context.Context, tool string, err error) (*mcp.CallToolResult, any, error) {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return nil, nil, context.Canceled
	}
	if errors.Is(err, codemode.ErrInternal) {
		return errorResult(genericToolError), nil, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorResult(sandbox.ErrTimeout.Error()), nil, nil
	}
	if errors.Is(err, sandbox.ErrBusy) { // global sandbox cap or codemode's per-user cap
		return errorResult(busyToolError), nil, nil
	}
	for _, target := range scriptErrors {
		if errors.Is(err, target) {
			return errorResult(err.Error()), nil, nil
		}
	}
	h.log.Error("mcpserver: unexpected tool error", "tool", tool, "error", err)
	return errorResult(genericToolError), nil, nil
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func formatSearch(res codemode.SearchResult) string {
	if len(res.Tools) == 0 {
		return "No matching tools."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Matching tools (%d of %d):\n", len(res.Tools), res.Total)
	for _, t := range res.Tools {
		fmt.Fprintf(&b, "- %s\n", t)
	}
	b.WriteString("\n")
	b.WriteString(res.Declarations)
	return b.String()
}
