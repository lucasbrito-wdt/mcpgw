package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// QualifiedName is the model-facing tool name: <server_slug>.<tool_name>.
func QualifiedName(server, tool string) string { return server + "." + tool }

// ParseQualified splits a qualified name at the first dot (slugs never contain dots).
func ParseQualified(q string) (server, tool string, ok bool) {
	server, tool, ok = strings.Cut(q, ".")
	if !ok || server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

var reservedWords = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true, "else": true, "enum": true,
	"export": true, "extends": true, "false": true, "finally": true, "for": true, "function": true,
	"if": true, "import": true, "in": true, "instanceof": true, "new": true, "null": true,
	"return": true, "super": true, "switch": true, "this": true, "throw": true, "true": true,
	"try": true, "typeof": true, "var": true, "void": true, "while": true, "with": true,
	"yield": true, "let": true, "static": true, "implements": true, "interface": true,
	"package": true, "private": true, "protected": true, "public": true, "await": true,
	"async": true, "arguments": true, "eval": true,
}

// Identifier sanitizes a tool name into a valid JS identifier. Not injective on its own:
// use IdentMap for a collision-free, reversible mapping.
func Identifier(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r == '$':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if b.Len() == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	id := b.String()
	if id == "" {
		return "_"
	}
	if reservedWords[id] {
		return id + "_"
	}
	return id
}

// IdentMap is a reversible mapping between raw tool names and unique JS identifiers
// within one server. Assignment is deterministic for a given set of names, and no identifier
// equals the raw name of a different tool:
//
//   - a raw name that already is a valid identifier keeps itself;
//   - every other name (processed in sorted order) gets its sanitized form, with a numeric
//     suffix (_2, _3, ...) when that is taken by another tool's name or identifier.
//
// Build it once per server from the FULL tool set and share it between the declarations shown
// to the model and the bridge that resolves calls; a map built from a subset would document
// one tool under the identifier the bridge resolves to another.
type IdentMap struct {
	toIdent map[string]string
	toName  map[string]string
}

// NewIdentMap builds the mapping for the given raw tool names.
func NewIdentMap(names []string) *IdentMap {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	m := &IdentMap{toIdent: map[string]string{}, toName: map[string]string{}}
	for _, n := range sorted {
		if _, dup := m.toIdent[n]; !dup && Identifier(n) == n {
			m.toIdent[n], m.toName[n] = n, n
		}
	}
	for _, n := range sorted {
		if _, done := m.toIdent[n]; done {
			continue
		}
		base := Identifier(n)
		id := base
		for i := 2; ; i++ {
			if _, taken := m.toName[id]; !taken {
				break
			}
			id = fmt.Sprintf("%s_%d", base, i)
		}
		m.toIdent[n], m.toName[id] = id, n
	}
	return m
}

// Ident returns the identifier of a raw tool name ("" when unknown).
func (m *IdentMap) Ident(name string) string { return m.toIdent[name] }

// Name returns the raw tool name behind an identifier.
func (m *IdentMap) Name(ident string) (string, bool) {
	n, ok := m.toName[ident]
	return n, ok
}
