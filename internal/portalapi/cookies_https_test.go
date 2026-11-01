package portalapi_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/portalapi"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const httpsPublicURL = "https://gw.example"

// httpsAPI builds the portal API behind an https PublicURL and returns its handler and the fake clock.
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
	api, err := portalapi.New(portalapi.Options{
		Pool: pool, PublicURL: httpsPublicURL, Sealer: v, OAuth: o,
		Credentials: mgr, Lister: fakeUpstream{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := oauth.HashPassword("a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (email, password_hash, is_admin) VALUES ('a@example.com', $1, false)`, hash); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Routes(mux)
	return mux, clock
}

func serve(handler http.Handler, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", httpsPublicURL)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertHardenedPortalCookies(t *testing.T, step string, rec *httptest.ResponseRecorder) {
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
		case c.Name == "__Host-mcpgw_session" && !c.HttpOnly:
			t.Errorf("%s: session cookie is not HttpOnly", step)
		}
	}
}

func TestCookiesUseHostPrefixOnHTTPS(t *testing.T) {
	handler, clock := httpsAPI(t)
	creds := `{"email":"a@example.com","password":"a long enough password"}`

	login := serve(handler, http.MethodPost, "/portal/api/session", creds, nil)
	if login.Code != http.StatusNoContent {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	assertHardenedPortalCookies(t, "login", login)
	session := login.Result().Cookies()

	me := serve(handler, http.MethodGet, "/portal/api/me", "", session)
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}
	assertHardenedPortalCookies(t, "me (csrf cookie)", me)

	clock.Advance(2 * time.Minute) // past the touch granularity so the session is re-sealed
	slid := serve(handler, http.MethodGet, "/portal/api/me", "", session)
	if slid.Code != http.StatusOK {
		t.Fatalf("slide: %d %s", slid.Code, slid.Body)
	}
	assertHardenedPortalCookies(t, "slide on /me", slid)
	var sawSession bool
	for _, c := range slid.Result().Cookies() {
		sawSession = sawSession || c.Name == "__Host-mcpgw_session"
	}
	if !sawSession {
		t.Error("slide did not re-issue the session cookie")
	}

	logout := serve(handler, http.MethodDelete, "/portal/api/session", "", session)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body)
	}
	assertHardenedPortalCookies(t, "logout", logout)
}
