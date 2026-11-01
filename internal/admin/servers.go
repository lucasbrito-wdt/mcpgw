package admin

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

type ServerSummary struct {
	ID, Slug, URL, Transport, AuthKind, Status, Category string
	ActiveTools, PendingTools                            int
	// HasSharedConnection: an active shared row exists, or (expand phase) legacy
	// static_header_ciphertext is set.
	HasSharedConnection, HasOAuthClient   bool
	CatalogSyncedAt                       *time.Time
	TrustAnnotations, AutoApproveNewTools bool
	// CredentialEnv is the environment variable a stdio server receives each user's token in.
	CredentialEnv string
}

type ServerDetail struct {
	ServerSummary
	OAuthClientID              string
	StdioLauncher, DockerImage string
	StdioNetwork               bool
	// StdioEnvNames are the names of the sealed fixed environment; values are never returned.
	StdioEnvNames []string
	// ConnectionEnv are the variables each connection sets for itself (fixed ones are defaults).
	ConnectionEnv                       []string
	Header                              *HeaderConfig
	DetectedTransport, DetectedProtocol string
}

type HeaderConfig struct{ Name, Prefix, FormatHint, HelpURL string }

type StdioInput struct {
	Launcher, Image, Command string
	Args                     []string
	Network                  bool
	Env                      map[string]string
	// CredentialEnv makes the server auth_kind header: each user's token is passed to the process
	// in this environment variable.
	CredentialEnv string
	// ConnectionEnv are variables each connection sets for itself (requires CredentialEnv).
	ConnectionEnv []string
}

type AddServerInput struct {
	Slug, URL, AuthKind, Transport, Category string
	AutoApproveNewTools                      bool
	Stdio                                    *StdioInput
	Header                                   *HeaderConfig
}

type TrustInput struct{ Annotations, AutoApproveNewTools *bool }

// ServerUpdate: nil fields are untouched. Header replaces the whole header config; Category ""
// clears the category.
type ServerUpdate struct {
	Category *string
	Header   *HeaderConfig
}

// serverRow is the part of upstream_servers every write needs.
type serverRow struct{ ID, Slug, URL, AuthKind, Status string }

// serverBySlug reports the legacy static_header kind as header, like every read.
func serverBySlug(ctx context.Context, db store.DB, slug string) (serverRow, error) {
	var r serverRow
	err := db.QueryRow(ctx, `SELECT s.id::text, s.slug, s.url, `+authKindSQL+`, s.status FROM upstream_servers s WHERE s.slug = $1`, slug).
		Scan(&r.ID, &r.Slug, &r.URL, &r.AuthKind, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, notFound("server not found: %s", "servidor não encontrado: %s", slug)
	}
	return r, err
}

const authKindSQL = `CASE WHEN s.auth_kind = 'static_header' THEN 'header' ELSE s.auth_kind END`

// summarySQL selects the ServerSummary columns of upstream_servers s; never ciphertext, only IS NOT NULL.
const summarySQL = `SELECT s.id::text, s.slug, s.url, s.transport, ` + authKindSQL + `, s.status, COALESCE(s.category, ''),
	(SELECT count(*) FROM tools t WHERE t.server_id = s.id AND t.status = 'active'),
	(SELECT count(*) FROM tools t WHERE t.server_id = s.id AND t.status = 'pending_approval'),
	s.static_header_ciphertext IS NOT NULL
		OR EXISTS (SELECT 1 FROM upstream_credentials c WHERE c.server_id = s.id AND c.shared AND c.status = 'active'),
	s.oauth_client_id IS NOT NULL, s.catalog_synced_at, s.trust_annotations, s.auto_approve_new_tools,
	COALESCE(s.credential_env, '')`

func summaryDest(v *ServerSummary) []any {
	return []any{&v.ID, &v.Slug, &v.URL, &v.Transport, &v.AuthKind, &v.Status, &v.Category, &v.ActiveTools, &v.PendingTools,
		&v.HasSharedConnection, &v.HasOAuthClient, &v.CatalogSyncedAt, &v.TrustAnnotations, &v.AutoApproveNewTools, &v.CredentialEnv}
}

func (s *Service) ListServers(ctx context.Context) ([]ServerSummary, error) {
	rows, err := s.o.Pool.Query(ctx, summarySQL+` FROM upstream_servers s ORDER BY s.slug`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (v ServerSummary, err error) {
		err = r.Scan(summaryDest(&v)...)
		return
	})
}

func (s *Service) GetServer(ctx context.Context, slug string) (ServerDetail, error) {
	var d ServerDetail
	var header HeaderConfig
	var clientID, launcher, image, detTransport, detProto, hName, hHint, hURL *string
	err := s.o.Pool.QueryRow(ctx, summarySQL+`, s.oauth_client_id, s.stdio_launcher, s.docker_image, s.stdio_network,
		s.detected_transport, s.detected_protocol_version, s.header_name, s.header_prefix, s.header_format_hint, s.header_help_url,
		s.connection_env
		FROM upstream_servers s WHERE s.slug = $1`, slug).
		Scan(append(summaryDest(&d.ServerSummary), &clientID, &launcher, &image, &d.StdioNetwork,
			&detTransport, &detProto, &hName, &header.Prefix, &hHint, &hURL, &d.ConnectionEnv)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, notFound("server not found: %s", "servidor não encontrado: %s", slug)
	}
	if err != nil {
		return d, err
	}
	d.OAuthClientID, d.StdioLauncher, d.DockerImage = deref(clientID), deref(launcher), deref(image)
	d.DetectedTransport, d.DetectedProtocol = deref(detTransport), deref(detProto)
	if d.AuthKind == "header" || d.AuthKind == "oauth" && hName != nil {
		header.Name, header.FormatHint, header.HelpURL = deref(hName), deref(hHint), deref(hURL)
		d.Header = &header
	}
	if d.Transport == string(upstream.TransportStdio) {
		d.StdioEnvNames, err = s.stdioEnvNames(ctx, d.ID)
	}
	return d, err
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Service) AddServer(ctx context.Context, in AddServerInput) (ServerSummary, error) {
	authKind, transport, err := s.validateAdd(in)
	if err != nil {
		return ServerSummary{}, err
	}
	ev := Event{Action: "server.add", Target: in.Slug, Details: map[string]any{"transport": transport, "auth_kind": authKind}}
	if in.Stdio != nil && in.Stdio.CredentialEnv != "" {
		ev.Details["credential_env"] = in.Stdio.CredentialEnv
	}
	if in.Stdio != nil && len(in.Stdio.ConnectionEnv) > 0 {
		ev.Details["connection_env"] = in.Stdio.ConnectionEnv
	}
	var out ServerSummary
	err = s.write(ctx, ev, func(tx pgx.Tx) error {
		var err error
		if transport == string(upstream.TransportStdio) {
			err = s.insertStdio(ctx, tx, in, authKind)
		} else {
			err = insertHTTP(ctx, tx, in, authKind, transport)
		}
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, summarySQL+` FROM upstream_servers s WHERE s.slug = $1`, in.Slug).Scan(summaryDest(&out)...)
	})
	if isUniqueViolation(err) {
		return ServerSummary{}, conflict("a server with slug %s already exists", "já existe um servidor com o slug %s", in.Slug)
	}
	return out, err
}

// validateAdd checks everything that does not need the database and returns the normalized
// auth kind and transport.
func (s *Service) validateAdd(in AddServerInput) (authKind, transport string, err error) {
	if err = ValidateSlug(in.Slug); err != nil {
		return
	}
	authKind = in.AuthKind
	if authKind == "" && in.Stdio != nil && in.Stdio.CredentialEnv != "" {
		authKind = "header"
	}
	if authKind, err = ValidateAuthKind(authKind); err != nil {
		return
	}
	if transport = in.Transport; transport == "" {
		transport = string(upstream.TransportAuto)
	}
	switch upstream.TransportKind(transport) {
	case upstream.TransportStdio:
		err = s.validateStdio(in, authKind)
	case upstream.TransportAuto, upstream.TransportStreamable, upstream.TransportSSE:
		err = validateHTTP(in, authKind)
	default:
		err = invalid("transport", "transport must be auto, streamable_http, sse or stdio", "o transporte deve ser auto, streamable_http, sse ou stdio")
	}
	if err == nil && in.Category != "" {
		err = ValidateCategory(in.Category)
	}
	return
}

func validateHTTP(in AddServerInput, authKind string) error {
	if in.Stdio != nil {
		return invalid("stdio", "stdio configuration only applies to the stdio transport", "a configuração stdio só vale com o transporte stdio")
	}
	if err := ValidateServerURL(in.URL, authKind); err != nil {
		return err
	}
	return validateHeaderFor(authKind, in.Header)
}

// validateHeaderFor checks an optional header config against the server's auth kind.
func validateHeaderFor(authKind string, h *HeaderConfig) error {
	if h == nil {
		return nil
	}
	if authKind != "header" && authKind != "oauth" {
		return invalid("header", "header configuration only applies to servers with header or oauth auth", "a configuração de header só vale para servidores com auth header ou oauth")
	}
	return validateHeaderConfig(*h)
}

func insertHTTP(ctx context.Context, tx pgx.Tx, in AddServerInput, authKind, transport string) error {
	h := in.Header
	if h == nil {
		h = &HeaderConfig{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO upstream_servers (slug, url, auth_kind, transport, category, auto_approve_new_tools,
			header_name, header_prefix, header_format_hint, header_help_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		in.Slug, in.URL, authKind, transport, nullIfEmpty(in.Category), in.AutoApproveNewTools,
		nullIfEmpty(h.Name), h.Prefix, nullIfEmpty(h.FormatHint), nullIfEmpty(h.HelpURL))
	return err
}

func stdioSpec(in *StdioInput) upstream.StdioSpec {
	spec := upstream.StdioSpec{Launcher: upstream.Launcher(in.Launcher), Command: in.Command, Args: slices.Clone(in.Args),
		Image: in.Image, Network: in.Network, Env: in.Env}
	if spec.Launcher == "" {
		spec.Launcher = upstream.LauncherDocker
	}
	if spec.Args == nil {
		spec.Args = []string{}
	}
	return spec
}

func (s *Service) validateStdio(in AddServerInput, authKind string) error {
	switch {
	case in.Stdio == nil:
		return invalid("stdio", "stdio configuration is required with the stdio transport", "a configuração stdio é obrigatória com o transporte stdio")
	case in.Stdio.Launcher == string(upstream.LauncherHost) && !s.o.AllowHostLauncher:
		return invalid("stdio_launcher", "stdio servers with the host launcher can only be created through the CLI", "servidores stdio com launcher host só podem ser criados pela CLI")
	case in.URL != "":
		return invalid("url", "the URL does not apply to the stdio transport", "a URL não se aplica ao transporte stdio")
	}
	if err := validateStdioAuth(in, authKind); err != nil {
		return err
	}
	spec := stdioSpec(in.Stdio)
	if spec.Launcher == upstream.LauncherDocker && spec.Image == "" {
		return invalid("stdio", "the docker image is required for stdio (or use the host launcher)", "a imagem docker é obrigatória para stdio (ou use o launcher host)")
	}
	if spec.Launcher == upstream.LauncherHost && spec.Command == "" {
		return invalid("stdio", "the command is required with the host launcher", "o comando é obrigatório com o launcher host")
	}
	if err := spec.Validate(); err != nil {
		return invalid("stdio", "invalid stdio specification: %v", "especificação stdio inválida: %v", err)
	}
	if err := validateStdioEnvNames(spec, in.Stdio.CredentialEnv); err != nil {
		return err
	}
	return validateConnectionEnv(spec, in.Stdio.CredentialEnv, in.Stdio.ConnectionEnv)
}

// insertStdio stores a stdio server. Its url is the endpoint key stdio://<id>, and its
// environment is sealed with the row id as AAD, so the row is inserted first.
func (s *Service) insertStdio(ctx context.Context, tx pgx.Tx, in AddServerInput, authKind string) error {
	spec := stdioSpec(in.Stdio)
	h := in.Header
	if h == nil {
		h = &HeaderConfig{}
	}
	var id string
	err := tx.QueryRow(ctx, `WITH n AS (SELECT uuidv7() AS id)
		INSERT INTO upstream_servers (id, slug, url, auth_kind, transport, category, auto_approve_new_tools, stdio_launcher,
			stdio_command, stdio_args, docker_image, stdio_network, credential_env, header_format_hint, header_help_url, connection_env)
		SELECT id, $1, 'stdio://' || id, $9, 'stdio', $2, $3, $4, NULLIF($5, ''), $6, NULLIF($7, ''), $8, NULLIF($10, ''), $11, $12, $13
		FROM n RETURNING id::text`,
		in.Slug, nullIfEmpty(in.Category), in.AutoApproveNewTools, string(spec.Launcher), spec.Command, spec.Args, spec.Image, spec.Network,
		authKind, in.Stdio.CredentialEnv, nullIfEmpty(h.FormatHint), nullIfEmpty(h.HelpURL), append([]string{}, in.Stdio.ConnectionEnv...)).Scan(&id)
	if err != nil || len(spec.Env) == 0 {
		return err
	}
	v, err := s.vault()
	if err != nil {
		return err
	}
	sealed, err := upstream.SealStdioEnv(v, id, spec.Env)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE upstream_servers SET stdio_env_ciphertext = $2 WHERE id = $1`, id, sealed)
	return err
}

// SetStatus takes "active" or "disabled".
func (s *Service) SetStatus(ctx context.Context, slug, status string) error {
	if status != "active" && status != "disabled" {
		return invalid("status", "status must be active or disabled", "o status deve ser active ou disabled")
	}
	return s.write(ctx, Event{Action: "server.set_status", Target: slug, Details: map[string]any{"status": status}}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE upstream_servers SET status = $2 WHERE slug = $1`, slug, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("server not found: %s", "servidor não encontrado: %s", slug)
		}
		return nil
	})
}

// ServerDependents counts the rows that RemoveServer deletes along with the server.
type ServerDependents struct{ Tools, Credentials, Consents int }

func (s *Service) ServerDependents(ctx context.Context, slug string) (ServerDependents, error) {
	var d ServerDependents
	err := s.o.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM tools t WHERE t.server_id = s.id),
		(SELECT count(*) FROM upstream_credentials c WHERE c.server_id = s.id),
		(SELECT count(*) FROM consents c WHERE c.server_id = s.id)
		FROM upstream_servers s WHERE s.slug = $1`, slug).Scan(&d.Tools, &d.Credentials, &d.Consents)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, notFound("server not found: %s", "servidor não encontrado: %s", slug)
	}
	return d, err
}

// RemoveServer deletes the server; every table referencing upstream_servers cascades. audit_events
// keep the slug as text and are untouched.
func (s *Service) RemoveServer(ctx context.Context, slug string) error {
	return s.write(ctx, Event{Action: "server.remove", Target: slug}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM upstream_servers WHERE slug = $1`, slug)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("server not found: %s", "servidor não encontrado: %s", slug)
		}
		return nil
	})
}

// SetTrust changes the catalog trust settings; only the fields given change. Changing
// Annotations recomputes tool levels and returns the names whose level changed.
func (s *Service) SetTrust(ctx context.Context, slug string, in TrustInput) (reclassified []string, err error) {
	if in.Annotations == nil && in.AutoApproveNewTools == nil {
		return nil, invalid("trust", "provide annotations and/or auto_approve_new_tools", "informe annotations e/ou auto_approve_new_tools")
	}
	details := map[string]any{}
	if in.Annotations != nil {
		details["annotations"] = *in.Annotations
	}
	if in.AutoApproveNewTools != nil {
		details["auto_approve_new_tools"] = *in.AutoApproveNewTools
	}
	err = s.write(ctx, Event{Action: "server.set_trust", Target: slug, Details: details}, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE upstream_servers SET
			trust_annotations = COALESCE($2, trust_annotations),
			auto_approve_new_tools = COALESCE($3, auto_approve_new_tools)
			WHERE id = $1::uuid`, srv.ID, in.Annotations, in.AutoApproveNewTools)
		if err != nil || in.Annotations == nil {
			return err
		}
		reclassified, err = catalog.Reclassify(ctx, tx, srv.ID)
		return err
	})
	return reclassified, err
}

// UpdateServer changes the category and/or the header config (header servers only).
func (s *Service) UpdateServer(ctx context.Context, slug string, in ServerUpdate) error {
	if in.Category == nil && in.Header == nil {
		return invalid("update", "no changes provided", "nenhuma alteração informada")
	}
	if in.Category != nil && *in.Category != "" {
		if err := ValidateCategory(*in.Category); err != nil {
			return err
		}
	}
	var changed []string
	if in.Category != nil {
		changed = append(changed, "category")
	}
	if in.Header != nil {
		changed = append(changed, "header")
	}
	return s.write(ctx, Event{Action: "server.update", Target: slug, Details: map[string]any{"fields": changed}}, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		if in.Category != nil {
			if _, err := tx.Exec(ctx, `UPDATE upstream_servers SET category = $2 WHERE id = $1::uuid`, srv.ID, nullIfEmpty(*in.Category)); err != nil {
				return err
			}
		}
		if in.Header == nil {
			return nil
		}
		if err := validateHeaderFor(srv.AuthKind, in.Header); err != nil {
			return err
		}
		h := in.Header
		if srv.AuthKind == "oauth" {
			// the shared value of an oauth server is a bearer grant, not this header's token
			if err := checkNoTokenConnections(ctx, tx, srv, h.Name); err != nil {
				return err
			}
		} else if err := s.checkPrefixNotInSharedValue(ctx, tx, srv.ID, h.Prefix); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE upstream_servers SET header_name = $2, header_prefix = $3, header_format_hint = $4, header_help_url = $5
			WHERE id = $1::uuid`, srv.ID, nullIfEmpty(h.Name), h.Prefix, nullIfEmpty(h.FormatHint), nullIfEmpty(h.HelpURL))
		return err
	})
}

// SetOAuthClient stores the pre-registered OAuth client of an oauth server; an empty secret keeps the stored one
// (it cannot be cleared). The audit details only say whether a secret was set.
func (s *Service) SetOAuthClient(ctx context.Context, slug, clientID, secret string) error {
	if clientID == "" {
		return invalid("client_id", "client_id is required", "o client_id é obrigatório")
	}
	mgr, err := s.manager()
	if err != nil {
		return err
	}
	ev := Event{Action: "server.set_oauth_client", Target: slug, Details: map[string]any{"client_id": clientID, "secret_set": secret != ""}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		if srv.AuthKind != "oauth" {
			return invalid("auth_kind", "server %s uses auth %s, not oauth", "o servidor %s usa auth %s, não oauth", srv.Slug, srv.AuthKind)
		}
		return mgr.SetStaticOAuthClient(ctx, tx, srv.ID, clientID, secret)
	})
}
