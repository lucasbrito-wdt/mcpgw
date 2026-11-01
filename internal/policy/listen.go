package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	channel    = "policy_changed"
	minBackoff = 100 * time.Millisecond
	maxBackoff = 30 * time.Second
	// DefaultHeartbeat bounds how long a half-open LISTEN connection can go unnoticed: after this
	// much silence the connection is pinged, and a failed ping flips the engine to DB reads.
	DefaultHeartbeat = 5 * time.Second
	maxPingTimeout   = 3 * time.Second

	keepAliveIdle     = 5 * time.Second
	keepAliveInterval = 5 * time.Second
	keepAliveCount    = 3
)

var errStaleLoad = errors.New("policy: cache invalidated during load")

type notification struct {
	Table  string  `json:"table"`
	Op     string  `json:"op"`
	UserID *string `json:"user_id"`
	// ServerID is the row id for upstream_servers notifications.
	ServerID *string `json:"server_id"`
}

// Listen keeps the policy_changed subscription alive until ctx is done, reconnecting with
// exponential backoff. It returns ctx.Err() on shutdown.
func (e *Engine) Listen(ctx context.Context) error {
	backoff := minBackoff
	for {
		connected, _ := e.listenOnce(ctx)
		e.healthy.Store(false)
		e.flushAll()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if connected {
			backoff = minBackoff
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// listenOnce holds one dedicated connection until it fails. connected reports whether LISTEN succeeded.
func (e *Engine) listenOnce(ctx context.Context) (connected bool, err error) {
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	// Hijack so a broken connection is closed instead of going back into the pool.
	raw := conn.Hijack()
	pingTimeout := min(e.heartbeat, maxPingTimeout)
	defer func() {
		// Any exit means notifications are no longer guaranteed: read the DB at once.
		e.healthy.Store(false)
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pingTimeout)
		defer cancel()
		_ = raw.Close(closeCtx)
	}()
	enableKeepAlive(raw)

	if _, err := raw.Exec(ctx, "LISTEN "+channel); err != nil {
		return false, err
	}
	// Flush after LISTEN is active: anything changed before it may have been missed.
	e.flushAll()
	e.healthy.Store(true)

	for {
		waitCtx, cancel := context.WithTimeout(ctx, e.heartbeat)
		n, err := raw.WaitForNotification(waitCtx)
		cancel()
		switch {
		case err == nil:
			e.handle(n.Payload)
		case ctx.Err() != nil:
			return true, ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := raw.Ping(pingCtx)
			cancel()
			if err != nil {
				e.healthy.Store(false)
				return true, err
			}
		default:
			return true, err
		}
	}
}

// enableKeepAlive makes the kernel detect a dead peer on the LISTEN socket (best effort: a
// non-TCP or wrapped connection is left alone, the heartbeat still covers it).
func enableKeepAlive(c *pgx.Conn) {
	nc := c.PgConn().Conn()
	for {
		switch v := nc.(type) {
		case *net.TCPConn:
			_ = v.SetKeepAliveConfig(net.KeepAliveConfig{
				Enable: true, Idle: keepAliveIdle, Interval: keepAliveInterval, Count: keepAliveCount,
			})
			return
		case interface{ NetConn() net.Conn }:
			nc = v.NetConn()
		default:
			return
		}
	}
}

// handle invalidates the cache entries named by a notification payload; anything it cannot
// understand flushes everything.
func (e *Engine) handle(payload string) {
	var n notification
	if err := json.Unmarshal([]byte(payload), &n); err != nil {
		e.flushAll()
		return
	}
	switch {
	case n.Table == "upstream_servers":
		e.invalidateTools()
		serverID := ""
		if n.ServerID != nil {
			serverID = *n.ServerID
		}
		e.notifyServerChanged(serverID)
	case n.Table == "tools":
		e.invalidateTools()
	case (n.Table == "consents" || n.Table == "trust_ceilings") && n.UserID != nil:
		e.invalidateUser(normalizeID(*n.UserID))
	default:
		e.flushAll()
	}
}
