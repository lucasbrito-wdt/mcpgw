//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(*exec.Cmd) {}

func signalProcessGroup(cmd *exec.Cmd, _ syscall.Signal) { _ = cmd.Process.Kill() }
