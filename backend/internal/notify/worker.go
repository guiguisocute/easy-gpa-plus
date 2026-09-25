package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/richtext"
	"easygpa/backend/internal/store"
)

type Worker struct {
	pool     *pgxpool.Pool
	mailer   Mailer
	policy   DeliveryPolicy
	ops      *pgxpool.Pool
	settings mailSettingsSource
}

func NewWorker(pool *pgxpool.Pool, mailer Mailer) (*Worker, error) {
	return NewWorkerWithPolicy(pool, mailer, nil)
}

func NewWorkerWithPolicy(pool *pgxpool.Pool, mailer Mailer, policy DeliveryPolicy) (*Worker, error) {
	if pool == nil || mailer == nil {
		return nil, errors.New("notification worker dependencies are required")
	}
	return &Worker{pool: pool, mailer: mailer, policy: policy}, nil
}

type recipient struct {
	UserID int64
	Email  string
	Name   string
}

func (w *Worker) Handle(ctx context.Context, event events.Event) error {
	return w.enqueue(ctx, event)
}

func notifiable(eventType string) bool {
	_, ok := notificationLabels[eventType]
	return ok
}

func payloadID(payload map[string]any, key string) int64 {
	return numericID(payload[key])
}

func numericID(raw any) int64 {
	switch value := raw.(type) {
	case float64:
		if value <= 0 || value != float64(int64(value)) {
			return 0
		}
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(value, 10, 64)
		return parsed
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
}

func payloadIDs(payload map[string]any, key string) []int64 {
	values, _ := payload[key].([]any)
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if id := numericID(value); id > 0 {
			result = append(result, id)
		}
	}
	return result
}

func stringEventIDs(values []string, field string) ([]int64, error) {
	result := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid %s event ID %q", field, value)
		}
		result = append(result, id)
	}
	return result, nil
}

func stringEventID(value, field string) (int64, error) {
	ids, err := stringEventIDs([]string{value}, field)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

func (w *Worker) recipients(ctx context.Context, event events.Event) ([]recipient, string, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, "", err
	}
	var targets []int64
	var className string
	all := false
	admins := false
	err := store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		switch event.Type {
		case events.SubmissionForceRejected, events.SubmissionForceScored:
			typed, err := events.Decode(event, submissionCorrectionEvent(event))
			if err != nil {
				return err
			}
			targets = append(targets, typed.StudentID)
		case events.AppealAssigned:
			typed, err := events.Decode(event, events.AppealAssignedEvent())
			if err != nil {
				return err
			}
			targets = append(targets, typed.HandlerID)
		case events.ReviewDecided:
			typed, err := events.Decode(event, events.ReviewDecidedEvent())
			if err != nil {
				return err
			}
			var studentID int64
			if err := tx.QueryRow(ctx, `SELECT student_id FROM submission WHERE id=$1`, typed.SubmissionID).Scan(&studentID); err != nil {
				return err
			}
			targets = append(targets, studentID)
		case events.ConflictRaised:
			typed, err := events.Decode(event, events.ConflictRaisedEvent())
			if err != nil {
				return err
			}
			var studentID int64
			if err := tx.QueryRow(ctx, `SELECT student_id FROM submission WHERE id=$1`, typed.SubmissionID).Scan(&studentID); err != nil {
				return err
			}
			targets = append(targets, studentID)
		case events.AppealFiled:
			typed, err := events.Decode(event, events.AppealFiledEvent())
			if err != nil {
				return err
			}
			var studentID int64
			if err := tx.QueryRow(ctx, `SELECT student_id FROM appeal WHERE id=$1`, typed.AppealID).Scan(&studentID); err != nil {
				return err
			}
			targets = append(targets, studentID)
		case events.AppealResolved:
			typed, err := events.Decode(event, events.AppealResolvedEvent())
			if err != nil {
				return err
			}
			var studentID int64
			if err := tx.QueryRow(ctx, `SELECT student_id FROM appeal WHERE id=$1`, typed.AppealID).Scan(&studentID); err != nil {
				return err
			}
			targets = append(targets, studentID)
		case events.ArbitrationResolved:
			typed, err := events.Decode(event, events.ArbitrationResolvedEvent())
			if err != nil {
				return err
			}
			if typed.AppealID != nil && *typed.AppealID > 0 {
				var studentID int64
				if err := tx.QueryRow(ctx, `SELECT student_id FROM appeal WHERE id=$1`, *typed.AppealID).Scan(&studentID); err != nil {
					return err
				}
				targets = append(targets, studentID)
			} else if typed.StudentID != nil && *typed.StudentID > 0 {
				targets = append(targets, *typed.StudentID)
			} else {
				return errors.New("arbitration.resolved payload has no recipient target")
			}
		case events.DispatchDone:
			typed, err := events.Decode(event, events.DispatchDoneEvent())
			if err != nil {
				return err
			}
			reviewerIDs, err := stringEventIDs(typed.ReviewerIDs, "reviewerIds")
			if err != nil {
				return err
			}
			targets = append(targets, reviewerIDs...)
		case events.DispatchReassigned:
			typed, err := events.Decode(event, events.DispatchReassignedEvent())
			if err != nil {
				return err
			}
			to, err := stringEventID(typed.To, "to")
			if err != nil {
				return err
			}
			targets = append(targets, to)
		case events.WindowReminder:
			targets = append(targets, payloadIDs(payload, "studentIds")...)
		case events.SealConfirmed:
			targets = append(targets, payloadID(payload, "studentId"))
		case events.ResultConfirmed:
			targets = append(targets, payloadID(payload, "studentId"))
		case events.ScorecardAuditAssigned:
			targets = append(targets, payloadID(payload, "reviewerId"))
		case events.ReviewSLAOverdue:
			targets = append(targets, payloadIDs(payload, "recipientIds")...)
		case events.ClassificationResolved:
			var studentID int64
			if err := tx.QueryRow(ctx, `SELECT student_id FROM submission WHERE id=$1`, payloadID(payload, "submissionId")).Scan(&studentID); err != nil {
				return err
			}
			targets = append(targets, studentID)
		case events.ClassificationSuggested, events.ScorecardAuditSubmitted,
			events.ScorecardAuditStale, events.ScorecardAuditBlocked:
			admins = true
		case events.ObjectionSubmitted:
			if _, err := events.Decode(event, events.ObjectionSubmittedEvent()); err != nil {
				return err
			}
			admins = true
		case events.ScorecardAuditCompleted:
			// 终审结论产生同时通知管理员与该学生——学生的确认计时从这一刻开始。
			// 旧事件的 payload 没有 studentId，payloadID 返回 0 会被 uniqueIDs 滤掉。
			admins = true
			targets = append(targets, payloadID(payload, "studentId"))
		case events.ExportDone:
			var requestedBy int64
			if err := tx.QueryRow(ctx, `SELECT requested_by FROM export_job WHERE id=$1::uuid`, fmt.Sprint(payload["jobId"])).Scan(&requestedBy); err != nil {
				return err
			}
			targets = append(targets, requestedBy)
		case events.GateForced, events.SettlementDone:
			all = true
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	targets = uniqueIDs(targets)
	var result []recipient
	err = store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM class WHERE id=$1`, event.ClassID).Scan(&className); err != nil {
			return err
		}
		query := `
			SELECT DISTINCT u.id,e.email,u.name
			  FROM app_user u JOIN user_email e ON e.user_id=u.id AND e.is_primary AND e.verified_at IS NOT NULL
			 WHERE u.status='active' AND ($1 OR ($2 AND u.role='class_admin') OR u.id=ANY($3::bigint[])) ORDER BY u.id
		`
		rows, err := tx.Query(ctx, query, all, admins, targets)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item recipient
			if err := rows.Scan(&item.UserID, &item.Email, &item.Name); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, className, err
}

func (w *Worker) deliver(ctx context.Context, event events.Event, className string, target recipient) error {
	already := false
	if err := store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notification_log WHERE event_id=$1::uuid AND recipient=$2)`, event.ID, target.Email).Scan(&already); err != nil || already {
			return err
		}
		// AuditedMailer records provider acceptance before this worker writes its
		// business receipt. Repair that receipt after a crash or DB interruption
		// without sending the accepted message again.
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mail_delivery WHERE event_id=$1::uuid AND lower(recipient)=lower(btrim($2)) AND status='sent')`, event.ID, target.Email).Scan(&already); err != nil {
			return err
		}
		if already {
			_, err := tx.Exec(ctx, `INSERT INTO notification_log (class_id,event_id,recipient) VALUES ($1,$2::uuid,$3) ON CONFLICT DO NOTHING`, event.ClassID, event.ID, target.Email)
			return err
		}
		if event.Type != events.DispatchDone {
			return nil
		}
		start, end, err := dispatchDigestWindow(ctx, tx, event.ID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM mail_delivery delivery
			  JOIN outbox_event source ON source.id=delivery.event_id
			   WHERE lower(delivery.recipient)=lower(btrim($1)) AND delivery.status='sent' AND source.type=$2
			     AND source.created_at>$3 AND source.created_at<=$4
			)
		`, target.Email, events.DispatchDone, start, end).Scan(&already); err != nil {
			return err
		}
		if already {
			_, err = tx.Exec(ctx, `
				INSERT INTO notification_log (class_id,event_id,recipient)
				VALUES ($1,$2::uuid,$3) ON CONFLICT DO NOTHING
			`, event.ClassID, event.ID, target.Email)
		}
		return err
	}); err != nil {
		return err
	}
	if already {
		return nil
	}
	templateName, templateData, err := notificationTemplate(event, className, target.Name)
	if err != nil {
		return err
	}
	if templateName == "" {
		return nil
	}
	if w.policy != nil {
		if err := w.policy.BeforeSend(ctx); err != nil {
			return err
		}
	}
	subject, body := notificationText(event, className, target.Name)
	if err := w.enrichTemplateData(ctx, event, target, templateData); err != nil {
		return err
	}
	_, sendErr := w.mailer.Send(ctx, Message{
		ClassID: event.ClassID, EventID: event.ID,
		To: target.Email, Subject: subject, Text: body, Template: templateName, Data: templateData,
	})
	if sendErr != nil {
		return sendErr
	}
	return store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO notification_log (class_id,event_id,recipient) VALUES ($1,$2::uuid,$3) ON CONFLICT DO NOTHING`, event.ClassID, event.ID, target.Email)
		return err
	})
}

func (w *Worker) enrichTemplateData(ctx context.Context, event events.Event, target recipient, data map[string]any) error {
	payload := make(map[string]any)
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.UseNumber()
	_ = decoder.Decode(&payload)
	return store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		switch event.Type {
		case events.DispatchDone:
			if _, err := events.Decode(event, events.DispatchDoneEvent()); err != nil {
				return err
			}
			pending, assigned := 0, 1
			if err := tx.QueryRow(ctx, `
				SELECT count(*)
				  FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id
				 WHERE sr.active AND sr.reviewer_id=$1
				   AND s.status IN ('pending','consensus')
				   AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=$1 AND r.superseded_at IS NULL)
			`, target.UserID).Scan(&pending); err != nil {
				return err
			}
			data["pending_count"] = pending
			start, end, err := dispatchDigestWindow(ctx, tx, event.ID)
			if err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `
					SELECT count(*) FROM submission_reviewer
					 WHERE reviewer_id=$1 AND assigned_at>$2 AND assigned_at<=$3
				`, target.UserID, start, end).Scan(&assigned); err != nil {
				return err
			}
			data["new_count"] = assigned
		case events.DispatchReassigned:
			if _, err := events.Decode(event, events.DispatchReassignedEvent()); err != nil {
				return err
			}
			var pending int
			if err := tx.QueryRow(ctx, `
				SELECT count(*)
				  FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id
				 WHERE sr.active AND sr.reviewer_id=$1
				   AND s.status IN ('pending','consensus')
				   AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=$1 AND r.superseded_at IS NULL)
			`, target.UserID).Scan(&pending); err != nil {
				return err
			}
			data["pending_count"], data["new_count"] = pending, 1
		case events.AppealFiled:
			typed, err := events.Decode(event, events.AppealFiledEvent())
			if err != nil {
				return err
			}
			if typed.AppealID == 0 {
				return nil
			}
			return enrichAppealTemplateData(ctx, tx, typed.AppealID, data)
		case events.AppealAssigned:
			typed, err := events.Decode(event, events.AppealAssignedEvent())
			if err != nil {
				return err
			}
			if typed.AppealID == 0 {
				return nil
			}
			return enrichAppealTemplateData(ctx, tx, typed.AppealID, data)
		case events.AppealResolved:
			typed, err := events.Decode(event, events.AppealResolvedEvent())
			if err != nil {
				return err
			}
			appealID := typed.AppealID
			if appealID == 0 {
				return nil
			}
			return enrichAppealTemplateData(ctx, tx, appealID, data)
		case events.ReviewDecided:
			typed, err := events.Decode(event, events.ReviewDecidedEvent())
			if err != nil {
				return err
			}
			submissionID, reviewID := typed.SubmissionID, typed.ReviewID
			if submissionID == 0 {
				return nil
			}
			var title, categoryKey, score, status, basis string
			err = tx.QueryRow(ctx, `
				SELECT s.title,s.category_key,COALESCE(s.final_score::text,''),s.status,COALESCE(r.reason,'')
				  FROM submission s LEFT JOIN review r ON r.id=$2 WHERE s.id=$1
			`, submissionID, reviewID).Scan(&title, &categoryKey, &score, &status, &basis)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			data["title"], data["category_name"] = title, categoryName(ctx, tx, categoryKey)
			data["decision_label"], data["basis"] = reviewStatusLabel(status), markdownMailSummary(basis, 180)
			switch status {
			case "consensus":
				data["review_heading"] = "单项初审正在进行"
				data["review_message"] = "已有一位审核人提交结论，仍需等待另一位审核人独立完成。当前状态不是最终认定结果。"
				data["score_label"], data["score"] = "当前认定分", "待两人完成"
				data["action_note"] = "你暂时无需操作；两位审核人完成后，平台会继续更新初审状态。"
			default:
				data["review_heading"] = "单项初审已形成结论"
				data["review_message"] = "两位审核人的结论已完成合议并形成单项认定结果，实时成绩已更新。"
				data["score_label"], data["score"] = "单项初审认定分", score
				data["action_note"] = "如对单项结果有异议，可按当前规则发起申诉；核对成绩不会取消申诉权。"
			}
		case events.ConflictRaised:
			typed, err := events.Decode(event, events.ConflictRaisedEvent())
			if err != nil {
				return err
			}
			submissionID := typed.SubmissionID
			if submissionID == 0 {
				return nil
			}
			var title, categoryKey string
			if err := tx.QueryRow(ctx, `SELECT title,category_key FROM submission WHERE id=$1`, submissionID).Scan(&title, &categoryKey); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return err
			}
			data["title"], data["category_name"] = title, categoryName(ctx, tx, categoryKey)
			data["score"], data["reason"] = "待裁定", "两位审核人的单项初审结论不一致，系统已将材料转入裁定流程。"
		case events.ArbitrationResolved:
			typed, err := events.Decode(event, events.ArbitrationResolvedEvent())
			if err != nil {
				return err
			}
			if typed.AppealID != nil && *typed.AppealID > 0 {
				return enrichAppealTemplateData(ctx, tx, *typed.AppealID, data)
			}
			if typed.SubmissionID == nil || *typed.SubmissionID <= 0 {
				return nil
			}
			var title, categoryKey string
			if err := tx.QueryRow(ctx, `SELECT title,category_key FROM submission WHERE id=$1`, *typed.SubmissionID).Scan(&title, &categoryKey); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return err
			}
			data["title"], data["category_name"] = title, categoryName(ctx, tx, categoryKey)
		case events.ScorecardAuditAssigned:
			assignmentID := payloadID(payload, "assignmentId")
			if assignmentID == 0 {
				return nil
			}
			var assignedAt, dueAt time.Time
			err := tx.QueryRow(ctx, `
				SELECT a.assigned_at,
				       a.assigned_at + make_interval(hours => COALESCE((SELECT scorecard_hours FROM review_sla_config WHERE class_id=a.class_id),24))
				  FROM scorecard_audit_assignment a WHERE a.id=$1 AND a.reviewer_id=$2
			`, assignmentID, target.UserID).Scan(&assignedAt, &dueAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			data["assignment_id"] = strconv.FormatInt(assignmentID, 10)
			data["assigned_at"], data["due_at"] = formatMailTime(assignedAt), formatMailTime(dueAt)
		case events.ScorecardAuditCompleted:
			batchID := payloadString(payload, "batchId", "")
			if batchID == "" {
				return nil
			}
			var studentID int64
			var studentName string
			var completedAt, deadline time.Time
			err := tx.QueryRow(ctx, `
				SELECT b.student_id,u.name,b.completed_at,
				       b.completed_at + make_interval(hours => COALESCE((SELECT confirm_hours FROM review_sla_config WHERE class_id=b.class_id),72))
				  FROM scorecard_audit_batch b JOIN app_user u ON u.id=b.student_id WHERE b.id=$1::uuid
			`, batchID).Scan(&studentID, &studentName, &completedAt, &deadline)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			data["student_name"], data["completed_at"], data["confirmation_deadline"] = studentName, formatMailTime(completedAt), formatMailTime(deadline)
			if target.UserID == studentID {
				data["audience_label"] = "你的整表终审已完成"
				data["confirmation_guidance"] = "请在截止时间前核对整表终审结果；无在途申诉或异议时，可在结果页完成确认。逾期将按班级规则自动确认。"
			} else {
				data["audience_label"] = "学生整表终审已完成"
				data["confirmation_guidance"] = "该学生的结果确认计时已经开始。请关注未解决的申诉、异议以及确认状态。"
			}
		case events.ResultConfirmed:
			var source string
			var confirmedAt time.Time
			err := tx.QueryRow(ctx, `
				SELECT source,confirmed_at FROM result_confirmation
				 WHERE student_id=$1 AND batch_id=NULLIF($2,'')::uuid
				 ORDER BY confirmed_at DESC,id DESC LIMIT 1
			`, target.UserID, payloadString(payload, "batchId", "")).Scan(&source, &confirmedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			data["confirmation_source"] = map[bool]string{true: "本人手动确认", false: "到期自动确认"}[source == "manual"]
			data["confirmed_at"] = formatMailTime(confirmedAt)
		case events.WindowReminder:
			var close time.Time
			err := tx.QueryRow(ctx, `
				SELECT (config #>> '{window,close}')::timestamptz FROM scheme
				 WHERE status='published' ORDER BY version DESC LIMIT 1
			`).Scan(&close)
			if err == nil {
				data["window_close"] = formatMailTime(close)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		case events.SealConfirmed:
			var sealedAt time.Time
			var source string
			var submitted, decided int
			err := tx.QueryRow(ctx, `
				SELECT s.sealed_at,s.source,
				       (SELECT count(*) FROM submission x WHERE x.student_id=s.student_id AND x.status<>'draft'),
				       (SELECT count(*) FROM submission x WHERE x.student_id=s.student_id AND x.final_score IS NOT NULL)
				  FROM seal s WHERE s.student_id=$1 AND s.unsealed_at IS NULL ORDER BY s.sealed_at DESC LIMIT 1
			`, target.UserID).Scan(&sealedAt, &source, &submitted, &decided)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			data["sealed_at"], data["submitted_count"], data["decided_count"] = formatMailTime(sealedAt), submitted, decided
			data["source_label"] = map[bool]string{true: "本人确认", false: "系统自动"}[source == "manual"]
		case events.SettlementDone:
			runID := payloadID(payload, "runId")
			if runID == 0 {
				return nil
			}
			var total, schemeName string
			var classRank, classSize, schemeVersion int
			var honor bool
			var settledAt time.Time
			var categoriesRaw []byte
			err := tx.QueryRow(ctx, `
				SELECT st.total_score::text,st.class_rank,
				       (SELECT count(*) FROM settlement x WHERE x.run_id=st.run_id),st.honor,
				       r.completed_at,sc.name,sc.version,st.category_scores
				  FROM settlement st JOIN settlement_run r ON r.id=st.run_id JOIN scheme sc ON sc.id=r.scheme_id
				 WHERE st.run_id=$1 AND st.student_id=$2
			`, runID, target.UserID).Scan(
				&total, &classRank, &classSize, &honor, &settledAt, &schemeName, &schemeVersion, &categoriesRaw,
			)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			data["total_score"], data["class_rank"], data["class_size"] = total, classRank, classSize
			data["honor_label"] = map[bool]string{true: "入选荣誉榜", false: "未入选荣誉榜"}[honor]
			data["settled_at"], data["scheme_name"], data["scheme_version"] = formatMailTime(settledAt), schemeName, schemeVersion
			var categories map[string]any
			if json.Unmarshal(categoriesRaw, &categories) == nil {
				data["score_gpa"] = payloadString(categories, "major", "—")
				data["score_moral"] = payloadString(categories, "moral", "—")
				data["score_practice"] = payloadString(categories, "practice", "—")
				data["score_health"] = payloadString(categories, "health", "—")
			}
		}
		return nil
	})
}

func enrichAppealTemplateData(ctx context.Context, tx pgx.Tx, appealID int64, data map[string]any) error {
	var studentName, studentSID, targetType, reason, status, title, scoreBefore, scoreAfter, resolutionReason string
	var filedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT u.name,u.sid,a.target_type,a.reason,a.created_at,a.status,
		       CASE WHEN a.target_type='submission'
		            THEN COALESCE((SELECT s.title FROM submission s WHERE s.id=a.target_id),'材料')
		            ELSE COALESCE((SELECT b.item_key FROM base_score b WHERE b.id=a.target_id),'计分项') END,
		       CASE WHEN a.target_type='submission'
		            THEN COALESCE((SELECT s.final_score::text FROM submission s WHERE s.id=a.target_id),'')
		            ELSE COALESCE((SELECT b.score::text FROM base_score b WHERE b.id=a.target_id),'') END,
		       COALESCE(a.resolution_score::text,''),COALESCE(a.resolution_reason,'')
		  FROM appeal a JOIN app_user u ON u.id=a.student_id WHERE a.id=$1
	`, appealID).Scan(
		&studentName, &studentSID, &targetType, &reason, &filedAt, &status,
		&title, &scoreBefore, &scoreAfter, &resolutionReason,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	data["student_name"], data["student_sid"] = studentName, studentSID
	data["target_type_label"], data["title"] = targetTypeLabel(targetType), title
	data["filed_at"], data["reason_excerpt"] = formatMailTime(filedAt), markdownMailSummary(reason, 180)
	data["result_label"] = appealStatusLabel(status)
	// The target row already contains the resolved score at this point;
	// showing it as the old value would be misleading.
	data["score_before"], data["score_after"] = "详见平台", scoreAfter
	if resolutionReason != "" {
		data["reason"] = markdownMailSummary(resolutionReason, 180)
	}
	return nil
}

func categoryName(ctx context.Context, tx pgx.Tx, key string) string {
	var name string
	err := tx.QueryRow(ctx, `
		SELECT category->>'name'
		  FROM scheme s CROSS JOIN LATERAL jsonb_array_elements(s.config->'categories') category
		 WHERE s.status='published' AND category->>'key'=$1 ORDER BY s.version DESC LIMIT 1
	`, key).Scan(&name)
	if err != nil || strings.TrimSpace(name) == "" {
		return key
	}
	return name
}

func formatMailTime(value time.Time) string {
	chinaTime := time.FixedZone("CST", 8*60*60)
	return value.In(chinaTime).Format("2006-01-02 15:04")
}

func excerpt(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}

func markdownMailSummary(value string, maxRunes int) string {
	summary := excerpt(richtext.PlainText(value), maxRunes)
	if summary == "" {
		summary = "—"
	}
	return summary + "\n\n完整内容（含附图）请登录平台查看。"
}

var notificationLabels = map[string]string{
	events.SubmissionForceRejected: "材料已被管理员强制驳回，该项认定分已归零",
	events.SubmissionForceScored:   "材料已由管理员强制改分，请查看新认定分与理由",
	events.AppealFiled:             "申诉已收到",
	events.AppealAssigned:          "有新的申诉等待受理", events.AppealResolved: "申诉已有处理结果",
	events.DispatchDone: "审核任务已分发", events.DispatchReassigned: "审核任务已改派",
	events.ReviewDecided: "材料单项初审进度更新", events.ConflictRaised: "材料初审出现分歧，等待裁定",
	events.ArbitrationResolved: "材料单项裁定已完成", events.WindowReminder: "请及时完成并封存材料",
	events.GateForced: "管理员已强制开启结算闸门", events.SealConfirmed: "材料封存成功",
	events.ExportDone: "导出文件已生成", events.SettlementDone: "班级综测结算已完成",
	events.ClassificationSuggested: "有新的归类建议等待处理", events.ClassificationResolved: "申报归类已有处理结果",
	events.ScorecardAuditAssigned: "有新的整表终审任务", events.ScorecardAuditSubmitted: "整表终审已有提交",
	events.ScorecardAuditCompleted: "整表终审已完成，请核对", events.ScorecardAuditStale: "整表终审快照已失效",
	events.ScorecardAuditBlocked: "整表复核因审核人不足而阻塞",
	events.ResultConfirmed:       "整表终审结果已确认",
	events.ObjectionSubmitted:    "有新的分数异议等待处理",
	events.ReviewSLAOverdue:      "有审核任务已经逾期",
}

// These low-frequency events remain visible in the in-app notification and
// audit streams, but intentionally have no outbound email template. Keeping
// the list explicit prevents an unreviewed catch-all SES template from being
// reintroduced as an accidental fallback.
var mailSuppressedEvents = map[string]struct{}{
	events.GateForced:              {},
	events.ExportDone:              {},
	events.ClassificationSuggested: {},
	events.ClassificationResolved:  {},
	events.ScorecardAuditSubmitted: {},
	events.ScorecardAuditStale:     {},
	events.ScorecardAuditBlocked:   {},
	events.ObjectionSubmitted:      {},
}

func submissionCorrectionEvent(event events.Event) events.Kind[events.SubmissionForceRejectedPayload] {
	if event.Type == events.SubmissionForceScored {
		return events.SubmissionForceScoredEvent()
	}
	return events.SubmissionForceRejectedEvent()
}

func notificationText(event events.Event, className, name string) (string, string) {
	label := notificationLabels[event.Type]
	if event.Type == events.SubmissionForceRejected {
		if typed, err := events.Decode(event, submissionCorrectionEvent(event)); err == nil && typed.SelfRejected {
			label = "本人主动强制驳回已生效，该项认定分已归零"
		}
	}
	if label == "" {
		label = "综测状态有更新"
	}
	subject := "[综测] " + label
	body := strings.Join([]string{name + "同学：", "", className + "：" + label + "。", "请登录 EasyGPA Plus 查看完整信息。"}, "\n")
	return subject, body
}

func notificationTemplate(event events.Event, className, name string) (string, map[string]any, error) {
	payload := make(map[string]any)
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", nil, fmt.Errorf("decode %s notification payload: %w", event.Type, err)
	}
	data := map[string]any{
		"name": name, "class_name": className,
	}
	switch event.Type {
	case events.SubmissionForceRejected, events.SubmissionForceScored:
		typed, err := events.Decode(event, submissionCorrectionEvent(event))
		if err != nil {
			return "", nil, err
		}
		data["title"] = typed.Title
		data["score"] = "0"
		data["reason"] = markdownMailSummary(typed.Reason, 180)
		data["ruling_heading"] = "材料已被管理员强制驳回"
		data["ruling_message"] = "管理员复核后已驳回该材料，该项认定分已由 " + optionalFloatString(typed.PreviousScore, "待认定") + " 调整为 0 分。"
		data["ruling_type_label"] = "强制驳回已生效"
		data["action_note"] = "请登录学生页查看驳回理由。该项原有审核流程已结束；如有疑问，请联系班级管理员。"
		if event.Type == events.SubmissionForceScored {
			if typed.Score == nil {
				return "", nil, fmt.Errorf("forced score notification missing score")
			}
			data["score"] = optionalFloatString(typed.Score, "")
			data["ruling_heading"] = "材料已由管理员强制改分"
			data["ruling_message"] = "管理员复核后，该项认定分已由 " + optionalFloatString(typed.PreviousScore, "待认定") + " 调整为 " + optionalFloatString(typed.Score, "") + " 分。"
			data["ruling_type_label"] = "强制改分已生效"
			data["action_note"] = "请登录学生页查看改分理由。原材料和审核记录已保留，该项原有流程已结束；如有疑问，请联系班级管理员。"
		}
		if typed.SelfRejected {
			data["ruling_heading"] = "本人主动强制驳回已生效"
			data["ruling_message"] = "你已主动驳回本人提交的材料，该项认定分已由 " + optionalFloatString(typed.PreviousScore, "待认定") + " 调整为 0 分。"
			data["action_note"] = "原材料、佐证和审核记录均已保留；该项不再受理申诉，也不能恢复原分。"
		}
		return TemplateRulingNotice, data, nil
	case events.ReviewSLAOverdue:
		data["pending_count"] = payloadString(payload, "overdueCount", "—")
		data["new_count"] = payloadString(payload, "overdueCount", "—")
		data["category_name"] = "逾期审核任务"
		data["task_kind"] = "逾期提醒"
		data["task_message"] = "你有审核任务已超过处理时限。请尽快进入工作台处理；如无法继续，请联系班级管理员改派。"
		return TemplateTaskNew, data, nil
	case events.DispatchDone:
		if _, err := events.Decode(event, events.DispatchDoneEvent()); err != nil {
			return "", nil, err
		}
		data["pending_count"] = 1
		data["new_count"] = 1
		data["category_name"] = "全部大项"
		data["task_kind"] = "今日初审任务"
		data["task_message"] = "请在工作台逐项独立审核分配给你的材料。"
		return TemplateTaskNew, data, nil
	case events.DispatchReassigned:
		if _, err := events.Decode(event, events.DispatchReassignedEvent()); err != nil {
			return "", nil, err
		}
		data["pending_count"] = 1
		data["new_count"] = 1
		data["category_name"] = "全部大项"
		data["task_kind"] = "任务已改派"
		data["task_message"] = "请在工作台逐项独立审核分配给你的材料。"
		return TemplateTaskNew, data, nil
	case events.AppealFiled:
		typed, err := events.Decode(event, events.AppealFiledEvent())
		if err != nil {
			return "", nil, err
		}
		data["title"] = "申诉 #" + strconv.FormatInt(typed.AppealID, 10)
		data["filed_at"] = "刚刚"
		return TemplateAppealReceived, data, nil
	case events.AppealAssigned:
		typed, err := events.Decode(event, events.AppealAssignedEvent())
		if err != nil {
			return "", nil, err
		}
		data["title"] = "申诉 #" + strconv.FormatInt(typed.AppealID, 10)
		data["target_type_label"] = targetTypeLabel("")
		data["filed_at"] = "请登录平台查看"
		data["reason_excerpt"] = "请登录管理端查看申诉理由与佐证。"
		return TemplateAppealPending, data, nil
	case events.AppealResolved:
		typed, err := events.Decode(event, events.AppealResolvedEvent())
		if err != nil {
			return "", nil, err
		}
		data["title"] = "申诉 #" + strconv.FormatInt(typed.AppealID, 10)
		data["result_label"] = appealStatusLabel(typed.Status)
		data["score_after"] = optionalFloatString(typed.Score, "—")
		data["reason"] = "完整终裁理由请登录平台查看。"
		return TemplateAppealResolved, data, nil
	case events.ReviewDecided:
		typed, err := events.Decode(event, events.ReviewDecidedEvent())
		if err != nil {
			return "", nil, err
		}
		data["title"] = "材料 #" + strconv.FormatInt(typed.SubmissionID, 10)
		data["decision_label"] = reviewStatusLabel(typed.Status)
		data["basis"] = "完整审核依据请登录平台查看。"
		return TemplateReviewDecided, data, nil
	case events.ConflictRaised:
		typed, err := events.Decode(event, events.ConflictRaisedEvent())
		if err != nil {
			return "", nil, err
		}
		data["title"] = "材料 #" + strconv.FormatInt(typed.SubmissionID, 10)
		data["score"] = "待裁定"
		data["ruling_heading"] = "单项初审出现分歧"
		data["ruling_message"] = "两位审核人的初审结论不一致，材料已自动进入裁定流程。"
		data["ruling_type_label"] = "等待单项裁定"
		data["action_note"] = "你暂时无需操作；裁定完成后平台会再次通知。"
		return TemplateRulingNotice, data, nil
	case events.ArbitrationResolved:
		typed, err := events.Decode(event, events.ArbitrationResolvedEvent())
		if err != nil {
			return "", nil, err
		}
		if typed.AppealID != nil && *typed.AppealID > 0 {
			data["title"] = "申诉 #" + strconv.FormatInt(*typed.AppealID, 10)
			data["result_label"] = "终裁完成"
			data["score_after"] = strconv.FormatFloat(typed.Score, 'f', -1, 64)
			data["reason"] = "完整终裁理由和处理轨迹请登录平台查看。"
			return TemplateAppealResolved, data, nil
		}
		submissionID := int64(0)
		if typed.SubmissionID != nil {
			submissionID = *typed.SubmissionID
		}
		data["title"] = "材料 #" + strconv.FormatInt(submissionID, 10)
		data["score"] = strconv.FormatFloat(typed.Score, 'f', -1, 64)
		reason := typed.Reason
		if strings.TrimSpace(reason) == "" {
			reason = "完整终裁理由请登录平台查看。"
		}
		data["reason"] = markdownMailSummary(reason, 180)
		data["ruling_heading"] = "单项裁定已完成"
		data["ruling_message"] = "你申报的材料已有单项裁定结果，实时成绩已更新。"
		data["ruling_type_label"] = "单项裁定生效"
		data["action_note"] = "请登录平台核对裁定理由及当前申诉状态。"
		return TemplateRulingNotice, data, nil
	case events.ScorecardAuditAssigned:
		data["assignment_id"] = payloadString(payload, "assignmentId", "—")
		return TemplateScorecardTask, data, nil
	case events.ScorecardAuditCompleted:
		data["student_name"] = "相关学生"
		data["audience_label"] = "整表终审已完成"
		data["confirmation_guidance"] = "请登录平台核对终审结果与确认状态。"
		return TemplateFinalReviewReady, data, nil
	case events.ResultConfirmed:
		data["confirmation_source"] = map[bool]string{true: "本人手动确认", false: "到期自动确认"}[payloadString(payload, "source", "auto") == "manual"]
		data["confirmed_at"] = "刚刚"
		return TemplateResultConfirmed, data, nil
	case events.WindowReminder:
		data["days_left"] = payloadString(payload, "daysRemaining", "请尽快处理")
		data["window_close"] = payloadString(payload, "windowClose", "请登录平台查看")
		return TemplateWindowReminder, data, nil
	case events.SealConfirmed:
		data["sealed_at"] = "刚刚"
		data["source_label"] = map[bool]string{true: "本人确认", false: "系统自动"}[payloadString(payload, "source", "auto") == "manual"]
		data["submitted_count"] = payloadString(payload, "submitted", "—")
		return TemplateSealConfirmed, data, nil
	case events.SettlementDone:
		data["class_size"] = payloadString(payload, "students", "—")
		data["settled_at"] = "刚刚"
		return TemplateSettlementDone, data, nil
	default:
		return "", nil, nil
	}
}

func optionalFloatString(value *float64, fallback string) string {
	if value == nil {
		return fallback
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func (w *Worker) dispatchDigestDelay(ctx context.Context, event events.Event) (time.Duration, error) {
	var end time.Time
	err := store.InTenantTx(ctx, w.pool, event.ClassID, func(tx pgx.Tx) error {
		_, value, err := dispatchDigestWindow(ctx, tx, event.ID)
		end = value
		return err
	})
	if err != nil {
		return 0, err
	}
	if delay := time.Until(end); delay > 0 {
		return delay, nil
	}
	return 0, nil
}

func dispatchDigestWindow(ctx context.Context, tx pgx.Tx, eventID string) (time.Time, time.Time, error) {
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM outbox_event WHERE id=$1::uuid`, eventID).Scan(&createdAt); err != nil {
		return time.Time{}, time.Time{}, err
	}
	start, end := dispatchDigestWindowAt(createdAt)
	return start, end, nil
}

func dispatchDigestWindowAt(createdAt time.Time) (time.Time, time.Time) {
	local := createdAt.In(shanghaiLocation)
	end := time.Date(local.Year(), local.Month(), local.Day(), 8, 30, 0, 0, shanghaiLocation)
	if !local.Before(end) {
		end = end.Add(24 * time.Hour)
	}
	return end.Add(-24 * time.Hour), end
}

func payloadString(payload map[string]any, key, fallback string) string {
	value, ok := payload[key]
	if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
		return fallback
	}
	return fmt.Sprint(value)
}

func targetTypeLabel(value string) string {
	label := map[string]string{
		"submission": "材料认定", "base_score": "基础分", "penalty_score": "扣分项",
	}[value]
	if label == "" {
		return "综测条目"
	}
	return label
}

func appealStatusLabel(value string) string {
	if value == "final" {
		return "终裁完成"
	}
	return "本轮申诉已处理"
}

func reviewStatusLabel(value string) string {
	label := map[string]string{
		"scored": "单项初审已形成结论", "consensus": "等待另一位审核人", "arbitrating": "等待单项裁定",
	}[value]
	if label == "" {
		return "已处理"
	}
	return label
}

func uniqueIDs(values []int64) []int64 {
	seen := make(map[int64]bool)
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value > 0 && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
