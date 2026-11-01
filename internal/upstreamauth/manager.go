// Package upstreamauth makes the gateway an OAuth client of upstream MCP servers.
//
// Flow (MCP authorization spec): discover the protected resource (RFC 9728) and its
// authorization server (RFC 8414 / OIDC), register a client (admin-provided, CIMD or DCR),
// run authorization code + PKCE S256 with the RFC 8707 resource indicator per user, and keep the
// tokens sealed with the vault. CredentialFor hands upstream.Client a bearer credential and
// refreshes it (single-flight, rotation-safe) when it is about to expire.
//
// Every outbound request goes through one SSRF-guarded client (see ssrf.go). Tokens are never
// logged and transport/AS errors are reduced to categories before they leave this package.
package upstreamauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const (
	stateTTL        = 10 * time.Minute
	refreshSkew     = 60 * time.Second
	refreshTimeout  = 30 * time.Second
	clientMetaPath  = "/oauth/client-metadata.json"
	callbackPath    = "/connect/callback"
	successPath     = "/connect/success"
	connectPathBase = "/connect/"
	AuthKindNone    = "none"
	AuthKindOAuth   = "oauth"
	AuthKindHeader  = "header"
	// AuthKindStatic is the pre-0007 name of AuthKindHeader, still stored until the contract migration.
	AuthKindStatic    = "static_header"
	portalConnectPath = "/portal/connect/?slug="
	gatewayClientName = "MCP Gateway"
	// DefaultProfile is the connection used when a call names none; shared rows are always it.
	DefaultProfile = "default"
	// MethodOAuth and MethodToken are upstream_credentials.method. "" is legacy: the server's
	// auth_kind decides (oauth -> oauth, header -> token).
	MethodOAuth = "oauth"
	MethodToken = "token"
)

var (
	// ErrNotConnected means the user has no (usable) credential for the server; the caller
	// should ask the user to open the connect URL. The concrete error is *NotConnectedError.
	ErrNotConnected = errors.New("upstream account not connected")
	// ErrServerNotFound is returned for an unknown upstream server id.
	ErrServerNotFound = errors.New("upstream server not found")
	// ErrNoClientRegistration means no admin client, CIMD or DCR is available for the authorization server.
	ErrNoClientRegistration = errors.New("no way to register an OAuth client with the authorization server")
	// ErrRegistrationRejected: the authorization server refused Dynamic Client Registration (e.g. an allowlist of known clients).
	ErrRegistrationRejected = errors.New("client registration rejected")

	// ErrTokenFormat: the token does not start with the server's format hint.
	ErrTokenFormat = errors.New("token does not match the expected format")
	// ErrInvalidToken: empty, too long (> 4096 bytes) or has CR/LF/NUL.
	ErrInvalidToken = errors.New("invalid token")
	// ErrTokenNotSupported: an oauth server with no alternative header (header_name) takes no pasted token.
	ErrTokenNotSupported = errors.New("server does not accept a pasted token")
	// ErrTokenTestUnsupported: TestToken does not start a stdio server process for an unsaved token.
	ErrTokenTestUnsupported = errors.New("token test is not available for stdio servers")

	// ErrInvalidProfile: the profile name does not match ^[a-z][a-z0-9_-]{0,31}$.
	ErrInvalidProfile = errors.New("invalid connection profile name")

	headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)
	profilePattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// ValidProfile reports whether name is a valid connection profile name (same rule as the schema).
func ValidProfile(name string) bool { return profilePattern.MatchString(name) }

// UpstreamRejectedError: the upstream refused the candidate token (HTTP 401/403).
type UpstreamRejectedError struct{ Status int }

func (e *UpstreamRejectedError) Error() string {
	return fmt.Sprintf("upstream rejected the token (HTTP %d)", e.Status)
}

// NotConnectedError tells which URL the user must open to connect. errors.Is(err, ErrNotConnected) holds.
type NotConnectedError struct {
	ServerSlug string
	ConnectURL string
	// Profile is the named connection that is missing; empty for the default one.
	Profile string
}

func (e *NotConnectedError) Error() string { return e.text().EN }

// Localize implements i18n.Localizer.
func (e *NotConnectedError) Localize(l i18n.Locale) string { return e.text().In(l) }

func (e *NotConnectedError) text() i18n.Text {
	if e.Profile != "" {
		return i18n.T("connection %q is not connected to server %q: ask the user to open %s",
			"conexão %q não conectada ao servidor %q: peça ao usuário para abrir %s", e.Profile, e.ServerSlug, e.ConnectURL)
	}
	return i18n.T("account not connected to server %q: ask the user to open %s",
		"conta não conectada ao servidor %q: peça ao usuário para abrir %s", e.ServerSlug, e.ConnectURL)
}

func (e *NotConnectedError) Is(target error) bool { return target == ErrNotConnected }

// NotConnected satisfies the codemode contract (`NotConnected() bool`).
func (e *NotConnectedError) NotConnected() bool { return true }

// UserFunc returns the authenticated gateway user of a browser request (the session of the
// login/consent pages); ok is false when there is none.
type UserFunc func(r *http.Request) (userID string, ok bool)

// Options configures a Manager.
type Options struct {
	Pool  *pgxpool.Pool
	Vault *vault.Vault
	// PublicURL is the gateway origin (config.PublicURL); the connect callback and the
	// CIMD document live under it.
	PublicURL string
	User      UserFunc
	// OnConnected, if set, is called after a user connected an account and its tokens were
	// stored (e.g. to sync the server's catalog). It runs on the callback request, so it must
	// return at once and do any work in the background.
	OnConnected func(serverID, userID string)
}

// Manager implements the connect flow and credential lookup. Safe for concurrent use.
type Manager struct {
	pool        *pgxpool.Pool
	vault       *vault.Vault
	publicURL   string
	user        UserFunc
	onConnected func(serverID, userID string)

	http    *fetcher
	now     func() time.Time
	disc    discoveryCache
	flights singleflight.Group
}

// New validates the options and builds a Manager.
func New(o Options) (*Manager, error) {
	if o.Pool == nil || o.Vault == nil || o.User == nil {
		return nil, errors.New("upstreamauth: Pool, Vault and User are required")
	}
	u, err := url.Parse(o.PublicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("upstreamauth: PublicURL must be an absolute http(s) URL")
	}
	return &Manager{
		pool:        o.Pool,
		vault:       o.Vault,
		publicURL:   strings.TrimRight(o.PublicURL, "/"),
		user:        o.User,
		onConnected: o.OnConnected,
		http:        newFetcher(),
		now:         time.Now,
	}, nil
}

// ConnectURL is where a user connects their account to the server with the given slug.
func (m *Manager) ConnectURL(slug string) string { return m.publicURL + connectPathBase + slug }

// ConnectURLForProfile is ConnectURL for a named connection; "" or "default" give ConnectURL.
func (m *Manager) ConnectURLForProfile(slug, profile string) string {
	return m.ConnectURL(slug) + profileQuery("?", profile)
}

// profileQuery is the query parameter selecting a named profile, empty for the default one.
func profileQuery(sep, profile string) string {
	if profile == "" || profile == DefaultProfile {
		return ""
	}
	return sep + "profile=" + url.QueryEscape(profile)
}

// ClientMetadataURL is the CIMD client_id of the gateway.
func (m *Manager) ClientMetadataURL() string { return m.publicURL + clientMetaPath }

func (m *Manager) redirectURI() string { return m.publicURL + callbackPath }

// PortalURL is where a user pastes a token for a header server.
func (m *Manager) PortalURL(slug string) string {
	return m.publicURL + portalConnectPath + url.QueryEscape(slug)
}

// notConnected points header servers at the portal and OAuth servers at the connect flow, for the
// given profile ("" or "default" for the default connection).
func (m *Manager) notConnected(srv *serverRow, profile string) error {
	u := m.ConnectURLForProfile(srv.Slug, profile)
	if srv.AuthKind != AuthKindOAuth {
		u = m.PortalURL(srv.Slug) + profileQuery("&", profile)
	}
	if profile == DefaultProfile {
		profile = ""
	}
	return &NotConnectedError{ServerSlug: srv.Slug, ConnectURL: u, Profile: profile}
}

// Routes mounts the connect flow and the CIMD document on mux:
//
//	GET /connect/{slug}                 start the flow (needs an authenticated user)
//	GET /connect/callback               authorization response
//	GET /connect/success                PT-BR confirmation page
//	GET /oauth/client-metadata.json     the gateway's Client ID Metadata Document
func (m *Manager) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+callbackPath, m.handleCallback)
	mux.HandleFunc("GET "+successPath, m.handleSuccess)
	mux.HandleFunc("GET "+connectPathBase+"{slug}", m.handleConnect)
	mux.HandleFunc("GET "+clientMetaPath, m.handleClientMetadata)
}

// ctxKey-free helper: bounded context for flows that must outlive the caller's cancellation.
func detached(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}
