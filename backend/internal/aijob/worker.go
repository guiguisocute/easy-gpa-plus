// Package aijob runs the asynchronous, idempotent material recognition job.
// Each image/PDF page is its own database checkpoint; batch composition starts
// only after those checkpoints have reached a terminal state.
package aijob

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"easygpa/backend/internal/aiassist"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/safeexec"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/store"
)

const (
	modelAttempts         = 3
	maxRenderedImageBytes = 25 * 1024 * 1024
)

var (
	errBatchCanceled       = errors.New("AI batch canceled")
	errTransientPerception = errors.New("AI perception remained unavailable after retries")
)

type ObjectReader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

type Config struct {
	Concurrency             int
	MaxItems                int
	MaxPDFPages             int
	NativeToolsEnabled      bool
	ConverterTimeoutSeconds int
	RuntimeLimits           func(context.Context) (Limits, error)
}

type Limits struct {
	Concurrency             int
	MaxItems                int
	MaxPDFPages             int
	NativeToolsEnabled      bool
	ConverterTimeoutSeconds int
}

type Worker struct {
	pool    *pgxpool.Pool
	objects ObjectReader
	client  llm.Client
	cfg     Config
	wait    func(context.Context, time.Duration) error
}

func NewWorker(pool *pgxpool.Pool, objects ObjectReader, client llm.Client, cfg Config) (*Worker, error) {
	if pool == nil || objects == nil || client == nil {
		return nil, errors.New("AI worker requires database, object storage and model client")
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 8 {
		return nil, errors.New("AI worker concurrency must be between 1 and 8")
	}
	if cfg.MaxItems < 1 || cfg.MaxItems > 100 {
		return nil, errors.New("AI worker item limit must be between 1 and 100")
	}
	if cfg.MaxPDFPages < 1 || cfg.MaxPDFPages > 64 {
		return nil, errors.New("AI worker PDF page limit must be between 1 and 64")
	}
	if cfg.ConverterTimeoutSeconds <= 0 {
		cfg.ConverterTimeoutSeconds = 60
	}
	if cfg.ConverterTimeoutSeconds < 5 || cfg.ConverterTimeoutSeconds > 300 {
		return nil, errors.New("AI worker converter timeout must be between 5 and 300 seconds")
	}
	return &Worker{pool: pool, objects: objects, client: client, cfg: cfg, wait: waitContext}, nil
}

func (w *Worker) limits(ctx context.Context) (Limits, error) {
	limits := Limits{
		Concurrency: w.cfg.Concurrency, MaxItems: w.cfg.MaxItems, MaxPDFPages: w.cfg.MaxPDFPages,
		NativeToolsEnabled: w.cfg.NativeToolsEnabled, ConverterTimeoutSeconds: w.cfg.ConverterTimeoutSeconds,
	}
	if w.cfg.RuntimeLimits != nil {
		resolved, err := w.cfg.RuntimeLimits(ctx)
		if err != nil {
			return Limits{}, err
		}
		limits = resolved
	}
	if limits.ConverterTimeoutSeconds <= 0 {
		limits.ConverterTimeoutSeconds = 60
	}
	if limits.Concurrency < 1 || limits.Concurrency > 8 || limits.MaxItems < 1 || limits.MaxItems > 100 || limits.MaxPDFPages < 1 || limits.MaxPDFPages > 64 || limits.ConverterTimeoutSeconds < 5 || limits.ConverterTimeoutSeconds > 300 {
		return Limits{}, errors.New("AI material runtime limits are invalid")
	}
	return limits, nil
}

type batchRecord struct {
	ID             int64
	Status         string
	SchemeSnapshot []byte
	VisionModel    string
	TextModel      string
	VisionRoute    []byte
	TextRoute      []byte
	StudentName    string
	StudentSID     string
}

func routeOrNil(route llm.Route) *llm.Route {
	if strings.TrimSpace(route.BaseURL) == "" || strings.TrimSpace(route.Model) == "" {
		return nil
	}
	return &route
}

type assetRecord struct {
	ID        int64
	ObjectKey string
	Filename  string
	MediaType string
	SizeBytes int64
	Status    string
	PageCount *int
}

type source struct {
	asset            assetRecord
	pdfPath          string
	pages            int
	converterTimeout time.Duration
}

func (w *Worker) Handle(ctx context.Context, event events.Event) error {
	if event.Type != events.AIBatchCreated {
		return nil
	}
	payload, err := events.Decode(event, events.AIBatchCreatedEvent())
	if err != nil {
		return errors.New("ai.batch.created payload is invalid")
	}
	batchID, err := strconv.ParseInt(payload.BatchID, 10, 64)
	if err != nil || batchID <= 0 {
		return errors.New("ai.batch.created payload is incomplete")
	}
	return w.withBatchLock(ctx, batchID, func() error {
		return w.process(ctx, event.ClassID, batchID)
	})
}

func (w *Worker) withBatchLock(ctx context.Context, batchID int64, fn func() error) error {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	key := batchLockKey(batchID)
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

func (w *Worker) process(ctx context.Context, classID, batchID int64) error {
	limits, err := w.limits(ctx)
	if err != nil {
		return err
	}
	var batch batchRecord
	var assets []assetRecord
	err = store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT b.id,b.status,b.scheme_snapshot,b.vision_model,b.text_model,b.vision_route,b.text_route,u.name,u.sid
			  FROM ai_batch b JOIN app_user u ON u.id=b.student_id
			 WHERE b.id=$1 FOR UPDATE OF b
		`, batchID).Scan(&batch.ID, &batch.Status, &batch.SchemeSnapshot, &batch.VisionModel, &batch.TextModel,
			&batch.VisionRoute, &batch.TextRoute,
			&batch.StudentName, &batch.StudentSID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		switch batch.Status {
		case "review", "complete", "canceled", "expired":
			return nil
		case "queued", "processing", "failed":
		default:
			return fmt.Errorf("AI batch has unexpected status %q", batch.Status)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ai_batch SET status='processing',started_at=COALESCE(started_at,now()),error=NULL,updated_at=now() WHERE id=$1
		`, batchID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id,object_key,filename,media_type,size_bytes,status,page_count
			  FROM ai_asset WHERE batch_id=$1 AND status IN ('ready','processing','complete','failed') ORDER BY id
		`, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var asset assetRecord
			if err := rows.Scan(&asset.ID, &asset.ObjectKey, &asset.Filename, &asset.MediaType, &asset.SizeBytes, &asset.Status, &asset.PageCount); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	if err != nil || batch.ID == 0 || batch.Status == "review" || batch.Status == "complete" || batch.Status == "canceled" || batch.Status == "expired" {
		return err
	}
	if len(assets) == 0 {
		return w.failBatch(ctx, classID, batchID, "没有可处理的材料")
	}
	var schemeConfig scheme.Config
	if err := json.Unmarshal(batch.SchemeSnapshot, &schemeConfig); err != nil {
		_ = w.failBatch(ctx, classID, batchID, "方案快照无法读取")
		return errors.New("AI batch scheme snapshot is invalid")
	}
	var visionRoute, textRoute llm.Route
	_ = json.Unmarshal(batch.VisionRoute, &visionRoute)
	_ = json.Unmarshal(batch.TextRoute, &textRoute)
	pipeline := aiassist.New(aiassist.Config{
		Enabled: true, VisionModel: batch.VisionModel, TextModel: batch.TextModel,
		VisionRoute: routeOrNil(visionRoute), TextRoute: routeOrNil(textRoute),
		// 名单、合影、集体表彰上全是人名，不告诉模型该找谁，这类材料只能一律按
		// "不确定"处理，最后就都被漏掉了。
		StudentName: batch.StudentName, StudentSID: batch.StudentSID,
	}, w.client)

	tempDir, err := os.MkdirTemp("", "easygpa-ai-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	sources, total, err := w.prepareSources(ctx, classID, batchID, assets, tempDir, limits)
	if errors.Is(err, errBatchCanceled) {
		return nil
	}
	if err != nil {
		return err
	}
	if total > limits.MaxItems {
		return w.failBatch(ctx, classID, batchID, fmt.Sprintf("材料共有 %d 张或页，超过本批上限 %d，请拆分后重试", total, limits.MaxItems))
	}
	if err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ai_batch SET total_count=$2,updated_at=now() WHERE id=$1 AND status='processing'`, batchID, total)
		return err
	}); err != nil {
		return err
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(limits.Concurrency)
	var hadTransientFailure atomic.Bool
	for _, item := range sources {
		if groupCtx.Err() != nil {
			break
		}
		item := item
		group.Go(func() error {
			err := w.processSource(groupCtx, classID, batchID, pipeline, item)
			switch {
			case err == nil:
				return nil
			case errors.Is(err, errTransientPerception):
				hadTransientFailure.Store(true)
				return nil
			default:
				return err
			}
		})
	}
	if err := group.Wait(); err != nil {
		if errors.Is(err, errBatchCanceled) {
			return nil
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	canceled, err := w.batchCanceled(ctx, classID, batchID)
	if err != nil {
		return err
	}
	if canceled {
		return nil
	}

	observations, failed, err := w.loadObservations(ctx, classID, batchID)
	if err != nil {
		return err
	}
	if len(observations) == 0 {
		if err := w.failBatch(ctx, classID, batchID, "所有材料都未能识别，请改用手动填写"); err != nil {
			return err
		}
		if hadTransientFailure.Load() {
			return errTransientPerception
		}
		return nil
	}
	outcome, err := w.composeInChunks(ctx, classID, batchID, pipeline, observations, schemeConfig)
	if errors.Is(err, errBatchCanceled) {
		return nil
	}
	if err != nil {
		return err
	}
	draft := outcome.draft
	if len(draft.Candidates) == 0 {
		/* 这条日志是归组失败唯一的线索。原来非 transient 的分支直接 return nil，
		   于是学生只看到一句"批次归组失败"，worker 日志里一个字都没有——超时、
		   模型 400、JSON 修不回来，三种完全不同的故障长得一模一样，没法查。 */
		slog.Error("AI batch composition failed", "class_id", classID, "batch_id", batchID,
			"observations", len(observations), "failed_items", failed,
			"chunks", outcome.chunks, "failed_chunks", outcome.failedChunks,
			"transient", transient(outcome.lastErr), "error", outcome.lastErr)
		if failErr := w.failBatch(ctx, classID, batchID, "批次归组失败，请稍后重试或改用手动填写"); failErr != nil {
			return failErr
		}
		if transient(outcome.lastErr) {
			return fmt.Errorf("AI batch composition remained unavailable after retries: %w", outcome.lastErr)
		}
		return nil
	}
	if outcome.failedObservations > 0 {
		draft.Warnings = append(draft.Warnings, aiassist.MissingChunkWarning(outcome.failedObservations))
	}
	if outcome.stopped {
		draft.Warnings = append(draft.Warnings, "归组被你手动停止，下面是停止前已经归好的候选；其余材料可以重跑，或手动补。")
	}
	if failed > 0 {
		draft.Warnings = append(draft.Warnings, fmt.Sprintf("有 %d 张图片或 PDF 页识别失败，候选仅依据其余材料生成", failed))
	}
	result, _ := json.Marshal(draft)
	usage := draft.Usage
	for _, observation := range observations {
		usage.InputTokens += observation.Usage.InputTokens
		usage.OutputTokens += observation.Usage.OutputTokens
		usage.TotalTokens += observation.Usage.TotalTokens
	}
	usageJSON, _ := json.Marshal(usage)
	raw := nullableJSON(draft.RawResponse)
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE ai_batch
			   SET status='review',result=$2,raw_response=$3,usage=$4,error=NULL,
			       request_hash=NULLIF($5,''),repair_request_hash=NULLIF($6,''),
			       processed_count=(SELECT count(*) FROM ai_item WHERE batch_id=$1 AND status IN ('complete','failed')),
			       completed_at=now(),updated_at=now()
			 WHERE id=$1 AND status='processing'
		`, batchID, result, raw, usageJSON, draft.RequestHash, draft.RepairRequestHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,after_data,metadata)
			VALUES ($1,NULL,'system','ai.batch_ready','ai_batch',$2::bigint::text,
			        jsonb_build_object('status','review','candidates',$3::int),
			        jsonb_build_object('failedItems',$4::int,'promptVersion',$5::text))
		`, classID, batchID, len(draft.Candidates), failed, aiassist.PromptVersion)
		return err
	})
}

type composeOutcome struct {
	draft              aiassist.BatchDraft
	chunks             int
	failedChunks       int
	failedObservations int
	stopped            bool
	lastErr            error
}

type composeChunkRow struct {
	seq          int
	status       string
	observations []aiassist.AssetRef
	candidates   []aiassist.Candidate
	warnings     []string
	usage        llm.Usage
	model        string
	durationMS   int64
	attempts     int
}

/*
composeInChunks 把归组拆成若干块逐块跑，每块成功立刻落库。

	原来这里是一次 ComposeBatch 调用，全程在内存里攒结果、成功才写一次 ai_batch.result。
	于是 worker 被重新部署掐掉、上游断流、模型返回空——任何一种都会把前面十几分钟的
	识图连同已经归好的候选一起作废，界面上表现为进度条停在 100%、十几分钟后一句
	"归组失败"。逐图识别从来没有这个毛病，因为每张图都是一行 ai_item 的检查点。

	重试也跟着下沉到块：以前 retryCall 包着整个 ComposeBatch，只有"一条候选都没出来"
	才会重跑，而且是把已经成功的块一起重跑。
*/
func (w *Worker) composeInChunks(
	ctx context.Context, classID, batchID int64, pipeline aiassist.Pipeline,
	observations []aiassist.Observation, schemeConfig scheme.Config,
) (composeOutcome, error) {
	chunks := aiassist.PlanChunks(observations)
	rows, err := w.planComposeChunks(ctx, classID, batchID, chunks)
	if err != nil {
		return composeOutcome{}, err
	}
	outcome := composeOutcome{chunks: len(chunks)}
	lastWrite := time.Time{}
	for index, chunk := range chunks {
		row := rows[index]
		// 已经归好的块原样带走，不重跑也不重新计费。
		if row.status == "complete" {
			aiassist.MergeDraft(&outcome.draft, storedChunkDraft(row))
			continue
		}
		canceled, err := w.batchCanceled(ctx, classID, batchID)
		if err != nil {
			return composeOutcome{}, err
		}
		if canceled {
			return composeOutcome{}, errBatchCanceled
		}
		stop, err := w.composeStopRequested(ctx, classID, batchID)
		if err != nil {
			return composeOutcome{}, err
		}
		if stop {
			// 停止只影响还没跑的块，已经归好的照常交给学生。
			outcome.stopped = true
			break
		}
		if err := w.markComposeChunk(ctx, classID, batchID, row.seq, "processing", nil); err != nil {
			return composeOutcome{}, err
		}

		/* 中间态写库节流到 1 秒一次：前端本来就 1.5 秒一轮询，写得再密也没人看得见，
		   只是白白多出一串事务。候选和思考共用这个闸门。 */
		before := aiassist.CandidateLines(outcome.draft.Candidates)
		progress := aiassist.ComposeProgress{
			OnCandidates: func(lines []string) {
				if time.Since(lastWrite) < time.Second {
					return
				}
				lastWrite = time.Now()
				w.writeComposePreview(ctx, classID, batchID, append(append([]string{}, before...), lines...))
			},
			OnThinking: func(text string) {
				if time.Since(lastWrite) < time.Second {
					return
				}
				lastWrite = time.Now()
				w.writeComposeThinking(ctx, classID, batchID, text)
			},
		}
		draft, callErr := retryCall(ctx, w.wait, func() (aiassist.BatchDraft, error) {
			return pipeline.ComposeChunk(ctx, chunk, schemeConfig, progress)
		})
		attempts := row.attempts + 1
		if callErr != nil {
			outcome.lastErr = callErr
			outcome.failedChunks++
			outcome.failedObservations += len(chunk)
			slog.Warn("AI compose chunk failed", "class_id", classID, "batch_id", batchID,
				"seq", row.seq, "observations", len(chunk), "attempts", attempts,
				"transient", transient(callErr), "error", callErr)
			if err := w.finishComposeChunk(ctx, classID, batchID, row.seq, aiassist.BatchDraft{}, attempts, callErr); err != nil {
				return composeOutcome{}, err
			}
			continue
		}
		if err := w.finishComposeChunk(ctx, classID, batchID, row.seq, draft, attempts, nil); err != nil {
			return composeOutcome{}, err
		}
		aiassist.MergeDraft(&outcome.draft, draft)
	}
	return outcome, nil
}

// planComposeChunks 把分块方案落库，并回报每块的当前状态。
//
// 方案会变：学生重跑识别失败的材料后观察集合变了，重新分块的结果和上一轮对不上。
// 所以逐块比对输入，只有输入一模一样的块才保留它的结果，其余一律回到 queued。
func (w *Worker) planComposeChunks(ctx context.Context, classID, batchID int64, chunks [][]aiassist.Observation) ([]composeChunkRow, error) {
	existing := make(map[int]composeChunkRow, len(chunks))
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT seq,status,observations,candidates,warnings,usage,COALESCE(model,''),duration_ms,attempts
			  FROM ai_compose_chunk WHERE batch_id=$1 ORDER BY seq
		`, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row composeChunkRow
			var observations, candidates, warnings, usage []byte
			if err := rows.Scan(&row.seq, &row.status, &observations, &candidates, &warnings, &usage,
				&row.model, &row.durationMS, &row.attempts); err != nil {
				return err
			}
			_ = json.Unmarshal(observations, &row.observations)
			_ = json.Unmarshal(candidates, &row.candidates)
			_ = json.Unmarshal(warnings, &row.warnings)
			_ = json.Unmarshal(usage, &row.usage)
			existing[row.seq] = row
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	planned := make([]composeChunkRow, len(chunks))
	for index, chunk := range chunks {
		refs := observationRefs(chunk)
		if row, ok := existing[index]; ok && sameRefs(row.observations, refs) {
			planned[index] = row
			continue
		}
		planned[index] = composeChunkRow{seq: index, status: "queued", observations: refs}
	}
	if err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		for _, row := range planned {
			if existingRow, ok := existing[row.seq]; ok && sameRefs(existingRow.observations, row.observations) {
				continue
			}
			refs, _ := json.Marshal(row.observations)
			if _, err := tx.Exec(ctx, `
				INSERT INTO ai_compose_chunk (class_id,batch_id,seq,status,observations)
				VALUES ($1,$2,$3,'queued',$4)
				ON CONFLICT (batch_id,seq) DO UPDATE
				   SET status='queued',observations=EXCLUDED.observations,candidates='[]'::jsonb,
				       warnings='[]'::jsonb,raw_response=NULL,usage='{}'::jsonb,model=NULL,
				       duration_ms=0,attempts=0,error=NULL,updated_at=now()
			`, classID, batchID, row.seq, refs); err != nil {
				return err
			}
		}
		// 观察变少时多出来的旧块必须清掉，否则它们的候选会跟着进最终结果。
		_, err := tx.Exec(ctx, `DELETE FROM ai_compose_chunk WHERE batch_id=$1 AND seq>=$2`, batchID, len(chunks))
		return err
	}); err != nil {
		return nil, err
	}
	return planned, nil
}

func (w *Worker) markComposeChunk(ctx context.Context, classID, batchID int64, seq int, status string, chunkErr error) error {
	message := ""
	if chunkErr != nil {
		message = chunkErr.Error()
	}
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE ai_compose_chunk SET status=$3,error=NULLIF($4,''),updated_at=now()
			 WHERE batch_id=$1 AND seq=$2
		`, batchID, seq, status, message)
		return err
	})
}

// finishComposeChunk 是这次改动的关键一步：一块归完立刻落盘。后面无论发生什么
// ——超时、被 kill、学生点停止——这块的候选都已经在库里了。
func (w *Worker) finishComposeChunk(
	ctx context.Context, classID, batchID int64, seq int,
	draft aiassist.BatchDraft, attempts int, chunkErr error,
) error {
	status := "complete"
	message := ""
	if chunkErr != nil {
		status = "failed"
		message = chunkErr.Error()
	}
	candidates, _ := json.Marshal(orEmpty(draft.Candidates))
	warnings, _ := json.Marshal(orEmpty(draft.Warnings))
	usage, _ := json.Marshal(draft.Usage)
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE ai_compose_chunk
			   SET status=$3,candidates=$4,warnings=$5,usage=$6,raw_response=$7,
			       model=NULLIF($8,''),duration_ms=$9,attempts=$10,error=NULLIF($11,''),updated_at=now()
			 WHERE batch_id=$1 AND seq=$2
		`, batchID, seq, status, candidates, warnings, usage, nullableJSON(draft.RawResponse),
			draft.Model, draft.DurationMS, attempts, message)
		return err
	})
}

func (w *Worker) composeStopRequested(ctx context.Context, classID, batchID int64) (bool, error) {
	stop := false
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		queryErr := tx.QueryRow(ctx, `SELECT compose_stop_requested FROM ai_batch WHERE id=$1`, batchID).Scan(&stop)
		if errors.Is(queryErr, pgx.ErrNoRows) {
			return nil
		}
		return queryErr
	})
	return stop, err
}

func storedChunkDraft(row composeChunkRow) aiassist.BatchDraft {
	return aiassist.BatchDraft{
		Candidates: row.candidates, Warnings: row.warnings, Model: row.model,
		Usage: row.usage, DurationMS: row.durationMS,
	}
}

func observationRefs(observations []aiassist.Observation) []aiassist.AssetRef {
	refs := make([]aiassist.AssetRef, 0, len(observations))
	for _, observation := range observations {
		refs = append(refs, aiassist.AssetRef{AssetID: observation.AssetID, Page: observation.Page})
	}
	return refs
}

func sameRefs(stored, planned []aiassist.AssetRef) bool {
	return slices.Equal(stored, planned)
}

// orEmpty 让 nil 切片序列化成 []，而不是 null——库里那两列是 NOT NULL jsonb。
func orEmpty[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func (w *Worker) prepareSources(ctx context.Context, classID, batchID int64, assets []assetRecord, tempDir string, limits Limits) ([]source, int, error) {
	result := make([]source, 0, len(assets))
	total := 0
	for _, asset := range assets {
		canceled, err := w.batchCanceled(ctx, classID, batchID)
		if err != nil {
			return nil, 0, err
		}
		if canceled {
			return nil, 0, errBatchCanceled
		}
		if asset.MediaType != "application/pdf" {
			result = append(result, source{asset: asset, pages: 1})
			total++
			continue
		}
		if !limits.NativeToolsEnabled {
			_ = w.recordSyntheticFailure(ctx, classID, batchID, asset, "本地原生转换工具已由运维关闭，请改用图片或手动填写")
			total++
			continue
		}
		pdfPath := filepath.Join(tempDir, strconv.FormatInt(asset.ID, 10)+".pdf")
		if err := w.download(ctx, asset, pdfPath); err != nil {
			_ = w.recordSyntheticFailure(ctx, classID, batchID, asset, "PDF 下载失败")
			total++
			continue
		}
		pages, err := inspectPDF(ctx, pdfPath, time.Duration(limits.ConverterTimeoutSeconds)*time.Second)
		if err != nil {
			_ = w.recordSyntheticFailure(ctx, classID, batchID, asset, err.Error())
			total++
			continue
		}
		if pages > limits.MaxPDFPages {
			_ = w.recordSyntheticFailure(ctx, classID, batchID, asset, fmt.Sprintf("PDF 共 %d 页，超过单文件上限 %d 页", pages, limits.MaxPDFPages))
			total++
			continue
		}
		if err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE ai_asset SET page_count=$2,status='processing',error=NULL,updated_at=now() WHERE id=$1`, asset.ID, pages)
			return err
		}); err != nil {
			return nil, 0, err
		}
		asset.PageCount = &pages
		result = append(result, source{asset: asset, pdfPath: pdfPath, pages: pages, converterTimeout: time.Duration(limits.ConverterTimeoutSeconds) * time.Second})
		total += pages
	}
	return result, total, nil
}

func (w *Worker) processSource(ctx context.Context, classID, batchID int64, pipeline aiassist.Pipeline, item source) error {
	canceled, err := w.batchCanceled(ctx, classID, batchID)
	if err != nil {
		return err
	}
	if canceled {
		return errBatchCanceled
	}
	completed, failed := 0, 0
	hadTransientFailure := false
	if item.asset.MediaType == "application/pdf" {
		for page := 1; page <= item.pages; page++ {
			canceled, err := w.batchCanceled(ctx, classID, batchID)
			if err != nil {
				return err
			}
			if canceled {
				return errBatchCanceled
			}
			data, err := renderPDFPage(ctx, item.pdfPath, page, item.converterTimeout)
			if err != nil {
				_ = w.markItemFailed(ctx, classID, batchID, item.asset.ID, page, "PDF 页面渲染失败")
				failed++
				continue
			}
			if _, err := w.processItem(ctx, classID, batchID, pipeline, item.asset, page, "image/jpeg", data); err != nil {
				if errors.Is(err, errBatchCanceled) {
					return err
				}
				hadTransientFailure = hadTransientFailure || transient(err)
				failed++
			} else {
				completed++
			}
		}
	} else {
		data, err := w.readObject(ctx, item.asset)
		if err != nil {
			_ = w.markItemFailed(ctx, classID, batchID, item.asset.ID, 0, "图片读取失败")
			failed++
		} else if _, err := w.processItem(ctx, classID, batchID, pipeline, item.asset, 0, item.asset.MediaType, data); err != nil {
			if errors.Is(err, errBatchCanceled) {
				return err
			}
			hadTransientFailure = transient(err)
			failed++
		} else {
			completed++
		}
	}
	status := "complete"
	var message *string
	if completed == 0 {
		status = "failed"
		value := "材料未能识别"
		message = &value
	} else if failed > 0 {
		value := fmt.Sprintf("%d 页识别失败", failed)
		message = &value
	}
	err = store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE ai_asset AS asset
			   SET status=$2,error=$3,updated_at=now()
			  FROM ai_batch AS batch
			 WHERE asset.id=$1 AND batch.id=$4 AND batch.id=asset.batch_id
			   AND batch.status='processing' AND asset.status NOT IN ('applied','rejected')
		`, item.asset.ID, status, message, batchID)
		return err
	})
	if err != nil {
		return err
	}
	if hadTransientFailure {
		return errTransientPerception
	}
	return nil
}

func (w *Worker) processItem(ctx context.Context, classID, batchID int64, pipeline aiassist.Pipeline, asset assetRecord, page int, mediaType string, data []byte) (aiassist.Observation, error) {
	if observation, ok, err := w.completedItem(ctx, classID, asset.ID, page); err != nil || ok {
		return observation, err
	}
	if err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO ai_item (class_id,batch_id,asset_id,page_no,status,attempts)
			SELECT $1,$2,$3,$4,'processing',1
			  FROM ai_batch WHERE id=$2 AND status='processing'
			ON CONFLICT (asset_id,page_no) DO UPDATE
			   SET status='processing',attempts=ai_item.attempts+1,error=NULL,updated_at=now()
			 WHERE EXISTS (SELECT 1 FROM ai_batch WHERE id=$2 AND status='processing')
		`, classID, batchID, asset.ID, page)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errBatchCanceled
		}
		return nil
	}); err != nil {
		return aiassist.Observation{}, err
	}
	observation, err := retryCall(ctx, w.wait, func() (aiassist.Observation, error) {
		return pipeline.Perceive(ctx, aiassist.Asset{ID: strconv.FormatInt(asset.ID, 10), Page: page, MediaType: mediaType, Data: data})
	})
	if err != nil {
		_ = w.markStartedItemFailed(ctx, classID, batchID, asset.ID, page, safeError(err))
		return aiassist.Observation{}, err
	}
	perception, _ := json.Marshal(observation)
	usage, _ := json.Marshal(observation.Usage)
	raw := nullableJSON(observation.RawResponse)
	err = store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE ai_item AS item
			   SET status='complete',perception=$2,raw_response=$3,model=$4,usage=$5,duration_ms=$6,
			       request_hash=NULLIF($8,''),repair_request_hash=NULLIF($9,''),error=NULL,updated_at=now()
			  FROM ai_batch AS batch
			 WHERE item.asset_id=$1 AND item.page_no=$7 AND batch.id=$10 AND batch.id=item.batch_id
			   AND batch.status='processing'
		`, asset.ID, perception, raw, observation.Model, usage, observation.DurationMS, page,
			observation.RequestHash, observation.RepairRequestHash, batchID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errBatchCanceled
		}
		return w.refreshProgress(ctx, tx, batchID)
	})
	return observation, err
}

func (w *Worker) completedItem(ctx context.Context, classID, assetID int64, page int) (aiassist.Observation, bool, error) {
	var observation aiassist.Observation
	var perception, usage, raw []byte
	var model, requestHash, repairRequestHash string
	var duration int64
	found := false
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT perception,usage,COALESCE(raw_response,'null'::jsonb),COALESCE(model,''),duration_ms,
			       COALESCE(request_hash,''),COALESCE(repair_request_hash,'')
			  FROM ai_item WHERE asset_id=$1 AND page_no=$2 AND status='complete'
		`, assetID, page).Scan(&perception, &usage, &raw, &model, &duration, &requestHash, &repairRequestHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return observation, found, err
	}
	if err := json.Unmarshal(perception, &observation); err != nil {
		return aiassist.Observation{}, false, err
	}
	_ = json.Unmarshal(usage, &observation.Usage)
	observation.Model = model
	observation.DurationMS = duration
	observation.RequestHash = requestHash
	observation.RepairRequestHash = repairRequestHash
	if string(raw) != "null" {
		observation.RawResponse = raw
	}
	return observation, true, nil
}

func (w *Worker) markItemFailed(ctx context.Context, classID, batchID, assetID int64, page int, message string) error {
	if len(message) > 1000 {
		message = message[:1000]
	}
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ai_item (class_id,batch_id,asset_id,page_no,status,attempts,error)
			VALUES ($1,$2,$3,$4,'failed',1,$5)
			ON CONFLICT (asset_id,page_no) DO UPDATE
			   SET status='failed',attempts=ai_item.attempts+1,error=$5,updated_at=now()
		`, classID, batchID, assetID, page, message)
		if err != nil {
			return err
		}
		return w.refreshProgress(ctx, tx, batchID)
	})
}

// processItem has already counted this delivery when it moved the row to
// processing. Finishing that same attempt must not increment attempts again.
func (w *Worker) markStartedItemFailed(ctx context.Context, classID, batchID, assetID int64, page int, message string) error {
	if len(message) > 1000 {
		message = message[:1000]
	}
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE ai_item SET status='failed',error=$3,updated_at=now()
			 WHERE asset_id=$1 AND page_no=$2
		`, assetID, page, message); err != nil {
			return err
		}
		return w.refreshProgress(ctx, tx, batchID)
	})
}

func (w *Worker) recordSyntheticFailure(ctx context.Context, classID, batchID int64, asset assetRecord, message string) error {
	if err := w.markItemFailed(ctx, classID, batchID, asset.ID, 0, message); err != nil {
		return err
	}
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ai_asset SET status='failed',error=$2,updated_at=now() WHERE id=$1`, asset.ID, message)
		return err
	})
}

func (w *Worker) refreshProgress(ctx context.Context, tx pgx.Tx, batchID int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE ai_batch SET processed_count=(SELECT count(*) FROM ai_item WHERE batch_id=$1 AND status IN ('complete','failed')),updated_at=now()
		 WHERE id=$1
	`, batchID)
	return err
}

func (w *Worker) loadObservations(ctx context.Context, classID, batchID int64) ([]aiassist.Observation, int, error) {
	result := make([]aiassist.Observation, 0)
	failed := 0
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT status,perception,usage,COALESCE(raw_response,'null'::jsonb),COALESCE(model,''),duration_ms,
			       COALESCE(request_hash,''),COALESCE(repair_request_hash,'')
			  FROM ai_item WHERE batch_id=$1 ORDER BY asset_id,page_no
		`, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var status, model, requestHash, repairRequestHash string
			var perception, usage, raw []byte
			var duration int64
			if err := rows.Scan(&status, &perception, &usage, &raw, &model, &duration, &requestHash, &repairRequestHash); err != nil {
				return err
			}
			if status != "complete" {
				failed++
				continue
			}
			var observation aiassist.Observation
			if err := json.Unmarshal(perception, &observation); err != nil {
				failed++
				continue
			}
			_ = json.Unmarshal(usage, &observation.Usage)
			observation.Model = model
			observation.DurationMS = duration
			observation.RequestHash = requestHash
			observation.RepairRequestHash = repairRequestHash
			if string(raw) != "null" {
				observation.RawResponse = raw
			}
			result = append(result, observation)
		}
		return rows.Err()
	})
	return result, failed, err
}

func (w *Worker) download(ctx context.Context, asset assetRecord, destination string) error {
	reader, err := w.objects.Open(ctx, asset.ObjectKey)
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, asset.SizeBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != asset.SizeBytes {
		return errors.New("object size changed after upload")
	}
	return nil
}

func (w *Worker) readObject(ctx context.Context, asset assetRecord) ([]byte, error) {
	reader, err := w.objects.Open(ctx, asset.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, asset.SizeBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != asset.SizeBytes {
		return nil, errors.New("object size changed after upload")
	}
	return data, nil
}

func inspectPDF(ctx context.Context, filename string, timeout time.Duration) (int, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := safeexec.Run(commandCtx, "pdfinfo", []string{filename}, safeexec.Options{MaxOutput: 64 * 1024})
	if err != nil {
		return 0, errors.New("PDF 已加密、损坏或无法读取，请改用图片或手动填写")
	}
	pages := 0
	encrypted := false
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "pages":
			pages, _ = strconv.Atoi(strings.TrimSpace(value))
		case "encrypted":
			encrypted = strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "yes")
		}
	}
	if encrypted {
		return 0, errors.New("加密 PDF 暂不支持，请解密后重试或手动填写")
	}
	if pages <= 0 {
		return 0, errors.New("PDF 没有可识别的页面")
	}
	return pages, nil
}

func renderPDFPage(ctx context.Context, filename string, page int, timeout time.Duration) ([]byte, error) {
	dir := filepath.Dir(filename)
	prefix := filepath.Join(dir, fmt.Sprintf("page-%d-%d", page, time.Now().UnixNano()))
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-jpeg", "-r", "160", filename, prefix}
	if output, err := safeexec.Run(commandCtx, "pdftoppm", args, safeexec.Options{MaxOutput: 64 * 1024}); err != nil {
		return nil, fmt.Errorf("pdftoppm: %s", strings.TrimSpace(string(output)))
	}
	outputFile := prefix + ".jpg"
	defer os.Remove(outputFile)
	info, err := os.Stat(outputFile)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxRenderedImageBytes {
		return nil, fmt.Errorf("rendered PDF page exceeds %d MB", maxRenderedImageBytes/(1024*1024))
	}
	return os.ReadFile(outputFile)
}

func (w *Worker) batchCanceled(ctx context.Context, classID, batchID int64) (bool, error) {
	canceled := false
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM ai_batch WHERE id=$1`, batchID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				canceled = true
				return nil
			}
			return err
		}
		canceled = status == "canceled" || status == "expired" || status == "complete"
		return nil
	})
	return canceled, err
}

// 归组进度只是给人看的，写不进去不影响结果，所以出错只记不返回——为了一行预览把
// 整批搞失败是本末倒置。status 卡在 processing 上：批次一旦结束就不该再被刷新。
func (w *Worker) writeComposePreview(ctx context.Context, classID, batchID int64, titles []string) {
	payload, err := json.Marshal(titles)
	if err != nil {
		return
	}
	if err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `UPDATE ai_batch SET compose_preview=$2,updated_at=now() WHERE id=$1 AND status='processing'`, batchID, string(payload))
		return execErr
	}); err != nil {
		slog.Warn("ai compose preview write failed", "batch_id", batchID, "error", err)
	}
}

// 思考比正文早得多（实测 11 秒对 64 秒），等待期的头一分钟全靠它。和预览一样，
// 写不进去只是少点东西看。
func (w *Worker) writeComposeThinking(ctx context.Context, classID, batchID int64, text string) {
	if err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `UPDATE ai_batch SET compose_thinking=$2,updated_at=now() WHERE id=$1 AND status='processing'`, batchID, text)
		return execErr
	}); err != nil {
		slog.Warn("ai compose thinking write failed", "batch_id", batchID, "error", err)
	}
}

func (w *Worker) failBatch(ctx context.Context, classID, batchID int64, message string) error {
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE ai_batch SET status='failed',error=$2,
			       processed_count=(SELECT count(*) FROM ai_item WHERE batch_id=$1 AND status IN ('complete','failed')),
			       completed_at=now(),updated_at=now()
			 WHERE id=$1 AND status NOT IN ('canceled','complete','expired')
		`, batchID, message)
		return err
	})
}

func retryCall[T any](ctx context.Context, wait func(context.Context, time.Duration) error, call func() (T, error)) (T, error) {
	var zero T
	var last error
	for attempt := 0; attempt < modelAttempts; attempt++ {
		value, err := call()
		if err == nil {
			return value, nil
		}
		last = err
		if !transient(err) || attempt == modelAttempts-1 {
			break
		}
		if err := wait(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
			return zero, err
		}
	}
	return zero, last
}

func transient(err error) bool {
	if llm.IsRetryable(err) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	return []byte(raw)
}

func safeError(err error) string {
	var callErr *llm.CallError
	if errors.As(err, &callErr) {
		if callErr.StatusCode > 0 {
			return fmt.Sprintf("模型服务返回 HTTP %d", callErr.StatusCode)
		}
		return "模型服务调用失败"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "模型服务调用超时"
	}
	return "材料识别失败"
}

func batchLockKey(batchID int64) int64 {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(batchID))
	sum := sha256.Sum256(append([]byte("easygpa.ai.batch\x00"), raw[:]...))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}
