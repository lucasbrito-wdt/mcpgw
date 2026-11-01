package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const (
	e2eProtocolVersion = "2026-07-28"
	e2eEmail           = "owner@example.test"
	e2ePassword        = "a long enough password"
	e2eRedirectURI     = "http://127.0.0.1:53682/callback"
	e2eServerSlug      = "fake"
	e2eWait            = 15 * time.Second
	e2ePollEvery       = 150 * time.Millisecond // stays under the per-user rate limit (10 rps)
)

// e2eEnv is a fully wired gateway (the production serve path) over a throwaway database,
// a fake upstream MCP server and an admin environment that runs the same code as the CLI.
type e2eEnv struct {
	t        *testing.T
	db       *adminEnv
	adminOut *bytes.Buffer
	base     string
	upstream *fakeUpstream
	userID   string
	serverID string
}

func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	pool := storetest.New(t)
	up := newFakeUpstream(t)
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

	out := &bytes.Buffer{}
	e := &e2eEnv{t: t, base: base, upstream: up, adminOut: out}
	e.db = &adminEnv{
		pool: pool, cfg: cfg, out: out, in: strings.NewReader(""), lister: upstream.New(),
		getenv: func(k string) string {
			if k == envAdminPassword {
				return e2ePassword
			}
			return ""
		},
	}
	// Admin setup runs before serve so the boot-time catalog sync cannot race the CLI sync.
	e.admin("user create --email " + e2eEmail)
	e.admin("server add --slug " + e2eServerSlug + " --url " + up.srv.URL + " --auth none")
	e.admin("catalog sync --slug " + e2eServerSlug)
	e.admin("ceiling set --email " + e2eEmail + " --slug " + e2eServerSlug + " --level write")
	e.lookupIDs()

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
		case <-time.After(20 * time.Second):
			t.Error("serve did not shut down")
		}
	})
	waitHealthy(t, base)
	return e
}

// admin runs one CLI admin command (flags split on spaces) and returns its output.
func (e *e2eEnv) admin(args string) string {
	e.t.Helper()
	e.adminOut.Reset()
	if err := adminDispatch(context.Background(), e.db, strings.Fields(args)); err != nil {
		e.t.Fatalf("admin %q: %v", args, err)
	}
	return e.adminOut.String()
}

func (e *e2eEnv) lookupIDs() {
	e.t.Helper()
	ctx := context.Background()
	if err := e.db.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, e2eEmail).Scan(&e.userID); err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.pool.QueryRow(ctx, `SELECT id::text FROM upstream_servers WHERE slug = $1`, e2eServerSlug).Scan(&e.serverID); err != nil {
		e.t.Fatal(err)
	}
}

// exec runs SQL directly (used to change consents the way an operator or the consent page would).
func (e *e2eEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// eventually polls cond until it holds or e2eWait passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(e2eWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", e2eWait, what)
		}
		time.Sleep(e2ePollEvery)
	}
}

// fakeUpstream is a stateless Streamable HTTP MCP server that records the tools that reached it.
type fakeUpstream struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []string
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{}
	s := mcp.NewServer(&mcp.Implementation{Name: "fake-upstream", Version: "1"}, nil)
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
	}}
	for _, name := range []string{"get_item", "update_item", "delete_item"} {
		s.AddTool(&mcp.Tool{Name: name, Description: "Item operation " + name, InputSchema: schema},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				u.record(req.Params.Name)
				var args map[string]any
				_ = json.Unmarshal(req.Params.Arguments, &args)
				body, _ := json.Marshal(map[string]any{"tool": req.Params.Name, "id": args["id"], "name": "widget"})
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, nil
			})
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	u.srv = httptest.NewServer(h)
	t.Cleanup(u.srv.Close)
	return u
}

func (u *fakeUpstream) record(tool string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, tool)
}

// received returns the tool names that reached the upstream, in order.
func (u *fakeUpstream) received() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.calls...)
}

func (u *fakeUpstream) count(tool string) int {
	n := 0
	for _, c := range u.received() {
		if c == tool {
			n++
		}
	}
	return n
}

// oauthClient plays the MCP client side of the OAuth flow over real HTTP, with a cookie jar
// (login session and CSRF cookie) and redirects left unfollowed.
type oauthClient struct {
	t        *testing.T
	base     string
	http     *http.Client
	clientID string
}

type pkcePair struct{ verifier, challenge string }

func newPKCE(t *testing.T) pkcePair {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return pkcePair{verifier: verifier, challenge: base64.RawURLEncoding.EncodeToString(sum[:])}
}

func newOAuthClient(t *testing.T, base string) *oauthClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &oauthClient{t: t, base: base, http: &http.Client{
		Jar: jar, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type httpReply struct {
	status int
	header http.Header
	body   string
}

func (c *oauthClient) do(req *http.Request) httpReply {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return httpReply{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func (c *oauthClient) get(path string) httpReply {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	return c.do(req)
}

func (c *oauthClient) postForm(path string, form url.Values) httpReply {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// register performs Dynamic Client Registration (RFC 7591) as a public client.
func (c *oauthClient) register() {
	c.t.Helper()
	body := `{"client_name":"e2e client","redirect_uris":["http://127.0.0.1/callback"],"token_endpoint_auth_method":"none"}`
	req, err := http.NewRequest(http.MethodPost, c.base+"/register", strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	reply := c.do(req)
	if reply.status != http.StatusCreated {
		c.t.Fatalf("register: %d %s", reply.status, reply.body)
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(reply.body), &out); err != nil || out.ClientID == "" {
		c.t.Fatalf("register response %q: %v", reply.body, err)
	}
	c.clientID = out.ClientID
}

func (c *oauthClient) authorizeQuery(p pkcePair, resource string) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {c.clientID}, "redirect_uri": {e2eRedirectURI},
		"code_challenge": {p.challenge}, "code_challenge_method": {"S256"}, "state": {"e2e-state"},
		"resource": {resource}, "scope": {"mcp"},
	}
}

var hiddenFieldPattern = regexp.MustCompile(`name="(csrf_token|request)" value="([^"]*)"`)

func hiddenFields(t *testing.T, r httpReply) url.Values {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("expected a form page, got %d: %s", r.status, r.body)
	}
	out := url.Values{}
	for _, m := range hiddenFieldPattern.FindAllStringSubmatch(r.body, -1) {
		out.Set(m[1], m[2])
	}
	if out.Get("csrf_token") == "" || out.Get("request") == "" {
		t.Fatalf("hidden fields missing: %s", r.body)
	}
	return out
}

// authorize drives /authorize: login form when there is no session yet, then the consent form
// choosing level for serverID. It returns the query of the redirect to the client.
func (c *oauthClient) authorize(p pkcePair, resource, serverID, level string) url.Values {
	c.t.Helper()
	page := c.get("/authorize?" + c.authorizeQuery(p, resource).Encode())
	form := hiddenFields(c.t, page)
	if strings.Contains(page.body, `name="password"`) {
		form.Set("action", "login")
		form.Set("email", e2eEmail)
		form.Set("password", e2ePassword)
		page = c.postForm("/authorize", form)
		form = hiddenFields(c.t, page)
	}
	if !strings.Contains(page.body, `name="level_`+serverID+`"`) {
		c.t.Fatalf("consent page does not offer server %s: %s", serverID, page.body)
	}
	form.Set("action", "consent")
	form.Set("level_"+serverID, level)
	done := c.postForm("/authorize", form)
	if done.status != http.StatusSeeOther {
		c.t.Fatalf("consent submit: %d %s", done.status, done.body)
	}
	loc, err := url.Parse(done.header.Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), e2eRedirectURI+"?") {
		c.t.Fatalf("redirect location %q: %v", done.header.Get("Location"), err)
	}
	return loc.Query()
}

func (c *oauthClient) exchange(code string, p pkcePair, resource string) httpReply {
	c.t.Helper()
	return c.postForm("/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {p.verifier},
		"client_id": {c.clientID}, "redirect_uri": {e2eRedirectURI}, "resource": {resource},
	})
}

type tokenReply struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func decodeToken(t *testing.T, r httpReply) tokenReply {
	t.Helper()
	var out tokenReply
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("token body %q: %v", r.body, err)
	}
	return out
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// connectMCP opens a go-sdk client session speaking 2026-07-28 with the bearer token.
func connectMCP(t *testing.T, base, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token}}, DisableStandaloneSSE: true,
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil).
		Connect(context.Background(), transport, &mcp.ClientSessionOptions{ProtocolVersion: e2eProtocolVersion})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// runScript executes code through the execute tool and returns the result (error results included).
func runScript(t *testing.T, cs *mcp.ClientSession, code string) *mcp.CallToolResult {
	t.Helper()
	return callTool(t, cs, "execute", map[string]any{"code": code})
}

// rawMCPPost sends a tools/list with the headers the 2026-07-28 spec requires.
func rawMCPPost(t *testing.T, base, token string) httpReply {
	t.Helper()
	meta := fmt.Sprintf(`"_meta":{"io.modelcontextprotocol/protocolVersion":%q,`+
		`"io.modelcontextprotocol/clientInfo":{"name":"raw","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}`, e2eProtocolVersion)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + meta + `}}`
	req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Method", "tools/list")
	req.Header.Set("Mcp-Protocol-Version", e2eProtocolVersion)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return httpReply{status: resp.StatusCode, header: resp.Header, body: string(out)}
}
