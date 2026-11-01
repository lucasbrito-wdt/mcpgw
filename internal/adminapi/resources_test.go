package adminapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
)

const secretValue = "s3cr3t-value"

func (h *harness) put(c *http.Client, path string, body any) *http.Response {
	return h.do(c, http.MethodPut, path, body, reqOpt{})
}

func (h *harness) del(c *http.Client, path string, body any) *http.Response {
	return h.do(c, http.MethodDelete, path, body, reqOpt{})
}

func (h *harness) reauth(c *http.Client) {
	h.t.Helper()
	if res := h.post(c, "/admin/api/session/reauth", map[string]string{"password": testPassword}); res.StatusCode != 204 {
		h.t.Fatalf("reauth: %d", res.StatusCode)
	}
}

// addServer creates an https server through the API; header servers get Authorization/"Bearer ".
func (h *harness) addServer(c *http.Client, slug, authKind string) {
	h.t.Helper()
	body := map[string]any{"slug": slug, "url": "https://" + slug + ".example.test/mcp", "auth_kind": authKind, "transport": "auto"}
	if authKind == "header" {
		body["header"] = map[string]string{"name": "Authorization", "prefix": "Bearer "}
	}
	if res := h.post(c, "/admin/api/servers", body); res.StatusCode != 201 {
		h.t.Fatalf("add server: %d %s", res.StatusCode, readAll(res))
	}
}

func (h *harness) setShared(c *http.Client, slug string) {
	h.t.Helper()
	h.reauth(c)
	if res := h.put(c, "/admin/api/servers/"+slug+"/header", map[string]string{"value": secretValue}); res.StatusCode != 204 {
		h.t.Fatalf("set shared: %d", res.StatusCode)
	}
}

func (h *harness) seedTool(slug, name, status, hash string) {
	h.t.Helper()
	h.exec(`INSERT INTO tools (server_id, name, schema_hash, trust_level, status, pending_changes)
		VALUES ((SELECT id FROM upstream_servers WHERE slug = $1), $2, $3, 'read', $4, '{schema}')`, slug, name, hash, status)
}

func decodeBody[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

type connsBody struct {
	Servers []struct {
		Shared *struct {
			Status string `json:"status"`
			Last4  string `json:"last4"`
		} `json:"shared"`
	} `json:"servers"`
}

type listBody struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

func TestSecretWriteRequiresReauth(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "linear", "header")
	res := h.put(c, "/admin/api/servers/linear/header", map[string]string{"value": secretValue})
	if res.StatusCode != 403 || !strings.Contains(readAll(res), `"reauth_required"`) {
		t.Fatalf("status %d", res.StatusCode)
	}
	h.reauth(c)
	if res := h.put(c, "/admin/api/servers/linear/header", map[string]string{"value": secretValue}); res.StatusCode != 204 {
		t.Fatalf("after reauth: %d", res.StatusCode)
	}
	h.clock.Advance(6 * time.Minute)
	if res := h.put(c, "/admin/api/servers/linear/header", map[string]string{"value": "x"}); res.StatusCode != 403 {
		t.Fatal("reauth did not expire")
	}
}

func TestOAuthClientSecretRequiresReauth(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "notion", "oauth")
	if res := h.put(c, "/admin/api/servers/notion/oauth-client", map[string]string{"client_id": "abc", "client_secret": "shh"}); res.StatusCode != 403 {
		t.Fatalf("secret without reauth: %d", res.StatusCode)
	}
	h.reauth(c)
	if res := h.put(c, "/admin/api/servers/notion/oauth-client", map[string]string{"client_id": "abc", "client_secret": "shh"}); res.StatusCode != 204 {
		t.Fatalf("secret after reauth: %d", res.StatusCode)
	}
}

func TestSecretNeverEchoed(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "linear", "header")
	h.setShared(c, "linear")
	for _, path := range []string{"/admin/api/servers/linear", "/admin/api/servers", "/admin/api/admin-events", "/admin/api/connections"} {
		if body := readAll(h.get(c, path)); strings.Contains(body, secretValue) {
			t.Fatalf("%s echoes the secret", path)
		}
	}
	if n := h.count(`SELECT count(*) FROM admin_events WHERE details::text LIKE '%' || $1 || '%'`, secretValue); n != 0 {
		t.Fatal("secret in admin_events")
	}
	srv := decodeBody[map[string]any](t, h.get(c, "/admin/api/servers/linear"))
	if srv["has_shared_connection"] != true {
		t.Fatalf("has_shared_connection: %v", srv["has_shared_connection"])
	}
	var userNull bool
	var by string
	if err := h.row(`SELECT user_id IS NULL, connected_by::text FROM upstream_credentials WHERE shared`).Scan(&userNull, &by); err != nil {
		t.Fatal(err)
	}
	if !userNull || by != h.adminID {
		t.Fatalf("shared row: userNull=%v by=%s", userNull, by)
	}
}

func TestWebCannotAddHostStdio(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	res := h.post(c, "/admin/api/servers", map[string]any{"slug": "fs", "transport": "stdio", "stdio_launcher": "host", "stdio_command": "/bin/sh", "auth_kind": "none"})
	if res.StatusCode != 422 || !strings.Contains(readAll(res), `"stdio_launcher"`) {
		t.Fatalf("status %d", res.StatusCode)
	}
	if n := h.count(`SELECT count(*) FROM upstream_servers`); n != 0 {
		t.Fatal("server created")
	}
}

func TestAddDockerStdioServer(t *testing.T) {
	h := newHarness(t)
	c := h.loginReauthed(h.adminEmail)
	res := h.post(c, "/admin/api/servers", map[string]any{"slug": "fs", "transport": "stdio", "stdio_launcher": "docker",
		"stdio_image": "ghcr.io/example/fs:1", "stdio_command": "serve", "stdio_args": []string{"--ro"}, "stdio_network": false, "auth_kind": "none"})
	if res.StatusCode != 201 {
		t.Fatalf("status %d %s", res.StatusCode, readAll(res))
	}
	if n := h.count(`SELECT count(*) FROM upstream_servers WHERE slug = 'fs' AND docker_image = 'ghcr.io/example/fs:1'`); n != 1 {
		t.Fatal("docker stdio not stored")
	}
}

func TestApproveStaleHashConflict(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "github", "none")
	h.seedTool("github", "create_issue", "pending_approval", strings.Repeat("a", 64))
	res := h.post(c, "/admin/api/tools/github/create_issue/approve", map[string]string{"hash": "bbbbbbbbbbbb"})
	if res.StatusCode != 409 || !strings.Contains(readAll(res), `"current_hash":"aaaaaaaaaaaa`) {
		t.Fatalf("status %d", res.StatusCode)
	}
	if n := h.count(`SELECT count(*) FROM tools WHERE status = 'pending_approval'`); n != 1 {
		t.Fatal("tool changed status")
	}
	if h.count(`SELECT count(*) FROM admin_events WHERE action LIKE 'tool.%'`) != 0 {
		t.Fatal("audit row written")
	}
	if res := h.post(c, "/admin/api/tools/github/create_issue/approve", map[string]string{"hash": "zz"}); res.StatusCode != 422 {
		t.Fatalf("bad hash: %d", res.StatusCode)
	}
	if res := h.post(c, "/admin/api/tools/github/create_issue/approve", map[string]string{"hash": "aaaaaaaaaaaa"}); res.StatusCode != 200 {
		t.Fatalf("approve: %d", res.StatusCode)
	}
}

func TestToolsLevelAndApproveAll(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "github", "none")
	h.seedTool("github", "a", "pending_approval", strings.Repeat("a", 64))
	h.seedTool("github", "b", "active", strings.Repeat("b", 64))
	list := decodeBody[listBody](t, h.get(c, "/admin/api/tools?server=github&status=pending_approval"))
	if len(list.Items) != 1 || list.Items[0]["name"] != "a" || list.Items[0]["server"] != "github" {
		t.Fatalf("%+v", list)
	}
	detail := decodeBody[map[string]any](t, h.get(c, "/admin/api/tools/github/b"))
	if _, ok := detail["input_schema"].(map[string]any); !ok {
		t.Fatalf("input_schema not an object: %v", detail["input_schema"])
	}
	if res := h.patch(c, "/admin/api/tools/github/b", map[string]string{"level": "destructive"}); res.StatusCode != 204 {
		t.Fatalf("level: %d", res.StatusCode)
	}
	if res := h.patch(c, "/admin/api/tools/github/b", map[string]string{"level": "none"}); res.StatusCode != 422 {
		t.Fatalf("level none: %d", res.StatusCode)
	}
	res := h.post(c, "/admin/api/servers/github/approve-all", nil)
	if body := readAll(res); res.StatusCode != 200 || !strings.Contains(body, `"approved":["a"]`) {
		t.Fatalf("approve-all: %d %s", res.StatusCode, body)
	}
}

func TestServerPatchAudited(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "github", "none")
	if res := h.patch(c, "/admin/api/servers/github", map[string]any{"status": "disabled"}); res.StatusCode != 200 {
		t.Fatalf("status %d %s", res.StatusCode, readAll(res))
	}
	var target string
	if err := h.row(`SELECT target FROM admin_events WHERE action = 'server.set_status'`).Scan(&target); err != nil || target != "github" {
		t.Fatalf("audit: %q %v", target, err)
	}
	res := h.patch(c, "/admin/api/servers/github", map[string]any{"trust_annotations": true, "category": "dev"})
	got := decodeBody[map[string]any](t, res)
	if got["status"] != "disabled" || got["trust_annotations"] != true || got["category"] != "dev" || got["reclassified"] == nil {
		t.Fatalf("%v", got)
	}
	if res := h.patch(c, "/admin/api/servers/github", map[string]any{}); res.StatusCode != 422 {
		t.Fatalf("empty patch: %d", res.StatusCode)
	}
}

func TestPrefixAlreadyInSharedValue(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "linear", "header")
	h.reauth(c)
	if res := h.put(c, "/admin/api/servers/linear/header", map[string]string{"value": "lin_tok-1234"}); res.StatusCode != 204 {
		t.Fatalf("set shared: %d", res.StatusCode)
	}
	res := h.patch(c, "/admin/api/servers/linear", map[string]any{"header": map[string]string{"name": "Authorization", "prefix": "lin_"}})
	body := readAll(res)
	if res.StatusCode != 422 || !strings.Contains(body, `"prefix_already_in_value"`) || !strings.Contains(body, `"header_prefix"`) {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
}

func TestListsArePaged(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	for _, slug := range []string{"aa", "bb", "cc"} {
		h.addServer(c, slug, "none")
	}
	first := decodeBody[listBody](t, h.get(c, "/admin/api/servers?limit=2"))
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	second := decodeBody[listBody](t, h.get(c, "/admin/api/servers?limit=2&cursor="+url.QueryEscape(first.NextCursor)))
	if len(second.Items) != 1 || second.NextCursor != "" || second.Items[0]["slug"] != "cc" {
		t.Fatalf("second page: %+v", second)
	}
	if res := h.get(c, "/admin/api/servers?cursor=!!"); res.StatusCode != 422 {
		t.Fatalf("bad cursor: %d", res.StatusCode)
	}
}

func TestSharedConnectionRoutes(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "linear", "header")
	h.setShared(c, "linear")
	conns := decodeBody[connsBody](t, h.get(c, "/admin/api/connections"))
	if len(conns.Servers) != 1 || conns.Servers[0].Shared == nil || conns.Servers[0].Shared.Last4 != "alue" {
		t.Fatalf("%+v", conns)
	}
	if res := h.del(c, "/admin/api/connections/linear/shared", map[string]string{"reason": "Conta trocada"}); res.StatusCode != 204 {
		t.Fatalf("revoke shared: %d %s", res.StatusCode, readAll(res))
	}
	conns2 := decodeBody[connsBody](t, h.get(c, "/admin/api/connections"))
	if conns2.Servers[0].Shared.Status != "revoked_by_admin" {
		t.Fatalf("%+v", conns2)
	}
	if res := h.del(c, "/admin/api/connections/linear/"+h.userID, map[string]string{"reason": ""}); res.StatusCode != 422 {
		t.Fatalf("empty reason: %d", res.StatusCode)
	}
	if res := h.del(c, "/admin/api/servers/linear/shared", nil); res.StatusCode != 204 {
		t.Fatalf("remove shared: %d", res.StatusCode)
	}
	if res := h.del(c, "/admin/api/servers/linear/shared", nil); res.StatusCode != 404 {
		t.Fatalf("remove again: %d", res.StatusCode)
	}
	if srv := decodeBody[map[string]any](t, h.get(c, "/admin/api/servers/linear")); srv["has_shared_connection"] != false {
		t.Fatalf("%v", srv["has_shared_connection"])
	}
	for _, action := range []string{"connection.set_shared", "connection.revoke", "connection.remove_shared"} {
		if n := h.count(`SELECT count(*) FROM admin_events WHERE action = $1`, action); n != 1 {
			t.Fatalf("%s: %d rows", action, n)
		}
	}
}

func TestAddServerHeaderAliasAndCategory(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	body := map[string]any{"slug": "gh", "url": "https://gh.example.test/mcp", "auth_kind": "static_header", "category": "dev",
		"header": map[string]string{"name": "Authorization", "prefix": "Bearer ", "format_hint": "ghp_", "help_url": "https://example.test/tokens"}}
	res := h.post(c, "/admin/api/servers", body)
	got := decodeBody[map[string]any](t, res)
	if res.StatusCode != 201 || got["auth_kind"] != "header" || got["category"] != "dev" {
		t.Fatalf("%d %v", res.StatusCode, got)
	}
	detail := decodeBody[struct {
		Header map[string]string `json:"header"`
	}](t, h.get(c, "/admin/api/servers/gh"))
	if detail.Header["format_hint"] != "ghp_" || detail.Header["prefix"] != "Bearer " {
		t.Fatalf("%v", detail.Header)
	}
	body["slug"], body["category"] = "gh2", "Dev!"
	res = h.post(c, "/admin/api/servers", body)
	if res.StatusCode != 422 || !strings.Contains(readAll(res), `"category"`) {
		t.Fatalf("bad category: %d", res.StatusCode)
	}
}

func TestSyncRoutes(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "notion", "oauth")
	res := h.post(c, "/admin/api/servers/notion/sync", nil)
	if res.StatusCode != 422 || !strings.Contains(readAll(res), `"as"`) {
		t.Fatalf("oauth sync: %d", res.StatusCode)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "up", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "get_issue", Description: "read an issue", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	up := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(up.Close)
	if res := h.post(c, "/admin/api/servers", map[string]any{"slug": "plain", "url": up.URL, "auth_kind": "none"}); res.StatusCode != 201 {
		t.Fatalf("add: %d", res.StatusCode)
	}
	res = h.post(c, "/admin/api/servers/plain/sync", nil)
	report := decodeBody[map[string]any](t, res)
	added, _ := report["added"].([]any)
	if res.StatusCode != 200 || len(added) != 1 || added[0] != "get_issue" || report["changed"] == nil || report["quarantined"] == nil {
		t.Fatalf("%d %v", res.StatusCode, report)
	}
}

func TestCeilingsConsentsAndActivity(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "github", "none")
	if res := h.put(c, "/admin/api/ceilings/"+h.userID+"/github", map[string]string{"level": "write"}); res.StatusCode != 204 {
		t.Fatalf("set ceiling: %d", res.StatusCode)
	}
	list := decodeBody[listBody](t, h.get(c, "/admin/api/ceilings?user="+h.userID))
	if len(list.Items) != 1 || list.Items[0]["server"] != "github" || list.Items[0]["level"] != "write" || list.Items[0]["email"] != h.userEmail {
		t.Fatalf("%+v", list)
	}
	if res := h.put(c, "/admin/api/ceilings/"+h.userID+"/github", map[string]string{"level": "none"}); res.StatusCode != 204 {
		t.Fatalf("clear ceiling: %d", res.StatusCode)
	}
	for _, path := range []string{"/admin/api/clients", "/admin/api/consents", "/admin/api/grants", "/admin/api/executions", "/admin/api/admin-events?actor=" + h.adminID} {
		if res := h.get(c, path); res.StatusCode != 200 {
			t.Fatalf("%s: %d", path, res.StatusCode)
		}
	}
	if res := h.get(c, "/admin/api/executions?from=yesterday"); res.StatusCode != 422 || !strings.Contains(readAll(res), `"from"`) {
		t.Fatal("bad from not rejected")
	}
	if ov := decodeBody[map[string]int](t, h.get(c, "/admin/api/overview")); ov["servers"] != 1 {
		t.Fatalf("%v", ov)
	}
	if h.del(c, "/admin/api/consents/not-a-uuid", nil).StatusCode != 404 {
		t.Fatal("unknown consent not 404")
	}
}

func TestResourceRoutesRequireAdmin(t *testing.T) {
	h := newHarness(t)
	routes := map[string][]string{
		http.MethodGet:    {"/servers", "/servers/x", "/tools", "/tools/x/y", "/ceilings", "/clients", "/consents", "/grants", "/executions", "/executions/x", "/admin-events", "/overview", "/connections"},
		http.MethodPost:   {"/servers", "/servers/x/sync", "/servers/x/approve-all", "/tools/x/y/approve"},
		http.MethodPatch:  {"/servers/x", "/tools/x/y"},
		http.MethodPut:    {"/servers/x/header", "/servers/x/oauth-client", "/ceilings/u/x"},
		http.MethodDelete: {"/servers/x/shared", "/consents/x", "/grants/x", "/connections/x/shared", "/connections/x/u"},
	}
	for method, paths := range routes {
		for _, p := range paths {
			if res := h.do(nil, method, "/admin/api"+p, map[string]string{}, reqOpt{}); res.StatusCode != 401 {
				t.Errorf("%s %s anonymous: %d", method, p, res.StatusCode)
			}
		}
	}
	user := h.login(h.adminEmail)
	h.exec(`UPDATE users SET is_admin = false WHERE id = $1`, h.adminID)
	h.createUser("second@example.com", true)
	if res := h.get(user, "/admin/api/servers"); res.StatusCode != 403 {
		t.Fatalf("demoted: %d", res.StatusCode)
	}
}

var hiddenField = regexp.MustCompile(`name="(csrf_token|request)" value="([^"]*)"`)

// accessToken drives the real authorize -> consent -> token flow against h.oauth.
func (h *harness) accessToken() string {
	h.t.Helper()
	h.exec(`INSERT INTO oauth_clients (client_id, registration_kind, redirect_uris, metadata)
		VALUES ('test-client', 'dcr', '{http://127.0.0.1/cb}', '{"client_id":"test-client","client_name":"Test","redirect_uris":["http://127.0.0.1/cb"]}')`)
	h.exec(`INSERT INTO trust_ceilings (user_id, server_id, max_level) SELECT $1::uuid, id, 'read' FROM upstream_servers WHERE slug = 'github'`, h.userID)
	mux := http.NewServeMux()
	h.oauth.Routes(mux)
	serve := func(r *http.Request, cookies []*http.Cookie) *httptest.ResponseRecorder {
		for _, ck := range cookies {
			r.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	sw := httptest.NewRecorder()
	if err := h.oauth.StartSession(sw, h.userID); err != nil {
		h.t.Fatal(err)
	}
	cookies := sw.Result().Cookies()
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	resource := publicURL + "/mcp"
	q := url.Values{"response_type": {"code"}, "client_id": {"test-client"}, "redirect_uri": {"http://127.0.0.1/cb"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"st"}, "resource": {resource}, "scope": {oauth.ScopeMCP}}
	page := serve(httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil), cookies)
	cookies = append(cookies, page.Result().Cookies()...)
	form := url.Values{"action": {"consent"}}
	for _, m := range hiddenField.FindAllStringSubmatch(page.Body.String(), -1) {
		form.Set(m[1], m[2])
	}
	var serverID string
	if err := h.row(`SELECT id::text FROM upstream_servers WHERE slug = 'github'`).Scan(&serverID); err != nil {
		h.t.Fatal(err)
	}
	form.Set("level_"+serverID, "read")
	post := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	redir := serve(post, cookies)
	loc, err := url.Parse(redir.Header().Get("Location"))
	if redir.Code != http.StatusSeeOther || err != nil || loc.Query().Get("code") == "" {
		h.t.Fatalf("consent: %d %s", redir.Code, redir.Body.String())
	}
	tok := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier},
		"client_id": {"test-client"}, "redirect_uri": {"http://127.0.0.1/cb"}, "resource": {resource}}
	tr := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tok.Encode()))
	tr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := serve(tr, nil)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.AccessToken == "" {
		h.t.Fatalf("token: %d %s", w.Code, w.Body.String())
	}
	return resp.AccessToken
}

func TestRevokeGrantImmediate(t *testing.T) {
	h := newHarness(t)
	c := h.login(h.adminEmail)
	h.addServer(c, "github", "none")
	access := h.accessToken()
	ctx := t.Context()
	if _, err := h.oauth.ValidateAccessToken(ctx, access); err != nil {
		t.Fatalf("token must be valid first: %v", err)
	}
	grants := decodeBody[listBody](t, h.get(c, "/admin/api/grants?user="+h.userID))
	if len(grants.Items) != 1 {
		t.Fatalf("%+v", grants)
	}
	if res := h.del(c, "/admin/api/grants/"+grants.Items[0]["family_id"].(string), nil); res.StatusCode != 204 {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if _, err := h.oauth.ValidateAccessToken(ctx, access); !errors.Is(err, oauth.ErrInvalidToken) {
		t.Fatalf("still valid: %v", err)
	}
	consents := decodeBody[listBody](t, h.get(c, "/admin/api/consents?user="+h.userID))
	if len(consents.Items) != 1 {
		t.Fatalf("%+v", consents)
	}
	if res := h.del(c, "/admin/api/consents/"+consents.Items[0]["id"].(string), nil); res.StatusCode != 204 {
		t.Fatalf("revoke consent: %d", res.StatusCode)
	}
}
