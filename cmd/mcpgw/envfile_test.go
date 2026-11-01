package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	in := "\xef\xbb\xbf# comment\n\nA=plain\nB=\"quoted value\"\nC='single'\nD=\"mismatch'\nE=$HOME\nF = spaced \nG=a=b\nH=\n"
	got, err := parseEnvFile(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "plain", "B": "quoted value", "C": "single", "D": "\"mismatch'", "E": "$HOME", "F": "spaced", "G": "a=b", "H": ""}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, err := parseEnvFile(strings.NewReader("A=1\nnot a pair\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("want line 2 error, got %v", err)
	}
}

func writeEnvFile(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mcpgw.env")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadEnvFileRealEnvWins(t *testing.T) {
	t.Setenv("MCPGW_T_KEEP", "real")
	t.Setenv("MCPGW_T_NEW", "")
	os.Unsetenv("MCPGW_T_NEW")
	p := writeEnvFile(t, 0o600, "MCPGW_T_KEEP=file\nMCPGW_T_NEW=\"from file\"\n")
	if err := loadEnvFile(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Unsetenv("MCPGW_T_NEW") })
	if got := os.Getenv("MCPGW_T_KEEP"); got != "real" {
		t.Errorf("KEEP = %q", got)
	}
	if got := os.Getenv("MCPGW_T_NEW"); got != "from file" {
		t.Errorf("NEW = %q", got)
	}
}

func TestLoadEnvFileRejectsLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes only")
	}
	p := writeEnvFile(t, 0o640, "MCPGW_T_X=1\n")
	err := loadEnvFile(p)
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("want chmod error, got %v", err)
	}
	if _, set := os.LookupEnv("MCPGW_T_X"); set {
		t.Fatal("file must not be loaded")
	}
}

func TestExtractEnvFile(t *testing.T) {
	get := func(string) string { return "from-env" }
	rest, path, err := extractEnvFile([]string{"user", "--env-file", "/a", "list"}, get)
	if err != nil || path != "/a" || strings.Join(rest, " ") != "user list" {
		t.Fatalf("%v %q %v", rest, path, err)
	}
	if _, path, _ = extractEnvFile([]string{"--env-file=/b"}, get); path != "/b" {
		t.Fatalf("path %q", path)
	}
	if _, path, _ = extractEnvFile(nil, get); path != "from-env" {
		t.Fatalf("path %q", path)
	}
	if _, _, err = extractEnvFile([]string{"--env-file"}, get); err == nil {
		t.Fatal("want error for missing path")
	}
}

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &out, &errOut, strings.NewReader("")); code != 0 {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("out %q", out.String())
	}
}

func TestRunMissingEnvFile(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"migrate", "--env-file", filepath.Join(t.TempDir(), "nope")}, &out, &errOut, strings.NewReader(""))
	if code != 2 || !strings.Contains(errOut.String(), "env file") {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
}
