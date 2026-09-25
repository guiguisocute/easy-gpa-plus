package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"easygpa/backend/internal/opsconfig"
)

type mailSettingsSource interface {
	Mail(context.Context) (opsconfig.Mail, error)
}

// RuntimeSESMailer resolves hot-reloadable ops settings for each send and
// caches the SDK client by a one-way configuration fingerprint. Database
// settings take precedence only when they form a complete, independent SES
// configuration; credentials are never mixed across sources.
type RuntimeSESMailer struct {
	source   mailSettingsSource
	cipher   *opsconfig.Cipher
	fallback SESConfig

	mu          sync.Mutex
	fingerprint string
	current     Mailer
}

func NewRuntimeSESMailer(source mailSettingsSource, cipher *opsconfig.Cipher, fallback SESConfig) (*RuntimeSESMailer, error) {
	fallback = normalizeSESConfig(fallback)
	if fallback.HasSettings() {
		if err := fallback.Validate(); err != nil {
			return nil, err
		}
	}
	return &RuntimeSESMailer{source: source, cipher: cipher, fallback: fallback}, nil
}

func (m *RuntimeSESMailer) Send(ctx context.Context, msg Message) (string, error) {
	mailer, err := m.resolve(ctx)
	if err != nil {
		return "", &NotSubmittedError{Err: err}
	}
	return mailer.Send(ctx, msg)
}

func (m *RuntimeSESMailer) Ready(ctx context.Context) error {
	_, err := m.resolve(ctx)
	return err
}

func (m *RuntimeSESMailer) resolve(ctx context.Context) (Mailer, error) {
	cfg, err := m.effective(ctx)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	fingerprint, err := sesConfigFingerprint(cfg)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && m.fingerprint == fingerprint {
		return m.current, nil
	}
	built, err := NewSESMailer(cfg)
	if err != nil {
		return nil, err
	}
	m.current, m.fingerprint = built, fingerprint
	return built, nil
}

func (m *RuntimeSESMailer) effective(ctx context.Context) (SESConfig, error) {
	if m.source == nil {
		return cloneSESConfig(m.fallback), nil
	}
	settings, err := m.source.Mail(ctx)
	if err != nil {
		return cloneSESConfig(m.fallback), nil
	}
	if !settings.SESHasSettings() {
		return cloneSESConfig(m.fallback), nil
	}
	return SESConfigFromSettings(settings, m.cipher)
}

func SESConfigFromSettings(settings opsconfig.Mail, cipher *opsconfig.Cipher) (SESConfig, error) {
	cfg := SESConfig{
		NotificationsEnabled: settings.NotificationsEnabled,
		NotificationFrom:     strings.TrimSpace(settings.SESNotificationFrom),
		NotificationFromName: strings.TrimSpace(settings.SESNotificationFromName),
		Region:               strings.TrimSpace(settings.SESRegion),
		From:                 strings.TrimSpace(settings.SESFrom),
		FromName:             strings.TrimSpace(settings.SESFromName),
		ReplyTo:              strings.TrimSpace(settings.SESReplyTo),
		TemplateIDs:          cloneTemplateIDs(settings.SESTemplateIDs),
	}
	if cfg.Region == "" {
		cfg.Region = "ap-guangzhou"
	}
	var err error
	cfg.SecretID, err = openSESSecret("SecretId", settings.SESSecretID, cipher)
	if err != nil {
		return SESConfig{}, err
	}
	cfg.SecretKey, err = openSESSecret("SecretKey", settings.SESSecretKey, cipher)
	if err != nil {
		return SESConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return SESConfig{}, err
	}
	return cfg, nil
}

func openSESSecret(label, stored string, cipher *opsconfig.Cipher) (string, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return "", nil
	}
	if !opsconfig.IsSealed(stored) {
		return "", errors.New("已保存的腾讯云 " + label + " 不是加密格式，请在运维页重新填写")
	}
	if cipher == nil {
		return "", errors.New("服务端未配置 MAIL_SECRET_KEY，无法解密腾讯云 SES 凭据")
	}
	return cipher.Open(stored)
}

func sesConfigFingerprint(cfg SESConfig) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func cloneSESConfig(cfg SESConfig) SESConfig {
	cfg.TemplateIDs = cloneTemplateIDs(cfg.TemplateIDs)
	return cfg
}

func cloneTemplateIDs(source map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
