package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"

	gwconfig "github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

// seedEmail names seeded user i (load-00001@example.test ...), in SQL.
// Cleanup: DELETE FROM users WHERE email LIKE 'load-%@example.test'.
const seedEmail = `'load-' || lpad(i::text, 5, '0') || '@example.test'`

const (
	seedClientID = "loadgen-seed"
	seedTokenTTL = 2 * time.Hour
	// vaultKeyID and signingKeyAAD mirror cmd/mcpgw and internal/oauth (unexported there).
	seedVaultKeyID = "k1"
	seedKeyAADPfx  = "mcpgw/signing-key/"
)

// seeder is a dev-only helper for load tests with thousands of users, where the real OAuth flow is
// too slow (logins are rate-limited per IP). It bulk-creates users that cannot log in, grants them
// a ceiling and a consent on one server, and signs access tokens with the gateway's active
// signing key, read from the database with the master key in MCPGW_MASTER_KEY. Never point it at
// a production database.
type seeder struct {
	server string
	level  string
}

func (s *seeder) tokens(users int) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := gwconfig.Load(os.Getenv)
	if err != nil {
		return nil, err
	}
	if err := cfg.RequireMasterKey(); err != nil {
		return nil, err
	}
	v, err := vault.New(cfg.MasterKey, seedVaultKeyID)
	if err != nil {
		return nil, err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	var serverID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM upstream_servers WHERE slug = $1`, s.server).Scan(&serverID); err != nil {
		return nil, fmt.Errorf("server %q: %w", s.server, err)
	}
	// One statement per table, all idempotent: re-running the seed reuses the same users.
	seeded := `SELECT u.id FROM generate_series(1, $1::int) i JOIN users u ON u.email = ` + seedEmail
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (email, password_hash)
		  SELECT ` + seedEmail + `, '!seeded-no-login' FROM generate_series(1, $1::int) i
		  ON CONFLICT (email) DO NOTHING`, []any{users}},
		{`INSERT INTO oauth_clients (client_id, registration_kind) VALUES ($1, 'dcr')
		  ON CONFLICT (client_id) DO NOTHING`, []any{seedClientID}},
		{`INSERT INTO trust_ceilings (user_id, server_id, max_level)
		  SELECT id, $2::uuid, $3 FROM (` + seeded + `) s
		  ON CONFLICT (user_id, server_id) DO UPDATE SET max_level = EXCLUDED.max_level`,
			[]any{users, serverID, s.level}},
		{`INSERT INTO consents (user_id, client_id, server_id, level)
		  SELECT id, $2, $3::uuid, $4 FROM (` + seeded + `) s
		  ON CONFLICT (user_id, client_id, server_id)
		  DO UPDATE SET level = EXCLUDED.level, revoked_at = NULL, expires_at = NULL`,
			[]any{users, seedClientID, serverID, s.level}},
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st.sql, st.args...); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	var kid string
	var sealed []byte
	if err := pool.QueryRow(ctx, `SELECT kid, private_key_ciphertext FROM signing_keys
		WHERE alg = 'ES256' AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`).Scan(&kid, &sealed); err != nil {
		return nil, fmt.Errorf("signing key (start the gateway once first): %w", err)
	}
	der, err := v.Open(sealed, []byte(seedKeyAADPfx+kid))
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("signing key is not ECDSA")
	}

	rows, err := pool.Query(ctx, `SELECT u.id::text FROM generate_series(1, $1::int) i
		JOIN users u ON u.email = `+seedEmail+` ORDER BY i`, users)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issuer := cfg.PublicURL
	now := time.Now()
	var out []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		tok, err := signSeedToken(priv, kid, issuer, userID, now)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != users {
		return nil, fmt.Errorf("seeded %d users, want %d", len(out), users)
	}
	return out, nil
}

func signSeedToken(priv *ecdsa.PrivateKey, kid, issuer, userID string, now time.Time) (string, error) {
	jti := make([]byte, 16)
	fid := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	if _, err := rand.Read(fid); err != nil {
		return "", err
	}
	fid[6] = fid[6]&0x0f | 0x40 // uuid v4: the gateway casts fid to uuid
	fid[8] = fid[8]&0x3f | 0x80
	claims := jwt.MapClaims{
		"iss": issuer, "sub": userID, "aud": []string{issuer + "/mcp"},
		"exp": now.Add(seedTokenTTL).Unix(), "nbf": now.Unix(), "iat": now.Unix(),
		"jti": fmt.Sprintf("%x", jti), "client_id": seedClientID, "scope": oauth.ScopeMCP,
		"fid": fmt.Sprintf("%x-%x-%x-%x-%x", fid[0:4], fid[4:6], fid[6:8], fid[8:10], fid[10:16]),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = kid
	tok.Header["typ"] = "at+jwt"
	return tok.SignedString(priv)
}
