package upstream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConvertResult(t *testing.T) {
	img := func(n int) *mcp.ImageContent {
		return &mcp.ImageContent{MIMEType: "image/png", Data: bytes.Repeat([]byte{'x'}, n)}
	}
	cases := []struct {
		name    string
		res     *mcp.CallToolResult
		limit   int64
		want    string // exact Value, when set
		check   func(t *testing.T, items []map[string]any)
		wantErr error
	}{
		{name: "empty", res: &mcp.CallToolResult{}, limit: 100, want: "null"},
		{name: "text json", res: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"a":1}`}}}, limit: 100, want: `{"a":1}`},
		{name: "text plain joined", res: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "a"}, &mcp.TextContent{Text: "b"}}}, limit: 100, want: `"a\nb"`},
		{
			name:  "structured preferred over content",
			res:   &mcp.CallToolResult{Content: []mcp.Content{img(3)}, StructuredContent: map[string]any{"ok": true}},
			limit: 100, want: `{"ok":true}`,
		},
		{name: "structured over limit", res: &mcp.CallToolResult{StructuredContent: map[string]any{"s": strings.Repeat("x", 200)}}, limit: 100, wantErr: ErrResponseTooLarge},
		{name: "text over limit", res: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 200)}}}, limit: 100, wantErr: ErrResponseTooLarge},
		{
			name: "image fits", res: &mcp.CallToolResult{Content: []mcp.Content{img(3)}}, limit: 1000,
			check: func(t *testing.T, items []map[string]any) {
				if items[0]["type"] != "image" || items[0]["data"] != "eHh4" || items[0]["mimeType"] != "image/png" {
					t.Fatalf("items = %v", items)
				}
			},
		},
		{
			name: "oversized image replaced", res: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "t"}, img(3000)}}, limit: 1000,
			check: func(t *testing.T, items []map[string]any) {
				if items[0]["text"] != "t" || items[1]["data"] != nil || !strings.Contains(items[1]["omitted"].(string), "4000 base64 bytes") {
					t.Fatalf("items = %v", items)
				}
			},
		},
		{
			name: "first blobs kept until the budget runs out", res: &mcp.CallToolResult{Content: []mcp.Content{img(300), img(300), img(300)}}, limit: 1100,
			check: func(t *testing.T, items []map[string]any) {
				kept := 0
				for _, it := range items {
					if it["data"] != nil {
						kept++
					}
				}
				if kept != 2 || items[2]["omitted"] == nil {
					t.Fatalf("kept %d: %v", kept, items)
				}
			},
		},
		{
			name: "blob resource replaced", limit: 300,
			res: &mcp.CallToolResult{Content: []mcp.Content{&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "mem://b", Blob: make([]byte, 600)}}}},
			check: func(t *testing.T, items []map[string]any) {
				r := items[0]["resource"].(map[string]any)
				if r["blob"] != nil || r["uri"] != "mem://b" || items[0]["omitted"] == nil {
					t.Fatalf("items = %v", items)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertResult(tc.res, tc.limit)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(got.Value)) > tc.limit {
				t.Fatalf("value of %d bytes exceeds the limit %d", len(got.Value), tc.limit)
			}
			if tc.want != "" && string(got.Value) != tc.want {
				t.Fatalf("value = %s, want %s", got.Value, tc.want)
			}
			if tc.check != nil {
				var items []map[string]any
				if err := json.Unmarshal(got.Value, &items); err != nil {
					t.Fatalf("value %s: %v", got.Value, err)
				}
				tc.check(t, items)
			}
		})
	}
}

// Documents the go-sdk v1.8.0 decoding gap sanitizeMessage works around.
func TestSanitizeMessageInputRequiredGap(t *testing.T) {
	raw := []byte(`{"resultType":"input_required","inputRequests":{"r1":{"method":"roots/list"}}}`)
	var direct mcp.CallToolResult
	if err := json.Unmarshal(raw, &direct); err == nil {
		t.Log("the SDK now decodes roots/list without params; the workaround may be dropped")
	}
	msg := []byte(`{"jsonrpc":"2.0","id":3,"result":` + string(raw) + `}`)
	out, changed := sanitizeMessage(msg)
	if !changed {
		t.Fatal("input_required message not rewritten")
	}
	var envelope struct{ Result json.RawMessage }
	if err := json.Unmarshal(out, &envelope); err != nil {
		t.Fatal(err)
	}
	var res mcp.CallToolResult
	if err := json.Unmarshal(envelope.Result, &res); err != nil || !res.NeedsInput() {
		t.Fatalf("rewritten result: needsInput=%v err=%v", res.NeedsInput(), err)
	}
	if plain := []byte(`{"jsonrpc":"2.0","id":1,"result":{"resultType":"complete","content":[]}}`); !bytes.Equal(must(sanitizeMessage(plain)), plain) {
		t.Fatal("complete result changed")
	}
}

func must(b []byte, _ bool) []byte { return b }

func TestLineFilterBoundsFrames(t *testing.T) {
	tooLarge := false
	src := io.NopCloser(strings.NewReader("short\n" + strings.Repeat("y", 100) + "\n"))
	f := newLineFilter(src, 50, sanitizeLine, func() { tooLarge = true })
	got, err := io.ReadAll(f)
	if string(got) != "short\n" || !errors.Is(err, ErrResponseTooLarge) || !tooLarge {
		t.Fatalf("got %q err=%v flagged=%v", got, err, tooLarge)
	}
}

func TestSanitizeEventLine(t *testing.T) {
	line := []byte(`data: {"jsonrpc":"2.0","id":1,"result":{"resultType":"input_required","inputRequests":{"a":{"method":"roots/list"}}}}` + "\n")
	out := sanitizeEventLine(line)
	if bytes.Contains(out, []byte("inputRequests")) || !bytes.HasPrefix(out, []byte("data: ")) || !bytes.HasSuffix(out, []byte("\n")) {
		t.Fatalf("out = %q", out)
	}
	if other := []byte("event: message\n"); !bytes.Equal(sanitizeEventLine(other), other) {
		t.Fatal("non-data line changed")
	}
}

func TestDockerArgsAreHardened(t *testing.T) {
	spec := StdioSpec{Launcher: LauncherDocker, Image: "mcp/filesystem:1", Args: []string{"/data"}, Env: map[string]string{"TOKEN": "s3cret"}}
	args := dockerArgs(spec, "mcpgw-x")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm -i", "--network none", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges",
		"--memory 512m", "--pids-limit 256", "--user 65534:65534", "--env TOKEN mcp/filesystem:1 /data",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, "s3cret") {
		t.Fatal("secret value on the docker argv")
	}
	spec.Network = true
	if slices.Contains(dockerArgs(spec, "n"), "none") {
		t.Fatal("network requested but still disabled")
	}
}

func TestStdioSpecValidate(t *testing.T) {
	cases := []struct {
		name string
		spec StdioSpec
		ok   bool
	}{
		{"docker", StdioSpec{Launcher: LauncherDocker, Image: "ghcr.io/x/y@sha256:abc"}, true},
		{"docker flag injection", StdioSpec{Launcher: LauncherDocker, Image: "--privileged"}, false},
		{"docker reserved env", StdioSpec{Launcher: LauncherDocker, Image: "x", Env: map[string]string{"DOCKER_HOST": "tcp://evil"}}, false},
		{"host absolute", StdioSpec{Launcher: LauncherHost, Command: "/usr/bin/node", Env: map[string]string{"PATH": "/usr/bin"}}, true},
		{"host relative", StdioSpec{Launcher: LauncherHost, Command: "node"}, false},
		{"host with image", StdioSpec{Launcher: LauncherHost, Command: "/bin/x", Image: "y"}, false},
		{"bad env name", StdioSpec{Launcher: LauncherHost, Command: "/bin/x", Env: map[string]string{"A=B": "c"}}, false},
		{"nul in arg", StdioSpec{Launcher: LauncherHost, Command: "/bin/x", Args: []string{"a\x00b"}}, false},
		{"unknown launcher", StdioSpec{Launcher: "shell", Command: "/bin/sh"}, false},
	}
	for _, tc := range cases {
		if err := tc.spec.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestCredentialID(t *testing.T) {
	a, b := credentialID(BearerToken("one")), credentialID(BearerToken("two"))
	if a == b || a == "" || credentialID(nil) != "" || a != credentialID(BearerToken("one")) {
		t.Fatalf("ids: %q %q", a, b)
	}
	if strings.Contains(a, "one") {
		t.Fatal("credential id leaks the token")
	}
}
