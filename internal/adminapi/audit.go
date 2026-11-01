package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

type actorKey struct{}

// actor is the authenticated admin of a request, set by requireAdmin.
type actor struct {
	uid, email, ip string
	sess           session
}

func actorFrom(ctx context.Context) (actor, bool) {
	act, ok := ctx.Value(actorKey{}).(actor)
	return act, ok
}

// audit is admin.Options.Audit: it never writes an anonymous row.
func (a *API) audit(ctx context.Context, tx pgx.Tx, ev admin.Event) error {
	act, ok := actorFrom(ctx)
	if !ok {
		return errors.New("adminapi: audit without an authenticated admin")
	}
	return WriteAdminEvent(ctx, tx, act.uid, act.ip, ev)
}

// WriteAdminEvent inserts one admin_events row inside tx, the transaction of the write it
// describes. ev.Details must never hold secret values; nil is stored as {}.
func WriteAdminEvent(ctx context.Context, tx pgx.Tx, actorUID, ip string, ev admin.Event) error {
	details := ev.Details
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_events (actor_user_id, action, target, details, ip)
		VALUES ($1::uuid, $2, $3, $4, $5)`, actorUID, ev.Action, ev.Target, string(b), ip)
	return err
}

// ClientIP is the TCP peer address, IPv6 reduced to its /64 (same rule as oauth's rate limit
// key). X-Forwarded-For is not trusted.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}
