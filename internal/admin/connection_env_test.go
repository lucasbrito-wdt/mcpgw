package admin_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

func TestAddStdioServerWithConnectionEnv(t *testing.T) {
	h := newHarness(t)
	in := portainerInput()
	in.Stdio.ConnectionEnv = []string{"PORTAINER_URL"} // also a fixed variable: it becomes the default
	if _, err := h.svc.AddServer(ctx, in); err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.GetServer(ctx, "portainer")
	if err != nil || !slices.Equal(d.ConnectionEnv, []string{"PORTAINER_URL"}) {
		t.Fatalf("detail = %+v, %v", d.ConnectionEnv, err)
	}
}

func TestConnectionEnvValidation(t *testing.T) {
	cases := map[string]func(*admin.AddServerInput){
		"credential name": func(in *admin.AddServerInput) { in.Stdio.ConnectionEnv = []string{"PORTAINER_API_KEY"} },
		"lowercase":       func(in *admin.AddServerInput) { in.Stdio.ConnectionEnv = []string{"portainer_url"} },
		"duplicate":       func(in *admin.AddServerInput) { in.Stdio.ConnectionEnv = []string{"A", "A"} },
		"reserved":        func(in *admin.AddServerInput) { in.Stdio.ConnectionEnv = []string{"DOCKER_HOST"} },
		"dangerous":       func(in *admin.AddServerInput) { in.Stdio.ConnectionEnv = []string{"LD_PRELOAD"} },
		"no credential_env": func(in *admin.AddServerInput) {
			in.Stdio.CredentialEnv, in.Stdio.ConnectionEnv = "", []string{"PORTAINER_URL"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			in := portainerInput()
			mutate(&in)
			if in.Stdio.CredentialEnv == "" {
				in.AuthKind = "none"
			}
			_, err := h.svc.AddServer(ctx, in)
			var ie *admin.InvalidError
			if !errors.As(err, &ie) || ie.Field != "connection_env" {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSetConnectionEnv(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, portainerInput()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetConnectionEnv(ctx, "portainer", []string{"PORTAINER_URL", "PORTAINER_TEAM"}); err != nil {
		t.Fatal(err)
	}
	d, _ := h.svc.GetServer(ctx, "portainer")
	if !slices.Equal(d.ConnectionEnv, []string{"PORTAINER_URL", "PORTAINER_TEAM"}) {
		t.Fatalf("connection_env = %v", d.ConnectionEnv)
	}
	ev := h.events[len(h.events)-1]
	if ev.Action != "server.set_connection_env" || ev.Target != "portainer" ||
		!slices.Equal(ev.Details["names"].([]string), []string{"PORTAINER_URL", "PORTAINER_TEAM"}) {
		t.Fatalf("audit = %+v", ev)
	}
	if err := h.svc.SetConnectionEnv(ctx, "portainer", nil); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.svc.GetServer(ctx, "portainer"); len(d.ConnectionEnv) != 0 {
		t.Fatalf("not cleared: %v", d.ConnectionEnv)
	}
	var ie *admin.InvalidError
	if err := h.svc.SetConnectionEnv(ctx, "portainer", []string{"PORTAINER_API_KEY"}); !errors.As(err, &ie) {
		t.Fatalf("credential name accepted: %v", err)
	}
	if err := h.svc.SetConnectionEnv(ctx, "missing", []string{"X"}); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("missing server: %v", err)
	}
}

func TestSetConnectionEnvRequiresStdioCredential(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "web", URL: "https://web.example/mcp", AuthKind: "none"}); err != nil {
		t.Fatal(err)
	}
	var ie *admin.InvalidError
	if err := h.svc.SetConnectionEnv(ctx, "web", []string{"X"}); !errors.As(err, &ie) {
		t.Fatalf("http server accepted connection_env: %v", err)
	}
}
