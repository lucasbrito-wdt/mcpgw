package upstream_test

import (
	"context"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestURLCredentialCallsTheConnectionURL(t *testing.T) {
	first, second := newFake(t), newFake(t)
	c := upstream.New()
	defer c.Close()
	cred := upstream.BearerToken(secretToken)
	// The server URL the caller passes is unreachable: only the connection URL may be dialed.
	const serverURL = "http://127.0.0.1:1/mcp"
	for _, f := range []*fake{first, second} {
		uc := upstream.URLCredential{Credential: cred, URL: f.srv.URL}
		if _, err := c.CallTool(context.Background(), serverURL, uc, "get_item", []byte(`{"id":"1"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for i, f := range []*fake{first, second} {
		f.mu.Lock()
		n := len(f.authSeen)
		f.mu.Unlock()
		if n == 0 {
			t.Fatalf("connection %d: its URL was never called (sessions shared across URLs?)", i)
		}
	}
}
