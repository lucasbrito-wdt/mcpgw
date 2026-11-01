//go:build windows

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
)

const (
	windowsServiceName = "mcpgw"
	stopWaitHintMs     = 30000
)

// runAsWindowsService runs `mcpgw serve` under the Service Control Manager when launched by it.
func runAsWindowsService(args []string) (handled bool, code int) {
	if len(args) == 0 || args[0] != "serve" {
		return false, 0
	}
	if isService, err := svc.IsWindowsService(); err != nil || !isService {
		return false, 0
	}
	logw := openServiceLog()
	defer func() {
		if c, ok := logw.(io.Closer); ok {
			_ = c.Close()
		}
	}()
	if err := svc.Run(windowsServiceName, &windowsService{args: args, logw: logw}); err != nil {
		return true, 1
	}
	return true, 0
}

type windowsService struct {
	args []string
	logw io.Writer
}

func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, s.args, s.logw, s.logw, os.Stdin) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: stopWaitHintMs}
				cancel()
				return exitCode(<-done)
			}
		case code := <-done:
			return exitCode(code)
		}
	}
}

func exitCode(code int) (bool, uint32) {
	if code == 0 {
		return false, 0
	}
	return true, uint32(code)
}

// openServiceLog appends to %ProgramData%\mcpgw\logs\mcpgw.log; falls back to discarding output.
func openServiceLog() io.Writer {
	dir := filepath.Join(os.Getenv("ProgramData"), "mcpgw", "logs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return io.Discard
	}
	f, err := os.OpenFile(filepath.Join(dir, "mcpgw.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return io.Discard
	}
	return f
}
