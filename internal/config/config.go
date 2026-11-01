// Package config loads and validates gateway configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	EnvDatabaseURL   = "MCPGW_DATABASE_URL"
	EnvListenAddr    = "MCPGW_LISTEN_ADDR"
	EnvPublicURL     = "MCPGW_PUBLIC_URL"
	EnvMasterKey     = "MCPGW_MASTER_KEY"
	EnvPprofAddr     = "MCPGW_PPROF_ADDR"
	EnvSandboxEngine = "MCPGW_SANDBOX_ENGINE"

	// EngineQJS is the QuickJS (JavaScript) sandbox, the default; EngineMonty runs Python in the montyrs worker.
	EngineQJS   = "qjs"
	EngineMonty = "monty"

	DefaultListenAddr = "127.0.0.1:8787"
	DefaultPublicURL  = "http://localhost:8787"

	masterKeyBytes = 32
)

// Config is the validated process configuration.
type Config struct {
	DatabaseURL string
	ListenAddr  string
	// PublicURL is the externally visible origin, without a trailing slash.
	PublicURL string
	// MasterKey is the 32-byte vault key; nil when unset (see RequireMasterKey).
	MasterKey []byte
	// PprofAddr, when set, serves net/http/pprof on this loopback address (profiling only).
	PprofAddr string
	// SandboxEngine is EngineQJS or EngineMonty.
	SandboxEngine string
}

// Load reads configuration through getenv (os.Getenv in production) and validates it.
// All problems are reported together.
func Load(getenv func(string) string) (*Config, error) {
	cfg := &Config{
		DatabaseURL: strings.TrimSpace(getenv(EnvDatabaseURL)),
		ListenAddr:  valueOr(getenv(EnvListenAddr), DefaultListenAddr),
		PublicURL:   valueOr(getenv(EnvPublicURL), DefaultPublicURL),
		PprofAddr:   strings.TrimSpace(getenv(EnvPprofAddr)),

		SandboxEngine: strings.ToLower(valueOr(getenv(EnvSandboxEngine), EngineQJS)),
	}
	var errs []error
	if cfg.SandboxEngine != EngineQJS && cfg.SandboxEngine != EngineMonty {
		errs = append(errs, fmt.Errorf("%s: must be %q or %q, got %q", EnvSandboxEngine, EngineQJS, EngineMonty, cfg.SandboxEngine))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, fmt.Errorf("%s is required", EnvDatabaseURL))
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddr); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", EnvListenAddr, err))
	}
	if cfg.PprofAddr != "" && !isLoopbackAddr(cfg.PprofAddr) {
		errs = append(errs, fmt.Errorf("%s: must be a loopback host:port (profiles expose memory)", EnvPprofAddr))
	}
	publicURL, err := normalizePublicURL(cfg.PublicURL)
	if err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", EnvPublicURL, err))
	}
	cfg.PublicURL = publicURL

	if raw := strings.TrimSpace(getenv(EnvMasterKey)); raw != "" {
		key, err := decodeMasterKey(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", EnvMasterKey, err))
		}
		cfg.MasterKey = key
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return cfg, nil
}

// RequireMasterKey fails when no master key was configured; call it before serving.
func (c *Config) RequireMasterKey() error {
	if len(c.MasterKey) == 0 {
		return fmt.Errorf("%s is required (base64 of %d random bytes)", EnvMasterKey, masterKeyBytes)
	}
	return nil
}

func valueOr(v, fallback string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return fallback
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func normalizePublicURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return raw, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return raw, errors.New("must be an absolute http(s) URL")
	}
	if u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return raw, errors.New("must be an origin without path, query or fragment")
	}
	return u.Scheme + "://" + u.Host, nil
}

func decodeMasterKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("not valid base64")
	}
	if len(key) != masterKeyBytes {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", masterKeyBytes, len(key))
	}
	return key, nil
}
