package adminapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
)

const (
	sessionCookie = "mcpgw_admin"
	csrfCookie    = "mcpgw_admin_csrf"
	aadSession    = "admin_session"

	idleTimeout      = 30 * time.Minute
	absoluteTimeout  = 8 * time.Hour
	touchGranularity = time.Minute
	reauthWindow     = 5 * time.Minute
)

var msgBadCredentials = i18n.T("Incorrect email or password.", "E-mail ou senha incorretos.")

// session lives only in the sealed mcpgw_admin cookie: no server-side table.
type session struct {
	UID      string `json:"uid"`
	IssuedAt int64  `json:"iat"`  // absolute limit 8 h from here
	LastSeen int64  `json:"ls"`   // idle limit 30 min; re-sealed when older than 1 min
	ReauthAt int64  `json:"ra"`   // 0 = never
	CSRF     string `json:"csrf"` // equals the csrf cookie
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// login answers the same 401 for a wrong password, an unknown email and a non-admin, so the
// endpoint is no account oracle.
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Origin") != a.origin {
		writeCSRFError(w, r)
		return
	}
	var in credentials
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	uid, err := a.o.Login(r.Context(), r, in.Email, in.Password)
	if err != nil {
		writeLoginError(w, r, err)
		return
	}
	isAdmin, _, err := a.lookupUser(r.Context(), uid)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	if !isAdmin {
		writeLoginError(w, r, oauth.ErrBadCredentials)
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	now := a.o.Now().Unix()
	sess := session{UID: uid, IssuedAt: now, LastSeen: now, CSRF: base64.RawURLEncoding.EncodeToString(token)}
	if err := a.writeSession(w, sess); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeLoginError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, oauth.ErrBadCredentials):
		apijson.WriteErrorFor(w, r, http.StatusUnauthorized, "unauthorized", msgBadCredentials, nil)
	case errors.Is(err, oauth.ErrLoginRateLimited):
		w.Header().Set("Retry-After", "60")
		apijson.WriteErrorFor(w, r, http.StatusTooManyRequests, "rate_limited", i18n.T("Too many attempts. Try again in a minute.", "Muitas tentativas. Tente novamente em um minuto."), nil)
	default:
		apijson.FromError(w, r, err)
	}
}

func (a *API) logout(w http.ResponseWriter, _ *http.Request) {
	a.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// reauth re-verifies the session user's password; secret writes need it within 5 minutes.
func (a *API) reauth(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	act, _ := actorFrom(r.Context())
	if _, err := a.o.Login(r.Context(), r, act.email, in.Password); err != nil {
		if errors.Is(err, oauth.ErrBadCredentials) {
			apijson.WriteErrorFor(w, r, http.StatusUnauthorized, "unauthorized", i18n.T("Incorrect password.", "Senha incorreta."), nil)
			return
		}
		writeLoginError(w, r, err)
		return
	}
	act.sess.ReauthAt = a.o.Now().Unix()
	if err := a.writeSession(w, act.sess); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireReauth writes 403 reauth_required and returns false unless the session confirmed its
// password in the last 5 minutes. Handlers that store secrets call it first.
func (a *API) requireReauth(w http.ResponseWriter, r *http.Request) bool {
	act, ok := actorFrom(r.Context())
	if ok && act.sess.ReauthAt != 0 && a.o.Now().Sub(time.Unix(act.sess.ReauthAt, 0)) <= reauthWindow {
		return true
	}
	apijson.WriteErrorFor(w, r, http.StatusForbidden, "reauth_required", i18n.T("Confirm your password to continue.", "Confirme sua senha para continuar."), nil)
	return false
}

// requireAdmin: valid sealed session (else 401, cookies cleared) -> is_admin re-read from the
// database on every request (else 403) -> for writes, Origin and X-CSRF-Token (else 403 csrf)
// -> slide the idle deadline -> actor in the context.
func (a *API) requireAdmin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		sess, ok := a.readSession(r)
		if !ok {
			a.clearCookies(w)
			apijson.WriteErrorFor(w, r, http.StatusUnauthorized, "unauthorized", i18n.T("Session expired. Sign in again.", "Sessão expirada. Entre novamente."), nil)
			return
		}
		isAdmin, email, err := a.lookupUser(r.Context(), sess.UID)
		if err != nil {
			apijson.FromError(w, r, err)
			return
		}
		if !isAdmin {
			apijson.WriteErrorFor(w, r, http.StatusForbidden, "forbidden", i18n.T("Administrator access only.", "Acesso restrito a administradores."), nil)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.validCSRF(r, sess) {
			writeCSRFError(w, r)
			return
		}
		sess = a.slide(w, sess)
		act := actor{uid: sess.UID, email: email, ip: ClientIP(r), sess: sess}
		next(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, act)))
	})
}

func writeCSRFError(w http.ResponseWriter, r *http.Request) {
	apijson.WriteErrorFor(w, r, http.StatusForbidden, "csrf", i18n.T("Request refused. Reload the page.", "Requisição recusada. Recarregue a página."), nil)
}

// validCSRF: Origin must be the gateway origin and X-CSRF-Token the token sealed in the session.
func (a *API) validCSRF(r *http.Request, sess session) bool {
	token := r.Header.Get("X-CSRF-Token")
	return r.Header.Get("Origin") == a.origin && token != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRF)) == 1
}

// lookupUser reads is_admin and email; a missing user is simply not an admin.
func (a *API) lookupUser(ctx context.Context, uid string) (isAdmin bool, email string, err error) {
	err = a.o.Pool.QueryRow(ctx, `SELECT is_admin, email FROM users WHERE id = $1::uuid`, uid).Scan(&isAdmin, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	return isAdmin, email, err
}

// slide re-seals the session when LastSeen is older than a minute; the absolute cap never moves.
func (a *API) slide(w http.ResponseWriter, sess session) session {
	now := a.o.Now()
	if now.Sub(time.Unix(sess.LastSeen, 0)) < touchGranularity {
		return sess
	}
	sess.LastSeen = now.Unix()
	if err := a.writeSession(w, sess); err != nil {
		a.o.Logger.Error("adminapi: slide session", "err", err)
	}
	return sess
}

func (a *API) readSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(a.cookieName(sessionCookie))
	if err != nil {
		return session{}, false
	}
	var sess session
	if a.open(c.Value, &sess) != nil || sess.UID == "" || sess.CSRF == "" {
		return session{}, false
	}
	now := a.o.Now()
	if now.Sub(time.Unix(sess.LastSeen, 0)) >= idleTimeout || now.Sub(time.Unix(sess.IssuedAt, 0)) >= absoluteTimeout {
		return session{}, false
	}
	return sess, true
}

// writeSession is the single path that sets both cookies (login, slide, reauth).
func (a *API) writeSession(w http.ResponseWriter, sess session) error {
	value, err := a.seal(sess)
	if err != nil {
		return err
	}
	http.SetCookie(w, a.cookie(sessionCookie, value, true))
	http.SetCookie(w, a.cookie(csrfCookie, sess.CSRF, false))
	return nil
}

func (a *API) clearCookies(w http.ResponseWriter) {
	for _, c := range []*http.Cookie{a.cookie(sessionCookie, "", true), a.cookie(csrfCookie, "", false)} {
		c.MaxAge = -1
		http.SetCookie(w, c)
	}
}

// cookie builds a browser-session cookie (no MaxAge); expiry is enforced from the sealed payload.
func (a *API) cookie(base, value string, httpOnly bool) *http.Cookie {
	return &http.Cookie{
		Name: a.cookieName(base), Value: value, Path: "/",
		HttpOnly: httpOnly, Secure: a.secure, SameSite: http.SameSiteStrictMode,
	}
}

// cookieName uses the __Host- prefix on https, as the oauth cookies do.
func (a *API) cookieName(base string) string {
	if a.secure {
		return "__Host-" + base
	}
	return base
}

func (a *API) seal(sess session) (string, error) {
	plain, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	sealed, err := a.o.Sealer.Seal(plain, []byte(aadSession))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (a *API) open(raw string, sess *session) error {
	sealed, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return err
	}
	plain, err := a.o.Sealer.Open(sealed, []byte(aadSession))
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, sess)
}
