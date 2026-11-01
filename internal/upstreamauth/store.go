package upstreamauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

type serverRow struct {
	ID, Slug, URL, AuthKind, Status string
	ClientID                        *string
	ClientSecretCT                  []byte
	// HeaderName is header_name, or the legacy static_header_name until the re-seal ran.
	HeaderName       *string
	HeaderPrefix     string
	HeaderFormatHint *string
	LegacyHeaderCT   []byte
	// CredentialEnv is set for stdio servers: the token goes to this environment variable.
	CredentialEnv *string
	// ConnectionEnv are the variables each connection may set (stdio with CredentialEnv only).
	ConnectionEnv []string
	// ConnectionURL: each connection may name its own upstream URL (header servers over HTTP).
	ConnectionURL bool
}

const serverColumns = `id::text, slug, url, auth_kind, status, oauth_client_id,
	oauth_client_secret_ciphertext, COALESCE(header_name, static_header_name), header_prefix,
	header_format_hint, static_header_ciphertext, credential_env, connection_env, connection_url`

func scanServer(row pgx.Row) (*serverRow, error) {
	var s serverRow
	err := row.Scan(&s.ID, &s.Slug, &s.URL, &s.AuthKind, &s.Status, &s.ClientID,
		&s.ClientSecretCT, &s.HeaderName, &s.HeaderPrefix, &s.HeaderFormatHint, &s.LegacyHeaderCT, &s.CredentialEnv, &s.ConnectionEnv, &s.ConnectionURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrServerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load upstream server: %w", err)
	}
	return &s, nil
}

func (m *Manager) serverBySlug(ctx context.Context, slug string) (*serverRow, error) {
	return scanServer(m.pool.QueryRow(ctx, `SELECT `+serverColumns+` FROM upstream_servers WHERE slug = $1`, slug))
}

func (m *Manager) serverByID(ctx context.Context, id string) (*serverRow, error) {
	if !validUUID(id) {
		return nil, ErrServerNotFound
	}
	return scanServer(m.pool.QueryRow(ctx, `SELECT `+serverColumns+` FROM upstream_servers WHERE id = $1::uuid`, id))
}

// validUUID is a cheap shape check so malformed ids never reach a ::uuid cast.
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'):
			return false
		}
	}
	return true
}

// AAD scheme: every sealed value is bound to the row it lives in.
func credentialAAD(serverID, userID string) []byte {
	return []byte("upstream_credentials:" + serverID + ":" + userID)
}

// credentialProfileAAD binds a named (non-default) profile's credential to its row; the default
// profile keeps credentialAAD so rows sealed before profiles existed still open.
func credentialProfileAAD(serverID, userID, profile string) []byte {
	if profile == DefaultProfile {
		return credentialAAD(serverID, userID)
	}
	return []byte("upstream_credentials:" + serverID + ":" + userID + ":profile:" + profile)
}

// sharedAAD binds a shared credential to its server; it never equals a personal credentialAAD.
func sharedAAD(serverID string) []byte {
	return []byte("upstream_credentials:" + serverID + ":shared")
}
func stateAAD(serverID, userID string) []byte {
	return []byte("upstream_oauth_states:" + serverID + ":" + userID)
}

// stateProfileAAD is stateAAD for the default profile and binds any other profile name.
func stateProfileAAD(serverID, userID, profile string) []byte {
	if profile == DefaultProfile {
		return stateAAD(serverID, userID)
	}
	return []byte("upstream_oauth_states:" + serverID + ":" + userID + ":profile:" + profile)
}
func serverFieldAAD(serverID, field string) []byte {
	return []byte("upstream_servers:" + serverID + ":" + field)
}
func dcrAAD(serverID, issuer string) []byte {
	return []byte("upstream_oauth_clients:" + serverID + ":" + issuer)
}

func hashState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

type credentialRow struct {
	ID, ServerID, Status string
	UserID               string // "" for a shared row
	Profile              string // always DefaultProfile for a shared row
	Shared               bool
	// Method is the stored upstream_credentials.method ("" = legacy, see MethodOAuth).
	Method          string
	Access, Refresh []byte
	// Env is the sealed JSON object of the connection's own environment values (stdio only).
	Env []byte
	// URL is the connection's own upstream address ("" = the server's).
	URL                             string
	ExpiresAt                       *time.Time
	Issuer, ClientID, TokenEndpoint string
}

const credentialColumns = `id::text, server_id::text, COALESCE(user_id::text, ''), profile, shared, status, method,
	access_ciphertext, refresh_ciphertext, expires_at,
	COALESCE(issuer, ''), COALESCE(client_id, ''), COALESCE(token_endpoint, ''), env_ciphertext, COALESCE(url, '')`

func scanCredential(row pgx.Row) (*credentialRow, error) {
	var c credentialRow
	err := row.Scan(&c.ID, &c.ServerID, &c.UserID, &c.Profile, &c.Shared, &c.Status, &c.Method, &c.Access, &c.Refresh,
		&c.ExpiresAt, &c.Issuer, &c.ClientID, &c.TokenEndpoint, &c.Env, &c.URL)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// aad is the AAD the row's ciphertexts are bound to.
func (c *credentialRow) aad() []byte {
	if c.Shared {
		return sharedAAD(c.ServerID)
	}
	return credentialProfileAAD(c.ServerID, c.UserID, c.Profile)
}

// resolveCredential picks the active credential to use for profile: the user's personal row with
// that profile, else (default profile only) the server's shared one. An empty or malformed userID
// matches only the shared row.
func (m *Manager) resolveCredential(ctx context.Context, serverID, userID, profile string) (*credentialRow, error) {
	if !validUUID(userID) {
		userID = ""
	}
	return scanCredential(m.pool.QueryRow(ctx, `SELECT `+credentialColumns+` FROM upstream_credentials
		WHERE server_id = $1::uuid AND status = 'active'
		    AND ((user_id = NULLIF($2, '')::uuid AND profile = $3) OR (shared AND $3 = 'default'))
		ORDER BY shared LIMIT 1`, serverID, userID, profile))
}

// lockCredential reloads one row by id under a row lock, inside the refresh transaction.
func lockCredential(ctx context.Context, tx pgx.Tx, rowID string) (*credentialRow, error) {
	return scanCredential(tx.QueryRow(ctx, `SELECT `+credentialColumns+` FROM upstream_credentials
		WHERE id = $1::uuid FOR UPDATE`, rowID))
}

// touchLastUsed records a use at most once a minute per row; it is best effort.
func (m *Manager) touchLastUsed(ctx context.Context, rowID string) {
	_, _ = m.pool.Exec(context.WithoutCancel(ctx), `UPDATE upstream_credentials SET last_used_at = now()
		WHERE id = $1::uuid AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, rowID)
}

// openStatic opens the legacy server-level header (pre-0007 storage, until MigrateLegacyHeaders ran).
func (m *Manager) openStatic(s *serverRow) (upstream.Credential, error) {
	if s.HeaderName == nil || len(s.LegacyHeaderCT) == 0 {
		return nil, fmt.Errorf("static header not configured for server %q", s.Slug)
	}
	value, err := m.vault.Open(s.LegacyHeaderCT, serverFieldAAD(s.ID, "static_header"))
	if err != nil {
		return nil, fmt.Errorf("open static header of server %q: %w", s.Slug, err)
	}
	return upstream.StaticHeader{Name: *s.HeaderName, Value: string(value)}, nil
}

// SetStaticHeader stores the sealed header an auth_kind = 'static_header' server is called with.
func (m *Manager) SetStaticHeader(ctx context.Context, db store.DB, serverID, name, value string) error {
	if !headerNamePattern.MatchString(name) {
		return errors.New("invalid header name")
	}
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("invalid header value")
	}
	if !validUUID(serverID) {
		return ErrServerNotFound
	}
	ct, err := m.vault.Seal([]byte(value), serverFieldAAD(strings.ToLower(serverID), "static_header"))
	if err != nil {
		return err
	}
	tag, err := db.Exec(ctx, `UPDATE upstream_servers SET static_header_name = $2, static_header_ciphertext = $3
		WHERE id = $1::uuid`, serverID, name, ct)
	return affected(tag.RowsAffected(), err)
}

// SetStaticOAuthClient stores an admin pre-registered OAuth client for the server. An empty secret
// keeps the stored one (there is no way to clear it). It takes precedence over CIMD and DCR.
func (m *Manager) SetStaticOAuthClient(ctx context.Context, db store.DB, serverID, clientID, secret string) error {
	if clientID == "" || !validUUID(serverID) {
		return errors.New("invalid client id or server id")
	}
	var ct []byte
	if secret != "" {
		var err error
		if ct, err = m.vault.Seal([]byte(secret), serverFieldAAD(strings.ToLower(serverID), "oauth_client_secret")); err != nil {
			return err
		}
	}
	tag, err := db.Exec(ctx, `UPDATE upstream_servers SET oauth_client_id = $2, oauth_client_secret_ciphertext = COALESCE($3, oauth_client_secret_ciphertext)
		WHERE id = $1::uuid`, serverID, clientID, ct)
	return affected(tag.RowsAffected(), err)
}

// PurgeExpiredStates deletes expired connect states; run it periodically.
func (m *Manager) PurgeExpiredStates(ctx context.Context) (int64, error) {
	tag, err := m.pool.Exec(ctx, `DELETE FROM upstream_oauth_states WHERE expires_at < now()`)
	return tag.RowsAffected(), err
}

func affected(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrServerNotFound
	}
	return nil
}
