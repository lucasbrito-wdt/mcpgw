package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// newExecuteBenchHandler wires the real handler (origin, auth seam, rate limit, SDK), codemode,
// sandbox, policy, audit and upstream client over a throwaway database and an instant in-process
// fake upstream. Only the OAuth token check is a seam.
func newExecuteBenchHandler(b *testing.B) *Handler {
	b.Helper()
	ctx := context.Background()
	pool := storetest.New(b)

	fake := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	fake.AddTool(&mcp.Tool{Name: "get_item", Description: "Fetch an item", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"id":"1","name":"widget"}`}}}, nil
		})
	up := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return fake },
		&mcp.StreamableHTTPOptions{Stateless: true}))
	b.Cleanup(up.Close)

	check := func(err error) {
		b.Helper()
		if err != nil {
			b.Fatal(err)
		}
	}
	run := func(sql string, args ...any) {
		b.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		check(err)
	}
	var userID, serverID string
	check(pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('u@example.com','h') RETURNING id::text`).Scan(&userID))
	check(pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('acme', $1, 'none') RETURNING id::text`, up.URL).Scan(&serverID))
	run(`INSERT INTO oauth_clients (client_id, registration_kind) VALUES ('c1','dcr')`)
	run(`INSERT INTO tools (server_id, name, description, schema_hash, trust_level, status) VALUES ($1::uuid,'get_item','Fetch an item','h','read','active')`, serverID)
	run(`INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid,$2::uuid,'read')`, userID, serverID)
	run(`INSERT INTO consents (user_id, client_id, server_id, level) VALUES ($1::uuid,'c1',$2::uuid,'read')`, userID, serverID)

	pol, err := policy.New(ctx, pool)
	check(err)
	// Production runs Listen; without it every Decide would query the database.
	listenCtx, stopListen := context.WithCancel(ctx)
	listenDone := make(chan struct{})
	go func() { _ = pol.Listen(listenCtx); close(listenDone) }()
	b.Cleanup(func() { stopListen(); <-listenDone })
	time.Sleep(300 * time.Millisecond) // let the LISTEN connection establish
	rec := audit.New(pool, audit.Options{FlushInterval: 20 * time.Millisecond})
	b.Cleanup(func() { _ = rec.Close(context.Background()) })
	log := slog.New(slog.DiscardHandler)
	svc, err := codemode.New(codemode.Options{
		Pool: pool, Sandbox: sharedSandbox, Policy: pol, Upstream: upstream.New(), Audit: rec,
		PublicURL: testIssuer, Logger: log, MaxConcurrentPerSubject: 1000,
	})
	check(err)
	h, err := newHandler(fakeAuth{claims: oauth.Claims{UserID: userID, ClientID: "c1"}}, svc,
		Options{Logger: log, RateLimit: RateLimit{RPS: 1e6, Burst: 1e6}})
	check(err)
	return h
}

// serveExecute posts one tools/call execute to the handler and fails on any error result.
func serveExecute(b *testing.B, h http.Handler, body string) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Mcp-Protocol-Version", protocolVersion)
	r.Header.Set("Mcp-Method", "tools/call")
	r.Header.Set("Mcp-Name", "execute")
	r.Header.Set("Authorization", "Bearer bench")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	resp, _ := io.ReadAll(w.Result().Body)
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *json.RawMessage `json:"error"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(resp, &rpc) != nil || rpc.Error != nil || rpc.Result.IsError {
		b.Errorf("execute failed: %d %s", w.Code, resp)
	}
}

func executeBody(code string) string {
	args, _ := json.Marshal(map[string]string{"code": code})
	return callBody("execute", string(args))
}

func benchHandlerExecute(b *testing.B, code string) {
	h := newExecuteBenchHandler(b)
	body := executeBody(code)
	serveExecute(b, h, body) // warm caches
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		serveExecute(b, h, body)
	}
}

// BenchmarkHandlerExecuteNoCalls is the full in-process request path with a script that makes no
// tool call: handler + SDK + codemode + sandbox + audit.
func BenchmarkHandlerExecuteNoCalls(b *testing.B) { benchHandlerExecute(b, `async () => 1`) }

// BenchmarkHandlerExecuteOneCall adds one inner call to the fake upstream.
func BenchmarkHandlerExecuteOneCall(b *testing.B) {
	benchHandlerExecute(b, `async () => await tools.acme.get_item({id: "1"})`)
}

func BenchmarkHandlerExecuteOneCallParallel(b *testing.B) {
	h := newExecuteBenchHandler(b)
	body := executeBody(`async () => await tools.acme.get_item({id: "1"})`)
	serveExecute(b, h, body)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			serveExecute(b, h, body)
		}
	})
}
