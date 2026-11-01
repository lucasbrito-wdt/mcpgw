package policy

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

var levels = []Level{LevelRead, LevelWrite, LevelDestructive}

type fixture struct {
	pool     *pgxpool.Pool
	engine   *Engine
	userID   string
	serverID string
	clientID string
}

func exec(tb testing.TB, pool *pgxpool.Pool, sql string, args ...any) {
	tb.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		tb.Fatalf("exec %q: %v", sql, err)
	}
}

// newFixture creates a user, client and server "srv" with one tool per level (tool_read, tool_write,
// tool_destructive). No ceiling or consent is created. When listen is true, Listen runs and the
// engine is waited on until it is healthy.
func newFixture(tb testing.TB, listen bool) *fixture {
	tb.Helper()
	pool := storetest.New(tb)
	f := &fixture{pool: pool, clientID: "client-1"}
	ctx := context.Background()
	must := func(err error) {
		tb.Helper()
		if err != nil {
			tb.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('u@example.com', 'h') RETURNING id::text`).Scan(&f.userID))
	must(pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('srv', 'http://u', 'none') RETURNING id::text`).Scan(&f.serverID))
	exec(tb, pool, `INSERT INTO oauth_clients (client_id, registration_kind) VALUES ($1, 'dcr')`, f.clientID)
	for _, l := range levels {
		exec(tb, pool, `INSERT INTO tools (server_id, name, schema_hash, trust_level) VALUES ($1::uuid, $2, 'h', $3)`,
			f.serverID, "tool_"+l.String(), l.String())
	}
	e, err := New(ctx, pool)
	must(err)
	f.engine = e
	if listen {
		lctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { _ = e.Listen(lctx); close(done) }()
		tb.Cleanup(func() { cancel(); <-done })
		waitFor(tb, "listener healthy", e.healthy.Load)
	}
	return f
}

func waitFor(tb testing.TB, what string, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			tb.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fixture) subject() Subject { return Subject{f.userID, f.clientID} }

func (f *fixture) decide(tool string) Decision {
	return f.engine.Decide(context.Background(), f.subject(), "srv", tool)
}

func (f *fixture) setCeiling(tb testing.TB, l Level) {
	exec(tb, f.pool, `INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ($1::uuid, $2::uuid, $3)
		ON CONFLICT (user_id, server_id) DO UPDATE SET max_level = EXCLUDED.max_level`, f.userID, f.serverID, l.String())
}

func (f *fixture) setConsent(tb testing.TB, l Level) {
	exec(tb, f.pool, `INSERT INTO consents (user_id, client_id, server_id, level) VALUES ($1::uuid, $2, $3::uuid, $4)
		ON CONFLICT (user_id, client_id, server_id) DO UPDATE SET level = EXCLUDED.level`, f.userID, f.clientID, f.serverID, l.String())
}

func TestEffectiveLevelMatrix(t *testing.T) {
	for _, mode := range []string{"cached", "bypass"} {
		for _, ceiling := range levels {
			for _, consent := range levels {
				name := fmt.Sprintf("%s/ceiling=%s/consent=%s", mode, ceiling, consent)
				t.Run(name, func(t *testing.T) {
					f := newFixture(t, mode == "cached")
					f.setCeiling(t, ceiling)
					f.setConsent(t, consent)
					want := min(ceiling, consent)
					if mode == "cached" {
						// Setup writes raced the listener; make sure the cache saw them.
						waitFor(t, "effective level", func() bool { return f.decide("tool_read").Effective == want })
					}
					for _, tool := range levels {
						d := f.decide("tool_" + tool.String())
						if d.Effective != want || d.Required != tool {
							t.Fatalf("%s: effective=%s required=%s, want %s/%s", tool, d.Effective, d.Required, want, tool)
						}
						if wantAllow := tool <= want; d.Allow != wantAllow {
							t.Fatalf("%s: allow=%v, want %v (%s)", tool, d.Allow, wantAllow, d.Reason)
						}
						if !d.Allow && d.Reason != ReasonLevelExceeded || d.Allow && d.Reason != ReasonAllowed {
							t.Fatalf("%s: unexpected reason %q", tool, d.Reason)
						}
					}
				})
			}
		}
	}
}

func TestDenyReasons(t *testing.T) {
	f := newFixture(t, false)
	check := func(tool, reason string) {
		t.Helper()
		if d := f.decide(tool); d.Allow != (reason == ReasonAllowed) || d.Reason != reason {
			t.Fatalf("%s: allow=%v reason=%q, want %q", tool, d.Allow, d.Reason, reason)
		}
	}
	check("tool_read", ReasonNoCeiling)

	f.setCeiling(t, LevelDestructive)
	check("tool_read", ReasonNoConsent)
	check("missing", ReasonUnknownTool)
	if d := f.engine.Decide(context.Background(), f.subject(), "nope", "tool_read"); d.Allow || d.Reason != ReasonUnknownTool {
		t.Fatalf("unknown server: %+v", d)
	}

	f.setConsent(t, LevelDestructive)
	if d := f.decide("tool_destructive"); !d.Allow {
		t.Fatalf("expected allow: %+v", d)
	}

	// Consent for another client does not count.
	other := Subject{f.userID, "other-client"}
	if d := f.engine.Decide(context.Background(), other, "srv", "tool_read"); d.Allow || d.Reason != ReasonNoConsent {
		t.Fatalf("other client: %+v", d)
	}
	// Unknown / malformed user fails closed.
	if d := f.engine.Decide(context.Background(), Subject{"not-a-uuid", f.clientID}, "srv", "tool_read"); d.Allow || d.Reason != ReasonPolicyError {
		t.Fatalf("bad user id: %+v", d)
	}

	exec(t, f.pool, `UPDATE consents SET revoked_at = now()`)
	check("tool_read", ReasonNoConsent)
	exec(t, f.pool, `UPDATE consents SET revoked_at = NULL, expires_at = now() - interval '1 minute'`)
	check("tool_read", ReasonConsentExpired)
	exec(t, f.pool, `UPDATE consents SET expires_at = now() + interval '1 hour'`)
	check("tool_read", ReasonAllowed)

	exec(t, f.pool, `UPDATE tools SET status = 'quarantined' WHERE name = 'tool_read'`)
	check("tool_read", ReasonToolQuarantined)
	exec(t, f.pool, `UPDATE upstream_servers SET status = 'disabled'`)
	check("tool_write", ReasonUnknownTool)
}

func TestConsentExpiresWithTime(t *testing.T) {
	f := newFixture(t, true)
	f.setCeiling(t, LevelWrite)
	f.setConsent(t, LevelWrite)
	exec(t, f.pool, `UPDATE consents SET expires_at = now() + interval '1 hour'`)
	waitFor(t, "allow", func() bool { return f.decide("tool_read").Allow })

	f.engine.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if d := f.decide("tool_read"); d.Allow || d.Reason != ReasonConsentExpired {
		t.Fatalf("cached consent must expire by clock: %+v", d)
	}
}

func TestAllowedTools(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	if got := f.engine.AllowedTools(ctx, f.subject()); len(got) != 0 {
		t.Fatalf("no policy must allow nothing, got %v", got)
	}
	f.setCeiling(t, LevelDestructive)
	f.setConsent(t, LevelWrite)
	exec(t, f.pool, `UPDATE tools SET status = 'quarantined' WHERE name = 'tool_read'`)
	got := f.engine.AllowedTools(ctx, f.subject())
	if len(got) != 1 || got[0] != (ToolRef{"srv", "tool_write", LevelWrite}) {
		t.Fatalf("got %v", got)
	}
}

func TestRevocationAfterNotify(t *testing.T) {
	f := newFixture(t, true)
	f.setCeiling(t, LevelWrite)
	f.setConsent(t, LevelWrite)
	waitFor(t, "allow", func() bool { return f.decide("tool_write").Allow })

	exec(t, f.pool, `UPDATE consents SET revoked_at = now()`)
	waitFor(t, "revocation to apply", func() bool { return !f.decide("tool_write").Allow })
	if d := f.decide("tool_write"); d.Reason != ReasonNoConsent {
		t.Fatalf("reason = %q", d.Reason)
	}

	exec(t, f.pool, `UPDATE tools SET status = 'quarantined' WHERE name = 'tool_read'`)
	waitFor(t, "quarantine", func() bool { return f.decide("tool_read").Reason == ReasonToolQuarantined })

	exec(t, f.pool, `UPDATE trust_ceilings SET max_level = 'read'`)
	exec(t, f.pool, `UPDATE consents SET revoked_at = NULL`)
	waitFor(t, "ceiling lowered", func() bool { return f.decide("tool_write").Reason == ReasonLevelExceeded })
}

func TestCacheServesWithoutDB(t *testing.T) {
	f := newFixture(t, true)
	f.setCeiling(t, LevelRead)
	f.setConsent(t, LevelRead)
	waitFor(t, "allow", func() bool { return f.decide("tool_read").Allow })

	// Cut the pool off: a warm cache must keep deciding without touching it.
	f.pool.Close()
	if d := f.decide("tool_read"); !d.Allow {
		t.Fatalf("warm cache should not need DB: %+v", d)
	}
}

func TestReconnectFlushesAndBypassesCache(t *testing.T) {
	f := newFixture(t, true)
	f.setCeiling(t, LevelRead)
	f.setConsent(t, LevelRead)
	waitFor(t, "allow", func() bool { return f.decide("tool_read").Allow })

	// Kill the LISTEN backend, then revoke while the listener is down or reconnecting.
	exec(t, f.pool, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE query LIKE 'LISTEN%' AND pid <> pg_backend_pid() AND datname = current_database()`)
	waitFor(t, "listener to notice drop", func() bool { return !f.engine.healthy.Load() })
	exec(t, f.pool, `UPDATE consents SET revoked_at = now()`)
	// Whether bypassing or already reconnected, the revocation must be visible immediately.
	if d := f.decide("tool_read"); d.Allow {
		t.Fatalf("revocation missed across disconnect: %+v", d)
	}
	waitFor(t, "listener to reconnect", f.engine.healthy.Load)
	if d := f.decide("tool_read"); d.Allow {
		t.Fatalf("revocation missed after reconnect: %+v", d)
	}
}

func TestInvalidationDuringLoadIsNotCached(t *testing.T) {
	f := newFixture(t, true)
	e := f.engine
	uid := normalizeID(f.userID)

	before := e.snapshot(uid, false)
	stale := &userPolicy{ceilings: map[string]Level{f.serverID: LevelDestructive}}
	e.invalidateUser(uid) // arrives while the load is in flight
	if e.storeUser(uid, stale, before) {
		t.Fatal("stale load was cached after invalidation")
	}
	e.mu.RLock()
	_, present := e.users[uid]
	e.mu.RUnlock()
	if present {
		t.Fatal("stale entry present")
	}

	beforeTools := e.snapshot("", true)
	e.invalidateTools()
	if e.storeTools(&toolSet{}, beforeTools) {
		t.Fatal("stale tools cached after invalidation")
	}
	beforeFlush := e.snapshot(uid, false)
	e.flushAll()
	if e.storeUser(uid, stale, beforeFlush) {
		t.Fatal("stale load cached after flush")
	}
}

func TestConcurrentDecide(t *testing.T) {
	f := newFixture(t, true)
	f.setCeiling(t, LevelWrite)
	f.setConsent(t, LevelWrite)
	waitFor(t, "allow", func() bool { return f.decide("tool_write").Allow })

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				f.decide("tool_" + levels[i%3].String())
				f.engine.AllowedTools(context.Background(), f.subject())
			}
		}()
	}
	for range 20 { // churn invalidations while readers run
		f.setConsent(t, LevelRead)
		f.setConsent(t, LevelWrite)
		time.Sleep(2 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	waitFor(t, "converge", func() bool { return f.decide("tool_write").Allow })
}

func BenchmarkDecideWarm(b *testing.B) {
	f := newFixture(b, true)
	f.setCeiling(b, LevelWrite)
	f.setConsent(b, LevelWrite)
	waitFor(b, "allow", func() bool { return f.decide("tool_write").Allow })
	ctx := context.Background()
	s := f.subject()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if !f.engine.Decide(ctx, s, "srv", "tool_write").Allow {
				b.Fatal("denied")
			}
		}
	})
}

func BenchmarkDecideWarmSerial(b *testing.B) {
	f := newFixture(b, true)
	f.setCeiling(b, LevelWrite)
	f.setConsent(b, LevelWrite)
	waitFor(b, "allow", func() bool { return f.decide("tool_write").Allow })
	ctx := context.Background()
	s := f.subject()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		f.engine.Decide(ctx, s, "srv", "tool_write")
	}
}

func TestOnInvalidateFiresForToolChangesOnly(t *testing.T) {
	f := newFixture(t, true)
	var calls atomic.Int32
	f.engine.OnInvalidate(func() { calls.Add(1) })
	f.setCeiling(t, LevelRead) // user-scoped change: must not fire
	time.Sleep(200 * time.Millisecond)
	before := calls.Load()

	exec(t, f.pool, `UPDATE tools SET status = 'quarantined' WHERE name = 'tool_read'`)
	waitFor(t, "invalidation hook", func() bool { return calls.Load() > before })
}

func TestCeilingOfFailsClosed(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	if got := CeilingOf(ctx, f.pool, f.userID, f.serverID); got != LevelNone {
		t.Fatalf("no ceiling row = %s", got)
	}
	f.setCeiling(t, LevelWrite)
	if got := CeilingOf(ctx, f.pool, f.userID, f.serverID); got != LevelWrite {
		t.Fatalf("ceiling = %s, want write", got)
	}
	if got := CeilingOf(ctx, f.pool, "user-a", "not-a-uuid"); got != LevelNone {
		t.Fatalf("malformed ids = %s", got)
	}
}

func TestOnServerChangedReportsServerOnEnvEdit(t *testing.T) {
	f := newFixture(t, true)
	var id string
	if err := f.pool.QueryRow(context.Background(), `WITH n AS (SELECT uuidv7() AS id)
		INSERT INTO upstream_servers (id, slug, url, auth_kind, transport, stdio_launcher, stdio_command)
		SELECT id, 'proc', 'stdio://' || id, 'none', 'stdio', 'host', '/bin/true' FROM n RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 8)
	f.engine.OnServerChanged(func(serverID string) { got <- serverID })
	exec(t, f.pool, `UPDATE upstream_servers SET stdio_env_ciphertext = '\x01'::bytea WHERE id = $1::uuid`, id)
	waitFor(t, "server changed hook", func() bool {
		for {
			select {
			case s := <-got:
				if s == id {
					return true
				}
			default:
				return false
			}
		}
	})
}
