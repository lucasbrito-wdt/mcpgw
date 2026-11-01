package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// codeVerifierPattern is RFC 7636 section 4.1.
var codeVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type tokenError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// errServer marks failures that are ours (500), as opposed to OAuth errors (400).
var errServer = errors.New("server error")

func invalidGrant(description string) *tokenError {
	return &tokenError{Error: "invalid_grant", Description: description}
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if !s.tokenLimiter.allow(clientIP(r), s.now()) {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, tokenError{Error: "slow_down"})
		return
	}
	form, terr := parseTokenForm(w, r)
	if terr == nil {
		var resp tokenResponse
		switch form.Get("grant_type") {
		case grantAuthorizationCode:
			resp, terr = s.exchangeCode(r.Context(), form)
		case grantRefreshToken:
			resp, terr = s.refresh(r.Context(), form)
		default:
			terr = &tokenError{Error: "unsupported_grant_type"}
		}
		if terr == nil {
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
	status := http.StatusBadRequest
	switch terr.Error {
	case "server_error":
		status = http.StatusInternalServerError
	case "invalid_client":
		status = http.StatusUnauthorized
	}
	writeJSON(w, status, terr)
}

func parseTokenForm(w http.ResponseWriter, r *http.Request) (url.Values, *tokenError) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/x-www-form-urlencoded" {
		return nil, &tokenError{Error: "invalid_request", Description: "body must be application/x-www-form-urlencoded"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return nil, &tokenError{Error: "invalid_request", Description: "malformed body"}
	}
	for key, values := range r.PostForm {
		if len(values) > 1 {
			return nil, &tokenError{Error: "invalid_request", Description: "repeated parameter " + key}
		}
	}
	return r.PostForm, nil
}

type storedCode struct {
	id, clientID, userID, redirectURI, challenge, scope string
	expiresAt                                           time.Time
	usedAt                                              *time.Time
}

func (s *Server) exchangeCode(ctx context.Context, form url.Values) (tokenResponse, *tokenError) {
	code, verifier, clientID := form.Get("code"), form.Get("code_verifier"), form.Get("client_id")
	if code == "" || clientID == "" || form.Get("redirect_uri") == "" {
		return tokenResponse{}, &tokenError{Error: "invalid_request", Description: "code, client_id and redirect_uri are required"}
	}
	if !codeVerifierPattern.MatchString(verifier) {
		return tokenResponse{}, &tokenError{Error: "invalid_request", Description: "a valid code_verifier is required"}
	}
	if res := form.Get("resource"); res != "" && !s.isOwnResource(res) {
		return tokenResponse{}, &tokenError{Error: "invalid_target"}
	}
	var resp tokenResponse
	var terr *tokenError
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		resp, terr = s.exchangeCodeTx(ctx, tx, form)
		return rollbackOnServerError(terr)
	})
	return s.finishGrant(resp, terr, err)
}

// exchangeCodeTx consumes the code before any check, so a failed attempt cannot be retried,
// and revokes the grant family when an already used code is presented again.
func (s *Server) exchangeCodeTx(ctx context.Context, tx pgx.Tx, form url.Values) (tokenResponse, *tokenError) {
	var c storedCode
	err := tx.QueryRow(ctx, `SELECT id::text, client_id, user_id::text, redirect_uri, code_challenge, scope,
		expires_at, used_at FROM oauth_authorization_codes WHERE code_hash = $1 FOR UPDATE`,
		hashToken(form.Get("code"))).Scan(&c.id, &c.clientID, &c.userID, &c.redirectURI, &c.challenge,
		&c.scope, &c.expiresAt, &c.usedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tokenResponse{}, invalidGrant("unknown authorization code")
	}
	if err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	if c.usedAt != nil {
		s.log.Warn("oauth: authorization code reuse, revoking grant", "client_id", c.clientID, "family_id", c.id)
		if err := s.revokeFamily(ctx, tx, c.id); err != nil {
			return tokenResponse{}, s.tokenServerError(err)
		}
		return tokenResponse{}, invalidGrant("authorization code already used")
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_authorization_codes SET used_at = $2 WHERE id = $1::uuid`, c.id, s.now()); err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	if terr := s.checkCode(c, form); terr != nil {
		return tokenResponse{}, terr
	}
	return s.issueTokens(ctx, tx, grant{userID: c.userID, clientID: c.clientID, scope: c.scope, familyID: c.id}, "")
}

func (s *Server) checkCode(c storedCode, form url.Values) *tokenError {
	if !s.now().Before(c.expiresAt) {
		return invalidGrant("authorization code expired")
	}
	if !constantTimeEqual(c.clientID, form.Get("client_id")) {
		return invalidGrant("authorization code was issued to another client")
	}
	if !constantTimeEqual(c.redirectURI, form.Get("redirect_uri")) {
		return invalidGrant("redirect_uri does not match the authorization request")
	}
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if !constantTimeEqual(base64.RawURLEncoding.EncodeToString(sum[:]), c.challenge) {
		return invalidGrant("PKCE verification failed")
	}
	return nil
}

type storedRefresh struct {
	id, familyID, clientID, userID, scope string
	expiresAt                             time.Time
	usedAt, revokedAt                     *time.Time
}

func (s *Server) refresh(ctx context.Context, form url.Values) (tokenResponse, *tokenError) {
	if form.Get("refresh_token") == "" || form.Get("client_id") == "" {
		return tokenResponse{}, &tokenError{Error: "invalid_request", Description: "refresh_token and client_id are required"}
	}
	if sc, ok := grantedScope(form.Get("scope")); !ok || sc != ScopeMCP {
		return tokenResponse{}, &tokenError{Error: "invalid_scope"}
	}
	if res := form.Get("resource"); res != "" && !s.isOwnResource(res) {
		return tokenResponse{}, &tokenError{Error: "invalid_target"}
	}
	var resp tokenResponse
	var terr *tokenError
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		resp, terr = s.refreshTx(ctx, tx, form)
		return rollbackOnServerError(terr)
	})
	return s.finishGrant(resp, terr, err)
}

// refreshTx rotates the refresh token. Presenting a token that was already rotated means it
// leaked (or a client bug): the whole family is revoked, including live access tokens.
func (s *Server) refreshTx(ctx context.Context, tx pgx.Tx, form url.Values) (tokenResponse, *tokenError) {
	var t storedRefresh
	err := tx.QueryRow(ctx, `SELECT id::text, family_id::text, client_id, user_id::text, scope, expires_at,
		used_at, revoked_at FROM oauth_refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
		hashToken(form.Get("refresh_token"))).Scan(&t.id, &t.familyID, &t.clientID, &t.userID, &t.scope,
		&t.expiresAt, &t.usedAt, &t.revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tokenResponse{}, invalidGrant("unknown refresh token")
	}
	if err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	switch {
	case !constantTimeEqual(t.clientID, form.Get("client_id")):
		return tokenResponse{}, invalidGrant("refresh token was issued to another client")
	case t.revokedAt != nil:
		return tokenResponse{}, invalidGrant("refresh token revoked")
	case t.usedAt != nil:
		s.log.Warn("oauth: refresh token reuse, revoking grant", "client_id", t.clientID, "family_id", t.familyID)
		if err := s.revokeFamily(ctx, tx, t.familyID); err != nil {
			return tokenResponse{}, s.tokenServerError(err)
		}
		return tokenResponse{}, invalidGrant("refresh token already used")
	case !s.now().Before(t.expiresAt):
		return tokenResponse{}, invalidGrant("refresh token expired")
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_refresh_tokens SET used_at = $2 WHERE id = $1::uuid`, t.id, s.now()); err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	return s.issueTokens(ctx, tx, grant{userID: t.userID, clientID: t.clientID, scope: t.scope, familyID: t.familyID}, t.id)
}

// issueTokens signs an access token and, when the client allows it, stores a new refresh
// token in the grant family. parentID is the rotated token ("" for a code exchange).
func (s *Server) issueTokens(ctx context.Context, tx pgx.Tx, g grant, parentID string) (tokenResponse, *tokenError) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT metadata FROM oauth_clients WHERE client_id = $1`, g.clientID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return tokenResponse{}, &tokenError{Error: "invalid_client"}
	}
	if err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	var meta ClientMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	access, _, err := s.issueAccessToken(g)
	if err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	resp := tokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(accessTokenTTL.Seconds()), Scope: g.scope}
	if !meta.allowsRefresh() {
		return resp, nil
	}
	if resp.RefreshToken, err = s.storeRefreshToken(ctx, tx, g, parentID); err != nil {
		return tokenResponse{}, s.tokenServerError(err)
	}
	return resp, nil
}

func (s *Server) storeRefreshToken(ctx context.Context, tx pgx.Tx, g grant, parentID string) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	var parent *string
	if parentID != "" {
		parent = &parentID
	}
	_, err = tx.Exec(ctx, `INSERT INTO oauth_refresh_tokens
		(token_hash, family_id, parent_id, client_id, user_id, scope, expires_at)
		VALUES ($1, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7)`,
		hashToken(token), g.familyID, parent, g.clientID, g.userID, g.scope, s.now().Add(refreshTokenTTL))
	return token, err
}

// RevokeFamily revokes a grant family (admin action): access tokens of the family are rejected at
// once in this process (grant cache) and by every instance through oauth_revoked_families.
func (s *Server) RevokeFamily(ctx context.Context, familyID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return s.revokeFamily(ctx, tx, familyID) })
}

// revokeFamily marks the whole grant revoked: in oauth_revoked_families (which also covers
// clients that never got a refresh token, so their access tokens die too) and on every
// refresh token of the family.
func (s *Server) revokeFamily(ctx context.Context, tx pgx.Tx, familyID string) error {
	s.grants.revoke(familyID)
	now := s.now()
	if _, err := tx.Exec(ctx, `INSERT INTO oauth_revoked_families (family_id, revoked_at)
		VALUES ($1::uuid, $2) ON CONFLICT (family_id) DO NOTHING`, familyID, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE oauth_refresh_tokens SET revoked_at = $2
		WHERE family_id = $1::uuid AND revoked_at IS NULL`, familyID, now)
	return err
}

// tokenServerError logs the cause and returns a generic error: internals never reach clients.
func (s *Server) tokenServerError(err error) *tokenError {
	s.log.Error("oauth: token endpoint", "err", err)
	return &tokenError{Error: "server_error"}
}

// rollbackOnServerError keeps OAuth outcomes (they must commit) and aborts on our failures.
func rollbackOnServerError(terr *tokenError) error {
	if terr != nil && terr.Error == "server_error" {
		return errServer
	}
	return nil
}

// finishGrant merges the transaction outcome. OAuth errors still commit (code consumption,
// family revocation); a commit failure turns any result into a server error.
func (s *Server) finishGrant(resp tokenResponse, terr *tokenError, txErr error) (tokenResponse, *tokenError) {
	if txErr != nil {
		switch {
		case errors.Is(txErr, context.Canceled):
			// The client hung up mid-request (e.g. an agent closed during a refresh): nothing to fix.
			s.log.Debug("oauth: token transaction canceled by client")
		case !errors.Is(txErr, errServer):
			s.log.Error("oauth: token transaction", "err", txErr)
		}
		return tokenResponse{}, &tokenError{Error: "server_error"}
	}
	if terr != nil {
		return tokenResponse{}, terr
	}
	return resp, nil
}
