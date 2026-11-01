package catalog

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	maxToolDocRunes = 200
	maxPropDocRunes = 100
	maxSchemaDepth  = 8
	// maxSchemaNodes bounds the work and output of one type: $ref fan-out can otherwise expand a
	// small schema exponentially (10 alternatives over 8 levels is 10^8 nodes).
	maxSchemaNodes = 2000
	// maxTypeBytes caps one rendered type; anything larger degrades to unknown.
	maxTypeBytes = 16 << 10
	// maxEnumMembers caps the literals listed for one enum; larger ones degrade to their base type.
	maxEnumMembers = 100
)

var identRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// TypeScript renders compact .d.ts declarations for tools, grouped by server:
//
//	declare namespace github {
//	  /** Fetch an item */
//	  function get_item(args: { id: string }): Promise<{ id: string }>;
//	}
//
// Descriptions are untrusted: they are collapsed to one line, truncated and have "*/" escaped.
// Schemas that cannot be expressed (remote $ref, cycles, depth) fall back to unknown.
// Server slugs must already be valid identifiers (enforced by the DB slug constraint).
func TypeScript(tools []ToolDef) string { return TypeScriptWithIdents(tools, nil) }

// TypeScriptWithIdents is TypeScript with the identifier maps to use per server slug. Pass the
// maps built from each server's full tool set (the ones the call bridge resolves with), so a
// declaration never names a tool differently from the bridge. A server missing from idents
// falls back to a map built from the given tools.
func TypeScriptWithIdents(tools []ToolDef, idents map[string]*IdentMap) string {
	byServer := map[string][]ToolDef{}
	for _, t := range tools {
		byServer[t.Server] = append(byServer[t.Server], t)
	}
	servers := make([]string, 0, len(byServer))
	for s := range byServer {
		servers = append(servers, s)
	}
	sort.Strings(servers)

	var b strings.Builder
	for _, s := range servers {
		if !validNamespace(s) { // would emit an unparsable declaration; fail closed
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		writeNamespace(&b, s, byServer[s], idents[s])
	}
	return b.String()
}

func writeNamespace(b *strings.Builder, server string, tools []ToolDef, idents *IdentMap) {
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	if idents == nil {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name
		}
		idents = NewIdentMap(names)
	}

	b.WriteString("declare namespace " + server + " {\n")
	for _, t := range tools {
		ident := idents.Ident(t.Name)
		if ident == "" { // a map built from another tool set: the bridge could not resolve it either
			continue
		}
		if d := docText(t.Description, maxToolDocRunes); d != "" {
			b.WriteString("  /** " + d + " */\n")
		}
		b.WriteString("  function " + ident + "(" + argsParam(t.InputSchema, t.ProfileArg) + "): Promise<" + outputType(t.OutputSchema) + ">;\n")
	}
	b.WriteString("}\n")
}

// validNamespace reports whether slug can name a `declare namespace`.
func validNamespace(slug string) bool { return identRe.MatchString(slug) && !reservedWords[slug] }

func newWriter(root map[string]any) *tsWriter {
	return &tsWriter{root: root, active: map[string]bool{}}
}

// capType degrades an oversized rendered type to unknown.
func capType(t string) string {
	if len(t) > maxTypeBytes {
		return "unknown"
	}
	return t
}

func argsParam(raw json.RawMessage, profileArg string) string {
	root := parseSchema(raw)
	node := withProfileArg(root, profileArg)
	if node == nil {
		return "args?: Record<string, unknown>"
	}
	w := newWriter(root)
	typ := capType(w.typ(node, 0, "  ", true))
	if typ == "unknown" {
		return "args?: Record<string, unknown>"
	}
	opt := "?"
	if m, ok := w.resolve(root).(map[string]any); ok {
		if req, _ := m["required"].([]any); len(req) > 0 {
			opt = ""
		}
	}
	return "args" + opt + ": " + typ
}

const profileArgDoc = "connection to use; defaults to default when omitted"

// withProfileArg returns the schema node to render: root plus an optional string property name
// (nil when there is nothing to render). root itself is not modified; $refs still resolve against it.
func withProfileArg(root map[string]any, name string) map[string]any {
	if name == "" {
		return root
	}
	prop := map[string]any{"type": "string", "description": profileArgDoc}
	if root == nil {
		return map[string]any{"type": "object", "properties": map[string]any{name: prop}, "additionalProperties": map[string]any{}}
	}
	if firstList(root, "allOf", "anyOf", "oneOf") != nil || root["$ref"] != nil {
		return map[string]any{"allOf": []any{root, map[string]any{"type": "object", "properties": map[string]any{name: prop}}}}
	}
	node := maps.Clone(root)
	props, _ := root["properties"].(map[string]any)
	if len(props) == 0 && root["additionalProperties"] == nil { // was Record<string, unknown>: keep other keys open
		node["additionalProperties"] = map[string]any{}
	}
	props = maps.Clone(props)
	if props == nil {
		props = map[string]any{}
	}
	props[name] = prop
	node["properties"] = props
	return node
}

func outputType(raw json.RawMessage) string {
	root := parseSchema(raw)
	if root == nil {
		return "unknown"
	}
	return capType(newWriter(root).typ(root, 0, "  ", false))
}

func parseSchema(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

type tsWriter struct {
	root   map[string]any
	active map[string]bool // $refs currently being expanded (cycle guard)
	nodes  int             // schema nodes visited so far (work budget)
}

// typ converts a schema node. docs enables per-property JSDoc on the top-level object only.
func (w *tsWriter) typ(node any, depth int, indent string, docs bool) string {
	w.nodes++
	if depth > maxSchemaDepth || w.nodes > maxSchemaNodes {
		return "unknown"
	}
	switch n := node.(type) {
	case bool:
		if n {
			return "unknown"
		}
		return "never"
	case map[string]any:
		return w.object(n, depth, indent, docs)
	}
	return "unknown"
}

func (w *tsWriter) object(n map[string]any, depth int, indent string, docs bool) string {
	if ref, ok := n["$ref"].(string); ok {
		return w.ref(ref, depth, indent, docs)
	}
	if v, ok := n["const"]; ok {
		return literal(v)
	}
	if enum, ok := n["enum"].([]any); ok && len(enum) > 0 {
		if len(enum) > maxEnumMembers {
			return enumBaseType(enum)
		}
		return unionOf(mapSlice(enum, literal))
	}
	if alts := firstList(n, "oneOf", "anyOf"); len(alts) > 0 {
		return unionOf(mapSlice(alts, func(a any) string { return w.typ(a, depth+1, indent, false) }))
	}
	if parts, ok := n["allOf"].([]any); ok && len(parts) > 0 {
		return joinTop(mapSlice(parts, func(a any) string {
			t := w.typ(a, depth+1, indent, false)
			if hasTopLevelOp(t) { // & binds tighter than |
				return "(" + t + ")"
			}
			return t
		}), " & ")
	}
	switch t := n["type"].(type) {
	case string:
		return w.byType(t, n, depth, indent, docs)
	case []any:
		var ts []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				ts = append(ts, w.byType(s, n, depth, indent, docs))
			}
		}
		return unionOf(ts)
	}
	switch {
	case n["properties"] != nil:
		return w.byType("object", n, depth, indent, docs)
	case n["items"] != nil:
		return w.byType("array", n, depth, indent, docs)
	}
	return "unknown"
}

func (w *tsWriter) byType(t string, n map[string]any, depth int, indent string, docs bool) string {
	switch t {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		return w.array(n, depth, indent)
	case "object":
		return w.objectType(n, depth, indent, docs)
	}
	return "unknown"
}

func (w *tsWriter) array(n map[string]any, depth int, indent string) string {
	if prefix, ok := n["prefixItems"].([]any); ok && len(prefix) > 0 {
		return "[" + strings.Join(mapSlice(prefix, func(a any) string { return w.typ(a, depth+1, indent, false) }), ", ") + "]"
	}
	items, ok := n["items"]
	if !ok {
		return "unknown[]"
	}
	el := w.typ(items, depth+1, indent, false)
	if hasTopLevelOp(el) {
		el = "(" + el + ")"
	}
	return el + "[]"
}

func (w *tsWriter) objectType(n map[string]any, depth int, indent string, docs bool) string {
	props, _ := n["properties"].(map[string]any)
	extra, hasExtra := n["additionalProperties"]
	if len(props) == 0 {
		if m, ok := extra.(map[string]any); ok {
			return "Record<string, " + w.typ(m, depth+1, indent, false) + ">"
		}
		return "Record<string, unknown>"
	}
	required := map[string]bool{}
	if req, ok := n["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type field struct{ doc, text string }
	fields := make([]field, 0, len(keys)+1)
	multiline := false
	for _, k := range keys {
		var f field
		if pm, ok := props[k].(map[string]any); ok && docs {
			if d, _ := pm["description"].(string); d != "" {
				f.doc = docText(d, maxPropDocRunes)
				multiline = multiline || f.doc != ""
			}
		}
		opt := "?"
		if required[k] {
			opt = ""
		}
		f.text = propKey(k) + opt + ": " + w.typ(props[k], depth+1, indent+"  ", false)
		fields = append(fields, f)
	}
	if m, ok := extra.(map[string]any); ok && hasExtra {
		fields = append(fields, field{text: "[key: string]: " + w.typ(m, depth+1, indent+"  ", false)})
	}

	if !multiline {
		parts := make([]string, len(fields))
		for i, f := range fields {
			parts[i] = f.text
		}
		return "{ " + strings.Join(parts, "; ") + " }"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, f := range fields {
		if f.doc != "" {
			b.WriteString(indent + "  /** " + f.doc + " */\n")
		}
		b.WriteString(indent + "  " + f.text + ";\n")
	}
	b.WriteString(indent + "}")
	return b.String()
}

// ref expands a local $ref (best effort); anything else, or a cycle, becomes unknown.
func (w *tsWriter) ref(ref string, depth int, indent string, docs bool) string {
	target, ok := w.lookupRef(ref)
	if !ok || w.active[ref] {
		return "unknown"
	}
	w.active[ref] = true
	defer delete(w.active, ref)
	return w.typ(target, depth+1, indent, docs)
}

// resolve follows $ref chains for inspection (no cycle expansion, bounded).
func (w *tsWriter) resolve(node any) any {
	for i := 0; i < 10; i++ {
		m, ok := node.(map[string]any)
		if !ok {
			return node
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return node
		}
		target, ok := w.lookupRef(ref)
		if !ok {
			return nil
		}
		node = target
	}
	return nil
}

func (w *tsWriter) lookupRef(ref string) (any, bool) {
	if ref == "#" {
		return w.root, true
	}
	path, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil, false
	}
	var cur any = w.root
	for _, seg := range strings.Split(path, "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func propKey(k string) string {
	if identRe.MatchString(k) {
		return k
	}
	b, err := json.Marshal(k) // JSON strings are valid JS/TS strings; strconv.Quote emits \U escapes TS rejects
	if err != nil {
		return `"_"`
	}
	return escapeRawControls(string(b))
}

// escapeRawControls rewrites the runes encoding/json leaves raw (DEL, C1 controls, format
// characters) as \uXXXX so a rendered literal never carries an invisible or line-altering rune.
func escapeRawControls(lit string) string {
	var out strings.Builder
	for _, r := range lit {
		if r == 0x7f || isDocSeparator(r) && r > 0x7e {
			fmt.Fprintf(&out, `\u%04x`, r)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// enumBaseType is the primitive type shared by every enum member (unknown when mixed).
func enumBaseType(enum []any) string {
	base := ""
	for _, v := range enum {
		var t string
		switch v.(type) {
		case string:
			t = "string"
		case float64:
			t = "number"
		case bool:
			t = "boolean"
		default:
			return "unknown"
		}
		if base != "" && base != t {
			return "unknown"
		}
		base = t
	}
	return base
}

func literal(v any) string {
	switch v.(type) {
	case string, float64, bool, nil:
		b, err := json.Marshal(v)
		if err != nil {
			return "unknown"
		}
		return escapeRawControls(string(b))
	}
	return "unknown"
}

func firstList(n map[string]any, keys ...string) []any {
	for _, k := range keys {
		if l, ok := n[k].([]any); ok && len(l) > 0 {
			return l
		}
	}
	return nil
}

func mapSlice(in []any, f func(any) string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func unionOf(parts []string) string { return joinTop(parts, " | ") }

// joinTop joins unique parts; "unknown" absorbs a union, "never" disappears from it.
func joinTop(parts []string, sep string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		if sep == " | " && p == "unknown" {
			return "unknown"
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	switch len(out) {
	case 0:
		return "unknown"
	case 1:
		return out[0]
	}
	return strings.Join(out, sep)
}

// hasTopLevelOp reports whether s has a | or & outside brackets, braces and parentheses.
func hasTopLevelOp(s string) bool {
	depth := 0
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{' || c == '[' || c == '(' || c == '<':
			depth++
		case c == '}' || c == ']' || c == ')' || c == '>':
			depth--
		case (c == '|' || c == '&') && depth == 0:
			return true
		}
	}
	return false
}

// isDocSeparator marks runes that must not reach a one-line comment: whitespace (including
// U+0085, U+2028, U+2029), control characters and invisible format characters (bidi overrides,
// zero-width) that can disguise text.
func isDocSeparator(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// docText makes untrusted text safe for a one-line JSDoc comment.
func docText(s string, maxRunes int) string {
	s = strings.Join(strings.FieldsFunc(strings.ToValidUTF8(s, "\uFFFD"), isDocSeparator), " ")
	s = strings.ReplaceAll(s, "*/", "*\\/")
	r := []rune(s)
	if len(r) > maxRunes {
		s = strings.TrimRight(string(r[:maxRunes]), " \\") + "..."
	}
	return s
}
