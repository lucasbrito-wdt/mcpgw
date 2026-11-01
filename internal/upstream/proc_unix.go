//go:build unix

package upstream

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the server in its own process group so that shutdown reaches children.
func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// signalGroup sends SIGTERM (or SIGKILL when kill is set) to the server's process group.
func signalGroup(cmd *exec.Cmd, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
