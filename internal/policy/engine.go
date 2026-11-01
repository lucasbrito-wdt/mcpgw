package policy

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const maxLoadAttempts = 3

// Engine is the PDP. It is safe for concurrent use.
type Engine struct {
	pool      *pgxpool.Pool
	now       func() time.Time
	heartbeat time.Duration

	// healthy is true only while the LISTEN connection is established; see package doc.
	healthy atomic.Bool

	mu       sync.RWMutex
	tools    *toolSet
	users    map[string]*userPolicy
	epoch    uint64            // bumped by flushAll
	toolsGen uint64            // bumped by invalidateTools
	userGens map[string]uint64 // bumped by invalidateUser

	hooksMu sync.Mutex
	hooks   []func() // see OnInvalidate
	// serverHooks: see OnServerChanged
	serverHooks []func(serverID string)
}

// Option customizes an Engine.
type Option func(*Engine)

// WithHeartbeat sets the LISTEN silence interval after which the connection is pinged
// (default DefaultHeartbeat). Non-positive values are ignored.
func WithHeartbeat(d time.Duration) Option {
	return func(e *Engine) {
		if d > 0 {
			e.heartbeat = d
		}
	}
}

// New creates an Engine and verifies database connectivity. Run Listen to enable caching.
func New(ctx context.Context, pool *pgxpool.Pool, opts ...Option) (*Engine, error) {
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("policy: ping database: %w", err)
	}
	e := &Engine{
		pool:      pool,
		now:       time.Now,
		heartbeat: DefaultHeartbeat,
		users:     make(map[string]*userPolicy),
		userGens:  make(map[string]uint64),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Decide authorizes a call by s to the tool serverSlug.toolName.
func (e *Engine) Decide(ctx context.Context, s Subject, serverSlug, toolName string) Decision {
	tools, err := e.getTools(ctx)
	if err != nil {
		return Decision{Reason: ReasonPolicyError}
	}
	tool, ok := tools.byKey[toolKey{serverSlug, toolName}]
	if !ok {
		return Decision{Reason: ReasonUnknownTool}
	}
	if !tool.active {
		return Decision{Reason: ReasonToolQuarantined, Required: tool.level}
	}
	up, err := e.getUser(ctx, normalizeID(s.UserID))
	if err != nil {
		return Decision{Reason: ReasonPolicyError, Required: tool.level}
	}
	return decide(up, s.ClientID, tool, e.now())
}

// AllowedTools lists the active tools s may call, ordered by server slug and name.
func (e *Engine) AllowedTools(ctx context.Context, s Subject) []ToolRef {
	tools, err := e.getTools(ctx)
	if err != nil {
		return nil
	}
	up, err := e.getUser(ctx, normalizeID(s.UserID))
	if err != nil {
		return nil
	}
	now := e.now()
	var out []ToolRef
	for _, r := range tools.rows {
		if d := decide(up, s.ClientID, r.info, now); d.Allow {
			out = append(out, ToolRef{r.key.serverSlug, r.key.name, r.info.level})
		}
	}
	return out
}

func decide(up *userPolicy, clientID string, tool toolInfo, now time.Time) Decision {
	d := Decision{Required: tool.level}
	if !tool.active {
		d.Reason = ReasonToolQuarantined
		return d
	}
	ceiling, ok := up.ceilings[tool.serverID]
	if !ok {
		d.Reason = ReasonNoCeiling
		return d
	}
	consent, ok := up.consents[consentKey{clientID, tool.serverID}]
	if !ok {
		d.Reason = ReasonNoConsent
		return d
	}
	if !consent.expiresAt.IsZero() && !consent.expiresAt.After(now) {
		d.Reason = ReasonConsentExpired
		return d
	}
	d.Effective = min(ceiling, consent.level)
	if tool.level > d.Effective {
		d.Reason = ReasonLevelExceeded
		return d
	}
	d.Allow, d.Reason = true, ReasonAllowed
	return d
}
