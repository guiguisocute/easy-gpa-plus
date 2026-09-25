package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/dispatch"
	"easygpa/backend/internal/events"
)

type dispatchInput struct {
	Seed            int64 `json:"seed,string,omitempty"`
	AvoidSelf       *bool `json:"avoidSelf,omitempty"`
	IncludeAssigned bool  `json:"includeAssigned,omitempty"`
}

type dispatchRunResult struct {
	dispatch.Plan
	Assigned int `json:"assigned"`
}

func dispatchOptions(input dispatchInput) (int64, bool) {
	if input.Seed == 0 {
		input.Seed = randomSeed()
	}
	return input.Seed, true
}

// 工作量池的范围是「本班尚未结算的提交」，不是「当前方案的提交」。
//
// 这两者曾经是同一件事，直到方案发了第二版：publishScheme 把一份 draft 行改成
// published 并涨版本号，但它不迁移 submission——submission.scheme_id 在提交那一刻
// 就钉死了。于是旧提交在发布新版的瞬间从这个池子里整体消失，而审核人那边照旧看得见
// （/review/tasks 全程没有方案过滤），也照旧能审：条目自带 rule_snapshot，判的是提交时
// 冻结的那份规则，换一版方案并不会让它失效。管理员因此看不到已经派出去的活。
//
// 现在用 status 划边界：结算把 scored 全部置成 locked（settlement_handlers.go:624），
// 所以 locked 即"这一轮已经收口"，下一轮自然从零开始算，累计量不会跨轮无限堆积。
// draft 排除掉是因为提交可以被撤回重编辑，撤回之后不该继续占着谁的工作量。
// 班级隔离由 RLS 负责（store/db.go:94），这里不需要也不该再写一遍 class_id。
//
// 三个数都只认 active 的分配行。「累计」以前不加这个条件，于是重排（includeAssigned）
// 每跑一次就虚增一轮：runDispatch 把旧行置 active=false 再插新行，失效行却仍被计数，
// 每条提交每次 +2，累计量滚雪球、进度条拉满、极差失真，而 Balance 又拿累计量当第一
// 排序键，于是越重排派得越偏。加上 sr.active 之后这个数就是「我现在手上背着多少」，
// 且恒等于「待审 + 已评」——已交结论的分配行不会被停用（改派与重排都跳过已评条目）。
func loadDispatchPool(ctx context.Context, tx pgx.Tx) ([]dispatch.Reviewer, map[int64]dispatch.Load, error) {
	rows, err := tx.Query(ctx, `
		SELECT u.id,u.sid,u.name,u.role,u.dispatch_paused,
		       (SELECT count(*) FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id
		         WHERE sr.reviewer_id=u.id AND s.status NOT IN ('draft','locked') AND sr.active),
		       (SELECT count(*) FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id
		         WHERE sr.reviewer_id=u.id AND s.status NOT IN ('draft','locked') AND sr.active
		           AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=sr.submission_id
		                            AND r.reviewer_id=u.id AND r.superseded_at IS NULL)),
		       (SELECT count(*) FROM review r JOIN submission s ON s.id=r.submission_id
		         WHERE r.reviewer_id=u.id AND s.status NOT IN ('draft','locked') AND r.superseded_at IS NULL)
		  FROM app_user u
		 WHERE u.status='active' AND u.role IN ('group','class_admin')
		 ORDER BY u.id
	`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	pool := make([]dispatch.Reviewer, 0)
	loads := make(map[int64]dispatch.Load)
	for rows.Next() {
		var reviewer dispatch.Reviewer
		if err := rows.Scan(&reviewer.UserID, &reviewer.SID, &reviewer.Name, &reviewer.Role, &reviewer.Paused,
			&reviewer.Assigned, &reviewer.Pending, &reviewer.Done); err != nil {
			return nil, nil, err
		}
		pool = append(pool, reviewer)
		loads[reviewer.UserID] = dispatch.Load{Assigned: reviewer.Assigned, Pending: reviewer.Pending}
	}
	return pool, loads, rows.Err()
}

// 待分发的条目同样不按方案筛：'pending'/'consensus' 已经把 draft 和结算完的排除干净了，
// 再叠一层 scheme_id 只会让上一版方案里还没派出去的条目永远等不到人。
func loadDispatchTargets(ctx context.Context, tx pgx.Tx, includeAssigned bool) ([]dispatch.Submission, error) {
	rows, err := tx.Query(ctx, `
		SELECT s.id,s.student_id,s.title,student.name,s.submitted_at,
		       COALESCE(array_agg(DISTINCT r.reviewer_id) FILTER (WHERE r.reviewer_id IS NOT NULL),'{}'::bigint[]),
		       COALESCE((SELECT array_agg(sr.reviewer_id) FROM submission_reviewer sr
		                  WHERE sr.submission_id=s.id AND sr.active),'{}'::bigint[])
		  FROM submission s
		  JOIN app_user student ON student.id=s.student_id
		  LEFT JOIN review r ON r.submission_id=s.id AND r.superseded_at IS NULL
		 WHERE s.status IN ('pending','consensus')
		   AND (($1 AND NOT EXISTS (
		          SELECT 1 FROM review current_review
		           WHERE current_review.submission_id=s.id AND current_review.superseded_at IS NULL
		        )) OR (NOT $1 AND NOT EXISTS (
		          SELECT 1 FROM submission_reviewer active_assignment
		           WHERE active_assignment.submission_id=s.id AND active_assignment.active
		        )))
		 GROUP BY s.id,student.name
		 ORDER BY s.submitted_at,s.id
	`, includeAssigned)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]dispatch.Submission, 0)
	for rows.Next() {
		var item dispatch.Submission
		var reviewedBy, assignedTo []int64
		if err := rows.Scan(&item.ID, &item.StudentID, &item.Title, &item.Student, &item.SubmittedAt, &reviewedBy, &assignedTo); err != nil {
			return nil, err
		}
		item.ReviewedBy = make(map[int64]bool, len(reviewedBy))
		item.AssignedReviewers = make(map[int64]bool)
		if includeAssigned {
			item.ReplacedReviewers = assignedTo
		}
		for _, reviewerID := range reviewedBy {
			item.ReviewedBy[reviewerID] = true
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func buildDispatchPlan(ctx context.Context, tx pgx.Tx, input dispatchInput) (dispatch.Plan, int64, error) {
	current, err := loadCurrentScheme(ctx, tx)
	if err != nil {
		return dispatch.Plan{}, 0, err
	}
	pool, loads, err := loadDispatchPool(ctx, tx)
	if err != nil {
		return dispatch.Plan{}, 0, err
	}
	targets, err := loadDispatchTargets(ctx, tx, input.IncludeAssigned)
	if err != nil {
		return dispatch.Plan{}, 0, err
	}
	seed, avoidSelf := dispatchOptions(input)
	plan, err := dispatch.Balance(targets, pool, loads, seed, avoidSelf)
	return plan, current.ID, err
}

func (s *Server) currentDispatch(c *gin.Context) {
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	pool, _, err := loadDispatchPool(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var automatic bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT dispatch_auto FROM class WHERE id=$1`, mustActor(c).ClassID).Scan(&automatic); err != nil {
		writeServiceError(c, err)
		return
	}
	var unassigned int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM submission s
		 WHERE s.status IN ('pending','consensus')
		   AND NOT EXISTS (SELECT 1 FROM submission_reviewer sr WHERE sr.submission_id=s.id AND sr.active)
	`).Scan(&unassigned); err != nil {
		writeServiceError(c, err)
		return
	}
	seed, avoidSelf := int64(0), true
	var lastRunAt *time.Time
	var lastRunBy *string
	hasRun := true
	err = tx.QueryRow(c.Request.Context(), `
		SELECT d.random_seed,d.avoid_self,d.created_at,u.name
		  FROM dispatch_run d LEFT JOIN app_user u ON u.id=d.created_by
		 WHERE d.scheme_id=$1 ORDER BY d.created_at DESC,d.id DESC LIMIT 1
	`, current.ID).Scan(&seed, &avoidSelf, &lastRunAt, &lastRunBy)
	if errors.Is(err, pgx.ErrNoRows) {
		hasRun = false
	} else if err != nil {
		writeServiceError(c, err)
		return
	}
	seedText := ""
	if hasRun {
		seedText = strconv.FormatInt(seed, 10)
	}
	assignedTotal, pendingTotal := 0, 0
	minimum, maximum := 0, 0
	for index := range pool {
		assignedTotal += pool[index].Assigned
		pendingTotal += pool[index].Pending
		if index == 0 || pool[index].Assigned < minimum {
			minimum = pool[index].Assigned
		}
		if index == 0 || pool[index].Assigned > maximum {
			maximum = pool[index].Assigned
		}
	}
	if err := appendAudit(c, tx, "dispatch.read", "dispatch_run", "", nil, nil, map[string]any{"reviewers": len(pool)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"policy": dispatch.Policy, "seed": seedText, "avoidSelf": avoidSelf, "auto": automatic,
		"reviewers": pool, "unassigned": unassigned, "assignedTotal": assignedTotal, "pendingTotal": pendingTotal,
		"spread": maximum - minimum, "lastRunAt": lastRunAt, "lastRunBy": lastRunBy,
	})
}

func (s *Server) previewDispatch(c *gin.Context) {
	var input dispatchInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "分发参数不正确", nil)
		return
	}
	plan, _, err := buildDispatchPlan(c.Request.Context(), mustTx(c), input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "dispatch_invalid", err.Error(), nil)
		return
	}
	if err := appendAudit(c, mustTx(c), "dispatch.previewed", "dispatch_run", "", nil, nil,
		map[string]any{"seed": plan.Seed, "targets": plan.Targets, "includeAssigned": input.IncludeAssigned}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, plan)
}

func (s *Server) runDispatch(c *gin.Context) {
	var input dispatchInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "分发参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if _, err := tx.Exec(c.Request.Context(), `SELECT pg_advisory_xact_lock($1)`, dispatch.ClassLockKey(actor.ClassID)); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := clearSelfReviewAssignments(c.Request.Context(), tx); err != nil {
		writeServiceError(c, err)
		return
	}
	plan, schemeID, err := buildDispatchPlan(c.Request.Context(), tx, input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "dispatch_invalid", err.Error(), nil)
		return
	}
	trigger := "manual"
	if input.IncludeAssigned {
		trigger = "rebalance"
	}
	var runID int64
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO dispatch_run (class_id,scheme_id,policy,random_seed,avoid_self,trigger,assigned,blocked,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id
	`, actor.ClassID, schemeID, dispatch.Policy, plan.Seed, plan.AvoidSelf, trigger,
		len(plan.Assignments), len(plan.Blocked), actor.UserID).Scan(&runID); err != nil {
		writeServiceError(c, err)
		return
	}
	reviewerSet := make(map[int64]bool)
	for _, assignment := range plan.Assignments {
		if input.IncludeAssigned {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE submission_reviewer SET active=false WHERE submission_id=$1 AND active`, assignment.SubmissionID); err != nil {
				writeServiceError(c, err)
				return
			}
		}
		for position, reviewerID := range assignment.Reviewers {
			if _, err := tx.Exec(c.Request.Context(), `
				INSERT INTO submission_reviewer (class_id,submission_id,reviewer_id,position,run_id)
				VALUES ($1,$2,$3,$4,$5)
			`, actor.ClassID, assignment.SubmissionID, reviewerID, position+1, runID); err != nil {
				writeServiceError(c, err)
				return
			}
			reviewerSet[reviewerID] = true
		}
	}
	if err := appendAudit(c, tx, "dispatch.completed", "dispatch_run", strconv.FormatInt(runID, 10), nil, plan,
		map[string]any{"schemeId": schemeID, "trigger": trigger, "includeAssigned": input.IncludeAssigned}); err != nil {
		writeServiceError(c, err)
		return
	}
	reviewerIDs := sortedIDStrings(reviewerSet)
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.DispatchDoneEvent(), events.DispatchDonePayload{
		RunID: strconv.FormatInt(runID, 10), Trigger: trigger, Assigned: len(plan.Assignments),
		Blocked: len(plan.Blocked), ReviewerIDs: reviewerIDs,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, dispatchRunResult{Plan: plan, Assigned: len(plan.Assignments)})
}

func (s *Server) updateDispatchAuto(c *gin.Context) {
	var input struct {
		On *bool `json:"on"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.On == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "自动分发参数不正确", nil)
		return
	}
	if !*input.On {
		writeError(c, http.StatusConflict, "auto_dispatch_required", "自动分发已固定开启，不能关闭", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var before bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT dispatch_auto FROM class WHERE id=$1 FOR UPDATE`, actor.ClassID).Scan(&before); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE class SET dispatch_auto=$1,updated_at=now() WHERE id=$2`, *input.On, actor.ClassID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "dispatch.auto_changed", "class", strconv.FormatInt(actor.ClassID, 10),
		map[string]any{"auto": before}, map[string]any{"auto": *input.On}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"auto": *input.On})
}

func (s *Server) updateDispatchReviewer(c *gin.Context) {
	uid, ok := dispatchPathID(c, "uid")
	if !ok {
		return
	}
	var input struct {
		Paused *bool `json:"paused"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Paused == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "审核人暂停参数不正确", nil)
		return
	}
	tx := mustTx(c)
	command, err := tx.Exec(c.Request.Context(), `
		UPDATE app_user SET dispatch_paused=$1,updated_at=now()
		 WHERE id=$2 AND status='active' AND role IN ('group','class_admin')
	`, *input.Paused, uid)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if command.RowsAffected() == 0 {
		writeError(c, http.StatusNotFound, "reviewer_not_found", "审核人不存在或已停用", nil)
		return
	}
	pool, _, err := loadDispatchPool(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var result *dispatch.Reviewer
	for index := range pool {
		if pool[index].UserID == uid {
			copy := pool[index]
			result = &copy
			break
		}
	}
	if err := appendAudit(c, tx, "dispatch.reviewer_paused", "app_user", strconv.FormatInt(uid, 10), nil,
		map[string]any{"paused": *input.Paused}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if *input.Paused {
		if _, err := reassignUnavailableSubmissionReviewer(c.Request.Context(), tx, actorClassID(c), uid); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, result)
}

func actorClassID(c *gin.Context) int64 {
	return mustActor(c).ClassID
}

// reassignUnavailableSubmissionReviewer hands over only an unsubmitted
// ordinary-review seat. The original assigned_at is preserved so a takeover
// cannot silently reset the task's SLA clock.
func reassignUnavailableSubmissionReviewer(ctx context.Context, tx pgx.Tx, classID, reviewerID int64) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT sr.id,sr.submission_id,sr.position,sr.assigned_at,s.student_id,s.title,s.submitted_at,s.scheme_id
		  FROM submission_reviewer sr
		  JOIN submission s ON s.id=sr.submission_id
		 WHERE sr.class_id=$1 AND sr.reviewer_id=$2 AND sr.active
		   AND s.status IN ('pending','consensus')
		   AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=$2 AND r.superseded_at IS NULL)
		 ORDER BY sr.assigned_at,sr.id
	`, classID, reviewerID)
	if err != nil {
		return 0, err
	}
	type seat struct {
		assignmentID, submissionID, studentID, schemeID int64
		position                                        int
		assignedAt                                      time.Time
		title                                           string
		submittedAt                                     time.Time
	}
	seats := make([]seat, 0)
	for rows.Next() {
		var value seat
		if err := rows.Scan(&value.assignmentID, &value.submissionID, &value.position, &value.assignedAt, &value.studentID, &value.title, &value.submittedAt, &value.schemeID); err != nil {
			rows.Close()
			return 0, err
		}
		seats = append(seats, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	reassigned := 0
	for _, value := range seats {
		pool, loads, err := loadDispatchPool(ctx, tx)
		if err != nil {
			return reassigned, err
		}
		assigned := make(map[int64]bool)
		reviewed := make(map[int64]bool)
		peerRows, err := tx.Query(ctx, `SELECT reviewer_id FROM submission_reviewer WHERE submission_id=$1 AND active`, value.submissionID)
		if err != nil {
			return reassigned, err
		}
		for peerRows.Next() {
			var id int64
			if err := peerRows.Scan(&id); err != nil {
				peerRows.Close()
				return reassigned, err
			}
			assigned[id] = true
		}
		peerRows.Close()
		reviewRows, err := tx.Query(ctx, `SELECT reviewer_id FROM review WHERE submission_id=$1 AND superseded_at IS NULL`, value.submissionID)
		if err != nil {
			return reassigned, err
		}
		for reviewRows.Next() {
			var id int64
			if err := reviewRows.Scan(&id); err != nil {
				reviewRows.Close()
				return reassigned, err
			}
			reviewed[id] = true
		}
		reviewRows.Close()
		var seed int64
		seedErr := tx.QueryRow(ctx, `SELECT random_seed FROM dispatch_run WHERE scheme_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, value.schemeID).Scan(&seed)
		if errors.Is(seedErr, pgx.ErrNoRows) {
			seed = value.submissionID
		} else if seedErr != nil {
			return reassigned, seedErr
		}
		selected, ok, err := dispatch.PickOne(dispatch.Submission{
			ID: value.submissionID, StudentID: value.studentID, Title: value.title, SubmittedAt: value.submittedAt,
			ReviewedBy: reviewed, AssignedReviewers: assigned,
		}, pool, loads, seed, true)
		if err != nil {
			return reassigned, err
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE submission_reviewer SET active=false WHERE id=$1 AND active`, value.assignmentID); err != nil {
			return reassigned, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO submission_reviewer (class_id,submission_id,reviewer_id,position,assigned_at)
			VALUES ($1,$2,$3,$4,$5)
		`, classID, value.submissionID, selected, value.position, value.assignedAt); err != nil {
			return reassigned, err
		}
		reassigned++
	}
	return reassigned, nil
}

func (s *Server) dispatchReviewerQueue(c *gin.Context) {
	uid, ok := dispatchPathID(c, "uid")
	if !ok {
		return
	}
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT s.id,s.title,student.name,student.sid,s.category_key,s.submitted_at,
		       (SELECT peer_user.name FROM submission_reviewer peer
		         JOIN app_user peer_user ON peer_user.id=peer.reviewer_id
		        WHERE peer.submission_id=s.id AND peer.active AND peer.reviewer_id<>$1
		        ORDER BY peer.position LIMIT 1),
		       EXISTS (SELECT 1 FROM review peer_review
		                WHERE peer_review.submission_id=s.id AND peer_review.reviewer_id<>$1
		                  AND peer_review.superseded_at IS NULL)
		  FROM submission_reviewer sr
		  JOIN submission s ON s.id=sr.submission_id
		  JOIN app_user student ON student.id=s.student_id
		 WHERE sr.reviewer_id=$1 AND sr.active AND s.status NOT IN ('draft','locked')
		   AND NOT EXISTS (SELECT 1 FROM review mine WHERE mine.submission_id=s.id
		                    AND mine.reviewer_id=$1 AND mine.superseded_at IS NULL)
		 ORDER BY s.submitted_at,s.id
	`, uid)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var submissionID int64
		var title, student, studentSID, category string
		var submittedAt *time.Time
		var peer *string
		var peerDecided bool
		if err := rows.Scan(&submissionID, &title, &student, &studentSID, &category, &submittedAt, &peer, &peerDecided); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{
			"submissionId": strconv.FormatInt(submissionID, 10), "title": title, "student": student,
			"studentId": studentSID, "category": category, "submittedAt": submittedAt, "peer": peer, "peerDecided": peerDecided,
		})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type dispatchReassignInput struct {
	From   int64  `json:"from,string"`
	To     int64  `json:"to,string"`
	Reason string `json:"reason"`
}

func validateReassignReason(reason string) (string, bool) {
	reason = strings.TrimSpace(reason)
	count := utf8.RuneCountInString(reason)
	return reason, count >= 4 && count <= 500
}

func validateReassignTarget(ctx context.Context, tx pgx.Tx, submissionID, studentID, to int64) error {
	if to <= 0 || to == studentID {
		return errors.New("目标审核人不能是学生本人")
	}
	var valid bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM app_user WHERE id=$1 AND status='active'
		               AND role IN ('group','class_admin') AND NOT dispatch_paused)
	`, to).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errors.New("目标审核人不存在、已停用或已暂停")
	}
	if err := tx.QueryRow(ctx, `
		SELECT NOT EXISTS (
		  SELECT 1 FROM submission_reviewer WHERE submission_id=$1 AND reviewer_id=$2 AND active
		  UNION ALL
		  SELECT 1 FROM review WHERE submission_id=$1 AND reviewer_id=$2 AND superseded_at IS NULL
		)
	`, submissionID, to).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errors.New("目标审核人已在该条上有分配或结论")
	}
	return nil
}

func (s *Server) reassignSubmission(c *gin.Context) {
	submissionID, ok := dispatchPathID(c, "id")
	if !ok {
		return
	}
	var input dispatchReassignInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "单条改派参数不正确", nil)
		return
	}
	reason, valid := validateReassignReason(input.Reason)
	if !valid || input.From <= 0 || input.To <= 0 || input.From == input.To {
		writeError(c, http.StatusBadRequest, "invalid_request", "改派需要不同的审核人和 4–500 字理由", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if _, err := tx.Exec(c.Request.Context(), `SELECT pg_advisory_xact_lock($1)`, dispatch.ClassLockKey(actor.ClassID)); err != nil {
		writeServiceError(c, err)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var oldID, studentID int64
	var position int16
	var reviewed bool
	/* 这里的范围要跟队列一致：队列已经把上一版方案的条目列出来了，守卫再按当前方案卡一道，
	   点「换人」就只会得到一句"原审核人没有这条活跃分配"。dispatch_run 仍记当前方案——
	   那记录的是"这次改派是在哪版方案下操作的"，跟条目属于哪版是两回事。 */
	err = tx.QueryRow(c.Request.Context(), `
		SELECT sr.id,s.student_id,sr.position,
		       EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=$2 AND r.superseded_at IS NULL)
		  FROM submission_reviewer sr JOIN submission s ON s.id=sr.submission_id
		 WHERE sr.submission_id=$1 AND s.status NOT IN ('draft','locked') AND sr.reviewer_id=$2 AND sr.active
		 FOR UPDATE OF sr
	`, submissionID, input.From).Scan(&oldID, &studentID, &position, &reviewed)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusUnprocessableEntity, "invalid_reviewer", "原审核人没有这条活跃分配", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if reviewed {
		writeError(c, http.StatusConflict, "already_reviewed", "原审核人已经提交结论，不能改派", nil)
		return
	}
	if err := validateReassignTarget(c.Request.Context(), tx, submissionID, studentID, input.To); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "invalid_reviewer", err.Error(), nil)
		return
	}
	var runID int64
	seed := randomSeed()
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO dispatch_run (class_id,scheme_id,policy,random_seed,avoid_self,trigger,assigned,blocked,created_by)
		VALUES ($1,$2,$3,$4,true,'handover',1,0,$5) RETURNING id
	`, actor.ClassID, current.ID, dispatch.Policy, seed, actor.UserID).Scan(&runID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE submission_reviewer SET active=false WHERE id=$1`, oldID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO submission_reviewer (class_id,submission_id,reviewer_id,position,run_id)
		VALUES ($1,$2,$3,$4,$5)
	`, actor.ClassID, submissionID, input.To, position, runID); err != nil {
		writeServiceError(c, err)
		return
	}
	metadata := map[string]any{"from": strconv.FormatInt(input.From, 10), "to": strconv.FormatInt(input.To, 10),
		"submissionId": strconv.FormatInt(submissionID, 10), "reason": reason}
	if err := appendAudit(c, tx, "dispatch.reassigned", "submission", strconv.FormatInt(submissionID, 10), nil, nil, metadata); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.DispatchReassignedEvent(), events.DispatchReassignedPayload{
		SubmissionID: strconv.FormatInt(submissionID, 10), From: strconv.FormatInt(input.From, 10),
		To: strconv.FormatInt(input.To, 10), Reason: reason,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	reviewers, err := activeSubmissionReviewers(c.Request.Context(), tx, submissionID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"submissionId": strconv.FormatInt(submissionID, 10), "reviewers": reviewers})
}

type handoverInput struct {
	From   int64  `json:"from,string"`
	To     *int64 `json:"to,string,omitempty"`
	Reason string `json:"reason"`
}

type handoverMove struct {
	OldID        int64
	SubmissionID int64
	StudentID    int64
	Position     int16
	To           int64
}

func (s *Server) handoverDispatch(c *gin.Context) {
	var input handoverInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "整体转出参数不正确", nil)
		return
	}
	reason, valid := validateReassignReason(input.Reason)
	if !valid || input.From <= 0 || (input.To != nil && (*input.To <= 0 || *input.To == input.From)) {
		writeError(c, http.StatusBadRequest, "invalid_request", "整体转出需要审核人和 4–500 字理由", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if _, err := tx.Exec(c.Request.Context(), `SELECT pg_advisory_xact_lock($1)`, dispatch.ClassLockKey(actor.ClassID)); err != nil {
		writeServiceError(c, err)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	pool, loads, err := loadDispatchPool(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	before := make(map[int64]int, len(loads))
	for id, load := range loads {
		before[id] = load.Assigned
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT sr.id,s.id,s.student_id,sr.position,s.title,student.name,s.submitted_at,
		       COALESCE(array_agg(DISTINCT r.reviewer_id) FILTER (WHERE r.reviewer_id IS NOT NULL),'{}'::bigint[]),
		       COALESCE(array_agg(DISTINCT active_sr.reviewer_id) FILTER (WHERE active_sr.reviewer_id IS NOT NULL),'{}'::bigint[])
		  FROM submission_reviewer sr
		  JOIN submission s ON s.id=sr.submission_id
		  JOIN app_user student ON student.id=s.student_id
		  LEFT JOIN review r ON r.submission_id=s.id AND r.superseded_at IS NULL
		  LEFT JOIN submission_reviewer active_sr ON active_sr.submission_id=s.id AND active_sr.active
		 WHERE sr.reviewer_id=$1 AND sr.active AND s.status NOT IN ('draft','locked')
		   AND NOT EXISTS (SELECT 1 FROM review mine WHERE mine.submission_id=s.id
		                    AND mine.reviewer_id=$1 AND mine.superseded_at IS NULL)
		 GROUP BY sr.id,s.id,student.name
		 ORDER BY s.submitted_at,s.id
	`, input.From)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	type handoverCandidate struct {
		move handoverMove
		item dispatch.Submission
	}
	var candidates []handoverCandidate
	seed := randomSeed()
	for rows.Next() {
		var move handoverMove
		var item dispatch.Submission
		var reviewedBy, assignedTo []int64
		if err := rows.Scan(&move.OldID, &item.ID, &item.StudentID, &move.Position, &item.Title, &item.Student,
			&item.SubmittedAt, &reviewedBy, &assignedTo); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		move.SubmissionID = item.ID
		item.ReviewedBy = make(map[int64]bool, len(reviewedBy))
		item.AssignedReviewers = make(map[int64]bool, len(assignedTo))
		for _, id := range reviewedBy {
			item.ReviewedBy[id] = true
		}
		for _, id := range assignedTo {
			item.AssignedReviewers[id] = true
		}
		candidates = append(candidates, handoverCandidate{move: move, item: item})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	var moves []handoverMove
	for _, candidate := range candidates {
		move, item := candidate.move, candidate.item
		fromLoad := loads[input.From]
		if fromLoad.Pending > 0 {
			fromLoad.Pending--
			loads[input.From] = fromLoad
		}
		if input.To != nil {
			if err := validateReassignTarget(c.Request.Context(), tx, item.ID, item.StudentID, *input.To); err != nil {
				writeError(c, http.StatusUnprocessableEntity, "invalid_reviewer", err.Error(), gin.H{"submissionId": strconv.FormatInt(item.ID, 10)})
				return
			}
			move.To = *input.To
		} else {
			selected, ok, err := dispatch.PickOne(item, pool, loads, seed, true)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			if !ok {
				writeError(c, http.StatusUnprocessableEntity, "not_enough_reviewers", "没有可接手该条目的审核人", gin.H{"submissionId": strconv.FormatInt(item.ID, 10)})
				return
			}
			move.To = selected
		}
		toLoad := loads[move.To]
		toLoad.Assigned++
		toLoad.Pending++
		loads[move.To] = toLoad
		moves = append(moves, move)
	}
	planRows := dispatchPlanRows(pool, before, loads)
	if len(moves) == 0 {
		c.JSON(http.StatusOK, gin.H{"moved": 0, "rows": planRows})
		return
	}
	var runID int64
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO dispatch_run (class_id,scheme_id,policy,random_seed,avoid_self,trigger,assigned,blocked,created_by)
		VALUES ($1,$2,$3,$4,true,'handover',$5,0,$6) RETURNING id
	`, actor.ClassID, current.ID, dispatch.Policy, seed, len(moves), actor.UserID).Scan(&runID); err != nil {
		writeServiceError(c, err)
		return
	}
	for _, move := range moves {
		if _, err := tx.Exec(c.Request.Context(), `UPDATE submission_reviewer SET active=false WHERE id=$1`, move.OldID); err != nil {
			writeServiceError(c, err)
			return
		}
		if _, err := tx.Exec(c.Request.Context(), `
			INSERT INTO submission_reviewer (class_id,submission_id,reviewer_id,position,run_id)
			VALUES ($1,$2,$3,$4,$5)
		`, actor.ClassID, move.SubmissionID, move.To, move.Position, runID); err != nil {
			writeServiceError(c, err)
			return
		}
		payload := events.DispatchReassignedPayload{
			SubmissionID: strconv.FormatInt(move.SubmissionID, 10), From: strconv.FormatInt(input.From, 10),
			To: strconv.FormatInt(move.To, 10), Reason: reason,
		}
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.DispatchReassignedEvent(), payload); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	metadata := map[string]any{"from": strconv.FormatInt(input.From, 10), "reason": reason, "moved": len(moves)}
	if input.To != nil {
		metadata["to"] = strconv.FormatInt(*input.To, 10)
	}
	if err := appendAudit(c, tx, "dispatch.reassigned", "dispatch_run", strconv.FormatInt(runID, 10), nil, planRows, metadata); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"moved": len(moves), "rows": planRows})
}

func activeSubmissionReviewers(ctx context.Context, tx pgx.Tx, submissionID int64) ([]dispatch.Reviewer, error) {
	pool, _, err := loadDispatchPool(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT reviewer_id FROM submission_reviewer WHERE submission_id=$1 AND active ORDER BY position`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[int64]dispatch.Reviewer, len(pool))
	for _, reviewer := range pool {
		byID[reviewer.UserID] = reviewer
	}
	result := make([]dispatch.Reviewer, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if reviewer, ok := byID[id]; ok {
			result = append(result, reviewer)
		}
	}
	return result, rows.Err()
}

func dispatchPlanRows(pool []dispatch.Reviewer, before map[int64]int, after map[int64]dispatch.Load) []dispatch.PlanRow {
	rows := make([]dispatch.PlanRow, 0, len(pool))
	for _, reviewer := range pool {
		value := after[reviewer.UserID].Assigned
		rows = append(rows, dispatch.PlanRow{ReviewerID: strconv.FormatInt(reviewer.UserID, 10), Name: reviewer.Name,
			SID: reviewer.SID, Before: before[reviewer.UserID], After: value, Delta: value - before[reviewer.UserID]})
	}
	return rows
}

func dispatchPathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param(name)), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_id", "资源 ID 不正确", nil)
		return 0, false
	}
	return id, true
}

func sortedIDStrings(set map[int64]bool) []string {
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, strconv.FormatInt(id, 10))
	}
	return result
}

func randomSeed() int64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 1
	}
	value := int64(binary.BigEndian.Uint64(raw[:]) & (1<<63 - 1))
	if value == 0 {
		return 1
	}
	return value
}
