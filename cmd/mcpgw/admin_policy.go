package main

import (
	"context"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

const shortHashLen = 12

func ceilingSet(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("ceiling set")
	email := fs.String("email", "", e.t("user e-mail", "e-mail do usuário"))
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	levelFlag := fs.String("level", "", e.t("none, read, write or destructive", "none, read, write ou destructive"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("email", *email, "slug", *slug, "level", *levelFlag); err != nil {
		return err
	}
	level, err := parseLevel(*levelFlag, true)
	if err != nil {
		return err
	}
	svc := e.service()
	userID, err := svc.UserIDByEmail(ctx, *email)
	if err != nil {
		return err
	}
	if err := svc.SetCeiling(ctx, userID, *slug, level); err != nil {
		return err
	}
	e.say("Ceiling of %s on %s: %s.\n", "Teto de %s em %s: %s.\n", *email, *slug, level)
	return nil
}

func ceilingList(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("ceiling list")
	email := fs.String("email", "", e.t("filter by e-mail", "filtrar por e-mail"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	ceilings, err := e.service().ListCeilings(ctx, admin.CeilingFilter{Email: *email})
	if err != nil {
		return err
	}
	var out [][]string
	for _, c := range ceilings {
		out = append(out, []string{c.Email, c.ServerSlug, c.Level})
	}
	e.table(e.t("EMAIL\tSERVER\tCEILING", "EMAIL\tSERVIDOR\tTETO"), out)
	return nil
}

func toolList(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("tool list")
	slug := fs.String("slug", "", e.t("filter by server", "filtrar por servidor"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	tools, err := e.service().ListTools(ctx, admin.ToolFilter{Server: *slug})
	if err != nil {
		return err
	}
	// CHANGED lists what changed since the last approval (new, description, schema, annotations).
	var out [][]string
	for _, t := range tools {
		origin := "auto"
		if t.Overridden {
			origin = "manual"
		}
		changes := "-"
		if len(t.PendingChanges) > 0 {
			changes = strings.Join(t.PendingChanges, ",")
		}
		out = append(out, []string{t.ServerSlug, t.Name, t.Level, origin, t.Status, changes, shortHash(t.Hash)})
	}
	e.table(e.t("SERVER\tTOOL\tLEVEL\tORIGIN\tSTATUS\tCHANGED\tHASH", "SERVIDOR\tTOOL\tNIVEL\tORIGEM\tSTATUS\tALTERADO\tHASH"), out)
	return nil
}

// shortHash is the 12-hex form shown in tables and accepted by tool approve --hash.
func shortHash(h string) string {
	if len(h) > shortHashLen {
		return h[:shortHashLen]
	}
	return h
}

func toolLevel(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("tool level")
	slug := fs.String("slug", "", e.t("server slug", "slug do servidor"))
	tool := fs.String("tool", "", e.t("tool name", "nome da tool"))
	levelFlag := fs.String("level", "", e.t("read, write or destructive", "read, write ou destructive"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("slug", *slug, "tool", *tool, "level", *levelFlag); err != nil {
		return err
	}
	level, err := parseLevel(*levelFlag, false)
	if err != nil {
		return err
	}
	if err := e.service().SetToolLevel(ctx, *slug, *tool, level); err != nil {
		return err
	}
	e.say("Level of %s.%s set to %s (manual).\n", "Nível de %s.%s definido como %s (manual).\n", *slug, *tool, level)
	return nil
}
