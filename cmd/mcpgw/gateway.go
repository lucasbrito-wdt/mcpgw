package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/adminapi"
	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/codemode"
	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/mcpserver"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/portalapi"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox/montyrs"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
	adminweb "github.com/lucasbrito-wdt/mcpgw/web/admin"
)

// vaultKeyID names the master key version used to seal secrets (see vault.KeyID for rotation).
const vaultKeyID = "k1"

// gateway holds every long-lived component of a running process.
type gateway struct {
	cfg          *config.Config
	log          *slog.Logger
	pool         *pgxpool.Pool
	oauth        *oauth.Server
	policy       *policy.Engine
	audit        *audit.Recorder
	sandbox      sandbox.Runner
	upstream     *upstream.Client
	upstreamAuth *upstreamauth.Manager
	autoSync     *catalog.AutoSyncer
	mcp          *mcpserver.Handler
	adminAPI     *adminapi.API
	portalAPI    *portalapi.API
	static       http.Handler

	stopListen context.CancelFunc
	listenDone chan struct{}
}

// newGateway builds the stack in dependency order: pool, migrations, vault, oauth, policy
// (plus its LISTEN loop), audit, sandbox, upstream, upstreamauth (+ catalog auto-sync),
// codemode, mcpserver.
// On error everything already built is closed. ctx only bounds construction; the LISTEN
// loop runs until Close.
func newGateway(ctx context.Context, cfg *config.Config, log *slog.Logger) (gw *gateway, err error) {
	gw = &gateway{cfg: cfg, log: log}
	defer func() {
		if err != nil {
			gw.Close(context.Background())
			gw = nil
		}
	}()

	if gw.pool, err = store.NewPool(ctx, cfg.DatabaseURL); err != nil {
		return gw, fmt.Errorf("database: %w", err)
	}
	if _, err = store.Migrate(ctx, gw.pool); err != nil {
		return gw, fmt.Errorf("migrate: %w", err)
	}
	v, err := vault.New(cfg.MasterKey, vaultKeyID)
	if err != nil {
		return gw, err
	}
	gw.oauth, err = oauth.New(ctx, oauth.Options{PublicURL: cfg.PublicURL, Pool: gw.pool, Sealer: v, Logger: log})
	if err != nil {
		return gw, err
	}
	if gw.policy, err = policy.New(ctx, gw.pool); err != nil {
		return gw, err
	}
	listenCtx, stop := context.WithCancel(context.Background())
	gw.stopListen, gw.listenDone = stop, make(chan struct{})
	go func() {
		defer close(gw.listenDone)
		if err := gw.policy.Listen(listenCtx); err != nil && listenCtx.Err() == nil {
			log.Error("policy listener stopped", "err", err)
		}
	}()

	gw.audit = audit.New(gw.pool, audit.Options{Logger: log})
	if err = gw.audit.EnsurePartitions(ctx, time.Now()); err != nil {
		return gw, err
	}
	if gw.sandbox, err = newSandbox(cfg.SandboxEngine); err != nil {
		return gw, err
	}
	gw.upstream = upstream.New(upstream.WithResolver(upstream.NewDBResolver(gw.pool, v)), upstream.WithLogger(log))
	// A stdio server edited elsewhere (web, CLI) gets fresh processes: see Client.InvalidateStdio.
	gw.policy.OnServerChanged(gw.upstream.InvalidateStdio)
	// The connect callback syncs the server's catalog with the new credential, in the background,
	// but only for users that hold a trust ceiling on the server (the ceiling may have been
	// revoked since /connect). The sync is insert-only: see catalog.AutoSyncer.
	gw.upstreamAuth, err = upstreamauth.New(upstreamauth.Options{
		Pool: gw.pool, Vault: v, PublicURL: cfg.PublicURL, User: gw.oauth.SessionUser,
		OnConnected: func(serverID, userID string) {
			if policy.CeilingOf(context.Background(), gw.pool, userID, serverID) > policy.LevelNone {
				gw.autoSync.Trigger(serverID, userID)
			}
		},
	})
	if err != nil {
		return gw, err
	}
	n, err := gw.upstreamAuth.MigrateLegacyHeaders(ctx)
	if err != nil {
		return gw, fmt.Errorf("migrate legacy header credentials: %w", err)
	}
	if n > 0 {
		log.Info("legacy header credentials moved to shared connections", "servers", n)
	}
	gw.autoSync = catalog.NewAutoSyncer(catalog.AutoSyncOptions{
		Pool: gw.pool, Lister: gw.upstream, Credentials: gw.upstreamAuth, Logger: log, Timeout: catalogSyncLimit,
	})
	svc, err := codemode.New(codemode.Options{
		Pool: gw.pool, Sandbox: gw.sandbox, Python: cfg.SandboxEngine == config.EngineMonty, Policy: gw.policy, Upstream: gw.upstream,
		Credentials: gw.upstreamAuth, Audit: gw.audit, PublicURL: cfg.PublicURL,
		Limits: sandbox.DefaultLimits(), Logger: log,
	})
	if err != nil {
		return gw, err
	}
	// The MCP SDK logs every request at INFO; only warnings and above are wanted from it.
	gw.mcp, err = mcpserver.New(mcpserver.Options{OAuth: gw.oauth, Codemode: svc, Logger: warnLogger(log)})
	if err != nil {
		return gw, err
	}
	err = gw.buildWeb(cfg, v, log)
	return gw, err
}

// workerBinary is the montyrs worker file name looked up next to the gateway executable.
const workerBinary = "montyrs-worker"

// newSandbox builds the executor for engine. The monty worker is $MCPGW_MONTYRS_WORKER, else
// montyrs-worker beside the running executable; a missing or non-executable file fails here,
// not on the first script.
func newSandbox(engine string) (sandbox.Runner, error) {
	if engine != config.EngineMonty {
		return runner(sandbox.NewExecutor())
	}
	path := os.Getenv(montyrs.WorkerPathEnv)
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("sandbox engine monty: locate executable: %w", err)
		}
		path = filepath.Join(filepath.Dir(exe), workerBinary)
	}
	// The worker is exec'd from another directory, so check and run the same absolute path.
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("sandbox engine monty: resolve worker path: %w", err)
	}
	if err := checkWorker(path); err != nil {
		return nil, err
	}
	return runner(montyrs.NewExecutor(montyrs.WithWorkerPath(path)))
}

// runner keeps a failed constructor from becoming a non-nil interface around a nil pointer,
// which Close would then dereference.
func runner[T sandbox.Runner](e T, err error) (sandbox.Runner, error) {
	if err != nil {
		return nil, err
	}
	return e, nil
}

func checkWorker(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("sandbox engine monty: worker %q: %w (build it with make worker or set %s)", path, err, montyrs.WorkerPathEnv)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("sandbox engine monty: worker %q is not an executable file", path)
	}
	return nil
}

// buildWeb builds the admin API, the user portal API and the static UI handler. Both APIs share
// one admin.Options (the web fills Vault, Credentials, Grants and Lister; host launchers stay off).
func (gw *gateway) buildWeb(cfg *config.Config, v *vault.Vault, log *slog.Logger) (err error) {
	adminOpts := admin.Options{
		Pool: gw.pool, Lister: gw.upstream, Grants: gw.oauth,
		Vault:       func() (*vault.Vault, error) { return v, nil },
		Credentials: func() (*upstreamauth.Manager, error) { return gw.upstreamAuth, nil },
	}
	if gw.adminAPI, err = adminapi.New(adminapi.Options{
		Pool: gw.pool, PublicURL: cfg.PublicURL, Sealer: v, Login: gw.oauth.Login, Admin: adminOpts, Logger: log,
	}); err != nil {
		return err
	}
	if gw.portalAPI, err = portalapi.New(portalapi.Options{
		Pool: gw.pool, PublicURL: cfg.PublicURL, Sealer: v, OAuth: gw.oauth,
		Credentials: gw.upstreamAuth, Lister: gw.upstream, Admin: adminOpts, Logger: log,
	}); err != nil {
		return err
	}
	gw.static, err = adminapi.StaticHandler(adminweb.FS)
	return err
}

// Handler returns the HTTP routes of the gateway.
func (gw *gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	gw.oauth.Routes(mux)
	gw.upstreamAuth.Routes(mux)
	mux.Handle("/mcp", gw.mcp)
	gw.adminAPI.Routes(mux)
	gw.portalAPI.Routes(mux)
	// No method: "GET /admin/" conflicts with the any-method /admin/api/ catch-all.
	mux.Handle("/admin/", gw.static)
	mux.Handle("/portal/", gw.static)
	mux.Handle("/_next/", gw.static)
	for name := range adminapi.RootAssets {
		mux.Handle("GET /"+name, gw.static)
	}
	mux.Handle("GET /admin", http.RedirectHandler("/admin/", http.StatusPermanentRedirect))
	mux.Handle("GET /portal", http.RedirectHandler("/portal/", http.StatusPermanentRedirect))
	mux.HandleFunc("GET /healthz", gw.handleHealth)
	return mux
}

func (gw *gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	if err := gw.pool.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

// Close releases everything in shutdown order: catalog auto-syncs, policy listener, audit flush,
// sandbox, upstream client, pool. It is safe on a partially built gateway. ctx bounds the audit flush.
func (gw *gateway) Close(ctx context.Context) {
	if gw.autoSync != nil {
		gw.autoSync.Close()
	}
	if gw.stopListen != nil {
		gw.stopListen()
		<-gw.listenDone
	}
	if gw.audit != nil {
		if err := gw.audit.Close(ctx); err != nil {
			gw.log.Error("audit close", "err", err)
		}
	}
	if gw.sandbox != nil {
		if err := gw.sandbox.Close(); err != nil {
			gw.log.Error("sandbox close", "err", err)
		}
	}
	if gw.upstream != nil {
		gw.upstream.Close()
	}
	if gw.pool != nil {
		gw.pool.Close()
	}
}

func warnLogger(base *slog.Logger) *slog.Logger {
	return slog.New(&minLevelHandler{Handler: base.Handler(), min: slog.LevelWarn})
}

// minLevelHandler drops records below min while sharing the destination of the wrapped handler.
type minLevelHandler struct {
	slog.Handler
	min slog.Level
}

func (h *minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h *minLevelHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return &minLevelHandler{Handler: h.Handler.WithAttrs(a), min: h.min}
}

func (h *minLevelHandler) WithGroup(name string) slog.Handler {
	return &minLevelHandler{Handler: h.Handler.WithGroup(name), min: h.min}
}
