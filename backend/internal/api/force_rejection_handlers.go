package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/scheme"
)

type forceRejection struct {
	Reason        string    `json:"reason"`
	PreviousScore *float64  `json:"previousScore"`
	RejectedAt    time.Time `json:"rejectedAt"`
	ActorName     string    `json:"actorName"`
	SelfRejected  bool      `json:"selfRejected,omitempty"`
	Score         *float64  `json:"score,omitempty"` // Present only for a forced score correction.
}

func forceRejectPermission(actor Actor, studentID int64, status string, rejected bool) (bool, string) {
	if actor.UserID == studentID {
		return false, selfAdjudicationReason
	}
	if actor.Role != "class_admin" && !(actor.Role == "group" && actor.IsDeputy) {
		return false, "无权强制驳回这项材料"
	}
	if status == "draft" {
		return false, "草稿尚未提交审核"
	}
	if rejected {
		return false, "该项已被强制驳回"
	}
	return true, ""
}

func (s *Server) forceRejectSubmission(c *gin.Context) {
	s.correctSubmission(c, false, false)
}

func (s *Server) selfForceRejectSubmission(c *gin.Context) {
	s.correctSubmission(c, true, false)
}

func (s *Server) forceScoreSubmission(c *gin.Context) {
	s.correctSubmission(c, false, true)
}

func forceScorePermission(actor Actor, studentID int64, status string, score *float64, rejected bool) (bool, string) {
	if ok, reason := forceRejectPermission(actor, studentID, status, rejected); !ok {
		return false, reason
	}
	if score == nil || (status != "scored" && status != "locked" && status != "appealing" && status != "arbitrating") {
		return false, "只能强制修改已定分的材料"
	}
	return true, ""
}

// Owners may give up an already awarded score, including while an appeal is
// pending. This is separate from adjudication and never permits erasing a
// negative score to increase one's own result.
func selfForceRejectPermission(status string, score *float64, rejected bool) (bool, string) {
	if rejected {
		return false, "该项已被强制驳回"
	}
	if score == nil || (status != "scored" && status != "locked" && status != "appealing" && status != "arbitrating") {
		return false, "只能主动驳回本人已定分的提交"
	}
	if *score < 0 {
		return false, "负分认定不能自行清零，请通过申诉处理"
	}
	return true, ""
}

func (s *Server) correctSubmission(c *gin.Context, selfReject, forceScore bool) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Reason        string   `json:"reason"`
		Score         *float64 `json:"score"`
		PreviousScore *float64 `json:"previousScore"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "材料处理参数不正确", nil)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if count := utf8.RuneCountInString(input.Reason); count < 1 || count > 2000 {
		writeError(c, http.StatusUnprocessableEntity, "reason_required", "请填写 1—2000 字的处理理由", nil)
		return
	}
	if forceScore && (input.Score == nil || input.PreviousScore == nil || math.IsNaN(*input.Score) || math.IsInf(*input.Score, 0) || math.Abs(*input.Score) > 99999) {
		writeError(c, http.StatusUnprocessableEntity, "score_invalid", "请填写有效的新认定分，并刷新确认原分数", nil)
		return
	}
	tx, actor := mustTx(c), mustActor(c)
	ctx := c.Request.Context()
	var studentID, schemeID int64
	// Snapshot producers must finish before we invalidate their output. Without
	// their advisory locks, a producer could read the old score, stay invisible
	// to our invalidation query, and publish that obsolete snapshot afterwards.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('easygpa:settlement:' || $1::bigint::text,0))`, actor.ClassID); err != nil {
		writeServiceError(c, err)
		return
	}
	err := tx.QueryRow(ctx, `SELECT student_id,scheme_id FROM submission WHERE id=$1 AND class_id=$2
		AND (NOT $3::boolean OR student_id=$4)`, id, actor.ClassID, selfReject, actor.UserID).Scan(&studentID, &schemeID)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('easygpa:blind-audit-student:' || $1::bigint::text || ':' || $2::bigint::text,0))`, actor.ClassID, studentID); err != nil {
		writeServiceError(c, err)
		return
	}
	var status, title string
	var score *float64
	var existing []byte
	err = tx.QueryRow(ctx, `
		SELECT student_id,scheme_id,status,title,final_score::float8,force_rejection
		  FROM submission WHERE id=$1 AND class_id=$2 AND (NOT $3::boolean OR student_id=$4) FOR UPDATE
	`, id, actor.ClassID, selfReject, actor.UserID).Scan(&studentID, &schemeID, &status, &title, &score, &existing)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !selfReject && !requireAdjudicationTarget(c, tx, actor, studentID) {
		return
	}
	allowed, reason := forceRejectPermission(actor, studentID, status, len(existing) > 0)
	if selfReject {
		allowed, reason = selfForceRejectPermission(status, score, len(existing) > 0)
	}
	if forceScore {
		allowed, reason = forceScorePermission(actor, studentID, status, score, len(existing) > 0)
	}
	if !allowed {
		code := "submission_force_rejected"
		if forceScore && len(existing) == 0 {
			code = "force_score_unavailable"
		} else if selfReject && len(existing) == 0 {
			code = "self_force_reject_unavailable"
		} else if status == "draft" {
			code = "submission_not_submitted"
		}
		writeError(c, http.StatusConflict, code, reason, nil)
		return
	}
	newScore := 0.0
	if forceScore {
		if scheme.NewPoints(*input.PreviousScore) != scheme.NewPoints(*score) {
			writeError(c, http.StatusConflict, "score_changed", "认定分已变化，请刷新材料后重新确认", nil)
			return
		}
		newScore = scheme.NewPoints(*input.Score).Float64()
		if newScore == *score {
			writeError(c, http.StatusUnprocessableEntity, "score_unchanged", "新认定分须与当前分数不同", nil)
			return
		}
		var rawRule []byte
		if err := tx.QueryRow(ctx, `SELECT rule_snapshot FROM submission WHERE id=$1`, id).Scan(&rawRule); err != nil {
			writeServiceError(c, err)
			return
		}
		var snapshot ruleSnapshot
		if err := json.Unmarshal(rawRule, &snapshot); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := scoreWithinRule(snapshot.Item.ScoreRule, newScore); err != nil {
			writeError(c, http.StatusUnprocessableEntity, "score_invalid", err.Error(), nil)
			return
		}
	}
	var actorName string
	if err := tx.QueryRow(ctx, `SELECT name FROM app_user WHERE id=$1`, actor.UserID).Scan(&actorName); err != nil {
		writeServiceError(c, err)
		return
	}
	rejection := forceRejection{Reason: input.Reason, PreviousScore: score, RejectedAt: time.Now().UTC(), ActorName: actorName, SelfRejected: selfReject}
	if forceScore {
		rejection.Score = &newScore
	}
	raw, err := json.Marshal(rejection)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	updateQuery := `
		UPDATE submission SET final_score=$3,status='scored',force_rejection=$2,
		       scored_at=now(),updated_at=now(),lock_version=lock_version+1 WHERE id=$1
	`
	if forceScore {
		updateQuery = strings.Replace(updateQuery, "force_rejection=$2", "forced_score=$2", 1)
	}
	if _, err := tx.Exec(ctx, updateQuery, id, raw, newScore); err != nil {
		writeServiceError(c, err)
		return
	}
	// Finish every older workflow without rewriting reviewers' historical
	// decisions. Database guards also stop new work racing with this correction.
	resolutionReason := "该材料已被管理员强制驳回：" + input.Reason
	if selfReject {
		resolutionReason = "该材料已由本人主动强制驳回：" + input.Reason
	}
	if forceScore {
		resolutionReason = "该材料已由管理员强制改分：" + input.Reason
	}
	for _, query := range []string{
		`UPDATE appeal SET status='final',handler_id=$2,resolution_score=$4,resolution_reason=$3,
		 resolved_at=now(),updated_at=now() WHERE target_type='submission' AND target_id=$1
		 AND status IN ('draft','filed','reviewing','escalated')`,
		`UPDATE objection SET status='dismissed',decided_by=$2,decided_score=$4,decision_reason=$3,
		 decided_at=now(),updated_at=now() WHERE kind='submission' AND target_id=$1 AND status IN ('draft','submitted')`,
		`UPDATE classification_suggestion SET status='rejected',resolved_by=$2,resolution_reason=$3,
		 resolved_at=now() WHERE submission_id=$1 AND status IN ('draft','pending')`,
		`UPDATE scorecard_audit_flag SET status='dismissed',resolved_by=$2,resolution_reason=$3,
		 resolved_at=now(),updated_at=now() WHERE target_submission_id=$1 AND status IN ('draft','submitted')`,
		`UPDATE report SET status='final',decided_by=$2,final_score=$4,decision_reason=$3,
		 decided_at=now(),updated_at=now() WHERE kind='submission' AND target_id=$1 AND status IN ('reviewing','escalated')`,
	} {
		args := []any{id, actor.UserID, resolutionReason}
		if strings.Contains(query, "$4") {
			args = append(args, newScore)
		}
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE submission_reviewer SET active=false WHERE submission_id=$1 AND active`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE governance_proposal SET status='stale',result='{"reason":"score_corrected"}' WHERE status IN ('discussion','voting','deliberating','blocked','passed') AND ((action='submission' AND target_id=$1) OR (action='appeal' AND target_id IN (SELECT id FROM appeal WHERE target_type='submission' AND target_id=$1)) OR (action='report' AND target_id IN (SELECT id FROM report WHERE kind='submission' AND target_id=$1)) OR (action='objection' AND target_id IN (SELECT id FROM objection WHERE kind='submission' AND target_id=$1)))`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateLatestSettlement(ctx, tx, resolutionReason); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateStudentBlindAudit(ctx, tx, schemeID, studentID, "", resolutionReason); err != nil {
		writeServiceError(c, err)
		return
	}
	action, field := "submission.force_rejected", "forceRejection"
	eventKind := events.SubmissionForceRejectedEvent()
	if forceScore {
		action, field = "submission.force_scored", "forcedScore"
		eventKind = events.SubmissionForceScoredEvent()
	}
	if err := appendAudit(c, tx, action, "submission", strconv.FormatInt(id, 10),
		gin.H{"status": status, "finalScore": score},
		gin.H{"status": "scored", "finalScore": newScore, field: rejection},
		gin.H{"reason": input.Reason, "studentId": studentID, "selfRejected": selfReject}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(ctx, tx, actor.ClassID, eventKind, events.SubmissionForceRejectedPayload{
		SubmissionID: id, StudentID: studentID, Title: title, Reason: input.Reason, PreviousScore: score, SelfRejected: selfReject, Score: rejection.Score,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": "scored", "finalScore": newScore, field: rejection})
}
