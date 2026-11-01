package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// setLocale pins the CLI locale: LC_ALL and LC_MESSAGES are cleared so LANG alone decides.
func setLocale(t *testing.T, lang string) {
	t.Helper()
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", lang)
}

func helpOutput(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	run(context.Background(), args, &stdout, &stderr, strings.NewReader(""))
	return stdout.String()
}

func TestHelpLocale(t *testing.T) {
	cases := []struct {
		lang, mainWant, adminWant string
	}{
		{"pt_BR.UTF-8", "uso: mcpgw <comando>", "uso: mcpgw admin <grupo> <comando>"},
		{"en_US.UTF-8", "usage: mcpgw <command>", "usage: mcpgw admin <group> <command>"},
		{"C", "usage: mcpgw <command>", "usage: mcpgw admin <group> <command>"},
		{"", "usage: mcpgw <command>", "usage: mcpgw admin <group> <command>"},
	}
	for _, c := range cases {
		t.Run("LANG="+c.lang, func(t *testing.T) {
			setLocale(t, c.lang)
			if out := helpOutput(t, "help"); !strings.Contains(out, c.mainWant) {
				t.Errorf("help = %q, want %q", out, c.mainWant)
			}
			out := helpOutput(t, "admin")
			if !strings.Contains(out, c.adminWant) {
				t.Errorf("admin help = %q, want %q", out, c.adminWant)
			}
			if strings.Contains(out, "static_header") || !strings.Contains(out, "--auth none|oauth|header") {
				t.Errorf("admin help must use the canonical auth value header: %q", out)
			}
		})
	}
}

func TestErrorsPrintInLocale(t *testing.T) {
	for lang, want := range map[string]string{
		"pt_BR.UTF-8": "informe o grupo e o comando",
		"en_US.UTF-8": "specify the group and the command",
	} {
		setLocale(t, lang)
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"admin", "user"}, &stdout, &stderr, strings.NewReader("")); code != 1 {
			t.Fatalf("%s: exit code %d", lang, code)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("%s: stderr = %q, want %q", lang, stderr.String(), want)
		}
	}
}
