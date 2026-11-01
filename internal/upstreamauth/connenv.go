package upstreamauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// maxEnvValueLen bounds one per-connection environment value.
const maxEnvValueLen = 2048

// ErrTokenRequired: an environment-only write targets a connection that does not exist (or is not
// active), so there is no token to keep.
var ErrTokenRequired = errors.New("token required for a new connection")

// EnvError rejects a per-connection environment value. Message is user-facing and never
// contains the value.
type EnvError struct {
	Name    string
	Message i18n.Text
}

func (e *EnvError) Error() string { return "connection env " + e.Name + ": " + e.Message.EN }

// Localize implements i18n.Localizer.
func (e *EnvError) Localize(l i18n.Locale) string { return e.Message.In(l) }

// ConnectionVar is a per-connection variable a stdio server declares. HasDefault: the server's
// fixed environment has a variable of that name, used when the connection leaves it unset.
type ConnectionVar struct {
	Name       string
	HasDefault bool
}

// ConnectionInput is a write to one connection: the user's personal profile, or the shared row.
type ConnectionInput struct {
	ServerID, ActorID, Profile string
	Shared                     bool
	// Token "" keeps the stored token (the connection must exist and be active).
	Token string
	// Env nil keeps the stored values; non-nil replaces them all (an unset variable falls back to
	// the server's fixed default).
	Env map[string]string
	// URL is the connection's own upstream address (servers with connection_url only); "" keeps
	// the stored one.
	URL string

	hint   *string // the server's token format hint, loaded by checkConnectionTarget
	method string  // upstream_credentials.method a token is stored with, loaded by checkConnectionTarget
}

// aad is the AAD of the row the input writes.
func (in ConnectionInput) aad() []byte {
	if in.Shared {
		return sharedAAD(in.ServerID)
	}
	return credentialProfileAAD(in.ServerID, in.ActorID, in.Profile)
}

// envAAD binds a connection's sealed environment to its row, apart from the token.
func envAAD(credentialAAD []byte) []byte { return slices.Concat(credentialAAD, []byte(":env")) }

// envWrite is the env_ciphertext an upsert stores; keep leaves the stored value untouched.
type envWrite struct {
	ct   []byte
	keep bool
}

// SetConnection stores a connection's token and/or its per-connection environment values and
// returns last4. With Token "" only the environment and/or URL change. Unknown names, bad values and
// missing required variables (no fixed default) are *EnvError; a bad or missing URL is *URLError.
// Who may write a shared row is the caller's decision.
func (m *Manager) SetConnection(ctx context.Context, db store.DB, in ConnectionInput) (string, error) {
	if in.Shared {
		in.Profile = DefaultProfile
	} else if !ValidProfile(in.Profile) {
		return "", ErrInvalidProfile
	}
	if in.Token != "" {
		return m.setToken(ctx, db, in)
	}
	if in.Env == nil && in.URL == "" {
		return "", ErrInvalidToken
	}
	in, err := m.checkConnectionTarget(ctx, db, in)
	if err != nil {
		return "", err
	}
	env, err := m.prepareEnv(ctx, db, in)
	if err != nil {
		return "", err
	}
	target, err := m.prepareURL(ctx, db, in)
	if err != nil {
		return "", err
	}
	var last4 string
	err = db.QueryRow(ctx, `UPDATE upstream_credentials
		SET env_ciphertext = CASE WHEN $6 THEN env_ciphertext ELSE $4 END,
		    url = CASE WHEN $8 THEN url ELSE $7 END, updated_at = now()
		WHERE server_id = $1::uuid AND status = 'active'
		    AND CASE WHEN $5 THEN shared ELSE user_id = NULLIF($2, '')::uuid AND profile = $3 END
		RETURNING COALESCE(last4, '')`, in.ServerID, in.ActorID, in.Profile, env.ct, in.Shared,
		env.keep, target.url, target.keep).Scan(&last4)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTokenRequired
	}
	return last4, wrapStore(err)
}

// checkConnectionTarget validates the actor and the server (an active header server, or oauth with a token alternative) and
// normalizes the ids.
func (m *Manager) checkConnectionTarget(ctx context.Context, db store.DB, in ConnectionInput) (ConnectionInput, error) {
	if !in.Shared && !validUUID(in.ActorID) || in.Shared && in.ActorID != "" && !validUUID(in.ActorID) {
		return in, errors.New("invalid user id")
	}
	hint, method, err := tokenServerTarget(ctx, db, in.ServerID)
	if err != nil {
		return in, err
	}
	in.hint, in.method = hint, method
	in.ServerID, in.ActorID = strings.ToLower(in.ServerID), strings.ToLower(in.ActorID)
	return in, nil
}

// prepareEnv validates in.Env against the server's declared variables and seals it. Env nil keeps
// the stored values of an existing connection; a new one must still satisfy the required ones.
func (m *Manager) prepareEnv(ctx context.Context, db store.DB, in ConnectionInput) (envWrite, error) {
	vars, err := m.ConnectionVars(ctx, db, in.ServerID)
	if err != nil {
		return envWrite{}, err
	}
	if in.Env == nil {
		exists, err := connectionExists(ctx, db, in)
		if err != nil || exists || len(vars) == 0 {
			return envWrite{keep: true}, err
		}
	}
	if err := checkEnv(vars, in.Env); err != nil {
		return envWrite{}, err
	}
	if len(in.Env) == 0 {
		return envWrite{}, nil
	}
	plain, err := json.Marshal(in.Env)
	if err != nil {
		return envWrite{}, err
	}
	ct, err := m.vault.Seal(plain, envAAD(in.aad()))
	return envWrite{ct: ct}, err
}

func connectionExists(ctx context.Context, db store.DB, in ConnectionInput) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM upstream_credentials
		WHERE server_id = $1::uuid AND status = 'active'
		    AND CASE WHEN $4 THEN shared ELSE user_id = NULLIF($2, '')::uuid AND profile = $3 END)`,
		in.ServerID, in.ActorID, in.Profile, in.Shared).Scan(&exists)
	return exists, err
}

func checkEnv(vars []ConnectionVar, env map[string]string) error {
	declared := make(map[string]bool, len(vars))
	for _, v := range vars {
		declared[v.Name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(env)) {
		value := env[name]
		if !declared[name] {
			return &EnvError{Name: name, Message: i18n.T("Variable not accepted by this server: %s.", "Variável não aceita por este servidor: %s.", name)}
		}
		if value == "" || len(value) > maxEnvValueLen || strings.ContainsAny(value, "\r\n\x00") {
			return &EnvError{Name: name, Message: i18n.T("Invalid value for %s: use 1 to 2048 characters, without line breaks.", "Valor inválido para %s: use de 1 a 2048 caracteres, sem quebras de linha.", name)}
		}
	}
	for _, v := range vars {
		if !v.HasDefault && env[v.Name] == "" {
			return &EnvError{Name: v.Name, Message: i18n.T("Provide %s (the server has no default value).", "Informe %s (o servidor não tem valor padrão).", v.Name)}
		}
	}
	return nil
}

// ConnectionVars lists the per-connection variables serverID declares, in declaration order.
// Empty for a server without any.
func (m *Manager) ConnectionVars(ctx context.Context, db store.DB, serverID string) ([]ConnectionVar, error) {
	if !validUUID(serverID) {
		return nil, ErrServerNotFound
	}
	var names []string
	var fixedCT []byte
	err := db.QueryRow(ctx, `SELECT connection_env, stdio_env_ciphertext FROM upstream_servers WHERE id = $1::uuid`, serverID).
		Scan(&names, &fixedCT)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrServerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load connection env: %w", err)
	}
	fixed := map[string]string{}
	if len(names) > 0 && len(fixedCT) > 0 {
		if fixed, err = upstream.OpenStdioEnv(m.vault, strings.ToLower(serverID), fixedCT); err != nil {
			return nil, err
		}
	}
	vars := make([]ConnectionVar, len(names))
	for i, name := range names {
		_, hasDefault := fixed[name]
		vars[i] = ConnectionVar{Name: name, HasDefault: hasDefault}
	}
	return vars, nil
}

// openConnectionEnv opens the connection's own environment values, keeping only the variables
// the server still declares. nil when there are none.
func (m *Manager) openConnectionEnv(srv *serverRow, row *credentialRow) (map[string]string, error) {
	if srv.CredentialEnv == nil || len(row.Env) == 0 {
		return nil, nil
	}
	env, err := m.openEnv(row.Env, row.aad())
	if err != nil {
		return nil, err
	}
	maps.DeleteFunc(env, func(name, _ string) bool { return !slices.Contains(srv.ConnectionEnv, name) })
	if len(env) == 0 {
		return nil, nil
	}
	return env, nil
}

func (m *Manager) openEnv(ct, credentialAAD []byte) (map[string]string, error) {
	plain, err := m.vault.Open(ct, envAAD(credentialAAD))
	if err != nil {
		return nil, fmt.Errorf("open connection env: %w", err)
	}
	env := map[string]string{}
	if err := json.Unmarshal(plain, &env); err != nil {
		return nil, fmt.Errorf("decode connection env: %w", err)
	}
	return env, nil
}

// ConnectionEnvSet returns the sorted names a personal connection has a value for (never the
// values), from its env_ciphertext. Never nil.
func (m *Manager) ConnectionEnvSet(serverID, userID, profile string, ct []byte) ([]string, error) {
	if len(ct) == 0 {
		return []string{}, nil
	}
	env, err := m.openEnv(ct, credentialProfileAAD(strings.ToLower(serverID), strings.ToLower(userID), profile))
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(env)), nil
}
