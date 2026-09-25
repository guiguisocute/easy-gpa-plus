package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/classtimeline"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/scheme"
)

type jsonID int64

func (id *jsonID) UnmarshalJSON(raw []byte) error {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return errors.New("ID 必须是正整数")
	}
	*id = jsonID(parsed)
	return nil
}

type appealTarget struct {
	Type         string
	ID           int64
	StudentID    int64
	SchemeID     int64
	Title        string
	Category     string
	ItemKey      string
	Status       string
	CurrentScore *float64
	FullScore    *float64
	Basis        string
	RuleSnapshot []byte
	RecorderID   int64
}

func loadAppealTarget(ctx context.Context, tx pgx.Tx, targetType string, targetID, studentID int64, lock bool) (appealTarget, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE"
	}
	target := appealTarget{Type: targetType, ID: targetID}
	switch targetType {
	case "submission":
		err := tx.QueryRow(ctx, `
			SELECT student_id,scheme_id,title,category_key,item_key,status,final_score::float8,rule_snapshot
			  FROM submission WHERE id=$1 AND student_id=$2`+lockSQL,
			targetID, studentID,
		).Scan(&target.StudentID, &target.SchemeID, &target.Title, &target.Category, &target.ItemKey, &target.Status,
			&target.CurrentScore, &target.RuleSnapshot)
		return target, err
	case "base_score", "penalty_score":
		var kind string
		err := tx.QueryRow(ctx, `
			SELECT student_id,scheme_id,item_key,category_key,kind,score::float8,full_score::float8,basis,recorded_by
			  FROM base_score WHERE id=$1 AND student_id=$2`+lockSQL,
			targetID, studentID,
		).Scan(&target.StudentID, &target.SchemeID, &target.ItemKey, &target.Category, &kind, &target.CurrentScore,
			&target.FullScore, &target.Basis, &target.RecorderID)
		if err != nil {
			return target, err
		}
		if (targetType == "base_score" && kind != "base") || (targetType == "penalty_score" && kind != "penalty") {
			return target, pgx.ErrNoRows
		}
		target.Title = target.ItemKey
		target.Status = "scored"
		return target, nil
	default:
		return target, errors.New("申诉对象类型不正确，请返回列表重新选择")
	}
}

type createAppealInput struct {
	TargetType string   `json:"targetType"`
	TargetID   jsonID   `json:"targetId"`
	Reason     string   `json:"reason"`
	Category   string   `json:"category"`
	ItemKey    string   `json:"itemKey"`
	Score      *float64 `json:"score"`
}

type appealDraftInput struct {
	TargetType string `json:"targetType"`
	TargetID   jsonID `json:"targetId"`
}

type appealSubmitInput struct {
	Reason   string   `json:"reason"`
	Category string   `json:"category"`
	ItemKey  string   `json:"itemKey"`
	Score    *float64 `json:"score"`
}

type appealClaimInput struct {
	Reason   string
	Category string
	ItemKey  string
	Score    *float64
}

type appealRequestProblem struct {
	status  int
	code    string
	message string
}

func (problem appealRequestProblem) Error() string { return problem.message }

func appealRequestFailure(status int, code, message string) error {
	return appealRequestProblem{status: status, code: code, message: message}
}

func writeAppealRequestFailure(c *gin.Context, err error) bool {
	var problem appealRequestProblem
	if errors.As(err, &problem) {
		writeError(c, problem.status, problem.code, problem.message, nil)
		return true
	}
	return false
}

func validAppealTargetType(value string) bool {
	return value == "submission" || value == "base_score" || value == "penalty_score"
}

type preparedAppealDraft struct {
	target     appealTarget
	previousID int64
	round      int
}

func (s *Server) prepareAppealDraft(ctx context.Context, tx pgx.Tx, actor Actor, targetType string, targetID int64) (preparedAppealDraft, error) {
	current, err := loadCurrentScheme(ctx, tx)
	if err != nil {
		return preparedAppealDraft{}, err
	}
	if err := ensureCapability(current.Config, "appeal", time.Now()); err != nil {
		return preparedAppealDraft{}, appealRequestFailure(http.StatusConflict, "appeal_closed", err.Error())
	}
	target, err := loadAppealTarget(ctx, tx, targetType, targetID, actor.UserID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return preparedAppealDraft{}, appealRequestFailure(http.StatusNotFound, "not_found", "申诉对象不存在")
	}
	if err != nil {
		return preparedAppealDraft{}, err
	}
	if target.Type == "submission" && target.Status != "scored" && target.Status != "locked" {
		return preparedAppealDraft{}, appealRequestFailure(http.StatusConflict, "not_appealable", "提交条目定分后才能申诉")
	}
	if target.CurrentScore == nil {
		return preparedAppealDraft{}, appealRequestFailure(http.StatusConflict, "not_appealable", "申诉对象尚未形成认定分")
	}
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM appeal
		 WHERE kind='student_appeal' AND target_type=$1 AND target_id=$2
		   AND status IN ('filed','reviewing','escalated'))
	`, target.Type, target.ID).Scan(&active); err != nil {
		return preparedAppealDraft{}, err
	}
	if active {
		return preparedAppealDraft{}, appealRequestFailure(http.StatusConflict, "appeal_in_progress", "这一笔申诉还在处理中，请等待结果后再操作")
	}
	var objectionPending bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM objection
		 WHERE student_id=$1 AND kind=CASE $2 WHEN 'submission' THEN 'submission'
		                           WHEN 'base_score' THEN 'base' ELSE 'penalty' END
		   AND (($2='submission' AND target_id=$3)
		     OR ($2<>'submission' AND scheme_id=$4 AND category_key=$5 AND item_key=$6))
		   AND status='submitted')
	`, actor.UserID, target.Type, target.ID, target.SchemeID, target.Category, target.ItemKey).Scan(&objectionPending); err != nil {
		return preparedAppealDraft{}, err
	}
	if objectionPending {
		return preparedAppealDraft{}, appealRequestFailure(http.StatusConflict, "objection_pending", "这一笔正在复核中，等结果出来再申诉")
	}
	var previous *appealRecord
	var previousID int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM appeal
		 WHERE kind='student_appeal' AND target_type=$1 AND target_id=$2 AND student_id=$3 AND status<>'draft'
		 ORDER BY round DESC,id DESC LIMIT 1
	`, target.Type, target.ID, actor.UserID).Scan(&previousID)
	if err == nil {
		loaded, loadErr := loadAppealRecord(ctx, tx, previousID, false)
		if loadErr != nil {
			return preparedAppealDraft{}, loadErr
		}
		previous = &loaded
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return preparedAppealDraft{}, err
	}
	round, err := nextStudentAppealRound(previous)
	if err != nil {
		code := "appeal_exhausted"
		if previous != nil && previous.Status == "final" {
			code = "appeal_finalized"
		} else if previous != nil && previous.Status != "resolved" {
			code = "appeal_in_progress"
		}
		return preparedAppealDraft{}, appealRequestFailure(http.StatusConflict, code, err.Error())
	}
	return preparedAppealDraft{target: target, previousID: previousID, round: round}, nil
}

func (s *Server) ensureAppealDraft(ctx context.Context, tx pgx.Tx, actor Actor, targetType string, targetID int64) (int64, bool, error) {
	var existingID int64
	err := tx.QueryRow(ctx, `
		SELECT id FROM appeal WHERE kind='student_appeal' AND target_type=$1 AND target_id=$2
		  AND student_id=$3 AND status='draft' ORDER BY id DESC LIMIT 1
	`, targetType, targetID, actor.UserID).Scan(&existingID)
	if err == nil {
		return existingID, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	prepared, err := s.prepareAppealDraft(ctx, tx, actor, targetType, targetID)
	if err != nil {
		return 0, false, err
	}
	var originalSnapshot any
	if len(prepared.target.RuleSnapshot) > 0 {
		originalSnapshot = prepared.target.RuleSnapshot
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO appeal
		    (class_id,target_type,target_id,student_id,reason,status,kind,round,filed_by,original_score,previous_appeal_id,
		     original_category_key,original_item_key,original_rule_snapshot,origin_batch_id)
		VALUES ($1,$2,$3,$4,'','draft','student_appeal',$5,$4,$6,NULLIF($7,0),$8,$9,$10,
		        COALESCE(
		          (SELECT origin_batch_id FROM appeal WHERE id=NULLIF($7,0)),
		          (CASE WHEN $2='submission' THEN (
		            SELECT origin_batch_id FROM classification_resolution
		             WHERE submission_id=$3 AND origin_batch_id IS NOT NULL
		             ORDER BY created_at DESC,id DESC LIMIT 1
		          ) END),
		          (SELECT o.origin_batch_id
		             FROM objection o
		             LEFT JOIN base_score b ON b.id=$3 AND $2 IN ('base_score','penalty_score')
		            WHERE o.origin_batch_id IS NOT NULL AND o.status IN ('applied','adjusted')
		              AND (($2='submission' AND o.kind='submission' AND o.target_id=$3)
		                OR ($2='base_score' AND o.kind='base' AND o.student_id=b.student_id AND o.scheme_id=b.scheme_id AND o.category_key=b.category_key AND o.item_key=b.item_key)
		                OR ($2='penalty_score' AND o.kind='penalty' AND o.student_id=b.student_id AND o.scheme_id=b.scheme_id AND o.category_key=b.category_key AND o.item_key=b.item_key))
		            ORDER BY o.decided_at DESC,o.id DESC LIMIT 1)
		        ))
		ON CONFLICT (class_id,target_type,target_id,student_id,round)
		  WHERE kind='student_appeal' DO NOTHING
		RETURNING id
	`, actor.ClassID, prepared.target.Type, prepared.target.ID, actor.UserID, prepared.round, *prepared.target.CurrentScore, prepared.previousID,
		prepared.target.Category, prepared.target.ItemKey, originalSnapshot).Scan(&existingID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			SELECT id FROM appeal WHERE kind='student_appeal' AND target_type=$1 AND target_id=$2
			  AND student_id=$3 AND round=$4 AND status='draft'
		`, prepared.target.Type, prepared.target.ID, actor.UserID, prepared.round).Scan(&existingID)
		return existingID, false, err
	}
	return existingID, err == nil, err
}

func (s *Server) createAppealDraft(c *gin.Context) {
	var input appealDraftInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "申诉草稿参数不正确", nil)
		return
	}
	input.TargetType = strings.TrimSpace(input.TargetType)
	if !validAppealTargetType(input.TargetType) {
		writeError(c, http.StatusUnprocessableEntity, "appeal_invalid", "申诉对象不正确", nil)
		return
	}
	tx, actor := mustTx(c), mustActor(c)
	id, created, err := s.ensureAppealDraft(c.Request.Context(), tx, actor, input.TargetType, int64(input.TargetID))
	if err != nil {
		if !writeAppealRequestFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	if created {
		if err := appendAudit(c, tx, "appeal.draft_created", "appeal", strconv.FormatInt(id, 10), nil,
			map[string]any{"status": "draft", "targetType": input.TargetType, "targetId": int64(input.TargetID)}, nil); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"id": strconv.FormatInt(id, 10)})
}

func (s *Server) createAppeal(c *gin.Context) {
	var input createAppealInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "申诉参数不正确", nil)
		return
	}
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.Reason = strings.TrimSpace(input.Reason)
	if !validAppealTargetType(input.TargetType) || len(input.Reason) < 8 || len(input.Reason) > 5000 {
		writeError(c, http.StatusUnprocessableEntity, "appeal_invalid", "申诉对象不正确，理由须为 8—5000 字符", nil)
		return
	}
	tx, actor := mustTx(c), mustActor(c)
	id, _, err := s.ensureAppealDraft(c.Request.Context(), tx, actor, input.TargetType, int64(input.TargetID))
	if err != nil {
		if !writeAppealRequestFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	result, err := s.fileAppealDraft(c, tx, actor, id, appealClaimInput{Reason: input.Reason, Category: input.Category, ItemKey: input.ItemKey, Score: input.Score})
	if err != nil {
		if !writeAppealRequestFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (s *Server) submitAppeal(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input appealSubmitInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "申诉提交参数不正确", nil)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if len(input.Reason) < 8 || len(input.Reason) > 5000 {
		writeError(c, http.StatusUnprocessableEntity, "appeal_invalid", "申诉理由须为 8—5000 字符", nil)
		return
	}
	result, err := s.fileAppealDraft(c, mustTx(c), mustActor(c), id, appealClaimInput{Reason: input.Reason, Category: input.Category, ItemKey: input.ItemKey, Score: input.Score})
	if err != nil {
		if !writeAppealRequestFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) fileAppealDraft(c *gin.Context, tx pgx.Tx, actor Actor, appealID int64, claim appealClaimInput) (gin.H, error) {
	reason := strings.TrimSpace(claim.Reason)
	row, err := loadAppealRecord(c.Request.Context(), tx, appealID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, appealRequestFailure(http.StatusNotFound, "not_found", "申诉草稿不存在")
	}
	if err != nil {
		return nil, err
	}
	if row.Kind != "student_appeal" || row.StudentID != actor.UserID || row.FiledBy != actor.UserID {
		return nil, appealRequestFailure(http.StatusForbidden, "forbidden", "无权提交这条申诉草稿")
	}
	if row.Status != "draft" {
		return nil, appealRequestFailure(http.StatusConflict, "appeal_not_draft", "只有申诉草稿可以提交")
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		return nil, err
	}
	if err := ensureCapability(current.Config, "appeal", time.Now()); err != nil {
		return nil, appealRequestFailure(http.StatusConflict, "appeal_closed", err.Error())
	}
	target, err := loadAppealTarget(c.Request.Context(), tx, row.TargetType, row.TargetID, actor.UserID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, appealRequestFailure(http.StatusNotFound, "not_found", "申诉对象不存在")
	}
	if err != nil {
		return nil, err
	}
	if target.Type == "submission" && target.Status != "scored" && target.Status != "locked" {
		return nil, appealRequestFailure(http.StatusConflict, "not_appealable", "提交条目定分后才能申诉")
	}
	if target.CurrentScore == nil {
		return nil, appealRequestFailure(http.StatusConflict, "not_appealable", "申诉对象尚未形成认定分")
	}
	proposedCategory, proposedItem := target.Category, target.ItemKey
	proposedScore := target.CurrentScore
	if target.Type == "submission" {
		if strings.TrimSpace(claim.Category) != "" || strings.TrimSpace(claim.ItemKey) != "" {
			if strings.TrimSpace(claim.Category) == "" || strings.TrimSpace(claim.ItemKey) == "" {
				return nil, appealRequestFailure(http.StatusUnprocessableEntity, "classification_invalid", "申诉分类必须同时填写大项和小项")
			}
			candidate, err := loadClassificationTarget(c.Request.Context(), tx, target.ID, claim.Category, claim.ItemKey, time.Now())
			if err != nil {
				return nil, appealRequestFailure(http.StatusUnprocessableEntity, "classification_invalid", err.Error())
			}
			proposedCategory, proposedItem = candidate.Category.Key, candidate.Item.Key
		}
		if claim.Score != nil {
			value := scheme.NewPoints(*claim.Score).Float64()
			proposedScore = &value
		}
		candidate, err := loadClassificationTarget(c.Request.Context(), tx, target.ID, proposedCategory, proposedItem, time.Now())
		if err != nil {
			return nil, appealRequestFailure(http.StatusUnprocessableEntity, "classification_invalid", err.Error())
		}
		if proposedScore == nil || scoreWithinRule(candidate.Item.ScoreRule, *proposedScore) != nil {
			return nil, appealRequestFailure(http.StatusUnprocessableEntity, "score_invalid", "申诉分数不符合主张小项的计分规则")
		}
	} else if claim.Score != nil {
		value := scheme.NewPoints(*claim.Score).Float64()
		if err := validateAppealResolution(target, value); err != nil {
			return nil, appealRequestFailure(http.StatusUnprocessableEntity, "score_invalid", err.Error())
		}
		proposedScore = &value
	}
	var active bool
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT EXISTS (SELECT 1 FROM appeal WHERE kind='student_appeal'
		  AND target_type=$1 AND target_id=$2 AND id<>$3
		  AND status IN ('filed','reviewing','escalated'))
	`, row.TargetType, row.TargetID, row.ID).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, appealRequestFailure(http.StatusConflict, "appeal_in_progress", "这一笔申诉还在处理中，请等待结果后再操作")
	}
	if row.Round == 2 {
		if row.PreviousAppealID == nil {
			return nil, appealRequestFailure(http.StatusConflict, "appeal_round_invalid", "第二轮申诉缺少上一轮记录")
		}
		previous, err := loadAppealRecord(c.Request.Context(), tx, *row.PreviousAppealID, false)
		if err != nil {
			return nil, err
		}
		if previous.Status != "resolved" {
			return nil, appealRequestFailure(http.StatusConflict, "appeal_in_progress", "上一轮申诉尚未结案")
		}
	}
	var objectionPending bool
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT EXISTS (SELECT 1 FROM objection
		 WHERE student_id=$1 AND kind=CASE $2 WHEN 'submission' THEN 'submission'
		                           WHEN 'base_score' THEN 'base' ELSE 'penalty' END
		   AND (($2='submission' AND target_id=$3)
		     OR ($2<>'submission' AND scheme_id=$4 AND category_key=$5 AND item_key=$6))
		   AND status='submitted')
	`, actor.UserID, target.Type, target.ID, target.SchemeID, target.Category, target.ItemKey).Scan(&objectionPending); err != nil {
		return nil, err
	}
	if objectionPending {
		return nil, appealRequestFailure(http.StatusConflict, "objection_pending", "这一笔正在复核中，等结果出来再申诉")
	}
	status, route := "reviewing", "original_reviewers"
	reviewers := make([]appealReviewerAssignment, 0, 2)
	governanceMode, err := loadGovernance(c.Request.Context(), tx, actor.ClassID)
	if err != nil {
		return nil, err
	}
	if governanceMode.Mode == "collective" {
		status, route = "escalated", "collective_panel"
	} else if row.Round == 1 {
		reviewers, err = loadOriginalAppealReviewers(c.Request.Context(), tx, target)
		if err != nil {
			return nil, err
		}
		if len(reviewers) == 0 {
			status, route = "escalated", "class_admin"
		}
	} else {
		status, route = "escalated", "class_admin"
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE appeal SET reason=$1,status=$2,original_score=$3,proposed_score=$4,
		       proposed_category_key=$5,proposed_item_key=$6,updated_at=now()
		 WHERE id=$7 AND status='draft'
	`, reason, status, *target.CurrentScore, proposedScore, proposedCategory, proposedItem, row.ID); err != nil {
		return nil, err
	}
	for _, reviewer := range reviewers {
		if _, err := tx.Exec(c.Request.Context(), `
			INSERT INTO appeal_reviewer (class_id,appeal_id,reviewer_id,position) VALUES ($1,$2,$3,$4)
		`, actor.ClassID, row.ID, reviewer.ID, reviewer.Position); err != nil {
			return nil, err
		}
	}
	if target.Type == "submission" {
		nextSubmissionStatus := "appealing"
		if status == "escalated" {
			nextSubmissionStatus = "arbitrating"
		}
		if _, err := tx.Exec(c.Request.Context(), `UPDATE submission SET status=$1,updated_at=now(),lock_version=lock_version+1 WHERE id=$2`, nextSubmissionStatus, target.ID); err != nil {
			return nil, err
		}
	}
	after := map[string]any{"status": status, "kind": "student_appeal", "round": row.Round, "route": route, "targetType": target.Type, "targetId": target.ID}
	if err := appendAudit(c, tx, "appeal.filed", "appeal", strconv.FormatInt(row.ID, 10), map[string]any{"status": "draft"}, after, map[string]any{"assigneeCount": len(reviewers)}); err != nil {
		return nil, err
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AppealFiledEvent(), events.AppealFiledPayload{
		AppealID: row.ID, TargetType: target.Type, TargetID: target.ID,
	}); err != nil {
		return nil, err
	}
	for _, reviewer := range reviewers {
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AppealAssignedEvent(), events.AppealAssignedPayload{
			AppealID: row.ID, HandlerID: reviewer.ID, Round: row.Round,
		}); err != nil {
			return nil, err
		}
	}
	if err := invalidateLatestSettlement(c.Request.Context(), tx, "appeal filed"); err != nil {
		return nil, err
	}
	if row.OriginBatchID != nil {
		if _, err := refreshBlindAuditBatch(c.Request.Context(), tx, actor.ClassID, *row.OriginBatchID); err != nil {
			return nil, err
		}
	} else if err := invalidateStudentBlindAudit(c.Request.Context(), tx, target.SchemeID, target.StudentID, "", "student appeal filed after scorecard review"); err != nil {
		return nil, err
	}
	return gin.H{"id": strconv.FormatInt(row.ID, 10), "status": status, "round": row.Round, "handlers": len(reviewers)}, nil
}

func (s *Server) deleteAppealDraft(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx, actor := mustTx(c), mustActor(c)
	row, err := loadAppealRecord(c.Request.Context(), tx, id, true)
	if notFound(c, err, "申诉草稿") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if row.FiledBy != actor.UserID || row.StudentID != actor.UserID {
		writeError(c, http.StatusForbidden, "forbidden", "无权删除这条申诉草稿", nil)
		return
	}
	if row.Status != "draft" {
		writeError(c, http.StatusConflict, "appeal_not_draft", "只有申诉草稿可以删除", nil)
		return
	}
	rows, err := tx.Query(c.Request.Context(), `SELECT object_key FROM evidence WHERE appeal_id=$1`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	objectKeys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		objectKeys = append(objectKeys, key)
	}
	rows.Close()
	if err := appendAudit(c, tx, "appeal.draft_deleted", "appeal", strconv.FormatInt(id, 10), map[string]any{"status": "draft"}, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM appeal WHERE id=$1 AND status='draft'`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	for _, objectKey := range objectKeys {
		s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	}
	c.Status(http.StatusNoContent)
}

func appealWithdrawable(row appealRecord, decided int) bool {
	return row.Kind == "student_appeal" && (row.Status == "filed" || row.Status == "reviewing") && decided == 0
}

// withdrawAppeal 把尚无人复评的学生申诉退回原草稿。理由、主张和佐证都保留，
// 但撤销审核人分派；提交条目恢复为 scored，重新计算实时成绩并使旧结算保持失效。
func (s *Server) withdrawAppeal(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx, actor := mustTx(c), mustActor(c)
	row, err := loadAppealRecord(c.Request.Context(), tx, id, true)
	if notFound(c, err, "申诉") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if row.FiledBy != actor.UserID || row.StudentID != actor.UserID {
		writeError(c, http.StatusForbidden, "forbidden", "只能撤回自己的申诉", nil)
		return
	}
	var collective bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM governance_proposal WHERE action='appeal' AND target_id=$1)`, id).Scan(&collective); err != nil {
		writeServiceError(c, err)
		return
	}
	if collective {
		writeError(c, http.StatusConflict, "collective_panel_frozen", "申诉已进入独立共治评审，不能撤回重抽小组；需要补充事实时请在班级共治中补证", nil)
		return
	}
	var decided int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*)::int FROM appeal_reviewer WHERE appeal_id=$1 AND decided_at IS NOT NULL
	`, id).Scan(&decided); err != nil {
		writeServiceError(c, err)
		return
	}
	if !appealWithdrawable(row, decided) {
		writeError(c, http.StatusConflict, "appeal_not_withdrawable", "只有尚无人复评的申诉可以撤回修改", nil)
		return
	}
	if row.TargetType == "submission" {
		var status string
		if err := tx.QueryRow(c.Request.Context(), `
			SELECT status FROM submission WHERE id=$1 AND student_id=$2 FOR UPDATE
		`, row.TargetID, actor.UserID).Scan(&status); err != nil {
			if notFound(c, err, "申诉对象") {
				return
			}
			writeServiceError(c, err)
			return
		}
		if status != "appealing" {
			writeError(c, http.StatusConflict, "appeal_target_changed", "申诉对象状态已经变化，请刷新后重试", nil)
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM appeal_reviewer WHERE appeal_id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE appeal SET status='draft',updated_at=now() WHERE id=$1 AND status IN ('filed','reviewing')
	`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if row.TargetType == "submission" {
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE submission
			   SET status='scored',updated_at=now(),lock_version=lock_version+1
			 WHERE id=$1 AND status='appealing'
		`, row.TargetID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "appeal.withdrawn", "appeal", strconv.FormatInt(id, 10),
		map[string]any{"status": row.Status}, map[string]any{"status": "draft"}, map[string]any{"removedAssignments": true}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateLatestSettlement(c.Request.Context(), tx, "student appeal withdrawn to draft"); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": "draft"})
}

type appealReviewerAssignment struct {
	ID       int64
	Position int
}

func loadOriginalAppealReviewers(ctx context.Context, tx pgx.Tx, target appealTarget) ([]appealReviewerAssignment, error) {
	query := `
		SELECT reviewer_id,row_number() OVER (ORDER BY reviewer_id)::integer
		  FROM (
			SELECT DISTINCT r.reviewer_id FROM review r
			JOIN app_user u ON u.id=r.reviewer_id
			 WHERE r.submission_id=$1 AND r.superseded_at IS NULL
			   AND u.status='active' AND u.role IN ('group','class_admin') AND r.reviewer_id<>$2
		  ) original
		 ORDER BY reviewer_id LIMIT 2
	`
	args := []any{target.ID, target.StudentID}
	if target.Type != "submission" {
		query = `
			SELECT candidate.reviewer_id,1
			  FROM (
			    SELECT o.proposer_id AS reviewer_id,0 AS priority
			      FROM objection o
			     WHERE o.student_id=$3 AND o.scheme_id=$1 AND o.category_key=$2 AND o.item_key=$4
			       AND o.status IN ('applied','adjusted')
			     ORDER BY o.decided_at DESC,o.id DESC LIMIT 1
			  ) candidate
			  JOIN app_user u ON u.id=candidate.reviewer_id
			 WHERE u.status='active' AND u.role IN ('group','class_admin') AND candidate.reviewer_id<>$3
			 UNION ALL
			SELECT $5::bigint,1
			 WHERE NOT EXISTS (
			   SELECT 1 FROM objection o WHERE o.student_id=$3 AND o.scheme_id=$1
			    AND o.category_key=$2 AND o.item_key=$4 AND o.status IN ('applied','adjusted')
			 ) AND EXISTS (
			   SELECT 1 FROM app_user u WHERE u.id=$5 AND u.status='active' AND u.role IN ('group','class_admin') AND u.id<>$3
			 )
			 LIMIT 1
		`
		args = []any{target.SchemeID, target.Category, target.StudentID, target.ItemKey, target.RecorderID}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reviewers := make([]appealReviewerAssignment, 0, 2)
	for rows.Next() {
		var reviewer appealReviewerAssignment
		if err := rows.Scan(&reviewer.ID, &reviewer.Position); err != nil {
			return nil, err
		}
		reviewers = append(reviewers, reviewer)
	}
	return reviewers, rows.Err()
}

func nextStudentAppealRound(previous *appealRecord) (int, error) {
	if previous == nil {
		return 1, nil
	}
	if previous.Status == "final" {
		return 0, errors.New("该认定分已经终裁，不能再次申诉")
	}
	if previous.Status != "resolved" {
		return 0, errors.New("上一轮申诉尚未结案")
	}
	if previous.Round == 1 {
		return 2, nil
	}
	return 0, errors.New("同一认定分最多申诉两次")
}

func reconcileAppealScores(expected int, scores []float64) (string, *float64, bool, error) {
	if expected < 1 || expected > 2 {
		return "", nil, false, errors.New("申诉复核人数量必须是一人或两人")
	}
	if len(scores) > expected {
		return "", nil, false, errors.New("申诉复核结果超过预期人数")
	}
	if len(scores) < expected {
		return "reviewing", nil, false, nil
	}
	for _, score := range scores[1:] {
		if scheme.NewPoints(score) != scheme.NewPoints(scores[0]) {
			return "escalated", nil, true, nil
		}
	}
	value := scores[0]
	return "resolved", &value, false, nil
}

type appealReviewOutcome struct {
	Category     string
	ItemKey      string
	RuleSnapshot []byte
	Score        float64
}

func reconcileAppealOutcomes(expected int, outcomes []appealReviewOutcome) (string, *appealReviewOutcome, bool, error) {
	if expected < 1 || expected > 2 {
		return "", nil, false, errors.New("申诉复核人数量必须是一人或两人")
	}
	if len(outcomes) > expected {
		return "", nil, false, errors.New("申诉复核结果超过预期人数")
	}
	if len(outcomes) < expected {
		return "reviewing", nil, false, nil
	}
	first := outcomes[0]
	for _, outcome := range outcomes[1:] {
		if outcome.Category != first.Category || outcome.ItemKey != first.ItemKey ||
			scheme.NewPoints(outcome.Score) != scheme.NewPoints(first.Score) {
			return "escalated", nil, true, nil
		}
	}
	return "resolved", &first, false, nil
}

type appealRecord struct {
	ID                 int64
	Kind               string
	Round              int
	TargetType         string
	TargetID           int64
	StudentID          int64
	FiledBy            int64
	Reason             string
	Status             string
	HandlerID          *int64
	OriginalScore      *float64
	ProposedScore      *float64
	ProposalReason     *string
	ResolutionScore    *float64
	ResolutionReason   *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ResolvedAt         *time.Time
	PreviousAppealID   *int64
	OriginalCategory   *string
	OriginalItem       *string
	ProposedCategory   *string
	ProposedItem       *string
	OriginalSnapshot   []byte
	ResolutionCategory *string
	ResolutionItem     *string
	ResolutionSnapshot []byte
	OriginBatchID      *string
}

func loadAppealRecord(ctx context.Context, tx pgx.Tx, id int64, lock bool) (appealRecord, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE"
	}
	var row appealRecord
	err := tx.QueryRow(ctx, `
		SELECT id,kind,round,target_type,target_id,student_id,filed_by,reason,status,handler_id,
		       original_score::float8,proposed_score::float8,proposal_reason,resolution_score::float8,
		       resolution_reason,created_at,updated_at,resolved_at,previous_appeal_id,
		       original_category_key,original_item_key,proposed_category_key,proposed_item_key,
		       original_rule_snapshot,resolution_category_key,resolution_item_key,resolution_rule_snapshot,
		       origin_batch_id::text
		  FROM appeal WHERE id=$1`+lockSQL, id).Scan(
		&row.ID, &row.Kind, &row.Round, &row.TargetType, &row.TargetID, &row.StudentID, &row.FiledBy,
		&row.Reason, &row.Status, &row.HandlerID, &row.OriginalScore, &row.ProposedScore,
		&row.ProposalReason, &row.ResolutionScore, &row.ResolutionReason, &row.CreatedAt,
		&row.UpdatedAt, &row.ResolvedAt, &row.PreviousAppealID,
		&row.OriginalCategory, &row.OriginalItem, &row.ProposedCategory, &row.ProposedItem,
		&row.OriginalSnapshot, &row.ResolutionCategory, &row.ResolutionItem, &row.ResolutionSnapshot,
		&row.OriginBatchID,
	)
	return row, err
}

func appealJSON(row appealRecord, target appealTarget, studentName, studentSID string, handlerName *string, showHandler bool) gin.H {
	item := gin.H{
		"id": strconv.FormatInt(row.ID, 10), "kind": row.Kind, "round": row.Round,
		"targetType": row.TargetType,
		"targetId":   strconv.FormatInt(row.TargetID, 10), "target": target.Title,
		"category": target.Category, "itemKey": target.ItemKey, "student": studentName,
		"studentId": studentSID, "reason": row.Reason, "status": row.Status,
		"currentScore": target.CurrentScore, "baselineScore": row.OriginalScore,
		"proposedScore": row.ProposedScore, "proposalReason": row.ProposalReason,
		"resolutionScore":  row.ResolutionScore,
		"resolutionReason": row.ResolutionReason, "createdAt": row.CreatedAt,
		"updatedAt": row.UpdatedAt, "resolvedAt": row.ResolvedAt,
		"originalCategory": row.OriginalCategory, "originalItemKey": row.OriginalItem,
		"proposedCategory": row.ProposedCategory, "proposedItemKey": row.ProposedItem,
		"resolutionCategory": row.ResolutionCategory, "resolutionItemKey": row.ResolutionItem,
		"originBatchId": row.OriginBatchID,
	}
	if row.PreviousAppealID != nil {
		item["previousAppealId"] = strconv.FormatInt(*row.PreviousAppealID, 10)
	}
	if row.Kind == "student_appeal" {
		if row.Round == 1 {
			item["route"] = "original_reviewers"
		} else {
			item["route"] = "class_admin"
		}
	} else {
		item["route"] = "class_admin"
	}
	/* 终裁人是班级管理员（或副班管），不是双评审核人。单盲只挡住复评人姓名，
	   终裁结论对学生也要能看见是谁签的。 */
	if handlerName != nil && (showHandler || row.Status == "final") {
		item["handler"] = *handlerName
	}
	if showHandler && row.HandlerID != nil {
		item["handlerId"] = strconv.FormatInt(*row.HandlerID, 10)
	}
	return item
}

func appealHandlersJSON(ctx context.Context, tx pgx.Tx, appealID int64, actor Actor, adjudicator bool) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT ar.reviewer_id,u.name,u.sid,ar.decision,ar.score::float8,ar.reason,
		       ar.spent_seconds,ar.decided_at,ar.category_key,ar.item_key,
		       bool_and(ar.decided_at IS NOT NULL) OVER () AS all_decided
		  FROM appeal_reviewer ar JOIN app_user u ON u.id=ar.reviewer_id
		 WHERE ar.appeal_id=$1 ORDER BY ar.position
	`, appealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0, 2)
	for rows.Next() {
		var reviewerID int64
		var name, sid string
		var decision, reason *string
		var score *float64
		var spent *int
		var decidedAt *time.Time
		var category, itemKey *string
		var allDecided bool
		if err := rows.Scan(&reviewerID, &name, &sid, &decision, &score, &reason, &spent, &decidedAt, &category, &itemKey, &allDecided); err != nil {
			return nil, err
		}
		mine := reviewerID == actor.UserID
		item := gin.H{
			"id": strconv.FormatInt(reviewerID, 10), "decided": decidedAt != nil,
			"rereview": nil,
		}
		if adjudicator {
			item["name"], item["sid"] = name, sid
		}
		if actor.Role == "group" || actor.Role == "class_admin" {
			item["mine"] = mine
		}
		if decision != nil && (actor.Role == "student" || adjudicator || mine || allDecided) {
			item["rereview"] = gin.H{
				"decision": *decision, "score": score, "reason": reason,
				"category": category, "itemKey": itemKey, "spentSeconds": spent, "at": decidedAt,
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func attachAppealHandlers(ctx context.Context, tx pgx.Tx, item gin.H, appealID int64, actor Actor, adjudicator bool) error {
	var collective bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governance_proposal WHERE action='appeal' AND target_id=$1)`, appealID).Scan(&collective); err != nil {
		return err
	}
	if collective {
		// The execution actor is only the member whose last opinion completed
		// the decision, never a publicly named final adjudicator.
		item["collective"] = true
		item["route"] = "collective"
		item["handler"] = "共治评审小组"
		delete(item, "handlerId")
		item["handlers"] = []gin.H{}
		return nil
	}
	handlers, err := appealHandlersJSON(ctx, tx, appealID, actor, adjudicator)
	if err != nil {
		return err
	}
	item["handlers"] = handlers
	return nil
}

func appealIdentity(ctx context.Context, tx pgx.Tx, row appealRecord) (string, string, *string, error) {
	var studentName, studentSID string
	var handlerName *string
	err := tx.QueryRow(ctx, `
		SELECT student.name,student.sid,handler.name
		  FROM app_user student LEFT JOIN app_user handler ON handler.id=$2
		 WHERE student.id=$1
	`, row.StudentID, row.HandlerID).Scan(&studentName, &studentSID, &handlerName)
	return studentName, studentSID, handlerName, err
}

func (s *Server) appeals(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT id FROM appeal
		 WHERE kind='student_appeal' AND student_id=$1 AND status<>'draft'
		 ORDER BY created_at DESC,id DESC
	`, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	items := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		row, err := loadAppealRecord(c.Request.Context(), tx, id, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		target, err := loadAppealTarget(c.Request.Context(), tx, row.TargetType, row.TargetID, row.StudentID, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		name, sid, handler, err := appealIdentity(c.Request.Context(), tx, row)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		item := appealJSON(row, target, name, sid, handler, false)
		if err := attachAppealHandlers(c.Request.Context(), tx, item, row.ID, actor, actor.Role == "class_admin"); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, item)
	}
	if err := appendAudit(c, tx, "appeal.list", "appeal", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func appealEvidence(ctx context.Context, tx pgx.Tx, appealID int64) ([]gin.H, error) {
	return appealEvidenceKind(ctx, tx, appealID, "claim")
}

func appealNoteEvidence(ctx context.Context, tx pgx.Tx, appealID int64) ([]gin.H, error) {
	return appealEvidenceKind(ctx, tx, appealID, "note")
}

func appealNoteEvidenceForReviewer(ctx context.Context, tx pgx.Tx, appealID, reviewerID int64) ([]gin.H, error) {
	return appealEvidenceKindForReviewer(ctx, tx, appealID, "note", &reviewerID)
}

func appealEvidenceKind(ctx context.Context, tx pgx.Tx, appealID int64, kind string) ([]gin.H, error) {
	return appealEvidenceKindForReviewer(ctx, tx, appealID, kind, nil)
}

func appealEvidenceKindForReviewer(ctx context.Context, tx pgx.Tx, appealID int64, kind string, reviewerID *int64) ([]gin.H, error) {
	var reviewer any
	if reviewerID != nil {
		reviewer = *reviewerID
	}
	rows, err := tx.Query(ctx, `
		SELECT id,filename,media_type,size_bytes,sha256,status,created_at
		  FROM evidence
		 WHERE appeal_id=$1 AND kind=$2
		   AND ($3::bigint IS NULL OR created_by=$3 OR (
		       NOT EXISTS (
		           SELECT 1 FROM appeal_reviewer
		            WHERE appeal_id=$1 AND decided_at IS NULL
		       ) AND EXISTS (
		           SELECT 1 FROM appeal_reviewer
		            WHERE appeal_id=$1 AND reviewer_id=evidence.created_by
		       )
		   ))
		 ORDER BY id
	`, appealID, kind, reviewer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, size int64
		var filename, mediaType, status string
		var sha *string
		var createdAt time.Time
		if err := rows.Scan(&id, &filename, &mediaType, &size, &sha, &status, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "name": filename, "mediaType": mediaType, "sizeBytes": size, "sha256": sha, "status": status, "uploadedAt": createdAt})
	}
	return items, rows.Err()
}

func appealTrail(ctx context.Context, tx pgx.Tx, appealID int64) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT action,after_data,metadata,created_at
		  FROM audit_log WHERE resource_type='appeal' AND resource_id=$1
		 ORDER BY created_at,id
	`, strconv.FormatInt(appealID, 10))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var action string
		var after, metadata []byte
		var at time.Time
		if err := rows.Scan(&action, &after, &metadata, &at); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"action": action, "after": json.RawMessage(after), "metadata": json.RawMessage(metadata), "at": at})
	}
	return items, rows.Err()
}

func (s *Server) appeal(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	row, err := loadAppealRecord(c.Request.Context(), tx, id, false)
	if notFound(c, err, "申诉") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var assignedReviewer bool
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT EXISTS (SELECT 1 FROM appeal_reviewer WHERE appeal_id=$1 AND reviewer_id=$2)
	`, id, actor.UserID).Scan(&assignedReviewer); err != nil {
		writeServiceError(c, err)
		return
	}
	allowed := actor.UserID == row.StudentID || actor.UserID == row.FiledBy || actor.Role == "class_admin" ||
		assignedReviewer || (row.HandlerID != nil && actor.UserID == *row.HandlerID)
	deputyReader, err := deputyCanReadTarget(c.Request.Context(), tx, actor, row.StudentID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if isDeputyAdjudication(c) && (!deputyReader || row.Status == "draft") {
		writeError(c, http.StatusForbidden, "deputy_scope", "副班管只能读取有关班管本人的已提交申诉", nil)
		return
	}
	// Deputy authority does not expose an unsubmitted student draft.
	deputyReader = deputyReader && row.Status != "draft"
	allowed = allowed || deputyReader
	adjudicator := actor.Role == "class_admin" || deputyReader
	if !allowed {
		writeError(c, http.StatusForbidden, "forbidden", "无权读取这条申诉", nil)
		return
	}
	target, err := loadAppealTarget(c.Request.Context(), tx, row.TargetType, row.TargetID, row.StudentID, false)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	studentName, studentSID, handlerName, err := appealIdentity(c.Request.Context(), tx, row)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	supplements, err := appealEvidence(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var noteEvidence []gin.H
	if actor.Role == "group" && !adjudicator {
		noteEvidence, err = appealNoteEvidenceForReviewer(c.Request.Context(), tx, id, actor.UserID)
	} else {
		noteEvidence, err = appealNoteEvidence(c.Request.Context(), tx, id)
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	trail, err := appealTrail(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response := appealJSON(row, target, studentName, studentSID, handlerName, adjudicator)
	if adjudicator {
		withAdjudicationPermission(response, actor, row.StudentID)
	}
	if err := attachAppealHandlers(c.Request.Context(), tx, response, row.ID, actor, adjudicator); err != nil {
		writeServiceError(c, err)
		return
	}
	response["evidence"] = supplements
	response["trail"] = trail
	if target.Type == "submission" {
		originalEvidence, err := submissionEvidence(c.Request.Context(), tx, target.ID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		originalNoteEvidence, err := submissionNoteEvidence(c.Request.Context(), tx, target.ID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		noteEvidence = append(noteEvidence, originalNoteEvidence...)
		response["originalEvidence"] = originalEvidence
		response["ruleSnapshot"] = json.RawMessage(target.RuleSnapshot)
		if !adjudicator {
			reviews, err := studentVisibleReviews(c.Request.Context(), tx, target.ID)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			response["originalReviews"] = reviews
		} else {
			rows, err := tx.Query(c.Request.Context(), `
				SELECT r.id,u.name,u.sid,r.decision,r.score::float8,r.reason,r.spent_seconds,r.created_at
				  FROM review r JOIN app_user u ON u.id=r.reviewer_id
				 WHERE r.submission_id=$1 AND r.superseded_at IS NULL ORDER BY r.created_at,r.id
			`, target.ID)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			reviews := make([]gin.H, 0)
			for rows.Next() {
				var reviewID int64
				var name, sid, decision, reason string
				var score float64
				var spent int
				var at time.Time
				if err := rows.Scan(&reviewID, &name, &sid, &decision, &score, &reason, &spent, &at); err != nil {
					rows.Close()
					writeServiceError(c, err)
					return
				}
				reviews = append(reviews, gin.H{"id": strconv.FormatInt(reviewID, 10), "reviewer": name, "reviewerSid": sid, "decision": decision, "score": score, "reason": reason, "spentSeconds": spent, "at": at})
			}
			rows.Close()
			response["originalReviews"] = reviews
		}
	} else {
		response["basis"] = target.Basis
		response["fullScore"] = target.FullScore
	}
	if row.PreviousAppealID != nil && (adjudicator || actor.UserID == row.StudentID) {
		previous, err := loadAppealRecord(c.Request.Context(), tx, *row.PreviousAppealID, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		previousHandlers, err := appealHandlersJSON(c.Request.Context(), tx, previous.ID, actor, adjudicator)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		previousNoteEvidence, err := appealNoteEvidence(c.Request.Context(), tx, previous.ID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		noteEvidence = append(noteEvidence, previousNoteEvidence...)
		response["previousRound"] = gin.H{
			"id": strconv.FormatInt(previous.ID, 10), "reason": previous.Reason,
			"resolutionScore": previous.ResolutionScore, "resolutionReason": previous.ResolutionReason,
			"resolvedAt": previous.ResolvedAt, "handlers": previousHandlers,
		}
	}
	response["noteEvidence"] = noteEvidence
	if err := appendAudit(c, tx, "appeal.read", "appeal", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (s *Server) presignAppealEvidence(c *gin.Context) {
	appealID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input evidenceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "文件参数不正确", nil)
		return
	}
	input.Filename = safeFilename(input.Filename)
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	if input.Filename == "" || input.SizeBytes <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_file", "文件名和大小不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	appealRow, err := loadAppealRecord(c.Request.Context(), tx, appealID, true)
	if notFound(c, err, "申诉") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if appealRow.StudentID != actor.UserID {
		writeError(c, http.StatusForbidden, "forbidden", "只有申诉人可以补充佐证", nil)
		return
	}
	if !appealEvidenceEditable(appealRow) {
		writeError(c, http.StatusConflict, "appeal_not_editable", "申诉已进入裁定，不能再补充佐证", nil)
		return
	}
	var rule *scheme.EvidenceRule
	if appealRow.TargetType == "submission" {
		target, err := loadAppealTarget(c.Request.Context(), tx, appealRow.TargetType, appealRow.TargetID, actor.UserID, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		var snapshot ruleSnapshot
		if json.Unmarshal(target.RuleSnapshot, &snapshot) == nil {
			rule = snapshot.Item.Evidence
		}
	}
	flags := s.runtimeFlags(c.Request.Context())
	if err := validateEvidencePolicy(rule, input, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "evidence_invalid", err.Error(), nil)
		return
	}
	if err := reserveEvidenceQuota(c.Request.Context(), tx, actor.UserID, input.SizeBytes, flags.EvidenceDailyMB); err != nil {
		if errors.Is(err, errEvidenceDailyQuotaExceeded) {
			writeError(c, http.StatusTooManyRequests, "evidence_daily_quota", "今天创建的佐证与备注附件已达到平台字节上限", nil)
		} else {
			writeServiceError(c, err)
		}
		return
	}
	objectKey := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/appeal-" + strconv.FormatInt(appealID, 10) + "/" + randomObjectPart() + objectKeySuffix(input.Filename)
	upload, err := s.deps.Objects.PresignUpload(c.Request.Context(), objectKey, input.MediaType, input.SizeBytes, 15*time.Minute)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var evidenceID int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO evidence (class_id,appeal_id,kind,object_key,filename,media_type,size_bytes,sha256,status,created_by)
		VALUES ($1,$2,'claim',$3,$4,$5,$6,NULLIF($7,''),'pending',$8) RETURNING id
	`, actor.ClassID, appealID, objectKey, input.Filename, input.MediaType, input.SizeBytes, input.SHA256, actor.UserID).Scan(&evidenceID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "appeal.evidence_presigned", "appeal", strconv.FormatInt(appealID, 10), nil, nil, map[string]any{"evidenceId": evidenceID, "filename": input.Filename}); err != nil {
		writeServiceError(c, err)
		return
	}
	response := uploadPolicyJSON(upload)
	response["evidenceId"] = strconv.FormatInt(evidenceID, 10)
	response["completeUrl"] = "/api/v1/appeals/" + strconv.FormatInt(appealID, 10) + "/evidence/" + strconv.FormatInt(evidenceID, 10) + "/complete"
	c.JSON(http.StatusCreated, response)
}

func appealEvidenceEditable(row appealRecord) bool {
	if row.Kind != "student_appeal" || row.ResolvedAt != nil {
		return false
	}
	switch row.Status {
	case "draft", "filed", "reviewing", "escalated":
		return true
	default:
		return false
	}
}

func (s *Server) completeAppealEvidence(c *gin.Context) {
	appealID, ok := pathID(c, "id")
	if !ok {
		return
	}
	evidenceID, ok := pathID(c, "eid")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey, filename, mediaType, status string
	var declaredSize int64
	err := tx.QueryRow(c.Request.Context(), `
		SELECT e.object_key,e.filename,e.media_type,e.size_bytes,e.status
		  FROM evidence e JOIN appeal a ON a.id=e.appeal_id
		 WHERE e.id=$1 AND e.appeal_id=$2 AND e.kind='claim' AND a.student_id=$3
		 FOR UPDATE OF e
	`, evidenceID, appealID, actor.UserID).Scan(&objectKey, &filename, &mediaType, &declaredSize, &status)
	if notFound(c, err, "申诉佐证") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status == "ready" {
		c.JSON(http.StatusOK, gin.H{"evidenceId": strconv.FormatInt(evidenceID, 10), "status": "ready", "filename": filename, "mediaType": mediaType})
		return
	}
	result, ok := s.settleEvidenceUpload(c, evidenceID, objectKey, filename, mediaType, declaredSize)
	if !ok {
		return
	}
	if err := appendAudit(c, tx, "appeal.evidence_completed", "appeal", strconv.FormatInt(appealID, 10), nil, nil, map[string]any{"evidenceId": evidenceID, "filename": result.Filename}); err != nil {
		writeServiceError(c, err)
		return
	}
	s.scheduleStorageReconcile(c, actor.ClassID)
	c.JSON(http.StatusOK, gin.H{"evidenceId": strconv.FormatInt(evidenceID, 10), "status": "ready", "filename": result.Filename, "mediaType": result.MediaType})
}

func (s *Server) deleteAppealEvidence(c *gin.Context) {
	appealID, ok := pathID(c, "id")
	if !ok {
		return
	}
	evidenceID, ok := pathID(c, "eid")
	if !ok {
		return
	}
	tx, actor := mustTx(c), mustActor(c)
	appealRow, err := loadAppealRecord(c.Request.Context(), tx, appealID, true)
	if notFound(c, err, "申诉") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if appealRow.StudentID != actor.UserID || !appealEvidenceEditable(appealRow) {
		writeError(c, http.StatusForbidden, "forbidden", "无权删除这份未完成的申诉佐证", nil)
		return
	}
	var objectKey string
	err = tx.QueryRow(c.Request.Context(), `
		SELECT object_key FROM evidence
		 WHERE id=$1 AND appeal_id=$2 AND kind='claim' AND created_by=$3 AND status<>'ready'
		 FOR UPDATE
	`, evidenceID, appealID, actor.UserID).Scan(&objectKey)
	if notFound(c, err, "未完成的申诉佐证") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM evidence WHERE id=$1`, evidenceID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "appeal.evidence_deleted", "appeal", strconv.FormatInt(appealID, 10), nil, nil,
		map[string]any{"evidenceId": evidenceID}); err != nil {
		writeServiceError(c, err)
		return
	}
	s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	c.Status(http.StatusNoContent)
}

func (s *Server) reviewerAppeals(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	scope := strings.TrimSpace(c.DefaultQuery("scope", "pending"))
	if scope != "pending" && scope != "done" && scope != "all" {
		writeError(c, http.StatusBadRequest, "invalid_scope", "申诉列表范围不正确", nil)
		return
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT a.id,ar.decided_at IS NOT NULL
		  FROM appeal a JOIN appeal_reviewer ar ON ar.appeal_id=a.id
		 WHERE ar.reviewer_id=$1 AND a.kind='student_appeal' AND a.round=1
		   AND ($2='all' OR ($2='pending' AND ar.decided_at IS NULL AND a.status='reviewing')
		                OR ($2='done' AND ar.decided_at IS NOT NULL))
		 ORDER BY ar.decided_at NULLS FIRST,a.created_at,a.id
	`, actor.UserID, scope)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	type assignedAppeal struct {
		ID      int64
		Decided bool
	}
	assigned := make([]assignedAppeal, 0)
	for rows.Next() {
		var item assignedAppeal
		if err := rows.Scan(&item.ID, &item.Decided); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		assigned = append(assigned, item)
	}
	rows.Close()
	items := make([]gin.H, 0, len(assigned))
	for _, assignedItem := range assigned {
		row, err := loadAppealRecord(c.Request.Context(), tx, assignedItem.ID, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		target, err := loadAppealTarget(c.Request.Context(), tx, row.TargetType, row.TargetID, row.StudentID, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		name, sid, handler, err := appealIdentity(c.Request.Context(), tx, row)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		item := appealJSON(row, target, name, sid, handler, false)
		if err := attachAppealHandlers(c.Request.Context(), tx, item, row.ID, actor, actor.Role == "class_admin"); err != nil {
			writeServiceError(c, err)
			return
		}
		item["type"] = "appeal"
		item["reviewedByMe"] = assignedItem.Decided
		var decidedCount, expectedCount int
		if err := tx.QueryRow(c.Request.Context(), `
			SELECT count(*) FILTER (WHERE score IS NOT NULL),count(*) FROM appeal_reviewer WHERE appeal_id=$1
		`, row.ID).Scan(&decidedCount, &expectedCount); err != nil {
			writeServiceError(c, err)
			return
		}
		item["reviewCount"] = decidedCount
		item["expectedReviews"] = expectedCount
		items = append(items, item)
	}
	if err := appendAudit(c, tx, "review.appeal_task_list", "appeal", "", nil, nil, map[string]any{"count": len(items), "scope": scope}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "tab": "appeal"})
}

func (s *Server) adminAppeals(c *gin.Context) {
	tx := mustTx(c)
	status := strings.TrimSpace(c.Query("status"))
	studentQuery := strings.TrimSpace(c.Query("student"))
	category := strings.TrimSpace(c.Query("category"))
	query := strings.TrimSpace(c.Query("q"))
	// origin_batch_id marks an appeal that came out of a scorecard audit: the
	// student is contesting something that batch changed, and the batch stays
	// at 'resolving' until this appeal closes. Those are the ones an
	// administrator has to clear to release a final table, so let them be
	// listed on their own instead of being buried among ordinary appeals.
	origin := strings.TrimSpace(c.Query("origin"))
	if origin != "" && origin != "batch" && origin != "direct" {
		writeError(c, http.StatusBadRequest, "invalid_origin", "申诉来源筛选只能是 batch 或 direct", nil)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT a.id
		  FROM appeal a JOIN app_user student ON student.id=a.student_id
		 WHERE a.kind='student_appeal' AND a.status<>'draft' AND ($1='' OR a.status=$1)
		   AND ($2='' OR student.sid ILIKE '%'||$2||'%' OR student.name ILIKE '%'||$2||'%')
		   AND ($3='' OR EXISTS (
		       SELECT 1 FROM submission s WHERE a.target_type='submission' AND s.id=a.target_id AND s.category_key=$3
		   ))
		   AND ($4='' OR a.reason ILIKE '%'||$4||'%' OR EXISTS (
		       SELECT 1 FROM submission s WHERE a.target_type='submission' AND s.id=a.target_id
		        AND (s.title ILIKE '%'||$4||'%' OR s.item_key ILIKE '%'||$4||'%')
		   ))
		   AND ($7='' OR ($7='batch' AND a.origin_batch_id IS NOT NULL)
		             OR ($7='direct' AND a.origin_batch_id IS NULL))
		   AND (NOT $8::boolean OR student.role='class_admin')
		 ORDER BY CASE a.status WHEN 'escalated' THEN 0 WHEN 'reviewing' THEN 1 ELSE 2 END,
		          a.updated_at DESC,a.id DESC
		 LIMIT $5 OFFSET $6
	`, status, studentQuery, category, query, pageSize, (page-1)*pageSize, origin, isDeputyAdjudication(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	items := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		row, err := loadAppealRecord(c.Request.Context(), tx, id, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		target, err := loadAppealTarget(c.Request.Context(), tx, row.TargetType, row.TargetID, row.StudentID, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		name, sid, handler, err := appealIdentity(c.Request.Context(), tx, row)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		item := appealJSON(row, target, name, sid, handler, true)
		withAdjudicationPermission(item, mustActor(c), row.StudentID)
		if err := attachAppealHandlers(c.Request.Context(), tx, item, row.ID, mustActor(c), true); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, item)
	}
	var total int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM appeal a JOIN app_user student ON student.id=a.student_id
		 WHERE a.kind='student_appeal' AND a.status<>'draft' AND ($1='' OR a.status=$1)
		   AND ($2='' OR student.sid ILIKE '%'||$2||'%' OR student.name ILIKE '%'||$2||'%')
		   AND ($3='' OR EXISTS (
		       SELECT 1 FROM submission s WHERE a.target_type='submission' AND s.id=a.target_id AND s.category_key=$3
		   ))
		   AND ($4='' OR a.reason ILIKE '%'||$4||'%' OR EXISTS (
		       SELECT 1 FROM submission s WHERE a.target_type='submission' AND s.id=a.target_id
		        AND (s.title ILIKE '%'||$4||'%' OR s.item_key ILIKE '%'||$4||'%')
		   ))
		   AND ($5='' OR ($5='batch' AND a.origin_batch_id IS NOT NULL)
		             OR ($5='direct' AND a.origin_batch_id IS NULL))
		   AND (NOT $6::boolean OR student.role='class_admin')
	`, status, studentQuery, category, query, origin, isDeputyAdjudication(c)).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "appeal.admin_list", "appeal", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

type appealResolutionInput struct {
	Decision     string   `json:"decision"`
	Category     string   `json:"category"`
	ItemKey      string   `json:"itemKey"`
	Score        *float64 `json:"score"`
	Reason       string   `json:"reason"`
	SpentSeconds int      `json:"spentSeconds"`
}

func resolveAppealOutcome(ctx context.Context, tx pgx.Tx, row appealRecord, target appealTarget, input appealResolutionInput) (appealReviewOutcome, error) {
	if input.Score == nil {
		return appealReviewOutcome{}, errors.New("必须填写认定分")
	}
	outcome := appealReviewOutcome{Category: target.Category, ItemKey: target.ItemKey, RuleSnapshot: target.RuleSnapshot, Score: scheme.NewPoints(*input.Score).Float64()}
	if target.Type != "submission" {
		if err := validateAppealResolution(target, outcome.Score); err != nil {
			return appealReviewOutcome{}, err
		}
		return outcome, nil
	}
	category, itemKey := strings.TrimSpace(input.Category), strings.TrimSpace(input.ItemKey)
	if category == "" && itemKey == "" {
		if row.ProposedCategory != nil && row.ProposedItem != nil {
			category, itemKey = *row.ProposedCategory, *row.ProposedItem
		} else {
			category, itemKey = target.Category, target.ItemKey
		}
	} else if category == "" || itemKey == "" {
		return appealReviewOutcome{}, errors.New("复评分类别必须同时填写大项和小项")
	}
	candidate, err := loadClassificationTarget(ctx, tx, target.ID, category, itemKey, time.Now())
	if err != nil {
		return appealReviewOutcome{}, err
	}
	if err := scoreWithinRule(candidate.Item.ScoreRule, outcome.Score); err != nil {
		return appealReviewOutcome{}, err
	}
	outcome.Category, outcome.ItemKey, outcome.RuleSnapshot = candidate.Category.Key, candidate.Item.Key, candidate.Snapshot
	return outcome, nil
}

func upholdOutcomeMatches(decision string, outcome appealReviewOutcome, row appealRecord) bool {
	if decision != "uphold" {
		return true
	}
	return row.OriginalScore != nil && row.OriginalCategory != nil && row.OriginalItem != nil &&
		scheme.NewPoints(outcome.Score) == scheme.NewPoints(*row.OriginalScore) &&
		outcome.Category == *row.OriginalCategory && outcome.ItemKey == *row.OriginalItem
}

func validateAppealResolution(target appealTarget, score float64) error {
	points := scheme.NewPoints(score)
	switch target.Type {
	case "submission":
		var snapshot ruleSnapshot
		if err := json.Unmarshal(target.RuleSnapshot, &snapshot); err != nil {
			return errors.New("规则快照损坏")
		}
		return scoreWithinRule(snapshot.Item.ScoreRule, score)
	case "base_score":
		if target.FullScore == nil || points < 0 || points > scheme.NewPoints(*target.FullScore) {
			return errors.New("基础项裁定分必须在 0 与满分之间")
		}
	case "penalty_score":
		if points > 0 {
			return errors.New("扣分项裁定分不能大于 0")
		}
	default:
		return errors.New("未知申诉对象")
	}
	return nil
}

func (s *Server) resolveAppeal(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input appealResolutionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "申诉处理参数不正确", nil)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Score == nil || (input.Decision != "uphold" && input.Decision != "adjust") || len(input.Reason) < 4 || len(input.Reason) > 5000 || input.SpentSeconds < 0 || input.SpentSeconds > 24*60*60 {
		writeError(c, http.StatusUnprocessableEntity, "resolution_invalid", "必须填写认定分和 4—5000 字理由", nil)
		return
	}
	s.decideFirstAppeal(c, id, input)
}

func (s *Server) finalizeAppealRequest(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input appealResolutionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "终裁参数不正确", nil)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Score == nil || (input.Decision != "" && input.Decision != "uphold" && input.Decision != "adjust") || len(input.Reason) < 6 || len(input.Reason) > 5000 {
		writeError(c, http.StatusUnprocessableEntity, "resolution_invalid", "必须填写终裁分和 6—5000 字终裁理由", nil)
		return
	}
	s.finalizeAppeal(c, id, input)
}

func (s *Server) decideFirstAppeal(c *gin.Context, id int64, input appealResolutionInput) {
	tx := mustTx(c)
	actor := mustActor(c)
	row, err := loadAppealRecord(c.Request.Context(), tx, id, true)
	if notFound(c, err, "申诉") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if actor.UserID == row.StudentID {
		writeError(c, http.StatusForbidden, "avoid_self", "不能复评自己的申诉，请由其他审核人或终裁人处理", nil)
		return
	}
	if row.Kind != "student_appeal" || row.Round != 1 || row.Status != "reviewing" {
		message := "只有复评中的第一次申诉可以复核，请刷新后查看当前状态"
		switch row.Status {
		case "final":
			message = "这条申诉已终裁，复评已结束，无需再提交"
		case "resolved":
			message = "这轮申诉已复评结案，无需再提交"
		case "escalated":
			message = "这条申诉已交终裁人处理，不再接受复评"
		}
		writeError(c, http.StatusConflict, "not_reconsiderable", message, nil)
		return
	}
	var position int
	var existingScore *float64
	err = tx.QueryRow(c.Request.Context(), `
		SELECT position,score::float8 FROM appeal_reviewer
		 WHERE appeal_id=$1 AND reviewer_id=$2 FOR UPDATE
	`, id, actor.UserID).Scan(&position, &existingScore)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusForbidden, "not_handler", "这条申诉没有路由给你", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if existingScore != nil {
		writeError(c, http.StatusConflict, "already_reviewed", "你已经处理过这次申诉", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "review", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "resolution_closed", err.Error(), nil)
		return
	}
	target, err := loadAppealTarget(c.Request.Context(), tx, row.TargetType, row.TargetID, row.StudentID, true)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	outcome, err := resolveAppealOutcome(c.Request.Context(), tx, row, target, input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "appeal_outcome_invalid", err.Error(), nil)
		return
	}
	if !upholdOutcomeMatches(input.Decision, outcome, row) {
		writeError(c, http.StatusUnprocessableEntity, "uphold_outcome_mismatch", "选择维持原判时，分类和分数都必须与申诉时的认定结果一致", nil)
		return
	}
	_, err = tx.Exec(c.Request.Context(), `
		UPDATE appeal_reviewer
		   SET decision=$1,score=$2,category_key=$3,item_key=$4,rule_snapshot=$5,
		       reason=$6,spent_seconds=$7,decided_at=now()
		 WHERE appeal_id=$8 AND reviewer_id=$9
	`, input.Decision, outcome.Score, outcome.Category, outcome.ItemKey, outcome.RuleSnapshot,
		input.Reason, input.SpentSeconds, id, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT reviewer_id,score::float8,category_key,item_key,rule_snapshot
		  FROM appeal_reviewer WHERE appeal_id=$1 ORDER BY position
	`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	outcomes := make([]appealReviewOutcome, 0, 2)
	expected := 0
	for rows.Next() {
		var reviewerID int64
		var score *float64
		var category, itemKey *string
		var ruleRaw []byte
		if err := rows.Scan(&reviewerID, &score, &category, &itemKey, &ruleRaw); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		expected++
		if score != nil && category != nil && itemKey != nil {
			outcomes = append(outcomes, appealReviewOutcome{Category: *category, ItemKey: *itemKey, RuleSnapshot: ruleRaw, Score: *score})
		}
	}
	rows.Close()
	nextStatus, finalOutcome, conflict, err := reconcileAppealOutcomes(expected, outcomes)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if finalOutcome != nil {
		var combinedReason string
		if err := tx.QueryRow(c.Request.Context(), `
			SELECT string_agg(reason,E'\n\n' ORDER BY position) FROM appeal_reviewer WHERE appeal_id=$1
		`, id).Scan(&combinedReason); err != nil {
			writeServiceError(c, err)
			return
		}
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE appeal SET status='resolved',resolution_score=$1,
			       resolution_category_key=$2,resolution_item_key=$3,resolution_rule_snapshot=$4,
			       resolution_reason=$5,resolved_at=now(),updated_at=now()
			 WHERE id=$6
		`, finalOutcome.Score, finalOutcome.Category, finalOutcome.ItemKey, finalOutcome.RuleSnapshot, combinedReason, id); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := applyAppealOutcome(c.Request.Context(), tx, actor, row, target, *finalOutcome, "第一次申诉经原审核人复核一致"); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := invalidateLatestSettlement(c.Request.Context(), tx, "first appeal reconsideration changed a score"); err != nil {
			writeServiceError(c, err)
			return
		}
	} else if conflict {
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE appeal SET status='escalated',resolution_score=NULL,
			       resolution_reason='原审核人复核分数不一致，转班级管理员仲裁',updated_at=now()
			 WHERE id=$1
		`, id); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := markAppealTargetArbitrating(c.Request.Context(), tx, target); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	after := map[string]any{"status": nextStatus, "reviewCount": len(outcomes), "expectedReviews": expected}
	if finalOutcome != nil {
		after["score"] = finalOutcome.Score
		after["category"] = finalOutcome.Category
		after["itemKey"] = finalOutcome.ItemKey
	}
	if err := appendAudit(c, tx, "appeal.rereviewed", "appeal", strconv.FormatInt(id, 10), map[string]any{"status": row.Status}, after, map[string]any{"position": position, "decision": input.Decision}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AppealRereviewedEvent(), events.AppealRereviewedPayload{
		AppealID: id, Status: nextStatus, ReviewerID: actor.UserID,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	if nextStatus != "reviewing" {
		if conflict {
			if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AppealEscalatedEvent(), events.AppealEscalatedPayload{
				AppealID: id, Status: nextStatus,
			}); err != nil {
				writeServiceError(c, err)
				return
			}
		} else {
			payload := events.AppealResolvedPayload{AppealID: id, Status: nextStatus}
			if finalOutcome != nil {
				payload.Score = &finalOutcome.Score
				payload.Category = &finalOutcome.Category
				payload.ItemKey = &finalOutcome.ItemKey
			}
			if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AppealResolvedEvent(), payload); err != nil {
				writeServiceError(c, err)
				return
			}
		}
	}
	if row.OriginBatchID != nil {
		if _, err := refreshBlindAuditBatch(c.Request.Context(), tx, actor.ClassID, *row.OriginBatchID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"id": strconv.FormatInt(id, 10), "status": nextStatus,
		"bothDecided": len(outcomes) == expected, "conflict": conflict, "resolvedScore": func() any {
			if finalOutcome == nil {
				return nil
			}
			return finalOutcome.Score
		}(), "resolvedCategory": func() any {
			if finalOutcome == nil {
				return nil
			}
			return finalOutcome.Category
		}(), "resolvedItemKey": func() any {
			if finalOutcome == nil {
				return nil
			}
			return finalOutcome.ItemKey
		}(),
	})
}

func (s *Server) finalizeAppeal(c *gin.Context, id int64, input appealResolutionInput) {
	if len(input.Reason) < 6 {
		writeError(c, http.StatusUnprocessableEntity, "resolution_invalid", "终裁理由须为 6—5000 字符", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	row, err := loadAppealRecord(c.Request.Context(), tx, id, true)
	if notFound(c, err, "申诉或异议") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if row.Kind != "student_appeal" || (row.Status != "escalated" && row.Status != "reviewing" && row.Status != "filed") {
		writeError(c, http.StatusConflict, "not_finalizable", "只有待终裁、复评中或待派发的申诉可以终裁", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "arbitrate", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "resolution_closed", err.Error(), nil)
		return
	}
	if rejectSelfArbitration(c, tx, actor, row.StudentID) {
		return
	}
	target, err := loadAppealTarget(c.Request.Context(), tx, row.TargetType, row.TargetID, row.StudentID, true)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	outcome, err := resolveAppealOutcome(c.Request.Context(), tx, row, target, input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "appeal_outcome_invalid", err.Error(), nil)
		return
	}
	if !upholdOutcomeMatches(input.Decision, outcome, row) {
		writeError(c, http.StatusUnprocessableEntity, "uphold_outcome_mismatch", "选择维持原判时，分类和分数都必须与申诉时的认定结果一致", nil)
		return
	}
	if err := applyAppealOutcome(c.Request.Context(), tx, actor, row, target, outcome, input.Reason); err != nil {
		writeServiceError(c, err)
		return
	}
	// handler_id has been on this table since the first migration but nothing
	// ever wrote it, so "谁终裁的" only lived in the audit log. The scorecard
	// audit trail reads it back, so record the administrator who signed.
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE appeal SET status='final',handler_id=$1,resolution_score=$2,resolution_category_key=$3,
		       resolution_item_key=$4,resolution_rule_snapshot=$5,resolution_reason=$6,
		       resolved_at=now(),updated_at=now()
		 WHERE id=$7
	`, actor.UserID, outcome.Score, outcome.Category, outcome.ItemKey, outcome.RuleSnapshot, input.Reason, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if row.OriginBatchID != nil {
		if _, err := refreshBlindAuditBatch(c.Request.Context(), tx, actor.ClassID, *row.OriginBatchID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	takeover := row.Status == "reviewing"
	if err := appendAudit(c, tx, "appeal.final", "appeal", strconv.FormatInt(id, 10), map[string]any{"status": row.Status, "score": target.CurrentScore, "category": target.Category, "itemKey": target.ItemKey}, map[string]any{"status": "final", "score": outcome.Score, "category": outcome.Category, "itemKey": outcome.ItemKey}, map[string]any{"targetType": target.Type, "targetId": target.ID, "takeover": takeover}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AppealResolvedEvent(), events.AppealResolvedPayload{
		AppealID: id, Status: "final", Score: &outcome.Score, Category: &outcome.Category, ItemKey: &outcome.ItemKey,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ArbitrationResolvedEvent(),
		events.NewAppealArbitrationResolvedPayload(id, target.Type, target.ID, outcome.Score, outcome.Category, outcome.ItemKey)); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateLatestSettlement(c.Request.Context(), tx, "appeal or objection finalization changed a score"); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": "final", "score": outcome.Score, "category": outcome.Category, "itemKey": outcome.ItemKey, "kind": row.Kind, "round": row.Round})
}

func upholdScoreMatches(decision string, score, original *float64) bool {
	if decision != "uphold" {
		return true
	}
	return score != nil && original != nil && scheme.NewPoints(*score) == scheme.NewPoints(*original)
}

func applyAppealOutcome(ctx context.Context, tx pgx.Tx, actor Actor, row appealRecord, target appealTarget, outcome appealReviewOutcome, basis string) error {
	if target.Type != "submission" {
		if err := applyAppealScore(ctx, tx, target, outcome.Score, basis); err != nil {
			return err
		}
		origin := ""
		if row.OriginBatchID != nil {
			origin = *row.OriginBatchID
		}
		return invalidateStudentBlindAudit(ctx, tx, target.SchemeID, target.StudentID, origin, "appeal changed a base or penalty score outside the active blind audit")
	}
	var originBatch any
	if row.OriginBatchID != nil && strings.TrimSpace(*row.OriginBatchID) != "" {
		originBatch = strings.TrimSpace(*row.OriginBatchID)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO classification_resolution
		    (class_id,scheme_id,submission_id,appeal_id,origin_batch_id,decided_by,
		     before_category_key,before_item_key,after_category_key,after_item_key,
		     before_rule_snapshot,after_rule_snapshot,before_score,after_score,reason)
		VALUES ($1,$2,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, actor.ClassID, target.SchemeID, target.ID, row.ID, originBatch, actor.UserID,
		target.Category, target.ItemKey, outcome.Category, outcome.ItemKey,
		target.RuleSnapshot, outcome.RuleSnapshot, target.CurrentScore, outcome.Score, basis); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE submission
		   SET category_key=$1,item_key=$2,rule_snapshot=$3,final_score=$4,status='scored',
		       scored_at=now(),updated_at=now(),lock_version=lock_version+1
		 WHERE id=$5
	`, outcome.Category, outcome.ItemKey, outcome.RuleSnapshot, outcome.Score, target.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE classification_suggestion
		   SET status=CASE WHEN to_category_key=$1 AND to_item_key=$2 THEN 'accepted' ELSE 'rejected' END,
		       resolved_by=$3,resolution_reason=$4,resolved_at=now()
		 WHERE submission_id=$5 AND status='pending'
	`, outcome.Category, outcome.ItemKey, actor.UserID, basis, target.ID); err != nil {
		return err
	}
	origin := ""
	if row.OriginBatchID != nil {
		origin = *row.OriginBatchID
	}
	if err := invalidateStudentBlindAudit(ctx, tx, target.SchemeID, target.StudentID, origin, "appeal changed an effective classification or score outside the active blind audit"); err != nil {
		return err
	}
	if origin != "" {
		_, err := refreshBlindAuditBatch(ctx, tx, actor.ClassID, origin)
		return err
	}
	return nil
}

func applyAppealScore(ctx context.Context, tx pgx.Tx, target appealTarget, score float64, basis string) error {
	if target.Type == "submission" {
		_, err := tx.Exec(ctx, `
			UPDATE submission SET final_score=$1,status='scored',scored_at=now(),updated_at=now(),lock_version=lock_version+1
			 WHERE id=$2
		`, score, target.ID)
		return err
	}
	if target.CurrentScore != nil && scheme.NewPoints(*target.CurrentScore) == scheme.NewPoints(score) {
		_, err := tx.Exec(ctx, `UPDATE base_score SET score=$1,updated_at=now() WHERE id=$2`, score, target.ID)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE base_score SET score=$1,basis=$2,updated_at=now() WHERE id=$3`, score, basis, target.ID)
	return err
}

func markAppealTargetArbitrating(ctx context.Context, tx pgx.Tx, target appealTarget) error {
	if target.Type != "submission" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE submission SET status='arbitrating',updated_at=now(),lock_version=lock_version+1 WHERE id=$1
	`, target.ID)
	return err
}

func (s *Server) adminSubmissions(c *gin.Context) {
	tx := mustTx(c)
	lockdown, err := classtimeline.Lockdown(c.Request.Context(), tx, mustActor(c).ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	forceRejectable := c.Query("force_rejectable") == "true"
	itemID := int64(0)
	if raw := strings.TrimSpace(c.Query("id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(c, http.StatusBadRequest, "invalid_id", "提交条目编号不正确", nil)
			return
		}
		itemID = parsed
	}
	status := strings.TrimSpace(c.Query("status"))
	category := strings.TrimSpace(c.Query("category"))
	studentQuery := strings.TrimSpace(c.Query("student"))
	query := strings.TrimSpace(c.Query("q"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT s.id,u.id,u.sid,u.name,s.category_key,s.item_key,s.filed_category_key,s.filed_item_key,s.title,s.requested_score::float8,
		       s.final_score::float8,s.status,s.submitted_at,s.updated_at,
		       s.claim,s.markdown_note,s.rule_snapshot,
		       CASE WHEN $7::boolean AND s.status IN ('pending','consensus') THEN '[]'::jsonb ELSE COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',r.id::text,'reviewerId',r.reviewer_id::text,'reviewer',reviewer.name,
		           'reviewerSid',reviewer.sid,'decision',r.decision,'score',r.score::float8,'reason',r.reason,
		           'spentSeconds',r.spent_seconds,'at',r.created_at
		         ) ORDER BY r.created_at,r.id)
		           FROM review r JOIN app_user reviewer ON reviewer.id=r.reviewer_id
		          WHERE r.submission_id=s.id AND r.superseded_at IS NULL
		       ),'[]'::jsonb) END,
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',e.id::text,'name',e.filename,'mediaType',e.media_type,
		           'sizeBytes',e.size_bytes,'sha256',e.sha256,'status',e.status,'uploadedAt',e.created_at
		         ) ORDER BY e.id)
		           FROM evidence e WHERE e.submission_id=s.id AND e.kind='claim'
		       ),'[]'::jsonb),
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',e.id::text,'name',e.filename,'mediaType',e.media_type,
		           'sizeBytes',e.size_bytes,'sha256',e.sha256,'status',e.status,'uploadedAt',e.created_at
		         ) ORDER BY e.id)
		           FROM evidence e WHERE e.submission_id=s.id AND e.kind='note'
		             AND (NOT $7::boolean OR s.status NOT IN ('pending','consensus') OR e.created_by=s.student_id)
		       ),'[]'::jsonb),
		       latest_appeal.id,latest_appeal.round,latest_appeal.status,s.force_rejection,s.forced_score
		  FROM submission s JOIN app_user u ON u.id=s.student_id
		  LEFT JOIN LATERAL (
		    SELECT a.id,a.round,a.status FROM appeal a
		     WHERE a.kind='student_appeal' AND a.target_type='submission' AND a.target_id=s.id
		       AND a.status<>'draft'
		     ORDER BY a.round DESC,a.id DESC LIMIT 1
		  ) latest_appeal ON true
		 WHERE ($1='' OR s.status=$1)
		   AND ($2='' OR s.category_key=$2)
		   AND ($3='' OR u.sid ILIKE '%'||$3||'%' OR u.name ILIKE '%'||$3||'%')
		   AND ($4='' OR s.title ILIKE '%'||$4||'%' OR s.item_key ILIKE '%'||$4||'%')
		   AND ($8::bigint=0 OR s.id=$8)
		   AND (NOT $7::boolean OR (u.role='class_admin' AND (s.status IN ('arbitrating','scored','locked','appealing') OR ($9::boolean AND s.status<>'draft'))))
		 ORDER BY CASE s.status WHEN 'arbitrating' THEN 0 WHEN 'appealing' THEN 1 WHEN 'pending' THEN 2 ELSE 3 END,
		          s.updated_at DESC,s.id DESC
		 LIMIT $5 OFFSET $6
	`, status, category, studentQuery, query, pageSize, (page-1)*pageSize, isDeputyAdjudication(c), itemID, forceRejectable)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, studentID int64
		var sid, name, categoryKey, itemKey, filedCategory, filedItem, title, submissionStatus string
		var requested, finalScore *float64
		var submittedAt *time.Time
		var updatedAt time.Time
		var claim, note, ruleSnapshot, reviews, evidence, noteEvidence []byte
		var appealID *int64
		var appealRound *int
		var appealStatus *string
		var forceRejectionRaw, forcedScoreRaw []byte
		if err := rows.Scan(&id, &studentID, &sid, &name, &categoryKey, &itemKey, &filedCategory, &filedItem, &title, &requested, &finalScore,
			&submissionStatus, &submittedAt, &updatedAt, &claim, &note, &ruleSnapshot, &reviews, &evidence, &noteEvidence,
			&appealID, &appealRound, &appealStatus, &forceRejectionRaw, &forcedScoreRaw); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		item := gin.H{
			"id": strconv.FormatInt(id, 10), "studentId": sid, "student": name, "category": categoryKey,
			"itemKey": itemKey, "filedCategory": filedCategory, "filedItemKey": filedItem,
			"title": title, "requestedScore": requested, "finalScore": finalScore,
			"status": submissionStatus, "submittedAt": submittedAt, "updatedAt": updatedAt,
			"reviews": json.RawMessage(reviews), "evidence": json.RawMessage(evidence),
			"noteEvidence":   json.RawMessage(noteEvidence),
			"forceRejection": json.RawMessage(forceRejectionRaw),
			"forcedScore":    json.RawMessage(forcedScoreRaw),
			// 仲裁台要看的是学生当初交了什么，而不只是两位审核人各说了什么。
			// claim / note / rule_snapshot 都在 submission 表上，取出来不多一次查询。
			"claim": json.RawMessage(claim), "note": string(note), "ruleSnapshot": json.RawMessage(ruleSnapshot),
		}
		withAdjudicationPermission(item, mustActor(c), studentID)
		item["canForceReject"], item["forceRejectBlockedReason"] = forceRejectPermission(mustActor(c), studentID, submissionStatus, len(forceRejectionRaw) > 0)
		item["canForceScore"], item["forceScoreBlockedReason"] = forceScorePermission(mustActor(c), studentID, submissionStatus, finalScore, len(forceRejectionRaw) > 0)
		if lockdown != nil && !time.Now().Before(*lockdown) {
			item["canForceReject"], item["forceRejectBlockedReason"] = false, "本学期已全系统封锁，请先调整班级时间线"
			item["canForceScore"], item["forceScoreBlockedReason"] = false, "本学期已全系统封锁，请先调整班级时间线"
		}
		if appealID != nil {
			item["appealId"] = strconv.FormatInt(*appealID, 10)
			item["appealRound"] = appealRound
			item["appealStatus"] = appealStatus
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeServiceError(c, err)
		return
	}
	rows.Close()
	var total int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM submission s JOIN app_user u ON u.id=s.student_id
		 WHERE ($1='' OR s.status=$1) AND ($2='' OR s.category_key=$2)
		   AND ($3='' OR u.sid ILIKE '%'||$3||'%' OR u.name ILIKE '%'||$3||'%')
		   AND ($4='' OR s.title ILIKE '%'||$4||'%' OR s.item_key ILIKE '%'||$4||'%')
		   AND ($6::bigint=0 OR s.id=$6)
		   AND (NOT $5::boolean OR (u.role='class_admin' AND (s.status IN ('arbitrating','scored','locked','appealing') OR ($7::boolean AND s.status<>'draft'))))
	`, status, category, studentQuery, query, isDeputyAdjudication(c), itemID, forceRejectable).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "submission.admin_list", "submission", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

type arbitrationInput struct {
	Decision string   `json:"decision,omitempty"`
	Category string   `json:"category"`
	ItemKey  string   `json:"itemKey"`
	Score    *float64 `json:"score"`
	Reason   string   `json:"reason"`
}

func (s *Server) arbitrateSubmission(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input arbitrationInput
	if err := c.ShouldBindJSON(&input); err != nil || input.Score == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "仲裁参数不正确", nil)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if len(input.Reason) < 6 || len(input.Reason) > 5000 {
		writeError(c, http.StatusUnprocessableEntity, "arbitration_invalid", "仲裁理由须为 6—5000 字符", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "arbitrate", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "arbitration_closed", err.Error(), nil)
		return
	}
	var studentID int64
	var schemeID int64
	var status string
	var beforeCategory, beforeItem string
	var beforeScore *float64
	var snapshotRaw []byte
	err = tx.QueryRow(c.Request.Context(), `
		SELECT student_id,scheme_id,status,category_key,item_key,final_score::float8,rule_snapshot
		  FROM submission WHERE id=$1 FOR UPDATE
	`, id).Scan(&studentID, &schemeID, &status, &beforeCategory, &beforeItem, &beforeScore, &snapshotRaw)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	legacyLockedClassification := false
	if status == "locked" {
		if err := tx.QueryRow(c.Request.Context(), `
			SELECT EXISTS (SELECT 1 FROM classification_suggestion
			 WHERE submission_id=$1 AND status='pending' AND scope='cross_category')
		`, id).Scan(&legacyLockedClassification); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if status != "arbitrating" && status != "scored" && !legacyLockedClassification {
		writeError(c, http.StatusConflict, "not_arbitrable", "只有双评冲突或已定分条目可以终裁", nil)
		return
	}
	if rejectSelfArbitration(c, tx, actor, studentID) {
		return
	}
	category, itemKey := strings.TrimSpace(input.Category), strings.TrimSpace(input.ItemKey)
	if category == "" && itemKey == "" {
		category, itemKey = beforeCategory, beforeItem
	} else if category == "" || itemKey == "" {
		writeError(c, http.StatusUnprocessableEntity, "classification_invalid", "分类终裁必须同时填写大项和小项", nil)
		return
	}
	target, err := loadClassificationTarget(c.Request.Context(), tx, id, category, itemKey, time.Now())
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "classification_invalid", err.Error(), nil)
		return
	}
	resolvedScore := scheme.NewPoints(*input.Score).Float64()
	if err := scoreWithinRule(target.Item.ScoreRule, resolvedScore); err != nil && !(governanceExecutionFor(c, "submission") && input.Decision == "rejected" && resolvedScore == 0) {
		writeError(c, http.StatusUnprocessableEntity, "score_invalid", err.Error(), nil)
		return
	}
	var linkedAppealID *int64
	var originBatchID *string
	var foundAppealID int64
	err = tx.QueryRow(c.Request.Context(), `
		SELECT id,origin_batch_id::text FROM appeal
		 WHERE target_type='submission' AND target_id=$1 AND status='escalated'
		 ORDER BY updated_at DESC,id DESC LIMIT 1 FOR UPDATE
	`, id).Scan(&foundAppealID, &originBatchID)
	if err == nil {
		linkedAppealID = &foundAppealID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	// A scorecard-audit classification is also a causal origin. Keeping its
	// batch ID prevents this very arbitration from marking the batch stale;
	// refreshBlindAuditBatch below will complete it once all findings close.
	if originBatchID == nil {
		var classificationOrigin *string
		err = tx.QueryRow(c.Request.Context(), `
			SELECT subject.batch_id::text
			  FROM classification_suggestion cs
			  JOIN scorecard_audit_assignment assignment ON assignment.id=cs.blind_assignment_id
			  JOIN scorecard_audit_subject subject ON subject.id=assignment.subject_id
			 WHERE cs.submission_id=$1 AND cs.status='pending' AND cs.scope='cross_category'
			 ORDER BY cs.id LIMIT 1 FOR UPDATE OF cs
		`, id).Scan(&classificationOrigin)
		if err == nil {
			originBatchID = classificationOrigin
		} else if !errors.Is(err, pgx.ErrNoRows) {
			writeServiceError(c, err)
			return
		}
	}
	var originBatch any
	if originBatchID != nil {
		originBatch = *originBatchID
	}
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO classification_resolution
		    (class_id,scheme_id,submission_id,appeal_id,origin_batch_id,decided_by,
		     before_category_key,before_item_key,after_category_key,after_item_key,
		     before_rule_snapshot,after_rule_snapshot,before_score,after_score,reason)
		VALUES ($1,$2,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, actor.ClassID, schemeID, id, linkedAppealID, originBatch, actor.UserID,
		beforeCategory, beforeItem, target.Category.Key, target.Item.Key,
		snapshotRaw, target.Snapshot, beforeScore, resolvedScore, input.Reason); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE submission
		   SET category_key=$1,item_key=$2,rule_snapshot=$3,final_score=$4,status='scored',
		       scored_at=now(),updated_at=now(),lock_version=lock_version+1
		 WHERE id=$5
	`, target.Category.Key, target.Item.Key, target.Snapshot, resolvedScore, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE classification_suggestion
		   SET status=CASE WHEN to_category_key=$1 AND to_item_key=$2 THEN 'accepted' ELSE 'rejected' END,
		       resolved_by=$3,resolution_reason=$4,resolved_at=now()
		 WHERE submission_id=$5 AND status='pending'
	`, target.Category.Key, target.Item.Key, actor.UserID, input.Reason, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if linkedAppealID != nil {
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE appeal SET status='final',handler_id=$1,resolution_score=$2,resolution_category_key=$3,
			       resolution_item_key=$4,resolution_rule_snapshot=$5,resolution_reason=$6,
			       resolved_at=now(),updated_at=now()
			 WHERE id=$7
		`, actor.UserID, resolvedScore, target.Category.Key, target.Item.Key, target.Snapshot, input.Reason, foundAppealID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "submission.arbitrated", "submission", strconv.FormatInt(id, 10), map[string]any{"status": status, "finalScore": beforeScore, "category": beforeCategory, "itemKey": beforeItem}, map[string]any{"status": "scored", "finalScore": resolvedScore, "category": target.Category.Key, "itemKey": target.Item.Key}, map[string]any{"reason": input.Reason, "studentId": studentID}); err != nil {
		writeServiceError(c, err)
		return
	}
	if linkedAppealID != nil {
		if err := appendAudit(c, tx, "appeal.final", "appeal", strconv.FormatInt(*linkedAppealID, 10), map[string]any{"status": "escalated"}, map[string]any{"status": "final", "score": resolvedScore, "category": target.Category.Key, "itemKey": target.Item.Key}, map[string]any{"via": "submission_arbitration"}); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ArbitrationResolvedEvent(),
		events.NewSubmissionArbitrationResolvedPayload(id, studentID, resolvedScore, target.Category.Key, target.Item.Key, input.Reason)); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateLatestSettlement(c.Request.Context(), tx, "submission arbitration changed a score"); err != nil {
		writeServiceError(c, err)
		return
	}
	origin := ""
	if originBatchID != nil {
		origin = *originBatchID
	}
	if err := invalidateStudentBlindAudit(c.Request.Context(), tx, schemeID, studentID, origin, "submission arbitration changed an effective classification or score"); err != nil {
		writeServiceError(c, err)
		return
	}
	if origin != "" {
		if _, err := refreshBlindAuditBatch(c.Request.Context(), tx, actor.ClassID, origin); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	response := gin.H{"id": strconv.FormatInt(id, 10), "status": "scored", "finalScore": resolvedScore, "category": target.Category.Key, "itemKey": target.Item.Key}
	if linkedAppealID != nil {
		response["appealId"] = strconv.FormatInt(*linkedAppealID, 10)
		response["appealStatus"] = "final"
	}
	c.JSON(http.StatusOK, response)
}
