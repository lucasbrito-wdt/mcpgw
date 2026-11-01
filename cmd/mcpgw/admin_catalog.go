package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

func catalogSync(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("catalog sync")
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	as := fs.String("as", "", e.t("e-mail of the user whose connected account is used (oauth servers)", "e-mail do usuário cuja conta conectada será usada (servidores oauth)"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	svc := e.service()
	srv, err := svc.GetServer(ctx, *slug)
	if err != nil {
		return err
	}
	var userID string
	switch {
	case srv.AuthKind == "oauth" && *as == "":
		return i18n.T("oauth servers require --as <e-mail> (a user with an already connected account)", "servidores oauth exigem --as <e-mail> (usuário com a conta já conectada)")
	case *as != "":
		if userID, err = svc.UserIDByEmail(ctx, *as); err != nil {
			return err
		}
	}
	report, err := svc.Sync(ctx, *slug, userID)
	if err != nil {
		return err
	}
	e.say("Catalog of %s synced: %d new, %d changed, %d awaiting approval, %d reactivated, %d quarantined, %d unchanged.\n",
		"Catálogo de %s sincronizado: %d novas, %d alteradas, %d aguardando aprovação, %d reativadas, %d em quarentena, %d inalteradas.\n",
		*slug, len(report.Added), len(report.Changed), len(report.Pending), len(report.Reactivated), len(report.Quarantined), report.Unchanged)
	if report.FirstSync {
		e.say("  first sync: definitions trusted as listed.\n", "  primeira sincronização: definições confiadas como listadas.\n")
	}
	for _, c := range report.Changed {
		e.say("  changed: %s (%s)\n", "  alterada: %s (%s)\n", safeText(c.Name, false), strings.Join(c.Kinds, ","))
	}
	// Names come from the upstream: every list goes through safeList.
	for _, l := range []struct {
		label   i18n.Text
		names   []string
		pending bool
	}{
		{i18n.T("new", "novas"), report.Added, false},
		{i18n.T("awaiting approval", "aguardando aprovação"), report.Pending, true},
		{i18n.T("reactivated", "reativadas"), report.Reactivated, false},
		{i18n.T("quarantined", "em quarentena"), report.Quarantined, false},
		{i18n.T("level recalculated", "nível recalculado"), report.Reclassified, false},
		{i18n.T("ignored (invalid name)", "ignoradas (nome inválido)"), report.Rejected, false},
	} {
		if len(l.names) == 0 {
			continue
		}
		suffix := ""
		if l.pending {
			suffix = " (use tool approve)"
		}
		e.printf("  %s: [%s]%s\n", l.label.In(e.loc), safeList(l.names), suffix)
	}
	return nil
}

func toolApprove(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("tool approve")
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	tool := fs.String("tool", "", e.t("name of the tool awaiting approval", "nome da tool aguardando aprovação"))
	all := fs.Bool("all", false, e.t("approve all tools of the server awaiting approval", "aprova todas as tools aguardando aprovação do servidor"))
	hash := fs.String("hash", "", e.t("hash of the reviewed definition (12+ hex, see tool list/show); refuses if the definition changed", "hash da definição revisada (12+ hex, veja tool list/show); recusa se a definição mudou"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug); err != nil {
		return err
	}
	if (*tool == "") == !*all {
		return i18n.T("provide --tool T or --all", "informe --tool T ou --all")
	}
	if *hash != "" && *tool == "" {
		return i18n.T("--hash only works with --tool T", "--hash só vale com --tool T")
	}
	svc := e.service()
	var approved []string
	var err error
	if *all {
		approved, err = svc.ApproveAll(ctx, *slug)
	} else {
		approved, err = svc.ApproveTool(ctx, *slug, *tool, *hash)
	}
	if err != nil {
		return err
	}
	if len(approved) == 0 {
		e.say("No tool of %s awaiting approval.\n", "Nenhuma tool de %s aguardando aprovação.\n", *slug)
		return nil
	}
	e.say("Approved in %s: %s.\n", "Aprovadas em %s: %s.\n", *slug, safeList(approved))
	return nil
}

func toolShow(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("tool show")
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	tool := fs.String("tool", "", e.t("tool name", "nome da tool"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug, "tool", *tool); err != nil {
		return err
	}
	t, err := e.service().GetTool(ctx, *slug, *tool)
	if err != nil {
		return err
	}
	changes := "-"
	if len(t.PendingChanges) > 0 {
		changes = strings.Join(t.PendingChanges, ",")
	}
	status, level, hash, desc := t.Status, t.Level, t.Hash, t.Description
	input, output, annotations := t.InputSchema, t.OutputSchema, t.Annotations
	// Everything below except the constants is upstream-sourced: neutralize it for the terminal.
	e.say("Tool: %s.%s\nStatus: %s\nLevel: %s\nPending changes: %s\nHash: %s\n", "Tool: %s.%s\nStatus: %s\nNível: %s\nAlterações pendentes: %s\nHash: %s\n", safeText(*slug, false),
		safeText(*tool, false), safeText(status, false), safeText(level, false), safeText(changes, false), safeText(hash, false))
	e.say("Description:\n%s\n", "Descrição:\n%s\n", safeText(desc, true))
	for _, f := range []struct{ label, raw string }{{"Input schema", input}, {"Output schema", output}, {"Annotations", annotations}} {
		e.printf("%s:\n%s\n", f.label, safeText(prettyJSON(f.raw), true))
	}
	return nil
}

// prettyJSON indents raw JSON, leaving it untouched if it does not parse.
func prettyJSON(raw string) string {
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(raw), "", "  ") != nil {
		return raw
	}
	return buf.String()
}
