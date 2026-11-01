package codemode

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const (
	redactedToken  = "[redacted]"
	minSecretChars = 8
)

// credentialSecrets returns the secret strings a credential would put on a request, by applying
// it to a scratch request. Used only to scrub upstream output; it is a defense in depth for an
// upstream that echoes its credentials back in a result or an error.
func credentialSecrets(cred upstream.Credential) (secrets [][]byte) {
	if cred == nil {
		return nil
	}
	defer func() { _ = recover() }() // a custom Credential must not be able to break a call
	req := &http.Request{Header: http.Header{}, URL: &url.URL{}}
	cred.Apply(req)
	for _, values := range req.Header {
		for _, v := range values {
			secrets = appendSecret(secrets, v)
			if _, rest, ok := strings.Cut(v, " "); ok {
				secrets = appendSecret(secrets, rest) // "Bearer <token>" -> "<token>"
			}
		}
	}
	return secrets
}

func appendSecret(list [][]byte, s string) [][]byte {
	if len(s) < minSecretChars {
		return list
	}
	return append(list, []byte(s))
}

func scrub(b []byte, secrets [][]byte) []byte {
	for _, s := range secrets {
		b = bytes.ReplaceAll(b, s, []byte(redactedToken))
	}
	return b
}

func scrubString(s string, secrets [][]byte) string {
	if len(secrets) == 0 {
		return s
	}
	return string(scrub([]byte(s), secrets))
}
