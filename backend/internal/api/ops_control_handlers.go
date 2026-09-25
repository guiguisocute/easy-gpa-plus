package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/workerhealth"
)

var opsWorkerNames = []string{"dispatch", "notify", "export", "maintenance", "backup", "ai", "agent"}

func (s *Server) opsWorkers(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	items := make([]workerhealth.State, 0, len(opsWorkerNames))
	for _, name := range opsWorkerNames {
		items = append(items, s.workerRuntimeState(ctx, name))
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "worker.list", "worker", "", gin.H{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) workerRuntimeState(ctx context.Context, name string) workerhealth.State {
	state := workerhealth.State{Name: name}
	if s.deps.Redis == nil {
		state.HeartbeatStatus = workerhealth.Error
		state.HeartbeatError = "redis unavailable"
		return state
	}
	raw, err := s.deps.Redis.Get(ctx, workerhealth.Key(name)).Result()
	applyWorkerHeartbeat(&state, time.Now(), raw, err)
	return state
}

func applyWorkerHeartbeat(state *workerhealth.State, now time.Time, raw string, err error) {
	if errors.Is(err, redis.Nil) {
		state.LastHeartbeat = nil
		state.HeartbeatAlive = false
		if state.HeartbeatStatus == "" {
			state.HeartbeatStatus = workerhealth.Missing
		}
		return
	}
	if err != nil {
		state.HeartbeatAlive = false
		state.HeartbeatStatus = workerhealth.Error
		state.HeartbeatError = err.Error()
		return
	}
	heartbeat, parseErr := time.Parse(time.RFC3339Nano, raw)
	if parseErr != nil {
		state.HeartbeatAlive = false
		state.HeartbeatStatus = workerhealth.Error
		state.HeartbeatError = parseErr.Error()
		return
	}
	state.LastHeartbeat = &heartbeat
	age := now.Sub(heartbeat)
	state.HeartbeatAlive = age >= -time.Minute && age < time.Minute
	if state.HeartbeatAlive {
		if state.HeartbeatStatus == "" {
			state.HeartbeatStatus = workerhealth.Alive
		}
		return
	}
	if state.HeartbeatStatus == "" {
		state.HeartbeatStatus = workerhealth.Stale
	}
}

func (s *Server) opsDeadLetters(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	end := "+"
	if before := strings.TrimSpace(c.Query("before")); before != "" {
		if !redisStreamID(before) {
			writeError(c, http.StatusBadRequest, "cursor_invalid", "死信游标格式不正确", nil)
			return
		}
		end = "(" + before
	}
	messages, err := s.deps.Redis.XRevRangeN(c.Request.Context(), events.DeadStream, end, "-", int64(limit+1)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		writeServiceError(c, err)
		return
	}
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	items := make([]gin.H, 0, len(messages))
	for _, message := range messages {
		values := make(map[string]string, 8)
		for _, key := range []string{"event_id", "class_id", "type", "group", "error", "last_error", "attempt", "attempts"} {
			if value := strings.TrimSpace(stringValue(message.Values[key])); value != "" {
				values[key] = value
			}
		}
		if payload := strings.TrimSpace(stringValue(message.Values["payload"])); payload != "" {
			hash := sha256.Sum256([]byte(payload))
			values["payload_sha256"] = fmt.Sprintf("%x", hash[:])
		}
		items = append(items, gin.H{"id": message.ID, "values": values})
	}
	var next any
	if hasMore && len(messages) > 0 {
		next = messages[len(messages)-1].ID
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "queue.dead_list", "queue", events.DeadStream, gin.H{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": items, "nextCursor": next})
}

func redisStreamID(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

func stringValue(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case []byte:
		return string(item)
	default:
		return ""
	}
}

func (s *Server) opsLifecycle(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	policy, err := s.opsConfig.Lifecycle(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "lifecycle.read", "ops_config", "lifecycle", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"policy": policy})
}

func (s *Server) updateOpsLifecycle(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input opsconfig.Lifecycle
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "生命周期策略参数不正确", nil)
		return
	}
	policy, err := opsconfig.ValidateLifecycle(input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "lifecycle_invalid", err.Error(), nil)
		return
	}
	raw, _ := json.Marshal(policy)
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
		INSERT INTO ops_config (key,value) VALUES ('lifecycle',$1)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()
	`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("lifecycle")
	if err := s.appendOpsAudit(c.Request.Context(), c, "lifecycle.updated", "ops_config", "lifecycle", policy); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy})
}

func (s *Server) createBackup(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	if s.cfg.BackupDir == "" {
		writeError(c, http.StatusServiceUnavailable, "backup_unavailable", "备份 Worker 尚未配置 BACKUP_DIR", nil)
		return
	}
	var outstanding int
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT count(*) FROM backup_job WHERE kind='backup' AND status IN ('queued','running')`).Scan(&outstanding); err != nil {
		writeServiceError(c, err)
		return
	}
	if outstanding >= 3 {
		writeError(c, http.StatusConflict, "backup_busy", "已有多个备份任务等待执行，请稍后再试", gin.H{"outstanding": outstanding})
		return
	}
	var id string
	err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		INSERT INTO backup_job (kind,status,offsite,detail)
		VALUES ('backup','queued',false,jsonb_build_object('manual',true,'requestedBy',$1::text))
		RETURNING id::text
	`, s.cfg.OpsAccount).Scan(&id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "backup.requested", "backup", id, gin.H{"manual": true}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": id, "status": "queued"})
}

func (s *Server) reconcileTenantStorage(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := s.reconcileClassStorage(c.Request.Context(), id); err != nil {
		writeServiceError(c, err)
		return
	}
	var bytes int64
	var calibratedAt time.Time
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT storage_bytes,storage_calibrated_at FROM class WHERE id=$1`, id).Scan(&bytes, &calibratedAt); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "tenant.storage_reconciled", "tenant", strconv.FormatInt(id, 10), gin.H{"storageBytes": bytes}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "storageBytes": bytes, "storageCalibratedAt": calibratedAt})
}

func (s *Server) opsTemplateShareRequests(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && status != "pending" && status != "approved" && status != "rejected" && status != "canceled" {
		writeError(c, http.StatusBadRequest, "status_invalid", "共享申请状态不正确", nil)
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `
		SELECT r.id::text,r.class_id::text,c.name,r.scheme_id::text,r.name,r.status,
		       r.review_reason,r.reviewed_by,r.template_id::text,r.created_at,r.reviewed_at
		  FROM template_share_request r JOIN class c ON c.id=r.class_id
		 WHERE ($1='' OR r.status=$1)
		 ORDER BY CASE WHEN r.status='pending' THEN 0 ELSE 1 END,r.created_at DESC,r.id DESC
		 LIMIT 200
	`, status)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, classID, className, schemeID, name, state string
		var reason, reviewedBy, templateID *string
		var createdAt time.Time
		var reviewedAt *time.Time
		if err := rows.Scan(&id, &classID, &className, &schemeID, &name, &state, &reason, &reviewedBy, &templateID, &createdAt, &reviewedAt); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "tenantId": classID, "tenantName": className, "schemeId": schemeID, "name": name, "status": state, "reviewReason": reason, "reviewedBy": reviewedBy, "templateId": templateID, "createdAt": createdAt, "reviewedAt": reviewedAt})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "template_share.list", "template_share_request", "", gin.H{"count": len(items), "status": status}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) reviewTemplateShareRequest(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if !backupIDPattern.MatchString(id) {
		writeError(c, http.StatusBadRequest, "request_id_invalid", "共享申请 ID 格式不正确", nil)
		return
	}
	var input struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "审核参数不正确", nil)
		return
	}
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Decision != "approved" && input.Decision != "rejected" {
		writeError(c, http.StatusUnprocessableEntity, "decision_invalid", "审核结果只能是 approved 或 rejected", nil)
		return
	}
	if len([]rune(input.Reason)) > 1000 || (input.Decision == "rejected" && input.Reason == "") {
		writeError(c, http.StatusUnprocessableEntity, "reason_invalid", "驳回时必须填写不超过 1000 字的原因", nil)
		return
	}
	flags, err := s.opsConfig.Flags(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var classID int64
	var name, status string
	var raw []byte
	err = tx.QueryRow(c.Request.Context(), `
		SELECT class_id,name,status,config FROM template_share_request WHERE id=$1::uuid FOR UPDATE
	`, id).Scan(&classID, &name, &status, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusNotFound, "not_found", "共享申请不存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "pending" {
		writeError(c, http.StatusConflict, "request_reviewed", "这份共享申请已经处理", gin.H{"status": status})
		return
	}
	var templateID *int64
	if input.Decision == "approved" {
		template, err := scheme.DecodeTemplate(raw)
		if err != nil {
			writeError(c, http.StatusUnprocessableEntity, "template_invalid", "申请中的方案快照无法解析", nil)
			return
		}
		if err := scheme.ValidateTemplate(template); err != nil {
			writeError(c, http.StatusUnprocessableEntity, "template_invalid", "申请中的方案已无法通过完整校验", err.Error())
			return
		}
		config := scheme.ApplyTemplate(scheme.DefaultSelfReportConfig("template"), template)
		if err := validateSchemeEvidencePolicy(config, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
			writeError(c, http.StatusUnprocessableEntity, "template_invalid", "申请中的方案不符合当前平台佐证策略", err.Error())
			return
		}
		canonical, err := json.Marshal(template)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		var created int64
		if err := tx.QueryRow(c.Request.Context(), `
			INSERT INTO platform_template (name,config,source_class_id) VALUES ($1,$2,$3) RETURNING id
		`, name, canonical, classID).Scan(&created); err != nil {
			writeServiceError(c, err)
			return
		}
		templateID = &created
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE template_share_request
		   SET status=$2,review_reason=NULLIF($3,''),reviewed_by=$4,template_id=$5,reviewed_at=now()
		 WHERE id=$1::uuid
	`, id, input.Decision, input.Reason, s.cfg.OpsAccount, templateID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	metadata := gin.H{"decision": input.Decision, "reason": input.Reason, "tenantId": strconv.FormatInt(classID, 10)}
	if templateID != nil {
		metadata["templateId"] = strconv.FormatInt(*templateID, 10)
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "template_share.reviewed", "template_share_request", id, metadata); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "status": input.Decision, "templateId": templateID})
}
