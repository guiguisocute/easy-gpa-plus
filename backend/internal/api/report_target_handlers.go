package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"easygpa/backend/internal/scheme"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Load the reported material only when its desk is opened. The report grants
// access to its assigned reviewers and adjudicators, never to its reporter.
// Reasons and review attachments remain behind the shared score-history policy.
func (s *Server) reportTargetDetail(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	ctx, tx, actor := c.Request.Context(), mustTx(c), mustActor(c)
	var row reportRecord
	err := tx.QueryRow(ctx, `
		SELECT r.student_id,r.scheme_id,r.kind,r.target_id,r.category_key,r.item_key
		 FROM report r JOIN app_user student ON student.id=r.student_id
		 WHERE r.id=$1 AND ($2='class_admin' OR ($2='group' AND (
		   ($4 AND student.role='class_admin' AND r.status<>'reviewing') OR
		   (r.student_id<>$3 AND EXISTS (SELECT 1 FROM report_reviewer rr
		     WHERE rr.report_id=r.id AND rr.reviewer_id=$3)))))`,
		id, actor.Role, actor.UserID, actor.IsDeputy).
		Scan(&row.StudentID, &row.SchemeID, &row.Kind, &row.TargetID, &row.Category, &row.ItemKey)
	if notFound(c, err, "举报") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	result := gin.H{"kind": row.Kind, "targetId": nil, "submission": nil, "evidence": []gin.H{},
		"currentScore": nil, "recordedBasis": "", "fullScore": nil, "perScore": nil}
	if row.Kind == "submission" {
		if row.TargetID == nil {
			writeError(c, http.StatusNotFound, "not_found", "举报原始材料不存在", nil)
			return
		}
		item, err := loadOwnedSubmission(ctx, tx, *row.TargetID, row.StudentID, false)
		if notFound(c, err, "原始材料") {
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		// Do not expose a withdrawn draft or a new, unfinished review through
		// an old report. The history and download routes enforce the same gate.
		if _, err := loadScoreHistoryTarget(ctx, tx, row.Kind, *row.TargetID); err != nil {
			if !notFound(c, err, "已定分材料") {
				writeServiceError(c, err)
			}
			return
		}
		files, err := scoreHistoryEvidence(ctx, tx, scoreHistoryTarget{ID: *row.TargetID, Kind: row.Kind}, nil)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		result["targetId"], result["currentScore"], result["evidence"] = item.ID, item.FinalScore, files
		result["submission"] = gin.H{
			"id": item.ID, "title": item.Title, "note": item.Note, "claim": item.Claim,
			"requestedScore": item.WantScore, "submittedAt": item.SubmittedAt,
			"ruleSnapshot": item.RuleSnapshot, "filedRuleSnapshot": item.FiledSnapshot,
		}
	} else {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT config FROM scheme WHERE id=$1`, row.SchemeID).Scan(&raw); err != nil {
			writeServiceError(c, err)
			return
		}
		var config scheme.Config
		if err := json.Unmarshal(raw, &config); err != nil {
			writeServiceError(c, err)
			return
		}
		_, _, full, per, err := schemeItemLabels(config, row.Category, row.ItemKey, row.Kind)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		result["fullScore"], result["perScore"] = full, per
		var targetID int64
		var score float64
		var basis string
		err = tx.QueryRow(ctx, `SELECT id,score::float8,basis FROM base_score
		 WHERE student_id=$1 AND scheme_id=$2 AND category_key=$3 AND item_key=$4 AND kind=$5`,
			row.StudentID, row.SchemeID, row.Category, row.ItemKey, row.Kind).Scan(&targetID, &score, &basis)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeServiceError(c, err)
			return
		}
		if err == nil {
			result["targetId"], result["currentScore"], result["recordedBasis"] = strconv.FormatInt(targetID, 10), score, basis
		}
	}
	c.JSON(http.StatusOK, result)
}
