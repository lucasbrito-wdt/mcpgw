package admin_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

func TestValidateSlug(t *testing.T) {
	for slug, ok := range map[string]bool{
		"github": true, "my_server2": true, "a": true, strings.Repeat("a", 63): true,
		"": false, "1abc": false, "Upper": false, "has-dash": false, "sp ace": false,
		strings.Repeat("a", 64): false, "callback": false, "success": false, "x'; drop": false,
	} {
		err := admin.ValidateSlug(slug)
		if (err == nil) != ok {
			t.Errorf("ValidateSlug(%q) = %v, want ok=%v", slug, err, ok)
		}
		if err != nil && !errors.Is(err, admin.ErrInvalid) {
			t.Errorf("ValidateSlug(%q) is not ErrInvalid: %v", slug, err)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got, err := admin.NormalizeEmail("  Lucas@Example.COM "); err != nil || got != "lucas@example.com" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "no-at", "Name <a@b.co>", "a@b.co, c@d.co", strings.Repeat("a", 320) + "@x.co"} {
		if _, err := admin.NormalizeEmail(bad); err == nil {
			t.Errorf("NormalizeEmail(%q) accepted", bad)
		}
	}
}

func TestValidateAuthKind(t *testing.T) {
	for in, want := range map[string]string{"none": "none", "oauth": "oauth", "header": "header", "static_header": "header"} {
		if got, err := admin.ValidateAuthKind(in); err != nil || got != want {
			t.Errorf("ValidateAuthKind(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "basic", "HEADER"} {
		if _, err := admin.ValidateAuthKind(bad); err == nil {
			t.Errorf("ValidateAuthKind(%q) accepted", bad)
		}
	}
}

func TestValidateServerURL(t *testing.T) {
	cases := []struct {
		url, kind string
		ok        bool
	}{
		{"https://mcp.example.com/mcp", "oauth", true},
		{"http://mcp.example.com/mcp", "oauth", false},
		{"http://127.0.0.1:9000/mcp", "none", true},
		{"http://localhost:9000/mcp", "header", true},
		{"http://localhost:9000/mcp", "static_header", true},
		{"ftp://x.example/mcp", "none", false},
		{"mcp.example.com", "none", false},
		{"https://user:pw@x.example/mcp", "none", false},
		{"https://x.example/mcp#frag", "none", false},
		{"", "none", false},
		{"http://", "none", false},
	}
	for _, c := range cases {
		if err := admin.ValidateServerURL(c.url, c.kind); (err == nil) != c.ok {
			t.Errorf("ValidateServerURL(%q, %s) = %v, want ok=%v", c.url, c.kind, err, c.ok)
		}
	}
}

func TestParseLevel(t *testing.T) {
	for in, ok := range map[string]bool{"none": true, "read": true, "write": true, "destructive": true, "admin": false, "": false, "READ": false} {
		if _, err := admin.ParseLevel(in, true); (err == nil) != ok {
			t.Errorf("ParseLevel(%q, true) = %v", in, err)
		}
	}
	if _, err := admin.ParseLevel("none", false); err == nil {
		t.Error("none accepted where not allowed")
	}
}

func TestValidateHeaderAndEnvNames(t *testing.T) {
	for name, ok := range map[string]bool{"X-Api-Key": true, "Authorization": true, "": false, "Bad Header": false, "X:Y": false, "a\r\nb": false} {
		if err := admin.ValidateHeaderName(name); (err == nil) != ok {
			t.Errorf("ValidateHeaderName(%q) = %v", name, err)
		}
	}
	for name, ok := range map[string]bool{"MY_TOKEN": true, "_x1": true, "1X": false, "A-B": false, "": false, "A=B": false} {
		if err := admin.ValidateEnvName(name); (err == nil) != ok {
			t.Errorf("ValidateEnvName(%q) = %v", name, err)
		}
	}
}

func TestValidateCategory(t *testing.T) {
	for in, ok := range map[string]bool{"dev": true, "dev-tools": true, "a_1": true, "Dev Tools": false, "1dev": false, "": false, strings.Repeat("a", 33): false} {
		if err := admin.ValidateCategory(in); (err == nil) != ok {
			t.Errorf("ValidateCategory(%q) = %v", in, err)
		}
	}
}

func TestValidateHelpURL(t *testing.T) {
	for in, ok := range map[string]bool{
		"https://docs.example.com/token": true, "http://docs.example.com": false, "https://u:p@x.example": false,
		"": false, "ftp://x.example": false, "https://x.example/" + strings.Repeat("a", 2048): false,
	} {
		if err := admin.ValidateHelpURL(in); (err == nil) != ok {
			t.Errorf("ValidateHelpURL(%.40q) = %v", in, err)
		}
	}
}

func TestNormalizeHashPrefix(t *testing.T) {
	if got, err := admin.NormalizeHashPrefix(" ABCDEF012345 "); err != nil || got != "abcdef012345" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := admin.NormalizeHashPrefix(""); err != nil || got != "" {
		t.Fatalf("empty: %q, %v", got, err)
	}
	for _, bad := range []string{"abc", "zzzzzzzzzzzz", strings.Repeat("a", 65)} {
		if _, err := admin.NormalizeHashPrefix(bad); err == nil {
			t.Errorf("NormalizeHashPrefix(%q) accepted", bad)
		}
	}
}

func TestInvalidErrorFields(t *testing.T) {
	var ie *admin.InvalidError
	if err := admin.ValidateSlug("Bad"); !errors.As(err, &ie) || ie.Field != "slug" {
		t.Fatalf("slug error = %#v", err)
	}
	if _, err := admin.ValidateAuthKind("x"); !errors.As(err, &ie) || ie.Field != "auth_kind" {
		t.Fatalf("auth_kind error = %#v", err)
	}
}
