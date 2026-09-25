package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"easygpa/backend/internal/opsconfig"
	sdkerrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"

	ses "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ses/v20201002"
)

func TestSESFrequencyLimitClassificationUsesTypedCode(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"frequency", sdkerrors.NewTencentCloudSDKError("FailedOperation.FrequencyLimit", "limited", "request"), true},
		{"wrapped", fmt.Errorf("send: %w", sdkerrors.NewTencentCloudSDKError("FailedOperation.FrequencyLimit", "limited", "request")), true},
		{"text only", errors.New("FailedOperation.FrequencyLimit"), false},
		{"other code", sdkerrors.NewTencentCloudSDKError("FailedOperation.TemplateNotApproved", "rejected", "request"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isSESFrequencyLimit(test.err); got != test.want {
				t.Fatalf("isSESFrequencyLimit=%v, want %v", got, test.want)
			}
		})
	}
}

type recordingSESAPI struct {
	request       *ses.SendEmailRequest
	err           error
	omitMessageID bool
}

func (r *recordingSESAPI) SendEmailWithContext(_ context.Context, request *ses.SendEmailRequest) (*ses.SendEmailResponse, error) {
	r.request = request
	if r.err != nil {
		return nil, r.err
	}
	if r.omitMessageID {
		return &ses.SendEmailResponse{}, nil
	}
	messageID := "qcloud-message-123"
	return &ses.SendEmailResponse{Response: &ses.SendEmailResponseParams{MessageId: &messageID}}, nil
}

func TestSESMailerDistinguishesLocalRejectionFromSubmittedOutcome(t *testing.T) {
	for _, test := range []struct {
		name             string
		apiErr           error
		omitID           bool
		invalidRecipient bool
		notSubmitted     bool
	}{
		{name: "validation", invalidRecipient: true, notSubmitted: true},
		{name: "timeout", apiErr: context.DeadlineExceeded},
		{name: "provider rejection", apiErr: sdkerrors.NewTencentCloudSDKError("FailedOperation.FrequencyLimit", "limited", "request")},
		{name: "missing message id", omitID: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &recordingSESAPI{err: test.apiErr, omitMessageID: test.omitID}
			mailer, err := newSESMailerWithAPI(completeSESConfig(), api)
			if err != nil {
				t.Fatal(err)
			}
			msg := Message{To: "student@example.test", Template: TemplateMailTest}
			if test.invalidRecipient {
				msg.To = "invalid"
			}
			_, err = mailer.Send(t.Context(), msg)
			var notSubmitted *NotSubmittedError
			if err == nil || errors.As(err, &notSubmitted) != test.notSubmitted {
				t.Fatalf("submission classification mismatch: %v", err)
			}
			if (api.request == nil) != test.notSubmitted {
				t.Fatal("submission classification did not match provider call")
			}
		})
	}
}

func completeSESConfig() SESConfig {
	ids := make(map[string]uint64, len(opsconfig.RequiredMailTemplates))
	for index, name := range opsconfig.RequiredMailTemplates {
		ids[name] = uint64(1000 + index)
	}
	return SESConfig{
		Region: "ap-guangzhou", SecretID: "secret-id", SecretKey: "secret-key",
		From: "notice@gpa.example.org", FromName: "EasyGPA Plus 综测平台", ReplyTo: "support@example.org",
		TemplateIDs: ids,
	}
}

func TestSESMailerUsesApprovedTemplateAndScalarData(t *testing.T) {
	api := &recordingSESAPI{}
	mailer, err := newSESMailerWithAPI(completeSESConfig(), api)
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := mailer.Send(context.Background(), Message{
		To: "Student <student@example.edu>", Subject: "[综测] 绑定邮箱验证码",
		Template: TemplateVerificationCode,
		Data:     map[string]any{"name": "张三", "code": 123456, "active": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "qcloud-message-123" || api.request == nil || api.request.Template == nil {
		t.Fatalf("messageID/request = %q %#v", messageID, api.request)
	}
	if got := *api.request.FromEmailAddress; got != "EasyGPA Plus 综测平台 <notice@gpa.example.org>" {
		t.Fatalf("FromEmailAddress = %q", got)
	}
	if got := *api.request.Destination[0]; got != "student@example.edu" {
		t.Fatalf("Destination = %q", got)
	}
	if got, want := *api.request.Template.TemplateID, completeSESConfig().TemplateIDs[TemplateVerificationCode]; got != want {
		t.Fatalf("TemplateID = %d, want %d", got, want)
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(*api.request.Template.TemplateData), &data); err != nil {
		t.Fatal(err)
	}
	if data["code"] != "123456" || data["active"] != "true" || *api.request.TriggerType != 1 {
		t.Fatalf("TemplateData/TriggerType = %#v/%d", data, *api.request.TriggerType)
	}
}

func TestSESConfigRequiresAuthenticationButNotRetiredTemplateIDs(t *testing.T) {
	cfg := completeSESConfig()
	delete(cfg.TemplateIDs, TemplateFinalReviewReady)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	delete(cfg.TemplateIDs, TemplateVerificationCode)
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), TemplateVerificationCode) {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSESMailerRejectsComplexTemplateData(t *testing.T) {
	mailer, err := newSESMailerWithAPI(completeSESConfig(), &recordingSESAPI{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mailer.Send(context.Background(), Message{
		To: "student@example.edu", Subject: "test", Template: TemplateMailTest,
		Data: map[string]any{"nested": map[string]any{"html": "<b>bad</b>"}},
	})
	if err == nil || !strings.Contains(err.Error(), "只支持字符串") {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestSESConfigRejectsDisplayNameSyntaxInSeparateField(t *testing.T) {
	cfg := completeSESConfig()
	cfg.FromName = "EasyGPA Plus: notice"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "显示名") {
		t.Fatalf("Validate() error = %v", err)
	}
}
