package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/opsconfig"
)

const (
	platformKnowledgeMaxFiles     = 1000
	platformKnowledgeMaxStorageMB = 1024
	platformKnowledgePreviewLimit = 100
)

func platformKnowledgeLimits(ctx context.Context, s *Server) gin.H {
	base := s.knowledgeLimits(ctx)
	return gin.H{"fileMb": base.FileMB, "files": platformKnowledgeMaxFiles, "storageMb": platformKnowledgeMaxStorageMB, "pdfPages": base.PDFPages}
}

func (s *Server) opsPlatformKnowledge(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `
		SELECT d.id,d.source_kind,COALESCE(d.builtin_key,''),d.filename,d.display_name,d.allowed_roles,
		       d.page_tags,d.keywords,d.sort_order,d.status,d.enabled,d.searchable,COALESCE(d.extractor,''),d.entries_count,
		       d.page_count,d.sheet_count,COALESCE(d.warning,''),COALESCE(d.error,''),d.content_version,COALESCE(d.content_hash,''),d.created_at,d.updated_at,d.published_at,
		       COALESCE(b.media_type,'text/markdown'),COALESCE(b.size_bytes,0)
		  FROM platform_knowledge_document d LEFT JOIN platform_knowledge_blob b ON b.id=d.blob_id
		 WHERE d.deleted_at IS NULL ORDER BY d.sort_order,d.updated_at DESC,d.id DESC
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	documents := make([]gin.H, 0)
	for rows.Next() {
		var id, size int64
		var sourceKind, key, filename, displayName, status, extractor, contentVersion, contentHash, mediaType string
		var roles, views, keywords []string
		var enabled, searchable bool
		var order, entries int
		var pages, sheets *int
		var warning, conversionError *string
		var created, updated time.Time
		var published *time.Time
		if err := rows.Scan(&id, &sourceKind, &key, &filename, &displayName, &roles, &views, &keywords, &order, &status, &enabled, &searchable, &extractor, &entries, &pages, &sheets, &warning, &conversionError, &contentVersion, &contentHash, &created, &updated, &published, &mediaType, &size); err != nil {
			writeServiceError(c, err)
			return
		}
		documents = append(documents, gin.H{"id": strconv.FormatInt(id, 10), "sourceKind": sourceKind, "builtinKey": key, "filename": filename, "displayName": displayName, "allowedRoles": roles, "views": views, "keywords": keywords, "sortOrder": order, "status": status, "enabled": enabled, "searchable": searchable, "extractor": extractor, "entries": entries, "pageCount": pages, "sheetCount": sheets, "warning": warning, "error": conversionError, "contentVersion": contentVersion, "contentHash": contentHash, "mediaType": mediaType, "sizeBytes": size, "createdAt": created, "updatedAt": updated, "publishedAt": published})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	var total, ready, processing int
	var storage int64
	_ = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		SELECT count(*),count(*) FILTER (WHERE status IN ('ready','partial') AND searchable),count(*) FILTER (WHERE status IN ('uploading','queued','processing'))
		  FROM platform_knowledge_document WHERE deleted_at IS NULL
	`).Scan(&total, &ready, &processing)
	_ = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT COALESCE(sum(b.size_bytes),0) FROM platform_knowledge_blob b WHERE b.deleted_at IS NULL AND EXISTS (SELECT 1 FROM platform_knowledge_document d WHERE d.blob_id=b.id AND d.deleted_at IS NULL)`).Scan(&storage)
	_ = s.appendOpsAudit(c.Request.Context(), c, "platform_knowledge.list", "platform_knowledge_document", "", map[string]any{"count": len(documents)})
	c.JSON(http.StatusOK, gin.H{"limits": platformKnowledgeLimits(c.Request.Context(), s), "stats": gin.H{"totalFiles": total, "readyFiles": ready, "processingFiles": processing, "storageBytes": storage}, "documents": documents})
}

func (s *Server) presignOpsPlatformKnowledge(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input struct {
		Filename  string `json:"filename"`
		MediaType string `json:"mediaType"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "平台知识文件参数不正确", nil)
		return
	}
	filename, _, err := validateKnowledgePath(input.Filename, input.Filename)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "platform_knowledge_path_invalid", err.Error(), nil)
		return
	}
	limits := platformKnowledgeLimits(c.Request.Context(), s)
	maxMB := limits["fileMb"].(int)
	if input.SizeBytes <= 0 || input.SizeBytes > int64(maxMB)<<20 {
		writeError(c, http.StatusRequestEntityTooLarge, "platform_knowledge_file_too_large", fmt.Sprintf("单文件不能超过 %d MB", maxMB), nil)
		return
	}
	var count int
	var storage int64
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT count(*),COALESCE((SELECT sum(b.size_bytes) FROM platform_knowledge_blob b WHERE b.deleted_at IS NULL AND EXISTS (SELECT 1 FROM platform_knowledge_document d WHERE d.blob_id=b.id AND d.deleted_at IS NULL)),0) FROM platform_knowledge_document WHERE deleted_at IS NULL`).Scan(&count, &storage); err != nil {
		writeServiceError(c, err)
		return
	}
	if count >= platformKnowledgeMaxFiles {
		writeError(c, http.StatusUnprocessableEntity, "platform_knowledge_file_limit", "平台知识文件数已达上限", nil)
		return
	}
	if storage+input.SizeBytes > int64(platformKnowledgeMaxStorageMB)<<20 {
		writeError(c, http.StatusUnprocessableEntity, "platform_knowledge_storage_limit", "平台知识库存储量已达上限", nil)
		return
	}
	key := "platform/knowledge/raw/" + randomObjectPart()
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var blobID, documentID int64
	if err := tx.QueryRow(c.Request.Context(), `INSERT INTO platform_knowledge_blob (object_key,media_type,size_bytes,status) VALUES ($1,$2,$3,'uploading') RETURNING id`, key, input.MediaType, input.SizeBytes).Scan(&blobID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.QueryRow(c.Request.Context(), `INSERT INTO platform_knowledge_document (source_kind,blob_id,filename,display_name,allowed_roles,status,enabled,searchable,uploaded_by) VALUES ('custom',$1,$2,$2,'{student,group,class_admin}','uploading',false,false,$3) RETURNING id`, blobID, filename, s.cfg.OpsAccount).Scan(&documentID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	upload, err := s.deps.Objects.PresignUpload(c.Request.Context(), key, input.MediaType, input.SizeBytes, 15*time.Minute)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	_ = s.appendOpsAudit(c.Request.Context(), c, "platform_knowledge.created", "platform_knowledge_document", strconv.FormatInt(documentID, 10), map[string]any{"filename": filename, "sizeBytes": input.SizeBytes, "enabled": false})
	response := uploadPolicyJSON(upload)
	response["documentId"] = strconv.FormatInt(documentID, 10)
	response["limits"] = limits
	c.JSON(http.StatusCreated, response)
}

func (s *Server) completeOpsPlatformKnowledge(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
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
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var blobID int64
	var key, media, status string
	var size int64
	err = tx.QueryRow(c.Request.Context(), `SELECT b.id,b.object_key,b.media_type,b.size_bytes,d.status FROM platform_knowledge_document d JOIN platform_knowledge_blob b ON b.id=d.blob_id WHERE d.id=$1 AND d.source_kind='custom' AND d.deleted_at IS NULL FOR UPDATE`, id).Scan(&blobID, &key, &media, &size, &status)
	if notFound(c, err, "平台知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "uploading" {
		c.JSON(http.StatusOK, gin.H{"documentId": strconv.FormatInt(id, 10), "status": status})
		return
	}
	info, err := s.deps.Objects.Stat(c.Request.Context(), key)
	if err != nil {
		writeError(c, http.StatusConflict, "upload_incomplete", "对象尚未上传完成", nil)
		return
	}
	if info.Size != size {
		s.discardInvalidUpload(c, key)
		writeError(c, http.StatusUnprocessableEntity, "size_mismatch", "实际文件大小与声明不一致", nil)
		return
	}
	if strings.Trim(input.ETag, `"`) != "" && !strings.EqualFold(strings.Trim(input.ETag, `"`), strings.Trim(info.ETag, `"`)) {
		writeError(c, http.StatusUnprocessableEntity, "etag_mismatch", "上传对象的 ETag 不匹配", nil)
		return
	}
	ocrRoute := []byte(`{}`)
	if binding, routeErr := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeKnowledgeOCR); routeErr == nil {
		snapshot := binding.Snapshot()
		snapshot.Legacy = binding.ProviderID == ""
		ocrRoute, _ = json.Marshal(snapshot)
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE platform_knowledge_blob SET status='queued',ocr_route=$2,updated_at=now() WHERE id=$1`, blobID, ocrRoute); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE platform_knowledge_document SET status='queued',updated_at=now() WHERE id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `SELECT enqueue_platform_knowledge_document($1)`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	_ = s.appendOpsAudit(c.Request.Context(), c, "platform_knowledge.uploaded", "platform_knowledge_document", strconv.FormatInt(id, 10), map[string]any{"status": "queued", "mediaType": media})
	c.JSON(http.StatusAccepted, gin.H{"documentId": strconv.FormatInt(id, 10), "status": "queued"})
}

func (s *Server) opsPlatformKnowledgeDocument(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var sourceKind, key, filename, displayName, status, extractor, warning, conversionError, contentVersion, contentHash, mediaType string
	var blobKey string
	var roles, views, keywords []string
	var enabled, searchable bool
	var size int64
	var order, entries int
	var pages, sheets *int
	var created, updated time.Time
	var published *time.Time
	err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT d.source_kind,COALESCE(d.builtin_key,''),d.filename,d.display_name,d.allowed_roles,d.page_tags,d.keywords,d.sort_order,d.status,d.enabled,d.searchable,COALESCE(d.extractor,''),d.entries_count,d.page_count,d.sheet_count,COALESCE(d.warning,''),COALESCE(d.error,''),d.content_version,COALESCE(d.content_hash,''),d.created_at,d.updated_at,d.published_at,COALESCE(b.object_key,''),COALESCE(b.media_type,'text/markdown'),COALESCE(b.size_bytes,0) FROM platform_knowledge_document d LEFT JOIN platform_knowledge_blob b ON b.id=d.blob_id WHERE d.id=$1 AND d.deleted_at IS NULL`, id).Scan(&sourceKind, &key, &filename, &displayName, &roles, &views, &keywords, &order, &status, &enabled, &searchable, &extractor, &entries, &pages, &sheets, &warning, &conversionError, &contentVersion, &contentHash, &created, &updated, &published, &blobKey, &mediaType, &size)
	if notFound(c, err, "平台知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `SELECT id,kind,locator,left(plain_text,$2),line_count,char_count,metadata FROM platform_knowledge_entry WHERE document_id=$1 ORDER BY id LIMIT $3`, id, knowledgeEntryPreviewBytes, platformKnowledgePreviewLimit)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	preview := make([]gin.H, 0)
	for rows.Next() {
		var eid int64
		var kind, text string
		var locator, metadata []byte
		var lines, chars int
		if err := rows.Scan(&eid, &kind, &locator, &text, &lines, &chars, &metadata); err != nil {
			writeServiceError(c, err)
			return
		}
		preview = append(preview, gin.H{"id": strconv.FormatInt(eid, 10), "kind": kind, "locator": json.RawMessage(locator), "metadata": json.RawMessage(metadata), "lineCount": lines, "charCount": chars, "startLine": 1, "endLine": max(1, lines), "text": text})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	response := gin.H{"id": strconv.FormatInt(id, 10), "sourceKind": sourceKind, "builtinKey": key, "filename": filename, "displayName": displayName, "allowedRoles": roles, "views": views, "keywords": keywords, "sortOrder": order, "status": status, "enabled": enabled, "searchable": searchable, "extractor": extractor, "entries": entries, "pageCount": pages, "sheetCount": sheets, "warning": warning, "error": conversionError, "contentVersion": contentVersion, "contentHash": contentHash, "mediaType": mediaType, "sizeBytes": size, "createdAt": created, "updatedAt": updated, "publishedAt": published, "entriesPreview": preview, "downloadUrl": "", "downloadExpiresIn": 0}
	if sourceKind == "custom" && blobKey != "" {
		if u, err := s.deps.Objects.PresignGet(c.Request.Context(), blobKey, 10*time.Minute, filename, false); err == nil {
			response["downloadUrl"] = u.String()
			response["downloadExpiresIn"] = 600
		}
	} else if sourceKind == "builtin" {
		var content string
		if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT plain_text FROM platform_knowledge_entry WHERE document_id=$1 ORDER BY id LIMIT 1`, id).Scan(&content); err == nil {
			response["content"] = content
		}
	}
	_ = s.appendOpsAudit(c.Request.Context(), c, "platform_knowledge.read", "platform_knowledge_document", strconv.FormatInt(id, 10), nil)
	c.JSON(http.StatusOK, response)
}

func (s *Server) updateOpsPlatformKnowledge(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		DisplayName  *string  `json:"displayName"`
		AllowedRoles []string `json:"allowedRoles"`
		Enabled      *bool    `json:"enabled"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "平台知识更新参数不正确", nil)
		return
	}
	validRoles := map[string]bool{"student": true, "group": true, "class_admin": true}
	if input.AllowedRoles != nil {
		if len(input.AllowedRoles) == 0 {
			writeError(c, http.StatusUnprocessableEntity, "platform_knowledge_roles_invalid", "至少选择一个业务角色", nil)
			return
		}
		for _, role := range input.AllowedRoles {
			if !validRoles[role] {
				writeError(c, http.StatusUnprocessableEntity, "platform_knowledge_roles_invalid", "角色范围不正确", nil)
				return
			}
		}
	}
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var sourceKind, status, beforeName string
	var beforeRoles []string
	var beforeEnabled bool
	err = tx.QueryRow(c.Request.Context(), `SELECT source_kind,status,display_name,allowed_roles,enabled FROM platform_knowledge_document WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&sourceKind, &status, &beforeName, &beforeRoles, &beforeEnabled)
	if notFound(c, err, "平台知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	name := beforeName
	if input.DisplayName != nil {
		if sourceKind == "builtin" {
			writeError(c, http.StatusForbidden, "platform_knowledge_builtin_readonly", "内置文档正文和名称不可修改", nil)
			return
		}
		name = strings.TrimSpace(*input.DisplayName)
		if name == "" || len([]rune(name)) > 300 || hasControl(name) {
			writeError(c, http.StatusUnprocessableEntity, "platform_knowledge_name_invalid", "显示名称不正确", nil)
			return
		}
	}
	roles := beforeRoles
	if input.AllowedRoles != nil {
		roles = input.AllowedRoles
	}
	enabled := beforeEnabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if enabled && (status != "ready" && status != "partial") {
		writeError(c, http.StatusConflict, "platform_knowledge_not_ready", "文件转换完成后才能发布", nil)
		return
	}
	if enabled && len(roles) == 0 {
		writeError(c, http.StatusUnprocessableEntity, "platform_knowledge_roles_invalid", "至少选择一个业务角色", nil)
		return
	}
	_, err = tx.Exec(c.Request.Context(), `UPDATE platform_knowledge_document SET display_name=$2,allowed_roles=$3,enabled=$4,published_at=CASE WHEN $4 THEN COALESCE(published_at,now()) ELSE NULL END,updated_at=now() WHERE id=$1`, id, name, roles, enabled)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	_ = s.appendOpsAudit(c.Request.Context(), c, "platform_knowledge.updated", "platform_knowledge_document", strconv.FormatInt(id, 10), map[string]any{"before": gin.H{"displayName": beforeName, "allowedRoles": beforeRoles, "enabled": beforeEnabled}, "after": gin.H{"displayName": name, "allowedRoles": roles, "enabled": enabled}})
	c.Status(http.StatusNoContent)
}

func (s *Server) reprocessOpsPlatformKnowledge(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	ocrRoute := []byte(`{}`)
	if binding, routeErr := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeKnowledgeOCR); routeErr == nil {
		snapshot := binding.Snapshot()
		snapshot.Legacy = binding.ProviderID == ""
		ocrRoute, _ = json.Marshal(snapshot)
	}
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var blobID int64
	var kind, status string
	err = tx.QueryRow(c.Request.Context(), `SELECT source_kind,blob_id,status FROM platform_knowledge_document WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&kind, &blobID, &status)
	if notFound(c, err, "平台知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if kind != "custom" {
		writeError(c, http.StatusForbidden, "platform_knowledge_builtin_readonly", "内置文档不能重处理", nil)
		return
	}
	if status == "uploading" {
		writeError(c, http.StatusConflict, "upload_incomplete", "文件尚未上传完成", nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE platform_knowledge_blob SET status='queued',error=NULL,ocr_route=$2,updated_at=now() WHERE id=$1`, blobID, ocrRoute); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE platform_knowledge_document SET status='queued',enabled=false,published_at=NULL,error=NULL,warning=NULL,updated_at=now() WHERE id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `SELECT enqueue_platform_knowledge_document($1)`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	_ = s.appendOpsAudit(c.Request.Context(), c, "platform_knowledge.reprocessed", "platform_knowledge_document", strconv.FormatInt(id, 10), nil)
	c.JSON(http.StatusAccepted, gin.H{"documentId": strconv.FormatInt(id, 10), "status": "queued"})
}

func (s *Server) deleteOpsPlatformKnowledge(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var kind, status string
	var key *string
	err = tx.QueryRow(c.Request.Context(), `SELECT d.source_kind,d.status,b.object_key FROM platform_knowledge_document d LEFT JOIN platform_knowledge_blob b ON b.id=d.blob_id WHERE d.id=$1 AND d.deleted_at IS NULL FOR UPDATE OF d`, id).Scan(&kind, &status, &key)
	if notFound(c, err, "平台知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if kind != "custom" {
		writeError(c, http.StatusForbidden, "platform_knowledge_builtin_readonly", "内置文档不能删除", nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE platform_knowledge_document SET status='deleted',enabled=false,searchable=false,deleted_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	_ = s.appendOpsAudit(c.Request.Context(), c, "platform_knowledge.deleted", "platform_knowledge_document", strconv.FormatInt(id, 10), map[string]any{"status": status})
	c.Status(http.StatusNoContent)
}
