package adminapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func decodeInto(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) seedAudit(ago time.Duration, server, tool, decision string, latency *int) {
	h.t.Helper()
	h.exec(`INSERT INTO audit_events (ts, user_id, client_id, server_slug, tool_name, decision, latency_ms)
		VALUES ($1, gen_random_uuid(), 'c', $2, $3, $4, $5)`, h.clock.Now().Add(-ago), server, tool, decision, latency)
}

func TestStatsEndpoint(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	lat := 40
	h.seedAudit(20*time.Second, "alpha", "get", "allow", &lat)
	h.seedAudit(25*time.Second, "alpha", "get", "deny", nil)
	h.seedAudit(25*time.Second, "beta", "put", "allow", &lat)

	var st struct {
		Window        string `json:"window"`
		BucketSeconds int    `json:"bucket_seconds"`
		Totals        struct {
			Calls  int      `json:"calls"`
			Denied int      `json:"denied"`
			P95MS  *float64 `json:"p95_ms"`
		} `json:"totals"`
		Series   []map[string]any `json:"series"`
		TopTools []map[string]any `json:"top_tools"`
		Servers  []map[string]any `json:"servers"`
	}
	res := h.get(c, "/admin/api/stats?window=5m&server=alpha")
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, readAll(res))
	}
	decodeInto(t, res, &st)
	if st.Window != "5m" || st.BucketSeconds != 10 || len(st.Series) != 30 || st.Totals.Calls != 2 || st.Totals.Denied != 1 ||
		st.Totals.P95MS == nil || *st.Totals.P95MS != 40 || len(st.Servers) != 1 || len(st.TopTools) != 1 {
		t.Fatalf("%+v", st)
	}
	for _, key := range []string{"ts", "calls", "denied", "p95_ms"} {
		if _, ok := st.Series[0][key]; !ok {
			t.Fatalf("series bucket lacks %q: %v", key, st.Series[0])
		}
	}
}

func TestStatsDefaultsAndValidation(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	var st struct {
		Window string `json:"window"`
		Series []any  `json:"series"`
	}
	res := h.get(c, "/admin/api/stats")
	decodeInto(t, res, &st)
	if res.StatusCode != 200 || st.Window != "1h" || len(st.Series) != 60 {
		t.Fatalf("%d %+v", res.StatusCode, st)
	}
	for _, q := range []string{"window=2h", "top=0x", "top=201", "top=-1"} {
		if res := h.get(c, "/admin/api/stats?"+q); res.StatusCode != 422 {
			t.Errorf("%s: %d", q, res.StatusCode)
		}
	}
}

func TestSystemEndpoint(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	var sys map[string]any
	res := h.get(c, "/admin/api/system")
	decodeInto(t, res, &sys)
	if res.StatusCode != 200 || sys["go_version"] == "" || sys["public_url"] != publicURL || sys["version"] == "" || sys["started_at"] == nil {
		t.Fatalf("%d %v", res.StatusCode, sys)
	}
	for _, key := range []string{"commit", "commit_time", "modified"} {
		if _, ok := sys[key]; !ok {
			t.Fatalf("missing %q: %v", key, sys)
		}
	}
}

func TestStatsSystemProbeRequireSession(t *testing.T) {
	h := newHarness(t)
	for _, r := range [][2]string{{"GET", "/stats"}, {"GET", "/system"}, {"POST", "/servers/x/test"}, {"POST", "/servers/test"}} {
		if res := h.do(nil, r[0], "/admin/api"+r[1], map[string]string{}, reqOpt{}); res.StatusCode != 401 {
			t.Errorf("%s %s anonymous: %d", r[0], r[1], res.StatusCode)
		}
	}
}
