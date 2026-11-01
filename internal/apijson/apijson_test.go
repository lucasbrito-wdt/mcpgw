package apijson_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/apijson"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

func decodeBody(body string) error {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst struct {
		Name string `json:"name"`
	}
	return apijson.Decode(r, &dst)
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	if err := decodeBody(`{"name":"a"}`); err != nil {
		t.Fatal(err)
	}
	if err := decodeBody(`{"name":"a","extra":1}`); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := decodeBody(`{"name":"a"} {"name":"b"}`); err == nil {
		t.Fatal("two values accepted")
	}
}

func TestDecodeRejectsLargeBody(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", 1<<20) + `"}`
	err := decodeBody(big)
	if err == nil {
		t.Fatal("body over 1 MB accepted")
	}
	rec := httptest.NewRecorder()
	apijson.FromError(rec, httptest.NewRequest(http.MethodGet, "/", nil), err)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"invalid"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := apijson.EncodeCursor("b@example.com", "0192-id")
	k, id, err := apijson.DecodeCursor(c)
	if err != nil || k != "b@example.com" || id != "0192-id" {
		t.Fatalf("%q %q %v", k, id, err)
	}
	if _, _, err := apijson.DecodeCursor("!!"); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestLimitClamps(t *testing.T) {
	for raw, want := range map[string]int{"": 50, "0": 50, "500": 200, "10": 10} {
		r := httptest.NewRequest(http.MethodGet, "/?limit="+raw, nil)
		if got, err := apijson.Limit(r); err != nil || got != want {
			t.Fatalf("limit %q: %d %v", raw, got, err)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/?limit=x", nil)
	_, err := apijson.Limit(r)
	rec := httptest.NewRecorder()
	apijson.FromError(rec, httptest.NewRequest(http.MethodGet, "/", nil), err)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestFromErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		want   string
	}{
		{admin.NewInvalid("email", i18n.T("invalid email", "e-mail inválido")), 422, `"fields":{"email":"invalid email"}`},
		{&admin.StaleHashError{Slug: "s", Tool: "t", Current: "abc", Given: "def"}, 409, `"current_hash":"abc"`},
		{fmt.Errorf("wrap: %w", admin.ErrNotFound), 404, `"not_found"`},
		{errors.New("secret db detail"), 500, `"Internal error."`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		apijson.FromError(rec, httptest.NewRequest(http.MethodGet, "/", nil), c.err)
		body := rec.Body.String()
		if rec.Code != c.status || !strings.Contains(body, c.want) {
			t.Fatalf("%v: %d %s", c.err, rec.Code, body)
		}
		if strings.Contains(body, "secret db detail") {
			t.Fatal("internal error leaked")
		}
		var env map[string]map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env["error"]["code"] == "" {
			t.Fatalf("envelope: %s", body)
		}
	}
}

func TestFromErrorLocalized(t *testing.T) {
	errs := []error{
		admin.NewInvalid("email", i18n.T("invalid email", "e-mail inválido")),
		&admin.StaleHashError{Slug: "s", Tool: "t", Current: "abc", Given: "def"},
		apijson.ErrBadJSON,
	}
	wantPT := []string{`"e-mail inválido"`, `"a definição de s.t mudou`, `"JSON inválido"`}
	for i, err := range errs {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(i18n.WithLocale(r.Context(), i18n.PTBR))
		rec := httptest.NewRecorder()
		apijson.FromError(rec, r, err)
		if !strings.Contains(rec.Body.String(), wantPT[i]) {
			t.Fatalf("%d: %s", i, rec.Body)
		}
	}
}
