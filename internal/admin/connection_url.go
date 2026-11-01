package admin

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// SetConnectionURL switches whether each connection of a header server over HTTP names its own
// upstream URL. Turning it off keeps the stored URLs but calls go back to the server's URL.
func (s *Service) SetConnectionURL(ctx context.Context, slug string, enabled bool) error {
	ev := Event{Action: "server.set_connection_url", Target: slug, Details: map[string]any{"enabled": enabled}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		var id, transport, authKind string
		err := tx.QueryRow(ctx, `SELECT id::text, transport, auth_kind FROM upstream_servers WHERE slug = $1 FOR UPDATE`, slug).
			Scan(&id, &transport, &authKind)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound("server not found: %s", "servidor não encontrado: %s", slug)
		}
		if err != nil {
			return err
		}
		if enabled && (transport == string(upstream.TransportStdio) || authKind != "header") {
			return invalid("connection_url", "per-connection address only applies to HTTP servers with header auth", "endereço por conexão só vale para servidores HTTP com auth header")
		}
		_, err = tx.Exec(ctx, `UPDATE upstream_servers SET connection_url = $2 WHERE id = $1::uuid`, id, enabled)
		return err
	})
}
