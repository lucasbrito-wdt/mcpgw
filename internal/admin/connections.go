package admin

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

const maxRevokeReasonChars = 500

// ErrPrefixInValue: the shared header value already starts with the prefix being set, so sending
// prefix + value would double it (plan gap G23). The HTTP layer maps it to 422 prefix_already_in_value.
var ErrPrefixInValue = errors.New("shared header value already starts with the prefix")

var errNoGrants = errors.New("admin: grant revoker is not configured")

type SharedConnection struct {
	Status, Last4, AccountLabel, ConnectedByEmail string
	LastUsedAt, ExpiresAt                         *time.Time
	RevokedReason                                 string
}

// ServerConnections summarizes one server's connections. UsingShared counts users with a ceiling
// and no personal row that the shared row serves.
type ServerConnections struct {
	Slug, AuthKind, Category                                                        string
	Shared                                                                          *SharedConnection // nil: no shared row
	Connected, UsingShared, Expiring, Expired, RefreshFailed, Revoked, NotConnected int
}

// UserConnection is one user's connection to a server under one profile (one row per user, server and profile); Status is connected, expiring, expired,
// refresh_failed, revoked_by_admin, shared (served by the shared row) or not_connected.
type UserConnection struct {
	ServerSlug, UserID, Email, Profile, Status, AccountLabel string
	LastUsedAt, ExpiresAt                                    *time.Time
}

type ConnectionFilter struct{ Server, Status string }

// connectionsCTE lists (server, user) pairs that have a ceiling or a personal row, with the status
// computed in SQL. An OAuth row with a refresh token renews itself, so only rows without one
// can expire (same rule as the portal).
const connectionsCTE = `WITH pairs AS (
		SELECT user_id, server_id FROM trust_ceilings
		UNION SELECT user_id, server_id FROM upstream_credentials WHERE user_id IS NOT NULL
	), conn AS (
		SELECT s.slug, p.user_id, u.email, COALESCE(c.profile, 'default') AS profile, COALESCE(c.account_label, '') AS account_label, c.last_used_at, c.expires_at,
			CASE WHEN c.id IS NULL THEN
					CASE WHEN EXISTS (SELECT 1 FROM upstream_credentials h WHERE h.server_id = s.id AND h.shared AND h.status = 'active')
						THEN 'shared' ELSE 'not_connected' END
				WHEN c.status <> 'active' THEN c.status
				WHEN c.refresh_ciphertext IS NOT NULL THEN 'connected'
				WHEN c.expires_at < now() THEN 'expired'
				WHEN c.expires_at < now() + interval '7 days' THEN 'expiring'
				ELSE 'connected' END AS status
		FROM pairs p JOIN upstream_servers s ON s.id = p.server_id AND s.auth_kind <> 'none'
			JOIN users u ON u.id = p.user_id
			LEFT JOIN upstream_credentials c ON c.server_id = p.server_id AND c.user_id = p.user_id
		WHERE ($1 = '' OR s.slug = $1)
	)`

func (s *Service) ListConnections(ctx context.Context, f ConnectionFilter, q PageQuery) ([]ServerConnections, Page[UserConnection], error) {
	servers, err := s.serverConnections(ctx, f.Server)
	if err != nil {
		return nil, Page[UserConnection]{}, err
	}
	users, err := s.userConnections(ctx, f, q)
	return servers, users, err
}

func (s *Service) serverConnections(ctx context.Context, slug string) ([]ServerConnections, error) {
	rows, err := s.o.Pool.Query(ctx, connectionsCTE+`
		SELECT v.slug, CASE WHEN v.auth_kind = 'static_header' THEN 'header' ELSE v.auth_kind END, COALESCE(v.category, ''),
			COALESCE(k.status, ''), COALESCE(k.n, 0)
		FROM upstream_servers v LEFT JOIN (SELECT slug, status, count(*) AS n FROM conn GROUP BY slug, status) k ON k.slug = v.slug
		WHERE v.auth_kind <> 'none' AND ($1 = '' OR v.slug = $1)
		ORDER BY v.slug`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerConnections
	for rows.Next() {
		var slug, kind, category, status string
		var n int
		if err := rows.Scan(&slug, &kind, &category, &status, &n); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Slug != slug {
			out = append(out, ServerConnections{Slug: slug, AuthKind: kind, Category: category})
		}
		out[len(out)-1].count(status, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Shared, err = s.sharedConnection(ctx, out[i].Slug); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *ServerConnections) count(status string, n int) {
	switch status {
	case "connected":
		c.Connected = n
	case "shared":
		c.UsingShared = n
	case "expiring":
		c.Expiring = n
	case "expired":
		c.Expired = n
	case "refresh_failed":
		c.RefreshFailed = n
	case "revoked_by_admin":
		c.Revoked = n
	case "not_connected":
		c.NotConnected = n
	}
}

func (s *Service) sharedConnection(ctx context.Context, slug string) (*SharedConnection, error) {
	var c SharedConnection
	err := s.o.Pool.QueryRow(ctx, `SELECT c.status, COALESCE(c.last4, ''), COALESCE(c.account_label, ''), COALESCE(u.email, ''),
			c.last_used_at, c.expires_at, COALESCE(c.revoked_reason, '')
		FROM upstream_credentials c JOIN upstream_servers s ON s.id = c.server_id LEFT JOIN users u ON u.id = c.connected_by
		WHERE s.slug = $1 AND c.shared`, slug).
		Scan(&c.Status, &c.Last4, &c.AccountLabel, &c.ConnectedByEmail, &c.LastUsedAt, &c.ExpiresAt, &c.RevokedReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

// userConnections pages on (slug, user id, profile) ascending; the cursor is "<slug>/<user id>/<profile>" (slugs and profiles have no "/").
func (s *Service) userConnections(ctx context.Context, f ConnectionFilter, q PageQuery) (Page[UserConnection], error) {
	slug, uid, profile, err := splitConnectionCursor(q.Cursor)
	if err != nil {
		return Page[UserConnection]{}, err
	}
	limit := q.limit()
	rows, err := s.o.Pool.Query(ctx, connectionsCTE+`
		SELECT slug, user_id::text, email, profile, status, account_label, last_used_at, expires_at FROM conn
		WHERE ($2 = '' OR status = $2) AND ($3 = '' OR (slug, user_id, profile) > ($3, $4::uuid, $5))
		ORDER BY slug, user_id, profile LIMIT $6`, f.Server, f.Status, slug, nilUUIDIfEmpty(uid), profile, limit+1)
	if err != nil {
		return Page[UserConnection]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (v UserConnection, err error) {
		err = r.Scan(&v.ServerSlug, &v.UserID, &v.Email, &v.Profile, &v.Status, &v.AccountLabel, &v.LastUsedAt, &v.ExpiresAt)
		return
	})
	if err != nil {
		return Page[UserConnection]{}, err
	}
	return pageRows(items, limit, func(v UserConnection) string { return v.ServerSlug + "/" + v.UserID + "/" + v.Profile }), nil
}

func splitConnectionCursor(cursor string) (slug, uid, profile string, err error) {
	if cursor == "" {
		return "", "", "", nil
	}
	parts := strings.SplitN(cursor, "/", 3)
	if len(parts) == 3 && parts[0] != "" && validUUID(parts[1]) && upstreamauth.ValidProfile(parts[2]) {
		return parts[0], parts[1], parts[2], nil
	}
	return "", "", "", invalid("cursor", "invalid cursor", "cursor inválido")
}

func nilUUIDIfEmpty(uid string) string {
	if uid == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return uid
}

// RevokeConnection (admin) disables a connection and keeps the reason; userID "" targets the
// shared row. The reason is admin text, not a secret.
func (s *Service) RevokeConnection(ctx context.Context, slug, userID, reason string) error {
	if n := utf8.RuneCountInString(reason); n < 1 || n > maxRevokeReasonChars {
		return invalid("reason", "provide a reason of 1 to %d characters", "informe um motivo de 1 a %d caracteres", maxRevokeReasonChars)
	}
	mgr, err := s.manager()
	if err != nil {
		return err
	}
	target := slug
	if userID != "" {
		target = userID + "/" + slug
	}
	ev := Event{Action: "connection.revoke", Target: target, Details: map[string]any{"reason": reason, "shared": userID == ""}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		err = mgr.RevokeConnection(ctx, tx, srv.ID, userID, reason)
		if errors.Is(err, upstreamauth.ErrServerNotFound) {
			return notFound("connection not found", "conexão não encontrada")
		}
		return err
	})
}

// SetSharedHeaderToken stores the server's shared header value (header servers only). actorID
// "" (CLI) leaves connected_by NULL. The token never reaches the audit details, only its last4.
func (s *Service) SetSharedHeaderToken(ctx context.Context, slug, actorID, token string) (last4 string, err error) {
	return s.SetSharedConnection(ctx, slug, actorID, token, nil, "")
}

// SetSharedConnection is SetSharedHeaderToken plus the shared connection's per-connection
// environment values (nil keeps them; token "" keeps the token). Only the names reach the audit.
// A rejected value is returned as *upstreamauth.EnvError. connURL is the connection's own upstream
// address on a server with connection_url ("" keeps it); a rejected one is *InvalidError on url.
func (s *Service) SetSharedConnection(ctx context.Context, slug, actorID, token string, env map[string]string, connURL string) (last4 string, err error) {
	mgr, err := s.manager()
	if err != nil {
		return "", err
	}
	details := map[string]any{}
	if env != nil {
		details["env_set"] = slices.Sorted(maps.Keys(env))
	}
	err = s.write(ctx, Event{Action: "connection.set_shared", Target: slug, Details: details}, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		if srv.AuthKind != "header" {
			return invalid("auth_kind", "server %s uses auth %s, not header", "o servidor %s usa auth %s, não header", srv.Slug, srv.AuthKind)
		}
		in := upstreamauth.ConnectionInput{ServerID: srv.ID, ActorID: actorID, Shared: true, Token: token, Env: env, URL: connURL}
		if last4, err = mgr.SetConnection(ctx, tx, in); err != nil {
			return mapTokenError(err, slug)
		}
		details["last4"] = last4
		return nil
	})
	if err != nil {
		return "", err
	}
	return last4, nil
}

// mapTokenError turns upstreamauth token errors into field errors; none echoes the token.
func mapTokenError(err error, slug string) error {
	var urlErr *upstreamauth.URLError
	switch {
	case errors.As(err, &urlErr):
		return NewInvalid("url", urlErr.Message)
	case errors.Is(err, upstreamauth.ErrTokenFormat):
		return invalid("token", "the value does not have the format this server expects", "o valor não tem o formato esperado por este servidor")
	case errors.Is(err, upstreamauth.ErrInvalidToken):
		return invalid("token", "invalid value: use 8 to 4096 characters, without line breaks", "valor inválido: use de 8 a 4096 caracteres, sem quebras de linha")
	case errors.Is(err, upstreamauth.ErrServerNotFound):
		return notFound("server not found or inactive: %s", "servidor não encontrado ou inativo: %s", slug)
	}
	return err
}

func (s *Service) RemoveSharedConnection(ctx context.Context, slug string) error {
	mgr, err := s.manager()
	if err != nil {
		return err
	}
	return s.write(ctx, Event{Action: "connection.remove_shared", Target: slug}, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM upstream_credentials WHERE server_id = $1::uuid AND shared)`, srv.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return notFound("server %s has no shared connection", "o servidor %s não tem conexão compartilhada", slug)
		}
		return mgr.Disconnect(ctx, tx, srv.ID, "", true)
	})
}

// checkPrefixNotInSharedValue guards a header_prefix change (G23): setting a non-empty prefix the
// shared value already carries would send it twice. Nothing to check when the prefix is unchanged.
func (s *Service) checkPrefixNotInSharedValue(ctx context.Context, tx pgx.Tx, serverID, prefix string) error {
	if prefix == "" {
		return nil
	}
	var current string
	if err := tx.QueryRow(ctx, `SELECT header_prefix FROM upstream_servers WHERE id = $1::uuid`, serverID).Scan(&current); err != nil {
		return err
	}
	if current == prefix {
		return nil
	}
	mgr, err := s.manager()
	if err != nil {
		return err
	}
	has, err := mgr.SharedValueHasPrefix(ctx, tx, serverID, prefix)
	if err != nil {
		return err
	}
	if has {
		return ErrPrefixInValue
	}
	return nil
}

// checkNoTokenConnections refuses to turn off an oauth server's alternative header (newName "")
// while an active connection still authenticates with a pasted token.
func checkNoTokenConnections(ctx context.Context, tx pgx.Tx, srv serverRow, newName string) error {
	if newName != "" {
		return nil
	}
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM upstream_credentials
		WHERE server_id = $1::uuid AND method = 'token' AND status = 'active'`, srv.ID).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return conflict("server %s has %d active connection(s) with a pasted token: disconnect them before turning off the alternative token", "o servidor %s tem %d conexão(ões) ativa(s) com token colado: desconecte-as antes de desligar o token alternativo", srv.Slug, n)
	}
	return nil
}
