package admin

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Client struct {
	ClientID, Name, RegistrationKind string
	RedirectURIs                     []string
	CreatedAt                        time.Time
	ActiveConsents                   int
}

type Consent struct {
	ID, UserID, Email, ClientID, ClientName, ServerSlug, Level string
	CreatedAt                                                  time.Time
	ExpiresAt, RevokedAt                                       *time.Time
}

type ConsentFilter struct{ UserID, ClientID string }

type Grant struct {
	FamilyID, UserID, Email, ClientID, Scope string
	CreatedAt, LastUsedAt, ExpiresAt         time.Time
}

// ListClients returns the registered OAuth clients, newest first. The name is client metadata,
// untrusted text.
func (s *Service) ListClients(ctx context.Context, q PageQuery) (Page[Client], error) {
	cursor, err := q.uuidCursor()
	if err != nil {
		return Page[Client]{}, err
	}
	limit := q.limit()
	rows, err := s.o.Pool.Query(ctx, `SELECT c.id::text, c.client_id, COALESCE(c.metadata->>'client_name', ''), c.registration_kind,
			c.redirect_uris, c.created_at,
			(SELECT count(*) FROM consents n WHERE n.client_id = c.client_id AND n.revoked_at IS NULL
				AND (n.expires_at IS NULL OR n.expires_at > now()))
		FROM oauth_clients c WHERE ($1 = '' OR c.id < NULLIF($1, '')::uuid)
		ORDER BY c.id DESC LIMIT $2`, cursor, limit+1)
	if err != nil {
		return Page[Client]{}, err
	}
	type row struct {
		id string
		Client
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (v row, err error) {
		err = r.Scan(&v.id, &v.ClientID, &v.Name, &v.RegistrationKind, &v.RedirectURIs, &v.CreatedAt, &v.ActiveConsents)
		return
	})
	if err != nil {
		return Page[Client]{}, err
	}
	page := pageRows(items, limit, func(v row) string { return v.id })
	out := Page[Client]{NextCursor: page.NextCursor, Items: make([]Client, len(page.Items))}
	for i, v := range page.Items {
		out.Items[i] = v.Client
	}
	return out, nil
}

func (s *Service) ListConsents(ctx context.Context, f ConsentFilter, q PageQuery) (Page[Consent], error) {
	cursor, err := q.uuidCursor()
	if err != nil {
		return Page[Consent]{}, err
	}
	if f.UserID != "" && !validUUID(f.UserID) {
		return Page[Consent]{}, nil
	}
	limit := q.limit()
	rows, err := s.o.Pool.Query(ctx, `SELECT n.id::text, n.user_id::text, u.email, n.client_id, COALESCE(c.metadata->>'client_name', ''),
			s.slug, n.level, n.created_at, n.expires_at, n.revoked_at
		FROM consents n JOIN users u ON u.id = n.user_id JOIN oauth_clients c ON c.client_id = n.client_id
			JOIN upstream_servers s ON s.id = n.server_id
		WHERE ($1 = '' OR n.user_id = NULLIF($1, '')::uuid) AND ($2 = '' OR n.client_id = $2)
			AND ($3 = '' OR n.id < NULLIF($3, '')::uuid)
		ORDER BY n.id DESC LIMIT $4`, f.UserID, f.ClientID, cursor, limit+1)
	if err != nil {
		return Page[Consent]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (v Consent, err error) {
		err = r.Scan(&v.ID, &v.UserID, &v.Email, &v.ClientID, &v.ClientName, &v.ServerSlug, &v.Level, &v.CreatedAt, &v.ExpiresAt, &v.RevokedAt)
		return
	})
	if err != nil {
		return Page[Consent]{}, err
	}
	return pageRows(items, limit, func(v Consent) string { return v.ID }), nil
}

func (s *Service) RevokeConsent(ctx context.Context, id string) error {
	if !validUUID(id) {
		return notFound("consent not found", "consentimento não encontrado")
	}
	return s.write(ctx, Event{Action: "consent.revoke", Target: id}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE consents SET revoked_at = now() WHERE id = $1::uuid AND revoked_at IS NULL`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("consent not found", "consentimento não encontrado")
		}
		return nil
	})
}

// ListGrants returns the live authorizations: one row per refresh family whose latest token is
// still usable. Families have random ids, so the cursor is the family id (descending), not time.
func (s *Service) ListGrants(ctx context.Context, userID string, q PageQuery) (Page[Grant], error) {
	cursor, err := q.uuidCursor()
	if err != nil {
		return Page[Grant]{}, err
	}
	if userID != "" && !validUUID(userID) {
		return Page[Grant]{}, nil
	}
	limit := q.limit()
	rows, err := s.o.Pool.Query(ctx, `SELECT family_id, user_id, email, client_id, scope, created_at, last_used_at, expires_at FROM (
			SELECT DISTINCT ON (t.family_id) t.family_id::text AS family_id, t.user_id::text AS user_id, u.email, t.client_id, t.scope,
				(SELECT min(f.created_at) FROM oauth_refresh_tokens f WHERE f.family_id = t.family_id) AS created_at,
				t.created_at AS last_used_at, t.expires_at
			FROM oauth_refresh_tokens t JOIN users u ON u.id = t.user_id
			WHERE t.revoked_at IS NULL AND t.used_at IS NULL AND t.expires_at > now()
				AND NOT EXISTS (SELECT 1 FROM oauth_revoked_families r WHERE r.family_id = t.family_id)
				AND ($1 = '' OR t.user_id = NULLIF($1, '')::uuid)
			ORDER BY t.family_id, t.created_at DESC) g
		WHERE $2 = '' OR family_id < $2
		ORDER BY family_id DESC LIMIT $3`, userID, cursor, limit+1)
	if err != nil {
		return Page[Grant]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (v Grant, err error) {
		err = r.Scan(&v.FamilyID, &v.UserID, &v.Email, &v.ClientID, &v.Scope, &v.CreatedAt, &v.LastUsedAt, &v.ExpiresAt)
		return
	})
	if err != nil {
		return Page[Grant]{}, err
	}
	return pageRows(items, limit, func(v Grant) string { return v.FamilyID }), nil
}

// RevokeGrant ends a refresh family. The oauth revoke runs in its own transaction and is
// idempotent, so it goes first and the audit row follows; a failed audit leaves a revoked grant.
func (s *Service) RevokeGrant(ctx context.Context, familyID string) error {
	if s.o.Grants == nil {
		return errNoGrants
	}
	if !validUUID(familyID) {
		return notFound("authorization not found", "autorização não encontrada")
	}
	var exists bool
	if err := s.o.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM oauth_refresh_tokens WHERE family_id = $1::uuid)`, familyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return notFound("authorization not found", "autorização não encontrada")
	}
	if err := s.o.Grants.RevokeFamily(ctx, familyID); err != nil {
		return err
	}
	return s.write(ctx, Event{Action: "grant.revoke", Target: familyID}, func(pgx.Tx) error { return nil })
}
