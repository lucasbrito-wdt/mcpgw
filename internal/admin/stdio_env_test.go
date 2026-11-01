package admin_test

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// openFixedEnv reads back the sealed fixed environment of a server straight from the database.
func openFixedEnv(h *harness, slug string) map[string]string {
	h.t.Helper()
	var id string
	var sealed []byte
	if err := h.pool.QueryRow(ctx, `SELECT id::text, stdio_env_ciphertext FROM upstream_servers WHERE slug = $1`, slug).Scan(&id, &sealed); err != nil {
		h.t.Fatal(err)
	}
	env, err := upstream.OpenStdioEnv(h.vault, id, sealed)
	if err != nil {
		h.t.Fatal(err)
	}
	return env
}

func TestSetStdioEnvMergesAndUnsets(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, portainerInput()); err != nil {
		t.Fatal(err)
	}
	set := map[string]string{"PORTAINER_TLS_VERIFY": "1", "PORTAINER_URL": "https://new.example"}
	if err := h.svc.SetStdioEnv(ctx, "portainer", set, []string{"PORTAINER_MCP_TRANSPORT", "NEVER_SET"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PORTAINER_TLS_VERIFY": "1", "PORTAINER_URL": "https://new.example"}
	if got := openFixedEnv(h, "portainer"); !maps.Equal(got, want) {
		t.Fatalf("env names = %v", slices.Sorted(maps.Keys(got)))
	}
	d, _ := h.svc.GetServer(ctx, "portainer")
	if !slices.Equal(d.StdioEnvNames, []string{"PORTAINER_TLS_VERIFY", "PORTAINER_URL"}) {
		t.Fatalf("detail names = %v", d.StdioEnvNames)
	}
	// Keys not mentioned are kept; removing the last ones clears the ciphertext.
	if err := h.svc.SetStdioEnv(ctx, "portainer", map[string]string{"EXTRA": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetStdioEnv(ctx, "portainer", nil, []string{"PORTAINER_TLS_VERIFY", "PORTAINER_URL", "EXTRA"}); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT count(*) FROM upstream_servers WHERE slug = 'portainer' AND stdio_env_ciphertext IS NULL`); n != 1 {
		t.Fatal("empty environment left a ciphertext")
	}
}

func TestSetStdioEnvAuditsNamesOnly(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, portainerInput()); err != nil {
		t.Fatal(err)
	}
	err := h.svc.SetStdioEnv(ctx, "portainer", map[string]string{"PORTAINER_TIMEOUT": "very-secret-120", "A_FIRST": "v"}, []string{"PORTAINER_MCP_TRANSPORT"})
	if err != nil {
		t.Fatal(err)
	}
	ev := h.events[len(h.events)-1]
	if ev.Action != "server.set_stdio_env" || ev.Target != "portainer" ||
		!slices.Equal(ev.Details["set"].([]string), []string{"A_FIRST", "PORTAINER_TIMEOUT"}) ||
		!slices.Equal(ev.Details["unset"].([]string), []string{"PORTAINER_MCP_TRANSPORT"}) {
		t.Fatalf("audit = %+v", ev)
	}
	if strings.Contains(fmtDetails(ev), "very-secret-120") {
		t.Fatal("audit carries a value")
	}
}

func fmtDetails(ev admin.Event) string {
	var b strings.Builder
	for k, v := range ev.Details {
		b.WriteString(k)
		b.WriteString(strings.Join(v.([]string), ","))
	}
	return b.String()
}

func TestSetStdioEnvValidation(t *testing.T) {
	long := strings.Repeat("x", 4097)
	cases := map[string]struct {
		set   map[string]string
		unset []string
	}{
		"lowercase name":    {set: map[string]string{"lower": "v"}},
		"empty value":       {set: map[string]string{"A": ""}},
		"newline":           {set: map[string]string{"A": "a\nb"}},
		"carriage return":   {set: map[string]string{"A": "a\rb"}},
		"too long":          {set: map[string]string{"A": long}},
		"credential name":   {set: map[string]string{"PORTAINER_API_KEY": "v"}},
		"reserved":          {set: map[string]string{"DOCKER_HOST": "v"}},
		"dangerous":         {set: map[string]string{"LD_PRELOAD": "v"}},
		"set and unset":     {set: map[string]string{"A": "v"}, unset: []string{"A"}},
		"invalid unset":     {unset: []string{"bad name"}},
		"nothing to change": {},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if _, err := h.svc.AddServer(ctx, portainerInput()); err != nil {
				t.Fatal(err)
			}
			var ie *admin.InvalidError
			err := h.svc.SetStdioEnv(ctx, "portainer", c.set, c.unset)
			if !errors.As(err, &ie) || ie.Field != "stdio_env" {
				t.Fatalf("err = %v", err)
			}
			if got := openFixedEnv(h, "portainer"); len(got) != 2 {
				t.Fatalf("rejected change was applied: %d names", len(got))
			}
		})
	}
}

func TestSetStdioEnvAcceptsConnectionEnvName(t *testing.T) {
	h := newHarness(t)
	in := portainerInput()
	in.Stdio.ConnectionEnv = []string{"PORTAINER_TLS_VERIFY"}
	if _, err := h.svc.AddServer(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetStdioEnv(ctx, "portainer", map[string]string{"PORTAINER_TLS_VERIFY": "1"}, nil); err != nil {
		t.Fatalf("default for a per-connection name rejected: %v", err)
	}
}

func TestSetStdioEnvOnlyStdioAndExisting(t *testing.T) {
	h := newHarness(t)
	h.server("web")
	var ie *admin.InvalidError
	if err := h.svc.SetStdioEnv(ctx, "web", map[string]string{"A": "v"}, nil); !errors.As(err, &ie) {
		t.Fatalf("http server accepted env: %v", err)
	}
	if err := h.svc.SetStdioEnv(ctx, "missing", map[string]string{"A": "v"}, nil); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("missing server: %v", err)
	}
}

func TestSetStdioEnvOnServerWithoutEnv(t *testing.T) {
	h := newHarness(t)
	in := portainerInput()
	in.Stdio.Env = nil
	if _, err := h.svc.AddServer(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetStdioEnv(ctx, "portainer", map[string]string{"A": "v"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := openFixedEnv(h, "portainer"); !maps.Equal(got, map[string]string{"A": "v"}) {
		t.Fatalf("env = %v", got)
	}
}

func TestSetCredentialEnvDropsSameNameFromFixedEnv(t *testing.T) {
	h := newHarness(t)
	in := portainerInput()
	in.AuthKind, in.Stdio.CredentialEnv = "none", ""
	in.Stdio.Env["PORTAINER_API_KEY"] = "shared-secret"
	if _, err := h.svc.AddServer(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetCredentialEnv(ctx, "portainer", "PORTAINER_API_KEY"); err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.GetServer(ctx, "portainer")
	if err != nil {
		t.Fatal(err)
	}
	if d.CredentialEnv != "PORTAINER_API_KEY" || d.AuthKind != "header" {
		t.Fatalf("detail = %+v", d.ServerSummary)
	}
	want := map[string]string{"PORTAINER_URL": "https://manager.example", "PORTAINER_MCP_TRANSPORT": "stdio"}
	if got := openFixedEnv(h, "portainer"); !maps.Equal(got, want) {
		t.Fatalf("fixed env names = %v", slices.Sorted(maps.Keys(got)))
	}
	ev := h.events[len(h.events)-1]
	if ev.Action != "server.set_credential_env" || ev.Details["credential_env"] != "PORTAINER_API_KEY" || ev.Details["removed_fixed"] != true {
		t.Fatalf("audit = %+v", ev)
	}
	if strings.Contains(fmt.Sprint(ev.Details), "shared-secret") {
		t.Fatal("audit carries a value")
	}
}

func TestSetCredentialEnvRejectsHTTPServer(t *testing.T) {
	h := newHarness(t)
	h.server("remote")
	err := h.svc.SetCredentialEnv(ctx, "remote", "SOME_KEY")
	var inv *admin.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("err = %v", err)
	}
}
