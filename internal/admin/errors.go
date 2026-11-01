package admin

import (
	"errors"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

var (
	ErrNotFound = errors.New("not found") // HTTP 404
	ErrConflict = errors.New("conflict")  // HTTP 409
	ErrInvalid  = errors.New("invalid")   // HTTP 422
)

// kindError carries a bilingual message and matches one of the sentinels with errors.Is.
// Error() is the English text (logs); Localize renders the user's language.
type kindError struct {
	kind error
	text i18n.Text
}

func (e *kindError) Error() string                 { return e.text.EN }
func (e *kindError) Is(target error) bool          { return target == e.kind }
func (e *kindError) Localize(l i18n.Locale) string { return e.text.In(l) }

func notFound(en, pt string, a ...any) error {
	return &kindError{ErrNotFound, i18n.T(en, pt, a...)}
}

func conflict(en, pt string, a ...any) error {
	return &kindError{ErrConflict, i18n.T(en, pt, a...)}
}

// InvalidError is a validation failure on one input field; errors.Is(err, ErrInvalid) holds.
// Message is the English text; Text carries both languages (build it with NewInvalid).
type InvalidError struct {
	Field, Message string
	Text           i18n.Text
}

// NewInvalid builds an InvalidError on field with a bilingual message.
func NewInvalid(field string, text i18n.Text) *InvalidError {
	return &InvalidError{Field: field, Message: text.EN, Text: text}
}

func (e *InvalidError) Error() string        { return e.Message }
func (e *InvalidError) Is(target error) bool { return target == ErrInvalid }

// Localize falls back to Message when the error was built without Text.
func (e *InvalidError) Localize(l i18n.Locale) string {
	if e.Text.EN == "" && e.Text.PT == "" {
		return e.Message
	}
	return e.Text.In(l)
}

func invalid(field, en, pt string, a ...any) error {
	return NewInvalid(field, i18n.T(en, pt, a...))
}

// StaleHashError: the tool definition changed since review; errors.Is(err, ErrConflict) holds.
type StaleHashError struct{ Slug, Tool, Current, Given string }

func (e *StaleHashError) text() i18n.Text {
	return i18n.T(
		"the definition of %s.%s changed: current hash %s, given %s (review with tool show)",
		"a definição de %s.%s mudou: hash atual %s, informado %s (revise com tool show)",
		e.Slug, e.Tool, e.Current, e.Given)
}

func (e *StaleHashError) Error() string                 { return e.text().EN }
func (e *StaleHashError) Is(target error) bool          { return target == ErrConflict }
func (e *StaleHashError) Localize(l i18n.Locale) string { return e.text().In(l) }
