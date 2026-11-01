package upstream_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const secretToken = "s3cr3t-token-value"

type fake struct {
	srv *httptest.Server

	mu       sync.Mutex
	authSeen []string
}

type itemArgs struct {
	ID string `json:"id"`
}
type item struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{}
	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, &mcp.ServerOptions{
		SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) {
			c.TTLMs = 60000
			c.CacheScope = "private"
		},
	})
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_item", Description: "Fetch an item",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, a itemArgs) (*mcp.CallToolResult, item, error) {
		return nil, item{ID: a.ID, Name: "widget"}, nil
	})
	yes := true
	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_item", Description: "Delete an item",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
	}, func(_ context.Context, _ *mcp.CallToolRequest, a itemArgs) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"deleted": a.ID}, nil
	})
	rawSchema := map[string]any{"type": "object"}
	text := func(name, body string) {
		s.AddTool(&mcp.Tool{Name: name, InputSchema: rawSchema},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}, nil
			})
	}
	text("text_json", `{"ok":true,"n":3}`)
	text("text_plain", "hello world")
	text("big", strings.Repeat("x", 200_000))
	s.AddTool(&mcp.Tool{Name: "fail", InputSchema: rawSchema},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}}, nil
		})
	s.AddTool(&mcp.Tool{Name: "slow", InputSchema: rawSchema},
		func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			select {
			case <-ctx.Done():
			case <-time.After(1500 * time.Millisecond):
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "late"}}}, nil
		})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		f.mu.Lock()
		f.authSeen = append(f.authSeen, got)
		f.mu.Unlock()
		if got != "Bearer "+secretToken {
			// Echo the header back to prove errors do not leak it.
			http.Error(w, "unauthorized: "+got, http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestListTools(t *testing.T) {
	f := newFake(t)
	c := upstream.New()
	tools, hint, err := c.ListTools(context.Background(), f.srv.URL, upstream.BearerToken(secretToken))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]upstream.Tool{}
	for _, tl := range tools {
		by[tl.Name] = tl
	}
	if len(by) != 7 {
		t.Fatalf("got %d tools: %v", len(by), by)
	}
	if !by["get_item"].Annotations.ReadOnlyHint {
		t.Error("get_item should be read-only")
	}
	if d := by["delete_item"].Annotations.DestructiveHint; d == nil || !*d {
		t.Error("delete_item should be destructive")
	}
	if by["get_item"].OutputSchema == nil || !json.Valid(by["get_item"].InputSchema) {
		t.Error("schemas missing")
	}
	if hint.TTL != 60*time.Second || hint.Scope != "private" {
		t.Errorf("hint = %+v", hint)
	}
}

func TestCallToolResults(t *testing.T) {
	f := newFake(t)
	c := upstream.New()
	cred := upstream.BearerToken(secretToken)
	ctx := context.Background()

	res, err := c.CallTool(ctx, f.srv.URL, cred, "get_item", json.RawMessage(`{"id":"7"}`))
	if err != nil || !res.Structured || string(res.Value) != `{"id":"7","name":"widget"}` {
		t.Fatalf("structured: %+v %v", res, err)
	}
	res, err = c.CallTool(ctx, f.srv.URL, cred, "text_json", nil)
	if err != nil || res.Structured || string(res.Value) != `{"ok":true,"n":3}` {
		t.Fatalf("text json: %+v %v", res, err)
	}
	res, err = c.CallTool(ctx, f.srv.URL, cred, "text_plain", json.RawMessage(`{}`))
	if err != nil || string(res.Value) != `"hello world"` {
		t.Fatalf("text plain: %+v %v", res, err)
	}
}

func TestCallToolIsError(t *testing.T) {
	f := newFake(t)
	_, err := upstream.New().CallTool(context.Background(), f.srv.URL, upstream.BearerToken(secretToken), "fail", nil)
	var te *upstream.ToolError
	if !errors.As(err, &te) || te.Message != "boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestCallToolRejectsNonObjectArgs(t *testing.T) {
	f := newFake(t)
	_, err := upstream.New().CallTool(context.Background(), f.srv.URL, upstream.BearerToken(secretToken), "get_item", json.RawMessage(`[1]`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAuthHeaderApplied(t *testing.T) {
	f := newFake(t)
	c := upstream.New()
	if _, _, err := c.ListTools(context.Background(), f.srv.URL, upstream.BearerToken(secretToken)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.authSeen) == 0 {
		t.Fatal("no requests")
	}
	for _, a := range f.authSeen {
		if a != "Bearer "+secretToken {
			t.Fatalf("request without credential: %q", a)
		}
	}
}

func TestStaticHeaderCredential(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Api-Key")
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	_, _, err := upstream.New().ListTools(context.Background(), srv.URL, upstream.StaticHeader{Name: "X-Api-Key", Value: "key-abcdef"})
	if err == nil || got != "key-abcdef" {
		t.Fatalf("err=%v got=%q", err, got)
	}
}

func TestErrorsDoNotLeakCredential(t *testing.T) {
	f := newFake(t)
	c := upstream.New()
	wrong := upstream.BearerToken("leaky-token-123456")
	_, _, err := c.ListTools(context.Background(), f.srv.URL, wrong)
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if strings.Contains(err.Error(), "leaky-token-123456") {
		t.Fatalf("token leaked: %v", err)
	}
	_, err = c.CallTool(context.Background(), f.srv.URL, wrong, "get_item", nil)
	if err == nil || strings.Contains(err.Error(), "leaky-token-123456") {
		t.Fatalf("token leaked or no error: %v", err)
	}
}

func TestNoCredentialSendsNoAuthorization(t *testing.T) {
	f := newFake(t)
	_, _, err := upstream.New().ListTools(context.Background(), f.srv.URL, nil)
	if err == nil {
		t.Fatal("expected 401")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.authSeen {
		if a != "" {
			t.Fatalf("unexpected Authorization %q", a)
		}
	}
}

func TestTimeout(t *testing.T) {
	f := newFake(t)
	c := upstream.New(upstream.WithTimeout(300 * time.Millisecond))
	start := time.Now()
	_, err := c.CallTool(context.Background(), f.srv.URL, upstream.BearerToken(secretToken), "slow", nil)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestOversizedResponse(t *testing.T) {
	f := newFake(t)
	c := upstream.New(upstream.WithMaxResponseBytes(50_000))
	_, err := c.CallTool(context.Background(), f.srv.URL, upstream.BearerToken(secretToken), "big", nil)
	if !errors.Is(err, upstream.ErrResponseTooLarge) {
		t.Fatalf("err = %v", err)
	}
	// Small responses still fit under the same bound.
	if _, err := c.CallTool(context.Background(), f.srv.URL, upstream.BearerToken(secretToken), "text_plain", nil); err != nil {
		t.Fatalf("small call: %v", err)
	}
}

func TestRedirectsNotFollowed(t *testing.T) {
	var hit bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, _, err := upstream.New().ListTools(context.Background(), srv.URL, upstream.BearerToken(secretToken))
	if err == nil || hit {
		t.Fatalf("err=%v redirected=%v", err, hit)
	}
}

// F7: net/http errors embed the request URL; query secrets and userinfo must not reach callers.
func TestErrorsDoNotLeakURLSecrets(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	base := dead.URL
	dead.Close() // connection refused: net/http reports the full URL
	target := strings.Replace(base, "http://", "http://user:pw-secret-9@", 1) + "/mcp?api_key=URLSECRET123&x=1"

	c := upstream.New()
	_, _, listErr := c.ListTools(context.Background(), target, nil)
	_, callErr := c.CallTool(context.Background(), target, nil, "get_item", nil)
	for name, err := range map[string]error{"ListTools": listErr, "CallTool": callErr} {
		if err == nil {
			t.Fatalf("%s: expected a connection error", name)
		}
		for _, leak := range []string{"URLSECRET123", "pw-secret-9", "api_key", "/mcp"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s leaked %q: %v", name, leak, err)
			}
		}
		if !strings.Contains(err.Error(), "?REDACTED") {
			t.Errorf("%s: want scheme://host?REDACTED in message: %v", name, err)
		}
	}
}

func TestRedactURLsUnwrapsURLError(t *testing.T) {
	inner := &url.Error{Op: "Post", URL: "https://h.example/mcp?token=AAA&b=2#frag", Err: errors.New("EOF")}
	got := upstream.RedactURLs(fmt.Errorf("wrapped: %w", inner))
	if strings.Contains(got, "AAA") || strings.Contains(got, "b=2") || !strings.Contains(got, "https://h.example?REDACTED") {
		t.Fatalf("got %q", got)
	}
	if plain := upstream.RedactURLs(errors.New("no url here")); plain != "no url here" {
		t.Fatalf("plain error changed: %q", plain)
	}
}

func TestRedactURLsDropsPathQueryKeysAndUserinfo(t *testing.T) {
	for _, raw := range []string{
		"https://h/mcp/SECRETINPATH?sk-live-abcdef123456",
		"https://u:SECRETPW@h:8443/mcp/SECRETINPATH?k=SECRETVAL#SECRETFRAG",
	} {
		got := upstream.RedactURLs(&url.Error{Op: "Post", URL: raw, Err: errors.New("EOF")})
		for _, leak := range []string{"SECRET", "sk-live", "abcdef", "/mcp"} {
			if strings.Contains(got, leak) {
				t.Errorf("%q leaked %q: %s", raw, leak, got)
			}
		}
		if !strings.Contains(got, "?REDACTED") || !strings.Contains(got, "://h") {
			t.Errorf("%q: want scheme://host?REDACTED, got %s", raw, got)
		}
	}
}

func TestRedactURLsKeepsQuotingIntact(t *testing.T) {
	raw := "https://h/\"?k=SECRETVAL#0"
	err := &url.Error{Op: "Post", URL: raw, Err: errors.New("EOF")}
	want := `Post "https://h?REDACTED": EOF`
	if got := upstream.RedactURLs(err); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRedactURLsNestedPrefixURLs(t *testing.T) {
	outer := "https://h/a?k=SECRETONE"
	inner := outer + "&k2=SECRETTWO"
	err := &url.Error{Op: "Get", URL: outer, Err: &url.Error{Op: "Get", URL: inner, Err: errors.New("x")}}
	if got := upstream.RedactURLs(err); strings.Contains(got, "SECRET") {
		t.Fatalf("leaked: %s", got)
	}
}
