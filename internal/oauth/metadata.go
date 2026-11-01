package oauth

import (
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const metadataCacheControl = "public, max-age=300"

// AuthServerMetadata returns the RFC 8414 document.
func (s *Server) AuthServerMetadata() *oauthex.AuthServerMeta {
	return &oauthex.AuthServerMeta{
		Issuer:                                     s.issuer,
		AuthorizationEndpoint:                      s.issuer + "/authorize",
		TokenEndpoint:                              s.issuer + "/token",
		JWKSURI:                                    s.issuer + "/jwks.json",
		RegistrationEndpoint:                       s.issuer + "/register",
		ScopesSupported:                            []string{ScopeMCP},
		ResponseTypesSupported:                     []string{"code"},
		ResponseModesSupported:                     []string{"query"},
		GrantTypesSupported:                        []string{grantAuthorizationCode, grantRefreshToken},
		TokenEndpointAuthMethodsSupported:          []string{authMethodNone},
		CodeChallengeMethodsSupported:              []string{"S256"},
		ClientIDMetadataDocumentSupported:          true,
		AuthorizationResponseIssParameterSupported: true,
	}
}

// ProtectedResourceMetadata returns the RFC 9728 document for the /mcp resource.
func (s *Server) ProtectedResourceMetadata() *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               s.resource,
		AuthorizationServers:   []string{s.issuer},
		ScopesSupported:        []string{ScopeMCP},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "MCP Gateway",
	}
}

func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", metadataCacheControl)
	writeJSON(w, http.StatusOK, s.AuthServerMetadata())
}

func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", metadataCacheControl)
	writeJSON(w, http.StatusOK, s.ProtectedResourceMetadata())
}

func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	set, err := s.keys.publicJWKS(r.Context())
	if err != nil {
		s.log.Error("oauth: load jwks", "err", err)
		writeJSON(w, http.StatusInternalServerError, tokenError{Error: "server_error"})
		return
	}
	w.Header().Set("Cache-Control", metadataCacheControl)
	writeJSON(w, http.StatusOK, set)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
