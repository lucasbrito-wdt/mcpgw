package sandbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// retentionRuns is how many executions the retention test performs. At the
// ~0.5 MB per instance seen before the fix, 60 runs retain ~30 MB; after it the
// growth is noise.
const retentionRuns = 60

// retentionBudgetMB is the live-heap growth tolerated over retentionRuns killed
// executions. Without the fix the growth is above 25 MB; with it, below 5 MB.
const retentionBudgetMB = 12

func TestFinishedAndKilledInstancesDoNotRetainMemory(t *testing.T) {
	kills := map[string]struct {
		code    string
		timeout time.Duration
	}{
		"deadline in a busy loop":   {`async () => { for (;;) {} }`, 50 * time.Millisecond},
		"completed run":             {`async () => 1`, 5 * time.Second},
		"deadline on a bridge wait": {`async () => { await tools.call("slow", {}) }`, 50 * time.Millisecond},
	}
	for name, k := range kills {
		t.Run(name, func(t *testing.T) {
			slow := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			// Warm the paths once so one-time allocations do not count.
			_, _ = ex.Run(context.Background(), k.code, slow, Limits{Timeout: k.timeout})
			baseline := liveHeapMB(t)
			for i := 0; i < retentionRuns; i++ {
				_, _ = ex.Run(context.Background(), k.code, slow, Limits{Timeout: k.timeout})
			}
			heap := liveHeapMB(t)
			t.Logf("baseline %d MB, after %d kills %d MB", baseline, retentionRuns, heap)
			if heap > baseline+retentionBudgetMB {
				t.Errorf("live heap grew %d MB over %d killed runs (budget %d MB)", heap-baseline, retentionRuns, retentionBudgetMB)
			}
		})
	}
}
