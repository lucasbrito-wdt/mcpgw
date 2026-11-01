// Package oauth implements the OAuth 2.1 Authorization Server that MCP clients use to obtain
// access tokens for the gateway's /mcp resource (MCP authorization spec 2026-07-28).
package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"
)

// ScopeMCP is the only scope issued. Per-server permissions come from consents, not scopes.
const ScopeMCP = "mcp"

const (
	accessTokenTTL       = 10 * time.Minute
	refreshTokenTTL      = 30 * 24 * time.Hour
	authorizationCodeTTL = 60 * time.Second
	consentTTL           = 90 * 24 * time.Hour
	sessionTTL           = 30 * time.Minute // idle timeout, slides on TouchSession
	sessionMaxAge        = 12 * time.Hour   // absolute cap from login
	authzRequestTTL      = 10 * time.Minute
	validationLeeway     = 30 * time.Second
	// bodyReadTimeout bounds how long a client may take to send the body of an unauthenticated
	// POST (/authorize, /token, /register), so trickled bodies cannot hold connections open.
	bodyReadTimeout = 10 * time.Second

	resourcePath             = "/mcp"
	protectedResourceWellKey = "/.well-known/oauth-protected-resource"
)

// Sealer provides authenticated encryption. aad binds a ciphertext to its purpose; Open must
// fail when the aad differs from the one used in Seal. The vault package satisfies it.
type Sealer interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(ciphertext, aad []byte) ([]byte, error)
}

// Options configures a Server.
type Options struct {
	// PublicURL is the externally visible origin (no trailing slash); it is the issuer.
	PublicURL string
	Pool      *pgxpool.Pool
	Sealer    Sealer
	Logger    *slog.Logger
	// Now overrides the clock (tests). Defaults to time.Now.
	Now func() time.Time
}

// Server is the authorization server. Create it with New.
type Server struct {
	issuer       string
	resource     string
	secureCookie bool
	pool         *pgxpool.Pool
	sealer       Sealer
	log          *slog.Logger
	now          func() time.Time

	keys    *keyRing
	clients *clientResolver
	grants  *grantCache

	loginLimiter    *keyedLimiter
	loginIPLimiter  *keyedLimiter
	emailFailures   *keyedLimiter
	registerLimiter *keyedLimiter
	tokenLimiter    *keyedLimiter
	passwordSlots   chan struct{}
	bodyTimeout     time.Duration
}

// New builds a Server and makes sure an active signing key exists.
func New(ctx context.Context, opts Options) (*Server, error) {
	if opts.Pool == nil || opts.Sealer == nil {
		return nil, errors.New("oauth: pool and sealer are required")
	}
	issuer, err := parseIssuer(opts.PublicURL)
	if err != nil {
		return nil, err
	}
	s := &Server{
		issuer:          issuer,
		resource:        issuer + resourcePath,
		secureCookie:    strings.HasPrefix(issuer, "https://"),
		pool:            opts.Pool,
		sealer:          opts.Sealer,
		log:             opts.Logger,
		now:             opts.Now,
		loginLimiter:    newKeyedLimiter(rate.Every(time.Minute), 5),
		loginIPLimiter:  newKeyedLimiter(rate.Every(6*time.Second), 20),
		emailFailures:   newKeyedLimiter(rate.Every(emailFailureWindow/emailFailureBurst), emailFailureBurst),
		registerLimiter: newKeyedLimiter(rate.Every(6*time.Minute), 5),
		tokenLimiter:    newKeyedLimiter(rate.Every(time.Second), 30),
		passwordSlots:   make(chan struct{}, 4),
		bodyTimeout:     bodyReadTimeout,
		grants:          newGrantCache(),
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.clients = newClientResolver(s.pool, newCIMDFetcher(), s.now)
	s.keys = newKeyRing(s.pool, s.sealer, s.now)
	if err := s.keys.ensureActive(ctx); err != nil {
		return nil, fmt.Errorf("oauth: signing key: %w", err)
	}
	return s, nil
}

// Issuer returns the issuer identifier (the public URL).
func (s *Server) Issuer() string { return s.issuer }

// Resource returns the protected resource identifier tokens are bound to (<public_url>/mcp).
func (s *Server) Resource() string { return s.resource }

// ResourceMetadataURL is the RFC 9728 metadata URL for the /mcp resource.
func (s *Server) ResourceMetadataURL() string {
	return s.issuer + protectedResourceWellKey + resourcePath
}

// WWWAuthenticate returns the WWW-Authenticate value for 401 responses from /mcp.
func (s *Server) WWWAuthenticate() string {
	return fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, s.ResourceMetadataURL(), ScopeMCP)
}

// Routes mounts every authorization server endpoint on mux.
func (s *Server) Routes(mux *http.ServeMux) {
	metadata := map[string]http.HandlerFunc{
		"/.well-known/oauth-authorization-server": s.handleAuthServerMetadata,
		protectedResourceWellKey:                  s.handleProtectedResourceMetadata,
		protectedResourceWellKey + resourcePath:   s.handleProtectedResourceMetadata,
		"/jwks.json":                              s.handleJWKS,
	}
	for path, h := range metadata {
		mux.Handle("GET "+path, s.secure(withPublicCORS(h)))
		mux.Handle("OPTIONS "+path, s.secure(withPublicCORS(h)))
	}
	mux.Handle("GET /authorize", s.secure(http.HandlerFunc(s.handleAuthorize)))
	mux.Handle("POST /authorize", s.secure(s.withBodyDeadline(http.HandlerFunc(s.handleAuthorizeForm))))
	mux.Handle("POST /token", s.secure(s.withBodyDeadline(http.HandlerFunc(s.handleToken))))
	mux.Handle("POST /register", s.secure(s.withBodyDeadline(http.HandlerFunc(s.handleRegister))))
}

func parseIssuer(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("oauth: public URL must be an absolute http(s) origin")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("oauth: public URL must be an origin without path, query or userinfo")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

// secure sets headers every response from this package must carry. Pages override the CSP
// with a nonce-based one; the default denies everything.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		// same-origin, not no-referrer: no-referrer makes browsers send "Origin: null" on our own form posts.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		h.Set("Pragma", "no-cache")
		if s.secureCookie {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// withBodyDeadline sets a read deadline on the connection for this request only (the HTTP
// server resets it for the next one), so the global server has no ReadTimeout that would cut
// long /mcp executions. Writers that do not support deadlines (test recorders) are left alone.
func (s *Server) withBodyDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(s.bodyTimeout))
		next.ServeHTTP(w, r)
	})
}

// withPublicCORS allows cross-origin reads of public metadata (RFC 9728 section 3.1).
func withPublicCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}
