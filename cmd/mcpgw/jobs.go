package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

const (
	partitionsEvery  = 24 * time.Hour
	purgeEvery       = time.Hour
	catalogSyncEvery = 15 * time.Minute
	catalogSyncLimit = 30 * time.Second
)

// startJobs runs the periodic maintenance until ctx ends; wait blocks until all of them stopped.
func (gw *gateway) startJobs(ctx context.Context) (wait func()) {
	var wg sync.WaitGroup
	every := func(name string, interval time.Duration, runAtStart bool, fn func(context.Context) error) {
		wg.Go(func() { runEvery(ctx, gw.log, name, interval, runAtStart, fn) })
	}
	every("audit partitions", partitionsEvery, false, func(ctx context.Context) error {
		return gw.audit.EnsurePartitions(ctx, time.Now())
	})
	every("purge upstream oauth states", purgeEvery, false, func(ctx context.Context) error {
		_, err := gw.upstreamAuth.PurgeExpiredStates(ctx)
		return err
	})
	every("purge oauth codes and refresh tokens", purgeEvery, false, func(ctx context.Context) error {
		_, err := gw.oauth.PurgeExpired(ctx)
		return err
	})
	every("catalog sync", catalogSyncEvery, true, func(ctx context.Context) error {
		syncStaticServers(ctx, gw.log, gw.pool, gw.upstream, gw.upstreamAuth)
		return nil
	})
	return wg.Wait
}

// runEvery calls fn every interval (and once at the start when asked) until ctx ends.
// Failures are logged and never stop the loop.
func runEvery(ctx context.Context, log *slog.Logger, name string, interval time.Duration, runAtStart bool, fn func(context.Context) error) {
	run := func() {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			log.Error("background job failed", "job", name, "err", err)
		}
	}
	if runAtStart {
		run()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// syncStaticServers syncs every active server that needs no per-user credential. OAuth servers
// are synced by `mcpgw admin catalog sync --as <email>`. Failures are logged; the rest go on.
// A server never synced is skipped: its first sync trusts every listed definition (TOFU), so
// only the admin's `catalog sync` may set that baseline, never an unattended background job.
// catalog_synced_at is never cleared, so the filter cannot race with a later reset.
func syncStaticServers(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, lister catalog.Lister, creds admin.CredentialSource) {
	rows, err := pool.Query(ctx, `SELECT id::text, slug, url, auth_kind, catalog_synced_at IS NULL FROM upstream_servers
		WHERE status = 'active' AND auth_kind IN ('none', 'header', 'static_header') ORDER BY slug`)
	if err != nil {
		log.Error("catalog sync: list servers", "err", err)
		return
	}
	var servers []admin.ServerRef
	for rows.Next() {
		var s admin.ServerRef
		var neverSynced bool
		if err := rows.Scan(&s.ID, &s.Slug, &s.URL, &s.AuthKind, &neverSynced); err != nil {
			rows.Close()
			log.Error("catalog sync: scan server", "err", err)
			return
		}
		if neverSynced {
			log.Warn("catalog sync skipped: no trusted baseline yet, run mcpgw admin catalog sync", "server", s.Slug)
			continue
		}
		servers = append(servers, s)
	}
	rows.Close()
	for _, s := range servers {
		if ctx.Err() != nil {
			return
		}
		report, err := admin.SyncServer(ctx, pool, lister, creds, s, "", catalogSyncLimit)
		if errors.Is(err, upstreamauth.ErrNotConnected) {
			// Only per-user (or named) connections exist: like OAuth, it syncs via `catalog sync --as`.
			log.Debug("catalog sync skipped: no shared connection", "server", s.Slug)
			continue
		}
		if err != nil {
			log.Warn("catalog sync failed", "server", s.Slug, "err", err)
			continue
		}
		log.Info("catalog synced", "server", s.Slug, "added", len(report.Added),
			"changed", len(report.Changed), "quarantined", len(report.Quarantined), "unchanged", report.Unchanged)
	}
}
