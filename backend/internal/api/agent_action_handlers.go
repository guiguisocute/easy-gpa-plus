package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/agentcontext"
	"easygpa/backend/internal/exportjob"
	"easygpa/backend/internal/scheme"
)

type agentActionRecord struct {
	ID, MessageID, OwnerID int64
	Kind, Status           string
	Title, Summary         string
	Payload, Diff          []byte
	Citations              []byte
	TargetView             string
	TokenHash              []byte
	ConfirmExpiresAt       *time.Time
	ExpiresAt              time.Time
	Result                 []byte
	CreatedAt              time.Time
}

func (s *Server) agentAction(c *gin.Context) {
	actionID, ok := pathID(c, "id")
	if !ok {
		return
	}
	record, err := loadAgentAction(c.Request.Context(), mustTx(c), actionID, mustActor(c).UserID, false)
	if notFound(c, err, "Agent 操作") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if record.Kind == "arbitration_reason_draft" {
		var payload agentcontext.ReasonDraft
		if json.Unmarshal(record.Payload, &payload) != nil || agentResourceRevoked(c.Request.Context(), mustTx(c), mustActor(c).UserID, payload.Context) {
			writeError(c, http.StatusForbidden, "source_forbidden", agentcontext.ErrResource.Error(), nil)
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, actionResponse(record))
}

func (s *Server) prepareAgentAction(c *gin.Context) {
	if !s.runtimeFlags(c.Request.Context()).AgentActionsEnabled {
		writeError(c, http.StatusNotImplemented, "agent_disabled", "Agent 草稿操作当前未启用", nil)
		return
	}
	actionID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	record, err := loadAgentAction(c.Request.Context(), tx, actionID, actor.UserID, true)
	if notFound(c, err, "Agent 操作") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if record.Status == "applied" {
		writeError(c, http.StatusConflict, "action_stale", "该操作已经应用", nil)
		return
	}
	if record.Status == "rejected" || record.Status == "stale" {
		writeError(c, http.StatusConflict, "action_stale", "该操作已失效", nil)
		return
	}
	if !time.Now().Before(record.ExpiresAt) {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE agent_action SET status='expired',updated_at=now() WHERE id=$1`, actionID)
		writeError(c, http.StatusConflict, "action_expired", "该操作建议已过期", nil)
		return
	}
	state, diff, risk, err := validateAgentAction(c.Request.Context(), tx, actor, record)
	if err != nil {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE agent_action SET status='stale',confirm_token_hash=NULL,confirm_expires_at=NULL,updated_at=now() WHERE id=$1`, actionID)
		writeError(c, http.StatusConflict, "action_stale", err.Error(), nil)
		return
	}
	token, tokenHash, err := newConfirmToken()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	expires := time.Now().Add(5 * time.Minute)
	stateRaw, _ := json.Marshal(state)
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE agent_action
		   SET status='prepared',diff=$2,result=$3,confirm_token_hash=$4,confirm_expires_at=$5,prepared_at=now(),updated_at=now()
		 WHERE id=$1
	`, actionID, diff, stateRaw, tokenHash, expires); err != nil {
		writeServiceError(c, err)
		return
	}
	record.Status, record.Diff, record.Result, record.ConfirmExpiresAt = "prepared", diff, stateRaw, &expires
	if err := appendAudit(c, tx, "agent.action_prepared", "agent_action", strconv.FormatInt(actionID, 10), nil, map[string]any{"kind": record.Kind, "expiresAt": expires}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	response := actionResponse(record)
	response["confirmToken"] = token
	response["confirmExpiresAt"] = expires
	response["risk"] = risk
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, response)
}

func (s *Server) applyAgentAction(c *gin.Context) {
	if !s.runtimeFlags(c.Request.Context()).AgentActionsEnabled {
		writeError(c, http.StatusNotImplemented, "agent_disabled", "Agent 草稿操作当前未启用", nil)
		return
	}
	actionID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		ConfirmToken string `json:"confirmToken"`
	}
	if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.ConfirmToken) == "" {
		writeError(c, http.StatusBadRequest, "invalid_request", "确认令牌不能为空", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	record, err := loadAgentAction(c.Request.Context(), tx, actionID, actor.UserID, true)
	if notFound(c, err, "Agent 操作") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if record.Status == "applied" {
		var result any
		_ = json.Unmarshal(record.Result, &result)
		c.JSON(http.StatusOK, result)
		return
	}
	if record.Status != "prepared" || record.ConfirmExpiresAt == nil || !time.Now().Before(*record.ConfirmExpiresAt) {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE agent_action SET status='expired',confirm_token_hash=NULL,updated_at=now() WHERE id=$1 AND status='prepared'`, actionID)
		writeError(c, http.StatusConflict, "action_expired", "确认令牌已过期，请重新查看并确认", nil)
		return
	}
	provided := sha256.Sum256([]byte(input.ConfirmToken))
	if len(record.TokenHash) != len(provided) || subtle.ConstantTimeCompare(record.TokenHash, provided[:]) != 1 {
		writeError(c, http.StatusForbidden, "action_forbidden", "确认令牌无效", nil)
		return
	}
	state, _, _, err := validateAgentAction(c.Request.Context(), tx, actor, record)
	if err != nil || !samePreparedState(record.Result, state) {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE agent_action SET status='stale',confirm_token_hash=NULL,confirm_expires_at=NULL,updated_at=now() WHERE id=$1`, actionID)
		writeError(c, http.StatusConflict, "action_stale", "角色、方案或目标资源已经变化，请重新让 Agent 生成建议", nil)
		return
	}
	result, err := s.executeAgentDraft(c.Request.Context(), tx, actor, record)
	if err != nil {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE agent_action SET status='stale',confirm_token_hash=NULL,confirm_expires_at=NULL,updated_at=now() WHERE id=$1`, actionID)
		writeError(c, http.StatusConflict, "action_stale", err.Error(), nil)
		return
	}
	resultRaw, _ := json.Marshal(result)
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE agent_action
		   SET status='applied',result=$2,confirm_token_hash=NULL,confirm_expires_at=NULL,applied_at=now(),updated_at=now()
		 WHERE id=$1
	`, actionID, resultRaw); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "agent.action_applied", "agent_action", strconv.FormatInt(actionID, 10), nil, map[string]any{"kind": record.Kind, "draftOnly": true}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func (s *Server) rejectAgentAction(c *gin.Context) {
	actionID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var status string
	err := tx.QueryRow(c.Request.Context(), `
		UPDATE agent_action SET status='rejected',confirm_token_hash=NULL,confirm_expires_at=NULL,updated_at=now()
		 WHERE id=$1 AND owner_id=$2 AND status IN ('proposed','prepared') RETURNING status
	`, actionID, actor.UserID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusConflict, "action_stale", "该操作已处理或不存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(actionID, 10), "status": status})
}

func loadAgentActions(ctx context.Context, tx pgx.Tx, messageID, ownerID int64) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,message_id,owner_id,kind,status,title,summary,payload,diff,citations,target_view,
		       confirm_expires_at,expires_at,result,created_at
		  FROM agent_action WHERE message_id=$1 AND owner_id=$2 ORDER BY id
	`, messageID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var record agentActionRecord
		if err := rows.Scan(&record.ID, &record.MessageID, &record.OwnerID, &record.Kind, &record.Status, &record.Title, &record.Summary, &record.Payload, &record.Diff, &record.Citations, &record.TargetView, &record.ConfirmExpiresAt, &record.ExpiresAt, &record.Result, &record.CreatedAt); err != nil {
			return nil, err
		}
		if record.Status == "proposed" || record.Status == "prepared" {
			if !time.Now().Before(record.ExpiresAt) {
				record.Status = "expired"
			}
		}
		items = append(items, actionResponse(record))
	}
	return items, rows.Err()
}

func loadAgentAction(ctx context.Context, tx pgx.Tx, actionID, ownerID int64, lock bool) (agentActionRecord, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE"
	}
	var record agentActionRecord
	err := tx.QueryRow(ctx, `
		SELECT id,message_id,owner_id,kind,status,title,summary,payload,diff,citations,target_view,
		       confirm_token_hash,confirm_expires_at,expires_at,result,created_at
		  FROM agent_action WHERE id=$1 AND owner_id=$2`+lockSQL,
		actionID, ownerID).Scan(&record.ID, &record.MessageID, &record.OwnerID, &record.Kind, &record.Status, &record.Title, &record.Summary, &record.Payload, &record.Diff, &record.Citations, &record.TargetView, &record.TokenHash, &record.ConfirmExpiresAt, &record.ExpiresAt, &record.Result, &record.CreatedAt)
	return record, err
}

func actionResponse(record agentActionRecord) gin.H {
	status := record.Status
	if (status == "proposed" || status == "prepared") && !time.Now().Before(record.ExpiresAt) {
		status = "expired"
	}
	var boundContext *agentcontext.Context
	if record.Kind == "arbitration_reason_draft" {
		var payload agentcontext.ReasonDraft
		if json.Unmarshal(record.Payload, &payload) == nil {
			boundContext = &payload.Context
		}
	}
	return gin.H{
		"id": strconv.FormatInt(record.ID, 10), "kind": record.Kind, "status": status,
		"title": record.Title, "summary": record.Summary, "diff": json.RawMessage(record.Diff),
		"citations": json.RawMessage(record.Citations), "expiresAt": record.ExpiresAt,
		"targetView": record.TargetView, "createdAt": record.CreatedAt,
		"context": boundContext,
	}
}

func validateAgentAction(ctx context.Context, tx pgx.Tx, actor Actor, record agentActionRecord) (map[string]any, []byte, string, error) {
	state := map[string]any{"kind": record.Kind, "actorId": strconv.FormatInt(actor.UserID, 10), "role": actor.Role}
	var diff any
	risk := "只创建草稿，不会执行正式提交、审核、发布、结算或导出。"
	switch record.Kind {
	case "arbitration_reason_draft":
		var payload agentcontext.ReasonDraft
		if json.Unmarshal(record.Payload, &payload) != nil || strings.TrimSpace(payload.Reason) == "" || len(payload.Reason) > 5000 {
			return nil, nil, "", errors.New("理由草稿参数不正确")
		}
		snapshot, err := agentcontext.Load(ctx, tx, actor.ClassID, actor.UserID, payload.Context)
		if err != nil {
			return nil, nil, "", err
		}
		if !snapshot.CanDraft || snapshot.CheckRevision(payload.Context.Revision) != nil {
			return nil, nil, "", errors.New("当前事项已变化、需回避或不可编辑，请返回原页核对")
		}
		state["resourceId"] = snapshot.Context.ResourceID
		state["resourceKind"] = snapshot.Context.ResourceKind
		state["revision"] = snapshot.Context.Revision
		before := ""
		if payload.Context.Draft != nil {
			before = payload.Context.Draft.Reason
		}
		diff = []any{gin.H{"field": "终裁理由", "before": before, "after": payload.Reason}}
		risk = "只预填本条事项的理由，不改变分数、归类，也不会定分或发出通知。请核对理由后在原表单提交。"
	case "submission_draft":
		var payload struct {
			Category string          `json:"category"`
			ItemKey  string          `json:"itemKey"`
			Title    string          `json:"title"`
			Claim    json.RawMessage `json:"claim"`
			Note     string          `json:"note"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil {
			return nil, nil, "", errors.New("申报草稿参数已损坏")
		}
		current, err := loadCurrentScheme(ctx, tx)
		if err != nil {
			return nil, nil, "", err
		}
		if err := ensureCapability(current.Config, "submit", time.Now()); err != nil {
			return nil, nil, "", err
		}
		if err := ensureNotSealed(ctx, tx, actor.UserID); err != nil {
			return nil, nil, "", err
		}
		input := submissionInput{Category: payload.Category, ItemKey: payload.ItemKey, Title: payload.Title, Claim: payload.Claim, Note: payload.Note}
		prepared, err := prepareSubmission(current, input, time.Now(), false)
		if err != nil {
			return nil, nil, "", err
		}
		state["schemeId"], state["schemeVersion"] = strconv.FormatInt(current.ID, 10), current.Version
		diff = []any{
			gin.H{"field": "category", "after": prepared.Category.Key}, gin.H{"field": "itemKey", "after": prepared.Item.Key},
			gin.H{"field": "title", "after": strings.TrimSpace(payload.Title)}, gin.H{"field": "claim", "after": json.RawMessage(payload.Claim)},
			gin.H{"field": "requestedScore", "after": prepared.Requested},
		}
	case "review_draft":
		if actor.Role != "group" && actor.Role != "class_admin" {
			return nil, nil, "", errors.New("当前身份不能准备审核草稿")
		}
		var payload struct {
			TaskID   string   `json:"taskId"`
			Decision string   `json:"decision"`
			Score    *float64 `json:"score"`
			Reason   string   `json:"reason"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil {
			return nil, nil, "", errors.New("审核草稿参数已损坏")
		}
		taskID, err := strconv.ParseInt(payload.TaskID, 10, 64)
		if err != nil || taskID <= 0 {
			return nil, nil, "", errors.New("审核任务 ID 不正确")
		}
		task, err := loadReviewerTaskForAction(ctx, tx, taskID, actor.UserID)
		if err != nil {
			return nil, nil, "", errors.New("审核任务已经变化或不再属于你")
		}
		if task.Status != "pending" && task.Status != "consensus" {
			return nil, nil, "", errors.New("审核任务当前不可处理")
		}
		if _, _, _, err := validateDecision(task, reviewDecisionInput{Decision: payload.Decision, Score: payload.Score, Reason: payload.Reason}); err != nil {
			return nil, nil, "", err
		}
		state["taskId"], state["assignmentId"], state["taskStatus"] = payload.TaskID, strconv.FormatInt(task.AssignmentID, 10), task.Status
		diff = []any{gin.H{"field": "decision", "after": payload.Decision}, gin.H{"field": "score", "after": payload.Score}, gin.H{"field": "reason", "after": strings.TrimSpace(payload.Reason)}}
	case "scheme_draft":
		if actor.Role != "class_admin" {
			return nil, nil, "", errors.New("只有班管可以准备方案草稿")
		}
		var payload struct {
			Name   string        `json:"name"`
			Config scheme.Config `json:"config"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil || strings.TrimSpace(payload.Name) == "" {
			return nil, nil, "", errors.New("方案草稿参数已损坏")
		}
		// Agent action payloads deliberately carry only reusable scoring fields;
		// class_timeline owns dates, switches, and honor-roll policy. Re-decoding
		// the stored JSON into Config therefore leaves its runtime envelope at
		// zero values, so validate it as a template just like every other import.
		if err := scheme.ValidateTemplate(scheme.TemplateFromConfig(payload.Config)); err != nil {
			return nil, nil, "", fmt.Errorf("方案草稿校验失败: %w", err)
		}
		current, err := loadCurrentScheme(ctx, tx)
		if err != nil {
			return nil, nil, "", err
		}
		state["schemeId"], state["schemeVersion"] = strconv.FormatInt(current.ID, 10), current.Version
		diff = []any{gin.H{"field": "name", "after": strings.TrimSpace(payload.Name)}, gin.H{"field": "config", "before": json.RawMessage(current.Raw), "after": payload.Config}}
		risk = "将新建一份独立方案草稿；当前已发布版本不会改变。"
	case "export_draft":
		if actor.Role != "class_admin" {
			return nil, nil, "", errors.New("只有班管可以准备导出草稿")
		}
		var payload struct {
			Kind    string         `json:"kind"`
			Options map[string]any `json:"options"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil || !exportjob.ValidKind(payload.Kind) {
			return nil, nil, "", errors.New("导出草稿参数不正确")
		}
		diff = []any{gin.H{"field": "kind", "after": payload.Kind}, gin.H{"field": "options", "after": payload.Options}}
		risk = "只会在导出中心预填选项，不会创建导出任务。"
	default:
		return nil, nil, "", errors.New("Agent 操作类型不受支持")
	}
	diffRaw, _ := json.Marshal(diff)
	return state, diffRaw, risk, nil
}

func (s *Server) executeAgentDraft(ctx context.Context, tx pgx.Tx, actor Actor, record agentActionRecord) (gin.H, error) {
	switch record.Kind {
	case "arbitration_reason_draft":
		var payload agentcontext.ReasonDraft
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			return nil, err
		}
		return gin.H{"id": strconv.FormatInt(record.ID, 10), "status": "applied", "kind": record.Kind, "navigateTo": payload.Context.View,
			"resourceId": payload.Context.ResourceID, "prefill": gin.H{"reason": payload.Reason, "context": payload.Context}}, nil
	case "submission_draft":
		var payload struct {
			Category string          `json:"category"`
			ItemKey  string          `json:"itemKey"`
			Title    string          `json:"title"`
			Claim    json.RawMessage `json:"claim"`
			Note     string          `json:"note"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil {
			return nil, errors.New("申报草稿参数已损坏")
		}
		current, err := loadCurrentScheme(ctx, tx)
		if err != nil {
			return nil, err
		}
		prepared, err := prepareSubmission(current, submissionInput{Category: payload.Category, ItemKey: payload.ItemKey, Title: payload.Title, Claim: payload.Claim, Note: payload.Note}, time.Now(), false)
		if err != nil {
			return nil, err
		}
		var id int64
		err = tx.QueryRow(ctx, `
			INSERT INTO submission
			    (class_id,student_id,scheme_id,scheme_version,category_key,item_key,filed_category_key,filed_item_key,
			     title,claim,requested_score,status,source,rule_snapshot,filed_rule_snapshot,markdown_note)
			VALUES ($1,$2,$3,$4,$5,$6,$5,$6,$7,$8,$9,'draft','ai',$10,$10,$11) RETURNING id
		`, actor.ClassID, actor.UserID, prepared.SchemeID, prepared.SchemeVersion, prepared.Category.Key, prepared.Item.Key,
			strings.TrimSpace(payload.Title), payload.Claim, prepared.Requested, prepared.Snapshot, payload.Note).Scan(&id)
		if err != nil {
			return nil, err
		}
		return gin.H{"id": strconv.FormatInt(record.ID, 10), "status": "applied", "kind": record.Kind, "navigateTo": "stuSubmit", "resourceId": strconv.FormatInt(id, 10), "draft": gin.H{"id": strconv.FormatInt(id, 10), "status": "draft", "source": "ai"}}, nil
	case "review_draft":
		var payload map[string]any
		_ = json.Unmarshal(record.Payload, &payload)
		resourceID, _ := payload["taskId"].(string)
		return gin.H{"id": strconv.FormatInt(record.ID, 10), "status": "applied", "kind": record.Kind, "navigateTo": "revDesk", "resourceId": resourceID, "prefill": payload}, nil
	case "scheme_draft":
		var payload struct {
			Name   string        `json:"name"`
			Config scheme.Config `json:"config"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil {
			return nil, errors.New("方案草稿参数已损坏")
		}
		name, err := uniqueAgentSchemeName(ctx, tx, strings.TrimSpace(payload.Name))
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(payload.Config)
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO scheme (class_id,name,status,config,created_by) VALUES ($1,$2,'draft',$3,$4) RETURNING id`, actor.ClassID, name, raw, actor.UserID).Scan(&id); err != nil {
			return nil, err
		}
		return gin.H{"id": strconv.FormatInt(record.ID, 10), "status": "applied", "kind": record.Kind, "navigateTo": "admScheme", "resourceId": strconv.FormatInt(id, 10), "draft": gin.H{"id": strconv.FormatInt(id, 10), "status": "draft", "name": name}}, nil
	case "export_draft":
		var payload map[string]any
		_ = json.Unmarshal(record.Payload, &payload)
		return gin.H{"id": strconv.FormatInt(record.ID, 10), "status": "applied", "kind": record.Kind, "navigateTo": "admExport", "resourceId": strconv.FormatInt(record.ID, 10), "prefill": payload}, nil
	default:
		return nil, errors.New("Agent 操作类型不受支持")
	}
}

func loadReviewerTaskForAction(ctx context.Context, tx pgx.Tx, submissionID, reviewerID int64) (reviewerTask, error) {
	var task reviewerTask
	err := tx.QueryRow(ctx, `
		SELECT s.id,sr.id,s.student_id,
		       (SELECT count(*) FROM submission_reviewer expected WHERE expected.submission_id=s.id AND expected.active),
		       s.status,s.title,s.category_key,s.item_key,s.claim,s.requested_score::float8,s.rule_snapshot,s.markdown_note,s.submitted_at,
		       student.sid,student.name
		  FROM submission s JOIN app_user student ON student.id=s.student_id
		  JOIN submission_reviewer sr ON sr.submission_id=s.id AND sr.reviewer_id=$2 AND sr.active
		 WHERE s.id=$1 AND s.student_id<>$2
		   AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=$2 AND r.superseded_at IS NULL)
	`, submissionID, reviewerID).Scan(&task.SubmissionID, &task.AssignmentID, &task.StudentID, &task.Expected, &task.Status,
		&task.Title, &task.Category, &task.ItemKey, &task.Claim, &task.Requested, &task.Snapshot,
		&task.Note, &task.SubmittedAt, &task.StudentSID, &task.StudentName)
	return task, err
}

func uniqueAgentSchemeName(ctx context.Context, tx pgx.Tx, base string) (string, error) {
	for suffix := 1; suffix <= 1000; suffix++ {
		candidate := base
		if suffix > 1 {
			candidate = fmt.Sprintf("%s（%d）", base, suffix)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scheme WHERE status='draft' AND lower(name)=lower($1))`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", errors.New("无法生成不重复的方案草稿名称")
}

func newConfirmToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func samePreparedState(raw []byte, state map[string]any) bool {
	var prior map[string]any
	if json.Unmarshal(raw, &prior) != nil {
		return false
	}
	left, _ := json.Marshal(prior)
	right, _ := json.Marshal(state)
	return subtle.ConstantTimeCompare(left, right) == 1
}
