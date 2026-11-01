//go:build !windows

package montyrs

// Tests that need Unix process control: SIGSTOP, ps (workerRSSMB) and a
// /bin/sh fake worker. On Windows the gateway's child is the launcher, not
// the confined worker, so stopping or measuring it would miss the worker.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

func TestHungWorkerIsKilledAtTheDeadline(t *testing.T) {
	e := newExecutor(t, WithMaxConcurrent(1))
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		signalWorker(t, e, syscall.SIGSTOP) // the worker stops answering, even to abort
		return json.RawMessage(`1`), nil
	}
	start := time.Now()
	_, err := e.Run(context.Background(), `call_tool("x")`, bridge, sandbox.Limits{Timeout: 200 * time.Millisecond})
	wantErr(t, err, sandbox.ErrTimeout, "")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %v, want about timeout + abort grace", elapsed)
	}
	mustRunOn(t, e, `1`)
}

func TestFinishedAndKilledRunsDoNotRetainMemory(t *testing.T) {
	slow := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	for name, k := range map[string]struct {
		code     string
		timeout  time.Duration
		recycles bool // the run ends in MemoryError, so its worker is replaced
	}{
		"deadline in a busy loop":          {"while True:\n    pass", 50 * time.Millisecond, false},
		"completed run":                    {`1`, 5 * time.Second, false},
		"deadline on a bridge wait":        {`call_tool("slow", {})`, 50 * time.Millisecond, false},
		"deadline on gathered async calls": {"import asyncio\nawait asyncio.gather(*[call_tool_async('slow') for i in range(8)])", 50 * time.Millisecond, false},
		"allocation bomb":                  {"x = []\nwhile True:\n    x.append('a' * (1 << 20))", 5 * time.Second, true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newExecutor(t, WithMaxConcurrent(1))
			_, _ = e.Run(context.Background(), k.code, slow, sandbox.Limits{Timeout: k.timeout})
			mustRunOn(t, e, `1`) // waits for a recycled worker to be back
			w := liveWorker(t, e)
			heap, rss := liveHeapMB(), workerRSSMB(t, w)
			for range retentionRuns {
				_, _ = e.Run(context.Background(), k.code, slow, sandbox.Limits{Timeout: k.timeout})
			}
			mustRunOn(t, e, `1`)
			if replaced := liveWorker(t, e) != w; replaced != k.recycles {
				t.Fatalf("worker replaced = %v, want %v", replaced, k.recycles)
			}
			heapEnd, rssEnd := liveHeapMB(), workerRSSMB(t, liveWorker(t, e))
			t.Logf("Go heap %d -> %d MB, worker RSS %d -> %d MB over %d runs", heap, heapEnd, rss, rssEnd, retentionRuns)
			if heapEnd > heap+retentionBudgetMB || rssEnd > rss+retentionBudgetMB {
				t.Errorf("memory grew beyond the %d MB budget", retentionBudgetMB)
			}
		})
	}
}

// TestOversizedCallFrameKillsTheWorker plays a compromised worker that skips
// the in-worker args cap: the gateway must kill it, not run the call.
func TestOversizedCallFrameKillsTheWorker(t *testing.T) {
	dir := t.TempDir()
	body, err := json.Marshal(message{Type: "call", ID: 1, Name: "a.b", Args: `"` + strings.Repeat("y", maxCallArgsBytes) + `"`})
	if err != nil {
		t.Fatal(err)
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	if err := os.WriteFile(filepath.Join(dir, "frame"), append(frame, body...), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "worker")
	script := "#!/bin/sh\n/bin/cat " + filepath.Join(dir, "frame") + "\nexec /bin/sleep 10\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	w, err := spawnWorker(fake, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer w.kill()
	bridge := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Error("the oversized call reached the bridge")
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = newSession(ctx, w, bridge, withDefaults(sandbox.Limits{}, 64<<20)).run(`1`)
	wantErr(t, err, sandbox.ErrRuntimeFault, "killed")
}
