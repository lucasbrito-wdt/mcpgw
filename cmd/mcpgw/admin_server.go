package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// serverAddFlags holds the parsed `server add` flags.
type serverAddFlags struct {
	slug, url, auth, transport, image, command *string
	credentialEnv                              *string
	hostProcess, network, autoApprove          *bool
	args, env                                  stringList
}

func serverAdd(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server add")
	f := serverAddFlags{
		slug:          fs.String("slug", "", e.t("server identifier (namespace in code)", "identificador do servidor (namespace no código)")),
		url:           fs.String("url", "", e.t("MCP server URL (not used with --transport stdio)", "URL do servidor MCP (não usada com --transport stdio)")),
		auth:          fs.String("auth", "", e.t("none, oauth or header (stdio: none, or header with --credential-env)", "none, oauth ou header (stdio: none, ou header com --credential-env)")),
		transport:     fs.String("transport", "auto", e.t("auto, streamable_http, sse or stdio", "auto, streamable_http, sse ou stdio")),
		image:         fs.String("docker-image", "", e.t("stdio: docker image (default launcher, isolated and without network)", "stdio: imagem docker (launcher padrão, isolado e sem rede)")),
		command:       fs.String("command", "", e.t("stdio: with --host-process, absolute path of the executable; with docker, command after the image", "stdio: com --host-process, caminho absoluto do executável; com docker, comando após a imagem")),
		hostProcess:   fs.Bool("host-process", false, e.t("stdio: run the command directly on the host, without a container (explicit opt-in)", "stdio: roda o comando direto no host, sem container (opt-in explícito)")),
		network:       fs.Bool("network", false, e.t("stdio docker: allow network access for the container", "stdio docker: libera rede para o container")),
		autoApprove:   fs.Bool("auto-approve-new-tools", false, e.t("tools that appear after the first sync become active without tool approve", "tools novas após a 1a sincronização ficam ativas sem tool approve")),
		credentialEnv: fs.String("credential-env", "", e.t("stdio: variable that receives each user's token (pasted on the portal); implies --auth header", "stdio: variável que recebe o token de cada usuário (colado no portal); implica --auth header")),
	}
	fs.Var(&f.args, "arg", e.t("stdio: process argument (repeatable)", "stdio: argumento do processo (repetível)"))
	fs.Var(&f.env, "env", e.t("stdio: NAME=SOURCE_VAR; the value is read from the environment and stored encrypted (repeatable)", "stdio: NOME=VARIAVEL_DE_ORIGEM; o valor é lido do ambiente e guardado cifrado (repetível)"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *f.credentialEnv != "" && *f.auth == "" {
		*f.auth = "header"
	}
	if err := requireFlags("slug", *f.slug, "auth", *f.auth); err != nil {
		return err
	}
	if err := validateAuthKind(*f.auth); err != nil {
		return err
	}
	in := admin.AddServerInput{Slug: *f.slug, URL: *f.url, AuthKind: *f.auth, Transport: *f.transport, AutoApproveNewTools: *f.autoApprove}
	switch upstream.TransportKind(*f.transport) {
	case upstream.TransportStdio:
		stdio, err := stdioInputFromFlags(e, f)
		if err != nil {
			return err
		}
		in.Stdio = &stdio
	case upstream.TransportAuto, upstream.TransportStreamable, upstream.TransportSSE:
		if *f.image != "" || *f.command != "" || *f.hostProcess || *f.network || len(f.args) > 0 || len(f.env) > 0 || *f.credentialEnv != "" {
			return i18n.T("--docker-image, --command, --host-process, --network, --arg, --env and --credential-env only work with --transport stdio", "--docker-image, --command, --host-process, --network, --arg, --env e --credential-env só valem com --transport stdio")
		}
		if err := requireFlags("url", *f.url); err != nil {
			return err
		}
	default:
		return i18n.T("--transport must be auto, streamable_http, sse or stdio", "--transport deve ser auto, streamable_http, sse ou stdio")
	}
	if _, err := e.service().AddServer(ctx, in); err != nil {
		return err
	}
	e.say("Server %s added (%s, %s).\n", "Servidor %s adicionado (%s, %s).\n", *f.slug, *f.transport, *f.auth)
	return nil
}

// stdioInputFromFlags reads the --env sources from the environment; internal/admin validates the rest.
func stdioInputFromFlags(e *adminEnv, f serverAddFlags) (admin.StdioInput, error) {
	in := admin.StdioInput{Launcher: string(upstream.LauncherDocker), Command: *f.command, Args: []string(f.args), Image: *f.image,
		Network: *f.network, CredentialEnv: *f.credentialEnv}
	if *f.hostProcess {
		in.Launcher = string(upstream.LauncherHost)
	}
	var err error
	in.Env, err = envFromSources(e, f.env)
	return in, err
}

// envFromSources turns NAME=SOURCE_VAR pairs into NAME -> value, reading each value from the
// SOURCE_VAR variable of this process (nil when there are no pairs).
func envFromSources(e *adminEnv, pairs stringList) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, source, ok := strings.Cut(pair, "=")
		if !ok || name == "" || source == "" {
			return nil, i18n.T("--env %q: use NAME=SOURCE_VAR", "--env %q: use NOME=VARIAVEL_DE_ORIGEM", name)
		}
		if _, dup := env[name]; dup {
			return nil, i18n.T("--env %s repeated", "--env %s repetido", name)
		}
		value, err := e.secretFromEnv("env", source)
		if err != nil {
			return nil, err
		}
		env[name] = value
	}
	return env, nil
}

func serverList(ctx context.Context, e *adminEnv, args []string) error {
	if err := e.newFlags("server list").Parse(args); err != nil {
		return err
	}
	servers, err := e.service().ListServers(ctx)
	if err != nil {
		return err
	}
	var out [][]string
	for _, v := range servers {
		out = append(out, []string{v.Slug, v.URL, v.Transport, v.AuthKind, v.Status, fmt.Sprint(v.ActiveTools),
			e.yesNo(v.HasSharedConnection), e.yesNo(v.HasOAuthClient)})
	}
	e.table("SLUG\tURL\tTRANSPORT\tAUTH\tSTATUS\tTOOLS\tHEADER\tCLIENT OAUTH", out)
	return nil
}

func (e *adminEnv) yesNo(b bool) string {
	if b {
		return e.t("yes", "sim")
	}
	return e.t("no", "não")
}

func serverDisable(ctx context.Context, e *adminEnv, args []string) error {
	return setServerStatus(ctx, e, "server disable", "disabled", i18n.T("disabled", "desabilitado"), args)
}

func serverEnable(ctx context.Context, e *adminEnv, args []string) error {
	return setServerStatus(ctx, e, "server enable", "active", i18n.T("enabled", "habilitado"), args)
}

func setServerStatus(ctx context.Context, e *adminEnv, name, status string, label i18n.Text, args []string) error {
	fs := e.newFlags(name)
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	if err := e.service().SetStatus(ctx, *slug, status); err != nil {
		return err
	}
	e.say("Server %s %s.\n", "Servidor %s %s.\n", *slug, label.In(e.loc))
	return nil
}

func serverRemove(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server remove")
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	yes := fs.Bool("yes", false, e.t("confirm the removal", "confirma a remoção"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	svc := e.service()
	if !*yes {
		d, err := svc.ServerDependents(ctx, *slug)
		if err != nil {
			return err
		}
		e.say("Will be removed: server %s, %d tools, %d credentials, %d consents.\n", "Serão removidos: servidor %s, %d tools, %d credenciais, %d consentimentos.\n", *slug, d.Tools, d.Credentials, d.Consents)
		return i18n.T("the removal is irreversible; run again with --yes to confirm", "a remoção é irreversível; execute novamente com --yes para confirmar")
	}
	if err := svc.RemoveServer(ctx, *slug); err != nil {
		return err
	}
	e.say("Server %s removed.\n", "Servidor %s removido.\n", *slug)
	return nil
}

func serverSetHeader(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-header")
	slug := fs.String("slug", "", e.t("server slug (header auth)", "slug do servidor (auth header)"))
	name := fs.String("name", "", e.t("HTTP header name", "nome do header HTTP"))
	valueEnv := fs.String("value-env", "", e.t("environment variable holding the header value", "variável de ambiente com o valor do header"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug, "name", *name, "value-env", *valueEnv); err != nil {
		return err
	}
	if err := validateHeaderName(*name); err != nil {
		return err
	}
	value, err := e.secretFromEnv("value-env", *valueEnv)
	if err != nil {
		return err
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return i18n.T("the header value contains invalid characters", "o valor do header contém caracteres inválidos")
	}
	svc := e.service()
	srv, err := svc.GetServer(ctx, *slug)
	if err != nil {
		return err
	}
	if srv.Header == nil {
		return i18n.T("server %s uses auth %s, not header", "o servidor %s usa auth %s, não header", srv.Slug, srv.AuthKind)
	}
	header := *srv.Header // keep the prefix, hint and help URL as configured
	header.Name = *name
	if err := svc.UpdateServer(ctx, *slug, admin.ServerUpdate{Header: &header}); err != nil {
		return err
	}
	if _, err := svc.SetSharedHeaderToken(ctx, *slug, "", value); err != nil {
		return err
	}
	e.say("Header %s set for %s.\n", "Header %s configurado para %s.\n", *name, srv.Slug)
	return nil
}

func serverSetOAuthClient(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-oauth-client")
	slug := fs.String("slug", "", e.t("server slug (oauth auth)", "slug do servidor (auth oauth)"))
	clientID := fs.String("client-id", "", e.t("pre-registered client_id", "client_id pré-registrado"))
	secretEnv := fs.String("secret-env", "", e.t("environment variable holding the client secret (omit for a public client)", "variável de ambiente com o client secret (omita para cliente público)"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug, "client-id", *clientID); err != nil {
		return err
	}
	var secret string
	if *secretEnv != "" {
		var err error
		if secret, err = e.secretFromEnv("secret-env", *secretEnv); err != nil {
			return err
		}
	}
	if err := e.service().SetOAuthClient(ctx, *slug, *clientID, secret); err != nil {
		return err
	}
	e.say("OAuth client set for %s.\n", "Cliente OAuth configurado para %s.\n", *slug)
	return nil
}

// serverSetTrust changes the catalog trust settings of a server; only the flags given change.
func serverSetTrust(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("server set-trust")
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	annotations := fs.Bool("annotations", false, e.t("trust readOnlyHint to lower the tools level (true|false)", "confia em readOnlyHint para baixar o nível das tools (true|false)"))
	autoApprove := fs.Bool("auto-approve-new-tools", false, e.t("new tools become active without tool approve (true|false)", "tools novas ficam ativas sem tool approve (true|false)"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if !given["annotations"] && !given["auto-approve-new-tools"] {
		return i18n.T("provide --annotations=true|false and/or --auto-approve-new-tools=true|false", "informe --annotations=true|false e/ou --auto-approve-new-tools=true|false")
	}
	var in admin.TrustInput
	if given["annotations"] {
		in.Annotations = annotations
	}
	if given["auto-approve-new-tools"] {
		in.AutoApproveNewTools = autoApprove
	}
	changed, err := e.service().SetTrust(ctx, *slug, in)
	if err != nil {
		return err
	}
	if given["annotations"] {
		e.say("Annotations of %s: %s trusted; %d tools with recalculated level.\n", "Anotações de %s: %s confiáveis; %d tools com nível recalculado.\n", *slug, e.trustLabel(*annotations), len(changed))
	}
	if given["auto-approve-new-tools"] {
		e.say("Automatic approval of new tools in %s: %s.\n", "Aprovação automática de tools novas em %s: %s.\n", *slug, e.yesNo(*autoApprove))
	}
	return nil
}

func (e *adminEnv) trustLabel(trusted bool) string {
	if trusted {
		return e.t("now", "agora")
	}
	return e.t("no longer", "não mais")
}
