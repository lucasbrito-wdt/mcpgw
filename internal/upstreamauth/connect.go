package upstreamauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

const (
	randomBytes = 32
	// sharedTicketTTL is how long a password confirmation (done by the portal) stays valid for
	// starting a shared OAuth connection.
	sharedTicketTTL = 5 * time.Minute
	sharedTicketAAD = "shared_oauth_ticket"
	// auditSharedOAuth is the admin_events action of a shared OAuth grant. admin_events.action
	// allows a single dot, hence the underscore.
	auditSharedOAuth = "connection.shared_oauth"
)

func randomToken() (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// handleConnect starts the authorization code + PKCE flow for the authenticated user.
func (m *Manager) handleConnect(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	userID, ok := m.user(r)
	if !ok {
		renderPage(w, r, http.StatusUnauthorized, i18n.T("Login required", "Login necessário"), i18n.T("Sign in to the gateway and open this link again.", "Entre no gateway e abra este link novamente."))
		return
	}
	slug := r.PathValue("slug")
	shared := r.URL.Query().Has("shared")
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		profile = DefaultProfile
	} else if shared || !ValidProfile(profile) {
		renderPage(w, r, http.StatusBadRequest, i18n.T("Invalid request", "Requisição inválida"), i18n.T("Invalid connection name. Use lowercase letters, numbers, _ or -, starting with a letter (up to 32 characters). Shared connections have no name.", "Nome de conexão inválido. Use letras minúsculas, números, _ ou -, começando por uma letra (até 32 caracteres). Conexões compartilhadas não têm nome."))
		return
	}
	if shared && !m.validSharedTicket(r, userID, slug) {
		renderPage(w, r, http.StatusForbidden, i18n.T("Access denied", "Acesso negado"), i18n.T("Only administrators can connect a shared account. Confirm your password in the portal and try again.", "Somente administradores podem conectar uma conta compartilhada. Confirme sua senha no portal e tente novamente."))
		return
	}
	srv, err := m.serverBySlug(r.Context(), slug)
	if err != nil || srv.AuthKind != AuthKindOAuth || srv.Status != "active" {
		renderPage(w, r, http.StatusNotFound, i18n.T("Server not found", "Servidor não encontrado"), i18n.T("This server does not exist or does not use per-account connections.", "Este servidor não existe ou não usa conexão por conta."))
		return
	}
	// Only users the admin granted access to the server may connect a personal account to it: the
	// connect triggers outbound discovery/registration and a catalog sync.
	if !shared && policy.CeilingOf(r.Context(), m.pool, userID, srv.ID) == policy.LevelNone {
		renderPage(w, r, http.StatusForbidden, i18n.T("Access denied", "Acesso negado"), i18n.T("You do not have access to this server. Ask an administrator to grant it.", "Você não tem acesso a este servidor. Peça ao administrador para liberá-lo."))
		return
	}
	authURL, err := m.beginFlow(r.Context(), srv, userID, profile, shared)
	if err != nil {
		slog.Warn("upstream connect failed", "server", srv.Slug, "err", err)
		message := i18n.T("Could not start authorization with the server. Try again later or tell an administrator.", "Não foi possível iniciar a autorização com o servidor. Tente novamente mais tarde ou avise o administrador.")
		if errors.Is(err, ErrRegistrationRejected) || errors.Is(err, ErrNoClientRegistration) {
			message = i18n.T("This server does not accept mcpgw automatic registration. An administrator must configure an OAuth app (client ID) of its own for it.", "Este servidor não aceita o cadastro automático do mcpgw. O administrador precisa configurar um app OAuth próprio (client ID) para ele.")
		}
		renderPage(w, r, http.StatusBadGateway, i18n.T("Could not connect", "Não foi possível conectar"), message)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

type sharedTicket struct {
	UID  string `json:"uid"`
	Slug string `json:"slug"`
	Exp  int64  `json:"exp"`
}

// SharedConnectURL mints the URL that starts a shared (admin) OAuth connection: the connect URL
// plus a sealed ticket proving userID passed the admin and password check (5 minutes). Only the
// portal calls it, right after that check. Empty when sealing fails.
func (m *Manager) SharedConnectURL(userID, slug string) string {
	raw, err := json.Marshal(sharedTicket{UID: userID, Slug: slug, Exp: m.now().Add(sharedTicketTTL).Unix()})
	if err != nil {
		return ""
	}
	sealed, err := m.vault.Seal(raw, []byte(sharedTicketAAD))
	if err != nil {
		return ""
	}
	return m.ConnectURL(slug) + "?shared=" + base64.RawURLEncoding.EncodeToString(sealed)
}

// validSharedTicket checks the ?shared= ticket against the session user and slug, and that the
// user is an admin right now.
func (m *Manager) validSharedTicket(r *http.Request, userID, slug string) bool {
	sealed, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("shared"))
	if err != nil {
		return false
	}
	raw, err := m.vault.Open(sealed, []byte(sharedTicketAAD))
	if err != nil {
		return false
	}
	var t sharedTicket
	if json.Unmarshal(raw, &t) != nil || !sameUserID(t.UID, userID) || t.Slug != slug || !m.now().Before(time.Unix(t.Exp, 0)) {
		return false
	}
	return m.isAdmin(r.Context(), userID)
}

func (m *Manager) isAdmin(ctx context.Context, userID string) bool {
	var admin bool
	err := m.pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1::uuid`, userID).Scan(&admin)
	return err == nil && admin
}

// beginFlow persists the state (with everything the callback must trust) and builds the
// authorization URL.
func (m *Manager) beginFlow(ctx context.Context, srv *serverRow, userID, profile string, shared bool) (string, error) {
	d, err := m.Discover(ctx, srv.URL, nil)
	if err != nil {
		return "", err
	}
	client, err := m.resolveClient(ctx, srv, d)
	if err != nil {
		return "", err
	}
	state, err := randomToken()
	if err != nil {
		return "", err
	}
	verifier, err := randomToken()
	if err != nil {
		return "", err
	}
	verifierCT, err := m.vault.Seal([]byte(verifier), stateProfileAAD(srv.ID, userID, profile))
	if err != nil {
		return "", err
	}
	_, err = m.pool.Exec(ctx, `INSERT INTO upstream_oauth_states
		(state, server_id, user_id, code_verifier_ciphertext, expires_at, issuer, client_id, token_endpoint, iss_required, shared, profile)
		VALUES ($1, $2::uuid, $3::uuid, $4, now() + $5::interval, $6, $7, $8, $9, $10, $11)`,
		hashState(state), srv.ID, userID, verifierCT, fmt.Sprintf("%d seconds", int(stateTTL.Seconds())),
		d.Meta.Issuer, client.ID, d.Meta.TokenEndpoint, d.Meta.AuthorizationResponseIssParameterSupported, shared, profile)
	if err != nil {
		return "", fmt.Errorf("store connect state: %w", err)
	}
	return m.authorizationURL(d, client, state, pkceChallenge(verifier))
}

func (m *Manager) authorizationURL(d *Discovery, c oauthClient, state, challenge string) (string, error) {
	u, err := url.Parse(d.Meta.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.ID)
	q.Set("redirect_uri", m.redirectURI())
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("resource", d.Resource)
	if d.Scope != "" {
		q.Set("scope", d.Scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// consumedState is a connect state that was atomically taken out of the table.
type consumedState struct {
	serverID, userID                string
	verifierCT                      []byte
	issuer, clientID, tokenEndpoint string
	issRequired                     bool
	shared                          bool
	profile                         string
}

// consumeState deletes and returns the state in one statement: single use, 10 minute lifetime.
// It runs before any other validation so a failed attempt can never be retried.
func (m *Manager) consumeState(ctx context.Context, state string) (*consumedState, error) {
	var s consumedState
	err := m.pool.QueryRow(ctx, `DELETE FROM upstream_oauth_states WHERE state = $1 AND expires_at > now()
		RETURNING server_id::text, user_id::text, code_verifier_ciphertext, issuer, client_id, token_endpoint, iss_required, shared, profile`,
		hashState(state)).Scan(&s.serverID, &s.userID, &s.verifierCT, &s.issuer, &s.clientID, &s.tokenEndpoint, &s.issRequired, &s.shared, &s.profile)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// handleCallback finishes the flow. The URL carries the authorization code, so it is never logged.
func (m *Manager) handleCallback(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	userID, ok := m.user(r)
	if !ok {
		renderPage(w, r, http.StatusUnauthorized, i18n.T("Login required", "Login necessário"), i18n.T("Your session expired. Sign in to the gateway and connect the server again.", "Sua sessão expirou. Entre no gateway e conecte o servidor novamente."))
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		renderPage(w, r, http.StatusBadRequest, i18n.T("Invalid request", "Requisição inválida"), i18n.T("Invalid authorization response.", "Resposta de autorização inválida."))
		return
	}
	st, err := m.consumeState(r.Context(), state)
	if errors.Is(err, pgx.ErrNoRows) {
		renderPage(w, r, http.StatusBadRequest, i18n.T("Link expired", "Link expirado"), i18n.T("This connection link has expired or was already used. Start the connection again.", "Este link de conexão expirou ou já foi utilizado. Inicie a conexão novamente."))
		return
	}
	if err != nil {
		slog.Warn("upstream callback: consume state", "err", err)
		renderPage(w, r, http.StatusInternalServerError, i18n.T("Internal error", "Erro interno"), i18n.T("Could not complete the connection.", "Não foi possível concluir a conexão."))
		return
	}
	if !sameUserID(st.userID, userID) {
		renderPage(w, r, http.StatusForbidden, i18n.T("Access denied", "Acesso negado"), i18n.T("This connection was started by another user.", "Esta conexão foi iniciada por outro usuário."))
		return
	}
	if st.shared && !m.isAdmin(r.Context(), userID) {
		renderPage(w, r, http.StatusForbidden, i18n.T("Access denied", "Acesso negado"), i18n.T("Only administrators can connect a shared account.", "Somente administradores podem conectar uma conta compartilhada."))
		return
	}
	if msg, bad := validateAuthResponse(q, st); bad {
		renderPage(w, r, http.StatusBadRequest, i18n.T("Connection not authorized", "Conexão não autorizada"), msg)
		return
	}
	slug, err := m.completeFlow(r.Context(), st, q.Get("code"), requestIP(r))
	if err != nil {
		slog.Warn("upstream callback: token exchange failed", "err", err)
		renderPage(w, r, http.StatusBadGateway, i18n.T("Could not connect", "Não foi possível conectar"),
			i18n.T("The server refused the authorization. Try again or tell an administrator.", "O servidor recusou a autorização. Tente novamente ou avise o administrador."))
		return
	}
	if m.onConnected != nil {
		m.onConnected(st.serverID, st.userID)
	}
	http.Redirect(w, r, "/portal/?connected="+url.QueryEscape(slug), http.StatusSeeOther)
}

func sameUserID(a, b string) bool { return validUUID(b) && normalizeID(a) == normalizeID(b) }

func normalizeID(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// validateAuthResponse checks error, iss (RFC 9207) and code; it returns a localizable message and true on failure.
// If the AS advertised iss support it is mandatory; if present anyway it must match the issuer
// recorded when the flow started (protection against mix-up attacks).
func validateAuthResponse(q url.Values, st *consumedState) (i18n.Text, bool) {
	if q.Get("error") != "" {
		return i18n.T("The server denied the authorization.", "O servidor negou a autorização."), true
	}
	iss, hasIss := q["iss"]
	switch {
	case hasIss && !sameURL(iss[0], st.issuer):
		return i18n.T("Authorization response from an unexpected issuer.", "Resposta de autorização de um emissor inesperado."), true
	case !hasIss && st.issRequired:
		return i18n.T("Authorization response without issuer identification.", "Resposta de autorização sem identificação do emissor."), true
	case q.Get("code") == "":
		return i18n.T("Authorization response without a code.", "Resposta de autorização sem código."), true
	}
	return i18n.Text{}, false
}

// completeFlow exchanges the code and stores the sealed tokens; it returns the server slug.
func (m *Manager) completeFlow(ctx context.Context, st *consumedState, code, ip string) (string, error) {
	srv, err := m.serverByID(ctx, st.serverID)
	if err != nil {
		return "", err
	}
	if srv.Status != "active" || srv.AuthKind != AuthKindOAuth {
		return "", errors.New("server is not connectable")
	}
	verifier, err := m.vault.Open(st.verifierCT, stateProfileAAD(st.serverID, st.userID, st.profile))
	if err != nil {
		return "", fmt.Errorf("open code verifier: %w", err)
	}
	client, err := m.clientForGrant(ctx, srv, st.issuer, st.clientID)
	if err != nil {
		return "", err
	}
	resource, err := canonicalResource(srv.URL)
	if err != nil {
		return "", err
	}
	tr, err := m.requestToken(ctx, st.tokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {m.redirectURI()},
		"code_verifier": {string(verifier)},
		"resource":      {resource},
	}, client)
	if err != nil {
		return "", err
	}
	return srv.Slug, m.saveGrant(ctx, st, tr, srv.Slug, ip)
}

// saveGrant stores the grant as the user's personal credential under the state's profile, or as the server's shared one
// (user_id NULL, connected_by = the admin) together with its audit event, in one transaction.
func (m *Manager) saveGrant(ctx context.Context, st *consumedState, tr *tokenResponse, slug, ip string) error {
	row := credentialRow{ServerID: st.serverID, UserID: st.userID, Profile: st.profile, Shared: st.shared}
	aad := row.aad()
	access, err := m.vault.Seal([]byte(tr.AccessToken), aad)
	if err != nil {
		return err
	}
	var refresh []byte
	if tr.RefreshToken != "" {
		if refresh, err = m.vault.Seal([]byte(tr.RefreshToken), aad); err != nil {
			return err
		}
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin store upstream credential: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	userCol, conflict := "$2::uuid", "(server_id, user_id, profile)"
	if st.shared {
		userCol, conflict = "NULL", "(server_id) WHERE shared"
	}
	_, err = tx.Exec(ctx, `INSERT INTO upstream_credentials
		(server_id, user_id, shared, connected_by, access_ciphertext, refresh_ciphertext, key_id, expires_at, scope, issuer, client_id, token_endpoint, profile, method)
		VALUES ($1::uuid, `+userCol+`, $11, CASE WHEN $11 THEN $2::uuid END, $3, $4, $5, $6, $7, $8, $9, $10, $12, 'oauth')
		ON CONFLICT `+conflict+` DO UPDATE SET
		    connected_by = EXCLUDED.connected_by,
		    access_ciphertext = EXCLUDED.access_ciphertext, refresh_ciphertext = EXCLUDED.refresh_ciphertext,
		    key_id = EXCLUDED.key_id, expires_at = EXCLUDED.expires_at, scope = EXCLUDED.scope,
		    issuer = EXCLUDED.issuer, client_id = EXCLUDED.client_id, token_endpoint = EXCLUDED.token_endpoint,
		    status = 'active', revoked_reason = NULL, last4 = NULL, method = 'oauth', updated_at = now()`,
		st.serverID, st.userID, access, refresh, m.vault.KeyID(), m.expiryOf(tr), tr.Scope,
		st.issuer, st.clientID, st.tokenEndpoint, st.shared, st.profile)
	if err != nil {
		return fmt.Errorf("store upstream credential: %w", err)
	}
	if st.shared {
		// details stays empty: an audit row must never carry a token.
		if _, err := tx.Exec(ctx, `INSERT INTO admin_events (actor_user_id, action, target, details, ip)
			VALUES ($1::uuid, $2, $3, '{}'::jsonb, $4)`, st.userID, auditSharedOAuth, slug, ip); err != nil {
			return fmt.Errorf("audit shared connection: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit upstream credential: %w", err)
	}
	return nil
}

// requestIP is the TCP peer address (IPv6 by /64), the same rule as the oauth package's clientIP;
// X-Forwarded-For is not trusted.
func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}
