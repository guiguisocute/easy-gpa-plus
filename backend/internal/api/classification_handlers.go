package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/scheme"
)

type classificationSuggestionInput struct {
	Category string `json:"category"`
	ItemKey  string `json:"itemKey"`
	Reason   string `json:"reason"`
}

type classificationTarget struct {
	SchemeID int64
	Category scheme.Category
	Item     scheme.Item
	Snapshot []byte
}

func loadSchemeByID(ctx context.Context, tx pgx.Tx, id int64) (storedScheme, error) {
	var row storedScheme
	err := tx.QueryRow(ctx, `SELECT id,version,name,config FROM scheme WHERE id=$1 AND status='published'`, id).
		Scan(&row.ID, &row.Version, &row.Name, &row.Raw)
	if err != nil {
		return storedScheme{}, err
	}
	if err := json.Unmarshal(row.Raw, &row.Config); err != nil {
		return storedScheme{}, err
	}
	return row, nil
}

func claimableSchemeItem(cfg scheme.Config, categoryKey, itemKey string) (scheme.Category, scheme.Item, bool) {
	for _, category := range cfg.Categories {
		if category.Key != categoryKey {
			continue
		}
		for _, item := range category.Items {
			if item.Key == itemKey {
				return category, item, true
			}
		}
		for _, base := range category.BaseItems {
			if base.Key == itemKey {
				if item, ok := scheme.StudentClaimBaseItem(base); ok {
					return category, item, true
				}
			}
		}
		if itemKey == scheme.OtherSelfReportKey(category.Key) && category.AcceptsSubmissions() {
			return category, scheme.OtherSelfReportItem(category), true
		}
		return scheme.Category{}, scheme.Item{}, false
	}
	return scheme.Category{}, scheme.Item{}, false
}

func loadClassificationTarget(ctx context.Context, tx pgx.Tx, submissionID int64, categoryKey, itemKey string, capturedAt time.Time) (classificationTarget, error) {
	categoryKey = strings.TrimSpace(categoryKey)
	itemKey = strings.TrimSpace(itemKey)
	var schemeID int64
	if err := tx.QueryRow(ctx, `SELECT scheme_id FROM submission WHERE id=$1`, submissionID).Scan(&schemeID); err != nil {
		return classificationTarget{}, err
	}
	stored, err := loadSchemeByID(ctx, tx, schemeID)
	if err != nil {
		return classificationTarget{}, err
	}
	category, item, ok := claimableSchemeItem(stored.Config, categoryKey, itemKey)
	if !ok {
		return classificationTarget{}, errors.New("建议小项不在该申报所属的已发布方案中")
	}
	snapshot, err := json.Marshal(ruleSnapshot{
		SchemeID: strconv.FormatInt(stored.ID, 10), Version: stored.Config.Version,
		CategoryKey: category.Key, CategoryName: category.Name, Item: item, CapturedAt: capturedAt.UTC(),
	})
	if err != nil {
		return classificationTarget{}, err
	}
	return classificationTarget{SchemeID: stored.ID, Category: category, Item: item, Snapshot: snapshot}, nil
}

func validateClassificationSuggestion(ctx context.Context, tx pgx.Tx, submissionID int64, input classificationSuggestionInput) (classificationTarget, string, string, error) {
	input.Category = strings.TrimSpace(input.Category)
	input.ItemKey = strings.TrimSpace(input.ItemKey)
	reason := strings.TrimSpace(input.Reason)
	if len(reason) < 4 || len(reason) > 5000 {
		return classificationTarget{}, "", "", errors.New("分类建议理由须为 4—5000 字符")
	}
	var fromCategory, fromItem string
	if err := tx.QueryRow(ctx, `SELECT category_key,item_key FROM submission WHERE id=$1`, submissionID).Scan(&fromCategory, &fromItem); err != nil {
		return classificationTarget{}, "", "", err
	}
	if input.Category == fromCategory && input.ItemKey == fromItem {
		return classificationTarget{}, "", "", errors.New("建议归类必须与当前有效归类不同")
	}
	target, err := loadClassificationTarget(ctx, tx, submissionID, input.Category, input.ItemKey, time.Now())
	return target, fromCategory, fromItem, err
}

func insertClassificationSuggestion(ctx context.Context, tx pgx.Tx, actor Actor, submissionID int64, source string, reviewID, blindAssignmentID *int64, status string, input classificationSuggestionInput) (int64, string, error) {
	target, fromCategory, fromItem, err := validateClassificationSuggestion(ctx, tx, submissionID, input)
	if err != nil {
		return 0, "", err
	}
	scope := "within_category"
	if fromCategory != target.Category.Key {
		scope = "cross_category"
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO classification_suggestion
		    (class_id,scheme_id,submission_id,suggested_by,source,review_id,blind_assignment_id,
		     from_category_key,from_item_key,to_category_key,to_item_key,to_rule_snapshot,scope,reason,status,submitted_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		        CASE WHEN $15='pending' THEN now() END)
		RETURNING id
	`, actor.ClassID, target.SchemeID, submissionID, actor.UserID, source, reviewID, blindAssignmentID,
		fromCategory, fromItem, target.Category.Key, target.Item.Key, target.Snapshot, scope, strings.TrimSpace(input.Reason), status).Scan(&id)
	return id, scope, err
}

func (s *Server) adminSuggestClassification(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input classificationSuggestionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "分类建议参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var status string
	var schemeID, studentID int64
	if err := tx.QueryRow(c.Request.Context(), `SELECT status,scheme_id,student_id FROM submission WHERE id=$1 FOR UPDATE`, id).Scan(&status, &schemeID, &studentID); err != nil {
		if notFound(c, err, "提交条目") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if status != "scored" && status != "arbitrating" && status != "locked" {
		writeError(c, http.StatusConflict, "not_classifiable", "只有已定分或待终裁条目可以提出分类建议", nil)
		return
	}
	if !requireAdjudicationTarget(c, tx, actor, studentID) {
		return
	}
	suggestionID, scope, err := insertClassificationSuggestion(c.Request.Context(), tx, actor, id, "admin", nil, nil, "pending", input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "classification_invalid", err.Error(), nil)
		return
	}
	if scope == "cross_category" && status != "locked" {
		if _, err := tx.Exec(c.Request.Context(), `UPDATE submission SET status='arbitrating',final_score=NULL,updated_at=now(),lock_version=lock_version+1 WHERE id=$1`, id); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := appendAudit(c, tx, "classification.suggested", "classification_suggestion", strconv.FormatInt(suggestionID, 10), nil,
		map[string]any{"submissionId": id, "scope": scope, "category": strings.TrimSpace(input.Category), "itemKey": strings.TrimSpace(input.ItemKey)}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.ClassificationSuggested, map[string]any{"suggestionId": suggestionID, "submissionId": id, "scope": scope}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateStudentBlindAudit(c.Request.Context(), tx, schemeID, studentID, "", "classification suggestion filed after scorecard review"); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(suggestionID, 10), "scope": scope, "status": "pending"})
}

func (s *Server) adminClassificationSuggestions(c *gin.Context) {
	tx := mustTx(c)
	status := strings.TrimSpace(c.DefaultQuery("status", "pending"))
	rows, err := tx.Query(c.Request.Context(), `
		SELECT cs.id,cs.submission_id,cs.scope,cs.status,cs.from_category_key,cs.from_item_key,
		       cs.to_category_key,cs.to_item_key,cs.reason,cs.source,cs.created_at,
		       student.id,student.sid,student.name,s.title,s.final_score::float8,cs.blind_assignment_id
		  FROM classification_suggestion cs
		  JOIN submission s ON s.id=cs.submission_id
		  JOIN app_user student ON student.id=s.student_id
		 WHERE ($1='' OR cs.status=$1)
		   AND (NOT $2::boolean OR (student.role='class_admin' AND s.status IN ('arbitrating','scored','locked','appealing')))
		 ORDER BY CASE cs.scope WHEN 'cross_category' THEN 0 ELSE 1 END,cs.created_at,cs.id
	`, status, isDeputyAdjudication(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var suggestionID, submissionID, studentID int64
		var scope, rowStatus, fromCategory, fromItem, toCategory, toItem, reason, source, sid, name, title string
		var createdAt time.Time
		var score *float64
		var assignmentID *int64
		if err := rows.Scan(&suggestionID, &submissionID, &scope, &rowStatus, &fromCategory, &fromItem,
			&toCategory, &toItem, &reason, &source, &createdAt, &studentID, &sid, &name, &title, &score, &assignmentID); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, withAdjudicationPermission(gin.H{
			"id": strconv.FormatInt(suggestionID, 10), "submissionId": strconv.FormatInt(submissionID, 10),
			"scope": scope, "status": rowStatus, "fromCategory": fromCategory, "fromItemKey": fromItem,
			"toCategory": toCategory, "toItemKey": toItem, "reason": reason, "source": source, "createdAt": createdAt,
			"studentId": strconv.FormatInt(studentID, 10), "studentSid": sid, "student": name, "title": title, "finalScore": score, "blindAssignmentId": assignmentID,
		}, mustActor(c), studentID))
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	rows.Close()
	if err := attachBlindNoteEvidence(c.Request.Context(), tx, items); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type classificationResolutionInput struct {
	SuggestionID *jsonID  `json:"suggestionId"`
	Category     string   `json:"category"`
	ItemKey      string   `json:"itemKey"`
	Score        *float64 `json:"score"`
	Reason       string   `json:"reason"`
	OriginBatch  string   `json:"originBatchId"`
}

func nullableID(value *jsonID) *int64 {
	if value == nil {
		return nil
	}
	id := int64(*value)
	return &id
}

func classificationResolutionRequiresArbitration(beforeCategory, targetCategory, suggestionScope string) bool {
	return suggestionScope == "cross_category" || beforeCategory != targetCategory
}

func invalidateStudentBlindAudit(ctx context.Context, tx pgx.Tx, schemeID, studentID int64, originBatch string, reason string) error {
	// A submission keeps its original scheme ID. Its score also appears in
	// scorecards generated under later versions, so invalidate across versions
	// of this student's class while preserving the originating review batch.
	originBatch = strings.TrimSpace(originBatch)
	_, err := tx.Exec(ctx, `
		WITH stale AS (
			UPDATE scorecard_audit_batch
			   SET status='stale',stale_at=now(),stale_reason=$1,
			       invalidated_at=now(),invalidated_reason=$1
			 WHERE class_id=(SELECT class_id FROM scheme WHERE id=$2) AND student_id=$3
			   AND status IN ('generating','blocked','open','resolving','complete')
			   AND ($4='' OR id<>$4::uuid)
			 RETURNING class_id,id
		)
		INSERT INTO outbox_event (class_id,type,payload)
		SELECT class_id,$5,jsonb_build_object('batchId',id::text,'studentId',$3,'reason',$1) FROM stale
	`, reason, schemeID, studentID, originBatch, events.ScorecardAuditStale)
	return err
}

func invalidateBlindAuditsForScheme(ctx context.Context, tx pgx.Tx, schemeID int64, reason string) error {
	_, err := tx.Exec(ctx, `
		WITH stale AS (
			UPDATE scorecard_audit_batch
			   SET status='stale',stale_at=now(),stale_reason=$1,
			       invalidated_at=now(),invalidated_reason=$1
			 WHERE scheme_id=$2 AND status IN ('generating','blocked','open','resolving','complete')
			 RETURNING class_id,id,student_id
		)
		INSERT INTO outbox_event (class_id,type,payload)
		SELECT class_id,$3,jsonb_build_object('batchId',id::text,'studentId',student_id,'reason',$1) FROM stale
	`, reason, schemeID, events.ScorecardAuditStale)
	return err
}

func (s *Server) resolveClassification(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input classificationResolutionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "分类处理参数不正确", nil)
		return
	}
	input.Category, input.ItemKey, input.Reason = strings.TrimSpace(input.Category), strings.TrimSpace(input.ItemKey), strings.TrimSpace(input.Reason)
	if len(input.Reason) < 4 || len(input.Reason) > 5000 {
		writeError(c, http.StatusUnprocessableEntity, "reason_required", "分类处理理由须为 4—5000 字符", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var schemeID, studentID int64
	var beforeCategory, beforeItem, status string
	var beforeSnapshot []byte
	var beforeScore *float64
	err := tx.QueryRow(c.Request.Context(), `
		SELECT scheme_id,student_id,category_key,item_key,rule_snapshot,final_score::float8,status
		  FROM submission WHERE id=$1 FOR UPDATE
	`, id).Scan(&schemeID, &studentID, &beforeCategory, &beforeItem, &beforeSnapshot, &beforeScore, &status)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !requireAdjudicationTarget(c, tx, actor, studentID) {
		return
	}
	target, err := loadClassificationTarget(c.Request.Context(), tx, id, input.Category, input.ItemKey, time.Now())
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "classification_invalid", err.Error(), nil)
		return
	}
	resolvedScore := beforeScore
	if input.Score != nil {
		value := scheme.NewPoints(*input.Score).Float64()
		resolvedScore = &value
	}
	if resolvedScore == nil {
		writeError(c, http.StatusUnprocessableEntity, "score_required", "待终裁条目必须同时填写认定分", nil)
		return
	}
	if err := scoreWithinRule(target.Item.ScoreRule, *resolvedScore); err != nil {
		message := err.Error()
		if input.Score == nil {
			message = "原认定分不符合新小项规则，必须同时填写新认定分"
		}
		writeError(c, http.StatusUnprocessableEntity, "score_invalid", message, nil)
		return
	}
	originBatchID := ""
	suggestionScope := ""
	if input.SuggestionID != nil {
		var suggestionSubmissionID int64
		var suggestionStatus string
		var derivedOrigin *string
		if err := tx.QueryRow(c.Request.Context(), `
			SELECT cs.submission_id,cs.status,cs.scope,subject.batch_id::text
			  FROM classification_suggestion cs
			  LEFT JOIN scorecard_audit_assignment assignment ON assignment.id=cs.blind_assignment_id
			  LEFT JOIN scorecard_audit_subject subject ON subject.id=assignment.subject_id
			 WHERE cs.id=$1 FOR UPDATE OF cs
		`, int64(*input.SuggestionID)).Scan(&suggestionSubmissionID, &suggestionStatus, &suggestionScope, &derivedOrigin); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(c, http.StatusNotFound, "suggestion_not_found", "分类建议不存在", nil)
			} else {
				writeServiceError(c, err)
			}
			return
		}
		if suggestionSubmissionID != id || suggestionStatus != "pending" {
			writeError(c, http.StatusConflict, "suggestion_unavailable", "分类建议不属于本申报或已经处理", nil)
			return
		}
		if derivedOrigin != nil {
			originBatchID = *derivedOrigin
		}
	}
	if classificationResolutionRequiresArbitration(beforeCategory, target.Category.Key, suggestionScope) {
		writeError(c, http.StatusConflict, "classification_arbitration_required", "跨大项分类必须在仲裁台同时签发分类、分数与终裁理由", nil)
		return
	}
	if claimed := strings.TrimSpace(input.OriginBatch); claimed != "" && claimed != originBatchID {
		writeError(c, http.StatusUnprocessableEntity, "origin_batch_invalid", "批次因果编号与分类建议来源不一致", nil)
		return
	}
	var originBatch any
	if originBatchID != "" {
		originBatch = originBatchID
	}
	var resolutionID int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO classification_resolution
		    (class_id,scheme_id,submission_id,suggestion_id,origin_batch_id,decided_by,
		     before_category_key,before_item_key,after_category_key,after_item_key,
		     before_rule_snapshot,after_rule_snapshot,before_score,after_score,reason)
		VALUES ($1,$2,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id
	`, actor.ClassID, schemeID, id, nullableID(input.SuggestionID), originBatch, actor.UserID,
		beforeCategory, beforeItem, target.Category.Key, target.Item.Key, beforeSnapshot, target.Snapshot, beforeScore, resolvedScore, input.Reason).Scan(&resolutionID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE submission
		   SET category_key=$1,item_key=$2,rule_snapshot=$3,final_score=$4,status='scored',
		       scored_at=COALESCE(scored_at,now()),updated_at=now(),lock_version=lock_version+1
		 WHERE id=$5
	`, target.Category.Key, target.Item.Key, target.Snapshot, *resolvedScore, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE classification_suggestion
		   SET status=CASE WHEN to_category_key=$1 AND to_item_key=$2 THEN 'accepted' ELSE 'rejected' END,
		       resolved_by=$3,resolution_reason=$4,resolved_at=now()
		 WHERE submission_id=$5 AND status='pending'
	`, target.Category.Key, target.Item.Key, actor.UserID, input.Reason, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateLatestSettlement(c.Request.Context(), tx, "effective classification or score changed"); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateStudentBlindAudit(c.Request.Context(), tx, schemeID, studentID, originBatchID, "effective classification changed outside the active blind audit"); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "classification.resolved", "classification_resolution", strconv.FormatInt(resolutionID, 10),
		map[string]any{"category": beforeCategory, "itemKey": beforeItem, "score": beforeScore},
		map[string]any{"category": target.Category.Key, "itemKey": target.Item.Key, "score": *resolvedScore},
		map[string]any{"submissionId": id, "reason": input.Reason, "originBatchId": originBatchID}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.ClassificationResolved, map[string]any{"resolutionId": resolutionID, "submissionId": id}); err != nil {
		writeServiceError(c, err)
		return
	}
	if originBatchID != "" {
		if _, err := refreshBlindAuditBatch(c.Request.Context(), tx, actor.ClassID, originBatchID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(resolutionID, 10), "submissionId": strconv.FormatInt(id, 10), "category": target.Category.Key, "itemKey": target.Item.Key, "score": *resolvedScore, "status": "scored"})
}
