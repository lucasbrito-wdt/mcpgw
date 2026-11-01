package upstream

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScrubResultSecretsRawAndEscapedForms(t *testing.T) {
	secret := `tok<"en>&value` // needs JSON escaping, with and without HTML escaping
	escaped, _ := json.Marshal(map[string]string{"a": secret, "b": "plain-api-key-123", "c": "short"})
	got := scrubResultSecrets(escaped, []string{secret, "plain-api-key-123", "short"})
	if !json.Valid(got) {
		t.Fatalf("scrubbed value is not valid JSON: %s", got)
	}
	s := string(got)
	if strings.Contains(s, "plain-api-key-123") || strings.Contains(s, "tok") {
		t.Fatalf("secret survived scrubbing: %s", s)
	}
	if !strings.Contains(s, `"short"`) {
		t.Fatalf("secret below %d bytes must be left alone: %s", minResultSecretBytes, s)
	}
}

func TestScrubResultSecretsNeverReturnsMalformedJSON(t *testing.T) {
	// A secret that spans JSON structure: replacing it breaks the document, so the whole value goes.
	got := scrubResultSecrets(json.RawMessage(`{"key":"x"}`), []string{`{"key":"`})
	if !json.Valid(got) || strings.Contains(string(got), "key") {
		t.Fatalf("got %s, want a valid fully redacted value", got)
	}
}

func TestScrubTextRedactsToolErrorMessages(t *testing.T) {
	got := scrubText(`failed with env DB_PASSWORD=hunter2-long-secret`, []string{"hunter2-long-secret"})
	if strings.Contains(got, "hunter2") || !strings.Contains(got, redactedSecret) {
		t.Fatalf("scrubText = %q", got)
	}
}

func TestDangerousEnvName(t *testing.T) {
	for name, want := range map[string]bool{
		"LD_PRELOAD": true, "ld_library_path": true, "DYLD_INSERT_LIBRARIES": true,
		"HTTPS_PROXY": true, "no_proxy": true, "SSH_AUTH_SOCK": true,
		"GITHUB_TOKEN": false, "PATH_PREFIX": false, "PROXY_URL": false,
	} {
		if got := dangerousEnvName(name); got != want {
			t.Errorf("dangerousEnvName(%q) = %v, want %v", name, got, want)
		}
	}
}
