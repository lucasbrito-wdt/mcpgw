// Command mcpgw is the MCP gateway binary: serve, migrate and admin subcommands.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

var usage = i18n.T(`usage: mcpgw <command> [flags]

commands:
  serve     run the gateway
  migrate   apply pending database migrations
  admin     administration commands (run "mcpgw admin" for the list)
  version   print the version

serve, migrate and admin accept --env-file <path> (or MCPGW_ENV_FILE): KEY=VALUE lines,
real environment variables win, the file must not be readable by group or others.
`, `uso: mcpgw <comando> [flags]

comandos:
  serve     executa o gateway
  migrate   aplica as migrations pendentes do banco de dados
  admin     comandos de administração (rode "mcpgw admin" para a lista)
  version   mostra a versão

serve, migrate e admin aceitam --env-file <caminho> (ou MCPGW_ENV_FILE): linhas CHAVE=VALOR,
variáveis de ambiente reais têm prioridade, o arquivo não pode ser legível por grupo ou outros.
`)

// version is stamped at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if handled, code := runAsWindowsService(os.Args[1:]); handled {
		os.Exit(code)
	}
	if err := loadDotEnv(".env", os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mcpgw: .env:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	l := i18n.FromEnv()
	if len(args) == 0 {
		fmt.Fprint(stderr, usage.In(l))
		return 2
	}
	var err error
	if args[0] == "serve" || args[0] == "migrate" || args[0] == "admin" {
		if args, err = applyEnvFile(args); err != nil {
			fmt.Fprintf(stderr, "mcpgw %s: %s\n", args[0], err)
			return 2
		}
	}
	switch args[0] {
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, version)
	case "serve":
		err = runServe(ctx, args[1:], stdout)
	case "migrate":
		err = runMigrate(ctx, args[1:], stdout, l)
	case "admin":
		err = runAdmin(ctx, args[1:], stdout, stdin, l)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage.In(l))
	default:
		fmt.Fprint(stderr, i18n.T("unknown command %q\n\n", "comando desconhecido %q\n\n", args[0]).In(l)+usage.In(l))
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "mcpgw %s: %s\n", args[0], safeText(i18n.Message(err, l), false)) // errors may quote upstream text
		return 1
	}
	return 0
}

// applyEnvFile strips the global --env-file flag from args[1:] and loads the file it (or
// MCPGW_ENV_FILE) names into the environment.
func applyEnvFile(args []string) ([]string, error) {
	rest, path, err := extractEnvFile(args[1:], os.Getenv)
	if err != nil {
		return args, err
	}
	if path != "" {
		if err := loadEnvFile(path); err != nil {
			return args, fmt.Errorf("env file: %w", err)
		}
	}
	return append([]string{args[0]}, rest...), nil
}

func runMigrate(ctx context.Context, args []string, stdout io.Writer, l i18n.Locale) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	applied, err := store.Migrate(ctx, pool)
	for _, version := range applied {
		fmt.Fprint(stdout, i18n.T("applied %s\n", "aplicada %s\n", version).In(l))
	}
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Fprintln(stdout, i18n.T("database is up to date", "banco de dados atualizado").In(l))
	}
	return checkMasterKey(ctx, cfg, pool)
}

// checkMasterKey fails early when the configured master key cannot open data already in the database.
func checkMasterKey(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	if cfg.MasterKey == nil {
		return nil
	}
	v, err := vault.New(cfg.MasterKey, vaultKeyID)
	if err != nil {
		return err
	}
	return wrapMasterKeyMismatch(oauth.CheckSigningKey(ctx, pool, v))
}
