package adminapi

import (
	"context"
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
)

// LoginFunc checks a password under the shared login rate limits (oauth.Server.Login).
type LoginFunc func(ctx context.Context, r *http.Request, email, password string) (string, error)

type Options struct {
	Pool *pgxpool.Pool
	// PublicURL is the gateway origin; writes must carry it as Origin, and https turns on
	// Secure + the __Host- cookie prefix.
	PublicURL string
	Sealer    oauth.Sealer // the vault
	Login     LoginFunc
	Admin     admin.Options // Audit is overwritten with the admin_events writer; Pool defaults to Pool
	Logger    *slog.Logger
	Now       func() time.Time // tests; default time.Now
}

type API struct {
	o       Options
	svc     *admin.Service
	secure  bool
	origin  string
	started time.Time
}

// resourceRoutes lets other files of this package (Task 4.1) register routes from init()
// without editing Routes.
var resourceRoutes []func(a *API, mux *http.ServeMux)

func New(o Options) (*API, error) {
	if o.Pool == nil || o.Sealer == nil || o.Login == nil {
		return nil, errors.New("adminapi: pool, sealer and login are required")
	}
	origin, err := parseOrigin(o.PublicURL)
	if err != nil {
		return nil, err
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Admin.Pool == nil {
		o.Admin.Pool = o.Pool
	}
	a := &API{o: o, secure: strings.HasPrefix(origin, "https://"), origin: origin, started: o.Now()}
	a.o.Admin.Audit = a.audit
	a.svc = admin.New(a.o.Admin)
	return a, nil
}

// Routes registers every /admin/api/ route on mux. All but POST /session require an admin
// session; unknown /admin/api/ paths answer a JSON 404 instead of reaching the static handler.
func (a *API) Routes(mux *http.ServeMux) {
	mux.Handle("POST /admin/api/session", i18n.Middleware(http.HandlerFunc(a.login)))
	a.handle(mux, "DELETE /admin/api/session", a.logout)
	a.handle(mux, "POST /admin/api/session/reauth", a.reauth)
	a.handle(mux, "GET /admin/api/me", a.me)
	a.handle(mux, "GET /admin/api/users", a.listUsers)
	a.handle(mux, "POST /admin/api/users", a.createUser)
	a.handle(mux, "PATCH /admin/api/users/{id}", a.setAdmin)
	a.handle(mux, "POST /admin/api/users/{id}/password", a.resetPassword)
	a.handle(mux, "/admin/api/", func(w http.ResponseWriter, r *http.Request) {
		apijson.WriteErrorFor(w, r, http.StatusNotFound, "not_found", i18n.T("Resource not found.", "Recurso não encontrado."), nil)
	})
	for _, register := range resourceRoutes {
		register(a, mux)
	}
}

// handle registers an admin-only route.
func (a *API) handle(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	mux.Handle(pattern, i18n.Middleware(a.requireAdmin(h)))
}

// parseOrigin mirrors the oauth issuer rule: an absolute http(s) origin, lower-cased.
func parseOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("adminapi: public URL must be an absolute http(s) origin")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}
