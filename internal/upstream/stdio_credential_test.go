package upstream_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const credentialVar = "PORTAINER_API_KEY"

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func callAs(t *testing.T, c *upstream.Client, ctx context.Context, key string, cred upstream.Credential, tool, args string) string {
	t.Helper()
	res, err := c.CallTool(ctx, key, cred, tool, []byte(args))
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var text string
	if err := json.Unmarshal(res.Value, &text); err != nil {
		return string(res.Value)
	}
	return text
}

func TestStdioCredentialEnvIsPerUserAndProfile(t *testing.T) {
	c, key := stdioClient(t)
	alice := upstream.WithOwner(context.Background(), "alice")
	bob := upstream.WithOwner(context.Background(), "bob")
	aliceDefault := upstream.EnvCredential{Name: credentialVar, Value: "alice-default-key"}
	aliceOps := upstream.EnvCredential{Name: credentialVar, Value: "alice-ops-key-0000"}
	bobDefault := upstream.EnvCredential{Name: credentialVar, Value: "bob-default-key-00"}

	type call struct {
		ctx  context.Context
		cred upstream.EnvCredential
	}
	pids := map[int]string{}
	for _, cl := range []call{{alice, aliceDefault}, {alice, aliceOps}, {bob, bobDefault}} {
		args := `{"name":"` + credentialVar + `"}`
		if got := callAs(t, c, cl.ctx, key, cl.cred, "env_hash", args); got != sha(cl.cred.Value) {
			t.Fatalf("%s: process saw another credential", cl.cred.Value)
		}
		pid, err := strconv.Atoi(callAs(t, c, cl.ctx, key, cl.cred, "pid", "{}"))
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := pids[pid]; dup {
			t.Fatalf("%s and %s shared process %d", prev, cl.cred.Value, pid)
		}
		pids[pid] = cl.cred.Value
	}
	// Same owner and credential reuses the warm process.
	again, _ := strconv.Atoi(callAs(t, c, alice, key, aliceDefault, "pid", "{}"))
	if pids[again] != aliceDefault.Value {
		t.Fatal("same credential did not reuse its process")
	}
}

func TestStdioCredentialIsScrubbedFromResults(t *testing.T) {
	c, key := stdioClient(t)
	cred := upstream.EnvCredential{Name: credentialVar, Value: "secret-portainer-key"}
	got := callAs(t, c, context.Background(), key, cred, "echo", `{"text":"secret-portainer-key"}`)
	if strings.Contains(got, cred.Value) {
		t.Fatalf("credential echoed back: %s", got)
	}
}

func TestStdioCredentialCannotOverrideFixedEnv(t *testing.T) {
	res := newTestResolver()
	spec := stdioSpec(t, "modern", map[string]string{credentialVar: "fixed"})
	key := res.add("stdio://fixed", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	c := upstream.New(upstream.WithResolver(res), upstream.WithTerminateGrace(200*time.Millisecond))
	defer c.Close()
	cred := upstream.EnvCredential{Name: credentialVar, Value: "user-key-12345"}
	if _, err := c.CallTool(context.Background(), key, cred, "pid", nil); err == nil {
		t.Fatal("credential overrode a fixed variable")
	}
}

func TestStdioRejectsHeaderCredential(t *testing.T) {
	c, key := stdioClient(t)
	if _, err := c.CallTool(context.Background(), key, upstream.BearerToken("tok-12345678"), "pid", nil); err == nil {
		t.Fatal("stdio server accepted an HTTP credential")
	}
}
