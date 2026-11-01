package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

const (
	// maxValueChars bounds every stored argument value.
	maxValueChars  = 256
	maskedValue    = "[redacted]"
	maxRedactDepth = 8
)

// sensitiveKey matches argument names that hold credentials. "auth" only counts as a whole
// word (auth, user_auth, auth-header) so that "author" and "authority" stay readable.
var sensitiveKey = regexp.MustCompile(`(?i)token|secret|passw(?:or)?d|pwd|authorization|authentication|oauth|` +
	`(?:^|[^a-z])auth(?:$|[^a-z])|api[_-]?key|private[_-]?key|credential|bearer|session|jwt|cookie`)

var (
	urlInText = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	// secretShape matches Bearer credentials, JWTs and well-known API token prefixes.
	secretShape = regexp.MustCompile(`(?i)\bbearer\s+[\w.~+/=-]+|` +
		`\beyJ[\w-]+\.eyJ[\w-]+\.[\w-]*|` +
		`\b(?:sk-|ghp_|github_pat_|xox[bp]-)[\w-]{8,}`)
)

// sanitizeText makes s storable in a Postgres text column: Postgres rejects 0x00 (22021) and
// invalid UTF-8, and one such value would fail a whole COPY batch.
func sanitizeText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "\uFFFD")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalJSON re-encodes JSON with sorted object keys and no whitespace. Numbers keep their
// literal form. Invalid or empty input yields the input bytes unchanged.
func canonicalJSON(raw json.RawMessage) []byte {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("null")
	}
	v, err := decodeUseNumber(raw)
	if err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func decodeUseNumber(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// hashArgs is the sha256 of the canonical form of args.
func hashArgs(args json.RawMessage) string { return sha256Hex(canonicalJSON(args)) }

// redactArgs keeps the top-level keys of an argument object. Values of sensitive keys (at any
// depth) are masked and every other value is cut to maxValueChars characters. Non-object or
// invalid args produce nil: nothing beyond the hash is stored for them.
func redactArgs(args json.RawMessage) json.RawMessage {
	v, err := decodeUseNumber(args)
	if err != nil {
		return nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(obj))
	for k, val := range obj {
		k = sanitizeText(k) // jsonb rejects \u0000 in keys and strings
		out[k] = redactValue(k, val, 0)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

func redactValue(key string, v any, depth int) any {
	if sensitiveKey.MatchString(key) {
		return maskedValue
	}
	masked := maskNested(v, depth)
	b, err := json.Marshal(masked)
	if err != nil {
		return maskedValue
	}
	if len([]rune(string(b))) <= maxValueChars {
		return masked
	}
	if s, ok := masked.(string); ok {
		return truncate(s)
	}
	return truncate(string(b))
}

// maskNested masks sensitive keys inside nested objects and arrays.
func maskNested(v any, depth int) any {
	if depth >= maxRedactDepth {
		return maskedValue
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			k = sanitizeText(k)
			if sensitiveKey.MatchString(k) {
				out[k] = maskedValue
				continue
			}
			out[k] = maskNested(val, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = maskNested(val, depth+1)
		}
		return out
	case string:
		return redactText(sanitizeText(t))
	}
	return v
}

// redactText masks secrets embedded in a string value whatever its key: URL userinfo, query and
// fragment, then Bearer credentials, JWTs and well-known API token prefixes.
func redactText(s string) string {
	s = urlInText.ReplaceAllStringFunc(s, redactURLInText)
	return secretShape.ReplaceAllString(s, maskedValue)
}

// redactURLInText keeps scheme://host/path of a URL and replaces its query with a marker.
// Userinfo and fragment are dropped. It works on the raw text (no url.Parse round trip) so the
// result is stable even for hosts that are not valid UTF-8 or escapes.
func redactURLInText(raw string) string {
	schemeEnd := strings.Index(raw, "://") + len("://")
	rest, _, _ := strings.Cut(raw[schemeEnd:], "#")
	rest, _, hasQuery := strings.Cut(rest, "?")
	authority, path, hasPath := strings.Cut(rest, "/")
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	out := raw[:schemeEnd] + authority
	if hasPath {
		out += "/" + path
	}
	if hasQuery {
		out += "?" + maskedValue
	}
	return out
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxValueChars {
		return s
	}
	return strings.ToValidUTF8(string(r[:maxValueChars]), "") + "...(truncated)"
}
