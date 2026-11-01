package oauth

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
)

const maxRegistrationBody = 16 << 10

type registrationResponse struct {
	ClientMetadata
	ClientIDIssuedAt int64 `json:"client_id_issued_at"`
}

// handleRegister is the deprecated RFC 7591 fallback for clients without CIMD support.
// Only public clients (token_endpoint_auth_method "none") are registered.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.registerLimiter.allow(clientIP(r), s.now()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, tokenError{Error: "slow_down", Description: "too many registrations"})
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_client_metadata", Description: "body must be application/json"})
		return
	}
	var meta ClientMetadata
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegistrationBody)).Decode(&meta); err != nil {
		writeJSON(w, http.StatusBadRequest, tokenError{Error: "invalid_client_metadata", Description: "malformed JSON body"})
		return
	}
	if err := meta.normalize(); err != nil {
		code := "invalid_client_metadata"
		if errors.Is(err, errInvalidRedirect) {
			code = "invalid_redirect_uri"
		}
		writeJSON(w, http.StatusBadRequest, tokenError{Error: code, Description: err.Error()})
		return
	}
	resp, err := s.registerClient(r, meta)
	if err != nil {
		s.log.Error("oauth: register client", "err", err)
		writeJSON(w, http.StatusInternalServerError, tokenError{Error: "server_error"})
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) registerClient(r *http.Request, meta ClientMetadata) (registrationResponse, error) {
	id, err := randomToken(18)
	if err != nil {
		return registrationResponse{}, err
	}
	meta.ClientID = "mcpgw-" + id
	raw, err := json.Marshal(meta)
	if err != nil {
		return registrationResponse{}, err
	}
	now := s.now()
	_, err = s.pool.Exec(r.Context(), `INSERT INTO oauth_clients
		(client_id, registration_kind, metadata, redirect_uris, created_at)
		VALUES ($1, 'dcr', $2, $3, $4)`, meta.ClientID, raw, meta.RedirectURIs, now)
	return registrationResponse{ClientMetadata: meta, ClientIDIssuedAt: now.Unix()}, err
}
