package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client talks to upstream MCP servers. Safe for concurrent use. Close it to stop every pooled
// session (and stdio process).
type Client struct {
	transport      *http.Transport
	timeout        time.Duration
	maxResponse    int64
	resolver       Resolver
	idleTTL        time.Duration
	maxSessions    int
	dockerPath     string
	terminateGrace time.Duration
	log            *slog.Logger

	mcp  *mcp.Client
	pool *pool

	detMu      sync.Mutex
	detections map[string]detectionEntry
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the per-operation deadline (connect + request + response).
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithMaxResponseBytes bounds the result of one call (Result.Value). One wire message may be up
// to twice as large, so that oversized binary payloads can be replaced by a placeholder instead
// of failing the call.
func WithMaxResponseBytes(n int64) Option { return func(c *Client) { c.maxResponse = n } }

// WithTransport replaces the shared transport (tests, custom dialers with SSRF guards).
func WithTransport(t *http.Transport) Option { return func(c *Client) { c.transport = t } }

// WithResolver resolves endpoint keys (stdio servers, stored detection). Without it only http(s)
// URLs work and detection is remembered in memory only.
func WithResolver(r Resolver) Option { return func(c *Client) { c.resolver = r } }

// WithIdleTTL sets how long an unused pooled session stays open (default DefaultIdleTTL).
func WithIdleTTL(d time.Duration) Option { return func(c *Client) { c.idleTTL = d } }

// WithMaxSessions caps the pooled sessions (default DefaultMaxSessions).
func WithMaxSessions(n int) Option { return func(c *Client) { c.maxSessions = n } }

// WithDockerPath sets the docker CLI used by the docker launcher (default "docker").
func WithDockerPath(path string) Option { return func(c *Client) { c.dockerPath = path } }

// WithTerminateGrace sets how long a stdio server gets to exit after stdin closes (and again
// after SIGTERM) before it is killed.
func WithTerminateGrace(d time.Duration) Option { return func(c *Client) { c.terminateGrace = d } }

// WithLogger sets the logger for background failures (default slog.Default()).
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// New builds a Client with a pooled HTTP/2-capable transport.
func New(opts ...Option) *Client {
	c := &Client{
		timeout: DefaultTimeout, maxResponse: DefaultMaxResponseBytes, resolver: urlResolver{},
		idleTTL: DefaultIdleTTL, maxSessions: DefaultMaxSessions, dockerPath: "docker",
		terminateGrace: defaultTerminateGrace, log: slog.Default(), detections: map[string]detectionEntry{},
	}
	for _, o := range opts {
		o(c)
	}
	if c.transport == nil {
		c.transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
		}
	}
	// A fixed, empty capability set: tool lists then never depend on what a session declared,
	// and server->client requests (roots, sampling, elicitation) are declined by the SDK.
	// Multi-round-trip is handled by the gateway (ErrInputRequired), never auto-fulfilled.
	c.mcp = mcp.NewClient(&mcp.Implementation{Name: "mcp-gateway", Version: "0.1.0"}, &mcp.ClientOptions{
		Capabilities:   &mcp.ClientCapabilities{},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	c.pool = newPool(c.dialSession, c.forgetDetection, c.idleTTL, c.maxSessions, c.timeout)
	return c
}

// Close stops every pooled session (stdio processes included) and releases idle connections.
func (c *Client) Close() {
	c.pool.close()
	c.transport.CloseIdleConnections()
}

// InvalidateStdio closes the pooled processes of one stdio server (by id) so that the next call
// spawns again from the stored configuration; "" does it for every stdio server. A change of the
// sealed environment is only picked up this way: pool keys fingerprint the credential, not it.
func (c *Client) InvalidateStdio(serverID string) {
	c.pool.dropMatching(func(endpoint string) bool {
		if serverID == "" {
			return strings.HasPrefix(endpoint, StdioKeyPrefix)
		}
		return endpoint == StdioKeyPrefix+serverID
	})
}

// wireLimit bounds one inbound message.
func (c *Client) wireLimit() int64 {
	if c.maxResponse <= 0 || c.maxResponse > 1<<29 {
		return 1 << 30
	}
	return 2 * c.maxResponse
}

// ListTools lists all tools of the upstream, following pagination. The CacheHint is the most
// conservative hint across pages (smallest ttl, private wins).
func (c *Client) ListTools(ctx context.Context, serverURL string, cred Credential) ([]Tool, CacheHint, error) {
	var tools []Tool
	var hint CacheHint
	err := c.withSession(ctx, serverURL, cred, true, func(ctx context.Context, s *session) error {
		tools, hint = nil, CacheHint{Scope: "public"}
		cursor := ""
		for page := 0; page < maxListPages; page++ {
			res, err := s.cs.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			if err != nil {
				return err
			}
			for _, t := range res.Tools {
				// Checked per tool, so one oversized page cannot get past the cap either.
				if len(tools) >= maxListTools {
					return fmt.Errorf("upstream returned more than %d tools", maxListTools)
				}
				if tool, ok := c.boundedTool(convertTool(t)); ok {
					tools = append(tools, tool)
				}
			}
			mergeHint(&hint, page == 0, res.GetTTLMs(), res.GetCacheScope())
			if res.NextCursor == "" {
				return nil
			}
			cursor = res.NextCursor
		}
		return fmt.Errorf("upstream tools list exceeds %d pages", maxListPages)
	})
	if err != nil {
		return nil, CacheHint{}, err
	}
	return tools, hint, nil
}

// boundedTool enforces per-tool size caps on untrusted definitions: an over-long description is
// truncated (still useful), a tool whose schema is over the cap is dropped and logged (a
// truncated schema would be invalid), so the list call still succeeds for the other tools.
func (c *Client) boundedTool(t Tool) (Tool, bool) {
	if len(t.InputSchema) > maxToolSchemaBytes || len(t.OutputSchema) > maxToolSchemaBytes {
		c.log.Warn("upstream: dropped tool with oversized schema", "tool", truncateForLog(t.Name), "bytes", max(len(t.InputSchema), len(t.OutputSchema)))
		return Tool{}, false
	}
	if len(t.Description) > maxToolDescriptionBytes {
		t.Description = strings.ToValidUTF8(t.Description[:maxToolDescriptionBytes], "")
	}
	return t, true
}

func truncateForLog(s string) string {
	if len(s) > 100 {
		return strings.ToValidUTF8(s[:100], "") + "..."
	}
	return s
}

// CallTool invokes one tool. args must be a JSON object (or empty). An upstream isError result
// is returned as *ToolError alongside the Result carrying its content.
func (c *Client) CallTool(ctx context.Context, serverURL string, cred Credential, name string, args json.RawMessage) (Result, error) {
	var arguments any = map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		var obj map[string]any
		if err := json.Unmarshal(args, &obj); err != nil {
			return Result{}, fmt.Errorf("tool arguments must be a JSON object: %w", err)
		}
		arguments = obj
	}
	var out Result
	err := c.withSession(ctx, serverURL, cred, false, func(ctx context.Context, s *session) error {
		if strings.HasPrefix(s.key.endpoint, "http") {
			// shortcut: a failed listing is not retried for this session; the call then goes out
			// without Mcp-Param-* headers and only servers that require them reject it.
			s.toolsListed.Do(func() {
				for _, err := range s.cs.Tools(ctx, nil) {
					if err != nil {
						break
					}
				}
			})
		}
		res, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
		if err != nil {
			return err
		}
		if res.NeedsInput() {
			return ErrInputRequired
		}
		if out, err = convertResult(res, c.maxResponse); err != nil {
			return err
		}
		// A server may echo its own credentials (stdio env, HTTP headers) back in a result.
		out.Value = scrubResultSecrets(out.Value, s.secrets)
		if res.IsError {
			return &ToolError{Message: scrubText(toolErrorMessage(res, out.Value), s.secrets)}
		}
		return nil
	})
	return out, err
}

// withSession runs fn on the pooled session of (endpoint, owner, credential) under the
// per-operation timeout. A session the server no longer knows (or one already shut down) is
// replaced and fn retried once; a connection that broke mid-request is retried only for
// idempotent operations, so a tool call that may have run is never repeated.
func (c *Client) withSession(ctx context.Context, endpoint string, cred Credential, idempotent bool, fn func(context.Context, *session) error) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	endpoint, cred = targetOf(endpoint, cred)
	key := sessionKey{endpoint: endpoint, owner: ownerFrom(ctx), credential: credentialID(cred)}
	for attempt := 0; ; attempt++ {
		s, err := c.pool.acquire(ctx, key, cred)
		if err != nil {
			return scrubError(err, false, credentialValues(cred))
		}
		err = fn(ctx, s)
		c.pool.release(s)
		if err == nil {
			return nil
		}
		retry := retryable(err, idempotent)
		if retry || s.isDead() || s.overflowed() || errors.Is(err, io.EOF) {
			c.pool.invalidate(s)
		}
		if retry && attempt == 0 && ctx.Err() == nil {
			if errors.Is(err, mcp.ErrSessionMissing) {
				c.forgetDetection(endpoint) // the server restarted: its era may have changed
			}
			continue
		}
		return s.sanitize(err)
	}
}

// retryable reports whether err proves the request never reached a live session (the server
// dropped the session, or the connection was already shutting down when it was sent), or the
// operation is idempotent and the connection broke under it.
func retryable(err error, idempotent bool) bool {
	if errors.Is(err, mcp.ErrSessionMissing) || errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	return idempotent && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF))
}
