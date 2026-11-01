// Package montyrs is a proof-of-concept alternative to package sandbox: it runs
// model-written Python in Pydantic's Monty interpreter, linked natively (the
// `monty` crate) into a pool of long-lived Rust worker processes (./worker).
// It mirrors package rustqjs on the Go side and exists to compare against the
// QuickJS engines and the wasm Monty binding (package monty); nothing in
// production uses it.
//
// Script contract: the code is a Python module; the value of its last
// expression is the result, returned as JSON. Capabilities:
//
//	call_tool("server.tool", {"x": 1})              # blocks, returns the decoded reply
//	await call_tool_async("server.tool", {"x": 1})  # awaitable; asyncio.gather overlaps calls
//
// A failed bridge call raises RuntimeError, which the script can catch. Any OS
// access (open, os.getenv, pathlib) raises a catchable PermissionError; imports
// of modules Monty does not ship raise ModuleNotFoundError.
//
// Differences from package monty (the wasm binding), all from driving Monty
// directly instead of through monty-go:
//   - call_tool_async hands back a pending future, so gathered calls really run
//     in parallel, up to Limits.MaxInFlight (extra calls queue FIFO);
//   - denied OS calls are Python exceptions, not an aborted run;
//   - results keep exact integers of any size and dict order; values JSON
//     cannot hold (set, bytes, NaN) fail with ErrNotSerializable like CPython's
//     json.dumps instead of being coerced.
//
// Isolation model:
//   - every Run gets a fresh MontyRun heap inside a worker, dropped before the
//     worker reports "done"; workers are reused across runs of the same
//     Limits.Owner only, so a worker compromised by one owner's script never
//     sees another owner's code or tool replies;
//   - Monty enforces the memory limit (MemoryError, counted by the monty-alloc
//     global allocator), recursion depth and CPU time; monty-alloc also kills
//     the worker (exit 65) past a hard ceiling a little above the limit;
//   - the gateway owns the wall-clock deadline: past it, it asks the worker to
//     abort and kills the process if it does not answer within abortGrace
//     (Monty cannot be interrupted from another thread, so a busy loop that
//     has not hit its own time budget yet costs a respawn);
//   - a worker crash, kill or hang fails only the Run it was serving; the
//     pool spawns a replacement.
package montyrs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

// WorkerPathEnv names the environment variable that locates the worker binary
// when WithWorkerPath is not used.
const WorkerPathEnv = "MCPGW_MONTYRS_WORKER"

const (
	defaultBusyWait    = 2 * time.Second
	defaultMinIdle     = 2
	defaultIdleTimeout = 30 * time.Second
	// abortGrace is how long a worker may take to acknowledge an abort before
	// it is killed.
	abortGrace = 250 * time.Millisecond
	// bridgeDrainGrace matches the wasm engine: after the script ends, wait
	// this long for cancelled bridge calls to return.
	bridgeDrainGrace = 100 * time.Millisecond
	// maxCallArgsBytes caps the JSON arguments of one tool call (the worker
	// raises TypeError past it; a bigger call frame kills the worker).
	maxCallArgsBytes = 1 << 20
	// maxErrorBytes caps the error text of a failed run (head and tail kept).
	maxErrorBytes = 4 << 10
	// A frame carries its payload as a JSON string: escaping can grow it up
	// to 6x (\u00XX), plus room for the other fields.
	jsonEscapeFactor   = 6
	frameOverheadBytes = 64 << 10
)

// maxFrameBytes bounds any frame a worker may send in a run whose result is
// capped at maxResultBytes.
func maxFrameBytes(maxResultBytes int) int64 {
	return int64(jsonEscapeFactor*max(maxCallArgsBytes, maxErrorBytes, maxResultBytes) + frameOverheadBytes)
}

// Option configures NewExecutor.
type Option func(*config)

type config struct {
	workerPath    string
	memoryBytes   int64
	maxConcurrent int
	minIdle       int
	idleTimeout   time.Duration
	busyWait      time.Duration
}

// WithWorkerPath sets the worker binary (default: $MCPGW_MONTYRS_WORKER).
func WithWorkerPath(p string) Option { return func(c *config) { c.workerPath = p } }

// WithMaxConcurrent caps how many workers exist, and so how many runs execute
// at once (default runtime.GOMAXPROCS(0)*2, the same default as package sandbox).
// Workers are started on demand, up to this cap.
func WithMaxConcurrent(n int) Option { return func(c *config) { c.maxConcurrent = n } }

// WithMinIdle sets how many workers stay alive when the pool is idle (default
// 2, never more than the cap). The pool starts with this many.
func WithMinIdle(n int) Option { return func(c *config) { c.minIdle = n } }

// WithIdleTimeout sets how long a worker beyond the minimum may sit idle
// before it is stopped (default 30s).
func WithIdleTimeout(d time.Duration) Option { return func(c *config) { c.idleTimeout = d } }

// WithMemoryBytes sets the per-run memory cap (default 64 MB). Limits.MemoryBytes
// can only lower it.
func WithMemoryBytes(n int64) Option { return func(c *config) { c.memoryBytes = n } }

func withBusyWait(d time.Duration) Option { return func(c *config) { c.busyWait = d } }

// Executor runs scripts on an elastic pool of worker processes. It is safe for
// concurrent use.
type Executor struct {
	cfg    config
	closed atomic.Bool

	seats    chan struct{} // one token per running script; capacity is the cap
	mu       sync.Mutex
	idle     []idleWorker         // oldest first; the newest is reused first
	children map[*worker]struct{} // every live process, for Close and tests
	stop     chan struct{}        // closed by Close to end the reaper
	reaped   chan struct{}        // closed when the reaper has returned
}

// idleWorker is a worker waiting for a run, and since when.
type idleWorker struct {
	w     *worker
	since time.Time
}

// NewExecutor starts the minimum number of workers and checks the pool with a
// warm-up run.
func NewExecutor(opts ...Option) (*Executor, error) {
	cfg := config{workerPath: os.Getenv(WorkerPathEnv), memoryBytes: sandbox.DefaultLimits().MemoryBytes, busyWait: defaultBusyWait, minIdle: defaultMinIdle}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.workerPath == "" {
		return nil, fmt.Errorf("montyrs: worker binary not set (use WithWorkerPath or $%s)", WorkerPathEnv)
	}
	if cfg.maxConcurrent <= 0 {
		cfg.maxConcurrent = runtime.GOMAXPROCS(0) * 2
	}
	if cfg.idleTimeout <= 0 {
		cfg.idleTimeout = defaultIdleTimeout
	}
	cfg.minIdle = max(0, min(cfg.minIdle, cfg.maxConcurrent))
	if cfg.memoryBytes < 1<<20 {
		return nil, fmt.Errorf("montyrs: memory cap %d is below 1 MiB", cfg.memoryBytes)
	}
	e := &Executor{
		cfg: cfg, seats: make(chan struct{}, cfg.maxConcurrent), children: map[*worker]struct{}{},
		stop: make(chan struct{}), reaped: make(chan struct{}),
	}
	for range cfg.minIdle {
		w, err := e.spawn()
		if err != nil {
			e.shutdown()
			return nil, fmt.Errorf("montyrs: %w", err)
		}
		e.idle = append(e.idle, idleWorker{w, time.Now()})
	}
	go e.reapLoop()
	if _, err := e.Run(context.Background(), `1`, nil, sandbox.Limits{Timeout: 30 * time.Second}); err != nil {
		_ = e.Close()
		return nil, fmt.Errorf("montyrs: warm up: %w", err)
	}
	return e, nil
}

// Close kills every worker and stops the reaper. Do not call it while Runs are
// in flight.
func (e *Executor) Close() error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	e.shutdown()
	<-e.reaped
	return nil
}

// shutdown stops the reaper (if started) and kills every process.
func (e *Executor) shutdown() {
	e.closed.Store(true)
	close(e.stop)
	e.mu.Lock()
	for w := range e.children {
		w.kill()
		delete(e.children, w)
	}
	e.idle = nil
	e.mu.Unlock()
}

func (e *Executor) spawn() (*worker, error) {
	w, err := spawnWorker(e.cfg.workerPath, e.cfg.memoryBytes)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed.Load() {
		w.kill()
		return nil, sandbox.ErrClosed
	}
	e.children[w] = struct{}{}
	go func() {
		<-w.exited
		e.mu.Lock()
		delete(e.children, w)
		e.mu.Unlock()
	}()
	return w, nil
}

// reapLoop stops workers that have been idle past the timeout, keeping at
// least minIdle processes alive.
func (e *Executor) reapLoop() {
	defer close(e.reaped)
	tick := time.NewTicker(max(e.cfg.idleTimeout/4, 5*time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-e.stop:
			return
		case now := <-tick.C:
			e.reap(now)
		}
	}
}

func (e *Executor) reap(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.idle) > 0 && len(e.children) > e.cfg.minIdle && now.Sub(e.idle[0].since) >= e.cfg.idleTimeout {
		w := e.idle[0].w
		e.idle = e.idle[1:]
		w.kill()
		delete(e.children, w)
	}
}

// acquire takes a seat, waiting at most busyWait, and returns an idle worker
// of owner or spawns one. The wait does not count against the script timeout.
func (e *Executor) acquire(ctx context.Context, owner string) (*worker, error) {
	select {
	case e.seats <- struct{}{}:
	default:
		timer := time.NewTimer(e.cfg.busyWait)
		defer timer.Stop()
		select {
		case e.seats <- struct{}{}:
		case <-ctx.Done():
			return nil, timeoutError(ctx)
		case <-timer.C:
			return nil, sandbox.ErrBusy
		}
	}
	if w := e.popIdle(owner); w != nil {
		return w, nil
	}
	w, err := e.spawn()
	if err != nil {
		<-e.seats
		return nil, fmt.Errorf("%w: spawn worker: %v", sandbox.ErrRuntimeFault, err)
	}
	w.claimed, w.owner = true, owner // not yet visible to other goroutines
	return w, nil
}

// popIdle returns the most recently used live idle worker of owner, or else a
// fresh one no run has used yet. Workers never cross owners: when the pool is
// full of other owners' idle workers, the oldest is stopped so the caller can
// spawn one.
func (e *Executor) popIdle(owner string) *worker {
	e.mu.Lock()
	defer e.mu.Unlock()
	// The worker sends nothing between runs: a frame buffered while idle is a
	// compromised worker trying to feed the next session, so it is killed.
	e.idle = slices.DeleteFunc(e.idle, func(iw idleWorker) bool {
		if len(iw.w.msgs) > 0 {
			iw.w.kill()
			delete(e.children, iw.w)
			return true
		}
		return iw.w.isDead()
	})
	if i := e.newestIdle(func(w *worker) bool { return w.claimed && w.owner == owner }); i >= 0 {
		return e.takeIdle(i, owner)
	}
	if i := e.newestIdle(func(w *worker) bool { return !w.claimed }); i >= 0 {
		return e.takeIdle(i, owner)
	}
	if len(e.idle) > 0 && len(e.children) >= e.cfg.maxConcurrent {
		oldest := e.idle[0].w
		e.idle = e.idle[1:]
		oldest.kill()
		delete(e.children, oldest)
	}
	return nil
}

func (e *Executor) newestIdle(match func(*worker) bool) int {
	for i := len(e.idle) - 1; i >= 0; i-- {
		if match(e.idle[i].w) {
			return i
		}
	}
	return -1
}

func (e *Executor) takeIdle(i int, owner string) *worker {
	w := e.idle[i].w
	e.idle = slices.Delete(e.idle, i, i+1)
	w.claimed, w.owner = true, owner
	return w
}

// release frees the seat. A healthy worker goes back to the idle list; a broken
// one is killed and the next Run that needs a worker spawns another.
func (e *Executor) release(w *worker, healthy bool) {
	defer func() { <-e.seats }()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !healthy || e.closed.Load() {
		w.kill()
		delete(e.children, w)
		return
	}
	e.idle = append(e.idle, idleWorker{w, time.Now()})
}

// Run executes code in a fresh Monty heap and returns its result as JSON.
func (e *Executor) Run(parent context.Context, code string, bridge sandbox.Bridge, lim sandbox.Limits) (json.RawMessage, error) {
	if e.closed.Load() {
		return nil, sandbox.ErrClosed
	}
	if bridge == nil {
		bridge = noBridge
	}
	lim = withDefaults(lim, e.cfg.memoryBytes)
	ctx, cancel := context.WithTimeout(parent, lim.Timeout)
	defer cancel()
	// A worker that died before this run produced any call had no side
	// effects, so the run is retried once on a fresh worker.
	for attempt := 0; ; attempt++ {
		w, err := e.acquire(parent, lim.Owner)
		if err != nil {
			return nil, err
		}
		s := newSession(ctx, w, bridge, lim)
		out, err := s.run(code)
		e.release(w, s.healthy)
		if s.diedEarly && attempt == 0 {
			continue
		}
		return out, err
	}
}

func withDefaults(l sandbox.Limits, memCap int64) sandbox.Limits {
	d := sandbox.DefaultLimits()
	if l.Timeout <= 0 {
		l.Timeout = d.Timeout
	}
	if l.MaxBridgeCalls <= 0 {
		l.MaxBridgeCalls = d.MaxBridgeCalls
	}
	if l.MaxInFlight <= 0 {
		l.MaxInFlight = d.MaxInFlight
	}
	if l.MaxResultBytes <= 0 {
		l.MaxResultBytes = d.MaxResultBytes
	}
	if l.MemoryBytes <= 0 || l.MemoryBytes > memCap {
		l.MemoryBytes = memCap
	}
	return l
}

func noBridge(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("tools are not available")
}

func timeoutError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w (%v)", sandbox.ErrTimeout, ctx.Err())
	}
	return ctx.Err()
}
