package upstreamauth

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestValidateConnectionURL(t *testing.T) {
	ok := map[string]string{
		"https://kafka-mcp.example.app/mcp":   "https://kafka-mcp.example.app/mcp",
		" https://mcp.example.com:8443/x?a=1": "https://mcp.example.com:8443/x?a=1",
		"http://localhost:9000/mcp":           "http://localhost:9000/mcp",
		"http://127.0.0.1:9000/mcp":           "http://127.0.0.1:9000/mcp",
		"http://[::1]:9000/mcp":               "http://[::1]:9000/mcp",
		"https://8.8.8.8/mcp":                 "https://8.8.8.8/mcp",
	}
	for raw, want := range ok {
		got, err := ValidateConnectionURL(raw)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", raw, got, err)
		}
	}
	bad := []string{
		"", "mcp.example.com/mcp", "/mcp", "ftp://mcp.example.com/", "http://mcp.example.com/mcp",
		"https://user:pass@mcp.example.com/mcp", "https://10.0.0.5/mcp", "https://172.16.3.1/mcp",
		"https://192.168.1.1/mcp", "https://169.254.169.254/latest", "https://[fc00::1]/mcp",
		"https://[fe80::1]/mcp", "https://[::ffff:10.0.0.1]/mcp", "https://0.0.0.0/mcp",
		"https://2852039166/mcp", "https://0x7f.1/mcp", "http://10.0.0.5/mcp",
		"https://mcp.example.com/" + strings.Repeat("a", 2048), "https://mcp.example.com/a\nb",
	}
	for _, raw := range bad {
		var ue *URLError
		if _, err := ValidateConnectionURL(raw); !errors.As(err, &ue) {
			t.Errorf("%q accepted: %v", raw, err)
		}
	}
}

func (e *env) enableConnectionURL(serverID string) {
	e.t.Helper()
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_servers SET connection_url = true WHERE id = $1::uuid`, serverID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) storedURL(serverID, userID string) *string {
	e.t.Helper()
	var u *string
	if err := e.pool.QueryRow(ctx, `SELECT url FROM upstream_credentials WHERE server_id = $1::uuid AND user_id = $2::uuid`,
		serverID, userID).Scan(&u); err != nil {
		e.t.Fatal(err)
	}
	return u
}

func TestConnectionURLIsStoredAndKeptWhenEmpty(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("kafka", "https://kafka.example/mcp", "Authorization", "Basic ", "")
	in := ConnectionInput{ServerID: id, ActorID: e.userA, Profile: DefaultProfile, Token: "token-one-1234"}
	var ue *URLError
	if _, err := e.mgr.SetConnection(ctx, e.pool, ConnectionInput{ServerID: id, ActorID: e.userA, Profile: DefaultProfile,
		Token: "token-one-1234", URL: "https://own.example/mcp"}); !errors.As(err, &ue) {
		t.Fatalf("url accepted without connection_url: %v", err)
	}
	e.enableConnectionURL(id)
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); !errors.As(err, &ue) {
		t.Fatalf("new connection without url: %v", err)
	}
	if e.credentialCount() != 0 {
		t.Fatal("stored without url")
	}
	in.URL = "https://own.example/mcp"
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
		t.Fatal(err)
	}
	in.URL, in.Token = "", "token-two-5678"
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
		t.Fatal(err)
	}
	if u := e.storedURL(id, e.userA); u == nil || *u != "https://own.example/mcp" {
		t.Fatalf("url = %v", u)
	}
	in.URL, in.Token = "https://other.example/mcp", ""
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
		t.Fatal(err)
	}
	if u := e.storedURL(id, e.userA); u == nil || *u != "https://other.example/mcp" {
		t.Fatalf("url-only update = %v", u)
	}
}

func TestForwardUsesTheConnectionURL(t *testing.T) {
	e := newEnv(t)
	up := newHeaderMCP(t)
	// The server's own URL is unreachable: a call can only succeed through the connection's URL.
	id := e.insertHeaderServer("kafka", "http://127.0.0.1:1/mcp", "Authorization", "Bearer ", "")
	e.enableConnectionURL(id)
	in := ConnectionInput{ServerID: id, ActorID: e.userA, Profile: DefaultProfile, Token: "good-token-1234", URL: up.URL + "/mcp"}
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
		t.Fatal(err)
	}
	cred, err := e.mgr.CredentialFor(ctx, id, e.userA)
	if err != nil {
		t.Fatal(err)
	}
	if uc, ok := cred.(upstream.URLCredential); !ok || uc.URL != up.URL+"/mcp" {
		t.Fatalf("credential = %T", cred)
	}
	client := upstream.New()
	defer client.Close()
	if _, _, err := client.ListTools(ctx, "http://127.0.0.1:1/mcp", cred); err != nil {
		t.Fatalf("forward: %v", err)
	}
	// Switched off, the stored URL is ignored.
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_servers SET connection_url = false WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	cred, err = e.mgr.CredentialFor(ctx, id, e.userA)
	if got := headerValue(t, cred, err); got != "Bearer good-token-1234" {
		t.Fatalf("value = %q", got)
	}
}

func TestTestConnectionUsesGivenOrSavedURL(t *testing.T) {
	e := newEnv(t)
	up := newHeaderMCP(t)
	id := e.insertHeaderServer("kafka", "http://127.0.0.1:1/mcp", "Authorization", "Bearer ", "")
	e.enableConnectionURL(id)
	client := upstream.New()
	defer client.Close()
	in := ConnectionInput{ServerID: id, ActorID: e.userA, Profile: DefaultProfile, Token: "good-token-1234"}
	var ue *URLError
	if _, err := e.mgr.TestConnection(ctx, client, in); !errors.As(err, &ue) {
		t.Fatalf("test without any url: %v", err)
	}
	in.URL = "https://10.1.2.3/mcp"
	if _, err := e.mgr.TestConnection(ctx, client, in); !errors.As(err, &ue) {
		t.Fatalf("private url tested: %v", err)
	}
	in.URL = up.URL + "/mcp"
	if res, err := e.mgr.TestConnection(ctx, client, in); err != nil || len(res.Tools) != 1 {
		t.Fatalf("given url: %+v %v", res, err)
	}
	if _, err := e.mgr.SetConnection(ctx, e.pool, in); err != nil {
		t.Fatal(err)
	}
	in.URL = ""
	if res, err := e.mgr.TestConnection(ctx, client, in); err != nil || len(res.Tools) != 1 || e.credentialCount() != 1 {
		t.Fatalf("saved url: %+v %v", res, err)
	}
}
