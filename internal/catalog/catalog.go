// Package catalog turns upstream MCP tools into the gateway's model-facing catalog:
// trust classification, namespacing, TypeScript declarations, search and DB sync.
package catalog

import (
	"encoding/json"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// Level is a tool trust level. Ordered: read < write < destructive.
type Level string

const (
	LevelRead        Level = "read"
	LevelWrite       Level = "write"
	LevelDestructive Level = "destructive"
)

// Rank returns the ordering of a level; unknown levels rank as destructive (most restrictive).
func (l Level) Rank() int {
	switch l {
	case LevelRead:
		return 0
	case LevelWrite:
		return 1
	default:
		return 2
	}
}

// ToolDef is a tool as seen by the model-facing layers.
type ToolDef struct {
	Server       string // upstream server slug
	Name         string // upstream tool name (raw)
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Annotations  upstream.Annotations
	// ProfileArg is the connection-profile argument the gateway adds to the declaration and strips
	// from calls ("" for none, see ProfileArg). It is never part of the stored definition, so
	// SchemaHash (and review) only ever sees the upstream schema.
	ProfileArg string
}

// Names of the injected connection-profile argument.
const (
	ProfileArgName      = "profile"
	ProfileArgCollision = "mcpgw_profile" // used when the upstream tool already declares "profile"
)

// ProfileArg returns the connection-profile argument to inject into a tool of a server with the
// given auth_kind: none for auth_kind none, else "profile", or "mcpgw_profile" when the tool's
// top-level input properties already have a "profile".
func ProfileArg(authKind string, inputSchema json.RawMessage) string {
	if authKind == "none" {
		return ""
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(inputSchema, &schema) // unparsable schema: nothing to collide with
	if _, taken := schema.Properties[ProfileArgName]; taken {
		return ProfileArgCollision
	}
	return ProfileArgName
}

// FromUpstream builds a ToolDef from an upstream tool.
func FromUpstream(server string, t upstream.Tool) ToolDef {
	return ToolDef{
		Server: server, Name: t.Name, Description: t.Description,
		InputSchema: t.InputSchema, OutputSchema: t.OutputSchema, Annotations: t.Annotations,
	}
}
