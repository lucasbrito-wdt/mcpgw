package adminapi_test

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/adminapi"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const httpsPublicURL = "https://gw.example"

// httpsAPI builds the admin API behind an https PublicURL and returns its handler and the fake clock.
func httpsAPI(t *testing.T) (http.Handler, *fakeClock) {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	v, err := vault.New(key, "k1")
	if err != nil {
		t.Fatal(err)
	}
	pool := storetest.New(t)
	clock := &fakeClock{now: time.Now()}
	o, err := oauth.New(context.Background(), oauth.Options{PublicURL: httpsPublicURL, Pool: pool, Sealer: v, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := upstreamauth.New(upstreamauth.Options{
		Pool: pool, Vault: v, PublicURL: httpsPublicURL,
		User: func(*http.Request) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := adminapi.New(adminapi.Options{
		Pool: pool, PublicURL: httpsPublicURL, Sealer: v, Login: o.Login, Now: clock.Now,
		Admin: admin.Options{
			Pool:        pool,
			Credentials: func() (*upstreamauth.Manager, error) { return mgr, nil },
			Lister:      upstream.New(),
			Grants:      o,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := oauth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (email, password_hash, is_admin) VALUES ('admin@example.com', $1, true)`, hash); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Routes(mux)
	return mux, clock
}

func serve(handler http.Handler, method, path, body string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", httpsPublicURL)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertHardenedAdminCookies(t *testing.T, step string, rec *httptest.ResponseRecorder) {
	t.Helper()
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("%s: no Set-Cookie", step)
	}
	for _, c := range cookies {
		switch {
		case !strings.HasPrefix(c.Name, "__Host-"):
			t.Errorf("%s: cookie %q lacks __Host- prefix", step, c.Name)
		case !c.Secure:
			t.Errorf("%s: cookie %q is not Secure", step, c.Name)
		case c.Path != "/":
			t.Errorf("%s: cookie %q Path = %q", step, c.Name, c.Path)
		case c.Domain != "":
			t.Errorf("%s: cookie %q sets Domain=%q", step, c.Name, c.Domain)
		case c.SameSite != http.SameSiteStrictMode:
			t.Errorf("%s: cookie %q SameSite = %v, want Strict", step, c.Name, c.SameSite)
		case c.Name == "__Host-mcpgw_admin" && !c.HttpOnly:
			t.Errorf("%s: session cookie is not HttpOnly", step)
		}
	}
}

func TestCookiesUseHostPrefixOnHTTPS(t *testing.T) {
	handler, clock := httpsAPI(t)
	creds := `{"email":"admin@example.com","password":"` + testPassword + `"}`

	login := serve(handler, http.MethodPost, "/admin/api/session", creds, nil, "")
	if login.Code != http.StatusNoContent {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	assertHardenedAdminCookies(t, "login", login)
	session := login.Result().Cookies()
	var csrf string
	for _, c := range session {
		if c.Name == "__Host-mcpgw_admin_csrf" {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("login did not set the __Host- csrf cookie")
	}

	clock.Advance(2 * time.Minute) // past touchGranularity so the session is re-sealed
	me := serve(handler, http.MethodGet, "/admin/api/me", "", session, "")
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}
	assertHardenedAdminCookies(t, "slide on /me", me)

	logout := serve(handler, http.MethodDelete, "/admin/api/session", "", session, csrf)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body)
	}
	assertHardenedAdminCookies(t, "logout", logout)
}
