package events

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestWaitStreamRetryBacksOffAndHonorsCancel(t *testing.T) {
	wait := time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitStreamRetry(ctx, &wait); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait err=%v", err)
	}
	open := context.Background()
	wait = 2 * time.Millisecond
	if err := waitStreamRetry(open, &wait); err != nil {
		t.Fatal(err)
	}
	if wait != 4*time.Millisecond {
		t.Fatalf("backoff wait=%s", wait)
	}
}

func TestTypedEventPayloadRoundTrip(t *testing.T) {
	raw, err := json.Marshal(SubmissionCreatedPayload{SubmissionID: 7, StudentID: 9, CategoryKey: "practice"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := Decode(Event{Type: SubmissionCreated, Payload: raw}, SubmissionCreatedEvent())
	if err != nil {
		t.Fatalf("decode typed payload: %v", err)
	}
	if payload.SubmissionID != 7 || payload.StudentID != 9 || payload.CategoryKey != "practice" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestTypedEventRejectsMismatchedKind(t *testing.T) {
	_, err := Decode(Event{Type: AIBatchCreated, Payload: json.RawMessage(`{"batchId":"7"}`)}, SubmissionCreatedEvent())
	if err == nil {
		t.Fatal("expected mismatched event kind to fail")
	}
}

func TestAppealAndObjectionPayloadRoundTrips(t *testing.T) {
	score := 8.75
	category := "practice"
	itemKey := "competition"
	assertTypedRoundTrip(t, AppealFiledEvent(), AppealFiledPayload{
		AppealID: 11, TargetType: "submission", TargetID: 21,
	})
	assertTypedRoundTrip(t, AppealAssignedEvent(), AppealAssignedPayload{
		AppealID: 11, HandlerID: 31, Round: 2,
	})
	assertTypedRoundTrip(t, AppealRereviewedEvent(), AppealRereviewedPayload{
		AppealID: 11, Status: "reviewing", ReviewerID: 31,
	})
	assertTypedRoundTrip(t, AppealEscalatedEvent(), AppealEscalatedPayload{
		AppealID: 11, Status: "escalated",
	})
	assertTypedRoundTrip(t, AppealResolvedEvent(), AppealResolvedPayload{
		AppealID: 11, Status: "resolved", Score: &score, Category: &category, ItemKey: &itemKey,
	})
	assertTypedRoundTrip(t, ObjectionSubmittedEvent(), ObjectionSubmittedPayload{
		BatchID: "batch-1", IDs: []int64{41, 42}, ProposerID: 51, Source: "blind_audit",
	})
	assertTypedRoundTrip(t, ObjectionDecidedEvent(), ObjectionDecidedPayload{
		ObjectionID: 41, Status: "adjusted", Score: &score, StudentID: 61, ProposerID: 51,
	})
}

func TestAppealAndObjectionPayloadCompatibility(t *testing.T) {
	resolved, err := Decode(Event{
		Type: AppealResolved, Payload: json.RawMessage(`{"appealId":11,"status":"resolved"}`),
	}, AppealResolvedEvent())
	if err != nil {
		t.Fatalf("decode appeal without optional outcome: %v", err)
	}
	if resolved.Score != nil || resolved.Category != nil || resolved.ItemKey != nil {
		t.Fatalf("optional outcome fields should remain nil: %+v", resolved)
	}

	submitted, err := Decode(Event{
		Type: ObjectionSubmitted, Payload: json.RawMessage(`{"batchId":"batch-1","ids":[41],"proposerId":51}`),
	}, ObjectionSubmittedEvent())
	if err != nil {
		t.Fatalf("decode objection without optional source: %v", err)
	}
	if submitted.Source != "" {
		t.Fatalf("optional source = %q, want empty", submitted.Source)
	}
}

func TestAppealAndObjectionPayloadRejectsWrongFieldType(t *testing.T) {
	_, err := Decode(Event{
		Type: AppealAssigned, Payload: json.RawMessage(`{"appealId":11,"handlerId":"not-a-number","round":2}`),
	}, AppealAssignedEvent())
	if err == nil {
		t.Fatal("expected invalid handlerId type to fail")
	}
}

func TestDispatchReviewAndArbitrationPayloadRoundTrips(t *testing.T) {
	assertTypedRoundTrip(t, DispatchDoneEvent(), DispatchDonePayload{
		RunID: "71", Trigger: "manual", Assigned: 8, Blocked: 1, ReviewerIDs: []string{"81", "82"},
	})
	assertTypedRoundTrip(t, DispatchReassignedEvent(), DispatchReassignedPayload{
		SubmissionID: "91", From: "81", To: "82", Reason: "原审核人请假",
	})
	assertTypedRoundTrip(t, ReviewDecidedEvent(), ReviewDecidedPayload{
		SubmissionID: 91, ReviewID: 101, Status: "consensus",
	})
	assertTypedRoundTrip(t, ConflictRaisedEvent(), ConflictRaisedPayload{
		SubmissionID: 91, ReviewID: 102, Status: "arbitrating",
	})
	assertTypedRoundTrip(t, ArbitrationResolvedEvent(),
		NewAppealArbitrationResolvedPayload(111, "submission", 91, 8.5, "practice", "competition"))
	assertTypedRoundTrip(t, ArbitrationResolvedEvent(),
		NewSubmissionArbitrationResolvedPayload(91, 121, 8.5, "practice", "competition", "依据复核结果裁定"))
}

func TestDispatchDonePreservesStringIDWireFormat(t *testing.T) {
	raw, err := json.Marshal(DispatchDonePayload{
		RunID: "71", Trigger: "auto", Assigned: 1, ReviewerIDs: []string{"9007199254740993"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["runId"].(string); !ok {
		t.Fatalf("runId wire type = %T, want string", wire["runId"])
	}
	reviewerIDs, ok := wire["reviewerIds"].([]any)
	if !ok || len(reviewerIDs) != 1 {
		t.Fatalf("reviewerIds wire value = %#v", wire["reviewerIds"])
	}
	if _, ok := reviewerIDs[0].(string); !ok {
		t.Fatalf("reviewerIds[0] wire type = %T, want string", reviewerIDs[0])
	}
}

func TestArbitrationResolvedPreservesBothLegacyShapes(t *testing.T) {
	appealRaw, err := json.Marshal(NewAppealArbitrationResolvedPayload(11, "submission", 21, 8.5, "practice", "competition"))
	if err != nil {
		t.Fatal(err)
	}
	var appeal map[string]any
	if err := json.Unmarshal(appealRaw, &appeal); err != nil {
		t.Fatal(err)
	}
	if _, ok := appeal["appealId"]; !ok || appeal["submissionId"] != nil || appeal["studentId"] != nil || appeal["reason"] != nil {
		t.Fatalf("unexpected appeal arbitration wire payload: %#v", appeal)
	}

	submissionRaw, err := json.Marshal(NewSubmissionArbitrationResolvedPayload(21, 31, 8.5, "practice", "competition", "裁定理由"))
	if err != nil {
		t.Fatal(err)
	}
	var submission map[string]any
	if err := json.Unmarshal(submissionRaw, &submission); err != nil {
		t.Fatal(err)
	}
	if _, ok := submission["submissionId"]; !ok || submission["appealId"] != nil || submission["targetId"] != nil || submission["targetType"] != nil {
		t.Fatalf("unexpected submission arbitration wire payload: %#v", submission)
	}
}

func TestDispatchPayloadRejectsNumericLegacyID(t *testing.T) {
	_, err := Decode(Event{
		Type: DispatchDone, Payload: json.RawMessage(`{"runId":"71","trigger":"auto","assigned":1,"blocked":0,"reviewerIds":[81]}`),
	}, DispatchDoneEvent())
	if err == nil {
		t.Fatal("expected numeric reviewer ID to fail the string-ID contract")
	}
}

func TestReportPayloadRoundTripsAndUsesAnonymityWhitelist(t *testing.T) {
	filed := ReportFiledPayload{ReportID: 131, StudentID: 141}
	reviewed := ReportReviewedPayload{ReportID: 131, Status: "reviewing"}
	escalated := ReportEscalatedPayload{ReportID: 131, Status: "escalated", StudentID: 141}
	decided := ReportDecidedPayload{ReportID: 131, Status: "dismissed", StudentID: 141}

	assertTypedRoundTrip(t, ReportFiledEvent(), filed)
	assertTypedRoundTrip(t, ReportReviewedEvent(), reviewed)
	assertTypedRoundTrip(t, ReportEscalatedEvent(), escalated)
	assertTypedRoundTrip(t, ReportDecidedEvent(), decided)

	assertJSONFields(t, filed, "reportId", "studentId")
	assertJSONFields(t, reviewed, "reportId", "status")
	assertJSONFields(t, escalated, "reportId", "status", "studentId")
	assertJSONFields(t, decided, "reportId", "status", "studentId")
}

func TestReportPayloadRejectsStringReportID(t *testing.T) {
	_, err := Decode(Event{
		Type: ReportFiled, Payload: json.RawMessage(`{"reportId":"131","studentId":141}`),
	}, ReportFiledEvent())
	if err == nil {
		t.Fatal("expected string reportId to fail the numeric report contract")
	}
}

func assertTypedRoundTrip[T any](t *testing.T, kind Kind[T], want T) {
	t.Helper()
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", kind.Name(), err)
	}
	got, err := Decode(Event{Type: kind.Name(), Payload: raw}, kind)
	if err != nil {
		t.Fatalf("decode %s payload: %v", kind.Name(), err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s payload = %#v, want %#v", kind.Name(), got, want)
	}
}

func assertJSONFields[T any](t *testing.T, payload T, want ...string) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != len(want) {
		t.Fatalf("JSON fields = %v, want exactly %v", fields, want)
	}
	for _, field := range want {
		if _, ok := fields[field]; !ok {
			t.Fatalf("JSON fields = %v, missing %q", fields, field)
		}
	}
}

func TestDecodeMessageAllowsOnlyPlatformKnowledgeWithoutClass(t *testing.T) {
	platform := redis.XMessage{Values: map[string]any{
		"event_id": "evt-1", "class_id": "0", "type": PlatformKnowledgeDocumentCreated, "payload": `{"documentId":"7"}`,
	}}
	event, err := decodeMessage(platform)
	if err != nil || event.ClassID != 0 || event.Type != PlatformKnowledgeDocumentCreated {
		t.Fatalf("event = %#v, err = %v", event, err)
	}

	classEvent := redis.XMessage{Values: map[string]any{
		"event_id": "evt-2", "class_id": "0", "type": SubmissionCreated, "payload": `{}`,
	}}
	if _, err := decodeMessage(classEvent); err == nil {
		t.Fatal("class-scoped event accepted an empty class_id")
	}
}

func TestDecodeMessageStillRequiresValidClassForOtherEvents(t *testing.T) {
	message := redis.XMessage{Values: map[string]any{
		"event_id": "evt-3", "class_id": "42", "type": SubmissionCreated, "payload": `{}`,
	}}
	event, err := decodeMessage(message)
	if err != nil || event.ClassID != 42 {
		t.Fatalf("event = %#v, err = %v", event, err)
	}
}
