package portalapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/adminapi"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
)

const (
	oauthSessionCookie = "mcpgw_session" // owned by oauth.Server; the portal only clears it on logout
	csrfCookie         = "mcpgw_portal_csrf"
	aadCSRF            = "portal_csrf"
)

var (
	msgBadCredentials = i18n.T("Incorrect email or password.", "E-mail ou senha incorretos.")
	msgSessionExpired = i18n.T("Session expired. Sign in again.", "Sessão expirada. Entre novamente.")
)

type userHandler func(w http.ResponseWriter, r *http.Request, u user)

// handle registers a route for signed-in users: session (else 401) -> user still exists (else
// 401, cookie cleared) -> for writes, Origin and CSRF (else 403 csrf).
func (a *API) handle(mux *http.ServeMux, pattern string, h userHandler) {
	mux.Handle(pattern, i18n.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		uid, ok := a.o.OAuth.TouchSession(w, r)
		if !ok {
			apijson.WriteErrorFor(w, r, http.StatusUnauthorized, "unauthorized", msgSessionExpired, nil)
			return
		}
		u, found, err := a.lookupUser(r.Context(), uid)
		if err != nil {
			apijson.FromError(w, r, err)
			return
		}
		if !found {
			a.clearCookies(w)
			apijson.WriteErrorFor(w, r, http.StatusUnauthorized, "unauthorized", msgSessionExpired, nil)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.validCSRF(r, uid) {
			writeCSRFError(w, r)
			return
		}
		h(w, r, u)
	})))
}

func writeCSRFError(w http.ResponseWriter, r *http.Request) {
	apijson.WriteErrorFor(w, r, http.StatusForbidden, "csrf", i18n.T("Request refused. Reload the page.", "Requisição recusada. Recarregue a página."), nil)
}

// login answers the same 401 for an unknown email and a wrong password (oauth.Server.Login).
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Origin") != a.origin {
		writeCSRFError(w, r)
		return
	}
	var in struct{ Email, Password string }
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	uid, err := a.o.OAuth.Login(r.Context(), r, in.Email, in.Password)
	if err == nil {
		err = a.o.OAuth.StartSession(w, uid)
	}
	if err != nil {
		writeLoginError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeLoginError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, oauth.ErrBadCredentials):
		apijson.WriteErrorFor(w, r, http.StatusUnauthorized, "unauthorized", msgBadCredentials, nil)
	case errors.Is(err, oauth.ErrLoginRateLimited):
		writeRateLimited(w, r)
	default:
		apijson.FromError(w, r, err)
	}
}

func writeRateLimited(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "60")
	apijson.WriteErrorFor(w, r, http.StatusTooManyRequests, "rate_limited", i18n.T("Too many attempts. Try again in a minute.", "Muitas tentativas. Tente novamente em um minuto."), nil)
}

// logout needs no CSRF token: logging someone out is harmless, and an expired session must still
// be able to clear its cookies. Origin is still checked.
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Origin") != a.origin {
		writeCSRFError(w, r)
		return
	}
	a.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) clearCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(oauthSessionCookie), Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, a.csrfCookie("", -1))
}

func (a *API) me(w http.ResponseWriter, r *http.Request, u user) {
	value, err := a.sealCSRF(u.ID)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	http.SetCookie(w, a.csrfCookie(value, 0))
	apijson.WriteJSON(w, http.StatusOK, map[string]any{"email": u.Email, "is_admin": u.IsAdmin})
}

// csrfCookie is readable by the page script (double submit) and lives as long as the browser session.
func (a *API) csrfCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: a.cookieName(csrfCookie), Value: value, Path: "/", MaxAge: maxAge,
		Secure: a.secure, SameSite: http.SameSiteStrictMode}
}

func (a *API) cookieName(base string) string {
	if a.secure {
		return "__Host-" + base
	}
	return base
}

type csrfClaim struct {
	UID   string `json:"uid"`
	Nonce string `json:"n"`
}

func (a *API) sealCSRF(uid string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plain, err := json.Marshal(csrfClaim{UID: uid, Nonce: base64.RawURLEncoding.EncodeToString(nonce)})
	if err != nil {
		return "", err
	}
	sealed, err := a.o.Sealer.Seal(plain, []byte(aadCSRF))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// validCSRF: Origin is the gateway origin, X-CSRF-Token equals the cookie, and the cookie was
// sealed for this session's user.
func (a *API) validCSRF(r *http.Request, uid string) bool {
	token := r.Header.Get("X-CSRF-Token")
	c, err := r.Cookie(a.cookieName(csrfCookie))
	if r.Header.Get("Origin") != a.origin || token == "" || err != nil ||
		subtle.ConstantTimeCompare([]byte(token), []byte(c.Value)) != 1 {
		return false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return false
	}
	plain, err := a.o.Sealer.Open(sealed, []byte(aadCSRF))
	var claim csrfClaim
	return err == nil && json.Unmarshal(plain, &claim) == nil && strings.EqualFold(claim.UID, uid)
}

// actorKey carries the acting user to the admin.Service audit hook of the shared writes.
type actorKey struct{}

type actor struct{ uid, ip string }

func (a *API) withActor(r *http.Request, u user) context.Context {
	return context.WithValue(r.Context(), actorKey{}, actor{uid: u.ID, ip: adminapi.ClientIP(r)})
}

// audit is admin.Options.Audit: it never writes an anonymous row.
func (a *API) audit(ctx context.Context, tx pgx.Tx, ev admin.Event) error {
	act, ok := ctx.Value(actorKey{}).(actor)
	if !ok {
		return errors.New("portalapi: audit without an authenticated user")
	}
	return adminapi.WriteAdminEvent(ctx, tx, act.uid, act.ip, ev)
}
