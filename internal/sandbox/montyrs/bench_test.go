package montyrs

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

// Mirrors internal/sandbox/bench_test.go (same names, same p50/p99 metrics) so
// the two engines can be compared with benchstat.

type latencies []time.Duration

func (l latencies) report(b *testing.B) {
	if len(l) == 0 {
		return
	}
	sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
	b.ReportMetric(float64(l[len(l)/2].Nanoseconds()), "p50-ns/op")
	b.ReportMetric(float64(l[len(l)*99/100].Nanoseconds()), "p99-ns/op")
}

func instantBridge(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func benchRun(b *testing.B, code string, bridge sandbox.Bridge) {
	lat := make(latencies, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t0 := time.Now()
		if _, err := ex.Run(context.Background(), code, bridge, sandbox.Limits{}); err != nil {
			b.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	b.StopTimer()
	lat.report(b)
}

func BenchmarkRunTrivial(b *testing.B) { benchRun(b, `1`, noBridge) }

func BenchmarkRunOneBridgeCall(b *testing.B) {
	benchRun(b, `call_tool("x", {"a": 1})`, instantBridge)
}

func BenchmarkRunTenSequentialBridgeCalls(b *testing.B) {
	benchRun(b, "n = 0\nfor i in range(10):\n    call_tool('x', {'i': i})\n    n += 1\nn", instantBridge)
}

// BenchmarkRunTenParallelBridgeCalls uses the awaitable form, which really
// overlaps the calls (package monty's gather over call_tool is sequential).
func BenchmarkRunTenParallelBridgeCalls(b *testing.B) {
	benchRun(b, `
import asyncio
len(await asyncio.gather(*[call_tool_async("x", {"i": i}) for i in range(10)]))
`, instantBridge)
}

func BenchmarkRunTrivialParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := ex.Run(context.Background(), `1`, noBridge, sandbox.Limits{}); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
