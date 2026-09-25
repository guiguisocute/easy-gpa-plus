package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConfigureQueue is mandatory: unconfigured/legacy workers fail closed.
func (w *Worker) ConfigureQueue(ops *pgxpool.Pool, settings mailSettingsSource) {
	w.ops, w.settings = ops, settings
}

func mailEventPayload(raw json.RawMessage) map[string]any {
	var p map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&p)
	return p
}

func notificationCategory(event events.Event, userID int64) string {
	if _, off := mailSuppressedEvents[event.Type]; off {
		return ""
	}
	p := mailEventPayload(event.Payload)
	switch event.Type {
	case events.ReviewDecided:
		if p["status"] == "scored" || p["status"] == "locked" {
			return "results"
		}
		return "progress"
	case events.ConflictRaised:
		return "progress"
	case events.DispatchDone, events.DispatchReassigned, events.AppealAssigned, events.ScorecardAuditAssigned:
		return "tasks"
	case events.WindowReminder, events.ReviewSLAOverdue:
		return "deadlines"
	case events.SealConfirmed, events.ResultConfirmed, events.AppealFiled:
		return "receipts"
	case events.ScorecardAuditCompleted:
		if payloadID(p, "studentId") != userID {
			return "class_activity"
		}
		return "decisions"
	case events.SubmissionForceScored, events.SubmissionForceRejected, events.ArbitrationResolved, events.AppealResolved, events.SettlementDone:
		return "decisions"
	}
	return ""
}

func notificationEntity(event events.Event) string {
	p := mailEventPayload(event.Payload)
	switch event.Type {
	case events.DispatchDone, events.DispatchReassigned:
		return "review-inbox"
	case events.WindowReminder:
		return "submission-deadline"
	case events.ReviewSLAOverdue:
		return "overdue-inbox"
	case events.SettlementDone:
		return "settlement"
	}
	for _, key := range []string{"appealId", "submissionId", "assignmentId", "batchId"} {
		if value, ok := p[key]; ok && fmt.Sprint(value) != "" {
			return key + ":" + fmt.Sprint(value)
		}
	}
	return event.Type
}

func (w *Worker) enqueue(ctx context.Context, event events.Event) error {
	if w.settings == nil || !notifiable(event.Type) {
		return nil
	}
	cfg, err := w.settings.Mail(ctx)
	if err != nil {
		return err
	}
	if !cfg.NotificationsEnabled || cfg.NotificationsSince.IsZero() {
		return nil
	}
	var created time.Time
	if err = w.pool.QueryRow(ctx, `SELECT created_at FROM outbox_event WHERE id=$1::uuid`, event.ID).Scan(&created); err != nil {
		return err
	}
	if created.Before(cfg.NotificationsSince) || created.Before(time.Now().Add(-48*time.Hour)) {
		return nil
	}
	targets, _, err := w.recipients(ctx, event)
	if err != nil {
		return err
	}
	return store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		for _, target := range targets {
			category := notificationCategory(event, target.UserID)
			if category == "" {
				continue
			}
			p, _, err := LoadPreferences(ctx, tx, event.ClassID, target.UserID)
			if err != nil {
				return err
			}
			if !p.Enabled || p.Categories[category] == MailOff {
				continue
			}
			entity := notificationEntity(event)
			// Keep only the newest state of a material/workflow, even when
			// Redis delivers its events out of order. An earlier burst keeps
			// its due time so continuing uploads cannot postpone it forever.
			var newer bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_notification n JOIN outbox_event e ON e.id=n.event_id
                WHERE n.user_id=$1 AND n.entity_key=$2 AND (e.created_at>$3 OR e.id=$4::uuid))`, target.UserID, entity, created, event.ID).Scan(&newer); err != nil {
				return err
			}
			if newer {
				continue
			}
			due := p.Due(time.Now(), p.Categories[category])
			if err = tx.QueryRow(ctx, `SELECT least($3::timestamptz,coalesce(min(due_at),$3)) FROM mail_notification WHERE user_id=$1 AND entity_key=$2 AND status='pending' AND category=$4`, target.UserID, entity, due, category).Scan(&due); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE mail_notification SET status='suppressed' WHERE user_id=$1 AND entity_key=$2 AND status='pending'`, target.UserID, entity); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO mail_notification(class_id,user_id,event_id,category,mode,entity_key,due_at,expires_at)
                VALUES($1,$2,$3::uuid,$4,$5,$6,$7,$8) ON CONFLICT(event_id,user_id) DO NOTHING`, event.ClassID, target.UserID, event.ID, category, p.Categories[category], entity, due, created.Add(72*time.Hour)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *Worker) RunQueue(ctx context.Context) error {
	if w.ops == nil || w.settings == nil {
		return errors.New("mail queue configuration is required")
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.Flush(ctx); err != nil && ctx.Err() == nil {
			slog.Error("mail queue cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) Flush(ctx context.Context) error {
	cfg, err := w.settings.Mail(ctx)
	if err != nil {
		return err
	}
	if !cfg.NotificationsEnabled {
		// Do not retain a backlog for a later enable. Ledger entries for an
		// in-flight provider request remain intact and prevent repeat sends.
		_, err = w.ops.Exec(ctx, `UPDATE mail_notification SET status='suppressed' WHERE status IN ('pending','batched')`)
		if err != nil {
			return err
		}
		_, err = w.ops.Exec(ctx, `UPDATE mail_batch SET status='suppressed',finished_at=now() WHERE status='pending'`)
		return err
	}
	rows, err := w.ops.Query(ctx, `SELECT class_id,user_id FROM (
        SELECT class_id,user_id,due_at AS due FROM mail_notification WHERE status='pending' AND due_at<=now()
        UNION ALL SELECT class_id,user_id,next_attempt_at FROM mail_batch WHERE status='pending' AND next_attempt_at<=now()
        ) q GROUP BY class_id,user_id ORDER BY min(due) LIMIT 100`)
	if err != nil {
		return err
	}
	var targets [][2]int64
	for rows.Next() {
		var target [2]int64
		if err = rows.Scan(&target[0], &target[1]); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, target := range targets {
		id, err := w.prepareBatch(ctx, target[0], target[1], cfg.NotificationsSince)
		if err == nil && id != "" {
			err = w.sendBatch(ctx, target[0], id)
		}
		// One recipient's refusal or stale work must never block a class.
		if err != nil && ctx.Err() == nil {
			slog.Error("mail recipient queue failed", "class_id", target[0], "error", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

type queuedNotification struct {
	ID       int64
	Category string
	Mode     MailMode
	Event    events.Event
}

func readQueued(ctx context.Context, tx pgx.Tx, userID int64, batchID string) ([]queuedNotification, error) {
	rows, err := tx.Query(ctx, `SELECT n.id,n.category,n.mode,e.id::text,e.class_id,e.type,e.payload
        FROM mail_notification n JOIN outbox_event e ON e.id=n.event_id
        WHERE n.user_id=$1 AND n.expires_at>now() AND (($2='' AND n.status='pending' AND (n.due_at<=now() OR (n.mode='immediate' AND n.due_at<=now()+interval '10 minutes')))
            OR ($2<>'' AND n.status='batched' AND n.batch_id=NULLIF($2,'')::uuid))
        ORDER BY e.created_at,n.id LIMIT 500`, userID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []queuedNotification{}
	for rows.Next() {
		var n queuedNotification
		if err = rows.Scan(&n.ID, &n.Category, &n.Mode, &n.Event.ID, &n.Event.ClassID, &n.Event.Type, &n.Event.Payload); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (w *Worker) prepareBatch(ctx context.Context, classID, userID int64, since time.Time) (string, error) {
	var batchID string
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		// Serialise reservations per account, including across worker replicas.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7151,$1::integer)`, userID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT id::text FROM mail_batch WHERE user_id=$1 AND status='pending' ORDER BY created_at LIMIT 1`, userID).Scan(&batchID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var address string
		err = tx.QueryRow(ctx, `SELECT e.email_normalized FROM app_user u JOIN class c ON c.id=u.class_id JOIN user_email e ON e.user_id=u.id
            WHERE u.id=$1 AND u.status='active' AND u.password_hash IS NOT NULL AND NOT c.archived AND e.is_primary AND e.verified_at IS NOT NULL`, userID).Scan(&address)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `UPDATE mail_notification SET status='suppressed' WHERE user_id=$1 AND status='pending'`, userID)
			return err
		}
		if err != nil {
			return err
		}
		p, _, err := LoadPreferences(ctx, tx, classID, userID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE mail_notification n SET status='suppressed' FROM outbox_event e
            WHERE n.user_id=$1 AND n.event_id=e.id AND n.status='pending' AND (n.expires_at<=now() OR e.created_at<$2 OR NOT $3)`, userID, since, p.Enabled); err != nil {
			return err
		}
		if !p.Enabled {
			return nil
		}
		now := time.Now()
		next := p.afterQuiet(now)
		if next.After(now) {
			_, err = tx.Exec(ctx, `UPDATE mail_notification SET due_at=greatest(due_at,$2) WHERE user_id=$1 AND status='pending'`, userID, next)
			return err
		}
		day := atClock(now, "00:00")
		var sent int
		var digestSent bool
		if err = tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_or(template=$3),false) FROM mail_batch WHERE user_id=$1 AND status='sent' AND finished_at>=$2`, userID, day, TemplateNotificationDigest).Scan(&sent, &digestSent); err != nil {
			return err
		}
		if sent >= p.DailyLimit {
			_, err = tx.Exec(ctx, `UPDATE mail_notification SET due_at=$2 WHERE user_id=$1 AND status='pending' AND due_at<$2`, userID, p.afterQuiet(day.AddDate(0, 0, 1)))
			return err
		}
		items, err := readQueued(ctx, tx, userID, "")
		if err != nil {
			return err
		}
		ids := []int64{}
		digest := false
		for _, item := range items {
			if p.Categories[item.Category] == MailOff {
				if _, err = tx.Exec(ctx, `UPDATE mail_notification SET status='suppressed' WHERE id=$1`, item.ID); err != nil {
					return err
				}
				continue
			}
			if item.Mode != p.Categories[item.Category] || (item.Mode == MailDigest && digestSent) {
				if _, err = tx.Exec(ctx, `UPDATE mail_notification SET due_at=$2,mode=$3 WHERE id=$1`, item.ID, p.Due(now, p.Categories[item.Category]), p.Categories[item.Category]); err != nil {
					return err
				}
				continue
			}
			live, err := notificationStillRelevant(ctx, tx, item.Event, userID)
			if err != nil {
				return err
			}
			if !live {
				if _, err = tx.Exec(ctx, `UPDATE mail_notification SET status='suppressed' WHERE id=$1`, item.ID); err != nil {
					return err
				}
				continue
			}
			// Frequent reminders contain one current event. Digest/combined
			// categories keep their own batching even in a mixed preference set.
			if item.Mode == MailFrequent && len(ids) > 0 {
				continue
			}
			ids = append(ids, item.ID)
			digest = digest || item.Mode == MailDigest
			if item.Mode == MailFrequent {
				break
			}
		}
		if len(ids) == 0 {
			return nil
		}
		template := TemplateNotificationAlert
		if digest {
			template = TemplateNotificationDigest
		}
		if err = tx.QueryRow(ctx, `INSERT INTO mail_batch(class_id,user_id,recipient,template) VALUES($1,$2,$3,$4) RETURNING id::text`, classID, userID, address, template).Scan(&batchID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE mail_notification SET status='batched',batch_id=$2::uuid WHERE id=ANY($1::bigint[])`, ids, batchID)
		return err
	})
	return batchID, err
}

func (w *Worker) sendBatch(ctx context.Context, classID int64, batchID string) error {
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		var userID int64
		var to, template, status string
		var isDue bool
		var createdAt time.Time
		var attempts int
		err := tx.QueryRow(ctx, `SELECT user_id,recipient,template,status,next_attempt_at<=now(),attempts,created_at FROM mail_batch WHERE id=$1::uuid FOR UPDATE`, batchID).Scan(&userID, &to, &template, &status, &isDue, &attempts, &createdAt)
		if err != nil || status != "pending" || !isDue {
			return err
		}
		finish := func(state string) error {
			if _, err := tx.Exec(ctx, `UPDATE mail_batch SET status=$2,finished_at=now() WHERE id=$1::uuid`, batchID, state); err != nil {
				return err
			}
			itemStatus := "suppressed"
			if state == "sent" {
				itemStatus = "sent"
			}
			_, err := tx.Exec(ctx, `UPDATE mail_notification SET status=$2 WHERE batch_id=$1::uuid AND status='batched'`, batchID, itemStatus)
			return err
		}
		// Repair an accepted send before preference/address checks. It cannot
		// be unsent, and must never be resent to a newly chosen primary email.
		var deliveryStatus string
		if err = w.ops.QueryRow(ctx, `SELECT delivery_status FROM ops_lookup_mail_delivery($1::uuid,$2)`, batchID, to).Scan(&deliveryStatus); err != nil {
			return err
		}
		if deliveryStatus == "sent" {
			return finish("sent")
		}
		if deliveryStatus == "queued" {
			_, err = tx.Exec(ctx, `UPDATE mail_batch SET next_attempt_at=now()+interval '1 hour' WHERE id=$1::uuid`, batchID)
			return err
		}
		cfg, err := w.settings.Mail(ctx)
		if err != nil {
			return err
		}
		if !cfg.NotificationsEnabled || createdAt.Before(cfg.NotificationsSince) {
			return finish("suppressed")
		}
		var name, className, role string
		var active bool
		if err = tx.QueryRow(ctx, `SELECT u.name,c.name,u.role,u.status='active' AND u.password_hash IS NOT NULL AND NOT c.archived AND EXISTS(
            SELECT 1 FROM user_email e WHERE e.user_id=u.id AND e.is_primary AND e.verified_at IS NOT NULL AND e.email_normalized=$2)
            FROM app_user u JOIN class c ON c.id=u.class_id WHERE u.id=$1`, userID, to).Scan(&name, &className, &role, &active); err != nil {
			return err
		}
		if !active {
			return finish("suppressed")
		}
		p, token, err := LoadPreferences(ctx, tx, classID, userID)
		if err != nil {
			return err
		}
		if !p.Enabled {
			return finish("suppressed")
		}
		now := time.Now()
		next := p.afterQuiet(now)
		var sent int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM mail_batch WHERE user_id=$1 AND status='sent' AND finished_at>=$2`, userID, atClock(now, "00:00")).Scan(&sent); err != nil {
			return err
		}
		if sent >= p.DailyLimit {
			next = p.afterQuiet(atClock(now, "00:00").AddDate(0, 0, 1))
		}
		if next.After(now) {
			_, err = tx.Exec(ctx, `UPDATE mail_batch SET next_attempt_at=$2 WHERE id=$1::uuid`, batchID, next)
			return err
		}
		items, err := readQueued(ctx, tx, userID, batchID)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		labels := map[string]int{}
		for _, item := range items {
			live, err := notificationStillRelevant(ctx, tx, item.Event, userID)
			if err != nil {
				return err
			}
			if !live || p.Categories[item.Category] == MailOff {
				continue
			}
			// Any frequency change applies to reserved, definitely-unsent mail
			// too. Requeue it so switching away from frequent cannot drain a
			// previously reserved stream of individual messages.
			if p.Categories[item.Category] != item.Mode {
				mode := p.Categories[item.Category]
				if _, err = tx.Exec(ctx, `UPDATE mail_notification SET status='pending',batch_id=NULL,mode=$3,due_at=$2 WHERE id=$1`, item.ID, p.Due(now, mode), mode); err != nil {
					return err
				}
				continue
			}
			counts[item.Category]++
			labels[notificationLabels[item.Event.Type]]++
		}
		if len(counts) == 0 {
			return finish("suppressed")
		}
		lines := []string{}
		for _, category := range MailCategories {
			if n := counts[category]; n > 0 {
				lines = append(lines, categoryLabels[category]+"："+strconv.Itoa(n)+" 项更新")
			}
		}
		// Only event labels/counts leave the application. Blind-review names,
		// appeal reasons, scores and uploaded material titles stay behind auth.
		details := []string{}
		for label, n := range labels {
			details = append(details, fmt.Sprintf("%s（%d）", label, n))
		}
		sort.Strings(details)
		heading, subject := "有新的综测进展", "[EasyGPA Plus] 综测进展提醒"
		if template == TemplateNotificationDigest {
			heading, subject = "今天的综测更新", "[EasyGPA Plus] 今日综测汇总"
		}
		view := "stuHome"
		if role == "group" {
			view = "revTasks"
		}
		if role == "class_admin" {
			view = "admBoard"
		}
		frequent := len(items) == 1 && items[0].Mode == MailFrequent
		preferenceNote := "你开启了以上类别的邮件提醒。同一时段的更新已合并；具体状态和截止时间请以平台为准。"
		if frequent {
			preferenceNote = "你为此类消息选择了逐条提醒。可在账号设置改为合并或每日汇总，并调整每日上限；具体状态和截止时间请以平台为准。"
		}
		msg := Message{ClassID: classID, EventID: batchID, To: to, Subject: subject, Template: template, Frequent: frequent, Text: strings.Join(lines, "\n"), Data: map[string]any{
			"name": name, "class_name": className, "heading": heading, "summary": strings.Join(lines, "\n"), "details": strings.Join(details, "\n"),
			"view": view, "opt_out_token": token, "sent_at": now.In(mailLocation).Format("2006-01-02 15:04"),
			"preference_note": preferenceNote,
		}}
		_, sendErr := w.mailer.Send(ctx, msg)
		if sendErr == nil {
			return finish("sent")
		}
		var suppressed *SuppressedError
		if errors.As(sendErr, &suppressed) {
			return finish("suppressed")
		}
		var deferred *events.DeferredError
		delay := 30 * time.Minute
		if errors.As(sendErr, &deferred) {
			delay = deferred.After
		} else {
			attempts++
		}
		if attempts >= 5 {
			return finish("failed")
		}
		_, err = tx.Exec(ctx, `UPDATE mail_batch SET next_attempt_at=$2,attempts=$3 WHERE id=$1::uuid`, batchID, time.Now().Add(delay), attempts)
		return err
	})
}

func notificationStillRelevant(ctx context.Context, tx pgx.Tx, event events.Event, userID int64) (bool, error) {
	p := mailEventPayload(event.Payload)
	query := ""
	args := []any{userID}
	switch event.Type {
	case events.DispatchDone, events.DispatchReassigned:
		query = `SELECT EXISTS(SELECT 1 FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id WHERE sr.reviewer_id=$1 AND sr.active AND s.status IN ('pending','consensus') AND NOT EXISTS(SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=$1 AND r.superseded_at IS NULL))`
	case events.ScorecardAuditAssigned:
		query = `SELECT EXISTS(SELECT 1 FROM scorecard_audit_assignment a JOIN scorecard_audit_subject s ON s.id=a.subject_id JOIN scorecard_audit_batch b ON b.id=s.batch_id WHERE a.reviewer_id=$1 AND a.id=$2 AND a.status='assigned' AND b.status IN ('open','resolving'))`
		args = append(args, payloadID(p, "assignmentId"))
	case events.AppealAssigned:
		query = `SELECT EXISTS(SELECT 1 FROM appeal WHERE handler_id=$1 AND id=$2 AND status='assigned')`
		args = append(args, payloadID(p, "appealId"))
	case events.WindowReminder:
		query = `SELECT EXISTS(SELECT 1 FROM class_timeline WHERE close_at>now()) AND NOT EXISTS(SELECT 1 FROM seal WHERE student_id=$1 AND unsealed_at IS NULL)`
	case events.ReviewSLAOverdue:
		query = `WITH cfg AS (SELECT coalesce((SELECT item_hours FROM review_sla_config),24) AS item_hours,coalesce((SELECT scorecard_hours FROM review_sla_config),24) AS scorecard_hours), overdue AS (
		SELECT sr.reviewer_id FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id,cfg WHERE sr.active AND s.status IN ('pending','consensus') AND sr.assigned_at+make_interval(hours=>cfg.item_hours)<=now() AND NOT EXISTS(SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=sr.reviewer_id AND r.superseded_at IS NULL)
		UNION ALL SELECT a.reviewer_id FROM scorecard_audit_assignment a JOIN scorecard_audit_subject s ON s.id=a.subject_id JOIN scorecard_audit_batch b ON b.id=s.batch_id,cfg WHERE a.status='assigned' AND b.status IN ('open','resolving') AND a.assigned_at+make_interval(hours=>cfg.scorecard_hours)<=now())
		SELECT EXISTS(SELECT 1 FROM overdue WHERE reviewer_id=$1 OR EXISTS(SELECT 1 FROM app_user WHERE id=$1 AND role='class_admin'))`
	case events.SettlementDone:
		query = `SELECT EXISTS(SELECT 1 FROM settlement_run WHERE id=$1 AND status='complete' AND stale_at IS NULL)`
		args = []any{payloadID(p, "runId")}
	case events.ScorecardAuditCompleted:
		query = `SELECT EXISTS(SELECT 1 FROM scorecard_audit_batch b WHERE id=$2::uuid AND status='complete' AND NOT EXISTS(SELECT 1 FROM result_confirmation r WHERE r.batch_id=b.id AND r.student_id=$1))`
		args = append(args, payloadString(p, "batchId", ""))
	case events.ReviewDecided, events.ConflictRaised:
		query = `SELECT EXISTS(SELECT 1 FROM submission WHERE student_id=$1 AND id=$2 AND force_rejection IS NULL AND forced_score IS NULL AND status<>'draft')`
		args = append(args, payloadID(p, "submissionId"))
	default:
		return true, nil
	}
	var live bool
	err := tx.QueryRow(ctx, query, args...).Scan(&live)
	return live, err
}
