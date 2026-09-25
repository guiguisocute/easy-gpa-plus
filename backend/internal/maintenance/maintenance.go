// Package maintenance runs durable, tenant-aware scheduled housekeeping.
package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/classtimeline"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/objectstore"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/store"
)

const interval = 5 * time.Minute

var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

type Runner struct {
	app     *pgxpool.Pool
	ops     *pgxpool.Pool
	redis   *redis.Client
	objects *objectstore.Client
	config  *opsconfig.Store
	now     func() time.Time
}

type cronRun struct {
	duration time.Duration
	problems []error
}

type cronStatus struct {
	Last       time.Time `json:"last"`
	DurationMs int64     `json:"durationMs"`
	Result     string    `json:"result"`
	Error      string    `json:"error,omitempty"`
}

func NewRunner(app, ops *pgxpool.Pool, redisClient *redis.Client, objects *objectstore.Client) (*Runner, error) {
	if app == nil || ops == nil || redisClient == nil || objects == nil {
		return nil, errors.New("maintenance dependencies are required")
	}
	return &Runner{app: app, ops: ops, redis: redisClient, objects: objects, config: opsconfig.New(ops, 0), now: time.Now}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	r.runOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.runOnce(ctx)

		}
	}
}

func (r *Runner) runOnce(ctx context.Context) {
	if err := r.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("maintenance tick failed", "error", err)
	}
}

func (r *Runner) Tick(ctx context.Context) error {
	policy, err := r.config.Lifecycle(ctx)
	if err != nil {
		return err
	}
	rows, err := r.ops.Query(ctx, `SELECT id FROM class WHERE NOT archived ORDER BY id`)
	if err != nil {
		return err
	}
	var classIDs []int64
	for rows.Next() {
		var classID int64
		if err := rows.Scan(&classID); err != nil {
			rows.Close()
			return err
		}
		classIDs = append(classIDs, classID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	now := r.now().UTC()
	var problems []error
	runs := map[string]*cronRun{
		"window-reminders": {}, "auto-seal": {},
		"review-sla": {}, "export-expiry": {},
		"ai-asset-expiry": {}, "agent-attachment-expiry": {},
		"knowledge-blob-expiry": {}, "platform-knowledge-expiry": {}, "storage-reconcile": {},
	}
	record := func(names []string, started time.Time, runErr error) {
		elapsed := time.Since(started)
		for _, name := range names {
			runs[name].duration += elapsed
			if runErr != nil {
				runs[name].problems = append(runs[name].problems, runErr)
			}
		}
	}
	for _, classID := range classIDs {
		started := time.Now()
		if err := r.runWindowTasks(ctx, classID, now); err != nil {
			record([]string{"window-reminders", "auto-seal", "review-sla"}, started, err)
			problems = append(problems, fmt.Errorf("class %d window tasks: %w", classID, err))
		} else {
			record([]string{"window-reminders", "auto-seal", "review-sla"}, started, nil)
		}
		started = time.Now()
		if err := r.expireExports(ctx, classID, now); err != nil {
			record([]string{"export-expiry"}, started, err)
			problems = append(problems, fmt.Errorf("class %d export expiry: %w", classID, err))
		} else {
			record([]string{"export-expiry"}, started, nil)
		}
		started = time.Now()
		if err := r.expireAIAssets(ctx, classID, now); err != nil {
			record([]string{"ai-asset-expiry"}, started, err)
			problems = append(problems, fmt.Errorf("class %d AI asset expiry: %w", classID, err))
		} else {
			record([]string{"ai-asset-expiry"}, started, nil)
		}
		started = time.Now()
		if err := r.expireAgentAttachments(ctx, classID, now, time.Duration(policy.AgentAttachmentGraceHours)*time.Hour); err != nil {
			record([]string{"agent-attachment-expiry"}, started, err)
			problems = append(problems, fmt.Errorf("class %d Agent attachment expiry: %w", classID, err))
		} else {
			record([]string{"agent-attachment-expiry"}, started, nil)
		}
		started = time.Now()
		if err := r.expireKnowledgeBlobs(ctx, classID, now, time.Duration(policy.KnowledgeDeleteGraceHours)*time.Hour); err != nil {
			record([]string{"knowledge-blob-expiry"}, started, err)
			problems = append(problems, fmt.Errorf("class %d knowledge blob expiry: %w", classID, err))
		} else {
			record([]string{"knowledge-blob-expiry"}, started, nil)
		}
	}
	started := time.Now()
	if err := r.expirePlatformKnowledge(ctx, now, time.Duration(policy.KnowledgeDeleteGraceHours)*time.Hour); err != nil {
		record([]string{"platform-knowledge-expiry"}, started, err)
		problems = append(problems, fmt.Errorf("platform knowledge expiry: %w", err))
	} else {
		record([]string{"platform-knowledge-expiry"}, started, nil)
	}
	started = time.Now()
	if err := r.reconcileStorageIfDue(ctx, now, time.Duration(policy.StorageReconcileMinutes)*time.Minute); err != nil {
		record([]string{"storage-reconcile"}, started, err)
		problems = append(problems, fmt.Errorf("storage reconciliation: %w", err))
	} else {
		record([]string{"storage-reconcile"}, started, nil)
	}
	pipe := r.redis.Pipeline()
	stamp := now.Format(time.RFC3339)
	for name, run := range runs {
		joined := errors.Join(run.problems...)
		status := cronStatus{Last: now, DurationMs: run.duration.Milliseconds(), Result: "ok"}
		if joined != nil {
			status.Result = "failed"
			status.Error = truncateError(joined.Error(), 1000)
		}
		raw, _ := json.Marshal(status)
		pipe.Set(ctx, "easygpa:cron:"+name+":status", raw, 0)
		if name != "storage-reconcile" {
			pipe.Set(ctx, "easygpa:cron:"+name+":last", stamp, 0)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

func truncateError(value string, limit int) string {
	chars := []rune(strings.TrimSpace(value))
	if len(chars) <= limit {
		return string(chars)
	}
	return string(chars[:limit]) + "…"
}

func (r *Runner) reconcileStorageIfDue(ctx context.Context, now time.Time, cadence time.Duration) error {
	if cadence <= 0 {
		cadence = time.Hour
	}
	const key = "easygpa:cron:storage-reconcile:last"
	if raw, err := r.redis.Get(ctx, key).Result(); err == nil {
		if last, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil && now.Sub(last) < cadence {
			return nil
		}
	}
	objects, err := r.objects.List(ctx)
	if err != nil {
		return err
	}
	totals := make(map[int64]int64)
	for _, object := range objects {
		classID, ok := classIDFromObjectKey(object.Key)
		if ok {
			totals[classID] += object.Size
		}
	}
	tx, err := r.ops.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `UPDATE class SET storage_bytes=0,storage_calibrated_at=$1,updated_at=now()`, now); err != nil {
		return err
	}
	for classID, bytes := range totals {
		if _, err := tx.Exec(ctx, `UPDATE class SET storage_bytes=$1,storage_calibrated_at=$2,updated_at=now() WHERE id=$3`, bytes, now, classID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_audit (actor,action,resource_type,resource_id,metadata)
		VALUES ('system','storage.reconciled','object_store',NULL,jsonb_build_object('objects',$1::integer,'tenants',$2::integer))
	`, len(objects), len(totals)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return r.redis.Set(ctx, key, now.UTC().Format(time.RFC3339), 0).Err()
}

func classIDFromObjectKey(key string) (int64, bool) {
	rest, ok := strings.CutPrefix(key, "class-")
	if !ok {
		return 0, false
	}
	raw, _, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

type expiredAgentAttachment struct {
	id  int64
	key string
}

// 选择后没有随消息发送的图片只是暂存对象。浏览器崩溃或直接关页时 remove 不会
// 执行，24 小时后由维护任务清掉；已经绑定到消息的图片则随会话留存。
func (r *Runner) expireAgentAttachments(ctx context.Context, classID int64, now time.Time, grace time.Duration) error {
	cutoff := now.Add(-grace)
	attachments := make([]expiredAgentAttachment, 0)
	if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id,object_key FROM agent_attachment
			 WHERE message_id IS NULL AND created_at<=$1 ORDER BY id
		`, cutoff)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item expiredAgentAttachment
			if err := rows.Scan(&item.id, &item.key); err != nil {
				return err
			}
			attachments = append(attachments, item)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	var problems []error
	for _, attachment := range attachments {
		if err := r.objects.Remove(ctx, attachment.key); err != nil {
			problems = append(problems, fmt.Errorf("remove Agent attachment %d: %w", attachment.id, err))
			continue
		}
		if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM agent_attachment WHERE id=$1 AND message_id IS NULL`, attachment.id)
			return err
		}); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

type expiredKnowledgeBlob struct {
	id       int64
	original string
	entries  []string
}

// expireKnowledgeBlobs removes object bytes only after every visible alias has
// been soft-deleted for at least seven days.  Metadata rows remain for audit;
// the object key is never exposed to an operator or business API.
func (r *Runner) expireKnowledgeBlobs(ctx context.Context, classID int64, now time.Time, grace time.Duration) error {
	cutoff := now.Add(-grace)
	blobs := make([]expiredKnowledgeBlob, 0)
	if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id,b.object_key,COALESCE(array_agg(e.text_object_key) FILTER (WHERE e.text_object_key IS NOT NULL),'{}')
			  FROM knowledge_blob b LEFT JOIN knowledge_entry e ON e.blob_id=b.id
			 WHERE b.status<>'deleted' AND b.updated_at<=$1
			   AND NOT EXISTS (SELECT 1 FROM knowledge_document d WHERE d.blob_id=b.id AND d.deleted_at IS NULL)
			 GROUP BY b.id,b.object_key ORDER BY b.id
		`, cutoff)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item expiredKnowledgeBlob
			if err := rows.Scan(&item.id, &item.original, &item.entries); err != nil {
				return err
			}
			blobs = append(blobs, item)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	var problems []error
	for _, blob := range blobs {
		keys := append([]string{blob.original}, blob.entries...)
		failed := false
		for _, key := range keys {
			if key == "" {
				continue
			}
			if err := r.objects.Remove(ctx, key); err != nil {
				problems = append(problems, fmt.Errorf("remove knowledge blob %d object: %w", blob.id, err))
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM knowledge_entry WHERE blob_id=$1`, blob.id); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE knowledge_blob SET status='deleted',deleted_at=now(),error=NULL,updated_at=now() WHERE id=$1`, blob.id)
			return err
		}); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

type expiredPlatformBlob struct {
	id       int64
	original string
	docIDs   []int64
	entries  []string
}

// Platform documents are global and therefore cleaned through the Ops pool,
// outside tenant iteration. Soft-deleted documents retain audit metadata until
// the same grace period used by class knowledge; raw and extracted objects are
// removed only when no active document still references the blob.
func (r *Runner) expirePlatformKnowledge(ctx context.Context, now time.Time, grace time.Duration) error {
	cutoff := now.Add(-grace)
	items := make([]expiredPlatformBlob, 0)
	rows, err := r.ops.Query(ctx, `
		SELECT b.id,b.object_key,
		       COALESCE(array_agg(DISTINCT d.id) FILTER (WHERE d.deleted_at IS NOT NULL AND d.deleted_at<=$1),'{}'),
		       COALESCE(array_agg(DISTINCT e.text_object_key) FILTER (WHERE e.text_object_key IS NOT NULL),'{}')
		  FROM platform_knowledge_blob b
		  LEFT JOIN platform_knowledge_document d ON d.blob_id=b.id
		  LEFT JOIN platform_knowledge_entry e ON e.document_id=d.id
		 WHERE NOT EXISTS (SELECT 1 FROM platform_knowledge_document active WHERE active.blob_id=b.id AND active.deleted_at IS NULL)
		   AND (b.deleted_at<=$1 OR (b.deleted_at IS NULL AND EXISTS (
		       SELECT 1 FROM platform_knowledge_document removed WHERE removed.blob_id=b.id AND removed.deleted_at<=$1)))
		 GROUP BY b.id,b.object_key ORDER BY b.id
	`, cutoff)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item expiredPlatformBlob
		if err := rows.Scan(&item.id, &item.original, &item.docIDs, &item.entries); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	var problems []error
	for _, item := range items {
		failed := false
		for _, key := range append([]string{item.original}, item.entries...) {
			if key == "" {
				continue
			}
			var refs int
			if err := r.ops.QueryRow(ctx, `
				SELECT count(*) FROM platform_knowledge_entry e
				 JOIN platform_knowledge_document d ON d.id=e.document_id
				 WHERE e.text_object_key=$1 AND (d.deleted_at IS NULL OR d.deleted_at>$2)
			`, key, cutoff).Scan(&refs); err != nil {
				problems = append(problems, err)
				failed = true
				break
			}
			if refs == 0 {
				if err := r.objects.Remove(ctx, key); err != nil {
					problems = append(problems, fmt.Errorf("remove platform knowledge blob %d object: %w", item.id, err))
					failed = true
					break
				}
			}
		}
		if failed {
			continue
		}
		tx, err := r.ops.Begin(ctx)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if len(item.docIDs) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE platform_knowledge_entry SET text_object_key=NULL,plain_text='',updated_at=now() WHERE document_id=ANY($1)`, item.docIDs); err != nil {
				_ = tx.Rollback(ctx)
				problems = append(problems, err)
				continue
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE platform_knowledge_blob SET status='deleted',deleted_at=COALESCE(deleted_at,now()),updated_at=now() WHERE id=$1 AND NOT EXISTS (SELECT 1 FROM platform_knowledge_document WHERE blob_id=$1 AND deleted_at IS NULL)`, item.id); err != nil {
			_ = tx.Rollback(ctx)
			problems = append(problems, err)
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

type expiredAIAsset struct {
	id  int64
	key string
}

// expireAIAssets removes only staging inputs that were never adopted. Applied
// assets are immutable audit sources and are deliberately retained; the normal
// submission owns an independent evidence object created during apply.
func (r *Runner) expireAIAssets(ctx context.Context, classID int64, now time.Time) error {
	assets := make([]expiredAIAsset, 0)
	if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE ai_batch SET status='expired',updated_at=now()
			 WHERE expires_at<=$1 AND status IN ('uploading','review','failed','canceled')
		`, now); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT a.id,a.object_key
			  FROM ai_asset a JOIN ai_batch b ON b.id=a.batch_id
			 WHERE b.status='expired' AND b.expires_at<=$1
			   AND a.status NOT IN ('applied','rejected') AND a.applied_submission_id IS NULL
			 ORDER BY a.id
		`, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var asset expiredAIAsset
			if err := rows.Scan(&asset.id, &asset.key); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	var problems []error
	for _, asset := range assets {
		if err := r.objects.Remove(ctx, asset.key); err != nil {
			problems = append(problems, fmt.Errorf("remove AI asset %d: %w", asset.id, err))
			continue
		}
		if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				UPDATE ai_asset SET status='rejected',error='expired and removed',updated_at=now()
				 WHERE id=$1 AND status NOT IN ('applied','rejected') AND applied_submission_id IS NULL
			`, asset.id)
			return err
		}); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

func (r *Runner) runWindowTasks(ctx context.Context, classID int64, now time.Time) error {
	return store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
		var schemeID int64
		var raw []byte
		err := tx.QueryRow(ctx, `
			SELECT id,config FROM scheme WHERE status='published'
			 ORDER BY version DESC,id DESC LIMIT 1
		`).Scan(&schemeID, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var cfg scheme.Config
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return err
		}
		// 窗口与开关不在 scheme.config 里，得从班级时间线上取——这条 SQL 是
		// 自己写的，绕过了 api 那边的加载路径，所以要在这里补上。
		timeline, err := classtimeline.Load(ctx, tx, classID)
		if err != nil {
			return err
		}
		timeline.Apply(&cfg)
		/* 全系统封锁之后，这个 runner 一件事都不该做。自动封存、自动确认、
		   盲审生成、SLA 提醒，每一样都会改状态——封锁的意思就是不再改状态。
		   班管把封锁时间往后挪，下一个 tick 自然全部恢复。 */
		if cfg.Window.LockedAt(now) {
			return nil
		}
		if days, due := reminderDue(now, cfg.Window.Close); due {
			if err := scheduleReminder(ctx, tx, classID, schemeID, days, now); err != nil {
				return err
			}
		}
		if !now.Before(cfg.Window.Close) {
			if err := autoSeal(ctx, tx, classID, schemeID, cfg.Window.Close); err != nil {
				return err
			}
		}
		if err := scheduleReviewSLAOverdue(ctx, tx, classID, now); err != nil {
			return err
		}
		return nil
	})
}

func scheduleReviewSLAOverdue(ctx context.Context, tx pgx.Tx, classID int64, now time.Time) error {
	local := now.In(shanghai)
	if local.Hour() < 8 {
		return nil
	}
	runKey := "review-sla:" + local.Format("2006-01-02")
	command, err := tx.Exec(ctx, `
		INSERT INTO maintenance_run (class_id,job_name,run_key,detail)
		VALUES ($1,'review_sla_overdue',$2,jsonb_build_object('date',$3::text))
		ON CONFLICT (class_id,job_name,run_key) DO NOTHING
	`, classID, runKey, local.Format("2006-01-02"))
	if err != nil || command.RowsAffected() == 0 {
		return err
	}
	rows, err := tx.Query(ctx, `
		WITH cfg AS (
		 SELECT COALESCE((SELECT item_hours FROM review_sla_config WHERE class_id=$1),24) AS item_hours,
		        COALESCE((SELECT scorecard_hours FROM review_sla_config WHERE class_id=$1),24) AS scorecard_hours
		), overdue AS (
		 SELECT sr.reviewer_id FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id,cfg
		  WHERE sr.active AND s.status IN ('pending','consensus')
		    AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=sr.reviewer_id AND r.superseded_at IS NULL)
		    AND sr.assigned_at + make_interval(hours => cfg.item_hours)<=$2
		)
		SELECT reviewer_id,count(*)::integer FROM overdue GROUP BY reviewer_id ORDER BY reviewer_id
	`, classID, now)
	if err != nil {
		return err
	}
	recipientSet := make(map[int64]bool)
	overdueCount := 0
	for rows.Next() {
		var reviewerID int64
		var count int
		if err := rows.Scan(&reviewerID, &count); err != nil {
			rows.Close()
			return err
		}
		recipientSet[reviewerID] = true
		overdueCount += count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if overdueCount == 0 {
		return nil
	}
	adminRows, err := tx.Query(ctx, `SELECT id FROM app_user WHERE status='active' AND role='class_admin' ORDER BY id`)
	if err != nil {
		return err
	}
	for adminRows.Next() {
		var adminID int64
		if err := adminRows.Scan(&adminID); err != nil {
			adminRows.Close()
			return err
		}
		recipientSet[adminID] = true
	}
	adminRows.Close()
	recipientIDs := make([]int64, 0, len(recipientSet))
	for id := range recipientSet {
		recipientIDs = append(recipientIDs, id)
	}
	sort.Slice(recipientIDs, func(i, j int) bool { return recipientIDs[i] < recipientIDs[j] })
	if err := enqueueMaintenanceEvent(ctx, tx, classID, events.ReviewSLAOverdue, map[string]any{"recipientIds": recipientIDs, "overdueCount": overdueCount, "date": local.Format("2006-01-02")}); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"recipientCount": len(recipientIDs), "overdueCount": overdueCount})
	_, err = tx.Exec(ctx, `INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,metadata) VALUES ($1,NULL,'system','review_sla.reminder_scheduled','review',$2)`, classID, metadata)
	return err
}

func enqueueMaintenanceEvent(ctx context.Context, tx pgx.Tx, classID int64, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_event (class_id,type,payload) VALUES ($1,$2,$3)`, classID, eventType, raw)
	return err
}

func reminderDue(now, close time.Time) (int, bool) {
	localNow := now.In(shanghai)
	localClose := close.In(shanghai)
	if !localNow.Before(localClose) || localNow.Hour() < 9 {
		return 0, false
	}
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, shanghai)
	closeDay := time.Date(localClose.Year(), localClose.Month(), localClose.Day(), 0, 0, 0, 0, shanghai)
	days := int(closeDay.Sub(today) / (24 * time.Hour))
	return days, days == 7 || days == 3 || days == 1
}

func scheduleReminder(ctx context.Context, tx pgx.Tx, classID, schemeID int64, days int, now time.Time) error {
	runKey := fmt.Sprintf("scheme:%d:day:%s:d%d", schemeID, now.In(shanghai).Format("2006-01-02"), days)
	command, err := tx.Exec(ctx, `
		INSERT INTO maintenance_run (class_id,job_name,run_key,detail)
		VALUES ($1,'window_reminder',$2,jsonb_build_object('schemeId',$3::bigint,'days',$4::integer))
		ON CONFLICT (class_id,job_name,run_key) DO NOTHING
	`, classID, runKey, schemeID, days)
	if err != nil || command.RowsAffected() == 0 {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT u.id FROM app_user u
		 WHERE u.status='active'
		   AND NOT EXISTS (SELECT 1 FROM seal s WHERE s.student_id=u.id AND s.unsealed_at IS NULL)
		 ORDER BY u.id
	`)
	if err != nil {
		return err
	}
	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(userIDs) > 0 {
		payload, _ := json.Marshal(map[string]any{"studentIds": userIDs, "daysRemaining": days, "source": "schedule"})
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_event (class_id,type,payload) VALUES ($1,$2,$3)`, classID, events.WindowReminder, payload); err != nil {
			return err
		}
	}
	metadata, _ := json.Marshal(map[string]any{"schemeId": schemeID, "daysRemaining": days, "recipientCount": len(userIDs)})
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,metadata)
		VALUES ($1,NULL,'system','window.reminder_scheduled','scheme',$2::bigint::text,$3)
	`, classID, schemeID, metadata)
	return err
}

func autoSeal(ctx context.Context, tx pgx.Tx, classID, schemeID int64, closedAt time.Time) error {
	rows, err := tx.Query(ctx, `
		INSERT INTO seal (class_id,student_id,source,sealed_at)
		SELECT $1,u.id,'auto',$2 FROM app_user u
		 WHERE u.status='active'
		   AND NOT EXISTS (SELECT 1 FROM seal s WHERE s.student_id=u.id AND s.unsealed_at IS NULL)
		ON CONFLICT (class_id,student_id) WHERE unsealed_at IS NULL DO NOTHING
		RETURNING id,student_id
	`, classID, closedAt)
	if err != nil {
		return err
	}
	type sealed struct{ id, userID int64 }
	var created []sealed
	for rows.Next() {
		var item sealed
		if err := rows.Scan(&item.id, &item.userID); err != nil {
			rows.Close()
			return err
		}
		created = append(created, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range created {
		metadata, _ := json.Marshal(map[string]any{"windowClose": closedAt, "schemeId": schemeID})
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,metadata)
			VALUES ($1,NULL,'system','seal.auto','user',$2::bigint::text,$3)
		`, classID, item.userID, metadata); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"studentId": item.userID, "source": "auto", "sealId": item.id})
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_event (class_id,type,payload) VALUES ($1,$2,$3)`, classID, events.SealConfirmed, payload); err != nil {
			return err
		}
	}
	return nil
}

type expiredExport struct {
	id     string
	key    string
	status string
}

func (r *Runner) expireExports(ctx context.Context, classID int64, now time.Time) error {
	var jobs []expiredExport
	if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id::text,object_key,status FROM export_job
			 WHERE object_key IS NOT NULL
			   AND ((status='complete' AND expires_at<=$1) OR status='expired')
			 ORDER BY expires_at NULLS LAST,id
		`, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item expiredExport
			if err := rows.Scan(&item.id, &item.key, &item.status); err != nil {
				return err
			}
			jobs = append(jobs, item)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	var problems []error
	for _, job := range jobs {
		claimed := job.status == "expired"
		if job.status == "complete" {
			err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
				command, err := tx.Exec(ctx, `
					UPDATE export_job SET status='expired',finished_at=COALESCE(finished_at,now())
					 WHERE id=$1::uuid AND status='complete' AND expires_at<=$2
				`, job.id, now)
				if err != nil || command.RowsAffected() == 0 {
					return err
				}
				_, err = tx.Exec(ctx, `
					INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,metadata)
					VALUES ($1,NULL,'system','export.expired','export_job',$2,'{}'::jsonb)
				`, classID, job.id)
				if err == nil {
					claimed = true
				}
				return err
			})
			if err != nil {
				problems = append(problems, err)
				continue
			}
		}
		if !claimed {
			continue
		}
		if err := r.objects.Remove(ctx, job.key); err != nil {
			problems = append(problems, fmt.Errorf("remove %s: %w", job.id, err))
			continue
		}
		if err := store.InTenantTx(ctx, r.app, classID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				UPDATE export_job SET object_key=NULL
				 WHERE id=$1::uuid AND status='expired' AND object_key=$2
			`, job.id, job.key)
			return err
		}); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}
