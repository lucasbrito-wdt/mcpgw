package oauth

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

const (
	levelNone        = "none"
	levelFieldPrefix = "level_"
)

// trustLevels in increasing order of privilege.
var trustLevels = []string{levelNone, "read", "write", "destructive"}

func levelRank(level string) int { return slices.Index(trustLevels, level) }

// consentServer is an upstream server the user may grant to the client, capped by the
// admin ceiling.
type consentServer struct {
	ID      string
	Slug    string
	Host    string
	Ceiling string
	Current string
}

type levelOption struct {
	Value, Label, Short string
	Selected            bool
}

// Options lists the levels up to the ceiling in loc, preselecting the current consent.
func (c consentServer) Options(loc i18n.Locale) []levelOption {
	var out []levelOption
	for _, l := range trustLevels[:levelRank(c.Ceiling)+1] {
		out = append(out, levelOption{Value: l, Label: levelLabels[l].In(loc), Short: levelShort[l].In(loc), Selected: l == c.Current})
	}
	return out
}

// consentServers lists active servers where the user has a trust ceiling, with the current
// unexpired consent for this client.
func (s *Server) consentServers(ctx context.Context, userID, clientID string, l i18n.Locale) ([]consentServer, error) {
	rows, err := s.pool.Query(ctx, `SELECT us.id::text, us.slug, us.url, tc.max_level, COALESCE(c.level, 'none')
		FROM trust_ceilings tc
		JOIN upstream_servers us ON us.id = tc.server_id AND us.status = 'active'
		LEFT JOIN consents c ON c.user_id = tc.user_id AND c.server_id = us.id AND c.client_id = $2
			AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at > $3)
		WHERE tc.user_id = $1::uuid
		ORDER BY us.slug`, userID, clientID, s.now())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (consentServer, error) {
		var c consentServer
		var rawURL string
		if err := row.Scan(&c.ID, &c.Slug, &rawURL, &c.Ceiling, &c.Current); err != nil {
			return c, err
		}
		if u, err := url.Parse(rawURL); err == nil {
			c.Host = u.Host
			if u.Scheme == "stdio" { // stdio://<server id> is a placeholder, not a place
				c.Host = i18n.T("local process", "processo local").In(l)
			}
		}
		if levelRank(c.Current) > levelRank(c.Ceiling) {
			c.Current = c.Ceiling
		}
		return c, nil
	})
}

// parseConsentForm reads the submitted level per server. The form is untrusted: unknown
// servers, unknown levels, repeated fields and levels above the ceiling are all rejected.
func parseConsentForm(form url.Values, servers []consentServer) (map[string]string, error) {
	known := make(map[string]consentServer, len(servers))
	for _, srv := range servers {
		known[srv.ID] = srv
	}
	for key, values := range form {
		id, ok := strings.CutPrefix(key, levelFieldPrefix)
		if !ok {
			continue
		}
		if _, ok := known[id]; !ok {
			return nil, fmt.Errorf("unknown server %q", id)
		}
		if len(values) != 1 {
			return nil, fmt.Errorf("repeated level for server %s", id)
		}
	}
	levels := make(map[string]string, len(servers))
	for _, srv := range servers {
		level := form.Get(levelFieldPrefix + srv.ID)
		if level == "" {
			level = levelNone
		}
		rank := levelRank(level)
		if rank < 0 {
			return nil, fmt.Errorf("unknown level %q", level)
		}
		if rank > levelRank(srv.Ceiling) {
			return nil, fmt.Errorf("level %s above ceiling %s for server %s", level, srv.Ceiling, srv.Slug)
		}
		levels[srv.ID] = level
	}
	return levels, nil
}

// saveConsents upserts granted levels and deletes the ones set to none, in one transaction.
func (s *Server) saveConsents(ctx context.Context, userID, clientID string, levels map[string]string) error {
	expiresAt := s.now().Add(consentTTL)
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for serverID, level := range levels {
			var err error
			if level == levelNone {
				_, err = tx.Exec(ctx, `DELETE FROM consents
					WHERE user_id = $1::uuid AND client_id = $2 AND server_id = $3::uuid`, userID, clientID, serverID)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO consents (user_id, client_id, server_id, level, expires_at)
					VALUES ($1::uuid, $2, $3::uuid, $4, $5)
					ON CONFLICT (user_id, client_id, server_id) DO UPDATE
					SET level = EXCLUDED.level, expires_at = EXCLUDED.expires_at, revoked_at = NULL`,
					userID, clientID, serverID, level, expiresAt)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Server) issueAuthorizationCode(ctx context.Context, req authzRequest, userID string) (string, error) {
	code, err := randomToken(32)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO oauth_authorization_codes
		(code_hash, client_id, user_id, redirect_uri, code_challenge, scope, resource, expires_at)
		VALUES ($1, $2, $3::uuid, $4, $5, $6, $7, $8)`,
		hashToken(code), req.ClientID, userID, req.RedirectURI, req.CodeChallenge, req.Scope,
		s.resource, s.now().Add(authorizationCodeTTL))
	return code, err
}
