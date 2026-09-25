package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMailReconciliationRequiresKnownOutcomeAndReceipt(t *testing.T) {
	tests := []struct {
		name  string
		input mailReconciliationInput
		valid bool
	}{
		{"accepted needs receipt", mailReconciliationInput{Status: "sent"}, false},
		{"rejected cannot have receipt", mailReconciliationInput{Status: "failed", MessageID: "ses-123"}, false},
		{"unknown status", mailReconciliationInput{Status: "queued"}, false},
		{"receipt cannot contain newline", mailReconciliationInput{Status: "sent", MessageID: "ses\n123"}, false},
		{"oversize receipt", mailReconciliationInput{Status: "sent", MessageID: strings.Repeat("x", 257)}, false},
		{"accepted with receipt", mailReconciliationInput{Status: "sent", MessageID: " ses-123 "}, true},
		{"confirmed rejection", mailReconciliationInput{Status: "failed"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateMailReconciliation(&tt.input); (err == nil) != tt.valid {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

type reconciliationTestTx struct {
	pgx.Tx
	rowErr     error
	auditErr   error
	auditCalls int
	auditArgs  []any
}

type reconciliationTestRow struct{ err error }

func (r reconciliationTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	eventID, tenantID := "01900000-0000-7000-8000-000000000001", int64(1)
	*dest[0].(**string) = &eventID
	*dest[1].(**int64) = &tenantID
	return nil
}
func (tx *reconciliationTestTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return reconciliationTestRow{err: tx.rowErr}
}
func (tx *reconciliationTestTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.auditCalls++
	tx.auditArgs = args
	return pgconn.NewCommandTag("INSERT 0 1"), tx.auditErr
}

func TestMailReconciliationAuditsExactOutcomeAndPropagatesAuditFailure(t *testing.T) {
	tx := &reconciliationTestTx{}
	eventID, tenantID, err := reconcileMailDelivery(t.Context(), tx, "test-operator", "127.0.0.1", 123, mailReconciliationInput{Status: "sent", MessageID: "ses-123"})
	if err != nil || eventID == nil || tenantID == nil || tx.auditCalls != 1 {
		t.Fatalf("result=%v %v err=%v audit=%d", eventID, tenantID, err, tx.auditCalls)
	}
	if tx.auditArgs[0] != "test-operator" || tx.auditArgs[1] != int64(123) {
		t.Fatalf("audit actor/id=%v", tx.auditArgs[:2])
	}
	var metadata map[string]any
	if err := json.Unmarshal(tx.auditArgs[2].([]byte), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["deliveryId"] != "123" || metadata["status"] != "sent" || metadata["messageId"] != "ses-123" || metadata["eventId"] != *eventID {
		t.Fatalf("audit metadata=%#v", metadata)
	}
	auditErr := errors.New("audit storage failed")
	tx.auditErr = auditErr
	if _, _, err := reconcileMailDelivery(t.Context(), tx, "test-operator", "127.0.0.1", 123, mailReconciliationInput{Status: "failed"}); !errors.Is(err, auditErr) {
		t.Fatalf("audit failure not returned: %v", err)
	}
}

func TestMailReconciliationDoesNotAuditRejectedChange(t *testing.T) {
	tx := &reconciliationTestTx{rowErr: pgx.ErrNoRows}
	if _, _, err := reconcileMailDelivery(t.Context(), tx, "test-operator", "", 123, mailReconciliationInput{Status: "failed"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("error=%v", err)
	}
	if tx.auditCalls != 0 {
		t.Fatal("conflicting reconciliation wrote successful audit")
	}
}
