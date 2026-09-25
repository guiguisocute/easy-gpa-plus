package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var defaultPasswordParams = PasswordParams{
	Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32,
}

const (
	maxPasswordHashLength = 512
	maxArgon2Memory       = 256 * 1024 // KiB; limits malformed database records
	maxArgon2Iterations   = 10
	maxArgon2Parallelism  = 8
	maxArgon2SaltLength   = 64
	maxArgon2KeyLength    = 64
)

func ValidatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("密码至少需要 8 个字符")
	}
	if len(password) > 128 {
		return errors.New("密码不能超过 128 个字符")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	return hashPassword(password, defaultPasswordParams)
}

func hashPassword(password string, params PasswordParams) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.Memory, params.Iterations, params.Parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(hash)), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	if len(encoded) > maxPasswordHashLength {
		return false, errors.New("password hash is too long")
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, errors.New("invalid argon2 parameters")
	}
	if memory == 0 || memory > maxArgon2Memory || iterations == 0 || iterations > maxArgon2Iterations || parallelism == 0 || parallelism > maxArgon2Parallelism {
		return false, errors.New("argon2 parameters exceed safety limits")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("invalid password salt")
	}
	if len(salt) == 0 || len(salt) > maxArgon2SaltLength {
		return false, errors.New("invalid password salt length")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > maxArgon2KeyLength {
		return false, errors.New("invalid password digest")
	}
	keyLength := uint32(len(want)) // #nosec G115 -- digest length is capped at 64 above.
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLength)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
