package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

// statsNow is aligned to every bucket size so seeded offsets land in a known bucket.
var statsNow = time.Now().UTC().Truncate(3 * time.Hour)

func (h *harness) seedAudit(ts time.Time, server, tool, decision, reason string, latency *int) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), `INSERT INTO audit_events (ts, user_id, client_id, server_slug, tool_name, decision, reason, latency_ms)
		VALUES ($1, gen_random_uuid(), 'c', $2, $3, $4, $5, $6)`, ts, server, tool, decision, reason, latency); err != nil {
		h.t.Fatal(err)
	}
}

func ms(v int) *int { return &v }

func TestStatsBucketsZeroFilledAndContiguous(t *testing.T) {
	h := newHarness(t)
	// 1h window = 60 buckets of 60s ending at statsNow; the last bucket starts at statsNow-60s.
	h.seedAudit(statsNow.Add(-30*time.Second), "alpha", "get", "allow", "", ms(10))
	h.seedAudit(statsNow.Add(-40*time.Second), "alpha", "get", "deny", "ceiling", nil)
	h.seedAudit(statsNow.Add(-10*time.Minute-5*time.Second), "alpha", "get", "allow", "", ms(30))
	h.seedAudit(statsNow.Add(-2*time.Hour), "alpha", "get", "allow", "", ms(99)) // outside the window
	st, err := h.svc.Stats(context.Background(), admin.StatsQuery{Window: "1h", Now: statsNow})
	if err != nil {
		t.Fatal(err)
	}
	if st.BucketSeconds != 60 || len(st.Series) != 60 {
		t.Fatalf("bucket_seconds=%d buckets=%d", st.BucketSeconds, len(st.Series))
	}
	for i := 1; i < len(st.Series); i++ {
		if d := st.Series[i].TS.Sub(st.Series[i-1].TS); d != time.Minute {
			t.Fatalf("gap %v at %d", d, i)
		}
	}
	last, mid := st.Series[59], st.Series[49]
	if !last.TS.Equal(statsNow.Add(-time.Minute)) || last.Calls != 2 || last.Denied != 1 || last.P95MS == nil || *last.P95MS != 10 {
		t.Fatalf("last bucket %+v", last)
	}
	if mid.Calls != 1 || mid.Denied != 0 || st.Series[0].Calls != 0 || st.Series[0].P95MS != nil {
		t.Fatalf("mid %+v first %+v", mid, st.Series[0])
	}
	if tt := st.Totals; tt.Calls != 3 || tt.Allowed != 2 || tt.Denied != 1 || tt.P50MS == nil || *tt.P50MS != 20 {
		t.Fatalf("totals %+v", tt)
	}
}

func TestStatsEmptyHasNullPercentilesAndNonNilLists(t *testing.T) {
	h := newHarness(t)
	st, err := h.svc.Stats(context.Background(), admin.StatsQuery{Now: statsNow})
	if err != nil {
		t.Fatal(err)
	}
	if st.Window != "1h" || st.Totals.P95MS != nil || st.Totals.P50MS != nil || st.TopTools == nil || st.TopDenies == nil || st.Servers == nil {
		t.Fatalf("%+v", st)
	}
}

func TestStatsServerFilterTopAndExecutions(t *testing.T) {
	h := newHarness(t)
	at := statsNow.Add(-time.Minute)
	for range 3 {
		h.seedAudit(at, "alpha", "busy", "allow", "", ms(5))
	}
	h.seedAudit(at, "alpha", "rare", "deny", "ceiling", ms(5))
	h.seedAudit(at, "alpha", "rare", "deny", "ceiling", ms(5))
	h.seedAudit(at, "beta", "other", "deny", "quarantine", ms(5))
	for _, status := range []string{"succeeded", "failed", "running"} {
		fin := "now()"
		if status == "running" {
			fin = "NULL"
		}
		h.exec(`INSERT INTO executions (user_id, client_id, kind, code, code_hash, status, started_at, finished_at)
			VALUES (gen_random_uuid(), 'c', 'execute', 'x', 'h', $1, $2, `+fin+`)`, status, at)
	}
	all, err := h.svc.Stats(context.Background(), admin.StatsQuery{Window: "1h", Now: statsNow})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Servers) != 2 || all.Servers[0].Server != "alpha" || all.Servers[0].Calls != 5 {
		t.Fatalf("servers %+v", all.Servers)
	}
	if all.TopTools[0].Tool != "busy" || all.TopTools[0].Calls != 3 || all.TopTools[1].Tool != "rare" || all.TopTools[1].Denied != 2 {
		t.Fatalf("top tools %+v", all.TopTools)
	}
	if all.TopDenies[0].Tool != "rare" || all.TopDenies[0].Reason != "ceiling" || all.TopDenies[0].Count != 2 || len(all.TopDenies) != 2 {
		t.Fatalf("top denies %+v", all.TopDenies)
	}
	if all.Totals.Executions != 3 || all.Totals.FailedExecutions != 1 {
		t.Fatalf("executions %+v", all.Totals)
	}
	one, err := h.svc.Stats(context.Background(), admin.StatsQuery{Window: "1h", Server: "beta", Top: 1, Now: statsNow})
	if err != nil {
		t.Fatal(err)
	}
	if one.Totals.Calls != 1 || len(one.Servers) != 1 || len(one.TopTools) != 1 || one.Totals.Executions != 3 {
		t.Fatalf("filtered %+v", one)
	}
}

func TestStatsRejectsBadWindowAndTop(t *testing.T) {
	h := newHarness(t)
	for _, q := range []admin.StatsQuery{{Window: "2h"}, {Top: -1}, {Top: 201}} {
		if _, err := h.svc.Stats(context.Background(), q); !errors.Is(err, admin.ErrInvalid) {
			t.Fatalf("%+v: %v", q, err)
		}
	}
}
