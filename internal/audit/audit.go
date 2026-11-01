// Package audit persists what the gateway did: one executions row per search/execute call
// (with the code, the main forensic evidence) and one audit_events row per inner tool call.
//
// Executions are always written synchronously. Events are written synchronously (Sync) or
// through a bounded buffer flushed in batches (Async). A full buffer degrades to a synchronous
// write instead of dropping the event. Raw tool arguments are never stored: only a hash of the
// canonical args and a redacted, truncated top-level view.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Mode selects how Record persists an event.
type Mode int

const (
	// Sync writes before Record returns. Use it before forwarding write/destructive calls.
	Sync Mode = iota
	// Async queues the event for a batched write.
	Async
)

// Execution kinds and statuses (match the CHECK constraints of the executions table).
const (
	KindSearch  = "search"
	KindExecute = "execute"

	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusTimedOut  = "timed_out"
	StatusCancelled = "cancelled"

	DecisionAllow = "allow"
	DecisionDeny  = "deny"
)

const (
	defaultBufferSize    = 1024
	defaultBatchSize     = 100
	defaultFlushInterval = 500 * time.Millisecond
	writeTimeout         = 10 * time.Second
	maxErrorChars        = 1000
)

// Execution starts an executions row. For kind "search" Code holds the query.
type Execution struct {
	UserID   string
	ClientID string
	Kind     string
	Code     string
}

// Event is one inner tool call decision. ExecutionID may be empty.
type Event struct {
	ExecutionID string
	UserID      string
	ClientID    string
	ServerSlug  string
	ToolName    string
	// Profile is the connection profile the call used; "" stores NULL (no credentials). It must
	// match ^[a-z][a-z0-9_-]{0,31}$: an invalid value would fail the whole batch on the CHECK.
	Profile     string
	Decision    string // DecisionAllow or DecisionDeny
	Reason      string
	Args        json.RawMessage // hashed and redacted by Record, never stored raw
	LatencyMS   int
	ResultBytes int
	// Time is the event time; zero means now.
	Time time.Time
}

// Options configures a Recorder. Zero values select defaults.
type Options struct {
	BufferSize    int // events queued for batching before Record falls back to a sync write
	BatchSize     int // events per batch write
	FlushInterval time.Duration
	Logger        *slog.Logger
}

// row is an Event after redaction: it no longer references the raw args.
type row struct {
	Event
	ts           time.Time
	argsHash     string
	argsRedacted json.RawMessage
}

// Recorder is safe for concurrent use.
type Recorder struct {
	pool  *pgxpool.Pool
	log   *slog.Logger
	opts  Options
	queue chan row
	done  chan struct{}
	flush func(context.Context, []row) error // replaced in tests

	mu     sync.RWMutex // guards closed and the send side of queue
	closed bool
}

// New starts the background batch writer. Call Close to flush it.
func New(pool *pgxpool.Pool, opts Options) *Recorder {
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBufferSize
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultFlushInterval
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	r := &Recorder{
		pool: pool, log: log, opts: opts,
		queue: make(chan row, opts.BufferSize),
		done:  make(chan struct{}),
	}
	r.flush = r.copyRows
	go r.loop()
	return r
}

// StartExecution stores the code and its sha256 synchronously with status running.
// The write ignores cancellation of ctx: the code must never be lost.
func (r *Recorder) StartExecution(ctx context.Context, e Execution) (string, error) {
	if e.Kind != KindSearch && e.Kind != KindExecute {
		return "", fmt.Errorf("audit: invalid execution kind %q", e.Kind)
	}
	ctx, cancel := detached(ctx)
	defer cancel()
	var id string
	// The hash covers the code exactly as received; the stored text is sanitized for Postgres.
	err := r.pool.QueryRow(ctx, `
		INSERT INTO executions (user_id, client_id, kind, code, code_hash)
		VALUES ($1::uuid, $2, $3, $4, $5) RETURNING id::text`,
		sanitizeText(e.UserID), sanitizeText(e.ClientID), e.Kind, sanitizeText(e.Code), sha256Hex([]byte(e.Code))).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("audit: start execution: %w", err)
	}
	return id, nil
}

// FinishExecution closes an execution. errMsg is truncated; pass "" for none.
func (r *Recorder) FinishExecution(ctx context.Context, id, status string, innerCalls int, errMsg string) error {
	switch status {
	case StatusSucceeded, StatusFailed, StatusTimedOut, StatusCancelled:
	default:
		return fmt.Errorf("audit: invalid execution status %q", status)
	}
	ctx, cancel := detached(ctx)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		UPDATE executions SET status = $2, inner_calls = $3, error = NULLIF($4, ''), finished_at = now()
		WHERE id = $1::uuid`, sanitizeText(id), status, innerCalls, truncateChars(sanitizeText(errMsg), maxErrorChars))
	if err != nil {
		return fmt.Errorf("audit: finish execution: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("audit: finish execution: unknown execution id")
	}
	return nil
}

// Record stores one event. Async events are queued; when the buffer is full (or the Recorder
// is closed) the event is written synchronously and a warning is logged.
func (r *Recorder) Record(ctx context.Context, e Event, mode Mode) error {
	if e.Decision != DecisionAllow && e.Decision != DecisionDeny {
		return fmt.Errorf("audit: invalid decision %q", e.Decision)
	}
	rw := row{Event: e, ts: e.Time, argsHash: hashArgs(e.Args), argsRedacted: redactArgs(e.Args)}
	rw.Args = nil
	rw.sanitize()
	if rw.ts.IsZero() {
		rw.ts = time.Now()
	}
	if mode == Async && r.enqueue(rw) {
		return nil
	}
	return r.writeSync(ctx, rw)
}

// enqueue reports whether the row was queued. A false result means the caller must write it.
func (r *Recorder) enqueue(rw row) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return false
	}
	select {
	case r.queue <- rw:
		return true
	default:
		r.log.Warn("audit buffer full, writing event synchronously", "buffer", cap(r.queue))
		return false
	}
}

func (r *Recorder) writeSync(ctx context.Context, rw row) error {
	ctx, cancel := detached(ctx)
	defer cancel()
	if err := r.flush(ctx, []row{rw}); err != nil {
		return err
	}
	return nil
}

// Close stops accepting async events, flushes everything queued and waits for the writer,
// or until ctx ends. Events recorded after Close are written synchronously.
func (r *Recorder) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("audit: close: %w", ctx.Err())
	}
}

// EnsurePartitions creates the audit_events partitions for the month of now and the next one.
// Call it at startup and daily.
func (r *Recorder) EnsurePartitions(ctx context.Context, now time.Time) error {
	y, m, _ := now.UTC().Date()
	first := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	for _, month := range []time.Time{first, first.AddDate(0, 1, 0)} {
		if _, err := r.pool.Exec(ctx, `SELECT create_audit_partition($1::date)`, month); err != nil {
			return fmt.Errorf("audit: create partition %s: %w", month.Format("2006-01"), err)
		}
	}
	return nil
}

func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
}

func truncateChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

var eventColumns = []string{
	"ts", "execution_id", "user_id", "client_id", "server_slug", "tool_name",
	"decision", "reason", "args_hash", "args_redacted", "latency_ms", "result_bytes", "profile",
}

// copyRows writes rows with COPY, which routes them to the monthly partitions.
func (r *Recorder) copyRows(ctx context.Context, rows []row) error {
	_, err := r.pool.CopyFrom(ctx, pgx.Identifier{"audit_events"}, eventColumns,
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			return rows[i].values(), nil
		}))
	if err != nil {
		return fmt.Errorf("audit: write events: %w", err)
	}
	return nil
}

// sanitize makes every text field storable in Postgres (no NUL, valid UTF-8).
func (w *row) sanitize() {
	w.ExecutionID = sanitizeText(w.ExecutionID)
	w.UserID = sanitizeText(w.UserID)
	w.ClientID = sanitizeText(w.ClientID)
	w.ServerSlug = sanitizeText(w.ServerSlug)
	w.ToolName = sanitizeText(w.ToolName)
	w.Decision = sanitizeText(w.Decision)
	w.Reason = sanitizeText(w.Reason)
}

func (w row) values() []any {
	var execID, redacted, profile any
	if w.ExecutionID != "" {
		execID = w.ExecutionID
	}
	if w.Profile != "" {
		profile = w.Profile
	}
	if w.argsRedacted != nil {
		redacted = string(w.argsRedacted)
	}
	return []any{
		w.ts, execID, w.UserID, w.ClientID, w.ServerSlug, w.ToolName,
		w.Decision, w.Reason, w.argsHash, redacted, w.LatencyMS, w.ResultBytes, profile,
	}
}
