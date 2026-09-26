package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"easygpa/backend/internal/opsconfig"
)

const resendEmailEndpoint = "https://api.resend.com/emails"

type ResendConfig struct {
	SenderConfig
	APIKey string
}

func (c ResendConfig) Validate() error {
	if err := c.SenderConfig.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.APIKey) == "" || strings.ContainsAny(c.APIKey, "\r\n") {
		return errors.New("Resend API Key 未配置或格式不正确")
	}
	return nil
}

func ResendConfigFromSettings(settings opsconfig.Mail, cipher *opsconfig.Cipher) (ResendConfig, error) {
	cfg := ResendConfig{SenderConfig: senderFromSettings(settings)}
	var err error
	if cfg.APIKey, err = openMailSecret("Resend API Key", settings.ResendAPIKey, cipher); err != nil {
		return ResendConfig{}, err
	}
	return cfg, cfg.Validate()
}

type ResendMailer struct {
	cfg    ResendConfig
	client *http.Client
}

func NewResendMailer(cfg ResendConfig) (*ResendMailer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &ResendMailer{cfg: cfg, client: &http.Client{
		Timeout: 30 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (m *ResendMailer) Ready(context.Context) error { return m.cfg.Validate() }

// https://resend.com/docs/api-reference/emails/send-email
func (m *ResendMailer) Send(ctx context.Context, msg Message) (string, error) {
	from, name, err := m.cfg.forMessage(msg)
	if err != nil {
		return "", &NotSubmittedError{Err: err}
	}
	if name != "" {
		from = name + " <" + from + ">"
	}
	input := struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html,omitempty"`
		Text    string   `json:"text,omitempty"`
		ReplyTo string   `json:"reply_to,omitempty"`
	}{From: from, To: []string{msg.To}, Subject: msg.Subject, HTML: msg.HTML, Text: msg.Text, ReplyTo: m.cfg.ReplyTo}
	body, err := json.Marshal(input)
	if err != nil {
		return "", &NotSubmittedError{Err: errors.New("Resend 邮件内容无法编码")}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resendEmailEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", &NotSubmittedError{Err: errors.New("Resend 请求无法构造")}
	}
	request.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	request.Header.Set("Content-Type", "application/json")
	if msg.EventID != "" {
		digest := sha256.Sum256([]byte(msg.EventID + "\x00" + strings.ToLower(strings.TrimSpace(msg.To))))
		request.Header.Set("Idempotency-Key", "easygpa-"+hex.EncodeToString(digest[:]))
	}
	response, err := m.client.Do(request)
	if err != nil {
		return "", &ProviderFailure{Provider: "Resend", Code: "OutcomeUnknown"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return "", &ProviderFailure{Provider: "Resend", Code: "InvalidResponse"}
	}
	var result struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", &ProviderFailure{Provider: "Resend", Code: "InvalidResponse"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Name != "" {
		return "", &ProviderFailure{Provider: "Resend", Code: safeProviderCode(result.Name),
			Rejected: response.StatusCode >= 400 && response.StatusCode < 500 && result.Name != "concurrent_idempotent_requests",
			Limited:  response.StatusCode == http.StatusTooManyRequests,
		}
	}
	if result.ID == "" || len(result.ID) > 256 || strings.ContainsAny(result.ID, "\r\n") {
		return "", &ProviderFailure{Provider: "Resend", Code: "MissingReceipt"}
	}
	return "resend:" + result.ID, nil
}
