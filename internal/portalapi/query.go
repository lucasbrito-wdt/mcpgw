package portalapi

import (
	"context"
	"fmt"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

// connectionsCTE lists the servers the user has a ceiling on, with the connection in effect.
// p = the user's representative personal row (the default profile, else an active one; the per-profile
// rows are listed by profilesFor), so a server never appears twice, sh = the server's active shared row, c = the row in effect:
// personal when active, else the shared one, else the (inactive) personal row so its status shows.
// An OAuth credential with a refresh token renews itself, so only rows without one can expire.
// Details of a shared row (label, last4, expiry) are never selected: only the personal row's.
const connectionsCTE = `WITH base AS (
	SELECT s.id AS server_id, s.slug,
		CASE s.auth_kind WHEN 'static_header' THEN 'header' ELSE s.auth_kind END AS auth_kind,
		s.category, tc.max_level AS level,
		CASE WHEN s.auth_kind = 'none' OR c.id IS NULL OR c.status <> 'active' THEN 'none'
		     WHEN c.shared THEN 'shared' ELSE 'personal' END AS connection,
		CASE WHEN s.auth_kind = 'none' THEN 'ready'
		     WHEN c.id IS NULL THEN 'not_connected'
		     WHEN c.status <> 'active' THEN c.status
		     WHEN c.refresh_ciphertext IS NULL AND c.expires_at < now() THEN 'expired'
		     WHEN c.refresh_ciphertext IS NULL AND c.expires_at < now() + interval '7 days' THEN 'expiring'
		     ELSE 'connected' END AS conn_status,
		CASE WHEN NOT c.shared THEN c.account_label END AS account_label,
		p.last_used_at, COALESCE(p.last_used_at, '-infinity'::timestamptz) AS lu_key,
		(SELECT count(*) FROM tools t WHERE t.server_id = s.id AND t.status = 'active') AS tool_count,
		CASE WHEN NOT c.shared THEN c.last4 END AS last4,
		CASE WHEN NOT c.shared THEN c.expires_at END AS expires_at,
		CASE WHEN NOT c.shared THEN c.scope END AS scope,
		CASE WHEN NOT c.shared THEN c.revoked_reason END AS revoked_reason,
		CASE WHEN NOT c.shared THEN c.created_at END AS cred_created_at,
		CASE WHEN NOT c.shared THEN c.updated_at END AS cred_updated_at,
		CASE WHEN NOT c.shared THEN c.status END AS cred_status,
		sh.id IS NOT NULL AS shared_available, sh.last4 AS shared_last4,
		s.header_help_url, s.header_format_hint, s.credential_env, s.connection_env, s.connection_url,
		COALESCE(s.header_name, '') <> '' AS has_header
	FROM trust_ceilings tc
	JOIN upstream_servers s ON s.id = tc.server_id AND s.status = 'active'
	LEFT JOIN LATERAL (SELECT x.* FROM upstream_credentials x WHERE x.server_id = s.id AND x.user_id = tc.user_id
		ORDER BY x.profile = 'default' DESC, x.status = 'active' DESC, x.updated_at DESC LIMIT 1) p ON true
	LEFT JOIN upstream_credentials sh ON sh.server_id = s.id AND sh.shared AND sh.status = 'active'
	LEFT JOIN LATERAL (SELECT x.* FROM upstream_credentials x
		WHERE x.id = CASE WHEN p.status = 'active' OR sh.id IS NULL THEN p.id ELSE sh.id END) c ON true
	WHERE tc.user_id = $1::uuid
)`

const rowColumns = `server_id::text, slug, auth_kind, category, level, connection, conn_status, account_label,
	last_used_at, tool_count, last4, expires_at, scope, revoked_reason, cred_created_at, cred_updated_at,
	cred_status, shared_available, shared_last4, header_help_url, header_format_hint, credential_env, connection_env, connection_url, has_header`

// connRow is one server as the user sees it; the first fields feed the list, all feed the detail.
type connRow struct {
	ServerID, Slug, AuthKind   string
	Category                   *string
	Level, Connection, Status  string
	AccountLabel               *string
	LastUsedAt                 *time.Time
	ToolCount                  int
	Last4                      *string
	ExpiresAt                  *time.Time
	Scope                      *string
	RevokedReason              *string
	CredCreatedAt, CredUpdated *time.Time
	CredStatus                 *string
	SharedAvailable            bool
	SharedLast4                *string
	HelpURL, FormatHint        *string
	CredentialEnv              *string
	ConnectionEnv              []string
	ConnectionURL              bool
	HasHeader                  bool // the server has header_name: it takes a pasted token
}

// authMethods lists how a user can connect: oauth, token, both (an oauth server with an alternative
// header) or none. Never nil.
func (c connRow) authMethods() []string {
	switch {
	case c.AuthKind == "oauth" && c.HasHeader:
		return []string{"oauth", "token"}
	case c.AuthKind == "oauth":
		return []string{"oauth"}
	case c.AuthKind == "header":
		return []string{"token"}
	}
	return []string{}
}

func scanConn(row pgx.Row) (c connRow, err error) {
	err = row.Scan(&c.ServerID, &c.Slug, &c.AuthKind, &c.Category, &c.Level, &c.Connection, &c.Status, &c.AccountLabel,
		&c.LastUsedAt, &c.ToolCount, &c.Last4, &c.ExpiresAt, &c.Scope, &c.RevokedReason, &c.CredCreatedAt, &c.CredUpdated,
		&c.CredStatus, &c.SharedAvailable, &c.SharedLast4, &c.HelpURL, &c.FormatHint, &c.CredentialEnv, &c.ConnectionEnv, &c.ConnectionURL, &c.HasHeader)
	return c, err
}

// connectionBySlug reads one server's row; pgx.ErrNoRows when the user has no ceiling on it.
func (a *API) connectionBySlug(ctx context.Context, uid, slug string) (connRow, error) {
	return scanConn(a.o.Pool.QueryRow(ctx, connectionsCTE+` SELECT `+rowColumns+` FROM base WHERE slug = $2`, uid, slug))
}

var (
	validStatuses = map[string]bool{"connected": true, "expiring": true, "expired": true, "refresh_failed": true,
		"revoked_by_admin": true, "scopes_changed": true, "not_connected": true, "ready": true}
	validAuthKinds = map[string]bool{"none": true, "oauth": true, "header": true}
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// summaryStatuses are always present in the summary, zero or not.
	summaryStatuses = []string{"connected", "expiring", "expired", "refresh_failed", "revoked_by_admin", "not_connected", "ready"}
)

const (
	sortName     = "name"
	sortLastUsed = "last_used"
	negInfinity  = "-infinity"
)

type listFilter struct {
	q, status, auth, category, sort string
	limit                           int
	cursorKey, cursorID             string // zero: first page
}

func parseListFilter(r *http.Request, limit int) (listFilter, error) {
	f := listFilter{q: strings.TrimSpace(r.FormValue("q")), status: r.FormValue("status"), auth: r.FormValue("auth"),
		category: r.FormValue("category"), sort: r.FormValue("sort"), limit: limit}
	if f.sort == "" {
		f.sort = sortName
	}
	switch {
	case f.status != "" && !validStatuses[f.status]:
		return f, admin.NewInvalid("status", i18n.T("invalid status", "status inválido"))
	case f.auth != "" && !validAuthKinds[f.auth]:
		return f, admin.NewInvalid("auth", i18n.T("invalid authentication type", "tipo de autenticação inválido"))
	case f.sort != sortName && f.sort != sortLastUsed:
		return f, admin.NewInvalid("sort", i18n.T("invalid sort order", "ordenação inválida"))
	}
	if raw := r.FormValue("cursor"); raw != "" {
		k, id, err := apijson.DecodeCursor(raw)
		if err == nil && !uuidPattern.MatchString(id) {
			err = admin.NewInvalid("cursor", i18n.T("invalid cursor", "cursor inválido"))
		}
		if err == nil && f.sort == sortLastUsed && k != negInfinity {
			if _, perr := time.Parse(time.RFC3339Nano, k); perr != nil {
				err = admin.NewInvalid("cursor", i18n.T("invalid cursor", "cursor inválido"))
			}
		}
		if err != nil {
			return f, err
		}
		f.cursorKey, f.cursorID = k, id
	}
	return f, nil
}

// likeEscaper makes a string literal inside an ILIKE pattern (escape character: backslash).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// filterSQL returns the WHERE conditions of the filters (not the cursor) and their args, which
// start at $2 ($1 is the user).
func (f listFilter) filterSQL() (conds []string, args []any) {
	add := func(format string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(format, len(args)+1))
	}
	if f.q != "" {
		add(`slug ILIKE $%d ESCAPE '\'`, "%"+likeEscaper.Replace(f.q)+"%")
	}
	if f.status != "" {
		add(`conn_status = $%d`, f.status)
	}
	if f.auth != "" {
		add(`auth_kind = $%d`, f.auth)
	}
	if f.category != "" {
		add(`category = $%d`, f.category)
	}
	return conds, args
}

func where(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

// listPage returns one keyset page plus the status counts of the whole filtered set.
func (a *API) listPage(ctx context.Context, uid string, f listFilter) (rows []connRow, summary map[string]int, next string, err error) {
	conds, args := f.filterSQL()
	if summary, err = a.summary(ctx, uid, conds, args); err != nil {
		return nil, nil, "", err
	}
	order := "slug, server_id"
	if f.sort == sortLastUsed {
		order = "lu_key DESC, server_id"
	}
	if f.cursorID != "" {
		args = append(args, f.cursorKey, f.cursorID)
		k, id := len(args), len(args)+1 // placeholders of the two cursor args ($1 is the user)
		if f.sort == sortLastUsed {
			conds = append(conds, fmt.Sprintf(`(lu_key < $%d::text::timestamptz OR (lu_key = $%d::text::timestamptz AND server_id > $%d::uuid))`, k, k, id))
		} else {
			conds = append(conds, fmt.Sprintf(`(slug, server_id) > ($%d, $%d::uuid)`, k, id))
		}
	}
	q := fmt.Sprintf(`%s SELECT %s FROM base%s ORDER BY %s LIMIT %d`, connectionsCTE, rowColumns, where(conds), order, f.limit+1)
	pgRows, err := a.o.Pool.Query(ctx, q, append([]any{uid}, args...)...)
	if err != nil {
		return nil, nil, "", err
	}
	defer pgRows.Close()
	for pgRows.Next() {
		c, err := scanConn(pgRows)
		if err != nil {
			return nil, nil, "", err
		}
		rows = append(rows, c)
	}
	if err := pgRows.Err(); err != nil {
		return nil, nil, "", err
	}
	if len(rows) > f.limit {
		rows = rows[:f.limit]
		next = apijson.EncodeCursor(rows[f.limit-1].sortKey(f.sort), rows[f.limit-1].ServerID)
	}
	return rows, summary, next, nil
}

func (c connRow) sortKey(sort string) string {
	if sort != sortLastUsed {
		return c.Slug
	}
	if c.LastUsedAt == nil {
		return negInfinity
	}
	return c.LastUsedAt.UTC().Format(time.RFC3339Nano)
}

func (a *API) summary(ctx context.Context, uid string, conds []string, args []any) (map[string]int, error) {
	rows, err := a.o.Pool.Query(ctx, connectionsCTE+` SELECT conn_status, count(*) FROM base`+where(conds)+` GROUP BY 1`,
		append([]any{uid}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int, len(summaryStatuses))
	for _, s := range summaryStatuses {
		out[s] = 0
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}
