// Package vault provides envelope encryption (AES-256-GCM) for secrets stored in Postgres.
//
// Key hierarchy: the master key from config is the KEK. A data key (DEK) is derived from it with
// HKDF-SHA256 (info = "mcpgw/vault/v1/" + keyID) instead of storing wrapped DEKs. This keeps the
// database free of key material, needs no extra table and no KMS round trip. The cost is that
// the KEK and the DEK have the same blast radius, which is acceptable: wrapping a DEK under the
// same in-process KEK would not add protection.
//
// Rotation: every ciphertext carries the key id that sealed it. To rotate, deploy the new master key
// under a new key id, keep one Vault per retired key (selected with KeyID(ciphertext)) to open old
// rows, and re-seal rows lazily or with a batch job. upstream_credentials.key_id mirrors the id.
//
// Ciphertext layout: version(1) | keyIDLen(1) | keyID | nonce(12) | AES-GCM(ciphertext||tag).
// The version byte, key id and nonce header is passed to GCM as part of the AAD together with the
// caller AAD, so the header cannot be altered, and a ciphertext moved to another row (different
// caller AAD) fails to open.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	// MasterKeySize is the required KEK length in bytes.
	MasterKeySize = 32

	formatVersion = 1
	nonceSize     = 12
	maxKeyIDLen   = 255
	hkdfInfo      = "mcpgw/vault/v1/"
)

var (
	// ErrInvalidCiphertext is returned for malformed, truncated or unsupported input.
	ErrInvalidCiphertext = errors.New("vault: invalid ciphertext")
	// ErrKeyMismatch is returned when the ciphertext was sealed under a different key id.
	ErrKeyMismatch = errors.New("vault: ciphertext key id does not match this vault")
	// ErrDecrypt is returned when authentication fails (wrong key, wrong AAD, tampering).
	ErrDecrypt = errors.New("vault: decryption failed")
)

// Vault seals and opens secrets under one key id. It is safe for concurrent use.
type Vault struct {
	keyID string
	aead  cipher.AEAD
}

// New derives the data key for keyID from the 32-byte master key.
func New(masterKey []byte, keyID string) (*Vault, error) {
	if len(masterKey) != MasterKeySize {
		return nil, fmt.Errorf("vault: master key must be %d bytes, got %d", MasterKeySize, len(masterKey))
	}
	if keyID == "" || len(keyID) > maxKeyIDLen {
		return nil, fmt.Errorf("vault: key id must be 1..%d bytes", maxKeyIDLen)
	}
	dek, err := hkdf.Key(sha256.New, masterKey, nil, hkdfInfo+keyID, 32)
	if err != nil {
		return nil, fmt.Errorf("vault: derive data key: %w", err)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("vault: init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: init gcm: %w", err)
	}
	return &Vault{keyID: keyID, aead: aead}, nil
}

// KeyID returns the id of the key this vault seals with.
func (v *Vault) KeyID() string { return v.keyID }

// Seal encrypts plaintext, binding it to aad (for example "upstream_credentials:<server_id>:<user_id>").
func (v *Vault) Seal(plaintext, aad []byte) ([]byte, error) {
	header := make([]byte, 0, 2+len(v.keyID)+nonceSize)
	header = append(header, formatVersion, byte(len(v.keyID)))
	header = append(header, v.keyID...)
	nonceStart := len(header)
	header = header[:nonceStart+nonceSize]
	if _, err := rand.Read(header[nonceStart:]); err != nil {
		return nil, fmt.Errorf("vault: generate nonce: %w", err)
	}
	out := v.aead.Seal(header, header[nonceStart:], plaintext, fullAAD(header, aad))
	return out, nil
}

// Open authenticates and decrypts a ciphertext produced by Seal with the same aad.
func (v *Vault) Open(ciphertext, aad []byte) ([]byte, error) {
	keyID, headerLen, err := parseHeader(ciphertext)
	if err != nil {
		return nil, err
	}
	if keyID != v.keyID {
		return nil, ErrKeyMismatch
	}
	header := ciphertext[:headerLen]
	nonce := header[headerLen-nonceSize:]
	plaintext, err := v.aead.Open(nil, nonce, ciphertext[headerLen:], fullAAD(header, aad))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// KeyID reads the key id from a ciphertext without decrypting it, to pick the right Vault.
func KeyID(ciphertext []byte) (string, error) {
	id, _, err := parseHeader(ciphertext)
	return id, err
}

func parseHeader(ct []byte) (keyID string, headerLen int, err error) {
	if len(ct) < 2 || ct[0] != formatVersion {
		return "", 0, ErrInvalidCiphertext
	}
	idLen := int(ct[1])
	headerLen = 2 + idLen + nonceSize
	if idLen == 0 || len(ct) < headerLen+16 { // 16 = GCM tag
		return "", 0, ErrInvalidCiphertext
	}
	return string(ct[2 : 2+idLen]), headerLen, nil
}

func fullAAD(header, aad []byte) []byte {
	out := make([]byte, 0, len(header)+len(aad))
	out = append(out, header...)
	return append(out, aad...)
}
