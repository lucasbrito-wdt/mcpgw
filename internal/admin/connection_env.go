package admin

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// maxConnectionEnv matches the schema bound on upstream_servers.connection_env.
const maxConnectionEnv = 16

// validateConnectionEnv checks the per-connection variable names of a stdio server: the stored name
// rule, no duplicates, only with a per-user credential and never its variable, and names the
// launcher accepts. A fixed variable of the same name is allowed: it is the connections' default.
func validateConnectionEnv(spec upstream.StdioSpec, credentialEnv string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if credentialEnv == "" {
		return invalid("connection_env", "per-connection variables require credential_env (each user's token)", "variáveis por conexão exigem credential_env (token de cada usuário)")
	}
	if len(names) > maxConnectionEnv {
		return invalid("connection_env", "at most %d variables per connection", "no máximo %d variáveis por conexão", maxConnectionEnv)
	}
	probe := make(map[string]string, len(names))
	for _, name := range names {
		switch _, dup := probe[name]; {
		case !stdioEnvNamePattern.MatchString(name):
			return invalid("connection_env", "invalid variable name: %s (use A-Z, 0-9 and _, up to 64)", "nome de variável inválido: %s (use A-Z, 0-9 e _, até 64)", name)
		case dup:
			return invalid("connection_env", "duplicate variable: %s", "variável repetida: %s", name)
		case name == credentialEnv:
			return invalid("connection_env", "%s is already the credential_env (the token)", "%s já é a credential_env (o token)", name)
		}
		probe[name] = "placeholder"
	}
	spec.Env = probe
	if err := spec.Validate(); err != nil {
		return invalid("connection_env", "invalid per-connection variable: %v", "variável por conexão inválida: %v", err)
	}
	return nil
}

// SetConnectionEnv replaces the per-connection variable names of a stdio server with a per-user
// credential (nil or empty clears them). Values already stored for a removed name stay sealed but
// are no longer applied.
func (s *Service) SetConnectionEnv(ctx context.Context, slug string, names []string) error {
	names = append([]string{}, names...)
	ev := Event{Action: "server.set_connection_env", Target: slug, Details: map[string]any{"names": names}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		var id, transport string
		var credentialEnv, launcher, command, image *string
		err := tx.QueryRow(ctx, `SELECT id::text, transport, credential_env, stdio_launcher, stdio_command, docker_image
			FROM upstream_servers WHERE slug = $1 FOR UPDATE`, slug).Scan(&id, &transport, &credentialEnv, &launcher, &command, &image)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound("server not found: %s", "servidor não encontrado: %s", slug)
		}
		if err != nil {
			return err
		}
		if transport != string(upstream.TransportStdio) {
			return invalid("connection_env", "per-connection variables only apply to stdio servers", "variáveis por conexão só valem para servidores stdio")
		}
		spec := upstream.StdioSpec{Launcher: upstream.Launcher(deref(launcher)), Command: deref(command), Image: deref(image)}
		if err := validateConnectionEnv(spec, deref(credentialEnv), names); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE upstream_servers SET connection_env = $2 WHERE id = $1::uuid`, id, names)
		return err
	})
}
