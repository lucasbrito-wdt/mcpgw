package i18n_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

func TestNegotiate(t *testing.T) {
	cases := map[string]i18n.Locale{
		"":                      i18n.EN,
		"pt-BR":                 i18n.PTBR,
		"pt":                    i18n.PTBR,
		"pt-PT,en;q=0.5":        i18n.PTBR,
		"en-US,pt-BR;q=0.9":     i18n.EN,
		"pt-BR;q=0.5,en;q=0.9":  i18n.EN,
		"fr,pt;q=0.4":           i18n.PTBR,
		"fr,de":                 i18n.EN,
		"*":                     i18n.EN,
		"pt;q=0,en":             i18n.EN,
		"en;q=0.1, pt-BR;q=0.8": i18n.PTBR,
		"ptx":                   i18n.EN,
		"pt-BR;q=bad":           i18n.PTBR,
		" PT-br , en ; q=0.2 ":  i18n.PTBR,
		"en;q=0.8,pt;q=0.8":     i18n.EN,
	}
	for in, want := range cases {
		if got := i18n.Negotiate(in); got != want {
			t.Errorf("Negotiate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name                string
		all, messages, lang string
		want                i18n.Locale
	}{
		{"none", "", "", "", i18n.EN},
		{"lang pt", "", "", "pt_BR.UTF-8", i18n.PTBR},
		{"lang en", "", "", "en_US.UTF-8", i18n.EN},
		{"messages beats lang", "", "pt_BR", "en_US", i18n.PTBR},
		{"all beats all", "en_US", "pt_BR", "pt_BR", i18n.EN},
		{"C locale", "C", "", "pt_BR", i18n.EN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("LC_ALL", c.all)
			t.Setenv("LC_MESSAGES", c.messages)
			t.Setenv("LANG", c.lang)
			if got := i18n.FromEnv(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestText(t *testing.T) {
	plain := i18n.T("not found", "não encontrado")
	if plain.In(i18n.EN) != "not found" || plain.In(i18n.PTBR) != "não encontrado" || plain.String() != "not found" {
		t.Fatalf("%+v", plain)
	}
	f := i18n.T("server %s: %d%%", "servidor %s: %d%%", "a", 5)
	if f.EN != "server a: 5%" || f.PT != "servidor a: 5%" {
		t.Fatalf("%+v", f)
	}
	if i18n.T("x", "y").In("fr") != "x" {
		t.Fatal("unknown locale must fall back to EN")
	}
	if got := i18n.Raw("same").In(i18n.PTBR); got != "same" {
		t.Fatal(got)
	}
}

func TestMessage(t *testing.T) {
	txt := i18n.T("gone", "sumiu")
	cases := []struct {
		name string
		err  error
		l    i18n.Locale
		want string
	}{
		{"plain", errors.New("boom"), i18n.PTBR, "boom"},
		{"localizer en", txt, i18n.EN, "gone"},
		{"localizer pt", txt, i18n.PTBR, "sumiu"},
		{"wrapped", fmt.Errorf("ctx: %w", txt), i18n.PTBR, "sumiu"},
	}
	for _, c := range cases {
		if got := i18n.Message(c.err, c.l); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMiddlewareAndContext(t *testing.T) {
	if i18n.FromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context()) != i18n.EN {
		t.Fatal("default must be EN")
	}
	var got i18n.Locale
	h := i18n.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = i18n.FromContext(r.Context()) }))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "pt-BR,en;q=0.5")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != i18n.PTBR {
		t.Fatalf("got %q", got)
	}
}
