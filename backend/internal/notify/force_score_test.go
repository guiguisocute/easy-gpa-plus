package notify

import (
	"easygpa/backend/internal/events"
	"encoding/json"
	"strings"
	"testing"
)

func TestForcedScoreSharesRulingTemplateAndDecisionPreferences(t *testing.T) {
	before, after := 4.0, 7.125
	raw, _ := json.Marshal(events.SubmissionForceRejectedPayload{StudentID: 2, SubmissionID: 1, Title: "合成材料", PreviousScore: &before, Score: &after, Reason: "核对原件后订正"})
	event := events.Event{Type: events.SubmissionForceScored, Payload: raw}
	template, data, err := notificationTemplate(event, "测试班", "测试成员")
	if err != nil || template != TemplateRulingNotice || data["score"] != "7.125" {
		t.Fatalf("%s %v %v", template, data, err)
	}
	if !strings.Contains(data["ruling_message"].(string), "4 调整为 7.125 分") || !strings.Contains(data["ruling_heading"].(string), "强制改分") {
		t.Fatalf("wrong correction: %v", data)
	}
	if !notifiable(event.Type) || notificationCategory(event, 2) != "decisions" {
		t.Fatal("missing decision notification")
	}
	subject, _ := notificationText(event, "测试班", "测试成员")
	if !strings.Contains(subject, "强制改分") || strings.Contains(subject, "归零") {
		t.Fatal(subject)
	}
}
