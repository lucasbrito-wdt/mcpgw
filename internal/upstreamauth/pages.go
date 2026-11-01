package upstreamauth

import (
	"html/template"
	"net/http"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<link rel="icon" href="/icon.svg" type="image/svg+xml">
<title>{{.Title}}</title>
<style>
*{box-sizing:border-box}
html{color-scheme:dark}
body{margin:0;min-height:100dvh;display:grid;place-items:center;padding:1rem;background:#07061A;color:#F8F9FC;font:15px/1.6 "Instrument Sans",ui-sans-serif,system-ui,-apple-system,sans-serif}
main{width:100%;max-width:28rem;padding:2rem;border:1px solid #FFFFFF14;border-radius:20px;background:#000;box-shadow:inset 0 1px 0 #FFFFFF0F}
.mark{display:grid;place-items:center;width:2.5rem;height:2.5rem;border-radius:22%;background:#1B1838;border:1px solid #FFFFFF26}
.mark svg{width:1.5rem;height:1.5rem}
h1{margin:1.5rem 0 .5rem;font-size:1.5rem;line-height:1.2;letter-spacing:-.02em}
p{margin:0;color:#AEAEC1}
a{display:inline-block;margin-top:1.5rem;padding:.5rem 1rem;border-radius:6px;background:#5F5CFF;color:#fff;text-decoration:none;font-weight:600}
a:hover{background:#6F6CFF}
a:focus-visible{outline:2px solid #A5A3FF;outline-offset:2px}
</style>
</head>
<body>
<main>
<div class="mark" aria-hidden="true"><svg viewBox="0 0 64 64" fill="none" stroke="#ECEAFF" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"><path d="M24 12H12v40h12"/><path d="M40 12h12v40H40"/><circle cx="32" cy="32" r="6" fill="#8B7BFF" stroke="none"/></svg></div>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
<a href="/portal/">{{.Back}}</a>
</main>
</body>
</html>`))

func secureHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
}

// requestLocale is the locale negotiated from the request's Accept-Language header.
func requestLocale(r *http.Request) i18n.Locale {
	return i18n.Negotiate(r.Header.Get("Accept-Language"))
}

func renderPage(w http.ResponseWriter, r *http.Request, status int, title, message i18n.Text) {
	l := requestLocale(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, map[string]string{
		"Lang":    string(l),
		"Title":   title.In(l),
		"Message": message.In(l),
		"Back":    i18n.T("Back to the portal", "Voltar ao portal").In(l),
	})
}

func (m *Manager) handleSuccess(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	server := r.URL.Query().Get("server")
	renderPage(w, r, http.StatusOK, i18n.T("Account connected", "Conta conectada"),
		i18n.T("Connection to server \"%s\" is complete. You can go back to your MCP client.",
			"Conexão com o servidor \"%s\" concluída. Você já pode voltar ao seu cliente MCP.", server))
}
