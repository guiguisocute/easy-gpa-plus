package agentjob

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/knowledge"
	"easygpa/backend/internal/llm"
)

type platformDocumentRecord struct {
	ID, BlobID                     int64
	Filename, ObjectKey, MediaType string
	Status                         string
	SizeBytes                      int64
	OCRRoute                       []byte
}

func (w *Worker) processPlatformDocument(ctx context.Context, documentID int64) (processErr error) {
	record, err := w.loadPlatformDocument(ctx, documentID)
	if err != nil || record.ID == 0 {
		return err
	}
	if record.Status == "ready" || record.Status == "partial" || record.Status == "unsupported" || record.Status == "deleted" || record.Status == "retired" || record.Status == "superseded" {
		return nil
	}
	if err := w.setPlatformStatus(ctx, record.ID, record.BlobID, "processing", "", ""); err != nil {
		return err
	}
	failureMessage := "文件处理失败，可重新处理"
	markFailed := func(failureCtx context.Context, message string) error {
		return w.setPlatformStatus(failureCtx, record.ID, record.BlobID, "failed", "", message)
	}
	defer func() {
		if processErr != nil {
			processErr = errors.Join(processErr, persistProcessingFailure(ctx, failureMessage, markFailed))
		}
	}()
	tempDir, err := os.MkdirTemp("", "easygpa-platform-knowledge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	localPath := filepath.Join(tempDir, "source")
	failureMessage = "原始文件读取失败，可重新处理"
	if err := w.downloadObject(ctx, record.ObjectKey, record.SizeBytes, localPath); err != nil {
		return err
	}
	failureMessage = "原始文件校验失败，可重新处理"
	hash, size, err := hashFile(localPath)
	if err != nil || size != record.SizeBytes {
		if err != nil {
			return err
		}
		return persistProcessingFailure(ctx, failureMessage, markFailed)
	}
	failureMessage = "转换配置读取失败，可重新处理"
	runtime, runtimeErr := w.runtime(ctx)
	if runtimeErr != nil {
		return runtimeErr
	}
	converter := knowledge.Converter{
		Vision: w.client, AllowOCR: runtime.Flags.AIEnabled && runtime.Flags.KnowledgeEnabled && runtime.Flags.KnowledgeEgressEnabled,
		VisionModel: runtime.AI.VisionModel, CommandTime: 60 * time.Second, DisableNativeTools: !runtime.Flags.NativeToolsEnabled,
		MaxPDFPages: runtime.Lifecycle.KnowledgeMaxPDFPages, MaxArchiveMembers: runtime.Lifecycle.KnowledgeMaxArchiveMembers,
		MaxArchiveBytes: int64(runtime.Lifecycle.KnowledgeMaxArchiveMB) * 1024 * 1024, MaxArchiveRatio: runtime.Lifecycle.KnowledgeMaxArchiveRatio,
		MaxArchiveDepth: runtime.Lifecycle.KnowledgeMaxArchiveDepth, MaxExtractedBytes: int64(runtime.Lifecycle.KnowledgeMaxExtractedTextMB) * 1024 * 1024,
	}
	if route, ok := parseRoute(record.OCRRoute); ok {
		converter.VisionModel, converter.VisionRoute = route.Model, &route
	}
	result, err := converter.Convert(ctx, localPath, record.Filename, record.MediaType)
	if err != nil {
		failureMessage = safeConversionError(err)
		if llm.IsRetryable(err) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return persistProcessingFailure(ctx, failureMessage, markFailed)
	}
	failureMessage = "文件去重失败，可重新处理"
	duplicate, removeKey, err := w.attachPlatformDuplicate(ctx, record, hash)
	if err != nil {
		return err
	}
	if duplicate {
		_ = w.objects.Remove(ctx, removeKey)
		return nil
	}
	failureMessage = "转换结果保存失败，可重新处理"
	return w.persistPlatformConversion(ctx, record.BlobID, hash, result)
}

func parseRoute(raw []byte) (llm.Route, bool) {
	var route llm.Route
	if json.Unmarshal(raw, &route) == nil && strings.TrimSpace(route.Model) != "" {
		return route, true
	}
	return route, false
}

func (w *Worker) loadPlatformDocument(ctx context.Context, documentID int64) (platformDocumentRecord, error) {
	var record platformDocumentRecord
	err := w.opsPool.QueryRow(ctx, `
		SELECT d.id,d.blob_id,d.filename,b.object_key,b.media_type,b.size_bytes,d.status,b.ocr_route
		  FROM platform_knowledge_document d JOIN platform_knowledge_blob b ON b.id=d.blob_id
		 WHERE d.id=$1 AND d.source_kind='custom' AND d.deleted_at IS NULL
	`, documentID).Scan(&record.ID, &record.BlobID, &record.Filename, &record.ObjectKey, &record.MediaType, &record.SizeBytes, &record.Status, &record.OCRRoute)
	if errors.Is(err, pgx.ErrNoRows) {
		return record, nil
	}
	return record, err
}

func (w *Worker) downloadObject(ctx context.Context, objectKey string, expectedSize int64, target string) error {
	if expectedSize <= 0 || expectedSize > 512<<20 {
		return errors.New("platform knowledge object has invalid size")
	}
	reader, err := w.objects.Open(ctx, objectKey)
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, expectedSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written != expectedSize {
		return fmt.Errorf("platform object size mismatch: declared %d, downloaded %d", expectedSize, written)
	}
	return closeErr
}

func (w *Worker) setPlatformStatus(ctx context.Context, documentID, blobID int64, status, warning, errorMessage string) error {
	tx, err := w.opsPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE platform_knowledge_blob SET status=$2,error=NULLIF($3,''),updated_at=now() WHERE id=$1`, blobID, status, errorMessage)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE platform_knowledge_document SET status=$2,warning=NULLIF($3,''),error=NULLIF($4,''),updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, documentID, status, warning, errorMessage)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// attachPlatformDuplicate atomically reuses a previously converted raw object
// and its extracted entries. The current upload is kept as a short-lived
// superseded blob so maintenance can remove its bytes after the grace period.
func (w *Worker) attachPlatformDuplicate(ctx context.Context, record platformDocumentRecord, hash string) (bool, string, error) {
	hashBytes, err := hex.DecodeString(hash[:16])
	if err != nil || len(hashBytes) != 8 {
		return false, "", errors.New("invalid platform document hash")
	}
	lock := int64(binary.BigEndian.Uint64(hashBytes))
	tx, err := w.opsPool.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lock); err != nil {
		return false, "", err
	}
	var existingID int64
	var existingKey, existingStatus string
	err = tx.QueryRow(ctx, `
		SELECT b.id,b.object_key,b.status
		  FROM platform_knowledge_blob b
		 WHERE b.sha256=$1 AND b.id<>$2 AND b.deleted_at IS NULL AND b.status<>'superseded'
		 ORDER BY b.id LIMIT 1
	`, hash, record.BlobID).Scan(&existingID, &existingKey, &existingStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `UPDATE platform_knowledge_blob SET sha256=$2,status='processing',error=NULL,updated_at=now() WHERE id=$1`, record.BlobID, hash); err != nil {
			return false, "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, "", err
		}
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	var sourceDocumentID int64
	var sourceStatus, extractor string
	var searchable bool
	var entryCount int
	var pageCount, sheetCount *int
	var warning, conversionError *string
	if err := tx.QueryRow(ctx, `
		SELECT d.id,d.status,d.searchable,COALESCE(d.extractor,''),d.entries_count,d.page_count,d.sheet_count,d.warning,d.error
		  FROM platform_knowledge_document d
		 WHERE d.blob_id=$1 AND d.deleted_at IS NULL ORDER BY d.id LIMIT 1
	`, existingID).Scan(&sourceDocumentID, &sourceStatus, &searchable, &extractor, &entryCount, &pageCount, &sheetCount, &warning, &conversionError); err != nil {
		return false, "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE platform_knowledge_document
		   SET blob_id=$2,status=$3,searchable=$4,extractor=$5,entries_count=$6,page_count=$7,sheet_count=$8,warning=$9,error=$10,updated_at=now()
		 WHERE id=$1
	`, record.ID, existingID, sourceStatus, searchable, extractor, entryCount, pageCount, sheetCount, warning, conversionError); err != nil {
		return false, "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM platform_knowledge_entry WHERE document_id=$1`, record.ID); err != nil {
		return false, "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_knowledge_entry (document_id,kind,locator,text_object_key,plain_text,line_count,char_count,metadata,content_hash)
		SELECT $1,kind,locator,text_object_key,plain_text,line_count,char_count,metadata,content_hash
		  FROM platform_knowledge_entry WHERE document_id=$2 ORDER BY id
	`, record.ID, sourceDocumentID); err != nil {
		return false, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE platform_knowledge_blob SET sha256=NULL,status='superseded',deleted_at=now(),updated_at=now() WHERE id=$1`, record.BlobID); err != nil {
		return false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", err
	}
	return true, record.ObjectKey, nil
}

func (w *Worker) persistPlatformConversion(ctx context.Context, blobID int64, hash string, result knowledge.Result) error {
	if err := result.ValidateText(); err != nil {
		return err
	}
	stored := make([]string, 0, len(result.Entries))
	for index, entry := range result.Entries {
		contentHash := entry.ContentHash()
		key := fmt.Sprintf("platform/knowledge/blob-%d/entry-%03d-%s.txt", blobID, index+1, contentHash[:16])
		if err := w.objects.Put(ctx, key, "text/plain; charset=utf-8", strings.NewReader(entry.Text), int64(len([]byte(entry.Text)))); err != nil {
			for _, prior := range stored {
				_ = w.objects.Remove(context.Background(), prior)
			}
			return err
		}
		stored = append(stored, key)
	}
	usage, _ := json.Marshal(result.Usage)
	tx, err := w.opsPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id FROM platform_knowledge_document WHERE blob_id=$1 AND deleted_at IS NULL ORDER BY id FOR UPDATE`, blobID)
	if err != nil {
		return err
	}
	documentIDs := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		documentIDs = append(documentIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(documentIDs) == 0 {
		return errors.New("platform knowledge document was removed during conversion")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM platform_knowledge_entry WHERE document_id=ANY($1)`, documentIDs); err != nil {
		return err
	}
	for _, currentDocumentID := range documentIDs {
		for index, entry := range result.Entries {
			locator, _ := json.Marshal(entry.Locator)
			metadata, _ := json.Marshal(entry.Metadata)
			if _, err := tx.Exec(ctx, `
				INSERT INTO platform_knowledge_entry (document_id,kind,locator,text_object_key,plain_text,line_count,char_count,metadata,content_hash)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			`, currentDocumentID, entry.Kind, locator, stored[index], entry.Text, entry.LineCount(), len([]rune(entry.Text)), metadata, entry.ContentHash()); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE platform_knowledge_blob SET sha256=$2,status=$3,converter_version=$4,usage=$5,error=NULL,updated_at=now() WHERE id=$1`, blobID, hash, result.Status, knowledge.ConverterVersion, usage); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE platform_knowledge_document SET status=$2,searchable=$3,extractor=$4,entries_count=$5,page_count=NULLIF($6,0),sheet_count=NULLIF($7,0),warning=NULLIF($8,''),error=NULL,updated_at=now() WHERE blob_id=$1 AND deleted_at IS NULL`, blobID, result.Status, result.Searchable, result.Extractor, len(result.Entries), result.PageCount, result.SheetCount, result.Warning); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
