package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type blindAssignment struct {
	ID          int64
	BatchID     string
	SubjectID   int64
	StudentID   int64
	SchemeID    int64
	BatchStatus string
	Status      string
	Snapshot    []byte
	Result      *string
	Note        string
	SubmittedAt *time.Time
}

func loadBlindAssignment(ctx context.Context, tx pgx.Tx, assignmentID, reviewerID int64, lock bool) (blindAssignment, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE OF a,b"
	}
	var row blindAssignment
	err := tx.QueryRow(ctx, `
		SELECT a.id,b.id::text,a.subject_id,subject.student_id,b.scheme_id,b.status,a.status,
		       subject.snapshot,a.result,a.note,a.submitted_at
		  FROM scorecard_audit_assignment a
		  JOIN scorecard_audit_subject subject ON subject.id=a.subject_id
		  JOIN scorecard_audit_batch b ON b.id=subject.batch_id
		 WHERE a.id=$1 AND a.reviewer_id=$2 AND a.status<>'superseded'`+lockSQL,
		assignmentID, reviewerID).Scan(&row.ID, &row.BatchID, &row.SubjectID, &row.StudentID, &row.SchemeID,
		&row.BatchStatus, &row.Status, &row.Snapshot, &row.Result, &row.Note, &row.SubmittedAt)
	return row, err
}

func blindAssignmentEditable(row blindAssignment) bool {
	return row.Status == "assigned" && (row.BatchStatus == "open" || row.BatchStatus == "resolving")
}

func anonymousBlindAuditSnapshot(raw []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var snapshot any
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode blind audit snapshot: %w", err)
	}
	stripBlindAuditReviewIdentities(snapshot)
	result, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode blind audit snapshot: %w", err)
	}
	return result, nil
}

// Rejection can happen after the snapshot was frozen. Read the current
// irreversible rejection state without rewriting historical snapshot evidence,
// hashes, scores, reviewer decisions or confirmations.
func reviewableBlindAuditSnapshot(ctx context.Context, tx pgx.Tx, studentID int64, raw []byte) (json.RawMessage, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM submission WHERE student_id=$1 AND force_rejection IS NOT NULL`, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rejected := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		rejected[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return filterRejectedSnapshotItems(raw, rejected)
}

func filterRejectedSnapshotItems(raw []byte, rejected map[string]bool) (json.RawMessage, error) {
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	var details map[string]json.RawMessage
	if err := json.Unmarshal(snapshot["details"], &details); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(details["items"], &items); err != nil {
		return nil, err
	}
	visible := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var target struct {
			ID             string          `json:"id"`
			ForceRejection json.RawMessage `json:"forceRejection"`
		}
		if err := json.Unmarshal(item, &target); err != nil {
			return nil, err
		}
		if rejected[target.ID] || (len(target.ForceRejection) > 0 && string(target.ForceRejection) != "null") {
			continue
		}
		visible = append(visible, item)
	}
	details["items"], _ = json.Marshal(visible)
	snapshot["details"], _ = json.Marshal(details)
	return json.Marshal(snapshot)
}

// Identity lives in three places in a snapshot: the original review pair, the
// appeal rounds those two reconsidered, and the administrator who signed the
// last one. All three are removed for the reviewer; none of the reasoning is.
var (
	reviewIdentityFields   = []string{"id", "reviewerId", "reviewerSid", "reviewer"}
	appealIdentityFields   = []string{"id", "handlerId", "handlerSid", "handler"}
	rereviewIdentityFields = []string{"reviewerId", "reviewerSid", "reviewer"}
)

func stripBlindAuditReviewIdentities(value any) {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			stripBlindAuditReviewIdentities(child)
		}
	case map[string]any:
		for key, child := range node {
			switch key {
			case "forceRejection":
				if rejection, ok := child.(map[string]any); ok {
					delete(rejection, "actorName")
				}
			case "reviews":
				stripJSONFields(child, reviewIdentityFields)
			case "appeals":
				stripJSONFields(child, appealIdentityFields)
				rounds, _ := child.([]any)
				for _, rawRound := range rounds {
					if round, ok := rawRound.(map[string]any); ok {
						stripJSONFields(round["rereviews"], rereviewIdentityFields)
					}
				}
			default:
				stripBlindAuditReviewIdentities(child)
			}
		}
	}
}

// stripJSONFields deletes the given keys from every object of a decoded JSON
// array, and does nothing at all if the value is not one.
func stripJSONFields(value any, fields []string) {
	rows, ok := value.([]any)
	if !ok {
		return
	}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range fields {
			delete(row, field)
		}
	}
}

// Historical origin IDs may still be present on an appeal. Completing that
// appeal must never restart a retired review batch or send a final-review notice.
func refreshBlindAuditBatch(_ context.Context, _ pgx.Tx, _ int64, _ string) (string, error) {
	return "", nil
}
