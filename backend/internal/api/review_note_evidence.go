package api

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func authorizeAdjudicationNote(ctx context.Context, tx pgx.Tx, actor Actor, studentID int64) error {
	err := authorizeAdjudication(ctx, tx, actor, studentID)
	var problem adjudicationProblem
	if errors.As(err, &problem) {
		return errNoteEvidenceForbidden
	}
	return err
}

// 终审附件只跟随本席位的正文公开；另一席位提交前不可枚举或下载。
func canReadBlindNote(ctx context.Context, tx pgx.Tx, actor Actor, assignmentID int64) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `
        SELECT $2='class_admin' OR ($2='group' AND (a.reviewer_id=$3 OR
          (a.status='submitted' AND (
            ($4 AND student.role='class_admin') OR
            EXISTS (SELECT 1 FROM scorecard_audit_assignment peer
              WHERE peer.subject_id=a.subject_id AND peer.reviewer_id=$3 AND peer.status<>'superseded'
                AND b.status<>'stale')))))
          FROM scorecard_audit_assignment a
          JOIN scorecard_audit_subject subject ON subject.id=a.subject_id
          JOIN scorecard_audit_batch b ON b.id=subject.batch_id
          JOIN app_user student ON student.id=subject.student_id
         WHERE a.id=$1`, assignmentID, actor.Role, actor.UserID, actor.Role == "group" && actor.IsDeputy).Scan(&allowed)
	return allowed, err
}

func canReadReportReviewNote(ctx context.Context, tx pgx.Tx, actor Actor, reportID int64, createdBy *int64) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `
        SELECT $2='class_admin' OR ($2='group' AND (($4 AND student.role='class_admin' AND r.status<>'reviewing') OR
          EXISTS (SELECT 1 FROM report_reviewer rr WHERE rr.report_id=r.id AND rr.reviewer_id=$3
            AND ($5::bigint=$3 OR r.status<>'reviewing'))))
          FROM report r JOIN app_user student ON student.id=r.student_id WHERE r.id=$1`,
		reportID, actor.Role, actor.UserID, actor.Role == "group" && actor.IsDeputy, createdBy).Scan(&allowed)
	return allowed, err
}

// 列表与下载使用相同的背靠背边界，不向另一名复核人泄露草稿文件名。
func reportReviewNoteEvidence(ctx context.Context, tx pgx.Tx, reportID int64, actor Actor) ([]gin.H, error) {
	return noteEvidenceWhere(ctx, tx, `review_report_id=$1 AND kind='note' AND (
        $2='class_admin' OR created_by=$3 OR EXISTS (
          SELECT 1 FROM report r JOIN app_user student ON student.id=r.student_id
          WHERE r.id=evidence.review_report_id AND r.status<>'reviewing' AND (
            ($4 AND student.role='class_admin') OR EXISTS (
              SELECT 1 FROM report_reviewer rr WHERE rr.report_id=r.id AND rr.reviewer_id=$3))))`,
		reportID, actor.Role, actor.UserID, actor.Role == "group" && actor.IsDeputy)

}

func attachBlindNoteEvidence(ctx context.Context, tx pgx.Tx, items []gin.H) error {
	for _, item := range items {
		id, ok := item["blindAssignmentId"].(*int64)
		if !ok || id == nil {
			continue
		}
		files, err := noteEvidenceOf(ctx, tx, noteOwnerBlindAudit, *id)
		if err != nil {
			return err
		}
		item["noteEvidence"] = files
	}
	return nil
}
