package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// allowAndDenyScript calls a read tool and a write tool (allowed at consent "write") and a
	// destructive tool (above the effective level), returning what the script saw.
	allowAndDenyScript = `async () => {
  const item = await tools.fake.get_item({ id: "42" });
  const updated = await tools.fake.update_item({ id: "42", name: "renamed" });
  let denied = null;
  try { await tools.fake.delete_item({ id: "42" }); } catch (e) { denied = String(e.message); }
  return { item, updated, denied };
}`

	// tryUpdateScript reports whether update_item was allowed or the denial message.
	tryUpdateScript = `async () => {
  try { await tools.fake.update_item({ id: "7", name: "again" }); return "allowed"; }
  catch (e) { return String(e.message); }
}`

	importScript = `async () => { const m = await import("node:fs"); return m; }`

	// callCapScript makes 51 sequential allowed calls; the default cap is 50 per execution.
	callCapScript = `async () => {
  for (let i = 0; i < 51; i++) { await tools.fake.get_item({ id: String(i) }); }
  return "done";
}`
)

// TestE2EPhase1 proves the phase-1 done criteria of docs/PLAN.md: a client completes OAuth and
// consent against the gateway, an execute script calls tools of an upstream MCP, calls above the
// effective trust level are denied, and every inner call and the script are in the audit tables.
// The steps share one gateway and run in order.
func TestE2EPhase1(t *testing.T) {
	e := newE2EEnv(t)
	oc := newOAuthClient(t, e.base)
	var token string
	parent := t

	step := func(name string, fn func(t *testing.T)) {
		t.Helper()
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}

	step("catalog classifies the fake upstream tools", func(t *testing.T) {
		out := e.admin("tool list --slug " + e2eServerSlug)
		for tool, level := range map[string]string{"get_item": "read", "update_item": "write", "delete_item": "destructive"} {
			if !hasRow(out, tool, level) {
				t.Errorf("tool %s is not classified %s:\n%s", tool, level, out)
			}
		}
	})

	step("oauth: DCR, PKCE S256, login, consent write, code exchange", func(t *testing.T) {
		oc.register()
		resource := e.base + "/mcp"
		p := newPKCE(t)
		params := oc.authorize(p, resource, e.serverID, "write")
		if params.Get("iss") != e.base {
			t.Errorf("redirect iss = %q, want %q (RFC 9207)", params.Get("iss"), e.base)
		}
		if params.Get("state") != "e2e-state" || params.Get("code") == "" {
			t.Fatalf("redirect params: %v", params)
		}
		reply := oc.exchange(params.Get("code"), p, resource)
		if reply.status != http.StatusOK {
			t.Fatalf("token exchange: %d %s", reply.status, reply.body)
		}
		tok := decodeToken(t, reply)
		if tok.TokenType != "Bearer" || tok.Scope != "mcp" || tok.AccessToken == "" {
			t.Fatalf("token response: %+v", tok)
		}
		token = tok.AccessToken
		var level string
		err := e.db.pool.QueryRow(context.Background(), `SELECT level FROM consents WHERE user_id = $1::uuid AND server_id = $2::uuid`,
			e.userID, e.serverID).Scan(&level)
		if err != nil || level != "write" {
			t.Errorf("stored consent = %q (%v), want write", level, err)
		}
	})

	cs := (*mcp.ClientSession)(nil)
	step("mcp: bearer works, tools/list and search are filtered by consent", func(t *testing.T) {
		if reply := rawMCPPost(t, e.base, token); reply.status != http.StatusOK {
			t.Fatalf("raw JSON-RPC with required headers: %d %s", reply.status, reply.body)
		}
		cs = connectMCP(parent, e.base, token) // closed with the parent test, not this step
		listed, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]string{}
		for _, tool := range listed.Tools {
			names[tool.Name] = tool.Description
		}
		if len(names) != 2 || names["search"] == "" || !strings.Contains(names["execute"], e2eServerSlug) {
			t.Fatalf("tools/list = %v", names)
		}
		found := searchTools(t, cs)
		if !found["fake.get_item"] || !found["fake.update_item"] {
			t.Errorf("search at write misses allowed tools: %v", found)
		}
		if found["fake.delete_item"] {
			t.Errorf("search at write leaks the destructive tool: %v", found)
		}
	})

	step("execute: allowed calls succeed, destructive call is denied before the upstream", func(t *testing.T) {
		res := runScript(t, cs, allowAndDenyScript)
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
		if !strings.Contains(out.Denied, "denied") || !strings.Contains(out.Denied, "level_exceeded") {
			t.Errorf("destructive call was not denied with level_exceeded: %q", out.Denied)
		}
		if got, want := e.upstream.received(), []string{"get_item", "update_item"}; !reflect.DeepEqual(got, want) {
			t.Errorf("upstream received %v, want %v", got, want)
		}
	})

	step("consent lowered to read: NOTIFY invalidation denies update_item", func(t *testing.T) {
		e.exec(`UPDATE consents SET level = 'read' WHERE user_id = $1::uuid AND server_id = $2::uuid`, e.userID, e.serverID)
		eventually(t, "update_item to disappear from search", func() bool { return !searchTools(t, cs)["fake.update_item"] })
		if msg := scriptString(t, runScript(t, cs, tryUpdateScript)); !strings.Contains(msg, "level_exceeded") {
			t.Fatalf("update_item after consent=read: %q", msg)
		}
		if n := e.upstream.count("update_item"); n != 1 {
			t.Errorf("update_item reached the upstream %d times, want 1 (only before the downgrade)", n)
		}
	})

	step("consent restored then ceiling lowered to read", func(t *testing.T) {
		e.exec(`UPDATE consents SET level = 'write' WHERE user_id = $1::uuid AND server_id = $2::uuid`, e.userID, e.serverID)
		eventually(t, "update_item to reappear in search", func() bool { return searchTools(t, cs)["fake.update_item"] })
		if msg := scriptString(t, runScript(t, cs, tryUpdateScript)); msg != "allowed" {
			t.Fatalf("update_item after consent restored: %q", msg)
		}
		e.admin("ceiling set --email " + e2eEmail + " --slug " + e2eServerSlug + " --level read")
		eventually(t, "update_item to vanish after ceiling=read", func() bool { return !searchTools(t, cs)["fake.update_item"] })
		if msg := scriptString(t, runScript(t, cs, tryUpdateScript)); !strings.Contains(msg, "level_exceeded") {
			t.Fatalf("update_item after ceiling=read: %q", msg)
		}
		if n := e.upstream.count("update_item"); n != 2 {
			t.Errorf("update_item reached the upstream %d times, want 2", n)
		}
	})

	step("audit: executions and per-call events", func(t *testing.T) {
		assertAudit(t, e, allowAndDenyScript, "succeeded", 3, []auditRow{
			{"delete_item", "deny", "level_exceeded", ""},
			{"get_item", "allow", "allowed", "outcome"},
			{"update_item", "allow", "allowed", "pre_forward"},
			{"update_item", "allow", "allowed", "outcome"},
		})
		assertAudit(t, e, tryUpdateScript, "succeeded", 0, nil) // ran three times; events checked below
		assertTryUpdateEvents(t, e)
		var searches int
		if err := e.db.pool.QueryRow(context.Background(), `SELECT count(*) FROM executions WHERE kind = 'search' AND user_id = $1::uuid`, e.userID).Scan(&searches); err != nil || searches < 3 {
			t.Errorf("search executions = %d (%v), want at least 3", searches, err)
		}
	})

	step("negative: missing, tampered and wrong-resource tokens", func(t *testing.T) {
		missing := rawMCPPost(t, e.base, "")
		if missing.status != http.StatusUnauthorized || !strings.Contains(missing.header.Get("WWW-Authenticate"), "resource_metadata=") {
			t.Errorf("no token: %d %q", missing.status, missing.header.Get("WWW-Authenticate"))
		}
		for name, bad := range map[string]string{
			"payload audience rewritten": rewriteAudience(t, token, "https://other.example/mcp"),
			"signature tampered":         tamperSignature(t, token),
			"garbage":                    "not-a-jwt",
		} {
			reply := rawMCPPost(t, e.base, bad)
			if reply.status != http.StatusUnauthorized || !strings.Contains(reply.header.Get("WWW-Authenticate"), `error="invalid_token"`) {
				t.Errorf("%s: %d %q", name, reply.status, reply.header.Get("WWW-Authenticate"))
			}
		}
		wrong := "https://other.example/mcp"
		p := newPKCE(t)
		resp := oc.get("/authorize?" + oc.authorizeQuery(p, wrong).Encode())
		loc, _ := url.Parse(resp.header.Get("Location"))
		if resp.status != http.StatusFound || loc == nil || loc.Query().Get("error") != "invalid_target" || loc.Query().Get("iss") != e.base {
			t.Errorf("authorize with wrong resource: %d %q", resp.status, resp.header.Get("Location"))
		}
		p = newPKCE(t)
		params := oc.authorize(p, e.base+"/mcp", e.serverID, "read")
		if reply := oc.exchange(params.Get("code"), p, wrong); reply.status != http.StatusBadRequest || decodeToken(t, reply).Error != "invalid_target" {
			t.Errorf("token with wrong resource: %d %s", reply.status, reply.body)
		}
		p = newPKCE(t)
		params = oc.authorize(p, e.base+"/mcp", e.serverID, "read")
		second := decodeToken(t, oc.exchange(params.Get("code"), p, e.base+"/mcp"))
		if reply := oc.exchange(params.Get("code"), p, e.base+"/mcp"); reply.status != http.StatusBadRequest {
			t.Errorf("authorization code reuse = %d, want 400", reply.status)
		}
		if reply := rawMCPPost(t, e.base, second.AccessToken); reply.status != http.StatusUnauthorized {
			t.Errorf("token of a grant revoked by code reuse: %d, want 401", reply.status)
		}
		// The first valid token must still work: none of the above revoked it.
		if reply := rawMCPPost(t, e.base, token); reply.status != http.StatusOK {
			t.Errorf("valid token after negative checks: %d", reply.status)
		}
	})

	step("negative: import and call cap fail the script", func(t *testing.T) {
		before := e.upstream.count("get_item")
		res := runScript(t, cs, importScript)
		if !res.IsError || !strings.Contains(strings.ToLower(resultText(res)), "import") {
			t.Errorf("import script: isError=%v %q", res.IsError, resultText(res))
		}
		if e.upstream.count("get_item") != before {
			t.Error("rejected script reached the upstream")
		}
		// Reads are allowed at ceiling=read, so the cap (not policy) is what stops call 51.
		res = runScript(t, cs, callCapScript)
		if !res.IsError {
			t.Fatalf("call cap script succeeded: %s", resultText(res))
		}
		if got := e.upstream.count("get_item") - before; got != 50 {
			t.Errorf("upstream saw %d reads from the capped script, want exactly 50", got)
		}
		assertExecutionStatus(t, e, importScript, "failed")
		assertExecutionStatus(t, e, callCapScript, "failed")
	})
}

// searchTools returns the qualified tool names the search tool lists for the connected user.
func searchTools(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	text := resultText(callTool(t, cs, "search", map[string]any{"query": "item"}))
	found := map[string]bool{}
	for _, name := range []string{"fake.get_item", "fake.update_item", "fake.delete_item"} {
		// Matching lines look like "- fake.get_item"; declarations also mention the names, so anchor on the list.
		found[name] = strings.Contains(text, "- "+name+"\n") || strings.HasSuffix(strings.SplitN(text, "\n\n", 2)[0], "- "+name)
	}
	return found
}

// scriptString decodes a script result that is a JSON string.
func scriptString(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res.IsError {
		t.Fatalf("execute failed: %s", resultText(res))
	}
	var s string
	if err := json.Unmarshal([]byte(resultText(res)), &s); err != nil {
		t.Fatalf("result %q is not a JSON string: %v", resultText(res), err)
	}
	return s
}

func hasRow(table, first, second string) bool {
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[1] == first && f[2] == second {
			return true
		}
	}
	return false
}

// auditRow is one audit event. Phase tells the two rows of an allowed write apart: "pre_forward"
// is the synchronous row written before the upstream call (result_bytes 0) and "outcome" the
// asynchronous one after it (result_bytes > 0). Denials have no phase.
type auditRow struct{ Tool, Decision, Reason, Phase string }

const auditRowsSQL = `SELECT a.tool_name, a.decision, a.reason,
	CASE WHEN a.decision <> 'allow' THEN '' WHEN a.result_bytes = 0 THEN 'pre_forward' ELSE 'outcome' END
	FROM audit_events a`

// assertAudit checks the executions row of the script with this code (hash, status, inner calls)
// and, when want is not nil, its audit_events. Read events are batched, so it polls.
func assertAudit(t *testing.T, e *e2eEnv, code, status string, innerCalls int, want []auditRow) {
	t.Helper()
	sum := sha256.Sum256([]byte(code))
	var id, gotStatus, gotCode, gotHash string
	var gotCalls int
	row := func() error {
		return e.db.pool.QueryRow(context.Background(), `SELECT id::text, code, code_hash, status, inner_calls FROM executions
			WHERE kind = 'execute' AND code = $1 AND user_id = $2::uuid ORDER BY started_at LIMIT 1`, code, e.userID).
			Scan(&id, &gotCode, &gotHash, &gotStatus, &gotCalls)
	}
	if err := row(); err != nil {
		t.Fatalf("executions row for script: %v", err)
	}
	if gotCode != code || gotHash != hex.EncodeToString(sum[:]) || gotStatus != status {
		t.Errorf("execution: hash %s status %s, want hash %s status %s", gotHash, gotStatus, hex.EncodeToString(sum[:]), status)
	}
	if want == nil {
		return
	}
	if gotCalls != innerCalls {
		t.Errorf("inner_calls = %d, want %d", gotCalls, innerCalls)
	}
	var got []auditRow
	eventually(t, "audit events of the script", func() bool {
		got = auditRows(t, e, auditRowsSQL+` WHERE a.execution_id = $1::uuid ORDER BY a.tool_name, a.result_bytes`, id)
		return len(got) >= len(want)
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audit events = %v, want %v", got, want)
	}
	assertEventMetadata(t, e, id)
}

func auditRows(t *testing.T, e *e2eEnv, sql string, args ...any) []auditRow {
	t.Helper()
	rows, err := e.db.pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.Tool, &r.Decision, &r.Reason, &r.Phase); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertEventMetadata checks the fields every event must carry besides the decision.
func assertEventMetadata(t *testing.T, e *e2eEnv, executionID string) {
	t.Helper()
	var bad int
	err := e.db.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events
		WHERE execution_id = $1::uuid AND (server_slug <> $2 OR user_id <> $3::uuid OR args_hash = '' OR client_id = '')`,
		executionID, e2eServerSlug, e.userID).Scan(&bad)
	if err != nil || bad != 0 {
		t.Errorf("%d audit events miss server/user/client/args_hash (%v)", bad, err)
	}
}

// assertTryUpdateEvents covers the three runs of tryUpdateScript: allowed once (two rows: the
// pre-forward row and the outcome row), denied twice.
func assertTryUpdateEvents(t *testing.T, e *e2eEnv) {
	t.Helper()
	var got []auditRow
	query := auditRowsSQL + ` JOIN executions x ON x.id = a.execution_id
		WHERE x.code = $1 AND x.user_id = $2::uuid ORDER BY x.started_at, a.result_bytes`
	eventually(t, "audit events of tryUpdateScript", func() bool {
		got = auditRows(t, e, query, tryUpdateScript, e.userID)
		return len(got) >= 4
	})
	want := []auditRow{
		{"update_item", "deny", "level_exceeded", ""},
		{"update_item", "allow", "allowed", "pre_forward"},
		{"update_item", "allow", "allowed", "outcome"},
		{"update_item", "deny", "level_exceeded", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tryUpdateScript events = %v, want %v", got, want)
	}
}

func assertExecutionStatus(t *testing.T, e *e2eEnv, code, status string) {
	t.Helper()
	var got string
	err := e.db.pool.QueryRow(context.Background(), `SELECT status FROM executions WHERE code = $1 AND user_id = $2::uuid`, code, e.userID).Scan(&got)
	if err != nil || got != status {
		t.Errorf("execution status = %q (%v), want %q", got, err, status)
	}
}

// rewriteAudience keeps the signature but changes the aud claim.
func rewriteAudience(t *testing.T, token, aud string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("access token is not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	claims["aud"] = aud
	raw, _ = json.Marshal(claims)
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join(parts, ".")
}

// tamperSignature replaces the first character of the signature.
func tamperSignature(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[2] == "" {
		t.Fatalf("access token is not a JWT: %q", token)
	}
	first := "A"
	if parts[2][0] == 'A' {
		first = "B"
	}
	parts[2] = first + parts[2][1:]
	return strings.Join(parts, ".")
}
