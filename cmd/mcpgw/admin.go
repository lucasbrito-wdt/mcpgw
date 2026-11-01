package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

var adminUsage = i18n.T(`usage: mcpgw admin <group> <command> [flags]

user create --email E [--admin]      create a user (password in MCPGW_ADMIN_PASSWORD or on the first stdin line)
user list
user set-admin --email E --admin=true|false
server add --slug S --url U --auth none|oauth|header [--auto-approve-new-tools]
    --auto-approve-new-tools is off by default: tools that appear after the first sync stay quarantined until tool approve
server list
server disable --slug S
server enable --slug S
server remove --slug S [--yes]      remove the server and its tools, credentials and consents (without --yes it only shows the impact)
server set-header --slug S --name H --value-env VAR
server set-oauth-client --slug S --client-id ID [--secret-env VAR]
server set-trust --slug S [--annotations=true|false] [--auto-approve-new-tools=true|false]
server set-connection-env --slug S --name X [--name Y] | --clear
    variables each connection (profile) fills in on the portal; a fixed variable with the same name becomes the default
server set-env --slug S [--env NAME=VAR ...] [--unset NAME ...]
    fixed env of the stdio process (encrypted): adds or replaces NAME with the value read from VAR, removes --unset; the rest stay
server set-credential-env --slug S --name VAR
    stdio: each user provides their own key on the portal, handed to the process in VAR (removes VAR from the fixed env, if present)
server set-header-format --slug S --name H [--prefix P] [--format-hint T] [--help-url U] | --slug S --clear
    header name, prefix (keep the trailing space: --prefix "Bearer "), hint and help link
server set-connection-url --slug S [--off]
    HTTP server with header auth: each connection provides its own MCP address on the portal (--off goes back to the server address)
ceiling set --email E --slug S --level none|read|write|destructive
ceiling list [--email E]
tool list [--slug S]
tool level --slug S --tool T --level read|write|destructive
tool show --slug S --tool T          show status, current definition and hash (review before approving)
tool approve --slug S --tool T [--hash H] | --all   approve new or changed definitions; --hash (12+ hex from tool list) refuses if the definition changed
    the first admin catalog sync sets the trusted baseline definition (TOFU); later changes require tool approve
    the automatic sync after an OAuth connection only adds new tools, always awaiting approval
catalog sync --slug S [--as EMAIL]
`, `uso: mcpgw admin <grupo> <comando> [flags]

user create --email E [--admin]      cria usuário (senha em MCPGW_ADMIN_PASSWORD ou na 1a linha do stdin)
user list
user set-admin --email E --admin=true|false
server add --slug S --url U --auth none|oauth|header [--auto-approve-new-tools]
    --auto-approve-new-tools vem desligado: tools novas após a 1a sincronização ficam em quarentena até tool approve
server list
server disable --slug S
server enable --slug S
server remove --slug S [--yes]      remove o servidor e suas tools, credenciais e consentimentos (sem --yes só mostra o impacto)
server set-header --slug S --name H --value-env VAR
server set-oauth-client --slug S --client-id ID [--secret-env VAR]
server set-trust --slug S [--annotations=true|false] [--auto-approve-new-tools=true|false]
server set-connection-env --slug S --name X [--name Y] | --clear
    variáveis que cada conexão (perfil) informa no portal; a variável fixa de mesmo nome vira o padrão
server set-env --slug S [--env NAME=VAR ...] [--unset NAME ...]
    env fixo do processo stdio (cifrado): adiciona ou troca NAME com o valor lido de VAR, remove --unset; as demais ficam
server set-credential-env --slug S --name VAR
    stdio: cada usuário informa a própria chave no portal, entregue ao processo em VAR (remove VAR do env fixo, se houver)
server set-header-format --slug S --name H [--prefix P] [--format-hint T] [--help-url U] | --slug S --clear
    nome, prefixo (preserve o espaço final: --prefix "Bearer "), dica e link de ajuda do header
server set-connection-url --slug S [--off]
    servidor HTTP com auth header: cada conexão informa no portal o próprio endereço do MCP (--off volta ao endereço do servidor)
ceiling set --email E --slug S --level none|read|write|destructive
ceiling list [--email E]
tool list [--slug S]
tool level --slug S --tool T --level read|write|destructive
tool show --slug S --tool T          mostra status, definição atual e hash (revise antes de aprovar)
tool approve --slug S --tool T [--hash H] | --all   aprova definições novas ou alteradas; --hash (12+ hex de tool list) recusa se a definição mudou
    a 1a catalog sync do admin define a definição-base confiável (TOFU); alterações depois dela exigem tool approve
    a sincronização automática após conexão OAuth só adiciona tools novas, sempre aguardando aprovação
catalog sync --slug S [--as EMAIL]
`)

// adminEnv is everything an admin command may touch. Tests build it directly.
type adminEnv struct {
	pool   *pgxpool.Pool
	cfg    *config.Config
	out    io.Writer
	in     io.Reader
	loc    i18n.Locale // zero value renders English
	getenv func(string) string
	lister catalog.Lister

	mgr *upstreamauth.Manager // built once by manager()
}

type adminCommand func(ctx context.Context, e *adminEnv, args []string) error

var adminCommands = map[string]map[string]adminCommand{
	"user":    {"create": userCreate, "list": userList, "set-admin": userSetAdmin},
	"server":  {"add": serverAdd, "list": serverList, "disable": serverDisable, "enable": serverEnable, "remove": serverRemove, "set-header": serverSetHeader, "set-oauth-client": serverSetOAuthClient, "set-trust": serverSetTrust, "set-connection-env": serverSetConnectionEnv, "set-env": serverSetEnv, "set-credential-env": serverSetCredentialEnv, "set-header-format": serverSetHeaderFormat, "set-connection-url": serverSetConnectionURL},
	"ceiling": {"set": ceilingSet, "list": ceilingList},
	"tool":    {"list": toolList, "level": toolLevel, "approve": toolApprove, "show": toolShow},
	"catalog": {"sync": catalogSync},
}

func runAdmin(ctx context.Context, args []string, stdout io.Writer, stdin io.Reader, l i18n.Locale) error {
	cmd, err := findAdminCommand(args)
	if err != nil {
		fmt.Fprint(stdout, adminUsage.In(l))
		if len(args) == 0 {
			return nil
		}
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
	var opts []upstream.Option
	if cfg.RequireMasterKey() == nil { // stdio servers need the vault for sealed env; others work without it
		v, err := vault.New(cfg.MasterKey, vaultKeyID)
		if err != nil {
			return err
		}
		opts = append(opts, upstream.WithResolver(upstream.NewDBResolver(pool, v)))
	}
	client := upstream.New(opts...)
	defer client.Close()
	env := &adminEnv{pool: pool, cfg: cfg, out: stdout, in: stdin, getenv: os.Getenv, lister: client, loc: l}
	return cmd(ctx, env, args[2:])
}

func findAdminCommand(args []string) (adminCommand, error) {
	if len(args) < 2 {
		return nil, i18n.T("specify the group and the command (e.g. user list)", "informe o grupo e o comando (ex.: user list)")
	}
	cmd, ok := adminCommands[args[0]][args[1]]
	if !ok {
		return nil, i18n.T("unknown command: %s %s", "comando desconhecido: %s %s", args[0], args[1])
	}
	return cmd, nil
}

// adminDispatch runs one admin command (used by tests).
func adminDispatch(ctx context.Context, e *adminEnv, args []string) error {
	cmd, err := findAdminCommand(args)
	if err != nil {
		return err
	}
	return cmd(ctx, e, args[2:])
}

// newFlags builds a flag set whose errors and usage go to the admin output.
func (e *adminEnv) newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.out)
	return fs
}

func (e *adminEnv) printf(format string, a ...any) { fmt.Fprintf(e.out, format, a...) }

// t renders a message in the CLI locale.
func (e *adminEnv) t(en, pt string, a ...any) string { return i18n.T(en, pt, a...).In(e.loc) }

// say prints a message in the CLI locale; en and pt take the same verbs in the same order.
func (e *adminEnv) say(en, pt string, a ...any) { fmt.Fprint(e.out, e.t(en, pt, a...)) }

// localizedPrefix prepends a fixed prefix to an error while keeping its translation.
type localizedPrefix struct {
	prefix string
	err    error
}

func (p localizedPrefix) Error() string                 { return p.prefix + p.err.Error() }
func (p localizedPrefix) Unwrap() error                 { return p.err }
func (p localizedPrefix) Localize(l i18n.Locale) string { return p.prefix + i18n.Message(p.err, l) }

// safeText neutralizes text that came from an upstream (or any non-admin source) before it reaches
// a terminal: C0/C1 controls, DEL, format characters (bidi, zero-width, tags), line/paragraph
// separators and invalid UTF-8 become visible \uXXXX / \UXXXXXXXX / \xNN escapes, so an escape
// sequence cannot hide or rewrite what the reviewing admin reads. A newline survives only when
// keepNewline is set (the multi-line description of `tool show`).
func safeText(s string, keepNewline bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02X`, s[i])
		case r == '\n' && keepNewline:
			b.WriteRune(r)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			if r > 0xFFFF {
				fmt.Fprintf(&b, `\U%08X`, r)
			} else {
				fmt.Fprintf(&b, `\u%04X`, r)
			}
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// safeList is safeText over a list of names, joined for printing.
func safeList(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = safeText(n, false)
	}
	return strings.Join(out, ", ")
}

// table prints rows with a header, column aligned. Cells are neutralized with safeText: they may
// carry upstream-sourced names.
func (e *adminEnv) table(header string, rows [][]string) {
	if len(rows) == 0 {
		e.say("No records.\n", "Nenhum registro.\n")
		return
	}
	for _, r := range rows {
		for i := range r {
			r[i] = safeText(r[i], false)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return strings.Join(rows[i], "\x00") < strings.Join(rows[j], "\x00") })
	w := tabwriter.NewWriter(e.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, header)
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	_ = w.Flush()
}

// secretFromEnv reads a secret from the environment variable named by flag value; secrets never
// travel through argv.
func (e *adminEnv) secretFromEnv(flagName, varName string) (string, error) {
	if err := validateEnvName(varName); err != nil {
		return "", localizedPrefix{"--" + flagName + ": ", err}
	}
	v := e.getenv(varName)
	if v == "" {
		return "", i18n.T("environment variable %s is empty or not set", "a variável de ambiente %s está vazia ou não definida", varName)
	}
	return v, nil
}

// service is the shared administration core. The CLI may create host stdio servers.
func (e *adminEnv) service() *admin.Service {
	return admin.New(admin.Options{Pool: e.pool, Credentials: e.manager, Vault: e.vault, Lister: e.lister, AllowHostLauncher: true})
}

// vault builds the vault that seals server secrets; it errors when the master key is missing.
func (e *adminEnv) vault() (*vault.Vault, error) {
	if err := e.cfg.RequireMasterKey(); err != nil {
		return nil, err
	}
	return vault.New(e.cfg.MasterKey, vaultKeyID)
}

// manager builds (once) the upstream auth manager used for sealed server secrets and credentials.
// Building it also re-seals legacy static_header values into shared rows, so a database upgraded
// and used only through the CLI is migrated.
func (e *adminEnv) manager() (*upstreamauth.Manager, error) {
	if e.mgr != nil {
		return e.mgr, nil
	}
	v, err := e.vault()
	if err != nil {
		return nil, err
	}
	mgr, err := upstreamauth.New(upstreamauth.Options{
		Pool: e.pool, Vault: v, PublicURL: e.cfg.PublicURL,
		User: func(*http.Request) (string, bool) { return "", false },
	})
	if err != nil {
		return nil, err
	}
	if _, err := mgr.MigrateLegacyHeaders(context.Background()); err != nil {
		return nil, err
	}
	e.mgr = mgr
	return mgr, nil
}
