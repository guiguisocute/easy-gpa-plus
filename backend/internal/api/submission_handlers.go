package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/scheme"
)

type submissionInput struct {
	Category string          `json:"category"`
	ItemKey  string          `json:"itemKey"`
	Title    string          `json:"title"`
	Claim    json.RawMessage `json:"claim"`
	Note     string          `json:"note"`
}

type ruleSnapshot struct {
	SchemeID     string      `json:"schemeId"`
	Version      string      `json:"version"`
	CategoryKey  string      `json:"categoryKey"`
	CategoryName string      `json:"categoryName"`
	Item         scheme.Item `json:"item"`
	CapturedAt   time.Time   `json:"capturedAt"`
}

type preparedSubmission struct {
	SchemeID      int64
	SchemeVersion int
	Category      scheme.Category
	Item          scheme.Item
	Claim         scheme.Claim
	Requested     *float64
	Snapshot      []byte
}

// requireTitle separates "saving a draft" from "sending it to review". Evidence
// has to hang off an existing row, so a student who uploads a photo before
// typing anything must still get a draft; demanding a title there blocks a
// perfectly normal order of work. Review is where the title becomes mandatory —
// that is the point at which a reviewer has to identify what they are looking at.
func prepareSubmission(current storedScheme, input submissionInput, capturedAt time.Time, requireTitle bool) (preparedSubmission, error) {
	input.Category = strings.TrimSpace(input.Category)
	input.ItemKey = strings.TrimSpace(input.ItemKey)
	input.Title = strings.TrimSpace(input.Title)
	if requireTitle && input.Title == "" {
		return preparedSubmission{}, errors.New("提交审核前必须填写材料标题")
	}
	if len(input.Title) > 300 {
		return preparedSubmission{}, errors.New("材料标题不能超过 300 字符")
	}
	if len(input.Note) > 20_000 {
		return preparedSubmission{}, errors.New("参与说明不能超过 20000 字符")
	}
	var category *scheme.Category
	var item *scheme.Item
	var generatedItem scheme.Item
	for i := range current.Config.Categories {
		if current.Config.Categories[i].Key != input.Category {
			continue
		}
		category = &current.Config.Categories[i]
		for j := range category.Items {
			if category.Items[j].Key == input.ItemKey {
				item = &category.Items[j]
				break
			}
		}
		// Category keys are unique (scheme.Validate enforces it), so the first
		// match is the only match.
		break
	}
	if category != nil && item == nil {
		for _, base := range category.BaseItems {
			if base.Key != input.ItemKey {
				continue
			}
			if candidate, ok := scheme.StudentClaimBaseItem(base); ok {
				generatedItem = candidate
				item = &generatedItem
			}
			break
		}
	}
	if category != nil && item == nil && input.ItemKey == scheme.OtherSelfReportKey(category.Key) {
		generatedItem = scheme.OtherSelfReportItem(*category)
		item = &generatedItem
	}
	if category == nil || item == nil {
		return preparedSubmission{}, errors.New("所选小项不在当前发布方案中")
	}
	/* 专业素质是教务导入的加权平均分，不由申报累加而来。结算和成绩单本来就按
	   key 跳过它，但入口一直是开的：学生可以往 major 里提交，材料照样进审核队列，
	   最后在结算时被静默丢掉——白填一场。这里把口子堵上，让三处口径一致。 */
	if !category.AcceptsSubmissions() {
		return preparedSubmission{}, errors.New("专业素质由教务成绩导入，不接受学生申报")
	}
	if len(input.Claim) == 0 || !json.Valid(input.Claim) {
		return preparedSubmission{}, errors.New("申报内容格式不正确，请刷新页面后重新填写")
	}
	var claim scheme.Claim
	if err := json.Unmarshal(input.Claim, &claim); err != nil {
		return preparedSubmission{}, errors.New("申报内容格式不正确，请检查数量、档位和期望分数")
	}
	var requested *float64
	points, err := scheme.ScoreClaim(item.ScoreRule, claim)
	if err != nil {
		// Quantity (per-unit) and self-reported scores are optional, including
		// claimable base items and catchalls. Preserve NULL until review; it is
		// neither an automatic zero nor a request to grant the default full score.
		var claimErr *scheme.ClaimError
		reviewerDetermines := errors.As(err, &claimErr) &&
			(item.ScoreRule.Type == "per_unit" && claimErr.Code == "claim_quantity_required" ||
				item.ScoreRule.Type == "free" && claimErr.Code == "claim_score_required")
		/* 草稿还允许 claim 整个空着。AI 归组挑不出方案里的档次时应当照建草稿、把原因
		   写进说明，让人回表单补一下，而不是让整批候选作废。只放过"还没填"——填错了
		   仍旧拒绝；按档和达标规则在提交审核时必须补齐所需信息。 */
		draftBlank := !requireTitle && claim.Quantity == nil && claim.Score == nil && strings.TrimSpace(claim.Option) == ""
		if !reviewerDetermines && !draftBlank {
			return preparedSubmission{}, err
		}
	} else {
		value := points.Float64()
		requested = &value
	}
	snapshot, _ := json.Marshal(ruleSnapshot{
		SchemeID: strconv.FormatInt(current.ID, 10), Version: current.Config.Version,
		CategoryKey: category.Key, CategoryName: category.Name, Item: *item, CapturedAt: capturedAt.UTC(),
	})
	return preparedSubmission{
		SchemeID: current.ID, SchemeVersion: current.Version, Category: *category,
		Item: *item, Claim: claim, Requested: requested, Snapshot: snapshot,
	}, nil
}

func ensureCapability(cfg scheme.Config, key string, now time.Time) error {
	// 中间件按路由拦，这里按业务语义拦。两道都留着：将来有人加了一条新的写路由
	// 而忘了它会绕过哪一层时，另一层还在。
	if cfg.Window.LockedAt(now) {
		return errors.New("本学期已全系统封锁")
	}
	// Close only ends material intake. Existing work must still be reviewed,
	// appealed and arbitrated after automatic sealing, until Lockdown.
	continuesAfterSeal := key == "review" || key == "appeal" || key == "arbitrate"
	if now.Before(cfg.Window.Open) || (!continuesAfterSeal && !now.Before(cfg.Window.Close)) {
		return errors.New("当前不在开放窗口内")
	}
	on := map[string]bool{
		"submit": cfg.Capabilities.Submit, "edit": cfg.Capabilities.Edit,
		"appeal": cfg.Capabilities.Appeal, "review": cfg.Capabilities.Review,
		"arbitrate": cfg.Capabilities.Arbitrate, "studentReport": cfg.Capabilities.StudentReport,
	}[key]
	if !on {
		label := map[string]string{"submit": "材料提交", "edit": "材料修改", "appeal": "申诉", "review": "审核", "arbitrate": "仲裁", "studentReport": "学生举报"}[key]
		if label == "" {
			label = "此操作"
		}
		return fmt.Errorf("%s当前未开放，请查看班级时间线或联系班级管理员", label)
	}
	return nil
}

func ensureNotSealed(ctx context.Context, tx pgx.Tx, userID int64) error {
	var sealed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM seal WHERE student_id=$1 AND unsealed_at IS NULL)`, userID).Scan(&sealed); err != nil {
		return err
	}
	if sealed {
		return errors.New("账号已封存，提交与修改入口已关闭")
	}
	return nil
}

func (s *Server) createSubmission(c *gin.Context) {
	var input submissionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "提交参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		if notFound(c, err, "当前方案") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "submit", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "submission_closed", err.Error(), nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	// A new row is always a draft, so it may still be untitled.
	prepared, err := prepareSubmission(current, input, time.Now(), false)
	if err != nil {
		writeClaimError(c, err)
		return
	}
	var id int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO submission
		    (class_id,student_id,scheme_id,scheme_version,category_key,item_key,filed_category_key,filed_item_key,
		     title,claim,requested_score,status,source,rule_snapshot,filed_rule_snapshot,markdown_note)
		VALUES ($1,$2,$3,$4,$5,$6,$5,$6,$7,$8,$9,'draft','manual',$10,$10,$11)
		RETURNING id
	`, actor.ClassID, actor.UserID, prepared.SchemeID, prepared.SchemeVersion, prepared.Category.Key,
		prepared.Item.Key, strings.TrimSpace(input.Title), []byte(input.Claim), prepared.Requested, prepared.Snapshot, input.Note).Scan(&id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "submission.draft_created", "submission", strconv.FormatInt(id, 10), nil, map[string]any{"category": prepared.Category.Key, "itemKey": prepared.Item.Key}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10), "status": "draft", "requestedScore": prepared.Requested})
}

type submissionListItem struct {
	ID                 string          `json:"id"`
	Student            string          `json:"student"`
	StudentID          string          `json:"studentId"`
	Category           string          `json:"category"`
	FiledCategory      string          `json:"filedCategory"`
	CategoryName       string          `json:"categoryName"`
	ItemKey            string          `json:"itemKey"`
	FiledItemKey       string          `json:"filedItemKey"`
	ItemName           string          `json:"itemName"`
	Title              string          `json:"title"`
	Claim              json.RawMessage `json:"claim"`
	WantScore          *float64        `json:"wantScore"`
	FinalScore         *float64        `json:"finalScore"`
	Status             string          `json:"status"`
	SubmittedAt        *time.Time      `json:"submittedAt"`
	RuleSnapshot       json.RawMessage `json:"ruleSnapshot"`
	FiledSnapshot      json.RawMessage `json:"filedRuleSnapshot"`
	EvidenceCount      int             `json:"evidenceCount"`
	Note               string          `json:"note,omitempty"`
	Source             string          `json:"source"`
	UpdatedAt          time.Time       `json:"updatedAt"`
	AppealRound        int             `json:"appealRound"`
	AppealsUsed        int             `json:"appealsUsed"`
	AppealPending      bool            `json:"appealPending"`
	CanAppeal          bool            `json:"canAppeal"`
	ForceRejection     json.RawMessage `json:"forceRejection"`
	ForcedScore        json.RawMessage `json:"forcedScore"`
	CanSelfForceReject bool            `json:"canSelfForceReject"`
}

func scanSubmissionRows(rows pgx.Rows) ([]submissionListItem, error) {
	items := make([]submissionListItem, 0)
	for rows.Next() {
		var id int64
		var snapshotRaw []byte
		var item submissionListItem
		var filedSnapshotRaw []byte
		if err := rows.Scan(&id, &item.Student, &item.StudentID, &item.Category, &item.ItemKey, &item.FiledCategory, &item.FiledItemKey, &filedSnapshotRaw, &item.Title,
			&item.Claim, &item.WantScore, &item.FinalScore, &item.Status, &item.SubmittedAt,
			&snapshotRaw, &item.EvidenceCount, &item.Note, &item.Source, &item.UpdatedAt, &item.ForceRejection, &item.ForcedScore); err != nil {
			return nil, err
		}
		item.ID = strconv.FormatInt(id, 10)
		item.RuleSnapshot = snapshotRaw
		item.FiledSnapshot = filedSnapshotRaw
		item.CanSelfForceReject, _ = selfForceRejectPermission(item.Status, item.FinalScore, len(item.ForceRejection) > 0)
		var snapshot ruleSnapshot
		if json.Unmarshal(snapshotRaw, &snapshot) == nil {
			item.CategoryName = snapshot.CategoryName
			item.ItemName = snapshot.Item.Name
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) submissions(c *gin.Context) {
	actor := mustActor(c)
	forceRejectedOnly := c.Query("force_rejected") == "true"
	category := strings.TrimSpace(c.Query("category"))
	status := strings.TrimSpace(c.Query("status"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT s.id,u.name,u.sid,s.category_key,s.item_key,s.filed_category_key,s.filed_item_key,s.filed_rule_snapshot,s.title,s.claim,
		       s.requested_score::float8,s.final_score::float8,s.status,s.submitted_at,
		       s.rule_snapshot,(SELECT count(*) FROM evidence e WHERE e.submission_id=s.id AND e.status='ready' AND e.kind='claim'),
		       s.markdown_note,s.source,s.updated_at,s.force_rejection,s.forced_score - 'actorName'
		  FROM submission s JOIN app_user u ON u.id=s.student_id
		 WHERE s.student_id=$1
		   AND ($2='' OR s.category_key=$2)
		   AND ($3='' OR s.status=$3)
		   AND (NOT $6::boolean OR s.force_rejection IS NOT NULL)
		 ORDER BY s.updated_at DESC,s.id DESC
		 LIMIT $4 OFFSET $5
	`, actor.UserID, category, status, pageSize, (page-1)*pageSize, forceRejectedOnly)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items, err := scanSubmissionRows(rows)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	appealAvailable := false
	selfRejectAvailable := false
	if current, currentErr := loadCurrentScheme(c.Request.Context(), tx); currentErr == nil {
		selfRejectAvailable = !current.Config.Window.LockedAt(time.Now())
		appealAvailable = ensureCapability(current.Config, "appeal", time.Now()) == nil
	} else if !errors.Is(currentErr, pgx.ErrNoRows) {
		writeServiceError(c, currentErr)
		return
	}
	for index := range items {
		if err := populateSubmissionAppealState(c.Request.Context(), tx, &items[index], actor.UserID); err != nil {
			writeServiceError(c, err)
			return
		}
		items[index].CanAppeal = items[index].CanAppeal && appealAvailable && len(items[index].ForcedScore) == 0
		items[index].CanSelfForceReject = items[index].CanSelfForceReject && selfRejectAvailable
	}
	var total int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM submission WHERE student_id=$1 AND ($2='' OR category_key=$2) AND ($3='' OR status=$3) AND (NOT $4::boolean OR force_rejection IS NOT NULL)`, actor.UserID, category, status, forceRejectedOnly).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "submission.list", "submission", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

func loadOwnedSubmission(ctx context.Context, tx pgx.Tx, id, userID int64, forUpdate bool) (submissionListItem, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE OF s"
	}
	rows, err := tx.Query(ctx, `
		SELECT s.id,u.name,u.sid,s.category_key,s.item_key,s.filed_category_key,s.filed_item_key,s.filed_rule_snapshot,s.title,s.claim,
		       s.requested_score::float8,s.final_score::float8,s.status,s.submitted_at,
		       s.rule_snapshot,(SELECT count(*) FROM evidence e WHERE e.submission_id=s.id AND e.status='ready' AND e.kind='claim'),
		       s.markdown_note,s.source,s.updated_at,s.force_rejection,s.forced_score - 'actorName'
		  FROM submission s JOIN app_user u ON u.id=s.student_id
		 WHERE s.id=$1 AND s.student_id=$2`+lock, id, userID)
	if err != nil {
		return submissionListItem{}, err
	}
	defer rows.Close()
	items, err := scanSubmissionRows(rows)
	if err != nil {
		return submissionListItem{}, err
	}
	if len(items) == 0 {
		return submissionListItem{}, pgx.ErrNoRows
	}
	return items[0], nil
}

func populateSubmissionAppealState(ctx context.Context, tx pgx.Tx, item *submissionListItem, studentID int64) error {
	id, err := strconv.ParseInt(item.ID, 10, 64)
	if err != nil {
		return err
	}
	var appealFinal, objectionPending bool
	err = tx.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM appeal a
		    WHERE a.kind='student_appeal' AND a.target_type='submission'
		      AND a.target_id=$1 AND a.student_id=$2 AND a.status<>'draft'),
		  EXISTS (SELECT 1 FROM appeal a
		    WHERE a.target_type='submission' AND a.target_id=$1
		      AND a.status IN ('filed','reviewing','escalated')),
		  EXISTS (SELECT 1 FROM appeal a
		    WHERE a.kind='student_appeal' AND a.target_type='submission' AND a.target_id=$1
		      AND a.status='final'),
		  EXISTS (SELECT 1 FROM objection o
		    WHERE o.kind='submission' AND o.target_id=$1 AND o.status='submitted')
	`, id, studentID).Scan(&item.AppealRound, &item.AppealPending, &appealFinal, &objectionPending)
	if err != nil {
		return err
	}
	item.AppealsUsed = item.AppealRound
	item.CanAppeal = len(item.ForceRejection) == 0 && len(item.ForcedScore) == 0 && (item.Status == "scored" || item.Status == "locked") && item.AppealRound < 2 && !item.AppealPending && !appealFinal && !objectionPending
	return nil
}

func (s *Server) submission(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	item, err := loadOwnedSubmission(c.Request.Context(), tx, id, actor.UserID, false)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := populateSubmissionAppealState(c.Request.Context(), tx, &item, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	if current, currentErr := loadCurrentScheme(c.Request.Context(), tx); currentErr == nil {
		item.CanAppeal = item.CanAppeal && ensureCapability(current.Config, "appeal", time.Now()) == nil
		item.CanSelfForceReject = item.CanSelfForceReject && !current.Config.Window.LockedAt(time.Now())
	} else if !errors.Is(currentErr, pgx.ErrNoRows) {
		writeServiceError(c, currentErr)
		return
	} else {
		item.CanAppeal = false
		item.CanSelfForceReject = false
	}
	reviews, err := studentVisibleReviews(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	evidence, err := submissionEvidence(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	noteEvidence, err := submissionNoteEvidence(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	trail, err := submissionTrail(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	classificationHistory, err := submissionClassificationHistory(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "submission.read", "submission", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"submission": item, "reviews": reviews, "evidence": evidence, "noteEvidence": noteEvidence, "trail": trail, "classificationHistory": classificationHistory})
}

func submissionClassificationHistory(ctx context.Context, tx pgx.Tx, submissionID int64) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,before_category_key,before_item_key,after_category_key,after_item_key,
		       before_score::float8,after_score::float8,reason,created_at
		  FROM classification_resolution WHERE submission_id=$1 ORDER BY created_at,id
	`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var beforeCategory, beforeItem, afterCategory, afterItem, reason string
		var beforeScore, afterScore *float64
		var at time.Time
		if err := rows.Scan(&id, &beforeCategory, &beforeItem, &afterCategory, &afterItem, &beforeScore, &afterScore, &reason, &at); err != nil {
			return nil, err
		}
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "beforeCategory": beforeCategory, "beforeItemKey": beforeItem,
			"afterCategory": afterCategory, "afterItemKey": afterItem, "beforeScore": beforeScore,
			"afterScore": afterScore, "reason": reason, "at": at,
		})
	}
	return items, rows.Err()
}

func studentVisibleReviews(ctx context.Context, tx pgx.Tx, submissionID int64) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT decision,score::float8,reason,spent_seconds,created_at
		  FROM review WHERE submission_id=$1 AND superseded_at IS NULL ORDER BY created_at,id
	`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var decision, reason string
		var score float64
		var spent int
		var at time.Time
		if err := rows.Scan(&decision, &score, &reason, &spent, &at); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"decision": decision, "score": score, "why": reason, "spentSeconds": spent, "at": at})
	}
	return items, rows.Err()
}

func submissionEvidence(ctx context.Context, tx pgx.Tx, submissionID int64) ([]gin.H, error) {
	return submissionEvidenceKind(ctx, tx, submissionID, "claim")
}

func submissionNoteEvidence(ctx context.Context, tx pgx.Tx, submissionID int64) ([]gin.H, error) {
	return submissionEvidenceKind(ctx, tx, submissionID, "note")
}

func submissionNoteEvidenceForReviewer(ctx context.Context, tx pgx.Tx, submissionID, reviewerID int64) ([]gin.H, error) {
	return submissionEvidenceKindForCreator(ctx, tx, submissionID, "note", &reviewerID)
}

func submissionEvidenceKind(ctx context.Context, tx pgx.Tx, submissionID int64, kind string) ([]gin.H, error) {
	return submissionEvidenceKindForCreator(ctx, tx, submissionID, kind, nil)
}

func submissionEvidenceKindForCreator(ctx context.Context, tx pgx.Tx, submissionID int64, kind string, creatorID *int64) ([]gin.H, error) {
	var creator any
	if creatorID != nil {
		creator = *creatorID
	}
	rows, err := tx.Query(ctx, `
		SELECT id,object_key,filename,media_type,size_bytes,sha256,status,created_at
		  FROM evidence
		 WHERE submission_id=$1 AND kind=$2
		   AND ($3::bigint IS NULL OR created_by=$3)
		 ORDER BY id
	`, submissionID, kind, creator)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, size int64
		var key, name, mediaType, status string
		var sha *string
		var at time.Time
		if err := rows.Scan(&id, &key, &name, &mediaType, &size, &sha, &status, &at); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "key": key, "name": name, "mediaType": mediaType, "sizeBytes": size, "sha256": sha, "status": status, "uploadedAt": at})
	}
	return items, rows.Err()
}

func submissionTrail(ctx context.Context, tx pgx.Tx, submissionID int64) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT action,after_data,metadata,created_at
		  FROM audit_log WHERE resource_type='submission' AND resource_id=$1
		 ORDER BY created_at,id
	`, strconv.FormatInt(submissionID, 10))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var action string
		var after, metadata []byte
		var at time.Time
		if err := rows.Scan(&action, &after, &metadata, &at); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"action": action, "after": json.RawMessage(after), "metadata": json.RawMessage(metadata), "at": at})
	}
	return items, rows.Err()
}

func (s *Server) updateSubmission(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input submissionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "提交参数不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	before, err := loadOwnedSubmission(c.Request.Context(), tx, id, actor.UserID, true)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if before.Status != "draft" && before.Status != "pending" {
		writeError(c, http.StatusConflict, "not_editable", "该条目已有审核进展，不能直接修改", nil)
		return
	}
	var reviewCount int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM review WHERE submission_id=$1`, id).Scan(&reviewCount); err != nil {
		writeServiceError(c, err)
		return
	}
	if reviewCount > 0 {
		writeError(c, http.StatusConflict, "not_editable", "已有审核记录，后续改动请走申诉", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "edit", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "edit_closed", err.Error(), nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	// Still a draft: keep letting it be untitled. Once it is pending review the
	// title is already there and must not be cleared.
	prepared, err := prepareSubmission(current, input, time.Now(), before.Status != "draft")
	if err != nil {
		writeClaimError(c, err)
		return
	}
	_, err = tx.Exec(c.Request.Context(), `
		UPDATE submission SET scheme_id=$1,scheme_version=$2,category_key=$3,item_key=$4,
		       filed_category_key=CASE WHEN status='draft' THEN $3 ELSE filed_category_key END,
		       filed_item_key=CASE WHEN status='draft' THEN $4 ELSE filed_item_key END,
		       filed_rule_snapshot=CASE WHEN status='draft' THEN $8 ELSE filed_rule_snapshot END,
		       title=$5,claim=$6,requested_score=$7,rule_snapshot=$8,markdown_note=$9,
		       lock_version=lock_version+1,updated_at=now()
		 WHERE id=$10
	`, prepared.SchemeID, prepared.SchemeVersion, prepared.Category.Key, prepared.Item.Key, strings.TrimSpace(input.Title),
		[]byte(input.Claim), prepared.Requested, prepared.Snapshot, input.Note, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "submission.updated", "submission", strconv.FormatInt(id, 10), before, map[string]any{"category": prepared.Category.Key, "itemKey": prepared.Item.Key, "title": input.Title}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": before.Status, "requestedScore": prepared.Requested})
}

func (s *Server) deleteSubmission(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	item, err := loadOwnedSubmission(c.Request.Context(), tx, id, actor.UserID, true)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if item.Status != "draft" && item.Status != "pending" {
		writeError(c, http.StatusConflict, "not_deletable", "该条目不能直接删除，后续异议请走申诉", nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	var reviewCount int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM review WHERE submission_id=$1`, id).Scan(&reviewCount); err != nil {
		writeServiceError(c, err)
		return
	}
	if reviewCount > 0 {
		writeError(c, http.StatusConflict, "not_deletable", "已有审核记录，不能撤回", nil)
		return
	}
	objectKeys, err := evidenceObjectKeys(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "submission.deleted", "submission", strconv.FormatInt(id, 10), item, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM submission WHERE id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	for _, objectKey := range objectKeys {
		s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	}
	c.Status(http.StatusNoContent)
}

func evidenceObjectKeys(ctx context.Context, tx pgx.Tx, submissionID int64) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT object_key FROM evidence WHERE submission_id=$1`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// withdrawSubmission 把在审的条目退回草稿，是 submitSubmission 的反向操作。
//
// 学生想改一条已提交的材料时，原来只有"删除"一条路：内容连同佐证一起没了，得
// 从头再传一遍。退回草稿把东西原样留着，同时因为审核队列查的是
// status IN ('pending','consensus')，这一条立刻从所有审核人手上消失——不存在
// "审核人正看着、学生同时在改"的竞态，也就不必去动 assignment（它是按类别配
// 的，不是每条一个任务，没有任务行要清）。
//
// 已经有人写下审核记录就不许退：那条结论是针对当时那份材料给的，退回去改完再
// 提交，结论就对不上了。这一条与 deleteSubmission 的判断保持一致。
func (s *Server) withdrawSubmission(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	item, err := loadOwnedSubmission(c.Request.Context(), tx, id, actor.UserID, true)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if item.Status == "draft" {
		// 幂等：重复点或并发点第二次时当作成功，别抛一个学生看不懂的冲突。
		c.JSON(http.StatusOK, gin.H{"id": item.ID, "status": "draft"})
		return
	}
	if item.Status != "pending" {
		writeError(c, http.StatusConflict, "not_withdrawable", "该条目已进入定分或申诉流程，不能退回草稿", nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	var reviewCount int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM review WHERE submission_id=$1`, id).Scan(&reviewCount); err != nil {
		writeServiceError(c, err)
		return
	}
	if reviewCount > 0 {
		writeError(c, http.StatusConflict, "not_withdrawable", "已有审核记录，不能退回草稿", nil)
		return
	}
	// WHERE 上再卡一次 status：两个并发请求只会有一个真的改到行。
	tag, err := tx.Exec(c.Request.Context(), `
		UPDATE submission SET status='draft',submitted_at=NULL,updated_at=now(),lock_version=lock_version+1
		 WHERE id=$1 AND status='pending'
	`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(c, http.StatusConflict, "not_withdrawable", "该条目状态刚刚变化，请刷新后重试", nil)
		return
	}
	if err := appendAudit(c, tx, "submission.withdrawn", "submission", strconv.FormatInt(id, 10),
		map[string]any{"status": "pending"}, map[string]any{"status": "draft"}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": "draft"})
}

func (s *Server) submitSubmission(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	item, err := loadOwnedSubmission(c.Request.Context(), tx, id, actor.UserID, true)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if item.Status != "draft" {
		if item.Status == "pending" || item.Status == "consensus" {
			c.JSON(http.StatusOK, gin.H{"id": item.ID, "status": item.Status})
			return
		}
		writeError(c, http.StatusConflict, "not_submittable", "该条目当前不能提交", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "submit", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "submission_closed", err.Error(), nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	// Entering review: this is where a title stops being optional.
	prepared, err := prepareSubmission(current, submissionInput{Category: item.Category, ItemKey: item.ItemKey, Title: item.Title, Claim: item.Claim, Note: item.Note}, time.Now(), true)
	if err != nil {
		writeClaimError(c, err)
		return
	}
	if prepared.Item.Evidence != nil && prepared.Item.Evidence.Required {
		var ready int
		if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM evidence WHERE submission_id=$1 AND status='ready' AND kind='claim'`, id).Scan(&ready); err != nil {
			writeServiceError(c, err)
			return
		}
		if ready == 0 {
			writeError(c, http.StatusUnprocessableEntity, "evidence_required", "该小项至少需要一份已上传佐证", nil)
			return
		}
	}
	_, err = tx.Exec(c.Request.Context(), `
		UPDATE submission SET scheme_id=$1,scheme_version=$2,category_key=$3,item_key=$4,
		       filed_category_key=$3,filed_item_key=$4,filed_rule_snapshot=$5,
		       requested_score=$6,rule_snapshot=$5,
		       status='pending',submitted_at=now(),updated_at=now(),lock_version=lock_version+1
		 WHERE id=$7 AND status='draft'
	`, prepared.SchemeID, prepared.SchemeVersion, prepared.Category.Key, prepared.Item.Key, prepared.Snapshot, prepared.Requested, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "submission.submitted", "submission", strconv.FormatInt(id, 10), map[string]any{"status": "draft"}, map[string]any{"status": "pending"}, map[string]any{"schemeVersion": prepared.SchemeVersion}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.SubmissionCreatedEvent(), events.SubmissionCreatedPayload{
		SubmissionID: id, StudentID: actor.UserID, CategoryKey: prepared.Category.Key,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": "pending", "submittedAt": time.Now().UTC()})
}
