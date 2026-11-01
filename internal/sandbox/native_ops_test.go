package sandbox

import (
	"context"
	"errors"
	"sort"
	"syscall"
	"testing"
	"time"
)

// The same probes used against modernc.org/quickjs (see ../alt). Natively
// looping builtins never return to the interpreter loop, so nothing in the engine
// polls an interrupt flag. Here wazero's CloseOnContextDone is the only thing
// that can stop them, so the test checks that the instance is actually killed
// (CPU goes quiet) and not merely abandoned by the watchdog.

var runawayProbes = map[string]string{
	"indexOf-generic-2pow53":  `async () => Array.prototype.indexOf.call({ length: 2 ** 53 - 1 }, 1)`,
	"includes-generic-2pow53": `async () => Array.prototype.includes.call({ length: 2 ** 53 - 1 }, 1)`,
	"lastIndexOf-2pow32":      `async () => Array.prototype.lastIndexOf.call({ length: 2 ** 32 - 1 }, 1)`,
	"join-sparse-4e8":         `async () => new Array(4e8).join("")`,
	"reverse-generic-2pow31":  `async () => Array.prototype.reverse.call({ length: 2 ** 31 })`,
	"fill-generic-2pow32":     `async () => Array.prototype.fill.call({ length: 2 ** 32 - 1 }, 0)`,
	"copyWithin-generic":      `async () => Array.prototype.copyWithin.call({ length: 2 ** 32 - 1 }, 0, 1)`,
	"bigint-toString-30M":     `async () => (2n ** 30000000n).toString().length`,
	"bigint-parse-5M-digits":  `async () => BigInt("9".repeat(5e6)).toString(16).length`,
	"bigint-div-large":        `async () => ((10n ** 3000000n) / (7n ** 1000000n)).toString().length`,
	"sparse-sort":             `async () => { const a = []; a[4e8] = 1; return a.sort().length }`,
	"string-lastIndexOf":      `async () => "a".repeat(30e6).lastIndexOf("b".repeat(1000))`,
	"localeCompare-long":      `async () => "a".repeat(30e6).localeCompare("a".repeat(30e6) + "b")`,
}

var recursionProbes = map[string]string{
	"json-stringify-deep-return": `async () => { let o = {}; for (let i = 0; i < 1e5; i++) o = { o }; return o }`,
	"json-stringify-deep-guest":  `async () => { let o = []; for (let i = 0; i < 1e6; i++) o = [o]; return JSON.stringify(o).length }`,
	"json-parse-deep":            `async () => JSON.parse("[".repeat(1e6))`,
	"eval-deep-brackets":         `async () => (0, eval)("[".repeat(1e6))`,
	"eval-deep-parens":           `async () => (0, eval)("(".repeat(1e6) + "1" + ")".repeat(1e6))`,
	"regexp-deep-groups":         `async () => new RegExp("(".repeat(1e5) + ")".repeat(1e5))`,
	"flat-infinity-deep":         `async () => { let o = [1]; for (let i = 0; i < 1e6; i++) o = [o]; return o.flat(Infinity).length }`,
	"proxy-get-recursion":        `async () => { const p = new Proxy({}, { get(t, k, r) { return r[k] } }); return p.x }`,
	"getter-recursion":           `async () => { const o = { get a() { return this.a } }; return o.a }`,
	"tostring-recursion":         `async () => { const o = { toString() { return String(o) } }; return String(o) }`,
	"toprimitive-recursion":      `async () => { const o = { [Symbol.toPrimitive]() { return +o } }; return +o }`,
	"tojson-recursion":           `async () => { const o = { toJSON() { return JSON.stringify(o) } }; return JSON.stringify(o) }`,
	"bind-chain-deep":            `async () => { let f = function () {}; for (let i = 0; i < 1e6; i++) f = f.bind(null); return f() }`,
	"prototype-chain-deep":       `async () => { let o = {}; for (let i = 0; i < 1e6; i++) o = Object.create(o); return o.missing }`,
	"apply-huge-args":            `async () => Math.max.apply(null, new Array(5e6).fill(1))`,
	"array-flatMap-recursion":    `async () => { const f = (x) => [x].flatMap(f); return f(1) }`,
	"sort-comparator-recursion":  `async () => { const f = () => [2, 1].sort(f); return f() }`,
	"replace-callback-recursion": `async () => { const f = () => "a".replace(/a/, f); return f() }`,
	"reduce-recursion":           `async () => { const f = () => [1, 2].reduce(f); return f() }`,
	"promise-then-nest-deep":     `async () => { let p = Promise.resolve(1); for (let i = 0; i < 1e6; i++) p = Promise.resolve(p); return await p }`,
	"class-extends-deep":         `async () => { let c = class {}; for (let i = 0; i < 1e5; i++) c = class extends c {}; return new c() && 1 }`,
}

func cpuTime() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func sortedProbeNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestC4_RunawayNativeBuiltinsAreKilledNotJustAbandoned(t *testing.T) {
	for _, name := range sortedProbeNames(runawayProbes) {
		t0 := time.Now()
		_, err := ex.Run(context.Background(), runawayProbes[name], noBridge, Limits{Timeout: 300 * time.Millisecond})
		returned := time.Since(t0)
		// If the instance were only abandoned it would keep a core busy; wait and see.
		time.Sleep(700 * time.Millisecond)
		cpu1 := cpuTime()
		time.Sleep(700 * time.Millisecond)
		idle := cpuTime() - cpu1
		t.Logf("%-26s returned after %-7v err=%v; CPU burned in the following 700ms: %v", name, returned.Round(time.Millisecond), firstErrLine(err), idle.Round(time.Millisecond))
		if returned > 3*time.Second {
			t.Errorf("%s: Run returned after %v with Timeout=300ms", name, returned)
		}
		if idle > 300*time.Millisecond {
			t.Errorf("%s: the instance is still burning CPU (%v in 700ms) after Run returned", name, idle)
		}
	}
}

func TestC3C4_NativeRecursionIsContainedInTheWasmSandbox(t *testing.T) {
	for _, name := range sortedProbeNames(recursionProbes) {
		t0 := time.Now()
		out, err := ex.Run(context.Background(), recursionProbes[name], noBridge, Limits{Timeout: 2 * time.Second})
		t.Logf("%-28s %-8v out=%.30s err=%v", name, time.Since(t0).Round(time.Millisecond), out, firstErrLine(err))
		if err != nil && !errors.Is(err, ErrScript) && !errors.Is(err, ErrRuntimeFault) && !errors.Is(err, ErrMemoryLimit) && !errors.Is(err, ErrTimeout) && !errors.Is(err, ErrNotSerializable) {
			t.Errorf("%s: unclassified error %v", name, err)
		}
	}
}

func firstErrLine(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	for i, c := range s {
		if c == '\n' || i > 150 {
			return s[:i]
		}
	}
	return s
}
