package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

type noCredentials struct{}

func (noCredentials) AnyCredentialFor(context.Context, string, string) (upstream.Credential, error) {
	return nil, nil
}

// The periodic sync must never set the TOFU baseline of a static/none server: whatever the
// upstream lists before the admin's first `catalog sync` would otherwise be trusted unseen.
func TestPeriodicSyncNeverSetsTOFUBaseline(t *testing.T) {
	h := newAdminHarness(t, nil)
	lister := &staticLister{tools: []upstream.Tool{{Name: "get_item", Description: "Get"}}}
	h.env.lister = lister
	h.mustRun("server add --slug fresh --url http://127.0.0.1:9999/mcp --auth none")
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)

	syncStaticServers(ctx, log, h.env.pool, lister, noCredentials{})
	var tools int
	var neverSynced bool
	if err := h.env.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM tools), catalog_synced_at IS NULL
		FROM upstream_servers WHERE slug = 'fresh'`).Scan(&tools, &neverSynced); err != nil {
		t.Fatal(err)
	}
	if tools != 0 || !neverSynced {
		t.Fatalf("background sync touched a server without baseline: tools=%d neverSynced=%v", tools, neverSynced)
	}

	h.mustRun("catalog sync --slug fresh")
	lister.tools = append(lister.tools, upstream.Tool{Name: "delete_item", Description: "Delete"})
	syncStaticServers(ctx, log, h.env.pool, lister, noCredentials{})
	if out := h.mustRun("tool list --slug fresh"); !strings.Contains(out, "delete_item") ||
		hasToolState(out, "delete_item", "", "active", "-") {
		t.Fatalf("after the baseline the periodic sync adds new tools, never trusted:\n%s", out)
	}
}

type notConnected struct{}

func (notConnected) AnyCredentialFor(context.Context, string, string) (upstream.Credential, error) {
	return nil, &upstreamauth.NotConnectedError{}
}

// A server whose only connections are per-user profiles has no shared credential: the periodic
// sync must skip it quietly instead of warning on every run.
func TestPeriodicSyncSkipsServerWithoutSharedConnection(t *testing.T) {
	h := newAdminHarness(t, nil)
	lister := &staticLister{tools: []upstream.Tool{{Name: "get_item", Description: "Get"}}}
	h.env.lister = lister
	h.mustRun("server add --slug profiled --url http://127.0.0.1:9999/mcp --auth none")
	h.mustRun("catalog sync --slug profiled")

	var out bytes.Buffer
	syncStaticServers(context.Background(), slog.New(slog.NewTextHandler(&out, nil)), h.env.pool, lister, notConnected{})
	if strings.Contains(out.String(), "catalog sync failed") {
		t.Fatalf("periodic sync warned for a server without shared connection:\n%s", out.String())
	}
}
