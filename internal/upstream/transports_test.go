package upstream_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

type matrixCase struct {
	name     string
	endpoint func(t *testing.T) upstream.Endpoint
	want     upstream.Detection
}

func matrixCases() []matrixCase {
	httpEndpoint := func(kind fakeKind, path string) func(t *testing.T) upstream.Endpoint {
		return func(t *testing.T) upstream.Endpoint {
			f := newHTTPFake(t, kind)
			return upstream.Endpoint{Transport: upstream.TransportAuto, URL: f.srv.URL + path}
		}
	}
	stdio := func(mode string) func(t *testing.T) upstream.Endpoint {
		return func(t *testing.T) upstream.Endpoint {
			spec := stdioSpec(t, mode, nil)
			return upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec}
		}
	}
	return []matrixCase{
		{"streamable modern stateless", httpEndpoint(fakeModern, "/mcp"), upstream.Detection{Transport: upstream.TransportStreamable, ProtocolVersion: modernVersion}},
		{"streamable legacy stateful", httpEndpoint(fakeLegacy, "/mcp"), upstream.Detection{Transport: upstream.TransportStreamable, ProtocolVersion: legacyVersion}},
		{"legacy sse at /sse", httpEndpoint(fakeSSE, "/sse"), upstream.Detection{Transport: upstream.TransportSSE, ProtocolVersion: legacyVersion}},
		{"legacy sse found by fallback", httpEndpoint(fakeSSE, "/events"), upstream.Detection{Transport: upstream.TransportSSE, ProtocolVersion: legacyVersion}},
		{"stdio modern", stdio("modern"), upstream.Detection{Transport: upstream.TransportStdio, ProtocolVersion: modernVersion}},
		{"stdio legacy", stdio("legacy"), upstream.Detection{Transport: upstream.TransportStdio, ProtocolVersion: legacyVersion}},
	}
}

func TestTransportMatrix(t *testing.T) {
	for _, tc := range matrixCases() {
		t.Run(tc.name, func(t *testing.T) {
			res := newTestResolver()
			key := res.add("key://"+tc.name, tc.endpoint(t))
			c := upstream.New(upstream.WithResolver(res), upstream.WithTimeout(10*time.Second))
			defer c.Close()
			ctx := context.Background()

			tools, _, err := c.ListTools(ctx, key, nil)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if !hasTool(tools, "echo") || !hasTool(tools, "media") {
				t.Fatalf("tools = %d, echo/media missing", len(tools))
			}
			if got := res.detection(key); got != tc.want {
				t.Fatalf("detection = %+v, want %+v", got, tc.want)
			}
			if got := callText(t, c, ctx, key, "echo", `{"text":"hi there"}`); got != "hi there" {
				t.Fatalf("echo = %q", got)
			}
			assertMediaMapped(t, c, key)
			var te *upstream.ToolError
			if _, err := c.CallTool(ctx, key, nil, "fail", nil); !errors.As(err, &te) || te.Message != "boom" {
				t.Fatalf("fail: %v", err)
			}
			assertServerRequestsDeclined(t, c, key)
		})
	}
}

func hasTool(tools []upstream.Tool, name string) bool {
	for _, tl := range tools {
		if tl.Name == name {
			return true
		}
	}
	return false
}

func assertMediaMapped(t *testing.T, c *upstream.Client, key string) {
	t.Helper()
	res, err := c.CallTool(context.Background(), key, nil, "media", nil)
	if err != nil {
		t.Fatalf("media: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal(res.Value, &items); err != nil || len(items) != 4 {
		t.Fatalf("media value = %s (%v)", res.Value, err)
	}
	if items[0]["type"] != "audio" || items[0]["data"] != "UklGRg==" {
		t.Errorf("audio = %v", items[0])
	}
	if items[1]["type"] != "resource_link" || items[1]["uri"] != "file:///a.txt" || items[1]["size"] != float64(42) {
		t.Errorf("resource_link = %v", items[1])
	}
	text, _ := items[2]["resource"].(map[string]any)
	blob, _ := items[3]["resource"].(map[string]any)
	if text["text"] != "inline" || blob["blob"] != "AAEC" {
		t.Errorf("embedded = %v / %v", text, blob)
	}
}

// Server->client requests (elicitation, sampling, roots) are declined without hanging.
func assertServerRequestsDeclined(t *testing.T, c *upstream.Client, key string) {
	t.Helper()
	for _, tool := range []string{"elicit", "sample", "roots"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		res, err := c.CallTool(ctx, key, nil, tool, nil)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s hung", tool)
		}
		if err == nil && strings.Contains(string(res.Value), "accepted") && tool != "roots" {
			t.Fatalf("%s was fulfilled: %s", tool, res.Value)
		}
	}
}

func TestDetectionIsRememberedAndReused(t *testing.T) {
	f := newHTTPFake(t, fakeLegacy)
	res := newTestResolver()
	stored := upstream.Detection{Transport: upstream.TransportStreamable, ProtocolVersion: "2025-06-18"}
	key := res.add(f.srv.URL, upstream.Endpoint{Transport: upstream.TransportAuto, URL: f.srv.URL, Detected: stored})
	c := upstream.New(upstream.WithResolver(res))
	defer c.Close()
	if got := callText(t, c, context.Background(), key, "echo", `{"text":"x"}`); got != "x" {
		t.Fatalf("echo = %q", got)
	}
	// The stored era was used as is: nothing new to record.
	if got := res.detection(key); got != (upstream.Detection{}) {
		t.Fatalf("re-probed despite stored detection: %+v", got)
	}
}

func TestStaleDetectionIsReprobed(t *testing.T) {
	f := newHTTPFake(t, fakeSSE)
	res := newTestResolver()
	stale := upstream.Detection{Transport: upstream.TransportStreamable, ProtocolVersion: modernVersion}
	key := res.add(f.srv.URL, upstream.Endpoint{Transport: upstream.TransportAuto, URL: f.srv.URL, Detected: stale})
	c := upstream.New(upstream.WithResolver(res))
	defer c.Close()
	if got := callText(t, c, context.Background(), key, "echo", `{"text":"x"}`); got != "x" {
		t.Fatalf("echo = %q", got)
	}
	if got := res.detection(key); got.Transport != upstream.TransportSSE {
		t.Fatalf("detection = %+v", got)
	}
}

// go-sdk v1.8.0 fails to decode a modern roots inputRequest without params; it must surface as
// ErrInputRequired. A task result (unsupported by the SDK) must be a clean error.
func TestModernInputRequiredAndTasks(t *testing.T) {
	f := newHTTPFake(t, fakeModern)
	c := upstream.New()
	defer c.Close()
	_, err := c.CallTool(context.Background(), f.srv.URL, nil, "needs_roots", nil)
	if !errors.Is(err, upstream.ErrInputRequired) {
		t.Fatalf("needs_roots: %v", err)
	}
	_, err = c.CallTool(context.Background(), f.srv.URL, nil, "as_task", nil)
	if err == nil || !strings.Contains(err.Error(), "tasks are not supported") {
		t.Fatalf("as_task: %v", err)
	}
	// The session survives both.
	if got := callText(t, c, context.Background(), f.srv.URL, "echo", `{"text":"ok"}`); got != "ok" {
		t.Fatalf("echo after = %q", got)
	}
}

func TestStdioEnvironmentIsExplicit(t *testing.T) {
	t.Setenv("GATEWAY_ONLY_SECRET", "must-not-leak")
	res := newTestResolver()
	spec := stdioSpec(t, "modern", map[string]string{"API_TOKEN": "tok-123"})
	key := res.add("stdio://env", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	c := upstream.New(upstream.WithResolver(res))
	defer c.Close()
	got := callText(t, c, context.Background(), key, "env", "{}")
	if got != `["API_TOKEN","`+stdioModeEnv+`"]` {
		t.Fatalf("server environment = %s", got)
	}
}

func TestStdioRejectsInvalidSpec(t *testing.T) {
	res := newTestResolver()
	spec := upstream.StdioSpec{Launcher: upstream.LauncherHost, Command: "relative/bin"}
	key := res.add("stdio://bad", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	c := upstream.New(upstream.WithResolver(res))
	defer c.Close()
	if _, err := c.CallTool(context.Background(), key, nil, "echo", nil); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownKeyWithoutResolver(t *testing.T) {
	c := upstream.New()
	defer c.Close()
	_, err := c.CallTool(context.Background(), "stdio://whatever", nil, "echo", nil)
	if !errors.Is(err, upstream.ErrUnknownEndpoint) {
		t.Fatalf("err = %v", err)
	}
}
