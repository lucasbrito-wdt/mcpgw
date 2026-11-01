package catalog

import "testing"

func TestQualifiedName(t *testing.T) {
	q := QualifiedName("github", "get.item")
	if q != "github.get.item" {
		t.Fatal(q)
	}
	s, n, ok := ParseQualified(q)
	if !ok || s != "github" || n != "get.item" {
		t.Fatal(s, n, ok)
	}
	for _, bad := range []string{"", "nodot", ".x", "x."} {
		if _, _, ok := ParseQualified(bad); ok {
			t.Errorf("ParseQualified(%q) ok", bad)
		}
	}
}

func TestIdentifier(t *testing.T) {
	tests := map[string]string{
		"get_item": "get_item",
		"get-item": "get_item",
		"get.item": "get_item",
		"3d_model": "_3d_model",
		"delete":   "delete_",
		"class":    "class_",
		"":         "_",
		"a b":      "a_b",
		"$ok":      "$ok",
		"café":     "caf_",
	}
	for in, want := range tests {
		if got := Identifier(in); got != want {
			t.Errorf("Identifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentMapCollisionsAndReversal(t *testing.T) {
	names := []string{"a-b", "a.b", "a_b", "a_b_2", "delete", "delete_", "dup", "dup"}
	m := NewIdentMap(names)
	seen := map[string]string{}
	for _, n := range names {
		id := m.Ident(n)
		if id == "" || !identRe.MatchString(id) {
			t.Fatalf("bad ident %q for %q", id, n)
		}
		if prev, ok := seen[id]; ok && prev != n {
			t.Fatalf("ident %q shared by %q and %q", id, prev, n)
		}
		seen[id] = n
		back, ok := m.Name(id)
		if !ok || back != n {
			t.Fatalf("round trip %q -> %q -> %q", n, id, back)
		}
	}
	// Deterministic regardless of input order.
	m2 := NewIdentMap([]string{"dup", "delete_", "delete", "a_b_2", "a_b", "a.b", "a-b"})
	for _, n := range names {
		if m.Ident(n) != m2.Ident(n) {
			t.Fatalf("not deterministic for %q: %q vs %q", n, m.Ident(n), m2.Ident(n))
		}
	}
	// a_b and a_b_2 are raw tool names: they keep their own identifier, the others go around them.
	if m.Ident("a_b") != "a_b" || m.Ident("a_b_2") != "a_b_2" || m.Ident("a-b") != "a_b_3" || m.Ident("a.b") != "a_b_4" {
		t.Fatalf("unexpected first assignments: %q %q", m.Ident("a-b"), m.Ident("a.b"))
	}
	if _, ok := m.Name("nope"); ok {
		t.Fatal("unknown ident resolved")
	}
	if m.Ident("missing") != "" {
		t.Fatal("unknown name resolved")
	}
}

// F3: an identifier must never equal the raw name of another tool.
func TestIdentMapNeverShadowsAnotherRawName(t *testing.T) {
	names := []string{"get-item", "get_item", "list-items", "list.items", "delete", "delete_", "x-1", "x_1", "x_1_2"}
	m := NewIdentMap(names)
	raw := map[string]bool{}
	for _, n := range names {
		raw[n] = true
	}
	for _, n := range names {
		if id := m.Ident(n); id != n && raw[id] {
			t.Errorf("ident %q of %q equals another tool's raw name", id, n)
		}
	}
	if m.Ident("get_item") != "get_item" {
		t.Errorf("get_item = %q, must keep its own name", m.Ident("get_item"))
	}
	if raw0, _ := m.Name(m.Ident("get-item")); raw0 != "get-item" {
		t.Errorf("get-item does not round trip")
	}
}
