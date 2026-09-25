package api

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// clearSelfReviewAssignments drops first-round seats where the reviewer is the
// student. Already-submitted self reviews stay (un-reviewing is arbitration);
// empty seats are released so the next dispatch can fill them with someone else.
func clearSelfReviewAssignments(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		UPDATE submission_reviewer sr SET active=false
		  FROM submission s
		 WHERE sr.submission_id=s.id
		   AND sr.active
		   AND sr.reviewer_id=s.student_id
		   AND NOT EXISTS (
		         SELECT 1 FROM review r
		          WHERE r.submission_id=s.id
		            AND r.reviewer_id=sr.reviewer_id
		            AND r.superseded_at IS NULL
		       )
	`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE scorecard_audit_assignment a
		   SET status='superseded', superseded_at=now()
		  FROM scorecard_audit_subject subject
		 WHERE a.subject_id=subject.id
		   AND a.status='assigned'
		   AND a.reviewer_id=subject.student_id
	`)
	return err
}

// A sole administrator must also recuse; their appointed deputy can sign.
func rejectSelfArbitration(c *gin.Context, tx pgx.Tx, actor Actor, studentID int64) bool {
	return !requireAdjudicationTarget(c, tx, actor, studentID)
}
