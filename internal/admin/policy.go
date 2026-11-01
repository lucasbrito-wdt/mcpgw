package admin

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

type Ceiling struct{ UserID, Email, ServerSlug, Level string }

type CeilingFilter struct{ Email, UserID string }

func (s *Service) ListCeilings(ctx context.Context, f CeilingFilter) ([]Ceiling, error) {
	email := ""
	if f.Email != "" {
		var err error
		if email, err = NormalizeEmail(f.Email); err != nil {
			return nil, err
		}
	}
	if f.UserID != "" && !validUUID(f.UserID) {
		return nil, nil
	}
	rows, err := s.o.Pool.Query(ctx, `SELECT c.user_id::text, u.email, s.slug, c.max_level
		FROM trust_ceilings c JOIN users u ON u.id = c.user_id JOIN upstream_servers s ON s.id = c.server_id
		WHERE ($1 = '' OR u.email = $1) AND ($2 = '' OR c.user_id = NULLIF($2, '')::uuid)
		ORDER BY u.email, s.slug`, email, f.UserID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (c Ceiling, err error) {
		err = r.Scan(&c.UserID, &c.Email, &c.ServerSlug, &c.Level)
		return
	})
}

// SetCeiling sets the user's maximum level on a server; LevelNone deletes the ceiling.
func (s *Service) SetCeiling(ctx context.Context, userID, slug string, level policy.Level) error {
	if !validUUID(userID) {
		return notFound("user not found: %s", "usuário não encontrado: %s", userID)
	}
	ev := Event{Action: "ceiling.set", Target: userID + "/" + slug, Details: map[string]any{"level": level.String()}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid)`, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return notFound("user not found: %s", "usuário não encontrado: %s", userID)
		}
		if level == policy.LevelNone {
			_, err = tx.Exec(ctx, `DELETE FROM trust_ceilings WHERE user_id = $1::uuid AND server_id = $2::uuid`, userID, srv.ID)
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid, $2::uuid, $3)
			ON CONFLICT (user_id, server_id) DO UPDATE SET max_level = EXCLUDED.max_level`, userID, srv.ID, level.String())
		return err
	})
}
