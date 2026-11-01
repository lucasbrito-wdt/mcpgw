package adminapi

import (
	"net/http"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "GET /admin/api/connections", a.listConnections)
		// The literal "shared" segment wins over {user_id} in the Go 1.22 mux.
		a.handle(mux, "DELETE /admin/api/connections/{slug}/shared", a.revokeSharedConnection)
		a.handle(mux, "DELETE /admin/api/connections/{slug}/{user_id}", a.revokeUserConnection)
	})
}

type sharedJSON struct {
	Status           string     `json:"status"`
	Last4            string     `json:"last4"`
	AccountLabel     *string    `json:"account_label"`
	ConnectedByEmail *string    `json:"connected_by_email"`
	LastUsedAt       *time.Time `json:"last_used_at"`
	ExpiresAt        *time.Time `json:"expires_at"`
	RevokedReason    *string    `json:"revoked_reason"`
}

type serverConnectionsJSON struct {
	Slug          string      `json:"slug"`
	AuthKind      string      `json:"auth_kind"`
	Category      string      `json:"category"`
	Connected     int         `json:"connected"`
	UsingShared   int         `json:"using_shared"`
	Expiring      int         `json:"expiring"`
	Expired       int         `json:"expired"`
	RefreshFailed int         `json:"refresh_failed"`
	Revoked       int         `json:"revoked"`
	NotConnected  int         `json:"not_connected"`
	Shared        *sharedJSON `json:"shared"`
}

type userConnectionJSON struct {
	Server       string     `json:"server"`
	UserID       string     `json:"user_id"`
	Email        string     `json:"email"`
	Profile      string     `json:"profile"`
	Status       string     `json:"status"`
	AccountLabel *string    `json:"account_label"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toServerConnectionsJSON(c admin.ServerConnections) serverConnectionsJSON {
	out := serverConnectionsJSON{Slug: c.Slug, AuthKind: c.AuthKind, Category: c.Category, Connected: c.Connected, UsingShared: c.UsingShared,
		Expiring: c.Expiring, Expired: c.Expired, RefreshFailed: c.RefreshFailed, Revoked: c.Revoked, NotConnected: c.NotConnected}
	if s := c.Shared; s != nil {
		out.Shared = &sharedJSON{Status: s.Status, Last4: s.Last4, AccountLabel: nilIfEmpty(s.AccountLabel), ConnectedByEmail: nilIfEmpty(s.ConnectedByEmail),
			LastUsedAt: s.LastUsedAt, ExpiresAt: s.ExpiresAt, RevokedReason: nilIfEmpty(s.RevokedReason)}
	}
	return out
}

func (a *API) listConnections(w http.ResponseWriter, r *http.Request) {
	q, err := pageQuery(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	v := r.URL.Query()
	servers, page, err := a.svc.ListConnections(r.Context(), admin.ConnectionFilter{Server: v.Get("server"), Status: v.Get("status")}, q)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]any{
		"servers": mapSlice(servers, toServerConnectionsJSON),
		"items": mapSlice(page.Items, func(u admin.UserConnection) userConnectionJSON {
			return userConnectionJSON{Server: u.ServerSlug, UserID: u.UserID, Email: u.Email, Profile: u.Profile, Status: u.Status,
				AccountLabel: nilIfEmpty(u.AccountLabel), LastUsedAt: u.LastUsedAt, ExpiresAt: u.ExpiresAt}
		}),
		"next_cursor": encodeCursor(page.NextCursor),
	})
}

func (a *API) revokeConnection(w http.ResponseWriter, r *http.Request, userID string) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	writeNoContent(w, r, a.svc.RevokeConnection(r.Context(), r.PathValue("slug"), userID, in.Reason))
}

func (a *API) revokeSharedConnection(w http.ResponseWriter, r *http.Request) {
	if !a.requireReauth(w, r) {
		return
	}
	a.revokeConnection(w, r, "")
}

func (a *API) revokeUserConnection(w http.ResponseWriter, r *http.Request) {
	a.revokeConnection(w, r, r.PathValue("user_id"))
}
