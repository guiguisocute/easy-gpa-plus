package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
)

type knowledgeLimits struct {
	FileMB         int `json:"fileMb"`
	FilesPerClass  int `json:"filesPerClass"`
	StorageMBClass int `json:"storageMbPerClass"`
	PDFPages       int `json:"pdfPages"`
}

const (
	knowledgeEntryPreviewLimit   = 100
	knowledgeEntryPreviewBytes   = 64 * 1024
	knowledgeEntryReadBytesLimit = 1 * 1024 * 1024
)

func (s *Server) knowledgeLimits(ctx context.Context) knowledgeLimits {
	settings := opsconfig.DefaultAI()
	lifecycle := opsconfig.DefaultLifecycle()
	if s.opsConfig != nil {
		if loaded, err := s.opsConfig.AI(ctx); err == nil {
			settings = loaded
		}
		if loaded, err := s.opsConfig.Lifecycle(ctx); err == nil {
			lifecycle = loaded
		}
	}
	return knowledgeLimits{
		FileMB: min(s.runtimeFlags(ctx).UploadMaxMB, 50), FilesPerClass: settings.KnowledgeMaxFilesPerClass,
		StorageMBClass: settings.KnowledgeMaxStorageMBPerClass, PDFPages: lifecycle.KnowledgeMaxPDFPages,
	}
}

func (s *Server) adminKnowledge(c *gin.Context) {
	tx := mustTx(c)
	actor := mustActor(c)
	var approved bool
	var approvedBy *int64
	var approvedAt, revokedAt *time.Time
	err := tx.QueryRow(c.Request.Context(), `
		SELECT external_processing_approved,approved_by,approved_at,revoked_at
		  FROM knowledge_policy WHERE class_id=$1
	`, actor.ClassID).Scan(&approved, &approvedBy, &approvedAt, &revokedAt)
	if !errors.Is(err, pgx.ErrNoRows) && err != nil {
		writeServiceError(c, err)
		return
	}
	documents, err := loadKnowledgeDocuments(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	evidenceFiles, err := loadKnowledgeEvidenceFiles(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var totalFiles, classFiles, evidenceFileCount, readyFiles, processingFiles int
	var storageBytes int64
	var updatedAt *time.Time
	if err := tx.QueryRow(c.Request.Context(), `
		WITH document_stats AS (
			SELECT count(*)::int AS files,
			       count(*) FILTER (WHERE d.status IN ('ready','partial') AND d.searchable)::int AS ready_files,
			       count(*) FILTER (WHERE d.status IN ('uploading','queued','processing'))::int AS processing_files,
			       COALESCE((SELECT sum(b.size_bytes) FROM knowledge_blob b WHERE b.deleted_at IS NULL AND EXISTS (SELECT 1 FROM knowledge_document x WHERE x.blob_id=b.id AND x.deleted_at IS NULL)),0) AS storage_bytes,
			       max(d.updated_at) AS updated_at
			  FROM knowledge_document d WHERE d.deleted_at IS NULL
		), evidence_stats AS (
			SELECT count(*) FILTER (WHERE status IN ('pending','ready'))::int AS files,
			       COALESCE(sum(size_bytes) FILTER (WHERE status='ready'),0) AS storage_bytes,
			       max(created_at) FILTER (WHERE status IN ('pending','ready')) AS updated_at
			  FROM evidence
		)
		SELECT document_stats.files+evidence_stats.files,
		       document_stats.files,evidence_stats.files,
		       document_stats.ready_files,document_stats.processing_files,
		       document_stats.storage_bytes+evidence_stats.storage_bytes,
		       GREATEST(document_stats.updated_at,evidence_stats.updated_at)
		  FROM document_stats,evidence_stats
	`).Scan(&totalFiles, &classFiles, &evidenceFileCount, &readyFiles, &processingFiles, &storageBytes, &updatedAt); err != nil {
		writeServiceError(c, err)
		return
	}
	flags := s.runtimeFlags(c.Request.Context())
	if err := appendAudit(c, tx, "knowledge.list", "class_file_inventory", "", nil, nil, map[string]any{"documentCount": len(documents), "evidenceCount": len(evidenceFiles), "totalCount": totalFiles}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"policy":    gin.H{"externalProcessingApproved": approved, "approvedBy": stringifyID(approvedBy), "approvedAt": approvedAt, "revokedAt": revokedAt},
		"limits":    s.knowledgeLimits(c.Request.Context()),
		"stats":     gin.H{"totalFiles": totalFiles, "classFiles": classFiles, "evidenceFiles": evidenceFileCount, "readyFiles": readyFiles, "processingFiles": processingFiles, "storageBytes": storageBytes, "updatedAt": updatedAt},
		"documents": documents,
		"evidence":  evidenceFiles,
		"platform":  gin.H{"knowledgeEnabled": flags.KnowledgeEnabled, "knowledgeEgressEnabled": flags.KnowledgeEgressEnabled, "aiEnabled": flags.AIEnabled},
	})
}

func (s *Server) updateKnowledgePolicy(c *gin.Context) {
	var input struct {
		Approved *bool `json:"externalProcessingApproved"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Approved == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "知识库授权参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var before bool
	_ = tx.QueryRow(c.Request.Context(), `SELECT external_processing_approved FROM knowledge_policy WHERE class_id=$1`, actor.ClassID).Scan(&before)
	if *input.Approved {
		_, err := tx.Exec(c.Request.Context(), `
			INSERT INTO knowledge_policy (class_id,external_processing_approved,approved_by,approved_at,revoked_at)
			VALUES ($1,true,$2,now(),NULL)
			ON CONFLICT (class_id) DO UPDATE
			SET external_processing_approved=true,approved_by=$2,approved_at=now(),revoked_at=NULL,updated_at=now()
		`, actor.ClassID, actor.UserID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	} else {
		_, err := tx.Exec(c.Request.Context(), `
			INSERT INTO knowledge_policy (class_id,external_processing_approved,revoked_at)
			VALUES ($1,false,now())
			ON CONFLICT (class_id) DO UPDATE
			SET external_processing_approved=false,revoked_at=now(),updated_at=now()
		`, actor.ClassID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "knowledge.policy_updated", "knowledge_policy", strconv.FormatInt(actor.ClassID, 10), map[string]any{"approved": before}, map[string]any{"approved": *input.Approved}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) presignKnowledgeDocument(c *gin.Context) {
	var input struct {
		Filename    string `json:"filename"`
		LogicalPath string `json:"logicalPath"`
		MediaType   string `json:"mediaType"`
		SizeBytes   int64  `json:"sizeBytes"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "文件参数不正确", nil)
		return
	}
	filename, logicalPath, err := validateKnowledgePath(input.Filename, input.LogicalPath)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "knowledge_path_invalid", err.Error(), nil)
		return
	}
	input.MediaType = strings.TrimSpace(input.MediaType)
	if input.MediaType == "" || len(input.MediaType) > 200 {
		input.MediaType = "application/octet-stream"
	}
	limits := s.knowledgeLimits(c.Request.Context())
	if input.SizeBytes <= 0 || input.SizeBytes > int64(limits.FileMB)*1024*1024 {
		writeError(c, http.StatusRequestEntityTooLarge, "knowledge_file_too_large", fmt.Sprintf("单文件不能超过 %d MB", limits.FileMB), nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if err := lockClassQuota(c.Request.Context(), tx, actor.ClassID); err != nil {
		writeServiceError(c, err)
		return
	}
	var count int
	var storage int64
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT (SELECT count(*) FROM knowledge_document WHERE deleted_at IS NULL),
		       COALESCE((SELECT sum(b.size_bytes) FROM knowledge_blob b WHERE b.deleted_at IS NULL AND EXISTS (SELECT 1 FROM knowledge_document d WHERE d.blob_id=b.id AND d.deleted_at IS NULL)),0)
	`).Scan(&count, &storage); err != nil {
		writeServiceError(c, err)
		return
	}
	if count >= limits.FilesPerClass {
		writeError(c, http.StatusUnprocessableEntity, "knowledge_file_limit", "本班知识库文件数已达上限", nil)
		return
	}
	if storage+input.SizeBytes > int64(limits.StorageMBClass)*1024*1024 {
		writeError(c, http.StatusUnprocessableEntity, "knowledge_storage_limit", "本班知识库存储量已达上限", nil)
		return
	}
	objectKey := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/knowledge/raw/" + randomObjectPart()
	var blobID, documentID int64
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO knowledge_blob (class_id,object_key,media_type,size_bytes,status)
		VALUES ($1,$2,$3,$4,'uploading') RETURNING id
	`, actor.ClassID, objectKey, input.MediaType, input.SizeBytes).Scan(&blobID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO knowledge_document
		    (class_id,blob_id,filename,logical_path,display_name,visibility,status,uploaded_by,published_at)
		VALUES ($1,$2,$3,$4,$3,'class','uploading',$5,now()) RETURNING id
	`, actor.ClassID, blobID, filename, logicalPath, actor.UserID).Scan(&documentID); err != nil {
		writeServiceError(c, err)
		return
	}
	upload, err := s.deps.Objects.PresignUpload(c.Request.Context(), objectKey, input.MediaType, input.SizeBytes, 15*time.Minute)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "knowledge.document_created", "knowledge_document", strconv.FormatInt(documentID, 10), nil, map[string]any{"status": "uploading", "sizeBytes": input.SizeBytes}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	response := uploadPolicyJSON(upload)
	response["documentId"] = strconv.FormatInt(documentID, 10)
	response["limits"] = limits
	c.JSON(http.StatusCreated, response)
}

func (s *Server) completeKnowledgeDocument(c *gin.Context) {
	documentID, ok := pathID(c, "id")
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
	var blobID, size int64
	var objectKey, mediaType, status string
	err := tx.QueryRow(c.Request.Context(), `
		SELECT b.id,b.object_key,b.media_type,b.size_bytes,d.status
		  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id
		 WHERE d.id=$1 AND d.deleted_at IS NULL FOR UPDATE OF d,b
	`, documentID).Scan(&blobID, &objectKey, &mediaType, &size, &status)
	if notFound(c, err, "知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "uploading" {
		c.JSON(http.StatusOK, gin.H{"documentId": strconv.FormatInt(documentID, 10), "status": status})
		return
	}
	info, err := s.deps.Objects.Stat(c.Request.Context(), objectKey)
	if err != nil {
		writeError(c, http.StatusConflict, "upload_incomplete", "对象尚未上传完成", nil)
		return
	}
	if info.Size != size {
		s.discardInvalidUpload(c, objectKey)
		writeError(c, http.StatusUnprocessableEntity, "size_mismatch", "实际文件大小与声明不一致", gin.H{"declared": size, "actual": info.Size})
		return
	}
	if strings.Trim(input.ETag, `"`) != "" && !strings.EqualFold(strings.Trim(input.ETag, `"`), strings.Trim(info.ETag, `"`)) {
		writeError(c, http.StatusUnprocessableEntity, "etag_mismatch", "上传对象的 ETag 不匹配", nil)
		return
	}
	plan := planKnowledgeCompletion(s.runtimeFlags(c.Request.Context()).KnowledgeEnabled)
	ocrRoute := []byte(`{}`)
	if plan.Process {
		if binding, routeErr := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeKnowledgeOCR); routeErr == nil {
			snapshot := binding.Snapshot()
			snapshot.Legacy = binding.ProviderID == ""
			ocrRoute, _ = json.Marshal(snapshot)
		}
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE knowledge_blob SET status=$2,ocr_route=$3,error=NULL,updated_at=now() WHERE id=$1`, blobID, plan.Status, ocrRoute); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE knowledge_document
		   SET status=$2,searchable=false,
		       extractor=CASE WHEN $2='unsupported' THEN 'original-only' ELSE NULL END,
		       warning=CASE WHEN $2='unsupported' THEN '内容处理未启用，原件仍可发布和下载' ELSE NULL END,
		       error=NULL,updated_at=now()
		 WHERE id=$1
	`, documentID, plan.Status); err != nil {
		writeServiceError(c, err)
		return
	}
	if plan.Process {
		if err := enqueueTypedEvent(c.Request.Context(), tx, mustActor(c).ClassID, events.KnowledgeDocumentCreatedEvent(),
			events.DocumentCreatedPayload{DocumentID: strconv.FormatInt(documentID, 10)}); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "knowledge.document_uploaded", "knowledge_document", strconv.FormatInt(documentID, 10), map[string]any{"status": "uploading"}, map[string]any{"status": plan.Status}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	s.scheduleStorageReconcile(c, mustActor(c).ClassID)
	c.JSON(http.StatusOK, gin.H{"documentId": strconv.FormatInt(documentID, 10), "status": plan.Status})
}

type knowledgeCompletionPlan struct {
	Status  string
	Process bool
}

func planKnowledgeCompletion(processingEnabled bool) knowledgeCompletionPlan {
	if processingEnabled {
		return knowledgeCompletionPlan{Status: "queued", Process: true}
	}
	return knowledgeCompletionPlan{Status: "unsupported", Process: false}
}

func (s *Server) knowledgeDocument(c *gin.Context) {
	documentID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	document, objectKey, err := loadKnowledgeDocument(c.Request.Context(), tx, documentID)
	if notFound(c, err, "知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	entries, err := loadKnowledgeEntries(c.Request.Context(), tx, documentID, 1, 200)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	downloadURL, err := s.deps.Objects.PresignGet(c.Request.Context(), objectKey, 10*time.Minute, document["filename"].(string), false)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	document["entriesPreview"] = entries
	document["downloadUrl"] = downloadURL.String()
	document["downloadExpiresIn"] = 600
	if err := appendAudit(c, tx, "knowledge.document_read", "knowledge_document", strconv.FormatInt(documentID, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, document)
}

func (s *Server) updateKnowledgeDocument(c *gin.Context) {
	documentID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input map[string]any
	if c.ShouldBindJSON(&input) != nil || len(input) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "文件更新参数不正确", nil)
		return
	}
	for key := range input {
		if key != "displayName" {
			writeError(c, http.StatusUnprocessableEntity, "knowledge_document_invalid", "包含不允许修改的字段", gin.H{"field": key})
			return
		}
	}
	tx := mustTx(c)
	var beforeName string
	if err := tx.QueryRow(c.Request.Context(), `SELECT display_name FROM knowledge_document WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, documentID).Scan(&beforeName); err != nil {
		if notFound(c, err, "知识文件") {
			return
		}
		writeServiceError(c, err)
		return
	}
	name := beforeName
	if value, exists := input["displayName"]; exists {
		text, valid := value.(string)
		text = strings.TrimSpace(text)
		if !valid || text == "" || len([]rune(text)) > 300 || hasControl(text) {
			writeError(c, http.StatusUnprocessableEntity, "knowledge_name_invalid", "显示名称不正确", nil)
			return
		}
		name = text
	}
	_, err := tx.Exec(c.Request.Context(), `
		UPDATE knowledge_document SET display_name=$2,visibility='class',
		       published_at=COALESCE(published_at,now()),updated_at=now()
		 WHERE id=$1
	`, documentID, name)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "knowledge.document_updated", "knowledge_document", strconv.FormatInt(documentID, 10), map[string]any{"displayName": beforeName}, map[string]any{"displayName": name}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func loadKnowledgeEvidenceFiles(ctx context.Context, tx pgx.Tx) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.id,e.filename,e.media_type,e.size_bytes,e.status,e.kind,
		       CASE WHEN e.submission_id IS NOT NULL THEN 'submission'
		            WHEN e.appeal_id IS NOT NULL THEN 'appeal' ELSE 'objection' END,
		       COALESCE(e.submission_id,e.appeal_id,e.objection_id),
		       COALESCE(s.title,''),subject.sid,subject.name,uploader.sid,uploader.name,e.created_at
		  FROM evidence e
		  LEFT JOIN submission s ON s.id=e.submission_id
		  LEFT JOIN appeal a ON a.id=e.appeal_id
		  LEFT JOIN objection o ON o.id=e.objection_id
		  JOIN app_user subject ON subject.id=COALESCE(s.student_id,a.student_id,o.student_id)
		  JOIN app_user uploader ON uploader.id=e.created_by
		 WHERE e.status IN ('pending','ready')
		 ORDER BY e.created_at DESC,e.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, sizeBytes, ownerID int64
		var filename, mediaType, status, kind, source, title, subjectSID, subjectName, uploaderSID, uploaderName string
		var createdAt time.Time
		if err := rows.Scan(&id, &filename, &mediaType, &sizeBytes, &status, &kind, &source, &ownerID, &title, &subjectSID, &subjectName, &uploaderSID, &uploaderName, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "filename": filename, "mediaType": mediaType,
			"sizeBytes": sizeBytes, "status": status, "kind": kind, "source": source,
			"ownerId": strconv.FormatInt(ownerID, 10), "title": title,
			"subjectSid": subjectSID, "subjectName": subjectName,
			"uploaderSid": uploaderSID, "uploaderName": uploaderName, "createdAt": createdAt,
		})
	}
	return items, rows.Err()
}

func (s *Server) reprocessKnowledgeDocument(c *gin.Context) {
	if !s.runtimeFlags(c.Request.Context()).KnowledgeEnabled {
		writeError(c, http.StatusConflict, "knowledge_processing_disabled", "内容处理当前未启用，原件仍可发布和下载", nil)
		return
	}
	documentID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var blobID int64
	var status string
	if err := tx.QueryRow(c.Request.Context(), `SELECT blob_id,status FROM knowledge_document WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, documentID).Scan(&blobID, &status); err != nil {
		if notFound(c, err, "知识文件") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if status == "uploading" {
		writeError(c, http.StatusConflict, "knowledge_upload_incomplete", "文件尚未上传完成", nil)
		return
	}
	ocrRoute := []byte(`{}`)
	if binding, routeErr := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeKnowledgeOCR); routeErr == nil {
		snapshot := binding.Snapshot()
		snapshot.Legacy = binding.ProviderID == ""
		ocrRoute, _ = json.Marshal(snapshot)
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE knowledge_blob SET status='queued',error=NULL,ocr_route=$2,updated_at=now() WHERE id=$1`, blobID, ocrRoute); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE knowledge_document SET status='queued',error=NULL,warning=NULL,updated_at=now() WHERE blob_id=$1 AND deleted_at IS NULL`, blobID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, mustActor(c).ClassID, events.KnowledgeDocumentCreatedEvent(),
		events.DocumentCreatedPayload{DocumentID: strconv.FormatInt(documentID, 10)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"documentId": strconv.FormatInt(documentID, 10), "status": "queued"})
}

func (s *Server) deleteKnowledgeDocument(c *gin.Context) {
	documentID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var status string
	err := tx.QueryRow(c.Request.Context(), `
		UPDATE knowledge_document SET status='deleted',deleted_at=now(),updated_at=now()
		 WHERE id=$1 AND deleted_at IS NULL RETURNING status
	`, documentID).Scan(&status)
	if notFound(c, err, "知识文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "knowledge.document_deleted", "knowledge_document", strconv.FormatInt(documentID, 10), nil, map[string]any{"status": "deleted"}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) adminKnowledgeEntry(c *gin.Context) {
	entryID, ok := pathID(c, "id")
	if !ok {
		return
	}
	start, end, valid := lineRange(c)
	if !valid {
		return
	}
	tx := mustTx(c)
	var documentID int64
	var filename, logicalPath, text string
	var locator, metadata []byte
	err := tx.QueryRow(c.Request.Context(), `
		SELECT d.id,d.display_name,d.logical_path,left(e.plain_text,$2),e.locator,e.metadata
		  FROM knowledge_entry e JOIN knowledge_document d ON d.blob_id=e.blob_id
		 WHERE e.id=$1 AND d.deleted_at IS NULL ORDER BY d.id LIMIT 1
	`, entryID, knowledgeEntryReadBytesLimit).Scan(&documentID, &filename, &logicalPath, &text, &locator, &metadata)
	if notFound(c, err, "知识条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	selected, actualStart, actualEnd := selectLines(text, start, end)
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(entryID, 10), "documentId": strconv.FormatInt(documentID, 10), "filename": filename, "logicalPath": logicalPath, "locator": json.RawMessage(locator), "metadata": json.RawMessage(metadata), "startLine": actualStart, "endLine": actualEnd, "text": selected})
}

func loadKnowledgeDocuments(ctx context.Context, tx pgx.Tx) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT d.id,d.filename,d.logical_path,d.display_name,b.media_type,b.size_bytes,d.visibility,d.status,d.searchable,
		       d.extractor,d.entries_count,d.page_count,d.sheet_count,d.warning,d.error,d.created_at,d.updated_at,d.published_at
		  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id
		 WHERE d.deleted_at IS NULL ORDER BY d.updated_at DESC,d.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, size int64
		var filename, logicalPath, displayName, mediaType, visibility, status string
		var searchable bool
		var extractor, warning, errorMessage *string
		var entryCount int
		var pageCount, sheetCount *int
		var created, updated time.Time
		var published *time.Time
		if err := rows.Scan(&id, &filename, &logicalPath, &displayName, &mediaType, &size, &visibility, &status, &searchable, &extractor, &entryCount, &pageCount, &sheetCount, &warning, &errorMessage, &created, &updated, &published); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "filename": filename, "logicalPath": logicalPath, "displayName": displayName, "mediaType": mediaType, "sizeBytes": size, "visibility": visibility, "status": status, "searchable": searchable, "extractor": extractor, "entries": entryCount, "pageCount": pageCount, "sheetCount": sheetCount, "warning": warning, "error": errorMessage, "createdAt": created, "updatedAt": updated, "publishedAt": published})
	}
	return items, rows.Err()
}

func loadKnowledgeDocument(ctx context.Context, tx pgx.Tx, id int64) (gin.H, string, error) {
	var objectKey string
	var filename, logicalPath, displayName, mediaType, visibility, status string
	var size int64
	var searchable bool
	var extractor, warning, errorMessage *string
	var entryCount int
	var pageCount, sheetCount *int
	var created, updated time.Time
	var published *time.Time
	err := tx.QueryRow(ctx, `
		SELECT b.object_key,d.filename,d.logical_path,d.display_name,b.media_type,b.size_bytes,d.visibility,d.status,d.searchable,
		       d.extractor,d.entries_count,d.page_count,d.sheet_count,d.warning,d.error,d.created_at,d.updated_at,d.published_at
		  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id WHERE d.id=$1 AND d.deleted_at IS NULL
	`, id).Scan(&objectKey, &filename, &logicalPath, &displayName, &mediaType, &size, &visibility, &status, &searchable, &extractor, &entryCount, &pageCount, &sheetCount, &warning, &errorMessage, &created, &updated, &published)
	return gin.H{"id": strconv.FormatInt(id, 10), "filename": filename, "logicalPath": logicalPath, "displayName": displayName, "mediaType": mediaType, "sizeBytes": size, "visibility": visibility, "status": status, "searchable": searchable, "extractor": extractor, "entries": entryCount, "pageCount": pageCount, "sheetCount": sheetCount, "warning": warning, "error": errorMessage, "createdAt": created, "updatedAt": updated, "publishedAt": published}, objectKey, err
}

func loadKnowledgeEntries(ctx context.Context, tx pgx.Tx, documentID int64, start, end int) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.id,e.kind,e.locator,left(e.plain_text,$2),e.line_count,e.char_count,e.metadata
		  FROM knowledge_document d JOIN knowledge_entry e ON e.blob_id=d.blob_id
		 WHERE d.id=$1 AND d.deleted_at IS NULL ORDER BY e.id LIMIT $3
	`, documentID, knowledgeEntryPreviewBytes, knowledgeEntryPreviewLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var kind, text string
		var locator, metadata []byte
		var lines, chars int
		if err := rows.Scan(&id, &kind, &locator, &text, &lines, &chars, &metadata); err != nil {
			return nil, err
		}
		preview, actualStart, actualEnd := selectLines(text, start, end)
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "kind": kind, "locator": json.RawMessage(locator), "metadata": json.RawMessage(metadata), "lineCount": lines, "charCount": chars, "startLine": actualStart, "endLine": actualEnd, "text": preview})
	}
	return items, rows.Err()
}

func validateKnowledgePath(filename, logicalPath string) (string, string, error) {
	filename = strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/"))
	filename = path.Base(filename)
	if filename == "" || filename == "." || filename == ".." || len([]rune(filename)) > 255 || hasControl(filename) {
		return "", "", errors.New("文件名不正确")
	}
	logicalPath = strings.TrimSpace(strings.ReplaceAll(logicalPath, "\\", "/"))
	if logicalPath == "" {
		logicalPath = filename
	}
	if strings.HasPrefix(logicalPath, "/") || strings.Contains(logicalPath, ":") || len([]rune(logicalPath)) > 1024 || hasControl(logicalPath) {
		return "", "", errors.New("相对路径不正确")
	}
	clean := path.Clean(logicalPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", errors.New("相对路径不能越过上传根目录")
	}
	return filename, clean, nil
}

func hasControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func lineRange(c *gin.Context) (int, int, bool) {
	start, _ := strconv.Atoi(c.DefaultQuery("startLine", "1"))
	end, _ := strconv.Atoi(c.DefaultQuery("endLine", "200"))
	if start < 1 || end < start || end-start > 499 {
		writeError(c, http.StatusUnprocessableEntity, "line_range_invalid", "单次最多读取连续 500 行", nil)
		return 0, 0, false
	}
	return start, end, true
}

func selectLines(text string, start, end int) (string, int, int) {
	lines := strings.Split(text, "\n")
	if text == "" {
		return "", 0, 0
	}
	startIndex := min(max(start-1, 0), len(lines))
	endIndex := min(max(end, 0), len(lines))
	if startIndex >= endIndex {
		return "", startIndex + 1, endIndex
	}
	return strings.Join(lines[startIndex:endIndex], "\n"), startIndex + 1, endIndex
}

func stringifyID(value *int64) any {
	if value == nil {
		return nil
	}
	return strconv.FormatInt(*value, 10)
}
