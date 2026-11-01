package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

func seamHandler(t *testing.T, auth TokenValidator, tools ToolService, o Options) *Handler {
	t.Helper()
	h, err := newHandler(auth, tools, o)
	must(t, err)
	return h
}

func TestAuthRejections(t *testing.T) {
	e := newEnv(t, Options{})
	challenge := e.oauth.WWWAuthenticate()

	t.Run("no token", func(t *testing.T) {
		w := post(t, e.handler, rawRequest{body: listBody})
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != challenge {
			t.Fatalf("got %d, WWW-Authenticate %q", w.Code, w.Header().Get("WWW-Authenticate"))
		}
	})
	t.Run("token in query", func(t *testing.T) {
		tok := e.issueToken("read")
		w := post(t, e.handler, rawRequest{body: listBody, query: "?access_token=" + tok})
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != challenge {
			t.Fatalf("got %d, WWW-Authenticate %q", w.Code, w.Header().Get("WWW-Authenticate"))
		}
		w = post(t, e.handler, rawRequest{body: listBody, token: tok, query: "?access_token=" + tok})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("header token plus query token must still be rejected, got %d", w.Code)
		}
	})
	t.Run("bad token", func(t *testing.T) {
		w := post(t, e.handler, rawRequest{body: listBody, token: "not.a.jwt"})
		want := challenge + `, error="invalid_token"`
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != want {
			t.Fatalf("got %d, WWW-Authenticate %q", w.Code, w.Header().Get("WWW-Authenticate"))
		}
	})
	t.Run("non bearer scheme", func(t *testing.T) {
		w := post(t, e.handler, rawRequest{body: listBody, headers: map[string]string{"Authorization": "Basic abc"}})
		if w.Code != http.StatusUnauthorized || !strings.HasSuffix(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
			t.Fatalf("got %d, WWW-Authenticate %q", w.Code, w.Header().Get("WWW-Authenticate"))
		}
	})
}

func TestInfraErrorIs503(t *testing.T) {
	h := seamHandler(t, fakeAuth{err: errors.New("db down")}, fakeTools{}, Options{})
	w := post(t, h, rawRequest{body: listBody, token: "tok"})
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("got %d, %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
}

func TestOriginCheck(t *testing.T) {
	e := newEnv(t, Options{})
	tok := e.issueToken("read")
	for origin, want := range map[string]int{
		"https://evil.example":          http.StatusForbidden,
		"null":                          http.StatusForbidden,
		"http://gw.example.test":        http.StatusForbidden,
		"https://gw.example.test.evil":  http.StatusForbidden,
		"https://GW.example.test":       http.StatusOK,
		"":                              http.StatusOK,
		"https://gw.example.test:8443":  http.StatusForbidden,
		"https://gw.example.test/extra": http.StatusForbidden,
	} {
		headers := map[string]string{"Mcp-Method": "tools/list"}
		if origin != "" {
			headers["Origin"] = origin
		}
		w := post(t, e.handler, rawRequest{body: listBody, token: tok, headers: headers})
		if w.Code != want {
			t.Errorf("Origin %q: got %d, want %d: %s", origin, w.Code, want, bodyOf(w))
		}
	}
	// Origin is checked before authentication.
	w := post(t, e.handler, rawRequest{body: listBody, headers: map[string]string{"Origin": "https://evil.example"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin without token: got %d", w.Code)
	}
}

func TestProtocolAndHeaderValidation(t *testing.T) {
	e := newEnv(t, Options{})
	tok := e.issueToken("read")
	call := callBody("search", `{"query":"item"}`)
	cases := []struct {
		name    string
		body    string
		headers map[string]string
		want    int
	}{
		{"valid list", listBody, map[string]string{"Mcp-Method": "tools/list"}, http.StatusOK},
		{"valid call", call, map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "search"}, http.StatusOK},
		{"missing protocol version", listBody, map[string]string{"Mcp-Protocol-Version": "", "Mcp-Method": "tools/list"}, http.StatusBadRequest},
		{"old protocol version", listBody, map[string]string{"Mcp-Protocol-Version": "2025-06-18", "Mcp-Method": "tools/list"}, http.StatusBadRequest},
		{"missing method header", listBody, nil, http.StatusBadRequest},
		{"method mismatch", listBody, map[string]string{"Mcp-Method": "tools/call"}, http.StatusBadRequest},
		{"missing name header", call, map[string]string{"Mcp-Method": "tools/call"}, http.StatusBadRequest},
		{"name mismatch", call, map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "execute"}, http.StatusBadRequest},
		{"batch bypass", "[" + listBody + "]", map[string]string{"Mcp-Method": "tools/list"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := post(t, e.handler, rawRequest{body: c.body, token: tok, headers: c.headers})
			if w.Code != c.want {
				t.Fatalf("got %d, want %d: %s", w.Code, c.want, bodyOf(w))
			}
		})
	}
}

func TestRateLimit(t *testing.T) {
	var calls int
	auth := fakeAuth{claims: oauth.Claims{UserID: "u1", ClientID: "c1"}}
	h := seamHandler(t, auth, fakeTools{}, Options{RateLimit: RateLimit{RPS: 0.5, Burst: 2}})
	rr := rawRequest{body: listBody, token: "t", headers: map[string]string{"Mcp-Method": "tools/list"}}
	for i := 0; i < 2; i++ {
		if w := post(t, h, rr); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, w.Code, bodyOf(w))
		}
		calls++
	}
	w := post(t, h, rr)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	// Another user has an independent bucket.
	h.auth = fakeAuth{claims: oauth.Claims{UserID: "u2", ClientID: "c1"}}
	if w := post(t, h, rr); w.Code != http.StatusOK {
		t.Fatalf("other user: %d", w.Code)
	}
}

func TestBodyTooLarge(t *testing.T) {
	auth := fakeAuth{claims: oauth.Claims{UserID: "u1", ClientID: "c1"}}
	h := seamHandler(t, auth, fakeTools{}, Options{MaxBodyBytes: 512})
	big := callBody("execute", fmt.Sprintf(`{"code":%q}`, strings.Repeat("a", 2048)))
	w := post(t, h, rawRequest{body: big, token: "t", headers: map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "execute"}})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d: %s", w.Code, bodyOf(w))
	}
}

func TestToolsListSubjectSpecific(t *testing.T) {
	e := newEnv(t, Options{})
	granted := e.connect(e.issueToken("read"))
	none := e.connect(e.issueToken("none"))

	list := func(cs *mcp.ClientSession) map[string]string {
		res, err := cs.ListTools(context.Background(), nil)
		must(t, err)
		out := map[string]string{}
		for _, tool := range res.Tools {
			out[tool.Name] = tool.Description
		}
		return out
	}
	g, n := list(granted), list(none)
	if len(g) != 2 || g["search"] == "" || g["execute"] == "" {
		t.Fatalf("tools: %v", g)
	}
	if !strings.Contains(g["execute"], "acme") || strings.Contains(n["execute"], "acme") || !strings.Contains(n["execute"], "none") {
		t.Fatalf("descriptions not subject specific:\ngranted: %s\nnone: %s", g["execute"], n["execute"])
	}
}

func TestToolsListCacheHints(t *testing.T) {
	e := newEnv(t, Options{})
	w := post(t, e.handler, rawRequest{body: listBody, token: e.issueToken("read"), headers: map[string]string{"Mcp-Method": "tools/list"}})
	var resp struct {
		Result struct {
			TTLMs      int    `json:"ttlMs"`
			CacheScope string `json:"cacheScope"`
		} `json:"result"`
	}
	must(t, json.Unmarshal([]byte(bodyOf(w)), &resp))
	if resp.Result.CacheScope != "private" || resp.Result.TTLMs != int(toolsListTTL.Milliseconds()) {
		t.Fatalf("cache hints: %+v (%s)", resp.Result, w.Body.String())
	}
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	must(t, err)
	return res
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestSearchAndExecuteEndToEnd(t *testing.T) {
	e := newEnv(t, Options{})
	cs := e.connect(e.issueToken("read"))

	res := callTool(t, cs, "search", map[string]any{"query": "item"})
	if res.IsError || !strings.Contains(textOf(res), "acme.get_item") {
		t.Fatalf("search: %+v %s", res, textOf(res))
	}
	sc, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(sc), `"tools":["acme.get_item"]`) {
		t.Fatalf("search structuredContent: %s", sc)
	}

	res = callTool(t, cs, "execute", map[string]any{"code": `async () => { return await tools.acme.get_item({id: "42"}); }`})
	if res.IsError {
		t.Fatalf("execute: %s", textOf(res))
	}
	sc, _ = json.Marshal(res.StructuredContent)
	if !strings.Contains(string(sc), `"widget"`) || !strings.Contains(string(sc), `"42"`) || !strings.Contains(textOf(res), "widget") {
		t.Fatalf("execute result: structured %s text %s", sc, textOf(res))
	}

	var n int
	must(t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM executions WHERE user_id = $1::uuid`, e.userID).Scan(&n))
	if n != 2 {
		t.Fatalf("audited executions: %d", n)
	}
}

func TestExecuteWithoutConsentIsDenied(t *testing.T) {
	e := newEnv(t, Options{})
	cs := e.connect(e.issueToken("none"))
	res := callTool(t, cs, "execute", map[string]any{"code": `async () => await tools.acme.get_item({id: "1"})`})
	if !res.IsError {
		t.Fatalf("expected isError, got %s", textOf(res))
	}
	res = callTool(t, cs, "search", map[string]any{"query": "item"})
	if res.IsError || strings.Contains(textOf(res), "acme.get_item") {
		t.Fatalf("search leaked a tool: %s", textOf(res))
	}
}

func TestExecuteScriptErrorIsToolError(t *testing.T) {
	e := newEnv(t, Options{})
	cs := e.connect(e.issueToken("read"))
	res := callTool(t, cs, "execute", map[string]any{"code": `async () => { throw new Error("kaboom"); }`})
	if !res.IsError || !strings.Contains(textOf(res), "kaboom") {
		t.Fatalf("got isError=%v %q", res.IsError, textOf(res))
	}
	res = callTool(t, cs, "execute", map[string]any{"code": `async () => { import("x"); }`})
	if !res.IsError {
		t.Fatalf("forbidden syntax must be isError, got %q", textOf(res))
	}
}

func TestToolInputValidation(t *testing.T) {
	e := newEnv(t, Options{})
	cs := e.connect(e.issueToken("read"))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "execute", Arguments: map[string]any{"cod": "x"}})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("missing code must fail: %+v", res)
	}
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "nope", Arguments: map[string]any{}}); err == nil {
		t.Fatal("unknown tool must fail")
	}
}

func TestFailureMapping(t *testing.T) {
	auth := fakeAuth{claims: oauth.Claims{UserID: "u1", ClientID: "c1"}}
	run := func(tools fakeTools) *mcp.CallToolResult {
		h := seamHandler(t, auth, tools, Options{})
		hc := &http.Client{Transport: handlerTransport{h}}
		ct := &mcp.StreamableClientTransport{Endpoint: "http://gw.test/mcp", HTTPClient: hc, DisableStandaloneSSE: true}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).
			Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
		must(t, err)
		defer cs.Close()
		return callTool(t, cs, "execute", map[string]any{"code": "x"})
	}

	res := run(fakeTools{executeErr: fmt.Errorf("%w: pg: connection refused to 10.0.0.5", codemode.ErrInternal)})
	if !res.IsError || textOf(res) != genericToolError {
		t.Fatalf("ErrInternal: isError=%v %q", res.IsError, textOf(res))
	}
	res = run(fakeTools{executeErr: errors.New("pgx: password authentication failed")})
	if !res.IsError || textOf(res) != genericToolError {
		t.Fatalf("unknown error: isError=%v %q", res.IsError, textOf(res))
	}
	res = run(fakeTools{executeErr: fmt.Errorf("%w: boom", sandbox.ErrScript)})
	if !res.IsError || !strings.Contains(textOf(res), "boom") {
		t.Fatalf("script error: isError=%v %q", res.IsError, textOf(res))
	}
	for _, busy := range []error{sandbox.ErrBusy, fmt.Errorf("%w: too many scripts of yours are already running", sandbox.ErrBusy)} {
		res = run(fakeTools{executeErr: busy})
		if !res.IsError || textOf(res) != busyToolError {
			t.Fatalf("busy must tell the model to wait and retry: isError=%v %q", res.IsError, textOf(res))
		}
	}
	res = run(fakeTools{executeErr: sandbox.ErrRuntimeFault})
	if !res.IsError || textOf(res) != genericToolError {
		t.Fatalf("runtime fault must be generic: %q", textOf(res))
	}
}

func TestCanceledYieldsNoResult(t *testing.T) {
	h := seamHandler(t, fakeAuth{}, fakeTools{executeErr: context.Canceled}, Options{})
	res, out, err := h.toolFailure(context.Background(), toolExecute, fmt.Errorf("run: %w", context.Canceled))
	if res != nil || out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v %v %v", res, out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res, _, err := h.toolFailure(ctx, toolExecute, errors.New("other")); res != nil || err == nil {
		t.Fatal("canceled context must not produce a result")
	}
}

func TestSubjectFromClaims(t *testing.T) {
	var got policy.Subject
	tools := fakeTools{desc: func(s policy.Subject) (string, string) { got = s; return "s", "e" }}
	h := seamHandler(t, fakeAuth{claims: oauth.Claims{UserID: "user-1", ClientID: "client-1"}}, tools, Options{})
	w := post(t, h, rawRequest{body: listBody, token: "t", headers: map[string]string{"Mcp-Method": "tools/list"}})
	if w.Code != http.StatusOK || got != (policy.Subject{UserID: "user-1", ClientID: "client-1"}) {
		t.Fatalf("%d subject %+v", w.Code, got)
	}
}

func TestGetIsRejectedStateless(t *testing.T) {
	h := seamHandler(t, fakeAuth{claims: oauth.Claims{UserID: "u"}}, fakeTools{}, Options{})
	r := newGet()
	w := newRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: got %d", w.Code)
	}
}
