package main

import (
	"bufio"
	"context"
	"flag"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

const envAdminPassword = "MCPGW_ADMIN_PASSWORD"

func userCreate(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("user create")
	emailFlag := fs.String("email", "", e.t("user e-mail", "e-mail do usuário"))
	isAdmin := fs.Bool("admin", false, e.t("create the user as admin", "cria o usuário como admin"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	email, err := normalizeEmail(*emailFlag)
	if err != nil {
		return err
	}
	password, err := e.readPassword()
	if err != nil {
		return err
	}
	u, err := e.service().CreateUser(ctx, admin.CreateUserInput{Email: email, Password: password, IsAdmin: *isAdmin})
	if err != nil {
		return err
	}
	e.say("User %s created.\n", "Usuário %s criado.\n", u.Email)
	return nil
}

// readPassword takes the password from MCPGW_ADMIN_PASSWORD or the first line of stdin, never argv.
func (e *adminEnv) readPassword() (string, error) {
	if p := e.getenv(envAdminPassword); p != "" {
		return p, nil
	}
	line, err := bufio.NewReader(e.in).ReadString('\n')
	if err != nil && line == "" {
		return "", i18n.T("provide the password in %s or on the first stdin line", "informe a senha em %s ou na primeira linha do stdin", envAdminPassword)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func userList(ctx context.Context, e *adminEnv, args []string) error {
	if err := e.newFlags("user list").Parse(args); err != nil {
		return err
	}
	users, err := e.service().ListUsers(ctx)
	if err != nil {
		return err
	}
	var out [][]string
	for _, u := range users {
		out = append(out, []string{u.Email, e.yesNo(u.IsAdmin), u.CreatedAt.UTC().Format("2006-01-02 15:04")})
	}
	e.table(e.t("EMAIL\tADMIN\tCREATED AT", "EMAIL\tADMIN\tCRIADO EM"), out)
	return nil
}

func userSetAdmin(ctx context.Context, e *adminEnv, args []string) error {
	fs := e.newFlags("user set-admin")
	email := fs.String("email", "", e.t("user e-mail", "e-mail do usuário"))
	isAdmin := fs.Bool("admin", false, e.t("true promotes, false removes admin access", "true promove, false remove o acesso de admin"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags("email", *email); err != nil {
		return err
	}
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "admin" })
	if !given {
		return i18n.T("--admin is required (true|false)", "--admin é obrigatório (true|false)")
	}
	normalized, err := normalizeEmail(*email)
	if err != nil {
		return err
	}
	svc := e.service()
	id, err := svc.UserIDByEmail(ctx, normalized)
	if err != nil {
		return err
	}
	if err := svc.SetAdmin(ctx, id, *isAdmin); err != nil {
		return err
	}
	if *isAdmin {
		e.say("%s is now an admin\n", "%s agora é admin\n", normalized)
	} else {
		e.say("%s is no longer an admin\n", "%s não é mais admin\n", normalized)
	}
	return nil
}
