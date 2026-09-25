package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	params := PasswordParams{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	hash, err := hashPassword("correct horse", params)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword("correct horse", hash)
	if err != nil || !ok {
		t.Fatalf("correct password: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong horse", hash)
	if err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
}

func TestVerifyPasswordRejectsUnsafeEncodedParameters(t *testing.T) {
	hash, err := hashPassword("correct horse", PasswordParams{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	unsafe := strings.Replace(hash, "m=1024", "m=262145", 1)
	if _, err := VerifyPassword("correct horse", unsafe); err == nil {
		t.Fatal("unsafe Argon2 memory parameter was accepted")
	}
	if _, err := VerifyPassword("correct horse", strings.Repeat("x", maxPasswordHashLength+1)); err == nil {
		t.Fatal("oversized password hash was accepted")
	}
}
