package adminapi

import (
	"net/http"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "GET /admin/api/clients", a.listClients)
		a.handle(mux, "GET /admin/api/consents", a.listConsents)
		a.handle(mux, "DELETE /admin/api/consents/{id}", a.revokeConsent)
		a.handle(mux, "GET /admin/api/grants", a.listGrants)
		a.handle(mux, "DELETE /admin/api/grants/{family_id}", a.revokeGrant)
	})
}

type clientJSON struct {
	ClientID         string    `json:"client_id"`
	Name             string    `json:"name"` // client metadata: untrusted text
	RegistrationKind string    `json:"registration_kind"`
	RedirectURIs     []string  `json:"redirect_uris"`
	CreatedAt        time.Time `json:"created_at"`
	ActiveConsents   int       `json:"active_consents"`
}

type consentJSON struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Email      string     `json:"email"`
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	Server     string     `json:"server"`
	Level      string     `json:"level"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type grantJSON struct {
	FamilyID   string    `json:"family_id"`
	UserID     string    `json:"user_id"`
	Email      string    `json:"email"`
	ClientID   string    `json:"client_id"`
	Scope      string    `json:"scope"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (a *API) listClients(w http.ResponseWriter, r *http.Request) {
	listPage(w, r, func(q admin.PageQuery) (admin.Page[admin.Client], error) { return a.svc.ListClients(r.Context(), q) },
		func(c admin.Client) clientJSON {
			return clientJSON{ClientID: c.ClientID, Name: c.Name, RegistrationKind: c.RegistrationKind, RedirectURIs: nonNil(c.RedirectURIs),
				CreatedAt: c.CreatedAt, ActiveConsents: c.ActiveConsents}
		})
}

func (a *API) listConsents(w http.ResponseWriter, r *http.Request) {
	f := admin.ConsentFilter{UserID: r.URL.Query().Get("user"), ClientID: r.URL.Query().Get("client")}
	listPage(w, r, func(q admin.PageQuery) (admin.Page[admin.Consent], error) {
		return a.svc.ListConsents(r.Context(), f, q)
	},
		func(c admin.Consent) consentJSON {
			return consentJSON{ID: c.ID, UserID: c.UserID, Email: c.Email, ClientID: c.ClientID, ClientName: c.ClientName,
				Server: c.ServerSlug, Level: c.Level, CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt, RevokedAt: c.RevokedAt}
		})
}

func (a *API) listGrants(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	listPage(w, r, func(q admin.PageQuery) (admin.Page[admin.Grant], error) {
		return a.svc.ListGrants(r.Context(), user, q)
	},
		func(g admin.Grant) grantJSON {
			return grantJSON{FamilyID: g.FamilyID, UserID: g.UserID, Email: g.Email, ClientID: g.ClientID, Scope: g.Scope,
				CreatedAt: g.CreatedAt, LastUsedAt: g.LastUsedAt, ExpiresAt: g.ExpiresAt}
		})
}

func (a *API) revokeConsent(w http.ResponseWriter, r *http.Request) {
	writeNoContent(w, r, a.svc.RevokeConsent(r.Context(), r.PathValue("id")))
}

func (a *API) revokeGrant(w http.ResponseWriter, r *http.Request) {
	writeNoContent(w, r, a.svc.RevokeGrant(r.Context(), r.PathValue("family_id")))
}

// writeNoContent answers 204, or the mapped error.
func writeNoContent(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
