// Package portalapi serves /portal/api: the signed-in user's own connections to upstream servers.
// Every query is scoped to the session user; nothing reads a user id from the request.
package portalapi

import (
	"context"
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

// SessionService is the part of oauth.Server the portal needs; the portal shares its mcpgw_session cookie.
type SessionService interface {
	TouchSession(w http.ResponseWriter, r *http.Request) (userID string, ok bool)
	Login(ctx context.Context, r *http.Request, email, password string) (string, error)
	StartSession(w http.ResponseWriter, userID string) error
}

type Options struct {
	Pool *pgxpool.Pool
	// PublicURL is the gateway origin; writes must carry it as Origin, https turns on Secure + __Host-.
	PublicURL   string
	Sealer      oauth.Sealer // the vault; seals the CSRF cookie
	OAuth       SessionService
	Credentials *upstreamauth.Manager
	Lister      upstreamauth.ToolLister // token/test
	// Admin configures the admin.Service behind the shared writes. Audit is overwritten here;
	// Pool and Credentials default to the fields above.
	Admin  admin.Options
	Logger *slog.Logger
}

type API struct {
	o      Options
	svc    *admin.Service
	secure bool
	origin string
}

func New(o Options) (*API, error) {
	if o.Pool == nil || o.Sealer == nil || o.OAuth == nil || o.Credentials == nil || o.Lister == nil {
		return nil, errors.New("portalapi: pool, sealer, oauth, credentials and lister are required")
	}
	origin, err := parseOrigin(o.PublicURL)
	if err != nil {
		return nil, err
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Admin.Pool == nil {
		o.Admin.Pool = o.Pool
	}
	if o.Admin.Credentials == nil {
		o.Admin.Credentials = func() (*upstreamauth.Manager, error) { return o.Credentials, nil }
	}
	a := &API{o: o, secure: strings.HasPrefix(origin, "https://"), origin: origin}
	a.o.Admin.Audit = a.audit
	a.svc = admin.New(a.o.Admin)
	return a, nil
}

// Routes registers every /portal/api/ route on mux. Unknown /portal/api/ paths answer a JSON
// 401 (anonymous) or 404 instead of reaching the static handler.
func (a *API) Routes(mux *http.ServeMux) {
	mux.Handle("POST /portal/api/session", i18n.Middleware(http.HandlerFunc(a.login)))
	mux.Handle("DELETE /portal/api/session", i18n.Middleware(http.HandlerFunc(a.logout)))
	a.handle(mux, "GET /portal/api/me", a.me)
	a.handle(mux, "GET /portal/api/connections", a.listConnections)
	a.handle(mux, "POST /portal/api/connections/disconnect", a.bulkDisconnect)
	a.handle(mux, "GET /portal/api/connections/{slug}", a.getConnection)
	a.handle(mux, "DELETE /portal/api/connections/{slug}", a.deleteConnection)
	a.handle(mux, "POST /portal/api/connections/{slug}/oauth", a.startOAuth)
	a.handle(mux, "POST /portal/api/connections/{slug}/token/test", a.testToken)
	a.handle(mux, "PUT /portal/api/connections/{slug}/token", a.putToken)
	a.handle(mux, "/portal/api/", func(w http.ResponseWriter, r *http.Request, _ user) {
		apijson.WriteErrorFor(w, r, http.StatusNotFound, "not_found", i18n.T("Resource not found.", "Recurso não encontrado."), nil)
	})
}

// parseOrigin mirrors adminapi's rule: an absolute http(s) origin, lower-cased.
func parseOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("portalapi: public URL must be an absolute http(s) origin")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

// user is the session user, re-read from the database on every request.
type user struct {
	ID, Email string
	IsAdmin   bool
}

// lookupUser reports found=false for a removed user.
func (a *API) lookupUser(ctx context.Context, uid string) (u user, found bool, err error) {
	u.ID = uid
	err = a.o.Pool.QueryRow(ctx, `SELECT email, is_admin FROM users WHERE id = $1::uuid`, uid).Scan(&u.Email, &u.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return user{}, false, nil
	}
	return u, err == nil, err
}
