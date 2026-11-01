package adminapi

import (
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"strconv"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "GET /admin/api/stats", a.stats)
	})
}

func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := admin.StatsQuery{Window: v.Get("window"), Server: v.Get("server"), Now: a.o.Now()}
	if raw := v.Get("top"); raw != "" {
		top, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, r, admin.NewInvalid("top", i18n.T("invalid top: provide an integer", "top inválido: informe um número inteiro")))
			return
		}
		q.Top = top
	}
	st, err := a.svc.Stats(r.Context(), q)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, st)
}
