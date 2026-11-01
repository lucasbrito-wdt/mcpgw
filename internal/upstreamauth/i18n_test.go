package upstreamauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

func TestPagesRenderInRequestLocale(t *testing.T) {
	m := &Manager{}
	for _, tc := range []struct {
		acceptLanguage, lang, want string
	}{
		{"", `lang="en"`, "Account connected"},
		{"pt-BR,pt;q=0.9", `lang="pt-BR"`, "Conta conectada"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/connect/success?server=alpha", nil)
		if tc.acceptLanguage != "" {
			req.Header.Set("Accept-Language", tc.acceptLanguage)
		}
		rec := httptest.NewRecorder()
		m.handleSuccess(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, tc.lang) || !strings.Contains(body, tc.want) {
			t.Errorf("Accept-Language %q: want %s and %q in %s", tc.acceptLanguage, tc.lang, tc.want, body)
		}
	}
}

func TestURLErrorLocalizes(t *testing.T) {
	_, err := ValidateConnectionURL("ftp://x")
	if got := i18n.Message(err, i18n.PTBR); !strings.HasPrefix(got, "Use https://") {
		t.Errorf("pt-BR message: %q", got)
	}
	if got := i18n.Message(err, i18n.EN); !strings.Contains(got, "only for localhost") {
		t.Errorf("en message: %q", got)
	}
}
