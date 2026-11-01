package montyrs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

// The decisive comparisons against package monty (the wasm binding): parallel
// external calls, catchable OS denials and type fidelity.

func TestGatherOverlapsAsyncBridgeCalls(t *testing.T) {
	b := &echoBridge{delay: 50 * time.Millisecond}
	start := time.Now()
	out := mustRun(t, `
import asyncio
results = await asyncio.gather(*[call_tool_async("t" + str(i), i) for i in range(8)])
[r["args"] for r in results]
`, b.call, sandbox.Limits{MaxInFlight: 8})
	elapsed := time.Since(start)
	t.Logf("8 x 50ms bridge calls via asyncio.gather: %v (peak in flight %d)", elapsed, b.peak.Load())
	if string(out) != `[0,1,2,3,4,5,6,7]` {
		t.Fatalf("got %s", out)
	}
	if b.peak.Load() != 8 || elapsed > 200*time.Millisecond {
		t.Errorf("peak %d, elapsed %v: want 8 overlapping calls well under 400ms", b.peak.Load(), elapsed)
	}
}

func TestGatherOverAsyncDefWrappersOverlaps(t *testing.T) {
	b := &echoBridge{delay: 50 * time.Millisecond}
	start := time.Now()
	out := mustRun(t, `
import asyncio

async def fetch(name):
    r = await call_tool_async(name, 1)
    return r["tool"]

await asyncio.gather(fetch("a"), fetch("b"), fetch("c"))
`, b.call, sandbox.Limits{})
	if string(out) != `["a","b","c"]` || b.peak.Load() != 3 {
		t.Fatalf("got %s with peak %d, want 3 overlapping calls", out, b.peak.Load())
	}
	if elapsed := time.Since(start); elapsed > 140*time.Millisecond {
		t.Errorf("took %v, want about one 50ms wave", elapsed)
	}
}

// TestGatherOverSyncCallToolStaysSequential pins the package monty behavior
// for the blocking form: a sync external call pauses the whole interpreter.
func TestGatherOverSyncCallToolStaysSequential(t *testing.T) {
	b := &echoBridge{delay: 50 * time.Millisecond}
	start := time.Now()
	out := mustRun(t, `
import asyncio

async def fetch(name):
    return call_tool(name, 1)

[r["tool"] for r in await asyncio.gather(fetch("a"), fetch("b"), fetch("c"))]
`, b.call, sandbox.Limits{MaxInFlight: 8})
	if string(out) != `["a","b","c"]` || b.peak.Load() != 1 {
		t.Fatalf("got %s with peak %d, want sequential calls", out, b.peak.Load())
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("took %v, want >= 3 x 50ms", elapsed)
	}
}

func TestMaxInFlightQueuesExtraCallsFIFO(t *testing.T) {
	b := &echoBridge{delay: 50 * time.Millisecond}
	start := time.Now()
	out := mustRun(t, `
import asyncio
[r["args"] for r in await asyncio.gather(*[call_tool_async("t", i) for i in range(6)])]
`, b.call, sandbox.Limits{MaxInFlight: 2})
	elapsed := time.Since(start)
	t.Logf("6 x 50ms calls with MaxInFlight=2: %v (peak %d)", elapsed, b.peak.Load())
	if string(out) != `[0,1,2,3,4,5]` || b.peak.Load() != 2 {
		t.Fatalf("got %s with peak %d, want all results and peak 2", out, b.peak.Load())
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("took %v, want >= 3 waves of 50ms", elapsed)
	}
}

func TestAsyncBridgeErrorsAreCatchableAtTheAwait(t *testing.T) {
	bridge := func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "bad" {
			return nil, errors.New("upstream 503")
		}
		return json.RawMessage(`"fine"`), nil
	}
	out := mustRun(t, `
import asyncio

async def safe(name):
    try:
        return await call_tool_async(name)
    except RuntimeError as e:
        return "error: " + str(e)

await asyncio.gather(safe("good"), safe("bad"), safe("good"))
`, bridge, sandbox.Limits{})
	if string(out) != `["fine","error: upstream 503","fine"]` {
		t.Fatalf("got %s", out)
	}
	_, err := ex.Run(context.Background(), "import asyncio\nawait asyncio.gather(call_tool_async('good'), call_tool_async('bad'))", bridge, sandbox.Limits{})
	wantErr(t, err, sandbox.ErrScript, "upstream 503")
}

func TestUnawaitedAsyncCallsDoNotHoldTheRun(t *testing.T) {
	cancelled := make(chan struct{})
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	start := time.Now()
	out := mustRun(t, "f = call_tool_async('slow')\n'done'", bridge, sandbox.Limits{Timeout: 5 * time.Second})
	if string(out) != `"done"` || time.Since(start) > time.Second {
		t.Fatalf("got %s after %v", out, time.Since(start))
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("the abandoned call's context was not cancelled")
	}
}

func TestOSAccessRaisesCatchablePermissionError(t *testing.T) {
	out := mustRun(t, `
import os
from pathlib import Path

def attempt(f):
    try:
        f()
        return "allowed"
    except OSError as e:
        return type(e).__name__ + ": " + str(e)

[
    attempt(lambda: open("/etc/passwd").read()),
    attempt(lambda: os.getenv("HOME")),
    attempt(lambda: Path("/etc").exists()),
    attempt(lambda: Path("/tmp/montyrs-pwned").write_text("x")),
]
`, nil, sandbox.Limits{})
	want := `["PermissionError: os access is not available: open",` +
		`"PermissionError: os access is not available: os.getenv",` +
		`"PermissionError: os access is not available: Path.exists",` +
		`"PermissionError: os access is not available: Path.write_text"]`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

func TestClockAndSleepWork(t *testing.T) {
	start := time.Now()
	out := mustRun(t, `
import asyncio
import time
from datetime import datetime
t0 = time.time()
await asyncio.sleep(0.05)
time.sleep(0.05)
[datetime.now().year >= 2025, time.time() - t0 >= 0.09]
`, nil, sandbox.Limits{})
	if string(out) != `[true,true]` {
		t.Fatalf("got %s", out)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v", elapsed)
	}
	_, err := ex.Run(context.Background(), "import time\ntime.sleep(5)", nil, sandbox.Limits{Timeout: 100 * time.Millisecond})
	wantErr(t, err, sandbox.ErrTimeout, "")
}

func TestResultKeepsExactIntegersAndOrder(t *testing.T) {
	for code, want := range map[string]string{
		`2 ** 64 + 1`:                   `18446744073709551617`,
		`-(2 ** 100)`:                   `-1267650600228229401496703205376`,
		`2 ** 53 + 1`:                   `9007199254740993`,
		`(1, "a", (2.5, None))`:         `[1,"a",[2.5,null]]`,
		`{"z": 1, "a": 2, 3: True}`:     `{"z":1,"a":2,"3":true}`,
		`"caf\u00e9 \U0001F600 \"q\""`:  `"café 😀 \"q\""`,
		`[0.1, 1e300, -0.0, 10 ** 2.0]`: `[0.1,1e+300,-0.0,100.0]`,
	} {
		if out := mustRun(t, code, nil, sandbox.Limits{}); string(out) != want {
			t.Errorf("%s: got %s, want %s", code, out, want)
		}
	}
}

func TestBridgeRepliesKeepExactIntegersAndOrder(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"z": 18446744073709551617, "a": 9007199254740993, "f": 1.5}`), nil
	}
	out := mustRun(t, `
r = call_tool("x")
[list(r), r["z"] + 1, r["a"] + 1, type(r["z"]).__name__, type(r["f"]).__name__]
`, bridge, sandbox.Limits{})
	if want := `[["z","a","f"],18446744073709551618,9007199254740994,"int","float"]`; string(out) != want {
		t.Fatalf("got %s, want %s", out, want)
	}
}

func TestBridgeArgsKeepExactIntegers(t *testing.T) {
	var seen json.RawMessage
	bridge := func(_ context.Context, _ string, args json.RawMessage) (json.RawMessage, error) {
		seen = args
		return nil, nil
	}
	mustRun(t, `call_tool("x", {"id": 2 ** 70, "pair": (1, 2)})`, bridge, sandbox.Limits{})
	if want := `{"id":1180591620717411303424,"pair":[1,2]}`; string(seen) != want {
		t.Fatalf("got %s, want %s", seen, want)
	}
}

func TestNonJSONResultsAreRejectedNotCoerced(t *testing.T) {
	for code, contains := range map[string]string{
		`{1, 2}`:                  "Object of type set",
		`frozenset([1])`:          "Object of type frozenset",
		`b"raw"`:                  "Object of type bytes",
		`float("nan")`:            "Out of range float",
		`[float("inf")]`:          "Out of range float",
		`{(1, 2): 3}`:             "keys must be str",
		"def f():\n    pass\nf":   "Object of type repr is not JSON serializable",
		"class A:\n    pass\nA()": "Object of type A",
		"x = []\nfor i in range(300):\n    x = [x]\nx": "nested deeper",
	} {
		_, err := ex.Run(context.Background(), code, nil, sandbox.Limits{})
		wantErr(t, err, sandbox.ErrNotSerializable, contains)
	}
}
