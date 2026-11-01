//go:build !linux && !windows

package montyrs

import (
	"os"
	"syscall"
)

// workerSysProcAttr has nothing to add off Linux: there is no Pdeathsig, and
// the worker already exits when its stdin closes.
func workerSysProcAttr() *syscall.SysProcAttr { return nil }

// workerArgs is empty: rlimits and Seatbelt need no input from the gateway.
func workerArgs(int64) []string { return nil }

// adoptWorker has nothing to do: the worker exits when its stdin closes.
func adoptWorker(*os.Process) error { return nil }
