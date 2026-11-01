// Package upstream is the gateway's MCP client toward upstream servers of any era and transport:
// Streamable HTTP (stateless 2026-07-28 and stateful legacy versions), legacy SSE and stdio
// (a hardened docker container by default, a host process only on explicit opt-in).
//
// Credentials are injected per request by the gateway; client tokens are never forwarded.
// Sessions are pooled per (endpoint, owner, credential) with an idle TTL, so a stateful upstream
// pays its handshake (or process start) once, and no session is ever shared across users or
// credentials. The transport and protocol era found for an upstream are remembered through the
// Resolver so that new sessions do not re-probe.
package upstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	DefaultTimeout          = 30 * time.Second
	DefaultMaxResponseBytes = 4 << 20
	maxListPages            = 20
	maxListTools            = 2000
	// Per-tool bounds on untrusted definitions (see Client.boundedTool).
	maxToolDescriptionBytes = 8 << 10
	maxToolSchemaBytes      = 256 << 10 // obscura ships tools with schemas above 64 KiB
)

var (
	// ErrResponseTooLarge is returned when an upstream exchange exceeds the response size bound.
	ErrResponseTooLarge = errors.New("upstream response too large")
	// ErrInputRequired is returned when the upstream asks for client input (MRTR); unsupported.
	ErrInputRequired = errors.New("upstream requires interactive input")
	// ErrCrossOrigin is returned when a request would leave the configured server origin (for
	// example a legacy SSE endpoint event naming another host); it is never sent.
	ErrCrossOrigin = errors.New("upstream request refused: target origin differs from the configured server")
)

// Credential injects authentication into an outgoing upstream request. nil means none.
type Credential interface{ Apply(*http.Request) }

// BearerToken is a Credential that sets an Authorization: Bearer header.
type BearerToken string

func (b BearerToken) Apply(r *http.Request) { r.Header.Set("Authorization", "Bearer "+string(b)) }

// StaticHeader is a Credential that sets one fixed header.
type StaticHeader struct{ Name, Value string }

func (h StaticHeader) Apply(r *http.Request) { r.Header.Set(h.Name, h.Value) }

// EnvCredential is the credential of a stdio server: the process is started with Value in the
// environment variable Name, on top of its fixed environment (never on argv). Env holds the
// connection's own values of the server's per-connection variables; each overrides the fixed
// variable of the same name (which acts as the default). It sets nothing on HTTP requests.
type EnvCredential struct {
	Name, Value string
	Env         map[string]string
}

func (EnvCredential) Apply(*http.Request) {}

// URLCredential is a credential bound to the connection's own upstream address: calls made with it
// go to URL instead of the server URL the caller passes, and pool apart from that URL's sessions.
type URLCredential struct {
	Credential
	URL string
}

// targetOf returns the endpoint and credential a call really uses, unwrapping a URLCredential.
func targetOf(endpoint string, cred Credential) (string, Credential) {
	if uc, ok := cred.(URLCredential); ok && uc.URL != "" {
		return uc.URL, uc.Credential
	}
	return endpoint, cred
}

// Annotations mirrors the MCP tool annotations the gateway cares about.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Tool is an upstream tool definition.
type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  Annotations     `json:"annotations"`
}

// CacheHint carries the upstream ttlMs / cacheScope of a list response.
type CacheHint struct {
	TTL   time.Duration
	Scope string // "public" (default) or "private"
}

// Result is a successful tool call outcome.
type Result struct {
	// Value is the structuredContent; else, for text-only content, the joined text parsed as
	// JSON when valid or a JSON string of it; else a JSON array of typed items (text, image,
	// audio, resource_link, resource) whose base64 payloads fit the response size limit.
	// "null" when the tool returned no usable content. Never larger than the limit.
	Value json.RawMessage
	// Structured reports whether Value came from structuredContent.
	Structured bool
}

// ToolError is returned when the upstream tool reported isError.
type ToolError struct{ Message string }

func (e *ToolError) Error() string { return "upstream tool error: " + e.Message }

// HTTPStatusError reports that the upstream rejected the connection with an auth status (401 or
// 403). It carries no upstream text.
type HTTPStatusError struct{ Status int }

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("upstream rejected the request (HTTP %d)", e.Status)
}

func mergeHint(h *CacheHint, first bool, ttlMs int, scope string) {
	ttl := time.Duration(max(ttlMs, 0)) * time.Millisecond
	if first || ttl < h.TTL {
		h.TTL = ttl
	}
	if scope == "private" {
		h.Scope = "private"
	}
}

func convertTool(t *mcp.Tool) Tool {
	out := Tool{Name: t.Name, Description: t.Description}
	out.InputSchema = marshalSchema(t.InputSchema)
	out.OutputSchema = marshalSchema(t.OutputSchema)
	if a := t.Annotations; a != nil {
		out.Annotations = Annotations{
			Title: a.Title, ReadOnlyHint: a.ReadOnlyHint, DestructiveHint: a.DestructiveHint,
			IdempotentHint: a.IdempotentHint, OpenWorldHint: a.OpenWorldHint,
		}
	}
	return out
}

func marshalSchema(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return nil
	}
	return b
}
