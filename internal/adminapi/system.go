package adminapi

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "GET /admin/api/system", a.system)
	})
}

type systemJSON struct {
	Version    string    `json:"version"`
	Commit     *string   `json:"commit"`
	CommitTime *string   `json:"commit_time"`
	Modified   bool      `json:"modified"`
	GoVersion  string    `json:"go_version"`
	PublicURL  string    `json:"public_url"`
	StartedAt  time.Time `json:"started_at"`
}

func (a *API) system(w http.ResponseWriter, _ *http.Request) {
	out := systemJSON{Version: "(devel)", PublicURL: a.o.PublicURL, StartedAt: a.started}
	if bi, ok := debug.ReadBuildInfo(); ok {
		out.GoVersion = bi.GoVersion
		if v := bi.Main.Version; v != "" {
			out.Version = v
		}
		for _, s := range bi.Settings {
			switch v := s.Value; s.Key {
			case "vcs.revision":
				out.Commit = &v
			case "vcs.time":
				out.CommitTime = &v
			case "vcs.modified":
				out.Modified = v == "true"
			}
		}
	}
	apijson.WriteJSON(w, http.StatusOK, out)
}
