package main

import (
	"context"
	"slices"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

// serverSetConnectionEnv replaces the per-connection variable names of a stdio server with a
// per-user credential; --clear removes them all.
func serverSetConnectionEnv(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-connection-env")
	slug := fs.String("slug", "", e.t("server slug (stdio with --credential-env)", "slug do servidor (stdio com --credential-env)"))
	var names stringList
	fs.Var(&names, "name", e.t("variable provided by each connection (repeatable), e.g. PORTAINER_URL", "variável informada por cada conexão (repetível), ex.: PORTAINER_URL"))
	clearAll := fs.Bool("clear", false, e.t("remove all per-connection variables", "remove todas as variáveis por conexão"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	if *clearAll == (len(names) > 0) {
		return i18n.T("provide --name (one or more times) or --clear", "informe --name (uma ou mais vezes) ou --clear")
	}
	if err := e.service().SetConnectionEnv(ctx, *slug, names); err != nil {
		return err
	}
	if *clearAll {
		e.say("Per-connection variables removed from %s.\n", "Variáveis por conexão removidas de %s.\n", *slug)
		return nil
	}
	e.say("Per-connection variables of %s: %s.\n", "Variáveis por conexão de %s: %s.\n", *slug, strings.Join(names, ", "))
	return nil
}

// serverSetConnectionURL lets each connection of a header server over HTTP name its own upstream
// URL; --off turns it back to the server's URL.
func serverSetConnectionURL(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-connection-url")
	slug := fs.String("slug", "", e.t("server slug (HTTP with header auth)", "slug do servidor (HTTP com auth header)"))
	off := fs.Bool("off", false, e.t("turn off: all connections go back to the server address", "desliga: todas as conexões voltam a usar o endereço do servidor"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	if err := e.service().SetConnectionURL(ctx, *slug, !*off); err != nil {
		return err
	}
	if *off {
		e.say("Connections of %s go back to the server address.\n", "Conexões de %s voltam a usar o endereço do servidor.\n", *slug)
		return nil
	}
	e.say("Each connection of %s now provides its own MCP address.\n", "Cada conexão de %s agora informa o próprio endereço do MCP.\n", *slug)
	return nil
}

// serverSetEnv merges changes into the sealed fixed environment of a stdio server: --env adds or
// replaces NAME with the value read from the SOURCE_VAR variable, --unset removes NAME.
func serverSetEnv(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-env")
	slug := fs.String("slug", "", e.t("stdio server slug", "slug do servidor stdio"))
	var pairs, unset stringList
	fs.Var(&pairs, "env", e.t("NAME=SOURCE_VAR; the value is read from the environment and stored encrypted (repeatable)", "NOME=VARIAVEL_DE_ORIGEM; o valor é lido do ambiente e guardado cifrado (repetível)"))
	fs.Var(&unset, "unset", e.t("NAME to remove from the fixed env (repeatable)", "NOME a remover do env fixo (repetível)"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	set, err := envFromSources(e, pairs)
	if err != nil {
		return err
	}
	if err := e.service().SetStdioEnv(ctx, *slug, set, unset); err != nil {
		return err
	}
	e.say("Fixed env of %s updated: %d set, %d removed.\n", "Env fixo de %s atualizado: %d definidas, %d removidas.\n", *slug, len(set), len(unset))
	return nil
}

// serverSetCredentialEnv switches a stdio server to a per-user credential passed in --name.
func serverSetCredentialEnv(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-credential-env")
	slug := fs.String("slug", "", e.t("stdio server slug", "slug do servidor stdio"))
	name := fs.String("name", "", e.t("variable that receives each user's token, e.g. PORTAINER_API_KEY", "variável que recebe o token de cada usuário, ex.: PORTAINER_API_KEY"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug, "name", *name); err != nil {
		return err
	}
	if err := e.service().SetCredentialEnv(ctx, *slug, *name); err != nil {
		return err
	}
	e.say("Credential of %s is now per user, in variable %s.\n", "Credencial de %s agora é por usuário, na variável %s.\n", *slug, *name)
	return nil
}

// serverSetHeaderFormat edits the header name, prefix and portal hints of a header server, or of an
// oauth server that also accepts a pasted token, through UpdateServer. --clear (oauth only) turns
// that alternative off. The prefix is passed as typed: its trailing space ("Bearer ") is significant.
func serverSetHeaderFormat(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-header-format")
	slug := fs.String("slug", "", e.t("server slug (header or oauth auth)", "slug do servidor (auth header ou oauth)"))
	name := fs.String("name", "", e.t("HTTP header name", "nome do header HTTP"))
	clearHeader := fs.Bool("clear", false, e.t("turn off the alternative token of an oauth server (without --name)", "desliga o token alternativo de um servidor oauth (sem --name)"))
	prefix := fs.String("prefix", "", e.t(`value prefix, with the trailing space, e.g. "Bearer "`, `prefixo do valor, com o espaço final, ex.: "Bearer "`))
	hint := fs.String("format-hint", "", e.t("format hint shown on the portal", "dica de formato mostrada no portal"))
	helpURL := fs.String("help-url", "", e.t("help link to obtain the key", "link de ajuda para obter a chave"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *clearHeader {
		return serverClearHeader(ctx, e, *slug, *name, *prefix, *hint, *helpURL)
	}
	if err := requireFlags("slug", *slug, "name", *name); err != nil {
		return err
	}
	header := admin.HeaderConfig{Name: *name, Prefix: *prefix, FormatHint: *hint, HelpURL: *helpURL}
	if err := e.service().UpdateServer(ctx, *slug, admin.ServerUpdate{Header: &header}); err != nil {
		return err
	}
	e.say("Header format of %s updated.\n", "Formato do header de %s atualizado.\n", *slug)
	return nil
}

// serverClearHeader removes the header config of an oauth server. UpdateServer refuses it while an
// active connection uses a pasted token.
func serverClearHeader(ctx context.Context, e *adminEnv, slug string, others ...string) error {
	if err := requireFlags("slug", slug); err != nil {
		return err
	}
	if slices.ContainsFunc(others, func(v string) bool { return v != "" }) {
		return i18n.T("--clear does not accept --name, --prefix, --format-hint or --help-url", "--clear não aceita --name, --prefix, --format-hint nem --help-url")
	}
	svc := e.service()
	srv, err := svc.GetServer(ctx, slug)
	if err != nil {
		return err
	}
	if srv.AuthKind != "oauth" {
		return i18n.T("--clear only works for oauth servers; %s uses auth %s", "--clear só vale para servidores oauth; %s usa auth %s", srv.Slug, srv.AuthKind)
	}
	if err := svc.UpdateServer(ctx, slug, admin.ServerUpdate{Header: &admin.HeaderConfig{}}); err != nil {
		return err
	}
	e.say("Alternative token of %s turned off.\n", "Token alternativo de %s desligado.\n", slug)
	return nil
}
