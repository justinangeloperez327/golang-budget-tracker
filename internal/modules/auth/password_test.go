package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashAndVerification(t *testing.T) {
	a, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !strings.HasPrefix(a, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatal("expected independently salted Argon2id PHC hashes")
	}
	if !VerifyPassword(a, "correct horse battery staple") || VerifyPassword(a, "different-password") {
		t.Fatal("password verification failed")
	}
}
func TestPasswordPolicyAndMalformedHash(t *testing.T) {
	if _, err := HashPassword("short"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatal(err)
	}
	if _, err := HashPassword(strings.Repeat("x", 129)); !errors.Is(err, ErrInvalidPassword) {
		t.Fatal(err)
	}
	for _, s := range []string{"", "plaintext", "$argon2id$v=19$m=1,t=1,p=1$AA$BB", "$argon2id$v=19$m=999999999,t=2,p=1$AA$BB"} {
		if VerifyPassword(s, "a-password-that-is-long") {
			t.Fatalf("accepted bad hash: %q", s)
		}
	}
}
func TestIdentityUtilities(t *testing.T) {
	got, err := NormalizeEmail("  Foo.BAR@Example.COM ")
	if err != nil || got != "foo.bar@example.com" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := NormalizeEmail("bad@@example.com"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	token, err := randomHex(32)
	if err != nil {
		t.Fatal(err)
	}
	if _, valid := tokenHash(token); !valid {
		t.Fatal("valid token rejected")
	}
	if _, valid := tokenHash("invalid"); valid {
		t.Fatal("invalid token accepted")
	}
	if RemoteIP("127.0.0.1:9090") != "127.0.0.1" {
		t.Fatal("failed to extract IP")
	}
}
