package oauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

const maxEmailLen = 320

// Soft lockout per account, independent of the client IP: this many failed logins within the
// window block further attempts for that email until tokens refill (one per window/burst).
const (
	emailFailureBurst  = 10
	emailFailureWindow = 15 * time.Minute
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, req authzRequest, client *Client) {
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	userID, err := s.Login(r.Context(), r, email, r.PostFormValue("password"))
	switch {
	case errors.Is(err, ErrLoginRateLimited):
		w.Header().Set("Retry-After", "60")
		s.renderLogin(w, r, req, client, http.StatusTooManyRequests, i18n.T("Too many attempts. Wait a few minutes and try again.", "Muitas tentativas. Aguarde alguns minutos e tente de novo."))
		return
	case errors.Is(err, ErrBadCredentials):
		s.renderLogin(w, r, req, client, http.StatusUnauthorized, i18n.T("Incorrect email or password.", "E-mail ou senha incorretos."))
		return
	case err != nil:
		s.serverError(w, r, "authenticate", err)
		return
	}
	if err := s.StartSession(w, userID); err != nil {
		s.serverError(w, r, "start session", err)
		return
	}
	s.renderConsent(w, r, req, client, userID, email)
}

var (
	// ErrBadCredentials: unknown email or wrong password (callers answer one generic 401).
	ErrBadCredentials = errors.New("bad credentials")
	// ErrLoginRateLimited: IP, IP+email or per-email failure limit hit (callers answer 429).
	ErrLoginRateLimited = errors.New("login rate limited")
)

// Login applies the login rate limits shared by every login form (OAuth, admin, portal) and
// checks the password. It returns the user id.
func (s *Server) Login(ctx context.Context, r *http.Request, email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	ip, now := clientIP(r), s.now()
	if !s.loginIPLimiter.allow(ip, now) || !s.loginLimiter.allow(ip+"|"+email, now) ||
		(email != "" && s.emailFailures.blocked(email, now)) {
		return "", ErrLoginRateLimited
	}
	userID, err := s.authenticate(ctx, email, password)
	if errors.Is(err, ErrBadCredentials) {
		if email != "" {
			s.emailFailures.spend(email, now)
		}
		s.log.Info("oauth: login failed", "ip", ip)
		return "", err
	}
	if err != nil {
		return "", err
	}
	s.emailFailures.reset(email)
	return userID, nil
}

// authenticate checks email and password. Unknown emails still pay for one argon2id
// verification, so timing does not reveal which accounts exist. Concurrent verifications
// are capped because each one allocates the full argon2 memory cost.
func (s *Server) authenticate(ctx context.Context, email, password string) (string, error) {
	if email == "" || password == "" || len(email) > maxEmailLen {
		return "", ErrBadCredentials
	}
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	var userID, hash string
	err := s.pool.QueryRow(ctx, `SELECT id::text, password_hash FROM users WHERE email = $1`, email).Scan(&userID, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword(password, dummyPasswordHash())
		return "", ErrBadCredentials
	}
	if err != nil {
		return "", err
	}
	ok, err := VerifyPassword(password, hash)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrBadCredentials
	}
	return userID, nil
}
