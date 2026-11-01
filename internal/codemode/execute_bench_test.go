package codemode_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// benchService is a codemode.Service over a throwaway database and an in-process fake upstream
// that answers instantly, so what is measured is the gateway: sandbox, policy, audit, and the
// upstream client (a new MCP session per call today).
type benchService struct {
	svc         *codemode.Service
	subj        policy.Subject
	upstreamURL string
}

func newBenchService(b *testing.B) *benchService {
	b.Helper()
	ctx := context.Background()
	pool := storetest.New(b)

	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "get_item", Description: "Fetch an item", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"id":"1","name":"widget"}`}}}, nil
		})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true}))
	b.Cleanup(srv.Close)

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
	check(pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('acme', $1, 'none') RETURNING id::text`, srv.URL).Scan(&serverID))
	run(`INSERT INTO oauth_clients (client_id, registration_kind) VALUES ($1,'dcr')`, clientID)
	run(`INSERT INTO tools (server_id, name, description, schema_hash, trust_level, status) VALUES ($1::uuid,'get_item','Fetch an item','h','read','active')`, serverID)
	run(`INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid,$2::uuid,'read')`, userID, serverID)
	run(`INSERT INTO consents (user_id, client_id, server_id, level) VALUES ($1::uuid,$2,$3::uuid,'read')`, userID, clientID, serverID)

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
	svc, err := codemode.New(codemode.Options{
		Pool: pool, Sandbox: sharedSandbox, Policy: pol, Upstream: upstream.New(),
		Audit: rec, PublicURL: "https://gw.example.com/", MaxConcurrentPerSubject: 1000,
	})
	check(err)
	return &benchService{svc: svc, subj: policy.Subject{UserID: userID, ClientID: clientID}, upstreamURL: srv.URL}
}

func (s *benchService) execute(b *testing.B, code string) {
	if _, err := s.svc.Execute(context.Background(), s.subj, code); err != nil {
		b.Error(err)
	}
}

func benchExecute(b *testing.B, code string) {
	s := newBenchService(b)
	s.execute(b, code) // warm the catalog snapshot and policy caches
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.execute(b, code)
	}
}

// BenchmarkExecuteNoCalls is the fixed overhead of one execution: sandbox instance plus the
// execution audit rows. No bridge call happens.
func BenchmarkExecuteNoCalls(b *testing.B) { benchExecute(b, `async () => 1`) }

// BenchmarkExecuteOneCall adds one tools.call through bridge, policy, upstream client and audit.
// Subtract BenchmarkExecuteNoCalls for the inner-call overhead.
func BenchmarkExecuteOneCall(b *testing.B) {
	benchExecute(b, `async () => await tools.acme.get_item({id: "1"})`)
}

func BenchmarkExecuteTenSequentialCalls(b *testing.B) {
	benchExecute(b, `async () => { let n = 0; for (let i = 0; i < 10; i++) { await tools.acme.get_item({id: String(i)}); n++; } return n; }`)
}

func BenchmarkExecuteOneCallParallel(b *testing.B) {
	s := newBenchService(b)
	code := `async () => await tools.acme.get_item({id: "1"})`
	s.execute(b, code)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.execute(b, code)
		}
	})
}

// BenchmarkUpstreamCallOnly is the floor for an inner call: the upstream client straight to the
// fake, with no sandbox, policy or audit.
func BenchmarkUpstreamCallOnly(b *testing.B) {
	s := newBenchService(b)
	cl := upstream.New()
	args := []byte(`{"id":"1"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cl.CallTool(context.Background(), s.upstreamURL, nil, "get_item", args); err != nil {
			b.Fatal(fmt.Errorf("call: %w", err))
		}
	}
}

// BenchmarkExecuteTenSequentialCallsParallel is the load-test shape (many users, a script making
// several inner calls) in-process, for CPU, block and mutex profiles.
func BenchmarkExecuteTenSequentialCallsParallel(b *testing.B) {
	s := newBenchService(b)
	code := `async () => { let n = 0; for (let i = 0; i < 10; i++) { await tools.acme.get_item({id: String(i)}); n++; } return n; }`
	s.execute(b, code)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.execute(b, code)
		}
	})
}
