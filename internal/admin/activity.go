package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	executionCodeLimit = 2048 // bytes of code returned by list calls
	executionAuditMax  = 500
)

type Execution struct {
	ID, UserID, Email, ClientID, Kind, Status string
	Code                                      string // untrusted; cut at executionCodeLimit in lists
	CodeTruncated                             bool
	InnerCalls                                int
	Error                                     string // untrusted
	StartedAt                                 time.Time
	FinishedAt                                *time.Time
}

type AuditEvent struct {
	ID                                     string
	TS                                     time.Time
	ServerSlug, ToolName, Decision, Reason string
	LatencyMS, ResultBytes                 *int
}

type ExecutionFilter struct {
	UserID, Status string
	From, To       *time.Time
}

type AdminEvent struct {
	ID                                  string
	TS                                  time.Time
	ActorID, ActorEmail, Action, Target string
	Details                             map[string]any
	IP                                  string
}

// executionSelect keeps at most codeBytes of the code (left counts characters, which never exceed
// bytes, cutUTF8 finishes the job) and flags a longer one. codeBytes is a trusted constant.
func executionSelect(codeBytes int) string {
	return fmt.Sprintf(`SELECT e.id::text, e.user_id::text, COALESCE(u.email, ''), e.client_id, e.kind, e.status,
		left(e.code, %[1]d), octet_length(e.code) > %[1]d, e.inner_calls, COALESCE(e.error, ''), e.started_at, e.finished_at
	FROM executions e LEFT JOIN users u ON u.id = e.user_id `, codeBytes)
}

func scanExecution(r pgx.Row) (v Execution, err error) {
	err = r.Scan(&v.ID, &v.UserID, &v.Email, &v.ClientID, &v.Kind, &v.Status, &v.Code, &v.CodeTruncated, &v.InnerCalls, &v.Error, &v.StartedAt, &v.FinishedAt)
	return
}

// cutUTF8 shortens s to at most n bytes without splitting a rune.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (s *Service) ListExecutions(ctx context.Context, f ExecutionFilter, q PageQuery) (Page[Execution], error) {
	cursor, err := q.uuidCursor()
	if err != nil {
		return Page[Execution]{}, err
	}
	if f.UserID != "" && !validUUID(f.UserID) {
		return Page[Execution]{}, nil
	}
	limit := q.limit()
	rows, err := s.o.Pool.Query(ctx, executionSelect(executionCodeLimit)+`
		WHERE ($1 = '' OR e.user_id = NULLIF($1, '')::uuid) AND ($2 = '' OR e.status = $2)
			AND ($3::timestamptz IS NULL OR e.started_at >= $3) AND ($4::timestamptz IS NULL OR e.started_at < $4)
			AND ($5 = '' OR e.id < NULLIF($5, '')::uuid)
		ORDER BY e.id DESC LIMIT $6`, f.UserID, f.Status, f.From, f.To, cursor, limit+1)
	if err != nil {
		return Page[Execution]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Execution, error) {
		v, err := scanExecution(r)
		v.Code = cutUTF8(v.Code, executionCodeLimit)
		return v, err
	})
	if err != nil {
		return Page[Execution]{}, err
	}
	return pageRows(items, limit, func(v Execution) string { return v.ID }), nil
}

// GetExecution returns the full code and the tool calls audited under the execution.
func (s *Service) GetExecution(ctx context.Context, id string) (Execution, []AuditEvent, error) {
	if !validUUID(id) {
		return Execution{}, nil, notFound("execution not found", "execução não encontrada")
	}
	ex, err := scanExecution(s.o.Pool.QueryRow(ctx, executionSelect(math.MaxInt32)+`WHERE e.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, nil, notFound("execution not found", "execução não encontrada")
	}
	if err != nil {
		return Execution{}, nil, err
	}
	rows, err := s.o.Pool.Query(ctx, `SELECT id::text, ts, server_slug, tool_name, decision, reason, latency_ms, result_bytes
		FROM audit_events WHERE execution_id = $1::uuid AND ts >= $2
		ORDER BY ts, id LIMIT $3`, id, ex.StartedAt, executionAuditMax)
	if err != nil {
		return Execution{}, nil, err
	}
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (v AuditEvent, err error) {
		err = r.Scan(&v.ID, &v.TS, &v.ServerSlug, &v.ToolName, &v.Decision, &v.Reason, &v.LatencyMS, &v.ResultBytes)
		return
	})
	return ex, events, err
}

func (s *Service) ListAdminEvents(ctx context.Context, actorID string, q PageQuery) (Page[AdminEvent], error) {
	cursor, err := q.uuidCursor()
	if err != nil {
		return Page[AdminEvent]{}, err
	}
	if actorID != "" && !validUUID(actorID) {
		return Page[AdminEvent]{}, nil
	}
	limit := q.limit()
	rows, err := s.o.Pool.Query(ctx, `SELECT a.id::text, a.ts, a.actor_user_id::text, COALESCE(u.email, ''), a.action, a.target, a.details, a.ip
		FROM admin_events a LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE ($1 = '' OR a.actor_user_id = NULLIF($1, '')::uuid) AND ($2 = '' OR a.id < NULLIF($2, '')::uuid)
		ORDER BY a.id DESC LIMIT $3`, actorID, cursor, limit+1)
	if err != nil {
		return Page[AdminEvent]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (v AdminEvent, err error) {
		err = r.Scan(&v.ID, &v.TS, &v.ActorID, &v.ActorEmail, &v.Action, &v.Target, &v.Details, &v.IP)
		return
	})
	if err != nil {
		return Page[AdminEvent]{}, err
	}
	return pageRows(items, limit, func(v AdminEvent) string { return v.ID }), nil
}
