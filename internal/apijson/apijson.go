// Package apijson holds the JSON plumbing shared by /admin/api and /portal/api: the error
// envelope, body decoding, list limits and keyset cursors.
package apijson

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

const (
	maxBodyBytes = 1 << 20
	defaultLimit = 50
	maxLimit     = 200
)

// ErrBadJSON is returned by Decode for any malformed, oversized or unexpected body (422 invalid).
var ErrBadJSON error = i18n.T("invalid JSON", "JSON inválido")

type errorBody struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	Fields      map[string]string `json:"fields,omitempty"`
	CurrentHash string            `json:"current_hash,omitempty"`
}

// WriteJSON writes v as the JSON response. Every API response is per user: never cached.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteErrorFor writes {"error": {"code", "message", "fields"?}} in the locale of r. err is rendered
// with i18n.Message, so an i18n.Text (which is an error) works as a literal message.
func WriteErrorFor(w http.ResponseWriter, r *http.Request, status int, code string, err error, fields map[string]i18n.Text) {
	l := i18n.FromContext(r.Context())
	body := errorBody{Code: code, Message: i18n.Message(err, l)}
	if len(fields) > 0 {
		body.Fields = make(map[string]string, len(fields))
		for k, v := range fields {
			body.Fields[k] = v.In(l)
		}
	}
	writeErrorBody(w, status, body)
}

func writeErrorBody(w http.ResponseWriter, status int, body errorBody) {
	WriteJSON(w, status, map[string]errorBody{"error": body})
}

// Decode reads exactly one JSON value of at most 1 MB into dst, rejecting unknown fields.
func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrBadJSON
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return ErrBadJSON
	}
	return nil
}

// Limit parses ?limit: default 50, values above 200 are clamped, non-numbers are invalid.
func Limit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	switch {
	case err != nil || n < 0:
		return 0, admin.NewInvalid("limit", i18n.T("invalid limit", "limite inválido"))
	case n == 0:
		return defaultLimit, nil
	case n > maxLimit:
		return maxLimit, nil
	}
	return n, nil
}

type cursor struct {
	K  string `json:"k"`
	ID string `json:"id"`
}

// EncodeCursor returns the opaque keyset cursor for (sort key, id) (plan gap G14).
func EncodeCursor(k, id string) string {
	b, _ := json.Marshal(cursor{K: k, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reverses EncodeCursor; a malformed cursor is an *admin.InvalidError on "cursor".
func DecodeCursor(s string) (k, id string, err error) {
	var c cursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err != nil || c.ID == "" {
		return "", "", admin.NewInvalid("cursor", i18n.T("invalid cursor", "cursor inválido"))
	}
	return c.K, c.ID, nil
}

// FromError maps service errors to the envelope, localized for r. Unknown errors are logged (never
// the request body) and answered with a generic 500 so internals do not leak.
func FromError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *admin.InvalidError
	var stale *admin.StaleHashError
	l := i18n.FromContext(r.Context())
	switch {
	case errors.Is(err, ErrBadJSON):
		WriteErrorFor(w, r, http.StatusUnprocessableEntity, "invalid", ErrBadJSON, nil)
	case errors.As(err, &invalid):
		WriteErrorFor(w, r, http.StatusUnprocessableEntity, "invalid", invalid, map[string]i18n.Text{invalid.Field: i18n.Raw(invalid.Localize(l))})
	case errors.As(err, &stale):
		writeErrorBody(w, http.StatusConflict, errorBody{Code: "conflict", Message: stale.Localize(l), CurrentHash: stale.Current})
	case errors.Is(err, admin.ErrConflict):
		WriteErrorFor(w, r, http.StatusConflict, "conflict", err, nil)
	case errors.Is(err, admin.ErrNotFound):
		WriteErrorFor(w, r, http.StatusNotFound, "not_found", err, nil)
	case errors.Is(err, admin.ErrInvalid):
		WriteErrorFor(w, r, http.StatusUnprocessableEntity, "invalid", err, nil)
	default:
		slog.Error("apijson: internal error", "err", err)
		WriteErrorFor(w, r, http.StatusInternalServerError, "internal", i18n.T("Internal error.", "Erro interno."), nil)
	}
}
