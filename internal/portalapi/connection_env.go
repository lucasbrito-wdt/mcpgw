package portalapi

import (
	"context"
	"errors"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"net/http"
	"slices"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

// connectionVarJSON is a per-connection variable a server declares. Its value (and the server's
// fixed default) is never returned: has_default only says a default exists.
type connectionVarJSON struct {
	Name       string `json:"name"`
	HasDefault bool   `json:"has_default"`
}

// connectionVarsFor maps each server id to the variables it declares (an empty, non-nil slice
// for most servers; only servers with any are looked up).
func (a *API) connectionVarsFor(ctx context.Context, rows []connRow) (map[string][]connectionVarJSON, error) {
	out := make(map[string][]connectionVarJSON, len(rows))
	for _, c := range rows {
		out[c.ServerID] = []connectionVarJSON{}
		if len(c.ConnectionEnv) == 0 {
			continue
		}
		vars, err := a.o.Credentials.ConnectionVars(ctx, a.o.Pool, c.ServerID)
		if err != nil {
			return nil, err
		}
		for _, v := range vars {
			out[c.ServerID] = append(out[c.ServerID], connectionVarJSON{Name: v.Name, HasDefault: v.HasDefault})
		}
	}
	return out, nil
}

// envSet lists the declared variables a personal connection has a value for; names only.
func (a *API) envSet(serverID, uid, profile string, ct []byte, declared []string) ([]string, error) {
	names, err := a.o.Credentials.ConnectionEnvSet(serverID, uid, profile, ct)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(names, func(n string) bool { return !slices.Contains(declared, n) }), nil
}

// writeConnectionEnvError writes the response of a rejected per-connection environment (400 with
// the field env.<NAME>) or of an environment-only write without a connection (422 on token).
// False when err is neither. Messages never contain a value.
func writeConnectionEnvError(w http.ResponseWriter, r *http.Request, err error) bool {
	var envErr *upstreamauth.EnvError
	switch {
	case errors.As(err, &envErr):
		apijson.WriteErrorFor(w, r, http.StatusBadRequest, "invalid_env", envErr.Message,
			map[string]i18n.Text{"env." + envErr.Name: envErr.Message})
	case errors.Is(err, upstreamauth.ErrTokenRequired):
		apijson.FromError(w, r, admin.NewInvalid("token", i18n.T("Provide the token: this connection does not exist yet.", "Informe o token: esta conexão ainda não existe.")))
	default:
		return false
	}
	return true
}
