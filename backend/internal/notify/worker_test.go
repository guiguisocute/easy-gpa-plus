package notify

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"easygpa/backend/internal/events"
)

func TestForceRejectedMailUsesApprovedRulingTemplateAndExplainsZeroScore(t *testing.T) {
	before := 4.0
	payload, err := json.Marshal(events.SubmissionForceRejectedPayload{
		SubmissionID: 91, StudentID: 121, Title: "比赛材料", PreviousScore: &before, Reason: "证书不符合申报条件",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := events.Event{Type: events.SubmissionForceRejected, Payload: payload}
	template, data, err := notificationTemplate(event, "测试班级", "测试学生")
	if err != nil || template != TemplateRulingNotice || data["score"] != "0" || data["title"] != "比赛材料" {
		t.Fatalf("template=%q data=%v err=%v", template, data, err)
	}
	if !strings.Contains(data["ruling_message"].(string), "4 调整为 0 分") || !strings.Contains(data["reason"].(string), "证书不符合申报条件") {
		t.Fatalf("forced rejection mail lacks correction details: %v", data)
	}
	if !notifiable(event.Type) {
		t.Fatal("forced rejection event must be delivered")
	}
	subject, _ := notificationText(event, "测试班级", "测试学生")
	if !strings.Contains(subject, "强制驳回") {
		t.Fatalf("mail subject does not identify forced rejection: %s", subject)
	}
}

func TestPayloadIDsPreserveInt64Precision(t *testing.T) {
	const largeID int64 = 9_007_199_254_740_993
	decoder := json.NewDecoder(bytes.NewBufferString(`{"submissionId":9007199254740993,"studentIds":[9007199254740993,"42"]}`))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if got := payloadID(payload, "submissionId"); got != largeID {
		t.Fatalf("payloadID() = %d, want %d", got, largeID)
	}
	ids := payloadIDs(payload, "studentIds")
	if len(ids) != 2 || ids[0] != largeID || ids[1] != 42 {
		t.Fatalf("payloadIDs() = %v", ids)
	}
}

func TestSelfForceRejectedNotificationAttributesActionToOwner(t *testing.T) {
	before := 4.0
	payload, err := json.Marshal(events.SubmissionForceRejectedPayload{
		SubmissionID: 91, StudentID: 121, Title: "交错材料", PreviousScore: &before, Reason: "本人发现传错证书", SelfRejected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	event := events.Event{Type: events.SubmissionForceRejected, Payload: payload}
	_, data, err := notificationTemplate(event, "测试班级", "测试学生")
	if err != nil {
		t.Fatal(err)
	}
	subject, body := notificationText(event, "测试班级", "测试学生")
	for _, text := range []string{subject, body, data["ruling_heading"].(string), data["ruling_message"].(string)} {
		if strings.Contains(text, "管理员") || !strings.Contains(text, "主动") {
			t.Fatalf("self rejection attributed incorrectly: %s", text)
		}
	}
	if !strings.Contains(data["ruling_message"].(string), "4 调整为 0 分") {
		t.Fatalf("missing score change: %v", data)
	}
}

func TestDispatchDigestWindowEndsAtNext0830Shanghai(t *testing.T) {
	for _, test := range []struct {
		name    string
		created string
		wantEnd string
	}{
		{name: "before cutoff", created: "2026-08-08T08:29:00+08:00", wantEnd: "2026-08-08T08:30:00+08:00"},
		{name: "at cutoff", created: "2026-08-08T08:30:00+08:00", wantEnd: "2026-08-09T08:30:00+08:00"},
		{name: "after cutoff", created: "2026-08-08T19:15:00+08:00", wantEnd: "2026-08-09T08:30:00+08:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			created, err := time.Parse(time.RFC3339, test.created)
			if err != nil {
				t.Fatal(err)
			}
			wantEnd, err := time.Parse(time.RFC3339, test.wantEnd)
			if err != nil {
				t.Fatal(err)
			}
			start, end := dispatchDigestWindowAt(created)
			if !end.Equal(wantEnd) || end.Sub(start) != 24*time.Hour {
				t.Fatalf("window = %s..%s, want 24h ending %s", start, end, wantEnd)
			}
		})
	}
}

func TestNumericIDRejectsFractionalValues(t *testing.T) {
	if got := numericID(12.5); got != 0 {
		t.Fatalf("numericID(12.5) = %d, want 0", got)
	}
}

func TestMarkdownMailSummaryUsesPlaintextAndLoginHint(t *testing.T) {
	got := markdownMailSummary("**同意**，见 ![](evidence:9)", 180)
	want := "同意，见 [附件]\n\n完整内容（含附图）请登录平台查看。"
	if got != want {
		t.Fatalf("markdownMailSummary() = %q, want %q", got, want)
	}
}

func TestNotifiableEventsUseApprovedTemplatesOrExplicitSuppression(t *testing.T) {
	for eventType, label := range notificationLabels {
		templateName, data, err := notificationTemplate(events.Event{
			Type:    eventType,
			Payload: json.RawMessage(`{"score":3}`),
		}, "测试班级", "测试用户")
		if err != nil {
			t.Errorf("notificationTemplate(%q): %v", eventType, err)
			continue
		}
		if _, suppressed := mailSuppressedEvents[eventType]; suppressed {
			if templateName != "" || data != nil {
				t.Errorf("mail-suppressed event %q (%s) unexpectedly uses template %q", eventType, label, templateName)
			}
			continue
		}
		if templateName == "" {
			t.Errorf("notifiable event %q (%s) has no HTML template", eventType, label)
			continue
		}
		if data == nil {
			t.Errorf("notifiable event %q (%s) has no template data", eventType, label)
		}
	}
}

func TestEverySuppressedMailEventRemainsAnInAppNotification(t *testing.T) {
	for eventType := range mailSuppressedEvents {
		if notificationLabels[eventType] == "" {
			t.Errorf("mail-suppressed event %q has no in-app notification label", eventType)
		}
	}
}

func TestReportEventsRemainOutsideNotificationBroadcasts(t *testing.T) {
	for _, eventType := range []string{
		events.ReportFiled, events.ReportReviewed, events.ReportEscalated, events.ReportDecided,
	} {
		if notifiable(eventType) {
			t.Errorf("anonymous report event %q must not enter notification broadcasts", eventType)
		}
	}
}

func TestInitialAndFinalReviewUseDifferentTemplates(t *testing.T) {
	initial, _, initialErr := notificationTemplate(events.Event{Type: events.ReviewDecided, Payload: json.RawMessage(`{"submissionId":91,"reviewId":101,"status":"scored"}`)}, "测试班级", "测试用户")
	final, _, finalErr := notificationTemplate(events.Event{Type: events.ScorecardAuditCompleted, Payload: json.RawMessage(`{}`)}, "测试班级", "测试用户")
	confirmed, _, confirmedErr := notificationTemplate(events.Event{Type: events.ResultConfirmed, Payload: json.RawMessage(`{"source":"manual"}`)}, "测试班级", "测试用户")
	if initialErr != nil || finalErr != nil || confirmedErr != nil {
		t.Fatalf("template errors = initial:%v final:%v confirmed:%v", initialErr, finalErr, confirmedErr)
	}
	if initial != TemplateReviewDecided || final != TemplateFinalReviewReady || confirmed != TemplateResultConfirmed {
		t.Fatalf("templates = initial:%q final:%q confirmed:%q", initial, final, confirmed)
	}
}

func TestDispatchNotificationTemplateRejectsWrongIDWireType(t *testing.T) {
	_, _, err := notificationTemplate(events.Event{
		Type:    events.DispatchDone,
		Payload: json.RawMessage(`{"runId":"71","trigger":"auto","assigned":1,"blocked":0,"reviewerIds":[81]}`),
	}, "测试班级", "测试用户")
	if err == nil {
		t.Fatal("expected numeric reviewer ID to fail typed dispatch decoding")
	}
}

func TestArbitrationNotificationTemplatesSupportBothPayloadShapes(t *testing.T) {
	appealRaw, err := json.Marshal(events.NewAppealArbitrationResolvedPayload(11, "submission", 21, 8.5, "practice", "competition"))
	if err != nil {
		t.Fatal(err)
	}
	submissionRaw, err := json.Marshal(events.NewSubmissionArbitrationResolvedPayload(21, 31, 8.5, "practice", "competition", "裁定理由"))
	if err != nil {
		t.Fatal(err)
	}
	appealTemplate, _, appealErr := notificationTemplate(events.Event{Type: events.ArbitrationResolved, Payload: appealRaw}, "测试班级", "测试用户")
	submissionTemplate, _, submissionErr := notificationTemplate(events.Event{Type: events.ArbitrationResolved, Payload: submissionRaw}, "测试班级", "测试用户")
	if appealErr != nil || submissionErr != nil {
		t.Fatalf("arbitration template errors = appeal:%v submission:%v", appealErr, submissionErr)
	}
	if appealTemplate != TemplateAppealResolved || submissionTemplate != TemplateRulingNotice {
		t.Fatalf("arbitration templates = appeal:%q submission:%q", appealTemplate, submissionTemplate)
	}
}

func TestAppealNotificationTemplateRejectsInvalidTypedPayload(t *testing.T) {
	_, _, err := notificationTemplate(events.Event{
		Type: events.AppealAssigned, Payload: json.RawMessage(`{"appealId":11,"handlerId":"invalid","round":1}`),
	}, "测试班级", "测试用户")
	if err == nil {
		t.Fatal("expected invalid typed appeal payload to fail")
	}
}

func TestReviewStatusLabelsDoNotCallWaitingConsensus(t *testing.T) {
	if got := reviewStatusLabel("consensus"); got != "等待另一位审核人" {
		t.Fatalf("reviewStatusLabel(consensus) = %q", got)
	}
}
