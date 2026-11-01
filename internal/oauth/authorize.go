package oauth

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

const (
	maxStateLen   = 2048
	maxFormBytes  = 64 << 10
	scopeOffline  = "offline_access"
	actionLogin   = "login"
	actionConsent = "consent"
	actionDeny    = "deny"
)

// codeChallengePattern is a base64url SHA-256 digest (RFC 7636 S256).
var codeChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// authorizeError is either shown on a page (the redirect target is not trusted yet) or sent
// to the client's redirect_uri with iss and state (RFC 6749 4.1.2.1, RFC 9207).
type authorizeError struct {
	redirectURI string
	state       string
	code        string
	description string
	message     i18n.Text // shown on the error page when there is no trusted redirect target
	status      int
}

var errClientValidation = i18n.T("Could not validate the application that requested access.", "Não foi possível validar o aplicativo que pediu acesso.")

func pageError(status int, message i18n.Text) *authorizeError {
	return &authorizeError{status: status, message: message}
}

// validateAuthorize checks an authorization request and returns it ready to seal.
func (s *Server) validateAuthorize(r *http.Request) (authzRequest, *Client, *authorizeError) {
	q := r.URL.Query()
	for key, values := range q {
		if len(values) > 1 {
			return authzRequest{}, nil, pageError(http.StatusBadRequest, i18n.T("Repeated request parameter: %s", "Parâmetro repetido na requisição: %s", key))
		}
	}
	clientID := q.Get("client_id")
	if clientID == "" {
		return authzRequest{}, nil, pageError(http.StatusBadRequest, i18n.T("Request without an application identifier (client_id).", "Requisição sem identificação do aplicativo (client_id)."))
	}
	client, err := s.clients.resolve(r.Context(), clientID, clientIP(r))
	if err != nil {
		s.log.Info("oauth: client resolution failed", "client_id", clientID, "err", err)
		return authzRequest{}, nil, pageError(http.StatusBadRequest, errClientValidation)
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" || !client.matchRedirect(redirectURI) {
		return authzRequest{}, nil, pageError(http.StatusBadRequest, i18n.T("The return address (redirect_uri) is not registered for this application.", "O endereço de retorno (redirect_uri) não está registrado para este aplicativo."))
	}
	req := authzRequest{ClientID: clientID, RedirectURI: redirectURI, State: q.Get("state")}
	fail := func(code, description string) (authzRequest, *Client, *authorizeError) {
		return authzRequest{}, nil, &authorizeError{redirectURI: redirectURI, state: req.State, code: code, description: description}
	}
	if len(req.State) > maxStateLen {
		return fail("invalid_request", "state too long")
	}
	if q.Get("response_type") != "code" {
		return fail("unsupported_response_type", "only response_type=code is supported")
	}
	if mode := q.Get("response_mode"); mode != "" && mode != "query" {
		return fail("invalid_request", "only response_mode=query is supported")
	}
	if q.Get("code_challenge_method") != "S256" || !codeChallengePattern.MatchString(q.Get("code_challenge")) {
		return fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
	}
	req.CodeChallenge = q.Get("code_challenge")
	if !s.isOwnResource(q.Get("resource")) {
		return fail("invalid_target", "resource must be "+s.resource)
	}
	scope, ok := grantedScope(q.Get("scope"))
	if !ok {
		return fail("invalid_scope", "supported scope: "+ScopeMCP)
	}
	req.Scope = scope
	return req, client, nil
}

// isOwnResource compares an RFC 8707 resource to ours, tolerating scheme/host case.
func (s *Server) isOwnResource(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	return strings.ToLower(u.Scheme)+"://"+strings.ToLower(u.Host)+u.EscapedPath() == s.resource
}

// grantedScope accepts an empty scope or any subset of {mcp, offline_access}; the grant is
// always "mcp" (refresh tokens are decided by the client's grant_types).
func grantedScope(requested string) (string, bool) {
	for _, sc := range strings.Fields(requested) {
		if sc != ScopeMCP && sc != scopeOffline {
			return "", false
		}
	}
	return ScopeMCP, true
}

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	req, client, aerr := s.validateAuthorize(r)
	if aerr != nil {
		s.failAuthorize(w, r, aerr)
		return
	}
	userID, email := s.currentUser(r)
	if userID == "" {
		s.renderLogin(w, r, req, client, http.StatusOK, i18n.Text{})
		return
	}
	s.renderConsent(w, r, req, client, userID, email)
}

// handleAuthorizeForm handles the login, consent and deny form posts.
func (s *Server) handleAuthorizeForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, i18n.T("Invalid form.", "Formulário inválido."))
		return
	}
	if !s.checkCSRF(r) {
		s.renderError(w, r, http.StatusForbidden, i18n.T("The form expired or comes from an unknown origin. Go back to the application and try again.", "Formulário expirado ou de origem desconhecida. Volte ao aplicativo e tente de novo."))
		return
	}
	req, err := s.openRequest(r.PostFormValue(requestField))
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, i18n.T("The authorization request expired or is invalid. Go back to the application and try again.", "Pedido de autorização expirado ou inválido. Volte ao aplicativo e tente de novo."))
		return
	}
	client, err := s.clients.resolve(r.Context(), req.ClientID, clientIP(r))
	if err != nil || !client.matchRedirect(req.RedirectURI) {
		s.renderError(w, r, http.StatusBadRequest, errClientValidation)
		return
	}
	switch r.PostFormValue("action") {
	case actionLogin:
		s.handleLogin(w, r, req, client)
	case actionConsent:
		s.handleConsent(w, r, req, client)
	case actionDeny:
		s.failAuthorize(w, r, &authorizeError{redirectURI: req.RedirectURI, state: req.State,
			code: "access_denied", description: "the user denied the request"})
	default:
		s.renderError(w, r, http.StatusBadRequest, i18n.T("Unknown action.", "Ação desconhecida."))
	}
}

func (s *Server) failAuthorize(w http.ResponseWriter, r *http.Request, aerr *authorizeError) {
	if aerr.redirectURI == "" {
		s.renderError(w, r, aerr.status, aerr.message)
		return
	}
	params := url.Values{"error": {aerr.code}}
	if aerr.description != "" {
		params.Set("error_description", aerr.description)
	}
	s.redirectToClient(w, r, aerr.redirectURI, aerr.state, params)
}

// redirectToClient sends the authorization response, always with iss (RFC 9207).
func (s *Server) redirectToClient(w http.ResponseWriter, r *http.Request, redirectURI, state string, params url.Values) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, i18n.T("Invalid return address.", "Endereço de retorno inválido."))
		return
	}
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	if state != "" {
		q.Set("state", state)
	}
	q.Set("iss", s.issuer)
	u.RawQuery = q.Encode()
	status := http.StatusFound
	if r.Method == http.MethodPost {
		status = http.StatusSeeOther
	}
	http.Redirect(w, r, u.String(), status)
}

// currentUser returns the session user id and email, or empty values without a valid
// session for an existing user.
func (s *Server) currentUser(r *http.Request) (string, string) {
	userID := s.sessionUser(r)
	if userID == "" {
		return "", ""
	}
	var email string
	err := s.pool.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1::uuid`, userID).Scan(&email)
	if err != nil {
		return "", ""
	}
	return userID, email
}

// SessionUser returns the user id of the login session carried by r (the cookie set by the
// login form), for sibling browser flows such as the upstream connect page.
func (s *Server) SessionUser(r *http.Request) (string, bool) {
	userID, _ := s.currentUser(r)
	return userID, userID != ""
}

func (s *Server) handleConsent(w http.ResponseWriter, r *http.Request, req authzRequest, client *Client) {
	userID, _ := s.currentUser(r)
	if userID == "" {
		s.renderLogin(w, r, req, client, http.StatusUnauthorized, i18n.T("Your session expired. Sign in again.", "Sua sessão expirou. Entre novamente."))
		return
	}
	ctx := r.Context()
	servers, err := s.consentServers(ctx, userID, client.ClientID, requestLocale(r))
	if err != nil {
		s.serverError(w, r, "load consent servers", err)
		return
	}
	levels, err := parseConsentForm(r.PostForm, servers)
	if err != nil {
		s.log.Warn("oauth: consent rejected", "user_id", userID, "client_id", client.ClientID, "err", err)
		s.renderError(w, r, http.StatusBadRequest, i18n.T("Invalid access level, or above what your account allows.", "Nível de acesso inválido ou acima do permitido para sua conta."))
		return
	}
	if err := s.saveConsents(ctx, userID, client.ClientID, levels); err != nil {
		s.serverError(w, r, "save consents", err)
		return
	}
	code, err := s.issueAuthorizationCode(ctx, req, userID)
	if err != nil {
		s.serverError(w, r, "issue authorization code", err)
		return
	}
	s.redirectToClient(w, r, req.RedirectURI, req.State, url.Values{"code": {code}})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.log.Error("oauth: "+what, "err", err)
	s.renderError(w, r, http.StatusInternalServerError, i18n.T("Internal error. Try again shortly.", "Erro interno. Tente novamente em instantes."))
}
