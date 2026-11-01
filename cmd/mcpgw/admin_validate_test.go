package main

import (
	"strings"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	for slug, ok := range map[string]bool{
		"github": true, "my_server2": true, "a": true, strings.Repeat("a", 63): true,
		"": false, "1abc": false, "Upper": false, "has-dash": false, "sp ace": false,
		strings.Repeat("a", 64): false, "callback": false, "success": false, "x'; drop": false,
	} {
		if err := validateSlug(slug); (err == nil) != ok {
			t.Errorf("validateSlug(%q) = %v, want ok=%v", slug, err, ok)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got, err := normalizeEmail("  Lucas@Example.COM "); err != nil || got != "lucas@example.com" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "no-at", "Name <a@b.co>", "a@b.co, c@d.co", strings.Repeat("a", 320) + "@x.co"} {
		if _, err := normalizeEmail(bad); err == nil {
			t.Errorf("normalizeEmail(%q) accepted", bad)
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
		{"http://localhost:9000/mcp", "static_header", true},
		{"ftp://x.example/mcp", "none", false},
		{"mcp.example.com", "none", false},
		{"https://user:pw@x.example/mcp", "none", false},
		{"https://x.example/mcp#frag", "none", false},
		{"", "none", false},
		{"http://", "none", false},
	}
	for _, c := range cases {
		if err := validateServerURL(c.url, c.kind); (err == nil) != c.ok {
			t.Errorf("validateServerURL(%q, %s) = %v, want ok=%v", c.url, c.kind, err, c.ok)
		}
	}
}

func TestParseLevel(t *testing.T) {
	for in, ok := range map[string]bool{"none": true, "read": true, "write": true, "destructive": true, "admin": false, "": false, "READ": false} {
		if _, err := parseLevel(in, true); (err == nil) != ok {
			t.Errorf("parseLevel(%q, true) = %v", in, err)
		}
	}
	if _, err := parseLevel("none", false); err == nil {
		t.Error("none accepted where not allowed")
	}
}

func TestValidateHeaderAndEnvNames(t *testing.T) {
	for name, ok := range map[string]bool{"X-Api-Key": true, "Authorization": true, "": false, "Bad Header": false, "X:Y": false, "a\r\nb": false} {
		if err := validateHeaderName(name); (err == nil) != ok {
			t.Errorf("validateHeaderName(%q) = %v", name, err)
		}
	}
	for name, ok := range map[string]bool{"MY_TOKEN": true, "_x1": true, "1X": false, "A-B": false, "": false, "A=B": false} {
		if err := validateEnvName(name); (err == nil) != ok {
			t.Errorf("validateEnvName(%q) = %v", name, err)
		}
	}
}

func TestFindAdminCommand(t *testing.T) {
	if _, err := findAdminCommand([]string{"user", "list"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"user"}, {"user", "drop"}, {"nope", "list"}} {
		if _, err := findAdminCommand(args); err == nil {
			t.Errorf("findAdminCommand(%v) accepted", args)
		}
	}
}
