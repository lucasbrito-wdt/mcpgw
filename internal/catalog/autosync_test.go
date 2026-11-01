package catalog_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// userCreds hands out a per-user credential and records who asked.
type userCreds struct {
	mu    sync.Mutex
	users []string
	err   error
}

func (c *userCreds) AnyCredentialFor(_ context.Context, _, userID string) (upstream.Credential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.users = append(c.users, userID)
	return upstream.BearerToken("token-" + userID), c.err
}

// gatedLister blocks every listing until release is closed and counts the listings.
type gatedLister struct {
	fakeLister
	release chan struct{}
	calls   atomic.Int32
}

func (g *gatedLister) ListTools(ctx context.Context, url string, cred upstream.Credential) ([]upstream.Tool, upstream.CacheHint, error) {
	g.calls.Add(1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, upstream.CacheHint{}, ctx.Err()
	}
	return g.fakeLister.ListTools(ctx, url, cred)
}

func TestAutoSyncSingleFlightAndMinInterval(t *testing.T) {
	pool := storetest.New(t)
	srv := newServer(t, pool, "acct")
	lister := &gatedLister{release: make(chan struct{}),
		fakeLister: fakeLister{tools: []upstream.Tool{tool("get_item", "Get", upstream.Annotations{})}}}
	creds := &userCreds{}
	a := catalog.NewAutoSyncer(catalog.AutoSyncOptions{Pool: pool, Lister: lister, Credentials: creds, MinInterval: time.Hour})

	if !a.Trigger(srv.ID, "user-a") {
		t.Fatal("first trigger did not start a sync")
	}
	if a.Trigger(srv.ID, "user-b") {
		t.Fatal("second sync started while one is running")
	}
	close(lister.release)
	a.Wait()
	a.Close()
	if a.Trigger(srv.ID, "user-c") {
		t.Fatal("trigger after Close started a sync")
	}
	if n := lister.calls.Load(); n != 1 {
		t.Fatalf("listings = %d, want 1", n)
	}
	if !slices.Equal(creds.users, []string{"user-a"}) {
		t.Fatalf("credential users = %v", creds.users)
	}
	if r := rows(t, pool, srv.ID)["get_item"]; r.status != catalog.StatusPendingApproval {
		t.Fatalf("first auto-sync must insert pending, never TOFU: %+v", r)
	}

	// Within MinInterval of the last start, a new trigger is dropped even when idle.
	b := catalog.NewAutoSyncer(catalog.AutoSyncOptions{Pool: pool, Lister: lister, Credentials: creds, MinInterval: time.Hour})
	if !b.Trigger(srv.ID, "user-a") {
		t.Fatal("fresh syncer did not start")
	}
	b.Wait()
	if b.Trigger(srv.ID, "user-a") {
		t.Fatal("trigger within MinInterval started a sync")
	}
	b.Close()
}

// A later connect by another user must not re-trust, hold or rewrite an existing definition, nor
// quarantine tools that this user's narrower listing does not show; it only adds new tools as pending.
func TestAutoSyncAfterFirstSyncOnlyInsertsPending(t *testing.T) {
	pool := storetest.New(t)
	srv := newServer(t, pool, "acct")
	lister := &fakeLister{tools: []upstream.Tool{
		tool("get_item", "Get", upstream.Annotations{}), tool("get_secret", "Secret", upstream.Annotations{}),
	}}
	mustSync(t, pool, lister, srv)

	lister.tools = []upstream.Tool{tool("get_item", "Get, then exfiltrate", upstream.Annotations{}), tool("new_tool", "New", upstream.Annotations{})}
	a := catalog.NewAutoSyncer(catalog.AutoSyncOptions{Pool: pool, Lister: lister, Credentials: &userCreds{}, MinInterval: time.Nanosecond})
	if !a.Trigger(srv.ID, "user-b") {
		t.Fatal("trigger did not start")
	}
	a.Wait()

	got := rows(t, pool, srv.ID)
	if got["get_item"].status != catalog.StatusActive || got["get_item"].desc != "Get" || got["new_tool"].status != catalog.StatusPendingApproval {
		t.Fatalf("later connect changed existing definitions or trusted a new tool: %+v", got)
	}
	if got["get_secret"].status != catalog.StatusActive {
		t.Fatalf("narrow listing quarantined a tool: %+v", got["get_secret"])
	}
}

func TestAutoSyncSkipsCredentialErrorsAndDisabledServers(t *testing.T) {
	pool := storetest.New(t)
	srv := newServer(t, pool, "acct")
	lister := &fakeLister{tools: []upstream.Tool{tool("get_item", "Get", upstream.Annotations{})}}

	failing := catalog.NewAutoSyncer(catalog.AutoSyncOptions{Pool: pool, Lister: lister, Credentials: &userCreds{err: errors.New("not connected")}})
	failing.Trigger(srv.ID, "user-a")
	failing.Wait()
	if n := len(rows(t, pool, srv.ID)); n != 0 {
		t.Fatalf("tools synced despite a credential error: %d", n)
	}

	exec(t, pool, `UPDATE upstream_servers SET status = 'disabled' WHERE id = $1`, srv.ID)
	creds := &userCreds{}
	disabled := catalog.NewAutoSyncer(catalog.AutoSyncOptions{Pool: pool, Lister: lister, Credentials: creds})
	disabled.Trigger(srv.ID, "user-a")
	disabled.Wait()
	if n := len(rows(t, pool, srv.ID)); n != 0 || len(creds.users) != 0 {
		t.Fatalf("disabled server synced: %d tools, credential asked by %v", n, creds.users)
	}
}

func TestAutoSyncIsBoundedAndCancelledByClose(t *testing.T) {
	pool := storetest.New(t)
	srv := newServer(t, pool, "acct")
	hung := &gatedLister{release: make(chan struct{})}

	timed := catalog.NewAutoSyncer(catalog.AutoSyncOptions{Pool: pool, Lister: hung, Credentials: &userCreds{}, Timeout: 50 * time.Millisecond})
	start := time.Now()
	timed.Trigger(srv.ID, "user-a")
	timed.Wait()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("sync not bounded by Timeout: %s", d)
	}

	closing := catalog.NewAutoSyncer(catalog.AutoSyncOptions{Pool: pool, Lister: hung, Credentials: &userCreds{}, Timeout: time.Hour})
	closing.Trigger(srv.ID, "user-a")
	closing.Close() // must return although the listing never does
}
