package admin

import (
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

const (
	maxEmailLen   = 320
	maxHelpURLLen = 2048
	minHashPrefix = 12
	maxHashPrefix = 64
)

var (
	slugPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	headerPattern     = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	categoryPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	prefixPattern     = regexp.MustCompile(`^[\x20-\x7E]{0,32}$`)
	formatHintPattern = regexp.MustCompile(`^[\x21-\x7E]{1,64}$`)
	uuidPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	// /connect/callback and /connect/success are literal routes (migration 0002).
	reservedSlugs = map[string]bool{"callback": true, "success": true}
)

func ValidateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return invalid("slug", "invalid slug: use lowercase letters, digits and _, starting with a letter (up to 63 characters)", "slug inválido: use minúsculas, dígitos e _, começando com letra (até 63 caracteres)")
	}
	if reservedSlugs[slug] {
		return invalid("slug", "slug %q is reserved", "slug %q é reservado", slug)
	}
	return nil
}

// NormalizeEmail lowercases and validates a bare address (the users table stores lowercase).
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > maxEmailLen {
		return "", invalid("email", "invalid email", "e-mail inválido")
	}
	return email, nil
}

// ValidateAuthKind returns the stored auth kind: none, oauth or header (static_header is the
// legacy spelling of header and is normalized).
func ValidateAuthKind(kind string) (string, error) {
	switch kind {
	case "none", "oauth", "header":
		return kind, nil
	case "static_header":
		return "header", nil
	}
	return "", invalid("auth_kind", "must be none, oauth or header", "deve ser none, oauth ou header")
}

// ValidateServerURL accepts http(s) URLs without credentials or fragment. OAuth upstreams must
// be https with no loopback exception, as upstreamauth requires.
func ValidateServerURL(raw, authKind string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return invalid("url", "invalid URL: provide an absolute http(s) URL", "URL inválida: informe uma URL absoluta http(s)")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return invalid("url", "invalid URL: the scheme must be http or https", "URL inválida: o esquema deve ser http ou https")
	}
	if u.User != nil || u.Fragment != "" {
		return invalid("url", "invalid URL: it cannot contain credentials or a fragment", "URL inválida: não pode conter credenciais nem fragmento")
	}
	if authKind == "oauth" && u.Scheme != "https" {
		return invalid("url", "oauth servers require an https URL", "servidores oauth exigem URL https")
	}
	return nil
}

// ValidateCategory checks a non-empty portal category.
func ValidateCategory(c string) error {
	if !categoryPattern.MatchString(c) {
		return invalid("category", "invalid category: use lowercase letters, digits, _ and -, starting with a letter (up to 32 characters)", "categoria inválida: use minúsculas, dígitos, _ e -, começando com letra (até 32 caracteres)")
	}
	return nil
}

// ParseLevel accepts the real trust levels, and none when allowNone is set.
func ParseLevel(s string, allowNone bool) (policy.Level, error) {
	if s == "none" && allowNone {
		return policy.LevelNone, nil
	}
	if l, ok := policy.ParseLevel(s); ok {
		return l, nil
	}
	if allowNone {
		return 0, invalid("level", "level must be none, read, write or destructive", "o nível deve ser none, read, write ou destructive")
	}
	return 0, invalid("level", "level must be read, write or destructive", "o nível deve ser read, write ou destructive")
}

func ValidateHeaderName(name string) error {
	if !headerPattern.MatchString(name) {
		return invalid("name", "invalid header name: use letters, digits and hyphen (up to 100 characters)", "nome do header inválido: use letras, dígitos e hífen (até 100 caracteres)")
	}
	return nil
}

func ValidateEnvName(name string) error {
	if !envNamePattern.MatchString(name) {
		return invalid("env", "invalid environment variable name", "nome de variável de ambiente inválido")
	}
	return nil
}

// NormalizeHashPrefix validates an optional schema_hash prefix; "" means no hash was given.
func NormalizeHashPrefix(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if len(raw) < minHashPrefix || len(raw) > maxHashPrefix || strings.Trim(raw, "0123456789abcdef") != "" {
		return "", invalid("hash", "the hash must be %d to %d hexadecimal characters", "o hash deve ter de %d a %d caracteres hexadecimais", minHashPrefix, maxHashPrefix)
	}
	return raw, nil
}

// ValidateHelpURL accepts an https URL without credentials, up to 2048 bytes.
func ValidateHelpURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > maxHelpURLLen || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return invalid("header_help_url", "invalid help URL: use https, without credentials (up to %d characters)", "URL de ajuda inválida: use https, sem credenciais (até %d caracteres)", maxHelpURLLen)
	}
	return nil
}

// validateHeaderConfig checks the optional parts; an empty Name is allowed (set later).
func validateHeaderConfig(h HeaderConfig) error {
	if h.Name != "" {
		if err := ValidateHeaderName(h.Name); err != nil {
			return err
		}
	}
	if !prefixPattern.MatchString(h.Prefix) {
		return invalid("header_prefix", "invalid prefix: use up to 32 printable ASCII characters", "prefixo inválido: use até 32 caracteres ASCII imprimíveis")
	}
	if h.FormatHint != "" && !formatHintPattern.MatchString(h.FormatHint) {
		return invalid("header_format_hint", "invalid format hint: use up to 64 ASCII characters without spaces", "dica de formato inválida: use até 64 caracteres ASCII sem espaços")
	}
	if h.HelpURL != "" {
		return ValidateHelpURL(h.HelpURL)
	}
	return nil
}

func validUUID(s string) bool { return uuidPattern.MatchString(s) }
