// Package mcpserver is the gateway's /mcp edge: Streamable HTTP (MCP 2026-07-28, stateless)
// exposing the search and execute tools. It authenticates every request with a bearer token,
// enforces origin, size and per-user rate limits, and leaves protocol handling (JSON-RPC,
// Mcp-Method/Mcp-Name header consistency, tool input validation) to the MCP Go SDK.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

const (
	protocolVersion = "2026-07-28"

	defaultMaxBodyBytes = 1 << 20
	// toolsListTTL is how long clients may cache tools/list. Tool descriptions depend on consent,
	// so it stays short.
	toolsListTTL = 5 * time.Second
)

// TokenValidator verifies bearer tokens. *oauth.Server implements it.
type TokenValidator interface {
	// ValidateAccessToken returns an error wrapping oauth.ErrInvalidToken when the token itself
	// is bad; any other error is an infrastructure failure.
	ValidateAccessToken(ctx context.Context, raw string) (oauth.Claims, error)
	// WWWAuthenticate is the challenge for 401 responses.
	WWWAuthenticate() string
	// Issuer is the gateway origin; it is the only Origin accepted from browsers.
	Issuer() string
}

// ToolService backs the two tools. *codemode.Service implements it.
type ToolService interface {
	Search(ctx context.Context, subj policy.Subject, query string) (codemode.SearchResult, error)
	Execute(ctx context.Context, subj policy.Subject, code string) (json.RawMessage, error)
	ToolDescriptions(ctx context.Context, subj policy.Subject) (searchDesc, executeDesc string)
}

// Options configures a Handler.
type Options struct {
	OAuth    *oauth.Server
	Codemode *codemode.Service
	Logger   *slog.Logger
	// RateLimit is applied per user (default 10 rps, burst 20).
	RateLimit RateLimit
	// MaxBodyBytes caps request bodies (default 1 MiB).
	MaxBodyBytes int64
}

// Handler serves the /mcp endpoint. It is safe for concurrent use.
type Handler struct {
	auth    TokenValidator
	tools   ToolService
	log     *slog.Logger
	limiter *userLimiter
	schemas *mcp.SchemaCache
	maxBody int64
	origin  string
	sdk     *mcp.StreamableHTTPHandler
}

type ctxKey struct{}

// New validates the options and builds a Handler.
func New(o Options) (*Handler, error) {
	if o.OAuth == nil {
		return nil, errors.New("mcpserver: OAuth is required")
	}
	if o.Codemode == nil {
		return nil, errors.New("mcpserver: Codemode is required")
	}
	return newHandler(o.OAuth, o.Codemode, o)
}

// newHandler is the seam used by tests to swap the token validator and the tool service.
func newHandler(auth TokenValidator, tools ToolService, o Options) (*Handler, error) {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	maxBody := o.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxBodyBytes
	}
	h := &Handler{
		auth: auth, tools: tools, log: log, maxBody: maxBody,
		limiter: newUserLimiter(o.RateLimit, time.Now),
		schemas: mcp.NewSchemaCache(),
		origin:  strings.ToLower(auth.Issuer()),
	}
	h.sdk = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		srv, _ := r.Context().Value(ctxKey{}).(*mcp.Server)
		return srv
	}, &mcp.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		Logger:              log,
		MaxRequestBodyBytes: maxBody,
	})
	return h, nil
}

// ServeHTTP runs, in order: Origin check, bearer authentication, per-user rate limit,
// protocol version header, body size and batch checks; then hands the request to the SDK.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		writeError(w, http.StatusForbidden, "forbidden_origin", "Origin not allowed")
		return
	}
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if ok, retry := h.limiter.allow(claims.UserID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests")
		return
	}
	if !checkProtocolVersion(w, r) || !h.checkBody(w, r) {
		return
	}
	subj := policy.Subject{UserID: claims.UserID, ClientID: claims.ClientID}
	ctx := context.WithValue(r.Context(), ctxKey{}, h.newServer(r.Context(), subj))
	h.sdk.ServeHTTP(w, r.WithContext(ctx))
}

// originAllowed implements the DNS rebinding protection of the MCP spec: a request that carries
// an Origin must come from the gateway's own origin. Non-browser clients send no Origin.
func (h *Handler) originAllowed(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) > 1 {
		return false
	}
	u, err := url.Parse(values[0])
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" && u.Path != "/" {
		return false
	}
	return strings.ToLower(u.Scheme+"://"+u.Host) == h.origin
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (oauth.Claims, bool) {
	if r.URL.Query().Has("access_token") {
		h.challenge(w, false)
		return oauth.Claims{}, false
	}
	token, presented := bearerToken(r)
	if token == "" {
		h.challenge(w, presented)
		return oauth.Claims{}, false
	}
	claims, err := h.auth.ValidateAccessToken(r.Context(), token)
	switch {
	case err == nil:
		return claims, true
	case errors.Is(err, oauth.ErrInvalidToken):
		h.challenge(w, true)
	default:
		h.log.Error("mcpserver: token validation failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "Authentication is temporarily unavailable")
	}
	return oauth.Claims{}, false
}

// bearerToken extracts the token from a single Authorization: Bearer header. presented reports
// whether the client sent Authorization at all (so a malformed one still gets invalid_token).
func bearerToken(r *http.Request) (token string, presented bool) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "", false
	}
	if len(values) > 1 {
		return "", true
	}
	scheme, rest, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}
	return strings.TrimSpace(rest), true
}

func (h *Handler) challenge(w http.ResponseWriter, tokenPresented bool) {
	value := h.auth.WWWAuthenticate()
	if tokenPresented {
		value += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", value)
	writeError(w, http.StatusUnauthorized, "unauthorized", "Valid bearer token required")
}

// checkProtocolVersion requires the MCP-Protocol-Version header to be the one version this
// gateway speaks. Mcp-Method/Mcp-Name consistency is then enforced by the SDK for that version.
func checkProtocolVersion(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get("Mcp-Protocol-Version")
	if got == protocolVersion {
		return true
	}
	msg := "MCP-Protocol-Version header is required and must be " + protocolVersion
	if got != "" {
		msg = "Unsupported MCP-Protocol-Version " + strconv.Quote(got) + "; supported: " + protocolVersion
	}
	data, _ := json.Marshal(mcp.UnsupportedProtocolVersionData{Supported: []string{protocolVersion}, Requested: got})
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"jsonrpc": "2.0", "id": nil,
		"error": map[string]any{"code": mcp.CodeUnsupportedProtocolVersion, "message": msg, "data": json.RawMessage(data)},
	})
	return false
}

// checkBody buffers a POST body within the size limit (413 beyond it) and rejects JSON-RPC
// batches: the SDK only validates Mcp-Method/Mcp-Name on single messages, and batching is gone
// in 2026-07-28, so a batch would be a header-validation bypass.
func (h *Handler) checkBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || r.Body == nil {
		return true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "Could not read request body")
		}
		return false
	}
	if trimmed := bytes.TrimLeft(body, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '[' {
		writeError(w, http.StatusBadRequest, "invalid_request", "JSON-RPC batches are not supported")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return true
}

func writeError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
