package catalog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const (
	defaultAutoSyncTimeout     = 30 * time.Second
	defaultAutoSyncMinInterval = time.Minute
)

// CredentialSource is the part of upstreamauth.Manager that the auto-sync needs.
type CredentialSource interface {
	AnyCredentialFor(ctx context.Context, serverID, userID string) (upstream.Credential, error)
}

// AutoSyncOptions configures an AutoSyncer.
type AutoSyncOptions struct {
	Pool        *pgxpool.Pool
	Lister      Lister
	Credentials CredentialSource
	Logger      *slog.Logger
	Timeout     time.Duration // per sync; default 30s
	MinInterval time.Duration // between two syncs of one server; default 1m
}

// AutoSyncer syncs a server's catalog in the background after a user connects an account to it,
// with that user's credential. The catalog is shared per server, and one user's listing must not
// shape what everyone else sees, so the sync is insert-only (Server.InsertOnly): it never performs
// the trust-on-first-use sync (that is the admin's `catalog sync`), adds every tool it does not
// know as pending_approval for an admin to approve, and never changes, reclassifies or
// quarantines an existing tool; definitions that differ are only logged. The caller must have
// checked that the user has a trust ceiling on the server. At most one sync per server runs at a
// time, and a server is not synced again within MinInterval of the previous start: triggers in
// between are dropped.
type AutoSyncer struct {
	opts   AutoSyncOptions
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	running map[string]bool
	started map[string]time.Time
}

// NewAutoSyncer builds an AutoSyncer; Close stops it.
func NewAutoSyncer(o AutoSyncOptions) *AutoSyncer {
	if o.Timeout <= 0 {
		o.Timeout = defaultAutoSyncTimeout
	}
	if o.MinInterval <= 0 {
		o.MinInterval = defaultAutoSyncMinInterval
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &AutoSyncer{
		opts: o, now: time.Now, ctx: ctx, cancel: cancel,
		running: map[string]bool{}, started: map[string]time.Time{},
	}
}

// Trigger starts a background sync of serverID with userID's credential and returns at once.
// It reports whether a sync was started.
func (a *AutoSyncer) Trigger(serverID, userID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.closed || a.running[serverID] || now.Sub(a.started[serverID]) < a.opts.MinInterval {
		return false
	}
	a.running[serverID], a.started[serverID] = true, now
	a.wg.Go(func() {
		defer a.finish(serverID)
		a.run(serverID, userID)
	})
	return true
}

func (a *AutoSyncer) finish(serverID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.running, serverID)
}

func (a *AutoSyncer) run(serverID, userID string) {
	ctx, cancel := context.WithTimeout(a.ctx, a.opts.Timeout)
	defer cancel()
	log := a.opts.Logger.With("server_id", serverID, "user_id", userID)
	report, err := a.sync(ctx, serverID, userID)
	if err != nil {
		if a.ctx.Err() == nil {
			log.Warn("catalog auto-sync failed", "err", err)
		}
		return
	}
	log.Info("catalog auto-synced", "pending", len(report.Pending), "unchanged", report.Unchanged,
		"rejected", len(report.Rejected))
	if len(report.Ignored) > 0 {
		log.Warn("catalog auto-sync ignored changed definitions (run admin catalog sync to review)", "tools", report.Ignored)
	}
}

func (a *AutoSyncer) sync(ctx context.Context, serverID, userID string) (SyncReport, error) {
	srv := Server{ID: serverID, KeepMissing: true, InsertOnly: true}
	err := a.opts.Pool.QueryRow(ctx, `SELECT slug, url FROM upstream_servers WHERE id = $1 AND status = 'active'`,
		serverID).Scan(&srv.Slug, &srv.URL)
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncReport{}, errors.New("server not found or disabled")
	}
	if err != nil {
		return SyncReport{}, err
	}
	if srv.Credential, err = a.opts.Credentials.AnyCredentialFor(ctx, serverID, userID); err != nil {
		return SyncReport{}, err
	}
	return Sync(ctx, a.opts.Pool, a.opts.Lister, srv)
}

// Close cancels running syncs and waits for them. Later triggers are ignored.
func (a *AutoSyncer) Close() {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.cancel()
	a.wg.Wait()
}
