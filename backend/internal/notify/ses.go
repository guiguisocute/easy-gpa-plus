package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"sort"
	"strconv"
	"strings"

	"easygpa/backend/internal/opsconfig"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	sdkerrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	ses "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ses/v20201002"
)

const tencentSESEndpoint = "ses.tencentcloudapi.com"

func isSESFrequencyLimit(err error) bool {
	var other *ProviderFailure
	if errors.As(err, &other) {
		return other.Limited
	}
	var providerError *sdkerrors.TencentCloudSDKError
	return errors.As(err, &providerError) && providerError != nil && providerError.GetCode() == "FailedOperation.FrequencyLimit"
}

var supportedSESRegions = map[string]struct{}{
	"ap-guangzhou": {},
	"ap-hongkong":  {},
}

// SESConfig contains the Tencent Cloud Simple Email Service API settings.
// TemplateIDs maps repository template names to approved Tencent Cloud IDs.
type SESConfig struct {
	NotificationsEnabled bool
	NotificationFrom     string
	NotificationFromName string
	Region               string
	SecretID             string
	SecretKey            string
	From                 string
	FromName             string
	ReplyTo              string
	TemplateIDs          map[string]uint64
}

func (c SESConfig) HasSettings() bool {
	return strings.TrimSpace(c.SecretID) != "" || strings.TrimSpace(c.SecretKey) != "" ||
		strings.TrimSpace(c.From) != "" || len(c.TemplateIDs) > 0
}

func (c SESConfig) Validate() error {
	var missing []string
	if strings.TrimSpace(c.SecretID) == "" {
		missing = append(missing, "SecretId")
	}
	if strings.TrimSpace(c.SecretKey) == "" {
		missing = append(missing, "SecretKey")
	}
	if strings.TrimSpace(c.From) == "" {
		missing = append(missing, "发信地址")
	}
	for _, name := range opsconfig.RequiredMailTemplates {
		if c.TemplateIDs[name] == 0 {
			missing = append(missing, "模板ID:"+name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("腾讯云 SES API 配置不完整，缺少 %s", strings.Join(missing, "、"))
	}

	region := strings.ToLower(strings.TrimSpace(c.Region))
	if _, ok := supportedSESRegions[region]; !ok {
		return errors.New("腾讯云 SES 地域只能是 ap-guangzhou 或 ap-hongkong")
	}
	if err := validatePlainEmail("发信地址", c.From); err != nil {
		return err
	}
	if replyTo := strings.TrimSpace(c.ReplyTo); replyTo != "" {
		if err := validatePlainEmail("回复地址", replyTo); err != nil {
			return err
		}
	}
	if strings.ContainsAny(c.FromName, ":<>\r\n") {
		return errors.New("发件人显示名不能包含冒号、尖括号或换行")
	}
	return nil
}

func validatePlainEmail(label, value string) error {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "<>\r\n") {
		return fmt.Errorf("%s必须是纯邮箱地址，显示名请单独填写", label)
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || !strings.EqualFold(parsed.Address, value) {
		return fmt.Errorf("%s不是合法邮箱地址", label)
	}
	return nil
}

type sesAPI interface {
	SendEmailWithContext(context.Context, *ses.SendEmailRequest) (*ses.SendEmailResponse, error)
}

type SESMailer struct {
	cfg SESConfig
	api sesAPI
}

func NewSESMailer(cfg SESConfig) (*SESMailer, error) {
	cfg = normalizeSESConfig(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = tencentSESEndpoint
	client, err := ses.NewClient(common.NewCredential(cfg.SecretID, cfg.SecretKey), cfg.Region, clientProfile)
	if err != nil {
		return nil, fmt.Errorf("创建腾讯云 SES 客户端: %w", err)
	}
	return &SESMailer{cfg: cfg, api: client}, nil
}

func newSESMailerWithAPI(cfg SESConfig, api sesAPI) (*SESMailer, error) {
	cfg = normalizeSESConfig(cfg)
	if api == nil {
		return nil, errors.New("腾讯云 SES 客户端不能为空")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &SESMailer{cfg: cfg, api: api}, nil
}

func normalizeSESConfig(cfg SESConfig) SESConfig {
	cfg.Region = strings.ToLower(strings.TrimSpace(cfg.Region))
	if cfg.Region == "" {
		cfg.Region = "ap-guangzhou"
	}
	cfg.SecretID = strings.TrimSpace(cfg.SecretID)
	cfg.SecretKey = strings.TrimSpace(cfg.SecretKey)
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.FromName = strings.TrimSpace(cfg.FromName)
	cfg.ReplyTo = strings.TrimSpace(cfg.ReplyTo)
	if cfg.TemplateIDs == nil {
		cfg.TemplateIDs = make(map[string]uint64)
	}
	return cfg
}

func (m *SESMailer) Send(ctx context.Context, msg Message) (string, error) {
	if m == nil || m.api == nil {
		return "", &NotSubmittedError{Err: errors.New("腾讯云 SES 客户端未初始化")}
	}
	if strings.TrimSpace(msg.Template) == "" {
		return "", &NotSubmittedError{Err: errors.New("腾讯云 SES API 仅允许使用已审核模板发送")}
	}
	// Every old business template is retired from the sending path. Neither
	// a stale worker event nor an accidental caller can bypass the new queue.
	business := isBusinessMail(msg)
	if business {
		if err := m.cfg.NotificationReady(); err != nil {
			return "", &NotSubmittedError{Err: err}
		}
		if msg.Template != TemplateNotificationAlert && msg.Template != TemplateNotificationDigest {
			return "", &NotSubmittedError{Err: errors.New("旧业务邮件模板已停用")}
		}
	}
	templateID := m.cfg.TemplateIDs[msg.Template]
	if templateID == 0 {
		return "", &NotSubmittedError{Err: fmt.Errorf("邮件模板 %q 未绑定腾讯云模板 ID", msg.Template)}
	}
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return "", &NotSubmittedError{Err: errors.New("邮件主题不能包含换行")}
	}
	recipient, err := mail.ParseAddress(strings.TrimSpace(msg.To))
	if err != nil || strings.TrimSpace(recipient.Address) == "" {
		return "", &NotSubmittedError{Err: errors.New("收件人邮箱地址不合法")}
	}
	templateData, err := encodeSESTemplateData(msg.Data)
	if err != nil {
		return "", &NotSubmittedError{Err: err}
	}

	request := ses.NewSendEmailRequest()
	from := m.cfg
	if business {
		from.From, from.FromName = m.cfg.NotificationFrom, m.cfg.NotificationFromName
	}
	if from.FromName == "" {
		from.FromName = "EasyGPA Plus"
	}
	request.FromEmailAddress = common.StringPtr(formatSESFrom(from))
	request.Subject = common.StringPtr(msg.Subject)
	request.Destination = []*string{common.StringPtr(recipient.Address)}
	if m.cfg.ReplyTo != "" {
		request.ReplyToAddresses = common.StringPtr(m.cfg.ReplyTo)
	}
	request.Template = &ses.Template{
		TemplateID:   common.Uint64Ptr(templateID),
		TemplateData: common.StringPtr(templateData),
	}
	request.Unsubscribe = common.StringPtr("0")
	request.TriggerType = common.Uint64Ptr(1)
	if business {
		request.Unsubscribe = common.StringPtr("1")
	}
	if msg.Template == TemplateNotificationDigest {
		request.TriggerType = common.Uint64Ptr(0)
	}

	response, err := m.api.SendEmailWithContext(ctx, request)
	if err != nil {
		return "", fmt.Errorf("腾讯云 SES SendEmail: %w", err)
	}
	if response == nil || response.Response == nil || response.Response.MessageId == nil || strings.TrimSpace(*response.Response.MessageId) == "" {
		return "", errors.New("腾讯云 SES SendEmail 未返回 MessageId")
	}
	return *response.Response.MessageId, nil
}

func isBusinessMail(msg Message) bool {
	return msg.EventID != "" || (msg.Template != TemplateVerificationCode && msg.Template != TemplatePasswordReset && msg.Template != TemplateMailTest)
}

func (c SESConfig) NotificationReady() error {
	if !c.NotificationsEnabled {
		return errors.New("业务邮件已暂停")
	}
	if err := validatePlainEmail("通知发信地址", c.NotificationFrom); err != nil {
		return err
	}
	if strings.EqualFold(strings.Split(c.NotificationFrom, "@")[1], strings.Split(c.From, "@")[len(strings.Split(c.From, "@"))-1]) {
		return errors.New("通知邮件必须使用独立发件域名")
	}
	if strings.ContainsAny(c.NotificationFromName, ":<>\r\n") {
		return errors.New("通知发件人显示名格式不正确")
	}
	for _, name := range opsconfig.NotificationMailTemplates {
		if c.TemplateIDs[name] == 0 {
			return fmt.Errorf("业务邮件仍待配置模板 ID：%s", name)
		}
	}
	return nil
}

func (m *SESMailer) Ready(context.Context) error {
	if m == nil {
		return errors.New("腾讯云 SES 客户端未初始化")
	}
	return m.cfg.Validate()
}

func formatSESFrom(cfg SESConfig) string {
	if cfg.FromName == "" {
		return cfg.From
	}
	return cfg.FromName + " <" + cfg.From + ">"
}

func encodeSESTemplateData(data map[string]any) (string, error) {
	values := make(map[string]string, len(data))
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			return "", errors.New("腾讯云模板变量名不能为空")
		}
		value, err := scalarTemplateValue(data[key])
		if err != nil {
			return "", fmt.Errorf("腾讯云模板变量 %q: %w", key, err)
		}
		values[key] = value
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("编码腾讯云模板变量: %w", err)
	}
	return string(raw), nil
}

func scalarTemplateValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	case int8:
		return strconv.FormatInt(int64(typed), 10), nil
	case int16:
		return strconv.FormatInt(int64(typed), 10), nil
	case int32:
		return strconv.FormatInt(int64(typed), 10), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case uint:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint64:
		return strconv.FormatUint(typed, 10), nil
	case float32:
		return scalarFloat(float64(typed), 32)
	case float64:
		return scalarFloat(typed, 64)
	case json.Number:
		if _, err := typed.Float64(); err != nil {
			return "", errors.New("不是合法数字")
		}
		return typed.String(), nil
	default:
		return "", fmt.Errorf("只支持字符串、数字、布尔值或 null，不能传 %T", value)
	}
}

func scalarFloat(value float64, bitSize int) (string, error) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return "", errors.New("不是有限数字")
	}
	return strconv.FormatFloat(value, 'f', -1, bitSize), nil
}
