package montyrs

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

// Same budget and run count as the other engines' retention tests.
const (
	retentionRuns     = 60
	retentionBudgetMB = 12
)

// liveWorker returns the one live process of a single-worker Executor.
func liveWorker(t *testing.T, e *Executor) *worker {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for w := range e.children {
		if !w.isDead() {
			return w
		}
	}
	t.Fatal("no live worker")
	return nil
}

// servingWorker returns the worker running the current Run: the only live
// child that is not idle. Call it from a bridge, while the run waits on it.
func servingWorker(t *testing.T, e *Executor) *worker {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	var serving []*worker
	for w := range e.children {
		isIdle := slices.ContainsFunc(e.idle, func(iw idleWorker) bool { return iw.w == w })
		if !isIdle && !w.isDead() {
			serving = append(serving, w)
		}
	}
	if len(serving) != 1 {
		t.Fatalf("%d busy workers, want 1", len(serving))
	}
	return serving[0]
}

func signalWorker(t *testing.T, w *worker, sig syscall.Signal) {
	t.Helper()
	if err := w.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

// killedState is how ProcessState describes a worker stopped by Process.Kill;
// on Windows that is TerminateProcess with exit status 1.
func killedState() string {
	if runtime.GOOS == "windows" {
		return "exit status 1"
	}
	return "killed"
}

func mustRunOn(t *testing.T, e *Executor, code string) {
	t.Helper()
	if _, err := e.Run(context.Background(), code, nil, sandbox.Limits{}); err != nil {
		t.Fatalf("follow-up run failed: %v", err)
	}
}

func TestWorkerKilledMidRunFailsOnlyThatRun(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1))
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		signalWorker(t, servingWorker(t, e), syscall.SIGKILL)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := e.Run(context.Background(), `call_tool("x")`, bridge, sandbox.Limits{Timeout: 5 * time.Second})
	wantErr(t, err, sandbox.ErrRuntimeFault, killedState())
	mustRunOn(t, e, `1`)
}

func TestHugeBridgeReplyHitsTheHardMemoryCeiling(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1))
	huge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"` + strings.Repeat("a", 64<<20) + `"`), nil
	}
	_, err := e.Run(context.Background(), `call_tool("x")`, huge, sandbox.Limits{MemoryBytes: 8 << 20})
	wantErr(t, err, sandbox.ErrMemoryLimit, "exit status 65")
	mustRunOn(t, e, `1`)
}

func TestWorkerDeadWhileIdleIsReplacedTransparently(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1))
	signalWorker(t, liveWorker(t, e), syscall.SIGKILL)
	mustRunOn(t, e, `1`)
}

func TestKilledWorkersDoNotLeakGoroutinesOrHeap(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1))
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		signalWorker(t, servingWorker(t, e), syscall.SIGKILL)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	run := func() {
		_, _ = e.Run(context.Background(), `call_tool("x")`, bridge, sandbox.Limits{Timeout: 5 * time.Second})
		mustRunOn(t, e, `1`) // waits for the replacement worker
	}
	run()
	heap, goroutines := liveHeapMB(), runtime.NumGoroutine()
	for range retentionRuns / 2 {
		run()
	}
	time.Sleep(100 * time.Millisecond)
	heapEnd, goroutinesEnd := liveHeapMB(), runtime.NumGoroutine()
	t.Logf("Go heap %d -> %d MB, goroutines %d -> %d over %d killed workers", heap, heapEnd, goroutines, goroutinesEnd, retentionRuns/2)
	if heapEnd > heap+retentionBudgetMB || goroutinesEnd > goroutines+3 {
		t.Errorf("killed workers leaked memory or goroutines")
	}
}

func liveHeapMB() uint64 {
	debug.FreeOSMemory()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc >> 20
}

func workerRSSMB(t *testing.T, w *worker) uint64 {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the gateway's child is the launcher, not the confined worker whose RSS matters")
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(w.cmd.Process.Pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	kb, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatalf("ps output %q: %v", out, err)
	}
	return kb >> 10
}
