package portalapi

import (
	"context"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"net/url"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

// profileParam reads the optional ?profile= of a write ("" means default). Same query parameter
// on every endpoint, so DELETE and GET work without a body. On false the 400 was written.
func profileParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		return upstreamauth.DefaultProfile, true
	}
	if !upstreamauth.ValidProfile(profile) {
		apijson.WriteErrorFor(w, r, http.StatusBadRequest, "invalid_profile",
			i18n.T("Invalid connection name: use lowercase letters, numbers, - or _, starting with a letter (up to 32 characters).", "Nome de conexão inválido: use letras minúsculas, números, - ou _, começando por letra (até 32 caracteres)."), nil)
		return "", false
	}
	return profile, true
}

// profileJSON is one personal connection of the user to a server. The token is never included.
type profileJSON struct {
	Profile      string     `json:"profile"`
	Status       string     `json:"status"`
	Last4        *string    `json:"last4"`
	AccountLabel *string    `json:"account_label"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	ConnectedAt  time.Time  `json:"connected_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	// Method is how this connection authenticates: "oauth" or "token".
	Method string `json:"method"`
	// EnvSet are the server's per-connection variables this connection has a value for (names only).
	EnvSet []string `json:"env_set"`
	// URLHost is the host of the connection's own MCP URL (servers with connection_url); never
	// the full URL, which may carry a query.
	URLHost *string `json:"url_host"`
}

// profilesFor lists the session user's personal rows of the given servers, default first. Servers
// without personal rows get an empty (non-nil) slice.
func (a *API) profilesFor(ctx context.Context, uid string, servers []connRow) (map[string][]profileJSON, error) {
	ids := make([]string, len(servers))
	out := make(map[string][]profileJSON, len(servers))
	declared := make(map[string][]string, len(servers))
	legacyMethod := make(map[string]string, len(servers))
	for i, c := range servers {
		legacyMethod[c.ServerID] = upstreamauth.MethodToken
		if c.AuthKind == upstreamauth.AuthKindOAuth {
			legacyMethod[c.ServerID] = upstreamauth.MethodOAuth
		}
		ids[i] = c.ServerID
		out[c.ServerID] = []profileJSON{}
		declared[c.ServerID] = c.ConnectionEnv
	}
	rows, err := a.o.Pool.Query(ctx, `SELECT server_id::text, profile,
			CASE WHEN status <> 'active' THEN status
			     WHEN refresh_ciphertext IS NULL AND expires_at < now() THEN 'expired'
			     WHEN refresh_ciphertext IS NULL AND expires_at < now() + interval '7 days' THEN 'expiring'
			     ELSE 'connected' END,
			last4, account_label, last_used_at, created_at, expires_at, env_ciphertext, method, url
		FROM upstream_credentials
		WHERE user_id = $1::uuid AND NOT shared AND server_id = ANY($2::uuid[])
		ORDER BY server_id, profile <> 'default', profile`, uid, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var serverID string
		var p profileJSON
		var envCT []byte
		var connURL *string
		if err := rows.Scan(&serverID, &p.Profile, &p.Status, &p.Last4, &p.AccountLabel, &p.LastUsedAt, &p.ConnectedAt, &p.ExpiresAt, &envCT, &p.Method, &connURL); err != nil {
			return nil, err
		}
		p.URLHost = hostOf(connURL)
		if p.Method == "" {
			p.Method = legacyMethod[serverID]
		}
		if p.EnvSet, err = a.envSet(serverID, uid, p.Profile, envCT, declared[serverID]); err != nil {
			return nil, err
		}
		p.LastUsedAt, p.ExpiresAt = utc(p.LastUsedAt), utc(p.ExpiresAt)
		p.ConnectedAt = p.ConnectedAt.UTC()
		out[serverID] = append(out[serverID], p)
	}
	return out, rows.Err()
}

// hostOf is the host (with port) of a stored URL; nil for none or an unparsable one.
func hostOf(raw *string) *string {
	if raw == nil {
		return nil
	}
	u, err := url.Parse(*raw)
	if err != nil || u.Host == "" {
		return nil
	}
	return &u.Host
}
