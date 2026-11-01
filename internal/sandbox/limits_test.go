package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitGoroutines polls until the goroutine count is back at (or below) base+slack.
func waitGoroutines(t *testing.T, base, slack int, within time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		n := runtime.NumGoroutine()
		if n <= base+slack || time.Now().After(deadline) {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---------------------------------------------------------------- MaxInFlight

func TestMaxInFlight_ExtraCallsQueueAndAllComplete(t *testing.T) {
	b := &echoBridge{delay: 30 * time.Millisecond}
	start := time.Now()
	out := mustRun(t, `async () => {
		const r = await Promise.all(Array.from({length: 10}, (_, i) => tools.call("t", i)));
		return r.map(x => x.args);
	}`, b.call, Limits{MaxInFlight: 3})
	elapsed := time.Since(start)

	var got []int
	mustJSON(t, out, &got)
	if fmt.Sprint(got) != "[0 1 2 3 4 5 6 7 8 9]" {
		t.Fatalf("results out of order or missing: %v", got)
	}
	if peak := b.peak.Load(); peak != 3 {
		t.Errorf("peak in-flight = %d, want exactly 3", peak)
	}
	// 10 calls, 3 at a time, 30ms each: 4 waves.
	if elapsed < 110*time.Millisecond {
		t.Errorf("elapsed %v: calls were not throttled", elapsed)
	}
}

func TestMaxInFlight_QueueIsFIFO(t *testing.T) {
	var mu sync.Mutex
	var order []string
	bridge := func(_ context.Context, _ string, args json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		order = append(order, string(args))
		mu.Unlock()
		return json.RawMessage(`null`), nil
	}
	mustRun(t, `async () => { await Promise.all([1,2,3,4,5,6].map(i => tools.call("t", i))); }`, bridge, Limits{MaxInFlight: 1})
	if strings.Join(order, ",") != "1,2,3,4,5,6" {
		t.Fatalf("order = %v", order)
	}
}

func TestMaxInFlight_DefaultCapIsEight(t *testing.T) {
	b := &echoBridge{delay: 20 * time.Millisecond}
	mustRun(t, `async () => { await Promise.all(Array.from({length: 20}, () => tools.call("t"))); }`, b.call, Limits{MaxBridgeCalls: 100})
	if peak := b.peak.Load(); peak != 8 {
		t.Fatalf("peak = %d, want 8", peak)
	}
}

func TestMaxInFlight_QueuedCallsStillHonourTheDeadline(t *testing.T) {
	b := &echoBridge{delay: time.Hour}
	start := time.Now()
	_, err := ex.Run(context.Background(), `async () => { await Promise.all([1,2,3,4].map(i => tools.call("t", i))); }`, b.call, Limits{MaxInFlight: 1, Timeout: 300 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if peak := b.peak.Load(); peak != 1 {
		t.Errorf("peak = %d, want 1", peak)
	}
}

// ---------------------------------------------------------------- MaxBridgeCalls

func TestMaxBridgeCalls_ExcessCallRejectsAsCatchableError(t *testing.T) {
	b := &echoBridge{}
	out := mustRun(t, `async () => {
		const ok = [];
		let caught = null;
		for (let i = 0; i < 5; i++) {
			try { ok.push((await tools.call("t", i)).args); }
			catch (e) { caught = {isError: e instanceof Error, message: e.message}; break; }
		}
		return {ok, caught};
	}`, b.call, Limits{MaxBridgeCalls: 3})
	var got struct {
		Ok     []int
		Caught *struct {
			IsError bool
			Message string
		}
	}
	mustJSON(t, out, &got)
	if fmt.Sprint(got.Ok) != "[0 1 2]" || got.Caught == nil || !got.Caught.IsError || !strings.Contains(got.Caught.Message, "too many bridge calls") {
		t.Fatalf("got %s", out)
	}
}

func TestMaxBridgeCalls_UncaughtSurfacesAsErrTooManyCalls(t *testing.T) {
	var calls atomic.Int32
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`1`), nil
	}
	_, err := ex.Run(context.Background(), `async () => { for (let i = 0; i < 10; i++) await tools.call("t", i); }`, bridge, Limits{MaxBridgeCalls: 4})
	if !errors.Is(err, ErrTooManyCalls) {
		t.Fatalf("err = %v, want ErrTooManyCalls", err)
	}
	if calls.Load() != 4 {
		t.Errorf("bridge saw %d calls, want 4 (the excess must never reach it)", calls.Load())
	}
}

func TestMaxBridgeCalls_CountsParallelAndQueuedCalls(t *testing.T) {
	b := &echoBridge{delay: 5 * time.Millisecond}
	out := mustRun(t, `async () => (await Promise.allSettled(Array.from({length: 7}, (_, i) => tools.call("t", i)))).map(r => r.status[0])`,
		b.call, Limits{MaxBridgeCalls: 5, MaxInFlight: 2})
	if string(out) != `["f","f","f","f","f","r","r"]` {
		t.Fatalf("got %s", out)
	}
}

// ---------------------------------------------------------------- un-awaited calls

func TestUnawaitedCall_ContextCancelledWhenScriptEnds(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	out := mustRun(t, `async () => { tools.call("slow", 1); await new Promise(r => r()); return "done" }`, bridge, Limits{})
	if string(out) != `"done"` {
		t.Fatalf("out = %s", out)
	}
	select {
	case <-started:
	default:
		t.Fatal("the un-awaited call never reached the bridge")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("bridge context was not cancelled when the script settled")
	}
}

func TestUnawaitedCall_RunWaitsForCooperativeBridgesToReturn(t *testing.T) {
	var running atomic.Int32
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		running.Add(1)
		defer running.Add(-1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	mustRun(t, `async () => { for (let i = 0; i < 5; i++) tools.call("slow", i); return 1 }`, bridge, Limits{})
	if n := running.Load(); n != 0 {
		t.Fatalf("%d bridge calls still running after Run returned", n)
	}
}

func TestUnawaitedCall_FailureDoesNotRaiseUnhandledRejection(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("boom")
	}
	out := mustRun(t, `async () => {
		tools.call("a"); tools.call("b");
		await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
		return "survived";
	}`, bridge, Limits{})
	if string(out) != `"survived"` {
		t.Fatalf("out = %s", out)
	}
}

func TestUnawaitedCall_LateResultsNeverTouchTheClosedRuntime(t *testing.T) {
	release := make(chan struct{})
	var finished atomic.Int32
	bridge := func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		<-release // deliberately ignores ctx, returns only after Run is gone
		finished.Add(1)
		return json.RawMessage(`{"late":true}`), nil
	}
	base := runtime.NumGoroutine()
	start := time.Now()
	out := mustRun(t, `async () => { tools.call("a"); tools.call("b"); return "early" }`, bridge, Limits{})
	if string(out) != `"early"` {
		t.Fatalf("out = %s", out)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("Run blocked %v on bridges that ignore their context", time.Since(start))
	}
	close(release)
	if n := waitGoroutines(t, base, 1, 2*time.Second); n > base+1 {
		t.Fatalf("goroutines %d -> %d after late results", base, n)
	}
	// waitGoroutines tolerates one straggler, so the second bridge may still be finishing.
	for deadline := time.Now().Add(2 * time.Second); finished.Load() != 2 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if finished.Load() != 2 {
		t.Fatalf("finished = %d", finished.Load())
	}
	if out := mustRun(t, `async () => 3`, noBridge, Limits{}); string(out) != "3" {
		t.Fatalf("follow-up run: %s", out)
	}
}

func TestUnawaitedCall_NoGoroutineLeakAcrossManyRuns(t *testing.T) {
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Hour):
			return nil, nil
		}
	}
	time.Sleep(100 * time.Millisecond)
	base := runtime.NumGoroutine()
	codes := []string{
		`async () => { tools.call("x"); tools.call("y"); return 1 }`,
		`async () => { tools.call("x"); throw new Error("e") }`,
		`async () => { Promise.all([tools.call("x"), tools.call("y")]); return 2 }`,
	}
	for i := 0; i < 30; i++ {
		_, _ = ex.Run(context.Background(), codes[i%len(codes)], bridge, Limits{})
	}
	// Timeouts and parent cancellation with calls in flight.
	for i := 0; i < 5; i++ {
		_, err := ex.Run(context.Background(), `async () => { await tools.call("slow") }`, bridge, Limits{Timeout: 100 * time.Millisecond})
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := waitGoroutines(t, base, 2, 3*time.Second); n > base+2 {
		t.Fatalf("goroutine leak: %d -> %d", base, n)
	}
}

// ---------------------------------------------------------------- os.sleep and friends

func TestHostBuiltins_UnreachableFromScripts(t *testing.T) {
	// Everything that could park the OS thread outside an interrupt poll lives
	// in the qjs:os / qjs:std modules, reachable only through import().
	evasions := map[string]string{
		"dynamic import":    `async () => { const os = await import("qjs:os"); os.sleep(5000) }`,
		"comment between":   `async () => { await import /* x */ ("qjs:os") }`,
		"newline between":   "async () => { await import\n(\"qjs:os\") }",
		"import.meta":       `async () => import.meta`,
		"inside a template": "async () => `${await import('qjs:os')}`",
		"static import":     `import * as os from "qjs:os"; async () => os.sleep(5000)`,
	}
	for name, code := range evasions {
		start := time.Now()
		_, err := ex.Run(context.Background(), code, noBridge, Limits{Timeout: time.Second})
		if !errors.Is(err, ErrForbiddenSyntax) {
			t.Errorf("%s: err = %v, want ErrForbiddenSyntax", name, err)
		}
		if time.Since(start) > 200*time.Millisecond {
			t.Errorf("%s: took %v, the scan must reject before any runtime is built", name, time.Since(start))
		}
	}

	// Spellings the scan does not catch must not parse as an import either.
	for name, code := range map[string]string{
		"export from":             `export * from "qjs:os"; async () => 1`,
		"unicode-escaped keyword": `async () => { await \u0069mport("qjs:os") }`,
		"string eval":             `async () => (0, eval)("import('qjs:os')")`,
		"Function":                `async () => Function("return import('qjs:os')")()`,
		"constructor":             `async () => (async () => {}).constructor("return import('qjs:os')")()`,
	} {
		out, err := ex.Run(context.Background(), code, noBridge, Limits{Timeout: time.Second})
		if err == nil {
			t.Errorf("%s: succeeded with %s", name, out)
		}
	}

	out := mustRun(t, `async () => [typeof os, typeof std, typeof bjson, typeof setTimeout, typeof setInterval, typeof require]`, noBridge, Limits{})
	if string(out) != `["undefined","undefined","undefined","undefined","undefined","undefined"]` {
		t.Fatalf("globals: %s", out)
	}
}

// ---------------------------------------------------------------- traps

func TestTrap_NeverReusesTheInstance(t *testing.T) {
	_, err := ex.Run(context.Background(), `async () => { globalThis.survivor = 1; function f() { return f() + 1 } return f() }`, noBridge, Limits{})
	if !errors.Is(err, ErrRuntimeFault) {
		t.Fatalf("err = %v, want ErrRuntimeFault", err)
	}
	for i := 0; i < 3; i++ {
		out := mustRun(t, `async () => [typeof globalThis.survivor, (await tools.call("t", 1)).args]`, (&echoBridge{}).call, Limits{})
		if string(out) != `["undefined",1]` {
			t.Fatalf("run %d after the trap: %s", i, out)
		}
	}
}

func TestTrap_AfterTimeoutAndMemoryKillNextRunIsHealthy(t *testing.T) {
	for name, tc := range map[string]struct {
		code string
		lim  Limits
		want error
	}{
		"timeout": {`async () => { for (;;) {} }`, Limits{Timeout: 100 * time.Millisecond}, ErrTimeout},
		"oom":     {`async () => { const a = []; for (;;) a.push(new ArrayBuffer(8 << 20)) }`, Limits{}, ErrMemoryLimit},
	} {
		if _, err := ex.Run(context.Background(), tc.code, noBridge, tc.lim); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if out := mustRun(t, `async () => ({v: typeof globalThis.x})`, noBridge, Limits{}); string(out) != `{"v":"undefined"}` {
			t.Fatalf("%s: follow-up %s", name, out)
		}
	}
}

// ---------------------------------------------------------------- result size

func TestMaxResultBytes(t *testing.T) {
	_, err := ex.Run(context.Background(), `async () => "x".repeat(1000)`, noBridge, Limits{MaxResultBytes: 500})
	if !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("err = %v, want ErrResultTooLarge", err)
	}
	if out := mustRun(t, `async () => "x".repeat(100)`, noBridge, Limits{MaxResultBytes: 500}); len(out) != 102 {
		t.Fatalf("len = %d", len(out))
	}
	// Default is 256KB.
	if _, err := ex.Run(context.Background(), `async () => "x".repeat(300 << 10)`, noBridge, Limits{}); !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("default cap: err = %v", err)
	}
}

// ---------------------------------------------------------------- tools proxy

func TestToolsProxy(t *testing.T) {
	var names []string
	var mu sync.Mutex
	bridge := func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		names = append(names, name+"|"+string(args))
		mu.Unlock()
		return json.RawMessage(`"ok"`), nil
	}
	out := mustRun(t, `async () => {
		await tools.github.list_issues({repo: "x"});
		await tools.fs["read-file"]({p: 1});
		await tools.call("direct.tool", 2);
		return [
			typeof tools.then, typeof tools.github.then, typeof tools.Bad, typeof tools[Symbol.iterator],
			Object.keys(tools).join(","),
		];
	}`, bridge, Limits{})
	if got := strings.Join(names, ";"); got != `github.list_issues|{"repo":"x"};fs.read-file|{"p":1};direct.tool|2` {
		t.Fatalf("bridge saw %s", got)
	}
	if string(out) != `["undefined","undefined","undefined","undefined","call"]` {
		t.Fatalf("out = %s", out)
	}
}

func TestToolsProxy_IsReadOnlyAndAwaitable(t *testing.T) {
	out := mustRun(t, `async () => {
		"use strict";
		const results = [];
		for (const attempt of [
			() => { tools.call = 1 }, () => { tools.x = 1 }, () => { delete tools.call },
			() => { tools.github.y = 1 }, () => { globalThis.tools = 1 },
		]) { try { attempt(); results.push("ok") } catch (e) { results.push("threw") } }
		await tools; await tools.github;
		return [results.join(","), typeof tools.call];
	}`, noBridge, Limits{})
	if string(out) != `["threw,threw,threw,threw,threw","function"]` {
		t.Fatalf("out = %s", out)
	}
}

// ---------------------------------------------------------------- concurrency

func TestExecutor_ConcurrentRunsFromManyGoroutines(t *testing.T) {
	const workers = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := &echoBridge{delay: 10 * time.Millisecond}
			code := fmt.Sprintf(`async () => {
				globalThis.me = %d;
				const r = await Promise.all([tools.call("t", %d), tools.github.x(%d)]);
				return [globalThis.me, r[0].args, r[1].args];
			}`, i, i, i*10)
			out, err := ex.Run(context.Background(), code, b.call, Limits{})
			if err != nil {
				errs <- fmt.Errorf("worker %d: %w", i, err)
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

func TestExecutor_ConcurrentMixedFailures(t *testing.T) {
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var code string
			var want error
			timeout := 20 * time.Second // memory bombs are slow under contention and -race
			switch i % 4 {
			case 0:
				code, want = `async () => 1`, nil
			case 1:
				code, want, timeout = `async () => { for (;;) {} }`, ErrTimeout, 400*time.Millisecond
			case 2:
				code, want = `async () => { function f() { return f() + 1 } return f() }`, ErrRuntimeFault
			default:
				code, want = `async () => { const a = []; for (;;) a.push(new ArrayBuffer(8 << 20)) }`, ErrMemoryLimit
			}
			_, err := ex.Run(context.Background(), code, noBridge, Limits{Timeout: timeout})
			if want == nil && err != nil || want != nil && !errors.Is(err, want) {
				t.Errorf("job %d: err = %v, want %v", i, err, want)
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------- executor lifecycle

func TestExecutor_SharedStateIsRefCounted(t *testing.T) {
	second, err := NewExecutor()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := second.Run(context.Background(), `async () => 1`, nil, Limits{}); err != nil || string(out) != "1" {
		t.Fatalf("%s %v", out, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Run(context.Background(), `async () => 1`, nil, Limits{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	// The suite's executor must be unaffected by the other one closing.
	if out := mustRun(t, `async () => 2`, noBridge, Limits{}); string(out) != "2" {
		t.Fatalf("out = %s", out)
	}
}

func TestExecutor_ConflictingMemoryCapIsRefused(t *testing.T) {
	if _, err := NewExecutor(WithMemoryBytes(32 << 20)); err == nil {
		t.Fatal("expected an error: the wasm cap is process-global")
	}
	if _, err := NewExecutor(WithMemoryBytes(1024)); err == nil {
		t.Fatal("expected an error for a tiny cap")
	}
}

func TestLimits_MemoryBytesCanOnlyLowerTheCap(t *testing.T) {
	lim := Limits{MemoryBytes: 1 << 40}.withDefaults(ex.memCap)
	if lim.MemoryBytes != ex.memCap {
		t.Fatalf("MemoryBytes = %d, want clamp to %d", lim.MemoryBytes, ex.memCap)
	}
	if got := (Limits{MemoryBytes: 8 << 20}).withDefaults(ex.memCap).MemoryBytes; got != 8<<20 {
		t.Fatalf("MemoryBytes = %d", got)
	}
	d := DefaultLimits()
	if d.MemoryBytes != 64<<20 || d.Timeout != 30*time.Second || d.MaxBridgeCalls != 50 || d.MaxInFlight != 8 || d.MaxResultBytes != 256<<10 {
		t.Fatalf("defaults drifted: %+v", d)
	}
}

func TestBridge_InvalidJSONBecomesCatchableError(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{not json`), nil
	}
	out := mustRun(t, `async () => { try { await tools.call("x") } catch (e) { return e.message } }`, bridge, Limits{})
	if !strings.Contains(string(out), "invalid JSON") {
		t.Fatalf("out = %s", out)
	}
}

// ---------------------------------------------------------------- MaxConcurrent

func newCappedExecutor(t *testing.T, n int, wait time.Duration) *Executor {
	t.Helper()
	e, err := NewExecutor(WithMaxConcurrent(n), withBusyWait(wait))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

// blockingRun starts a script that parks in the bridge until release is closed, and waits
// until it is really running.
func blockingRun(t *testing.T, e *Executor, release <-chan struct{}) <-chan error {
	t.Helper()
	started := make(chan struct{})
	var once sync.Once
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(`1`), nil
	}
	res := make(chan error, 1)
	go func() {
		_, err := e.Run(context.Background(), `async () => await tools.call("t", {})`, bridge, Limits{})
		res <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("blocking script never started")
	}
	return res
}

func TestMaxConcurrent_ExtraRunReturnsErrBusy(t *testing.T) {
	e := newCappedExecutor(t, 1, 50*time.Millisecond)
	release := make(chan struct{})
	first := blockingRun(t, e, release)

	start := time.Now()
	_, err := e.Run(context.Background(), `async () => 1`, nil, Limits{})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("busy answer took %v", d)
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first run: %v", err)
	}
	if out, err := e.Run(context.Background(), `async () => 2`, nil, Limits{}); err != nil || string(out) != "2" {
		t.Fatalf("slot not released: %s %v", out, err)
	}
}

func TestMaxConcurrent_WaiterGetsSlotWhenOneFreesInTime(t *testing.T) {
	e := newCappedExecutor(t, 1, 5*time.Second)
	release := make(chan struct{})
	first := blockingRun(t, e, release)
	time.AfterFunc(100*time.Millisecond, func() { close(release) })
	if out, err := e.Run(context.Background(), `async () => 3`, nil, Limits{}); err != nil || string(out) != "3" {
		t.Fatalf("waiter: %s %v", out, err)
	}
	<-first
}

func TestMaxConcurrent_WaitRespectsContext(t *testing.T) {
	e := newCappedExecutor(t, 1, 5*time.Second)
	release := make(chan struct{})
	first := blockingRun(t, e, release)
	defer func() { close(release); <-first }()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := e.Run(ctx, `async () => 1`, nil, Limits{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("wait ignored the context")
	}
}

func TestMaxConcurrent_NeverExceedsCap(t *testing.T) {
	const limit = 3
	e := newCappedExecutor(t, limit, 10*time.Second)
	b := &echoBridge{delay: 30 * time.Millisecond}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Run(context.Background(), `async () => await tools.call("t", 1)`, b.call, Limits{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p := b.peak.Load(); p > limit {
		t.Fatalf("peak concurrent scripts = %d, cap %d", p, limit)
	}
}

func TestMaxConcurrent_DefaultIsTwiceGOMAXPROCS(t *testing.T) {
	if got, want := cap(ex.slots), runtime.GOMAXPROCS(0)*2; got != want {
		t.Fatalf("default cap = %d, want %d", got, want)
	}
}
