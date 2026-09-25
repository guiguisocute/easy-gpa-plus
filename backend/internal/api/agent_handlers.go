package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	_ "golang.org/x/image/webp"

	"easygpa/backend/internal/agentcontext"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
)

const (
	agentImageMaxBytes        int64 = 5 << 20
	agentImageMessageMaxBytes int64 = 12 << 20
	agentImageDailyMaxBytes   int64 = 100 << 20
	agentImageMaxCount              = 4
	agentImageHardMaxBytes    int64 = 50 << 20
	agentImageHardMaxCount          = 12
)

func (s *Server) agentStatus(c *gin.Context) {
	flags := s.runtimeFlags(c.Request.Context())
	settings, settingsErr := s.runtimeAISettings(c.Request.Context())
	if settingsErr != nil {
		writeServiceError(c, settingsErr)
		return
	}
	textBinding, textErr := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeAgentText)
	_, visionErr := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeAgentVision)
	configured := textErr == nil
	visionConfigured := visionErr == nil
	tx := mustTx(c)
	actor := mustActor(c)
	var approved bool
	_ = tx.QueryRow(c.Request.Context(), `SELECT external_processing_approved FROM knowledge_policy WHERE class_id=$1`, actor.ClassID).Scan(&approved)
	var used int
	var attachmentUsed int64
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT (SELECT count(*) FROM agent_message
		         WHERE actor_id=$1 AND role='user' AND created_at>=date_trunc('day',now())),
		       (SELECT COALESCE(sum(size_bytes),0) FROM agent_attachment
		         WHERE owner_id=$1 AND created_at>=date_trunc('day',now()))
	`, actor.UserID).Scan(&used, &attachmentUsed); err != nil {
		writeServiceError(c, err)
		return
	}
	enabled := flags.AIEnabled && flags.KnowledgeEnabled && flags.KnowledgeEgressEnabled && configured && approved
	reason := ""
	switch {
	case !flags.AIEnabled:
		reason = "agent_disabled"
	case !flags.KnowledgeEnabled:
		reason = "knowledge_disabled"
	case !flags.KnowledgeEgressEnabled || !approved:
		reason = "knowledge_consent_required"
	case !configured:
		reason = "agent_disabled"
	case used >= settings.AgentDailyMessages:
		reason = "quota_exceeded"
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"enabled": enabled, "reason": reason, "actionsEnabled": flags.AgentActionsEnabled,
		"externalProcessingApproved": approved,
		"quota":                      gin.H{"used": used, "limit": settings.AgentDailyMessages},
		"attachmentQuota": gin.H{
			"maxFileMb": settings.AgentAttachmentMaxFileMB, "maxMessageMb": settings.AgentAttachmentMaxMessageMB,
			"dailyMb": settings.AgentAttachmentDailyMB, "dailyUsedBytes": attachmentUsed,
			"maxCount": settings.AgentAttachmentMaxCount,
		},
		"model": gin.H{
			"configured": configured, "visionConfigured": visionConfigured,
			"inherited": configured && textBinding.ProviderID == "" && settings.AgentModel == "",
		},
	})
}

type agentAttachmentInput struct {
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
}

// presignAgentAttachment 建一个只属于当前用户的临时图片对象。它还不是消息的一部分；
// createAgentMessage 会在同一事务里锁住并绑定这些 ready 记录，避免把别人的附件 ID
// 或仍在上传的半成品塞进请求。
func (s *Server) presignAgentAttachment(c *gin.Context) {
	var input agentAttachmentInput
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "附件参数不正确", nil)
		return
	}
	settings, err := s.runtimeAISettings(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "agent_disabled", "Agent 模型配置尚未就绪", nil)
		return
	}
	maxFileBytes := int64(settings.AgentAttachmentMaxFileMB) << 20
	filename, mediaType, err := validateAgentAttachmentLimit(input, maxFileBytes)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "agent_attachment_invalid", err.Error(), nil)
		return
	}
	if !s.agentAttachmentGate(c) {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if err := lockUserQuota(c.Request.Context(), tx, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	var used int64
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT COALESCE(sum(size_bytes),0) FROM agent_attachment
		 WHERE owner_id=$1 AND created_at>=date_trunc('day',now())
	`, actor.UserID).Scan(&used); err != nil {
		writeServiceError(c, err)
		return
	}
	if used+input.SizeBytes > int64(settings.AgentAttachmentDailyMB)<<20 {
		writeError(c, http.StatusTooManyRequests, "agent_attachment_quota", fmt.Sprintf("今天上传给 Agent 的图片已达到 %d MB 上限", settings.AgentAttachmentDailyMB), nil)
		return
	}
	objectKey := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/agent/user-" + strconv.FormatInt(actor.UserID, 10) + "/" + randomObjectPart()
	var id int64
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO agent_attachment (class_id,owner_id,object_key,filename,media_type,size_bytes)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id
	`, actor.ClassID, actor.UserID, objectKey, filename, mediaType, input.SizeBytes).Scan(&id); err != nil {
		writeServiceError(c, err)
		return
	}
	upload, err := s.deps.Objects.PresignUpload(c.Request.Context(), objectKey, mediaType, input.SizeBytes, 15*time.Minute)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "agent.attachment_presigned", "agent_attachment", strconv.FormatInt(id, 10), nil, map[string]any{
		"filename": filename, "mediaType": mediaType, "sizeBytes": input.SizeBytes,
	}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	response := uploadPolicyJSON(upload)
	response["attachmentId"] = strconv.FormatInt(id, 10)
	c.JSON(http.StatusCreated, response)
}

func (s *Server) completeAgentAttachment(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		ETag string `json:"etag"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "上传完成参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey, filename, mediaType, status string
	var size int64
	err := tx.QueryRow(c.Request.Context(), `
		SELECT object_key,filename,media_type,size_bytes,status FROM agent_attachment
		 WHERE id=$1 AND owner_id=$2 AND message_id IS NULL FOR UPDATE
	`, id, actor.UserID).Scan(&objectKey, &filename, &mediaType, &size, &status)
	if notFound(c, err, "附件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status == "ready" {
		c.JSON(http.StatusOK, agentAttachmentJSON(id, filename, mediaType, size))
		return
	}
	info, err := s.deps.Objects.Stat(c.Request.Context(), objectKey)
	if err != nil {
		writeError(c, http.StatusConflict, "upload_incomplete", "图片尚未上传完成", nil)
		return
	}
	if info.Size != size {
		s.discardInvalidUpload(c, objectKey)
		writeError(c, http.StatusUnprocessableEntity, "size_mismatch", "图片实际大小与声明不一致", gin.H{"declared": size, "actual": info.Size})
		return
	}
	if contentType := strings.ToLower(strings.TrimSpace(info.ContentType)); contentType != "" && contentType != mediaType {
		s.discardInvalidUpload(c, objectKey)
		writeError(c, http.StatusUnprocessableEntity, "media_type_mismatch", "图片的实际媒体类型与声明不一致", nil)
		return
	}
	if etag := strings.Trim(input.ETag, `"`); etag != "" && !strings.EqualFold(etag, strings.Trim(info.ETag, `"`)) {
		writeError(c, http.StatusUnprocessableEntity, "etag_mismatch", "图片的 ETag 不匹配", nil)
		return
	}
	reader, err := s.deps.Objects.Open(c.Request.Context(), objectKey)
	if err != nil {
		writeError(c, http.StatusConflict, "upload_incomplete", "图片尚未上传完成", nil)
		return
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, agentImageHardMaxBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) != size {
		s.discardInvalidUpload(c, objectKey)
		writeError(c, http.StatusUnprocessableEntity, "agent_attachment_invalid", "图片读取或大小校验失败", nil)
		return
	}
	if err := validateAgentImageBytes(data, mediaType); err != nil {
		s.discardInvalidUpload(c, objectKey)
		writeError(c, http.StatusUnprocessableEntity, "agent_attachment_invalid", err.Error(), nil)
		return
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE agent_attachment SET status='ready',sha256=$2,uploaded_at=now(),updated_at=now()
		 WHERE id=$1
	`, id, digest); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "agent.attachment_completed", "agent_attachment", strconv.FormatInt(id, 10), map[string]any{"status": "uploading"}, map[string]any{"status": "ready"}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	s.scheduleStorageReconcile(c, actor.ClassID)
	c.JSON(http.StatusOK, agentAttachmentJSON(id, filename, mediaType, size))
}

func (s *Server) deleteAgentAttachment(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey string
	err := tx.QueryRow(c.Request.Context(), `
		SELECT object_key FROM agent_attachment
		 WHERE id=$1 AND owner_id=$2 AND message_id IS NULL FOR UPDATE
	`, id, actor.UserID).Scan(&objectKey)
	if notFound(c, err, "未发送附件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM agent_attachment WHERE id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "agent.attachment_deleted", "agent_attachment", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	c.Status(http.StatusNoContent)
}

func (s *Server) agentAttachmentURL(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey, filename, mediaType string
	var size int64
	err := tx.QueryRow(c.Request.Context(), `
		SELECT object_key,filename,media_type,size_bytes FROM agent_attachment
		 WHERE id=$1 AND owner_id=$2 AND status='ready'
	`, id, actor.UserID).Scan(&objectKey, &filename, &mediaType, &size)
	if notFound(c, err, "附件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	link, err := s.deps.Objects.PresignGet(c.Request.Context(), objectKey, 10*time.Minute, filename, true)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "agent.attachment_url_issued", "agent_attachment", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"attachmentId": strconv.FormatInt(id, 10), "filename": filename, "mediaType": mediaType,
		"sizeBytes": size, "downloadUrl": link.String(), "expiresIn": 600,
	})
}

func (s *Server) agentAttachmentGate(c *gin.Context) bool {
	flags := s.runtimeFlags(c.Request.Context())
	if !flags.AIEnabled || !flags.KnowledgeEnabled {
		writeError(c, http.StatusNotImplemented, "agent_disabled", "Agent 当前未启用", nil)
		return false
	}
	if !flags.KnowledgeEgressEnabled {
		writeError(c, http.StatusForbidden, "knowledge_consent_required", "平台尚未允许内容发送到模型", nil)
		return false
	}
	if _, err := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeAgentVision); err != nil {
		writeError(c, http.StatusServiceUnavailable, "agent_disabled", "Agent 模型配置尚未就绪", nil)
		return false
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var approved bool
	_ = tx.QueryRow(c.Request.Context(), `SELECT external_processing_approved FROM knowledge_policy WHERE class_id=$1`, actor.ClassID).Scan(&approved)
	if !approved {
		writeError(c, http.StatusForbidden, "knowledge_consent_required", "本班尚未确认第三方处理授权", nil)
		return false
	}
	return true
}

func validateAgentAttachment(input agentAttachmentInput) (string, string, error) {
	return validateAgentAttachmentLimit(input, agentImageMaxBytes)
}

func validateAgentAttachmentLimit(input agentAttachmentInput, maxBytes int64) (string, string, error) {
	filename := safeFilename(input.Filename)
	mediaType := strings.ToLower(strings.TrimSpace(input.MediaType))
	if filename == "" || input.SizeBytes <= 0 {
		return "", "", errors.New("图片文件名和大小不正确")
	}
	if maxBytes <= 0 || maxBytes > agentImageHardMaxBytes {
		maxBytes = agentImageMaxBytes
	}
	if input.SizeBytes > maxBytes {
		return "", "", fmt.Errorf("单张图片不能超过 %d MB", maxBytes>>20)
	}
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(filename)), ".")
	expected := map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}[extension]
	if expected == "" || mediaType != expected {
		return "", "", errors.New("只支持扩展名与媒体类型一致的 PNG、JPEG 或 WebP 图片")
	}
	return filename, mediaType, nil
}

func validateAgentImageBytes(data []byte, mediaType string) error {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errors.New("文件内容不是可识别的图片")
	}
	expected := map[string]string{"image/jpeg": "jpeg", "image/png": "png", "image/webp": "webp"}[mediaType]
	if format != expected {
		return errors.New("图片内容与声明格式不一致")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 12_000 || config.Height > 12_000 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return errors.New("图片尺寸过大，请缩小到 4000 万像素以内")
	}
	return nil
}

func parseAgentAttachmentIDs(raw []string) ([]int64, error) {
	return parseAgentAttachmentIDsLimit(raw, agentImageMaxCount)
}

func parseAgentAttachmentIDsLimit(raw []string, maxCount int) ([]int64, error) {
	if maxCount <= 0 || maxCount > agentImageHardMaxCount {
		maxCount = agentImageMaxCount
	}
	if len(raw) > maxCount {
		return nil, fmt.Errorf("每条消息最多添加 %d 张图片", maxCount)
	}
	ids := make([]int64, 0, len(raw))
	seen := make(map[int64]struct{}, len(raw))
	for _, value := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("图片附件 ID 不正确")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errors.New("同一张图片不能重复添加")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func agentAttachmentJSON(id int64, filename, mediaType string, size int64) gin.H {
	return gin.H{"id": strconv.FormatInt(id, 10), "filename": filename, "mediaType": mediaType, "sizeBytes": size}
}

func (s *Server) agentConversations(c *gin.Context) {
	tx := mustTx(c)
	actor := mustActor(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT c.id,c.title,c.created_at,c.updated_at,
		       COALESCE((SELECT left(m.content,120) FROM agent_message m WHERE m.conversation_id=c.id AND m.role='user' ORDER BY m.id DESC LIMIT 1),''),
		       COALESCE((SELECT jsonb_agg(DISTINCT jsonb_build_object('view',m.request_context->>'view',
		         'resourceKind',m.request_context->>'resourceKind','resourceId',m.request_context->>'resourceId'))
		         FROM agent_message m WHERE m.conversation_id=c.id AND COALESCE(m.request_context->>'resourceKind','')<>''),'[]'::jsonb)
		  FROM agent_conversation c WHERE c.owner_id=$1 AND c.deleted_at IS NULL
		 ORDER BY c.updated_at DESC,c.id DESC LIMIT 100
	`, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	contexts := make([][]agentcontext.Context, 0)
	for rows.Next() {
		var id int64
		var title, preview string
		var rawContexts []byte
		var created, updated time.Time
		if err := rows.Scan(&id, &title, &created, &updated, &preview, &rawContexts); err != nil {
			writeServiceError(c, err)
			return
		}
		var bound []agentcontext.Context
		if err := json.Unmarshal(rawContexts, &bound); err != nil {
			writeServiceError(c, err)
			return
		}
		contexts = append(contexts, bound)
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "title": title, "preview": preview, "createdAt": created, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	rows.Close()
	// Titles and previews can contain the same business data as message bodies.
	// Recheck each distinct resource once before returning conversation metadata.
	revokedResources := make(map[string]bool)
	for i, bound := range contexts {
		for _, resource := range bound {
			key := resource.DocumentID()
			revoked, checked := revokedResources[key]
			if !checked {
				revoked = agentResourceRevoked(c.Request.Context(), tx, actor.UserID, resource)
				revokedResources[key] = revoked
			}
			if revoked {
				items[i]["title"], items[i]["preview"] = "部分事项已不可读取", ""
				break
			}
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) createAgentConversation(c *gin.Context) {
	var input struct {
		Title string `json:"title"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "对话参数不正确", nil)
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		input.Title = "新对话"
	}
	if len([]rune(input.Title)) > 120 || hasControl(input.Title) {
		writeError(c, http.StatusUnprocessableEntity, "conversation_title_invalid", "对话标题不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var id int64
	var created time.Time
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO agent_conversation (class_id,owner_id,title) VALUES ($1,$2,$3) RETURNING id,created_at
	`, actor.ClassID, actor.UserID, input.Title).Scan(&id, &created); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10), "title": input.Title, "messages": []any{}, "createdAt": created, "updatedAt": created})
}

func (s *Server) agentConversation(c *gin.Context) {
	conversationID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var title string
	var created, updated time.Time
	err := tx.QueryRow(c.Request.Context(), `
		SELECT title,created_at,updated_at FROM agent_conversation
		 WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL
	`, conversationID, actor.UserID).Scan(&title, &created, &updated)
	if notFound(c, err, "对话") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	messages, err := s.loadAgentMessages(c.Request.Context(), tx, conversationID, actor.UserID, actor.Role)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for _, message := range messages {
		if revoked, _ := message["sourceRevoked"].(bool); revoked {
			title = "部分事项已不可读取"
			break
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(conversationID, 10), "title": title, "messages": messages, "createdAt": created, "updatedAt": updated})
}

func (s *Server) deleteAgentConversation(c *gin.Context) {
	conversationID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var id int64
	err := tx.QueryRow(c.Request.Context(), `
		UPDATE agent_conversation SET deleted_at=now(),updated_at=now()
		 WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL RETURNING id
	`, conversationID, actor.UserID).Scan(&id)
	if notFound(c, err, "对话") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	_, _ = tx.Exec(c.Request.Context(), `UPDATE agent_message SET status='canceled',finished_at=now(),updated_at=now() WHERE conversation_id=$1 AND status IN ('queued','running')`, conversationID)
	c.Status(http.StatusNoContent)
}

func (s *Server) createAgentMessage(c *gin.Context) {
	conversationID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Content       string               `json:"content"`
		AttachmentIDs []string             `json:"attachmentIds"`
		Context       agentcontext.Context `json:"context"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "消息参数不正确", nil)
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	attachmentIDs, err := parseAgentAttachmentIDsLimit(input.AttachmentIDs, agentImageHardMaxCount)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "agent_attachment_invalid", err.Error(), nil)
		return
	}
	if (input.Content == "" && len(attachmentIDs) == 0) || len([]rune(input.Content)) > 10_000 || hasControlExceptWhitespace(input.Content) {
		writeError(c, http.StatusUnprocessableEntity, "message_invalid", "消息和图片不能同时为空，文字不能超过 10000 字", nil)
		return
	}
	flags := s.runtimeFlags(c.Request.Context())
	if !flags.AIEnabled {
		writeError(c, http.StatusNotImplemented, "agent_disabled", "Agent 当前未启用", nil)
		return
	}
	if !flags.KnowledgeEnabled {
		writeError(c, http.StatusNotImplemented, "knowledge_disabled", "班级知识库当前未启用", nil)
		return
	}
	if !flags.KnowledgeEgressEnabled {
		writeError(c, http.StatusForbidden, "knowledge_consent_required", "平台尚未允许知识内容发送到模型", nil)
		return
	}
	settings, err := s.runtimeAISettings(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "agent_disabled", "Agent 模型配置尚未就绪", nil)
		return
	}
	if len(attachmentIDs) > settings.AgentAttachmentMaxCount {
		writeError(c, http.StatusUnprocessableEntity, "agent_attachment_invalid", fmt.Sprintf("每条消息最多添加 %d 张图片", settings.AgentAttachmentMaxCount), nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	agentCtx, contextErr := agentcontext.ResolveInput(actor.Role, input.Context)
	if contextErr != nil {
		writeError(c, http.StatusUnprocessableEntity, "agent_context_invalid", "页面上下文不正确或当前角色无法采用该视角", nil)
		return
	}
	if agentCtx.ResourceKind != "" {
		resource, err := agentcontext.Load(c.Request.Context(), tx, actor.ClassID, actor.UserID, agentCtx)
		if err != nil {
			writeError(c, http.StatusForbidden, "agent_context_invalid", agentcontext.ErrResource.Error(), nil)
			return
		}
		if resource.CheckRevision(agentCtx.Revision) != nil {
			writeError(c, http.StatusConflict, "action_stale", agentcontext.ErrStale.Error(), nil)
			return
		}
		agentCtx = resource.Context
	}
	var approved bool
	_ = tx.QueryRow(c.Request.Context(), `SELECT external_processing_approved FROM knowledge_policy WHERE class_id=$1`, actor.ClassID).Scan(&approved)
	if !approved {
		writeError(c, http.StatusForbidden, "knowledge_consent_required", "本班尚未确认第三方处理授权", nil)
		return
	}
	if err := lockUserQuota(c.Request.Context(), tx, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	var daily int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM agent_message WHERE actor_id=$1 AND role='user' AND created_at>=date_trunc('day',now())`, actor.UserID).Scan(&daily); err != nil {
		writeServiceError(c, err)
		return
	}
	if daily >= settings.AgentDailyMessages {
		writeError(c, http.StatusTooManyRequests, "quota_exceeded", "今天的 Agent 消息额度已用完", gin.H{"limit": settings.AgentDailyMessages})
		return
	}
	var exists bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM agent_conversation WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL)`, conversationID, actor.UserID).Scan(&exists); err != nil {
		writeServiceError(c, err)
		return
	}
	if !exists {
		writeError(c, http.StatusNotFound, "not_found", "对话不存在", nil)
		return
	}
	var attachmentBytes int64
	if len(attachmentIDs) > 0 {
		rows, err := tx.Query(c.Request.Context(), `
			SELECT id,size_bytes FROM agent_attachment
			 WHERE id=ANY($1) AND owner_id=$2 AND message_id IS NULL AND status='ready'
			 FOR UPDATE
		`, attachmentIDs, actor.UserID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		found := 0
		for rows.Next() {
			var id, size int64
			if err := rows.Scan(&id, &size); err != nil {
				rows.Close()
				writeServiceError(c, err)
				return
			}
			found++
			attachmentBytes += size
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			writeServiceError(c, err)
			return
		}
		if found != len(attachmentIDs) {
			writeError(c, http.StatusUnprocessableEntity, "agent_attachment_invalid", "有图片不存在、尚未上传完成或已经发送", nil)
			return
		}
		if attachmentBytes > int64(settings.AgentAttachmentMaxMessageMB)<<20 {
			writeError(c, http.StatusRequestEntityTooLarge, "agent_attachment_too_large", fmt.Sprintf("每条消息的图片合计不能超过 %d MB", settings.AgentAttachmentMaxMessageMB), nil)
			return
		}
	}
	contextRaw, _ := json.Marshal(agentCtx)
	var historyHasImages bool
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM agent_attachment a
			JOIN agent_message m ON m.id=a.message_id
			WHERE m.conversation_id=$1 AND a.status='ready'
		)
	`, conversationID).Scan(&historyHasImages); err != nil {
		writeServiceError(c, err)
		return
	}
	purpose := opsconfig.PurposeAgentText
	if len(attachmentIDs) > 0 || historyHasImages || agentCtx.ResourceKind != "" {
		purpose = opsconfig.PurposeAgentVision
	}
	binding, err := s.runtimeModelBinding(c.Request.Context(), purpose)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "agent_disabled", "Agent 模型路由尚未就绪", nil)
		return
	}
	snapshot := binding.Snapshot()
	snapshot.Legacy = binding.ProviderID == ""
	assistantContext := map[string]any{}
	_ = json.Unmarshal(contextRaw, &assistantContext)
	assistantContext["modelRoute"] = snapshot
	assistantContextRaw, _ := json.Marshal(assistantContext)
	var userMessageID, assistantMessageID int64
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO agent_message
		    (class_id,conversation_id,actor_id,actor_role,role,status,content,request_context,finished_at)
		VALUES ($1,$2,$3,$4,'user','complete',$5,$6,now()) RETURNING id
	`, actor.ClassID, conversationID, actor.UserID, actor.Role, input.Content, contextRaw).Scan(&userMessageID); err != nil {
		writeServiceError(c, err)
		return
	}
	if len(attachmentIDs) > 0 {
		tag, err := tx.Exec(c.Request.Context(), `
			UPDATE agent_attachment SET message_id=$2,updated_at=now()
			 WHERE id=ANY($1) AND owner_id=$3 AND message_id IS NULL AND status='ready'
		`, attachmentIDs, userMessageID, actor.UserID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if tag.RowsAffected() != int64(len(attachmentIDs)) {
			writeError(c, http.StatusConflict, "agent_attachment_invalid", "图片状态已变化，请重新添加", nil)
			return
		}
	}
	selectedModel := binding.Model
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO agent_message
		    (class_id,conversation_id,actor_id,actor_role,role,status,model,request_context)
		VALUES ($1,$2,$3,$4,'assistant','queued',$5,$6) RETURNING id
	`, actor.ClassID, conversationID, actor.UserID, actor.Role, selectedModel, assistantContextRaw).Scan(&assistantMessageID); err != nil {
		writeServiceError(c, err)
		return
	}
	_, _ = tx.Exec(c.Request.Context(), `
		UPDATE agent_conversation
		   SET title=CASE WHEN title='新对话' THEN left(COALESCE(NULLIF($2,''),'图片提问'),80) ELSE title END,updated_at=now()
		 WHERE id=$1
	`, conversationID, input.Content)
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AgentMessageCreatedEvent(),
		events.AgentMessageCreatedPayload{MessageID: strconv.FormatInt(assistantMessageID, 10)}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "agent.message_created", "agent_message", strconv.FormatInt(assistantMessageID, 10), nil, map[string]any{"status": "queued"}, map[string]any{"conversationId": strconv.FormatInt(conversationID, 10), "userMessageId": strconv.FormatInt(userMessageID, 10), "attachments": len(attachmentIDs), "model": selectedModel}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"messageId": strconv.FormatInt(assistantMessageID, 10), "status": "queued"})
}

func (s *Server) cancelAgentMessage(c *gin.Context) {
	messageID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var status string
	err := tx.QueryRow(c.Request.Context(), `
		UPDATE agent_message m SET status='canceled',finished_at=now(),updated_at=now()
		  FROM agent_conversation c
		 WHERE m.id=$1 AND m.conversation_id=c.id AND c.owner_id=$2 AND m.role='assistant'
		   AND m.status IN ('queued','running') RETURNING m.status
	`, messageID, actor.UserID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		var current string
		if tx.QueryRow(c.Request.Context(), `SELECT m.status FROM agent_message m JOIN agent_conversation c ON c.id=m.conversation_id WHERE m.id=$1 AND c.owner_id=$2`, messageID, actor.UserID).Scan(&current) == nil {
			c.JSON(http.StatusOK, gin.H{"messageId": strconv.FormatInt(messageID, 10), "status": current})
			return
		}
		writeError(c, http.StatusNotFound, "not_found", "消息不存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"messageId": strconv.FormatInt(messageID, 10), "status": status})
}

// agentSource 解析一条引用，返回原件的短时效下载地址。
//
// 它以前回的是规范化正文，界面据此弹一个预览框——但那对当前方案这种来源就是
// 一整份配置 JSON，读不了。引用要的是"这句话出自哪儿"，给一个能取到原件的链接
// 比给一段截取的纯文本更有用，也更接近原始证据。
//
// 可见范围的判定与其他知识接口完全一致，所以能下到的原件正是本来就读得到的那些；
// 变的是拿到的东西（原件而不是抽取文本），不是拿得到的范围。
func (s *Server) agentSource(c *gin.Context) {
	documentRaw := strings.TrimSpace(c.Param("documentId"))
	entryRaw := strings.TrimSpace(c.Param("entryId"))
	if strings.HasPrefix(documentRaw, "business-") {
		s.agentBusinessSource(c, documentRaw, entryRaw)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if strings.HasPrefix(documentRaw, "platform-") {
		if s.deps.Pools == nil || s.deps.Pools.Ops == nil {
			writeError(c, http.StatusServiceUnavailable, "source_unavailable", "平台知识来源暂不可用", nil)
			return
		}
		documentID, err := strconv.ParseInt(strings.TrimPrefix(documentRaw, "platform-"), 10, 64)
		entryID, entryErr := strconv.ParseInt(entryRaw, 10, 64)
		audienceRole := strings.TrimSpace(c.Query("audienceRole"))
		if audienceRole == "" {
			audienceRole = actor.Role
		}
		if err != nil || documentID <= 0 || entryErr != nil || entryID <= 0 || !agentcontext.CanAssume(actor.Role, audienceRole) {
			writeError(c, http.StatusBadRequest, "invalid_id", "平台来源或回答视角不正确", nil)
			return
		}
		var sourceKind, filename, displayName, text string
		var locator []byte
		var objectKey *string
		err = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
			SELECT d.source_kind,d.filename,d.display_name,e.locator,e.plain_text,b.object_key
			  FROM platform_knowledge_document d
			  JOIN platform_knowledge_entry e ON e.document_id=d.id
			  LEFT JOIN platform_knowledge_blob b ON b.id=d.blob_id
			 WHERE d.id=$1 AND e.id=$2 AND d.deleted_at IS NULL AND d.enabled AND d.searchable
			   AND d.status IN ('ready','partial') AND $3=ANY(d.allowed_roles)
		`, documentID, entryID, audienceRole).Scan(&sourceKind, &filename, &displayName, &locator, &text, &objectKey)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, http.StatusNotFound, "source_forbidden", "来源不存在或权限已被收回", nil)
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		response := gin.H{"documentId": documentRaw, "entryId": entryRaw, "scope": "platform", "sourceKind": sourceKind, "filename": displayName, "logicalPath": "平台产品知识/" + displayName, "locator": json.RawMessage(locator), "downloadUrl": "", "expiresIn": 0, "content": "", "mediaType": "text/markdown; charset=utf-8"}
		if sourceKind == "custom" && objectKey != nil && *objectKey != "" {
			downloadURL, err := s.deps.Objects.PresignGet(c.Request.Context(), *objectKey, 10*time.Minute, filename, false)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			response["downloadUrl"], response["expiresIn"] = downloadURL.String(), 600
		} else {
			response["content"] = text
		}
		if err := appendAudit(c, tx, "platform_knowledge.source_read", "platform_knowledge_document", strconv.FormatInt(documentID, 10), nil, nil, map[string]any{"entryId": entryRaw, "audienceRole": audienceRole}); err != nil {
			writeServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, response)
		return
	}
	// 当前已发布方案不是上传的文件，没有原件可下；只回它的标识，界面渲染成纯文本。
	if strings.HasPrefix(documentRaw, "scheme-") && entryRaw == "0" {
		schemeID, err := strconv.ParseInt(strings.TrimPrefix(documentRaw, "scheme-"), 10, 64)
		if err != nil || schemeID <= 0 {
			writeError(c, http.StatusBadRequest, "invalid_id", "来源 ID 不正确", nil)
			return
		}
		var name string
		var version int
		if err := tx.QueryRow(c.Request.Context(), `SELECT name,version FROM scheme WHERE id=$1 AND status='published'`, schemeID).Scan(&name, &version); err != nil {
			if notFound(c, err, "来源") {
				return
			}
			writeServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"documentId": documentRaw, "entryId": "0", "filename": name, "logicalPath": "系统/当前已发布方案", "locator": gin.H{"version": version}, "downloadUrl": "", "expiresIn": 0})
		return
	}
	documentID, err := strconv.ParseInt(documentRaw, 10, 64)
	if err != nil || documentID <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_id", "来源 ID 不正确", nil)
		return
	}
	// entryId 为空或 0 是文件级引用：模型把整份文件交给用户，没有指向某一段。
	entryID := int64(0)
	if entryRaw != "" && entryRaw != "0" {
		entryID, err = strconv.ParseInt(entryRaw, 10, 64)
		if err != nil || entryID <= 0 {
			writeError(c, http.StatusBadRequest, "invalid_id", "来源条目 ID 不正确", nil)
			return
		}
	}
	var displayName, filename, logicalPath, objectKey string
	var locator []byte
	err = tx.QueryRow(c.Request.Context(), `
		SELECT d.display_name,d.filename,d.logical_path,b.object_key,
		       (SELECT e.locator FROM knowledge_entry e WHERE e.blob_id=d.blob_id AND e.id=$2)
		  FROM knowledge_document d
		  JOIN knowledge_blob b ON b.id=d.blob_id
		 WHERE d.id=$1 AND d.deleted_at IS NULL
		   AND ($2=0 OR EXISTS (SELECT 1 FROM knowledge_entry e WHERE e.blob_id=d.blob_id AND e.id=$2))
		   AND (d.visibility='class' OR ($3 IN ('group','class_admin') AND d.visibility='review') OR ($3='class_admin' AND d.visibility='admin'))
	`, documentID, entryID, actor.Role).Scan(&displayName, &filename, &logicalPath, &objectKey, &locator)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusNotFound, "source_forbidden", "来源不存在或权限已被收回", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	downloadURL, err := s.deps.Objects.PresignGet(c.Request.Context(), objectKey, 10*time.Minute, filename, false)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// 取原件比读摘录敏感，留一条审计。.url_issued 已经是既有的读操作后缀。
	if err := appendAudit(c, tx, "knowledge.source_url_issued", "knowledge_document", documentRaw, nil, nil, map[string]any{"entryId": entryRaw}); err != nil {
		writeServiceError(c, err)
		return
	}
	// 文件级引用没有条目，locator 是 NULL；这里要回 {} 而不是 null，前端 schema 只
	// 认对象。
	locatorMap := map[string]any{}
	_ = json.Unmarshal(locator, &locatorMap)
	c.JSON(http.StatusOK, gin.H{"documentId": documentRaw, "entryId": entryRaw, "filename": displayName, "logicalPath": logicalPath, "locator": locatorMap, "downloadUrl": downloadURL.String(), "expiresIn": 600})
}

func (s *Server) loadAgentMessages(ctx context.Context, tx pgx.Tx, conversationID, ownerID int64, role string) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.id,m.role,m.status,m.content,m.citations,m.tool_trace,m.final_thought,m.error,m.created_at,m.finished_at,m.request_context
		  FROM agent_message m JOIN agent_conversation c ON c.id=m.conversation_id
		 WHERE m.conversation_id=$1 AND c.owner_id=$2 AND c.deleted_at IS NULL ORDER BY m.id
	`, conversationID, ownerID)
	if err != nil {
		return nil, err
	}
	type messageRow struct {
		id                        int64
		messageRole, status, body string
		finalThought              string
		citations, trace          []byte
		errorMessage              *string
		created                   time.Time
		finished                  *time.Time
		requestContext            []byte
	}
	scanned := make([]messageRow, 0)
	for rows.Next() {
		var item messageRow
		if err := rows.Scan(&item.id, &item.messageRole, &item.status, &item.body, &item.citations, &item.trace, &item.finalThought, &item.errorMessage, &item.created, &item.finished, &item.requestContext); err != nil {
			rows.Close()
			return nil, err
		}
		scanned = append(scanned, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	// pgx transactions use one connection. Finish and close the message cursor
	// before the per-message authorization/action queries below, otherwise the
	// nested query fails with "conn busy" as soon as a conversation has content.
	rows.Close()
	attachments := make(map[int64][]gin.H)
	attachmentRows, err := tx.Query(ctx, `
		SELECT a.id,a.message_id,a.filename,a.media_type,a.size_bytes
		  FROM agent_attachment a JOIN agent_message m ON m.id=a.message_id
		 WHERE m.conversation_id=$1 AND a.status='ready' ORDER BY a.message_id,a.id
	`, conversationID)
	if err != nil {
		return nil, err
	}
	for attachmentRows.Next() {
		var id, messageID, size int64
		var filename, mediaType string
		if err := attachmentRows.Scan(&id, &messageID, &filename, &mediaType, &size); err != nil {
			attachmentRows.Close()
			return nil, err
		}
		attachments[messageID] = append(attachments[messageID], agentAttachmentJSON(id, filename, mediaType, size))
	}
	if err := attachmentRows.Err(); err != nil {
		attachmentRows.Close()
		return nil, err
	}
	attachmentRows.Close()

	items := make([]gin.H, 0, len(scanned))
	for _, item := range scanned {
		revoked, err := s.citationsRevoked(ctx, tx, item.citations, role, ownerID)
		if err != nil {
			return nil, err
		}
		var messageContext agentcontext.Context
		_ = json.Unmarshal(item.requestContext, &messageContext)
		revoked = revoked || agentResourceRevoked(ctx, tx, ownerID, messageContext)
		// Drafts and model-route internals are never replayed to the UI.
		messageContext.Draft = nil
		actions, err := loadAgentActions(ctx, tx, item.id, ownerID)
		if err != nil {
			return nil, err
		}
		// 运行中的消息还没有写 tool_trace（那是完成时才落的快照），所以改读
		// agent_tool_call 的逐步行——Worker 在每一步开始时就写了一行，前端因此
		// 能在回答出现之前看到 Agent 正在做什么。
		if item.status == "queued" || item.status == "running" {
			live, err := loadLiveToolTrace(ctx, tx, item.id)
			if err != nil {
				return nil, err
			}
			item.trace = live
		}
		if revoked {
			item.body = ""
			item.citations = []byte(`[]`)
			item.finalThought = ""
			item.trace = []byte(`[]`)
			actions = []gin.H{}
			messageContext = agentcontext.Context{}
		}
		messageAttachments := attachments[item.id]
		if messageAttachments == nil {
			messageAttachments = []gin.H{}
		}
		if revoked {
			messageAttachments = []gin.H{}
		}
		items = append(items, gin.H{"id": strconv.FormatInt(item.id, 10), "role": item.messageRole, "status": item.status, "content": item.body, "attachments": messageAttachments, "citations": json.RawMessage(item.citations), "toolTrace": json.RawMessage(item.trace), "finalThought": item.finalThought, "actions": actions, "context": messageContext, "sourceRevoked": revoked, "error": item.errorMessage, "createdAt": item.created, "finishedAt": item.finished})
	}
	return items, nil
}

// loadLiveToolTrace 把运行中消息的工具行拼成和 agent_message.tool_trace 相同的
// JSON 形状，前端两种情况可以用同一个解析器。
func loadLiveToolTrace(ctx context.Context, tx pgx.Tx, messageID int64) ([]byte, error) {
	rows, err := tx.Query(ctx, `
		SELECT seq,tool,status,thought,summary,duration_ms
		  FROM agent_tool_call WHERE message_id=$1 ORDER BY seq
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type traceItem struct {
		Seq        int    `json:"seq"`
		Tool       string `json:"tool"`
		Status     string `json:"status"`
		Thought    string `json:"thought"`
		Summary    string `json:"summary"`
		DurationMS int64  `json:"durationMs"`
	}
	trace := make([]traceItem, 0)
	for rows.Next() {
		var item traceItem
		if err := rows.Scan(&item.Seq, &item.Tool, &item.Status, &item.Thought, &item.Summary, &item.DurationMS); err != nil {
			return nil, err
		}
		trace = append(trace, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(trace)
}

func (s *Server) citationsRevoked(ctx context.Context, tx pgx.Tx, raw []byte, role string, ownerID int64) (bool, error) {
	var citations []struct {
		DocumentID   string `json:"documentId"`
		AudienceRole string `json:"audienceRole"`
		EntryID      string `json:"entryId"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &citations) != nil {
		return false, nil
	}
	for _, citation := range citations {
		if strings.HasPrefix(citation.DocumentID, "business-") {
			input, err := agentcontext.ParseDocumentID(citation.DocumentID)
			if err != nil {
				return true, nil
			}
			if strings.HasPrefix(citation.EntryID, "evidence-") {
				input.EvidenceID = strings.TrimPrefix(citation.EntryID, "evidence-")
			}
			if agentResourceRevoked(ctx, tx, ownerID, input) {
				return true, nil
			}
			continue
		}
		if strings.HasPrefix(citation.DocumentID, "scheme-") {
			continue
		}
		if strings.HasPrefix(citation.DocumentID, "platform-") {
			id, err := strconv.ParseInt(strings.TrimPrefix(citation.DocumentID, "platform-"), 10, 64)
			audienceRole := citation.AudienceRole
			if audienceRole == "" {
				audienceRole = role
			}
			if err != nil || id <= 0 || !agentcontext.CanAssume(role, audienceRole) || s.deps.Pools == nil || s.deps.Pools.Ops == nil {
				return true, nil
			}
			var allowed bool
			if err := s.deps.Pools.Ops.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform_knowledge_document WHERE id=$1 AND deleted_at IS NULL AND enabled AND searchable AND status IN ('ready','partial') AND $2=ANY(allowed_roles))`, id, audienceRole).Scan(&allowed); err != nil {
				return false, err
			}
			if !allowed {
				return true, nil
			}
			continue
		}
		id, err := strconv.ParseInt(citation.DocumentID, 10, 64)
		if err != nil || id <= 0 {
			return true, nil
		}
		var allowed bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM knowledge_document d WHERE d.id=$1 AND d.deleted_at IS NULL
			 AND (d.visibility='class' OR ($2 IN ('group','class_admin') AND d.visibility='review') OR ($2='class_admin' AND d.visibility='admin')))
		`, id, role).Scan(&allowed); err != nil {
			return false, err
		}
		if !allowed {
			return true, nil
		}
	}
	return false, nil
}

func hasControlExceptWhitespace(value string) bool {
	for _, char := range value {
		if char < 32 && char != '\n' && char != '\r' && char != '\t' {
			return true
		}
	}
	return false
}
