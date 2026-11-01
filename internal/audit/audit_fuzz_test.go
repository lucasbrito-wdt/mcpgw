package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

const fuzzCanary = "CANARY-9f3a1c7e-secret-value"

func FuzzSanitizeText(f *testing.F) {
	for _, s := range []string{"", "plain", "nul\x00byte", "\xff\xfe", "é \x00\xc3", strings.Repeat("\x00", 10), "a\xe2\x82"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := sanitizeText(s)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 survived: %q", out)
		}
		if strings.IndexByte(out, 0) >= 0 {
			t.Fatalf("NUL survived: %q", out)
		}
		if sanitizeText(out) != out {
			t.Fatalf("not idempotent: %q -> %q", out, sanitizeText(out))
		}
		if utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 && out != s {
			t.Fatalf("clean input modified: %q -> %q", s, out)
		}
	})
}

func FuzzRedactArgs(f *testing.F) {
	f.Add(`{"password":"hunter2","nested":{"api_key":"k","list":[{"Authorization":"Bearer x"}]}}`, "token")
	f.Add(`{"a":"`+strings.Repeat("x", 600)+`"}`, "Cookie")
	f.Add(`{"k\u0000":"v\u0000","d":[[[[[[[[[[1]]]]]]]]]]}`, "X-Api-Key")
	f.Add(`[1,2]`, "secret")
	f.Add(`{"a":1e999999,"b":{"\ud800":"\udc00"}}`, "PASSWORD")
	f.Add(``, "")
	f.Fuzz(func(t *testing.T, raw, key string) {
		args := json.RawMessage(raw)
		_ = hashArgs(args)
		if out := redactArgs(args); out != nil {
			decoded, err := decodeUseNumber(out)
			if err != nil {
				t.Fatalf("redactArgs produced invalid JSON %q: %v", out, err)
			}
			assertCleanJSON(t, decoded)
			if obj, ok := decoded.(map[string]any); ok {
				for k, v := range obj {
					assertBoundedValue(t, k, v)
				}
			}
		}

		// A canary under any key the redactor treats as sensitive must never be stored, at any depth.
		if !sensitiveKey.MatchString(key) {
			return
		}
		wrapped, err := json.Marshal(map[string]any{"top": map[string]any{key: fuzzCanary}, key: fuzzCanary, "list": []any{map[string]any{key: []any{fuzzCanary}}}})
		if err != nil {
			t.Fatal(err)
		}
		if out := redactArgs(wrapped); out == nil || strings.Contains(string(out), fuzzCanary) {
			t.Fatalf("canary survived redaction of key %q: %s", key, out)
		}
	})
}

// FuzzRedactText checks that the secret shapes never survive, whatever surrounds them, and that
// redaction is a fixed point.
func FuzzRedactText(f *testing.F) {
	f.Add("see https://h/p?a=b", "x")
	f.Add("", "")
	f.Add("\x00 https://u:p@h?", "Bearer ")
	f.Fuzz(func(t *testing.T, prefix, suffix string) {
		if strings.Contains(prefix+suffix, "CANARY") {
			return
		}
		for _, secret := range []string{
			"sk-CANARY12345678", "ghp_CANARY12345678", "xoxb-CANARY12345678",
			"eyJCANARY.eyJCANARY.sig", "https://h.example/p?CANARY=1",
		} {
			out := redactText(prefix + " " + secret + " " + suffix)
			if strings.Contains(out, "CANARY") {
				t.Fatalf("secret survived: %q -> %q", secret, out)
			}
			if again := redactText(out); again != out {
				t.Fatalf("not idempotent: %q -> %q", out, again)
			}
		}
	})
}

func assertCleanJSON(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case string:
		if strings.IndexByte(x, 0) >= 0 || !utf8.ValidString(x) {
			t.Fatalf("unstorable string %q", x)
		}
	case map[string]any:
		for k, val := range x {
			assertCleanJSON(t, k)
			assertCleanJSON(t, val)
		}
	case []any:
		for _, val := range x {
			assertCleanJSON(t, val)
		}
	}
}

func assertBoundedValue(t *testing.T, key string, v any) {
	t.Helper()
	if sensitiveKey.MatchString(key) && v != maskedValue {
		t.Fatalf("sensitive key %q kept value %v", key, v)
	}
	b, _ := json.Marshal(v)
	if n := len([]rune(string(b))); n > maxValueChars+2*len(truncatedSuffix) && n > 2*maxValueChars {
		t.Fatalf("value of %q not bounded: %d chars", key, n)
	}
}

const truncatedSuffix = "...(truncated)"

func FuzzHashArgsCanonical(f *testing.F) {
	f.Add(`{"b":1,"a":[1,2,{"d":1,"c":2}]}`)
	f.Add(` { "a" : 1.0 , "a" : 2 } `)
	f.Add(`{"s":"<>& \u0000"}`)
	f.Add(`null`)
	f.Add(``)
	f.Fuzz(func(t *testing.T, raw string) {
		canon := canonicalJSON(json.RawMessage(raw))
		if string(canonicalJSON(canon)) != string(canon) {
			t.Fatalf("canonical form not idempotent: %q -> %q", canon, canonicalJSON(canon))
		}
		v, err := decodeUseNumber([]byte(raw))
		if err != nil {
			return // invalid JSON hashes its raw bytes: no stability contract
		}
		indented, err := json.MarshalIndent(v, "  ", "\t")
		if err != nil {
			t.Fatal(err)
		}
		if hashArgs(indented) != hashArgs(json.RawMessage(raw)) {
			t.Fatalf("hash differs for semantically equal input:\n%s\n%s", raw, indented)
		}
	})
}
