package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ex is the Executor shared by the suite; tests that need a different
// configuration build their own with NewExecutor.
var ex *Executor

func TestMain(m *testing.M) {
	// Children measure cold start themselves, so they must not warm up here.
	if os.Getenv(childEnv) != "" {
		os.Exit(m.Run())
	}
	var err error
	if ex, err = NewExecutor(); err != nil {
		fmt.Fprintln(os.Stderr, "new executor:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = ex.Close()
	os.Exit(code)
}

// echoBridge answers after delay with {"tool":name,"args":args}, tracking how
// many calls are in flight at once.
type echoBridge struct {
	delay    time.Duration
	inflight atomic.Int32
	peak     atomic.Int32
}

func (e *echoBridge) call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	n := e.inflight.Add(1)
	defer e.inflight.Add(-1)
	for {
		p := e.peak.Load()
		if n <= p || e.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-time.After(e.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return json.RawMessage(fmt.Sprintf(`{"tool":%q,"args":%s}`, name, args)), nil
}

func mustRun(t *testing.T, code string, bridge Bridge, lim Limits) json.RawMessage {
	t.Helper()
	out, err := ex.Run(context.Background(), code, bridge, lim)
	if err != nil {
		t.Fatalf("Run failed: %v\ncode: %s", err, code)
	}
	return out
}

func mustJSON(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("result %q is not the expected JSON: %v", raw, err)
	}
}

// ---------------------------------------------------------------- criterion 1

func TestC1_AsyncBridgeResolvesWithJSON(t *testing.T) {
	b := &echoBridge{delay: 50 * time.Millisecond}
	out := mustRun(t, `async () => {
		const r = await tools.call("github.list_issues", {repo: "x"});
		return r;
	}`, b.call, Limits{})
	var got struct {
		Tool string
		Args struct{ Repo string }
	}
	mustJSON(t, out, &got)
	if got.Tool != "github.list_issues" || got.Args.Repo != "x" {
		t.Fatalf("unexpected bridge round trip: %s", out)
	}
}

func TestC1_PromiseAllRunsBridgeCallsConcurrently(t *testing.T) {
	b := &echoBridge{delay: 50 * time.Millisecond}
	start := time.Now()
	out := mustRun(t, `async () => {
		const r = await Promise.all([
			tools.call("a", 1), tools.call("b", 2), tools.call("c", 3),
		]);
		return r.map(x => x.tool);
	}`, b.call, Limits{})
	elapsed := time.Since(start)

	var tools []string
	mustJSON(t, out, &tools)
	if strings.Join(tools, ",") != "a,b,c" {
		t.Fatalf("results out of order or missing: %v", tools)
	}
	t.Logf("3 x 50ms bridge calls via Promise.all: %v (peak in-flight bridge calls: %d)", elapsed, b.peak.Load())
	if b.peak.Load() != 3 {
		t.Errorf("peak in-flight bridge calls = %d, want 3", b.peak.Load())
	}
	if elapsed > 120*time.Millisecond {
		t.Errorf("took %v, want well under 150ms", elapsed)
	}
}

func TestC1_SequentialAwaitsStaySequential(t *testing.T) {
	// Control: proves the timing assertion above can tell the two apart.
	b := &echoBridge{delay: 50 * time.Millisecond}
	start := time.Now()
	mustRun(t, `async () => {
		await tools.call("a", 1); await tools.call("b", 2); await tools.call("c", 3);
	}`, b.call, Limits{})
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("3 sequential awaits took %v, want >= 150ms", elapsed)
	}
	if b.peak.Load() != 1 {
		t.Errorf("peak in-flight = %d, want 1", b.peak.Load())
	}
}

func TestC1_NewCallsStartedFromContinuations(t *testing.T) {
	b := &echoBridge{delay: 10 * time.Millisecond}
	out := mustRun(t, `async () => {
		const first = await Promise.all([tools.call("a", 1), tools.call("b", 2)]);
		const second = await Promise.all(first.map(f => tools.call("next", f.tool)));
		return second.map(s => s.args);
	}`, b.call, Limits{})
	var got []string
	mustJSON(t, out, &got)
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("got %v", got)
	}
}

func TestC1_ArgsReachGoAsExactJSON(t *testing.T) {
	var seen json.RawMessage
	bridge := func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		seen = args
		return json.RawMessage(`null`), nil
	}
	mustRun(t, `async () => { await tools.call("n", {a: [1, "two", {three: 3}], u: "héllo", skip: undefined}); }`, bridge, Limits{})
	if string(seen) != `{"a":[1,"two",{"three":3}],"u":"héllo"}` {
		t.Fatalf("args = %s", seen)
	}
	mustRun(t, `async () => { await tools.call("n"); }`, bridge, Limits{})
	if string(seen) != `null` {
		t.Fatalf("missing args should arrive as null, got %s", seen)
	}
}

// ---------------------------------------------------------------- criterion 2

func TestC2_BridgeErrorIsCatchableJSException(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("upstream 503: boom")
	}
	out := mustRun(t, `async () => {
		try { await tools.call("x", {}); return {caught: false}; }
		catch (e) { return {caught: true, isError: e instanceof Error, message: e.message}; }
	}`, bridge, Limits{})
	var got struct {
		Caught, IsError bool
		Message         string
	}
	mustJSON(t, out, &got)
	if !got.Caught || !got.IsError || got.Message != "upstream 503: boom" {
		t.Fatalf("got %+v", got)
	}
}

func TestC2_UncaughtBridgeErrorSurfacesAsScriptError(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("denied by policy")
	}
	_, err := ex.Run(context.Background(), `async () => await tools.call("x", {})`, bridge, Limits{})
	if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("err = %v", err)
	}
}

func TestC2_AllSettledMixesSuccessAndFailure(t *testing.T) {
	bridge := func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "bad" {
			return nil, errors.New("nope")
		}
		return json.RawMessage(`"ok"`), nil
	}
	out := mustRun(t, `async () => (await Promise.allSettled([tools.call("good"), tools.call("bad")])).map(r => r.status)`, bridge, Limits{})
	if string(out) != `["fulfilled","rejected"]` {
		t.Fatalf("got %s", out)
	}
}

func TestC2_BridgePanicBecomesJSException(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { panic("kaboom") }
	out := mustRun(t, `async () => { try { await tools.call("x"); } catch (e) { return e.message } }`, bridge, Limits{})
	if !strings.Contains(string(out), "kaboom") {
		t.Fatalf("got %s", out)
	}
}

// ---------------------------------------------------------------- criterion 3

func heapInUseMB() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse >> 20
}

// liveHeapMB waits for in-flight runs to drain (an abandoned guest keeps its slot until it is
// really gone), then returns the live Go heap after a full GC. HeapAlloc is used, not HeapInuse:
// InUse also counts span fragmentation, which does not shrink after a bomb.
func liveHeapMB(t *testing.T) uint64 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(ex.slots) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(ex.slots); n > 0 {
		t.Fatalf("%d runs still in flight", n)
	}
	debug.FreeOSMemory() // forces a GC
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc >> 20
}

// bombHeapBudgetMB is how much a bomb may leave on the Go heap beyond the baseline taken before
// it. A run is capped at the wasm memory cap plus engine overhead (72 MB by default); a cap that
// did not hold would grow the heap by GBs before the 20 s timeout. The budget is a delta, not an
// absolute ceiling: earlier tests in the package retain memory from instances killed
// by a deadline or trap, which is unrelated to the cap this test checks.
const bombHeapBudgetMB = 128

func TestC3_MemoryCapStopsAllocationBombs(t *testing.T) {
	bombs := map[string]string{
		"growing array of 1MB strings":  `async () => { const a = []; for (let i = 0; ; i++) a.push("y".repeat(1 << 20) + i); }`,
		"growing array of ArrayBuffers": `async () => { const a = []; for (;;) a.push(new ArrayBuffer(1 << 20)); }`,
		"single huge allocation":        `async () => new Array(1 << 28).fill(0).length`,
		"string doubling":               `async () => { let s = "x"; for (;;) s += s; }`,
	}
	for name, code := range bombs {
		t.Run(name, func(t *testing.T) {
			baseline := liveHeapMB(t)
			start := time.Now()
			_, err := ex.Run(context.Background(), code, noBridge, Limits{Timeout: 20 * time.Second})
			t.Logf("stopped after %v: %v", time.Since(start).Round(time.Millisecond), err)
			if !errors.Is(err, ErrMemoryLimit) {
				t.Fatalf("err = %v, want ErrMemoryLimit", err)
			}
			if heap := liveHeapMB(t); heap > baseline+bombHeapBudgetMB {
				t.Errorf("live Go heap after the bomb = %d MB, baseline %d MB, budget +%d MB", heap, baseline, bombHeapBudgetMB)
			}
			// The process must stay healthy: the next script succeeds.
			if out := mustRun(t, `async () => 1 + 1`, noBridge, Limits{}); string(out) != "2" {
				t.Fatalf("follow-up run returned %s", out)
			}
		})
	}
}

func TestC3_ScriptSeesOOMAsCatchableErrorAtTheCap(t *testing.T) {
	out := mustRun(t, `async () => {
		const a = [];
		try { for (;;) a.push(new ArrayBuffer(1 << 20)); }
		catch (e) { const mb = a.length; a.length = 0; return {mb, name: e.name, message: e.message}; }
	}`, noBridge, Limits{})
	var got struct {
		MB      int
		Name    string
		Message string
	}
	mustJSON(t, out, &got)
	t.Logf("allocated %d MB before %s: %s (cap %d MB + %d MB runtime overhead)", got.MB, got.Name, got.Message, ex.memCap>>20, runtimeOverheadBytes>>20)
	if got.MB < 32 || int64(got.MB) > (ex.memCap+runtimeOverheadBytes)>>20 {
		t.Errorf("allocated %d MB, want roughly the configured cap", got.MB)
	}
	if got.Name != "InternalError" || !strings.Contains(got.Message, "out of memory") {
		t.Errorf("got %+v", got)
	}
}

// ---------------------------------------------------------------- criterion 4

func TestC4_SpinIsInterruptedAtDeadline(t *testing.T) {
	start := time.Now()
	_, err := ex.Run(context.Background(), `async () => { while (true) {} }`, noBridge, Limits{Timeout: 2 * time.Second})
	elapsed := time.Since(start)
	t.Logf("while(true){} stopped after %v: %v", elapsed.Round(time.Millisecond), err)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed < 2*time.Second || elapsed > 2500*time.Millisecond {
		t.Errorf("elapsed = %v, want within [2s, 2.5s]", elapsed)
	}
}

func TestC4_SpinCannotBeSwallowedByTryCatch(t *testing.T) {
	start := time.Now()
	_, err := ex.Run(context.Background(), `async () => { for (;;) { try { for (;;) {} } catch (e) {} } }`, noBridge, Limits{Timeout: time.Second})
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestC4_TimeoutWhileAwaitingBridgeThatIgnoresContext(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		time.Sleep(3 * time.Second) // deliberately ignores ctx
		return json.RawMessage(`1`), nil
	}
	start := time.Now()
	_, err := ex.Run(context.Background(), `async () => await tools.call("slow")`, bridge, Limits{Timeout: 500 * time.Millisecond})
	elapsed := time.Since(start)
	t.Logf("slow bridge call stopped after %v: %v", elapsed.Round(time.Millisecond), err)
	if !errors.Is(err, ErrTimeout) || elapsed > 900*time.Millisecond {
		t.Fatalf("err = %v after %v", err, elapsed)
	}
}

func TestC4_TimeoutCancelsTheBridgeContext(t *testing.T) {
	cancelled := make(chan struct{})
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	_, err := ex.Run(context.Background(), `async () => await tools.call("slow")`, bridge, Limits{Timeout: 300 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("bridge context was not cancelled")
	}
}

func TestC4_ParentCancellationStopsTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := ex.Run(ctx, `async () => { while (true) {} }`, noBridge, Limits{Timeout: 10 * time.Second})
	if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestC4_NeverSettlingPromiseFailsFast(t *testing.T) {
	start := time.Now()
	_, err := ex.Run(context.Background(), `async () => { await new Promise(() => {}); }`, noBridge, Limits{Timeout: 5 * time.Second})
	if !errors.Is(err, ErrNeverSettles) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

// A guest parked in a host-side timer cannot be interrupted by wazero
// (poll_oneoff sleeps with time.Sleep). It is only reachable through
// import("qjs:os"), which Run blocks, so this bypasses the scan on purpose.
func TestC4_TimerStallIsOnlyBoundedByWatchdogAndLeaksUntilItEnds(t *testing.T) {
	// TestC4_TimeoutWhileAwaitingBridgeThatIgnoresContext leaves a bridge goroutine sleeping for
	// 3 s; if it ends inside this test's window the goroutine counts below are off by one.
	time.Sleep(3200 * time.Millisecond)
	before := runtime.NumGoroutine()
	start := time.Now()
	_, err := ex.run(context.Background(), `async () => {
		const os = await import("qjs:os");
		os.sleep(2500);
		return "woke";
	}`, noBridge, Limits{Timeout: 500 * time.Millisecond}, false)
	elapsed := time.Since(start)
	t.Logf("guest sleeping in os.sleep(2500): Run returned after %v: %v", elapsed.Round(time.Millisecond), err)
	if !errors.Is(err, ErrTimeout) || elapsed > 900*time.Millisecond {
		t.Fatalf("err = %v after %v", err, elapsed)
	}
	during := runtime.NumGoroutine()
	t.Logf("goroutines: before=%d right after Run=%d (the stuck instance is still alive)", before, during)
	if during <= before {
		t.Errorf("expected the stuck instance to keep a goroutine alive, got %d <= %d", during, before)
	}
	time.Sleep(2500 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Errorf("goroutines did not return to baseline after the sleep ended: %d > %d", after, before)
	}
}

func TestC4_RecursionBombIsContainedAsRuntimeFault(t *testing.T) {
	_, err := ex.Run(context.Background(), `async () => { function f() { return f() + 1 } return f() }`, noBridge, Limits{})
	t.Logf("infinite recursion: %v", err)
	if !errors.Is(err, ErrRuntimeFault) {
		t.Fatalf("err = %v, want ErrRuntimeFault", err)
	}
	if out := mustRun(t, `async () => 7`, noBridge, Limits{}); string(out) != "7" {
		t.Fatalf("follow-up run returned %s", out)
	}
}

func TestC4_RecursionDepthIsSmall(t *testing.T) {
	// Documents the ergonomic cost: QuickJS's stack limit is inactive in this
	// wasm build and the wasm shadow stack is small.
	var maxOK int
	for _, depth := range []int{50, 100, 150, 200, 300, 500, 1000} {
		code := fmt.Sprintf(`async () => { function f(n) { return n === 0 ? 0 : 1 + f(n - 1) } return f(%d) }`, depth)
		if _, err := ex.Run(context.Background(), code, noBridge, Limits{}); err != nil {
			break
		}
		maxOK = depth
	}
	t.Logf("deepest successful JS recursion among the probes: %d frames", maxOK)
	if maxOK < 50 {
		t.Errorf("recursion deeper than 50 frames should work, max ok = %d", maxOK)
	}
}

// ---------------------------------------------------------------- criterion 5

func TestC5_NoAmbientGlobals(t *testing.T) {
	names := []string{
		"fetch", "require", "process", "XMLHttpRequest", "WebSocket", "std", "os", "bjson",
		"setTimeout", "setInterval", "clearTimeout", "clearInterval", "eval", "module", "exports",
		"Deno", "Bun", "window", "document", "Buffer", "WebAssembly", "Worker", "__call", "__settle",
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf(`typeof %s`, n)
	}
	out := mustRun(t, `async () => [`+strings.Join(quoted, ",")+`]`, noBridge, Limits{})
	var types []string
	mustJSON(t, out, &types)
	for i, got := range types {
		if got != "undefined" {
			t.Errorf("typeof %s = %q, want undefined", names[i], got)
		}
	}
}

func TestC5_RemainingGlobalsAreLogged(t *testing.T) {
	out := mustRun(t, `async () => Object.getOwnPropertyNames(globalThis).filter(n => !/^[A-Z]/.test(n))`, noBridge, Limits{})
	t.Logf("non-constructor globals still present: %s", out)
}

func TestC5_ImportOfStdOsAndQjsModulesIsRejected(t *testing.T) {
	for _, code := range []string{
		`async () => { const std = await import("std"); return 1 }`,
		`async () => { const os = await import("os"); return 1 }`,
		`async () => { const std = await import("qjs:std"); return std.getenv("HOME") }`,
		`async () => { const os = await import("qjs:os"); return os.readdir("/") }`,
		`async () => { const b = await import("qjs:bjson"); return 1 }`,
		`async () => { await import /* x */ ("qjs:os") }`,
	} {
		_, err := ex.Run(context.Background(), code, noBridge, Limits{})
		if !errors.Is(err, ErrForbiddenSyntax) {
			t.Errorf("code %q: err = %v, want ErrForbiddenSyntax", code, err)
		}
	}
	// A static `import * as std from "std"` is a SyntaxError in a function
	// expression anyway, and also contains the blocked token.
	if _, err := ex.Run(context.Background(), `import * as std from "std"; async () => 1`, noBridge, Limits{}); err == nil {
		t.Error("static import must not run")
	}
}

func TestC5_StringToCodeEvasionsAreClosed(t *testing.T) {
	for name, code := range map[string]string{
		"eval":                     `async () => eval("1")`,
		"indirect eval":            `async () => (0, eval)("1")`,
		"Function":                 `async () => Function("return 1")()`,
		"new Function":             `async () => new Function("return 1")()`,
		"function constructor":     `async () => (function () {}).constructor("return 1")()`,
		"async function ctor":      `async () => (async () => {}).constructor("return 1")()`,
		"generator ctor":           `async () => (function* () {}).constructor("return 1")`,
		"async generator ctor":     `async () => (async function* () {}).constructor("return 1")`,
		"via Promise.constructor":  `async () => Promise.constructor("return 1")()`,
		"via Object.constructor":   `async () => Object.constructor("return 1")()`,
		"via prototype reflection": `async () => Reflect.getPrototypeOf(async () => {}).constructor("return 1")()`,
		"via globalThis lookup":    `async () => globalThis["Fun" + "ction"]("return 1")()`,
		"class extends Function":   `async () => { class F extends Function {} return new F("return 1")() }`,
	} {
		out, err := ex.Run(context.Background(), code, noBridge, Limits{})
		if err == nil {
			t.Errorf("%s: code generation from a string succeeded: %s", name, out)
		}
	}
}

func TestC5_WasmImportsHaveNoNetworkOrProcessSpawn(t *testing.T) {
	imports := wasmImports(t)
	t.Logf("%d imported functions: %s", len(imports), strings.Join(imports, " "))
	for _, name := range imports {
		if strings.Contains(name, "sock_") || strings.Contains(name, "proc_raise") || strings.Contains(name, "fork") || strings.Contains(name, "exec") {
			t.Errorf("wasm imports %s", name)
		}
	}
	for _, want := range []string{"path_open", "path_unlink_file", "poll_oneoff", "proc_exit"} {
		if !contains(imports, "wasi_snapshot_preview1."+want) {
			t.Errorf("expected the module to import %s (documents the FS and sleep surface)", want)
		}
	}
}

func TestC5_QjsWasmIsTheAuditedBinary(t *testing.T) {
	const want = "88fcbeead33cca3ec8394feb0fb3eec8047ec9ac5062aba7a63799cee11989b7"
	if got := sha256Hex(qjsWasm); got != want {
		t.Fatalf("vendored qjs.wasm sha256 = %s, want %s (fastschema/qjs v0.0.6)", got, want)
	}
}

// If a script did reach qjs:std / qjs:os (the scan and lockdown are best
// effort), the WASI sandbox must still leave nothing to abuse. The import scan
// is bypassed on purpose to test containment independently.
func TestC5_ContainmentHoldsEvenWhenQjsModulesAreReached(t *testing.T) {
	hostDir := t.TempDir()
	canary := filepath.Join(hostDir, "canary.txt")
	if err := os.WriteFile(canary, []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCPGW_SANDBOX_SECRET_ENV", "leak-me")

	code := fmt.Sprintf(`async () => {
		const std = await import("qjs:std");
		const os = await import("qjs:os");
		const r = {};
		r.readHostCanaryByAbsolutePath = std.loadFile(%q);
		r.readHostCanaryByTraversal = std.loadFile("/../../../../../../../../" + %q.slice(1));
		r.rootListing = os.readdir("/")[0].filter(n => n !== "." && n !== "..");
		r.writeFile = (() => { try { const f = std.open("/pwned.txt", "w"); if (f) { f.puts("x"); f.close(); return "created" } return "refused" } catch (e) { return "refused: " + e.message } })();
		r.mkdir = os.mkdir("/pwned-dir") ;
		r.getenv = std.getenv("MCPGW_SANDBOX_SECRET_ENV");
		r.environ = Object.keys(std.getenviron()).length;
		r.cwd = os.getcwd()[0];
		r.noExec = [typeof os.exec, typeof os.pipe, typeof os.fork, typeof os.Worker, typeof std.popen, typeof std.urlGet, typeof os.connect, typeof os.socket];
		return r;
	}`, canary, canary)
	out, err := ex.run(context.Background(), code, noBridge, Limits{}, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("reachable qjs:std / qjs:os, observed: %s", out)

	var got struct {
		ReadHostCanaryByAbsolutePath *string
		ReadHostCanaryByTraversal    *string
		RootListing                  []string
		WriteFile                    string
		Mkdir                        int
		Getenv                       *string
		Environ                      int
		NoExec                       []string
	}
	mustJSON(t, out, &got)
	if got.ReadHostCanaryByAbsolutePath != nil || got.ReadHostCanaryByTraversal != nil {
		t.Errorf("host file readable from the guest")
	}
	if len(got.RootListing) != 0 {
		t.Errorf("guest root is not empty: %v", got.RootListing)
	}
	if got.Getenv != nil || got.Environ != 0 {
		t.Errorf("host environment visible: %v / %d vars", got.Getenv, got.Environ)
	}
	for i, typ := range got.NoExec {
		if typ != "undefined" {
			t.Errorf("os/std API #%d is %q, want undefined", i, typ)
		}
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: the read-only mount (chmod 0555) is not enforced, skipping write checks")
	}
	if strings.HasPrefix(got.WriteFile, "created") || got.Mkdir == 0 {
		t.Errorf("guest could write to the mounted directory: writeFile=%q mkdir=%d", got.WriteFile, got.Mkdir)
	}
	entries, _ := os.ReadDir(ex.dir)
	if len(entries) != 0 {
		t.Errorf("mounted directory was modified: %v", entries)
	}
}

// ---------------------------------------------------------------- criterion 6

func TestC6_GlobalsAndPrototypesDoNotLeakBetweenExecutions(t *testing.T) {
	mustRun(t, `async () => {
		globalThis.leak = 42;
		Object.prototype.polluted = "yes";
		Array.prototype.polluted = "yes";
		JSON.stringify = () => "forged";
	}`, noBridge, Limits{})
	out := mustRun(t, `async () => ({
		leak: typeof globalThis.leak,
		objectPolluted: typeof ({}).polluted,
		arrayPolluted: typeof [].polluted,
		stringify: JSON.stringify({a: 1}),
	})`, noBridge, Limits{})
	if string(out) != `{"leak":"undefined","objectPolluted":"undefined","arrayPolluted":"undefined","stringify":"{\"a\":1}"}` {
		t.Fatalf("state leaked between executions: %s", out)
	}
}

// ---------------------------------------------------------------- criterion 7

func TestC7_ReturnValuesComeBackAsJSON(t *testing.T) {
	cases := map[string]string{
		`async () => ({a: 1, b: [true, null, "x"], c: {d: 1.5}})`: `{"a":1,"b":[true,null,"x"],"c":{"d":1.5}}`,
		`async () => "héllo ñ 日本"`:                                `"héllo ñ 日本"`,
		`async () => 42`:                                          `42`,
		`async () => true`:                                        `true`,
		`async () => null`:                                        `null`,
		`async () => undefined`:                                   `null`,
		`async () => {}`:                                          `null`,
		`async () => NaN`:                                         `null`,
		`async () => [1, undefined, () => 1]`:                     `[1,null,null]`,
		`async () => ({f() {}, u: undefined, k: 1})`:              `{"k":1}`,
		`async () => new Date(0)`:                                 `"1970-01-01T00:00:00.000Z"`,
		`async () => new Map([[1, 2]])`:                           `{}`,
		`() => 5`:                                                 `5`,
		`async () => 1;`:                                          `1`,
		`  async () => 1  ;  `:                                    `1`,
		`async () => "x".repeat(200 << 10)`:                       `"` + strings.Repeat("x", 200<<10) + `"`,
	}
	for code, want := range cases {
		out, err := ex.Run(context.Background(), code, noBridge, Limits{})
		if err != nil {
			t.Errorf("%.60q: %v", code, err)
			continue
		}
		if string(out) != want {
			t.Errorf("%.60q: got %.80s, want %.80s", code, out, want)
		}
	}
}

func TestC7_NonSerializableValuesGiveAClearError(t *testing.T) {
	for name, code := range map[string]string{
		"function":        `async () => () => 1`,
		"symbol":          `async () => Symbol("s")`,
		"bigint":          `async () => 10n`,
		"nested bigint":   `async () => ({n: 10n})`,
		"circular":        `async () => { const a = {}; a.self = a; return a }`,
		"throwing toJSON": `async () => ({toJSON() { throw new Error("no") }})`,
	} {
		_, err := ex.Run(context.Background(), code, noBridge, Limits{})
		if !errors.Is(err, ErrNotSerializable) {
			t.Errorf("%s: err = %v, want ErrNotSerializable", name, err)
			continue
		}
		t.Logf("%-16s -> %v", name, err)
	}
}

func TestC7_ScriptFailuresAreDistinguishable(t *testing.T) {
	for name, tc := range map[string]struct{ code, want string }{
		"throw Error":    {`async () => { throw new Error("boom") }`, "Error: boom"},
		"throw string":   {`async () => { throw "plain" }`, "plain"},
		"throw object":   {`async () => { throw {code: 7} }`, `{"code":7}`},
		"reference":      {`async () => missingVariable`, "ReferenceError"},
		"syntax":         {`async () => {`, "SyntaxError"},
		"not a function": {`1 + 1`, "code must evaluate to a function"},
		"statements":     {`const r = await tools.call("a"); return r;`, "SyntaxError"},
		"strict mode":    {`async () => { undeclared = 1 }`, "ReferenceError"},
	} {
		_, err := ex.Run(context.Background(), tc.code, noBridge, Limits{})
		if !errors.Is(err, ErrScript) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want ErrScript containing %q", name, err, tc.want)
		}
	}
}

// ---------------------------------------------------------------- robustness

func TestConcurrentRunsAreIndependent(t *testing.T) {
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := &echoBridge{delay: 20 * time.Millisecond}
			code := fmt.Sprintf(`async () => {
				globalThis.me = %d;
				const r = await Promise.all([tools.call("t", %d), tools.call("t", %d)]);
				return [globalThis.me, r[0].args, r[1].args];
			}`, i, i, i*10)
			out, err := ex.Run(context.Background(), code, b.call, Limits{})
			if err != nil {
				errs <- err
				return
			}
			if want := fmt.Sprintf("[%d,%d,%d]", i, i, i*10); string(out) != want {
				errs <- fmt.Errorf("worker %d got %s, want %s", i, out, want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestHealthStaysFlatAcrossMixedFailures(t *testing.T) {
	scenarios := []struct {
		name string
		code string
		lim  Limits
		want error
	}{
		{"ok", `async () => (await tools.call("x")).tool`, Limits{}, nil},
		{"oom", `async () => { const a = []; for (;;) a.push(new ArrayBuffer(8 << 20)); }`, Limits{}, ErrMemoryLimit},
		{"spin", `async () => { for (;;) {} }`, Limits{Timeout: 150 * time.Millisecond}, ErrTimeout},
		{"bridge error", `async () => await tools.call("fail")`, Limits{}, ErrScript},
		{"recursion", `async () => { function f() { return f() + 1 } return f() }`, Limits{}, ErrRuntimeFault},
		{"never settles", `async () => { await new Promise(() => {}) }`, Limits{}, ErrNeverSettles},
		{"slow bridge", `async () => await tools.call("slow")`, Limits{Timeout: 150 * time.Millisecond}, ErrTimeout},
	}
	bridge := func(ctx context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "fail":
			return nil, errors.New("fail")
		case "slow":
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return json.RawMessage(`{"tool":"x"}`), nil
	}

	const rounds = 15
	runtime.GC()
	baselineGoroutines := runtime.NumGoroutine()
	var heapMid uint64
	for round := range rounds {
		for _, sc := range scenarios {
			out, err := ex.Run(context.Background(), sc.code, bridge, sc.lim)
			if sc.want == nil && err != nil || sc.want != nil && !errors.Is(err, sc.want) {
				t.Fatalf("round %d %s: out=%s err=%v, want %v", round, sc.name, out, err, sc.want)
			}
		}
		if round == 4 {
			heapMid = heapInUseMB()
		}
	}
	heapEnd := heapInUseMB()
	time.Sleep(200 * time.Millisecond)
	goroutinesEnd := runtime.NumGoroutine()
	t.Logf("%d executions: goroutines %d -> %d, Go heap in use after round 5 = %d MB, after round %d = %d MB",
		rounds*len(scenarios), baselineGoroutines, goroutinesEnd, heapMid, rounds, heapEnd)
	if goroutinesEnd > baselineGoroutines+3 {
		t.Errorf("goroutine leak: %d -> %d", baselineGoroutines, goroutinesEnd)
	}
	if heapEnd > heapMid+64 {
		t.Errorf("heap keeps growing: %d MB -> %d MB", heapMid, heapEnd)
	}
}

// Atomics.wait is the one built-in that could park the OS thread outside any
// interrupt poll (it needs can_block). Checked because os.sleep did exactly that.
func TestC4_AtomicsWaitCannotParkTheThread(t *testing.T) {
	t0 := time.Now()
	out, err := ex.Run(context.Background(), `async () => {
		try { return Atomics.wait(new Int32Array(new SharedArrayBuffer(16)), 0, 0, 3000) } catch (e) { return String(e) }
	}`, noBridge, Limits{Timeout: 400 * time.Millisecond})
	elapsed := time.Since(t0)
	t.Logf("Atomics.wait(3000ms) with Timeout=400ms -> out=%s err=%v elapsed=%v", out, err, elapsed.Round(time.Millisecond))
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Atomics.wait parked the call for %v despite a 400ms deadline", elapsed)
	}
}
