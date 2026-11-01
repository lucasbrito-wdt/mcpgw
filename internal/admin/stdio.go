package admin

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

// validateStdioAuth: a stdio server either has no credential (auth none, secrets in the fixed env)
// or takes each user's token through credential_env (auth header). A header config may carry
// only the portal hints (format hint, help URL): no header name or prefix applies to a process.
func validateStdioAuth(in AddServerInput, authKind string) error {
	credentialEnv := in.Stdio.CredentialEnv
	switch {
	case credentialEnv == "" && (authKind != "none" || in.Header != nil):
		return invalid("auth_kind", "stdio servers require auth none (secrets go in env) or credential_env", "servidores stdio exigem auth none (segredos vão em env) ou credential_env")
	case credentialEnv != "" && authKind != "header":
		return invalid("auth_kind", "with credential_env the auth must be header (each user's token)", "com credential_env o auth deve ser header (token de cada usuário)")
	case in.Header != nil && (in.Header.Name != "" || in.Header.Prefix != ""):
		return invalid("header", "stdio servers do not use a header name or prefix", "servidores stdio não usam nome nem prefixo de header")
	}
	return validateHeaderFor(authKind, in.Header)
}

var stdioEnvNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// validateStdioEnvNames checks the fixed env names and credential_env against the stored name rule,
// and credential_env against collisions and the launcher's reserved names (by validating the spec
// with it added).
func validateStdioEnvNames(spec upstream.StdioSpec, credentialEnv string) error {
	for name := range spec.Env {
		if !stdioEnvNamePattern.MatchString(name) {
			return invalid("stdio_env", "invalid variable name: %s (use A-Z, 0-9 and _, up to 64)", "nome de variável inválido: %s (use A-Z, 0-9 e _, até 64)", name)
		}
	}
	if credentialEnv == "" {
		return nil
	}
	if !stdioEnvNamePattern.MatchString(credentialEnv) {
		return invalid("credential_env", "invalid variable name: use A-Z, 0-9 and _, up to 64", "nome de variável inválido: use A-Z, 0-9 e _, até 64")
	}
	if _, fixed := spec.Env[credentialEnv]; fixed {
		return invalid("credential_env", "credential_env %s collides with a fixed env variable", "credential_env %s colide com uma variável fixa do env", credentialEnv)
	}
	spec.Env = maps.Clone(spec.Env)
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	spec.Env[credentialEnv] = "placeholder"
	if err := spec.Validate(); err != nil {
		return invalid("credential_env", "invalid credential_env: %v", "credential_env inválido: %v", err)
	}
	return nil
}

// stdioEnvNames opens the sealed fixed environment of a stdio server and returns its sorted names.
func (s *Service) stdioEnvNames(ctx context.Context, serverID string) ([]string, error) {
	var sealed []byte
	if err := s.o.Pool.QueryRow(ctx, `SELECT stdio_env_ciphertext FROM upstream_servers WHERE id = $1::uuid`, serverID).Scan(&sealed); err != nil {
		return nil, err
	}
	names := []string{}
	if len(sealed) == 0 {
		return names, nil
	}
	v, err := s.vault()
	if err != nil {
		return nil, err
	}
	env, err := upstream.OpenStdioEnv(v, serverID, sealed)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(env)), nil
}

func (s *Service) vault() (*vault.Vault, error) {
	if s.o.Vault == nil {
		return nil, errors.New("admin: vault is not configured")
	}
	return s.o.Vault()
}
