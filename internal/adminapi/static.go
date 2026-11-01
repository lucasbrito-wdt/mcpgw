// Package adminapi serves the admin web export and (elsewhere) the admin API.
package adminapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	scriptBlock = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	styleBlock  = regexp.MustCompile(`(?is)<style\b[^>]*>(.*?)</style>`)
	srcAttr     = regexp.MustCompile(`(?i)\bsrc\s*=`)
)

const (
	placeholderCSP  = "default-src 'none'"
	placeholderPage = `<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><title>MCP Gateway</title></head><body><p>UI não compilada, rode make admin</p></body></html>`
	immutableCache  = "public, max-age=31536000, immutable"
	rootAssetCache  = "public, max-age=86400"
)

// RootAssets are the brand files the export ships at its root; they are the only root-level
// paths the handler serves, with the content type forced because the OS mime table varies.
var RootAssets = map[string]string{
	"favicon.ico":          "image/x-icon",
	"icon.svg":             "image/svg+xml",
	"apple-icon.png":       "image/png",
	"manifest.webmanifest": "application/manifest+json",
	// the PWA icons the manifest lists
	"icon-192.png":          "image/png",
	"icon-512.png":          "image/png",
	"icon-maskable-512.png": "image/png",
}

// StaticHandler serves the embedded web export under /admin/, /portal/ and /_next/. The CSP is
// computed once: script-src lists the sha256 of every inline <script> in every exported page,
// style-src the sha256 of every inline <style> block (G26). When admin/index.html is absent
// (UI not built) /admin/* and /portal/* answer 503 with a PT-BR placeholder page.
func StaticHandler(fsys fs.FS) (http.Handler, error) {
	scripts, err := inlineHashes(fsys, scriptBlock)
	if err != nil {
		return nil, err
	}
	styles, err := inlineHashes(fsys, styleBlock)
	if err != nil {
		return nil, err
	}
	_, statErr := fs.Stat(fsys, "admin/index.html")
	return &staticHandler{fsys: fsys, csp: buildCSP(scripts, styles), built: statErr == nil}, nil
}

type staticHandler struct {
	fsys  fs.FS
	csp   string
	built bool
}

func (s *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy", s.csp)

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root, name, ok := resolve(r.URL.Path)
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.built && root != "_next" && root != "" {
		h.Set("Content-Security-Policy", placeholderCSP)
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(placeholderPage))
		}
		return
	}
	if info, err := fs.Stat(s.fsys, name); err == nil && info.IsDir() {
		if _, err := fs.Stat(s.fsys, name+"/index.html"); err == nil && !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, redirectTarget(r), http.StatusPermanentRedirect)
			return
		}
		name += "/index.html"
	}
	s.serveFile(w, r, name)
}

// resolve maps a URL path to a clean fs name. It rejects traversal, empty or dot segments, and
// anything outside the three served roots.
func resolve(urlPath string) (root, name string, ok bool) {
	if !strings.HasPrefix(urlPath, "/") || strings.ContainsAny(urlPath, "\\\x00") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(urlPath, "/")
	dir := strings.HasSuffix(trimmed, "/")
	segments := strings.Split(strings.TrimSuffix(trimmed, "/"), "/")
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			return "", "", false
		}
	}
	root = segments[0]
	if _, asset := RootAssets[trimmed]; asset {
		return "", trimmed, true
	}
	if root != "admin" && root != "portal" && root != "_next" {
		return "", "", false
	}
	name = path.Join(segments...)
	if dir {
		name += "/index.html"
	}
	return root, name, fs.ValidPath(name)
}

func redirectTarget(r *http.Request) string {
	target := r.URL.Path + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target
}

func (s *staticHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		s.notFound(w, r)
		return
	}
	cache := "no-cache"
	if strings.HasPrefix(name, "_next/static/") {
		cache = immutableCache
	}
	if ct, ok := RootAssets[name]; ok {
		cache = rootAssetCache
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", cache)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (s *staticHandler) notFound(w http.ResponseWriter, r *http.Request) {
	page, err := fs.ReadFile(s.fsys, "404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusNotFound)
	if r.Method == http.MethodGet {
		_, _ = w.Write(page)
	}
}

func buildCSP(scripts, styles []string) string {
	return strings.Join([]string{
		"default-src 'self'",
		join("script-src 'self'", scripts),
		join("style-src 'self'", styles),
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'self'",
	}, "; ")
}

func join(prefix string, hashes []string) string {
	return strings.TrimSpace(prefix + " " + strings.Join(hashes, " "))
}

// inlineHashes returns the sorted, de-duplicated CSP sha256 sources of every inline block that re
// matches in the exported HTML pages. The last capture group is the block body; when a leading
// attributes group exists (scripts), matches carrying a src attribute are external and skipped.
func inlineHashes(fsys fs.FS, re *regexp.Regexp) ([]string, error) {
	seen := map[string]struct{}{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		page, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllSubmatch(page, -1) {
			if len(m) == 3 && srcAttr.Match(m[1]) {
				continue
			}
			sum := sha256.Sum256(m[len(m)-1])
			seen["'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'"] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}
