package upstreamauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

var ctx = context.Background()

type headerMCP struct {
	*httptest.Server
	mu   sync.Mutex
	auth string
}

func (h *headerMCP) lastAuth() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.auth
}

// newHeaderMCP is an upstream MCP server that accepts only two known bearer tokens.
func newHeaderMCP(t *testing.T) *headerMCP {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "header-upstream", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "get_item", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
		})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
	h := &headerMCP{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		h.mu.Lock()
		h.auth = got
		h.mu.Unlock()
		if got != "Bearer good-token-1234" && got != "Bearer good-shared-5678" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(h.Close)
	return h
}

func (e *env) insertHeaderServer(slug, serverURL, name, prefix, hint string) string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind, header_name, header_prefix, header_format_hint)
		VALUES ($1, $2, 'header', $3, $4, NULLIF($5, '')) RETURNING id::text`, slug, serverURL, name, prefix, hint).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) setSharedToken(serverID, token string) {
	e.t.Helper()
	if _, err := e.mgr.SetToken(ctx, e.pool, serverID, e.userB, token, true); err != nil {
		e.t.Fatal(err)
	}
}

func headerValue(t *testing.T, cred upstream.Credential, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	h, ok := cred.(upstream.StaticHeader)
	if !ok {
		t.Fatalf("credential is %T", cred)
	}
	return h.Value
}

func TestHeaderCredentialSent(t *testing.T) {
	e := newEnv(t)
	up := newHeaderMCP(t)
	id := e.insertHeaderServer("gh", up.URL+"/mcp", "Authorization", "Bearer ", "good-")
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-1234", false); err != nil {
		t.Fatal(err)
	}
	cred, err := e.mgr.CredentialFor(ctx, id, e.userA)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := upstream.New().ListTools(ctx, up.URL+"/mcp", cred); err != nil {
		t.Fatal(err)
	}
	if got := up.lastAuth(); got != "Bearer good-token-1234" {
		t.Fatalf("upstream got %q", got)
	}
}

func TestHeaderNotConnected(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	_, err := e.mgr.CredentialFor(ctx, id, e.userA)
	var nc *NotConnectedError
	if !errors.Is(err, ErrNotConnected) || !errors.As(err, &nc) ||
		nc.ConnectURL != "http://localhost:8787/portal/connect/?slug=gh" {
		t.Fatalf("err = %v", err)
	}
}

func TestHeaderServerWithoutHeaderNameIsConfigError(t *testing.T) {
	e := newEnv(t)
	id := e.insertServer("bare", "https://bare.example/mcp", AuthKindHeader)
	_, err := e.mgr.CredentialFor(ctx, id, e.userA)
	if err == nil || errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestPersonalOverridesShared(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	e.setSharedToken(id, "good-shared-5678")
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-1234", false); err != nil {
		t.Fatal(err)
	}
	cred, err := e.mgr.CredentialFor(ctx, id, e.userA)
	if got := headerValue(t, cred, err); got != "Bearer good-token-1234" {
		t.Fatalf("got %q", got)
	}
}

func TestSharedFallback(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	e.setSharedToken(id, "good-shared-5678")
	cred, err := e.mgr.CredentialFor(ctx, id, e.userB)
	if got := headerValue(t, cred, err); got != "Bearer good-shared-5678" {
		t.Fatalf("userB got %q", got)
	}
	cred, err = e.mgr.CredentialFor(ctx, id, "")
	if got := headerValue(t, cred, err); got != "Bearer good-shared-5678" {
		t.Fatalf("catalog sync got %q", got)
	}
	if err := e.mgr.RevokeConnection(ctx, e.pool, id, "", "rotated"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.CredentialFor(ctx, id, e.userB); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestSetTokenRejectsFormatHint(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "good-")
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "bad-token", false); !errors.Is(err, ErrTokenFormat) {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"", "good-\r\nX: 1", "good-\x00", "good-" + strings.Repeat("x", 4100)} {
		if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, bad, false); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("token %q: err = %v", bad[:min(len(bad), 8)], err)
		}
	}
	if e.credentialCount() != 0 {
		t.Fatal("a rejected token was stored")
	}
}

func TestSetTokenReturnsLast4AndSealsValue(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	last4, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-1234", false)
	var ct []byte
	_ = e.pool.QueryRow(ctx, `SELECT access_ciphertext FROM upstream_credentials`).Scan(&ct)
	if err != nil || last4 != "1234" || strings.Contains(string(ct), "good-token") {
		t.Fatalf("last4=%q err=%v", last4, err)
	}
	// replacing keeps one row
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-9999", false); err != nil || e.credentialCount() != 1 {
		t.Fatalf("err=%v rows=%d", err, e.credentialCount())
	}
}

func TestSetTokenOnOAuthServerWithoutAlternativeIsNotSupported(t *testing.T) {
	e := newEnv(t)
	if _, err := e.mgr.SetToken(ctx, e.pool, e.serverID, e.userA, "tok-1234", false); !errors.Is(err, ErrTokenNotSupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestTestTokenRejectedStoresNothing(t *testing.T) {
	e := newEnv(t)
	up := newHeaderMCP(t)
	id := e.insertHeaderServer("gh", up.URL+"/mcp", "Authorization", "Bearer ", "good-")
	_, err := e.mgr.TestToken(ctx, upstream.New(), id, e.userA, "good-wrong")
	var rej *UpstreamRejectedError
	if !errors.As(err, &rej) || rej.Status != 401 || e.credentialCount() != 0 {
		t.Fatalf("err = %v rows = %d", err, e.credentialCount())
	}
	res, err := e.mgr.TestToken(ctx, upstream.New(), id, e.userA, "good-token-1234")
	if err != nil || len(res.Tools) != 1 || res.Tools[0] != "get_item" || e.credentialCount() != 0 {
		t.Fatalf("res = %+v err = %v rows = %d", res, err, e.credentialCount())
	}
}

func TestRevokeConnectionKeepsReasonDropsSecret(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-1234", false); err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.RevokeConnection(ctx, e.pool, id, e.userA, "left the company"); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	var access, refresh []byte
	_ = e.pool.QueryRow(ctx, `SELECT status, revoked_reason, access_ciphertext, refresh_ciphertext FROM upstream_credentials`).
		Scan(&status, &reason, &access, &refresh)
	if status != "revoked_by_admin" || reason != "left the company" || access != nil || refresh != nil {
		t.Fatalf("status=%s reason=%q", status, reason)
	}
	if _, err := e.mgr.CredentialFor(ctx, id, e.userA); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	if err := e.mgr.RevokeConnection(ctx, e.pool, id, e.userB, "x"); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("missing row: %v", err)
	}
}

func TestDisconnectDeletesOnlyTheTargetRow(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	e.setSharedToken(id, "good-shared-5678")
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-1234", false); err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.Disconnect(ctx, e.pool, id, e.userA, false); err != nil || e.credentialCount() != 1 {
		t.Fatalf("err=%v rows=%d", err, e.credentialCount())
	}
	if err := e.mgr.Disconnect(ctx, e.pool, id, e.userA, false); err != nil {
		t.Fatalf("missing must be nil: %v", err)
	}
	if err := e.mgr.Disconnect(ctx, e.pool, id, "", true); err != nil || e.credentialCount() != 0 {
		t.Fatalf("err=%v rows=%d", err, e.credentialCount())
	}
}

func TestSharedAndPersonalUseDifferentAAD(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-1234", false); err != nil {
		t.Fatal(err)
	}
	_, err := e.pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, shared, access_ciphertext, key_id, last4)
		SELECT server_id, NULL, true, access_ciphertext, key_id, last4 FROM upstream_credentials WHERE user_id = $1::uuid`, e.userA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.CredentialFor(ctx, id, e.userB); !errors.Is(err, vault.ErrDecrypt) {
		t.Fatalf("err = %v", err)
	}
}

func TestSharedValueHasPrefix(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "", "")
	if ok, err := e.mgr.SharedValueHasPrefix(ctx, e.pool, id, "Bearer "); ok || err != nil {
		t.Fatalf("no shared row: %v %v", ok, err)
	}
	e.setSharedToken(id, "Bearer abc-1234")
	for prefix, want := range map[string]bool{"Bearer ": true, "Token ": false, "": false} {
		if ok, err := e.mgr.SharedValueHasPrefix(ctx, e.pool, id, prefix); ok != want || err != nil {
			t.Fatalf("prefix %q: %v %v", prefix, ok, err)
		}
	}
}

func TestLastUsedAtThrottled(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "good-token-1234", false); err != nil {
		t.Fatal(err)
	}
	read := func() (set bool) {
		_ = e.pool.QueryRow(ctx, `SELECT last_used_at IS NOT NULL FROM upstream_credentials`).Scan(&set)
		return
	}
	if read() {
		t.Fatal("last_used_at set before any use")
	}
	if _, err := e.mgr.CredentialFor(ctx, id, e.userA); err != nil || !read() {
		t.Fatalf("err=%v set=%v", err, read())
	}
	var first string
	_ = e.pool.QueryRow(ctx, `SELECT last_used_at::text FROM upstream_credentials`).Scan(&first)
	if _, err := e.mgr.CredentialFor(ctx, id, e.userA); err != nil {
		t.Fatal(err)
	}
	var second string
	_ = e.pool.QueryRow(ctx, `SELECT last_used_at::text FROM upstream_credentials`).Scan(&second)
	if first != second {
		t.Fatal("last_used_at updated within a minute")
	}
}
