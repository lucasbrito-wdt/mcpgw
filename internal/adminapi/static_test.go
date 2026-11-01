package adminapi_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lucasbrito-wdt/mcpgw/internal/adminapi"
)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func directive(csp, name string) string {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, name+" ") {
			return part
		}
	}
	return ""
}

func TestAdminCSPHashes(t *testing.T) {
	inline := `self.__next_f.push([1,"x"])`
	fsys := fstest.MapFS{
		"admin/index.html":  {Data: []byte(`<html><script src="/_next/static/a.js"></script><script>` + inline + `</script></html>`)},
		"_next/static/a.js": {Data: []byte("console.log(1)")},
	}
	h, err := adminapi.StaticHandler(fsys)
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, h, "/admin/")
	csp := rec.Header().Get("Content-Security-Policy")
	if rec.Code != 200 || !strings.Contains(csp, hashOf(inline)) {
		t.Fatalf("code %d csp %q", rec.Code, csp)
	}
	if strings.Contains(csp, "unsafe-") {
		t.Fatalf("unsafe csp %q", csp)
	}
	if strings.Contains(directive(csp, "script-src"), hashOf("console.log(1)")) {
		t.Fatalf("external script must not be hashed: %q", csp)
	}
	for k, v := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
		"Cache-Control":          "no-cache",
	} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	asset := get(t, h, "/_next/static/a.js")
	if asset.Code != 200 || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset code %d cache %q", asset.Code, asset.Header().Get("Cache-Control"))
	}
}

func TestAdminCSPStyleHashes(t *testing.T) {
	style := `.a{color:red}`
	with := fstest.MapFS{"admin/index.html": {Data: []byte(`<html><style>` + style + `</style></html>`)}}
	h, err := adminapi.StaticHandler(with)
	if err != nil {
		t.Fatal(err)
	}
	got := directive(get(t, h, "/admin/").Header().Get("Content-Security-Policy"), "style-src")
	if !strings.Contains(got, hashOf(style)) {
		t.Fatalf("style-src %q", got)
	}

	without := fstest.MapFS{"admin/index.html": {Data: []byte(`<html></html>`)}}
	h, err = adminapi.StaticHandler(without)
	if err != nil {
		t.Fatal(err)
	}
	got = directive(get(t, h, "/admin/").Header().Get("Content-Security-Policy"), "style-src")
	if got != "style-src 'self'" {
		t.Fatalf("style-src %q", got)
	}
}

func TestStaticPlaceholderWhenNotBuilt(t *testing.T) {
	h, err := adminapi.StaticHandler(fstest.MapFS{".keep": {}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/admin/", "/portal/x/"} {
		rec := get(t, h, p)
		if rec.Code != 503 || !strings.Contains(rec.Body.String(), "UI não compilada, rode make admin") {
			t.Fatalf("%s: code %d body %q", p, rec.Code, rec.Body.String())
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "unsafe-") {
			t.Fatalf("csp %q", csp)
		}
	}
}

func TestStaticServesTrailingSlashRoutes(t *testing.T) {
	fsys := fstest.MapFS{
		"admin/index.html":         {Data: []byte("home")},
		"admin/servers/index.html": {Data: []byte("servers")},
		"404.html":                 {Data: []byte("custom 404")},
	}
	h, err := adminapi.StaticHandler(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(t, h, "/admin/servers/"); rec.Code != 200 || rec.Body.String() != "servers" {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}
	rec := get(t, h, "/admin/servers")
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/admin/servers/" {
		t.Fatalf("code %d loc %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get(t, h, "/admin/nope/"); rec.Code != 404 {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestStaticRejectsPathTraversal(t *testing.T) {
	fsys := fstest.MapFS{
		"admin/index.html": {Data: []byte("home")},
		"go.mod":           {Data: []byte("secret")},
	}
	h, err := adminapi.StaticHandler(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/admin/../go.mod", "/admin/%2e%2e/go.mod", "/_next/../go.mod", "/_next/static/../../go.mod", "/admin/..%5cgo.mod", "/go.mod", "/"} {
		rec := get(t, h, p)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: code %d body %q", p, rec.Code, rec.Body.String())
		}
	}
}

func rootAssetFS() fstest.MapFS {
	fsys := fstest.MapFS{
		"admin/index.html": {Data: []byte("<html></html>")},
		"404.html":         {Data: []byte("<html>nf</html>")},
		"secret.txt":       {Data: []byte("secret")},
	}
	for name := range adminapi.RootAssets {
		fsys[name] = &fstest.MapFile{Data: []byte("asset:" + name)}
	}
	return fsys
}

func TestStaticServesEachRootAssetWithMappedContentType(t *testing.T) {
	h, err := adminapi.StaticHandler(rootAssetFS())
	if err != nil {
		t.Fatal(err)
	}
	for name, ct := range adminapi.RootAssets {
		rec := get(t, h, "/"+name)
		if rec.Code != 200 {
			t.Errorf("%s: code %d", name, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Type"); got != ct {
			t.Errorf("%s: content type %q, want %q", name, got, ct)
		}
		if rec.Body.String() != "asset:"+name {
			t.Errorf("%s: body %q", name, rec.Body.String())
		}
	}
}

func TestStaticRootAssetsAreCachedForADay(t *testing.T) {
	h, _ := adminapi.StaticHandler(rootAssetFS())
	if got := get(t, h, "/icon.svg").Header().Get("Cache-Control"); got != "public, max-age=86400" {
		t.Fatalf("cache-control %q", got)
	}
}

func TestStaticDoesNotServeUnlistedRootFiles(t *testing.T) {
	h, _ := adminapi.StaticHandler(rootAssetFS())
	for _, p := range []string{"/secret.txt", "/404.html"} {
		rec := get(t, h, p)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: code %d body %q", p, rec.Code, rec.Body.String())
		}
	}
}

func TestStaticRootAssetMissingFromFSIs404(t *testing.T) {
	fsys := rootAssetFS()
	delete(fsys, "icon.svg")
	h, _ := adminapi.StaticHandler(fsys)
	if rec := get(t, h, "/icon.svg"); rec.Code != 404 {
		t.Fatalf("code %d", rec.Code)
	}
}
