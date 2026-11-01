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

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
)

var usage = i18n.T(`usage: mcpgw <command> [flags]

commands:
  serve     run the gateway
  migrate   apply pending database migrations
  admin     administration commands (run "mcpgw admin" for the list)
`, `uso: mcpgw <comando> [flags]

comandos:
  serve     executa o gateway
  migrate   aplica as migrations pendentes do banco de dados
  admin     comandos de administração (rode "mcpgw admin" para a lista)
`)

func main() {
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
	switch args[0] {
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
	return nil
}
