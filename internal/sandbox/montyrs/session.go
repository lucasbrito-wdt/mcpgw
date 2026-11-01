package montyrs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

// tooManyCallsMessage is the RuntimeError text past MaxBridgeCalls; an uncaught
// one maps back to sandbox.ErrTooManyCalls (same text as the other engines).
const tooManyCallsMessage = "sandbox: too many bridge calls"

type completion struct {
	id  uint64
	out json.RawMessage
	err error
}

// session drives one run on one worker. Only the goroutine calling run talks
// to the worker; bridge goroutines only send on done, which is big enough
// that they never block.
type session struct {
	ctx         context.Context
	callCtx     context.Context // cancelled when the run ends
	cancelCalls context.CancelFunc
	w           *worker
	bridge      sandbox.Bridge
	lim         sandbox.Limits

	done     chan completion
	queue    []message // calls waiting for an in-flight slot, FIFO
	inflight int
	calls    int

	healthy   bool // the worker finished the run cleanly and can be reused
	diedEarly bool // the worker died before the script made any call
}

func newSession(ctx context.Context, w *worker, bridge sandbox.Bridge, lim sandbox.Limits) *session {
	callCtx, cancel := context.WithCancel(ctx)
	return &session{
		ctx: ctx, callCtx: callCtx, cancelCalls: cancel, w: w, bridge: bridge, lim: lim,
		done: make(chan completion, lim.MaxBridgeCalls),
	}
}

func (s *session) run(code string) (json.RawMessage, error) {
	defer s.waitForBridges()
	defer s.cancelCalls()
	deadline, _ := s.ctx.Deadline()
	timeoutMS := time.Until(deadline).Milliseconds()
	if timeoutMS <= 0 {
		s.healthy = true
		return nil, timeoutError(s.ctx)
	}
	s.w.maxFrame.Store(maxFrameBytes(s.lim.MaxResultBytes))
	start := message{
		Type: "run", Code: code, MemoryBytes: s.lim.MemoryBytes,
		TimeoutMS: timeoutMS, MaxResultBytes: s.lim.MaxResultBytes,
	}
	if err := s.w.send(start, deadline.Add(abortGrace)); err != nil {
		return nil, s.crashed()
	}
	for {
		select {
		case m, ok := <-s.w.msgs:
			if !ok {
				return nil, s.crashed()
			}
			if m.Type == "done" {
				// After a MemoryError or any allocation-heavy run the system
				// allocator keeps the freed pages (RSS settles near the peak), so
				// that worker is recycled.
				s.healthy = m.Kind != "memory" && !m.Heavy
				return s.output(m)
			}
			if m.Type != "call" || len(m.Args) > maxCallArgsBytes || s.startCall(m) != nil {
				s.w.kill()
				return nil, s.crashed()
			}
		case c := <-s.done:
			s.inflight--
			s.launchQueued()
			if s.reply(c) != nil {
				return nil, s.crashed()
			}
		case <-s.ctx.Done():
			s.abort()
			return nil, timeoutError(s.ctx)
		}
	}
}

// startCall launches, queues or rejects a tool call coming from the script.
func (s *session) startCall(m message) error {
	s.calls++
	switch {
	case s.calls > s.lim.MaxBridgeCalls:
		err := fmt.Errorf("%s (max %d per execution)", tooManyCallsMessage, s.lim.MaxBridgeCalls)
		return s.reply(completion{id: m.ID, err: err})
	case !json.Valid([]byte(m.Args)):
		return s.reply(completion{id: m.ID, err: errors.New("invalid call arguments")})
	case s.inflight >= s.lim.MaxInFlight:
		s.queue = append(s.queue, m)
	default:
		s.launch(m)
	}
	return nil
}

func (s *session) launch(m message) {
	s.inflight++
	ctx, bridge, done := s.callCtx, s.bridge, s.done
	go func() {
		out, err := callBridge(ctx, bridge, m.Name, json.RawMessage(m.Args))
		done <- completion{m.ID, out, err} // buffered: never blocks, even after the run ended
	}()
}

func (s *session) launchQueued() {
	for len(s.queue) > 0 && s.inflight < s.lim.MaxInFlight {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.launch(next)
	}
}

func callBridge(ctx context.Context, bridge sandbox.Bridge, name string, args json.RawMessage) (out json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("bridge panic: %v", r)
		}
	}()
	out, err = bridge(ctx, name, args)
	if err == nil && len(out) > 0 && !json.Valid(out) {
		return nil, fmt.Errorf("bridge returned invalid JSON")
	}
	return out, err
}

func (s *session) reply(c completion) error {
	m := message{Type: "result", ID: c.id}
	if c.err != nil {
		msg := c.err.Error()
		m.Error = &msg
	} else {
		out := string(c.out)
		m.OK = &out
	}
	deadline, _ := s.ctx.Deadline()
	return s.w.send(m, deadline.Add(abortGrace))
}

// abort asks the worker to end the run and waits briefly for its "done";
// a worker that does not answer (stopped, wedged in native code) is killed.
func (s *session) abort() {
	grace := time.Now().Add(abortGrace)
	if s.w.send(message{Type: "abort"}, grace) != nil {
		s.w.kill()
		return
	}
	timer := time.NewTimer(abortGrace)
	defer timer.Stop()
	for {
		select {
		case m, ok := <-s.w.msgs:
			if !ok {
				return
			}
			if m.Type == "done" { // calls made before the abort landed are dropped
				s.healthy = !m.Heavy
				return
			}
		case <-timer.C:
			s.w.kill()
			return
		}
	}
}

// crashed reports a worker that went away (or broke the protocol) mid-run.
func (s *session) crashed() error {
	s.w.kill()
	s.diedEarly = s.calls == 0
	err := s.w.exitError()
	if s.ctx.Err() != nil {
		return timeoutError(s.ctx)
	}
	if s.w.diedOfOOM() {
		return fmt.Errorf("%w: %v", sandbox.ErrMemoryLimit, err)
	}
	return fmt.Errorf("%w: %v", sandbox.ErrRuntimeFault, err)
}

// waitForBridges gives cancelled bridge calls a short grace to return, so a
// well-behaved bridge is finished when Run returns. Late results are dropped.
func (s *session) waitForBridges() {
	if s.inflight == 0 {
		return
	}
	timer := time.NewTimer(bridgeDrainGrace)
	defer timer.Stop()
	for s.inflight > 0 {
		select {
		case <-s.done:
			s.inflight--
		case <-timer.C:
			return
		}
	}
}

func (s *session) output(m message) (json.RawMessage, error) {
	if m.Kind != "ok" {
		m.Payload = clipMiddle(m.Payload, maxErrorBytes)
	}
	switch m.Kind {
	case "ok":
		if len(m.Payload) > s.lim.MaxResultBytes {
			return nil, fmt.Errorf("%w: %d bytes, max %d", sandbox.ErrResultTooLarge, len(m.Payload), s.lim.MaxResultBytes)
		}
		if !json.Valid([]byte(m.Payload)) {
			return nil, fmt.Errorf("%w: invalid JSON from worker", sandbox.ErrRuntimeFault)
		}
		return json.RawMessage(m.Payload), nil
	case "too_large":
		return nil, fmt.Errorf("%w: %s bytes, max %d", sandbox.ErrResultTooLarge, m.Payload, s.lim.MaxResultBytes)
	case "unserializable":
		return nil, fmt.Errorf("%w: %s", sandbox.ErrNotSerializable, m.Payload)
	case "memory":
		return nil, fmt.Errorf("%w: %s", sandbox.ErrMemoryLimit, m.Payload)
	case "timeout", "aborted":
		if s.ctx.Err() != nil {
			return nil, timeoutError(s.ctx)
		}
		return nil, fmt.Errorf("%w (worker deadline)", sandbox.ErrTimeout)
	}
	if s.ctx.Err() != nil {
		return nil, timeoutError(s.ctx)
	}
	if strings.Contains(m.Payload, tooManyCallsMessage) {
		return nil, fmt.Errorf("%w: %s", sandbox.ErrTooManyCalls, lastLine(m.Payload))
	}
	return nil, fmt.Errorf("%w: %s", sandbox.ErrScript, m.Payload)
}

// lastLine picks the "Type: message" line out of a Python traceback.
func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	return s[strings.LastIndex(s, "\n")+1:]
}

// clipMiddle keeps the head and tail of s (a traceback's last line names the
// error) within about max bytes, cutting on UTF-8 boundaries.
func clipMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	head, tail := max/2, len(s)-max/2
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return fmt.Sprintf("%s\n... [%d bytes truncated] ...\n%s", s[:head], tail-head, s[tail:])
}
