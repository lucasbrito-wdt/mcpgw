package oauth

import (
	"net/http"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

// pageStrings is the copy of the login, consent and error pages in one locale. Fields ending in
// F are printf formats taking the client name (or host); every value is escaped by html/template.
type pageStrings struct {
	LoginTitle, LoginHeading, LoginLeadF, LoginHintF, Email, Password, SignIn            string
	ConsentTitle, ConsentHeadingF, ConsentLeadF, SignedInPre, SignedInPost, AccessLevels string
	NoServers, Note, Deny, Authorize                                                     string
	IdentityPublishedF, IdentityUnverifiedPre, IdentityUnverifiedPost, ReturnsTo         string
	LoopbackWarningF                                                                     string
	ErrorTitle, ErrorHeading, ErrorLead                                                  string
	InternalError                                                                        string
}

func stringsFor(l i18n.Locale) pageStrings {
	t := func(en, pt string) string { return i18n.Text{EN: en, PT: pt}.In(l) }
	return pageStrings{
		LoginTitle:      t("Sign in", "Entrar"),
		LoginHeading:    t("Sign in to mcpgw", "Entrar no mcpgw"),
		LoginLeadF:      t("%s wants to access your servers through mcpgw. Sign in to choose what it can do.", "O %s quer acessar seus servidores pelo mcpgw. Entre para escolher o que ele pode fazer."),
		LoginHintF:      t("Use your gateway account, not your %s account.", "Use a conta do gateway, não a do %s."),
		Email:           t("Email", "E-mail"),
		Password:        t("Password", "Senha"),
		SignIn:          t("Sign in", "Entrar"),
		ConsentTitle:    t("Authorize access", "Autorizar acesso"),
		ConsentHeadingF: t("Authorize %s", "Autorizar %s"),
		ConsentLeadF:    t("Choose what %s can do on each server. Calls are made on your behalf, through mcpgw.", "Escolha o que o %s pode fazer em cada servidor. As chamadas saem em seu nome, pelo mcpgw."),
		SignedInPre:     t("You are signed in as", "Você entrou como"),
		SignedInPost:    ".",
		AccessLevels:    t("Access level per server", "Nível de acesso por servidor"),
		NoServers:       t("No server is enabled for your account. Ask an administrator to set an access limit.", "Nenhum servidor está liberado para sua conta. Peça ao administrador para definir um limite de acesso."),
		Note:            t("Only the levels the administrator allowed are shown. Valid for 90 days and can be revoked in the portal.", "Só aparecem os níveis que o administrador liberou. Vale por 90 dias e pode ser revogado no portal."),
		Deny:            t("Deny", "Negar"),
		Authorize:       t("Authorize", "Autorizar"),

		IdentityPublishedF:     t("Identity published by %s", "Identidade publicada por %s"),
		IdentityUnverifiedPre:  t("Unverified identity: from dynamic registration", "Identidade não verificada: veio de registro dinâmico"),
		IdentityUnverifiedPost: "",
		ReturnsTo:              t("Returns to", "Retorno para"),
		LoopbackWarningF:       t("The return goes to a local address on this computer. Only continue if you just started this connection in %s.", "O retorno vai para um endereço local deste computador. Só continue se você acabou de iniciar esta conexão no %s."),

		ErrorTitle:    t("Error", "Erro"),
		ErrorHeading:  t("Could not continue", "Não foi possível continuar"),
		ErrorLead:     t("The connection was interrupted before reaching the servers.", "A conexão foi interrompida antes de chegar aos servidores."),
		InternalError: t("Internal error.", "Erro interno."),
	}
}

// requestLocale is the locale negotiated from the request's Accept-Language header.
func requestLocale(r *http.Request) i18n.Locale {
	return i18n.Negotiate(r.Header.Get("Accept-Language"))
}

var levelLabels = map[string]i18n.Text{
	levelNone:     i18n.T("No access", "Sem acesso"),
	"read":        i18n.T("Read only", "Somente leitura"),
	"write":       i18n.T("Read and write", "Leitura e escrita"),
	"destructive": i18n.T("Full (includes destructive actions)", "Total (inclui ações destrutivas)"),
}

// levelShort labels the segmented control, where the long labels would wrap.
var levelShort = map[string]i18n.Text{
	levelNone:     i18n.T("None", "Nenhum"),
	"read":        i18n.T("Read", "Leitura"),
	"write":       i18n.T("Write", "Escrita"),
	"destructive": i18n.T("Full", "Total"),
}
