package upstream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

const taskUnsupportedError = `{"code":-32603,"message":"upstream returned an asynchronous task; tasks are not supported"}`

// sanitizeMessage rewrites one inbound JSON-RPC message around go-sdk v1.8.0 interop gaps:
//   - an input_required result keeps only its marker: the SDK fails to decode some input
//     requests (roots/list without params, seen in server-everything) and the gateway never
//     fulfils them anyway, so the call surfaces as ErrInputRequired instead of a decode error;
//   - a task result (the SDK has no tasks support) becomes a JSON-RPC error.
//
// Anything else is returned unchanged with changed false.
func sanitizeMessage(msg []byte) (out []byte, changed bool) {
	if !bytes.Contains(msg, []byte("resultType")) ||
		(!bytes.Contains(msg, []byte("input_required")) && !bytes.Contains(msg, []byte("task"))) {
		return msg, false
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(msg, &envelope) != nil {
		return msg, false
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(envelope["result"], &result) != nil {
		return msg, false
	}
	var resultType string
	_ = json.Unmarshal(result["resultType"], &resultType)
	switch resultType {
	case "input_required":
		delete(result, "inputRequests")
		b, err := json.Marshal(result)
		if err != nil {
			return msg, false
		}
		envelope["result"] = b
	case "task":
		delete(envelope, "result")
		envelope["error"] = json.RawMessage(taskUnsupportedError)
	default:
		return msg, false
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		return msg, false
	}
	return b, true
}

// sanitizeLine applies sanitizeMessage to a newline-delimited JSON frame (stdio).
func sanitizeLine(line []byte) []byte {
	out, changed := sanitizeMessage(bytes.TrimRight(line, "\r\n"))
	if !changed {
		return line
	}
	return append(out, '\n')
}

// sanitizeEventLine applies sanitizeMessage to the data field of a server-sent event line.
func sanitizeEventLine(line []byte) []byte {
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return line
	}
	out, changed := sanitizeMessage(bytes.TrimPrefix(bytes.TrimRight(payload, "\r\n"), []byte(" ")))
	if !changed {
		return line
	}
	return append(append([]byte("data: "), out...), '\n')
}

// lineFilter reads newline-delimited frames, bounds each frame to maxLine bytes and rewrites
// it with transform. A frame over the bound fails the stream with ErrResponseTooLarge.
type lineFilter struct {
	src        *bufio.Reader
	closer     io.Closer
	maxLine    int
	transform  func([]byte) []byte
	onTooLarge func()

	pending []byte
	err     error
}

func newLineFilter(rc io.ReadCloser, maxLine int, transform func([]byte) []byte, onTooLarge func()) *lineFilter {
	return &lineFilter{
		src: bufio.NewReaderSize(rc, 64<<10), closer: rc, maxLine: maxLine,
		transform: transform, onTooLarge: onTooLarge,
	}
}

func (f *lineFilter) Read(p []byte) (int, error) {
	for len(f.pending) == 0 {
		if f.err != nil {
			return 0, f.err
		}
		line, err := f.readLine()
		if len(line) > 0 {
			f.pending = f.transform(line)
		}
		f.err = err
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

func (f *lineFilter) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := f.src.ReadSlice('\n')
		line = append(line, chunk...)
		if f.maxLine > 0 && len(line) > f.maxLine {
			if f.onTooLarge != nil {
				f.onTooLarge()
			}
			return nil, ErrResponseTooLarge
		}
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

func (f *lineFilter) Close() error { return f.closer.Close() }

// jsonBody buffers a whole application/json response (already size-bounded by the caller) and
// sanitizes it on the first read.
type jsonBody struct {
	rc io.ReadCloser
	// limit bounds the buffered bytes (<= 0: unbounded); onExceeded is called when it is passed.
	limit      int64
	onExceeded func()
	once       sync.Once
	r          *bytes.Reader
	err        error
}

func (b *jsonBody) Read(p []byte) (int, error) {
	b.once.Do(func() {
		var src io.Reader = b.rc
		if b.limit > 0 {
			src = io.LimitReader(b.rc, b.limit+1)
		}
		data, err := io.ReadAll(src)
		if b.limit > 0 && int64(len(data)) > b.limit {
			if b.onExceeded != nil {
				b.onExceeded()
			}
			data, err = nil, ErrResponseTooLarge
		}
		out, _ := sanitizeMessage(bytes.TrimSpace(data))
		b.r, b.err = bytes.NewReader(out), err
	})
	if n, err := b.r.Read(p); n > 0 || b.err == nil {
		return n, err
	}
	return 0, b.err
}

func (b *jsonBody) Close() error { return b.rc.Close() }
