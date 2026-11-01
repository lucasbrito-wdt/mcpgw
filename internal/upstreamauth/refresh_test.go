package upstreamauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestRefreshOnExpiryWithRotation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")

	first, _ := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	if e.fake.refreshCalls.Load() != 0 {
		t.Fatal("refreshed a fresh token")
	}
	e.clock.Advance(3600*time.Second - 30*time.Second) // inside the 60s window
	second, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	if err != nil {
		t.Fatal(err)
	}
	if e.fake.refreshCalls.Load() != 1 || bearerValue(t, first) == bearerValue(t, second) {
		t.Fatalf("refreshes = %d, tokens %v -> %v", e.fake.refreshCalls.Load(), first, second)
	}
	// Cached now: no second refresh while the new token is fresh.
	if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); err != nil || e.fake.refreshCalls.Load() != 1 {
		t.Fatalf("unexpected refresh: %v (%d)", err, e.fake.refreshCalls.Load())
	}
	// The rotated refresh token was persisted: the next cycle works too (reuse would be invalid_grant).
	e.clock.Advance(3600*time.Second - 30*time.Second)
	if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); err != nil || e.fake.refreshCalls.Load() != 2 {
		t.Fatalf("second rotation: %v (%d)", err, e.fake.refreshCalls.Load())
	}
}

func TestRefreshKeepsOldRefreshTokenWhenNotRotated(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")
	e.fake.rotate = false
	for range 2 {
		e.clock.Advance(3600*time.Second - 30*time.Second)
		if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); err != nil {
			t.Fatal(err)
		}
	}
	if e.fake.refreshCalls.Load() != 2 {
		t.Fatalf("refreshes = %d", e.fake.refreshCalls.Load())
	}
}

func TestRefreshInvalidGrantDisconnects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")
	e.fake.mu.Lock()
	e.fake.refresh = map[string]bool{} // grant revoked at the AS
	e.fake.mu.Unlock()
	e.clock.Advance(time.Hour)
	_, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	if !errors.Is(err, ErrNotConnected) || e.credentialCount() != 1 {
		t.Fatalf("err = %v, rows = %d", err, e.credentialCount())
	}
}

func TestRefreshTransientFailureKeepsCredentialAndScrubs(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")
	e.fake.refreshStatus = 503
	e.clock.Advance(time.Hour)
	_, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	if err == nil || errors.Is(err, ErrNotConnected) || e.credentialCount() != 1 {
		t.Fatalf("err = %v, rows = %d", err, e.credentialCount())
	}
	if strings.Contains(err.Error(), "rt-") || strings.Contains(err.Error(), "at-") {
		t.Fatalf("error leaks token material: %v", err)
	}
}

func TestExpiredWithoutRefreshTokenIsNotConnected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_credentials SET refresh_ciphertext = NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); err != nil {
		t.Fatalf("still valid: %v", err)
	}
	e.clock.Advance(2 * time.Hour)
	if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestRefreshSingleFlight(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")
	e.fake.refreshDelay = 200 * time.Millisecond
	e.clock.Advance(time.Hour)

	const n = 24
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	tokens := make([]string, n)
	errs := make([]error, n)
	ready.Add(n)
	done.Add(n)
	for i := range n {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			cred, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
			errs[i] = err
			if err == nil {
				tokens[i] = string(cred.(upstream.BearerToken))
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	for i := range n {
		if errs[i] != nil || tokens[i] != tokens[0] || tokens[i] == "" {
			t.Fatalf("caller %d: %q, %v (first %q)", i, tokens[i], errs[i], tokens[0])
		}
	}
	if got := e.fake.refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestCallerCancelDoesNotFailOtherWaiters(t *testing.T) {
	e := newEnv(t)
	e.connect(e.userA, "alpha")
	e.fake.refreshDelay = 300 * time.Millisecond
	e.clock.Advance(time.Hour)

	cancelCtx, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := e.mgr.CredentialFor(cancelCtx, e.serverID, e.userA)
		firstErr <- err
	}()
	time.Sleep(50 * time.Millisecond)
	secondErr := make(chan error, 1)
	go func() {
		_, err := e.mgr.CredentialFor(context.Background(), e.serverID, e.userA)
		secondErr <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("first: %v", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second waiter failed because the first caller canceled: %v", err)
	}
	if e.fake.refreshCalls.Load() != 1 {
		t.Fatalf("refreshes = %d", e.fake.refreshCalls.Load())
	}
}

func TestRefreshFailureMarksStatus(t *testing.T) {
	e := newEnv(t)
	e.connect(e.userA, "alpha")
	e.fake.mu.Lock()
	e.fake.refresh = map[string]bool{}
	e.fake.mu.Unlock()
	e.clock.Advance(time.Hour)
	if _, err := e.mgr.CredentialFor(context.Background(), e.serverID, e.userA); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	var status string
	var access, refresh []byte
	_ = e.pool.QueryRow(context.Background(), `SELECT status, access_ciphertext, refresh_ciphertext FROM upstream_credentials`).Scan(&status, &access, &refresh)
	if status != "refresh_failed" || access != nil || refresh != nil {
		t.Fatalf("status=%s access=%v refresh=%v", status, access != nil, refresh != nil)
	}
}

func ticketOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("shared")
}

func (e *env) requestURI(rawURL string) string {
	e.t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.RequestURI()
}

func TestSharedOAuthRequiresAdminAndIsAudited(t *testing.T) {
	e := newEnv(t)
	c := context.Background()
	if _, err := e.pool.Exec(c, `UPDATE users SET is_admin = true WHERE id = $1::uuid`, e.userA); err != nil {
		t.Fatal(err)
	}
	e.insertServer("beta", e.fake.mcpURL(), AuthKindOAuth)
	forbidden := func(name, user, target string) {
		t.Helper()
		if rec := e.get(user, target); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
	}

	forbidden("bare shared=1", e.userA, "/connect/alpha?shared=1")
	forbidden("non-admin ticket", e.userB, e.requestURI(e.mgr.SharedConnectURL(e.userB, "alpha")))
	forbidden("ticket of another user", e.userB, e.requestURI(e.mgr.SharedConnectURL(e.userA, "alpha")))
	forbidden("ticket for another slug", e.userA, "/connect/beta?shared="+url.QueryEscape(
		ticketOf(t, e.mgr.SharedConnectURL(e.userA, "alpha"))))
	expired := e.requestURI(e.mgr.SharedConnectURL(e.userA, "alpha"))
	e.clock.Advance(6 * time.Minute)
	forbidden("expired ticket", e.userA, expired)

	demoted := e.requestURI(e.mgr.SharedConnectURL(e.userA, "alpha"))
	if _, err := e.pool.Exec(c, `UPDATE users SET is_admin = false WHERE id = $1::uuid`, e.userA); err != nil {
		t.Fatal(err)
	}
	forbidden("demoted admin", e.userA, demoted)
	if _, err := e.pool.Exec(c, `UPDATE users SET is_admin = true WHERE id = $1::uuid`, e.userA); err != nil {
		t.Fatal(err)
	}
	if e.credentialCount() != 0 {
		t.Fatal("a refused request stored a credential")
	}

	rec := e.get(e.userA, e.requestURI(e.mgr.SharedConnectURL(e.userA, "alpha")))
	if rec.Code != http.StatusFound {
		t.Fatalf("fresh admin ticket: status %d", rec.Code)
	}
	if cb := e.callback(e.userA, e.fake.authorize(rec.Header().Get("Location"))); cb.Code != http.StatusSeeOther {
		t.Fatalf("callback: status %d %s", cb.Code, cb.Body.String())
	}
	var rows int
	_ = e.pool.QueryRow(c, `SELECT count(*) FROM upstream_credentials WHERE shared AND user_id IS NULL AND connected_by = $1::uuid`, e.userA).Scan(&rows)
	var events int
	var actor, target, details string
	_ = e.pool.QueryRow(c, `SELECT count(*), coalesce(max(actor_user_id::text), ''), coalesce(max(target), ''), coalesce(max(details::text), '')
		FROM admin_events WHERE action = 'connection.shared_oauth'`).Scan(&events, &actor, &target, &details)
	if rows != 1 || e.credentialCount() != 1 || events != 1 || actor != e.userA || target != "alpha" ||
		strings.Contains(details, "at-") || strings.Contains(details, "rt-") {
		t.Fatalf("rows=%d events=%d actor=%s target=%s details=%s", rows, events, actor, target, details)
	}
	// the shared row serves every user without a personal connection
	if _, err := e.mgr.CredentialFor(c, e.serverID, e.userB); err != nil {
		t.Fatalf("userB: %v", err)
	}

	e.connect(e.userB, "alpha")
	var total int
	_ = e.pool.QueryRow(c, `SELECT count(*) FROM admin_events`).Scan(&total)
	if total != 1 {
		t.Fatalf("a personal flow wrote an admin event (%d)", total)
	}
}
