package montyrs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

// ex is the Executor shared by the suite and the benchmarks.
var ex *Executor

// workerPath is the binary under test: $MCPGW_MONTYRS_WORKER, or a release
// build of ./worker made once by TestMain.
var workerPath string

func TestMain(m *testing.M) {
	var err error
	if workerPath, err = buildWorker(); err != nil {
		fmt.Fprintln(os.Stderr, "build worker:", err)
		os.Exit(1)
	}
	if ex, err = NewExecutor(WithWorkerPath(workerPath)); err != nil {
		fmt.Fprintln(os.Stderr, "new executor:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = ex.Close()
	os.Exit(code)
}

func buildWorker() (string, error) {
	if p := os.Getenv(WorkerPathEnv); p != "" {
		return p, allowAppContainers(p)
	}
	cmd := exec.Command("cargo", "build", "--release", "--quiet")
	cmd.Dir = "worker"
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v: %s", err, out)
	}
	return releaseBinary("montyrs-worker")
}

// releaseBinary is the absolute path of a release build of a ./worker binary.
func releaseBinary(name string) (string, error) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p, err := filepath.Abs(filepath.Join("worker", "target", "release", name))
	if err != nil {
		return "", err
	}
	return p, allowAppContainers(p)
}

// allowAppContainers lets the confined worker, an AppContainer process on
// Windows, load its own binary: what an operator grants an installed one.
func allowAppContainers(path string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if out, err := exec.Command("icacls", path, "/grant", "*S-1-15-2-1:(RX)").CombinedOutput(); err != nil {
		return fmt.Errorf("icacls %s: %v: %s", path, err, out)
	}
	return nil
}

// echoBridge answers after delay with {"tool":name,"args":args}, tracking the
// peak number of calls in flight at once.
type echoBridge struct {
	delay    time.Duration
	inflight atomic.Int32
	peak     atomic.Int32
}

func (e *echoBridge) call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	n := e.inflight.Add(1)
	defer e.inflight.Add(-1)
	for p := e.peak.Load(); n > p && !e.peak.CompareAndSwap(p, n); p = e.peak.Load() {
	}
	select {
	case <-time.After(e.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return json.RawMessage(fmt.Sprintf(`{"tool":%q,"args":%s}`, name, args)), nil
}

func mustRun(t *testing.T, code string, bridge sandbox.Bridge, lim sandbox.Limits) json.RawMessage {
	t.Helper()
	out, err := ex.Run(context.Background(), code, bridge, lim)
	if err != nil {
		t.Fatalf("Run failed: %v\ncode: %s", err, code)
	}
	return out
}

func wantErr(t *testing.T, err, target error, contains string) {
	t.Helper()
	if !errors.Is(err, target) || !strings.Contains(fmt.Sprint(err), contains) {
		t.Fatalf("err = %v, want %v containing %q", err, target, contains)
	}
}

// TestBridgeRoundTripReturnsJSON differs from package monty in one way: dict
// order is the script's insertion order, not Go's sorted map keys.
func TestBridgeRoundTripReturnsJSON(t *testing.T) {
	b := &echoBridge{}
	out := mustRun(t, `
r = call_tool("github.list_issues", {"repo": "x", "n": [1, "two", None]})
{"tool": r["tool"], "args": r["args"], "pi": 3.5, "ok": True}
`, b.call, sandbox.Limits{})
	want := `{"tool":"github.list_issues","args":{"repo":"x","n":[1,"two",null]},"pi":3.5,"ok":true}`
	if string(out) != want {
		t.Fatalf("got %s, want %s", out, want)
	}
}

func TestMissingArgsReachBridgeAsNull(t *testing.T) {
	var seen json.RawMessage
	bridge := func(_ context.Context, _ string, args json.RawMessage) (json.RawMessage, error) {
		seen = args
		return nil, nil
	}
	if out := mustRun(t, `call_tool("n")`, bridge, sandbox.Limits{}); string(out) != "null" {
		t.Fatalf("empty bridge reply should be None, got %s", out)
	}
	if string(seen) != "null" {
		t.Fatalf("missing args should arrive as null, got %s", seen)
	}
}

func TestBadCallArgumentsAreCatchableTypeErrors(t *testing.T) {
	b := &echoBridge{}
	for code, contains := range map[string]string{
		`call_tool(1)`:                  "tool name string",
		`call_tool("x", {1, 2})`:        "Object of type set is not JSON serializable",
		`call_tool("x", float("nan"))`:  "Out of range float",
		`call_tool("x", {(1, 2): "v"})`: "keys must be str",
	} {
		_, err := ex.Run(context.Background(), code, b.call, sandbox.Limits{})
		wantErr(t, err, sandbox.ErrScript, "TypeError")
		wantErr(t, err, sandbox.ErrScript, contains)
	}
	if b.peak.Load() != 0 {
		t.Fatalf("a rejected call reached the bridge")
	}
}

func TestBridgeErrorIsCatchableException(t *testing.T) {
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("upstream 503: boom")
	}
	out := mustRun(t, `
try:
    call_tool("x", {})
    r = "not caught"
except RuntimeError as e:
    r = str(e)
r
`, bridge, sandbox.Limits{})
	if string(out) != `"upstream 503: boom"` {
		t.Fatalf("got %s", out)
	}
}

func TestUncaughtBridgeErrorAndPanicAreScriptErrors(t *testing.T) {
	failing := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("denied by policy")
	}
	_, err := ex.Run(context.Background(), `call_tool("x", {})`, failing, sandbox.Limits{})
	wantErr(t, err, sandbox.ErrScript, "denied by policy")

	panicking := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { panic("kaboom") }
	_, err = ex.Run(context.Background(), `call_tool("x", {})`, panicking, sandbox.Limits{})
	wantErr(t, err, sandbox.ErrScript, "bridge panic: kaboom")
}

func TestScriptErrorsAreErrScript(t *testing.T) {
	for code, contains := range map[string]string{
		`raise ValueError("boom")`: "ValueError: boom",
		`1 / 0`:                    "ZeroDivisionError",
		`undefined_name`:           "NameError",
		`def (`:                    "",
	} {
		_, err := ex.Run(context.Background(), code, nil, sandbox.Limits{})
		wantErr(t, err, sandbox.ErrScript, contains)
	}
}

func TestBusyLoopIsInterruptedAtDeadline(t *testing.T) {
	for _, code := range []string{
		"while True:\n    pass",
		"try:\n    while True:\n        pass\nexcept BaseException:\n    1",
	} {
		start := time.Now()
		_, err := ex.Run(context.Background(), code, nil, sandbox.Limits{Timeout: 100 * time.Millisecond})
		wantErr(t, err, sandbox.ErrTimeout, "")
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("returned after %v, want close to 100ms", elapsed)
		}
	}
}

func TestBusyLoopAfterABridgeWaitStopsAtTheWallDeadline(t *testing.T) {
	b := &echoBridge{delay: 80 * time.Millisecond}
	start := time.Now()
	_, err := ex.Run(context.Background(), "call_tool('x')\nwhile True:\n    pass", b.call, sandbox.Limits{Timeout: 150 * time.Millisecond})
	wantErr(t, err, sandbox.ErrTimeout, "")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %v, want close to 150ms", elapsed)
	}
}

func TestTimeoutWhileWaitingOnBridgeThatIgnoresContext(t *testing.T) {
	stuck := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		time.Sleep(2 * time.Second)
		return nil, nil
	}
	for _, code := range []string{`call_tool("slow")`, `await call_tool_async("slow")`} {
		start := time.Now()
		_, err := ex.Run(context.Background(), code, stuck, sandbox.Limits{Timeout: 100 * time.Millisecond})
		wantErr(t, err, sandbox.ErrTimeout, "")
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("%s: returned after %v, want close to 100ms", code, elapsed)
		}
	}
}

func TestTimeoutCancelsTheBridgeContext(t *testing.T) {
	cancelled := make(chan struct{})
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	_, err := ex.Run(context.Background(), `call_tool("slow")`, bridge, sandbox.Limits{Timeout: 50 * time.Millisecond})
	wantErr(t, err, sandbox.ErrTimeout, "")
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("bridge context was not cancelled")
	}
}

func TestParentCancellationStopsTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := ex.Run(ctx, "while True:\n    pass", nil, sandbox.Limits{Timeout: 5 * time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestMemoryLimitStopsAllocationBombsAndCannotBeCaught(t *testing.T) {
	for _, code := range []string{
		"x = []\nwhile True:\n    x.append('a' * 100000)",
		"s = 'a'\nfor i in range(40):\n    s = s + s",
		"2 ** (10 ** 8)",
		"try:\n    x = 'a' * (200 * 1024 * 1024)\nexcept MemoryError:\n    x = 'caught'\nx",
	} {
		_, err := ex.Run(context.Background(), code, nil, sandbox.Limits{MemoryBytes: 16 << 20})
		wantErr(t, err, sandbox.ErrMemoryLimit, "MemoryError")
	}
}

func TestMaxBridgeCallsRaisesCatchableThenUncaughtError(t *testing.T) {
	b := &echoBridge{}
	out := mustRun(t, `
calls = 0
try:
    for i in range(10):
        call_tool("x", i)
        calls += 1
except RuntimeError as e:
    msg = str(e)
[calls, msg]
`, b.call, sandbox.Limits{MaxBridgeCalls: 3})
	if !strings.HasPrefix(string(out), `[3,"sandbox: too many bridge calls`) {
		t.Fatalf("got %s", out)
	}
	_, err := ex.Run(context.Background(), "for i in range(10):\n    call_tool('x', i)", b.call, sandbox.Limits{MaxBridgeCalls: 3})
	wantErr(t, err, sandbox.ErrTooManyCalls, "max 3 per execution")
}

func TestEndlessFailingCallLoopHitsTheSuspensionBudget(t *testing.T) {
	b := &echoBridge{}
	_, err := ex.Run(context.Background(), `
while True:
    try:
        call_tool("x")
    except RuntimeError as e:
        last_error = e  # the fixture retries forever on purpose: the host budget must stop it
`, b.call, sandbox.Limits{MaxBridgeCalls: 3})
	wantErr(t, err, sandbox.ErrScript, "too many host suspensions")
}

func TestMaxResultBytes(t *testing.T) {
	_, err := ex.Run(context.Background(), `"x" * 2000`, nil, sandbox.Limits{MaxResultBytes: 1000})
	wantErr(t, err, sandbox.ErrResultTooLarge, "max 1000")
	mustRun(t, `"x" * 900`, nil, sandbox.Limits{MaxResultBytes: 1000})
}

func TestCallArgsAreCappedBelowTheMemoryLimit(t *testing.T) {
	var calls atomic.Int32
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return nil, nil
	}
	code := "b = 'y' * 2_000_000\ntry:\n    call_tool('a.b', b)\nexcept TypeError as e:\n    r = str(e)\nr"
	out := mustRun(t, code, bridge, sandbox.Limits{})
	if !strings.Contains(string(out), "args too large") || calls.Load() != 0 {
		t.Fatalf("out = %s, bridge calls = %d; want a TypeError and no call", out, calls.Load())
	}
}

func TestHugeErrorMessagesAreClipped(t *testing.T) {
	for _, char := range []string{"x", "\u00e9"} {
		_, err := ex.Run(context.Background(), "raise ValueError('"+char+"' * 10_000_000 + 'END')", nil, sandbox.Limits{})
		wantErr(t, err, sandbox.ErrScript, "bytes truncated")
		msg := err.Error()
		if len(msg) > maxErrorBytes+200 || !utf8.ValidString(msg) || !strings.HasSuffix(msg, "END") {
			t.Fatalf("%q: error is %d bytes (valid UTF-8 %v, ends %q)", char, len(msg), utf8.ValidString(msg), msg[max(0, len(msg)-10):])
		}
	}
}

func TestClipMiddleKeepsUTF8(t *testing.T) {
	s := strings.Repeat("é", 5000)
	got := clipMiddle(s, 101)
	if !utf8.ValidString(got) || len(got) > 200 {
		t.Fatalf("clipMiddle = %q", got)
	}
	if clipMiddle("short", 101) != "short" {
		t.Fatal("short text was changed")
	}
}

func TestEscapeAttemptsAreDenied(t *testing.T) {
	for code, contains := range map[string]string{
		`open("/etc/passwd").read()`:                                   "PermissionError",
		"from pathlib import Path\nPath('/etc/passwd').read_text()":    "PermissionError",
		"from pathlib import Path\nPath('/tmp/x').write_text('pwned')": "PermissionError",
		"import os\nos.getenv('HOME')":                                 "PermissionError",
		"import os\nos.environ":                                        "PermissionError",
		"import subprocess":                                            "ModuleNotFoundError",
		"import socket":                                                "ModuleNotFoundError",
		"import ctypes":                                                "ModuleNotFoundError",
		`__import__("os")`:                                             "NameError",
		// Monty 1.4 ships eval/exec; they run in the same sandbox, so they
		// reach nothing the script itself could not.
		`eval("__import__('os')")`:                   "NameError",
		`exec("import subprocess")`:                  "ModuleNotFoundError",
		`eval("open('/etc/passwd').read()")`:         "PermissionError",
		`compile("1", "f", "eval")`:                  "NameError",
		`globals()`:                                  "NameError",
		`().__class__.__bases__[0].__subclasses__()`: "",
	} {
		out, err := ex.Run(context.Background(), code, nil, sandbox.Limits{})
		if !errors.Is(err, sandbox.ErrScript) || !strings.Contains(err.Error(), contains) {
			t.Errorf("%q: out=%s err=%v, want ErrScript containing %q", code, out, err, contains)
		}
	}
}

func TestRecursionBombIsContained(t *testing.T) {
	_, err := ex.Run(context.Background(), "def f(n):\n    return f(n + 1)\nf(0)", nil, sandbox.Limits{})
	wantErr(t, err, sandbox.ErrScript, "RecursionError")
}

func TestGlobalsDoNotLeakBetweenExecutions(t *testing.T) {
	// One worker, so the second run is guaranteed to reuse the first's process.
	e := newExecutor(t, WithMaxConcurrent(1))
	if _, err := e.Run(context.Background(), "leaked = 42\nleaked", nil, sandbox.Limits{}); err != nil {
		t.Fatal(err)
	}
	_, err := e.Run(context.Background(), "leaked", nil, sandbox.Limits{})
	wantErr(t, err, sandbox.ErrScript, "NameError")
}

func TestConcurrentRunsAreIndependent(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := &echoBridge{delay: 20 * time.Millisecond}
			code := fmt.Sprintf(`
import asyncio
me = %d
r = await asyncio.gather(call_tool_async("t", %d), call_tool_async("t", %d))
[me, r[0]["args"], r[1]["args"]]
`, i, i, i*10)
			out, err := ex.Run(context.Background(), code, b.call, sandbox.Limits{})
			if want := fmt.Sprintf("[%d,%d,%d]", i, i, i*10); err == nil && string(out) != want {
				err = fmt.Errorf("run %d got %s, want %s", i, out, want)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestClosedExecutorRefusesRuns(t *testing.T) {
	e, err := NewExecutor(WithWorkerPath(workerPath), WithMaxConcurrent(1))
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Close()
	if _, err := e.Run(context.Background(), "1", nil, sandbox.Limits{}); !errors.Is(err, sandbox.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func newExecutor(t *testing.T, opts ...Option) *Executor {
	t.Helper()
	e, err := NewExecutor(append([]Option{WithWorkerPath(workerPath)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}
