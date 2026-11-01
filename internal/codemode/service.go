// Package codemode orchestrates the two MCP tools the gateway exposes, search and execute.
//
// search filters the tool catalog by what the caller may actually call and returns TypeScript
// declarations. execute runs model-written JavaScript in the sandbox; every tools.<server>.<tool>
// call goes through one pipeline: policy decision, upstream credential, audit, upstream call.
// Policy is authoritative on every inner call; the catalog cache only decides which types the
// model sees. Gateway and upstream tokens never enter the sandbox: credentials are applied to
// the outgoing HTTP request only, and anything that looks like one is scrubbed from results
// and errors.
package codemode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// ErrNotConnected is the contract between codemode and the upstream credential store:
// CredentialSource.ProfileCredentialFor must return an error for which errors.Is(err, ErrNotConnected)
// holds (wrap it with %w), or which has a method `NotConnected() bool` returning true, when the
// user has not connected the server (no stored credential, or it cannot be refreshed and the
// user must reconnect). Any other error is treated as an infrastructure failure.
var ErrNotConnected = errors.New("upstream not connected")

// ErrInternal marks failures that are not caused by the script (for example the audit store is
// down). The message is safe to show to the model; details are logged.
var ErrInternal = errors.New("codemode: internal error")

// CredentialSource resolves the credential to use for a user on an upstream server.
// A nil Credential with a nil error means the call needs no authentication.
type CredentialSource interface {
	// ProfileCredentialFor resolves the user's connection named profile ("" or "default": the
	// personal default, else the shared one).
	ProfileCredentialFor(ctx context.Context, serverID, userID, profile string) (upstream.Credential, error)
	// Profiles lists the connection names the user can call serverID with.
	Profiles(ctx context.Context, serverID, userID string) ([]string, error)
}

// Options configures a Service. Credentials may be nil when every server has auth_kind none.
type Options struct {
	Pool    *pgxpool.Pool
	Sandbox sandbox.Runner
	// Python reports that Sandbox runs Python scripts (montyrs) instead of JavaScript; it only
	// changes the tool descriptions shown to the model.
	Python      bool
	Policy      *policy.Engine
	Upstream    *upstream.Client
	Credentials CredentialSource
	Audit       *audit.Recorder
	PublicURL   string // base URL of the gateway, used in user-facing hints
	Limits      sandbox.Limits
	Logger      *slog.Logger
	// CatalogTTL is how long the tool catalog is cached (default 30s).
	CatalogTTL time.Duration
	// MaxConcurrentPerSubject caps the execute calls one user may have running at once; the
	// next one fails immediately with an error wrapping sandbox.ErrBusy. Zero means
	// DefaultMaxConcurrentPerSubject.
	MaxConcurrentPerSubject int
}

// DefaultMaxConcurrentPerSubject is the per-user execute cap when Options leaves it zero.
const DefaultMaxConcurrentPerSubject = 3

// Service is safe for concurrent use.
type Service struct {
	sandbox   sandbox.Runner
	python    bool
	policy    *policy.Engine
	upstream  *upstream.Client
	creds     CredentialSource
	audit     *audit.Recorder
	publicURL string
	limits    sandbox.Limits
	log       *slog.Logger
	catalog   *catalogCache

	maxPerSubject int
	subjMu        sync.Mutex
	running       map[string]int // concurrent executes per user id
}

// acquireSubject reserves an execute slot for the user; release must be called once.
func (s *Service) acquireSubject(userID string) (release func(), ok bool) {
	s.subjMu.Lock()
	defer s.subjMu.Unlock()
	if s.running[userID] >= s.maxPerSubject {
		return nil, false
	}
	s.running[userID]++
	return func() {
		s.subjMu.Lock()
		defer s.subjMu.Unlock()
		if s.running[userID]--; s.running[userID] <= 0 {
			delete(s.running, userID)
		}
	}, true
}

// New validates the options and builds a Service.
func New(o Options) (*Service, error) {
	switch {
	case o.Pool == nil:
		return nil, errors.New("codemode: Pool is required")
	case o.Sandbox == nil:
		return nil, errors.New("codemode: Sandbox is required")
	case o.Policy == nil:
		return nil, errors.New("codemode: Policy is required")
	case o.Upstream == nil:
		return nil, errors.New("codemode: Upstream is required")
	case o.Audit == nil:
		return nil, errors.New("codemode: Audit is required")
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	if o.MaxConcurrentPerSubject <= 0 {
		o.MaxConcurrentPerSubject = DefaultMaxConcurrentPerSubject
	}
	catalog := newCatalogCache(o.Pool, o.CatalogTTL)
	o.Policy.OnInvalidate(catalog.invalidate)
	return &Service{
		sandbox: o.Sandbox, python: o.Python, policy: o.Policy, upstream: o.Upstream, creds: o.Credentials, audit: o.Audit,
		publicURL: strings.TrimRight(o.PublicURL, "/"), limits: o.Limits, log: log,
		catalog:       catalog,
		maxPerSubject: o.MaxConcurrentPerSubject,
		running:       map[string]int{},
	}, nil
}

const searchDescription = `Find tools you can call. Pass a few keywords (for example "issues github"). ` +
	`Returns TypeScript declarations of the best matches (top %d) and their qualified names. ` +
	`Call this before execute to learn exact tool names and argument types.`

const searchDescriptionPython = `Find tools you can call. Pass a few keywords (for example "issues github"). ` +
	`Returns declarations of the best matches (top %d) and their qualified names; ` +
	`the declarations use TypeScript notation to describe the JSON shape of each tool's argument, ` +
	`which you pass as a Python dict. ` +
	`Call this before execute to learn exact tool names and argument types.`

const executeDescription = `Run JavaScript that calls tools. Pass an async arrow function: ` +
	`async () => { const r = await tools.<server>.<tool>(args); return r; }. ` +
	`Available servers: %s. ` +
	`Use search first for exact names and arguments. ` +
	`Run independent reads in parallel (up to 8 at once): ` +
	`const [a, b] = await Promise.all([tools.x.get_a(args), tools.y.get_b(args)]); ` +
	`use Promise.allSettled when one failure must not discard the rest. ` +
	`Keep dependent writes sequential: await each before starting the next. ` +
	`Return only the data you need; large results are rejected. ` +
	`No network, files or timers. The word "import" is forbidden anywhere in the code.`

const executeDescriptionPython = `Run a Python script that calls tools. Two globals are available: ` +
	`call_tool("server.tool", {"arg": 1}) blocks and returns the decoded JSON reply, and ` +
	`await call_tool_async("server.tool", {"arg": 1}) is awaitable and overlaps with other calls. ` +
	`Available servers: %s. ` +
	`Tool names are qualified, for example "tracker.get_issue"; every tool takes one dict argument. ` +
	`Use search first for exact names and arguments. ` +
	`Run independent reads in parallel (up to 8 at once) with import asyncio and top-level await: ` +
	`a, b = await asyncio.gather(call_tool_async("x.get_a", args), call_tool_async("y.get_b", args)). ` +
	`The blocking call_tool pauses the whole script, so calls made through it never overlap, even inside asyncio.gather. ` +
	`A failed tool call raises RuntimeError (at the call for call_tool, at the await for call_tool_async); ` +
	`use try/except to handle it. asyncio.gather has no return_exceptions support: to keep going when some calls fail, ` +
	`wrap each call in an async def helper that catches RuntimeError, then gather the helpers. ` +
	`Keep dependent calls sequential: use the result of one call before starting the next. ` +
	`The value of the last expression of the script is the result and must be JSON-serializable ` +
	`(dict, list, str, int, float, bool, None); make the last statement a bare expression, for example a dict literal or a variable name. ` +
	`Do not print the result. Return only the data you need; large results are rejected. ` +
	`No network, files or real OS access (open, os, pathlib raise PermissionError). ` +
	`Only a subset of Python and its standard library is available: no third-party packages, ` +
	`and some modules or features may be missing; if something is unsupported, use a simpler construct.`

// ToolDescriptions returns the model-facing descriptions of the search and execute tools. The
// execute description lists the server namespaces the subject can use.
func (s *Service) ToolDescriptions(ctx context.Context, subj policy.Subject) (searchDesc, executeDesc string) {
	seen := map[string]bool{}
	for _, t := range s.policy.AllowedTools(ctx, subj) {
		seen[t.ServerSlug] = true
	}
	servers := make([]string, 0, len(seen))
	for slug := range seen {
		servers = append(servers, slug)
	}
	sort.Strings(servers)
	list := "none (no access granted)"
	if len(servers) > 0 {
		list = strings.Join(servers, ", ")
	}
	if s.python {
		return fmt.Sprintf(searchDescriptionPython, maxSearchResults), fmt.Sprintf(executeDescriptionPython, list)
	}
	return fmt.Sprintf(searchDescription, maxSearchResults), fmt.Sprintf(executeDescription, list)
}
