package montyrs

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the worker binary is a launcher: it runs the real worker in an
// AppContainer, inside a job that dies with the launcher (see
// worker/src/confine_windows.rs). Killing the launcher therefore kills the
// confined worker too.

// workerSysProcAttr detaches the launcher from the gateway's console, so a
// Ctrl+C there does not reach it, without creating a console of its own.
func workerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
}

// workerArgs passes the memory cap to the launcher, which sizes the job's
// per-process memory limit from it.
func workerArgs(memoryBytes int64) []string {
	return []string{"--max-memory-bytes=" + strconv.FormatInt(memoryBytes, 10)}
}

// gatewayJob holds every launcher. It kills them when its only handle, which
// this process never closes, goes away with the gateway: Windows has no
// Pdeathsig.
var gatewayJob = sync.OnceValues(func() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return job, nil
})

// adoptWorker puts a just-started launcher in gatewayJob. The pid cannot be
// reused meanwhile: os.Process holds a handle to the process.
func adoptWorker(p *os.Process) error {
	job, err := gatewayJob()
	if err != nil {
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return nil
}
