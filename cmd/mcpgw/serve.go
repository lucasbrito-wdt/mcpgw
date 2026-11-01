package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second
)

func runServe(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if err := cfg.RequireMasterKey(); err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(stdout, nil))
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	return serve(ctx, cfg, log, ln)
}

// serve runs the gateway on ln until ctx is cancelled, then shuts down in order:
// HTTP server (15s), background jobs, audit flush, sandbox, upstream client, pool.
func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, ln net.Listener) error {
	gw, err := newGateway(ctx, cfg, log)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("start gateway: %w", err)
	}
	if cfg.PprofAddr != "" {
		stopPprof, err := startPprof(cfg.PprofAddr, log)
		if err != nil {
			_ = ln.Close()
			gw.Close(context.Background())
			return err
		}
		defer stopPprof()
	}
	jobsCtx, stopJobs := context.WithCancel(context.Background())
	waitJobs := gw.startJobs(jobsCtx)

	srv := &http.Server{
		Handler:           gw.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()
	log.Info("gateway listening", "addr", ln.Addr().String(), "public_url", cfg.PublicURL)
	if isTerminal(os.Stderr) {
		writeBanner(os.Stderr, i18n.FromEnv(), buildVersion(), cfg.PublicURL, os.Getenv("NO_COLOR") == "")
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failed:
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}
	stopJobs()
	waitJobs()
	gw.Close(shutCtx)
	log.Info("gateway stopped")
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}

// startPprof serves the profiling endpoints on their own mux and listener (never on the gateway
// handler) and enables mutex and block sampling. config.Load only accepts loopback addresses.
func startPprof(addr string, log *slog.Logger) (stop func(), err error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("pprof listen: %w", err)
	}
	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(int(time.Millisecond))
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
	go func() { _ = srv.Serve(ln) }()
	log.Warn("pprof enabled", "addr", ln.Addr().String())
	return func() { _ = srv.Close() }, nil
}

const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[38;2;61;220;151m"
	ansiViolet = "\x1b[38;2;165;163;255m"
	ansiDot    = "\x1b[38;2;139;123;255m"
	ansiDim    = "\x1b[2m"
)

// writeBanner prints the startup banner. It takes plain values (no secrets, no DSN) and only
// emits ANSI escapes when color is true. version may be empty.
func writeBanner(w io.Writer, l i18n.Locale, version, publicURL string, color bool) {
	paint := func(code, text string) string {
		if !color {
			return text
		}
		return code + text + ansiReset
	}
	title := i18n.T("MCP gateway", "gateway MCP").In(l)
	if version != "" {
		title += " " + version
	}
	ok := paint(ansiGreen, "ok")
	base := strings.TrimRight(publicURL, "/")
	fmt.Fprintf(w, "\n  [%s]  %s  %s\n", paint(ansiDot, "·"), "mcpgw", paint(ansiDim, title))
	fmt.Fprint(w, "  "+ok+"  "+i18n.T("database connected", "banco de dados conectado").In(l)+"\n")
	fmt.Fprint(w, "  "+ok+"  "+i18n.T("listening on %s", "ouvindo em %s", paint(ansiViolet, base)).In(l)+"\n")
	fmt.Fprintf(w, "  admin   %s\n", paint(ansiViolet, base+"/admin/"))
	fmt.Fprintf(w, "  portal  %s\n\n", paint(ansiViolet, base+"/portal/"))
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// buildVersion returns the module version stamped into the binary, or "" for local builds.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "(devel)" {
		return ""
	}
	return info.Main.Version
}
