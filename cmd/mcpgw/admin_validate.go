package main

import (
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

// The validators are thin wrappers over internal/admin; those whose message names a CLI flag
// rewrite it so the terminal output keeps its wording.

func validateSlug(slug string) error { return admin.ValidateSlug(slug) }

func normalizeEmail(raw string) (string, error) { return admin.NormalizeEmail(raw) }

func validateAuthKind(kind string) error {
	if _, err := admin.ValidateAuthKind(kind); err != nil {
		return i18n.T("--auth must be none, oauth or header", "--auth deve ser none, oauth ou header")
	}
	return nil
}

func validateServerURL(raw, authKind string) error { return admin.ValidateServerURL(raw, authKind) }

func parseLevel(s string, allowNone bool) (policy.Level, error) {
	l, err := admin.ParseLevel(s, allowNone)
	switch {
	case err == nil:
		return l, nil
	case allowNone:
		return 0, i18n.T("--level must be none, read, write or destructive", "--level deve ser none, read, write ou destructive")
	}
	return 0, i18n.T("--level must be read, write or destructive", "--level deve ser read, write ou destructive")
}

func validateHeaderName(name string) error {
	if admin.ValidateHeaderName(name) != nil {
		return i18n.T("invalid --name: use letters, digits and hyphens (up to 100 characters)", "--name inválido: use letras, dígitos e hífen (até 100 caracteres)")
	}
	return nil
}

func validateEnvName(name string) error { return admin.ValidateEnvName(name) }

// requireFlags takes name/value pairs and fails with a localized message on the first empty value.
func requireFlags(pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) == "" {
			return i18n.T("--%s is required", "--%s é obrigatório", pairs[i])
		}
	}
	return nil
}
