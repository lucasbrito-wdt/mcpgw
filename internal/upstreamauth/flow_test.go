package upstreamauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

func bearerValue(t *testing.T, c upstream.Credential) string {
	t.Helper()
	b, ok := c.(upstream.BearerToken)
	if !ok {
		t.Fatalf("credential is %T, want BearerToken", c)
	}
	return string(b)
}

func TestDiscovery(t *testing.T) {
	ctx := context.Background()
	t.Run("challenge hint and scope", func(t *testing.T) {
		e := newEnv(t)
		d, err := e.mgr.Discover(ctx, e.fake.mcpURL(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if d.Scope != "mcp.read" || d.Meta.Issuer != e.fake.srv.URL || d.Resource != e.fake.mcpURL() {
			t.Fatalf("unexpected discovery %+v", d)
		}
	})
	t.Run("well-known fallback uses scopes_supported", func(t *testing.T) {
		e := newEnv(t)
		e.fake.noChallenge = true
		d, err := e.mgr.Discover(ctx, e.fake.mcpURL(), nil)
		if err != nil || d.Scope != "mcp.read mcp.write" {
			t.Fatalf("got %+v, %v", d, err)
		}
	})
	t.Run("oidc fallback", func(t *testing.T) {
		e := newEnv(t)
		e.fake.oidcOnly = true
		if _, err := e.mgr.Discover(ctx, e.fake.mcpURL(), nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("resource mismatch", func(t *testing.T) {
		e := newEnv(t)
		e.fake.prmResource = "https://other.example/mcp"
		if _, err := e.mgr.Discover(ctx, e.fake.mcpURL(), nil); !errors.Is(err, ErrDiscovery) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no S256", func(t *testing.T) {
		e := newEnv(t)
		e.fake.omitS256 = true
		if _, err := e.mgr.Discover(ctx, e.fake.mcpURL(), nil); !errors.Is(err, ErrDiscovery) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestConnectFlowWithDCR(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")

	cred, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	if err != nil {
		t.Fatal(err)
	}
	token := bearerValue(t, cred)
	if !strings.HasPrefix(token, "at-") {
		t.Fatalf("token = %q", token)
	}

	var access, refresh []byte
	var issuer, clientID string
	err = e.pool.QueryRow(ctx, `SELECT access_ciphertext, refresh_ciphertext, issuer, client_id FROM upstream_credentials`).
		Scan(&access, &refresh, &issuer, &clientID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(access, []byte(token)) || bytes.Contains(refresh, []byte("rt-")) {
		t.Fatal("tokens are stored in plaintext")
	}
	if issuer != e.fake.srv.URL || clientID != "dcr-1" {
		t.Fatalf("issuer=%q client=%q", issuer, clientID)
	}
	if keyID, _ := vault.KeyID(access); keyID != "k1" {
		t.Fatalf("key id = %q", keyID)
	}

	e.connect(e.userB, "alpha")
	if n := e.fake.registerCalls.Load(); n != 1 {
		t.Fatalf("DCR ran %d times, want 1 (cached per server+issuer)", n)
	}
	if rec := e.get(e.userA, "/connect/success?server=alpha"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Account connected") {
		t.Fatalf("success page: %d %s", rec.Code, rec.Body.String())
	}
}

func TestConnectRejections(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(e *env)
		tamper func(q url.Values)
		user   func(e *env) string
		want   int
	}{
		{name: "iss mismatch while advertised", setup: func(e *env) { e.fake.issSupported = true },
			tamper: func(q url.Values) { q.Set("iss", "https://evil.example") }, want: 400},
		{name: "iss mismatch although not advertised",
			tamper: func(q url.Values) { q.Set("iss", "https://evil.example") }, want: 400},
		{name: "iss missing while advertised", setup: func(e *env) { e.fake.issSupported = true },
			tamper: func(q url.Values) { q.Del("iss") }, want: 400},
		{name: "authorization error", tamper: func(q url.Values) { q.Del("code"); q.Set("error", "access_denied") }, want: 400},
		{name: "unknown state", tamper: func(q url.Values) { q.Set("state", "nope") }, want: 400},
		{name: "other user", user: func(e *env) string { return e.userB }, want: 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.setup != nil {
				tt.setup(e)
			}
			q := e.fake.authorize(e.startFlow(e.userA, "alpha"))
			if tt.tamper != nil {
				tt.tamper(q)
			}
			user := e.userA
			if tt.user != nil {
				user = tt.user(e)
			}
			if rec := e.callback(user, q); rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body.String())
			}
			if e.credentialCount() != 0 {
				t.Fatal("credential stored despite rejection")
			}
		})
	}
}

func TestStateIsSingleUse(t *testing.T) {
	e := newEnv(t)
	q := e.fake.authorize(e.startFlow(e.userA, "alpha"))
	if rec := e.callback(e.userB, q); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-user callback: %d", rec.Code)
	}
	// The wrong-user attempt consumed the state: the legitimate user cannot replay it either.
	if rec := e.callback(e.userA, q); rec.Code != http.StatusBadRequest {
		t.Fatalf("replay after consumption: %d", rec.Code)
	}

	q = e.fake.authorize(e.startFlow(e.userA, "alpha"))
	if rec := e.callback(e.userA, q); rec.Code != http.StatusSeeOther {
		t.Fatalf("first use: %d", rec.Code)
	}
	if rec := e.callback(e.userA, q); rec.Code != http.StatusBadRequest {
		t.Fatalf("reuse: %d", rec.Code)
	}
}

// OnConnected fires once per successful connect, with the server and the connecting user, and
// never for a rejected callback.
func TestOnConnectedHook(t *testing.T) {
	e := newEnv(t)
	var calls []string
	e.mgr.onConnected = func(serverID, userID string) { calls = append(calls, serverID+"/"+userID) }

	q := e.fake.authorize(e.startFlow(e.userA, "alpha"))
	if rec := e.callback(e.userB, q); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-user callback: %d", rec.Code)
	}
	q = e.fake.authorize(e.startFlow(e.userA, "alpha"))
	q.Set("error", "access_denied")
	if rec := e.callback(e.userA, q); rec.Code != http.StatusBadRequest {
		t.Fatalf("denied callback: %d", rec.Code)
	}
	if len(calls) != 0 {
		t.Fatalf("hook fired for rejected callbacks: %v", calls)
	}

	e.connect(e.userA, "alpha")
	if len(calls) != 1 || calls[0] != e.serverID+"/"+e.userA {
		t.Fatalf("calls = %v", calls)
	}
}

func TestStateExpiryAndPurge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := e.fake.authorize(e.startFlow(e.userA, "alpha"))
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_oauth_states SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if rec := e.callback(e.userA, q); rec.Code != http.StatusBadRequest {
		t.Fatalf("expired state accepted: %d", rec.Code)
	}
	e.startFlow(e.userA, "alpha")
	e.pool.Exec(ctx, `UPDATE upstream_oauth_states SET expires_at = now() - interval '1 minute'`)
	if n, err := e.mgr.PurgeExpiredStates(ctx); err != nil || n != 2 {
		t.Fatalf("purge = %d, %v", n, err)
	}
}

func TestStateStoredHashedAndVerifierSealed(t *testing.T) {
	e := newEnv(t)
	authURL := e.startFlow(e.userA, "alpha")
	u, _ := url.Parse(authURL)
	state := u.Query().Get("state")
	var stored string
	var ct []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT state, code_verifier_ciphertext FROM upstream_oauth_states`).Scan(&stored, &ct); err != nil {
		t.Fatal(err)
	}
	if stored == state || stored != hashState(state) || len(state) < 43 {
		t.Fatalf("state not hashed or too short (%d chars)", len(state))
	}
	if _, err := e.vault.Open(ct, stateAAD(e.serverID, e.userA)); err != nil {
		t.Fatalf("verifier not sealed with expected AAD: %v", err)
	}
}

func TestConnectEndpoints(t *testing.T) {
	e := newEnv(t)
	e.insertServer("plain", "https://plain.example/mcp", AuthKindNone)
	off := e.insertServer("off", e.fake.mcpURL(), AuthKindOAuth)
	if _, err := e.pool.Exec(context.Background(), `UPDATE upstream_servers SET status = 'disabled' WHERE id = $1::uuid`, off); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, user, path string
		want             int
	}{
		{"unauthenticated connect", "", "/connect/alpha", 401},
		{"unauthenticated callback", "", "/connect/callback?state=x", 401},
		{"unknown slug", e.userA, "/connect/missing", 404},
		{"non-oauth server", e.userA, "/connect/plain", 404},
		{"disabled server", e.userA, "/connect/off", 404},
		{"callback without state", e.userA, "/connect/callback", 400},
	} {
		if rec := e.get(tt.user, tt.path); rec.Code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, rec.Code, tt.want)
		}
	}
	rec := e.get(e.userA, "/connect/callback?state=x")
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("callback must be no-store / no-referrer")
	}
}

// A user without a trust ceiling on the server cannot start a connect: no state row, no outbound
// discovery or registration, and so no OnConnected (catalog auto-sync) either.
func TestConnectRequiresCeiling(t *testing.T) {
	e := newEnv(t)
	outsider := e.insertUser("c@example.com")
	rec := e.get(outsider, "/connect/alpha")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Access denied") {
		t.Fatalf("connect without ceiling = %d %s", rec.Code, rec.Body.String())
	}
	var states int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM upstream_oauth_states`).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if states != 0 || e.fake.registerCalls.Load() != 0 {
		t.Fatalf("outbound work done for a user without ceiling: states=%d registrations=%d", states, e.fake.registerCalls.Load())
	}
}

func TestStaticClientWinsAndSecretSealed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.clients["admin-client"] = "admin-secret"
	if err := e.mgr.SetStaticOAuthClient(ctx, e.pool, e.serverID, "admin-client", "admin-secret"); err != nil {
		t.Fatal(err)
	}
	e.connect(e.userA, "alpha")
	if e.fake.registerCalls.Load() != 0 {
		t.Fatal("DCR used although an admin client is configured")
	}
	var ct []byte
	if err := e.pool.QueryRow(ctx, `SELECT oauth_client_secret_ciphertext FROM upstream_servers WHERE id = $1::uuid`, e.serverID).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("admin-secret")) {
		t.Fatal("client secret stored in plaintext")
	}
	// Refresh authenticates as the same confidential client (the fake enforces Basic auth).
	e.clock.Advance(3600 * 1e9)
	if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); err != nil {
		t.Fatalf("refresh with static client: %v", err)
	}
}

func TestCIMDWhenSupportedAndPublicHTTPS(t *testing.T) {
	e := newEnvWithPublicURL(t, "https://gateway.test")
	e.fake.cimd = true
	authURL := e.startFlow(e.userA, "alpha")
	u, _ := url.Parse(authURL)
	wantID := "https://gateway.test/oauth/client-metadata.json"
	if u.Query().Get("client_id") != wantID || u.Query().Get("redirect_uri") != "https://gateway.test/connect/callback" {
		t.Fatalf("authorization request: %v", u.Query())
	}
	if rec := e.callback(e.userA, e.fake.authorize(authURL)); rec.Code != http.StatusSeeOther {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	if e.fake.registerCalls.Load() != 0 {
		t.Fatal("DCR used although CIMD is available")
	}

	rec := e.get("", "/oauth/client-metadata.json")
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || rec.Code != 200 {
		t.Fatalf("metadata doc: %d %v", rec.Code, err)
	}
	if doc["client_id"] != wantID || doc["token_endpoint_auth_method"] != "none" ||
		doc["redirect_uris"].([]any)[0] != "https://gateway.test/connect/callback" {
		t.Fatalf("unexpected document %v", doc)
	}
}

func TestCIMDSkippedOnHTTPPublicURL(t *testing.T) {
	e := newEnv(t) // http://localhost: the AS could not fetch the document
	e.fake.cimd = true
	e.connect(e.userA, "alpha")
	if e.fake.registerCalls.Load() != 1 {
		t.Fatal("expected fallback to DCR")
	}
}

func TestNoClientRegistrationAvailable(t *testing.T) {
	e := newEnv(t)
	e.fake.noRegistration = true
	if rec := e.get(e.userA, "/connect/alpha"); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestNotConnectedNoneAndStatic(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA)
	var nc *NotConnectedError
	if !errors.Is(err, ErrNotConnected) || !errors.As(err, &nc) || nc.ConnectURL != "http://localhost:8787/connect/alpha" {
		t.Fatalf("err = %v", err)
	}

	var coded interface{ NotConnected() bool }
	if !errors.As(err, &coded) || !coded.NotConnected() {
		t.Fatalf("err does not expose NotConnected() bool: %v", err)
	}

	noneID := e.insertServer("plain", "https://plain.example/mcp", AuthKindNone)
	if cred, err := e.mgr.CredentialFor(ctx, noneID, e.userA); cred != nil || err != nil {
		t.Fatalf("none: %v, %v", cred, err)
	}

	staticID := e.insertServer("keyed", "https://keyed.example/mcp", AuthKindStatic)
	if err := e.mgr.SetStaticHeader(ctx, e.pool, staticID, "X-Api-Key", "k-12345\r\nX-Evil: 1"); err == nil {
		t.Fatal("header injection accepted")
	}
	if err := e.mgr.SetStaticHeader(ctx, e.pool, staticID, "X-Api-Key", "k-12345"); err != nil {
		t.Fatal(err)
	}
	cred, err := e.mgr.CredentialFor(ctx, staticID, e.userA)
	if err != nil || cred != (upstream.StaticHeader{Name: "X-Api-Key", Value: "k-12345"}) {
		t.Fatalf("static: %v, %v", cred, err)
	}
	var ct []byte
	e.pool.QueryRow(ctx, `SELECT static_header_ciphertext FROM upstream_servers WHERE id = $1::uuid`, staticID).Scan(&ct)
	if bytes.Contains(ct, []byte("k-12345")) {
		t.Fatal("static header stored in plaintext")
	}
	if _, err := e.mgr.CredentialFor(ctx, "not-a-uuid", e.userA); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("bad id: %v", err)
	}
}

func TestCiphertextBoundToUserRow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connect(e.userA, "alpha")
	_, err := e.pool.Exec(ctx, `INSERT INTO upstream_credentials
		(server_id, user_id, access_ciphertext, refresh_ciphertext, key_id, expires_at, issuer, client_id, token_endpoint)
		SELECT server_id, $2::uuid, access_ciphertext, refresh_ciphertext, key_id, expires_at, issuer, client_id, token_endpoint
		FROM upstream_credentials WHERE user_id = $1::uuid`, e.userA, e.userB)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.mgr.CredentialFor(ctx, e.serverID, e.userB)
	if !errors.Is(err, vault.ErrDecrypt) || errors.Is(err, ErrNotConnected) {
		t.Fatalf("copied ciphertext must fail to open, got %v", err)
	}
	if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); err != nil {
		t.Fatalf("original row still works: %v", err)
	}
}
