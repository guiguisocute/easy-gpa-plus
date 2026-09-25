// Package events is the shared, typo-resistant event catalogue used by the API
// and workers. Payloads remain JSON so services communicate through storage.
package events

import (
	"encoding/json"
	"fmt"
)

const (
	SubmissionCreated       = "submission.created"
	SubmissionForceRejected = "submission.force_rejected"
	SubmissionForceScored   = "submission.force_scored"
	AppealFiled             = "appeal.filed"
	AppealAssigned          = "appeal.assigned"
	AppealRereviewed        = "appeal.rereviewed"
	AppealEscalated         = "appeal.escalated"
	AppealResolved          = "appeal.resolved"
	ObjectionSubmitted      = "objection.submitted"
	ObjectionDecided        = "objection.decided"
	// 举报事件的载荷里绝不放举报人：事件会流到通知worker，通知里带上就等于广播出去。
	ReportFiled                      = "report.filed"
	ReportReviewed                   = "report.reviewed"
	ReportEscalated                  = "report.escalated"
	ReportDecided                    = "report.decided"
	DispatchDone                     = "dispatch.done"
	DispatchReassigned               = "dispatch.reassigned"
	ReviewDecided                    = "review.decided"
	ConflictRaised                   = "conflict.raised"
	ArbitrationResolved              = "arbitration.resolved"
	WindowReminder                   = "window.reminder"
	GateForced                       = "gate.forced"
	SealConfirmed                    = "seal.confirmed"
	ResultConfirmed                  = "result.confirmed"
	ExportRequested                  = "export.requested"
	ExportDone                       = "export.done"
	SettlementDone                   = "settlement.done"
	AIBatchCreated                   = "ai.batch.created"
	KnowledgeDocumentCreated         = "knowledge.document.created"
	PlatformKnowledgeDocumentCreated = "platform.knowledge_document.created"
	AgentMessageCreated              = "agent.message.created"
	ClassificationSuggested          = "classification.suggested"
	ClassificationResolved           = "classification.resolved"
	ScorecardAuditAssigned           = "scorecard_audit.assigned"
	ScorecardAuditSubmitted          = "scorecard_audit.submitted"
	ScorecardAuditCompleted          = "scorecard_audit.completed"
	ScorecardAuditStale              = "scorecard_audit.stale"
	ScorecardAuditBlocked            = "scorecard_audit.blocked"
	ReviewSLAOverdue                 = "review.sla_overdue"
)

// Kind binds an event name to its JSON payload at compile time. New event
// producers and consumers should use Kind instead of pairing a string with an
// untyped map independently on both sides of the outbox boundary.
type Kind[T any] struct{ name string }

func (k Kind[T]) Name() string { return k.name }

func Decode[T any](event Event, kind Kind[T]) (T, error) {
	var payload T
	if event.Type != kind.name {
		return payload, fmt.Errorf("event type %q does not match %q", event.Type, kind.name)
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return payload, fmt.Errorf("decode %s payload: %w", kind.name, err)
	}
	return payload, nil
}

type SubmissionCreatedPayload struct {
	SubmissionID int64  `json:"submissionId"`
	StudentID    int64  `json:"studentId"`
	CategoryKey  string `json:"categoryKey"`
}

type SubmissionForceRejectedPayload struct {
	SubmissionID  int64    `json:"submissionId"`
	StudentID     int64    `json:"studentId"`
	Title         string   `json:"title"`
	Reason        string   `json:"reason"`
	PreviousScore *float64 `json:"previousScore"`
	SelfRejected  bool     `json:"selfRejected,omitempty"`
	Score         *float64 `json:"score,omitempty"`
}

func SubmissionForceScoredEvent() Kind[SubmissionForceRejectedPayload] {
	return Kind[SubmissionForceRejectedPayload]{name: SubmissionForceScored}
}

func SubmissionForceRejectedEvent() Kind[SubmissionForceRejectedPayload] {
	return Kind[SubmissionForceRejectedPayload]{name: SubmissionForceRejected}
}

type AppealFiledPayload struct {
	AppealID   int64  `json:"appealId"`
	TargetType string `json:"targetType"`
	TargetID   int64  `json:"targetId"`
}

type AppealAssignedPayload struct {
	AppealID  int64 `json:"appealId"`
	HandlerID int64 `json:"handlerId"`
	Round     int   `json:"round"`
}

type AppealRereviewedPayload struct {
	AppealID   int64  `json:"appealId"`
	Status     string `json:"status"`
	ReviewerID int64  `json:"reviewerId"`
}

type AppealEscalatedPayload struct {
	AppealID int64  `json:"appealId"`
	Status   string `json:"status"`
}

type AppealResolvedPayload struct {
	AppealID int64    `json:"appealId"`
	Status   string   `json:"status"`
	Score    *float64 `json:"score,omitempty"`
	Category *string  `json:"category,omitempty"`
	ItemKey  *string  `json:"itemKey,omitempty"`
}

type ObjectionSubmittedPayload struct {
	BatchID    string  `json:"batchId"`
	IDs        []int64 `json:"ids"`
	ProposerID int64   `json:"proposerId"`
	Source     string  `json:"source,omitempty"`
}

type ObjectionDecidedPayload struct {
	ObjectionID int64    `json:"objectionId"`
	Status      string   `json:"status"`
	Score       *float64 `json:"score"`
	StudentID   int64    `json:"studentId"`
	ProposerID  int64    `json:"proposerId"`
}

type DispatchDonePayload struct {
	RunID       string   `json:"runId"`
	Trigger     string   `json:"trigger"`
	Assigned    int      `json:"assigned"`
	Blocked     int      `json:"blocked"`
	ReviewerIDs []string `json:"reviewerIds"`
}

type DispatchReassignedPayload struct {
	SubmissionID string `json:"submissionId"`
	From         string `json:"from"`
	To           string `json:"to"`
	Reason       string `json:"reason"`
}

type ReviewDecidedPayload struct {
	SubmissionID int64  `json:"submissionId"`
	ReviewID     int64  `json:"reviewId"`
	Status       string `json:"status"`
}

type ConflictRaisedPayload struct {
	SubmissionID int64  `json:"submissionId"`
	ReviewID     int64  `json:"reviewId"`
	Status       string `json:"status"`
}

// ArbitrationResolvedPayload preserves the existing flat JSON contract for
// both appeal arbitration and direct submission arbitration. Exactly one of
// AppealID and SubmissionID is set by the constructors below.
type ArbitrationResolvedPayload struct {
	AppealID     *int64  `json:"appealId,omitempty"`
	SubmissionID *int64  `json:"submissionId,omitempty"`
	StudentID    *int64  `json:"studentId,omitempty"`
	TargetType   string  `json:"targetType,omitempty"`
	TargetID     *int64  `json:"targetId,omitempty"`
	Score        float64 `json:"score"`
	Category     string  `json:"category"`
	ItemKey      string  `json:"itemKey"`
	Reason       string  `json:"reason,omitempty"`
}

// Report payloads intentionally contain only the report and reported-student
// identifiers needed by downstream processing. Reporter identity, request
// metadata, evidence and report contents must never cross the event boundary.
type ReportFiledPayload struct {
	ReportID  int64 `json:"reportId"`
	StudentID int64 `json:"studentId"`
}

type ReportReviewedPayload struct {
	ReportID int64  `json:"reportId"`
	Status   string `json:"status"`
}

type ReportEscalatedPayload struct {
	ReportID  int64  `json:"reportId"`
	Status    string `json:"status"`
	StudentID int64  `json:"studentId"`
}

type ReportDecidedPayload struct {
	ReportID  int64  `json:"reportId"`
	Status    string `json:"status"`
	StudentID int64  `json:"studentId"`
}

func NewAppealArbitrationResolvedPayload(appealID int64, targetType string, targetID int64, score float64, category, itemKey string) ArbitrationResolvedPayload {
	return ArbitrationResolvedPayload{
		AppealID: &appealID, TargetType: targetType, TargetID: &targetID,
		Score: score, Category: category, ItemKey: itemKey,
	}
}

func NewSubmissionArbitrationResolvedPayload(submissionID, studentID int64, score float64, category, itemKey, reason string) ArbitrationResolvedPayload {
	return ArbitrationResolvedPayload{
		SubmissionID: &submissionID, StudentID: &studentID, Score: score,
		Category: category, ItemKey: itemKey, Reason: reason,
	}
}

type AIBatchCreatedPayload struct {
	BatchID string `json:"batchId"`
}

type DocumentCreatedPayload struct {
	DocumentID string `json:"documentId"`
}

type AgentMessageCreatedPayload struct {
	MessageID string `json:"messageId"`
}

type ExportRequestedPayload struct {
	JobID string `json:"jobId"`
	RunID int64  `json:"runId"`
	Kind  string `json:"kind"`
}

func SubmissionCreatedEvent() Kind[SubmissionCreatedPayload] {
	return Kind[SubmissionCreatedPayload]{name: SubmissionCreated}
}

func AppealFiledEvent() Kind[AppealFiledPayload] {
	return Kind[AppealFiledPayload]{name: AppealFiled}
}

func AppealAssignedEvent() Kind[AppealAssignedPayload] {
	return Kind[AppealAssignedPayload]{name: AppealAssigned}
}

func AppealRereviewedEvent() Kind[AppealRereviewedPayload] {
	return Kind[AppealRereviewedPayload]{name: AppealRereviewed}
}

func AppealEscalatedEvent() Kind[AppealEscalatedPayload] {
	return Kind[AppealEscalatedPayload]{name: AppealEscalated}
}

func AppealResolvedEvent() Kind[AppealResolvedPayload] {
	return Kind[AppealResolvedPayload]{name: AppealResolved}
}

func ObjectionSubmittedEvent() Kind[ObjectionSubmittedPayload] {
	return Kind[ObjectionSubmittedPayload]{name: ObjectionSubmitted}
}

func ObjectionDecidedEvent() Kind[ObjectionDecidedPayload] {
	return Kind[ObjectionDecidedPayload]{name: ObjectionDecided}
}

func DispatchDoneEvent() Kind[DispatchDonePayload] {
	return Kind[DispatchDonePayload]{name: DispatchDone}
}

func DispatchReassignedEvent() Kind[DispatchReassignedPayload] {
	return Kind[DispatchReassignedPayload]{name: DispatchReassigned}
}

func ReviewDecidedEvent() Kind[ReviewDecidedPayload] {
	return Kind[ReviewDecidedPayload]{name: ReviewDecided}
}

func ConflictRaisedEvent() Kind[ConflictRaisedPayload] {
	return Kind[ConflictRaisedPayload]{name: ConflictRaised}
}

func ArbitrationResolvedEvent() Kind[ArbitrationResolvedPayload] {
	return Kind[ArbitrationResolvedPayload]{name: ArbitrationResolved}
}

func ReportFiledEvent() Kind[ReportFiledPayload] {
	return Kind[ReportFiledPayload]{name: ReportFiled}
}

func ReportReviewedEvent() Kind[ReportReviewedPayload] {
	return Kind[ReportReviewedPayload]{name: ReportReviewed}
}

func ReportEscalatedEvent() Kind[ReportEscalatedPayload] {
	return Kind[ReportEscalatedPayload]{name: ReportEscalated}
}

func ReportDecidedEvent() Kind[ReportDecidedPayload] {
	return Kind[ReportDecidedPayload]{name: ReportDecided}
}

func AIBatchCreatedEvent() Kind[AIBatchCreatedPayload] {
	return Kind[AIBatchCreatedPayload]{name: AIBatchCreated}
}

func KnowledgeDocumentCreatedEvent() Kind[DocumentCreatedPayload] {
	return Kind[DocumentCreatedPayload]{name: KnowledgeDocumentCreated}
}

func PlatformDocumentCreatedEvent() Kind[DocumentCreatedPayload] {
	return Kind[DocumentCreatedPayload]{name: PlatformKnowledgeDocumentCreated}
}

func AgentMessageCreatedEvent() Kind[AgentMessageCreatedPayload] {
	return Kind[AgentMessageCreatedPayload]{name: AgentMessageCreated}
}

func ExportRequestedEvent() Kind[ExportRequestedPayload] {
	return Kind[ExportRequestedPayload]{name: ExportRequested}
}
