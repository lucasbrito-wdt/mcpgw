// Package admin is the administration core shared by the mcpgw admin CLI and the /admin/api
// handlers: typed inputs and results, validation and SQL. No operation is implemented twice.
package admin

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

const defaultSyncTimeout = 2 * time.Minute

// Event describes one successful write for the admin audit log. Details never hold secret values.
type Event struct {
	Action  string // "<resource>.<verb>", e.g. "server.set_header"
	Target  string // slug, email, "<slug>/<tool>", family id...
	Details map[string]any
}

// AuditFunc records ev inside tx, the transaction of the write it describes.
type AuditFunc func(ctx context.Context, tx pgx.Tx, ev Event) error

// CredentialSource is the part of upstreamauth.Manager catalog sync needs.
type CredentialSource interface {
	AnyCredentialFor(ctx context.Context, serverID, userID string) (upstream.Credential, error)
}

// GrantRevoker ends an OAuth refresh family and evicts its cached access tokens.
type GrantRevoker interface {
	RevokeFamily(ctx context.Context, familyID string) error
}

type Options struct {
	Pool *pgxpool.Pool
	// Credentials returns the upstream auth manager; it errors when the master key is missing,
	// so operations that need no secrets work without one (CLI user/ceiling/tool commands).
	Credentials func() (*upstreamauth.Manager, error)
	// Vault returns the vault that seals stdio environments; same laziness as Credentials.
	// Only AddServer with a stdio env needs it.
	Vault  func() (*vault.Vault, error)
	Lister catalog.Lister
	// Grants revokes OAuth refresh families (oauth.Server satisfies it); used by Task 3.3.
	// nil in the CLI. Declared here so 3.3 does not edit service.go.
	Grants GrantRevoker
	Audit  AuditFunc // nil: no audit (CLI)
	// AllowHostLauncher permits stdio servers with launcher=host (CLI only, never the web).
	AllowHostLauncher bool
	SyncTimeout       time.Duration // default 2 * time.Minute (the CLI's catalogSyncLimit)
}

type Service struct{ o Options }

func New(o Options) *Service {
	if o.SyncTimeout <= 0 {
		o.SyncTimeout = defaultSyncTimeout
	}
	return &Service{o: o}
}

// write runs fn and the audit insert in one transaction.
func (s *Service) write(ctx context.Context, ev Event, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.o.Pool, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if s.o.Audit == nil {
			return nil
		}
		return s.o.Audit(ctx, tx, ev)
	})
}

func (s *Service) manager() (*upstreamauth.Manager, error) {
	if s.o.Credentials == nil {
		return nil, errors.New("admin: credentials are not configured")
	}
	return s.o.Credentials()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
