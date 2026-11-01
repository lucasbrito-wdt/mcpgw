package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	sessionCookie = "mcpgw_session"
	csrfCookie    = "mcpgw_csrf"
	csrfField     = "csrf_token"
	requestField  = "request"
	csrfTTL       = time.Hour

	sessionTouchGranularity = time.Minute

	aadSession      = "mcpgw/oauth-session"
	aadAuthzRequest = "mcpgw/oauth-authz-request"
)

var errExpired = errors.New("expired")

// session is the login state. It lives only in a sealed, short-lived cookie: no server-side
// table, and it cannot be forged or read without the sealer key.
type session struct {
	UserID    string `json:"uid"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"` // idle deadline; IssuedAt+sessionMaxAge is the absolute one
}

// authzRequest is a validated /authorize request carried through the login and consent
// forms. Sealing makes it tamper-proof, so POST handlers do not trust form fields for it.
type authzRequest struct {
	ClientID      string `json:"cid"`
	RedirectURI   string `json:"ru"`
	State         string `json:"st,omitempty"`
	CodeChallenge string `json:"cc"`
	Scope         string `json:"sc"`
	ExpiresAt     int64  `json:"exp"`
}

// cookieName uses the __Host- prefix on https so the cookie cannot be set by subdomains or
// over plain http.
func (s *Server) cookieName(base string) string {
	if s.secureCookie {
		return "__Host-" + base
	}
	return base
}

func (s *Server) newCookie(base, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name: s.cookieName(base), Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) seal(v any, aad string) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sealed, err := s.sealer.Seal(plain, []byte(aad))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Server) open(raw, aad string, v any) error {
	sealed, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return err
	}
	plain, err := s.sealer.Open(sealed, []byte(aad))
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, v)
}

// StartSession sets the mcpgw_session login cookie; used by the portal login.
func (s *Server) StartSession(w http.ResponseWriter, userID string) error {
	now := s.now()
	return s.writeSession(w, session{UserID: userID, IssuedAt: now.Unix()}, now)
}

// writeSession seals sess with a fresh idle deadline (capped at the absolute one) and sets the cookie.
func (s *Server) writeSession(w http.ResponseWriter, sess session, now time.Time) error {
	idle := now.Add(sessionTTL)
	if hard := time.Unix(sess.IssuedAt, 0).Add(sessionMaxAge); idle.After(hard) {
		idle = hard
	}
	sess.ExpiresAt = idle.Unix()
	value, err := s.seal(sess, aadSession)
	if err != nil {
		return err
	}
	http.SetCookie(w, s.newCookie(sessionCookie, value, idle.Sub(now)))
	return nil
}

// readSession opens the session cookie and enforces both the idle and the absolute limit.
func (s *Server) readSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(s.cookieName(sessionCookie))
	if err != nil {
		return session{}, false
	}
	var sess session
	if s.open(c.Value, aadSession, &sess) != nil || !s.validSession(sess) {
		return session{}, false
	}
	return sess, true
}

func (s *Server) validSession(sess session) bool {
	now := s.now().Unix()
	return sess.UserID != "" && now < sess.ExpiresAt && now < sess.IssuedAt+int64(sessionMaxAge.Seconds())
}

// sessionUser returns the logged-in user id, or "" without a valid session. It never writes.
func (s *Server) sessionUser(r *http.Request) string {
	sess, _ := s.readSession(r)
	return sess.UserID
}

// TouchSession validates the session like sessionUser and slides the idle deadline by re-sealing
// the cookie when the current one is older than a minute. The absolute cap never moves.
func (s *Server) TouchSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	sess, ok := s.readSession(r)
	if !ok {
		return "", false
	}
	now := s.now()
	if time.Unix(sess.ExpiresAt, 0).Sub(now) < sessionTTL-sessionTouchGranularity {
		if err := s.writeSession(w, sess, now); err != nil {
			s.log.Error("oauth: touch session", "err", err)
		}
	}
	return sess.UserID, true
}

func (s *Server) sealRequest(req authzRequest) (string, error) {
	req.ExpiresAt = s.now().Add(authzRequestTTL).Unix()
	return s.seal(req, aadAuthzRequest)
}

func (s *Server) openRequest(raw string) (authzRequest, error) {
	var req authzRequest
	if err := s.open(raw, aadAuthzRequest, &req); err != nil {
		return authzRequest{}, err
	}
	if s.now().Unix() >= req.ExpiresAt {
		return authzRequest{}, errExpired
	}
	return req, nil
}

// csrfToken returns the double-submit token, setting the cookie when absent.
func (s *Server) csrfToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if c, err := r.Cookie(s.cookieName(csrfCookie)); err == nil && len(c.Value) >= 32 {
		return c.Value, nil
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, s.newCookie(csrfCookie, token, csrfTTL))
	return token, nil
}

// checkCSRF requires the form token to equal the cookie (constant time) and rejects
// requests that the browser marks as cross-site.
func (s *Server) checkCSRF(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.issuer {
		return false
	}
	c, err := r.Cookie(s.cookieName(csrfCookie))
	form := r.PostFormValue(csrfField)
	if err != nil || c.Value == "" || form == "" {
		return false
	}
	return constantTimeEqual(c.Value, form)
}

// PurgeExpired deletes authorization codes and refresh tokens past their expiry, plus revoked-family
// rows older than the longest access token lifetime, and returns how
// many rows went. Used refresh tokens are kept until they expire: reuse detection needs them.
func (s *Server) PurgeExpired(ctx context.Context) (int64, error) {
	var total int64
	for _, table := range []string{"oauth_authorization_codes", "oauth_refresh_tokens"} {
		tag, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE expires_at < $1`, s.now())
		if err != nil {
			return total, fmt.Errorf("oauth: purge %s: %w", table, err)
		}
		total += tag.RowsAffected()
	}
	// A revoked family only matters until its last access token is gone.
	tag, err := s.pool.Exec(ctx, `DELETE FROM oauth_revoked_families WHERE revoked_at < $1`,
		s.now().Add(-(accessTokenTTL + validationLeeway)))
	if err != nil {
		return total, fmt.Errorf("oauth: purge oauth_revoked_families: %w", err)
	}
	return total + tag.RowsAffected(), nil
}
