package codemode_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox/montyrs"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

const (
	canary   = "canary-token-0123456789"
	clientID = "client-1"
)

var (
	sharedSandbox *sandbox.Executor
	sharedMonty   *montyrs.Executor // nil unless MCPGW_MONTYRS_WORKER is set
)

func TestMain(m *testing.M) {
	exec, err := sandbox.NewExecutor()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
	sharedSandbox = exec
	if path := os.Getenv(montyrs.WorkerPathEnv); path != "" {
		if sharedMonty, err = montyrs.NewExecutor(montyrs.WithWorkerPath(path)); err != nil {
			fmt.Fprintln(os.Stderr, "montyrs:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = exec.Close()
	if sharedMonty != nil {
		_ = sharedMonty.Close()
	}
	os.Exit(code)
}

type itemArgs struct {
	ID string `json:"id"`
}

type authKey struct{}

// fakeUpstream is an MCP server over httptest with a few tools.
type fakeUpstream struct {
	srv *httptest.Server

	mu          sync.Mutex
	auths       []string
	createCheck func() // runs inside the create_item handler
	blocked     atomic.Int32
	release     chan struct{}
	concurrent  atomic.Int32
	maxSeen     atomic.Int32
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{release: make(chan struct{})}
	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	text := func(body string, isErr bool) *mcp.CallToolResult {
		return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: body}}}
	}
	raw := map[string]any{"type": "object"}
	add := func(name, desc string, h func(ctx context.Context, args map[string]any) *mcp.CallToolResult) {
		s.AddTool(&mcp.Tool{Name: name, Description: desc, InputSchema: raw},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args map[string]any
				_ = json.Unmarshal(req.Params.Arguments, &args)
				return h(ctx, args), nil
			})
	}
	add("get_item", "Fetch an item by id", func(_ context.Context, a map[string]any) *mcp.CallToolResult {
		f.concurrent.Add(1)
		defer f.concurrent.Add(-1)
		time.Sleep(50 * time.Millisecond)
		if n := f.concurrent.Load(); n > f.maxSeen.Load() {
			f.maxSeen.Store(n)
		}
		return text(fmt.Sprintf(`{"id":%q,"name":"widget"}`, a["id"]), false)
	})
	add("get-thing", "Fetch a thing", func(context.Context, map[string]any) *mcp.CallToolResult { return text(`{"thing":1}`, false) })
	add("create_item", "Create an item", func(context.Context, map[string]any) *mcp.CallToolResult {
		f.mu.Lock()
		check := f.createCheck
		f.mu.Unlock()
		if check != nil {
			check()
		}
		return text(`{"created":true}`, false)
	})
	add("fail_write", "Write that fails", func(context.Context, map[string]any) *mcp.CallToolResult {
		return text("write failed", true)
	})
	add("nul_error", "Error containing a NUL byte", func(context.Context, map[string]any) *mcp.CallToolResult {
		return text("bad\x00 thing", true)
	})
	add("block", "Parks until released", func(ctx context.Context, _ map[string]any) *mcp.CallToolResult {
		f.blocked.Add(1)
		select {
		case <-f.release:
		case <-ctx.Done():
		}
		return text(`{}`, false)
	})
	add("delete_item", "Delete an item", func(context.Context, map[string]any) *mcp.CallToolResult { return text(`{"deleted":true}`, false) })
	add("hidden_tool", "Quarantined tool", func(context.Context, map[string]any) *mcp.CallToolResult { return text(`{}`, false) })
	add("leak_result", "Echo the credential in a result", func(ctx context.Context, _ map[string]any) *mcp.CallToolResult {
		return text(fmt.Sprintf(`{"auth":%q}`, ctx.Value(authKey{})), false)
	})
	add("leak_error", "Echo the credential in an error", func(ctx context.Context, _ map[string]any) *mcp.CallToolResult {
		return text("bad request, header was "+fmt.Sprint(ctx.Value(authKey{})), true)
	})
	echo := func(_ context.Context, a map[string]any) *mcp.CallToolResult {
		b, _ := json.Marshal(a)
		return text(string(b), false)
	}
	add("echo_args", "Echo the arguments", echo)
	add("echo_profile", "Echo the arguments of a tool with its own profile property", echo)
	add("fail", "Always fails", func(context.Context, map[string]any) *mcp.CallToolResult {
		return text("boom "+strings.Repeat("e", 1000), true)
	})

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		f.mu.Lock()
		f.auths = append(f.auths, auth)
		f.mu.Unlock()
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authKey{}, auth)))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type fakeCreds struct {
	mu   sync.Mutex
	cred upstream.Credential
	err  error
	seen []string // server ids requested
}

func (c *fakeCreds) ProfileCredentialFor(_ context.Context, serverID, _, _ string) (upstream.Credential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, serverID)
	return c.cred, c.err
}

func (c *fakeCreds) Profiles(context.Context, string, string) ([]string, error) {
	return []string{}, nil
}

type notConnectedErr struct{}

func (notConnectedErr) Error() string      { return "no credential row" }
func (notConnectedErr) NotConnected() bool { return true }

type fixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	svc      *codemode.Service
	pol      *policy.Engine
	rec      *audit.Recorder
	up       *fakeUpstream
	creds    *fakeCreds
	subj     policy.Subject
	serverID string
}

type setup struct {
	consent    string // consent level, "" for none
	limits     sandbox.Limits
	perSubject int            // codemode.Options.MaxConcurrentPerSubject
	python     bool           // run on the montyrs engine; skips the test when no worker is configured
	runner     sandbox.Runner // overrides the engine when set
}

func newFixture(t *testing.T, s setup) *fixture {
	t.Helper()
	if s.python && sharedMonty == nil {
		t.Skipf("%s not set", montyrs.WorkerPathEnv)
	}
	ctx := context.Background()
	pool := storetest.New(t)
	up := newFakeUpstream(t)
	f := &fixture{t: t, pool: pool, up: up, creds: &fakeCreds{cred: upstream.BearerToken(canary)}}

	var userID string
	must(t, pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('u@example.com','h') RETURNING id::text`).Scan(&userID))
	must(t, pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('acme', $1, 'oauth') RETURNING id::text`, up.srv.URL).Scan(&f.serverID))
	exec(t, pool, `INSERT INTO oauth_clients (client_id, registration_kind) VALUES ($1,'dcr')`, clientID)
	for _, tl := range []struct{ name, level, status string }{
		{"get_item", "read", "active"}, {"get-thing", "read", "active"}, {"create_item", "write", "active"},
		{"delete_item", "destructive", "active"}, {"hidden_tool", "read", "quarantined"},
		{"leak_result", "read", "active"}, {"leak_error", "read", "active"}, {"fail", "read", "active"},
	} {
		exec(t, pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level, status)
			VALUES ($1::uuid, $2, $2 || ' description', 'h', $3, $4)`, f.serverID, tl.name, tl.level, tl.status)
	}
	exec(t, pool, `INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid,$2::uuid,'destructive')`, userID, f.serverID)
	if s.consent != "" {
		exec(t, pool, `INSERT INTO consents (user_id, client_id, server_id, level) VALUES ($1::uuid,$2,$3::uuid,$4)`,
			userID, clientID, f.serverID, s.consent)
	}
	// A second server the user has no ceiling for: its tools must never show up.
	var otherID string
	must(t, pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('other', $1, 'none') RETURNING id::text`, up.srv.URL).Scan(&otherID))
	exec(t, pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level) VALUES ($1::uuid,'secret_report','Fetch the secret report','h','read')`, otherID)

	pol, err := policy.New(ctx, pool)
	must(t, err)
	f.pol = pol
	f.rec = audit.New(pool, audit.Options{FlushInterval: 20 * time.Millisecond})
	t.Cleanup(func() { _ = f.rec.Close(context.Background()) })
	var runner sandbox.Runner = sharedSandbox
	if s.python {
		runner = sharedMonty
	}
	if s.runner != nil {
		runner = s.runner
	}
	f.svc, err = codemode.New(codemode.Options{
		Pool: pool, Sandbox: runner, Python: s.python, Policy: pol, Upstream: upstream.New(), Credentials: f.creds,
		Audit: f.rec, PublicURL: "https://gw.example.com/", Limits: s.limits, MaxConcurrentPerSubject: s.perSubject,
	})
	must(t, err)
	f.subj = policy.Subject{UserID: userID, ClientID: clientID}
	return f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	must(t, err)
}

type eventRow struct{ Server, Tool, Decision, Reason string }

// events flushes the recorder and returns the audit events in time order.
func (f *fixture) events() []eventRow {
	f.t.Helper()
	must(f.t, f.rec.Close(context.Background()))
	rows, err := f.pool.Query(context.Background(), `SELECT server_slug, tool_name, decision, reason FROM audit_events ORDER BY ts, id`)
	must(f.t, err)
	defer rows.Close()
	var out []eventRow
	for rows.Next() {
		var e eventRow
		must(f.t, rows.Scan(&e.Server, &e.Tool, &e.Decision, &e.Reason))
		out = append(out, e)
	}
	return out
}

func (f *fixture) execute(code string) (json.RawMessage, error) {
	return f.svc.Execute(context.Background(), f.subj, code)
}

func TestAllowedReadCallReturnsData(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	out, err := f.execute(`async () => await tools.acme.get_item({id: "7"})`)
	must(t, err)
	if string(out) != `{"id":"7","name":"widget"}` {
		t.Fatalf("out = %s", out)
	}
	f.up.mu.Lock()
	auths := f.up.auths
	f.up.mu.Unlock()
	if len(auths) == 0 || auths[len(auths)-1] != "Bearer "+canary {
		t.Fatalf("upstream did not get the credential: %v", auths)
	}
	ev := f.events()
	if len(ev) != 1 || ev[0] != (eventRow{"acme", "get_item", "allow", "allowed"}) {
		t.Fatalf("events = %+v", ev)
	}
}

func TestSanitizedIdentifierResolvesToRawName(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	out, err := f.execute(`async () => await tools.acme.get_thing({})`)
	must(t, err)
	if string(out) != `{"thing":1}` {
		t.Fatalf("out = %s", out)
	}
	if ev := f.events(); len(ev) != 1 || ev[0].Tool != "get-thing" {
		t.Fatalf("events = %+v", ev)
	}
}

func TestWriteDeniedWithReadConsent(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	var forwarded atomic.Bool
	f.up.createCheck = func() { forwarded.Store(true) }
	_, err := f.execute(`async () => await tools.acme.create_item({title: "x"})`)
	if err == nil || !strings.Contains(err.Error(), "denied: level_exceeded") ||
		!strings.Contains(err.Error(), "raise the consent level for acme") {
		t.Fatalf("err = %v", err)
	}
	if forwarded.Load() {
		t.Fatal("denied call reached the upstream")
	}
	ev := f.events()
	if len(ev) != 1 || ev[0] != (eventRow{"acme", "create_item", "deny", "level_exceeded"}) {
		t.Fatalf("events = %+v", ev)
	}
}

func TestNoConsentDenied(t *testing.T) {
	f := newFixture(t, setup{})
	_, err := f.execute(`async () => await tools.acme.get_item({id: "1"})`)
	if err == nil || !strings.Contains(err.Error(), "denied: no_consent") {
		t.Fatalf("err = %v", err)
	}
}

func TestQuarantinedAndUnknownToolsDenied(t *testing.T) {
	f := newFixture(t, setup{consent: "destructive"})
	for code, reason := range map[string]string{
		`async () => await tools.acme.hidden_tool({})`:    "tool_quarantined",
		`async () => await tools.acme.nope({})`:           "unknown_tool",
		`async () => await tools.nowhere.x({})`:           "unknown_tool",
		`async () => await tools.call("no_dot_name", {})`: "unknown_tool",
		`async () => await tools.other.secret_report({})`: "no_ceiling",
	} {
		_, err := f.execute(code)
		if err == nil || !strings.Contains(err.Error(), "denied: "+reason) {
			t.Errorf("%s: err = %v, want reason %s", code, err, reason)
		}
	}
	for _, e := range f.events() {
		if e.Decision != "deny" {
			t.Errorf("unexpected event %+v", e)
		}
	}
}

func TestMultiCallPromiseAll(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	out, err := f.execute(`async () => {
		const rs = await Promise.all([1, 2, 3, 4].map(i => tools.acme.get_item({id: String(i)})));
		return rs.map(r => r.id);
	}`)
	must(t, err)
	if string(out) != `["1","2","3","4"]` {
		t.Fatalf("out = %s", out)
	}
	if f.up.maxSeen.Load() < 2 {
		t.Errorf("calls were not concurrent (max %d)", f.up.maxSeen.Load())
	}
	if ev := f.events(); len(ev) != 4 {
		t.Fatalf("events = %d, want 4", len(ev))
	}
	var calls int
	var status string
	must(t, f.pool.QueryRow(context.Background(), `SELECT inner_calls, status FROM executions`).Scan(&calls, &status))
	if calls != 4 || status != "succeeded" {
		t.Fatalf("execution = %d %s", calls, status)
	}
}

func TestNotConnectedMessage(t *testing.T) {
	for name, err := range map[string]error{
		"sentinel":     fmt.Errorf("lookup: %w", codemode.ErrNotConnected),
		"method":       notConnectedErr{},
		"upstreamauth": &upstreamauth.NotConnectedError{ServerSlug: "acme", ConnectURL: "https://gw.example.com/connect/acme"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, setup{consent: "read"})
			f.creds.cred, f.creds.err = nil, err
			_, got := f.execute(`async () => await tools.acme.get_item({id: "1"})`)
			want := "not connected: ask the user to open https://gw.example.com/connect/acme"
			if got == nil || !strings.Contains(got.Error(), want) {
				t.Fatalf("err = %v", got)
			}
			if ev := f.events(); len(ev) != 1 || ev[0].Decision != "deny" || ev[0].Reason != "not_connected" {
				t.Fatalf("events = %+v", ev)
			}
		})
	}
}

func TestCredentialInfrastructureErrorIsGeneric(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	f.creds.cred, f.creds.err = nil, errors.New("pg: connection refused host=10.0.0.5")
	_, err := f.execute(`async () => await tools.acme.get_item({id: "1"})`)
	if err == nil || strings.Contains(err.Error(), "10.0.0.5") || !strings.Contains(err.Error(), "credential unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpstreamToolErrorIsTruncated(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	_, err := f.execute(`async () => await tools.acme.fail({})`)
	if err == nil || !strings.Contains(err.Error(), "upstream tool error: boom") {
		t.Fatalf("err = %v", err)
	}
	if strings.Count(err.Error(), "e") > 400 {
		t.Fatalf("message not truncated: %d chars", len(err.Error()))
	}
	if ev := f.events(); len(ev) != 1 || ev[0].Decision != "allow" || ev[0].Reason != "upstream_error" {
		t.Fatalf("events = %+v", ev)
	}
}

func TestWriteIsAuditedBeforeForwarding(t *testing.T) {
	f := newFixture(t, setup{consent: "write"})
	var rowsAtForward atomic.Int32
	f.up.createCheck = func() {
		var n int
		err := f.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM audit_events WHERE tool_name = 'create_item' AND decision = 'allow'`).Scan(&n)
		if err != nil {
			n = -1
		}
		rowsAtForward.Store(int32(n))
	}
	out, err := f.execute(`async () => await tools.acme.create_item({title: "x"})`)
	must(t, err)
	if string(out) != `{"created":true}` {
		t.Fatalf("out = %s", out)
	}
	if rowsAtForward.Load() != 1 {
		t.Fatalf("audit rows visible when the upstream handler ran = %d, want 1", rowsAtForward.Load())
	}
	// F8: the sync pre-forward row is followed by an outcome row of the same execution.
	if ev := f.events(); len(ev) != 2 {
		t.Fatalf("events = %+v, want pre-forward and outcome", ev)
	}
	rows, err := f.pool.Query(context.Background(),
		`SELECT decision, reason, result_bytes, execution_id IS NOT NULL FROM audit_events WHERE tool_name = 'create_item' ORDER BY ts, id`)
	must(t, err)
	defer rows.Close()
	type ev struct {
		decision, reason string
		resultBytes      int
		linked           bool
	}
	var got []ev
	for rows.Next() {
		var e ev
		must(t, rows.Scan(&e.decision, &e.reason, &e.resultBytes, &e.linked))
		got = append(got, e)
	}
	if len(got) != 2 || got[0].resultBytes != 0 || got[1] != (ev{"allow", "allowed", len(`{"created":true}`), true}) || !got[0].linked {
		t.Fatalf("rows = %+v", got)
	}
}

func TestWriteUpstreamFailureIsRecordedAsOutcome(t *testing.T) {
	f := newFixture(t, setup{consent: "write"})
	exec(t, f.pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level) VALUES ($1::uuid,'fail_write','x','h','write')`, f.serverID)
	_, err := f.execute(`async () => await tools.acme.fail_write({})`)
	if err == nil || !strings.Contains(err.Error(), "upstream tool error") {
		t.Fatalf("err = %v", err)
	}
	ev := f.events()
	if len(ev) != 2 || ev[0].Reason != "allowed" || ev[1].Reason != "upstream_error" {
		t.Fatalf("events = %+v, want allowed then upstream_error", ev)
	}
}

func TestDestructiveNeedsDestructiveConsent(t *testing.T) {
	f := newFixture(t, setup{consent: "write"})
	_, err := f.execute(`async () => await tools.acme.delete_item({id: "1"})`)
	if err == nil || !strings.Contains(err.Error(), "denied: level_exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecutionStoresCodeAndInnerCalls(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	code := `async () => { await tools.acme.get_item({id: "1"}); try { await tools.acme.create_item({}); } catch (e) {} return 1; }`
	if _, err := f.execute(code); err != nil {
		t.Fatal(err)
	}
	var got, hash, kind, status string
	var calls int
	must(t, f.pool.QueryRow(context.Background(), `SELECT code, code_hash, kind, status, inner_calls FROM executions`).Scan(&got, &hash, &kind, &status, &calls))
	if got != code || len(hash) != 64 || kind != "execute" || status != "succeeded" || calls != 2 {
		t.Fatalf("execution = %q %q %q %q %d", got, hash, kind, status, calls)
	}
	var linked int
	f.events()
	must(t, f.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events e JOIN executions x ON x.id = e.execution_id`).Scan(&linked))
	if linked != 2 {
		t.Fatalf("events linked to the execution = %d, want 2", linked)
	}
}

func TestScriptErrorMarksExecutionFailed(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	_, err := f.execute(`async () => { throw new Error("nope"); }`)
	if !errors.Is(err, sandbox.ErrScript) {
		t.Fatalf("err = %v", err)
	}
	var status string
	var msg *string
	must(t, f.pool.QueryRow(context.Background(), `SELECT status, error FROM executions`).Scan(&status, &msg))
	if status != "failed" || msg == nil {
		t.Fatalf("execution = %s %v", status, msg)
	}
}

func TestTimeoutMapsToTimedOut(t *testing.T) {
	f := newFixture(t, setup{consent: "read", limits: sandbox.Limits{Timeout: 300 * time.Millisecond}})
	_, err := f.execute(`async () => { while (true) {} }`)
	if !errors.Is(err, sandbox.ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	var status string
	must(t, f.pool.QueryRow(context.Background(), `SELECT status FROM executions`).Scan(&status))
	if status != "timed_out" {
		t.Fatalf("status = %s", status)
	}
}

func TestCancelledContextMapsToCancelled(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	_, err := f.svc.Execute(ctx, f.subj, `async () => { while (true) {} }`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	var status string
	must(t, f.pool.QueryRow(context.Background(), `SELECT status FROM executions`).Scan(&status))
	if status != "cancelled" {
		t.Fatalf("status = %s", status)
	}
}

func TestSearchHidesDisallowedTools(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	ctx := context.Background()

	res, err := f.svc.Search(ctx, f.subj, "item")
	must(t, err)
	joined := strings.Join(res.Tools, ",")
	if !strings.Contains(joined, "acme.get_item") {
		t.Fatalf("tools = %v", res.Tools)
	}
	for _, hidden := range []string{"create_item", "delete_item", "hidden_tool", "secret_report"} {
		if strings.Contains(joined, hidden) || strings.Contains(res.Declarations, hidden) {
			t.Errorf("search leaked %s: %v\n%s", hidden, res.Tools, res.Declarations)
		}
	}
	if !strings.Contains(res.Declarations, "declare namespace acme") {
		t.Errorf("declarations = %s", res.Declarations)
	}
	// Searching for a hidden tool by its exact name finds nothing.
	res, err = f.svc.Search(ctx, f.subj, "secret_report")
	must(t, err)
	if len(res.Tools) != 0 || res.Declarations != "" {
		t.Fatalf("hidden tool found: %+v", res)
	}
	// The same query with more consent shows more tools (cache is keyed by the allowed set).
	exec(t, f.pool, `UPDATE consents SET level = 'destructive'`)
	res, err = f.svc.Search(ctx, f.subj, "item")
	must(t, err)
	if !strings.Contains(strings.Join(res.Tools, ","), "acme.delete_item") {
		t.Fatalf("expanded consent not reflected: %v", res.Tools)
	}
	var searches int
	must(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM executions WHERE kind = 'search' AND status = 'succeeded'`).Scan(&searches))
	if searches != 3 {
		t.Fatalf("search executions = %d, want 3", searches)
	}
}

func TestSearchCapsResults(t *testing.T) {
	f := newFixture(t, setup{consent: "destructive"})
	for i := 0; i < 15; i++ {
		exec(t, f.pool, `INSERT INTO tools (server_id, name, schema_hash, trust_level) VALUES ($1::uuid, $2, 'h', 'read')`, f.serverID, fmt.Sprintf("bulk_%02d", i))
	}
	res, err := f.svc.Search(context.Background(), f.subj, "bulk")
	must(t, err)
	if len(res.Tools) != 10 || res.Total != 15 {
		t.Fatalf("tools=%d total=%d", len(res.Tools), res.Total)
	}
}

func TestCanaryTokenNeverLeaks(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	out, err := f.execute(`async () => {
		const r = await tools.acme.leak_result({});
		let e = "";
		try { await tools.acme.leak_error({}); } catch (err) { e = String(err.message); }
		return {r, e, globalThisKeys: Object.getOwnPropertyNames(globalThis).join(",")};
	}`)
	must(t, err)
	if strings.Contains(string(out), canary) {
		t.Fatalf("canary in result: %s", out)
	}
	if !strings.Contains(string(out), "[redacted]") {
		t.Fatalf("expected scrubbed placeholder in %s", out)
	}
	// Errors that escape the script must not carry it either.
	_, err = f.execute(`async () => { await tools.acme.leak_error({}); }`)
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("err = %v", err)
	}
	f.events()
	for _, q := range []string{
		`SELECT coalesce(string_agg(x::text, ' '), '') FROM executions x`,
		`SELECT coalesce(string_agg(e::text, ' '), '') FROM audit_events e`,
	} {
		var dump string
		must(t, f.pool.QueryRow(context.Background(), q).Scan(&dump))
		if strings.Contains(dump, canary) {
			t.Fatalf("canary stored in audit data: %s", q)
		}
	}
}

func TestToolDescriptions(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	search, execDesc := f.svc.ToolDescriptions(context.Background(), f.subj)
	t.Logf("search: %s", search)
	t.Logf("execute: %s", execDesc)
	if !strings.Contains(execDesc, "Available servers: acme.") || strings.Contains(execDesc, "other") ||
		!strings.Contains(execDesc, `"import" is forbidden`) || !strings.Contains(execDesc, "Promise.all") ||
		!strings.Contains(execDesc, "tools.<server>.<tool>(args)") {
		t.Fatalf("execute description = %s", execDesc)
	}
	if !strings.Contains(search, "search") && !strings.Contains(search, "Find tools") {
		t.Fatalf("search description = %s", search)
	}
	none := policy.Subject{UserID: f.subj.UserID, ClientID: "someone-else"}
	_, noneDesc := f.svc.ToolDescriptions(context.Background(), none)
	if !strings.Contains(noneDesc, "none (no access granted)") {
		t.Fatalf("description without access = %s", noneDesc)
	}
}

func TestToolDescriptionsPython(t *testing.T) {
	f := newFixture(t, setup{consent: "read", python: true})
	search, execDesc := f.svc.ToolDescriptions(context.Background(), f.subj)
	for _, want := range []string{"Available servers: acme.", "call_tool_async", "asyncio.gather", "RuntimeError", "PermissionError"} {
		if !strings.Contains(execDesc, want) {
			t.Errorf("execute description lacks %q: %s", want, execDesc)
		}
	}
	if strings.Contains(execDesc, "Promise") {
		t.Errorf("execute description = %s", execDesc)
	}
	if !strings.Contains(search, "TypeScript notation") || !strings.Contains(search, "Python dict") {
		t.Errorf("search description = %s", search)
	}
}

func TestPythonScriptEndToEnd(t *testing.T) {
	f := newFixture(t, setup{consent: "read", python: true})
	out, err := f.execute(`import asyncio
a, b = await asyncio.gather(call_tool_async("acme.get_item", {"id": "1"}), call_tool_async("acme.get_item", {"id": "2"}))
[a["id"], b["id"], call_tool("acme.get_item", {"id": "3"})["name"]]`)
	must(t, err)
	if string(out) != `["1","2","widget"]` {
		t.Fatalf("out = %s", out)
	}
	if ev := f.events(); len(ev) != 3 || ev[0].Decision != "allow" {
		t.Fatalf("events = %+v", ev)
	}
	var calls int
	var status string
	must(t, f.pool.QueryRow(context.Background(), `SELECT inner_calls, status FROM executions`).Scan(&calls, &status))
	if calls != 3 || status != "succeeded" {
		t.Fatalf("execution = %d %s", calls, status)
	}
}

// limitsRecorder is a Runner that records the limits of its last run.
type limitsRecorder struct{ got sandbox.Limits }

func (r *limitsRecorder) Run(_ context.Context, _ string, _ sandbox.Bridge, lim sandbox.Limits) (json.RawMessage, error) {
	r.got = lim
	return json.RawMessage(`1`), nil
}

func (r *limitsRecorder) Close() error { return nil }

// Pooled engines key workers by Limits.Owner: it must always carry the caller's user ID.
func TestExecuteSetsOwnerToUserID(t *testing.T) {
	rec := &limitsRecorder{}
	f := newFixture(t, setup{consent: "read", runner: rec})
	_, err := f.execute(`1`)
	must(t, err)
	if rec.got.Owner == "" || rec.got.Owner != f.subj.UserID {
		t.Fatalf("Owner = %q, want %q", rec.got.Owner, f.subj.UserID)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := codemode.New(codemode.Options{}); err == nil {
		t.Fatal("empty options accepted")
	}
}

// F1: one NUL in a script must not cost any audit event or leave the execution running.
func TestNULBytesDoNotDropAuditEvents(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	exec(t, f.pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level) VALUES ($1::uuid,'nul_error','x','h','read')`, f.serverID)
	other := "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	must(t, f.rec.Record(context.Background(), audit.Event{
		UserID: other, ClientID: clientID, ServerSlug: "acme", ToolName: "get_item", Decision: audit.DecisionAllow, Reason: "allowed",
	}, audit.Async)) // another user's event in the same batch window

	_, err := f.execute(`async () => {
		const r = await Promise.allSettled([tools.acme.get_item({q: "\0"}), tools.call("x.\0", {}), tools.acme.nul_error({}).catch(e => { throw new Error("up\0stream " + e.message); })]);
		return r.map(x => x.status);
	}`)
	must(t, err)
	_, err = f.execute(`async () => { throw new Error("a\0b"); }`)
	if err == nil {
		t.Fatal("expected script error")
	}

	ev := f.events()
	if len(ev) != 4 {
		t.Fatalf("events = %+v, want 4 (other user's, get_item, deny, nul_error)", ev)
	}
	var running int
	must(t, f.pool.QueryRow(context.Background(), `SELECT count(*) FROM executions WHERE status = 'running'`).Scan(&running))
	if running != 0 {
		t.Fatalf("%d executions left running", running)
	}
}

// F2: per-user cap on concurrent executes.
func TestPerSubjectConcurrencyCap(t *testing.T) {
	f := newFixture(t, setup{consent: "read", perSubject: 2})
	exec(t, f.pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level) VALUES ($1::uuid,'block','x','h','read')`, f.serverID)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.execute(`async () => await tools.acme.block({})`); err != nil {
				t.Error(err)
			}
		}()
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.up.blocked.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("blockers never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	_, err := f.execute(`async () => 1`)
	if !errors.Is(err, sandbox.ErrBusy) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v, want an immediate sandbox.ErrBusy", err, time.Since(start))
	}
	otherUser := policy.Subject{UserID: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", ClientID: clientID}
	if out, err := f.svc.Execute(context.Background(), otherUser, `async () => 7`); err != nil || string(out) != "7" {
		t.Fatalf("another user must not be capped: %s %v", out, err)
	}

	close(f.up.release)
	wg.Wait()
	if out, err := f.execute(`async () => 2`); err != nil || string(out) != "2" {
		t.Fatalf("slot not released: %s %v", out, err)
	}
}

func TestPerSubjectCapDefaultsToThree(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	exec(t, f.pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level) VALUES ($1::uuid,'block','x','h','read')`, f.serverID)
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = f.execute(`async () => await tools.acme.block({})`) }()
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.up.blocked.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	_, err := f.execute(`async () => 1`)
	close(f.up.release)
	wg.Wait()
	if !errors.Is(err, sandbox.ErrBusy) {
		t.Fatalf("4th concurrent execute: err = %v, want ErrBusy", err)
	}
}

// F3: with get-item and get_item both present, get_item must be documented as itself and the
// bridge must run the tool the declaration promises.
func TestIdentifierConfusionBetweenSimilarNames(t *testing.T) {
	f := newFixture(t, setup{consent: "destructive"})
	exec(t, f.pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level) VALUES ($1::uuid,'get-item','dashed variant','h','destructive')`, f.serverID)

	res, err := f.svc.Search(context.Background(), f.subj, "get item")
	must(t, err)
	if !strings.Contains(res.Declarations, "/** get_item description */\n  function get_item(") {
		t.Fatalf("get_item not documented as itself:\n%s", res.Declarations)
	}
	if !strings.Contains(res.Declarations, "/** dashed variant */\n  function get_item_2(") {
		t.Fatalf("get-item not documented as get_item_2:\n%s", res.Declarations)
	}

	_, err = f.execute(`async () => { await tools.acme.get_item({id: "1"}); try { await tools.acme.get_item_2({}); } catch (e) {} return 1; }`)
	must(t, err)
	ev := f.events()
	// get_item (read): 1 event; get-item (destructive): pre-forward row plus outcome row.
	if len(ev) != 3 || ev[0].Tool != "get_item" || ev[1].Tool != "get-item" || ev[2].Tool != "get-item" {
		t.Fatalf("events = %+v, want get_item then get-item twice", ev)
	}
}

// F7: a connection error must not hand the model the upstream URL's query secrets.
func TestUpstreamURLSecretsNeverReachTheScript(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	exec(t, f.pool, `UPDATE upstream_servers SET url = $1 WHERE slug = 'acme'`, deadURL+"/mcp?api_key=URLSECRET123")
	out, err := f.execute(`async () => { try { await tools.acme.get_item({id: "1"}); return "no error"; } catch (e) { return String(e.message); } }`)
	must(t, err)
	if strings.Contains(string(out), "URLSECRET123") || !strings.Contains(string(out), "upstream call failed") {
		t.Fatalf("out = %s", out)
	}
}

// A quarantined tool must not be shadowed by an active tool whose sanitized identifier equals its
// raw name: the fixture has an active "get-thing" (identifier get_thing) and gets a quarantined
// "get_thing". Calling get_thing must be denied and audited, never run get-thing.
func TestQuarantinedRawNameIsNotShadowedByActiveIdentifier(t *testing.T) {
	f := newFixture(t, setup{consent: "destructive"})
	exec(t, f.pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level, status)
		VALUES ($1::uuid, 'get_thing', 'x', 'h', 'read', 'quarantined')`, f.serverID)

	_, err := f.execute(`async () => await tools.acme.get_thing({})`)
	if err == nil || !strings.Contains(err.Error(), "denied: tool_quarantined") {
		t.Fatalf("err = %v", err)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0] != (eventRow{"acme", "get_thing", "deny", "tool_quarantined"}) {
		t.Fatalf("events = %v", evs)
	}
}

// A tool pending approval (new or changed upstream definition) is invisible to search, its
// description never reaches the declarations, and calling it is denied before the upstream.
func TestPendingApprovalToolHiddenAndDenied(t *testing.T) {
	f := newFixture(t, setup{consent: "destructive"})
	ctx := context.Background()
	const marker = "PENDING_DESCRIPTION_MARKER"
	exec(t, f.pool, `INSERT INTO tools (server_id, name, description, schema_hash, trust_level, status, pending_changes)
		VALUES ($1::uuid, 'get_pending', $2, 'h', 'read', 'pending_approval', '{new}')`, f.serverID, "Get item "+marker)
	exec(t, f.pool, `UPDATE tools SET status = 'pending_approval', description = $2
		WHERE server_id = $1::uuid AND name = 'get_item'`, f.serverID, "Get item changed "+marker)

	for _, q := range []string{"item", "get_pending", marker} {
		res, err := f.svc.Search(ctx, f.subj, q)
		must(t, err)
		joined := strings.Join(res.Tools, ",")
		if strings.Contains(joined, "get_pending") || strings.Contains(joined, "acme.get_item") ||
			strings.Contains(res.Declarations, marker) || strings.Contains(res.Declarations, "get_pending") {
			t.Fatalf("search %q leaked a pending tool: %v\n%s", q, res.Tools, res.Declarations)
		}
	}
	for _, name := range []string{"get_pending", "get_item"} {
		_, err := f.execute(`async () => await tools.acme.` + name + `({id: "1"})`)
		if err == nil || !strings.Contains(err.Error(), "denied: tool_quarantined") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	f.up.mu.Lock()
	defer f.up.mu.Unlock()
	if n := len(f.up.auths); n != 0 {
		t.Fatalf("pending tool reached the upstream %d times", n)
	}
}

func TestSearchReflectsToolChangeBeforeTTL(t *testing.T) {
	f := newFixture(t, setup{consent: "read"})
	ctx := context.Background()
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = f.pol.Listen(lctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	res, err := f.svc.Search(ctx, f.subj, "hidden")
	must(t, err)
	if len(res.Tools) != 0 {
		t.Fatalf("quarantined tool visible: %v", res.Tools)
	}
	// Approve-like change: the tool becomes active with a new definition. The default catalog TTL
	// is 30s, so only the LISTEN-driven invalidation can make the next search see it.
	exec(t, f.pool, `UPDATE tools SET description = 'approved fresh definition', status = 'active' WHERE name = 'hidden_tool'`)
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err = f.svc.Search(ctx, f.subj, "hidden")
		must(t, err)
		if strings.Contains(res.Declarations, "approved fresh definition") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("search still stale: %v\n%s", res.Tools, res.Declarations)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
