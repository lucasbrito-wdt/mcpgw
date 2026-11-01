package upstreamauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const (
	maxTokenBytes = 4096
	// minTokenLen keeps last4 from revealing most of a short token.
	minTokenLen = 8
)

// ToolLister is the part of upstream.Client (and catalog.Lister) TestToken needs.
type ToolLister interface {
	ListTools(ctx context.Context, serverURL string, cred upstream.Credential) ([]upstream.Tool, upstream.CacheHint, error)
}

// TokenTestResult is what a successful TestToken learned. AccountLabel is empty until an upstream
// exposes an identity endpoint.
type TokenTestResult struct {
	Tools        []string
	AccountLabel string
}

func validateToken(token string) error {
	if len(token) < minTokenLen || len(token) > maxTokenBytes || strings.ContainsAny(token, "\r\n\x00") {
		return ErrInvalidToken
	}
	return nil
}

func checkFormat(token string, hint *string) error {
	if hint != nil && !strings.HasPrefix(token, *hint) {
		return ErrTokenFormat
	}
	return nil
}

// last4Of is the last 4 characters of a token, for the masked display.
func last4Of(token string) string {
	r := []rune(token)
	return string(r[max(len(r)-4, 0):])
}

// tokenServerTarget returns the server's format hint and the credential method a pasted token is
// stored with: "" for a header server, MethodToken for an oauth server that accepts a token
// (header_name set). ErrServerNotFound when the server is not active or takes no token at all;
// ErrTokenNotSupported for an oauth server without the alternative.
func tokenServerTarget(ctx context.Context, db store.DB, serverID string) (hint *string, method string, err error) {
	if !validUUID(serverID) {
		return nil, "", ErrServerNotFound
	}
	var kind string
	var hasHeader bool
	err = db.QueryRow(ctx, `SELECT header_format_hint, auth_kind, COALESCE(header_name, '') <> '' FROM upstream_servers
		WHERE id = $1::uuid AND auth_kind IN ('header', 'static_header', 'oauth') AND status = 'active'`, serverID).Scan(&hint, &kind, &hasHeader)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrServerNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("load token server: %w", err)
	}
	if kind != AuthKindOAuth {
		return hint, "", nil
	}
	if !hasHeader {
		return nil, "", ErrTokenNotSupported
	}
	return hint, MethodToken, nil
}

// SetToken seals and stores a header token: the user's personal one (actorID is that user), or the
// server's shared one (shared=true; actorID goes to connected_by, "" for none). Who may write a
// shared token is the caller's decision. Returns last4. A personal token goes to the default profile.
// The connection's stored environment values are kept.
func (m *Manager) SetToken(ctx context.Context, db store.DB, serverID, actorID, token string, shared bool) (string, error) {
	return m.setToken(ctx, db, ConnectionInput{ServerID: serverID, ActorID: actorID, Profile: DefaultProfile, Token: token, Shared: shared})
}

// SetProfileToken is SetToken for the user's personal connection named profile (created or
// replaced). An invalid name returns ErrInvalidProfile and stores nothing.
func (m *Manager) SetProfileToken(ctx context.Context, db store.DB, serverID, userID, profile, token string) (string, error) {
	if !ValidProfile(profile) {
		return "", ErrInvalidProfile
	}
	return m.setToken(ctx, db, ConnectionInput{ServerID: serverID, ActorID: userID, Profile: profile, Token: token})
}

func (m *Manager) setToken(ctx context.Context, db store.DB, in ConnectionInput) (string, error) {
	if err := validateToken(in.Token); err != nil {
		return "", err
	}
	in, err := m.checkConnectionTarget(ctx, db, in)
	if err != nil {
		return "", err
	}
	if err := checkFormat(in.Token, in.hint); err != nil {
		return "", err
	}
	env, err := m.prepareEnv(ctx, db, in)
	if err != nil {
		return "", err
	}
	target, err := m.prepareURL(ctx, db, in)
	if err != nil {
		return "", err
	}
	last4 := last4Of(in.Token)
	if in.Shared {
		return last4, m.upsertSharedToken(ctx, db, in, last4, env, target)
	}
	return last4, m.upsertPersonalToken(ctx, db, in, last4, env, target)
}

func (m *Manager) upsertPersonalToken(ctx context.Context, db store.DB, in ConnectionInput, last4 string, env envWrite, target urlWrite) error {
	ct, err := m.vault.Seal([]byte(in.Token), credentialProfileAAD(in.ServerID, in.ActorID, in.Profile))
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, profile, shared, access_ciphertext, key_id, last4, status, env_ciphertext, method, url)
		VALUES ($1::uuid, $2::uuid, $6, false, $3, $4, $5, 'active', $7, $9, $10)
		ON CONFLICT (server_id, user_id, profile) DO UPDATE SET access_ciphertext = EXCLUDED.access_ciphertext,
		    refresh_ciphertext = NULL, key_id = EXCLUDED.key_id, expires_at = NULL, last4 = EXCLUDED.last4,
		    status = 'active', revoked_reason = NULL, updated_at = now(), method = EXCLUDED.method,
		    issuer = '', client_id = '', token_endpoint = '', scope = '', account_label = NULL,
		    env_ciphertext = CASE WHEN $8 THEN upstream_credentials.env_ciphertext ELSE EXCLUDED.env_ciphertext END,
		    url = CASE WHEN $11 THEN upstream_credentials.url ELSE EXCLUDED.url END`,
		in.ServerID, in.ActorID, ct, m.vault.KeyID(), last4, in.Profile, env.ct, env.keep, in.method, target.url, target.keep)
	return wrapStore(err)
}

func (m *Manager) upsertSharedToken(ctx context.Context, db store.DB, in ConnectionInput, last4 string, env envWrite, target urlWrite) error {
	ct, err := m.vault.Seal([]byte(in.Token), sharedAAD(in.ServerID))
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, shared, connected_by, access_ciphertext, key_id, last4, status, env_ciphertext, method, url)
		VALUES ($1::uuid, NULL, true, NULLIF($2, '')::uuid, $3, $4, $5, 'active', $6, $8, $9)
		ON CONFLICT (server_id) WHERE shared DO UPDATE SET access_ciphertext = EXCLUDED.access_ciphertext,
		    refresh_ciphertext = NULL, connected_by = EXCLUDED.connected_by, key_id = EXCLUDED.key_id,
		    expires_at = NULL, last4 = EXCLUDED.last4, status = 'active', revoked_reason = NULL, updated_at = now(), method = EXCLUDED.method,
		    env_ciphertext = CASE WHEN $7 THEN upstream_credentials.env_ciphertext ELSE EXCLUDED.env_ciphertext END,
		    url = CASE WHEN $10 THEN upstream_credentials.url ELSE EXCLUDED.url END`,
		in.ServerID, in.ActorID, ct, m.vault.KeyID(), last4, env.ct, env.keep, in.method, target.url, target.keep)
	return wrapStore(err)
}

func wrapStore(err error) error {
	if err != nil {
		return fmt.Errorf("store upstream credential: %w", err)
	}
	return nil
}

// TestToken lists the server's tools with the candidate token. Nothing is stored. An upstream
// 401/403 becomes *UpstreamRejectedError; other upstream errors are returned already scrubbed.
func (m *Manager) TestToken(ctx context.Context, lister ToolLister, serverID, userID, token string) (TokenTestResult, error) {
	return m.TestConnection(ctx, lister, ConnectionInput{ServerID: serverID, ActorID: userID, Profile: DefaultProfile, Token: token})
}

// TestConnection is TestToken for one of the user's connections (in.Profile; Shared and Env are
// ignored). On a server with connection_url the call goes to in.URL, else to the URL the
// connection already has; with neither it is a *URLError.
func (m *Manager) TestConnection(ctx context.Context, lister ToolLister, in ConnectionInput) (TokenTestResult, error) {
	serverID, userID, token := in.ServerID, in.ActorID, in.Token
	if err := validateToken(token); err != nil {
		return TokenTestResult{}, err
	}
	srv, err := m.serverByID(ctx, serverID)
	if err != nil {
		return TokenTestResult{}, err
	}
	if srv.Status != "active" || srv.AuthKind == AuthKindNone {
		return TokenTestResult{}, ErrServerNotFound
	}
	if srv.AuthKind == AuthKindOAuth && (srv.HeaderName == nil || *srv.HeaderName == "") {
		return TokenTestResult{}, ErrTokenNotSupported
	}
	if srv.CredentialEnv != nil {
		return TokenTestResult{}, ErrTokenTestUnsupported
	}
	if srv.HeaderName == nil || *srv.HeaderName == "" {
		return TokenTestResult{}, fmt.Errorf("header not configured for server %q", srv.Slug)
	}
	if err := checkFormat(token, srv.HeaderFormatHint); err != nil {
		return TokenTestResult{}, err
	}
	target, err := m.testTarget(ctx, srv, in)
	if err != nil {
		return TokenTestResult{}, err
	}
	cred := tokenCredential(srv, token, nil)
	tools, _, err := lister.ListTools(upstream.WithOwner(ctx, userID), target, cred)
	var status *upstream.HTTPStatusError
	if errors.As(err, &status) {
		return TokenTestResult{}, &UpstreamRejectedError{Status: status.Status}
	}
	if err != nil {
		return TokenTestResult{}, err
	}
	res := TokenTestResult{Tools: make([]string, len(tools))}
	for i, t := range tools {
		res.Tools[i] = t.Name
	}
	return res, nil
}

// testTarget is the URL a token test calls: the server's, or the connection's own (see TestConnection).
func (m *Manager) testTarget(ctx context.Context, srv *serverRow, in ConnectionInput) (string, error) {
	if !srv.ConnectionURL {
		if in.URL != "" {
			return "", errURLNotAccepted
		}
		return srv.URL, nil
	}
	if in.URL != "" {
		return ValidateConnectionURL(in.URL)
	}
	if !ValidProfile(in.Profile) {
		return "", ErrInvalidProfile
	}
	in.Shared, in.ServerID = false, srv.ID
	if !validUUID(in.ActorID) {
		return "", errURLRequired
	}
	saved, err := savedConnectionURL(ctx, m.pool, in)
	if err == nil && saved == "" {
		err = errURLRequired
	}
	return saved, err
}

// Disconnect deletes the user's personal default-profile row (shared=false) or the server's shared
// row (shared=true, userID ignored). A missing row is not an error.
func (m *Manager) Disconnect(ctx context.Context, db store.DB, serverID, userID string, shared bool) error {
	if !shared {
		return m.DisconnectProfile(ctx, db, serverID, userID, DefaultProfile)
	}
	if !validUUID(serverID) {
		return ErrServerNotFound
	}
	_, err := db.Exec(ctx, `DELETE FROM upstream_credentials WHERE server_id = $1::uuid AND shared`, serverID)
	return err
}

// DisconnectProfile deletes the user's personal row named profile. A missing row is not an error;
// an invalid name returns ErrInvalidProfile.
func (m *Manager) DisconnectProfile(ctx context.Context, db store.DB, serverID, userID, profile string) error {
	if !ValidProfile(profile) {
		return ErrInvalidProfile
	}
	if !validUUID(serverID) {
		return ErrServerNotFound
	}
	if !validUUID(userID) {
		return nil
	}
	_, err := db.Exec(ctx, `DELETE FROM upstream_credentials
		WHERE server_id = $1::uuid AND user_id = $2::uuid AND profile = $3`, serverID, userID, profile)
	return err
}

// Profiles lists the connection names the user can call serverID with, sorted: the user's active
// personal profiles, plus "default" when only the server's shared row provides it. Never nil.
func (m *Manager) Profiles(ctx context.Context, serverID, userID string) ([]string, error) {
	if !validUUID(serverID) {
		return nil, ErrServerNotFound
	}
	if !validUUID(userID) {
		userID = ""
	}
	rows, err := m.pool.Query(ctx, `SELECT DISTINCT profile FROM upstream_credentials
		WHERE server_id = $1::uuid AND status = 'active' AND (user_id = NULLIF($2, '')::uuid OR shared)
		ORDER BY profile`, serverID, userID)
	if err != nil {
		return nil, fmt.Errorf("list connection profiles: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list connection profiles: %w", err)
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// RevokeConnection (admin) keeps the row with status revoked_by_admin and the reason, and drops
// the ciphertexts. userID "" targets the shared row. No such row -> ErrServerNotFound.
func (m *Manager) RevokeConnection(ctx context.Context, db store.DB, serverID, userID, reason string) error {
	if !validUUID(serverID) || userID != "" && !validUUID(userID) {
		return ErrServerNotFound
	}
	tag, err := db.Exec(ctx, `UPDATE upstream_credentials SET status = 'revoked_by_admin', revoked_reason = $3,
		access_ciphertext = NULL, refresh_ciphertext = NULL, env_ciphertext = NULL, updated_at = now()
		WHERE server_id = $1::uuid AND user_id IS NOT DISTINCT FROM NULLIF($2, '')::uuid`,
		serverID, userID, reason)
	return affected(tag.RowsAffected(), err)
}

// SharedValueHasPrefix opens the server's active shared header value and reports whether it already
// starts with prefix (G23). No shared row or an empty prefix -> false.
func (m *Manager) SharedValueHasPrefix(ctx context.Context, db store.DB, serverID, prefix string) (bool, error) {
	if prefix == "" || !validUUID(serverID) {
		return false, nil
	}
	var ct []byte
	err := db.QueryRow(ctx, `SELECT access_ciphertext FROM upstream_credentials
		WHERE server_id = $1::uuid AND shared AND status = 'active'`, serverID).Scan(&ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load shared credential: %w", err)
	}
	value, err := m.vault.Open(ct, sharedAAD(strings.ToLower(serverID)))
	if err != nil {
		return false, wrapOpen(err)
	}
	return strings.HasPrefix(string(value), prefix), nil
}
