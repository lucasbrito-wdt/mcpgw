package vault

import (
	"bytes"
	"errors"
	"testing"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, MasterKeySize) }

func newVault(t *testing.T, key byte, id string) *Vault {
	t.Helper()
	v, err := New(testKey(key), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNewValidation(t *testing.T) {
	if _, err := New(make([]byte, 16), "k1"); err == nil {
		t.Error("short master key accepted")
	}
	if _, err := New(testKey(1), ""); err == nil {
		t.Error("empty key id accepted")
	}
	if _, err := New(testKey(1), string(make([]byte, 256))); err == nil {
		t.Error("oversized key id accepted")
	}
}

func TestRoundTrip(t *testing.T) {
	v := newVault(t, 1, "k1")
	aad := []byte("upstream_credentials:srv:user")
	for _, pt := range [][]byte{[]byte("secret-token"), {}, bytes.Repeat([]byte("x"), 100000)} {
		ct, err := v.Seal(pt, aad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Open(ct, aad)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("round trip mismatch for len %d", len(pt))
		}
	}
	ct, _ := v.Seal([]byte("a"), nil)
	if id, err := KeyID(ct); err != nil || id != "k1" {
		t.Fatalf("KeyID = %q, %v", id, err)
	}
}

func TestWrongAAD(t *testing.T) {
	v := newVault(t, 1, "k1")
	ct, _ := v.Seal([]byte("secret"), []byte("upstream_credentials:s1:u1"))
	if _, err := v.Open(ct, []byte("upstream_credentials:s1:u2")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
	if _, err := v.Open(ct, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("nil aad err = %v, want ErrDecrypt", err)
	}
}

func TestTamperedByteFails(t *testing.T) {
	v := newVault(t, 1, "k1")
	aad := []byte("ctx")
	ct, _ := v.Seal([]byte("secret"), aad)
	for i := range ct {
		mut := bytes.Clone(ct)
		mut[i] ^= 0x01
		if _, err := v.Open(mut, aad); err == nil {
			t.Fatalf("tampering byte %d went undetected", i)
		}
	}
}

func TestWrongKey(t *testing.T) {
	v1 := newVault(t, 1, "k1")
	ct, _ := v1.Seal([]byte("secret"), nil)

	if _, err := newVault(t, 2, "k1").Open(ct, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("different master key: %v, want ErrDecrypt", err)
	}
	if _, err := newVault(t, 1, "k2").Open(ct, nil); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("different key id: %v, want ErrKeyMismatch", err)
	}
}

func TestKeyIDDomainSeparation(t *testing.T) {
	// Same master key, different key ids derive different DEKs: forging the id in the header must fail.
	a := newVault(t, 1, "k1")
	b := newVault(t, 1, "k2")
	ct, _ := a.Seal([]byte("secret"), nil)
	forged := bytes.Replace(ct, []byte("k1"), []byte("k2"), 1)
	if _, err := b.Open(forged, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestNonceUniqueness(t *testing.T) {
	v := newVault(t, 1, "k1")
	seen := make(map[string]struct{})
	for range 5000 {
		ct, err := v.Seal([]byte("same"), nil)
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(ct[2+len("k1") : 2+len("k1")+nonceSize])
		if _, dup := seen[nonce]; dup {
			t.Fatal("nonce reused")
		}
		seen[nonce] = struct{}{}
	}
}

func TestGarbageInputDoesNotPanic(t *testing.T) {
	v := newVault(t, 1, "k1")
	valid, _ := v.Seal([]byte("secret"), nil)
	inputs := [][]byte{nil, {}, {1}, {1, 0}, {1, 2, 'k'}, {2, 2, 'k', '1'}, bytes.Repeat([]byte{0xff}, 64), valid[:len(valid)-1], valid[:10]}
	for i, in := range inputs {
		if _, err := v.Open(in, nil); err == nil {
			t.Errorf("input %d: expected error", i)
		}
		_, _ = KeyID(in)
	}
}
