package portalapi

import (
	"context"
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

const (
	maxBulkSlugs     = 100
	tokenTestTimeout = 20 * time.Second
)

var msgNotFound = i18n.T("Server not found.", "Servidor não encontrado.")

// serverRef is a server the session user has a ceiling on; the isolation choke point returns it.
type serverRef struct {
	ID, Slug, AuthKind string
	FormatHint         *string
}

// serverForUser resolves {slug} for the session user. A missing slug, a slug without a ceiling
// and a disabled server all answer the same 404, so nothing can be enumerated. On false the
// response was already written.
func (a *API) serverForUser(w http.ResponseWriter, r *http.Request, uid, slug string) (serverRef, bool) {
	var ref serverRef
	err := a.o.Pool.QueryRow(r.Context(), `SELECT s.id::text, s.slug, s.auth_kind, s.header_format_hint
		FROM upstream_servers s JOIN trust_ceilings tc ON tc.server_id = s.id
		WHERE tc.user_id = $1::uuid AND s.slug = $2 AND s.status = 'active'`, uid, slug).
		Scan(&ref.ID, &ref.Slug, &ref.AuthKind, &ref.FormatHint)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		apijson.WriteErrorFor(w, r, http.StatusNotFound, "not_found", msgNotFound, nil)
		return ref, false
	case err != nil:
		apijson.FromError(w, r, err)
		return ref, false
	}
	return ref, true
}

// displayName derives a name from the slug ("gitlab_ci" -> "Gitlab Ci"); servers have no name column.
func displayName(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool { return r == '_' || r == '-' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

type appJSON struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	AuthKind string `json:"auth_kind"`
	// AuthMethods are the ways to connect: ["oauth"], ["token"], ["oauth","token"] or [].
	AuthMethods  []string   `json:"auth_methods"`
	Category     *string    `json:"category"`
	Connection   string     `json:"connection"`
	Status       string     `json:"status"`
	AccountLabel *string    `json:"account_label"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	ToolCount    int        `json:"tool_count"`
	Level        string     `json:"level"`
	// CredentialEnv is set for stdio servers: the token is passed as this environment variable,
	// not as an HTTP header.
	CredentialEnv *string `json:"credential_env"`
	// ConnectionEnv are the variables each connection sets for itself (names only).
	ConnectionEnv []connectionVarJSON `json:"connection_env"`
	// ConnectionURL: each connection informs its own MCP URL (url on token writes and tests).
	ConnectionURL bool `json:"connection_url"`
	// Profiles are the user's personal connections, one per profile name.
	Profiles []profileJSON `json:"profiles"`
}

func (a *API) listConnections(w http.ResponseWriter, r *http.Request, u user) {
	limit, err := apijson.Limit(r)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	f, err := parseListFilter(r, limit)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	rows, summary, next, err := a.listPage(r.Context(), u.ID, f)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	profiles, err := a.profilesFor(r.Context(), u.ID, rows)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	vars, err := a.connectionVarsFor(r.Context(), rows)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	items := make([]appJSON, len(rows))
	for i, c := range rows {
		items[i] = appJSON{Slug: c.Slug, Name: displayName(c.Slug), AuthKind: c.AuthKind, AuthMethods: c.authMethods(), Category: c.Category,
			Connection: c.Connection, Status: c.Status, AccountLabel: c.AccountLabel, LastUsedAt: utc(c.LastUsedAt),
			ToolCount: c.ToolCount, Level: c.Level, CredentialEnv: c.CredentialEnv, ConnectionEnv: vars[c.ServerID],
			ConnectionURL: c.ConnectionURL, Profiles: profiles[c.ServerID]}
	}
	var nextCursor *string
	if next != "" {
		nextCursor = &next
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]any{"summary": summary, "items": items, "next_cursor": nextCursor})
}

type timelineEvent struct {
	TS    time.Time `json:"ts"`
	Event string    `json:"event"`
}

type toolJSON struct {
	Name  string `json:"name"`
	Level string `json:"level"`
}

type detailJSON struct {
	Slug            string              `json:"slug"`
	Name            string              `json:"name"`
	AuthKind        string              `json:"auth_kind"`
	AuthMethods     []string            `json:"auth_methods"`
	Category        *string             `json:"category"`
	Connection      string              `json:"connection"`
	Status          string              `json:"status"`
	AccountLabel    *string             `json:"account_label"`
	Last4           *string             `json:"last4"`
	SharedAvailable bool                `json:"shared_available"`
	ExpiresAt       *time.Time          `json:"expires_at"`
	ScopeGranted    string              `json:"scope_granted"`
	HelpURL         *string             `json:"help_url"`
	FormatHint      *string             `json:"format_hint"`
	CredentialEnv   *string             `json:"credential_env"`
	ConnectionEnv   []connectionVarJSON `json:"connection_env"`
	ConnectionURL   bool                `json:"connection_url"`
	Timeline        []timelineEvent     `json:"timeline"`
	Tools           []toolJSON          `json:"tools"`
	RevokedReason   *string             `json:"revoked_reason"`
	Profiles        []profileJSON       `json:"profiles"`
}

// adminDetailJSON adds the shared token's last4, which only admins see.
type adminDetailJSON struct {
	detailJSON
	SharedLast4 *string `json:"shared_last4"`
}

func (a *API) getConnection(w http.ResponseWriter, r *http.Request, u user) {
	ref, ok := a.serverForUser(w, r, u.ID, r.PathValue("slug"))
	if !ok {
		return
	}
	c, err := a.connectionBySlug(r.Context(), u.ID, ref.Slug)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	tools, err := a.activeTools(r.Context(), ref.ID)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	profiles, err := a.profilesFor(r.Context(), u.ID, []connRow{c})
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	vars, err := a.connectionVarsFor(r.Context(), []connRow{c})
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	d := detailJSON{Slug: c.Slug, Name: displayName(c.Slug), AuthKind: c.AuthKind, AuthMethods: c.authMethods(), Category: c.Category,
		Connection: c.Connection, Status: c.Status, AccountLabel: c.AccountLabel, Last4: c.Last4,
		SharedAvailable: c.SharedAvailable, ExpiresAt: utc(c.ExpiresAt), HelpURL: c.HelpURL, FormatHint: c.FormatHint,
		CredentialEnv: c.CredentialEnv, ConnectionEnv: vars[c.ServerID], ConnectionURL: c.ConnectionURL,
		Timeline: c.timeline(), Tools: tools, RevokedReason: c.RevokedReason,
		Profiles: profiles[c.ServerID]}
	if c.Scope != nil {
		d.ScopeGranted = *c.Scope
	}
	if u.IsAdmin {
		apijson.WriteJSON(w, http.StatusOK, adminDetailJSON{detailJSON: d, SharedLast4: c.SharedLast4})
		return
	}
	apijson.WriteJSON(w, http.StatusOK, d)
}

// timeline is derived from the personal credential row: connected, last used, and why it stopped.
func (c connRow) timeline() []timelineEvent {
	events := []timelineEvent{}
	if c.CredCreatedAt != nil {
		events = append(events, timelineEvent{c.CredCreatedAt.UTC(), "connected"})
	}
	if c.LastUsedAt != nil {
		events = append(events, timelineEvent{c.LastUsedAt.UTC(), "last_used"})
	}
	if c.CredStatus != nil && *c.CredStatus != "active" && c.CredUpdated != nil {
		events = append(events, timelineEvent{c.CredUpdated.UTC(), *c.CredStatus})
	}
	return events
}

func (a *API) activeTools(ctx context.Context, serverID string) ([]toolJSON, error) {
	rows, err := a.o.Pool.Query(ctx, `SELECT name, trust_level FROM tools
		WHERE server_id = $1::uuid AND status = 'active' ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tools := []toolJSON{}
	for rows.Next() {
		var t toolJSON
		if err := rows.Scan(&t.Name, &t.Level); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// connectBody is the body of the writes that can target the shared connection. Env (servers with
// connection_env) replaces the connection's own variable values when present; URL (servers with
// connection_url) is the connection's MCP address, "" keeps the stored one. With Token "" only Env
// and URL change and the connection must already exist.
type connectBody struct {
	Token    string            `json:"token"`
	Shared   bool              `json:"shared"`
	Password string            `json:"password"`
	Env      map[string]string `json:"env"`
	URL      string            `json:"url"`
}

func (a *API) startOAuth(w http.ResponseWriter, r *http.Request, u user) {
	ref, ok := a.serverForUser(w, r, u.ID, r.PathValue("slug"))
	if !ok {
		return
	}
	var in struct {
		Shared   bool   `json:"shared"`
		Password string `json:"password"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	profile, ok := profileParam(w, r)
	if !ok {
		return
	}
	if in.Shared && !a.requireAdminPassword(w, r, u.ID, in.Password) {
		return
	}
	if ref.AuthKind != upstreamauth.AuthKindOAuth {
		apijson.WriteErrorFor(w, r, http.StatusNotFound, "not_found", msgNotFound, nil)
		return
	}
	url := a.o.Credentials.ConnectURLForProfile(ref.Slug, profile)
	if in.Shared {
		url = a.o.Credentials.SharedConnectURL(u.ID, ref.Slug)
	}
	if url == "" {
		apijson.FromError(w, r, errors.New("portalapi: could not build the connect URL"))
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]string{"redirect_url": url})
}

func (a *API) testToken(w http.ResponseWriter, r *http.Request, u user) {
	ref, ok := a.serverForUser(w, r, u.ID, r.PathValue("slug"))
	if !ok {
		return
	}
	var in struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	profile, ok := profileParam(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tokenTestTimeout)
	defer cancel()
	res, err := a.o.Credentials.TestConnection(ctx, a.o.Lister, upstreamauth.ConnectionInput{
		ServerID: ref.ID, ActorID: u.ID, Profile: profile, Token: in.Token, URL: in.URL})
	if err != nil {
		a.writeTokenError(w, r, ref, err)
		return
	}
	var label *string
	if res.AccountLabel != "" {
		label = &res.AccountLabel
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]any{"tools": res.Tools, "account_label": label})
}

func (a *API) putToken(w http.ResponseWriter, r *http.Request, u user) {
	ref, ok := a.serverForUser(w, r, u.ID, r.PathValue("slug"))
	if !ok {
		return
	}
	var in connectBody
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	if in.Shared {
		a.putSharedToken(w, r, u, ref, in)
		return
	}
	profile, ok := profileParam(w, r)
	if !ok {
		return
	}
	last4, err := a.o.Credentials.SetConnection(r.Context(), a.o.Pool, upstreamauth.ConnectionInput{
		ServerID: ref.ID, ActorID: u.ID, Profile: profile, Token: in.Token, Env: in.Env, URL: in.URL})
	if err != nil {
		a.writeTokenError(w, r, ref, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]string{"last4": last4})
}

// writeTokenError maps the token errors of upstreamauth. None of the messages echoes the token.
func (a *API) writeTokenError(w http.ResponseWriter, r *http.Request, ref serverRef, err error) {
	var rejected *upstreamauth.UpstreamRejectedError
	var urlErr *upstreamauth.URLError
	switch {
	case writeConnectionEnvError(w, r, err):
	case errors.As(err, &urlErr):
		apijson.FromError(w, r, admin.NewInvalid("url", urlErr.Message))
	case errors.As(err, &rejected):
		apijson.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{
			"code": "upstream_rejected", "status": rejected.Status,
			"message": i18n.T("The server rejected the token (HTTP %d).", "O servidor recusou o token (HTTP %d).", rejected.Status).In(i18n.FromContext(r.Context())),
		}})
	case errors.Is(err, upstreamauth.ErrTokenFormat):
		msg := i18n.T("The token does not have the expected format.", "O token não tem o formato esperado.")
		if ref.FormatHint != nil {
			msg = i18n.T("The token does not have the expected format (starts with %s).", "O token não tem o formato esperado (começa com %s).", *ref.FormatHint)
		}
		apijson.FromError(w, r, admin.NewInvalid("token", msg))
	case errors.Is(err, upstreamauth.ErrInvalidToken):
		apijson.FromError(w, r, admin.NewInvalid("token", i18n.T("Invalid token: use 8 to 4096 characters, without line breaks.", "Token inválido: use de 8 a 4096 caracteres, sem quebras de linha.")))
	case errors.Is(err, upstreamauth.ErrTokenNotSupported):
		apijson.WriteErrorFor(w, r, http.StatusUnprocessableEntity, "token_not_supported",
			i18n.T("This server does not accept a pasted token: connect through the account.", "Este servidor não aceita token colado: conecte pela conta."), nil)
	case errors.Is(err, upstreamauth.ErrTokenTestUnsupported):
		apijson.WriteErrorFor(w, r, http.StatusUnprocessableEntity, "test_unavailable",
			i18n.T("Test not available for stdio servers: save the token and use the server to validate it.", "Teste não disponível para servidores stdio: salve o token e use o servidor para validá-lo."), nil)
	case errors.Is(err, upstreamauth.ErrServerNotFound):
		apijson.WriteErrorFor(w, r, http.StatusNotFound, "not_found", msgNotFound, nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		apijson.WriteErrorFor(w, r, http.StatusGatewayTimeout, "upstream_unavailable", i18n.T("The server took too long to respond.", "O servidor demorou demais para responder."), nil)
	default:
		a.o.Logger.Error("portalapi: token request failed", "slug", ref.Slug, "err", err)
		apijson.WriteErrorFor(w, r, http.StatusBadGateway, "upstream_unavailable", i18n.T("Could not reach the server. Try again.", "Não foi possível falar com o servidor. Tente novamente."), nil)
	}
}

func (a *API) deleteConnection(w http.ResponseWriter, r *http.Request, u user) {
	ref, ok := a.serverForUser(w, r, u.ID, r.PathValue("slug"))
	if !ok {
		return
	}
	if r.URL.Query().Get("shared") == "1" {
		a.deleteShared(w, r, u, ref)
		return
	}
	profile, ok := profileParam(w, r)
	if !ok {
		return
	}
	if err := a.o.Credentials.DisconnectProfile(r.Context(), a.o.Pool, ref.ID, u.ID, profile); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// bulkDisconnect removes all of the user's own personal profiles on each server (a slug is reported
// as disconnected only if a row was deleted); shared rows and slugs the user has
// no ceiling on are never touched (the latter are reported as skipped).
func (a *API) bulkDisconnect(w http.ResponseWriter, r *http.Request, u user) {
	var in struct {
		Slugs []string `json:"slugs"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	if len(in.Slugs) == 0 || len(in.Slugs) > maxBulkSlugs {
		apijson.FromError(w, r, &admin.InvalidError{Field: "slugs", Message: "Informe de 1 a 100 servidores."})
		return
	}
	granted, err := a.grantedServers(r.Context(), u.ID, in.Slugs)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	disconnected, skipped := []string{}, []string{}
	seen := map[string]bool{}
	err = pgxTx(r.Context(), a, func(tx pgx.Tx) error {
		for _, slug := range in.Slugs {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			id, ok := granted[slug]
			if !ok {
				skipped = append(skipped, slug)
				continue
			}
			tag, err := tx.Exec(r.Context(), `DELETE FROM upstream_credentials
				WHERE server_id = $1::uuid AND user_id = $2::uuid AND NOT shared`, id, u.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				disconnected = append(disconnected, slug)
			}
		}
		return nil
	})
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]any{"disconnected": disconnected, "skipped": skipped})
}

// grantedServers maps the requested slugs the user has a ceiling on (active servers) to their ids.
func (a *API) grantedServers(ctx context.Context, uid string, slugs []string) (map[string]string, error) {
	rows, err := a.o.Pool.Query(ctx, `SELECT s.slug, s.id::text FROM upstream_servers s
		JOIN trust_ceilings tc ON tc.server_id = s.id
		WHERE tc.user_id = $1::uuid AND s.status = 'active' AND s.slug = ANY($2)`, uid, slugs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}

func pgxTx(ctx context.Context, a *API, fn func(pgx.Tx) error) error {
	tx, err := a.o.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
