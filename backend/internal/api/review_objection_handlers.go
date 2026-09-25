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

type objectionInput struct {
	Kind          string   `json:"kind"`
	StudentUserID jsonID   `json:"studentUserId"`
	Category      string   `json:"category"`
	ItemKey       string   `json:"itemKey"`
	TargetID      *jsonID  `json:"targetId"`
	ProposedScore *float64 `json:"proposedScore"`
	Quantity      *float64 `json:"quantity"`
	Basis         string   `json:"basis"`
}

type objectionProblem struct {
	status  int
	code    string
	message string
}

func (problem objectionProblem) Error() string { return problem.message }

func objectionFailure(status int, code, message string) error {
	return objectionProblem{status: status, code: code, message: message}
}

// 班长和综测小组自己也要吃基础分、考勤和处分。结算把全部 active 用户算进排名，
// 扣分对象必须和这个范围对齐，不能只认 role=student。
func isScorableClassMember(role, status string) bool {
	if status != "active" {
		return false
	}
	switch role {
	case "student", "group", "class_admin":
		return true
	default:
		return false
	}
}

func rejectIfNotObjectionTarget(role, status string) error {
	if isScorableClassMember(role, status) {
		return nil
	}
	return objectionFailure(http.StatusUnprocessableEntity, "not_student", "只能对在册班级成员提出扣分或异议")
}

func writeObjectionFailure(c *gin.Context, err error) bool {
	var problem objectionProblem
	if errors.As(err, &problem) {
		writeError(c, problem.status, problem.code, problem.message, nil)
		return true
	}
	return false
}

type validatedObjection struct {
	SchemeID     int64
	StudentID    int64
	TargetID     *int64
	Current      *float64
	Category     string
	CategoryName string
	ItemKey      string
	ItemName     string
	FullScore    *float64
	PerScore     *float64
}

func normalizeObjectionInput(input *objectionInput) error {
	input.Kind = strings.TrimSpace(input.Kind)
	input.Category = strings.TrimSpace(input.Category)
	input.ItemKey = strings.TrimSpace(input.ItemKey)
	input.Basis = strings.TrimSpace(input.Basis)
	if input.Kind != "base" && input.Kind != "penalty" && input.Kind != "submission" {
		return objectionFailure(http.StatusUnprocessableEntity, "objection_invalid", "提案类型不正确")
	}
	if input.ProposedScore == nil || len(input.Basis) < 4 || len(input.Basis) > 5000 {
		return objectionFailure(http.StatusUnprocessableEntity, "objection_invalid", "必须填写建议分和 4—5000 字依据")
	}
	if input.Kind == "penalty" && (input.Quantity == nil || *input.Quantity <= 0) {
		return objectionFailure(http.StatusUnprocessableEntity, "quantity_invalid", "扣分项必须填写大于 0 的次数")
	}
	if input.Kind != "penalty" && input.Quantity != nil {
		return objectionFailure(http.StatusUnprocessableEntity, "quantity_invalid", "只有扣分项可以填写次数")
	}
	return nil
}

func schemeItemLabels(config scheme.Config, categoryKey, itemKey, kind string) (string, string, *float64, *float64, error) {
	for _, category := range config.Categories {
		if category.Key != categoryKey {
			continue
		}
		if kind == "base" {
			for _, item := range category.BaseItems {
				if item.Key == itemKey && item.StudentClaim == nil {
					full := item.Full
					return category.Name, item.Name, &full, nil, nil
				}
			}
		}
		if kind == "penalty" {
			for _, item := range category.PenaltyItems {
				if item.Key == itemKey {
					per := item.Per
					return category.Name, item.Name, nil, &per, nil
				}
			}
		}
	}
	return "", "", nil, nil, objectionFailure(http.StatusUnprocessableEntity, "objection_invalid", "大项或小项不在当前方案中")
}

func validateObjection(ctx context.Context, tx pgx.Tx, actor Actor, input objectionInput) (validatedObjection, error) {
	if err := normalizeObjectionInput(&input); err != nil {
		return validatedObjection{}, err
	}
	studentID := int64(input.StudentUserID)
	var role, status string
	if err := tx.QueryRow(ctx, `SELECT role,status FROM app_user WHERE id=$1`, studentID).Scan(&role, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return validatedObjection{}, objectionFailure(http.StatusNotFound, "student_not_found", "班级成员不存在")
		}
		return validatedObjection{}, err
	}
	if err := rejectIfNotObjectionTarget(role, status); err != nil {
		return validatedObjection{}, err
	}

	if input.Kind == "submission" {
		if input.TargetID == nil {
			return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "target_required", "定分异议必须选择一个已定分条目")
		}
		var targetStudent, schemeID int64
		var category, itemKey, submissionStatus string
		var current *float64
		var snapshotRaw []byte
		err := tx.QueryRow(ctx, `
			SELECT student_id,scheme_id,category_key,item_key,status,final_score::float8,rule_snapshot
			  FROM submission WHERE id=$1
		`, int64(*input.TargetID)).Scan(&targetStudent, &schemeID, &category, &itemKey, &submissionStatus, &current, &snapshotRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return validatedObjection{}, objectionFailure(http.StatusNotFound, "target_not_found", "已定分条目不存在")
		}
		if err != nil {
			return validatedObjection{}, err
		}
		if targetStudent != studentID || category != input.Category || itemKey != input.ItemKey {
			return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "target_mismatch", "所选条目与学生或方案项目不一致")
		}
		if submissionStatus != "scored" && submissionStatus != "locked" {
			return validatedObjection{}, objectionFailure(http.StatusConflict, "not_objectable", "只有已定分条目可以提出异议")
		}
		var snapshot ruleSnapshot
		if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
			return validatedObjection{}, err
		}
		if err := scoreWithinRule(snapshot.Item.ScoreRule, *input.ProposedScore); err != nil {
			return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "score_invalid", err.Error())
		}
		targetID := int64(*input.TargetID)
		locked, err := objectionTargetHasOpenAppeal(ctx, tx, "submission", targetID)
		if err != nil {
			return validatedObjection{}, err
		}
		if locked {
			return validatedObjection{}, objectionFailure(http.StatusConflict, "target_appealing", "学生正在申诉这一笔，请等申诉结束后再提异议")
		}
		return validatedObjection{
			SchemeID: schemeID, StudentID: studentID, TargetID: &targetID, Current: current,
			Category: category, CategoryName: snapshot.CategoryName, ItemKey: itemKey,
			ItemName: snapshot.Item.Name,
		}, nil
	}

	currentScheme, err := loadCurrentScheme(ctx, tx)
	if err != nil {
		return validatedObjection{}, err
	}
	categoryName, itemName, full, per, err := schemeItemLabels(currentScheme.Config, input.Category, input.ItemKey, input.Kind)
	if err != nil {
		return validatedObjection{}, err
	}
	points := scheme.NewPoints(*input.ProposedScore)
	if input.Kind == "base" && (points < 0 || full == nil || points > scheme.NewPoints(*full)) {
		return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "score_invalid", "基础项建议分必须在 0 与满分之间")
	}
	if input.Kind == "penalty" {
		if points > 0 {
			return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "score_invalid", "扣分项建议分不能大于 0")
		}
		if grant, ok := ctx.Value(governanceExecutionKey{}).(governanceExecution); ok && grant.Action == "objection" && *per < 0 {
			quantity := *input.ProposedScore / *per
			input.Quantity = &quantity
		}
		expected := scheme.NewPoints(*input.Quantity * *per)
		if points != expected {
			return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "quantity_mismatch", "建议分必须等于次数乘以该扣分项单价")
		}
	}
	var rowID int64
	var current float64
	err = tx.QueryRow(ctx, `
		SELECT id,score::float8 FROM base_score
		 WHERE student_id=$1 AND scheme_id=$2 AND category_key=$3 AND item_key=$4 AND kind=$5
	`, studentID, currentScheme.ID, input.Category, input.ItemKey, input.Kind).Scan(&rowID, &current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return validatedObjection{}, err
	}
	var targetID *int64
	var currentScore *float64
	if err == nil {
		targetID, currentScore = &rowID, &current
		if input.TargetID != nil && int64(*input.TargetID) != rowID {
			return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "target_mismatch", "基础或扣分记录已经变化，请刷新后重试")
		}
		appealType := "base_score"
		if input.Kind == "penalty" {
			appealType = "penalty_score"
		}
		locked, err := objectionTargetHasOpenAppeal(ctx, tx, appealType, rowID)
		if err != nil {
			return validatedObjection{}, err
		}
		if locked {
			return validatedObjection{}, objectionFailure(http.StatusConflict, "target_appealing", "学生正在申诉这一笔，请等申诉结束后再提异议")
		}
	} else if input.TargetID != nil {
		return validatedObjection{}, objectionFailure(http.StatusUnprocessableEntity, "target_mismatch", "基础或扣分记录不存在，请刷新后重试")
	}
	return validatedObjection{
		SchemeID: currentScheme.ID, StudentID: studentID, TargetID: targetID, Current: currentScore,
		Category: input.Category, CategoryName: categoryName, ItemKey: input.ItemKey,
		ItemName: itemName, FullScore: full, PerScore: per,
	}, nil
}

func objectionTargetHasOpenAppeal(ctx context.Context, tx pgx.Tx, targetType string, targetID int64) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM appeal
		 WHERE kind='student_appeal' AND target_type=$1 AND target_id=$2
		   AND status IN ('filed','reviewing','escalated'))
	`, targetType, targetID).Scan(&active)
	return active, err
}

func (s *Server) createObjection(c *gin.Context) {
	var input objectionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "扣分或异议参数不正确", nil)
		return
	}
	actor := mustActor(c)
	valid, err := validateObjection(c.Request.Context(), mustTx(c), actor, input)
	if err != nil {
		if !writeObjectionFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	var id int64
	err = mustTx(c).QueryRow(c.Request.Context(), `
		INSERT INTO objection
		    (class_id,kind,student_id,proposer_id,scheme_id,category_key,item_key,target_id,
		     current_score,proposed_score,quantity,basis)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id
	`, actor.ClassID, input.Kind, valid.StudentID, actor.UserID, valid.SchemeID, valid.Category,
		valid.ItemKey, valid.TargetID, valid.Current, *input.ProposedScore, input.Quantity, strings.TrimSpace(input.Basis)).Scan(&id)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "objection_exists", "这一笔已经有未结提案，请先处理现有提案", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, mustTx(c), "objection.created", "objection", strconv.FormatInt(id, 10), nil, map[string]any{"status": "draft", "kind": input.Kind, "studentId": valid.StudentID}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10), "status": "draft"})
}

func (s *Server) updateObjection(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input objectionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "扣分或异议参数不正确", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var status string
	err := tx.QueryRow(c.Request.Context(), `SELECT status FROM objection WHERE id=$1 AND proposer_id=$2 FOR UPDATE`, id, actor.UserID).Scan(&status)
	if notFound(c, err, "提案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "draft" {
		writeError(c, http.StatusConflict, "objection_not_draft", "只有草稿可以修改", nil)
		return
	}
	valid, err := validateObjection(c.Request.Context(), tx, actor, input)
	if err != nil {
		if !writeObjectionFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	_, err = tx.Exec(c.Request.Context(), `
		UPDATE objection SET kind=$1,student_id=$2,scheme_id=$3,category_key=$4,item_key=$5,
		       target_id=$6,current_score=$7,proposed_score=$8,quantity=$9,basis=$10,updated_at=now()
		 WHERE id=$11
	`, input.Kind, valid.StudentID, valid.SchemeID, valid.Category, valid.ItemKey, valid.TargetID,
		valid.Current, *input.ProposedScore, input.Quantity, strings.TrimSpace(input.Basis), id)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "objection_exists", "这一笔已经有未结提案", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10)})
}

func (s *Server) deleteObjection(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT e.object_key FROM evidence e JOIN objection o ON o.id=e.objection_id
		 WHERE o.id=$1 AND o.proposer_id=$2 AND o.status='draft'
	`, id, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	objectKeys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		objectKeys = append(objectKeys, key)
	}
	rows.Close()
	command, err := tx.Exec(c.Request.Context(), `DELETE FROM objection WHERE id=$1 AND proposer_id=$2 AND status='draft'`, id, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if command.RowsAffected() == 0 {
		writeError(c, http.StatusConflict, "objection_not_draft", "提案不存在或已不再是草稿", nil)
		return
	}
	for _, objectKey := range objectKeys {
		s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	}
	c.Status(http.StatusNoContent)
}

type objectionRecord struct {
	ID, StudentID, ProposerID, SchemeID    int64
	Kind, Category, ItemKey, Basis, Status string
	TargetID                               *int64
	CurrentScore                           *float64
	ProposedScore                          float64
	Quantity                               *float64
	BatchID                                *string
	DecidedBy                              *int64
	DecidedScore                           *float64
	DecisionReason                         *string
	CreatedAt                              time.Time
	SubmittedAt, DecidedAt                 *time.Time
	BlindAssignmentID                      *int64
	OriginBatchID                          *string
}

func loadObjectionRecord(ctx context.Context, tx pgx.Tx, id int64, lock bool) (objectionRecord, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE"
	}
	var row objectionRecord
	err := tx.QueryRow(ctx, `
		SELECT id,kind,student_id,proposer_id,scheme_id,category_key,item_key,target_id,
		       current_score::float8,proposed_score::float8,quantity::float8,basis,status,
		       batch_id::text,decided_by,decided_score::float8,decision_reason,
		       created_at,submitted_at,decided_at,blind_assignment_id,origin_batch_id::text
		  FROM objection WHERE id=$1`+lockSQL, id).Scan(
		&row.ID, &row.Kind, &row.StudentID, &row.ProposerID, &row.SchemeID, &row.Category,
		&row.ItemKey, &row.TargetID, &row.CurrentScore, &row.ProposedScore, &row.Quantity,
		&row.Basis, &row.Status, &row.BatchID, &row.DecidedBy, &row.DecidedScore,
		&row.DecisionReason, &row.CreatedAt, &row.SubmittedAt, &row.DecidedAt,
		&row.BlindAssignmentID, &row.OriginBatchID,
	)
	return row, err
}

func inputFromObjection(row objectionRecord) objectionInput {
	student := jsonID(row.StudentID)
	var target *jsonID
	if row.TargetID != nil {
		value := jsonID(*row.TargetID)
		target = &value
	}
	return objectionInput{
		Kind: row.Kind, StudentUserID: student, Category: row.Category, ItemKey: row.ItemKey,
		TargetID: target, ProposedScore: &row.ProposedScore, Quantity: row.Quantity, Basis: row.Basis,
	}
}

func parseObjectionIDs(raw []jsonID) ([]int64, error) {
	if len(raw) == 0 || len(raw) > 500 {
		return nil, errors.New("必须选择 1—500 条提案")
	}
	seen := make(map[int64]bool, len(raw))
	ids := make([]int64, 0, len(raw))
	for _, value := range raw {
		id := int64(value)
		if seen[id] {
			return nil, errors.New("提案列表不能包含重复项")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Server) submitObjections(c *gin.Context) {
	var input struct {
		IDs []jsonID `json:"ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "批量提交参数不正确", nil)
		return
	}
	ids, err := parseObjectionIDs(input.IDs)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "objection_invalid", err.Error(), nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	targets := make(map[[2]int64]bool)
	for _, id := range ids {
		row, err := loadObjectionRecord(c.Request.Context(), tx, id, true)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.ProposerID != actor.UserID) {
			writeError(c, http.StatusNotFound, "not_found", "待提交提案不存在", gin.H{"id": strconv.FormatInt(id, 10)})
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if row.OriginBatchID == nil {
			targets[[2]int64{row.SchemeID, row.StudentID}] = true
		}
		if row.Status != "draft" {
			writeError(c, http.StatusConflict, "objection_not_draft", "批次中包含非草稿提案", gin.H{"id": strconv.FormatInt(id, 10)})
			return
		}
		if _, err := validateObjection(c.Request.Context(), tx, actor, inputFromObjection(row)); err != nil {
			if !writeObjectionFailure(c, err) {
				writeServiceError(c, err)
			}
			return
		}
	}
	var batchID string
	if err := tx.QueryRow(c.Request.Context(), `SELECT gen_random_uuid()::text`).Scan(&batchID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE objection SET status='submitted',batch_id=$1::uuid,submitted_at=now(),updated_at=now()
		 WHERE id=ANY($2::bigint[])
	`, batchID, ids); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "objection.submitted", "objection", batchID, nil, map[string]any{"ids": ids, "submitted": len(ids)}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ObjectionSubmittedEvent(), events.ObjectionSubmittedPayload{
		BatchID: batchID, IDs: ids, ProposerID: actor.UserID,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	for target := range targets {
		if err := invalidateStudentBlindAudit(c.Request.Context(), tx, target[0], target[1], "", "score objection filed after scorecard review"); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	for _, id := range ids {
		if err := s.ensureGovernanceCase(c, "objection", id, true); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"batchId": batchID, "submitted": len(ids)})
}

func (s *Server) withdrawObjection(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	command, err := mustTx(c).Exec(c.Request.Context(), `
		UPDATE objection SET status='withdrawn',updated_at=now()
		 WHERE id=$1 AND proposer_id=$2 AND status='submitted'
	`, id, mustActor(c).UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if command.RowsAffected() == 0 {
		writeError(c, http.StatusConflict, "objection_not_withdrawable", "只有本人尚未裁定的提案可以撤回", nil)
		return
	}
	if err := appendAudit(c, mustTx(c), "objection.withdrawn", "objection", strconv.FormatInt(id, 10), map[string]any{"status": "submitted"}, map[string]any{"status": "withdrawn"}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": "withdrawn"})
}

func objectionJSON(ctx context.Context, tx pgx.Tx, row objectionRecord, admin bool) (gin.H, error) {
	var studentSID, studentName, proposerName string
	if err := tx.QueryRow(ctx, `
		SELECT student.sid,student.name,proposer.name
		  FROM app_user student JOIN app_user proposer ON proposer.id=$2 WHERE student.id=$1
	`, row.StudentID, row.ProposerID).Scan(&studentSID, &studentName, &proposerName); err != nil {
		return nil, err
	}
	var configRaw []byte
	if err := tx.QueryRow(ctx, `SELECT config FROM scheme WHERE id=$1`, row.SchemeID).Scan(&configRaw); err != nil {
		return nil, err
	}
	var config scheme.Config
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return nil, err
	}
	categoryName, itemName, _, _, _ := schemeItemLabels(config, row.Category, row.ItemKey, row.Kind)
	if row.Kind == "submission" {
		_ = tx.QueryRow(ctx, `SELECT title FROM submission WHERE id=$1`, row.TargetID).Scan(&itemName)
		for _, category := range config.Categories {
			if category.Key == row.Category {
				categoryName = category.Name
			}
		}
	}
	item := gin.H{
		"id": strconv.FormatInt(row.ID, 10), "kind": row.Kind,
		"studentId": studentSID, "studentUserId": strconv.FormatInt(row.StudentID, 10), "student": studentName,
		"category": row.Category, "categoryName": categoryName, "itemKey": row.ItemKey, "itemName": itemName,
		"targetId": nil, "currentScore": row.CurrentScore, "proposedScore": row.ProposedScore,
		"quantity": row.Quantity, "basis": row.Basis, "status": row.Status, "batchId": row.BatchID,
		"decidedScore": row.DecidedScore, "decisionReason": row.DecisionReason,
		"createdAt": row.CreatedAt, "submittedAt": row.SubmittedAt, "decidedAt": row.DecidedAt,
	}
	if row.TargetID != nil {
		item["targetId"] = strconv.FormatInt(*row.TargetID, 10)
	}
	if admin {
		item["proposer"], item["proposerId"] = proposerName, strconv.FormatInt(row.ProposerID, 10)
	}
	var draftCreator *int64
	if !admin && (row.Status == "draft" || row.Status == "submitted") {
		draftCreator = &row.ProposerID
	}
	noteEvidence, err := objectionNoteEvidence(ctx, tx, row.ID, draftCreator)
	if err != nil {
		return nil, err
	}
	item["noteEvidence"] = noteEvidence
	return item, nil
}

func listObjections(c *gin.Context, admin bool) {
	actor := mustActor(c)
	tx := mustTx(c)
	status := strings.TrimSpace(c.Query("status"))
	if status == "all" {
		status = ""
	}
	student := strings.TrimSpace(c.Query("student"))
	category := strings.TrimSpace(c.Query("category"))
	query := strings.TrimSpace(c.Query("q"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT o.id FROM objection o JOIN app_user u ON u.id=o.student_id
		 WHERE (($1::boolean AND o.status<>'draft') OR (NOT $1::boolean AND o.proposer_id=$2))
		   AND ($3='' OR o.status=$3)
		   AND ($4='' OR u.sid ILIKE '%'||$4||'%' OR u.name ILIKE '%'||$4||'%')
		   AND ($5='' OR o.category_key=$5)
		   AND ($6='' OR o.item_key ILIKE '%'||$6||'%' OR o.basis ILIKE '%'||$6||'%')
		   AND (NOT $9::boolean OR u.role='class_admin')
		 ORDER BY CASE o.status WHEN 'submitted' THEN 0 WHEN 'draft' THEN 1 ELSE 2 END,
		          o.updated_at DESC,o.id DESC LIMIT $7 OFFSET $8
	`, admin, actor.UserID, status, student, category, query, pageSize, (page-1)*pageSize, isDeputyAdjudication(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	items := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		row, err := loadObjectionRecord(c.Request.Context(), tx, id, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		item, err := objectionJSON(c.Request.Context(), tx, row, admin)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if admin {
			withAdjudicationPermission(item, actor, row.StudentID)
		}
		items = append(items, item)
	}
	var total int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM objection o JOIN app_user u ON u.id=o.student_id
		 WHERE (($1::boolean AND o.status<>'draft') OR (NOT $1::boolean AND o.proposer_id=$2))
		   AND ($3='' OR o.status=$3)
		   AND ($4='' OR u.sid ILIKE '%'||$4||'%' OR u.name ILIKE '%'||$4||'%')
		   AND ($5='' OR o.category_key=$5)
		   AND ($6='' OR o.item_key ILIKE '%'||$6||'%' OR o.basis ILIKE '%'||$6||'%')
		   AND (NOT $7::boolean OR u.role='class_admin')
	`, admin, actor.UserID, status, student, category, query, isDeputyAdjudication(c)).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

func (s *Server) reviewObjections(c *gin.Context) { listObjections(c, false) }
func (s *Server) adminObjections(c *gin.Context)  { listObjections(c, true) }

func (s *Server) reviewStudents(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defaultBaseCount := defaultBaseItemCount(current.Config)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT u.id,u.sid,u.name,
		       EXISTS (SELECT 1 FROM seal se WHERE se.student_id=u.id AND se.unsealed_at IS NULL),
		       $2+(SELECT count(*) FROM submission s WHERE s.student_id=u.id AND s.status IN ('scored','locked')),
		       (SELECT count(*) FROM objection o WHERE o.student_id=u.id AND o.proposer_id=$1 AND o.status IN ('draft','submitted'))
		  FROM app_user u WHERE u.role IN ('student','group','class_admin') AND u.status='active'
		 ORDER BY u.sid,u.id
	`, actor.UserID, defaultBaseCount)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var sid, name string
		var sealed bool
		var scored, open int
		if err := rows.Scan(&id, &sid, &name, &sealed, &scored, &open); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"userId": strconv.FormatInt(id, 10), "sid": sid, "name": name, "sealed": sealed, "scoredCount": scored, "openObjections": open, "self": id == actor.UserID})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func defaultBaseItemCount(config scheme.Config) int {
	count := 0
	for _, category := range config.Categories {
		for _, item := range category.BaseItems {
			if item.StudentClaim == nil {
				count++
			}
		}
	}
	return count
}

func (s *Server) reviewStudentScorecard(c *gin.Context) {
	studentID, ok := pathID(c, "uid")
	if !ok {
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var sid, name, role, status string
	err := tx.QueryRow(c.Request.Context(), `SELECT sid,name,role,status FROM app_user WHERE id=$1`, studentID).Scan(&sid, &name, &role, &status)
	if notFound(c, err, "班级成员") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !isScorableClassMember(role, status) {
		writeError(c, http.StatusForbidden, "not_student", "目标账号不是在册班级成员，无法查看计分卡", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	type recordedBase struct {
		ID      int64
		Score   float64
		Basis   string
		Updated time.Time
		Locked  bool
	}
	recorded := make(map[string]recordedBase)
	claimableBaseKeys := make(map[string]bool)
	for _, category := range current.Config.Categories {
		for _, item := range category.BaseItems {
			if item.StudentClaim != nil {
				claimableBaseKeys[category.Key+"\x00"+item.Key] = true
			}
		}
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT b.id,b.category_key,b.item_key,b.kind,b.score::float8,b.basis,b.updated_at,
		       EXISTS (SELECT 1 FROM appeal a WHERE a.kind='student_appeal'
		         AND a.target_type=CASE b.kind WHEN 'base' THEN 'base_score' ELSE 'penalty_score' END
		         AND a.target_id=b.id AND a.status IN ('filed','reviewing','escalated'))
		  FROM base_score b WHERE b.student_id=$1 AND b.scheme_id=$2
	`, studentID, current.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for rows.Next() {
		var category, itemKey, kind string
		var value recordedBase
		if err := rows.Scan(&value.ID, &category, &itemKey, &kind, &value.Score, &value.Basis, &value.Updated, &value.Locked); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		if kind != "base" || !claimableBaseKeys[category+"\x00"+itemKey] {
			recorded[category+"\x00"+kind+"\x00"+itemKey] = value
		}
	}
	rows.Close()
	baseItems := make([]gin.H, 0)
	for _, category := range current.Config.Categories {
		for _, item := range category.BaseItems {
			if item.StudentClaim != nil {
				continue
			}
			value := recorded[category.Key+"\x00base\x00"+item.Key]
			row := gin.H{"id": nil, "category": category.Key, "categoryName": category.Name, "itemKey": item.Key, "itemName": item.Name, "kind": "base", "fullScore": item.Full, "perScore": nil, "score": item.Full, "basis": "方案默认满分", "recorded": false, "locked": false, "updatedAt": nil}
			if value.ID != 0 {
				row["id"], row["score"], row["basis"], row["recorded"], row["locked"], row["updatedAt"] = strconv.FormatInt(value.ID, 10), value.Score, value.Basis, true, value.Locked, value.Updated
			}
			baseItems = append(baseItems, row)
		}
		for _, item := range category.PenaltyItems {
			value := recorded[category.Key+"\x00penalty\x00"+item.Key]
			row := gin.H{"id": nil, "category": category.Key, "categoryName": category.Name, "itemKey": item.Key, "itemName": item.Name, "kind": "penalty", "fullScore": nil, "perScore": item.Per, "score": 0.0, "basis": "暂无扣分记录", "recorded": false, "locked": false, "updatedAt": nil}
			if value.ID != 0 {
				row["id"], row["score"], row["basis"], row["recorded"], row["locked"], row["updatedAt"] = strconv.FormatInt(value.ID, 10), value.Score, value.Basis, true, value.Locked, value.Updated
			}
			baseItems = append(baseItems, row)
		}
	}
	rows, err = tx.Query(c.Request.Context(), `
		SELECT s.id,s.category_key,s.item_key,s.title,s.final_score::float8,s.status,s.rule_snapshot,
		       EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=$2 AND r.superseded_at IS NULL),
		       EXISTS (SELECT 1 FROM appeal a WHERE a.kind='student_appeal' AND a.target_type='submission'
		         AND a.target_id=s.id AND a.status IN ('filed','reviewing','escalated')),s.scored_at,s.source
		  FROM submission s WHERE s.student_id=$1 AND s.final_score IS NOT NULL AND s.status IN ('scored','locked','appealing','arbitrating')
		 ORDER BY s.category_key,s.scored_at DESC,s.id DESC
	`, studentID, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	submissions := make([]gin.H, 0)
	submissionIDs := make([]int64, 0)
	for rows.Next() {
		var id int64
		var category, itemKey, title, submissionStatus, source string
		var finalScore *float64
		var snapshotRaw []byte
		var reviewed, locked bool
		var scoredAt *time.Time
		if err := rows.Scan(&id, &category, &itemKey, &title, &finalScore, &submissionStatus, &snapshotRaw, &reviewed, &locked, &scoredAt, &source); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		var snapshot ruleSnapshot
		if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		submissionIDs = append(submissionIDs, id)
		submissions = append(submissions, gin.H{"id": strconv.FormatInt(id, 10), "category": category, "categoryName": snapshot.CategoryName, "itemKey": itemKey, "itemName": snapshot.Item.Name, "title": title, "finalScore": finalScore, "status": submissionStatus, "ruleSnapshot": json.RawMessage(snapshotRaw), "reviewedByMe": reviewed, "locked": locked, "scoredAt": scoredAt})
		submissions[len(submissions)-1]["source"] = source
	}
	rows.Close()
	// 每条已定分条目当初交了什么，在这一页上要看得到也下得下来：小组要判"这条给得对不对"，
	// 光看标题和分数判不了。开放范围与 evidenceURL 里那一段是同一条线——条目定分之后
	// 对整个小组开放，定分之前仍然只有派到的人看得见。
	for at, id := range submissionIDs {
		if strings.HasPrefix(c.FullPath(), "/api/v1/governance/") {
			submissions[at]["evidence"] = []gin.H{}
			continue
		}
		files, err := submissionEvidence(c.Request.Context(), tx, id)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		submissions[at]["evidence"] = files
	}
	var sealed bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM seal WHERE student_id=$1 AND unsealed_at IS NULL)`, studentID).Scan(&sealed); err != nil {
		writeServiceError(c, err)
		return
	}
	var scoredCount, openCount int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT (SELECT count(*) FROM submission WHERE student_id=$1 AND status IN ('scored','locked')),
		       (SELECT count(*) FROM objection WHERE student_id=$1 AND proposer_id=$2 AND status IN ('draft','submitted'))
	`, studentID, actor.UserID).Scan(&scoredCount, &openCount); err != nil {
		writeServiceError(c, err)
		return
	}
	scoredCount += defaultBaseItemCount(current.Config)
	c.JSON(http.StatusOK, gin.H{
		"student":   gin.H{"userId": strconv.FormatInt(studentID, 10), "sid": sid, "name": name, "sealed": sealed, "scoredCount": scoredCount, "openObjections": openCount, "self": studentID == actor.UserID},
		"baseItems": baseItems, "submissions": submissions,
	})
}

type objectionDecisionInput struct {
	Action string   `json:"action"`
	Score  *float64 `json:"score"`
	Reason string   `json:"reason"`
}

func decideObjection(ctx context.Context, tx pgx.Tx, actor Actor, row objectionRecord, input objectionDecisionInput) (string, *float64, error) {
	// Shared by individual and batch decisions, including dismissal. Every
	// object is checked before mutation so a mixed-scope batch rolls back.
	if err := authorizeAdjudication(ctx, tx, actor, row.StudentID); err != nil {
		return "", nil, err
	}
	input.Action = strings.TrimSpace(input.Action)
	input.Reason = strings.TrimSpace(input.Reason)
	if row.Status != "submitted" {
		return "", nil, objectionFailure(http.StatusConflict, "objection_not_submitted", "只有待终裁提案可以处理")
	}
	if input.Action != "apply" && input.Action != "adjust" && input.Action != "dismiss" {
		return "", nil, objectionFailure(http.StatusUnprocessableEntity, "decision_invalid", "终裁动作不正确")
	}
	if len(input.Reason) < 4 || len(input.Reason) > 5000 {
		return "", nil, objectionFailure(http.StatusUnprocessableEntity, "decision_invalid", "终裁理由须为 4—5000 字符")
	}
	if input.Action == "dismiss" {
		_, err := tx.Exec(ctx, `
			UPDATE objection SET status='dismissed',decided_by=$1,decision_reason=$2,decided_at=now(),updated_at=now() WHERE id=$3
		`, actor.UserID, input.Reason, row.ID)
		return "dismissed", nil, err
	}
	score := row.ProposedScore
	if input.Action == "adjust" {
		if input.Score == nil {
			return "", nil, objectionFailure(http.StatusUnprocessableEntity, "score_required", "改分生效时必须填写裁定分")
		}
		score = *input.Score
	}
	validationInput := inputFromObjection(row)
	validationInput.ProposedScore = &score
	valid, err := validateObjection(ctx, tx, actor, validationInput)
	if err != nil {
		return "", nil, err
	}
	if row.Kind == "submission" {
		if _, err := tx.Exec(ctx, `SELECT id FROM submission WHERE id=$1 FOR UPDATE`, *valid.TargetID); err != nil {
			return "", nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE submission SET final_score=$1,status='scored',scored_at=now(),updated_at=now(),lock_version=lock_version+1 WHERE id=$2
		`, score, *valid.TargetID); err != nil {
			return "", nil, err
		}
	} else {
		kind := row.Kind
		if valid.TargetID != nil {
			if _, err := tx.Exec(ctx, `SELECT id FROM base_score WHERE id=$1 FOR UPDATE`, *valid.TargetID); err != nil {
				return "", nil, err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO base_score
			    (class_id,student_id,scheme_id,category_key,item_key,kind,full_score,score,basis,recorded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (class_id,student_id,scheme_id,category_key,item_key)
			DO UPDATE SET kind=EXCLUDED.kind,full_score=EXCLUDED.full_score,score=EXCLUDED.score,
			              basis=EXCLUDED.basis,recorded_by=EXCLUDED.recorded_by,updated_at=now()
		`, actor.ClassID, row.StudentID, row.SchemeID, row.Category, row.ItemKey, kind, valid.FullScore, score, row.Basis, row.ProposerID); err != nil {
			return "", nil, err
		}
	}
	nextStatus := "applied"
	if input.Action == "adjust" {
		nextStatus = "adjusted"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE objection SET status=$1,decided_by=$2,decided_score=$3,decision_reason=$4,
		       decided_at=now(),updated_at=now() WHERE id=$5
	`, nextStatus, actor.UserID, score, input.Reason, row.ID); err != nil {
		return "", nil, err
	}
	if err := invalidateLatestSettlement(ctx, tx, "objection applied"); err != nil {
		return "", nil, err
	}
	origin := ""
	if row.OriginBatchID != nil {
		origin = *row.OriginBatchID
	}
	if err := invalidateStudentBlindAudit(ctx, tx, row.SchemeID, row.StudentID, origin, "objection changed a score outside the active blind audit"); err != nil {
		return "", nil, err
	}
	return nextStatus, &score, nil
}

func (s *Server) decideAdminObjection(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input objectionDecisionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "终裁参数不正确", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	row, err := loadObjectionRecord(c.Request.Context(), tx, id, true)
	if notFound(c, err, "提案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	status, score, err := decideObjection(c.Request.Context(), tx, actor, row, input)
	if err != nil {
		if !writeAdjudicationFailure(c, err) && !writeObjectionFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	action := "objection.applied"
	if status == "dismissed" {
		action = "objection.dismissed"
	}
	if err := appendAudit(c, tx, action, "objection", strconv.FormatInt(id, 10), map[string]any{"status": row.Status}, map[string]any{"status": status, "score": score}, map[string]any{"proposerId": row.ProposerID, "adjusted": status == "adjusted"}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ObjectionDecidedEvent(), events.ObjectionDecidedPayload{
		ObjectionID: id, Status: status, Score: score, StudentID: row.StudentID, ProposerID: row.ProposerID,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	if row.OriginBatchID != nil {
		if _, err := refreshBlindAuditBatch(c.Request.Context(), tx, actor.ClassID, *row.OriginBatchID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": status, "score": score})
}

func (s *Server) decideAdminObjectionsBatch(c *gin.Context) {
	var input struct {
		IDs    []jsonID `json:"ids"`
		Action string   `json:"action"`
		Reason string   `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "批量终裁参数不正确", nil)
		return
	}
	ids, err := parseObjectionIDs(input.IDs)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "decision_invalid", err.Error(), nil)
		return
	}
	if input.Action != "apply" && input.Action != "dismiss" {
		writeError(c, http.StatusUnprocessableEntity, "decision_invalid", "批量终裁只支持照准或驳回", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	originBatches := make(map[string]bool)
	for _, id := range ids {
		row, err := loadObjectionRecord(c.Request.Context(), tx, id, true)
		if notFound(c, err, "提案") {
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		status, score, err := decideObjection(c.Request.Context(), tx, actor, row, objectionDecisionInput{Action: input.Action, Reason: input.Reason})
		if err != nil {
			if !writeAdjudicationFailure(c, err) && !writeObjectionFailure(c, err) {
				writeServiceError(c, err)
			}
			return
		}
		action := "objection.applied"
		if status == "dismissed" {
			action = "objection.dismissed"
		}
		if err := appendAudit(c, tx, action, "objection", strconv.FormatInt(id, 10), map[string]any{"status": row.Status}, map[string]any{"status": status, "score": score}, map[string]any{"proposerId": row.ProposerID, "adjusted": false, "batch": true}); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ObjectionDecidedEvent(), events.ObjectionDecidedPayload{
			ObjectionID: id, Status: status, Score: score, StudentID: row.StudentID, ProposerID: row.ProposerID,
		}); err != nil {
			writeServiceError(c, err)
			return
		}
		if row.OriginBatchID != nil {
			originBatches[*row.OriginBatchID] = true
		}
	}
	for batchID := range originBatches {
		if _, err := refreshBlindAuditBatch(c.Request.Context(), tx, actor.ClassID, batchID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"decided": len(ids)})
}
