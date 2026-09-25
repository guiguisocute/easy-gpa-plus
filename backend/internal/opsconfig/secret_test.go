package opsconfig

import (
	"strings"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	cipher, err := NewCipher(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal("腾讯云-SES-SecretKey")
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(sealed) || strings.Contains(sealed, "腾讯云") {
		t.Fatalf("sealed value still exposes the secret: %q", sealed)
	}
	plain, err := cipher.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "腾讯云-SES-SecretKey" {
		t.Fatalf("round trip = %q", plain)
	}
}

// 同一段明文两次加密必须不同，否则密文本身就泄露了"密码有没有换过"。
func TestCipherSealIsRandomized(t *testing.T) {
	cipher, _ := NewCipher(strings.Repeat("k", 32))
	first, _ := cipher.Seal("same")
	second, _ := cipher.Seal("same")
	if first == second {
		t.Fatal("expected a fresh nonce per seal")
	}
}

// 换了 MAIL_SECRET_KEY 之后要给出"密钥对不上"，而不是让运维以为密码填错了。
func TestCipherOpenWithWrongKeyFails(t *testing.T) {
	sealed, _ := mustCipher(t, strings.Repeat("k", 32)).Seal("secret")
	if _, err := mustCipher(t, strings.Repeat("j", 32)).Open(sealed); err == nil {
		t.Fatal("expected decryption with a different key to fail")
	}
}

func TestCipherFromKeyEmptyIsNotAnError(t *testing.T) {
	cipher, err := CipherFromKey("   ")
	if err != nil || cipher != nil {
		t.Fatalf("empty key = (%v, %v), want (nil, nil)", cipher, err)
	}
	if _, err := CipherFromKey("too-short"); err == nil {
		t.Fatal("expected an unusable key to fail loudly")
	}
}

// nil Cipher 出现在"没配 MAIL_SECRET_KEY"的部署里，不能 panic。
func TestNilCipherRefusesInsteadOfPanicking(t *testing.T) {
	var cipher *Cipher
	if _, err := cipher.Seal("x"); err == nil {
		t.Fatal("expected sealing without a key to fail")
	}
	if _, err := cipher.Open(secretPrefix + "AAAA"); err == nil {
		t.Fatal("expected opening without a key to fail")
	}
}

func mustCipher(t *testing.T, key string) *Cipher {
	t.Helper()
	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}
