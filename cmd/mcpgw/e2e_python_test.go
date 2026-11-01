package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox/montyrs"
)

const pythonAllowAndDenyScript = `item = call_tool("fake.get_item", {"id": "42"})
updated = call_tool("fake.update_item", {"id": "42", "name": "renamed"})
denied = None
try:
    call_tool("fake.delete_item", {"id": "42"})
except RuntimeError as e:
    denied = str(e)
{"item": item, "updated": updated, "denied": denied}`

// The whole gateway on the monty engine: OAuth, MCP over HTTP, the confined worker and the upstream.
func TestE2EPythonEngine(t *testing.T) {
	if os.Getenv(montyrs.WorkerPathEnv) == "" {
		t.Skipf("%s not set", montyrs.WorkerPathEnv)
	}
	e := newE2EEnvEngine(t, config.EngineMonty)
	oc := newOAuthClient(t, e.base)
	oc.register()
	resource := e.base + "/mcp"
	p := newPKCE(t)
	params := oc.authorize(p, resource, e.serverID, "write")
	tok := decodeToken(t, oc.exchange(params.Get("code"), p, resource))
	cs := connectMCP(t, e.base, tok.AccessToken)

	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "execute" && !strings.Contains(tool.Description, "call_tool(") {
			t.Fatalf("execute description is not the Python one: %s", tool.Description)
		}
	}

	res := runScript(t, cs, pythonAllowAndDenyScript)
	if res.IsError {
		t.Fatalf("execute failed: %s", resultText(res))
	}
	var out struct {
		Item    json.RawMessage `json:"item"`
		Updated json.RawMessage `json:"updated"`
		Denied  string          `json:"denied"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("result %q: %v", resultText(res), err)
	}
	if !strings.Contains(string(out.Item), "widget") || !strings.Contains(string(out.Updated), "update_item") {
		t.Errorf("script did not see upstream results: item=%s updated=%s", out.Item, out.Updated)
	}
	if !strings.Contains(out.Denied, "denied") {
		t.Errorf("destructive call was not denied: %q", out.Denied)
	}
	if n := e.upstream.count("delete_item"); n != 0 {
		t.Errorf("denied call reached the upstream %d times", n)
	}

	if res := runScript(t, cs, `open("/etc/passwd").read()`); !res.IsError {
		t.Errorf("file access was not refused: %s", resultText(res))
	}
}
