package adminapi

import (
	"encoding/json"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "GET /admin/api/tools", a.listTools)
		a.handle(mux, "GET /admin/api/tools/{slug}/{tool}", a.getTool)
		a.handle(mux, "PATCH /admin/api/tools/{slug}/{tool}", a.setToolLevel)
		a.handle(mux, "POST /admin/api/tools/{slug}/{tool}/approve", a.approveTool)
		a.handle(mux, "POST /admin/api/servers/{slug}/approve-all", a.approveAll)
		a.handle(mux, "GET /admin/api/ceilings", a.listCeilings)
		a.handle(mux, "PUT /admin/api/ceilings/{user_id}/{slug}", a.setCeiling)
	})
}

type toolJSON struct {
	Server         string   `json:"server"`
	Name           string   `json:"name"`
	Level          string   `json:"level"`
	Overridden     bool     `json:"overridden"`
	Status         string   `json:"status"`
	PendingChanges []string `json:"pending_changes"`
	Hash           string   `json:"hash"`
}

type toolDetailJSON struct {
	toolJSON
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Annotations  json.RawMessage `json:"annotations"`
	ApprovedHash string          `json:"approved_hash"`
}

func toToolJSON(t admin.Tool) toolJSON {
	return toolJSON{Server: t.ServerSlug, Name: t.Name, Level: t.Level, Overridden: t.Overridden, Status: t.Status,
		PendingChanges: nonNil(t.PendingChanges), Hash: t.Hash}
}

// rawJSON passes a stored jsonb document through; an empty one becomes null.
func rawJSON(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("null")
	}
	return json.RawMessage(s)
}

func (a *API) listTools(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tools, err := a.svc.ListTools(r.Context(), admin.ToolFilter{Server: q.Get("server"), Status: q.Get("status")})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writePagedSlice(w, r, tools, func(t admin.Tool) string { return t.ServerSlug + "/" + t.Name }, toToolJSON)
}

func (a *API) getTool(w http.ResponseWriter, r *http.Request) {
	d, err := a.svc.GetTool(r.Context(), r.PathValue("slug"), r.PathValue("tool"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, toolDetailJSON{toolJSON: toToolJSON(d.Tool), Description: d.Description,
		InputSchema: rawJSON(d.InputSchema), OutputSchema: rawJSON(d.OutputSchema), Annotations: rawJSON(d.Annotations), ApprovedHash: d.ApprovedHash})
}

func (a *API) setToolLevel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Level string `json:"level"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	level, err := admin.ParseLevel(in.Level, false)
	if err == nil {
		err = a.svc.SetToolLevel(r.Context(), r.PathValue("slug"), r.PathValue("tool"), level)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// approveTool requires the hash the admin reviewed (the service allows an empty one for the CLI).
func (a *API) approveTool(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hash string `json:"hash"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	hash, err := admin.NormalizeHashPrefix(in.Hash)
	if err == nil && hash == "" {
		err = admin.NewInvalid("hash", i18n.T("provide the hash of the reviewed definition", "informe o hash da definição revisada"))
	}
	var approved []string
	if err == nil {
		approved, err = a.svc.ApproveTool(r.Context(), r.PathValue("slug"), r.PathValue("tool"), hash)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string][]string{"approved": nonNil(approved)})
}

func (a *API) approveAll(w http.ResponseWriter, r *http.Request) {
	approved, err := a.svc.ApproveAll(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, map[string][]string{"approved": nonNil(approved)})
}

type ceilingJSON struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Server string `json:"server"`
	Level  string `json:"level"`
}

func (a *API) listCeilings(w http.ResponseWriter, r *http.Request) {
	cs, err := a.svc.ListCeilings(r.Context(), admin.CeilingFilter{UserID: r.URL.Query().Get("user")})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writePagedSlice(w, r, cs, func(c admin.Ceiling) string { return c.Email + "/" + c.ServerSlug },
		func(c admin.Ceiling) ceilingJSON {
			return ceilingJSON{UserID: c.UserID, Email: c.Email, Server: c.ServerSlug, Level: c.Level}
		})
}

func (a *API) setCeiling(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Level string `json:"level"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	level, err := admin.ParseLevel(in.Level, true)
	if err == nil {
		err = a.svc.SetCeiling(r.Context(), r.PathValue("user_id"), r.PathValue("slug"), level)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
