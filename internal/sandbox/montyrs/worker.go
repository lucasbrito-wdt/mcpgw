package montyrs

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// message is every frame exchanged with the worker; unused fields are omitted.
type message struct {
	Type string `json:"type"`

	// run
	Code           string `json:"code,omitempty"`
	MemoryBytes    int64  `json:"memory_bytes,omitempty"`
	TimeoutMS      int64  `json:"timeout_ms,omitempty"`
	MaxResultBytes int    `json:"max_result_bytes,omitempty"`

	// call (worker -> gateway) and result (gateway -> worker)
	ID    uint64  `json:"id,omitempty"`
	Name  string  `json:"name,omitempty"`
	Args  string  `json:"args,omitempty"`
	OK    *string `json:"ok,omitempty"`
	Error *string `json:"error,omitempty"`

	// done
	Kind    string `json:"kind,omitempty"`
	Payload string `json:"payload,omitempty"`
	Heavy   bool   `json:"heavy,omitempty"` // the run peaked high enough that the worker should be recycled
}

// worker is one montyrs-worker process. msgs is fed by a reader goroutine and
// closed when the process's stdout ends (exit, crash or kill).
type worker struct {
	cmd    *exec.Cmd
	stdin  *os.File // owned pipe end, so writes can carry a deadline
	msgs   chan message
	exited chan struct{} // closed once Wait returns
	killed chan struct{} // closed by kill; unblocks the reader after the run is abandoned
	once   sync.Once
	stderr *tailBuffer
	// maxFrame bounds incoming frames; each session sets it for its limits.
	maxFrame atomic.Int64

	// The Limits.Owner this worker serves; guarded by Executor.mu. A worker
	// no run has claimed yet can go to any owner.
	owner   string
	claimed bool
}

// spawnWorker starts a worker whose runs are capped at memoryBytes.
func spawnWorker(path string, memoryBytes int64) (*worker, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_, _ = inR.Close(), inW.Close()
		return nil, err
	}
	cmd := workerCommand(path, workerArgs(memoryBytes)...)
	cmd.Stdin, cmd.Stdout = inR, outW
	w := &worker{cmd: cmd, stdin: inW, msgs: make(chan message, 16), exited: make(chan struct{}), killed: make(chan struct{}), stderr: &tailBuffer{}}
	cmd.Stderr = w.stderr
	w.maxFrame.Store(maxFrameBytes(0))
	err = cmd.Start()
	_, _ = inR.Close(), outW.Close() // the child holds its own copies
	if err != nil {
		_, _ = inW.Close(), outR.Close()
		return nil, fmt.Errorf("start worker %s: %w", path, err)
	}
	if err := adoptWorker(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_, _ = inW.Close(), outR.Close()
		return nil, fmt.Errorf("adopt worker %s: %w", path, err)
	}
	go w.readLoop(outR)
	go func() {
		_ = cmd.Wait()
		close(w.exited)
	}()
	return w, nil
}

// workerCommand starts a worker binary with nothing but stdin, stdout and
// stderr: no environment, no extra fds, and "/" as its directory.
func workerCommand(path string, args ...string) *exec.Cmd {
	cmd := exec.Command(path, args...)
	cmd.Env = []string{}
	cmd.Dir = "/"
	cmd.SysProcAttr = workerSysProcAttr()
	return cmd
}

// readLoop decodes frames until EOF. A frame larger than the worker could
// legitimately produce, or one that is not JSON, kills the worker.
func (w *worker) readLoop(r *os.File) {
	defer close(w.msgs)
	defer r.Close()
	br := bufio.NewReader(r)
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return
		}
		n := int64(binary.BigEndian.Uint32(hdr[:]))
		if n > w.maxFrame.Load() {
			w.kill()
			return
		}
		buf := make([]byte, n)
		var m message
		if _, err := io.ReadFull(br, buf); err != nil || json.Unmarshal(buf, &m) != nil {
			w.kill()
			return
		}
		select {
		case w.msgs <- m:
		case <-w.killed:
			return
		}
	}
}

// send writes one frame; the deadline keeps a stopped worker with a full pipe
// from blocking the gateway.
func (w *worker) send(m message, deadline time.Time) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	frame := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	frame = append(frame, body...)
	_ = w.stdin.SetWriteDeadline(deadline)
	_, err = w.stdin.Write(frame)
	return err
}

func (w *worker) isDead() bool {
	select {
	case <-w.exited:
		return true
	default:
		return false
	}
}

// kill stops the process and closes stdin; it is safe to call more than once.
func (w *worker) kill() {
	w.once.Do(func() {
		close(w.killed)
		_ = w.cmd.Process.Kill()
		_ = w.stdin.Close()
	})
}

// oomExitCode is monty_types::OOM_EXIT_CODE: monty-alloc ends the worker with
// it when an allocation crosses the hard memory ceiling.
const oomExitCode = 65

// diedOfOOM reports whether the worker exited past its hard memory ceiling.
func (w *worker) diedOfOOM() bool {
	ps := w.cmd.ProcessState
	return ps != nil && ps.ExitCode() == oomExitCode
}

// exitError describes why the worker went away, waiting briefly for Wait so
// the exit status is known.
func (w *worker) exitError() error {
	select {
	case <-w.exited:
	case <-time.After(time.Second):
		w.kill()
		<-w.exited
	}
	state := "unknown"
	if ps := w.cmd.ProcessState; ps != nil {
		state = ps.String()
	}
	// The stderr tail stays out of the error, which reaches the model.
	if tail := w.stderr.String(); tail != "" {
		slog.Warn("montyrs: worker exited", "state", state, "stderr", tail)
	}
	return fmt.Errorf("worker exited (%s)", state)
}

// tailBuffer keeps the last stderrTailBytes written by the worker.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const stderrTailBytes = 2048

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - stderrTailBytes; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
