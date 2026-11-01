package upstream_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// stdioModeEnv makes the test binary act as a stdio MCP server ("modern" or "legacy").
const stdioModeEnv = "UPSTREAM_TEST_STDIO_SERVER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(stdioModeEnv); mode != "" {
		s := newToolServer(mode == "legacy", nil)
		_ = s.Run(context.Background(), &mcp.StdioTransport{})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const (
	modernVersion = "2026-07-28"
	legacyVersion = "2025-11-25"
)

type echoArgs struct {
	Text string `json:"text"`
}

type sizeArgs struct {
	Bytes int `json:"bytes,omitempty"`
	Count int `json:"count,omitempty"`
}

// newToolServer builds the go-sdk server every fake serves. legacy rejects server/discover the
// way pre-2026 servers do, so the client falls back to initialize. initCount, when set, counts
// initialize handshakes.
func newToolServer(legacy bool, initCount *atomic.Int64) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if legacy && method == "server/discover" {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"}
			}
			if method == "initialize" && initCount != nil {
				initCount.Add(1)
			}
			return next(ctx, method, req)
		}
	})
	addContentTools(s)
	addProcessTools(s)
	addServerRequestTools(s)
	return s
}

func addContentTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, a echoArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: a.Text}}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "structured"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("img")}}},
			map[string]any{"ok": true}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "images"}, func(_ context.Context, _ *mcp.CallToolRequest, a sizeArgs) (*mcp.CallToolResult, any, error) {
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pictures"}}}
		for i := range max(a.Count, 1) {
			res.Content = append(res.Content, &mcp.ImageContent{MIMEType: "image/png", Data: bytes.Repeat([]byte{byte('a' + i)}, a.Bytes)})
		}
		return res, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "media"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		size := int64(42)
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.AudioContent{MIMEType: "audio/wav", Data: []byte("RIFF")},
			&mcp.ResourceLink{URI: "file:///a.txt", Name: "a.txt", MIMEType: "text/plain", Size: &size},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "mem://t", MIMEType: "text/plain", Text: "inline"}},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "mem://b", MIMEType: "application/octet-stream", Blob: []byte{0, 1, 2}}},
		}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "fail"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "slow"}, func(ctx context.Context, _ *mcp.CallToolRequest, a sizeArgs) (*mcp.CallToolResult, any, error) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(a.Bytes) * time.Millisecond):
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "late"}}}, nil, nil
	})
}

// addProcessTools expose the server process (meaningful over stdio).
func addProcessTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "pid"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprint(os.Getpid())}}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "env"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		names := []string{}
		for _, kv := range os.Environ() {
			name, _, _ := strings.Cut(kv, "=")
			names = append(names, name)
		}
		slices.Sort(names)
		b, _ := json.Marshal(names)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
	})
	// env_hash reveals a variable's value only as a hash: results are scrubbed of secrets.
	mcp.AddTool(s, &mcp.Tool{Name: "env_hash"}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Name string `json:"name"`
	}) (*mcp.CallToolResult, any, error) {
		sum := sha256.Sum256([]byte(os.Getenv(in.Name)))
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: hex.EncodeToString(sum[:])}}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "exit"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		os.Exit(3)
		return nil, nil, nil
	})
}

// addServerRequestTools issue legacy server->client requests the gateway must decline.
func addServerRequestTools(s *mcp.Server) {
	respond := func(err error) *mcp.CallToolResult {
		msg := "accepted"
		if err != nil {
			msg = "declined: " + err.Error()
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
	}
	mcp.AddTool(s, &mcp.Tool{Name: "elicit"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		_, err := req.Session.Elicit(ctx, &mcp.ElicitParams{Message: "name?", RequestedSchema: map[string]any{"type": "object"}})
		return respond(err), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "sample"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		_, err := req.Session.CreateMessage(ctx, &mcp.CreateMessageParams{MaxTokens: 5,
			Messages: []*mcp.SamplingMessage{{Role: "user", Content: &mcp.TextContent{Text: "hi"}}}})
		return respond(err), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "roots"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		res, err := req.Session.ListRoots(ctx, nil)
		if err == nil && len(res.Roots) > 0 {
			err = fmt.Errorf("client exposed %d roots", len(res.Roots))
		}
		return respond(err), nil, nil
	})
}

// httpFake serves one tool server over HTTP and records what it saw.
type httpFake struct {
	srv   *httptest.Server
	inits atomic.Int64

	mu      sync.Mutex
	auth    []string
	handler http.Handler
	legacy  bool
}

type fakeKind int

const (
	fakeModern fakeKind = iota // stateless Streamable HTTP, 2026-07-28
	fakeLegacy                 // stateful Streamable HTTP with session ids, legacy era only
	fakeSSE                    // legacy SSE (GET stream + POST endpoint)
)

func newHTTPFake(t *testing.T, kind fakeKind) *httpFake {
	t.Helper()
	f := &httpFake{legacy: kind != fakeModern}
	f.handler = f.build(kind)
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *httpFake) build(kind fakeKind) http.Handler {
	s := newToolServer(f.legacy, &f.inits)
	get := func(*http.Request) *mcp.Server { return s }
	if kind == fakeSSE {
		return mcp.NewSSEHandler(get, nil)
	}
	return mcp.NewStreamableHTTPHandler(get, &mcp.StreamableHTTPOptions{Stateless: kind == fakeModern})
}

// reset replaces the handler, dropping every server-side session (the next request with an old
// session id gets 404).
func (f *httpFake) reset(kind fakeKind) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = f.build(kind)
}

func (f *httpFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	h := f.handler
	f.mu.Unlock()
	if r.Method == http.MethodPost && cannedReply(w, r) {
		return
	}
	h.ServeHTTP(w, r)
}

func (f *httpFake) authSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.auth)
}

// cannedReplies reproduce real-server results the go-sdk server cannot produce. The modern
// roots inputRequest without params comes from server-everything (crashes the SDK decoder).
var cannedReplies = map[string]string{
	"needs_roots": `{"resultType":"input_required","inputRequests":{"r1":{"method":"roots/list"}}}`,
	"as_task":     `{"resultType":"task","task":{"taskId":"t1","status":"working"}}`,
}

func cannedReply(w http.ResponseWriter, r *http.Request) bool {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &msg) != nil || msg.Method != "tools/call" {
		return false
	}
	result, ok := cannedReplies[msg.Params.Name]
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, msg.ID, result)
	return true
}

// testResolver serves fixed endpoints and records detections.
type testResolver struct {
	mu       sync.Mutex
	eps      map[string]upstream.Endpoint
	recorded map[string]upstream.Detection
}

func newTestResolver() *testResolver {
	return &testResolver{eps: map[string]upstream.Endpoint{}, recorded: map[string]upstream.Detection{}}
}

func (r *testResolver) add(key string, ep upstream.Endpoint) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eps[key] = ep
	return key
}

func (r *testResolver) Resolve(_ context.Context, key string) (upstream.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ep, ok := r.eps[key]
	if !ok {
		return upstream.Endpoint{}, upstream.ErrUnknownEndpoint
	}
	return ep, nil
}

func (r *testResolver) RecordDetection(_ context.Context, key string, d upstream.Detection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recorded[key] = d
	return nil
}

func (r *testResolver) detection(key string) upstream.Detection {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recorded[key]
}

// stdioSpec runs this test binary as a stdio server through the host launcher.
func stdioSpec(t *testing.T, mode string, extraEnv map[string]string) upstream.StdioSpec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{stdioModeEnv: mode}
	for k, v := range extraEnv {
		env[k] = v
	}
	return upstream.StdioSpec{Launcher: upstream.LauncherHost, Command: exe, Env: env}
}

func callText(t *testing.T, c *upstream.Client, ctx context.Context, key, tool string, args string) string {
	t.Helper()
	res, err := c.CallTool(ctx, key, nil, tool, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var s string
	if err := json.Unmarshal(res.Value, &s); err != nil {
		return string(res.Value)
	}
	return s
}
