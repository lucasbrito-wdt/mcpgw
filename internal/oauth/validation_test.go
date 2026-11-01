package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// grantFor issues real tokens so the family/client checks have rows to look at.
func (e *testEnv) grantFor() (tokenResponse, accessTokenClaims) {
	p := newPKCE()
	tokens := decodeTokens(e.t, e.exchange(e.authorizeCode(p), p.verifier, callback))
	var c accessTokenClaims
	_, _, err := jwt.NewParser().ParseUnverified(tokens.AccessToken, &c)
	e.must(err)
	return tokens, c
}

func (e *testEnv) signWith(method jwt.SigningMethod, key any, kid string, mutate func(*accessTokenClaims)) string {
	_, c := e.grantFor()
	mutate(&c)
	tok := jwt.NewWithClaims(method, c)
	tok.Header["kid"] = kid
	tok.Header["typ"] = accessTokenType
	signed, err := tok.SignedString(key)
	e.must(err)
	return signed
}

func TestValidateAccessTokenRejections(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	active := e.srv.keys.signer()
	same := func(*accessTokenClaims) {}

	pubDER, err := x509.MarshalPKIXPublicKey(&active.priv.PublicKey)
	e.must(err)
	jwkJSON, err := json.Marshal(mustJWK(t, &active.priv.PublicKey))
	e.must(err)
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	e.must(err)

	cases := map[string]string{
		"wrong audience": e.signWith(jwt.SigningMethodES256, active.priv, active.kid, func(c *accessTokenClaims) {
			c.Audience = jwt.ClaimStrings{"https://other.example/mcp"}
		}),
		"extra audience": e.signWith(jwt.SigningMethodES256, active.priv, active.kid, func(c *accessTokenClaims) {
			c.Audience = append(c.Audience, "https://other.example/mcp")
		}),
		"wrong issuer": e.signWith(jwt.SigningMethodES256, active.priv, active.kid, func(c *accessTokenClaims) {
			c.Issuer = "https://evil.example"
		}),
		"expired": e.signWith(jwt.SigningMethodES256, active.priv, active.kid, func(c *accessTokenClaims) {
			c.ExpiresAt = jwt.NewNumericDate(e.clock.Now().Add(-validationLeeway - time.Second))
		}),
		"not yet valid": e.signWith(jwt.SigningMethodES256, active.priv, active.kid, func(c *accessTokenClaims) {
			c.NotBefore = jwt.NewNumericDate(e.clock.Now().Add(time.Hour))
		}),
		"missing exp": e.signWith(jwt.SigningMethodES256, active.priv, active.kid, func(c *accessTokenClaims) {
			c.ExpiresAt = nil
		}),
		"unknown client": e.signWith(jwt.SigningMethodES256, active.priv, active.kid, func(c *accessTokenClaims) {
			c.ClientID = "mcpgw-deleted"
		}),
		"alg none":               e.signWith(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, active.kid, same),
		"hs256 with pubkey":      e.signWith(jwt.SigningMethodHS256, pubDER, active.kid, same),
		"hs256 with jwk":         e.signWith(jwt.SigningMethodHS256, jwkJSON, active.kid, same),
		"unknown kid":            e.signWith(jwt.SigningMethodES256, otherKey, "unknown-kid", same),
		"foreign key, known kid": e.signWith(jwt.SigningMethodES256, otherKey, active.kid, same),
		"garbage":                "not.a.jwt",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.srv.ValidateAccessToken(ctx, raw)
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("err = %v, want ErrInvalidToken", err)
			}
		})
	}

	t.Run("wrong typ", func(t *testing.T) {
		_, c := e.grantFor()
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, c)
		tok.Header["kid"] = active.kid
		tok.Header["typ"] = "JWT"
		raw, err := tok.SignedString(active.priv)
		e.must(err)
		if _, err := e.srv.ValidateAccessToken(ctx, raw); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("expires with the clock", func(t *testing.T) {
		tokens, _ := e.grantFor()
		e.clock.Advance(accessTokenTTL + validationLeeway + time.Second)
		if _, err := e.srv.ValidateAccessToken(ctx, tokens.AccessToken); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("client deleted", func(t *testing.T) {
		tokens, _ := e.grantFor()
		_, err := e.pool.Exec(ctx, `DELETE FROM oauth_clients WHERE client_id = $1`, e.clientID)
		e.must(err)
		if _, err := e.srv.ValidateAccessToken(ctx, tokens.AccessToken); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("err = %v", err)
		}
	})
}

func mustJWK(t *testing.T, pub *ecdsa.PublicKey) publicJWK {
	jwk, err := toPublicJWK(pub)
	if err != nil {
		t.Fatal(err)
	}
	return jwk
}

func TestSigningKeyPersistence(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	var sealed []byte
	var count int
	e.must(e.pool.QueryRow(ctx, `SELECT count(*), max(private_key_ciphertext) FROM signing_keys`).Scan(&count, &sealed))
	if count != 1 || strings.Contains(string(sealed), "PRIVATE") {
		t.Fatalf("signing keys: count %d", count)
	}
	// A second server on the same DB and sealer reuses the key and validates the first one's tokens.
	tokens, _ := e.grantFor()
	other, err := New(ctx, Options{PublicURL: testIssuer, Pool: e.pool, Sealer: e.srv.sealer, Now: e.clock.Now})
	e.must(err)
	if other.keys.signer().kid != e.srv.keys.signer().kid {
		t.Fatal("existing active key must be reused")
	}
	if _, err := other.ValidateAccessToken(ctx, tokens.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, Options{PublicURL: testIssuer, Pool: e.pool, Sealer: newAESSealer(t), Now: e.clock.Now}); err == nil {
		t.Fatal("a different sealer key must fail to open the signing key")
	}
}

func TestTokenVerifierAdapter(t *testing.T) {
	e := newTestEnv(t)
	tokens, _ := e.grantFor()
	info, err := e.srv.TokenVerifier()(context.Background(), tokens.AccessToken, nil)
	if err != nil || info.UserID != e.userID || info.Extra["client_id"] != e.clientID || len(info.Scopes) != 1 {
		t.Fatalf("token info: %+v %v", info, err)
	}
}
