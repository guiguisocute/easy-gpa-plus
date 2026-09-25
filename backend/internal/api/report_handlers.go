// 学生匿名举报。
//
// 综测小组的「扣分与异议」是实名提案，走班级管理员终裁。这一套是给小组以外的
// 普通学生用的，两处不同：
//
//  1. 匿名。举报人不在 report 表里，单独存进 report_reporter，而业务连接对那张表
//     只有 INSERT 权限（迁移 000041）。审计也走 appendAnonymousAudit：班级管理员
//     读得到本班审计，actor_id / IP / UA 里任何一样留下来，匿名就已经破了。
//  2. 不进小组提案队列。举报先由两名审核人背靠背复核，两人一致即落定，
//     不一致才升给班级管理员，作为普通冲突条目在仲裁台上处理。
//
// 举报对象和小组提案一样有三种：基础项、扣分项、已定分条目。三种共同的硬约束是
// 「举报只能让分变差」——举报不是送分的通道，允许往上提就等于开了一条互相刷分的路。
//
// 为什么要先过两个人：扣分是全系统最容易起争议的地方，而举报比小组提案更容易被
// 当成攻击手段。让举报直接落到班管手上，班管就成了唯一的过滤器，一旦量大就只能
// 草草签字。两个人先看一遍，滤掉的是"我看他不顺眼"这一类，留给班管的是真有分歧的。

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/scheme"
)

// 一个人一天最多提这么多条。限流靠 report_count_today，它只能从人查到条数，
// 反过来查不了。这不是为了拦住有心人，是为了让"顺手刷一轮"这种事有代价。
const reportDailyLimit = 10

type reportInput struct {
	Kind          string   `json:"kind"`
	StudentUserID jsonID   `json:"studentUserId"`
	Category      string   `json:"category"`
	ItemKey       string   `json:"itemKey"`
	TargetID      *jsonID  `json:"targetId"`
	Quantity      *float64 `json:"quantity"`
	ProposedScore *float64 `json:"proposedScore"`
	Basis         string   `json:"basis"`
}

type reportTarget struct {
	StudentID     int64
	SchemeID      int64
	CategoryName  string
	ItemName      string
	TargetID      *int64
	CurrentScore  *float64
	ProposedScore float64
	Quantity      *float64
}

func reportFailure(status int, code, message string) error {
	return objectionProblem{status: status, code: code, message: message}
}

// 举报只能让被举报人的分变差。三种对象的"变差"方向不同——扣分项是往负里走，
// 基础项和已定分条目是往低里走——但判定都归到这一句上。
func rejectIfNotWorse(proposed float64, current *float64) error {
	if current == nil {
		return nil
	}
	if scheme.NewPoints(proposed) >= scheme.NewPoints(*current) {
		return reportFailure(http.StatusUnprocessableEntity, "not_worse",
			"举报只能主张把分改低。认为分给少了，请本人走申诉")
	}
	return nil
}

func validateReport(ctx context.Context, tx pgx.Tx, actor Actor, input *reportInput) (reportTarget, error) {
	input.Kind = strings.TrimSpace(input.Kind)
	input.Category = strings.TrimSpace(input.Category)
	input.ItemKey = strings.TrimSpace(input.ItemKey)
	input.Basis = strings.TrimSpace(input.Basis)
	if input.Kind != "base" && input.Kind != "penalty" && input.Kind != "submission" {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "report_invalid", "举报类型不正确")
	}
	if count := utf8.RuneCountInString(input.Basis); count < 10 || count > 5000 {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "report_invalid", "举报必须写 10—5000 字说明")
	}
	studentID := int64(input.StudentUserID)
	if studentID == actor.UserID {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "self_report", "不能举报自己")
	}
	var role, status string
	if err := tx.QueryRow(ctx, `SELECT role,status FROM app_user WHERE id=$1`, studentID).Scan(&role, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return reportTarget{}, reportFailure(http.StatusNotFound, "student_not_found", "班级成员不存在")
		}
		return reportTarget{}, err
	}
	if !isScorableClassMember(role, status) {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "not_student", "只能举报在册班级成员")
	}
	if input.Kind == "submission" {
		return validateSubmissionReport(ctx, tx, input, studentID)
	}
	return validateBaseReport(ctx, tx, input, studentID)
}

// 已定分条目。举报人主张这一条不该给这么多分。
func validateSubmissionReport(ctx context.Context, tx pgx.Tx, input *reportInput, studentID int64) (reportTarget, error) {
	if input.TargetID == nil || input.ProposedScore == nil {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "target_required", "对已定分条目举报必须选中条目并给出建议分")
	}
	if input.Quantity != nil {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "quantity_invalid", "只有扣分项可以填写次数")
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
		return reportTarget{}, reportFailure(http.StatusNotFound, "target_not_found", "已定分条目不存在")
	}
	if err != nil {
		return reportTarget{}, err
	}
	if targetStudent != studentID {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "target_mismatch", "所选条目不属于这名学生")
	}
	if submissionStatus != "scored" && submissionStatus != "locked" {
		return reportTarget{}, reportFailure(http.StatusConflict, "not_reportable", "只有已定分条目可以举报")
	}
	var snapshot ruleSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return reportTarget{}, err
	}
	if err := scoreWithinRule(snapshot.Item.ScoreRule, *input.ProposedScore); err != nil {
		return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "score_invalid", err.Error())
	}
	if err := rejectIfNotWorse(*input.ProposedScore, current); err != nil {
		return reportTarget{}, err
	}
	locked, err := objectionTargetHasOpenAppeal(ctx, tx, "submission", int64(*input.TargetID))
	if err != nil {
		return reportTarget{}, err
	}
	if locked {
		return reportTarget{}, reportFailure(http.StatusConflict, "target_appealing", "本人正在申诉这一笔，等申诉结束后再举报")
	}
	targetID := int64(*input.TargetID)
	input.Category, input.ItemKey = category, itemKey
	return reportTarget{
		StudentID: studentID, SchemeID: schemeID, TargetID: &targetID, CurrentScore: current,
		CategoryName: snapshot.CategoryName, ItemName: snapshot.Item.Name, ProposedScore: *input.ProposedScore,
	}, nil
}

// 基础项与扣分项。扣分项报次数（分值 = 次数 × 单价），基础项报建议得分。
func validateBaseReport(ctx context.Context, tx pgx.Tx, input *reportInput, studentID int64) (reportTarget, error) {
	current, err := loadCurrentScheme(ctx, tx)
	if err != nil {
		return reportTarget{}, err
	}
	categoryName, itemName, full, per, err := schemeItemLabels(current.Config, input.Category, input.ItemKey, input.Kind)
	if err != nil {
		return reportTarget{}, err
	}

	var proposed float64
	switch input.Kind {
	case "penalty":
		if input.Quantity == nil || *input.Quantity <= 0 || *input.Quantity > 100 {
			return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "quantity_invalid", "扣分项必须填写 0 与 100 之间的次数")
		}
		// 分值由方案里的单价乘出来，前端报的是次数，不是分数。
		proposed = scheme.NewPoints(*input.Quantity * *per).Float64()
	default:
		if input.Quantity != nil {
			return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "quantity_invalid", "只有扣分项可以填写次数")
		}
		if input.ProposedScore == nil || full == nil {
			return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "report_invalid", "基础项举报必须给出建议得分")
		}
		points := scheme.NewPoints(*input.ProposedScore)
		if points < 0 || points > scheme.NewPoints(*full) {
			return reportTarget{}, reportFailure(http.StatusUnprocessableEntity, "score_invalid", "基础项建议分必须在 0 与满分之间")
		}
		proposed = points.Float64()
	}

	// 基础项没有记录时按方案满分算当前分——学生的基础分默认就是满分。
	var rowID int64
	var recorded float64
	err = tx.QueryRow(ctx, `
		SELECT id,score::float8 FROM base_score
		 WHERE student_id=$1 AND scheme_id=$2 AND category_key=$3 AND item_key=$4 AND kind=$5
	`, studentID, current.ID, input.Category, input.ItemKey, input.Kind).Scan(&rowID, &recorded)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return reportTarget{}, err
	}
	var targetID *int64
	var currentScore *float64
	if err == nil {
		targetID, currentScore = &rowID, &recorded
		appealType := "base_score"
		if input.Kind == "penalty" {
			appealType = "penalty_score"
		}
		locked, lockErr := objectionTargetHasOpenAppeal(ctx, tx, appealType, rowID)
		if lockErr != nil {
			return reportTarget{}, lockErr
		}
		if locked {
			return reportTarget{}, reportFailure(http.StatusConflict, "target_appealing", "本人正在申诉这一笔，等申诉结束后再举报")
		}
	} else if input.Kind == "base" && full != nil {
		baseline := *full
		currentScore = &baseline
	}
	// 扣分项是累加的，没有"变好"这回事，所以只对基础项判方向。
	if input.Kind == "base" {
		if err := rejectIfNotWorse(proposed, currentScore); err != nil {
			return reportTarget{}, err
		}
	}
	return reportTarget{
		StudentID: studentID, SchemeID: current.ID, TargetID: targetID, CurrentScore: currentScore,
		CategoryName: categoryName, ItemName: itemName, ProposedScore: proposed, Quantity: input.Quantity,
	}, nil
}

// 派两名复核人。这里不走提交材料那套负载均衡计划器：那个计划器围绕 submission 建模，
// 为一条举报把整张计划算一遍既慢又绕。举报要的只是"挑两个手头最闲、和这件事没关系的人"。
//
// 排除三种人：被举报人自己、举报人自己（否则他能自审自己的举报），以及被暂停派发的。
// 举报人要排除，但表里没有举报人——所以由调用方把 actor.UserID 传进来，
// 这是举报人身份唯一一次离开 report_reporter，且只用于排除、不写进任何一行。
func assignReportReviewers(ctx context.Context, tx pgx.Tx, classID, reportID, studentID, reporterID int64) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT u.id
		  FROM app_user u
		 WHERE u.status='active' AND u.role IN ('group','class_admin')
		   AND NOT u.dispatch_paused AND u.id<>$1 AND u.id<>$2
		 ORDER BY (SELECT count(*) FROM report_reviewer rr
		            WHERE rr.reviewer_id=u.id AND rr.decided_at IS NULL),
		          (SELECT count(*) FROM submission_reviewer sr
		            WHERE sr.reviewer_id=u.id AND sr.active),
		          u.id
		 LIMIT 2
	`, studentID, reporterID)
	if err != nil {
		return 0, err
	}
	reviewers := make([]int64, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		reviewers = append(reviewers, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(reviewers) < 2 {
		return 0, reportFailure(http.StatusConflict, "no_reviewers", "班里可用的审核人不足两名，暂时无法受理举报")
	}
	for _, reviewerID := range reviewers {
		if _, err := tx.Exec(ctx, `
			INSERT INTO report_reviewer (class_id,report_id,reviewer_id) VALUES ($1,$2,$3)
		`, classID, reportID, reviewerID); err != nil {
			return 0, err
		}
	}
	return len(reviewers), nil
}

func (s *Server) createReport(c *gin.Context) {
	var input reportInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "举报参数不正确", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "studentReport", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "report_closed", "本班没有开放学生举报", nil)
		return
	}
	target, err := validateReport(c.Request.Context(), tx, actor, &input)
	if err != nil {
		if !writeObjectionFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}

	var already bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT report_already_filed($1,$2,$3,$4,$5,$6,$7)`,
		actor.ClassID, actor.UserID, target.StudentID, target.SchemeID, input.Kind, input.Category, input.ItemKey).Scan(&already); err != nil {
		writeServiceError(c, err)
		return
	}
	if already {
		writeError(c, http.StatusConflict, "report_exists", "你已经就这一条提过举报，正在处理中", nil)
		return
	}
	var today int
	if err := tx.QueryRow(c.Request.Context(), `SELECT report_count_today($1,$2)`, actor.ClassID, actor.UserID).Scan(&today); err != nil {
		writeServiceError(c, err)
		return
	}
	if today >= reportDailyLimit {
		writeError(c, http.StatusTooManyRequests, "report_daily_limit", "今天提交的举报已达上限，明天再来", nil)
		return
	}

	var reportID int64
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO report (class_id,kind,student_id,scheme_id,category_key,item_key,target_id,
		                    current_score,quantity,proposed_score,basis)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id
	`, actor.ClassID, input.Kind, target.StudentID, target.SchemeID, input.Category, input.ItemKey,
		target.TargetID, target.CurrentScore, target.Quantity, target.ProposedScore, input.Basis).Scan(&reportID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO report_reporter (report_id,class_id,reporter_id) VALUES ($1,$2,$3)
	`, reportID, actor.ClassID, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	g, err := loadGovernance(c.Request.Context(), tx, actor.ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	assigned := 0
	if g.Mode == "collective" {
		err = s.ensureGovernanceCase(c, "report", reportID, true)
	} else {
		assigned, err = assignReportReviewers(c.Request.Context(), tx, actor.ClassID, reportID, target.StudentID, actor.UserID)
	}
	if err != nil {
		if !writeObjectionFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	// 匿名：不记 actor、不记 IP、不记 UA，metadata 里也不放任何指向举报人的东西。
	if err := appendAnonymousAudit(c, tx, "report.filed", "report", strconv.FormatInt(reportID, 10),
		map[string]any{"studentId": target.StudentID, "kind": input.Kind, "itemKey": input.ItemKey, "reviewers": assigned}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ReportFiledEvent(), events.ReportFiledPayload{
		ReportID: reportID, StudentID: target.StudentID,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(reportID, 10), "status": "reviewing", "reviewers": assigned})
}

// 我提过的举报。只回自己的，靠 report_ids_by_reporter 从人查到举报——
// 反向查不了，所以这个接口不可能被用来问"这条是谁报的"。
func (s *Server) myReports(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT r.id,r.kind,r.category_key,r.item_key,r.quantity::float8,r.current_score::float8,
		       r.proposed_score::float8,r.basis,r.status,r.final_score::float8,r.decision_reason,
		       r.created_at,r.decided_at,
		       (SELECT count(*) FROM report_reviewer rr WHERE rr.report_id=r.id),
		       (SELECT count(*) FROM report_reviewer rr WHERE rr.report_id=r.id AND rr.decided_at IS NOT NULL)
		  FROM report r
		 WHERE r.id IN (SELECT report_ids_by_reporter($1,$2))
		 ORDER BY r.created_at DESC,r.id DESC
	`, actor.ClassID, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		var kind, category, itemKey, basis, status string
		var quantity, currentScore, finalScore *float64
		var proposed float64
		var decisionReason *string
		var createdAt time.Time
		var decidedAt *time.Time
		var expected, decided int
		if err := rows.Scan(&id, &kind, &category, &itemKey, &quantity, &currentScore, &proposed, &basis, &status,
			&finalScore, &decisionReason, &createdAt, &decidedAt, &expected, &decided); err != nil {
			writeServiceError(c, err)
			return
		}
		categoryName, itemName := reportItemLabels(current.Config, kind, category, itemKey)
		ids = append(ids, id)
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "kind": kind, "category": category, "categoryName": categoryName,
			"itemKey": itemKey, "itemName": itemName, "quantity": quantity, "currentScore": currentScore,
			"proposedScore": proposed, "basis": basis, "status": status, "finalScore": finalScore,
			"decisionReason": decisionReason, "createdAt": createdAt, "decidedAt": decidedAt,
			"expectedReviews": expected, "decidedReviews": decided,
			// 被举报人姓名不回给举报人：他知道自己报了谁，接口没必要再确认一次，
			// 万一这个响应被人从背后看到，也不至于把两件事对上。
		})
	}
	// 附件另起一趟查：游标没关就发第二条查询，pgx 会报 conn busy。
	rows.Close()
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	for at, id := range ids {
		files, err := reportNoteEvidence(c.Request.Context(), tx, id)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		items[at]["evidence"] = files
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// 已定分条目的名字在提交那一行上，基础项和扣分项的在方案里。
func reportItemLabels(config scheme.Config, kind, categoryKey, itemKey string) (string, string) {
	for _, category := range config.Categories {
		if category.Key != categoryKey {
			continue
		}
		if kind == "penalty" {
			for _, item := range category.PenaltyItems {
				if item.Key == itemKey {
					return category.Name, item.Name
				}
			}
		} else {
			for _, item := range category.BaseItems {
				if item.Key == itemKey {
					return category.Name, item.Name
				}
			}
			for _, item := range category.Items {
				if item.Key == itemKey {
					return category.Name, item.Name
				}
			}
		}
		return category.Name, itemKey
	}
	return categoryKey, itemKey
}

// 全班计分总览。开关打开后学生看得到每个人的基础分、扣分和已定分条目，但看不到
// 依据原文和佐证附件——依据里常写着处分文号、宿舍检查记录这类东西，那是给审核人和
// 班管看的。举报要判断的是"这一条给得对不对"，分数和项目已经够了。
func (s *Server) classPenalties(c *gin.Context) {
	tx := mustTx(c)
	actor := mustActor(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !current.Config.Capabilities.StudentReport && !current.Config.Window.PublicityOpen(time.Now()) {
		writeError(c, http.StatusForbidden, "report_closed", "本班没有开放学生举报", nil)
		return
	}

	type entry struct {
		kind, category, categoryName, itemKey, itemName string
		targetID                                        string
		source                                          string
		score                                           float64
		updatedAt                                       time.Time
	}
	byStudent := make(map[int64][]entry)

	// 已经落库的基础项与扣分项。只取分数，basis 一列根本不查出来——查出来再决定不下发，
	// 迟早有人在下一次改动里顺手把它加进 JSON。
	type recordedRow struct {
		id        int64
		score     float64
		updatedAt time.Time
	}
	// 按结构体做键，不拼字符串：拼起来就得挑一个分隔符，而分隔符只要在某个
	// item_key 里出现过一次，两项就会互相顶掉。
	type recordedKey struct{ category, kind, itemKey string }
	recorded := make(map[int64]map[recordedKey]recordedRow)
	baseRows, err := tx.Query(c.Request.Context(), `
		SELECT b.id,b.student_id,b.kind,b.category_key,b.item_key,b.score::float8,b.updated_at
		  FROM base_score b WHERE b.scheme_id=$1
	`, current.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for baseRows.Next() {
		var id, studentID int64
		var kind, category, itemKey string
		var score float64
		var updatedAt time.Time
		if err := baseRows.Scan(&id, &studentID, &kind, &category, &itemKey, &score, &updatedAt); err != nil {
			baseRows.Close()
			writeServiceError(c, err)
			return
		}
		if recorded[studentID] == nil {
			recorded[studentID] = make(map[recordedKey]recordedRow)
		}
		recorded[studentID][recordedKey{category, kind, itemKey}] = recordedRow{id: id, score: score, updatedAt: updatedAt}
	}
	baseRows.Close()
	if err := baseRows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}

	// 已定分条目。举报人要能对"这一条凭什么给 40 分"提出异议。
	subRows, err := tx.Query(c.Request.Context(), `
		SELECT s.id,s.student_id,s.category_key,s.item_key,s.title,s.final_score::float8,
		       COALESCE(s.scored_at,s.updated_at),s.source
		  FROM submission s
		 WHERE s.status IN ('scored','locked','appealing','arbitrating') AND s.final_score IS NOT NULL
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for subRows.Next() {
		var id, studentID int64
		var category, itemKey, title, source string
		var score float64
		var updatedAt time.Time
		if err := subRows.Scan(&id, &studentID, &category, &itemKey, &title, &score, &updatedAt, &source); err != nil {
			subRows.Close()
			writeServiceError(c, err)
			return
		}
		categoryName, _ := reportItemLabels(current.Config, "submission", category, itemKey)
		byStudent[studentID] = append(byStudent[studentID], entry{
			kind: "submission", category: category, categoryName: categoryName, itemKey: itemKey,
			itemName: title, targetID: strconv.FormatInt(id, 10), score: score, updatedAt: updatedAt, source: source,
		})
	}
	subRows.Close()
	if err := subRows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}

	// Student reports expose no attachment metadata, including filenames and
	// object keys. Published review reasons are loaded separately on expansion.

	rows, err := tx.Query(c.Request.Context(), `
		SELECT u.id,u.sid,u.name,
		       (SELECT count(*) FROM report r
		         WHERE r.student_id=u.id AND (r.kind='submission' OR r.scheme_id=$1) AND r.status IN ('reviewing','escalated'))
		  FROM app_user u
		 WHERE u.role IN ('student','group','class_admin') AND u.status='active'
		 ORDER BY u.sid,u.id
	`, current.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var userID int64
		var sid, name string
		var openReports int
		if err := rows.Scan(&userID, &sid, &name, &openReports); err != nil {
			writeServiceError(c, err)
			return
		}
		entries := make([]gin.H, 0)
		penaltyTotal := 0.0
		/* 基础项按方案铺开，不是只列已经落库的那几行。
		   base_score 里没有行不代表这一项没有分——学生的基础分默认就是满分，
		   要等有人调过才会写进库。只列已落库的行，等于一个还没被扣过分的班
		   在这一页上什么都看不到，而小组的「扣分与异议」那边是满的。
		   两个台面摆的必须是同一份东西，所以这里照着 reviewStudentScorecard 的口径来：
		   走方案里的每一项，有记录就盖上去，没有就按满分。
		   studentClaim 的那些跳过——它们由学生自己申报，会以已定分条目的身份出现。 */
		for _, category := range current.Config.Categories {
			for _, item := range category.BaseItems {
				if item.StudentClaim != nil {
					continue
				}
				row := gin.H{
					"kind": "base", "category": category.Key, "categoryName": category.Name,
					"itemKey": item.Key, "itemName": item.Name, "targetId": nil,
					"score": item.Full, "updatedAt": nil, "evidence": []gin.H{},
				}
				if hit, ok := recorded[userID][recordedKey{category.Key, "base", item.Key}]; ok {
					row["targetId"], row["score"], row["updatedAt"] = strconv.FormatInt(hit.id, 10), hit.score, hit.updatedAt
				}
				entries = append(entries, row)
			}
			/* 扣分项只列已经记在册的。方案里的全量清单走 penaltyItems 那一份——
			   举报扣分是从无到有，不是对着已有的一行提意见；把没记过的也当成
			   0 分的条目铺出来，"已记在册"那一行就会写满一堆没发生过的事。 */
			for _, item := range category.PenaltyItems {
				hit, ok := recorded[userID][recordedKey{category.Key, "penalty", item.Key}]
				if !ok {
					continue
				}
				penaltyTotal += hit.score
				entries = append(entries, gin.H{
					"kind": "penalty", "category": category.Key, "categoryName": category.Name,
					"itemKey": item.Key, "itemName": item.Name,
					"targetId": strconv.FormatInt(hit.id, 10), "score": hit.score,
					"updatedAt": hit.updatedAt, "evidence": []gin.H{},
				})
			}
		}
		for _, row := range byStudent[userID] {
			entries = append(entries, gin.H{
				"kind": row.kind, "category": row.category, "categoryName": row.categoryName,
				"itemKey": row.itemKey, "itemName": row.itemName, "targetId": row.targetID,
				"score": row.score, "updatedAt": row.updatedAt, "evidence": []gin.H{}, "source": row.source,
			})
		}
		items = append(items, gin.H{
			"userId": strconv.FormatInt(userID, 10), "sid": sid, "name": name,
			"self": userID == actor.UserID, "penaltyTotal": scheme.NewPoints(penaltyTotal).Float64(),
			"openReports": openReports, "entries": entries,
		})
	}

	// 扣分项要单独给一份清单：它可以在"还没有任何记录"时被举报，
	// 前两类都是对着已有的一行提意见，扣分项是从无到有。
	penaltyItems := make([]gin.H, 0)
	for _, category := range current.Config.Categories {
		for _, item := range category.PenaltyItems {
			penaltyItems = append(penaltyItems, gin.H{
				"category": category.Key, "categoryName": category.Name,
				"itemKey": item.Key, "itemName": item.Name, "perScore": item.Per,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "penaltyItems": penaltyItems, "dailyLimit": reportDailyLimit})
}
