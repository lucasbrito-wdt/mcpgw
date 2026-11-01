package upstreamauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const pastedToken = "fc-pasted-token-1234"

func (e *env) allowTokenOnAlpha() {
	e.t.Helper()
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_servers SET header_name = 'X-Alt-Key', header_prefix = '' WHERE id = $1::uuid`, e.serverID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) method(userID, profile string) string {
	e.t.Helper()
	var m string
	err := e.pool.QueryRow(ctx, `SELECT method FROM upstream_credentials
		WHERE server_id = $1::uuid AND user_id = $2::uuid AND profile = $3`, e.serverID, userID, profile).Scan(&m)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func TestPastedTokenOnOAuthServerIsForwardedInAlternativeHeader(t *testing.T) {
	e := newEnv(t)
	e.allowTokenOnAlpha()
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, e.serverID, e.userA, DefaultProfile, pastedToken); err != nil {
		t.Fatal(err)
	}
	if got := e.method(e.userA, DefaultProfile); got != MethodToken {
		t.Fatalf("method = %q", got)
	}
	cred, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cred.(upstream.StaticHeader); !ok {
		t.Fatalf("credential is %T", cred)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	cred.Apply(req)
	if req.Header.Get("X-Alt-Key") != pastedToken || req.Header.Get("Authorization") != "" {
		t.Fatalf("headers = %v", req.Header)
	}
}

func TestPastedTokenAppliesPrefixAndCatalogSyncFindsIt(t *testing.T) {
	e := newEnv(t)
	e.allowTokenOnAlpha()
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_servers SET header_prefix = 'Key ' WHERE id = $1::uuid`, e.serverID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, e.serverID, e.userA, "work", pastedToken); err != nil {
		t.Fatal(err)
	}
	cred, err := e.mgr.AnyCredentialFor(ctx, e.serverID, e.userA)
	if got := headerValue(t, cred, err); got != "Key "+pastedToken {
		t.Fatalf("value = %q", got)
	}
}

func TestOAuthServerWithoutAlternativeRejectsToken(t *testing.T) {
	e := newEnv(t)
	_, err := e.mgr.SetProfileToken(ctx, e.pool, e.serverID, e.userA, DefaultProfile, pastedToken)
	if !errors.Is(err, ErrTokenNotSupported) {
		t.Fatalf("SetProfileToken = %v", err)
	}
	_, err = e.mgr.TestToken(ctx, nil, e.serverID, e.userA, pastedToken)
	if !errors.Is(err, ErrTokenNotSupported) {
		t.Fatalf("TestToken = %v", err)
	}
	if e.credentialCount() != 0 {
		t.Fatal("a credential was stored")
	}
}

func TestOAuthAndTokenReplaceEachOther(t *testing.T) {
	e := newEnv(t)
	e.allowTokenOnAlpha()
	e.connect(e.userA, "alpha")
	if got := e.method(e.userA, DefaultProfile); got != MethodOAuth {
		t.Fatalf("after oauth method = %q", got)
	}
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, e.serverID, e.userA, DefaultProfile, pastedToken); err != nil {
		t.Fatal(err)
	}
	if got := e.method(e.userA, DefaultProfile); got != MethodToken || e.credentialCount() != 1 {
		t.Fatalf("after token method = %q, rows = %d", got, e.credentialCount())
	}
	var refresh []byte
	if err := e.pool.QueryRow(ctx, `SELECT refresh_ciphertext FROM upstream_credentials`).Scan(&refresh); err != nil || refresh != nil {
		t.Fatalf("refresh token kept: %v %v", refresh, err)
	}
	e.connect(e.userA, "alpha")
	if got := e.method(e.userA, DefaultProfile); got != MethodOAuth {
		t.Fatalf("after reconnect method = %q", got)
	}
	cred, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cred.(upstream.BearerToken); !ok {
		t.Fatalf("credential is %T", cred)
	}
}
