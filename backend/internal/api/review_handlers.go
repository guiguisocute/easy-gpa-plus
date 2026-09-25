package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/dispatch"
	"easygpa/backend/internal/events"
	reviewdomain "easygpa/backend/internal/review"
	"easygpa/backend/internal/scheme"
)

func (s *Server) reviewTasks(c *gin.Context) {
	tab := c.DefaultQuery("tab", "mine")
	if tab == "appeal" {
		s.reviewerAppeals(c)
		return
	}
	if tab != "mine" && tab != "peer" && tab != "conflict" {
		writeError(c, http.StatusBadRequest, "invalid_tab", "审核任务标签不正确", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT s.id,s.title,s.category_key,s.item_key,s.requested_score::float8,s.status,s.submitted_at,
		       student.sid,student.name,sr.id,sr.assigned_at,
		       mine.id IS NOT NULL AS reviewed_by_me,
		       (SELECT count(*) FROM review r WHERE r.submission_id=s.id AND r.superseded_at IS NULL) AS review_count,
		       (SELECT count(*) FROM submission_reviewer expected WHERE expected.submission_id=s.id AND expected.active) AS expected_count,
		       COALESCE((SELECT item_hours FROM review_sla_config WHERE class_id=s.class_id),24)
		  FROM submission s
		  JOIN app_user student ON student.id=s.student_id
		  JOIN submission_reviewer sr ON sr.submission_id=s.id AND sr.reviewer_id=$1 AND sr.active
		  LEFT JOIN review mine ON mine.submission_id=s.id AND mine.reviewer_id=$1 AND mine.superseded_at IS NULL
		 WHERE s.student_id<>$1
		   AND (
		      ($2='mine' AND mine.id IS NULL AND s.status IN ('pending','consensus')) OR
		      ($2='peer' AND mine.id IS NOT NULL AND s.status='consensus') OR
		      ($2='conflict' AND mine.id IS NOT NULL AND s.status='arbitrating')
		   )
		 ORDER BY CASE WHEN sr.assigned_at + make_interval(hours => COALESCE((SELECT item_hours FROM review_sla_config WHERE class_id=s.class_id),24)) < now() THEN 0 ELSE 1 END,
		          sr.assigned_at,s.id
	`, actor.UserID, tab)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, assignmentID int64
		var title, category, itemKey, status, sid, studentName string
		var requested *float64
		var submittedAt *time.Time
		var assignedAt time.Time
		var reviewed bool
		var reviewCount, expectedCount, slaHours int
		if err := rows.Scan(&id, &title, &category, &itemKey, &requested, &status, &submittedAt, &sid, &studentName, &assignmentID, &assignedAt, &reviewed, &reviewCount, &expectedCount, &slaHours); err != nil {
			writeServiceError(c, err)
			return
		}
		dueAt := assignedAt.Add(time.Duration(slaHours) * time.Hour)
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "type": "submission", "title": title, "category": category,
			"itemKey": itemKey, "requestedScore": requested, "status": status, "submittedAt": submittedAt,
			"studentId": sid, "student": studentName, "assignmentId": strconv.FormatInt(assignmentID, 10), "assignedAt": assignedAt,
			"reviewedByMe": reviewed, "reviewCount": reviewCount, "expectedReviews": expectedCount, "slaHours": slaHours, "dueAt": dueAt, "overdue": time.Now().After(dueAt),
		})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	rows.Close()
	// 学生匿名举报也进这个收件箱，同样用前缀 id 避开与 submission id 的碰撞。
	// 举报判的是负分，队列里必须一眼看得出来——前端靠 type='report' 做提示。
	// 没有 conflict 这一档：举报的分歧不留在小组，直接升给班级管理员。
	// 队列里要写得出项目名，就得有方案在手：report 只存 item_key，名字一律从方案里查。
	reportScheme, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	reportRows, err := tx.Query(c.Request.Context(), `
		SELECT r.id,r.kind,r.category_key,r.item_key,r.proposed_score::float8,r.status,r.created_at,
		       student.sid,student.name,rr.id,rr.assigned_at,rr.decided_at IS NOT NULL,
		       (SELECT count(*) FROM report_reviewer peer WHERE peer.report_id=r.id AND peer.decided_at IS NOT NULL),
		       (SELECT count(*) FROM report_reviewer peer WHERE peer.report_id=r.id),
		       COALESCE((SELECT item_hours FROM review_sla_config WHERE class_id=r.class_id),24)
		  FROM report r
		  JOIN app_user student ON student.id=r.student_id
		  JOIN report_reviewer rr ON rr.report_id=r.id AND rr.reviewer_id=$1
		 WHERE r.student_id<>$1
		   AND (($2='mine' AND rr.decided_at IS NULL AND r.status='reviewing')
		     OR ($2='peer' AND rr.decided_at IS NOT NULL AND r.status='reviewing'))
		 ORDER BY rr.assigned_at,r.id
	`, actor.UserID, tab)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for reportRows.Next() {
		var id, assignmentID int64
		var kind, category, itemKey, status, sid, studentName string
		var proposed float64
		var createdAt, assignedAt time.Time
		var reviewedByMe bool
		var decidedCount, expectedCount, slaHours int
		if err := reportRows.Scan(&id, &kind, &category, &itemKey, &proposed, &status, &createdAt,
			&sid, &studentName, &assignmentID, &assignedAt, &reviewedByMe, &decidedCount, &expectedCount, &slaHours); err != nil {
			reportRows.Close()
			writeServiceError(c, err)
			return
		}
		categoryName, itemName := reportItemLabels(reportScheme.Config, kind, category, itemKey)
		dueAt := assignedAt.Add(time.Duration(slaHours) * time.Hour)
		items = append(items, gin.H{
			"id": "report:" + strconv.FormatInt(id, 10), "type": "report",
			"title": "匿名举报复核", "kind": kind, "category": category, "itemKey": itemKey,
			"categoryName": categoryName, "itemName": itemName,
			"requestedScore": proposed, "status": status, "submittedAt": createdAt,
			"studentId": sid, "student": studentName,
			"assignmentId": strconv.FormatInt(assignmentID, 10), "assignedAt": assignedAt,
			"reviewedByMe": reviewedByMe, "reviewCount": decidedCount, "expectedReviews": expectedCount,
			"slaHours": slaHours, "dueAt": dueAt, "overdue": time.Now().After(dueAt),
		})
	}
	if err := reportRows.Err(); err != nil {
		reportRows.Close()
		writeServiceError(c, err)
		return
	}
	reportRows.Close()
	sort.SliceStable(items, func(i, j int) bool {
		leftOverdue, _ := items[i]["overdue"].(bool)
		rightOverdue, _ := items[j]["overdue"].(bool)
		if leftOverdue != rightOverdue {
			return leftOverdue
		}
		leftDue, leftOK := items[i]["dueAt"].(time.Time)
		rightDue, rightOK := items[j]["dueAt"].(time.Time)
		if leftOK && rightOK && !leftDue.Equal(rightDue) {
			return leftDue.Before(rightDue)
		}
		return items[i]["id"].(string) < items[j]["id"].(string)
	})
	if err := appendAudit(c, tx, "review.task_list", "review", "", nil, nil, map[string]any{"tab": tab, "count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "tab": tab})
}

type reviewerTask struct {
	SubmissionID int64
	AssignmentID int64
	StudentID    int64
	Expected     int
	Status       string
	Title        string
	Category     string
	ItemKey      string
	Claim        []byte
	Requested    *float64
	Snapshot     []byte
	Note         string
	SubmittedAt  *time.Time
	StudentSID   string
	StudentName  string
}

// The same eligibility check drives the history entry, workbench and write.
// Writes evaluate it in a fresh statement after locking the submission, so a
// peer's concurrent decision cannot finalize a score behind an open edit form.
const reviewEditableSQL = `
	r.superseded_at IS NULL AND s.status IN ('pending','consensus') AND s.final_score IS NULL
	AND s.student_id<>$1
	AND EXISTS (SELECT 1 FROM submission_reviewer sr
	            WHERE sr.id=r.assignment_id AND sr.submission_id=s.id AND sr.reviewer_id=$1 AND sr.active)
	AND NOT EXISTS (SELECT 1 FROM review peer
	                WHERE peer.submission_id=s.id AND peer.reviewer_id<>$1 AND peer.superseded_at IS NULL)`

func reviewEditingOpen(cfg scheme.Config, now time.Time) bool {
	return ensureCapability(cfg, "review", now) == nil
}

func loadReviewerTask(c *gin.Context, submissionID int64, lock bool) (reviewerTask, error) {
	actor := mustActor(c)
	tx := mustTx(c)
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE OF s"
	}
	var task reviewerTask
	err := tx.QueryRow(c.Request.Context(), `
		SELECT s.id,sr.id,s.student_id,
		       (SELECT count(*) FROM submission_reviewer expected WHERE expected.submission_id=s.id AND expected.active),
		       s.status,s.title,s.category_key,s.item_key,s.claim,s.requested_score::float8,s.rule_snapshot,s.markdown_note,s.submitted_at,
		       student.sid,student.name
		  FROM submission s
		  JOIN app_user student ON student.id=s.student_id
		  JOIN submission_reviewer sr ON sr.submission_id=s.id AND sr.reviewer_id=$2 AND sr.active
		 WHERE s.id=$1 AND s.student_id<>$2`+lockSQL, submissionID, actor.UserID).Scan(
		&task.SubmissionID, &task.AssignmentID, &task.StudentID, &task.Expected, &task.Status,
		&task.Title, &task.Category, &task.ItemKey, &task.Claim, &task.Requested, &task.Snapshot,
		&task.Note, &task.SubmittedAt, &task.StudentSID, &task.StudentName,
	)
	return task, err
}

func (s *Server) reviewTask(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	task, err := loadReviewerTask(c, id, false)
	if notFound(c, err, "审核任务") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	evidence, err := submissionEvidence(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	noteEvidence, err := submissionNoteEvidenceForReviewer(c.Request.Context(), tx, id, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var ownReview any
	var ownClassificationSuggestion any
	var reviewID int64
	var decision, reason string
	var score float64
	var spent int
	var reviewedAt time.Time
	var canEdit bool
	err = tx.QueryRow(c.Request.Context(), `
		SELECT r.id,r.decision,r.score::float8,r.reason,r.spent_seconds,r.created_at,(`+reviewEditableSQL+`)
		  FROM review r JOIN submission s ON s.id=r.submission_id
		 WHERE r.submission_id=$2 AND r.reviewer_id=$1 AND r.superseded_at IS NULL
	`, actor.UserID, id).Scan(&reviewID, &decision, &score, &reason, &spent, &reviewedAt, &canEdit)
	if err == nil {
		ownReview = gin.H{"id": strconv.FormatInt(reviewID, 10), "decision": decision, "score": score, "reason": reason, "spentSeconds": spent, "at": reviewedAt}
		var suggestionID int64
		var toCategory, toItem, suggestionReason, scope, suggestionStatus string
		err = tx.QueryRow(c.Request.Context(), `
			SELECT id,to_category_key,to_item_key,reason,scope,status
			  FROM classification_suggestion WHERE review_id=$1
		`, reviewID).Scan(&suggestionID, &toCategory, &toItem, &suggestionReason, &scope, &suggestionStatus)
		if err == nil {
			ownClassificationSuggestion = gin.H{"id": strconv.FormatInt(suggestionID, 10), "category": toCategory, "itemKey": toItem, "reason": suggestionReason, "scope": scope, "status": suggestionStatus}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			writeServiceError(c, err)
			return
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var peerSubmitted bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS (
		SELECT 1 FROM review WHERE submission_id=$1 AND reviewer_id<>$2 AND superseded_at IS NULL
	)`, id, actor.UserID).Scan(&peerSubmitted); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "review.task_read", "submission", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": strconv.FormatInt(id, 10), "title": task.Title, "category": task.Category, "itemKey": task.ItemKey,
		"claim": json.RawMessage(task.Claim), "requestedScore": task.Requested, "ruleSnapshot": json.RawMessage(task.Snapshot),
		"note": task.Note, "submittedAt": task.SubmittedAt, "studentId": task.StudentSID, "student": task.StudentName,
		"status": task.Status, "expectedReviews": task.Expected, "evidence": evidence, "noteEvidence": noteEvidence, "myReview": ownReview,
		"myClassificationSuggestion": ownClassificationSuggestion,
		"canEdit":                    canEdit && reviewEditingOpen(current.Config, time.Now()), "peerSubmitted": peerSubmitted,
	})
}

type reviewDecisionInput struct {
	ReviewID                 string                         `json:"reviewId"`
	Decision                 string                         `json:"decision"`
	Score                    *float64                       `json:"score"`
	Reason                   string                         `json:"reason"`
	SpentSeconds             int                            `json:"spentSeconds"`
	ClassificationSuggestion *classificationSuggestionInput `json:"classificationSuggestion"`
}

func validateDecision(task reviewerTask, input reviewDecisionInput) (reviewdomain.Decision, scheme.Points, string, error) {
	decision := reviewdomain.Decision(input.Decision)
	if decision != reviewdomain.DecisionAccept && decision != reviewdomain.DecisionAdjust && decision != reviewdomain.DecisionReject {
		return "", 0, "", errors.New("审核结论不正确")
	}
	reason := strings.TrimSpace(input.Reason)
	if len(reason) > 5000 {
		return "", 0, "", errors.New("审核理由不能超过 5000 字符")
	}
	if decision != reviewdomain.DecisionAccept && len(reason) < 4 {
		return "", 0, "", errors.New("调分或驳回必须填写至少 4 字理由")
	}
	var score float64
	switch decision {
	case reviewdomain.DecisionReject:
		score = 0
	case reviewdomain.DecisionAccept:
		if task.Requested != nil {
			score = *task.Requested
		} else if input.Score != nil {
			score = *input.Score
		} else {
			return "", 0, "", errors.New("学生未填写数量，审核通过时需要给出认定分")
		}
		if reason == "" {
			reason = "材料与规则一致"
		}
	case reviewdomain.DecisionAdjust:
		if input.Score == nil {
			return "", 0, "", errors.New("调分结论必须填写认定分")
		}
		score = *input.Score
	}
	var snapshot ruleSnapshot
	if err := json.Unmarshal(task.Snapshot, &snapshot); err != nil {
		return "", 0, "", errors.New("规则快照损坏")
	}
	if err := scoreWithinRule(snapshot.Item.ScoreRule, score); err != nil {
		return "", 0, "", err
	}
	return decision, scheme.NewPoints(score), reason, nil
}

func scoreWithinRule(rule scheme.ScoreRule, score float64) error {
	points := scheme.NewPoints(score)
	switch rule.Type {
	case "free":
		if rule.Min == nil || rule.Max == nil || points < scheme.NewPoints(*rule.Min) || points > scheme.NewPoints(*rule.Max) {
			return errors.New("认定分超出 free 规则上下限")
		}
	case "per_unit":
		if points < 0 || (rule.Cap != nil && points > scheme.NewPoints(*rule.Cap)) {
			return errors.New("认定分超出按数量规则范围")
		}
	case "enum":
		max := scheme.Points(0)
		for _, option := range rule.Options {
			if scheme.NewPoints(option.Score) > max {
				max = scheme.NewPoints(option.Score)
			}
		}
		if points < 0 || points > max {
			return errors.New("认定分超出枚举规则范围")
		}
	case "threshold":
		if rule.Award == nil || points < 0 || points > scheme.NewPoints(*rule.Award) {
			return errors.New("认定分超出条件基础项范围")
		}
	default:
		return errors.New("未知计分规则")
	}
	return nil
}

func (s *Server) decideReviewTask(c *gin.Context) {
	s.saveReviewTask(c, false)
}

func (s *Server) reviseReviewTask(c *gin.Context) {
	s.saveReviewTask(c, true)
}

func (s *Server) saveReviewTask(c *gin.Context, revision bool) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input reviewDecisionInput
	if err := c.ShouldBindJSON(&input); err != nil || input.SpentSeconds < 0 || input.SpentSeconds > 24*60*60 {
		writeError(c, http.StatusBadRequest, "invalid_request", "审核结论参数不正确", nil)
		return
	}
	task, err := loadReviewerTask(c, id, true)
	if notFound(c, err, "审核任务") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if task.Status != "pending" && task.Status != "consensus" {
		writeError(c, http.StatusConflict, "not_reviewable", "任务当前不接受审核结论", nil)
		return
	}
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "review", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "review_closed", err.Error(), nil)
		return
	}
	decision, score, reason, err := validateDecision(task, input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "review_invalid", err.Error(), nil)
		return
	}
	if input.ClassificationSuggestion != nil {
		if _, _, _, err := validateClassificationSuggestion(c.Request.Context(), tx, id, *input.ClassificationSuggestion); err != nil {
			writeError(c, http.StatusUnprocessableEntity, "classification_invalid", err.Error(), nil)
			return
		}
	}
	actor := mustActor(c)
	before := map[string]any{"status": task.Status}
	if revision {
		var previousID int64
		var previousDecision, previousReason string
		var previousScore float64
		var previousSpent int
		var canEdit bool
		err := tx.QueryRow(c.Request.Context(), `
			SELECT r.id,r.decision,r.score::float8,r.reason,r.spent_seconds,(`+reviewEditableSQL+`)
			  FROM review r JOIN submission s ON s.id=r.submission_id
			 WHERE r.submission_id=$2 AND r.reviewer_id=$1 AND r.superseded_at IS NULL
		`, actor.UserID, id).Scan(&previousID, &previousDecision, &previousScore, &previousReason, &previousSpent, &canEdit)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, http.StatusConflict, "not_reviewed", "你尚未提交这条任务的审核结论", nil)
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if !canEdit {
			writeError(c, http.StatusConflict, "review_not_editable", "另一名审核人已提交或任务状态已变化，不能再修改", nil)
			return
		}
		if input.ReviewID != strconv.FormatInt(previousID, 10) {
			writeError(c, http.StatusConflict, "review_changed", "你的审核结论已更新，请重新打开后修改", nil)
			return
		}
		before["reviewId"], before["decision"], before["score"], before["reason"] = previousID, previousDecision, previousScore, previousReason
		// Preserve the old conclusion and its suggestion; only the new revision
		// participates in reconciliation and reviewer workload counts.
		if _, err := tx.Exec(c.Request.Context(), `UPDATE review SET superseded_at=now() WHERE id=$1`, previousID); err != nil {
			writeServiceError(c, err)
			return
		}
		if _, err := tx.Exec(c.Request.Context(), `UPDATE classification_suggestion SET status='withdrawn' WHERE review_id=$1 AND status='pending'`, previousID); err != nil {
			writeServiceError(c, err)
			return
		}
		input.SpentSeconds += previousSpent
	}
	var reviewID int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO review (class_id,submission_id,reviewer_id,assignment_id,decision,score,reason,spent_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id
	`, actor.ClassID, id, actor.UserID, task.AssignmentID, string(decision), score.Float64(), reason, input.SpentSeconds).Scan(&reviewID)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "already_reviewed", "你已经提交过这条任务的结论", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var suggestionID *int64
	var suggestionScope string
	if input.ClassificationSuggestion != nil {
		createdID, scope, err := insertClassificationSuggestion(c.Request.Context(), tx, actor, id, "item_review", &reviewID, nil, "pending", *input.ClassificationSuggestion)
		if err != nil {
			writeError(c, http.StatusUnprocessableEntity, "classification_invalid", err.Error(), nil)
			return
		}
		suggestionID = &createdID
		suggestionScope = scope
	}
	rows, err := tx.Query(c.Request.Context(), `SELECT reviewer_id,decision,score::float8,reason FROM review WHERE submission_id=$1 AND superseded_at IS NULL ORDER BY reviewer_id`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	results := make([]reviewdomain.Result, 0, 2)
	for rows.Next() {
		var result reviewdomain.Result
		var decisionText string
		var scoreValue float64
		if err := rows.Scan(&result.ReviewerID, &decisionText, &scoreValue, &result.Reason); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		result.Decision = reviewdomain.Decision(decisionText)
		result.Score = scheme.NewPoints(scoreValue)
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeServiceError(c, err)
		return
	}
	rows.Close()
	outcome, err := reviewdomain.Reconcile(task.Expected, results)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	classificationArbitration := false
	if len(results) >= task.Expected {
		var crossCategoryPending bool
		if err := tx.QueryRow(c.Request.Context(), `
			SELECT EXISTS (SELECT 1 FROM classification_suggestion
			 WHERE submission_id=$1 AND status='pending' AND scope='cross_category')
		`, id).Scan(&crossCategoryPending); err != nil {
			writeServiceError(c, err)
			return
		}
		if crossCategoryPending {
			classificationArbitration = true
			outcome.Status = reviewdomain.StatusArbitrating
			outcome.FinalScore = nil
			outcome.Conflict = true
		}
	}
	var final any
	if outcome.FinalScore != nil {
		final = outcome.FinalScore.Float64()
	}
	_, err = tx.Exec(c.Request.Context(), `
		UPDATE submission
		   SET status=$1,final_score=$2,scored_at=CASE WHEN $1='scored' THEN now() ELSE scored_at END,
		       updated_at=now(),lock_version=lock_version+1
		 WHERE id=$3
	`, string(outcome.Status), final, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	metadata := map[string]any{"reviewId": reviewID, "decision": decision, "score": score.Float64(), "resultStatus": outcome.Status, "classificationArbitration": classificationArbitration}
	if suggestionID != nil {
		metadata["classificationSuggestionId"] = *suggestionID
		metadata["classificationScope"] = suggestionScope
	}
	action := "review.decided"
	if revision {
		action = "review.revised"
	}
	if err := appendAudit(c, tx, action, "submission", strconv.FormatInt(id, 10), before, map[string]any{"status": outcome.Status, "finalScore": final, "reviewId": reviewID, "decision": decision, "score": score.Float64(), "reason": reason}, metadata); err != nil {
		writeServiceError(c, err)
		return
	}
	if outcome.Conflict {
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ConflictRaisedEvent(), events.ConflictRaisedPayload{
			SubmissionID: id, ReviewID: reviewID, Status: string(outcome.Status),
		}); err != nil {
			writeServiceError(c, err)
			return
		}
	} else {
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ReviewDecidedEvent(), events.ReviewDecidedPayload{
			SubmissionID: id, ReviewID: reviewID, Status: string(outcome.Status),
		}); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if suggestionID != nil {
		if err := enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.ClassificationSuggested, map[string]any{"suggestionId": *suggestionID, "submissionId": id, "scope": suggestionScope, "source": "item_review"}); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if outcome.FinalScore != nil {
		if err := invalidateLatestSettlement(c.Request.Context(), tx, "review decision changed a final score"); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	statusCode := http.StatusCreated
	if revision {
		statusCode = http.StatusOK
	}
	c.JSON(statusCode, gin.H{"reviewId": strconv.FormatInt(reviewID, 10), "submissionStatus": outcome.Status, "finalScore": final, "conflict": outcome.Conflict, "classificationArbitration": classificationArbitration, "classificationSuggestionId": suggestionID})
}

func (s *Server) reviewCategory(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	/* 范围跟分发池逐字一致（loadDispatchPool）：这一页把「我的累计」和「按大项分布的合计」
	   并排显示，两者用不同口径查就会当场打架。方案换版不影响谁背了多少，结算才是边界。 */
	rows, err := tx.Query(c.Request.Context(), `
		SELECT s.category_key,count(*) AS total,
		       count(*) FILTER (WHERE s.status='scored') AS scored,
		       count(*) FILTER (WHERE s.status='arbitrating') AS conflicts,
		       avg(s.final_score::float8) FILTER (WHERE s.final_score IS NOT NULL) AS average
		  FROM submission s
		 WHERE s.status NOT IN ('draft','locked')
		   AND (EXISTS (SELECT 1 FROM submission_reviewer sr
		                 WHERE sr.submission_id=s.id AND sr.reviewer_id=$1 AND sr.active)
		        OR EXISTS (SELECT 1 FROM review r
		                    WHERE r.submission_id=s.id AND r.reviewer_id=$1 AND r.superseded_at IS NULL))
		 GROUP BY s.category_key ORDER BY s.category_key
	`, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var category string
		var total, scored, conflicts int
		var average *float64
		if err := rows.Scan(&category, &total, &scored, &conflicts, &average); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"category": category, "total": total, "scored": scored, "conflicts": conflicts, "average": average})
	}
	pool, _, err := loadDispatchPool(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	assigned, pending, done, total := 0, 0, 0, 0
	minimum, maximum := 0, 0
	for index, reviewer := range pool {
		total += reviewer.Assigned
		if index == 0 || reviewer.Assigned < minimum {
			minimum = reviewer.Assigned
		}
		if index == 0 || reviewer.Assigned > maximum {
			maximum = reviewer.Assigned
		}
		if reviewer.UserID == actor.UserID {
			assigned, pending, done = reviewer.Assigned, reviewer.Pending, reviewer.Done
		}
	}
	ranked := append([]dispatch.Reviewer(nil), pool...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Assigned != ranked[j].Assigned {
			return ranked[i].Assigned > ranked[j].Assigned
		}
		return ranked[i].UserID < ranked[j].UserID
	})
	rank := 0
	for index, reviewer := range ranked {
		if reviewer.UserID == actor.UserID {
			rank = index + 1
			break
		}
	}
	average := 0.0
	if len(pool) > 0 {
		average = float64(total) / float64(len(pool))
	}
	mine, group, err := reviewDecisionMix(c.Request.Context(), tx, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	load := gin.H{"assigned": assigned, "pending": pending, "done": done, "classAverage": average,
		"spread": maximum - minimum, "rank": rank, "reviewerCount": len(pool),
		"decisions": mine.Counts, "avgSpentSeconds": mine.AvgSpent,
		"groupDecisions": group.Counts, "groupAvgSpentSeconds": group.AvgSpent}
	if err := appendAudit(c, tx, "review.category_read", "review", "", nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "load": load})
}

// decisionMix 是「通过 / 调分 / 驳回 各多少条、平均花多久」。审核人拿它跟全组比，
// 判断自己的口径是不是偏松或偏严——这是背靠背之下唯一能自查口径的途径。
type decisionMix struct {
	Counts   map[string]int
	AvgSpent float64
}

// 同一条 SQL 同时算出「我的」与「全组的」：分两次查会在两次查询之间被新提交的结论
// 割开，出现"我的通过数比全组通过数还多"这种自相矛盾的画面。
//
// 全组口径是所有审核人的合计，不落到任何一个人头上——逐人对比会把工作量页变成排行榜，
// 也会越过背靠背（§5.1）。
func reviewDecisionMix(ctx context.Context, tx pgx.Tx, userID int64) (decisionMix, decisionMix, error) {
	mine := decisionMix{Counts: map[string]int{"accepted": 0, "adjusted": 0, "rejected": 0}}
	group := decisionMix{Counts: map[string]int{"accepted": 0, "adjusted": 0, "rejected": 0}}
	// 范围同上：这三个数的合计要等于「我已评」，那个数来自分发池。
	rows, err := tx.Query(ctx, `
		SELECT r.reviewer_id=$1 AS mine,r.decision,count(*),sum(r.spent_seconds)
		  FROM review r JOIN submission s ON s.id=r.submission_id
		 WHERE s.status NOT IN ('draft','locked') AND r.superseded_at IS NULL
		 GROUP BY 1,2
	`, userID)
	if err != nil {
		return mine, group, err
	}
	defer rows.Close()
	mineSeconds, groupSeconds := 0, 0
	for rows.Next() {
		var isMine bool
		var decision string
		var count, seconds int
		if err := rows.Scan(&isMine, &decision, &count, &seconds); err != nil {
			return mine, group, err
		}
		// 「全组」含我自己：审核人问的是"我跟大家比怎么样"，把自己从大家里剔掉，
		// 人少的时候（六个人）会让这个基数抖得厉害。
		group.Counts[decision] += count
		groupSeconds += seconds
		if isMine {
			mine.Counts[decision] += count
			mineSeconds += seconds
		}
	}
	if err := rows.Err(); err != nil {
		return mine, group, err
	}
	mine.AvgSpent = averagePer(mineSeconds, mine.Counts)
	group.AvgSpent = averagePer(groupSeconds, group.Counts)
	return mine, group, nil
}

func averagePer(seconds int, counts map[string]int) float64 {
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return 0
	}
	return float64(seconds) / float64(total)
}

func (s *Server) reviewHistory(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	editingOpen := reviewEditingOpen(current.Config, time.Now())
	rows, err := tx.Query(c.Request.Context(), `
		SELECT r.id,s.id,s.title,s.category_key,r.decision,r.score::float8,r.reason,r.spent_seconds,r.created_at,
		       s.final_score::float8,s.status,(s.final_score IS NOT NULL AND s.final_score<>r.score) AS changed,
		       (`+reviewEditableSQL+`),student.sid,student.name
		  FROM review r JOIN submission s ON s.id=r.submission_id
		  JOIN app_user student ON student.id=s.student_id
		 WHERE r.reviewer_id=$1 AND r.superseded_at IS NULL
		 ORDER BY r.created_at DESC,r.id DESC LIMIT 200
	`, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var reviewID, submissionID int64
		var title, category, decision, reason, status, studentSID, studentName string
		var score float64
		var spent int
		var at time.Time
		var finalScore *float64
		var changed, canEdit bool
		if err := rows.Scan(&reviewID, &submissionID, &title, &category, &decision, &score, &reason, &spent, &at, &finalScore, &status, &changed, &canEdit, &studentSID, &studentName); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": strconv.FormatInt(reviewID, 10), "submissionId": strconv.FormatInt(submissionID, 10), "title": title, "category": category, "studentId": studentSID, "student": studentName, "decision": decision, "score": score, "reason": reason, "spentSeconds": spent, "at": at, "finalScore": finalScore, "status": status, "changed": changed, "canEdit": canEdit && editingOpen})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "review.history_read", "review", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func invalidateLatestSettlement(ctx context.Context, tx pgx.Tx, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO settlement_invalidation (class_id,run_id,reason)
		SELECT app_current_class_id(),r.id,$1
		  FROM settlement_run r
		 WHERE r.status='complete'
		   AND NOT EXISTS (SELECT 1 FROM settlement_invalidation i WHERE i.run_id=r.id)
		 ORDER BY r.created_at DESC LIMIT 1
	`, reason)
	return err
}
