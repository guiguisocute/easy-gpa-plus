// Package agentjob owns the asynchronous knowledge conversion and agent
// message workers.  Every business query runs inside a tenant transaction.
package agentjob

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/knowledge"
	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/store"
)

type ObjectStore interface {
	Open(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, string, io.Reader, int64) error
	Remove(context.Context, string) error
}

type Runtime struct {
	Flags     opsconfig.Flags
	AI        opsconfig.AIRuntime
	Lifecycle opsconfig.Lifecycle
}

type RuntimeProvider func(context.Context) (Runtime, error)

type Config struct {
	Runtime RuntimeProvider
	Client  llm.Client
	OpsPool *pgxpool.Pool
}

type Worker struct {
	pool    *pgxpool.Pool
	opsPool *pgxpool.Pool
	objects ObjectStore
	runtime RuntimeProvider
	client  llm.Client
}

func NewWorker(pool *pgxpool.Pool, objects ObjectStore, cfg Config) (*Worker, error) {
	if pool == nil || objects == nil || cfg.Runtime == nil || cfg.Client == nil {
		return nil, errors.New("agent worker requires database, object storage, runtime config and model client")
	}
	return &Worker{pool: pool, opsPool: cfg.OpsPool, objects: objects, runtime: cfg.Runtime, client: cfg.Client}, nil
}

func (w *Worker) Handle(ctx context.Context, event events.Event) error {
	switch event.Type {
	case events.KnowledgeDocumentCreated:
		payload, decodeErr := events.Decode(event, events.KnowledgeDocumentCreatedEvent())
		if decodeErr != nil {
			return errors.New("knowledge.document.created payload is invalid")
		}
		id, err := positiveID(payload.DocumentID)
		if err != nil {
			return err
		}
		return w.withLock(ctx, "knowledge.document", id, func() error { return w.processDocument(ctx, event.ClassID, id) })
	case events.PlatformKnowledgeDocumentCreated:
		if event.ClassID != 0 || w.opsPool == nil {
			return errors.New("platform knowledge event requires ops database and class_id=0")
		}
		payload, decodeErr := events.Decode(event, events.PlatformDocumentCreatedEvent())
		if decodeErr != nil {
			return errors.New("platform knowledge payload is invalid")
		}
		id, err := positiveID(payload.DocumentID)
		if err != nil {
			return err
		}
		return w.withLock(ctx, "platform.knowledge.document", id, func() error { return w.processPlatformDocument(ctx, id) })
	case events.AgentMessageCreated:
		payload, decodeErr := events.Decode(event, events.AgentMessageCreatedEvent())
		if decodeErr != nil {
			return errors.New("agent.message.created payload is invalid")
		}
		id, err := positiveID(payload.MessageID)
		if err != nil {
			return err
		}
		return w.withLock(ctx, "agent.message", id, func() error { return w.processMessage(ctx, event.ClassID, id) })
	default:
		return nil
	}
}

func positiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("event payload contains an invalid id")
	}
	return id, nil
}

func (w *Worker) withLock(ctx context.Context, namespace string, id int64, fn func() error) error {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	key := lockKey(namespace, id)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key)
	}()
	return fn()
}

type documentRecord struct {
	ID, BlobID, UploaderID int64
	Filename, LogicalPath  string
	MediaType, ObjectKey   string
	Status                 string
	SizeBytes              int64
	Approved               bool
	OCRRoute               []byte
}

func (w *Worker) processDocument(ctx context.Context, classID, documentID int64) (processErr error) {
	record, err := w.loadDocument(ctx, classID, documentID)
	if err != nil || record.ID == 0 {
		return err
	}
	if record.Status == "ready" || record.Status == "partial" || record.Status == "unsupported" || record.Status == "deleted" || record.Status == "superseded" {
		return nil
	}
	if err := w.setDocumentStatus(ctx, classID, record.BlobID, "processing", "", ""); err != nil {
		return err
	}
	failureMessage := "文件处理失败，可重新处理"
	markFailed := func(failureCtx context.Context, message string) error {
		return w.failDocument(failureCtx, classID, record.BlobID, message)
	}
	defer func() {
		if processErr != nil {
			processErr = errors.Join(processErr, persistProcessingFailure(ctx, failureMessage, markFailed))
		}
	}()

	tempDir, err := os.MkdirTemp("", "easygpa-knowledge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	localPath := filepath.Join(tempDir, "source")
	failureMessage = "原始文件读取失败，可重新处理"
	if err := w.download(ctx, record.ObjectKey, record.SizeBytes, localPath); err != nil {
		return err
	}
	failureMessage = "原始文件校验失败，可重新处理"
	hash, actualSize, err := hashFile(localPath)
	if err != nil {
		return err
	}
	if actualSize != record.SizeBytes {
		return persistProcessingFailure(ctx, "原始文件大小与上传声明不一致", markFailed)
	}
	failureMessage = "文件去重失败，可重新处理"
	deduplicated, duplicateKey, err := w.attachDuplicate(ctx, classID, record, hash)
	if err != nil {
		return err
	}
	if deduplicated {
		if duplicateKey != "" {
			_ = w.objects.Remove(ctx, duplicateKey)
		}
		return nil
	}

	runtime, runtimeErr := w.runtime(ctx)
	allowOCR := runtimeErr == nil && runtime.Flags.AIEnabled && runtime.Flags.KnowledgeEnabled && runtime.Flags.KnowledgeEgressEnabled && record.Approved
	converter := knowledge.Converter{Vision: w.client, AllowOCR: allowOCR, CommandTime: 60 * time.Second, DisableNativeTools: true}
	if runtimeErr == nil {
		converter.DisableNativeTools = !runtime.Flags.NativeToolsEnabled
		converter.CommandTime = time.Duration(runtime.Lifecycle.KnowledgeConverterTimeoutSeconds) * time.Second
		converter.MaxPDFPages = runtime.Lifecycle.KnowledgeMaxPDFPages
		converter.MaxArchiveMembers = runtime.Lifecycle.KnowledgeMaxArchiveMembers
		converter.MaxArchiveBytes = int64(runtime.Lifecycle.KnowledgeMaxArchiveMB) * 1024 * 1024
		converter.MaxArchiveRatio = runtime.Lifecycle.KnowledgeMaxArchiveRatio
		converter.MaxArchiveDepth = runtime.Lifecycle.KnowledgeMaxArchiveDepth
		converter.MaxExtractedBytes = int64(runtime.Lifecycle.KnowledgeMaxExtractedTextMB) * 1024 * 1024
	}
	var route llm.Route
	if json.Unmarshal(record.OCRRoute, &route) == nil && strings.TrimSpace(route.Model) != "" {
		converter.VisionModel = route.Model
		converter.VisionRoute = &route
	} else if runtimeErr == nil {
		converter.VisionModel = runtime.AI.VisionModel
	}
	result, err := converter.Convert(ctx, localPath, record.Filename, record.MediaType)
	if err != nil {
		failureMessage = safeConversionError(err)
		if llm.IsRetryable(err) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return persistProcessingFailure(ctx, failureMessage, markFailed)
	}
	failureMessage = "转换结果保存失败，可重新处理"
	return w.persistConversion(ctx, classID, record.BlobID, result)
}

func (w *Worker) loadDocument(ctx context.Context, classID, documentID int64) (documentRecord, error) {
	var record documentRecord
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT d.id,d.blob_id,d.uploaded_by,d.filename,d.logical_path,b.media_type,b.object_key,d.status,b.size_bytes,
			       COALESCE(p.external_processing_approved,false),b.ocr_route
			  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id
			  LEFT JOIN knowledge_policy p ON p.class_id=d.class_id
			 WHERE d.id=$1 AND d.deleted_at IS NULL
		`, documentID).Scan(&record.ID, &record.BlobID, &record.UploaderID, &record.Filename, &record.LogicalPath,
			&record.MediaType, &record.ObjectKey, &record.Status, &record.SizeBytes, &record.Approved, &record.OCRRoute)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return record, err
}

func (w *Worker) download(ctx context.Context, objectKey string, expectedSize int64, target string) error {
	if expectedSize <= 0 || expectedSize > 512<<20 {
		return errors.New("knowledge object has an invalid declared size")
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
		return fmt.Errorf("knowledge object size mismatch: declared %d, downloaded %d", expectedSize, written)
	}
	return closeErr
}

func hashFile(filename string) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func (w *Worker) attachDuplicate(ctx context.Context, classID int64, record documentRecord, hash string) (bool, string, error) {
	var duplicate bool
	var removeKey string
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		hashBytes, _ := hex.DecodeString(hash[:16])
		lock := int64(binary.BigEndian.Uint64(hashBytes))
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lock); err != nil {
			return err
		}
		var existingID int64
		var status, converter *string
		err := tx.QueryRow(ctx, `
			SELECT id,status,converter_version FROM knowledge_blob
			 WHERE sha256=$1 AND id<>$2 AND deleted_at IS NULL AND status<>'superseded'
			 ORDER BY id LIMIT 1
		`, hash, record.BlobID).Scan(&existingID, &status, &converter)
		if !errors.Is(err, pgx.ErrNoRows) && err != nil {
			return err
		}
		if existingID == 0 {
			_, err = tx.Exec(ctx, `UPDATE knowledge_blob SET sha256=$2,status='processing',error=NULL,updated_at=now() WHERE id=$1`, record.BlobID, hash)
			return err
		}
		duplicate = true
		removeKey = record.ObjectKey
		var docStatus, extractor, warning, errorMessage *string
		var searchable bool
		var entryCount int
		var pageCount, sheetCount *int
		_ = tx.QueryRow(ctx, `
			SELECT status,searchable,extractor,entries_count,page_count,sheet_count,warning,error
			  FROM knowledge_document WHERE blob_id=$1 AND deleted_at IS NULL ORDER BY id LIMIT 1
		`, existingID).Scan(&docStatus, &searchable, &extractor, &entryCount, &pageCount, &sheetCount, &warning, &errorMessage)
		resolvedStatus := "queued"
		if status != nil {
			resolvedStatus = *status
		}
		if docStatus != nil {
			resolvedStatus = *docStatus
		}
		if _, err := tx.Exec(ctx, `
			UPDATE knowledge_document
			   SET blob_id=$2,status=$3,searchable=$4,extractor=$5,entries_count=$6,page_count=$7,sheet_count=$8,
			       warning=$9,error=$10,updated_at=now()
			 WHERE id=$1
		`, record.ID, existingID, resolvedStatus, searchable, extractor, entryCount, pageCount, sheetCount, warning, errorMessage); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE knowledge_blob SET sha256=$2,status='superseded',deleted_at=now(),updated_at=now() WHERE id=$1`, record.BlobID, hash)
		return err
	})
	return duplicate, removeKey, err
}

func (w *Worker) persistConversion(ctx context.Context, classID, blobID int64, result knowledge.Result) error {
	if err := result.ValidateText(); err != nil {
		return err
	}
	type storedEntry struct {
		entry knowledge.Entry
		key   string
	}
	stored := make([]storedEntry, 0, len(result.Entries))
	for index, entry := range result.Entries {
		key := fmt.Sprintf("class-%d/knowledge/blob-%d/entry-%03d-%s.txt", classID, blobID, index+1, entry.ContentHash()[:16])
		if err := w.objects.Put(ctx, key, "text/plain; charset=utf-8", strings.NewReader(entry.Text), int64(len([]byte(entry.Text)))); err != nil {
			for _, prior := range stored {
				_ = w.objects.Remove(context.Background(), prior.key)
			}
			return err
		}
		stored = append(stored, storedEntry{entry: entry, key: key})
	}
	usage, _ := json.Marshal(result.Usage)
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM knowledge_entry WHERE blob_id=$1`, blobID); err != nil {
			return err
		}
		for _, item := range stored {
			locator, _ := json.Marshal(item.entry.Locator)
			metadata, _ := json.Marshal(item.entry.Metadata)
			if _, err := tx.Exec(ctx, `
				INSERT INTO knowledge_entry
				    (class_id,blob_id,kind,locator,text_object_key,plain_text,line_count,char_count,metadata,content_hash)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			`, classID, blobID, item.entry.Kind, locator, item.key, item.entry.Text, item.entry.LineCount(), len([]rune(item.entry.Text)), metadata, item.entry.ContentHash()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE knowledge_blob
			   SET status=$2,converter_version=$3,usage=$4,error=NULL,updated_at=now()
			 WHERE id=$1
		`, blobID, result.Status, knowledge.ConverterVersion, usage); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE knowledge_document
			   SET status=$2,searchable=$3,extractor=$4,entries_count=$5,
			       page_count=NULLIF($6,0),sheet_count=NULLIF($7,0),warning=NULLIF($8,''),error=NULL,updated_at=now()
			 WHERE blob_id=$1 AND deleted_at IS NULL
		`, blobID, result.Status, result.Searchable, result.Extractor, len(stored), result.PageCount, result.SheetCount, result.Warning)
		return err
	})
	if err != nil {
		for _, item := range stored {
			_ = w.objects.Remove(context.Background(), item.key)
		}
	}
	return err
}

func (w *Worker) setDocumentStatus(ctx context.Context, classID, blobID int64, status, warning, errorMessage string) error {
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE knowledge_blob SET status=$2,error=NULLIF($3,''),updated_at=now() WHERE id=$1`, blobID, status, errorMessage); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE knowledge_document SET status=$2,warning=NULLIF($3,''),error=NULLIF($4,''),updated_at=now() WHERE blob_id=$1 AND deleted_at IS NULL`, blobID, status, warning, errorMessage)
		return err
	})
}

func (w *Worker) failDocument(ctx context.Context, classID, blobID int64, message string) error {
	return w.setDocumentStatus(ctx, classID, blobID, "failed", "", message)
}

func safeConversionError(err error) string {
	message := strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(err.Error(), "�"), "\x00", ""))
	if len([]rune(message)) > 300 {
		return string([]rune(message)[:300])
	}
	return message
}

// A canceled conversion still needs a visible terminal status. Keep this
// independent of the work deadline, bounded, and propagate a failed write so
// the event cannot be acknowledged with its document stuck in processing.
func persistProcessingFailure(ctx context.Context, message string, save func(context.Context, string) error) error {
	failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := save(failureCtx, message); err != nil {
		return fmt.Errorf("record knowledge conversion failure: %w", err)
	}
	return nil
}

func lockKey(namespace string, id int64) int64 {
	var idBytes [8]byte
	binary.BigEndian.PutUint64(idBytes[:], uint64(id))
	sum := sha256.Sum256(append([]byte(namespace+"\x00"), idBytes[:]...))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}
