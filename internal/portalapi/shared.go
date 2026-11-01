package portalapi

import (
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"

	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
)

// requireAdminPassword gates every shared (for everybody) write: the session user must be an
// admin right now (else 403 forbidden) and must re-confirm the password (else 403
// reauth_required; 429 when the login rate limit trips). On false the response was written.
func (a *API) requireAdminPassword(w http.ResponseWriter, r *http.Request, uid, password string) bool {
	u, found, err := a.lookupUser(r.Context(), uid)
	if err != nil {
		apijson.FromError(w, r, err)
		return false
	}
	if !found || !u.IsAdmin {
		apijson.WriteErrorFor(w, r, http.StatusForbidden, "forbidden", i18n.T("Only administrators can create the connection for everyone.", "Só administradores podem criar a conexão para todos."), nil)
		return false
	}
	if _, err := a.o.OAuth.Login(r.Context(), r, u.Email, password); err != nil {
		switch {
		case errors.Is(err, oauth.ErrBadCredentials):
			apijson.WriteErrorFor(w, r, http.StatusForbidden, "reauth_required", i18n.T("Confirm your password to continue.", "Confirme sua senha para continuar."), nil)
		case errors.Is(err, oauth.ErrLoginRateLimited):
			writeRateLimited(w, r)
		default:
			apijson.FromError(w, r, err)
		}
		return false
	}
	return true
}

// putSharedToken stores the server's shared header token through admin.Service, which audits it
// (action connection.set_shared, last4 only) in the same transaction.
func (a *API) putSharedToken(w http.ResponseWriter, r *http.Request, u user, ref serverRef, in connectBody) {
	if !a.requireAdminPassword(w, r, u.ID, in.Password) {
		return
	}
	last4, err := a.svc.SetSharedConnection(a.withActor(r, u), ref.Slug, u.ID, in.Token, in.Env, in.URL)
	if err != nil {
		if !writeConnectionEnvError(w, r, err) {
			apijson.FromError(w, r, err)
		}
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]string{"last4": last4})
}

func (a *API) deleteShared(w http.ResponseWriter, r *http.Request, u user, ref serverRef) {
	var in struct {
		Password string `json:"password"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	if !a.requireAdminPassword(w, r, u.ID, in.Password) {
		return
	}
	if err := a.svc.RemoveSharedConnection(a.withActor(r, u), ref.Slug); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
