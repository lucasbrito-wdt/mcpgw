//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) { _ = syscall.Kill(-cmd.Process.Pid, sig) }
