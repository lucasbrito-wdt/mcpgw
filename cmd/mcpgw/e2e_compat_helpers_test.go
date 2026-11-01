package main

// Helpers of the compat matrix (e2e_compat_test.go). Everything here is inert unless
// MCPGW_COMPAT=1: real-server cases need node, npm packages, docker and network.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const (
	compatEnvVar = "MCPGW_COMPAT"

	pkgEverything = "server-everything"
	pkgFilesystem = "server-filesystem"
	pkgMemory     = "server-memory"

	pkgEverythingVersion = "2.0.1"
	pkgFilesystemVersion = "2.0.1"
	pkgMemoryVersion     = "2.0.0"

	compatFSImage = "mcpgw-compat-fs:2.0.1"
)

// requireCompat skips the test unless the compat matrix was asked for.
func requireCompat(t *testing.T) {
	t.Helper()
	if os.Getenv(compatEnvVar) != "1" {
		t.Skip("compat matrix is opt-in: set " + compatEnvVar + "=1")
	}
}

// ---------------------------------------------------------------------------------------
// Real servers: npm packages resolved once into a cache dir, never npx per call.

var (
	npmOnce sync.Once
	npmDir  string
	npmErr  error
)

// compatNode returns the absolute path of node or skips.
func compatNode(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed: " + err.Error())
	}
	return p
}

// compatPackageEntry resolves the pinned official servers once per process into
// <user cache>/mcpgw-compat/npm and returns the dist/index.js of pkg. It skips when node, npm
// or the network are missing.
func compatPackageEntry(t *testing.T, pkg string) string {
	t.Helper()
	compatNode(t)
	npmOnce.Do(func() { npmDir, npmErr = installCompatPackages() })
	if npmErr != nil {
		t.Skip("cannot resolve the npm packages (need npm and network on the first run): " + npmErr.Error())
	}
	return filepath.Join(npmDir, "node_modules", "@modelcontextprotocol", pkg, "dist", "index.js")
}

func installCompatPackages() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "mcpgw-compat", "npm")
	want := map[string]string{pkgEverything: pkgEverythingVersion, pkgFilesystem: pkgFilesystemVersion, pkgMemory: pkgMemoryVersion}
	if installedVersionsMatch(dir, want) {
		return dir, nil
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	args := []string{"install", "--prefix", dir, "--no-audit", "--no-fund", "--ignore-scripts", "--prefer-offline"}
	for pkg, v := range want {
		args = append(args, "@modelcontextprotocol/"+pkg+"@"+v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, npm, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("npm install: %v: %s", err, tail(string(out), 400))
	}
	if !installedVersionsMatch(dir, want) {
		return "", fmt.Errorf("npm install finished but the pinned versions are not in %s", dir)
	}
	return dir, nil
}

func installedVersionsMatch(dir string, want map[string]string) bool {
	for pkg, v := range want {
		raw, err := os.ReadFile(filepath.Join(dir, "node_modules", "@modelcontextprotocol", pkg, "package.json"))
		if err != nil {
			return false
		}
		var meta struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &meta) != nil || meta.Version != v {
			return false
		}
	}
	return true
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// startNodeHTTP starts server-everything in an HTTP mode ("sse" or "streamableHttp") on a free
// port with a sanitized environment, waits until it listens, and returns the port. The process
// group is killed on cleanup.
func startNodeHTTP(t *testing.T, mode string) int {
	t.Helper()
	node, entry := compatNode(t), compatPackageEntry(t, pkgEverything)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	logs := &syncBuffer{}
	cmd := exec.Command(node, entry, mode)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), fmt.Sprintf("PORT=%d", port)}
	cmd.Stdout, cmd.Stderr = logs, logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server-everything %s: %v", mode, err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-exited
		}
		if t.Failed() {
			t.Logf("server-everything %s log:\n%s", mode, logs.String())
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			t.Fatalf("server-everything %s exited early:\n%s", mode, logs.String())
		default:
		}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			_ = c.Close()
			return port
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server-everything %s did not listen on %d in 30s:\n%s", mode, port, logs.String())
	return 0
}

// requireDockerImage skips without a docker daemon, then builds (once, cached by tag) a node
// image holding the pinned filesystem server and returns its tag.
func requireDockerImage(t *testing.T) string {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, docker, "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skip("docker daemon unavailable: " + tail(strings.TrimSpace(string(out)), 200))
	}
	if exec.CommandContext(ctx, docker, "image", "inspect", compatFSImage).Run() == nil {
		return compatFSImage
	}
	dir := t.TempDir()
	dockerfile := "FROM node:22-alpine\n" +
		"RUN npm install --prefix /app --no-audit --no-fund --ignore-scripts @modelcontextprotocol/" + pkgFilesystem + "@" + pkgFilesystemVersion + "\n" +
		"ENTRYPOINT [\"node\", \"/app/node_modules/@modelcontextprotocol/" + pkgFilesystem + "/dist/index.js\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, docker, "build", "-q", "-t", compatFSImage, dir).CombinedOutput(); err != nil {
		t.Skip("cannot build the node image (needs network for node:22-alpine and npm): " + tail(string(out), 400))
	}
	return compatFSImage
}

// requireNoManagedContainers fails when a docker container started by the launcher is still up.
func requireNoManagedContainers(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-q", "--filter", "label=io.mcpgw.managed=true").Output()
	if err != nil {
		t.Logf("docker ps: %v", err)
		return
	}
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		t.Errorf("launcher containers still running after shutdown: %v", ids)
		_ = exec.Command("docker", append([]string{"rm", "-f"}, ids...)...).Run()
	}
}

// ---------------------------------------------------------------------------------------
// Gateway under test: the production serve path over a throwaway database. Unlike newE2EEnv
// the servers come from the caller and the admin lister resolves stdio servers (like the CLI).

type compatEnv struct {
	*e2eEnv
	slug     string
	extraEnv map[string]string
	level    string // consent level chosen at login (default write)
	cs       *mcp.ClientSession
}

// newCompatEnv boots a gateway with one upstream registered by addServer (admin CLI flags of
// "server add", without --slug), synced, with ceiling write for the test user.
func newCompatEnv(t *testing.T, slug string, addServer ...string) *compatEnv {
	t.Helper()
	return newCompatEnvWith(t, slug, nil, addServer...)
}

func newCompatEnvWith(t *testing.T, slug string, extraEnv map[string]string, addServer ...string) *compatEnv {
	t.Helper()
	pool := storetest.New(t)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	cfg := &config.Config{DatabaseURL: pool.Config().ConnString(), ListenAddr: ln.Addr().String(), PublicURL: base, MasterKey: key}
	v, err := vault.New(key, vaultKeyID)
	if err != nil {
		t.Fatal(err)
	}
	lister := upstream.New(upstream.WithResolver(upstream.NewDBResolver(pool, v)), upstream.WithTerminateGrace(time.Second))
	t.Cleanup(lister.Close)

	out := &bytes.Buffer{}
	ce := &compatEnv{slug: slug, extraEnv: extraEnv, level: "write", e2eEnv: &e2eEnv{t: t, base: base, adminOut: out}}
	ce.db = &adminEnv{
		pool: pool, cfg: cfg, out: out, in: strings.NewReader(""), lister: lister,
		getenv: func(k string) string {
			if k == envAdminPassword {
				return e2ePassword
			}
			return extraEnv[k]
		},
	}
	ce.admin("user create --email " + e2eEmail)
	ce.adminArgs(append([]string{"server", "add", "--slug", slug}, addServer...)...)
	ce.adminArgs("catalog", "sync", "--slug", slug)
	ce.admin("ceiling set --email " + e2eEmail + " --slug " + slug + " --level write")
	ce.lookupCompatIDs()

	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("gateway log:\n%s", logs.String())
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, cfg, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})), ln)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve returned %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("serve did not shut down")
		}
	})
	waitHealthy(t, base)
	return ce
}

// grant raises the ceiling and the consent level used at login (e.g. destructive for write_file).
func (e *compatEnv) grant(level string) {
	e.t.Helper()
	e.admin("ceiling set --email " + e2eEmail + " --slug " + e.slug + " --level " + level)
	e.level = level
}

// adminArgs runs one admin command with explicit arguments (values may contain spaces).
func (e *compatEnv) adminArgs(args ...string) string {
	e.t.Helper()
	e.adminOut.Reset()
	if err := adminDispatch(context.Background(), e.db, args); err != nil {
		e.t.Fatalf("admin %q: %v", strings.Join(args, " "), err)
	}
	return e.adminOut.String()
}

// adminErr is adminArgs for commands that are expected to fail.
func (e *compatEnv) adminErr(args ...string) error {
	e.t.Helper()
	e.adminOut.Reset()
	return adminDispatch(context.Background(), e.db, args)
}

func (e *compatEnv) lookupCompatIDs() {
	e.t.Helper()
	ctx := context.Background()
	if err := e.db.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, e2eEmail).Scan(&e.userID); err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.pool.QueryRow(ctx, `SELECT id::text FROM upstream_servers WHERE slug = $1`, e.slug).Scan(&e.serverID); err != nil {
		e.t.Fatal(err)
	}
}

// login runs DCR, PKCE, login and consent write, and opens the MCP client session.
func (e *compatEnv) login() *mcp.ClientSession {
	e.t.Helper()
	oc := newOAuthClient(e.t, e.base)
	oc.register()
	resource := e.base + "/mcp"
	p := newPKCE(e.t)
	params := oc.authorize(p, resource, e.serverID, e.level)
	reply := oc.exchange(params.Get("code"), p, resource)
	if reply.status != http.StatusOK {
		e.t.Fatalf("token exchange: %d %s", reply.status, reply.body)
	}
	e.cs = connectMCP(e.t, e.base, decodeToken(e.t, reply).AccessToken)
	return e.cs
}

// search runs the search tool and returns its text.
func (e *compatEnv) search(query string) string {
	e.t.Helper()
	res := callTool(e.t, e.cs, "search", map[string]any{"query": query})
	if res.IsError {
		e.t.Fatalf("search %q failed: %s", query, resultText(res))
	}
	return resultText(res)
}

// run executes code and returns the raw JSON the script returned (fails when it errors).
func (e *compatEnv) run(code string) json.RawMessage {
	e.t.Helper()
	res := runScript(e.t, e.cs, code)
	if res.IsError {
		e.t.Fatalf("script failed: %s\n%s", resultText(res), code)
	}
	return json.RawMessage(resultText(res))
}

// runDeadline runs code and fails the test when it does not finish in d (hang detector).
func (e *compatEnv) runDeadline(code string, d time.Duration) (*mcp.CallToolResult, time.Duration) {
	e.t.Helper()
	type outcome struct {
		res *mcp.CallToolResult
		err error
	}
	ch := make(chan outcome, 1)
	start := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		res, err := e.cs.CallTool(ctx, &mcp.CallToolParams{Name: "execute", Arguments: map[string]any{"code": code}})
		ch <- outcome{res, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			e.t.Fatalf("execute did not return a result within %v: %v", d, o.err)
		}
		return o.res, time.Since(start)
	case <-time.After(d + 5*time.Second):
		e.t.Fatalf("execute hung past %v", d)
		return nil, 0
	}
}

// detected returns the transport and protocol version the gateway stored for the server.
func (e *compatEnv) detected() (transport, version string) {
	e.t.Helper()
	var tr, ver *string
	err := e.db.pool.QueryRow(context.Background(),
		`SELECT detected_transport, detected_protocol_version FROM upstream_servers WHERE id = $1::uuid`, e.serverID).Scan(&tr, &ver)
	if err != nil {
		e.t.Fatal(err)
	}
	return deref(tr), deref(ver)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// toolNames lists the active tool names of the server as the catalog holds them.
func (e *compatEnv) toolNames() map[string]bool {
	e.t.Helper()
	rows, err := e.db.pool.Query(context.Background(),
		`SELECT name FROM tools WHERE server_id = $1::uuid AND status = 'active'`, e.serverID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			e.t.Fatal(err)
		}
		names[n] = true
	}
	return names
}

// auditCount counts allow events of one tool of the server.
func (e *compatEnv) auditCount(tool, decision string) int {
	e.t.Helper()
	var n int
	err := e.db.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE server_slug = $1 AND tool_name = $2 AND decision = $3 AND user_id = $4::uuid`,
		e.slug, tool, decision, e.userID).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// assertAudited waits until every tool has at least one allow event (reads are batched) and
// checks that the scripts that ran are recorded as executions of the user.
func (e *compatEnv) assertAudited(tools ...string) {
	e.t.Helper()
	for _, tool := range tools {
		eventually(e.t, "audit allow event of "+tool, func() bool { return e.auditCount(tool, "allow") > 0 })
	}
	var bad int
	err := e.db.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events
		WHERE server_slug = $1 AND (user_id <> $2::uuid OR args_hash = '' OR client_id = '')`, e.slug, e.userID).Scan(&bad)
	if err != nil || bad != 0 {
		e.t.Errorf("%d audit events miss user/client/args_hash (%v)", bad, err)
	}
	var execs int
	err = e.db.pool.QueryRow(context.Background(), `SELECT count(*) FROM executions WHERE kind = 'execute' AND user_id = $1::uuid`, e.userID).Scan(&execs)
	if err != nil || execs == 0 {
		e.t.Errorf("executions rows = %d (%v), want at least 1", execs, err)
	}
}

// decode unmarshals raw script output into v.
func decode(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

// ---------------------------------------------------------------------------------------
// In-process upstream servers (go-sdk) for the stateless 2026-07-28 case, pending_approval and
// the legacy server->client request path.

// richUpstream is a stateless Streamable HTTP server with one tool per result kind. Its
// "get_item" definition can be changed at runtime (a rug pull).
type richUpstream struct {
	srv *httptest.Server
	s   *mcp.Server
	fakeUpstream
}

var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}

func newRichUpstream(t *testing.T) *richUpstream {
	t.Helper()
	u := &richUpstream{s: mcp.NewServer(&mcp.Implementation{Name: "rich-upstream", Version: "1"}, nil)}
	u.addGetItem("Read an item")
	obj := map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}}
	u.add("echo_text", "Echo a message as text", obj, func(args map[string]any) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprint(args["message"])}}}
	})
	u.add("get_structured", "Return structured content", obj, func(map[string]any) *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "text twin"}},
			StructuredContent: map[string]any{"city": "Lisbon", "temp": 21.5, "tags": []any{"a", "b"}},
		}
	})
	u.add("get_image", "Return an image", obj, func(map[string]any) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "here is the image"},
			&mcp.ImageContent{Data: tinyPNG, MIMEType: "image/png"},
		}}
	})
	u.add("get_mixed", "Return resource link and embedded resource", obj, func(map[string]any) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.ResourceLink{URI: "file:///x.txt", Name: "x"},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///y.txt", MIMEType: "text/plain", Text: "body"}},
		}}
	})
	u.add("tool_is_error", "Always reports isError", obj, func(map[string]any) *mcp.CallToolResult {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "disk on fire"}}}
	})
	u.s.AddTool(&mcp.Tool{Name: "protocol_error", Description: "Fails with a JSON-RPC error", InputSchema: obj},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, fmt.Errorf("kaboom from the server")
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return u.s }, &mcp.StreamableHTTPOptions{Stateless: true})
	u.srv = httptest.NewServer(h)
	t.Cleanup(u.srv.Close)
	return u
}

func (u *richUpstream) add(name, desc string, schema map[string]any, fn func(args map[string]any) *mcp.CallToolResult) {
	u.s.AddTool(&mcp.Tool{Name: name, Description: desc, InputSchema: schema},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			u.record(req.Params.Name)
			var args map[string]any
			_ = json.Unmarshal(req.Params.Arguments, &args)
			return fn(args), nil
		})
}

// addGetItem (re)registers get_item; calling it again with another description is the rug pull.
func (u *richUpstream) addGetItem(desc string) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}
	u.add("get_item", desc, schema, func(args map[string]any) *mcp.CallToolResult {
		body, _ := json.Marshal(map[string]any{"id": args["id"], "desc": desc})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}
	})
}

// legacyAskUpstream is a stateful server that downgrades itself to the 2024-2025 era (server/
// discover is answered with -32601) and whose tools send requests to the client: sampling,
// elicitation and roots. The gateway declares no client capabilities and must decline them.
type legacyAskUpstream struct {
	srv *httptest.Server
	mu  sync.Mutex
	// replies records what each server->client request returned: tool -> error text ("" = ok).
	replies map[string]string
}

func newLegacyAskUpstream(t *testing.T) *legacyAskUpstream {
	t.Helper()
	u := &legacyAskUpstream{replies: map[string]string{}}
	s := mcp.NewServer(&mcp.Implementation{Name: "legacy-ask", Version: "1"}, nil)
	obj := map[string]any{"type": "object", "properties": map[string]any{}}
	ask := func(name string, call func(ctx context.Context, ss *mcp.ServerSession) error) {
		s.AddTool(&mcp.Tool{Name: name, Description: "Sends a " + name + " request to the client", InputSchema: obj},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				err := call(ctx, req.Session)
				msg := ""
				if err != nil {
					msg = err.Error()
				}
				u.mu.Lock()
				u.replies[name] = msg
				u.mu.Unlock()
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "client answered: " + msg}}}, nil
			})
	}
	ask("ask_sampling", func(ctx context.Context, ss *mcp.ServerSession) error {
		_, err := ss.CreateMessage(ctx, &mcp.CreateMessageParams{
			MaxTokens: 10, Messages: []*mcp.SamplingMessage{{Role: "user", Content: &mcp.TextContent{Text: "hi"}}},
		})
		return err
	})
	ask("ask_elicit", func(ctx context.Context, ss *mcp.ServerSession) error {
		_, err := ss.Elicit(ctx, &mcp.ElicitParams{
			Message: "name?", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "string"}}},
		})
		return err
	})
	ask("ask_roots", func(ctx context.Context, ss *mcp.ServerSession) error {
		res, err := ss.ListRoots(ctx, nil)
		if err == nil {
			// The SDK client answers roots/list with an empty list; anything else would be a leak.
			err = fmt.Errorf("answered with %d roots", len(res.Roots))
		}
		return err
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{})
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if r.Method == http.MethodPost && json.Unmarshal(body, &msg) == nil && msg.Method == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}`, msg.ID)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *legacyAskUpstream) reply(tool string) (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	r, ok := u.replies[tool]
	return r, ok
}

// b64 is the base64 of b (what the gateway puts in image items).
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
