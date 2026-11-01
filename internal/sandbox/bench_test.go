package sandbox

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"
)

// latencies collects per-iteration durations so the benchmarks can report the
// median and p99: `go test -bench` only prints the mean.
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

func benchRun(b *testing.B, code string, bridge Bridge) {
	lat := make(latencies, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t0 := time.Now()
		if _, err := ex.Run(context.Background(), code, bridge, Limits{}); err != nil {
			b.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	b.StopTimer()
	lat.report(b)
}

func BenchmarkRunTrivial(b *testing.B) { benchRun(b, `async () => 1`, noBridge) }

func BenchmarkRunOneBridgeCall(b *testing.B) {
	benchRun(b, `async () => await tools.call("x", {a: 1})`, instantBridge)
}

func BenchmarkRunTenSequentialBridgeCalls(b *testing.B) {
	benchRun(b, `async () => { let n = 0; for (let i = 0; i < 10; i++) { await tools.call("x", {i}); n++; } return n; }`, instantBridge)
}

func BenchmarkRunTenParallelBridgeCalls(b *testing.B) {
	benchRun(b, `async () => (await Promise.all(Array.from({length: 10}, (_, i) => tools.call("x", {i})))).length`, instantBridge)
}

func BenchmarkRunTrivialParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := ex.Run(context.Background(), `async () => 1`, noBridge, Limits{}); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
