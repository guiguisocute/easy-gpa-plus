package opsconfig

// 运维在网页上填的 SES/CAM 凭据与模型 API Key 要落进 ops 库，所以必须先加密。
// 库备份、只读副本、一次误导出都会把它们一起带走——明文存等于把权限一并备份出去。
// 加密密钥本身不进库，仍然只在部署 Secret 里；库里只有密文。
//
// 没配密钥时 Cipher 为 nil：其余配置照常读写，只有"保存密码"这一个动作会被明确拒绝，
// 而不是悄悄降级成明文。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// 前缀既是版本号也是判据：读到没有前缀的值，说明它是历史遗留的明文，拒绝使用。
const secretPrefix = "enc:v1:"

type Cipher struct {
	aead    cipher.AEAD
	keyName string
}

// NewCipher accepts the key as 64 hex chars, standard base64, or 32 raw bytes.
func NewCipher(key string) (*Cipher, error) {
	return NewNamedCipher(key, "配置加密密钥")
}

// NewNamedCipher keeps errors tied to the deployment secret that needs fixing.
// The name is never persisted and does not take part in encryption.
func NewNamedCipher(key, keyName string) (*Cipher, error) {
	keyName = strings.TrimSpace(keyName)
	if keyName == "" {
		keyName = "配置加密密钥"
	}
	raw, err := decodeKey(strings.TrimSpace(key), keyName)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, keyName: keyName}, nil
}

// CipherFromKey returns a nil Cipher when no key is configured. Running without
// one is a legitimate deployment (SES credentials entirely from environment variables), but
// a key that cannot be parsed must fail at startup rather than when an operator
// first tries to save a password.
func CipherFromKey(key string) (*Cipher, error) {
	return CipherFromNamedKey(key, "MAIL_SECRET_KEY")
}

// CipherFromNamedKey is the shared constructor for independently rotated
// operational secrets such as SES credentials and the AI provider API key.
func CipherFromNamedKey(key, keyName string) (*Cipher, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil
	}
	return NewNamedCipher(key, keyName)
}

func decodeKey(key, keyName string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("%s 未设置", keyName)
	}
	if decoded, err := hex.DecodeString(key); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(key); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(key) == 32 {
		return []byte(key), nil
	}
	return nil, fmt.Errorf("%s 必须是 32 字节（64 位十六进制、base64 或 32 个字符）", keyName)
}

func (c *Cipher) Seal(plain string) (string, error) {
	if c == nil {
		return "", errors.New("未配置加密密钥，无法保存秘密值")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return secretPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Open(token string) (string, error) {
	if c == nil {
		return "", errors.New("未配置加密密钥，无法解密已保存的秘密值")
	}
	if !IsSealed(token) {
		return "", errors.New("秘密值不是本系统加密的格式")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(token, secretPrefix))
	if err != nil {
		return "", err
	}
	size := c.aead.NonceSize()
	if len(sealed) < size {
		return "", errors.New("密文长度不足")
	}
	plain, err := c.aead.Open(nil, sealed[:size], sealed[size:], nil)
	if err != nil {
		// 换过密钥就会走到这里。说清楚是密钥对不上，而不是让人以为密码填错了。
		name := c.keyName
		if name == "" {
			name = "配置加密密钥"
		}
		return "", fmt.Errorf("解密失败：%s 与保存密钥时使用的不是同一个", name)
	}
	return string(plain), nil
}

func IsSealed(token string) bool { return strings.HasPrefix(token, secretPrefix) }
