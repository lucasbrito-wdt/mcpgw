package oauth

import (
	"context"
	"testing"
	"time"
)

// A client without the refresh_token grant has no oauth_refresh_tokens rows, so code reuse
// must still kill the access token issued by the first exchange.
func TestCodeReuseRevokesAccessTokenWithoutRefreshGrant(t *testing.T) {
	e := newTestEnv(t)
	e.docs.Store("/client.json", ClientMetadata{
		ClientID: e.clientID, ClientName: "Cliente sem refresh", RedirectURIs: []string{e.redirect},
		GrantTypes: []string{grantAuthorizationCode}, TokenEndpointAuthMethod: authMethodNone,
	})
	p := newPKCE()
	code := e.authorizeCode(p)
	tokens := decodeTokens(t, e.exchange(code, p.verifier, callback))
	if tokens.RefreshToken != "" {
		t.Fatalf("client without refresh grant got a refresh token")
	}
	ctx := context.Background()
	if _, err := e.srv.ValidateAccessToken(ctx, tokens.AccessToken); err != nil {
		t.Fatalf("token must be valid before reuse: %v", err)
	}
	if w := e.exchange(code, p.verifier, callback); oauthErrorCode(t, w) != "invalid_grant" {
		t.Fatalf("reuse: %d %s", w.Code, w.Body.String())
	}
	if _, err := e.srv.ValidateAccessToken(ctx, tokens.AccessToken); err == nil {
		t.Fatal("access token must be rejected after code reuse")
	}
	var n int
	e.must(e.pool.QueryRow(ctx, `SELECT count(*) FROM oauth_revoked_families`).Scan(&n))
	if n != 1 {
		t.Fatalf("revoked families = %d, want 1", n)
	}
}

func TestPurgeExpiredDropsOldRevokedFamilies(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	now := e.clock.Now()
	_, err := e.pool.Exec(ctx, `INSERT INTO oauth_revoked_families (family_id, revoked_at) VALUES
		(uuidv7(), $1), (uuidv7(), $2)`, now.Add(-accessTokenTTL-validationLeeway-time.Second), now.Add(-time.Minute))
	e.must(err)
	n, err := e.srv.PurgeExpired(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge = %d, %v; want 1", n, err)
	}
	var left int
	e.must(e.pool.QueryRow(ctx, `SELECT count(*) FROM oauth_revoked_families`).Scan(&left))
	if left != 1 {
		t.Fatalf("remaining = %d, want 1", left)
	}
}

// A revocation made outside the process (admin CLI, SQL) reaches a cached grant within
// grantCacheTTL; negative results are never cached.
func TestExternalRevocationHonoredAfterGrantCacheTTL(t *testing.T) {
	e := newTestEnv(t)
	p := newPKCE()
	tokens := decodeTokens(t, e.exchange(e.authorizeCode(p), p.verifier, callback))
	ctx := context.Background()
	if _, err := e.srv.ValidateAccessToken(ctx, tokens.AccessToken); err != nil {
		t.Fatal(err)
	}
	_, err := e.pool.Exec(ctx, `UPDATE oauth_refresh_tokens SET revoked_at = now()`)
	e.must(err)
	if _, err := e.srv.ValidateAccessToken(ctx, tokens.AccessToken); err != nil {
		t.Fatalf("within the TTL the cached grant is still accepted: %v", err)
	}
	e.clock.Advance(grantCacheTTL)
	for i := range 2 {
		if _, err := e.srv.ValidateAccessToken(ctx, tokens.AccessToken); err == nil {
			t.Fatalf("attempt %d: token must be rejected once the cache entry expires", i)
		}
	}
}

func TestGrantCacheNeverRecachesRevokedFamily(t *testing.T) {
	g := newGrantCache()
	now := time.Now()
	k := grantKey{clientID: "c", userID: "u", familyID: "f"}
	g.put(k, now)
	other := grantKey{clientID: "c", userID: "u", familyID: "g"}
	g.put(other, now)
	g.revoke("f")
	g.put(k, now) // a check that raced the revocation
	if g.ok(k, now) {
		t.Fatal("revoked family must not be served from cache")
	}
	if !g.ok(other, now) {
		t.Fatal("other families must stay cached")
	}
	if g.ok(other, now.Add(grantCacheTTL)) {
		t.Fatal("entries expire after the TTL")
	}
}
