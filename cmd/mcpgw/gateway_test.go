package main

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

func TestServeEndToEndWiring(t *testing.T) {
	pool := storetest.New(t)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		DatabaseURL: pool.Config().ConnString(),
		ListenAddr:  "127.0.0.1:0",
		PublicURL:   "http://localhost:8787",
		MasterKey:   key,
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, slog.New(slog.DiscardHandler), ln) }()
	base := "http://" + ln.Addr().String()
	waitHealthy(t, base)

	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/oauth/client-metadata.json",
	} {
		resp := get(t, base+path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	resp, err := http.Post(base+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	challenge := resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(challenge, "resource_metadata=") {
		t.Errorf("POST /mcp without token = %d, WWW-Authenticate %q", resp.StatusCode, challenge)
	}

	resp = get(t, base+"/connect/whatever")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /connect/x without session = %d, want 401", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "ok" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("gateway never became healthy")
}

func TestWarnLoggerDropsInfo(t *testing.T) {
	var sb strings.Builder
	log := warnLogger(slog.New(slog.NewJSONHandler(&sb, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.Info("noisy")
	log.With("k", "v").Warn("kept")
	out := sb.String()
	if strings.Contains(out, "noisy") || !strings.Contains(out, "kept") {
		t.Fatalf("log output = %q", out)
	}
}

func TestNewSandboxMontyChecksWorker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPGW_MONTYRS_WORKER", filepath.Join(dir, "missing"))
	if _, err := newSandbox(config.EngineMonty); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing worker: err = %v", err)
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCPGW_MONTYRS_WORKER", plain)
	if _, err := newSandbox(config.EngineMonty); err == nil || !strings.Contains(err.Error(), "not an executable") {
		t.Fatalf("non-executable worker: err = %v", err)
	}
}

func TestNewSandboxDefaultsToQJS(t *testing.T) {
	r, err := newSandbox(config.EngineQJS)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
}

func TestNewSandboxMontyResolvesRelativeWorker(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("plain", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"plain", "./plain", "absent"} {
		t.Setenv("MCPGW_MONTYRS_WORKER", rel)
		_, err := newSandbox(config.EngineMonty)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, strings.TrimPrefix(rel, "./"))) {
			t.Fatalf("%s: err = %v, want it to name the absolute path", rel, err)
		}
	}
}
