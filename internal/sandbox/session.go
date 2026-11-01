package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fastschema/qjs"
)

type completion struct {
	id  int
	out json.RawMessage
	err error
}

type queuedCall struct {
	id      int
	name    string
	payload json.RawMessage
}

type settled struct{ kind, payload string }

// session owns one qjs runtime for exactly one execution. Every wasm call
// happens on the goroutine that calls run; bridge goroutines only send on
// done, a channel big enough that they can never block. (Calling into the
// module from another goroutine, as the qjs README does, corrupts the heap.)
type session struct {
	rt     *qjs.Runtime
	exec   *qjs.Value // prelude entry point
	broken bool       // a trap or deadline hit the instance: never call into it again

	ctx         context.Context // execution deadline
	callCtx     context.Context // handed to bridge calls; cancelled when the script ends
	cancelCalls context.CancelFunc
	bridge      Bridge
	lim         Limits

	done      chan completion
	pending   map[int]*qjs.Value // promises of calls not yet settled (running or queued)
	queue     []queuedCall       // waiting for an in-flight slot, FIFO
	immediate []completion       // rejected without reaching the bridge
	nextID    int
	inflight  int // goroutines launched and not yet received from done
	result    *settled
}

// tooManyCallsMessage is the text of the JS error raised past MaxBridgeCalls.
// An uncaught one is mapped back to ErrTooManyCalls by its message.
const tooManyCallsMessage = "sandbox: too many bridge calls"

func newSession(ctx context.Context, opt qjs.Option, bridge Bridge, lim Limits) (*session, error) {
	rt, err := qjs.New(opt)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create runtime: %w", err)
	}
	callCtx, cancel := context.WithCancel(ctx)
	s := &session{
		rt: rt, ctx: ctx, callCtx: callCtx, cancelCalls: cancel, bridge: bridge, lim: lim,
		done:    make(chan completion, lim.MaxBridgeCalls),
		pending: map[int]*qjs.Value{},
	}
	c := rt.Context()
	c.SetAsyncFunc("__call", s.start)
	c.SetFunc("__settle", s.settle)
	if s.exec, err = c.Eval("prelude.js", qjs.Code(preludeSource)); err != nil {
		s.cancelCalls()
		closeQuietly(rt)
		releaseWazeroRuntime(rt)
		return nil, fmt.Errorf("sandbox: prelude: %w", err)
	}
	return s, nil
}

// close ends the execution: it cancels every bridge call still running, waits
// briefly for them to return (so a well-behaved bridge is finished when Run
// returns), then releases the runtime. Late results are never delivered: the
// instance is gone and done is only ever drained here. A broken instance is not
// touched at all (after a wasm trap even free() can spin); it is left to the GC.
func (s *session) close() {
	s.cancelCalls()
	s.waitForBridges()
	if !s.broken {
		closeQuietly(s.rt)
	}
	releaseWazeroRuntime(s.rt)
}

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

// closeQuietly: qjs.Runtime.Close panics when the module was already closed by
// a context deadline.
func closeQuietly(rt *qjs.Runtime) {
	defer func() { _ = recover() }()
	rt.Close()
}

// start is the async host function behind tools.call. It runs on the wasm
// goroutine, reads its arguments there, and either launches, queues or rejects.
func (s *session) start(this *qjs.This) {
	args := this.Args()
	name, payload := args[0].String(), json.RawMessage(args[1].String())
	for _, a := range args {
		a.Free()
	}
	s.nextID++
	call := queuedCall{id: s.nextID, name: name, payload: payload}
	s.pending[call.id] = this.Promise().Clone()

	switch {
	case s.nextID > s.lim.MaxBridgeCalls:
		err := fmt.Errorf("%s (max %d per execution)", tooManyCallsMessage, s.lim.MaxBridgeCalls)
		s.immediate = append(s.immediate, completion{id: call.id, err: err})
	case s.inflight >= s.lim.MaxInFlight:
		s.queue = append(s.queue, call)
	default:
		s.launch(call)
	}
}

func (s *session) launch(c queuedCall) {
	s.inflight++
	ctx, bridge, done := s.callCtx, s.bridge, s.done
	go func() {
		out, err := callBridge(ctx, bridge, c.name, c.payload)
		done <- completion{c.id, out, err} // buffered: never blocks, even after the run ended
	}()
}

func callBridge(ctx context.Context, bridge Bridge, name string, args json.RawMessage) (out json.RawMessage, err error) {
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

// settle receives the final outcome from the prelude. First call wins.
func (s *session) settle(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer func() {
		for _, a := range args {
			a.Free()
		}
	}()
	if s.result == nil && len(args) == 2 {
		s.result = &settled{kind: args[0].String(), payload: args[1].String()}
	}
	return this.Context().NewUndefined(), nil
}

func (s *session) run(code string) (out json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.broken = true
			err = panicToError(s.ctx, r)
		}
	}()
	c := s.rt.Context()
	userFn, err := c.Eval("user.js", qjs.Code("(\n"+strings.TrimRight(code, " \t\r\n;")+"\n)"))
	if err != nil {
		return nil, classify(s.ctx, err.Error())
	}
	undefined := c.NewUndefined()
	_, err = c.Invoke(s.exec, undefined, userFn)
	userFn.Free() // explicit, not deferred: never touch the instance after a trap
	undefined.Free()
	if err != nil {
		return nil, classify(s.ctx, err.Error())
	}
	return s.loop()
}

// loop is the single-owner event loop: run queued promise jobs, then wait for
// the next bridge completion (or the deadline) and settle the matching promise.
func (s *session) loop() (json.RawMessage, error) {
	for {
		if err := s.drain(); err != nil {
			return nil, classify(s.ctx, err.Error())
		}
		if s.result != nil {
			return s.output()
		}
		if len(s.immediate) > 0 {
			d := s.immediate[0]
			s.immediate = s.immediate[1:]
			if err := s.complete(d); err != nil {
				return nil, classify(s.ctx, err.Error())
			}
			continue
		}
		if len(s.pending) == 0 {
			return nil, ErrNeverSettles
		}
		select {
		case d := <-s.done:
			s.inflight--
			s.launchQueued()
			if err := s.complete(d); err != nil {
				return nil, classify(s.ctx, err.Error())
			}
		case <-s.ctx.Done():
			return nil, timeoutError(s.ctx)
		}
	}
}

func (s *session) launchQueued() {
	for len(s.queue) > 0 && s.inflight < s.lim.MaxInFlight {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.launch(next)
	}
}

// drain runs queued promise jobs. qjs exposes no JS_ExecutePendingJob, but
// every QJS_Eval ends with js_std_loop, so evaluating a no-op drains the queue.
func (s *session) drain() error {
	v, err := s.rt.Context().Eval("drain.js", qjs.Code("0"))
	if err != nil {
		return err
	}
	v.Free()
	return nil
}

// complete settles the JS promise of a finished (or rejected) bridge call.
func (s *session) complete(d completion) error {
	c := s.rt.Context()
	p := s.pending[d.id]
	delete(s.pending, d.id)
	defer p.Free()

	if d.err != nil {
		e := c.NewError(d.err)
		defer e.Free()
		return p.Reject(e)
	}
	payload := string(d.out)
	if payload == "" {
		payload = "null"
	}
	v := c.NewString(payload)
	defer v.Free()
	return p.Resolve(v)
}

func (s *session) output() (json.RawMessage, error) {
	switch r := s.result; r.kind {
	case "ok":
		if len(r.payload) > s.lim.MaxResultBytes {
			return nil, fmt.Errorf("%w: %d bytes, max %d", ErrResultTooLarge, len(r.payload), s.lim.MaxResultBytes)
		}
		if !json.Valid([]byte(r.payload)) {
			return nil, fmt.Errorf("%w: invalid JSON from engine", ErrRuntimeFault)
		}
		return json.RawMessage(r.payload), nil
	case "unserializable":
		return nil, fmt.Errorf("%w: %s", ErrNotSerializable, r.payload)
	default:
		if strings.Contains(r.payload, tooManyCallsMessage) {
			return nil, fmt.Errorf("%w: %s", ErrTooManyCalls, firstLine(r.payload))
		}
		return nil, classify(s.ctx, r.payload)
	}
}
