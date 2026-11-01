package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const callback = "http://127.0.0.1:53682/callback"

func TestHappyPathWithLogin(t *testing.T) {
	e := newTestEnv(t)
	p := newPKCE()

	w := e.getAuthorize(e.authorizeParams(p))
	if !strings.Contains(w.Body.String(), `name="password"`) {
		t.Fatalf("expected login page, got: %s", w.Body.String())
	}
	assertPageHeaders(t, w)
	form := formState(t, w)
	csrf := cookieNamed(t, w, csrfCookie)
	if !csrf.HttpOnly || !csrf.Secure || csrf.SameSite != http.SameSiteLaxMode || !strings.HasPrefix(csrf.Name, "__Host-") {
		t.Fatalf("csrf cookie attributes: %+v", csrf)
	}

	form.Set("action", actionLogin)
	form.Set("email", strings.ToUpper(testEmail))
	form.Set("password", testPassword)
	w = e.postForm("/authorize", form, csrf)
	sess := cookieNamed(t, w, sessionCookie)
	if !sess.HttpOnly || !sess.Secure || sess.SameSite != http.SameSiteLaxMode || sess.MaxAge != int(sessionTTL.Seconds()) {
		t.Fatalf("session cookie attributes: %+v", sess)
	}
	body := w.Body.String()
	for _, want := range []string{"Claude Code (teste)", "github", "notion", "127.0.0.1", "local address"} {
		if !strings.Contains(body, want) {
			t.Errorf("consent page lacks %q", want)
		}
	}
	if strings.Contains(body, e.serverIDs["secret"]) {
		t.Error("consent page lists a server without a ceiling")
	}
	if notion := selectBlock(body, e.serverIDs["notion"]); notion == "" || strings.Contains(notion, `value="write"`) {
		t.Errorf("notion select must stop at its read ceiling: %q", notion)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' http://127.0.0.1:53682") {
		t.Errorf("consent CSP form-action: %s", csp)
	}

	form = formState(t, w)
	form.Set("action", actionConsent)
	form.Set(levelFieldPrefix+e.serverIDs["github"], "write")
	form.Set(levelFieldPrefix+e.serverIDs["notion"], "read")
	params := redirectParams(t, e.postForm("/authorize", form, csrf, sess), http.StatusSeeOther)
	if params.Get("iss") != testIssuer || params.Get("state") != "xyz-state" || params.Get("code") == "" {
		t.Fatalf("redirect params: %v", params)
	}

	tokens := decodeTokens(t, e.exchange(params.Get("code"), p.verifier, callback))
	if tokens.TokenType != "Bearer" || tokens.RefreshToken == "" || tokens.ExpiresIn != 600 || tokens.Scope != ScopeMCP {
		t.Fatalf("token response: %+v", tokens)
	}
	claims, err := e.srv.ValidateAccessToken(context.Background(), tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != e.userID || claims.ClientID != e.clientID || claims.Audience != testIssuer+"/mcp" ||
		claims.Issuer != testIssuer || claims.TokenID == "" || claims.ExpiresAt.Sub(claims.IssuedAt) != accessTokenTTL {
		t.Fatalf("claims: %+v", claims)
	}
	assertConsents(t, e, map[string]string{"github": "write", "notion": "read"})
}

func selectBlock(body, serverID string) string {
	start := strings.Index(body, `<fieldset class="server" id="level_`+serverID+`"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start:], "</fieldset>")
	return body[start : start+end]
}

func newGet(path string) *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }

func newFormRequest(path string, form url.Values, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func assertPageHeaders(t *testing.T, w interface{ Header() http.Header }) {
	t.Helper()
	h := w.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "frame-ancestors 'none'", "style-src 'nonce-"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if h.Get("X-Frame-Options") != "DENY" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("headers: %v", h)
	}
}

func assertConsents(t *testing.T, e *testEnv, want map[string]string) {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT us.slug, c.level, c.expires_at FROM consents c
		JOIN upstream_servers us ON us.id = c.server_id WHERE c.user_id = $1 AND c.client_id = $2`, e.userID, e.clientID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var slug, level string
		var exp time.Time
		e.must(rows.Scan(&slug, &level, &exp))
		got[slug] = level
		if d := exp.Sub(e.clock.Now()); d < consentTTL-time.Minute || d > consentTTL+time.Minute {
			t.Errorf("consent %s expires in %v", slug, d)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("consents %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("consents %v, want %v", got, want)
		}
	}
}

func TestLoginFailures(t *testing.T) {
	e := newTestEnv(t)
	w := e.getAuthorize(e.authorizeParams(newPKCE()))
	form, csrf := formState(t, w), cookieNamed(t, w, csrfCookie)
	form.Set("action", actionLogin)
	form.Set("email", testEmail)
	form.Set("password", "wrong password here")
	if w := e.postForm("/authorize", form, csrf); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d", w.Code)
	}
	for range 5 {
		e.postForm("/authorize", form, csrf)
	}
	form.Set("password", testPassword)
	if w := e.postForm("/authorize", form, csrf); w.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: status %d", w.Code)
	}
}

func TestAuthorizeRejections(t *testing.T) {
	e := newTestEnv(t)
	sess := e.sessionCookie()
	cases := []struct {
		name     string
		mutate   func(url.Values)
		redirect bool
		errCode  string
	}{
		{"pkce plain", func(q url.Values) { q.Set("code_challenge_method", "plain") }, true, "invalid_request"},
		{"pkce missing method", func(q url.Values) { q.Del("code_challenge_method") }, true, "invalid_request"},
		{"pkce missing challenge", func(q url.Values) { q.Del("code_challenge") }, true, "invalid_request"},
		{"wrong resource", func(q url.Values) { q.Set("resource", "https://other.example/mcp") }, true, "invalid_target"},
		{"missing resource", func(q url.Values) { q.Del("resource") }, true, "invalid_target"},
		{"token response type", func(q url.Values) { q.Set("response_type", "token") }, true, "unsupported_response_type"},
		{"unknown scope", func(q url.Values) { q.Set("scope", "admin") }, true, "invalid_scope"},
		{"redirect mismatch", func(q url.Values) { q.Set("redirect_uri", "https://evil.example/callback") }, false, ""},
		{"loopback other path", func(q url.Values) { q.Set("redirect_uri", "http://127.0.0.1:5000/other") }, false, ""},
		{"loopback other host", func(q url.Values) { q.Set("redirect_uri", "http://localhost:5000/callback") }, false, ""},
		{"loopback userinfo trick", func(q url.Values) { q.Set("redirect_uri", "http://127.0.0.1:80@evil.example/callback") }, false, ""},
		{"unknown client", func(q url.Values) { q.Set("client_id", "mcpgw-unknown") }, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := e.authorizeParams(newPKCE())
			tc.mutate(q)
			w := e.getAuthorize(q, sess)
			if !tc.redirect {
				if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
					t.Fatalf("want error page, got %d location %q", w.Code, w.Header().Get("Location"))
				}
				return
			}
			params := redirectParams(t, w, http.StatusFound)
			if params.Get("error") != tc.errCode || params.Get("iss") != testIssuer || params.Get("state") != "xyz-state" {
				t.Fatalf("redirect params: %v", params)
			}
		})
	}
}

func TestTokenExchangeFailures(t *testing.T) {
	e := newTestEnv(t)
	t.Run("pkce mismatch consumes the code", func(t *testing.T) {
		p := newPKCE()
		code := e.authorizeCode(p)
		if w := e.exchange(code, newPKCE().verifier, callback); oauthErrorCode(t, w) != "invalid_grant" {
			t.Fatalf("wrong verifier: %d %s", w.Code, w.Body.String())
		}
		if w := e.exchange(code, p.verifier, callback); w.Code != http.StatusBadRequest {
			t.Fatalf("code must be single use after a failed attempt: %d", w.Code)
		}
	})
	t.Run("redirect mismatch", func(t *testing.T) {
		p := newPKCE()
		code := e.authorizeCode(p)
		if w := e.exchange(code, p.verifier, "http://127.0.0.1:9999/callback"); oauthErrorCode(t, w) != "invalid_grant" {
			t.Fatalf("redirect mismatch: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("wrong resource", func(t *testing.T) {
		p := newPKCE()
		code := e.authorizeCode(p)
		w := e.postForm("/token", url.Values{"grant_type": {grantAuthorizationCode}, "code": {code},
			"code_verifier": {p.verifier}, "client_id": {e.clientID}, "redirect_uri": {callback},
			"resource": {"https://other.example/mcp"}})
		if oauthErrorCode(t, w) != "invalid_target" {
			t.Fatalf("wrong resource: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("expired code", func(t *testing.T) {
		p := newPKCE()
		code := e.authorizeCode(p)
		e.clock.Advance(authorizationCodeTTL + time.Second)
		if w := e.exchange(code, p.verifier, callback); oauthErrorCode(t, w) != "invalid_grant" {
			t.Fatalf("expired code: %d %s", w.Code, w.Body.String())
		}
	})
}

func TestCodeReuseRevokesFamily(t *testing.T) {
	e := newTestEnv(t)
	p := newPKCE()
	code := e.authorizeCode(p)
	tokens := decodeTokens(t, e.exchange(code, p.verifier, callback))
	if w := e.exchange(code, p.verifier, callback); oauthErrorCode(t, w) != "invalid_grant" {
		t.Fatalf("reuse: %d %s", w.Code, w.Body.String())
	}
	if w := e.refreshWith(tokens.RefreshToken); oauthErrorCode(t, w) != "invalid_grant" {
		t.Fatalf("refresh after code reuse must fail: %d %s", w.Code, w.Body.String())
	}
	if _, err := e.srv.ValidateAccessToken(context.Background(), tokens.AccessToken); err == nil {
		t.Fatal("access token of a revoked family must be rejected")
	}
}

func TestRefreshRotationAndReuse(t *testing.T) {
	e := newTestEnv(t)
	p := newPKCE()
	first := decodeTokens(t, e.exchange(e.authorizeCode(p), p.verifier, callback))
	second := decodeTokens(t, e.refreshWith(first.RefreshToken))
	if second.RefreshToken == "" || second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh token must rotate")
	}
	if _, err := e.srv.ValidateAccessToken(context.Background(), second.AccessToken); err != nil {
		t.Fatal(err)
	}
	var hashed int
	e.must(e.pool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_refresh_tokens WHERE token_hash = $1`,
		hashToken(second.RefreshToken)).Scan(&hashed))
	if hashed != 1 {
		t.Fatal("refresh token must be stored hashed")
	}

	if w := e.refreshWith(first.RefreshToken); oauthErrorCode(t, w) != "invalid_grant" {
		t.Fatalf("reuse of rotated token: %d %s", w.Code, w.Body.String())
	}
	if w := e.refreshWith(second.RefreshToken); oauthErrorCode(t, w) != "invalid_grant" {
		t.Fatal("reuse detection must revoke the newest token of the family")
	}
	if _, err := e.srv.ValidateAccessToken(context.Background(), second.AccessToken); err == nil {
		t.Fatal("access token of a revoked family must be rejected")
	}
	var revoked int
	e.must(e.pool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_refresh_tokens WHERE revoked_at IS NOT NULL`).Scan(&revoked))
	if revoked != 2 {
		t.Fatalf("revoked tokens %d, want 2", revoked)
	}
}

func TestRefreshOtherClientRejected(t *testing.T) {
	e := newTestEnv(t)
	p := newPKCE()
	tokens := decodeTokens(t, e.exchange(e.authorizeCode(p), p.verifier, callback))
	w := e.postForm("/token", url.Values{"grant_type": {grantRefreshToken}, "refresh_token": {tokens.RefreshToken},
		"client_id": {"mcpgw-other"}})
	if oauthErrorCode(t, w) != "invalid_grant" {
		t.Fatalf("other client: %d %s", w.Code, w.Body.String())
	}
}

func TestConsentEnforcement(t *testing.T) {
	e := newTestEnv(t)
	cases := map[string]func(url.Values){
		"above ceiling":  func(f url.Values) { f.Set(levelFieldPrefix+e.serverIDs["notion"], "write") },
		"unknown level":  func(f url.Values) { f.Set(levelFieldPrefix+e.serverIDs["github"], "admin") },
		"no ceiling":     func(f url.Values) { f.Set(levelFieldPrefix+e.serverIDs["secret"], "read") },
		"repeated level": func(f url.Values) { f.Add(levelFieldPrefix+e.serverIDs["github"], "read") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			form, cookies := e.consentPage(newPKCE())
			form.Set("action", actionConsent)
			form.Set(levelFieldPrefix+e.serverIDs["github"], "write")
			mutate(form)
			w := e.postForm("/authorize", form, cookies...)
			if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
				t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
			}
		})
	}
	assertConsents(t, e, map[string]string{})
}

func TestCSRFRequired(t *testing.T) {
	e := newTestEnv(t)
	form, cookies := e.consentPage(newPKCE())
	form.Set("action", actionConsent)
	sess := cookies[0]

	missing := url.Values{requestField: {form.Get(requestField)}, "action": {actionConsent}}
	if w := e.postForm("/authorize", missing, cookies...); w.Code != http.StatusForbidden {
		t.Fatalf("missing token: %d", w.Code)
	}
	if w := e.postForm("/authorize", form, sess); w.Code != http.StatusForbidden {
		t.Fatalf("missing cookie: %d", w.Code)
	}
	wrong := url.Values{}
	for k, v := range form {
		wrong[k] = v
	}
	wrong.Set(csrfField, strings.Repeat("A", 43))
	if w := e.postForm("/authorize", wrong, cookies...); w.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", w.Code)
	}
	r := newFormRequest("/authorize", form, cookies...)
	r.Header.Set("Origin", "https://evil.example")
	if w := e.do(r); w.Code != http.StatusForbidden {
		t.Fatalf("cross origin: %d", w.Code)
	}
	tampered := url.Values{}
	for k, v := range form {
		tampered[k] = v
	}
	tampered.Set(requestField, form.Get(requestField)[:20]+"AAAA"+form.Get(requestField)[24:])
	if w := e.postForm("/authorize", tampered, cookies...); w.Code != http.StatusBadRequest {
		t.Fatalf("tampered request: %d", w.Code)
	}
}

func TestDenyRedirectsWithIss(t *testing.T) {
	e := newTestEnv(t)
	form, cookies := e.consentPage(newPKCE())
	form.Set("action", actionDeny)
	params := redirectParams(t, e.postForm("/authorize", form, cookies...), http.StatusSeeOther)
	if params.Get("error") != "access_denied" || params.Get("iss") != testIssuer || params.Get("state") != "xyz-state" {
		t.Fatalf("deny params: %v", params)
	}
}

func TestMetadataDocuments(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp", "/jwks.json"} {
		w := e.do(newGet(path))
		if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: %d", path, w.Code)
		}
		var doc map[string]any
		e.must(json.Unmarshal(w.Body.Bytes(), &doc))
		switch path {
		case "/.well-known/oauth-authorization-server":
			if doc["issuer"] != testIssuer || doc["authorization_response_iss_parameter_supported"] != true ||
				doc["client_id_metadata_document_supported"] != true {
				t.Fatalf("AS metadata: %v", doc)
			}
		case "/jwks.json":
			if keys, _ := doc["keys"].([]any); len(keys) != 1 {
				t.Fatalf("jwks: %v", doc)
			}
		default:
			if doc["resource"] != testIssuer+"/mcp" {
				t.Fatalf("PRM: %v", doc)
			}
		}
	}
	want := `Bearer resource_metadata="` + testIssuer + `/.well-known/oauth-protected-resource/mcp", scope="mcp"`
	if got := e.srv.WWWAuthenticate(); got != want {
		t.Fatalf("WWWAuthenticate %q", got)
	}
}

func TestLoginPageRendersInRequestLocale(t *testing.T) {
	e := newTestEnv(t)
	for _, tc := range []struct {
		acceptLanguage, lang, want string
	}{
		{"", `<html lang="en">`, "Sign in to mcpgw"},
		{"pt-BR", `<html lang="pt-BR">`, "Entrar no mcpgw"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/authorize?"+e.authorizeParams(newPKCE()).Encode(), nil)
		if tc.acceptLanguage != "" {
			r.Header.Set("Accept-Language", tc.acceptLanguage)
		}
		body := e.do(r).Body.String()
		if !strings.Contains(body, tc.lang) || !strings.Contains(body, tc.want) {
			t.Errorf("Accept-Language %q: want %s and %q in the login page", tc.acceptLanguage, tc.lang, tc.want)
		}
	}
}
