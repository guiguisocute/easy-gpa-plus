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

// RuntimeSESMailer keeps its historical name while routing all supported mail
// providers. Settings are hot-reloadable; credentials never cross channels.
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
	cfg, err := m.configuration(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && m.fingerprint == fingerprint {
		return m.current, nil
	}
	var built Mailer
	switch cfg.Provider {
	case "tencent_ses":
		built, err = NewSESMailer(cfg.SES)
	case "smtp":
		built, err = NewSMTPMailer(cfg.SMTP)
	case "aliyun_dm":
		built, err = NewAliyunMailer(cfg.Aliyun)
	case "resend":
		built, err = NewResendMailer(cfg.Resend)
	default:
		err = errors.New("不支持的邮件通道")
	}
	if err != nil {
		return nil, err
	}
	if previous, ok := m.current.(*AliyunMailer); ok {
		previous.client.CloseIdleConnections()
	}
	if previous, ok := m.current.(*ResendMailer); ok {
		previous.client.CloseIdleConnections()
	}
	m.current, m.fingerprint = built, fingerprint
	return built, nil
}

func (m *RuntimeSESMailer) effective(ctx context.Context) (SESConfig, error) {
	cfg, err := m.configuration(ctx)
	if err != nil {
		return SESConfig{}, err
	}
	if cfg.Provider != "tencent_ses" {
		return SESConfig{}, errors.New("当前邮件通道不是腾讯云 SES")
	}
	return cfg.SES, nil
}

type mailConfiguration struct {
	Provider string
	SES      SESConfig
	SMTP     SMTPConfig
	Aliyun   AliyunMailConfig
	Resend   ResendConfig
}

func (m *RuntimeSESMailer) configuration(ctx context.Context) (mailConfiguration, error) {
	if m.source == nil {
		return mailConfiguration{Provider: "tencent_ses", SES: cloneSESConfig(m.fallback)}, nil
	}
	settings, err := m.source.Mail(ctx)
	if err != nil {
		// A settings outage must not silently switch SMTP/Aliyun traffic back
		// to the environment's Tencent identity.
		return mailConfiguration{}, errors.New("邮件配置暂时无法读取")
	}
	return ResolveMailConfiguration(settings, m.cipher, m.fallback)
}

func ResolveMailConfiguration(settings opsconfig.Mail, cipher *opsconfig.Cipher, fallback SESConfig) (mailConfiguration, error) {
	provider := settings.Provider
	if provider == "" {
		provider = "tencent_ses"
	}
	result := mailConfiguration{Provider: provider}
	var err error
	switch provider {
	case "tencent_ses":
		if settings.SESHasSettings() {
			result.SES, err = SESConfigFromSettings(settings, cipher)
		} else {
			result.SES = cloneSESConfig(fallback)
			err = result.SES.Validate()
		}
	case "smtp":
		result.SMTP, err = SMTPConfigFromSettings(settings, cipher)
	case "aliyun_dm":
		result.Aliyun, err = AliyunMailConfigFromSettings(settings, cipher)
	case "resend":
		result.Resend, err = ResendConfigFromSettings(settings, cipher)
	default:
		err = errors.New("邮件通道只能是 tencent_ses、smtp、aliyun_dm 或 resend")
	}
	return result, err
}

func ValidateMailNotifications(settings opsconfig.Mail) error {
	if settings.Provider == "tencent_ses" || settings.Provider == "" {
		return (SESConfig{NotificationsEnabled: settings.NotificationsEnabled, From: settings.SESFrom, NotificationFrom: settings.SESNotificationFrom, NotificationFromName: settings.SESNotificationFromName, TemplateIDs: settings.SESTemplateIDs}).NotificationReady()
	}
	return senderFromSettings(settings).NotificationReady()
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
