package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const nameSep = "\x1f"

func FuzzParseQualified(f *testing.F) {
	for _, s := range [][2]string{{"github", "get_item"}, {"a", "b.c"}, {"", "x"}, {"x", ""}, {"", ""}, {"a.b", "c"}, {"s", "\x00"}, {"s", "é. "}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, server, tool string) {
		q := QualifiedName(server, tool)
		gotServer, gotTool, ok := ParseQualified(q)
		if ok {
			if gotServer == "" || gotTool == "" || strings.Contains(gotServer, ".") || QualifiedName(gotServer, gotTool) != q {
				t.Fatalf("ParseQualified(%q) = %q %q: not a faithful split", q, gotServer, gotTool)
			}
		}
		if server != "" && tool != "" && !strings.Contains(server, ".") {
			if !ok || gotServer != server || gotTool != tool {
				t.Fatalf("round trip of (%q, %q) via %q gave %q %q %v", server, tool, q, gotServer, gotTool, ok)
			}
		}
	})
}

func FuzzIdentMap(f *testing.F) {
	f.Add("get-item" + nameSep + "get_item" + nameSep + "get_item_2")
	f.Add("class" + nameSep + "class_" + nameSep + "class__2" + nameSep + "yield")
	f.Add("1abc" + nameSep + "_1abc" + nameSep + "" + nameSep + "é" + nameSep + "_")
	f.Add("a.b" + nameSep + "a b" + nameSep + "a_b" + nameSep + "a_b_2" + nameSep + "a_b_2")
	f.Add("__proto__" + nameSep + "constructor" + nameSep + "$x" + nameSep + "日本語" + nameSep + "\x00")
	f.Fuzz(func(t *testing.T, joined string) {
		names := strings.Split(joined, nameSep)
		if len(names) > 64 {
			names = names[:64]
		}
		m := NewIdentMap(names)
		raw := map[string]bool{}
		for _, n := range names {
			raw[n] = true
		}
		byIdent := map[string]string{}
		for n := range raw {
			id := m.Ident(n)
			if !identRe.MatchString(id) || reservedWords[id] {
				t.Fatalf("Ident(%q) = %q is not a usable identifier", n, id)
			}
			if back, ok := m.Name(id); !ok || back != n {
				t.Fatalf("Name(Ident(%q)=%q) = %q %v: not reversible", n, id, back, ok)
			}
			if other, dup := byIdent[id]; dup {
				t.Fatalf("%q and %q share identifier %q", n, other, id)
			}
			byIdent[id] = n
			if id != n && raw[id] {
				t.Fatalf("Ident(%q) = %q collides with another tool's raw name", n, id)
			}
		}
		reversed := make([]string, len(names))
		for i, n := range names {
			reversed[len(names)-1-i] = n
		}
		again := NewIdentMap(reversed)
		for n := range raw {
			if m.Ident(n) != again.Ident(n) {
				t.Fatalf("assignment depends on input order for %q: %q vs %q", n, m.Ident(n), again.Ident(n))
			}
		}
	})
}

// checkDeclarations is a cheap, strict validator for the .d.ts shape the generator emits:
// declare namespace NAME { [/** one-line doc */] function ident(...): Promise<...>; ... }
// It returns the number of declared functions.
func checkDeclarations(out string) (int, error) {
	if !utf8.ValidString(out) {
		return 0, fmt.Errorf("invalid UTF-8")
	}
	for _, r := range out {
		if r == 0x2028 || r == 0x2029 || r == 0x85 || r == 0x7f || (r < 0x20 && r != '\n') {
			return 0, fmt.Errorf("line-altering rune %U", r)
		}
	}
	toks, err := tokenizeDecl(out)
	if err != nil {
		return 0, err
	}
	p := &declParser{toks: toks}
	return p.parse()
}

type declTok struct {
	kind byte // 'c' comment, 's' string, 'w' word, 'p' punct
	text string
}

func tokenizeDecl(src string) ([]declTok, error) {
	var toks []declTok
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n' || c == ' ':
			i++
		case strings.HasPrefix(src[i:], "/**"):
			end := strings.Index(src[i+3:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment at %d", i)
			}
			body := src[i+3 : i+3+end]
			if strings.ContainsRune(body, '\n') {
				return nil, fmt.Errorf("multi-line comment %q", body)
			}
			i += 3 + end + 2
			if i < len(src) && src[i] != '\n' {
				return nil, fmt.Errorf("text after comment end: %q", src[i:min(i+20, len(src))])
			}
			toks = append(toks, declTok{'c', body})
		case c == '"':
			j := i + 1
			for ; j < len(src) && src[j] != '"'; j++ {
				if src[j] < 0x20 || src[j] == 0x7f {
					return nil, fmt.Errorf("control byte %#x in string", src[j])
				}
				if src[j] == '\\' {
					j++
					if j >= len(src) || !strings.ContainsRune(`"\/bfnrtu`, rune(src[j])) {
						return nil, fmt.Errorf("bad escape in string at %d", j)
					}
				}
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string at %d", i)
			}
			toks = append(toks, declTok{'s', src[i : j+1]})
			i = j + 1
		case strings.IndexByte("{}[]()<>:;,?|&", c) >= 0:
			toks = append(toks, declTok{'p', string(c)})
			i++
		case c == '_' || c == '$' || c == '.' || c == '+' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] == '$' || src[j] == '.' || src[j] == '+' || src[j] == '-' || src[j] >= '0' && src[j] <= '9' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z') {
				j++
			}
			toks = append(toks, declTok{'w', src[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected byte %q at %d", c, i)
		}
	}
	return toks, nil
}

type declParser struct {
	toks []declTok
	pos  int
}

func (p *declParser) peek() declTok {
	if p.pos >= len(p.toks) {
		return declTok{}
	}
	return p.toks[p.pos]
}

func (p *declParser) expect(kind byte, text string) error {
	tk := p.peek()
	if tk.kind != kind || (text != "" && tk.text != text) {
		return fmt.Errorf("token %d: want %c %q, got %c %q", p.pos, kind, text, tk.kind, tk.text)
	}
	p.pos++
	return nil
}

func (p *declParser) ident() error {
	tk := p.peek()
	if tk.kind != 'w' || !identRe.MatchString(tk.text) || reservedWords[tk.text] {
		return fmt.Errorf("token %d: %q is not a usable identifier", p.pos, tk.text)
	}
	p.pos++
	return nil
}

// skipBalanced consumes a bracketed group starting at the current opener.
func (p *declParser) skipBalanced() error {
	var stack []string
	closer := map[string]string{"(": ")", "[": "]", "{": "}", "<": ">"}
	for ; p.pos < len(p.toks); p.pos++ {
		tk := p.toks[p.pos]
		if tk.kind != 'p' {
			continue
		}
		if c, ok := closer[tk.text]; ok {
			stack = append(stack, c)
			continue
		}
		if tk.text == ")" || tk.text == "]" || tk.text == "}" || tk.text == ">" {
			if len(stack) == 0 || stack[len(stack)-1] != tk.text {
				return fmt.Errorf("token %d: unbalanced %q", p.pos, tk.text)
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				p.pos++
				return nil
			}
		}
	}
	return fmt.Errorf("unterminated group")
}

func (p *declParser) parse() (int, error) {
	funcs := 0
	for p.pos < len(p.toks) {
		for _, step := range []func() error{
			func() error { return p.expect('w', "declare") },
			func() error { return p.expect('w', "namespace") },
			p.ident,
			func() error { return p.expect('p', "{") },
		} {
			if err := step(); err != nil {
				return funcs, err
			}
		}
		for p.peek().text != "}" || p.peek().kind != 'p' {
			if p.peek().kind == 'c' {
				p.pos++
			}
			steps := []func() error{
				func() error { return p.expect('w', "function") },
				p.ident,
				func() error {
					if p.peek().text != "(" {
						return fmt.Errorf("token %d: want (", p.pos)
					}
					return p.skipBalanced()
				},
				func() error { return p.expect('p', ":") },
				func() error { return p.expect('w', "Promise") },
				func() error {
					if p.peek().text != "<" {
						return fmt.Errorf("token %d: want <", p.pos)
					}
					return p.skipBalanced()
				},
				func() error { return p.expect('p', ";") },
			}
			for _, step := range steps {
				if err := step(); err != nil {
					return funcs, err
				}
			}
			funcs++
		}
		p.pos++
	}
	return funcs, nil
}

func loadCompatTools(tb testing.TB) []ToolDef {
	tb.Helper()
	files, _ := filepath.Glob(filepath.Join("testdata", "compat", "*.tools.json"))
	var out []ToolDef
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			tb.Fatal(err)
		}
		var doc struct {
			Tools []struct {
				Name         string          `json:"name"`
				Description  string          `json:"description"`
				InputSchema  json.RawMessage `json:"inputSchema"`
				OutputSchema json.RawMessage `json:"outputSchema"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			tb.Fatal(err)
		}
		server := strings.SplitN(filepath.Base(file), ".", 2)[0]
		for _, t := range doc.Tools {
			out = append(out, ToolDef{Server: server, Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema})
		}
	}
	return out
}

func TestTypeScriptCompatCorpusIsValidDeclarations(t *testing.T) {
	tools := loadCompatTools(t)
	if len(tools) < 50 {
		t.Fatalf("compat corpus too small: %d tools", len(tools))
	}
	out := TypeScript(tools)
	n, err := checkDeclarations(out)
	if err != nil {
		t.Fatalf("invalid declarations: %v", err)
	}
	if n != len(tools) {
		t.Fatalf("declared %d functions for %d tools", n, len(tools))
	}
}

func FuzzTypeScript(f *testing.F) {
	for _, td := range loadCompatTools(f) {
		f.Add(td.Name, td.Description, []byte(td.InputSchema), []byte(td.OutputSchema))
	}
	for _, in := range []string{
		`{"$ref":"#"}`,
		`{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`,
		`{"type":["string","null","object"],"properties":{"a b":{"type":"integer"},"x\"y":{"enum":["*/"," "]}}}`,
		`{"allOf":[{"oneOf":[{"type":"string"},{"type":"number"}]},{"type":"object"}]}`,
		`{"type":"array","items":{"anyOf":[{"type":"string"},{"type":"null"}]}}`,
		`{"properties":{"k":{"description":"x */ declare namespace evil { }","type":"string"}}}`,
		`{"properties":{"` + "\U000f0000" + `":{"type":"string"},"\u0000":{}}}`,
		`{"$ref":"http://evil/x","enum":[1,2,3]}`,
		`[]`, `null`, `"x"`, `{`, ``,
	} {
		f.Add("tool", "desc */   x\x00y", []byte(in), []byte(in))
	}
	f.Add("deep", "", []byte(strings.Repeat(`{"items":`, 40)+`{"type":"string"}`+strings.Repeat(`}`, 40)), []byte(nil))
	f.Add("fan", "", []byte(fanOutSchema(10, 10)), []byte(fanOutSchema(10, 10)))
	f.Add("enum", "", []byte(`{"enum":[`+strings.TrimSuffix(strings.Repeat(`"v",`, 5000), ",")+`]}`), []byte(nil))

	f.Fuzz(func(t *testing.T, name, desc string, input, output []byte) {
		tools := []ToolDef{
			{Server: "srv", Name: name, Description: desc, InputSchema: input, OutputSchema: output},
			{Server: "srv", Name: Identifier(name), Description: desc, InputSchema: output, OutputSchema: input},
			{Server: "other", Name: "plain", Description: desc},
		}
		out := TypeScript(tools)
		n, err := checkDeclarations(out)
		if err != nil {
			t.Fatalf("invalid declarations: %v\n%s", err, out)
		}
		if n != len(tools) {
			t.Fatalf("declared %d functions for %d tools\n%s", n, len(tools), out)
		}
		if limit := 3*(2*maxTypeBytes+4*maxToolDocRunes+3*len(name)+128) + 128; len(out) > limit {
			t.Fatalf("output %d bytes exceeds bound %d", len(out), limit)
		}
		if again := TypeScript(tools); again != out {
			t.Fatal("generation is not deterministic")
		}
	})
}

func fanOutSchema(width, levels int) string {
	alts := make([]string, width)
	for i := range alts {
		alts[i] = `{"$ref":"#/$defs/n"}`
	}
	node := `{"oneOf":[` + strings.Join(alts, ",") + `]}`
	return fmt.Sprintf(`{"$defs":{"n":%s},"$ref":"#/$defs/n","x-levels":%d}`, node, levels)
}

func TestCheckDeclarationsRejectsBreakouts(t *testing.T) {
	for _, bad := range []string{
		"declare namespace a {\n  /** x */ evil\n  function f(): Promise<unknown>;\n}\n",
		"declare namespace a {\n  /** x */\n  function f(): Promise<\"a b\">;\n}\n",
		"declare namespace class {\n}\n",
		"declare namespace a {\n  function (): Promise<unknown>;\n}\n",
		"declare namespace a {\n  function f(: Promise<unknown>;\n}\n",
	} {
		if _, err := checkDeclarations(bad); err == nil {
			t.Errorf("checker accepted %q", bad)
		}
	}
}
