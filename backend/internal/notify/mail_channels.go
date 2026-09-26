package notify

import (
	"errors"
	"fmt"
	"strings"

	"easygpa/backend/internal/opsconfig"
)

// SenderConfig is shared by the channels which send locally rendered mail.
type SenderConfig struct {
	From                 string
	FromName             string
	ReplyTo              string
	NotificationsEnabled bool
	NotificationFrom     string
	NotificationFromName string
}

func senderFromSettings(m opsconfig.Mail) SenderConfig {
	return SenderConfig{From: m.SESFrom, FromName: m.SESFromName, ReplyTo: m.SESReplyTo,
		NotificationsEnabled: m.NotificationsEnabled, NotificationFrom: m.SESNotificationFrom, NotificationFromName: m.SESNotificationFromName}
}

func (s SenderConfig) Validate() error {
	if err := validatePlainEmail("发信地址", s.From); err != nil {
		return err
	}
	if s.ReplyTo != "" {
		if err := validatePlainEmail("回复地址", s.ReplyTo); err != nil {
			return err
		}
	}
	if strings.ContainsAny(s.FromName, ":<>\r\n") || len(s.FromName) > 256 {
		return errors.New("发件人显示名格式不正确")
	}
	return nil
}

func (s SenderConfig) NotificationReady() error {
	if !s.NotificationsEnabled {
		return errors.New("业务邮件已暂停")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if err := validatePlainEmail("通知发信地址", s.NotificationFrom); err != nil {
		return err
	}
	_, domain, _ := strings.Cut(s.From, "@")
	_, notificationDomain, _ := strings.Cut(s.NotificationFrom, "@")
	if strings.EqualFold(domain, notificationDomain) {
		return errors.New("通知邮件必须使用独立发件域名")
	}
	if strings.ContainsAny(s.NotificationFromName, ":<>\r\n") || len(s.NotificationFromName) > 256 {
		return errors.New("通知发件人显示名格式不正确")
	}
	return nil
}

func (s SenderConfig) forMessage(msg Message) (string, string, error) {
	if err := s.Validate(); err != nil {
		return "", "", err
	}
	if strings.ContainsAny(msg.Subject, "\r\n") || strings.TrimSpace(msg.Subject) == "" {
		return "", "", errors.New("邮件主题不能为空或包含换行")
	}
	if err := validatePlainEmail("收件人", msg.To); err != nil {
		return "", "", err
	}
	if msg.HTML == "" && msg.Text == "" {
		return "", "", errors.New("邮件正文不能为空")
	}
	if isBusinessMail(msg) {
		if err := s.NotificationReady(); err != nil {
			return "", "", err
		}
		if msg.Template != TemplateNotificationAlert && msg.Template != TemplateNotificationDigest {
			return "", "", errors.New("旧业务邮件模板已停用")
		}
		return s.NotificationFrom, s.NotificationFromName, nil
	}
	return s.From, s.FromName, nil
}

func openMailSecret(label, stored string, cipher *opsconfig.Cipher) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !opsconfig.IsSealed(stored) {
		return "", fmt.Errorf("已保存的 %s 不是加密格式，请重新填写", label)
	}
	if cipher == nil {
		return "", errors.New("服务端未配置 MAIL_SECRET_KEY，无法解密邮件凭据")
	}
	return cipher.Open(stored)
}

// ProviderFailure deliberately excludes arbitrary provider response text,
// which can contain recipient addresses, credentials, or message contents.
type ProviderFailure struct {
	Provider string
	Code     string
	Rejected bool
	Limited  bool
}

func (e *ProviderFailure) Error() string   { return e.Provider + ": " + e.Code }
func (e *ProviderFailure) GetCode() string { return e.Provider + "." + e.Code }

func safeProviderCode(code string) string {
	if code == "" || len(code) > 128 {
		return "RequestRejected"
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return "RequestRejected"
		}
	}
	return code
}
