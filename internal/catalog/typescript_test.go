package catalog

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("golden %s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestTypeScriptGolden(t *testing.T) {
	tools := []ToolDef{
		{
			Server: "github", Name: "create_issue", Description: "Create an issue.\nSecond line.",
			InputSchema: raw(`{"type":"object","required":["repo","title"],"properties":{
				"repo":{"type":"string","description":"owner/name"},
				"title":{"type":"string"},
				"labels":{"type":"array","items":{"type":"string"}},
				"priority":{"enum":["low","high",3]},
				"assignee":{"type":["string","null"]},
				"meta":{"type":"object","properties":{"a":{"type":"integer"},"b-c":{"type":"boolean"}},"required":["a"]}}}`),
			OutputSchema: raw(`{"type":"object","required":["id"],"properties":{"id":{"type":"integer"},"url":{"type":"string"}}}`),
		},
		{
			Server: "github", Name: "list-issues", Description: "List issues",
			InputSchema: raw(`{"type":"object","properties":{"state":{"type":"string","enum":["open","closed"]}}}`),
		},
		{
			Server: "github", Name: "delete", Description: "Reserved word name",
			InputSchema: raw(`{"type":"object","properties":{}}`),
		},
		{
			Server: "tree", Name: "get_node", Description: "Recursive and ref schemas",
			InputSchema: raw(`{"type":"object","required":["node"],"properties":{"node":{"$ref":"#/$defs/Node"},"remote":{"$ref":"https://example.com/x.json"}},
				"$defs":{"Node":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"children":{"type":"array","items":{"$ref":"#/$defs/Node"}}}}}}`),
			OutputSchema: raw(`{"oneOf":[{"type":"string"},{"type":"array","items":{"anyOf":[{"type":"string"},{"type":"number"}]}}]}`),
		},
		{
			Server: "misc", Name: "weird", Description: "Evil */ description with /* nesting */ and\ttabs " + strings.Repeat("long ", 80),
			InputSchema:  raw(`{"type":"object","properties":{"x":{"type":"string","description":"close */ now"},"m":{"type":"object","additionalProperties":{"type":"number"}},"t":{"type":"array","prefixItems":[{"type":"string"},{"type":"number"}]},"u":{}},"required":["x"]}`),
			OutputSchema: raw(`{"allOf":[{"type":"object","properties":{"a":{"type":"string"}}},{"type":"object","properties":{"b":{"type":"number"}}}]}`),
		},
		{Server: "misc", Name: "no_schema", Annotations: upstream.Annotations{ReadOnlyHint: true}},
	}
	out := TypeScript(tools)
	golden(t, "typescript", out)
	if strings.Contains(out, "*/ description") || strings.Contains(out, "close */ now") {
		t.Error("unescaped comment terminator")
	}
}

func TestTypeScriptCommentInjection(t *testing.T) {
	out := TypeScript([]ToolDef{{Server: "s", Name: "t", Description: "x */ function evil() {} /*", InputSchema: raw(`{"type":"object","properties":{"p":{"description":"*/ boom","type":"string"}}}`)}})
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "/**") && strings.Count(trim, "*/") != 1 {
			t.Errorf("doc line with extra terminator: %q", line)
		}
	}
	if strings.Contains(out, "evil() {}\n") {
		t.Errorf("injection escaped the comment:\n%s", out)
	}
}

func TestTypeScriptBadSchemaFallsBack(t *testing.T) {
	out := TypeScript([]ToolDef{{Server: "s", Name: "t", InputSchema: raw(`not json`), OutputSchema: raw(`[]`)}})
	want := "declare namespace s {\n  function t(args?: Record<string, unknown>): Promise<unknown>;\n}\n"
	if out != want {
		t.Errorf("got %q", out)
	}
}

// F3: declarations rendered for a subset of a server's tools must use the identifiers of the
// full tool set, because that is what the call bridge resolves.
func TestTypeScriptWithIdentsUsesFullSetMap(t *testing.T) {
	all := []string{"list-items", "list.items"}
	idents := map[string]*IdentMap{"s": NewIdentMap(all)}
	subset := []ToolDef{{Server: "s", Name: "list.items", Description: "dotted"}}

	if alone := TypeScript(subset); !strings.Contains(alone, "function list_items(") {
		t.Fatalf("precondition: a subset-only map names it list_items:\n%s", alone)
	}
	out := TypeScriptWithIdents(subset, idents)
	want := idents["s"].Ident("list.items")
	if want == "list_items" || !strings.Contains(out, "function "+want+"(") || strings.Contains(out, "function list_items(") {
		t.Fatalf("want %q from the full-set map:\n%s", want, out)
	}
}

func TestTypeScriptDocumentsRawNamedToolUnderItsOwnName(t *testing.T) {
	tools := []ToolDef{
		{Server: "s", Name: "get-item", Description: "read variant"},
		{Server: "s", Name: "get_item", Description: "destructive variant"},
	}
	out := TypeScript(tools)
	if !strings.Contains(out, "/** destructive variant */\n  function get_item(") {
		t.Fatalf("get_item must be documented as itself:\n%s", out)
	}
	if !strings.Contains(out, "/** read variant */\n  function get_item_2(") {
		t.Fatalf("get-item must get the suffixed ident:\n%s", out)
	}
}

func TestTypeScriptRefFanOutIsBounded(t *testing.T) {
	// 10 alternatives, each a $ref to a node with 10 alternatives, down to the depth cap: ~10^8 nodes unbounded.
	out := TypeScript([]ToolDef{{Server: "s", Name: "fan", InputSchema: json.RawMessage(fanOutSchema(10, 0))}})
	if len(out) > 2*maxTypeBytes {
		t.Fatalf("output not bounded: %d bytes", len(out))
	}
	if _, err := checkDeclarations(out); err != nil {
		t.Fatal(err)
	}
}

func TestTypeScriptHugeEnumDegradesToBaseType(t *testing.T) {
	members := strings.TrimSuffix(strings.Repeat(`"v",`, 5000), ",")
	out := TypeScript([]ToolDef{{Server: "s", Name: "t", InputSchema: json.RawMessage(`{"type":"object","properties":{"e":{"enum":[` + members + `]},"m":{"enum":["a",1]}}}`)}})
	if !strings.Contains(out, "e?: string") || strings.Count(out, `"v"`) > 0 {
		t.Fatalf("huge enum not degraded to its base type:\n%.300s", out)
	}
}

func TestTypeScriptPropertyKeysUseJSStringEscapes(t *testing.T) {
	// strconv.Quote emits \U000f0000 for non-printable astral runes, which TypeScript rejects.
	out := TypeScript([]ToolDef{{Server: "s", Name: "t", InputSchema: json.RawMessage("{\"properties\":{\"k\U000f0000\":{\"type\":\"string\"},\"\\u0000\":{}}}")}})
	if strings.Contains(out, `\U`) {
		t.Fatalf("Go-only escape in output: %s", out)
	}
	if _, err := checkDeclarations(out); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestTypeScriptAllOfParenthesizesUnions(t *testing.T) {
	out := TypeScript([]ToolDef{{Server: "s", Name: "t", InputSchema: json.RawMessage(`{"allOf":[{"oneOf":[{"type":"string"},{"type":"number"}]},{"type":"object","properties":{"a":{"type":"string"}}}]}`)}})
	if !strings.Contains(out, "(string | number) & { a?: string }") {
		t.Fatalf("union inside intersection is not parenthesized:\n%s", out)
	}
}

func TestTypeScriptSkipsUnrenderableDeclarations(t *testing.T) {
	tools := []ToolDef{
		{Server: "ok", Name: "known"},
		{Server: "ok", Name: "stale"}, // not in the identifier map built from another tool set
		{Server: "class", Name: "x"},  // reserved word cannot name a namespace
		{Server: "bad-slug", Name: "x"},
		{Server: "", Name: "x"},
	}
	out := TypeScriptWithIdents(tools, map[string]*IdentMap{"ok": NewIdentMap([]string{"known"})})
	n, err := checkDeclarations(out)
	if err != nil || n != 1 || !strings.Contains(out, "function known(") || strings.Contains(out, "stale") {
		t.Fatalf("n=%d err=%v\n%s", n, err, out)
	}
}

func TestDocTextRepairsInvalidUTF8(t *testing.T) {
	if got := docText("a\xffb\xc3", 50); !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8 survived: %q", got)
	}
}

func TestTypeScriptEscapesControlAndFormatRunesInKeysAndDocs(t *testing.T) {
	// Found by FuzzTypeScript: DEL in a property key was emitted raw.
	out := TypeScript([]ToolDef{{
		Server: "s", Name: "t", Description: "a\u0080b\u202ec\u200bd\x7fe",
		InputSchema: json.RawMessage("{\"properties\":{\"\\u007f\":{},\"k\u0090\u202e\":{\"enum\":[\"\\u007f\\u0085x\"]}}}"),
	}})
	for _, r := range out {
		if r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x202e || r == 0x200b {
			t.Fatalf("raw %U in output:\n%s", r, out)
		}
	}
	if !strings.Contains(out, "/** a b c d e */") {
		t.Fatalf("doc not normalized:\n%s", out)
	}
	if _, err := checkDeclarations(out); err != nil {
		t.Fatal(err)
	}
}

func TestProfileArgumentInjectedForAuthServers(t *testing.T) {
	schema := raw(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`)
	for kind, want := range map[string]string{
		"oauth": "profile", "header": "profile", "static_header": "profile", "none": "",
	} {
		if got := ProfileArg(kind, schema); got != want {
			t.Errorf("ProfileArg(%q) = %q, want %q", kind, got, want)
		}
	}
	out := TypeScript([]ToolDef{
		{Server: "auth", Name: "get", InputSchema: schema, ProfileArg: "profile"},
		{Server: "open", Name: "get", InputSchema: schema},
	})
	auth, open, _ := strings.Cut(out, "declare namespace open")
	if !strings.Contains(auth, "/** connection to use; defaults to default when omitted */") || !strings.Contains(auth, "profile?: string;") ||
		!strings.Contains(auth, "id: string;") || !strings.Contains(auth, "function get(args: {") {
		t.Fatalf("auth server declaration lacks profile:\n%s", out)
	}
	if strings.Contains(open, "profile") {
		t.Fatalf("auth none server got profile:\n%s", out)
	}
	if _, err := checkDeclarations(out); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestProfileArgumentRenamedOnCollision(t *testing.T) {
	schema := raw(`{"type":"object","properties":{"profile":{"type":"string","description":"upstream own"}}}`)
	if got := ProfileArg("oauth", schema); got != "mcpgw_profile" {
		t.Fatalf("ProfileArg = %q, want mcpgw_profile", got)
	}
	out := TypeScript([]ToolDef{{Server: "s", Name: "t", InputSchema: schema, ProfileArg: "mcpgw_profile"}})
	if !strings.Contains(out, "/** upstream own */") || !strings.Contains(out, "mcpgw_profile?: string;") {
		t.Fatalf("declaration:\n%s", out)
	}
}

func TestProfileArgumentOnSchemasWithoutProperties(t *testing.T) {
	for name, schema := range map[string]string{
		"empty":   ``,
		"bare":    `{"type":"object"}`,
		"ref":     `{"$ref":"#/$defs/A","$defs":{"A":{"type":"object","properties":{"a":{"type":"string"}}}}}`,
		"invalid": `not json`,
	} {
		out := TypeScript([]ToolDef{{Server: "s", Name: "t", InputSchema: raw(schema), ProfileArg: "profile"}})
		if !strings.Contains(out, "profile?: string") {
			t.Errorf("%s: profile missing:\n%s", name, out)
		}
		if _, err := checkDeclarations(out); err != nil {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
}
