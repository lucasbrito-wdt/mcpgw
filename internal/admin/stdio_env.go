package admin

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// maxStdioEnvValue bounds one fixed variable edited after creation.
const maxStdioEnvValue = 4096

// validateStdioEnvEdit checks the shape of an edit: names, values, and no name both set and unset.
func validateStdioEnvEdit(set map[string]string, unset []string) error {
	if len(set) == 0 && len(unset) == 0 {
		return invalid("stdio_env", "no variable to add, replace or remove", "nenhuma variável para adicionar, trocar ou remover")
	}
	for name, value := range set {
		switch {
		case !stdioEnvNamePattern.MatchString(name):
			return invalid("stdio_env", "invalid variable name: %s (use A-Z, 0-9 and _, up to 64)", "nome de variável inválido: %s (use A-Z, 0-9 e _, até 64)", name)
		case value == "":
			return invalid("stdio_env", "provide a value for %s", "informe o valor de %s", name)
		case len(value) > maxStdioEnvValue:
			return invalid("stdio_env", "the value of %s exceeds %d characters", "o valor de %s passa de %d caracteres", name, maxStdioEnvValue)
		case strings.ContainsAny(value, "\r\n"):
			return invalid("stdio_env", "the value of %s cannot contain a line break", "o valor de %s não pode ter quebra de linha", name)
		}
	}
	for _, name := range unset {
		if !stdioEnvNamePattern.MatchString(name) {
			return invalid("stdio_env", "invalid variable name: %s (use A-Z, 0-9 and _, up to 64)", "nome de variável inválido: %s (use A-Z, 0-9 e _, até 64)", name)
		}
		if _, both := set[name]; both {
			return invalid("stdio_env", "%s cannot be replaced and removed at the same time", "%s não pode ser trocada e removida ao mesmo tempo", name)
		}
	}
	return nil
}

// SetStdioEnv merges an edit into the sealed fixed environment of a stdio server: names in set are
// added or replaced, names in unset removed (absent ones are ignored), the rest kept. Values are
// secrets: they are sealed with the same AAD and never audited or read back. A name equal to
// credential_env is rejected; a connection_env name is allowed (it acts as the default).
func (s *Service) SetStdioEnv(ctx context.Context, slug string, set map[string]string, unset []string) error {
	if err := validateStdioEnvEdit(set, unset); err != nil {
		return err
	}
	ev := Event{Action: "server.set_stdio_env", Target: slug, Details: map[string]any{
		"set": slices.Sorted(maps.Keys(set)), "unset": slices.Sorted(slices.Values(unset))}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		var id, transport string
		var credentialEnv, launcher, command, image *string
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT id::text, transport, credential_env, stdio_launcher, stdio_command, docker_image, stdio_env_ciphertext
			FROM upstream_servers WHERE slug = $1 FOR UPDATE`, slug).Scan(&id, &transport, &credentialEnv, &launcher, &command, &image, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound("server not found: %s", "servidor não encontrado: %s", slug)
		}
		if err != nil {
			return err
		}
		if transport != string(upstream.TransportStdio) {
			return invalid("stdio_env", "environment variables only apply to stdio servers", "variáveis de ambiente só valem para servidores stdio")
		}
		v, err := s.vault()
		if err != nil {
			return err
		}
		env := map[string]string{}
		if len(sealed) > 0 {
			if env, err = upstream.OpenStdioEnv(v, id, sealed); err != nil {
				return err
			}
		}
		maps.Copy(env, set)
		for _, name := range unset {
			delete(env, name)
		}
		spec := upstream.StdioSpec{Launcher: upstream.Launcher(deref(launcher)), Command: deref(command), Image: deref(image), Env: env}
		if _, clash := env[deref(credentialEnv)]; clash && credentialEnv != nil {
			return invalid("stdio_env", "%s is the credential_env (each user's token), not a fixed variable", "%s é a credential_env (o token de cada usuário), não uma variável fixa", *credentialEnv)
		}
		if err := spec.Validate(); err != nil {
			return invalid("stdio_env", "invalid variable: %v", "variável inválida: %v", err)
		}
		if sealed, err = upstream.SealStdioEnv(v, id, env); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE upstream_servers SET stdio_env_ciphertext = $2 WHERE id = $1::uuid`, id, sealed)
		return err
	})
}

// SetCredentialEnv makes a stdio server pass each user's token in the variable name (auth_kind
// header). A fixed variable of the same name is dropped from the sealed environment in the same
// transaction, so the shared value cannot linger as a fallback. Clearing is not supported.
func (s *Service) SetCredentialEnv(ctx context.Context, slug, name string) error {
	ev := Event{Action: "server.set_credential_env", Target: slug, Details: map[string]any{"credential_env": name, "removed_fixed": false}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		var id, transport string
		var launcher, command, image *string
		var connectionEnv []string
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT id::text, transport, stdio_launcher, stdio_command, docker_image, stdio_env_ciphertext, connection_env
			FROM upstream_servers WHERE slug = $1 FOR UPDATE`, slug).Scan(&id, &transport, &launcher, &command, &image, &sealed, &connectionEnv)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound("server not found: %s", "servidor não encontrado: %s", slug)
		}
		if err != nil {
			return err
		}
		if transport != string(upstream.TransportStdio) {
			return invalid("credential_env", "credential_env only applies to stdio servers", "credential_env só vale para servidores stdio")
		}
		spec := upstream.StdioSpec{Launcher: upstream.Launcher(deref(launcher)), Command: deref(command), Image: deref(image)}
		if err := validateStdioEnvNames(spec, name); err != nil {
			return err
		}
		if slices.Contains(connectionEnv, name) {
			return invalid("credential_env", "%s is already a per-connection variable", "%s já é uma variável por conexão", name)
		}
		removed := false
		if len(sealed) > 0 {
			v, err := s.vault()
			if err != nil {
				return err
			}
			env, err := upstream.OpenStdioEnv(v, id, sealed)
			if err != nil {
				return err
			}
			if _, removed = env[name]; removed {
				delete(env, name)
				if sealed, err = upstream.SealStdioEnv(v, id, env); err != nil {
					return err
				}
			}
		}
		ev.Details["removed_fixed"] = removed
		if removed {
			_, err = tx.Exec(ctx, `UPDATE upstream_servers SET stdio_env_ciphertext = $2 WHERE id = $1::uuid`, id, sealed)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE upstream_servers SET credential_env = $2, auth_kind = 'header' WHERE id = $1::uuid`, id, name)
		return err
	})
}
