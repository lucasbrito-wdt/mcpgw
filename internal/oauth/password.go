package oauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (RFC 9106 second recommended option).
const (
	argonTime     uint32 = 3
	argonMemoryKB uint32 = 64 * 1024
	argonThreads  uint8  = 4
	argonKeyLen   uint32 = 32
	argonSaltLen         = 16

	// Upper bounds accepted when parsing stored hashes, so a bad row cannot exhaust memory.
	maxArgonMemoryKB = 256 * 1024
	maxArgonTime     = 10

	minPasswordLen = 12
	maxPasswordLen = 1024
)

var errMalformedHash = errors.New("malformed password hash")

// ErrWeakPassword is returned by HashPassword for passwords outside the accepted length.
var ErrWeakPassword = fmt.Errorf("password must have between %d and %d characters", minPasswordLen, maxPasswordLen)

// HashPassword returns an argon2id PHC string ($argon2id$v=19$m=..,t=..,p=..$salt$hash).
func HashPassword(password string) (string, error) {
	if n := len([]rune(password)); n < minPasswordLen || len(password) > maxPasswordLen {
		return "", ErrWeakPassword
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKB, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKB, argonTime, argonThreads, b64(salt), b64(key)), nil
}

// VerifyPassword reports whether password matches encoded, in constant time.
func VerifyPassword(password, encoded string) (bool, error) {
	if len(password) > maxPasswordLen {
		return false, nil
	}
	p, err := parseArgonHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	return subtle.ConstantTimeCompare(got, p.key) == 1, nil
}

type argonHash struct {
	time, memory uint32
	threads      uint8
	salt, key    []byte
}

func parseArgonHash(encoded string) (argonHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return argonHash{}, errMalformedHash
	}
	var p argonHash
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonHash{}, errMalformedHash
	}
	if p.memory == 0 || p.memory > maxArgonMemoryKB || p.time == 0 || p.time > maxArgonTime || p.threads == 0 {
		return argonHash{}, errMalformedHash
	}
	var errSalt, errKey error
	p.salt, errSalt = base64.RawStdEncoding.DecodeString(parts[4])
	p.key, errKey = base64.RawStdEncoding.DecodeString(parts[5])
	if errSalt != nil || errKey != nil || len(p.salt) < 8 || len(p.key) < 16 {
		return argonHash{}, errMalformedHash
	}
	return p, nil
}

// dummyPasswordHash is verified when the email is unknown so that response timing does not
// reveal which accounts exist.
var dummyPasswordHash = sync.OnceValue(func() string {
	h, err := HashPassword("dummy-password-for-timing")
	if err != nil {
		panic(err)
	}
	return h
})
