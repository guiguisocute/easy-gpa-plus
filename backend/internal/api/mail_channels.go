package api

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"easygpa/backend/internal/notify"
	"easygpa/backend/internal/opsconfig"
	"github.com/gin-gonic/gin"
)

func (s *Server) environmentMailConfig() notify.SESConfig {
	if s.cfg == nil {
		return notify.SESConfig{}
	}
	return notify.SESConfig{Region: s.cfg.TencentCloudSESRegion, SecretID: s.cfg.TencentCloudSecretID, SecretKey: s.cfg.TencentCloudSecretKey,
		From: s.cfg.TencentCloudSESFrom, FromName: s.cfg.TencentCloudSESFromName, ReplyTo: s.cfg.TencentCloudSESReplyTo, TemplateIDs: s.cfg.TencentCloudSESTemplateIDs}
}

func (s *Server) mailChannelStatus(settings opsconfig.Mail) gin.H {
	_, err := notify.ResolveMailConfiguration(settings, s.deps.MailCipher, s.environmentMailConfig())
	status := gin.H{
		"smtpUsernameSet": settings.SMTPUsername != "", "smtpPasswordSet": settings.SMTPPassword != "",
		"aliyunAccessKeyIdSet": settings.AliyunAccessKeyID != "", "aliyunAccessKeySecretSet": settings.AliyunAccessKeySecret != "",
		"resendApiKeySet": settings.ResendAPIKey != "",
		"configured":      err == nil, "restartRequired": false,
		"feedbackSupported": settings.Provider == "tencent_ses" || settings.Provider == "",
	}
	if err != nil {
		status["configurationError"] = err.Error()
	}
	return status
}

func normalizeMailChannels(input map[string]any, cipher *opsconfig.Cipher) error {
	if err := normalizeMailSES(input, cipher); err != nil {
		return err
	}
	for _, field := range []string{"provider", "smtpHost", "smtpSecurity", "aliyunRegion"} {
		if value, exists := input[field]; exists {
			text, ok := value.(string)
			if !ok {
				return errors.New(field + " 必须是字符串")
			}
			input[field] = strings.ToLower(strings.TrimSpace(text))
		}
	}
	if value, exists := input["provider"]; exists {
		if value != "tencent_ses" && value != "smtp" && value != "aliyun_dm" && value != "resend" {
			return errors.New("邮件通道只能是 tencent_ses、smtp、aliyun_dm 或 resend")
		}
	}
	if value, exists := input["smtpPort"]; exists {
		port, ok := value.(float64)
		if !ok || port < 1 || port > 65535 || port != math.Trunc(port) {
			return errors.New("SMTP 端口必须是 1—65535 的整数")
		}
	}
	if value, exists := input["smtpSecurity"]; exists && value != "starttls" && value != "tls" {
		return errors.New("SMTP 必须使用 STARTTLS 或隐式 TLS")
	}
	if value, exists := input["smtpHost"].(string); exists && value != "" {
		// Validate a destination independently so an incomplete draft can be
		// saved or credentials cleared without claiming it is ready to send.
		cfg := notify.SMTPConfig{SenderConfig: notify.SenderConfig{From: "sender@example.org"}, Host: value, Port: 587, Security: "starttls"}
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	if value, exists := input["aliyunRegion"].(string); exists && !notify.ValidAliyunMailRegion(value) {
		return errors.New("阿里云邮件地域只能是 cn-hangzhou、ap-southeast-1、us-east-1 或 eu-central-1")
	}
	if input["provider"] == "aliyun_dm" {
		for _, field := range []string{"sesFromName", "sesNotificationFromName"} {
			if value, ok := input[field].(string); ok && utf8.RuneCountInString(value) > 15 {
				return errors.New("阿里云邮件发件人显示名不能超过 15 个字符")
			}
		}
	}
	return nil
}

func validateSMTPDestinationChange(current, next opsconfig.Mail, input map[string]any) error {
	if current.SMTPHost == "" || current.SMTPHost == next.SMTPHost && current.SMTPPort == next.SMTPPort || current.SMTPUsername == "" && current.SMTPPassword == "" {
		return nil
	}
	_, usernameProvided := input["smtpUsername"]
	_, passwordProvided := input["smtpPassword"]
	if !usernameProvided || !passwordProvided {
		return errors.New("更换 SMTP 主机或端口时，请重新填写或同时清除用户名和密码，避免旧凭据发送到新服务")
	}
	return nil
}

func mailProviderName(provider string) string {
	switch provider {
	case "smtp":
		return "SMTP"
	case "aliyun_dm":
		return "阿里云邮件推送"
	case "resend":
		return "Resend"
	default:
		return "腾讯云 SES API"
	}
}
