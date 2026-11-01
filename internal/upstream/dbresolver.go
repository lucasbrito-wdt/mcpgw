package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

// DBResolver resolves endpoint keys from upstream_servers (migration 0004) and persists the
// detected transport and protocol era there. Stdio environments are opened with the vault.
type DBResolver struct {
	pool  *pgxpool.Pool
	vault *vault.Vault
}

// NewDBResolver builds a resolver. v may be nil when no stdio server has a sealed environment.
func NewDBResolver(pool *pgxpool.Pool, v *vault.Vault) *DBResolver {
	return &DBResolver{pool: pool, vault: v}
}

const resolveQuery = `SELECT id::text, transport, stdio_launcher, stdio_command, stdio_args,
	stdio_env_ciphertext, docker_image, stdio_network, detected_transport, detected_protocol_version
	FROM upstream_servers WHERE url = $1 ORDER BY created_at LIMIT 1`

type serverRow struct {
	id, transport            string
	launcher, command, image *string
	args                     []string
	envCT                    []byte
	network                  bool
	detTransport, detVersion *string
}

// Resolve implements Resolver. An http(s) key with no row resolves to auto-detection.
func (r *DBResolver) Resolve(ctx context.Context, key string) (Endpoint, error) {
	var row serverRow
	err := r.pool.QueryRow(ctx, resolveQuery, key).Scan(&row.id, &row.transport, &row.launcher, &row.command,
		&row.args, &row.envCT, &row.image, &row.network, &row.detTransport, &row.detVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return urlResolver{}.Resolve(ctx, key)
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("resolve upstream: %w", err)
	}
	ep := Endpoint{
		Transport: TransportKind(row.transport),
		Detected:  Detection{Transport: TransportKind(deref(row.detTransport)), ProtocolVersion: deref(row.detVersion)},
	}
	if ep.Transport != TransportStdio {
		ep.URL = key
		return ep, nil
	}
	spec := StdioSpec{
		Launcher: Launcher(deref(row.launcher)), Command: deref(row.command), Args: row.args,
		Image: deref(row.image), Network: row.network,
	}
	if spec.Env, err = r.openEnv(row.id, row.envCT); err != nil {
		return Endpoint{}, err
	}
	ep.Stdio = &spec
	return ep, nil
}

func (r *DBResolver) openEnv(serverID string, ct []byte) (map[string]string, error) {
	if len(ct) == 0 {
		return map[string]string{}, nil
	}
	if r.vault == nil {
		return nil, errors.New("stdio environment is sealed but no vault is configured")
	}
	return OpenStdioEnv(r.vault, serverID, ct)
}

// OpenStdioEnv opens an environment sealed by SealStdioEnv.
func OpenStdioEnv(v *vault.Vault, serverID string, ct []byte) (map[string]string, error) {
	plain, err := v.Open(ct, StdioEnvAAD(serverID))
	if err != nil {
		return nil, fmt.Errorf("open stdio environment: %w", err)
	}
	env := map[string]string{}
	if err := json.Unmarshal(plain, &env); err != nil {
		return nil, fmt.Errorf("decode stdio environment: %w", err)
	}
	return env, nil
}

// RecordDetection implements Resolver.
func (r *DBResolver) RecordDetection(ctx context.Context, key string, d Detection) error {
	_, err := r.pool.Exec(ctx, `UPDATE upstream_servers SET detected_transport = $2,
		detected_protocol_version = NULLIF($3, ''), detected_at = now() WHERE url = $1`,
		key, string(d.Transport), d.ProtocolVersion)
	return err
}

// StdioEnvAAD binds a sealed stdio environment to its upstream_servers row.
func StdioEnvAAD(serverID string) []byte {
	return []byte("upstream_servers:" + strings.ToLower(serverID) + ":stdio_env")
}

// SealStdioEnv seals the environment of a stdio server for upstream_servers.stdio_env_ciphertext.
// An empty environment seals to nil (no column value).
func SealStdioEnv(v *vault.Vault, serverID string, env map[string]string) ([]byte, error) {
	if len(env) == 0 {
		return nil, nil
	}
	plain, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return v.Seal(plain, StdioEnvAAD(serverID))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
