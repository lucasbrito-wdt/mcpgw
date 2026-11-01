package admin_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

func portainerInput() admin.AddServerInput {
	return admin.AddServerInput{Slug: "portainer", Transport: "stdio", Stdio: &admin.StdioInput{
		Image: "portainer/portainer-mcp:2.45.3", Network: true, CredentialEnv: "PORTAINER_API_KEY",
		Env: map[string]string{"PORTAINER_URL": "https://manager.example", "PORTAINER_MCP_TRANSPORT": "stdio"},
	}}
}

func TestAddStdioServerWithCredentialEnv(t *testing.T) {
	h := newHarness(t)
	s, err := h.svc.AddServer(ctx, portainerInput())
	if err != nil {
		t.Fatal(err)
	}
	if s.AuthKind != "header" || s.CredentialEnv != "PORTAINER_API_KEY" {
		t.Fatalf("summary = %+v", s)
	}
	d, err := h.svc.GetServer(ctx, "portainer")
	if err != nil {
		t.Fatal(err)
	}
	if !d.StdioNetwork || d.CredentialEnv != "PORTAINER_API_KEY" ||
		!slices.Equal(d.StdioEnvNames, []string{"PORTAINER_MCP_TRANSPORT", "PORTAINER_URL"}) {
		t.Fatalf("detail = %+v", d)
	}
}

func TestAddStdioServerCredentialEnvValidation(t *testing.T) {
	cases := map[string]struct {
		field  string
		mutate func(*admin.AddServerInput)
	}{
		"collides with fixed env": {"credential_env", func(in *admin.AddServerInput) { in.Stdio.Env["PORTAINER_API_KEY"] = "x" }},
		"lowercase name":          {"credential_env", func(in *admin.AddServerInput) { in.Stdio.CredentialEnv = "portainer_key" }},
		"too long name":           {"credential_env", func(in *admin.AddServerInput) { in.Stdio.CredentialEnv = "K" + strings.Repeat("A", 64) }},
		"reserved docker name":    {"credential_env", func(in *admin.AddServerInput) { in.Stdio.CredentialEnv = "DOCKER_HOST" }},
		"bad fixed env name":      {"stdio_env", func(in *admin.AddServerInput) { in.Stdio.Env["lower"] = "x" }},
		"auth none":               {"auth_kind", func(in *admin.AddServerInput) { in.AuthKind = "none" }},
		"auth oauth":              {"auth_kind", func(in *admin.AddServerInput) { in.AuthKind = "oauth" }},
		"header name on stdio":    {"header", func(in *admin.AddServerInput) { in.Header = &admin.HeaderConfig{Name: "Authorization"} }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			in := portainerInput()
			c.mutate(&in)
			_, err := h.svc.AddServer(ctx, in)
			var ie *admin.InvalidError
			if !errors.As(err, &ie) || ie.Field != c.field {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestAddStdioHeaderWithoutCredentialEnvIsInvalid(t *testing.T) {
	h := newHarness(t)
	in := portainerInput()
	in.Stdio.CredentialEnv, in.AuthKind = "", "header"
	if _, err := h.svc.AddServer(ctx, in); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestAddStdioNetworkOnlyWithDocker(t *testing.T) {
	h := newHarness(t)
	svc := admin.New(admin.Options{Pool: h.pool, AllowHostLauncher: true})
	_, err := svc.AddServer(ctx, admin.AddServerInput{Slug: "local", Transport: "stdio", AuthKind: "none",
		Stdio: &admin.StdioInput{Launcher: "host", Command: "/usr/bin/true", Network: true}})
	if !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestAddStdioCredentialEnvAcceptsHelpURL(t *testing.T) {
	h := newHarness(t)
	in := portainerInput()
	in.Header = &admin.HeaderConfig{FormatHint: "ptr_", HelpURL: "https://docs.portainer.io/api/access"}
	if _, err := h.svc.AddServer(ctx, in); err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.GetServer(ctx, "portainer")
	if err != nil || d.Header == nil || d.Header.FormatHint != "ptr_" || d.Header.HelpURL == "" {
		t.Fatalf("detail = %+v, %v", d.Header, err)
	}
}

func TestProbeStdioServerWithoutCredentialDoesNotSpawn(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, portainerInput()); err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.ProbeServer(ctx, "portainer")
	if err != nil || res.OK || !res.RequiresAuth || res.Error == nil || !strings.Contains(*res.Error, "credencial") {
		t.Fatalf("probe = %+v, %v", res, err)
	}
}

func TestSyncStdioServerWithoutCredentialIsInvalid(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, portainerInput()); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Sync(ctx, "portainer", "")
	var ie *admin.InvalidError
	if !errors.As(err, &ie) || ie.Field != "credential" {
		t.Fatalf("err = %v", err)
	}
}
