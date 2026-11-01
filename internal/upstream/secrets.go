package upstream

import (
	"bytes"
	"encoding/json"
	"strings"
)

// minResultSecretBytes: shorter secrets are ignored when scrubbing results, since replacing
// them would mangle ordinary output (a 3-byte env value matches almost anything).
const minResultSecretBytes = 8

const redactedSecret = "[redacted]"

// scrubResultSecrets replaces every secret (raw and JSON-escaped forms) in a result value. A
// replacement that leaves the value invalid JSON (a secret that matched JSON structure) is
// replaced entirely, so the outcome is never unscrubbed and never malformed.
func scrubResultSecrets(value json.RawMessage, secrets []string) json.RawMessage {
	out := []byte(value)
	for _, form := range secretForms(secrets) {
		out = bytes.ReplaceAll(out, []byte(form), []byte(redactedSecret))
	}
	if len(value) > 0 && !json.Valid(out) {
		b, _ := json.Marshal("[redacted: result matched a credential]")
		return b
	}
	return out
}

// scrubText is scrubResultSecrets for plain text (tool error messages).
func scrubText(s string, secrets []string) string {
	for _, form := range secretForms(secrets) {
		s = strings.ReplaceAll(s, form, redactedSecret)
	}
	return s
}

// secretForms returns each long-enough secret as written raw and as it appears inside a JSON
// string, with and without HTML escaping.
func secretForms(secrets []string) []string {
	var forms []string
	for _, s := range secrets {
		if len(s) < minResultSecretBytes {
			continue
		}
		forms = append(forms, s)
		for _, escaped := range jsonEscapedForms(s) {
			if escaped != s {
				forms = append(forms, escaped)
			}
		}
	}
	return forms
}

func jsonEscapedForms(s string) []string {
	var forms []string
	if b, err := json.Marshal(s); err == nil {
		forms = append(forms, string(b[1:len(b)-1]))
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(s) == nil {
		b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		forms = append(forms, string(b[1:len(b)-1]))
	}
	return forms
}
