package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/aiassist"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
)

const aiUploadMaxMB int64 = 50

type aiBatchLimits struct {
	Items          int      `json:"items"`
	DailyBatches   int      `json:"dailyBatches"`
	ActiveBatches  int      `json:"activeBatches"`
	FileMB         int64    `json:"fileMb"`
	BatchMB        int      `json:"batchMb"`
	PDFPages       int      `json:"pdfPages"`
	AllowedFormats []string `json:"allowedFormats"`
}

func (s *Server) aiLimits(ctx context.Context) aiBatchLimits {
	settings := s.aiMaterialSettings(ctx)
	return aiBatchLimits{
		Items:          settings.MaterialMaxItems,
		DailyBatches:   settings.MaterialDailyBatches,
		ActiveBatches:  settings.MaterialActiveBatches,
		FileMB:         minInt64(int64(settings.MaterialMaxFileMB), int64(s.runtimeFlags(ctx).UploadMaxMB)),
		BatchMB:        settings.MaterialMaxBatchMB,
		PDFPages:       settings.MaterialMaxPDFPages,
		AllowedFormats: append([]string(nil), settings.MaterialAllowedFormats...),
	}
}

func (s *Server) aiMaterialSettings(ctx context.Context) opsconfig.AI {
	settings := s.aiFallback()
	if s.opsConfig == nil {
		return settings
	}
	stored, err := s.opsConfig.AI(ctx)
	if err != nil {
		return settings
	}
	return stored
}

type aiBatchRecord struct {
	ID              int64
	SchemeID        int64
	SchemeVersion   int
	SchemeSnapshot  []byte
	Status          string
	TotalCount      int
	ProcessedCount  int
	ComposePreview  string
	ComposeThinking string
	Result          []byte
	Usage           []byte
	VisionModel     string
	TextModel       string
	PromptVersion   string
	Error           *string
	StartedAt       *time.Time
	CompletedAt     *time.Time
	ExpiresAt       time.Time
	CreatedAt       time.Time
}

type aiAssetRecord struct {
	ID                  int64
	ObjectKey           string
	Filename            string
	MediaType           string
	SizeBytes           int64
	SHA256              *string
	ObjectETag          *string
	Status              string
	PageCount           *int
	AppliedSubmissionID *int64
	Error               *string
}

func (s *Server) requireAI() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.aiSwitchEnabled(c.Request.Context()) {
			writeError(c, http.StatusNotImplemented, "ai_disabled", "AI 材料整理当前未启用", nil)
			return
		}
		if err := s.runtimeModelBindingsReady(c.Request.Context(), opsconfig.PurposeMaterialVision, opsconfig.PurposeMaterialCompose); err != nil {
			writeError(c, http.StatusServiceUnavailable, "ai_not_configured", "AI 服务配置尚未就绪，请联系运维管理员", nil)
			return
		}
		c.Next()
	}
}

func (s *Server) aiStatus(c *gin.Context) {
	enabled := s.aiSwitchEnabled(c.Request.Context())
	configured := s.runtimeModelBindingsReady(c.Request.Context(), opsconfig.PurposeMaterialVision, opsconfig.PurposeMaterialCompose) == nil
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"enabled": enabled && configured, "configured": configured, "limits": s.aiLimits(c.Request.Context()),
	})
}

func (s *Server) createAIBatch(c *gin.Context) {
	tx := mustTx(c)
	actor := mustActor(c)
	settings := s.aiMaterialSettings(c.Request.Context())
	if err := lockUserQuota(c.Request.Context(), tx, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	var activeBatches int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM ai_batch
		 WHERE student_id=$1 AND status IN ('uploading','queued','processing','review')
	`, actor.UserID).Scan(&activeBatches); err != nil {
		writeServiceError(c, err)
		return
	}
	if activeBatches >= settings.MaterialActiveBatches {
		writeError(c, http.StatusTooManyRequests, "ai_batch_quota", "请先完成或删除已有材料批次，再创建新的批次", gin.H{"activeLimit": settings.MaterialActiveBatches})
		return
	}
	visionBinding, err := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeMaterialVision)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "ai_not_configured", "AI 服务配置尚未就绪，请联系运维管理员", nil)
		return
	}
	composeBinding, err := s.runtimeModelBinding(c.Request.Context(), opsconfig.PurposeMaterialCompose)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "ai_not_configured", "AI 服务配置尚未就绪，请联系运维管理员", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "submit", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "submission_closed", err.Error(), nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	visionSnapshot := visionBinding.Snapshot()
	visionSnapshot.Legacy = visionBinding.ProviderID == ""
	composeSnapshot := composeBinding.Snapshot()
	composeSnapshot.Legacy = composeBinding.ProviderID == ""
	visionRoute, _ := json.Marshal(visionSnapshot)
	composeRoute, _ := json.Marshal(composeSnapshot)
	var id int64
	expiresAt := time.Now().Add(time.Duration(settings.MaterialRetentionDays) * 24 * time.Hour)
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO ai_batch
		    (class_id,student_id,scheme_id,scheme_version,scheme_snapshot,vision_model,text_model,vision_route,text_route,prompt_version,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id,expires_at
	`, actor.ClassID, actor.UserID, current.ID, current.Version, current.Raw,
		visionBinding.Model, composeBinding.Model, visionRoute, composeRoute, aiassist.PromptVersion, expiresAt).Scan(&id, &expiresAt)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.batch_created", "ai_batch", strconv.FormatInt(id, 10), nil,
		map[string]any{"status": "uploading", "schemeVersion": current.Version}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id": strconv.FormatInt(id, 10), "status": "uploading", "expiresAt": expiresAt,
		"limits": s.aiLimits(c.Request.Context()),
	})
}

func (s *Server) presignAIAsset(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input evidenceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "文件参数不正确", nil)
		return
	}
	input.Filename = safeFilename(input.Filename)
	input.MediaType = strings.ToLower(strings.TrimSpace(strings.Split(input.MediaType, ";")[0]))
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	limits := s.aiLimits(c.Request.Context())
	if err := validateAIAsset(input, limits.FileMB, limits.AllowedFormats); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "asset_invalid", err.Error(), nil)
		return
	}

	tx := mustTx(c)
	actor := mustActor(c)
	var status string
	var expiresAt time.Time
	err := tx.QueryRow(c.Request.Context(), `
		SELECT status,expires_at FROM ai_batch
		 WHERE id=$1 AND student_id=$2 FOR UPDATE
	`, batchID, actor.UserID).Scan(&status, &expiresAt)
	if notFound(c, err, "AI 批次") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "uploading" || !time.Now().Before(expiresAt) {
		writeError(c, http.StatusConflict, "batch_not_uploading", "该批次已不能继续上传", nil)
		return
	}
	var count int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM ai_asset WHERE batch_id=$1 AND status<>'rejected'`, batchID).Scan(&count); err != nil {
		writeServiceError(c, err)
		return
	}
	if count >= limits.Items {
		writeError(c, http.StatusUnprocessableEntity, "batch_limit", fmt.Sprintf("每批最多处理 %d 张图片或 PDF 页", limits.Items), nil)
		return
	}
	var usedBytes int64
	if err := tx.QueryRow(c.Request.Context(), `SELECT COALESCE(sum(size_bytes),0) FROM ai_asset WHERE batch_id=$1 AND status<>'rejected'`, batchID).Scan(&usedBytes); err != nil {
		writeServiceError(c, err)
		return
	}
	batchLimit := int64(limits.BatchMB) * 1024 * 1024
	if input.SizeBytes > batchLimit || usedBytes > batchLimit-input.SizeBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "batch_storage_limit", fmt.Sprintf("每批材料总量不能超过 %d MB", limits.BatchMB), gin.H{"limitMb": limits.BatchMB})
		return
	}
	objectKey := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/ai-batch-" + strconv.FormatInt(batchID, 10) + "/" + randomObjectPart() + objectKeySuffix(input.Filename)
	upload, err := s.deps.Objects.PresignUpload(c.Request.Context(), objectKey, input.MediaType, input.SizeBytes, 15*time.Minute)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var assetID int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO ai_asset (class_id,batch_id,object_key,filename,media_type,size_bytes,sha256)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')) RETURNING id
	`, actor.ClassID, batchID, objectKey, input.Filename, input.MediaType, input.SizeBytes, input.SHA256).Scan(&assetID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.asset_presigned", "ai_asset", strconv.FormatInt(assetID, 10), nil,
		map[string]any{"batchId": strconv.FormatInt(batchID, 10), "sizeBytes": input.SizeBytes, "mediaType": input.MediaType}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	response := uploadPolicyJSON(upload)
	response["assetId"] = strconv.FormatInt(assetID, 10)
	response["completeUrl"] = "/api/v1/ai/batches/" + strconv.FormatInt(batchID, 10) + "/assets/" + strconv.FormatInt(assetID, 10) + "/complete"
	c.JSON(http.StatusCreated, response)
}

func validateAIAsset(input evidenceInput, maxMB int64, allowedFormats []string) error {
	if input.Filename == "" || input.SizeBytes <= 0 {
		return errors.New("文件名和大小不正确")
	}
	if maxMB <= 0 || maxMB > aiUploadMaxMB {
		maxMB = aiUploadMaxMB
	}
	if input.SizeBytes > maxMB*1024*1024 {
		return fmt.Errorf("单个文件不能超过 %d MB", maxMB)
	}
	ext := strings.ToLower(path.Ext(input.Filename))
	format := map[string]string{
		".jpg": "jpeg", ".jpeg": "jpeg", ".png": "png", ".webp": "webp", ".pdf": "pdf",
	}[ext]
	wanted := map[string]string{
		"jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp", "pdf": "application/pdf",
	}[format]
	if wanted == "" {
		return fmt.Errorf("当前只允许上传 %s", materialFormatSummary(allowedFormats))
	}
	allowed := false
	for _, value := range allowedFormats {
		if value == format {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("当前未允许上传 %s 格式；允许的格式为 %s", strings.ToUpper(format), materialFormatSummary(allowedFormats))
	}
	if input.MediaType != wanted {
		return errors.New("文件扩展名与 mediaType 不一致")
	}
	if input.SHA256 != "" {
		decoded, err := hex.DecodeString(input.SHA256)
		if err != nil || len(decoded) != 32 {
			return errors.New("sha256 必须是 64 位十六进制字符串")
		}
	}
	return nil
}

func materialFormatSummary(values []string) string {
	labels := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case "jpeg":
			labels = append(labels, "JPEG")
		case "png":
			labels = append(labels, "PNG")
		case "webp":
			labels = append(labels, "WebP")
		case "pdf":
			labels = append(labels, "PDF")
		}
	}
	if len(labels) == 0 {
		return "已由运维启用的格式"
	}
	return strings.Join(labels, "、")
}

func (s *Server) completeAIAsset(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	assetID, ok := pathID(c, "asset")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var asset aiAssetRecord
	var batchStatus string
	err := tx.QueryRow(c.Request.Context(), `
		SELECT a.object_key,a.filename,a.media_type,a.size_bytes,a.status,b.status
		  FROM ai_asset a JOIN ai_batch b ON b.id=a.batch_id
		 WHERE a.id=$1 AND a.batch_id=$2 AND b.student_id=$3
		 FOR UPDATE OF a,b
	`, assetID, batchID, actor.UserID).Scan(&asset.ObjectKey, &asset.Filename, &asset.MediaType, &asset.SizeBytes, &asset.Status, &batchStatus)
	if notFound(c, err, "AI 材料") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if asset.Status == "ready" {
		c.JSON(http.StatusOK, gin.H{"assetId": strconv.FormatInt(assetID, 10), "status": "ready"})
		return
	}
	if batchStatus != "uploading" || asset.Status != "pending" {
		writeError(c, http.StatusConflict, "asset_not_completable", "该材料已不能完成上传", nil)
		return
	}
	info, err := s.deps.Objects.Stat(c.Request.Context(), asset.ObjectKey)
	if err != nil {
		writeError(c, http.StatusConflict, "upload_incomplete", "对象尚未上传完成", nil)
		return
	}
	if info.Size != asset.SizeBytes {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE ai_asset SET status='rejected',error='size mismatch',updated_at=now() WHERE id=$1`, assetID)
		s.discardInvalidUpload(c, asset.ObjectKey)
		writeError(c, http.StatusUnprocessableEntity, "size_mismatch", "实际文件大小与声明不一致", gin.H{"declared": asset.SizeBytes, "actual": info.Size})
		return
	}
	storedType := strings.ToLower(strings.TrimSpace(strings.Split(info.ContentType, ";")[0]))
	if storedType != "" && storedType != "application/octet-stream" && storedType != asset.MediaType {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE ai_asset SET status='rejected',error='media type mismatch',updated_at=now() WHERE id=$1`, assetID)
		s.discardInvalidUpload(c, asset.ObjectKey)
		writeError(c, http.StatusUnprocessableEntity, "media_type_mismatch", "对象存储中的文件类型与声明不一致", nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE ai_asset SET status='ready',object_etag=NULLIF($2,''),error=NULL,updated_at=now() WHERE id=$1
	`, assetID, strings.Trim(info.ETag, `"`)); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.asset_completed", "ai_asset", strconv.FormatInt(assetID, 10),
		map[string]any{"status": asset.Status}, map[string]any{"status": "ready"}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	s.scheduleStorageReconcile(c, actor.ClassID)
	c.JSON(http.StatusOK, gin.H{"assetId": strconv.FormatInt(assetID, 10), "status": "ready", "filename": asset.Filename})
}

func (s *Server) deleteAIAsset(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	assetID, ok := pathID(c, "asset")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey, filename, assetStatus, batchStatus string
	err := tx.QueryRow(c.Request.Context(), `
		SELECT asset.object_key,asset.filename,asset.status,batch.status
		  FROM ai_asset AS asset JOIN ai_batch AS batch ON batch.id=asset.batch_id
		 WHERE asset.id=$1 AND asset.batch_id=$2 AND batch.student_id=$3
		 FOR UPDATE OF asset,batch
	`, assetID, batchID, actor.UserID).Scan(&objectKey, &filename, &assetStatus, &batchStatus)
	if notFound(c, err, "AI 材料") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if batchStatus != "uploading" || (assetStatus != "pending" && assetStatus != "ready" && assetStatus != "rejected") {
		writeError(c, http.StatusConflict, "asset_not_deletable", "该材料已进入处理流程，不能单独删除", nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM ai_asset WHERE id=$1`, assetID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.asset_deleted", "ai_asset", strconv.FormatInt(assetID, 10),
		map[string]any{"batchId": strconv.FormatInt(batchID, 10), "filename": filename, "status": assetStatus}, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	c.Status(http.StatusNoContent)
}

func (s *Server) startAIBatch(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var status string
	var expiresAt time.Time
	err := tx.QueryRow(c.Request.Context(), `SELECT status,expires_at FROM ai_batch WHERE id=$1 AND student_id=$2 FOR UPDATE`, batchID, actor.UserID).Scan(&status, &expiresAt)
	if notFound(c, err, "AI 批次") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status == "queued" || status == "processing" || status == "review" {
		c.JSON(http.StatusAccepted, gin.H{"id": strconv.FormatInt(batchID, 10), "status": status})
		return
	}
	/* failed 可以重来，而且很便宜：逐张识图的结果都落在 ai_item 里，worker 的
	   processItem 见到已 complete 的条目直接复用，所以重试只会重跑真正没成的那
	   部分——归组失败时就只重跑归组那一次调用。
	   以前这里只认 uploading，学生等了十几分钟、钱也花了，最后归组失败就只剩
	   「整理新一批」一条路：全部材料重传、重识别。 */
	retry := status == "failed"
	if (status != "uploading" && !retry) || !time.Now().Before(expiresAt) {
		writeError(c, http.StatusConflict, "batch_not_startable", "该批次已不能开始处理", nil)
		return
	}
	var total, ready int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*),count(*) FILTER (WHERE status='ready') FROM ai_asset WHERE batch_id=$1 AND status<>'rejected'
	`, batchID).Scan(&total, &ready); err != nil {
		writeServiceError(c, err)
		return
	}
	if total == 0 {
		writeError(c, http.StatusConflict, "assets_not_ready", "请等待所有材料上传完成后再开始", gin.H{"total": total, "ready": ready})
		return
	}
	/* 只有首次开始才要求每份材料都是 ready；跑过一轮之后它们已经是 complete
	   或 failed，再拿 ready 去卡就永远重试不了。 */
	if !retry && ready != total {
		writeError(c, http.StatusConflict, "assets_not_ready", "请等待所有材料上传完成后再开始", gin.H{"total": total, "ready": ready})
		return
	}
	if err := lockUserQuota(c.Request.Context(), tx, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	settings := s.aiMaterialSettings(c.Request.Context())
	var dailyStarted int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM ai_batch
		 WHERE student_id=$1 AND queued_at>=date_trunc('day',now()) AND id<>$2
	`, actor.UserID, batchID).Scan(&dailyStarted); err != nil {
		writeServiceError(c, err)
		return
	}
	if dailyStarted >= settings.MaterialDailyBatches {
		writeError(c, http.StatusTooManyRequests, "ai_daily_quota", "今天的 AI 材料整理额度已用完", gin.H{"limit": settings.MaterialDailyBatches})
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE ai_batch SET status='queued',queued_at=COALESCE(queued_at,now()),total_count=$2,
		       error=NULL,completed_at=NULL,compose_stop_requested=false,compose_thinking='',updated_at=now()
		 WHERE id=$1
	`, batchID, total); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AIBatchCreatedEvent(),
		events.AIBatchCreatedPayload{BatchID: strconv.FormatInt(batchID, 10)}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.batch_started", "ai_batch", strconv.FormatInt(batchID, 10),
		map[string]any{"status": status}, map[string]any{"status": "queued", "total": total}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": strconv.FormatInt(batchID, 10), "status": "queued", "total": total})
}

// recomposeAIBatch 让已经出候选的批次把没读出来的材料补跑一遍，然后重新归组。
//
// 单独开一个端点而不是复用 start：start 对 review 是幂等的（重复点、并发轮询都
// 只回当前状态），而这里会覆盖掉学生已经改过的候选，绝不能被一次误触发触发。
//
// 逐张识图的结果都在 ai_item 里，worker 见到已 complete 的直接复用，所以这一趟
// 只重跑 failed 的那几张 + 一次归组。
func (s *Server) recomposeAIBatch(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var status string
	var expiresAt time.Time
	err := tx.QueryRow(c.Request.Context(), `SELECT status,expires_at FROM ai_batch WHERE id=$1 AND student_id=$2 FOR UPDATE`, batchID, actor.UserID).Scan(&status, &expiresAt)
	if notFound(c, err, "AI 批次") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "review" || !time.Now().Before(expiresAt) {
		writeError(c, http.StatusConflict, "batch_not_recomposable", "只有已出候选、尚未过期的批次可以重跑", nil)
		return
	}
	var failedItems int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM ai_item WHERE batch_id=$1 AND status='failed'`, batchID).Scan(&failedItems); err != nil {
		writeServiceError(c, err)
		return
	}
	/* 归组按块跑之后，"没归成"和"没识别成"是两件事：材料全都读出来了，但某一块归组
	   失败或者被手动停止，同样有东西可补。只看 ai_item 的话这种批次会被拒绝重跑，
	   而它恰恰是最该给一个重跑入口的那种。 */
	var pendingChunks int
	if err := tx.QueryRow(c.Request.Context(),
		`SELECT count(*) FROM ai_compose_chunk WHERE batch_id=$1 AND status<>'complete'`, batchID).Scan(&pendingChunks); err != nil {
		writeServiceError(c, err)
		return
	}
	if failedItems == 0 && pendingChunks == 0 {
		writeError(c, http.StatusConflict, "nothing_to_retry", "这一批没有需要重跑的材料", nil)
		return
	}
	/* 保留 result：重跑万一又失败，批次会落到 failed，旧候选至少还在库里，
	   不至于连审计都查不到学生当时看到的是什么。
	   清掉停止标记，否则重跑会立刻在第一块前又停下。 */
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE ai_batch SET status='queued',error=NULL,completed_at=NULL,
		       compose_stop_requested=false,compose_thinking='',updated_at=now()
		 WHERE id=$1 AND status='review'
	`, batchID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.AIBatchCreatedEvent(),
		events.AIBatchCreatedPayload{BatchID: strconv.FormatInt(batchID, 10)}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.batch_recomposed", "ai_batch", strconv.FormatInt(batchID, 10),
		map[string]any{"status": "review"}, map[string]any{"status": "queued"},
		map[string]any{"failedItems": failedItems}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": strconv.FormatInt(batchID, 10), "status": "queued", "retryingItems": failedItems})
}

/*
stopComposeAIBatch 让学生在归组半路下车，同时保住已经归好的候选。

	和「放弃整批」是两件事：那个会把识图结果一起删掉，等于把十几分钟的等待清零。
	归组按块落库之后，停在当前块、把已完成的块交出去成了可能，这才是卡了很久时
	真正想要的那个按钮。

	这里只置标记，不动状态：正在跑的那一块要让 worker 自己收尾，API 直接改成
	review 会和 worker 的收尾事务撞车。
*/
func (s *Server) stopComposeAIBatch(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var status string
	err := tx.QueryRow(c.Request.Context(),
		`SELECT status FROM ai_batch WHERE id=$1 AND student_id=$2 FOR UPDATE`, batchID, actor.UserID).Scan(&status)
	if notFound(c, err, "AI 批次") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "queued" && status != "processing" {
		writeError(c, http.StatusConflict, "batch_not_running", "只有正在处理的批次可以停止归组", nil)
		return
	}
	var complete int
	if err := tx.QueryRow(c.Request.Context(),
		`SELECT count(*) FROM ai_compose_chunk WHERE batch_id=$1 AND status='complete'`, batchID).Scan(&complete); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(),
		`UPDATE ai_batch SET compose_stop_requested=true,updated_at=now() WHERE id=$1`, batchID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.compose_stop_requested", "ai_batch", strconv.FormatInt(batchID, 10),
		map[string]any{"status": status}, map[string]any{"composeStopRequested": true},
		map[string]any{"completeChunks": complete}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"id": strconv.FormatInt(batchID, 10), "status": status, "completeChunks": complete,
	})
}

func (s *Server) aiBatch(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	record, err := loadOwnedAIBatch(c.Request.Context(), tx, batchID, actor.UserID, false)
	if notFound(c, err, "AI 批次") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !time.Now().Before(record.ExpiresAt) && (record.Status == "uploading" || record.Status == "review" || record.Status == "failed") {
		record.Status = "expired"
		_, _ = tx.Exec(c.Request.Context(), `UPDATE ai_batch SET status='expired',updated_at=now() WHERE id=$1`, batchID)
	}
	assets, err := loadAIAssets(c.Request.Context(), tx, batchID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	assetOut := make([]gin.H, 0, len(assets))
	for _, asset := range assets {
		item := gin.H{
			"id": strconv.FormatInt(asset.ID, 10), "filename": asset.Filename, "mediaType": asset.MediaType,
			"sizeBytes": asset.SizeBytes, "status": asset.Status, "pageCount": asset.PageCount, "error": asset.Error,
		}
		/* failed 也要给预览链接。识别失败的是模型这一步，原图还好端端在对象存储里，
		   而"哪一张没读出来"恰恰是学生最需要看清的那张——文件名多半是一串哈希，
		   没有缩略图就只能靠猜。rejected 不给：那是批次被放弃后清理过的对象。 */
		if asset.Status == "ready" || asset.Status == "processing" || asset.Status == "complete" || asset.Status == "applied" || asset.Status == "failed" {
			if u, signErr := s.deps.Objects.PresignGet(c.Request.Context(), asset.ObjectKey, 10*time.Minute, asset.Filename, true); signErr == nil {
				item["previewUrl"] = u.String()
			}
		}
		assetOut = append(assetOut, item)
	}
	itemRows, err := tx.Query(c.Request.Context(), `
		SELECT id,asset_id,page_no,status,perception,error,duration_ms
		  FROM ai_item WHERE batch_id=$1 ORDER BY asset_id,page_no
	`, batchID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]gin.H, 0)
	for itemRows.Next() {
		var id, assetID, durationMS int64
		var pageNo int
		var status string
		var perception []byte
		var itemError *string
		if err := itemRows.Scan(&id, &assetID, &pageNo, &status, &perception, &itemError, &durationMS); err != nil {
			itemRows.Close()
			writeServiceError(c, err)
			return
		}
		entry := gin.H{"id": strconv.FormatInt(id, 10), "assetId": strconv.FormatInt(assetID, 10), "page": pageNo, "status": status, "error": itemError, "durationMs": durationMS}
		if len(perception) > 0 {
			entry["perception"] = json.RawMessage(perception)
		}
		items = append(items, entry)
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	chunks, err := loadComposeProgress(c.Request.Context(), tx, batchID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": strconv.FormatInt(record.ID, 10), "status": record.Status,
		"total": record.TotalCount, "processed": record.ProcessedCount, "composePreview": composePreview(record.ComposePreview),
		"composeThinking": record.ComposeThinking, "composeChunks": chunks,
		"result": json.RawMessage(record.Result),
		"usage":  json.RawMessage(record.Usage), "visionModel": record.VisionModel, "textModel": record.TextModel,
		"promptVersion": record.PromptVersion, "error": record.Error, "startedAt": record.StartedAt,
		"completedAt": record.CompletedAt, "expiresAt": record.ExpiresAt, "createdAt": record.CreatedAt,
		"assets": assetOut, "items": items, "limits": s.aiLimits(c.Request.Context()),
	})
}

type applyAIBatchInput struct {
	Candidates []aiassist.Candidate `json:"candidates"`
}

type aiAppliedDraft struct {
	ID             string   `json:"id"`
	CandidateID    string   `json:"candidateId"`
	RequestedScore *float64 `json:"requestedScore"`
}

func (s *Server) applyAIBatch(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input applyAIBatchInput
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Candidates) == 0 || len(input.Candidates) > 100 {
		writeError(c, http.StatusBadRequest, "invalid_request", "候选申报参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	record, err := loadOwnedAIBatch(c.Request.Context(), tx, batchID, actor.UserID, true)
	if notFound(c, err, "AI 批次") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if record.Status == "complete" {
		writeError(c, http.StatusConflict, "batch_already_applied", "这个批次已经应用过，不能重复创建草稿", nil)
		return
	}
	if record.Status != "review" || !time.Now().Before(record.ExpiresAt) {
		writeError(c, http.StatusConflict, "batch_not_applicable", "该批次当前不能应用", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if current.ID != record.SchemeID || current.Version != record.SchemeVersion {
		writeError(c, http.StatusConflict, "scheme_changed", "综测方案已更新，请重新整理材料", nil)
		return
	}
	frozenScheme, err := restoreAIBatchScheme(current, record.SchemeSnapshot)
	if err != nil {
		writeError(c, http.StatusConflict, "scheme_snapshot_invalid", "本批次的方案快照无法校验，请重新整理材料", nil)
		return
	}
	if err := ensureCapability(current.Config, "submit", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "submission_closed", err.Error(), nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	assets, err := loadAIAssets(c.Request.Context(), tx, batchID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	assetByID := make(map[string]aiAssetRecord, len(assets))
	for _, asset := range assets {
		id := strconv.FormatInt(asset.ID, 10)
		assetByID[id] = asset
	}
	verified := make([]aiassist.Candidate, len(input.Candidates))
	prepared := make([]preparedSubmission, len(input.Candidates))
	for i, candidate := range input.Candidates {
		verified[i], prepared[i], err = prepareAICandidate(frozenScheme, candidate, assetByID, time.Now())
		if err != nil {
			writeError(c, http.StatusUnprocessableEntity, "candidate_invalid", err.Error(), gin.H{"candidateId": candidate.ID})
			return
		}
	}
	// Applying a batch copies each cited source into its own draft. Bound the
	// resulting storage too, since the same source may be cited by many edited
	// candidates and otherwise amplify one upload into unbounded copies.
	materialLimit := int64(s.aiLimits(c.Request.Context()).BatchMB) * 1024 * 1024
	var copiedBytes int64
	for _, candidate := range verified {
		seen := make(map[string]struct{}, len(candidate.Assets))
		for _, ref := range candidate.Assets {
			if _, duplicate := seen[ref.AssetID]; duplicate {
				continue
			}
			seen[ref.AssetID] = struct{}{}
			asset := assetByID[ref.AssetID]
			if asset.SizeBytes > materialLimit || copiedBytes > materialLimit-asset.SizeBytes {
				writeError(c, http.StatusRequestEntityTooLarge, "batch_storage_limit", fmt.Sprintf("应用批次产生的佐证副本不能超过 %d MB", materialLimit/(1024*1024)), nil)
				return
			}
			copiedBytes += asset.SizeBytes
		}
	}

	created := make([]aiAppliedDraft, 0, len(verified))
	copiedKeys := make([]string, 0)
	cleanupCopies := func() {
		for _, key := range copiedKeys {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.deps.Objects.Remove(cleanupCtx, key)
			cancel()
		}
	}
	for i, candidate := range verified {
		claim, _ := json.Marshal(candidate.Claim)
		var submissionID int64
		err := tx.QueryRow(c.Request.Context(), `
			INSERT INTO submission
			    (class_id,student_id,scheme_id,scheme_version,category_key,item_key,filed_category_key,filed_item_key,
			     title,claim,requested_score,status,source,rule_snapshot,filed_rule_snapshot,markdown_note)
			VALUES ($1,$2,$3,$4,$5,$6,$5,$6,$7,$8,$9,'draft','ai',$10,$10,$11)
			RETURNING id
		`, actor.ClassID, actor.UserID, prepared[i].SchemeID, prepared[i].SchemeVersion,
			prepared[i].Category.Key, prepared[i].Item.Key, strings.TrimSpace(candidate.Title), claim,
			prepared[i].Requested, prepared[i].Snapshot, candidate.Note).Scan(&submissionID)
		if err != nil {
			cleanupCopies()
			writeServiceError(c, err)
			return
		}
		uniqueAssets := make(map[string]struct{})
		for _, ref := range candidate.Assets {
			if _, duplicate := uniqueAssets[ref.AssetID]; duplicate {
				continue
			}
			uniqueAssets[ref.AssetID] = struct{}{}
			asset := assetByID[ref.AssetID]
			destination := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/submission-" + strconv.FormatInt(submissionID, 10) + "/" + randomObjectPart() + objectKeySuffix(asset.Filename)
			if err := s.deps.Objects.Copy(c.Request.Context(), asset.ObjectKey, destination, asset.MediaType, asset.SizeBytes); err != nil {
				cleanupCopies()
				writeServiceError(c, fmt.Errorf("copy AI evidence: %w", err))
				return
			}
			copiedKeys = append(copiedKeys, destination)
			if _, err := tx.Exec(c.Request.Context(), `
				INSERT INTO evidence (class_id,submission_id,kind,object_key,filename,media_type,size_bytes,sha256,status,created_by)
				VALUES ($1,$2,'claim',$3,$4,$5,$6,$7,'ready',$8)
			`, actor.ClassID, submissionID, destination, asset.Filename, asset.MediaType, asset.SizeBytes, asset.SHA256, actor.UserID); err != nil {
				cleanupCopies()
				writeServiceError(c, err)
				return
			}
			if _, err := tx.Exec(c.Request.Context(), `
				UPDATE ai_asset SET status='applied',applied_submission_id=COALESCE(applied_submission_id,$2),updated_at=now() WHERE id=$1
			`, asset.ID, submissionID); err != nil {
				cleanupCopies()
				writeServiceError(c, err)
				return
			}
		}
		if err := appendAudit(c, tx, "submission.draft_created", "submission", strconv.FormatInt(submissionID, 10), nil,
			map[string]any{"category": prepared[i].Category.Key, "itemKey": prepared[i].Item.Key, "source": "ai"},
			map[string]any{"aiBatchId": strconv.FormatInt(batchID, 10), "candidateId": candidate.ID}); err != nil {
			cleanupCopies()
			writeServiceError(c, err)
			return
		}
		created = append(created, aiAppliedDraft{ID: strconv.FormatInt(submissionID, 10), CandidateID: candidate.ID, RequestedScore: prepared[i].Requested})
	}
	result, _ := json.Marshal(aiassist.BatchDraft{Candidates: verified})
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE ai_batch SET status='complete',result=$2,completed_at=now(),updated_at=now(),error=NULL WHERE id=$1
	`, batchID, result); err != nil {
		cleanupCopies()
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.batch_applied", "ai_batch", strconv.FormatInt(batchID, 10),
		map[string]any{"status": record.Status}, map[string]any{"status": "complete", "drafts": len(created)}, nil); err != nil {
		cleanupCopies()
		writeServiceError(c, err)
		return
	}
	s.scheduleStorageReconcile(c, actor.ClassID)
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(batchID, 10), "status": "complete", "drafts": created})
}

// AI batches freeze scoring rules, not the class timeline. Config deliberately
// omits the runtime envelope from JSON; validating a decoded snapshot without
// restoring it would reject every batch because both window dates are zero.
// Keep the original scoring tree while validating against the current class
// timeline, just as loadCurrentScheme does for the published scheme.
func restoreAIBatchScheme(current storedScheme, snapshot []byte) (storedScheme, error) {
	var cfg scheme.Config
	if err := json.Unmarshal(snapshot, &cfg); err != nil {
		return storedScheme{}, err
	}
	cfg.Window = current.Config.Window
	cfg.Capabilities = current.Config.Capabilities
	cfg.HonorRoll = current.Config.HonorRoll
	if err := scheme.Validate(cfg); err != nil {
		return storedScheme{}, err
	}
	frozen := current
	frozen.Config = cfg
	frozen.Raw = append([]byte(nil), snapshot...)
	return frozen, nil
}

func prepareAICandidate(current storedScheme, candidate aiassist.Candidate, assets map[string]aiAssetRecord, capturedAt time.Time) (aiassist.Candidate, preparedSubmission, error) {
	if len(candidate.Assets) == 0 {
		return candidate, preparedSubmission{}, errors.New("候选缺少可用的佐证材料")
	}
	allowedAssets := make(map[string]struct{}, len(candidate.Assets))
	for _, ref := range candidate.Assets {
		id := strings.TrimSpace(ref.AssetID)
		asset, ok := assets[id]
		if !ok || (asset.Status != "complete" && asset.Status != "applied") {
			return candidate, preparedSubmission{}, errors.New("候选引用了不属于本批次或尚未处理完成的材料")
		}
		allowedAssets[id] = struct{}{}
	}
	// A human edit is request data, not model output to repair. Validate before
	// normalization so an invalid tier or out-of-range score cannot be cleared
	// into an otherwise valid blank draft. Genuinely unfilled claims still use
	// the normal draft semantics and may be completed before review.
	claim, err := json.Marshal(candidate.Claim)
	if err != nil {
		return candidate, preparedSubmission{}, errors.New("claim 字段不符合计分规则")
	}
	if _, err := prepareSubmission(current, submissionInput{
		Category: candidate.CategoryKey, ItemKey: candidate.ItemKey, Title: candidate.Title,
		Claim: claim, Note: candidate.Note,
	}, capturedAt, false); err != nil {
		return candidate, preparedSubmission{}, err
	}
	verified := aiassist.VerifyEditedCandidate(candidate, current.Config, allowedAssets)
	verified.Assets = normaliseAIPageRefs(verified.Assets, assets)
	claim, _ = json.Marshal(verified.Claim)
	prepared, err := prepareSubmission(current, submissionInput{
		Category: verified.CategoryKey, ItemKey: verified.ItemKey, Title: verified.Title,
		Claim: claim, Note: verified.Note,
	}, capturedAt, false)
	return verified, prepared, err
}

// normaliseAIPageRefs 把候选里的页码修到这份材料真实存在的范围内，并去掉修正后重复
// 的引用。
//
// 页码只是界面上"第几页"的标注：佐证是整份原件按 assetId 去重复制的，页码对最终草稿
// 没有任何影响。以前这里页码一对不上就整批 422——而提示词的示例恰好写着 "page":0，
// 模型给 PDF 照抄一个 0 就能让全部候选在最后一步交不上去。
func normaliseAIPageRefs(refs []aiassist.AssetRef, assets map[string]aiAssetRecord) []aiassist.AssetRef {
	seen := make(map[string]struct{}, len(refs))
	kept := refs[:0]
	for _, ref := range refs {
		ref.Page = normaliseAIPage(assets[ref.AssetID], ref.Page)
		key := ref.AssetID + ":" + strconv.Itoa(ref.Page)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, ref)
	}
	return kept
}

func normaliseAIPage(asset aiAssetRecord, page int) int {
	if asset.MediaType != "application/pdf" {
		return 0
	}
	if asset.PageCount == nil || *asset.PageCount < 1 || page < 1 {
		return 1
	}
	if page > *asset.PageCount {
		return *asset.PageCount
	}
	return page
}

func (s *Server) deleteAIBatch(c *gin.Context) {
	batchID, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	record, err := loadOwnedAIBatch(c.Request.Context(), tx, batchID, actor.UserID, true)
	if notFound(c, err, "AI 批次") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if record.Status == "complete" {
		writeError(c, http.StatusConflict, "batch_applied", "已创建草稿的批次需要保留审计记录", nil)
		return
	}
	if record.Status == "canceled" {
		c.Status(http.StatusNoContent)
		return
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT object_key FROM ai_asset WHERE batch_id=$1 AND status<>'applied' AND applied_submission_id IS NULL
	`, batchID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		keys = append(keys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE ai_batch SET status='canceled',error=NULL,updated_at=now() WHERE id=$1`, batchID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE ai_asset SET status='rejected',error='batch canceled',updated_at=now()
		 WHERE batch_id=$1 AND status<>'applied' AND applied_submission_id IS NULL
	`, batchID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE ai_item SET status='failed',error='batch canceled',updated_at=now()
		 WHERE batch_id=$1 AND status IN ('queued','processing')
	`, batchID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "ai.batch_canceled", "ai_batch", strconv.FormatInt(batchID, 10),
		map[string]any{"status": record.Status}, map[string]any{"status": "canceled"}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	for _, key := range keys {
		s.removeObjectAfterCommit(c, actor.ClassID, key)
	}
	c.Status(http.StatusNoContent)
}

func loadOwnedAIBatch(ctx context.Context, tx pgx.Tx, batchID, studentID int64, forUpdate bool) (aiBatchRecord, error) {
	query := `
		SELECT id,scheme_id,scheme_version,scheme_snapshot,status,total_count,processed_count,compose_preview,compose_thinking,
		       result,usage,vision_model,text_model,prompt_version,error,started_at,completed_at,expires_at,created_at
		  FROM ai_batch WHERE id=$1 AND student_id=$2`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row aiBatchRecord
	err := tx.QueryRow(ctx, query, batchID, studentID).Scan(
		&row.ID, &row.SchemeID, &row.SchemeVersion, &row.SchemeSnapshot, &row.Status, &row.TotalCount,
		&row.ProcessedCount, &row.ComposePreview, &row.ComposeThinking,
		&row.Result, &row.Usage, &row.VisionModel, &row.TextModel, &row.PromptVersion,
		&row.Error, &row.StartedAt, &row.CompletedAt, &row.ExpiresAt, &row.CreatedAt,
	)
	return row, err
}

func loadAIAssets(ctx context.Context, tx pgx.Tx, batchID int64) ([]aiAssetRecord, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,object_key,filename,media_type,size_bytes,sha256,object_etag,status,page_count,applied_submission_id,error
		  FROM ai_asset WHERE batch_id=$1 ORDER BY id
	`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]aiAssetRecord, 0)
	for rows.Next() {
		var asset aiAssetRecord
		if err := rows.Scan(&asset.ID, &asset.ObjectKey, &asset.Filename, &asset.MediaType, &asset.SizeBytes,
			&asset.SHA256, &asset.ObjectETag, &asset.Status, &asset.PageCount, &asset.AppliedSubmissionID, &asset.Error); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// 归组现在是逐块跑、每块落库，所以这一步终于有真进度可报：几块里归完了几块、
// 已经出了多少条候选。原来界面上只能显示一个不动的"归组中"。
func loadComposeProgress(ctx context.Context, tx pgx.Tx, batchID int64) (gin.H, error) {
	var total, complete, failed, candidates int
	err := tx.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status='complete'),
		       count(*) FILTER (WHERE status='failed'),
		       COALESCE(sum(jsonb_array_length(candidates)) FILTER (WHERE status='complete'),0)
		  FROM ai_compose_chunk WHERE batch_id=$1
	`, batchID).Scan(&total, &complete, &failed, &candidates)
	if err != nil {
		return nil, err
	}
	return gin.H{"total": total, "complete": complete, "failed": failed, "candidates": candidates}, nil
}

// 归组过程中已经成型的候选标题。库里存的是一段 JSON 数组，坏掉就当没有——这一列
// 纯粹是给等待中的界面看的，不值得为它把整个批次接口打成 500。
func composePreview(raw string) []string {
	titles := make([]string, 0)
	if raw == "" {
		return titles
	}
	if json.Unmarshal([]byte(raw), &titles) != nil {
		return []string{}
	}
	return titles
}
