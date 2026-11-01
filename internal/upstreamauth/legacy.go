package upstreamauth

import (
	"context"
	"fmt"
	"log/slog"
)

type legacyHeader struct {
	id, slug string
	ct       []byte
}

// MigrateLegacyHeaders re-seals every server-level static header (pre-0007 storage) into the
// server's shared credential, sets auth_kind = 'header' and clears the legacy columns. One
// transaction per server; idempotent; safe when several gateways start at once. A value that does
// not open is logged by slug (never the value) and left untouched. Returns the number migrated.
func (m *Manager) MigrateLegacyHeaders(ctx context.Context) (int, error) {
	rows, err := m.pool.Query(ctx, `SELECT id::text, slug, static_header_ciphertext FROM upstream_servers
		WHERE static_header_ciphertext IS NOT NULL ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("list legacy headers: %w", err)
	}
	var todo []legacyHeader
	for rows.Next() {
		var h legacyHeader
		if err := rows.Scan(&h.id, &h.slug, &h.ct); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan legacy header: %w", err)
		}
		todo = append(todo, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("list legacy headers: %w", err)
	}
	migrated := 0
	for _, h := range todo {
		ok, err := m.reseal(ctx, h)
		if err != nil {
			return migrated, err
		}
		if ok {
			migrated++
		}
	}
	return migrated, nil
}

func (m *Manager) reseal(ctx context.Context, h legacyHeader) (bool, error) {
	value, err := m.vault.Open(h.ct, serverFieldAAD(h.id, "static_header"))
	if err != nil {
		slog.Warn("legacy header not migrated: value does not open", "server", h.slug)
		return false, nil
	}
	sealed, err := m.vault.Seal(value, sharedAAD(h.id))
	if err != nil {
		return false, err
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin legacy migration: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Claim first: a concurrent run blocks here, then sees the cleared column and skips.
	tag, err := tx.Exec(ctx, `UPDATE upstream_servers SET header_name = static_header_name, auth_kind = 'header',
		static_header_name = NULL, static_header_ciphertext = NULL
		WHERE id = $1::uuid AND static_header_ciphertext = $2`, h.id, h.ct)
	if err != nil {
		return false, fmt.Errorf("clear legacy header of %q: %w", h.slug, err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	// A shared row that already exists is newer than the legacy value: keep it.
	_, err = tx.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, shared, connected_by, access_ciphertext, key_id, last4, status)
		VALUES ($1::uuid, NULL, true, NULL, $2, $3, $4, 'active') ON CONFLICT (server_id) WHERE shared DO NOTHING`,
		h.id, sealed, m.vault.KeyID(), last4Of(string(value)))
	if err != nil {
		return false, fmt.Errorf("store shared credential of %q: %w", h.slug, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit legacy migration of %q: %w", h.slug, err)
	}
	return true, nil
}
