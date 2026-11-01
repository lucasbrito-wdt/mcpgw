package oauth

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestClientIPKeys(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7:4000":                   "203.0.113.7",
		"[::ffff:203.0.113.7]:4000":          "203.0.113.7",
		"[2001:db8:1:2:aaaa:bbbb:cccc:1]:80": "2001:db8:1:2::/64",
		"[2001:db8:1:2:1111:2222:3333:4]:81": "2001:db8:1:2::/64",
		"[2001:db8:1:3::1]:80":               "2001:db8:1:3::/64",
		"[fe80::1%eth0]:80":                  "fe80::/64",
		"not-an-addr":                        "not-an-addr",
	}
	for remote, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		if got := clientIP(r); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestKeyedLimiterEvictsInsteadOfDenyingNewKeys(t *testing.T) {
	l := newKeyedLimiter(rate.Every(time.Minute), 1)
	now := time.Now()
	for i := range limiterMaxKeys {
		if !l.allow("k"+strconv.Itoa(i), now.Add(time.Duration(i)*time.Millisecond)) {
			t.Fatalf("key %d denied while filling", i)
		}
	}
	late := now.Add(time.Second)
	if !l.allow("newcomer", late) {
		t.Fatal("a new key must be admitted when the table is full")
	}
	if len(l.entries) != limiterMaxKeys {
		t.Fatalf("table size = %d, want %d", len(l.entries), limiterMaxKeys)
	}
	if _, ok := l.entries["k0"]; ok {
		t.Fatal("the least recently seen key should have been evicted")
	}
	if l.allow("newcomer", late) {
		t.Fatal("the admitted key must still be rate limited")
	}
}

func TestEmailLockoutIndependentOfIP(t *testing.T) {
	e := newTestEnv(t)
	w := e.getAuthorize(e.authorizeParams(newPKCE()))
	form, csrf := formState(t, w), cookieNamed(t, w, csrfCookie)
	form.Set("action", actionLogin)
	form.Set("email", testEmail)

	attempt := func(i int) int {
		r := newFormRequest("/authorize", form, csrf)
		r.RemoteAddr = "198.51.100." + strconv.Itoa(i+1) + ":5000"
		return e.do(r).Code
	}
	form.Set("password", "wrong password here")
	for i := range emailFailureBurst {
		if code := attempt(i); code != http.StatusUnauthorized {
			t.Fatalf("failure %d: status %d", i, code)
		}
	}
	// A fresh IP with the right password is still refused: the account is soft-locked.
	form.Set("password", testPassword)
	if code := attempt(50); code != http.StatusTooManyRequests {
		t.Fatalf("locked account: status %d, want 429", code)
	}
	e.clock.Advance(emailFailureWindow/emailFailureBurst + time.Second)
	if code := attempt(51); code != http.StatusOK {
		t.Fatalf("after refill: status %d, want 200", code)
	}
}
