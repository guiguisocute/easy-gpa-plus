package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
)

type reviewProgressRow struct {
	ID                       string
	Kind                     string
	Status                   string
	StudentID                string
	Student                  string
	Title                    string
	Reviewers                []any
	Expected                 int
	Submitted                int
	AssignedAt               *time.Time
	WaitingSeconds           int64
	Overdue                  bool
	DetailURL                string
	BatchID                  string
	CompletionMode           string
	SubmissionID             string
	FinalScore               *float64
	ForceRejection           json.RawMessage
	CanForceReject           bool
	ForceRejectBlockedReason string
}

type reviewProgressTrailEvent struct {
	At     time.Time `json:"at"`
	Event  string    `json:"event"`
	Detail string    `json:"detail"`
}

func (s *Server) adminReviewProgress(c *gin.Context) {
	page := positiveQueryInt(c, "page", 1, 1, 100000)
	pageSize := positiveQueryInt(c, "page_size", 25, 1, 100)
	kindFilter := strings.TrimSpace(c.Query("kind"))
	statusFilter := strings.TrimSpace(c.Query("status"))
	searchFilter := strings.TrimSpace(c.Query("q"))
	studentFilter := strings.TrimSpace(c.Query("student"))
	reviewerFilter := strings.TrimSpace(c.Query("reviewer"))
	tx := mustTx(c)
	if err := clearSelfReviewAssignments(c.Request.Context(), tx); err != nil {
		writeServiceError(c, err)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	itemSLA, scorecardSLA := 24, 24
	_ = tx.QueryRow(c.Request.Context(), `SELECT item_hours,scorecard_hours FROM review_sla_config WHERE class_id=$1`, mustActor(c).ClassID).Scan(&itemSLA, &scorecardSLA)
	rows := make([]reviewProgressRow, 0)

	// Submission scheme IDs identify frozen rules, not a new submission round.
	// Like dispatch and the student's list, include every submitted record in
	// the RLS-isolated class after publishing another scheme version.
	itemRows, err := tx.Query(c.Request.Context(), `
		SELECT s.id,s.title,student.sid,student.name,s.status,s.submitted_at,
		       count(DISTINCT sr.reviewer_id)::integer,
		       count(DISTINCT r.reviewer_id)::integer,
		       COALESCE((SELECT jsonb_agg(jsonb_build_object('id',u.id::text,'sid',u.sid,'name',u.name,
		          'submitted',EXISTS (SELECT 1 FROM review rr WHERE rr.submission_id=s.id AND rr.reviewer_id=u.id AND rr.superseded_at IS NULL)) ORDER BY u.sid)
		          FROM submission_reviewer ssr JOIN app_user u ON u.id=ssr.reviewer_id
		         WHERE ssr.submission_id=s.id AND ssr.active),'[]'),
		       EXISTS (SELECT 1 FROM review rr WHERE rr.submission_id=s.id AND rr.superseded_at IS NULL),
		       EXISTS (SELECT 1 FROM submission_reviewer ssr WHERE ssr.submission_id=s.id AND ssr.active),
		       s.student_id,s.final_score::float8,s.force_rejection,min(sr.assigned_at)
		  FROM submission s
		  JOIN app_user student ON student.id=s.student_id
		  LEFT JOIN submission_reviewer sr ON sr.submission_id=s.id AND sr.active
		  LEFT JOIN review r ON r.submission_id=s.id AND r.superseded_at IS NULL
		 WHERE s.status<>'draft'
		   AND ($1='' OR strpos(lower(concat_ws(' ',student.sid,student.name,s.title,s.markdown_note,s.rule_snapshot#>>'{item,name}')),lower($1))>0)
		 GROUP BY s.id,s.title,student.sid,student.name,s.status,s.submitted_at
		 ORDER BY s.submitted_at NULLS LAST,s.id
	`, searchFilter)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for itemRows.Next() {
		var id int64
		var title, sid, student, businessStatus string
		var submittedAt *time.Time
		var expected, submitted int
		var reviewersRaw []byte
		var hasReview, hasAssignment bool
		var studentUserID int64
		var finalScore *float64
		var rejectionRaw []byte
		var assignedAt *time.Time
		if err := itemRows.Scan(&id, &title, &sid, &student, &businessStatus, &submittedAt, &expected, &submitted, &reviewersRaw, &hasReview, &hasAssignment, &studentUserID, &finalScore, &rejectionRaw, &assignedAt); err != nil {
			itemRows.Close()
			writeServiceError(c, err)
			return
		}
		var reviewers []any
		_ = json.Unmarshal(reviewersRaw, &reviewers)
		status := "complete"
		switch {
		case businessStatus == "scored" || businessStatus == "locked":
			status = "complete"
		case !hasAssignment:
			status = "unassigned"
		case businessStatus == "arbitrating":
			status = "pending_admin"
		case submitted == 0:
			status = "0/2"
		case submitted < expected:
			status = "1/2"
		default:
			status = "pending_admin"
		}
		canReject, blockedReason := forceRejectPermission(mustActor(c), studentUserID, businessStatus, len(rejectionRaw) > 0)
		if current.Config.Window.LockedAt(time.Now()) {
			canReject, blockedReason = false, "本学期已全系统封锁，请先调整班级时间线"
		}
		rows = append(rows, reviewProgressRow{ID: "item:" + strconv.FormatInt(id, 10), Kind: "item", Status: status, StudentID: sid, Student: student, Title: title, Reviewers: reviewers, Expected: expected, Submitted: submitted, AssignedAt: assignedAt, DetailURL: "?v=admSubs&submission=" + strconv.FormatInt(id, 10), SubmissionID: strconv.FormatInt(id, 10), FinalScore: finalScore, ForceRejection: rejectionRaw, CanForceReject: canReject, ForceRejectBlockedReason: blockedReason})
		_ = hasReview
	}
	if err := itemRows.Err(); err != nil {
		itemRows.Close()
		writeServiceError(c, err)
		return
	}
	itemRows.Close()

	filtered := rows[:0]
	for _, row := range rows {
		if kindFilter != "" && kindFilter != row.Kind {
			continue
		}
		if statusFilter != "" && statusFilter != "all" && statusFilter != "unfinished" && statusFilter != "overdue" && statusFilter != row.Status {
			continue
		}
		if statusFilter == "unfinished" && row.Status == "complete" {
			continue
		}
		if studentFilter != "" && !strings.Contains(strings.ToLower(row.StudentID+" "+row.Student), strings.ToLower(studentFilter)) {
			continue
		}
		if reviewerFilter != "" {
			raw, _ := json.Marshal(row.Reviewers)
			if !strings.Contains(strings.ToLower(string(raw)), strings.ToLower(reviewerFilter)) {
				continue
			}
		}
		if row.AssignedAt != nil {
			row.WaitingSeconds = maxInt64(0, int64(time.Since(*row.AssignedAt).Seconds()))
			slaHours := itemSLA
			if row.Kind == "scorecard" {
				slaHours = scorecardSLA
			}
			row.Overdue = row.WaitingSeconds >= int64(slaHours)*60*60 && row.Status != "complete"
		}
		if statusFilter == "overdue" && !row.Overdue {
			continue
		}
		filtered = append(filtered, row)
	}
	total := len(filtered)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := make([]gin.H, 0, end-start)
	for _, row := range filtered[start:end] {
		trail, err := loadReviewProgressTrail(c.Request.Context(), tx, row)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": row.ID, "kind": row.Kind, "status": row.Status, "studentId": row.StudentID, "student": row.Student, "title": row.Title, "reviewers": row.Reviewers, "expected": row.Expected, "submitted": row.Submitted, "assignedAt": row.AssignedAt, "waitingSeconds": row.WaitingSeconds, "overdue": row.Overdue, "detailUrl": row.DetailURL, "batchId": row.BatchID, "completionMode": row.CompletionMode, "trail": trail, "submissionId": row.SubmissionID, "finalScore": row.FinalScore, "forceRejection": row.ForceRejection, "canForceReject": row.CanForceReject, "forceRejectBlockedReason": row.ForceRejectBlockedReason})
	}
	if err := appendAudit(c, tx, "review_progress.read", "review", "", nil, nil, map[string]any{"kind": kindFilter, "status": statusFilter, "page": page, "pageSize": pageSize}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

func loadReviewProgressTrail(ctx context.Context, tx pgx.Tx, row reviewProgressRow) ([]reviewProgressTrailEvent, error) {
	var rows pgx.Rows
	var err error
	if row.Kind == "item" {
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(row.ID, "item:"), 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err = tx.Query(ctx, `
			SELECT at,event,detail FROM (
			 SELECT s.submitted_at AS at,'学生提交'::text AS event,''::text AS detail FROM submission s WHERE s.id=$1
			 UNION ALL
			 SELECT sr.assigned_at,'分配审核席位',u.name||'（'||u.sid||'）' FROM submission_reviewer sr JOIN app_user u ON u.id=sr.reviewer_id WHERE sr.submission_id=$1
			 UNION ALL
			 SELECT r.created_at,'提交单项结论',u.name||'（'||u.sid||'）' FROM review r JOIN app_user u ON u.id=r.reviewer_id WHERE r.submission_id=$1 AND r.superseded_at IS NULL
			 UNION ALL
			 SELECT (s.force_rejection->>'rejectedAt')::timestamptz,CASE WHEN s.force_rejection->>'selfRejected'='true' THEN '本人主动强制驳回' ELSE '管理员强制驳回' END,s.force_rejection->>'reason' FROM submission s WHERE s.id=$1 AND s.force_rejection IS NOT NULL
			) event_rows WHERE at IS NOT NULL ORDER BY at,event
		`, id)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT at,event,detail FROM (
			 SELECT b.created_at AS at,'生成整表轮次'::text AS event,''::text AS detail FROM scorecard_audit_batch b WHERE b.id=$1::uuid
			 UNION ALL
			 SELECT b.opened_at,'开放整表复核','' FROM scorecard_audit_batch b WHERE b.id=$1::uuid
			 UNION ALL
			 SELECT a.assigned_at,'分配整表席位',u.name||'（'||u.sid||'）' FROM scorecard_audit_assignment a JOIN scorecard_audit_subject subject ON subject.id=a.subject_id JOIN app_user u ON u.id=a.reviewer_id WHERE subject.batch_id=$1::uuid
			 UNION ALL
			 SELECT a.submitted_at,'提交整表结论',u.name||'（'||u.sid||'）' FROM scorecard_audit_assignment a JOIN scorecard_audit_subject subject ON subject.id=a.subject_id JOIN app_user u ON u.id=a.reviewer_id WHERE subject.batch_id=$1::uuid
			 UNION ALL
			 SELECT b.completed_at,'整表复核完成',COALESCE(b.detail->>'completionMode','') FROM scorecard_audit_batch b WHERE b.id=$1::uuid
			) event_rows WHERE at IS NOT NULL ORDER BY at,event
		`, row.BatchID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trail := make([]reviewProgressTrailEvent, 0)
	for rows.Next() {
		var event reviewProgressTrailEvent
		if err := rows.Scan(&event.At, &event.Event, &event.Detail); err != nil {
			return nil, err
		}
		trail = append(trail, event)
	}
	return trail, rows.Err()
}

func positiveQueryInt(c *gin.Context, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(c.Query(key))
	if err != nil || value < minimum {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Server) remindOverdueReviews(c *gin.Context) {
	var input struct {
		Kind     string `json:"kind"`
		Search   string `json:"q"`
		Student  string `json:"student"`
		Reviewer string `json:"reviewer"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "提醒筛选参数不正确", nil)
		return
	}
	input.Kind, input.Student, input.Reviewer = strings.TrimSpace(input.Kind), strings.TrimSpace(input.Student), strings.TrimSpace(input.Reviewer)
	input.Search = strings.TrimSpace(input.Search)
	tx, actor := mustTx(c), mustActor(c)
	rows, err := tx.Query(c.Request.Context(), `
		WITH cfg AS (
		 SELECT COALESCE((SELECT item_hours FROM review_sla_config WHERE class_id=$1),24) AS item_hours
		), overdue AS (
		 SELECT 'item'::text AS kind,sr.reviewer_id,student.sid,student.name
		   FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id JOIN app_user student ON student.id=s.student_id,cfg
		  WHERE sr.active AND s.status IN ('pending','consensus')
		    AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=sr.reviewer_id AND r.superseded_at IS NULL)
		    AND sr.assigned_at + make_interval(hours => cfg.item_hours)<=now()
		    AND ($5='' OR strpos(lower(concat_ws(' ',student.sid,student.name,s.title,s.markdown_note,s.rule_snapshot#>>'{item,name}')),lower($5))>0)
		)
		SELECT overdue.reviewer_id,count(*)::integer FROM overdue
		 JOIN app_user reviewer ON reviewer.id=overdue.reviewer_id
		 WHERE ($2='' OR kind=$2) AND ($3='' OR strpos(lower(overdue.sid||' '||overdue.name),lower($3))>0)
		   AND ($4='' OR lower(reviewer.sid||' '||reviewer.name) LIKE '%'||lower($4)||'%')
		 GROUP BY overdue.reviewer_id ORDER BY overdue.reviewer_id
	`, actor.ClassID, input.Kind, input.Student, input.Reviewer, input.Search)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	recipients := make([]int64, 0)
	overdueCount := 0
	for rows.Next() {
		var reviewerID int64
		var count int
		if err := rows.Scan(&reviewerID, &count); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		recipients = append(recipients, reviewerID)
		overdueCount += count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if overdueCount > 0 {
		if err := enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.ReviewSLAOverdue, map[string]any{"recipientIds": recipients, "overdueCount": overdueCount, "manual": true, "triggeredBy": actor.UserID}); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "review_sla.reminder_requested", "review", "", nil, nil, map[string]any{"kind": input.Kind, "student": input.Student, "reviewer": input.Reviewer, "recipientCount": len(recipients), "overdueCount": overdueCount}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"recipientCount": len(recipients), "overdueCount": overdueCount})
}
