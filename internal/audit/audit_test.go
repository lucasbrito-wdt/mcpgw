package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

const (
	testUser   = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	testClient = "client-1"
)

func newRecorder(t *testing.T, opts Options) (*Recorder, *pgxpool.Pool) {
	t.Helper()
	pool := storetest.New(t)
	r := New(pool, opts)
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r, pool
}

func countEvents(t *testing.T, pool *pgxpool.Pool, where string, args ...any) int {
	t.Helper()
	var n int
	q := "SELECT count(*) FROM audit_events"
	if where != "" {
		q += " WHERE " + where
	}
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitForCount(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := countEvents(t, pool, "")
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("events = %d, want %d", n, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func event(tool string) Event {
	return Event{
		UserID: testUser, ClientID: testClient, ServerSlug: "srv", ToolName: tool,
		Decision: DecisionAllow, Reason: "allowed", Args: json.RawMessage(`{"a":1}`),
	}
}

func TestStartExecutionStoresCodeAndHash(t *testing.T) {
	r, pool := newRecorder(t, Options{})
	ctx := context.Background()
	code := "async () => { return await tools.srv.x({}); }"
	id, err := r.StartExecution(ctx, Execution{UserID: testUser, ClientID: testClient, Kind: KindExecute, Code: code})
	if err != nil {
		t.Fatal(err)
	}
	var gotCode, hash, status string
	var finished *time.Time
	err = pool.QueryRow(ctx, `SELECT code, code_hash, status, finished_at FROM executions WHERE id = $1::uuid`, id).
		Scan(&gotCode, &hash, &status, &finished)
	if err != nil {
		t.Fatal(err)
	}
	if gotCode != code || hash != sha256Hex([]byte(code)) || status != "running" || finished != nil {
		t.Fatalf("row = %q %q %q %v", gotCode, hash, status, finished)
	}
	if _, err := r.StartExecution(ctx, Execution{UserID: testUser, ClientID: testClient, Kind: "bogus"}); err == nil {
		t.Fatal("invalid kind accepted")
	}
}

func TestStartExecutionSurvivesCancelledContext(t *testing.T) {
	r, _ := newRecorder(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.StartExecution(ctx, Execution{UserID: testUser, ClientID: testClient, Kind: KindExecute, Code: "x"}); err != nil {
		t.Fatalf("code must be stored even when ctx is cancelled: %v", err)
	}
}

func TestFinishExecution(t *testing.T) {
	r, pool := newRecorder(t, Options{})
	ctx := context.Background()
	id, err := r.StartExecution(ctx, Execution{UserID: testUser, ClientID: testClient, Kind: KindExecute, Code: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.FinishExecution(ctx, id, StatusFailed, 3, "boom"); err != nil {
		t.Fatal(err)
	}
	var status string
	var calls int
	var msg *string
	var finished *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, inner_calls, error, finished_at FROM executions WHERE id = $1::uuid`, id).
		Scan(&status, &calls, &msg, &finished); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || calls != 3 || msg == nil || *msg != "boom" || finished == nil {
		t.Fatalf("row = %s %d %v %v", status, calls, msg, finished)
	}
	if err := r.FinishExecution(ctx, id, "weird", 0, ""); err == nil {
		t.Fatal("invalid status accepted")
	}
	if err := r.FinishExecution(ctx, "0190a1b2-c3d4-7e5f-8a9b-000000000000", StatusSucceeded, 0, ""); err == nil {
		t.Fatal("unknown execution id accepted")
	}
}

func TestRedactArgs(t *testing.T) {
	long := strings.Repeat("x", 300)
	tests := []struct {
		name string
		in   string
		want map[string]any // nil means the result must be nil
	}{
		{"keeps short values", `{"id":"7","n":3,"ok":true}`, map[string]any{"id": "7", "n": json.Number("3"), "ok": true}},
		{"masks sensitive keys", `{"api_key":"k","Authorization":"Bearer z","password":"p","x-api-key":"k","Cookie":"c","client_secret":"s","access_token":"t","q":"hi"}`,
			map[string]any{"api_key": maskedValue, "Authorization": maskedValue, "password": maskedValue, "x-api-key": maskedValue,
				"Cookie": maskedValue, "client_secret": maskedValue, "access_token": maskedValue, "q": "hi"}},
		{"truncates long strings", `{"body":"` + long + `"}`, map[string]any{"body": strings.Repeat("x", maxValueChars) + "...(truncated)"}},
		{"masks nested secrets", `{"headers":{"authorization":"abc","accept":"json"}}`,
			map[string]any{"headers": map[string]any{"authorization": maskedValue, "accept": "json"}}},
		{"non object", `[1,2]`, nil},
		{"invalid json", `{nope`, nil},
		{"empty", ``, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactArgs(json.RawMessage(tc.in))
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %s", got)
				}
				return
			}
			var m map[string]any
			dec := json.NewDecoder(strings.NewReader(string(got)))
			dec.UseNumber()
			if err := dec.Decode(&m); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(m) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v\nwant %v", m, tc.want)
			}
		})
	}
}

func TestRedactTruncatesLongNonStrings(t *testing.T) {
	arr := "[" + strings.Repeat("1,", 400) + "1]"
	got := string(redactArgs(json.RawMessage(`{"list":` + arr + `}`)))
	if len(got) > maxValueChars+64 || !strings.Contains(got, "truncated") {
		t.Fatalf("not truncated: %d bytes", len(got))
	}
}

func TestHashArgsCanonical(t *testing.T) {
	a := hashArgs(json.RawMessage(`{"b": 2, "a": [1, {"d":1,"c":2}]}`))
	b := hashArgs(json.RawMessage(`{"a":[1,{"c":2,"d":1}],"b":2}`))
	if a != b {
		t.Fatal("hash depends on key order or whitespace")
	}
	if a == hashArgs(json.RawMessage(`{"a":[1,{"c":2,"d":1}],"b":3}`)) {
		t.Fatal("different args share a hash")
	}
}

func TestRecordSyncStoresRedactedArgs(t *testing.T) {
	r, pool := newRecorder(t, Options{})
	ctx := context.Background()
	e := event("create")
	e.Args = json.RawMessage(`{"title":"t","token":"canary-123"}`)
	e.LatencyMS, e.ResultBytes = 12, 34
	if err := r.Record(ctx, e, Sync); err != nil {
		t.Fatal(err)
	}
	var redacted, hash, decision string
	var lat, size int
	if err := pool.QueryRow(ctx, `SELECT args_redacted::text, args_hash, decision, latency_ms, result_bytes FROM audit_events`).
		Scan(&redacted, &hash, &decision, &lat, &size); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(redacted, "canary-123") || !strings.Contains(redacted, maskedValue) {
		t.Fatalf("args_redacted = %s", redacted)
	}
	if hash != hashArgs(e.Args) || decision != "allow" || lat != 12 || size != 34 {
		t.Fatalf("row = %s %s %d %d", hash, decision, lat, size)
	}
	if err := r.Record(ctx, Event{Decision: "maybe"}, Sync); err == nil {
		t.Fatal("invalid decision accepted")
	}
}

func TestAsyncBatchBySize(t *testing.T) {
	r, pool := newRecorder(t, Options{BatchSize: 3, FlushInterval: time.Hour})
	for i := 0; i < 3; i++ {
		if err := r.Record(context.Background(), event(fmt.Sprint("t", i)), Async); err != nil {
			t.Fatal(err)
		}
	}
	waitForCount(t, pool, 3)
}

func TestAsyncBatchByInterval(t *testing.T) {
	r, pool := newRecorder(t, Options{BatchSize: 100, FlushInterval: 50 * time.Millisecond})
	if err := r.Record(context.Background(), event("t"), Async); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, pool, 1)
}

func TestCloseFlushesQueuedEvents(t *testing.T) {
	r, pool := newRecorder(t, Options{BatchSize: 100, FlushInterval: time.Hour})
	for i := 0; i < 5; i++ {
		if err := r.Record(context.Background(), event(fmt.Sprint("t", i)), Async); err != nil {
			t.Fatal(err)
		}
	}
	if n := countEvents(t, pool, ""); n != 0 {
		t.Fatalf("events written before flush: %d", n)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, pool, ""); n != 5 {
		t.Fatalf("events after Close = %d, want 5", n)
	}
	// After Close, async degrades to a synchronous write.
	if err := r.Record(context.Background(), event("late"), Async); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, pool, "tool_name = 'late'"); n != 1 {
		t.Fatal("event recorded after Close was lost")
	}
}

func TestBufferFullFallsBackToSyncWrite(t *testing.T) {
	r, pool := newRecorder(t, Options{BufferSize: 1, BatchSize: 2, FlushInterval: time.Hour})
	entered, release := make(chan struct{}), make(chan struct{})
	real := r.flush
	r.flush = func(ctx context.Context, rows []row) error {
		if len(rows) == 2 { // the background batch: block it so the queue stays full
			close(entered)
			<-release
		}
		return real(ctx, rows)
	}
	ctx := context.Background()
	for _, n := range []string{"a1", "a2"} {
		// With BufferSize 1, a2 only queues once the loop has taken a1; otherwise it would go
		// through the sync fallback and the batch of two would never form.
		waitQueueEmpty(t, r)
		if err := r.Record(ctx, event(n), Async); err != nil {
			t.Fatal(err)
		}
	}
	select { // a1 and a2 are in the blocked batch
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("background batch of two never formed")
	}
	if err := r.Record(ctx, event("a3"), Async); err != nil { // fills the buffer
		t.Fatal(err)
	}
	if err := r.Record(ctx, event("a4"), Async); err != nil { // buffer full: sync fallback
		t.Fatal(err)
	}
	if n := countEvents(t, pool, ""); n != 1 || countEvents(t, pool, "tool_name = 'a4'") != 1 {
		t.Fatalf("expected only a4 written synchronously, got %d rows", n)
	}
	close(release)
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, pool, ""); n != 4 {
		t.Fatalf("events = %d, want 4", n)
	}
}

func TestEnsurePartitions(t *testing.T) {
	r, pool := newRecorder(t, Options{})
	ctx := context.Background()
	if err := r.EnsurePartitions(ctx, time.Date(2031, 12, 31, 23, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := r.EnsurePartitions(ctx, time.Date(2031, 12, 31, 23, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("not idempotent: %v", err)
	}
	for _, name := range []string{"audit_events_2031_12", "audit_events_2032_01"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok); err != nil || !ok {
			t.Fatalf("partition %s missing (%v)", name, err)
		}
	}
	e := event("future")
	e.Time = time.Date(2032, 1, 10, 0, 0, 0, 0, time.UTC)
	if err := r.Record(ctx, e, Sync); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events_2032_01`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("event not routed to its partition: %d %v", n, err)
	}
}

func TestConcurrentRecord(t *testing.T) {
	r, pool := newRecorder(t, Options{BufferSize: 16, BatchSize: 8, FlushInterval: 10 * time.Millisecond})
	const workers, each = 16, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				mode := Async
				if i%5 == 0 {
					mode = Sync
				}
				if err := r.Record(context.Background(), event(fmt.Sprintf("w%d_%d", w, i)), mode); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, pool, ""); n != workers*each {
		t.Fatalf("events = %d, want %d", n, workers*each)
	}
}

// F1: NUL bytes and invalid UTF-8 must never stop an event, a code body or an execution from
// being stored.

func TestNULInEventFieldsAndArgsIsStored(t *testing.T) {
	r, pool := newRecorder(t, Options{FlushInterval: 20 * time.Millisecond})
	e := event("bad\x00tool")
	e.ServerSlug = "srv\x00"
	e.Reason = "r\x00\xff"
	e.Args = json.RawMessage(`{"q":"\u0000","k\u0000":"v","n":{"a\u0000":["x\u0000y"]}}`)
	rawHash := hashArgs(e.Args)
	if err := r.Record(context.Background(), e, Async); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(context.Background(), e, Sync); err != nil {
		t.Fatalf("sync record with NUL: %v", err)
	}
	waitForCount(t, pool, 2)
	var tool, args, hash string
	if err := pool.QueryRow(context.Background(), `SELECT tool_name, args_redacted::text, args_hash FROM audit_events LIMIT 1`).Scan(&tool, &args, &hash); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(tool, 0) || !strings.Contains(tool, "�") {
		t.Errorf("tool_name = %q", tool)
	}
	if hash != rawHash {
		t.Errorf("args_hash must stay over the raw bytes")
	}
	if !strings.Contains(args, `"q": "�"`) && !strings.Contains(args, `"q": "�"`) {
		t.Errorf("args_redacted = %s", args)
	}
}

func TestNULInExecutionIsStoredAndFinished(t *testing.T) {
	r, pool := newRecorder(t, Options{})
	ctx := context.Background()
	code := "async () => '\x00'"
	id, err := r.StartExecution(ctx, Execution{UserID: testUser, ClientID: testClient, Kind: KindExecute, Code: code})
	if err != nil {
		t.Fatalf("start with NUL code: %v", err)
	}
	if err := r.FinishExecution(ctx, id, StatusFailed, 1, "upstream said \x00 and \xff"); err != nil {
		t.Fatalf("finish with NUL error: %v", err)
	}
	var status, hash string
	if err := pool.QueryRow(ctx, `SELECT status, code_hash FROM executions WHERE id = $1::uuid`, id).Scan(&status, &hash); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || hash != sha256Hex([]byte(code)) {
		t.Fatalf("status = %s hash = %s", status, hash)
	}
}

func TestFailedBatchFallsBackToRowByRow(t *testing.T) {
	r, pool := newRecorder(t, Options{FlushInterval: 20 * time.Millisecond, BatchSize: 10})
	bad := event("bad")
	bad.UserID = "not-a-uuid" // still rejected by Postgres after sanitizing
	for _, e := range []Event{event("a"), bad, event("b"), event("c")} {
		if err := r.Record(context.Background(), e, Async); err != nil {
			t.Fatal(err)
		}
	}
	waitForCount(t, pool, 3)
	if n := countEvents(t, pool, "tool_name = 'bad'"); n != 0 {
		t.Fatalf("bad row stored: %d", n)
	}
}

func TestSensitiveKeyCoverage(t *testing.T) {
	for _, k := range []string{"passwd", "PWD", "bearer", "credentials", "private_key", "privateKey", "private-key",
		"session_id", "jwt", "Set-Cookie", "auth", "user_auth", "X-Auth", "oauth_code", "authentication"} {
		if !sensitiveKey.MatchString(k) {
			t.Errorf("key %q should be sensitive", k)
		}
	}
	for _, k := range []string{"author", "authority", "title", "path", "query", "description"} {
		if sensitiveKey.MatchString(k) {
			t.Errorf("key %q should not be sensitive", k)
		}
	}
}

func TestRedactValueLevelSecrets(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.sig-nature_1"
	in := map[string]any{
		"url":      "https://u:pw@h.example/mcp/path?sk-live-abcdef123456&k=SECRETVAL#frag",
		"note":     "call https://h.example/a?token=SECRETVAL now",
		"plain":    "https://h.example/a/b",
		"bearer":   "Bearer abc.def-123",
		"hdr":      "Authorization: Bearer SECRETBEARER",
		"jwt":      jwt,
		"jwt_text": "got " + jwt + " back",
		"keys":     []any{"sk-abcdefgh12345678", "ghp_abcdefgh12345678", "github_pat_abcdefgh12345678", "xoxb-abcdefgh-1234", "xoxp-abcdefgh-1234"},
		"safe":     "task-force desk-lamp sk-1",
	}
	raw, _ := json.Marshal(in)
	out := string(redactArgs(raw))
	for _, leak := range []string{"SECRET", "sk-live", "abcdefgh", "pw@", "frag", "eyJ", "sig-nature"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q: %s", leak, out)
		}
	}
	for _, keep := range []string{"https://h.example/mcp/path?[redacted]", "https://h.example/a/b", "task-force desk-lamp sk-1"} {
		if !strings.Contains(out, keep) {
			t.Errorf("want %q kept: %s", keep, out)
		}
	}
}

func TestRedactTextIdempotent(t *testing.T) {
	once := redactText("x https://h/p?a=1&b=2 Bearer tok sk-abcdefgh1234")
	if twice := redactText(once); twice != once {
		t.Fatalf("not idempotent: %q -> %q", once, twice)
	}
}

func TestRedactSecretShapeThenTruncate(t *testing.T) {
	long := strings.Repeat("x", maxValueChars-3) + " sk-abcdefgh12345678 " + strings.Repeat("y", 50)
	got := string(redactArgs(json.RawMessage(`{"body":"` + long + `"}`)))
	if strings.Contains(got, "sk-abc") || !strings.Contains(got, "(truncated)") {
		t.Fatalf("got %s", got)
	}
}

// waitQueueEmpty waits until the background loop has taken every queued row.
func waitQueueEmpty(t *testing.T, r *Recorder) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(r.queue) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("audit queue never drained")
		}
		time.Sleep(time.Millisecond)
	}
}
