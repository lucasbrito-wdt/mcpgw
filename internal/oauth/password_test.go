package oauth

import (
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash format: %s", hash)
	}
	if ok, err := VerifyPassword(testPassword, hash); !ok || err != nil {
		t.Fatalf("verify correct password: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword(testPassword+"x", hash); ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=65536,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=99999999,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA", "plain"} {
		if _, err := VerifyPassword(testPassword, bad); err == nil {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}
