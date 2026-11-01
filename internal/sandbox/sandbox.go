// Package sandbox runs untrusted, model-written JavaScript in QuickJS compiled
// to WebAssembly (github.com/fastschema/qjs on wazero, no cgo). The only
// capability handed to the script is tools.call(name, args), which goes back to
// a Go Bridge.
//
// Script contract: the code is a function EXPRESSION, normally
//
//	async () => { const r = await tools.call("server.tool", {x: 1}); return r; }
//
// It is evaluated, called with no arguments, awaited, and the resolved value is
// returned as JSON. A trailing ";" is tolerated. tools.<server>.<tool>(args) is
// sugar for tools.call("<server>.<tool>", args).
//
// Hardening (each item was proven necessary in an earlier sandbox spike):
//   - a fresh QuickJS instance per Run, never reused, never pooled;
//   - the wasm module's memory section is patched to declare a maximum, which is
//     the only memory limit that actually bounds a guest in this build;
//   - the async bridge is driven by one goroutine that owns the instance: the
//     goroutine-resolves-the-promise pattern from the qjs README corrupts the heap;
//   - std/os/bjson/timer globals are deleted, eval and the Function constructors
//     are disabled, and any source containing the "import" token is refused;
//   - the WASI root is an empty, read-only directory, with no env, args or sockets.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "embed"

	"github.com/fastschema/qjs"
)

//go:embed prelude.js
var preludeSource string

//go:embed qjs.wasm
var qjsWasm []byte // byte-identical copy of the module embedded in fastschema/qjs v0.0.6

// Bridge is the only way out of the sandbox. Each call runs on its own
// goroutine, so several calls made by one script (Promise.all) can execute
// concurrently, up to Limits.MaxInFlight. The context is cancelled when the
// script ends, times out, or the caller cancels, whichever comes first.
type Bridge func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error)

// Limits bounds one execution. Zero or negative fields fall back to DefaultLimits.
type Limits struct {
	// MemoryBytes can only lower the QuickJS-level limit below the Executor's
	// wasm cap (WithMemoryBytes); the cap itself is process-wide and is what
	// actually bounds the guest.
	MemoryBytes int64
	// Timeout is the wall clock for the whole execution, bridge waits included.
	Timeout time.Duration
	// MaxBridgeCalls caps tools.call invocations per execution (queued ones
	// count). Calls beyond it reject with a catchable error.
	MaxBridgeCalls int
	// MaxInFlight caps bridge calls running at once. Extra calls wait in a FIFO
	// queue inside the sandbox until a slot frees up (they are not rejected).
	MaxInFlight int
	// MaxResultBytes caps the JSON-encoded return value.
	MaxResultBytes int
}

// DefaultLimits returns the production defaults.
func DefaultLimits() Limits {
	return Limits{
		MemoryBytes:    64 << 20,
		Timeout:        30 * time.Second,
		MaxBridgeCalls: 50,
		MaxInFlight:    8,
		MaxResultBytes: 256 << 10,
	}
}

func (l Limits) withDefaults(memCap int64) Limits {
	d := DefaultLimits()
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

var (
	ErrTimeout         = errors.New("sandbox: timed out")
	ErrMemoryLimit     = errors.New("sandbox: memory limit exceeded")
	ErrScript          = errors.New("sandbox: script error")
	ErrRuntimeFault    = errors.New("sandbox: wasm runtime fault")
	ErrTooManyCalls    = errors.New("sandbox: too many bridge calls")
	ErrResultTooLarge  = errors.New("sandbox: result too large")
	ErrForbiddenSyntax = errors.New("sandbox: forbidden syntax")
	ErrNotSerializable = errors.New("sandbox: result is not JSON-serializable")
	ErrNeverSettles    = errors.New("sandbox: script never settled (awaiting a promise nothing can resolve)")
	ErrClosed          = errors.New("sandbox: executor is closed")
	// ErrBusy means the Executor is running its maximum number of scripts and none finished
	// within the short wait. It is safe to retry later.
	ErrBusy = errors.New("sandbox: busy, too many scripts running")
)

// defaultBusyWait is how long Run waits for a free slot before returning ErrBusy.
const defaultBusyWait = 2 * time.Second

// bridgeDrainGrace is how long Run waits, after the script ends, for cancelled
// bridge calls to return. A bridge that ignores its context is abandoned after
// this; its goroutine ends when it returns and its result is dropped.
const bridgeDrainGrace = 100 * time.Millisecond

// importToken matches the `import` keyword anywhere in the source, strings and
// comments included (false positives are accepted; a missed token would expose
// the qjs:std / qjs:os modules, which stay registered in the engine). Keywords
// cannot be spelled with unicode escapes, so this is sufficient for syntax.
var importToken = regexp.MustCompile(`\bimport\b`)

// Option configures NewExecutor.
type Option func(*config)

type config struct {
	memoryBytes   int64
	maxConcurrent int
	busyWait      time.Duration
}

// WithMaxConcurrent caps how many scripts run at once in this Executor (default
// runtime.GOMAXPROCS(0)*2). Every Run is a fresh wasm instance of tens of MB, so the cap
// bounds memory and CPU. A Run that finds no free slot waits briefly, then fails with ErrBusy.
func WithMaxConcurrent(n int) Option { return func(c *config) { c.maxConcurrent = n } }

// withBusyWait overrides the slot wait; tests only.
func withBusyWait(d time.Duration) Option { return func(c *config) { c.busyWait = d } }

// WithMemoryBytes sets the wasm linear-memory cap (default 64 MB). The cap is
// baked into the compiled module, which qjs caches process-wide, so every live
// Executor in the process must use the same value.
func WithMemoryBytes(n int64) Option { return func(c *config) { c.memoryBytes = n } }

// runtimeOverheadBytes is wasm memory used by the engine itself (prelude,
// std/os modules), added on top of the cap so scripts get all of it.
const runtimeOverheadBytes = 8 << 20

// shared is the process-global state that qjs forces on us: one compiled
// module, one wazero config, one patched wasm, one mounted directory.
var shared struct {
	mu       sync.Mutex
	refs     int
	memBytes int64
	wasm     []byte
	dir      string
}

// Executor runs scripts. It is safe for concurrent use by many goroutines.
type Executor struct {
	memCap int64
	wasm   []byte
	dir    string
	closed atomic.Bool

	slots    chan struct{} // one token per running script
	busyWait time.Duration
}

// NewExecutor patches the wasm, creates the empty root directory and pays the
// cold-start cost (about 300 ms) with a warm-up run, so the first Run is fast.
func NewExecutor(opts ...Option) (*Executor, error) {
	cfg := config{memoryBytes: DefaultLimits().MemoryBytes, busyWait: defaultBusyWait}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxConcurrent <= 0 {
		cfg.maxConcurrent = runtime.GOMAXPROCS(0) * 2
	}
	if cfg.memoryBytes < 1<<20 {
		return nil, fmt.Errorf("sandbox: memory cap %d is below 1 MiB", cfg.memoryBytes)
	}
	e, err := acquire(cfg.memoryBytes)
	if err != nil {
		return nil, err
	}
	e.slots, e.busyWait = make(chan struct{}, cfg.maxConcurrent), cfg.busyWait
	if _, err := e.Run(context.Background(), `async () => 1`, nil, Limits{Timeout: 30 * time.Second}); err != nil {
		_ = e.Close()
		return nil, fmt.Errorf("sandbox: warm up: %w", err)
	}
	return e, nil
}

func acquire(memBytes int64) (*Executor, error) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.refs > 0 && shared.memBytes != memBytes {
		return nil, fmt.Errorf("sandbox: another Executor already uses a %d byte memory cap", shared.memBytes)
	}
	if shared.refs == 0 {
		dir, err := newEmptyDir()
		if err != nil {
			return nil, err
		}
		pages := uint32((memBytes + runtimeOverheadBytes) / wasmPageSize)
		wasm, err := withMemoryMax(qjsWasm, pages)
		if err != nil {
			_ = removeDir(dir)
			return nil, fmt.Errorf("sandbox: patch wasm memory: %w", err)
		}
		shared.memBytes, shared.wasm, shared.dir = memBytes, wasm, dir
	}
	shared.refs++
	return &Executor{memCap: memBytes, wasm: shared.wasm, dir: shared.dir}, nil
}

// newEmptyDir creates the directory qjs mounts at "/". Read-only so that
// path_open(O_CREAT) fails with EACCES; not effective when running as root, so
// production must also use a read-only filesystem.
func newEmptyDir() (string, error) {
	dir, err := os.MkdirTemp("", "mcpgw-sandbox-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		_ = removeDir(dir)
		return "", err
	}
	return dir, nil
}

func removeDir(dir string) error {
	_ = os.Chmod(dir, 0o755)
	return os.RemoveAll(dir)
}

// Close releases the Executor. The process-global state is dropped with the
// last one. Do not call it while Runs are in flight.
func (e *Executor) Close() error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.refs--; shared.refs > 0 {
		return nil
	}
	err := removeDir(shared.dir)
	shared.wasm, shared.dir = nil, ""
	return err
}

func (e *Executor) option(ctx context.Context, lim Limits) qjs.Option {
	return qjs.Option{
		Context:            ctx,
		CloseOnContextDone: true, // the only thing that stops a native runaway builtin
		QuickJSWasmBytes:   e.wasm,
		CWD:                e.dir,
		MemoryLimit:        int(lim.MemoryBytes),
		Stdout:             io.Discard, // library default is os.Stdout
		Stderr:             io.Discard, // library default is os.Stderr
	}
}

// Run executes code in a fresh runtime and returns its result as JSON.
func (e *Executor) Run(parent context.Context, code string, bridge Bridge, lim Limits) (json.RawMessage, error) {
	return e.run(parent, code, bridge, lim, true)
}

// run is Run with the syntax scan optional, so tests can prove that the WASI
// containment holds even if a script reaches the qjs: modules.
func (e *Executor) run(parent context.Context, code string, bridge Bridge, lim Limits, scan bool) (json.RawMessage, error) {
	if e.closed.Load() {
		return nil, ErrClosed
	}
	if scan && importToken.MatchString(code) {
		return nil, fmt.Errorf("%w: import is not allowed", ErrForbiddenSyntax)
	}
	if bridge == nil {
		bridge = noBridge
	}
	lim = lim.withDefaults(e.memCap)
	if err := e.acquireSlot(parent); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, lim.Timeout)
	defer cancel()

	type outcome struct {
		out json.RawMessage
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		// The slot is held until the instance is really gone, even if Run already returned
		// at the deadline: an abandoned guest still occupies its memory.
		defer func() { <-e.slots }()
		out, err := e.runOnce(ctx, code, bridge, lim)
		done <- outcome{out, err}
	}()

	// Watchdog: a guest parked in a host-side sleep cannot be interrupted by
	// wazero, so return at the deadline regardless and let the goroutine end
	// whenever it can. Unreachable with the scan on; kept as defense in depth.
	select {
	case o := <-done:
		return o.out, o.err
	case <-ctx.Done():
		return nil, timeoutError(ctx)
	}
}

// acquireSlot takes a run slot, waiting at most busyWait. The wait does not count against the
// script timeout.
func (e *Executor) acquireSlot(ctx context.Context) error {
	select {
	case e.slots <- struct{}{}:
		return nil
	default:
	}
	timer := time.NewTimer(e.busyWait)
	defer timer.Stop()
	select {
	case e.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return timeoutError(ctx)
	case <-timer.C:
		return ErrBusy
	}
}

func noBridge(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("tools are not available")
}

func (e *Executor) runOnce(ctx context.Context, code string, bridge Bridge, lim Limits) (out json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicToError(ctx, r)
		}
	}()
	s, err := newSession(ctx, e.option(ctx, lim), bridge, lim)
	if err != nil {
		return nil, err
	}
	defer s.close()
	return s.run(code)
}

func timeoutError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w (%v)", ErrTimeout, ctx.Err())
	}
	return ctx.Err()
}

// panicToError converts a wasm trap or a wazero "module closed" panic (qjs
// panics instead of returning errors) into an error.
func panicToError(ctx context.Context, r any) error {
	if ctx.Err() != nil {
		return timeoutError(ctx)
	}
	first, _, _ := strings.Cut(fmt.Sprint(r), "\n")
	return fmt.Errorf("%w: %s", ErrRuntimeFault, first)
}

func classify(ctx context.Context, msg string) error {
	if ctx.Err() != nil {
		return timeoutError(ctx)
	}
	if strings.Contains(msg, "out of memory") {
		return fmt.Errorf("%w: %s", ErrMemoryLimit, firstLine(msg))
	}
	return fmt.Errorf("%w: %s", ErrScript, msg)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
