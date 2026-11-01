package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fastschema/qjs"
	"github.com/tetratelabs/wazero"
)

// Some behaviours either crash the process or mutate process-global library
// state (qjs caches the compiled wasm module and its wazero config on the
// first New). Those run in a child process: this test binary re-executed with
// childEnv set, so the parent suite stays green and deterministic.
const childEnv = "MCPGW_SANDBOX_CHILD"

func TestChildEntry(t *testing.T) {
	mode := os.Getenv(childEnv)
	if mode == "" {
		t.Skip("only runs as a child process")
	}
	switch mode {
	case "native-async":
		childNativeAsync()
	case "stock-wasm":
		childStockWasm()
	case "cold":
		childCold()
	default:
		t.Fatalf("unknown child mode %q", mode)
	}
}

var errChildHung = errors.New("child hung and was killed")

// runChild runs the test binary as a child in the given mode. The output buffer is only read
// after cmd.Wait returned (the copy goroutines are done), also on the hang path: reading it
// while the child's output was still being copied was a data race. A hung child is killed and
// reported as errChildHung together with what it printed.
func runChild(t *testing.T, mode string, extraEnv ...string) (output string, exitErr error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildEntry$", "-test.count=1")
	cmd.Env = append(os.Environ(), childEnv+"="+mode)
	cmd.Env = append(cmd.Env, extraEnv...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return buf.String(), errChildHung
	}
}

func childLine(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(line, "CHILD "); ok {
			return rest
		}
	}
	t.Fatalf("child printed no CHILD line:\n%s", tail(output, 2000))
	return ""
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func wasmImports(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)
	compiled, err := rt.CompileModule(ctx, qjsWasm)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range compiled.ImportedFunctions() {
		module, name, _ := f.Import()
		names = append(names, module+"."+name)
	}
	return names
}

func newStockRuntime(dir string) (*qjs.Runtime, error) {
	// Library defaults except for output noise: stock wasm, CWD mounted at "/".
	return qjs.New(qjs.Option{CWD: dir, MemoryLimit: 64 << 20, Stdout: io.Discard, Stderr: io.Discard})
}

// ---------------------------------------------------------------- native async

func childNativeAsync() {
	dir, _ := os.MkdirTemp("", "qjs-child-")
	defer os.RemoveAll(dir)
	// Heap corruption sometimes shows up as a deadlock instead of a crash.
	time.AfterFunc(20*time.Second, func() {
		fmt.Println("fatal error: HANG: the README async pattern deadlocked the process")
		os.Exit(3)
	})
	const iterations = 200
	for i := 0; i < iterations; i++ {
		rt, err := newStockRuntime(dir)
		if err != nil {
			fmt.Println("CHILD", fmt.Sprintf(`{"error":%q}`, err))
			return
		}
		c := rt.Context()
		// The pattern from the qjs README: resolve the promise from a goroutine.
		c.SetAsyncFunc("callTool", func(this *qjs.This) {
			go func() {
				time.Sleep(50 * time.Millisecond)
				this.Promise().Resolve(this.Context().NewString("ok"))
			}()
		})
		v, err := c.Eval("t.js", qjs.Code(`(async () => (await Promise.all([callTool(), callTool(), callTool()])).join(","))()`))
		if err != nil || v.String() != "ok,ok,ok" {
			fmt.Println("CHILD", fmt.Sprintf(`{"wrong_result_at":%d}`, i))
			return
		}
		rt.Close()
	}
	fmt.Println("CHILD", fmt.Sprintf(`{"survived":%d}`, iterations))
}

func TestC1_NativeAsyncResolveFromGoroutineCrashesTheProcess(t *testing.T) {
	out, err := runChild(t, "native-async")
	if err == nil {
		t.Fatalf("expected the README async pattern to crash the process, but it survived:\n%s", tail(out, 500))
	}
	var evidence []string
	for _, line := range strings.Split(out, "\n") {
		// The failure mode is nondeterministic (heap corruption): a runtime
		// fatal error, a Go panic from a use-after-free, or a race report.
		if strings.HasPrefix(line, "fatal error:") || strings.HasPrefix(line, "panic") || strings.Contains(line, "DATA RACE") || strings.HasPrefix(line, "SIGSEGV") || strings.HasPrefix(line, "unexpected fault") || strings.Contains(line, "wasm error:") {
			evidence = append(evidence, strings.TrimSpace(line))
			if len(evidence) == 4 {
				break
			}
		}
	}
	t.Logf("child exited with %v; evidence: %q", err, evidence)
	if len(evidence) == 0 {
		t.Errorf("child failed without a recognizable crash signature:\n%s", tail(out, 800))
	}
}

// ---------------------------------------------------------------- stock config

func childStockWasm() {
	dir := os.Getenv("MCPGW_SANDBOX_DIR")
	rt, err := newStockRuntime(dir)
	if err != nil {
		fmt.Println("CHILD", fmt.Sprintf(`{"error":%q}`, err))
		return
	}
	defer rt.Close()
	v, err := rt.Context().Eval("stock.js", qjs.Code(`
		const r = {};
		r.globals = [typeof std, typeof os, typeof bjson, typeof setTimeout, typeof fetch, typeof require, typeof process];
		r.readCanary = std.loadFile("/canary.txt");
		const f = std.open("/created-by-guest.txt", "w");
		r.write = f ? (f.puts("x"), f.close(), "created") : "refused";
		r.mkdir = os.mkdir("/created-dir");
		const a = []; let oom = "none";
		try { for (let i = 0; i < 300; i++) a.push(new ArrayBuffer(1 << 20)); } catch (e) { oom = e.message }
		r.allocatedMB = a.length; r.oom = oom;
		JSON.stringify(r)
	`))
	if err != nil {
		fmt.Println("CHILD", fmt.Sprintf(`{"error":%q}`, err))
		return
	}
	fmt.Println("CHILD", v.String())
}

func TestC3andC5_NegativeControl_StockLibraryConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "canary.txt"), []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runChild(t, "stock-wasm", "MCPGW_SANDBOX_DIR="+dir)
	if err != nil {
		t.Fatalf("child: %v\n%s", err, tail(out, 1000))
	}
	line := childLine(t, out)
	t.Logf("stock fastschema/qjs, MemoryLimit=64MB, CWD mounted: %s", line)

	var got struct {
		Globals     []string
		ReadCanary  *string
		Write       string
		Mkdir       int
		AllocatedMB int
		OOM         string
		Error       string
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil || got.Error != "" {
		t.Fatalf("bad child output %q (%v)", line, err)
	}
	// The default configuration is NOT safe: these assertions document it.
	if got.Globals[0] != "object" || got.Globals[1] != "object" || got.Globals[2] != "object" || got.Globals[3] != "function" {
		t.Errorf("expected std/os/bjson/setTimeout globals in the stock build, got %v", got.Globals)
	}
	if got.ReadCanary == nil || *got.ReadCanary != "TOP-SECRET" {
		t.Errorf("expected the guest to read the mounted host file, got %v", got.ReadCanary)
	}
	if got.Write != "created" {
		t.Errorf("expected the guest to create a file on the host, got %q", got.Write)
	} else if _, err := os.Stat(filepath.Join(dir, "created-by-guest.txt")); err != nil {
		t.Errorf("guest reported a write but the host file is missing: %v", err)
	}
	if got.AllocatedMB < 300 || got.OOM != "none" {
		t.Errorf("expected MemoryLimit=64MB to NOT stop 300MB of allocations, got %d MB, oom=%q", got.AllocatedMB, got.OOM)
	}
}

// ---------------------------------------------------------------- cold start

func childCold() {
	t0 := time.Now()
	e, err0 := NewExecutor()
	first := time.Since(t0)
	t1 := time.Now()
	_, err1 := e.Run(context.Background(), `async () => 1`, noBridge, Limits{})
	second := time.Since(t1)
	fmt.Println("CHILD", fmt.Sprintf(`{"first_ms":%.1f,"second_ms":%.1f,"errs":%q}`, ms(first), ms(second), fmt.Sprint(err0, err1)))
	_ = e.Close()
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func TestColdStart(t *testing.T) {
	out, err := runChild(t, "cold")
	if err != nil {
		t.Fatalf("child: %v\n%s", err, tail(out, 800))
	}
	line := childLine(t, out)
	t.Logf("cold process: %s (first = NewExecutor including wasm compile and warm-up; second = first Run afterwards)", line)
	var got struct {
		FirstMS  float64 `json:"first_ms"`
		SecondMS float64 `json:"second_ms"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.FirstMS > 10000 {
		t.Errorf("cold start %.0f ms is too slow", got.FirstMS)
	}
}
