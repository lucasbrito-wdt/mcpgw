package montyrs

import (
	"os"
	"syscall"
)

// workerSysProcAttr kills the worker if the gateway dies, and gives it its own
// process group so signals sent to the gateway's group do not reach it.
func workerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}

// workerArgs is empty: rlimits and seccomp need no input from the gateway.
func workerArgs(int64) []string { return nil }

// adoptWorker has nothing to do: Pdeathsig already ties the worker to the gateway.
func adoptWorker(*os.Process) error { return nil }
