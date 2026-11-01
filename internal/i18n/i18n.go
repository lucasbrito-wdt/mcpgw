// Package i18n is the minimal en / pt-BR layer of the Go side: locale negotiation, a request-scoped
// locale, and Text, a message carrying both languages. Code, logs and error strings stay English;
// Text.In / Message pick the language only at the user-facing edge.
package i18n

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Locale is a supported UI language.
type Locale string

const (
	EN   Locale = "en"
	PTBR Locale = "pt-BR"

	Default = EN
)

type ctxKey struct{}

// WithLocale returns ctx carrying l.
func WithLocale(ctx context.Context, l Locale) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the locale stored by WithLocale, or EN.
func FromContext(ctx context.Context) Locale {
	if l, ok := ctx.Value(ctxKey{}).(Locale); ok {
		return l
	}
	return Default
}

// Middleware stores the locale negotiated from Accept-Language in the request context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := Negotiate(r.Header.Get("Accept-Language"))
		next.ServeHTTP(w, r.WithContext(WithLocale(r.Context(), l)))
	})
}

// fromTag maps one language tag ("pt", "pt-BR", "pt_BR.UTF-8", "en-US") to a supported locale.
func fromTag(tag string) (Locale, bool) {
	tag = strings.ToLower(tag)
	if i := strings.IndexAny(tag, "-_.@"); i >= 0 {
		tag = tag[:i]
	}
	switch tag {
	case "pt":
		return PTBR, true
	case "en":
		return EN, true
	}
	return "", false
}

// Negotiate picks a locale from an Accept-Language header: highest q first (ties keep header
// order), the first supported tag wins, any pt* is PTBR, anything else is EN.
func Negotiate(acceptLanguage string) Locale {
	type cand struct {
		tag string
		q   float64
	}
	var cands []cand
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		if q > 0 {
			cands = append(cands, cand{strings.TrimSpace(tag), q})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].q > cands[j].q })
	for _, c := range cands {
		if l, ok := fromTag(c.tag); ok {
			return l
		}
	}
	return Default
}

// FromEnv reads the CLI locale from LC_ALL, LC_MESSAGES, then LANG (first non-empty wins).
func FromEnv() Locale {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			l, _ := fromTag(v)
			if l == "" {
				return Default
			}
			return l
		}
	}
	return Default
}

// Text is one message in both languages.
type Text struct{ EN, PT string }

// T builds a Text. With args, both languages are formatted with fmt.Sprintf (same verbs, same order).
func T(en, pt string, args ...any) Text {
	if len(args) > 0 {
		return Text{fmt.Sprintf(en, args...), fmt.Sprintf(pt, args...)}
	}
	return Text{en, pt}
}

// Raw wraps a string that has no translation (for example text from a package not yet migrated).
func Raw(s string) Text { return Text{s, s} }

// In returns the text for l; anything but PTBR is English.
func (t Text) In(l Locale) string {
	if l == PTBR {
		return t.PT
	}
	return t.EN
}

// String returns the English text (logs, debugging).
func (t Text) String() string { return t.EN }

// Localize implements Localizer.
func (t Text) Localize(l Locale) string { return t.In(l) }

// Error makes a Text usable as an error value; it reports the English text.
func (t Text) Error() string { return t.EN }

// Localizer is implemented by errors that can render themselves in a locale.
type Localizer interface{ Localize(Locale) string }

// Message renders err in l: the first Localizer in the chain, else err.Error().
func Message(err error, l Locale) string {
	var lz Localizer
	if errors.As(err, &lz) {
		return lz.Localize(l)
	}
	return err.Error()
}
