package upstream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Hardening applied to every docker-launched server.
const (
	dockerMemory = "512m"
	dockerPIDs   = "256"
	dockerUser   = "65534:65534"
	dockerTmpfs  = "/tmp:rw,noexec,nosuid,size=64m"

	defaultTerminateGrace = 2 * time.Second
	dockerRemoveTimeout   = 5 * time.Second
)

// dockerCLIEnv are the only gateway variables the docker CLI process receives (to find its
// binary helpers, config and daemon). The server inside the container never sees them.
var dockerCLIEnv = []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY"}

// stdioTransport starts one server process per Connect. The process is exec'd without a
// shell, with an explicit environment, and dies when the session closes.
type stdioTransport struct {
	spec       StdioSpec
	dockerPath string
	grace      time.Duration
	lineLimit  int
	onTooLarge func()
}

func (t *stdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	proc, stdout, err := startProcess(t.spec, t.dockerPath, t.grace, func(rc io.ReadCloser) io.ReadCloser {
		return newLineFilter(rc, t.lineLimit, sanitizeLine, t.onTooLarge)
	})
	if err != nil {
		return nil, err
	}
	// The line limit is enforced by the filter, so the SDK cap is disabled.
	return (&mcp.IOTransport{Reader: stdout, Writer: proc, MaxLineLength: -1}).Connect(ctx)
}

// stdioProcess owns a running server: writes go to its stdin, Close shuts it down.
type stdioProcess struct {
	cmd        *exec.Cmd
	stdin      *os.File
	exited     chan struct{}
	grace      time.Duration
	container  string
	dockerPath string
	closeOnce  sync.Once
}

func startProcess(spec StdioSpec, dockerPath string, grace time.Duration, wrap func(io.ReadCloser) io.ReadCloser) (*stdioProcess, io.ReadCloser, error) {
	if err := spec.Validate(); err != nil {
		return nil, nil, err
	}
	p := &stdioProcess{exited: make(chan struct{}), grace: grace, dockerPath: dockerPath}
	if p.grace <= 0 {
		p.grace = defaultTerminateGrace
	}
	argv0, args, env, err := p.commandLine(spec)
	if err != nil {
		return nil, nil, err
	}
	p.cmd = exec.Command(argv0, args...)
	p.cmd.Env = env
	// Not the gateway's CWD: that is where .env and other secrets files live.
	p.cmd.Dir = string(filepath.Separator)
	setProcessGroup(p.cmd)

	// Plain os pipes (not StdoutPipe) so that Wait never closes the read side under the reader.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW)
		return nil, nil, err
	}
	p.cmd.Stdin, p.cmd.Stdout = stdinR, stdoutW
	err = p.cmd.Start()
	closeAll(stdinR, stdoutW)
	if err != nil {
		closeAll(stdinW, stdoutR)
		return nil, nil, fmt.Errorf("start stdio server: %w", err)
	}
	p.stdin = stdinW
	go func() {
		_ = p.cmd.Wait()
		close(p.exited)
	}()
	return p, wrap(stdoutR), nil
}

func (p *stdioProcess) commandLine(spec StdioSpec) (argv0 string, args, env []string, err error) {
	serverEnv := make([]string, 0, len(spec.Env))
	for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
		serverEnv = append(serverEnv, k+"="+spec.Env[k])
	}
	if spec.Launcher == LauncherHost {
		return spec.Command, spec.Args, serverEnv, nil
	}
	if p.container, err = containerName(); err != nil {
		return "", nil, nil, err
	}
	env = serverEnv
	for _, k := range dockerCLIEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return p.dockerPath, dockerArgs(spec, p.container), env, nil
}

// dockerArgs builds the hardened `docker run` argv. Server variables are passed by name only
// (-e NAME), so their values never appear in the process list.
func dockerArgs(spec StdioSpec, name string) []string {
	args := []string{
		"run", "--rm", "-i", "--init", "--name", name, "--label", "io.mcpgw.managed=true",
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--memory", dockerMemory, "--memory-swap", dockerMemory, "--pids-limit", dockerPIDs,
		"--user", dockerUser, "--tmpfs", dockerTmpfs, "--env", "HOME=/tmp",
	}
	if !spec.Network {
		args = append(args, "--network", "none")
	}
	for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
		args = append(args, "--env", k)
	}
	args = append(args, spec.Image)
	if spec.Command != "" {
		args = append(args, spec.Command)
	}
	return append(args, spec.Args...)
}

func containerName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mcpgw-" + hex.EncodeToString(b), nil
}

func (p *stdioProcess) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Close closes stdin (a well-behaved server exits on EOF), then escalates to SIGTERM and
// SIGKILL of the whole process group, and force-removes the container if one was left.
func (p *stdioProcess) Close() error {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		if p.waitExit() {
			return
		}
		signalGroup(p.cmd, false)
		if !p.waitExit() {
			signalGroup(p.cmd, true)
			p.waitExit()
		}
		p.removeContainer()
	})
	return nil
}

func (p *stdioProcess) waitExit() bool {
	select {
	case <-p.exited:
		return true
	case <-time.After(p.grace):
		return false
	}
}

func (p *stdioProcess) removeContainer() {
	if p.container == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerRemoveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.dockerPath, "rm", "-f", p.container)
	cmd.Env = p.cmd.Env
	_ = cmd.Run()
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
