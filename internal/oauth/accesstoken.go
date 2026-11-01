package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// accessTokenType is the JWT "typ" header for OAuth access tokens (RFC 9068).
const accessTokenType = "at+jwt"

// ErrInvalidToken is wrapped by every ValidateAccessToken error caused by the token itself.
// Any other error is an infrastructure failure (respond 5xx, not 401).
var ErrInvalidToken = auth.ErrInvalidToken

// Claims are the validated claims of a gateway access token.
type Claims struct {
	Issuer   string
	UserID   string
	ClientID string
	Audience string
	Scope    string
	TokenID  string
	// FamilyID identifies the authorization grant; refresh token reuse revokes it.
	FamilyID  string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Scopes splits Scope into its space-separated values.
func (c Claims) Scopes() []string { return strings.Fields(c.Scope) }

type accessTokenClaims struct {
	jwt.RegisteredClaims
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
	FamilyID string `json:"fid"`
}

type grant struct {
	userID   string
	clientID string
	scope    string
	familyID string
}

func (s *Server) issueAccessToken(g grant) (string, time.Time, error) {
	key := s.keys.signer()
	now := s.now()
	exp := now.Add(accessTokenTTL)
	jti, err := randomToken(16)
	if err != nil {
		return "", time.Time{}, err
	}
	claims := accessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   g.userID,
			Audience:  jwt.ClaimStrings{s.resource},
			ExpiresAt: jwt.NewNumericDate(exp),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        jti,
		},
		ClientID: g.clientID,
		Scope:    g.scope,
		FamilyID: g.familyID,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = key.kid
	tok.Header["typ"] = accessTokenType
	signed, err := tok.SignedString(key.priv)
	return signed, exp, err
}

// ValidateAccessToken verifies a bearer token presented to /mcp: ES256 signature by a known
// kid, typ, issuer, audience, exp/nbf/iat with a small leeway, and that the client, the user
// and the grant family are still valid.
func (s *Server) ValidateAccessToken(ctx context.Context, raw string) (Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{signingAlg}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.resource),
		jwt.WithLeeway(validationLeeway),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(s.now),
	)
	var c accessTokenClaims
	tok, err := parser.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return s.keys.lookup(ctx, kid)
	})
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	typ, _ := tok.Header["typ"].(string)
	if !strings.EqualFold(strings.TrimPrefix(typ, "application/"), accessTokenType) {
		return Claims{}, fmt.Errorf("%w: unexpected typ", ErrInvalidToken)
	}
	if c.Subject == "" || c.ClientID == "" || c.FamilyID == "" || c.ID == "" || len(c.Audience) != 1 {
		return Claims{}, fmt.Errorf("%w: missing claims", ErrInvalidToken)
	}
	if err := s.checkGrantActive(ctx, c); err != nil {
		return Claims{}, err
	}
	return Claims{
		Issuer: c.Issuer, UserID: c.Subject, ClientID: c.ClientID, Audience: c.Audience[0],
		Scope: c.Scope, TokenID: c.ID, FamilyID: c.FamilyID,
		IssuedAt: c.IssuedAt.Time, ExpiresAt: c.ExpiresAt.Time,
	}, nil
}

func (s *Server) checkGrantActive(ctx context.Context, c accessTokenClaims) error {
	key := grantKey{clientID: c.ClientID, userID: c.Subject, familyID: c.FamilyID}
	if s.grants.ok(key, s.now()) {
		return nil
	}
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM oauth_clients WHERE client_id = $1)
		AND EXISTS (SELECT 1 FROM users WHERE id = $2::uuid)
		AND NOT EXISTS (SELECT 1 FROM oauth_revoked_families WHERE family_id = $3::uuid)
		AND NOT EXISTS (SELECT 1 FROM oauth_refresh_tokens
			WHERE family_id = $3::uuid AND revoked_at IS NOT NULL)`,
		c.ClientID, c.Subject, c.FamilyID).Scan(&active)
	if err != nil {
		return fmt.Errorf("oauth: check grant: %w", err)
	}
	if !active {
		return fmt.Errorf("%w: client, user or grant no longer valid", ErrInvalidToken)
	}
	s.grants.put(key, s.now())
	return nil
}

// TokenVerifier adapts ValidateAccessToken to the SDK's auth.RequireBearerToken middleware.
// TokenInfo.Extra carries "client_id" and "claims" (a Claims value).
func (s *Server) TokenVerifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		c, err := s.ValidateAccessToken(ctx, token)
		if err != nil {
			return nil, err
		}
		return &auth.TokenInfo{
			Scopes:     c.Scopes(),
			Expiration: c.ExpiresAt,
			UserID:     c.UserID,
			Extra:      map[string]any{"client_id": c.ClientID, "claims": c},
		}, nil
	}
}
