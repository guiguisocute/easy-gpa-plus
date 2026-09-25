package api

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type noteEvidenceQueryTx struct {
	pgx.Tx
	query string
	args  []any
}

func (tx *noteEvidenceQueryTx) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	tx.query = query
	tx.args = args
	return emptyNoteEvidenceRows{}, nil
}

type emptyNoteEvidenceRows struct{ pgx.Rows }

func (emptyNoteEvidenceRows) Close()            {}
func (emptyNoteEvidenceRows) Err() error        { return nil }
func (emptyNoteEvidenceRows) Next() bool        { return false }
func (emptyNoteEvidenceRows) Scan(...any) error { return nil }

func TestSubmissionNoteEvidenceForReviewerScopesCreator(t *testing.T) {
	tx := &noteEvidenceQueryTx{}
	if _, err := submissionNoteEvidenceForReviewer(context.Background(), tx, 41, 7); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tx.query, "created_by=$3") {
		t.Fatalf("query does not scope note evidence to its creator: %s", tx.query)
	}
	if want := []any{int64(41), "note", int64(7)}; !reflect.DeepEqual(tx.args, want) {
		t.Fatalf("query args = %#v, want %#v", tx.args, want)
	}
}

func TestAppealNoteEvidenceForReviewerWaitsForPeerReveal(t *testing.T) {
	tx := &noteEvidenceQueryTx{}
	if _, err := appealNoteEvidenceForReviewer(context.Background(), tx, 43, 9); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tx.query, "created_by=$3") ||
		!strings.Contains(tx.query, "decided_at IS NULL") ||
		!strings.Contains(tx.query, "reviewer_id=evidence.created_by") {
		t.Fatalf("query does not preserve appeal back-to-back visibility: %s", tx.query)
	}
	if want := []any{int64(43), "note", int64(9)}; !reflect.DeepEqual(tx.args, want) {
		t.Fatalf("query args = %#v, want %#v", tx.args, want)
	}
}

func TestGroupNoteEvidenceReadPolicy(t *testing.T) {
	tests := []struct {
		name                     string
		owner                    noteOwnerKind
		own                      bool
		submissionAppealReviewer bool
		appealAllDecided         bool
		appealCreatorReviewer    bool
		want                     bool
	}{
		{"submission owner", noteOwnerSubmission, true, false, false, false, true},
		{"submission peer before appeal", noteOwnerSubmission, false, false, false, false, false},
		{"submission note needed for appeal", noteOwnerSubmission, false, true, false, false, true},
		{"appeal owner", noteOwnerAppeal, true, false, false, false, true},
		{"appeal peer before both decide", noteOwnerAppeal, false, false, false, true, false},
		{"appeal peer after both decide", noteOwnerAppeal, false, false, true, true, true},
		{"appeal administrator draft", noteOwnerAppeal, false, false, true, false, false},
		{"objection peer", noteOwnerObjection, false, false, false, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := groupCanReadNoteEvidence(test.owner, test.own, test.submissionAppealReviewer, test.appealAllDecided, test.appealCreatorReviewer)
			if got != test.want {
				t.Fatalf("groupCanReadNoteEvidence() = %v, want %v", got, test.want)
			}
		})
	}
}
