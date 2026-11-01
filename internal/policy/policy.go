// Package policy is the in-process policy decision point (PDP).
//
// A call by (user, client) to a tool is allowed iff the tool is active and
// tool.trust_level <= min(trust ceiling for user+server, live consent for user+client+server).
// Everything unknown is denied. Admins get no implicit bypass.
//
// Caching: the tools table and each user's ceilings and consents live in memory and are
// invalidated by the policy_changed NOTIFY. Decide never touches the database while the
// LISTEN connection is healthy.
//
// Disconnected behavior (decision: bypass the cache and read the DB, not fail closed): while the
// LISTEN connection is down, or before Listen has established it, notifications may be missed, so
// nothing is served from or written to the cache and every Decide reads the DB directly. This stays
// correct and available at the cost of latency. If the DB itself is unreachable, Decide denies
// with reason "policy_error". On every (re)connect all caches are flushed.
//
// upstream_servers has a trigger since migration 0002 (status and slug changes, insert, delete),
// so disabling a server invalidates the tools cache like any tools change. Disabled servers' tools
// are excluded when the tools cache is loaded.
//
// Half-open connections: the LISTEN connection is pinged after DefaultHeartbeat of silence and has
// TCP keepalive enabled; a failed ping or any read error marks the engine unhealthy at once, so
// revocations are stale for at most about heartbeat plus ping timeout.
package policy

import "strings"

// Level is a trust level. LevelNone means "no access" and orders below every real level.
type Level int

const (
	LevelNone Level = iota
	LevelRead
	LevelWrite
	LevelDestructive
)

func (l Level) String() string {
	switch l {
	case LevelRead:
		return "read"
	case LevelWrite:
		return "write"
	case LevelDestructive:
		return "destructive"
	default:
		return "none"
	}
}

// ParseLevel converts a database level string; unknown values yield LevelNone, false.
func ParseLevel(s string) (Level, bool) {
	switch s {
	case "read":
		return LevelRead, true
	case "write":
		return LevelWrite, true
	case "destructive":
		return LevelDestructive, true
	}
	return LevelNone, false
}

// Decision reasons.
const (
	ReasonAllowed         = "allowed"
	ReasonUnknownTool     = "unknown_tool"
	ReasonToolQuarantined = "tool_quarantined"
	ReasonNoCeiling       = "no_ceiling"
	ReasonNoConsent       = "no_consent"
	ReasonConsentExpired  = "consent_expired"
	ReasonLevelExceeded   = "level_exceeded"
	ReasonPolicyError     = "policy_error"
)

// Subject identifies the caller: the user and the OAuth client acting for them.
type Subject struct{ UserID, ClientID string }

// Decision is the outcome of an authorization check.
type Decision struct {
	Allow     bool
	Reason    string
	Effective Level // min(ceiling, consent); LevelNone when either is missing
	Required  Level // trust level of the tool; LevelNone for unknown tools
}

// ToolRef names an allowed tool and its trust level.
type ToolRef struct {
	ServerSlug string
	Name       string
	Level      Level
}

func normalizeID(s string) string { return strings.ToLower(s) }
