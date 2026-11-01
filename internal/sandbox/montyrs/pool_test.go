package montyrs

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

func liveWorkers(e *Executor) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for w := range e.children {
		if !w.isDead() {
			n++
		}
	}
	return n
}

func waitForWorkers(t *testing.T, e *Executor, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for liveWorkers(e) != want {
		if time.Now().After(deadline) {
			t.Fatalf("live workers = %d, want %d", liveWorkers(e), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPoolStartsAtMinAndGrowsUnderLoadUpToTheCap(t *testing.T) {
	const cap = 4
	e := newExecutor(t, WithMaxConcurrent(cap), WithMinIdle(1), WithIdleTimeout(time.Hour))
	if got := liveWorkers(e); got != 1 {
		t.Fatalf("workers at start = %d, want 1", got)
	}
	var started sync.WaitGroup
	started.Add(cap)
	var inFlight atomic.Int32
	release := make(chan struct{})
	bridge := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		if inFlight.Add(1) <= cap {
			started.Done()
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(`1`), nil
	}
	var wg sync.WaitGroup
	for range cap + 2 { // two more than the cap must wait, not spawn
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Run(context.Background(), `call_tool("x")`, bridge, sandbox.Limits{}); err != nil {
				t.Errorf("run: %v", err)
			}
		}()
	}
	started.Wait() // cap runs are in flight at the same time
	if got := liveWorkers(e); got != cap {
		t.Errorf("workers under load = %d, want %d", got, cap)
	}
	close(release)
	wg.Wait()
	if got := liveWorkers(e); got != cap {
		t.Errorf("workers after load with a long idle timeout = %d, want %d", got, cap)
	}
}

func TestPoolShrinksToMinAfterTheIdleTimeout(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(4), WithMinIdle(1), WithIdleTimeout(100*time.Millisecond))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mustRunOn(t, e, "x = 0\nfor i in range(200000):\n    x += i\nx")
		}()
	}
	wg.Wait()
	waitForWorkers(t, e, 1)
	mustRunOn(t, e, `1`) // the surviving worker still serves
}

func TestCloseStopsWorkersAndReaper(t *testing.T) {
	before := runtime.NumGoroutine()
	e, err := NewExecutor(WithWorkerPath(workerPath), WithMaxConcurrent(3), WithMinIdle(3), WithIdleTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	var procs []*worker
	e.mu.Lock()
	for w := range e.children {
		procs = append(procs, w)
	}
	e.mu.Unlock()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	for _, w := range procs {
		select {
		case <-w.exited:
		case <-time.After(5 * time.Second):
			t.Fatal("worker still alive after Close")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+1 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines %d -> %d after Close", before, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHeavyRunRecyclesTheWorkerSoRSSDropsToFresh(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1), WithMinIdle(1))
	first := liveWorker(t, e)
	fresh := workerRSSMB(t, first)
	heavy := "a = []\nfor i in range(60000):\n    a.append({\"i\": i, \"s\": \"x\" * 20})\nlen(a)"
	mustRunOn(t, e, heavy)
	waitForWorkers(t, e, 0) // the heavy worker is gone; the next run spawns a fresh one
	mustRunOn(t, e, `1`)
	next := liveWorker(t, e)
	after := workerRSSMB(t, next)
	t.Logf("worker RSS: fresh %d MB, after a heavy run %d MB", fresh, after)
	if next == first {
		t.Error("worker was not recycled after a heavy run")
	}
	if after > fresh+4 {
		t.Errorf("worker RSS %d MB is not near its fresh size (%d MB)", after, fresh)
	}
}

func TestLightRunsKeepTheirWorker(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1), WithMinIdle(1))
	first := liveWorker(t, e)
	for range 20 {
		mustRunOn(t, e, "x = [i for i in range(1000)]\nlen(x)")
	}
	if liveWorker(t, e) != first {
		t.Error("worker was recycled by light runs")
	}
}

func TestWorkersAreNeverSharedAcrossOwners(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1), WithMinIdle(1))
	run := func(owner string) *worker {
		t.Helper()
		if _, err := e.Run(context.Background(), `1`, nil, sandbox.Limits{Owner: owner}); err != nil {
			t.Fatal(err)
		}
		return liveWorker(t, e)
	}
	alice := run("alice")
	if again := run("alice"); again != alice {
		t.Error("an owner's idle worker was not reused for the same owner")
	}
	bob := run("bob")
	if bob == alice {
		t.Fatal("bob's run reused alice's worker")
	}
	if !alice.isDead() {
		t.Error("alice's idle worker was not stopped to make room at the cap")
	}
	if again := run("alice"); again == bob {
		t.Error("alice's run reused bob's worker")
	}
}

func TestFrameBufferedWhileIdleKillsTheWorker(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1), WithMinIdle(1))
	w := liveWorker(t, e)
	w.msgs <- message{Type: "call", ID: 1, Name: "a.b", Args: "{}"} // as if the worker queued it after "done"
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Error("a stray frame reached the bridge")
		return nil, nil
	}
	if _, err := e.Run(context.Background(), `1`, bridge, sandbox.Limits{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker with a buffered frame is still alive")
	}
	if liveWorker(t, e) == w {
		t.Fatal("the worker with a buffered frame served the run")
	}
}
