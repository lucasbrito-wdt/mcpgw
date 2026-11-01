package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestValidateRedirectURI(t *testing.T) {
	cases := []struct {
		uri, appType string
		ok           bool
	}{
		{"https://app.example.com/cb", appTypeWeb, true},
		{"https://localhost/cb", appTypeWeb, false},
		{"http://127.0.0.1:3000/cb", appTypeNative, true},
		{"http://[::1]/cb", appTypeNative, true},
		{"http://localhost:8080/cb", appTypeNative, true},
		{"http://127.0.0.1/cb", appTypeWeb, false},
		{"http://app.example.com/cb", appTypeNative, false},
		{"cursor://anysphere.cursor-mcp/oauth/callback", appTypeNative, true},
		{"cursor://anysphere.cursor-mcp/oauth/callback", appTypeWeb, false},
		{"javascript:alert(1)", appTypeNative, false},
		{"data:text/html,x", appTypeNative, false},
		{"https://app.example.com/cb#frag", appTypeWeb, false},
		{"https://user@app.example.com/cb", appTypeWeb, false},
		{"/relative", appTypeWeb, false},
	}
	for _, tc := range cases {
		if err := validateRedirectURI(tc.uri, tc.appType); (err == nil) != tc.ok {
			t.Errorf("validateRedirectURI(%q, %s) err=%v, want ok=%v", tc.uri, tc.appType, err, tc.ok)
		}
	}
}

func TestMatchRedirect(t *testing.T) {
	c := &Client{Metadata: ClientMetadata{RedirectURIs: []string{
		"http://127.0.0.1/callback", "https://app.example.com/cb", "http://localhost:3000/cb?x=1",
	}}}
	cases := map[string]bool{
		"http://127.0.0.1/callback":              true,
		"http://127.0.0.1:61234/callback":        true,
		"http://127.0.0.1:61234/callback/":       false,
		"http://127.0.0.1:99999/callback":        false,
		"http://localhost:61234/callback":        false,
		"http://localhost:4000/cb?x=1":           true,
		"http://localhost:4000/cb?x=2":           false,
		"https://app.example.com/cb":             true,
		"https://app.example.com:8443/cb":        false,
		"https://app.example.com/cb?extra":       false,
		"http://127.0.0.1:1@evil/callback":       false,
		"http://127.0.0.1:5/callback#frag":       false,
		"https://127.0.0.1:61234/callback":       false,
		"http://127.0.0.1.evil.example/callback": false,
	}
	for uri, want := range cases {
		if got := c.matchRedirect(uri); got != want {
			t.Errorf("matchRedirect(%q) = %v, want %v", uri, got, want)
		}
	}
}

func (e *testEnv) register(body string) *httptest.ResponseRecorder {
	return e.registerFrom("192.0.2.1", body)
}

// registerFrom sets the peer address so validation cases do not hit the per-IP rate limit.
func (e *testEnv) registerFrom(ip, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = ip + ":40000"
	return e.do(r)
}

func TestDynamicClientRegistration(t *testing.T) {
	e := newTestEnv(t)
	w := e.register(`{"client_name":"Cursor","redirect_uris":["cursor://anysphere.cursor-mcp/oauth/callback"],
		"grant_types":["authorization_code","refresh_token"],"token_endpoint_auth_method":"none"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	e.must(json.Unmarshal(w.Body.Bytes(), &resp))
	clientID, _ := resp["client_id"].(string)
	if !strings.HasPrefix(clientID, "mcpgw-") || resp["application_type"] != appTypeNative || resp["client_secret"] != nil {
		t.Fatalf("registration response: %v", resp)
	}

	// The registered client can start an authorization with its private-use redirect.
	q := e.authorizeParams(newPKCE())
	q.Set("client_id", clientID)
	q.Set("redirect_uri", "cursor://anysphere.cursor-mcp/oauth/callback")
	page := e.getAuthorize(q, e.sessionCookie())
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "dynamic registration") {
		t.Fatalf("authorize with DCR client: %d", page.Code)
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' cursor:") {
		t.Fatalf("CSP: %s", csp)
	}

	rejected := map[string]string{
		"confidential":      `{"redirect_uris":["https://a.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		"web with loopback": `{"redirect_uris":["http://127.0.0.1/cb"],"application_type":"web"}`,
		"no redirects":      `{"client_name":"x"}`,
		"implicit":          `{"redirect_uris":["https://a.example/cb"],"response_types":["token"]}`,
		"bad app type":      `{"redirect_uris":["https://a.example/cb"],"application_type":"daemon"}`,
		"malformed":         `{"redirect_uris":`,
	}
	for name, body := range rejected {
		if w := e.registerFrom("198.51.100."+strconv.Itoa(len(name)), body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", name, w.Code, w.Body.String())
		}
	}
	if code := oauthErrorCode(t, e.registerFrom("203.0.113.9", `{"redirect_uris":["http://evil.example/cb"]}`)); code != "invalid_redirect_uri" {
		t.Errorf("error code %q", code)
	}
}

func TestRegistrationRateLimited(t *testing.T) {
	e := newTestEnv(t)
	body := `{"redirect_uris":["http://127.0.0.1/cb"]}`
	var last int
	for range 10 {
		last = e.register(body).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("status %d after burst", last)
	}
}

func TestDCRClientWithoutRefreshGrant(t *testing.T) {
	e := newTestEnv(t)
	w := e.register(`{"client_name":"No refresh","redirect_uris":["http://127.0.0.1/callback"],"grant_types":["authorization_code"]}`)
	var resp map[string]any
	e.must(json.Unmarshal(w.Body.Bytes(), &resp))
	e.clientID = resp["client_id"].(string)
	p := newPKCE()
	tokens := decodeTokens(t, e.exchange(e.authorizeCode(p), p.verifier, callback))
	if tokens.RefreshToken != "" {
		t.Fatal("refresh token issued to a client without the refresh_token grant")
	}
}
