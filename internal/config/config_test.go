package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validKey() string { return base64.StdEncoding.EncodeToString(make([]byte, 32)) }

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{EnvDatabaseURL: "postgres://x/y"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != DefaultListenAddr || cfg.PublicURL != DefaultPublicURL {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.MasterKey != nil {
		t.Fatal("master key should be nil when unset")
	}
	if err := cfg.RequireMasterKey(); err == nil {
		t.Fatal("RequireMasterKey should fail without a key")
	}
}

func TestLoadFull(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		EnvDatabaseURL: "postgres://x/y",
		EnvListenAddr:  "0.0.0.0:9000",
		EnvPublicURL:   "https://gw.example.com/",
		EnvMasterKey:   validKey(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://gw.example.com" {
		t.Fatalf("public url not normalized: %q", cfg.PublicURL)
	}
	if err := cfg.RequireMasterKey(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"missing database url":  {},
		"bad listen addr":       {EnvDatabaseURL: "p", EnvListenAddr: "nope"},
		"public url with path":  {EnvDatabaseURL: "p", EnvPublicURL: "http://a/b"},
		"public url not http":   {EnvDatabaseURL: "p", EnvPublicURL: "ftp://a"},
		"master key not base64": {EnvDatabaseURL: "p", EnvMasterKey: "!!!"},
		"master key wrong size": {EnvDatabaseURL: "p", EnvMasterKey: base64.StdEncoding.EncodeToString([]byte("short"))},
		"pprof on all ifaces":   {EnvDatabaseURL: "p", EnvPprofAddr: ":6060"},
		"pprof on public ip":    {EnvDatabaseURL: "p", EnvPprofAddr: "0.0.0.0:6060"},
		"pprof without port":    {EnvDatabaseURL: "p", EnvPprofAddr: "127.0.0.1"},
		"unknown engine":        {EnvDatabaseURL: "p", EnvSandboxEngine: "v8"},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(m)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadSandboxEngine(t *testing.T) {
	for in, want := range map[string]string{"": EngineQJS, "qjs": EngineQJS, "monty": EngineMonty, " Monty ": EngineMonty} {
		cfg, err := Load(env(map[string]string{EnvDatabaseURL: "p", EnvSandboxEngine: in}))
		if err != nil || cfg.SandboxEngine != want {
			t.Fatalf("%q: engine=%v err=%v, want %s", in, cfg, err, want)
		}
	}
}

func TestLoadPprofLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:6060", "[::1]:6060", "localhost:6060"} {
		cfg, err := Load(env(map[string]string{EnvDatabaseURL: "p", EnvPprofAddr: addr}))
		if err != nil || cfg.PprofAddr != addr {
			t.Fatalf("%s: cfg=%v err=%v", addr, cfg, err)
		}
	}
}

func TestLoadReportsAllProblems(t *testing.T) {
	_, err := Load(env(map[string]string{EnvListenAddr: "nope", EnvMasterKey: "!!!"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{EnvDatabaseURL, EnvListenAddr, EnvMasterKey} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}
