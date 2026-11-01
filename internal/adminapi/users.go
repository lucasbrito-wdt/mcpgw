package adminapi

import (
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

type userJSON struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserJSON(u admin.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, IsAdmin: u.IsAdmin, CreatedAt: u.CreatedAt}
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	act, _ := actorFrom(r.Context())
	apijson.WriteJSON(w, http.StatusOK, map[string]string{"email": act.email})
}

// listUsers pages admin.ListUsers (ordered by email) with an (email, id) cursor.
// shortcut: pages in memory over the full user list, fine for tens of admins/users; move the
// keyset into SQL if users grow to thousands.
func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	limit, err := apijson.Limit(r)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	after := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if after, _, err = apijson.DecodeCursor(raw); err != nil {
			apijson.FromError(w, r, err)
			return
		}
	}
	users, err := a.svc.ListUsers(r.Context())
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	// Byte order, not the DB collation, so the cursor comparison below matches the ordering.
	slices.SortFunc(users, func(x, y admin.User) int { return strings.Compare(x.Email, y.Email) })
	items := []userJSON{}
	next := ""
	for _, u := range users {
		if after != "" && u.Email <= after {
			continue
		}
		if len(items) == limit {
			last := items[len(items)-1]
			next = apijson.EncodeCursor(last.Email, last.ID)
			break
		}
		items = append(items, toUserJSON(u))
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	if in.IsAdmin && !a.requireReauth(w, r) {
		return
	}
	u, err := a.svc.CreateUser(r.Context(), admin.CreateUserInput{Email: in.Email, Password: in.Password, IsAdmin: in.IsAdmin})
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusCreated, toUserJSON(u))
}

func (a *API) setAdmin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IsAdmin *bool `json:"is_admin"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	if in.IsAdmin == nil {
		apijson.FromError(w, r, admin.NewInvalid("is_admin", i18n.T("provide is_admin", "informe is_admin")))
		return
	}
	if *in.IsAdmin && !a.requireReauth(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := a.svc.SetAdmin(r.Context(), id, *in.IsAdmin); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	var u userJSON
	err := a.o.Pool.QueryRow(r.Context(), `SELECT id::text, email, is_admin, created_at FROM users WHERE id = $1::uuid`, id).
		Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt)
	if err != nil {
		apijson.FromError(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, u)
}

func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !a.requireReauth(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	if err := a.svc.ResetPassword(r.Context(), r.PathValue("id"), in.Password); err != nil {
		apijson.FromError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
