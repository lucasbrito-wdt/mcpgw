package montyrs

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
)

// SelftestPathEnv points at a prebuilt confine-selftest binary (cargo build
// --release --features confine-selftest --bin confine-selftest); without it
// the test builds one, or skips when cargo is missing.
const SelftestPathEnv = "MCPGW_MONTYRS_CONFINE_SELFTEST"

func selftestPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv(SelftestPathEnv); p != "" {
		if err := allowAppContainers(p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo not found and %s unset", SelftestPathEnv)
	}
	cmd := exec.Command("cargo", "build", "--release", "--quiet", "--features", "confine-selftest", "--bin", "confine-selftest")
	cmd.Dir = "worker"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build confine-selftest: %v: %s", err, out)
	}
	p, err := releaseBinary("confine-selftest")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestConfinementBlocksEscapes runs each escape attempt in a confined probe
// spawned like a real worker. "full" is the worker's confinement; in
// "sandbox-only" the rlimits are off, so seccomp/Seatbelt alone must block it.
func TestConfinementBlocksEscapes(t *testing.T) {
	bin := selftestPath(t)
	attempts := []string{"open", "create", "socket", "connect", "fork", "exec", "exec-memory"}
	for _, mode := range []string{"full", "sandbox-only"} {
		t.Run(mode+"/none", func(t *testing.T) {
			out, err := workerCommand(bin, "none", mode).Output()
			if err != nil || strings.TrimSpace(string(out)) != "ok" {
				t.Fatalf("confined probe did not run cleanly: %v, output %q", err, out)
			}
		})
		for _, attempt := range attempts {
			t.Run(mode+"/"+attempt, func(t *testing.T) {
				if runtime.GOOS == "darwin" && (attempt == "exec-memory" || attempt == "socket" && mode == "sandbox-only") {
					t.Skip("Seatbelt does not mediate socket() (RLIMIT_NOFILE does, connect is denied) or W^X memory")
				}
				if runtime.GOOS == "windows" && attempt == "socket" {
					t.Skip("an AppContainer mediates connect, not socket creation")
				}
				args := []string{attempt, mode}
				var ln net.Listener
				if attempt == "connect" {
					ln = listenLoopback(t)
					args = append(args, ln.Addr().String())
				}
				out, err := workerCommand(bin, args...).Output()
				assertBlocked(t, string(out), err, mode)
				if ln != nil {
					assertNothingAccepted(t, ln)
				}
			})
		}
		t.Run(mode+"/connect-external", func(t *testing.T) {
			out, err := workerCommand(bin, "connect", mode, reachableExternalAddr(t)).Output()
			assertBlocked(t, string(out), err, mode)
		})
	}
}

// externalAddr is a public HTTPS endpoint (Cloudflare DNS) to dial out to.
const externalAddr = "1.1.1.1:443"

// reachableExternalAddr returns externalAddr once the test itself can reach
// it, so a failed dial from the probe can only be the sandbox.
func reachableExternalAddr(t *testing.T) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", externalAddr, 3*time.Second)
	if err != nil {
		t.Skipf("%s unreachable from the test itself: %v", externalAddr, err)
	}
	_ = conn.Close()
	return externalAddr
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// assertNothingAccepted fails if the probe, which has exited, reached the
// listener: a completed handshake waits in the backlog.
func assertNothingAccepted(t *testing.T, ln net.Listener) {
	t.Helper()
	_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(200 * time.Millisecond))
	if conn, err := ln.Accept(); err == nil {
		_ = conn.Close()
		t.Fatal("the confined probe connected to the loopback listener")
	}
}

// incidentalWindowsErrors fail an attempt with no sandbox at all: file not
// found, path not found. A refused connect is not among them: the probe dials
// a live listener (checked to accept nothing) or a host the test reached.
var incidentalWindowsErrors = []string{"(os error 2)", "(os error 3)"}

// assertBlocked accepts a seccomp kill (SIGSYS) or a reported denial. The
// sandbox alone must answer EPERM, not some incidental error.
func assertBlocked(t *testing.T, out string, err error, mode string) {
	t.Helper()
	// "bad system call" is SIGSYS, which syscall does not define on Windows.
	var exit *exec.ExitError
	if errors.As(err, &exit) && strings.Contains(exit.String(), "bad system call") {
		return
	}
	want := "blocked"
	if mode == "sandbox-only" && runtime.GOOS != "windows" {
		want = "blocked Operation not permitted"
	}
	out = strings.TrimSpace(out)
	incidental := runtime.GOOS == "windows" && slices.ContainsFunc(incidentalWindowsErrors, func(e string) bool {
		return strings.HasSuffix(out, e)
	})
	if err != nil || !strings.HasPrefix(out, want) || incidental {
		t.Fatalf("want SIGSYS or %q, got err %v, output %q", want, err, out)
	}
}

// TestConfinedWorkerRunsRegexScripts: the regex engine probes cgroup files on
// first use, which the seccomp filter must answer with EPERM, not a kill.
func TestConfinedWorkerRunsRegexScripts(t *testing.T) {
	out := mustRun(t, "import re\nre.match('a+b', 'aaab').group(0)", nil, sandbox.Limits{})
	if string(out) != `"aaab"` {
		t.Fatalf("got %s", out)
	}
}
