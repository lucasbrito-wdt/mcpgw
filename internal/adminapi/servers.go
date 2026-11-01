package adminapi

import (
	"context"
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"strings"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
)

const syncTimeout = 2 * time.Minute

func init() {
	resourceRoutes = append(resourceRoutes, func(a *API, mux *http.ServeMux) {
		a.handle(mux, "GET /admin/api/servers", a.listServers)
		a.handle(mux, "POST /admin/api/servers", a.addServer)
		a.handle(mux, "GET /admin/api/servers/{slug}", a.getServer)
		a.handle(mux, "PATCH /admin/api/servers/{slug}", a.patchServer)
		a.handle(mux, "PUT /admin/api/servers/{slug}/header", a.putSharedHeader)
		a.handle(mux, "DELETE /admin/api/servers/{slug}/shared", a.removeShared)
		a.handle(mux, "PUT /admin/api/servers/{slug}/oauth-client", a.putOAuthClient)
		a.handle(mux, "POST /admin/api/servers/{slug}/sync", a.syncServer)
	})
}

// writeErr is apijson.FromError plus the one service error it does not know (plan gap G23).
func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, admin.ErrPrefixInValue) {
		msg := i18n.T("The shared value already starts with this prefix.", "O valor compartilhado já começa com este prefixo.")
		apijson.WriteErrorFor(w, r, http.StatusUnprocessableEntity, "prefix_already_in_value", msg, map[string]i18n.Text{"header_prefix": msg})
		return
	}
	apijson.FromError(w, r, err)
}

// Response structs carry no secret field: header values and client secrets are write-only.

type headerJSON struct {
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	FormatHint string `json:"format_hint"`
	HelpURL    string `json:"help_url"`
}

type serverJSON struct {
	Slug                string     `json:"slug"`
	URL                 string     `json:"url"`
	Transport           string     `json:"transport"`
	AuthKind            string     `json:"auth_kind"`
	Category            string     `json:"category"`
	Status              string     `json:"status"`
	ActiveTools         int        `json:"active_tools"`
	PendingTools        int        `json:"pending_tools"`
	HasSharedConnection bool       `json:"has_shared_connection"`
	HasOAuthClient      bool       `json:"has_oauth_client"`
	CatalogSyncedAt     *time.Time `json:"catalog_synced_at"`
	TrustAnnotations    bool       `json:"trust_annotations"`
	AutoApproveNewTools bool       `json:"auto_approve_new_tools"`
	// CredentialEnv (stdio only): the variable each user's token is passed in; null otherwise.
	CredentialEnv *string `json:"credential_env"`
}

type serverDetailJSON struct {
	serverJSON
	OAuthClientID string   `json:"oauth_client_id"`
	StdioLauncher string   `json:"stdio_launcher"`
	DockerImage   string   `json:"docker_image"`
	StdioNetwork  bool     `json:"stdio_network"`
	StdioEnvNames []string `json:"stdio_env_names"`
	// ConnectionEnv are the variables each connection sets for itself; never null.
	ConnectionEnv     []string    `json:"connection_env"`
	DetectedTransport string      `json:"detected_transport"`
	DetectedProtocol  string      `json:"detected_protocol_version"`
	Header            *headerJSON `json:"header"`
}

func toServerJSON(s admin.ServerSummary) serverJSON {
	return serverJSON{Slug: s.Slug, URL: s.URL, Transport: s.Transport, AuthKind: s.AuthKind, Category: s.Category, Status: s.Status,
		ActiveTools: s.ActiveTools, PendingTools: s.PendingTools, HasSharedConnection: s.HasSharedConnection,
		HasOAuthClient: s.HasOAuthClient, CatalogSyncedAt: s.CatalogSyncedAt, TrustAnnotations: s.TrustAnnotations,
		AutoApproveNewTools: s.AutoApproveNewTools, CredentialEnv: nonEmpty(s.CredentialEnv)}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toServerDetailJSON(d admin.ServerDetail) serverDetailJSON {
	out := serverDetailJSON{serverJSON: toServerJSON(d.ServerSummary), OAuthClientID: d.OAuthClientID, StdioLauncher: d.StdioLauncher,
		DockerImage: d.DockerImage, StdioNetwork: d.StdioNetwork, StdioEnvNames: d.StdioEnvNames,
		ConnectionEnv: nonNil(d.ConnectionEnv), DetectedTransport: d.DetectedTransport, DetectedProtocol: d.DetectedProtocol}
	if h := d.Header; h != nil {
		out.Header = &headerJSON{Name: h.Name, Prefix: h.Prefix, FormatHint: h.FormatHint, HelpURL: h.HelpURL}
	}
	return out
}

// headerIn is a header config where every field is optional (PATCH merges it over the stored one).
type headerIn struct {
	Name       *string `json:"name"`
	Prefix     *string `json:"prefix"`
	FormatHint *string `json:"format_hint"`
	HelpURL    *string `json:"help_url"`
}

func (h *headerIn) overlay(base admin.HeaderConfig) *admin.HeaderConfig {
	for dst, src := range map[*string]*string{&base.Name: h.Name, &base.Prefix: h.Prefix, &base.FormatHint: h.FormatHint, &base.HelpURL: h.HelpURL} {
		if src != nil {
			*dst = *src
		}
	}
	return &base
}

func (a *API) listServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.svc.ListServers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writePagedSlice(w, r, servers, func(s admin.ServerSummary) string { return s.Slug }, toServerJSON)
}

func (a *API) getServer(w http.ResponseWriter, r *http.Request) {
	d, err := a.svc.GetServer(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, toServerDetailJSON(d))
}

type addServerBody struct {
	Slug          string    `json:"slug"`
	URL           string    `json:"url"`
	AuthKind      string    `json:"auth_kind"`
	Transport     string    `json:"transport"`
	Category      string    `json:"category"`
	AutoApprove   bool      `json:"auto_approve_new_tools"`
	Header        *headerIn `json:"header"`
	StdioLauncher string    `json:"stdio_launcher"`
	StdioImage    string    `json:"stdio_image"`
	StdioCommand  string    `json:"stdio_command"`
	StdioArgs     []string  `json:"stdio_args"`
	StdioNetwork  bool      `json:"stdio_network"`
	// StdioEnv is the fixed environment, sealed on write and never echoed back (GET lists names).
	StdioEnv      []envPair `json:"stdio_env"`
	CredentialEnv string    `json:"credential_env"`
	ConnectionEnv []string  `json:"connection_env"`
}

type envPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (b addServerBody) input() (admin.AddServerInput, error) {
	in := admin.AddServerInput{Slug: b.Slug, URL: b.URL, AuthKind: b.AuthKind, Transport: b.Transport, Category: b.Category,
		AutoApproveNewTools: b.AutoApprove}
	if b.Header != nil {
		in.Header = b.Header.overlay(admin.HeaderConfig{})
	}
	if b.StdioLauncher == "" && b.StdioImage == "" && b.StdioCommand == "" && len(b.StdioArgs) == 0 &&
		len(b.StdioEnv) == 0 && b.CredentialEnv == "" && len(b.ConnectionEnv) == 0 {
		return in, nil
	}
	in.Stdio = &admin.StdioInput{Launcher: b.StdioLauncher, Image: b.StdioImage, Command: b.StdioCommand, Args: b.StdioArgs,
		Network: b.StdioNetwork, CredentialEnv: b.CredentialEnv, ConnectionEnv: b.ConnectionEnv}
	if len(b.StdioEnv) > 0 {
		in.Stdio.Env = make(map[string]string, len(b.StdioEnv))
	}
	for _, p := range b.StdioEnv {
		if _, dup := in.Stdio.Env[p.Name]; dup {
			return in, admin.NewInvalid("stdio_env", i18n.T("duplicate variable: %s", "variável repetida: %s", p.Name))
		}
		in.Stdio.Env[p.Name] = p.Value
	}
	return in, nil
}

func (a *API) addServer(w http.ResponseWriter, r *http.Request) {
	var in addServerBody
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	input, err := in.input()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if (input.Transport == "stdio" || input.Stdio != nil) && !a.requireReauth(w, r) {
		return
	}
	s, err := a.svc.AddServer(r.Context(), input)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusCreated, toServerJSON(s))
}

type patchServerBody struct {
	Status              *string   `json:"status"`
	TrustAnnotations    *bool     `json:"trust_annotations"`
	AutoApproveNewTools *bool     `json:"auto_approve_new_tools"`
	Category            *string   `json:"category"`
	Header              *headerIn `json:"header"`
	// ConnectionEnv replaces the per-connection variable names ([] clears); needs reauth.
	ConnectionEnv *[]string `json:"connection_env"`
	// StdioEnv merges into the sealed fixed environment (write-only values); needs reauth.
	StdioEnv *stdioEnvPatch `json:"stdio_env"`
}

type stdioEnvPatch struct {
	Set   map[string]string `json:"set"`
	Unset []string          `json:"unset"`
}

// patchServer applies each present group (header/category, connection_env, stdio_env, trust, status) in turn; every group is
// its own audited transaction, so a later failure leaves the earlier groups applied.
func (a *API) patchServer(w http.ResponseWriter, r *http.Request) {
	var in patchServerBody
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	slug := r.PathValue("slug")
	hasTrust := in.TrustAnnotations != nil || in.AutoApproveNewTools != nil
	switch {
	case in.Status == nil && !hasTrust && in.Category == nil && in.Header == nil && in.ConnectionEnv == nil && in.StdioEnv == nil:
		writeErr(w, r, admin.NewInvalid("body", i18n.T("no changes provided", "nenhuma alteração informada")))
		return
	case in.Status != nil && *in.Status != "active" && *in.Status != "disabled":
		writeErr(w, r, admin.NewInvalid("status", i18n.T("status must be active or disabled", "o status deve ser active ou disabled")))
		return
	}
	if (in.ConnectionEnv != nil || in.StdioEnv != nil) && !a.requireReauth(w, r) {
		return
	}
	ctx := r.Context()
	reclassified := []string{}
	err := a.patchUpdate(ctx, slug, in)
	if err == nil && in.ConnectionEnv != nil {
		err = a.svc.SetConnectionEnv(ctx, slug, *in.ConnectionEnv)
	}
	if err == nil && in.StdioEnv != nil {
		err = a.svc.SetStdioEnv(ctx, slug, in.StdioEnv.Set, in.StdioEnv.Unset)
	}
	if err == nil && hasTrust {
		var names []string
		if names, err = a.svc.SetTrust(ctx, slug, admin.TrustInput{Annotations: in.TrustAnnotations, AutoApproveNewTools: in.AutoApproveNewTools}); err == nil {
			reclassified = append(reclassified, names...)
		}
	}
	if err == nil && in.Status != nil {
		err = a.svc.SetStatus(ctx, slug, *in.Status)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	d, err := a.svc.GetServer(ctx, slug)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	apijson.WriteJSON(w, http.StatusOK, struct {
		serverJSON
		Reclassified []string `json:"reclassified"`
	}{toServerJSON(d.ServerSummary), reclassified})
}

func (a *API) patchUpdate(ctx context.Context, slug string, in patchServerBody) error {
	if in.Category == nil && in.Header == nil {
		return nil
	}
	up := admin.ServerUpdate{Category: in.Category}
	if in.Header != nil {
		h, err := a.mergedHeader(ctx, slug, in.Header)
		if err != nil {
			return err
		}
		up.Header = h
	}
	return a.svc.UpdateServer(ctx, slug, up)
}

// mergedHeader overlays the given fields on the server's stored header config.
func (a *API) mergedHeader(ctx context.Context, slug string, in *headerIn) (*admin.HeaderConfig, error) {
	d, err := a.svc.GetServer(ctx, slug)
	if err != nil {
		return nil, err
	}
	var base admin.HeaderConfig
	if d.Header != nil {
		base = *d.Header
	}
	return in.overlay(base), nil
}

// putSharedHeader stores the shared header value (write-only); an optional name renames the header first.
func (a *API) putSharedHeader(w http.ResponseWriter, r *http.Request) {
	if !a.requireReauth(w, r) {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	ctx, slug := r.Context(), r.PathValue("slug")
	if name := strings.TrimSpace(in.Name); name != "" {
		d, err := a.svc.GetServer(ctx, slug)
		if err == nil && (d.Header == nil || d.Header.Name != name) {
			var h *admin.HeaderConfig
			if h, err = a.mergedHeader(ctx, slug, &headerIn{Name: &name}); err == nil {
				err = a.svc.UpdateServer(ctx, slug, admin.ServerUpdate{Header: h})
			}
		}
		if err != nil {
			writeErr(w, r, err)
			return
		}
	}
	act, _ := actorFrom(ctx)
	if _, err := a.svc.SetSharedHeaderToken(ctx, slug, act.uid, in.Value); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) removeShared(w http.ResponseWriter, r *http.Request) {
	if !a.requireReauth(w, r) {
		return
	}
	if err := a.svc.RemoveSharedConnection(r.Context(), r.PathValue("slug")); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) putOAuthClient(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	// Whether a secret is present is only known after decoding; reauth still runs before it is used.
	if err := apijson.Decode(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	if in.ClientSecret != "" && !a.requireReauth(w, r) {
		return
	}
	if err := a.svc.SetOAuthClient(r.Context(), r.PathValue("slug"), in.ClientID, in.ClientSecret); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) syncServer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), syncTimeout)
	defer cancel()
	slug, asUser := r.PathValue("slug"), ""
	// A stdio server never starts without a token: sync it with the acting admin's connection
	// (personal, else a named profile, else the shared one).
	if d, err := a.svc.GetServer(ctx, slug); err == nil && d.CredentialEnv != "" {
		act, _ := actorFrom(ctx)
		asUser = act.uid
	}
	rep, err := a.svc.Sync(ctx, slug, asUser)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	changed := make([]string, 0, len(rep.Changed))
	for _, c := range rep.Changed {
		changed = append(changed, c.Name)
	}
	apijson.WriteJSON(w, http.StatusOK, map[string]any{
		"first_sync": rep.FirstSync, "added": nonNil(rep.Added), "changed": changed, "pending": nonNil(rep.Pending),
		"reactivated": nonNil(rep.Reactivated), "reclassified": nonNil(rep.Reclassified),
		"quarantined": nonNil(rep.Quarantined), "rejected": nonNil(rep.Rejected),
	})
}

func nonNil(s []string) []string { return append([]string{}, s...) }
