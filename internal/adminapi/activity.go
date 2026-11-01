package adminapi

import (
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "GET /admin/api/overview", a.overview)
		a.handle(mux, "GET /admin/api/executions", a.listExecutions)
		a.handle(mux, "GET /admin/api/executions/{id}", a.getExecution)
		a.handle(mux, "GET /admin/api/admin-events", a.listAdminEvents)
	})
}

// Pagination helpers shared by every list endpoint of this package. A cursor wraps one opaque
// service value (a uuid, a "<slug>/<user>" key or a sort key) in apijson's keyset envelope.

func encodeCursor(v string) string {
	if v == "" {
		return ""
	}
	return apijson.EncodeCursor("c", v)
}

func decodeCursor(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	_, id, err := apijson.DecodeCursor(raw)
	return id, err
}

func pageQuery(r *http.Request) (admin.PageQuery, error) {
	limit, err := apijson.Limit(r)
	if err != nil {
		return admin.PageQuery{}, err
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	return admin.PageQuery{Limit: limit, Cursor: cursor}, err
}

func mapSlice[T, U any](in []T, f func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

func writePage[U any](w http.ResponseWriter, items []U, next string) {
	apijson.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": encodeCursor(next)})
}

// listPage serves a service that pages itself (keyset in SQL).
func listPage[T, U any](w http.ResponseWriter, r *http.Request, fetch func(admin.PageQuery) (admin.Page[T], error), conv func(T) U) {
	q, err := pageQuery(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	page, err := fetch(q)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writePage(w, mapSlice(page.Items, conv), page.NextCursor)
}

// writePagedSlice pages a full in-memory list ordered by key (byte order, so the cursor comparison
// matches the sort).
// shortcut: the services return every row for these lists, fine for tens to hundreds of servers,
// tools and ceilings; push a keyset into SQL if they grow to thousands.
func writePagedSlice[T, U any](w http.ResponseWriter, r *http.Request, all []T, key func(T) string, conv func(T) U) {
	q, err := pageQuery(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	all = slices.Clone(all)
	slices.SortFunc(all, func(x, y T) int { return strings.Compare(key(x), key(y)) })
	if q.Cursor != "" {
		all = all[slices.IndexFunc(all, func(v T) bool { return key(v) > q.Cursor }):max(0, len(all))]
	}
	next := ""
	if len(all) > q.Limit {
		all = all[:q.Limit]
		next = key(all[q.Limit-1])
	}
	writePage(w, mapSlice(all, conv), next)
}

type overviewJSON struct {
	Servers          int `json:"servers"`
	ServersDisabled  int `json:"servers_disabled"`
	ToolsActive      int `json:"tools_active"`
	ToolsQuarantined int `json:"tools_quarantined"`
	PendingApprovals int `json:"pending_approvals"`
	Denies24h        int `json:"denies_24h"`
	Executions24h    int `json:"executions_24h"`
}

type executionJSON struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Email         string     `json:"email"`
	ClientID      string     `json:"client_id"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	Code          string     `json:"code"` // untrusted: render as text only
	CodeTruncated bool       `json:"code_truncated"`
	InnerCalls    int        `json:"inner_calls"`
	Error         string     `json:"error"` // untrusted
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

type auditEventJSON struct {
	TS          time.Time `json:"ts"`
	Server      string    `json:"server"`
	Tool        string    `json:"tool"`
	Decision    string    `json:"decision"`
	Reason      string    `json:"reason"`
	LatencyMS   *int      `json:"latency_ms"`
	ResultBytes *int      `json:"result_bytes"`
}

type adminEventJSON struct {
	ID         string         `json:"id"`
	TS         time.Time      `json:"ts"`
	ActorID    string         `json:"actor_id"`
	ActorEmail string         `json:"actor_email"`
	Action     string         `json:"action"`
	Target     string         `json:"target"`
	Details    map[string]any `json:"details"`
	IP         string         `json:"ip"`
}

func toExecutionJSON(e admin.Execution) executionJSON {
	return executionJSON{ID: e.ID, UserID: e.UserID, Email: e.Email, ClientID: e.ClientID, Kind: e.Kind, Status: e.Status,
		Code: e.Code, CodeTruncated: e.CodeTruncated, InnerCalls: e.InnerCalls, Error: e.Error, StartedAt: e.StartedAt, FinishedAt: e.FinishedAt}
}

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	o, err := a.svc.Overview(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, overviewJSON(o))
}

// parseTime reads an optional RFC 3339 query value; a bad one is a field error.
func parseTime(r *http.Request, field string) (*time.Time, error) {
	raw := r.URL.Query().Get(field)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, admin.NewInvalid(field, i18n.T("invalid date: use the RFC 3339 format", "data inválida: use o formato RFC 3339"))
	}
	return &t, nil
}

func (a *API) listExecutions(w http.ResponseWriter, r *http.Request) {
	f := admin.ExecutionFilter{UserID: r.URL.Query().Get("user"), Status: r.URL.Query().Get("status")}
	var err error
	if f.From, err = parseTime(r, "from"); err == nil {
		f.To, err = parseTime(r, "to")
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	listPage(w, r, func(q admin.PageQuery) (admin.Page[admin.Execution], error) {
		return a.svc.ListExecutions(r.Context(), f, q)
	}, toExecutionJSON)
}

func (a *API) getExecution(w http.ResponseWriter, r *http.Request) {
	ex, events, err := a.svc.GetExecution(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]any{"execution": toExecutionJSON(ex), "audit_events": mapSlice(events,
		func(e admin.AuditEvent) auditEventJSON {
			return auditEventJSON{TS: e.TS, Server: e.ServerSlug, Tool: e.ToolName, Decision: e.Decision, Reason: e.Reason,
				LatencyMS: e.LatencyMS, ResultBytes: e.ResultBytes}
		})})
}

func (a *API) listAdminEvents(w http.ResponseWriter, r *http.Request) {
	actor := r.URL.Query().Get("actor")
	listPage(w, r, func(q admin.PageQuery) (admin.Page[admin.AdminEvent], error) {
		return a.svc.ListAdminEvents(r.Context(), actor, q)
	},
		func(e admin.AdminEvent) adminEventJSON {
			return adminEventJSON{ID: e.ID, TS: e.TS, ActorID: e.ActorID, ActorEmail: e.ActorEmail, Action: e.Action, Target: e.Target,
				Details: e.Details, IP: e.IP}
		})
}
