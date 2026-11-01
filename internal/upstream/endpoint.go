package upstream

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// TransportKind selects how the gateway talks to an upstream.
type TransportKind string

const (
	// TransportAuto probes Streamable HTTP first and falls back to legacy SSE.
	TransportAuto       TransportKind = "auto"
	TransportStreamable TransportKind = "streamable_http"
	TransportSSE        TransportKind = "sse"
	TransportStdio      TransportKind = "stdio"
)

// Launcher selects how a stdio server process is started.
type Launcher string

const (
	// LauncherDocker runs the server in a hardened, throwaway container (the default).
	LauncherDocker Launcher = "docker"
	// LauncherHost runs the server as a direct host process; only with an explicit admin opt-in.
	LauncherHost Launcher = "host"
)

// StdioKeyPrefix prefixes the endpoint key (the upstream_servers.url column) of stdio servers.
const StdioKeyPrefix = "stdio://"

// StdioSpec describes a stdio server process. Env is the complete environment the server sees:
// nothing is inherited from the gateway.
type StdioSpec struct {
	Launcher Launcher
	// Command is the executable (absolute path) for LauncherHost, or an optional command that
	// overrides the image entrypoint arguments for LauncherDocker.
	Command string
	Args    []string
	Env     map[string]string
	// Image is the container image for LauncherDocker.
	Image string
	// Network lets a docker server reach the network; without it the container has none.
	Network bool
}

// Detection is the transport and protocol era found for an upstream, remembered so that a new
// session does not re-probe.
type Detection struct {
	Transport       TransportKind
	ProtocolVersion string
}

// Endpoint is everything needed to open a session with one upstream.
type Endpoint struct {
	Transport TransportKind
	URL       string
	Stdio     *StdioSpec
	Detected  Detection
}

// Resolver maps the endpoint key callers pass (the server URL, or StdioKeyPrefix + server id)
// to an Endpoint, and persists detection results.
type Resolver interface {
	Resolve(ctx context.Context, key string) (Endpoint, error)
	RecordDetection(ctx context.Context, key string, d Detection) error
}

// ErrUnknownEndpoint is returned when a key cannot be resolved to an upstream.
var ErrUnknownEndpoint = errors.New("unknown upstream endpoint")

// urlResolver serves plain http(s) keys with auto-detection and no persistence.
type urlResolver struct{}

func (urlResolver) Resolve(_ context.Context, key string) (Endpoint, error) {
	if !isHTTPURL(key) {
		return Endpoint{}, fmt.Errorf("%w: only http(s) URLs are supported without a resolver", ErrUnknownEndpoint)
	}
	return Endpoint{Transport: TransportAuto, URL: key}, nil
}

func (urlResolver) RecordDetection(context.Context, string, Detection) error { return nil }

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

var (
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// Image references: no leading dash (flag injection), registry/name[:tag][@digest].
	imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)
)

// launcherReservedEnv are variables the docker CLI itself reads; a server env must not set them
// because docker passes server variables through its own environment (-e NAME, no value on argv).
var launcherReservedEnv = map[string]bool{
	"PATH": true, "HOME": true, "DOCKER_HOST": true, "DOCKER_CONTEXT": true, "DOCKER_CONFIG": true,
	"DOCKER_CERT_PATH": true, "DOCKER_TLS_VERIFY": true, "DOCKER_API_VERSION": true,
}

const (
	maxStdioArgs     = 64
	maxStdioEnv      = 64
	maxStdioValueLen = 8192
)

// Validate checks a stdio spec before it is stored or launched.
func (s StdioSpec) Validate() error {
	switch s.Launcher {
	case LauncherDocker:
		if !imagePattern.MatchString(s.Image) {
			return errors.New("stdio: invalid docker image reference")
		}
	case LauncherHost:
		if s.Image != "" || s.Network {
			return errors.New("stdio: image and network apply to the docker launcher only")
		}
		if !filepath.IsAbs(s.Command) {
			return errors.New("stdio: host command must be an absolute path")
		}
	default:
		return fmt.Errorf("stdio: unknown launcher %q", s.Launcher)
	}
	if len(s.Args) > maxStdioArgs || len(s.Env) > maxStdioEnv {
		return errors.New("stdio: too many arguments or environment variables")
	}
	if !safeValue(s.Command) {
		return errors.New("stdio: invalid command")
	}
	for _, a := range s.Args {
		if !safeValue(a) {
			return errors.New("stdio: invalid argument")
		}
	}
	return validateEnv(s.Launcher, s.Env)
}

func validateEnv(l Launcher, env map[string]string) error {
	for k, v := range env {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("stdio: invalid environment variable name %q", k)
		}
		if l == LauncherDocker && (launcherReservedEnv[k] || strings.HasPrefix(k, "DOCKER_")) {
			return fmt.Errorf("stdio: environment variable %s is reserved by the docker launcher", k)
		}
		if dangerousEnvName(k) {
			return fmt.Errorf("stdio: environment variable %s is not allowed", k)
		}
		if !safeValue(v) {
			return fmt.Errorf("stdio: invalid value for environment variable %s", k)
		}
	}
	return nil
}

// dangerousEnvName reports variables that change how the launched process (the docker CLI
// included, since server variables travel through its environment) loads code, routes traffic
// or reaches the gateway's agents.
func dangerousEnvName(name string) bool {
	n := strings.ToUpper(name)
	return n == "LD_PRELOAD" || n == "LD_LIBRARY_PATH" || n == "SSH_AUTH_SOCK" ||
		strings.HasPrefix(n, "DYLD_") || strings.HasSuffix(n, "_PROXY")
}

func safeValue(s string) bool {
	return len(s) <= maxStdioValueLen && !strings.ContainsRune(s, 0)
}
