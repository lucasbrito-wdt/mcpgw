package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	t.Setenv("DOTENV_KEEP", "from-process")
	t.Setenv("DOTENV_NEW", "") // registers cleanup; unset below so the file can define it
	os.Unsetenv("DOTENV_NEW")
	var warn strings.Builder
	path := filepath.Join(t.TempDir(), ".env")
	body := "# comment\n\nDOTENV_NEW=\"quoted value\"\nDOTENV_KEEP=from-file\nnot a pair\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadDotEnv(path, &warn); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DOTENV_NEW"); got != "quoted value" {
		t.Errorf("DOTENV_NEW = %q", got)
	}
	if got := os.Getenv("DOTENV_KEEP"); got != "from-process" {
		t.Errorf("DOTENV_KEEP = %q, process value must win", got)
	}
	if err := loadDotEnv(filepath.Join(t.TempDir(), "missing"), &warn); err != nil {
		t.Errorf("missing file: %v", err)
	}
	if warn.Len() != 0 {
		t.Errorf("0600 file or missing file must not warn, got %q", warn.String())
	}
}

func TestLoadDotEnvWarnsOnBroadMode(t *testing.T) {
	for mode, wantWarn := range map[os.FileMode]bool{0o600: false, 0o400: false, 0o640: true, 0o644: true, 0o604: true} {
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("DOTENV_MODE_TEST=1\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil { // bypass umask
			t.Fatal(err)
		}
		t.Setenv("DOTENV_MODE_TEST", "")
		os.Unsetenv("DOTENV_MODE_TEST")
		var warn strings.Builder
		if err := loadDotEnv(path, &warn); err != nil {
			t.Fatal(err)
		}
		if got := warn.Len() > 0; got != wantWarn {
			t.Errorf("mode %04o: warned=%v, want %v (%q)", mode, got, wantWarn, warn.String())
		}
		if wantWarn && !strings.Contains(warn.String(), "chmod 600") {
			t.Errorf("mode %04o: warning lacks remediation: %q", mode, warn.String())
		}
		if os.Getenv("DOTENV_MODE_TEST") != "1" {
			t.Errorf("mode %04o: file must still be loaded", mode)
		}
	}
}
