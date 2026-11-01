package upstreamauth

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
)

// maxConnectionURLLen matches the schema bound on upstream_credentials.url.
const maxConnectionURLLen = 2048

// URLError rejects a per-connection upstream URL. Message is user-facing.
type URLError struct{ Message i18n.Text }

func (e *URLError) Error() string { return "connection url: " + e.Message.EN }

// Localize implements i18n.Localizer.
func (e *URLError) Localize(l i18n.Locale) string { return e.Message.In(l) }

var (
	errURLRequired    = &URLError{Message: i18n.T("Provide the MCP address of this connection.", "Informe o endereço do MCP desta conexão.")}
	errURLNotAccepted = &URLError{Message: i18n.T("This server does not accept a per-connection address.", "Este servidor não aceita endereço por conexão.")}
)

// ValidateConnectionURL checks a connection's upstream address and returns it normalized: absolute
// https (http only for loopback), no userinfo, at most 2048 characters, and never an IP literal
// in a private, link-local (cloud metadata) or unspecified range. Host names are not resolved.
func ValidateConnectionURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxConnectionURLLen || strings.ContainsAny(raw, "\r\n\x00") {
		return "", &URLError{Message: i18n.T("Invalid address: use a URL of up to 2048 characters.", "Endereço inválido: use uma URL de até 2048 caracteres.")}
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "" {
		return "", &URLError{Message: i18n.T("Invalid address: use a full URL, e.g. https://server/mcp.", "Endereço inválido: use uma URL completa, ex.: https://servidor/mcp.")}
	}
	if u.User != nil {
		return "", &URLError{Message: i18n.T("The address cannot contain a username or password.", "O endereço não pode conter usuário ou senha.")}
	}
	host := strings.ToLower(u.Hostname())
	loopback := isLoopbackHost(host)
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", &URLError{Message: i18n.T("Use https:// (http:// only for localhost).", "Use https:// (http:// só para localhost).")}
	}
	if !loopback && blockedHost(host) {
		return "", &URLError{Message: i18n.T("Address not allowed: private or internal network IP.", "Endereço não permitido: IP de rede privada ou interna.")}
	}
	return u.String(), nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

// blockedHost refuses private, link-local, unspecified and multicast IP literals, and numeric
// host forms (2130706433, 0x7f.1) a system resolver may still read as an IP.
func blockedHost(host string) bool {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		last := host[strings.LastIndexByte(host, '.')+1:]
		return last == "" || last[0] >= '0' && last[0] <= '9' || strings.HasPrefix(last, "0x")
	}
	addr = addr.Unmap()
	return addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified() || addr.IsMulticast()
}

// urlWrite is the url an upsert stores; keep leaves the stored value untouched.
type urlWrite struct {
	url  *string
	keep bool
}

// prepareURL validates in.URL against the server's connection_url switch. With the switch on, an
// empty URL keeps the one an existing connection already has, and is required otherwise. A server
// without the switch refuses a URL.
func (m *Manager) prepareURL(ctx context.Context, db store.DB, in ConnectionInput) (urlWrite, error) {
	enabled, err := connectionURLEnabled(ctx, db, in.ServerID)
	if err != nil {
		return urlWrite{}, err
	}
	if !enabled {
		if in.URL != "" {
			return urlWrite{}, errURLNotAccepted
		}
		return urlWrite{keep: true}, nil
	}
	if in.URL == "" {
		saved, err := savedConnectionURL(ctx, db, in)
		if err != nil {
			return urlWrite{}, err
		}
		if saved == "" {
			return urlWrite{}, errURLRequired
		}
		return urlWrite{keep: true}, nil
	}
	normalized, err := ValidateConnectionURL(in.URL)
	if err != nil {
		return urlWrite{}, err
	}
	return urlWrite{url: &normalized}, nil
}

func connectionURLEnabled(ctx context.Context, db store.DB, serverID string) (bool, error) {
	var enabled bool
	err := db.QueryRow(ctx, `SELECT connection_url FROM upstream_servers WHERE id = $1::uuid`, serverID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrServerNotFound
	}
	return enabled, err
}

// savedConnectionURL is the url of the active connection in targets; "" when it has none.
func savedConnectionURL(ctx context.Context, db store.DB, in ConnectionInput) (string, error) {
	var saved string
	err := db.QueryRow(ctx, `SELECT COALESCE(url, '') FROM upstream_credentials
		WHERE server_id = $1::uuid AND status = 'active'
		    AND CASE WHEN $4 THEN shared ELSE user_id = NULLIF($2, '')::uuid AND profile = $3 END`,
		in.ServerID, in.ActorID, in.Profile, in.Shared).Scan(&saved)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return saved, err
}
