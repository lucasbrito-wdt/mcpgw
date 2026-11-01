package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

const (
	defaultStatsWindow = "1h"
	defaultStatsTop    = 10
	maxStatsTop        = 200
)

// statsWindows maps a window name to its length and bucket size.
var statsWindows = map[string]struct{ length, bucket time.Duration }{
	"5m":  {5 * time.Minute, 10 * time.Second},
	"15m": {15 * time.Minute, 30 * time.Second},
	"1h":  {time.Hour, time.Minute},
	"24h": {24 * time.Hour, 15 * time.Minute},
	"7d":  {7 * 24 * time.Hour, 3 * time.Hour},
}

// StatsQuery selects the window (default 1h), an optional server slug and the size of the top
// lists (default 10, max 200). Now is for tests; zero means time.Now().
type StatsQuery struct {
	Window string
	Server string
	Top    int
	Now    time.Time
}

// The json tags are the contract of web/admin/lib/stats-types.ts.
type StatsTotals struct {
	Calls            int      `json:"calls"`
	Allowed          int      `json:"allowed"`
	Denied           int      `json:"denied"`
	Executions       int      `json:"executions"`
	FailedExecutions int      `json:"failed_executions"`
	P50MS            *float64 `json:"p50_ms"`
	P95MS            *float64 `json:"p95_ms"`
}

type StatsBucket struct {
	TS     time.Time `json:"ts"`
	Calls  int       `json:"calls"`
	Denied int       `json:"denied"`
	P95MS  *float64  `json:"p95_ms"`
}

type ToolStat struct {
	Server string   `json:"server"`
	Tool   string   `json:"tool"`
	Calls  int      `json:"calls"`
	Denied int      `json:"denied"`
	P95MS  *float64 `json:"p95_ms"`
}

type DenyStat struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type ServerStat struct {
	Server string   `json:"server"`
	Calls  int      `json:"calls"`
	Denied int      `json:"denied"`
	P95MS  *float64 `json:"p95_ms"`
}

type Stats struct {
	Window        string        `json:"window"`
	From          time.Time     `json:"from"`
	To            time.Time     `json:"to"`
	BucketSeconds int           `json:"bucket_seconds"`
	Totals        StatsTotals   `json:"totals"`
	Series        []StatsBucket `json:"series"`
	TopTools      []ToolStat    `json:"top_tools"`
	TopDenies     []DenyStat    `json:"top_denies"`
	Servers       []ServerStat  `json:"servers"`
}

const p95 = `round(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms))`

// auditWindow filters audit_events to [$1, $2) and the optional server $3.
const auditWindow = `FROM audit_events WHERE ts >= $1 AND ts < $2 AND ($3 = '' OR server_slug = $3)`

// Stats aggregates audit_events and executions over the window. The window ends at the next
// bucket boundary after now, so the newest bucket always holds the current moment.
func (s *Service) Stats(ctx context.Context, q StatsQuery) (Stats, error) {
	if q.Window == "" {
		q.Window = defaultStatsWindow
	}
	w, ok := statsWindows[q.Window]
	if !ok {
		return Stats{}, invalid("window", "invalid window: use 5m, 15m, 1h, 24h or 7d", "janela inválida: use 5m, 15m, 1h, 24h ou 7d")
	}
	if q.Top == 0 {
		q.Top = defaultStatsTop
	}
	if q.Top < 1 || q.Top > maxStatsTop {
		return Stats{}, invalid("top", "invalid top: use a value between 1 and %d", "top inválido: use um valor entre 1 e %d", maxStatsTop)
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	to := q.Now.UTC().Truncate(w.bucket)
	if to.Before(q.Now) {
		to = to.Add(w.bucket)
	}
	st := Stats{Window: q.Window, From: to.Add(-w.length), To: to, BucketSeconds: int(w.bucket / time.Second)}
	args := []any{st.From, st.To, q.Server}
	var err error
	if err = s.statsTotals(ctx, &st, args); err == nil {
		err = s.statsSeries(ctx, &st, args)
	}
	if err == nil {
		err = s.statsTops(ctx, &st, args, q.Top)
	}
	return st, err
}

func (s *Service) statsTotals(ctx context.Context, st *Stats, args []any) error {
	t := &st.Totals
	err := s.o.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE decision = 'allow'), count(*) FILTER (WHERE decision = 'deny'),
		round(percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_ms)), `+p95+` `+auditWindow, args...).
		Scan(&t.Calls, &t.Allowed, &t.Denied, &t.P50MS, &t.P95MS)
	if err != nil {
		return err
	}
	return s.o.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status NOT IN ('running', 'succeeded'))
		FROM executions WHERE started_at >= $1 AND started_at < $2`, st.From, st.To).Scan(&t.Executions, &t.FailedExecutions)
}

func (s *Service) statsSeries(ctx context.Context, st *Stats, args []any) error {
	rows, err := s.o.Pool.Query(ctx, `WITH agg AS (
			SELECT date_bin($4::int * interval '1 second', ts, $1::timestamptz) AS b, count(*) AS calls,
				count(*) FILTER (WHERE decision = 'deny') AS denied, `+p95+` AS p95 `+auditWindow+` GROUP BY 1)
		SELECT g, COALESCE(agg.calls, 0), COALESCE(agg.denied, 0), agg.p95
		FROM generate_series($1::timestamptz, $2::timestamptz - $4::int * interval '1 second', $4::int * interval '1 second') g
		LEFT JOIN agg ON agg.b = g ORDER BY g`, append(args, st.BucketSeconds)...)
	if err != nil {
		return err
	}
	st.Series, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (v StatsBucket, err error) {
		err = r.Scan(&v.TS, &v.Calls, &v.Denied, &v.P95MS)
		return
	})
	return err
}

func (s *Service) statsTops(ctx context.Context, st *Stats, args []any, top int) (err error) {
	rows, err := s.o.Pool.Query(ctx, `SELECT server_slug, tool_name, count(*), count(*) FILTER (WHERE decision = 'deny'), `+p95+` `+auditWindow+`
		GROUP BY 1, 2 ORDER BY 3 DESC, 1, 2 LIMIT $4`, append(args, top)...)
	if err != nil {
		return err
	}
	if st.TopTools, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (v ToolStat, err error) {
		err = r.Scan(&v.Server, &v.Tool, &v.Calls, &v.Denied, &v.P95MS)
		return
	}); err != nil {
		return err
	}
	if rows, err = s.o.Pool.Query(ctx, `SELECT server_slug, tool_name, reason, count(*) `+auditWindow+` AND decision = 'deny'
		GROUP BY 1, 2, 3 ORDER BY 4 DESC, 1, 2, 3 LIMIT $4`, append(args, top)...); err != nil {
		return err
	}
	if st.TopDenies, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (v DenyStat, err error) {
		err = r.Scan(&v.Server, &v.Tool, &v.Reason, &v.Count)
		return
	}); err != nil {
		return err
	}
	if rows, err = s.o.Pool.Query(ctx, `SELECT server_slug, count(*), count(*) FILTER (WHERE decision = 'deny'), `+p95+` `+auditWindow+`
		GROUP BY 1 ORDER BY 2 DESC, 1`, args...); err != nil {
		return err
	}
	st.Servers, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (v ServerStat, err error) {
		err = r.Scan(&v.Server, &v.Calls, &v.Denied, &v.P95MS)
		return
	})
	return err
}

const probeTimeout = 20 * time.Second

// ProbeResult is the outcome of one connection test; json tags follow stats-types.ts.
type ProbeResult struct {
	OK           bool    `json:"ok"`
	LatencyMS    int64   `json:"latency_ms"`
	Status       *int    `json:"status"`
	RequiresAuth bool    `json:"requires_auth"`
	Tools        *int    `json:"tools"`
	Error        *string `json:"error"` // PT-BR and scrubbed: never upstream text or a credential
}

// ProbeServer lists the tools of a stored server with its shared credential (none when it has
// no shared one). Nothing is written: no catalog, detection or audit row.
func (s *Service) ProbeServer(ctx context.Context, slug string) (ProbeResult, error) {
	srv, err := serverBySlug(ctx, s.o.Pool, slug)
	if err != nil {
		return ProbeResult{}, err
	}
	mgr, err := s.manager()
	if err != nil {
		return ProbeResult{}, err
	}
	cred, err := mgr.CredentialFor(ctx, srv.ID, "")
	if err != nil && !errors.Is(err, upstreamauth.ErrNotConnected) {
		return failedProbe(0, nil, "Não foi possível carregar a credencial compartilhada do servidor."), nil
	}
	// A stdio server is never started without its credential variable.
	if err != nil && strings.HasPrefix(srv.URL, upstream.StdioKeyPrefix) {
		res := failedProbe(0, nil, "O servidor precisa de credencial: conecte um token no portal ou configure a conexão compartilhada.")
		res.RequiresAuth = true
		return res, nil
	}
	return s.probe(ctx, srv.URL, cred), nil
}

// ProbeURL tests a URL before the server exists, without a credential. Only http transports.
func (s *Service) ProbeURL(ctx context.Context, rawURL, transport string) (ProbeResult, error) {
	switch upstream.TransportKind(transport) {
	case "", upstream.TransportAuto, upstream.TransportStreamable, upstream.TransportSSE:
	case upstream.TransportStdio:
		return ProbeResult{}, invalid("transport", "the URL test only applies to http transports", "o teste de URL só vale para transportes http")
	default:
		return ProbeResult{}, invalid("transport", "invalid transport: use auto, streamable or sse", "transporte inválido: use auto, streamable ou sse")
	}
	if err := ValidateServerURL(rawURL, "none"); err != nil {
		return ProbeResult{}, err
	}
	return s.probe(ctx, rawURL, nil), nil
}

func (s *Service) probe(ctx context.Context, url string, cred upstream.Credential) ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	start := time.Now()
	tools, _, err := s.o.Lister.ListTools(ctx, url, cred)
	latency := time.Since(start).Milliseconds()
	if err == nil {
		n := len(tools)
		return ProbeResult{OK: true, LatencyMS: latency, Tools: &n}
	}
	var status *upstream.HTTPStatusError
	switch {
	case errors.As(err, &status):
		res := failedProbe(latency, &status.Status, fmt.Sprintf("O servidor recusou o acesso (HTTP %d): verifique a credencial.", status.Status))
		res.RequiresAuth = status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden
		return res
	case errors.Is(err, context.DeadlineExceeded):
		return failedProbe(latency, nil, "Tempo esgotado ao conectar (20 s).")
	}
	return failedProbe(latency, nil, "Não foi possível conectar ao servidor ou listar as ferramentas.")
}

func failedProbe(latency int64, status *int, msg string) ProbeResult {
	return ProbeResult{LatencyMS: latency, Status: status, Error: &msg}
}
