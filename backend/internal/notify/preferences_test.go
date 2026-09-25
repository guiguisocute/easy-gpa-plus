package notify

import (
	"context"
	"testing"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	cloudErrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	ses "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ses/v20201002"
)

func TestPreferenceScheduleAndValidation(t *testing.T) {
	p := DefaultPreferences()
	for _, tt := range []struct {
		at   string
		mode MailMode
		want string
	}{
		{"2026-09-06T21:55:00+08:00", MailImmediate, "2026-09-07T08:00:00+08:00"},
		{"2026-09-06T02:00:00+08:00", MailImmediate, "2026-09-06T08:00:00+08:00"},
		{"2026-09-06T18:31:00+08:00", MailDigest, "2026-09-07T18:30:00+08:00"},
		{"2026-09-06T11:00:00+08:00", MailDigest, "2026-09-06T18:30:00+08:00"},
		{"2026-09-06T11:00:00+08:00", MailFrequent, "2026-09-06T11:00:00+08:00"},
		{"2026-09-06T23:00:00+08:00", MailFrequent, "2026-09-07T08:00:00+08:00"},
	} {
		at, _ := time.Parse(time.RFC3339, tt.at)
		if got := p.Due(at, tt.mode).Format(time.RFC3339); got != tt.want {
			t.Fatalf("due=%s want=%s", got, tt.want)
		}
	}
	if p.Categories["receipts"] != MailOff || p.Categories["progress"] != MailOff {
		t.Fatal("no-action events default to mail")
	}
	p.Categories["unknown"] = MailImmediate
	if p.Validate() == nil {
		t.Fatal("unknown category accepted")
	}
	delete(p.Categories, "unknown")
	if !p.Enabled || p.DailyLimit != 3 {
		t.Fatal("default is not light, enabled delivery")
	}
	for _, mode := range p.Categories {
		if mode == MailFrequent {
			t.Fatal("frequent mail must require an explicit choice")
		}
	}
	p.Categories["receipts"] = MailFrequent
	p.DailyLimit = 50
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.DailyLimit = 51
	if p.Validate() == nil {
		t.Fatal("unsafe daily cap accepted")
	}
}

func TestBusinessSESUsesIndependentDomainAndUnsubscribe(t *testing.T) {
	cfg := completeSESConfig()
	cfg.NotificationsEnabled = true
	cfg.NotificationFrom = "notice@notify.example.invalid"
	cfg.NotificationFromName = "EasyGPA Plus 提醒"
	cfg.TemplateIDs[TemplateNotificationAlert] = 10001
	cfg.TemplateIDs[TemplateNotificationDigest] = 10002
	api := &recordingSESAPI{}
	mailer := &SESMailer{cfg: cfg, api: api}
	msg := Message{Template: TemplateNotificationDigest, To: "fixture@example.invalid", Subject: "每日汇总"}
	if _, err := mailer.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if *api.request.FromEmailAddress != "EasyGPA Plus 提醒 <notice@notify.example.invalid>" || *api.request.Unsubscribe != "1" || *api.request.TriggerType != 0 {
		t.Fatal("business mail routing/opt-out missing")
	}
	mailer.cfg.NotificationsEnabled = false
	api.request = nil
	if _, err := mailer.Send(context.Background(), msg); err == nil || api.request != nil {
		t.Fatal("paused business mail submitted")
	}
	msg.Template = TemplateVerificationCode
	if _, err := mailer.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if *api.request.Unsubscribe != "0" || *api.request.FromEmailAddress == "EasyGPA Plus 提醒 <notice@notify.example.invalid>" {
		t.Fatal("authentication used notification settings")
	}
	mailer.cfg.NotificationsEnabled = true
	msg.Template = TemplateReviewDecided
	api.request = nil
	if _, err := mailer.Send(context.Background(), msg); err == nil || api.request != nil {
		t.Fatal("legacy business template still sends")
	}
}

func TestSESFeedbackOnlyExplicitPermanentSignalsSuppress(t *testing.T) {
	// An address error without an explicit recipient scope may concern the
	// sender configuration. It must not permanently disable a valid account.
	if scope, _ := permanentSESFailure(cloudErrors.NewTencentCloudSDKError("InvalidParameterValue.EmailAddress", "", "")); scope != "" {
		t.Fatal("ambiguous address error suppressed recipient")
	}
	for _, tt := range []struct {
		s             *ses.SendEmailStatus
		scope, reason string
	}{
		{&ses.SendEmailStatus{UserUnsubscribed: common.BoolPtr(true)}, "business", "provider_unsubscribed"},
		{&ses.SendEmailStatus{UserComplained: common.BoolPtr(true)}, "business", "provider_complaint"},
		{&ses.SendEmailStatus{DeliverStatus: common.Int64Ptr(8)}, "", ""},
		{&ses.SendEmailStatus{DeliverStatus: common.Int64Ptr(3)}, "", ""},
		{&ses.SendEmailStatus{DeliverStatus: common.Int64Ptr(3), DeliverMessage: common.StringPtr("550 5.1.1 invalid")}, "all", "invalid_address"},
		{&ses.SendEmailStatus{UserClicked: common.BoolPtr(true), UserOpened: common.BoolPtr(true)}, "", ""},
	} {
		_, scope, reason := classifySESStatus(tt.s)
		if scope != tt.scope || reason != tt.reason {
			t.Fatalf("classification=%s/%s", scope, reason)
		}
	}
}
