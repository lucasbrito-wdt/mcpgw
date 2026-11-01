package upstreamauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// oauthClient is the client identity the gateway uses against one authorization server.
// An empty Secret means a public client (PKCE only).
type oauthClient struct {
	ID     string
	Secret string
}

// resolveClient picks how the gateway identifies itself to the authorization server, in order:
//  1. admin-provided client (upstream_servers.oauth_client_id): explicit configuration wins;
//  2. CIMD, when the AS advertises support and the gateway public URL is https (the AS must be
//     able to fetch the document, so a local http origin falls through);
//  3. a cached or fresh Dynamic Client Registration;
//  4. otherwise ErrNoClientRegistration.
func (m *Manager) resolveClient(ctx context.Context, srv *serverRow, d *Discovery) (oauthClient, error) {
	if srv.ClientID != nil && *srv.ClientID != "" {
		return m.staticClient(srv)
	}
	if d.Meta.ClientIDMetadataDocumentSupported && strings.HasPrefix(m.publicURL, "https://") {
		return oauthClient{ID: m.ClientMetadataURL()}, nil
	}
	if d.Meta.RegistrationEndpoint != "" {
		return m.dcrClient(ctx, srv, d)
	}
	return oauthClient{}, ErrNoClientRegistration
}

func (m *Manager) staticClient(srv *serverRow) (oauthClient, error) {
	c := oauthClient{ID: *srv.ClientID}
	if len(srv.ClientSecretCT) == 0 {
		return c, nil
	}
	secret, err := m.vault.Open(srv.ClientSecretCT, serverFieldAAD(srv.ID, "oauth_client_secret"))
	if err != nil {
		return oauthClient{}, fmt.Errorf("open client secret of server %q: %w", srv.Slug, err)
	}
	c.Secret = string(secret)
	return c, nil
}

// clientForGrant returns the client a stored grant was issued to, so a refresh authenticates
// exactly as the original authorization did even if the registration strategy changed since.
func (m *Manager) clientForGrant(ctx context.Context, srv *serverRow, issuer, clientID string) (oauthClient, error) {
	if srv.ClientID != nil && *srv.ClientID == clientID {
		return m.staticClient(srv)
	}
	c, found, err := m.loadDCR(ctx, srv.ID, issuer)
	if err != nil {
		return oauthClient{}, err
	}
	if found && c.ID == clientID {
		return c, nil
	}
	return oauthClient{ID: clientID}, nil // CIMD (or an orphaned registration): public client
}

func (m *Manager) loadDCR(ctx context.Context, serverID, issuer string) (c oauthClient, found bool, err error) {
	var ct []byte
	var redirectURI string
	err = m.pool.QueryRow(ctx, `SELECT client_id, client_secret_ciphertext, redirect_uri
		FROM upstream_oauth_clients WHERE server_id = $1::uuid AND issuer = $2`, serverID, issuer).
		Scan(&c.ID, &ct, &redirectURI)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthClient{}, false, nil
	}
	if err != nil {
		return oauthClient{}, false, fmt.Errorf("load registered client: %w", err)
	}
	if len(ct) > 0 {
		secret, err := m.vault.Open(ct, dcrAAD(serverID, issuer))
		if err != nil {
			return oauthClient{}, false, fmt.Errorf("open registered client secret: %w", err)
		}
		c.Secret = string(secret)
	}
	// A registration made for another redirect URI (public URL changed) is stale.
	return c, redirectURI == m.redirectURI(), nil
}

func (m *Manager) dcrClient(ctx context.Context, srv *serverRow, d *Discovery) (oauthClient, error) {
	issuer := d.Meta.Issuer
	if c, found, err := m.loadDCR(ctx, srv.ID, issuer); err != nil || found {
		return c, err
	}
	registered, err := m.registerClient(ctx, d.Meta.RegistrationEndpoint)
	if err != nil {
		return oauthClient{}, err
	}
	var secretCT []byte
	if registered.Secret != "" {
		if secretCT, err = m.vault.Seal([]byte(registered.Secret), dcrAAD(srv.ID, issuer)); err != nil {
			return oauthClient{}, err
		}
	}
	// The database picks the winner of concurrent registrations (and replaces a stale one).
	_, err = m.pool.Exec(ctx, `INSERT INTO upstream_oauth_clients (server_id, issuer, client_id, client_secret_ciphertext, redirect_uri)
		VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (server_id, issuer) DO UPDATE
		SET client_id = EXCLUDED.client_id, client_secret_ciphertext = EXCLUDED.client_secret_ciphertext,
		    redirect_uri = EXCLUDED.redirect_uri, created_at = now()
		WHERE upstream_oauth_clients.redirect_uri <> EXCLUDED.redirect_uri`,
		srv.ID, issuer, registered.ID, secretCT, m.redirectURI())
	if err != nil {
		return oauthClient{}, fmt.Errorf("store registered client: %w", err)
	}
	c, found, err := m.loadDCR(ctx, srv.ID, issuer)
	if err != nil || !found {
		return oauthClient{}, errors.Join(err, errors.New("registered client not stored"))
	}
	return c, nil
}

// registerClient performs RFC 7591 Dynamic Client Registration as a public (PKCE) web client.
func (m *Manager) registerClient(ctx context.Context, endpoint string) (oauthClient, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                gatewayClientName,
		"client_uri":                 m.publicURL,
		"redirect_uris":              []string{m.redirectURI()},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"application_type":           "web",
	})
	resp, err := m.http.post(ctx, endpoint, "application/json", bytes.NewReader(body), nil)
	if err != nil {
		return oauthClient{}, fmt.Errorf("client registration: %w", err)
	}
	if resp.status != http.StatusCreated && resp.status != http.StatusOK {
		return oauthClient{}, fmt.Errorf("%w (HTTP %d)", ErrRegistrationRejected, resp.status)
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(resp.body, &out); err != nil || out.ClientID == "" {
		return oauthClient{}, errors.New("client registration: malformed response")
	}
	return oauthClient{ID: out.ClientID, Secret: out.ClientSecret}, nil
}

// handleClientMetadata serves the gateway's Client ID Metadata Document (CIMD). Its client_id
// is its own URL, as the draft requires.
func (m *Manager) handleClientMetadata(w http.ResponseWriter, _ *http.Request) {
	doc, _ := json.Marshal(map[string]any{
		"client_id":                  m.ClientMetadataURL(),
		"client_name":                gatewayClientName,
		"client_uri":                 m.publicURL,
		"redirect_uris":              []string{m.redirectURI()},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"application_type":           "web",
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(doc)
}
