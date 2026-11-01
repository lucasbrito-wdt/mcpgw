package upstream_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const urlVar = "PORTAINER_URL"

// portainerClient serves a stdio server whose fixed environment carries a default PORTAINER_URL.
func portainerClient(t *testing.T) (*upstream.Client, string) {
	t.Helper()
	res := newTestResolver()
	spec := stdioSpec(t, "modern", map[string]string{urlVar: "https://default.example"})
	key := res.add("stdio://portainer", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	c := upstream.New(upstream.WithResolver(res), upstream.WithTerminateGrace(200*time.Millisecond))
	t.Cleanup(c.Close)
	return c, key
}

func TestConnectionEnvSeparatesProcessesPerProfile(t *testing.T) {
	c, key := portainerClient(t)
	alice := upstream.WithOwner(context.Background(), "alice")
	acme := upstream.EnvCredential{Name: credentialVar, Value: "key-acme-000001",
		Env: map[string]string{urlVar: "https://acme.example"}}
	globex := upstream.EnvCredential{Name: credentialVar, Value: "key-globex-0001",
		Env: map[string]string{urlVar: "https://globex.example"}}
	pids := map[int]bool{}
	for _, cred := range []upstream.EnvCredential{acme, globex} {
		if got := callAs(t, c, alice, key, cred, "env_hash", `{"name":"`+urlVar+`"}`); got != sha(cred.Env[urlVar]) {
			t.Fatalf("%s: process saw another URL", cred.Env[urlVar])
		}
		if got := callAs(t, c, alice, key, cred, "env_hash", `{"name":"`+credentialVar+`"}`); got != sha(cred.Value) {
			t.Fatalf("%s: process saw another token", cred.Env[urlVar])
		}
		pid, err := strconv.Atoi(callAs(t, c, alice, key, cred, "pid", "{}"))
		if err != nil {
			t.Fatal(err)
		}
		pids[pid] = true
	}
	if len(pids) != 2 {
		t.Fatal("two connections with different URLs shared a process")
	}
	// Same token, different URL: still a separate process.
	sameToken := upstream.EnvCredential{Name: credentialVar, Value: acme.Value, Env: map[string]string{urlVar: "https://other.example"}}
	pid, _ := strconv.Atoi(callAs(t, c, alice, key, sameToken, "pid", "{}"))
	if pids[pid] {
		t.Fatal("pool key ignores the connection environment")
	}
}

func TestConnectionEnvFallsBackToFixedDefault(t *testing.T) {
	c, key := portainerClient(t)
	cred := upstream.EnvCredential{Name: credentialVar, Value: "key-default-0001"}
	if got := callAs(t, c, context.Background(), key, cred, "env_hash", `{"name":"`+urlVar+`"}`); got != sha("https://default.example") {
		t.Fatal("empty connection value did not fall back to the fixed variable")
	}
}

func TestConnectionEnvCannotSetCredentialVariable(t *testing.T) {
	c, key := portainerClient(t)
	cred := upstream.EnvCredential{Name: credentialVar, Value: "key-0000000001", Env: map[string]string{credentialVar: "other"}}
	if _, err := c.CallTool(context.Background(), key, cred, "pid", nil); err == nil {
		t.Fatal("connection environment overrode the credential variable")
	}
}

func TestInvalidateStdioRespawnsWithNewFixedEnv(t *testing.T) {
	res := newTestResolver()
	spec := stdioSpec(t, "modern", map[string]string{urlVar: "https://old.example"})
	key := res.add("stdio://portainer", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	c := upstream.New(upstream.WithResolver(res), upstream.WithTerminateGrace(200*time.Millisecond))
	t.Cleanup(c.Close)
	cred := upstream.EnvCredential{Name: credentialVar, Value: "key-respawn-0001"}
	seen := func() string {
		return callAs(t, c, context.Background(), key, cred, "env_hash", `{"name":"`+urlVar+`"}`)
	}
	if seen() != sha("https://old.example") {
		t.Fatal("first process saw another URL")
	}
	updated := stdioSpec(t, "modern", map[string]string{urlVar: "https://new.example"})
	res.add(key, upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &updated})
	if seen() != sha("https://old.example") {
		t.Fatal("a pooled process must keep its environment until invalidated")
	}
	c.InvalidateStdio("portainer")
	if seen() != sha("https://new.example") {
		t.Fatal("invalidated server did not respawn with the new environment")
	}
}

func TestInvalidateStdioSpares(t *testing.T) {
	res := newTestResolver()
	spec := stdioSpec(t, "modern", nil)
	a := res.add("stdio://a", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	b := res.add("stdio://b", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	c := upstream.New(upstream.WithResolver(res), upstream.WithTerminateGrace(200*time.Millisecond))
	t.Cleanup(c.Close)
	pid := func(key string) string { return callAs(t, c, context.Background(), key, nil, "pid", "{}") }
	pa, pb := pid(a), pid(b)
	c.InvalidateStdio("a")
	if pid(b) != pb || pid(a) == pa {
		t.Fatal("InvalidateStdio(a) must restart only a")
	}
	pa, pb = pid(a), pid(b)
	c.InvalidateStdio("")
	if pid(a) == pa || pid(b) == pb {
		t.Fatal("InvalidateStdio(\"\") must restart every stdio server")
	}
}
