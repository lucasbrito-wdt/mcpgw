package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	mintCallback = "http://127.0.0.1:53682/callback"
	mintVerifier = "loadgen-verifier-0123456789-0123456789-0123456789"
	// The OAuth endpoints rate-limit per IP (logins: burst 20, then one per 6 s; registrations:
	// burst 5, then one per 6 min), so one client is registered for all users and a 429 is retried.
	mintRetryWait = 6 * time.Second
	mintRetries   = 5
)

var (
	hiddenField = regexp.MustCompile(`name="(csrf_token|request)" value="([^"]*)"`)
	levelField  = regexp.MustCompile(`name="(level_[^"]+)"`)
)

// minter is a dev-only helper: for users that already exist (mcpgw admin user create) it runs the
// public OAuth flow (dynamic client registration, login, consent, code exchange) and returns one
// access token per user. It needs a gateway whose public URL matches -url.
type minter struct {
	emailFmt string
	password string
	level    string
}

func (m *minter) tokens(mcpURL string, users int) ([]string, error) {
	u, err := url.Parse(mcpURL)
	if err != nil {
		return nil, err
	}
	origin := u.Scheme + "://" + u.Host
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	regBody := `{"client_name":"loadgen","redirect_uris":["http://127.0.0.1/callback"],"token_endpoint_auth_method":"none"}`
	if err := doJSON(client, http.MethodPost, origin+"/register", regBody, &reg); err != nil || reg.ClientID == "" {
		return nil, fmt.Errorf("register: %v", err)
	}
	var out []string
	for i := 1; i <= users; i++ {
		token, err := m.mintWithRetry(client, origin, mcpURL, reg.ClientID, fmt.Sprintf(m.emailFmt, i))
		if err != nil {
			return nil, fmt.Errorf("user %d: %w", i, err)
		}
		out = append(out, token)
	}
	return out, nil
}

func (m *minter) mintWithRetry(c *http.Client, origin, resource, clientID, email string) (token string, err error) {
	for attempt := 0; attempt < mintRetries; attempt++ {
		if token, err = m.mintOne(c, origin, resource, clientID, email); err == nil || !strings.Contains(err.Error(), "status 429") {
			return token, err
		}
		time.Sleep(mintRetryWait)
	}
	return "", err
}

func (m *minter) mintOne(c *http.Client, origin, resource, clientID, email string) (string, error) {
	sum := sha256.Sum256([]byte(mintVerifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {mintCallback},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"s"}, "resource": {resource}, "scope": {"mcp"},
	}
	page, cookies, _, err := doForm(c, http.MethodGet, origin+"/authorize?"+q.Encode(), nil, nil)
	if err != nil {
		return "", fmt.Errorf("authorize: %w", err)
	}
	login := hidden(page)
	login.Set("action", "login")
	login.Set("email", email)
	login.Set("password", m.password)
	page, more, _, err := doForm(c, http.MethodPost, origin+"/authorize", login, cookies)
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	cookies = append(cookies, more...)
	consent := hidden(page)
	consent.Set("action", "consent")
	fields := levelField.FindAllStringSubmatch(page, -1)
	if len(fields) == 0 {
		return "", fmt.Errorf("consent page has no level fields (wrong password, or the user has no ceiling on any server?)")
	}
	for _, f := range fields {
		consent.Set(f[1], m.level)
	}
	_, _, location, err := doForm(c, http.MethodPost, origin+"/authorize", consent, cookies)
	if err != nil {
		return "", fmt.Errorf("consent: %w", err)
	}
	redirect, err := url.Parse(location)
	if err != nil || redirect.Query().Get("code") == "" {
		return "", fmt.Errorf("consent did not redirect with a code (Location %q)", location)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "code_verifier": {mintVerifier},
		"client_id": {clientID}, "redirect_uri": {mintCallback}, "resource": {resource},
	}
	if err := postFormJSON(c, origin+"/token", form, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("token: %v", err)
	}
	return tok.AccessToken, nil
}

func hidden(page string) url.Values {
	v := url.Values{}
	for _, m := range hiddenField.FindAllStringSubmatch(page, -1) {
		v.Set(m[1], m[2])
	}
	return v
}

// doForm sends a GET or a form POST and returns the body, the cookies set and the Location header.
func doForm(c *http.Client, method, target string, form url.Values, cookies []*http.Cookie) (body string, set []*http.Cookie, location string, err error) {
	var rd io.Reader
	if form != nil {
		rd = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, target, rd)
	if err != nil {
		return "", nil, "", err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther {
		return "", nil, "", fmt.Errorf("status %d: %s", resp.StatusCode, shorten(string(raw)))
	}
	return string(raw), resp.Cookies(), resp.Header.Get("Location"), nil
}

func doJSON(c *http.Client, method, target, body string, into any) error {
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return decode(c, req, into)
}

func postFormJSON(c *http.Client, target string, form url.Values, into any) error {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return decode(c, req, into)
}

func decode(c *http.Client, req *http.Request, into any) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, shorten(string(raw)))
	}
	return json.Unmarshal(raw, into)
}
