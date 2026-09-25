package api

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func governanceEvidenceAllowed(ctx context.Context, tx pgx.Tx, actor Actor, id int64) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM evidence e JOIN governance_proposal p ON (
   (p.action='submission' AND e.submission_id=p.target_id) OR
   (p.action='appeal' AND (e.appeal_id=p.target_id OR (e.submission_id IN (SELECT target_id FROM appeal WHERE id=p.target_id AND target_type='submission')))) OR
   (p.action='report' AND (e.report_id=p.target_id OR (e.submission_id IN (SELECT target_id FROM report WHERE id=p.target_id AND kind='submission')))) OR
   (p.action='objection' AND (e.objection_id=p.target_id OR (e.submission_id IN (SELECT target_id FROM objection WHERE id=p.target_id AND kind='submission')))) OR
   (p.action IN ('bonus','gpa') AND p.payload->>'uploadId'=e.bonus_upload_id::text)
 ) WHERE e.id=$1 AND e.blind_assignment_id IS NULL AND e.review_report_id IS NULL
 AND (e.kind='claim' OR e.report_id IS NOT NULL OR e.created_by=p.subject_id OR (p.action='objection' AND e.objection_id=p.target_id) OR e.bonus_upload_id IS NOT NULL)
 AND (p.subject_id=$2 OR p.author_id=$2 OR EXISTS(SELECT 1 FROM governance_voter v WHERE v.proposal_id=p.id AND v.user_id=$2 AND v.active))
 AND EXISTS(SELECT 1 FROM class_governance WHERE mode='collective')
 ) OR EXISTS(SELECT 1 FROM evidence e JOIN bonus_grant_upload u ON u.id=e.bonus_upload_id JOIN governance_member m ON m.user_id=u.created_by JOIN class_governance g ON g.class_id=m.class_id WHERE e.id=$1 AND u.created_by=$2 AND u.used_by IS NULL AND m.left_at IS NULL AND g.mode='collective')
	`, id, actor.UserID).Scan(&allowed)
	return allowed, err
}
