package upstreamauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

var oauthErrorCode = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// tokenError is an OAuth error response; only the sanitized error code is kept, never the
// description (it is attacker-influenced text and may echo request data).
type tokenError struct {
	Code   string
	Status int
}

func (e *tokenError) Error() string {
	return fmt.Sprintf("token endpoint error %q (HTTP %d)", e.Code, e.Status)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// requestToken POSTs a token request (authorization_code or refresh_token grant).
func (m *Manager) requestToken(ctx context.Context, endpoint string, form url.Values, c oauthClient) (*tokenResponse, error) {
	var setup func(*http.Request)
	if c.Secret == "" {
		form.Set("client_id", c.ID)
	} else {
		setup = func(r *http.Request) { r.SetBasicAuth(url.QueryEscape(c.ID), url.QueryEscape(c.Secret)) }
	}
	resp, err := m.http.post(ctx, endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), setup)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	if resp.status != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(resp.body, &e)
		if !oauthErrorCode.MatchString(e.Error) {
			e.Error = "unknown"
		}
		return nil, &tokenError{Code: e.Error, Status: resp.status}
	}
	var tr tokenResponse
	if err := json.Unmarshal(resp.body, &tr); err != nil || tr.AccessToken == "" {
		return nil, errors.New("token request: malformed response")
	}
	if !strings.EqualFold(tr.TokenType, "bearer") {
		return nil, errors.New("token request: unsupported token type")
	}
	return &tr, nil
}

func (m *Manager) expiryOf(tr *tokenResponse) *time.Time {
	if tr.ExpiresIn <= 0 {
		return nil
	}
	t := m.now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return &t
}

// CredentialFor returns the credential to call the upstream server serverID on behalf of userID.
//
//   - auth_kind none: (nil, nil);
//   - header (and legacy static_header): the user's personal token, else the server's shared one,
//     else (until MigrateLegacyHeaders ran) the legacy server-level header. The value is the
//     server's header_prefix plus the token;
//   - oauth: the personal bearer token, else the shared one, refreshed first when it expires
//     within 60s. A connection whose method is "token" (pasted on a server with an alternative
//     header) is sent as that header instead, without refresh.
//
// With nothing usable the error is a *NotConnectedError (errors.Is(err, ErrNotConnected)) carrying
// the URL to connect (portal for header servers). An empty userID (catalog sync) skips the personal
// lookup. Vault failures (tampered row, wrong AAD) are returned as-is and are not ErrNotConnected.
// It is ProfileCredentialFor with the default profile.
func (m *Manager) CredentialFor(ctx context.Context, serverID, userID string) (upstream.Credential, error) {
	return m.ProfileCredentialFor(ctx, serverID, userID, DefaultProfile)
}

// ProfileCredentialFor is CredentialFor for one of the user's named connections. profile "" means
// "default", which behaves exactly like CredentialFor (personal default, else shared, else legacy).
// A named profile uses only the user's active personal row with that name, never the shared one;
// when missing the *NotConnectedError carries the profile and the URL to connect it. An invalid
// name returns ErrInvalidProfile.
func (m *Manager) ProfileCredentialFor(ctx context.Context, serverID, userID, profile string) (upstream.Credential, error) {
	if profile == "" {
		profile = DefaultProfile
	}
	if !ValidProfile(profile) {
		return nil, ErrInvalidProfile
	}
	srv, err := m.serverByID(ctx, serverID)
	if err != nil {
		return nil, err
	}
	switch srv.AuthKind {
	case AuthKindNone:
		return nil, nil
	case AuthKindHeader, AuthKindStatic:
		return m.headerCredential(ctx, srv, userID, profile)
	case AuthKindOAuth:
		return m.oauthCredential(ctx, srv, userID, profile)
	}
	return nil, fmt.Errorf("unsupported auth kind %q", srv.AuthKind)
}

// AnyCredentialFor is for catalog sync, where the tool list is the same for any of the user's
// accounts: CredentialFor, and when that is ErrNotConnected, the credential of the user's first
// active named profile (sorted). With none the original not-connected error is returned. Calls on
// behalf of a user must keep using ProfileCredentialFor, which never falls back.
func (m *Manager) AnyCredentialFor(ctx context.Context, serverID, userID string) (upstream.Credential, error) {
	cred, err := m.CredentialFor(ctx, serverID, userID)
	if err == nil || !errors.Is(err, ErrNotConnected) || userID == "" {
		return cred, err
	}
	names, perr := m.Profiles(ctx, serverID, userID)
	if perr != nil {
		return nil, perr
	}
	for _, name := range names {
		if name != DefaultProfile {
			return m.ProfileCredentialFor(ctx, serverID, userID, name)
		}
	}
	return nil, err
}

func (m *Manager) headerCredential(ctx context.Context, srv *serverRow, userID, profile string) (upstream.Credential, error) {
	if srv.CredentialEnv == nil && (srv.HeaderName == nil || *srv.HeaderName == "") {
		return nil, fmt.Errorf("header not configured for server %q", srv.Slug)
	}
	row, err := m.resolveCredential(ctx, srv.ID, userID, profile)
	if errors.Is(err, pgx.ErrNoRows) {
		if len(srv.LegacyHeaderCT) > 0 && profile == DefaultProfile {
			return m.openStatic(srv)
		}
		return nil, m.notConnected(srv, profile)
	}
	if err != nil {
		return nil, fmt.Errorf("load upstream credential: %w", err)
	}
	token, err := m.vault.Open(row.Access, row.aad())
	if err != nil {
		return nil, fmt.Errorf("open upstream header token: %w", err)
	}
	env, err := m.openConnectionEnv(srv, row)
	if err != nil {
		return nil, err
	}
	m.touchLastUsed(ctx, row.ID)
	cred := tokenCredential(srv, string(token), env)
	if srv.ConnectionURL && row.URL != "" {
		return upstream.URLCredential{Credential: cred, URL: row.URL}, nil
	}
	return cred, nil
}

// tokenCredential applies a header token: for a stdio server as its credential environment
// variable (the raw token, plus the connection's own variables), else as the configured HTTP
// header with its prefix.
func tokenCredential(srv *serverRow, token string, env map[string]string) upstream.Credential {
	if srv.CredentialEnv != nil {
		return upstream.EnvCredential{Name: *srv.CredentialEnv, Value: token, Env: env}
	}
	return upstream.StaticHeader{Name: *srv.HeaderName, Value: srv.HeaderPrefix + token}
}

func (m *Manager) oauthCredential(ctx context.Context, srv *serverRow, userID, profile string) (upstream.Credential, error) {
	row, err := m.resolveCredential(ctx, srv.ID, userID, profile)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, m.notConnected(srv, profile)
	}
	if err != nil {
		return nil, fmt.Errorf("load upstream credential: %w", err)
	}
	if row.Method == MethodToken {
		return m.pastedTokenCredential(ctx, srv, row, profile)
	}
	if m.expiring(row) {
		if len(row.Refresh) == 0 {
			if m.expired(row) {
				return nil, m.notConnected(srv, profile)
			}
			// about to expire but still valid and nothing to refresh with
		} else {
			token, err := m.refreshShared(ctx, srv, row.ID, profile)
			if err != nil {
				return nil, err
			}
			m.touchLastUsed(ctx, row.ID)
			return upstream.BearerToken(token), nil
		}
	}
	cred, err := m.bearer(row)
	if err == nil {
		m.touchLastUsed(ctx, row.ID)
	}
	return cred, err
}

// pastedTokenCredential sends the token a user pasted on an oauth server in the server's alternative
// header, never refreshing it. Without that header (the admin cleared it) the user must reconnect.
func (m *Manager) pastedTokenCredential(ctx context.Context, srv *serverRow, row *credentialRow, profile string) (upstream.Credential, error) {
	if srv.HeaderName == nil || *srv.HeaderName == "" {
		return nil, m.notConnected(srv, profile)
	}
	token, err := m.vault.Open(row.Access, row.aad())
	if err != nil {
		return nil, fmt.Errorf("open upstream header token: %w", err)
	}
	m.touchLastUsed(ctx, row.ID)
	return tokenCredential(srv, string(token), nil), nil
}

func (m *Manager) bearer(row *credentialRow) (upstream.Credential, error) {
	token, err := m.vault.Open(row.Access, row.aad())
	if err != nil {
		return nil, fmt.Errorf("open upstream access token: %w", err)
	}
	return upstream.BearerToken(token), nil
}

func (m *Manager) expiring(row *credentialRow) bool {
	return row.ExpiresAt != nil && m.now().Add(refreshSkew).After(*row.ExpiresAt)
}

func (m *Manager) expired(row *credentialRow) bool {
	return row.ExpiresAt != nil && !m.now().Before(*row.ExpiresAt)
}

// refreshShared single-flights the refresh per credential row so concurrent callers never
// spend the same refresh token twice. The flight outlives any single caller's cancellation.
func (m *Manager) refreshShared(ctx context.Context, srv *serverRow, rowID, profile string) (string, error) {
	ch := m.flights.DoChan(rowID, func() (any, error) {
		fctx, cancel := detached(ctx, refreshTimeout)
		defer cancel()
		return m.refresh(fctx, srv, rowID, profile)
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		return res.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// refresh runs under a row lock so that other gateway instances serialize with this one; the
// expiry is re-checked after taking the lock because someone else may have just refreshed.
func (m *Manager) refresh(ctx context.Context, srv *serverRow, rowID, profile string) (string, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	row, err := lockCredential(ctx, tx, rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", m.notConnected(srv, profile)
	}
	if err != nil {
		return "", fmt.Errorf("lock upstream credential: %w", err)
	}
	if row.Status != "active" { // another instance already marked it failed or an admin revoked it
		return "", m.notConnected(srv, profile)
	}
	aad := row.aad()
	if !m.expiring(row) {
		token, err := m.vault.Open(row.Access, aad)
		return string(token), wrapOpen(err)
	}
	refreshToken, err := m.vault.Open(row.Refresh, aad)
	if err != nil {
		return "", wrapOpen(err)
	}
	client, err := m.clientForGrant(ctx, srv, row.Issuer, row.ClientID)
	if err != nil {
		return "", err
	}
	resource, err := canonicalResource(srv.URL)
	if err != nil {
		return "", err
	}
	tr, err := m.requestToken(ctx, row.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {string(refreshToken)},
		"resource":      {resource},
	}, client)
	var te *tokenError
	if errors.As(err, &te) && te.Code == "invalid_grant" {
		// Keep the row (status and reason stay visible to the user and the admin), drop the secrets.
		if _, uerr := tx.Exec(ctx, `UPDATE upstream_credentials SET status = 'refresh_failed', access_ciphertext = NULL,
			refresh_ciphertext = NULL, updated_at = now() WHERE id = $1::uuid`, row.ID); uerr != nil {
			return "", fmt.Errorf("mark credential refresh_failed: %w", uerr)
		}
		if cerr := tx.Commit(ctx); cerr != nil {
			return "", fmt.Errorf("commit refresh_failed credential: %w", cerr)
		}
		return "", m.notConnected(srv, profile)
	}
	if err != nil {
		return "", err
	}
	if err := m.storeRefreshed(ctx, tx, row, tr); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit refresh: %w", err)
	}
	return tr.AccessToken, nil
}

// storeRefreshed saves the new tokens; when the AS rotates the refresh token the new one
// replaces the old, otherwise the old one stays valid and is kept.
func (m *Manager) storeRefreshed(ctx context.Context, tx pgx.Tx, row *credentialRow, tr *tokenResponse) error {
	aad := row.aad()
	access, err := m.vault.Seal([]byte(tr.AccessToken), aad)
	if err != nil {
		return err
	}
	refresh := row.Refresh
	if tr.RefreshToken != "" {
		if refresh, err = m.vault.Seal([]byte(tr.RefreshToken), aad); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE upstream_credentials
		SET access_ciphertext = $2, refresh_ciphertext = $3, key_id = $4, expires_at = $5,
		    scope = CASE WHEN $6 = '' THEN scope ELSE $6 END, updated_at = now()
		WHERE id = $1::uuid`,
		row.ID, access, refresh, m.vault.KeyID(), m.expiryOf(tr), tr.Scope)
	if err != nil {
		return fmt.Errorf("store refreshed credential: %w", err)
	}
	return nil
}

func wrapOpen(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("open upstream credential: %w", err)
}
