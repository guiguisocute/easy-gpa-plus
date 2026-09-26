package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" // Direct Mail RPC v1 specifies HMAC-SHA1.
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"easygpa/backend/internal/opsconfig"
	"github.com/google/uuid"
)

// Official API endpoints (Sydney is decommissioned):
// https://www.alibabacloud.com/help/en/direct-mail/api-dm-2015-11-23-endpoint
var aliyunMailEndpoints = map[string]string{
	"cn-hangzhou":    "https://dm.aliyuncs.com/",
	"ap-southeast-1": "https://dm.ap-southeast-1.aliyuncs.com/",
	"us-east-1":      "https://dm.us-east-1.aliyuncs.com/",
	"eu-central-1":   "https://dm.eu-central-1.aliyuncs.com/",
}

func ValidAliyunMailRegion(region string) bool { return aliyunMailEndpoints[region] != "" }

type AliyunMailConfig struct {
	SenderConfig
	Region          string
	AccessKeyID     string
	AccessKeySecret string
}

func (c AliyunMailConfig) Validate() error {
	if err := c.SenderConfig.Validate(); err != nil {
		return err
	}
	if aliyunMailEndpoints[c.Region] == "" {
		return errors.New("阿里云邮件地域只能是 cn-hangzhou、ap-southeast-1、us-east-1 或 eu-central-1")
	}
	if strings.TrimSpace(c.AccessKeyID) == "" || strings.TrimSpace(c.AccessKeySecret) == "" {
		return errors.New("阿里云邮件需要独立配置 AccessKey ID 和 AccessKey Secret")
	}
	if utf8.RuneCountInString(c.FromName) > 15 || utf8.RuneCountInString(c.NotificationFromName) > 15 {
		return errors.New("阿里云邮件发件人显示名不能超过 15 个字符")
	}
	return nil
}

func AliyunMailConfigFromSettings(settings opsconfig.Mail, cipher *opsconfig.Cipher) (AliyunMailConfig, error) {
	cfg := AliyunMailConfig{SenderConfig: senderFromSettings(settings), Region: settings.AliyunRegion}
	var err error
	if cfg.AccessKeyID, err = openMailSecret("阿里云 AccessKey ID", settings.AliyunAccessKeyID, cipher); err != nil {
		return AliyunMailConfig{}, err
	}
	if cfg.AccessKeySecret, err = openMailSecret("阿里云 AccessKey Secret", settings.AliyunAccessKeySecret, cipher); err != nil {
		return AliyunMailConfig{}, err
	}
	return cfg, cfg.Validate()
}

type AliyunMailer struct {
	cfg    AliyunMailConfig
	client *http.Client
	now    func() time.Time
	nonce  func() string
}

func NewAliyunMailer(cfg AliyunMailConfig) (*AliyunMailer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &AliyunMailer{cfg: cfg, now: time.Now, nonce: uuid.NewString, client: &http.Client{
		Timeout: 30 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (m *AliyunMailer) Ready(context.Context) error { return m.cfg.Validate() }

func (m *AliyunMailer) Send(ctx context.Context, msg Message) (string, error) {
	from, name, err := m.cfg.forMessage(msg)
	if err != nil {
		return "", &NotSubmittedError{Err: err}
	}
	if utf8.RuneCountInString(msg.Subject) > 100 || len(msg.HTML)+len(msg.Text) > 80*1024 {
		return "", &NotSubmittedError{Err: errors.New("阿里云邮件主题不能超过 100 个字符，正文不能超过 80 KB")}
	}
	parameters := url.Values{
		"Action": {"SingleSendMail"}, "Version": {"2015-11-23"}, "Format": {"JSON"}, "RegionId": {m.cfg.Region},
		"AccessKeyId": {m.cfg.AccessKeyID}, "SignatureMethod": {"HMAC-SHA1"}, "SignatureVersion": {"1.0"},
		"SignatureNonce": {m.nonce()}, "Timestamp": {m.now().UTC().Format("2006-01-02T15:04:05Z")},
		"AccountName": {from}, "AddressType": {"1"}, "ReplyToAddress": {"false"},
		"ToAddress": {msg.To}, "Subject": {msg.Subject}, "ClickTrace": {"0"},
	}
	if name != "" {
		parameters.Set("FromAlias", name)
	}
	if m.cfg.ReplyTo != "" {
		parameters.Set("ReplyAddress", m.cfg.ReplyTo)
	}
	if msg.HTML != "" {
		parameters.Set("HtmlBody", msg.HTML)
	}
	if msg.Text != "" {
		parameters.Set("TextBody", msg.Text)
	}
	parameters.Set("Signature", aliyunMailSignature(http.MethodPost, parameters, m.cfg.AccessKeySecret))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, aliyunMailEndpoints[m.cfg.Region], strings.NewReader(parameters.Encode()))
	if err != nil {
		return "", &NotSubmittedError{Err: errors.New("阿里云邮件请求无法构造")}
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := m.client.Do(request)
	if err != nil {
		return "", &ProviderFailure{Provider: "Aliyun", Code: "OutcomeUnknown"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return "", &ProviderFailure{Provider: "Aliyun", Code: "InvalidResponse"}
	}
	var result struct {
		EnvID string `json:"EnvId"`
		Code  string `json:"Code"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", &ProviderFailure{Provider: "Aliyun", Code: "InvalidResponse"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Code != "" {
		code := safeProviderCode(result.Code)
		return "", &ProviderFailure{Provider: "Aliyun", Code: code,
			Rejected: response.StatusCode >= 400 && response.StatusCode < 500,
			Limited:  response.StatusCode == http.StatusTooManyRequests || strings.HasPrefix(code, "Throttling") || strings.HasPrefix(code, "LimitExceeded"),
		}
	}
	if result.EnvID == "" || len(result.EnvID) > 256 || strings.ContainsAny(result.EnvID, "\r\n") {
		return "", &ProviderFailure{Provider: "Aliyun", Code: "MissingReceipt"}
	}
	// Prefix keeps these receipts out of Tencent-only feedback polling even
	// after an operator later changes the active sending channel.
	return "aliyun:" + result.EnvID, nil
}

// RPC v1 signing is documented, with the test vector used in aliyun_test.go:
// https://www.alibabacloud.com/help/en/direct-mail/signature
func aliyunMailSignature(method string, parameters url.Values, secret string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, aliyunPercentEncode(key)+"="+aliyunPercentEncode(parameters.Get(key)))
	}
	toSign := method + "&%2F&" + aliyunPercentEncode(strings.Join(parts, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(toSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func aliyunPercentEncode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
