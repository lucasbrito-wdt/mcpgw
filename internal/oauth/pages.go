package oauth

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"regexp"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

//go:embed templates/*.html
var templateFS embed.FS

var pageTemplates = map[string]*template.Template{
	"login":   parsePage("login.html"),
	"consent": parsePage("consent.html"),
	"error":   parsePage("error.html"),
}

func parsePage(file string) *template.Template {
	return template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/"+file))
}

// cspSourcePattern restricts what may be interpolated into a CSP source expression.
var cspSourcePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:(//[A-Za-z0-9.:\[\]-]+)?$`)

type clientView struct {
	ID, Name, IDHost, RedirectHost string
	Loopback                       bool
}

type pageData struct {
	Title, Nonce, Error, CSRF, Request, Email string
	Lang                                      i18n.Locale
	S                                         pageStrings
	Client                                    clientView
	Servers                                   []consentServer
}

func newClientView(c *Client, redirectURI string) clientView {
	v := clientView{ID: c.ClientID, Name: c.displayName(), IDHost: c.idHost()}
	if u, err := url.Parse(redirectURI); err == nil {
		v.RedirectHost = u.Scheme + "://" + u.Host
		v.Loopback = u.Scheme == "http" && isLoopbackHost(u.Hostname())
	}
	return v
}

// formActionTarget is the CSP source for the client redirect; browsers apply form-action to
// the redirect that follows a form post.
func formActionTarget(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return ""
	}
	source := u.Scheme + ":"
	if u.Scheme == "http" || u.Scheme == "https" {
		source = u.Scheme + "://" + u.Host
	}
	if !cspSourcePattern.MatchString(source) {
		return ""
	}
	return source
}

// render writes a page with a per-response nonce CSP. formAction lists extra form-action
// sources; when an unsafe target is skipped the directive is omitted so the flow still works.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data pageData, formAction string) {
	l := requestLocale(r)
	data.Lang, data.S = l, stringsFor(l)
	nonce, err := randomToken(18)
	if err != nil {
		http.Error(w, data.S.InternalError, http.StatusInternalServerError)
		return
	}
	data.Nonce = nonce
	var buf bytes.Buffer
	if err := pageTemplates[name].ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("oauth: render page", "page", name, "err", err)
		http.Error(w, data.S.InternalError, http.StatusInternalServerError)
		return
	}
	csp := "default-src 'none'; style-src 'nonce-" + nonce + "'; frame-ancestors 'none'; base-uri 'none'"
	if formAction != "" {
		csp += "; form-action " + formAction
	}
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message i18n.Text) {
	l := requestLocale(r)
	s.render(w, r, status, "error", pageData{Title: stringsFor(l).ErrorTitle, Error: message.In(l)}, "'none'")
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, req authzRequest, c *Client, status int, message i18n.Text) {
	data, ok := s.formPage(w, r, req, c, stringsFor(requestLocale(r)).LoginTitle)
	if !ok {
		return
	}
	data.Error = message.In(requestLocale(r))
	s.render(w, r, status, "login", data, "'self'")
}

func (s *Server) renderConsent(w http.ResponseWriter, r *http.Request, req authzRequest, c *Client, userID, email string) {
	data, ok := s.formPage(w, r, req, c, stringsFor(requestLocale(r)).ConsentTitle)
	if !ok {
		return
	}
	servers, err := s.consentServers(r.Context(), userID, c.ClientID, requestLocale(r))
	if err != nil {
		s.serverError(w, r, "load consent servers", err)
		return
	}
	data.Email, data.Servers = email, servers
	formAction := "'self'"
	if target := formActionTarget(req.RedirectURI); target != "" {
		formAction += " " + target
	} else {
		formAction = ""
	}
	s.render(w, r, http.StatusOK, "consent", data, formAction)
}

// formPage prepares the CSRF token and the sealed request shared by login and consent.
func (s *Server) formPage(w http.ResponseWriter, r *http.Request, req authzRequest, c *Client, title string) (pageData, bool) {
	csrf, err := s.csrfToken(w, r)
	if err != nil {
		s.serverError(w, r, "csrf token", err)
		return pageData{}, false
	}
	sealed, err := s.sealRequest(req)
	if err != nil {
		s.serverError(w, r, "seal request", err)
		return pageData{}, false
	}
	return pageData{Title: title, CSRF: csrf, Request: sealed, Client: newClientView(c, req.RedirectURI)}, true
}
