package adminapi

import (
	"net/http"

	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "POST /admin/api/servers/{slug}/test", a.probeServer)
		a.handle(mux, "POST /admin/api/servers/test", a.probeURL)
	})
}

func (a *API) probeServer(w http.ResponseWriter, r *http.Request) {
	res, err := a.svc.ProbeServer(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, res)
}

func (a *API) probeURL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL       string `json:"url"`
		Transport string `json:"transport"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	res, err := a.svc.ProbeURL(r.Context(), in.URL, in.Transport)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, res)
}
