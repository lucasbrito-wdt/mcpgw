package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

// ServerRef identifies an upstream server row.
type ServerRef struct{ ID, Slug, URL, AuthKind string }

// SyncServer lists the tools of one server with the credential of userID (ignored for the auth
// kinds none and header) and reconciles the tools table through db (a pool or a transaction).
func SyncServer(ctx context.Context, db store.DB, lister catalog.Lister, creds CredentialSource, srv ServerRef, userID string, limit time.Duration) (catalog.SyncReport, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cred, err := creds.AnyCredentialFor(ctx, srv.ID, userID)
	if err != nil {
		return catalog.SyncReport{}, fmt.Errorf("credential for %s: %w", srv.Slug, err)
	}
	return catalog.Sync(ctx, db, lister, catalog.Server{ID: srv.ID, Slug: srv.Slug, URL: srv.URL, Credential: cred})
}

// Sync reconciles the catalog of one server. OAuth servers need asUserID, a user whose account
// is already connected.
func (s *Service) Sync(ctx context.Context, slug, asUserID string) (catalog.SyncReport, error) {
	srv, err := serverBySlug(ctx, s.o.Pool, slug)
	if err != nil {
		return catalog.SyncReport{}, err
	}
	if srv.AuthKind == "oauth" && asUserID == "" {
		return catalog.SyncReport{}, invalid("as", "oauth servers require a user with an already connected account", "servidores oauth exigem um usuário com a conta já conectada")
	}
	if asUserID != "" && !validUUID(asUserID) {
		return catalog.SyncReport{}, notFound("user not found: %s", "usuário não encontrado: %s", asUserID)
	}
	mgr, err := s.manager()
	if err != nil {
		return catalog.SyncReport{}, err
	}
	var report catalog.SyncReport
	ev := Event{Action: "catalog.sync", Target: slug, Details: map[string]any{}} // filled once the sync ran
	ref := ServerRef{ID: srv.ID, Slug: srv.Slug, URL: srv.URL, AuthKind: srv.AuthKind}
	err = s.write(ctx, ev, func(tx pgx.Tx) error {
		var err error
		if report, err = SyncServer(ctx, tx, s.o.Lister, mgr, ref, asUserID, s.o.SyncTimeout); err != nil {
			return err
		}
		ev.Details["added"], ev.Details["changed"], ev.Details["pending"] = len(report.Added), len(report.Changed), len(report.Pending)
		ev.Details["reactivated"], ev.Details["quarantined"], ev.Details["unchanged"] = len(report.Reactivated), len(report.Quarantined), report.Unchanged
		return nil
	})
	if errors.Is(err, upstreamauth.ErrNotConnected) {
		return report, invalid("credential", "the server needs a credential: connect a token in the portal or configure the shared connection", "o servidor precisa de credencial: conecte um token no portal ou configure a conexão compartilhada")
	}
	return report, err
}
