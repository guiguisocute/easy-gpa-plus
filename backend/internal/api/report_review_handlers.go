/* 举报的背靠背复核，以及班级管理员对分歧的终裁。

   形状照搬初审：两个人各判一次，互相看不到对方判了什么，两人一致即落定，不一致升给
   班级管理员。差别只有一处，而这一处正是这一页存在的理由——初审判的是学生自己申报的
   加分，这里判的是别人替他报上来的扣分。举报人是谁，复核人无从得知，也不该得知。

   三种结论：
   · uphold —— 举报属实，按举报的次数和单价扣；
   · adjust —— 属实但次数不对，改分（仍必须是负值，且不能比举报的还狠）；
   · reject —— 举报不成立，计 0，不扣分。

   为什么 adjust 不许扣得比举报更狠：复核人手上只有举报人给的那点材料，比举报人自己
   主张的还要重，说明他用的是材料以外的东西。要加码就自己走小组提案，实名签字。 */

package api

import (
	"context"
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

type reportRecord struct {
	ID, StudentID, SchemeID  int64
	Kind                     string
	TargetID                 *int64
	CurrentScore             *float64
	Category, ItemKey, Basis string
	Quantity                 *float64
	ProposedScore            float64
	Status                   string
	FinalScore               *float64
	DecisionReason           *string
	CreatedAt                time.Time
	DecidedAt                *time.Time
	StudentSID, StudentName  string
	AssignmentID             int64
	Expected, Decided        int
	MyDecision, MyReason     *string
	MyScore                  *float64
	MySpentSeconds           *int
	MyDecidedAt              *time.Time
	CategoryName, ItemName   string
	PerScore                 float64
	AssignedAt               time.Time
}

func loadReportForReviewer(ctx context.Context, tx pgx.Tx, reportID, reviewerID int64, lock bool) (reportRecord, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE OF r"
	}
	var row reportRecord
	err := tx.QueryRow(ctx, `
		SELECT r.id,r.student_id,r.scheme_id,r.kind,r.target_id,r.current_score::float8,
		       r.category_key,r.item_key,r.quantity::float8,
		       r.proposed_score::float8,r.basis,r.status,r.final_score::float8,r.decision_reason,
		       r.created_at,r.decided_at,student.sid,student.name,
		       me.id,me.assigned_at,me.decision,me.score::float8,me.reason,me.spent_seconds,me.decided_at,
		       (SELECT count(*) FROM report_reviewer rr WHERE rr.report_id=r.id),
		       (SELECT count(*) FROM report_reviewer rr WHERE rr.report_id=r.id AND rr.decided_at IS NOT NULL)
		  FROM report r
		  JOIN app_user student ON student.id=r.student_id
		  JOIN report_reviewer me ON me.report_id=r.id AND me.reviewer_id=$2
		 WHERE r.id=$1 AND r.student_id<>$2`+lockSQL, reportID, reviewerID).Scan(
		&row.ID, &row.StudentID, &row.SchemeID, &row.Kind, &row.TargetID, &row.CurrentScore,
		&row.Category, &row.ItemKey, &row.Quantity,
		&row.ProposedScore, &row.Basis, &row.Status, &row.FinalScore, &row.DecisionReason,
		&row.CreatedAt, &row.DecidedAt, &row.StudentSID, &row.StudentName,
		&row.AssignmentID, &row.AssignedAt, &row.MyDecision, &row.MyScore, &row.MyReason,
		&row.MySpentSeconds, &row.MyDecidedAt, &row.Expected, &row.Decided,
	)
	return row, err
}

func (s *Server) reviewReport(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	row, err := loadReportForReviewer(c.Request.Context(), tx, id, actor.UserID, false)
	if notFound(c, err, "举报") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	categoryName, itemName := reportItemLabels(current.Config, row.Kind, row.Category, row.ItemKey)
	var mine any
	if row.MyDecidedAt != nil {
		mine = gin.H{"decision": *row.MyDecision, "score": *row.MyScore, "reason": *row.MyReason,
			"spentSeconds": *row.MySpentSeconds, "at": *row.MyDecidedAt}
	}
	// 这一项现在是多少分。三种举报问的不是同一件事：
	//   penalty    —— 同一项已经扣了多少。复核人不知道这个就会重复扣。
	//   base       —— 这一项现在给了多少。
	//   submission —— 这一条现在认定多少分。
	// 一律读实时值；读不到才退回举报落库时存的那份快照（base_score 还没有行、
	// 或者条目被撤回的情况）。查错了表的后果不是显示难看，是复核人按 0 分起判。
	var recorded *float64
	var lookupErr error
	if row.Kind == "submission" {
		if row.TargetID != nil {
			lookupErr = tx.QueryRow(c.Request.Context(), `
				SELECT final_score::float8 FROM submission WHERE id=$1
			`, *row.TargetID).Scan(&recorded)
		}
	} else {
		lookupErr = tx.QueryRow(c.Request.Context(), `
			SELECT score::float8 FROM base_score
			 WHERE student_id=$1 AND scheme_id=$2 AND category_key=$3 AND item_key=$4 AND kind=$5
		`, row.StudentID, row.SchemeID, row.Category, row.ItemKey, row.Kind).Scan(&recorded)
	}
	if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
		writeServiceError(c, lookupErr)
		return
	}
	if recorded == nil {
		recorded = row.CurrentScore
	}
	// 举报人附上的材料。复核人本来只有一段自述可依据，有了这些才判得动。
	files, err := reportNoteEvidence(c.Request.Context(), tx, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "report.task_read", "report", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	notes, err := reportReviewNoteEvidence(c.Request.Context(), tx, id, actor)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": strconv.FormatInt(id, 10), "kind": row.Kind, "evidence": files, "noteEvidence": notes,
		"student": row.StudentName, "studentId": row.StudentSID,
		"category": row.Category, "categoryName": categoryName, "itemKey": row.ItemKey, "itemName": itemName,
		"quantity": row.Quantity, "proposedScore": row.ProposedScore, "basis": row.Basis,
		"status": row.Status, "createdAt": row.CreatedAt, "currentScore": recorded,
		"expectedReviews": row.Expected, "decidedReviews": row.Decided, "myReview": mine,
		// 举报人不下发，任何形式都不下发。
	})
}

type reportDecisionInput struct {
	Decision     string   `json:"decision"`
	Score        *float64 `json:"score"`
	Reason       string   `json:"reason"`
	SpentSeconds int      `json:"spentSeconds"`
}

// 举报被驳回时该回到哪个分。扣分项是"再扣多少"，驳回就是再扣 0；
// 基础项和已定分条目是"改成多少"，驳回就是维持当前分。这个区别不摆平的话，
// 一条把基础分改成 0 的举报和一条被驳回的举报会写出同一个结果。
func reportStatusQuo(row reportRecord) float64 {
	if row.Kind == "penalty" || row.CurrentScore == nil {
		return 0
	}
	return *row.CurrentScore
}

func validateReportDecision(row reportRecord, input reportDecisionInput) (string, float64, string, error) {
	decision := strings.TrimSpace(input.Decision)
	reason := strings.TrimSpace(input.Reason)
	if decision != "uphold" && decision != "adjust" && decision != "reject" {
		return "", 0, "", errors.New("复核结论不正确")
	}
	// 按字数而不是字节数：中文一个字三字节，用 len() 的话"太短"两个字就够 6 了。
	// 同 validateReassignReason。
	if count := utf8.RuneCountInString(reason); count < 6 || count > 5000 {
		return "", 0, "", errors.New("复核理由须为 6—5000 字")
	}
	quo := reportStatusQuo(row)
	switch decision {
	case "uphold":
		return decision, row.ProposedScore, reason, nil
	case "reject":
		return decision, quo, reason, nil
	default:
		if input.Score == nil {
			return "", 0, "", errors.New("改分必须填写认定分")
		}
		score := scheme.NewPoints(*input.Score)
		// 举报主张的那一头最重，维持现状的那一头最轻，改分只能落在两者之间。
		// 判得比举报还重，说明用的是举报材料以外的东西——那要走实名的小组提案。
		if score < scheme.NewPoints(row.ProposedScore) {
			return "", 0, "", errors.New("不能判得比举报主张的还重；要加码请走实名的小组提案")
		}
		if score > scheme.NewPoints(quo) {
			return "", 0, "", errors.New("复核不能把分改得比现在还高；那不是举报该做的事")
		}
		return decision, score.Float64(), reason, nil
	}
}

// 两条结论一致就落定。"一致"看的是最终的分，不是结论词：一个人 uphold、另一个人
// adjust 到同一个分，对被举报的学生来说是同一个结果，没有理由升给班管再走一遍。
// 落在"维持现状"那个分上就是驳回，别的都是改分生效。
func reportOutcome(scores []float64, quo float64) (string, float64, bool) {
	if len(scores) < 2 {
		return "reviewing", 0, false
	}
	first := scheme.NewPoints(scores[0])
	for _, value := range scores[1:] {
		if scheme.NewPoints(value) != first {
			return "escalated", 0, true
		}
	}
	if first == scheme.NewPoints(quo) {
		return "dismissed", quo, false
	}
	return "applied", first.Float64(), false
}

func (s *Server) decideReport(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input reportDecisionInput
	if err := c.ShouldBindJSON(&input); err != nil || input.SpentSeconds < 0 || input.SpentSeconds > 24*60*60 {
		writeError(c, http.StatusBadRequest, "invalid_request", "复核结论参数不正确", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	row, err := loadReportForReviewer(c.Request.Context(), tx, id, actor.UserID, true)
	if notFound(c, err, "举报") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if row.Status != "reviewing" {
		writeError(c, http.StatusConflict, "not_reviewable", "这条举报当前不接受复核结论", nil)
		return
	}
	if row.MyDecidedAt != nil {
		writeError(c, http.StatusConflict, "already_reviewed", "你已经复核过这条举报", nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "review", time.Now()); err != nil {
		writeError(c, http.StatusConflict, "review_closed", err.Error(), nil)
		return
	}
	decision, score, reason, err := validateReportDecision(row, input)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "report_review_invalid", err.Error(), nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE report_reviewer SET decision=$1,score=$2,reason=$3,spent_seconds=$4,decided_at=now()
		 WHERE report_id=$5 AND reviewer_id=$6
	`, decision, score, reason, input.SpentSeconds, id, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}

	rows, err := tx.Query(c.Request.Context(), `
		SELECT score::float8 FROM report_reviewer WHERE report_id=$1 AND decided_at IS NOT NULL ORDER BY reviewer_id
	`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	scores := make([]float64, 0, 2)
	for rows.Next() {
		var value float64
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		scores = append(scores, value)
	}
	rows.Close()

	status := "reviewing"
	var settled float64
	conflict := false
	if len(scores) >= row.Expected {
		status, settled, conflict = reportOutcome(scores, reportStatusQuo(row))
	}
	if status == "reviewing" {
		if err := appendAudit(c, tx, "report.reviewed", "report", strconv.FormatInt(id, 10), nil,
			map[string]any{"status": status}, map[string]any{"decision": decision, "score": score}); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ReportReviewedEvent(), events.ReportReviewedPayload{
			ReportID: id, Status: status,
		}); err != nil {
			writeServiceError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"reportStatus": status, "bothDecided": false, "conflict": false, "finalScore": nil})
		return
	}

	if err := settleReport(c, tx, actor, id, row, status, settled); err != nil {
		if !writeObjectionFailure(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	if conflict {
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ReportEscalatedEvent(), events.ReportEscalatedPayload{
			ReportID: id, Status: status, StudentID: row.StudentID,
		}); err != nil {
			writeServiceError(c, err)
			return
		}
	} else {
		if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ReportDecidedEvent(), events.ReportDecidedPayload{
			ReportID: id, Status: status, StudentID: row.StudentID,
		}); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	var final any
	if status == "applied" {
		final = settled
	}
	c.JSON(http.StatusCreated, gin.H{"reportStatus": status, "bothDecided": true, "conflict": conflict, "finalScore": final})
}

// 落定一条举报。applied 才真正写分；dismissed 只是记账，escalated 留给班管。
// 写分走 base_score 的 upsert，和小组提案生效是同一条路——扣分最终只有一个来源。
func settleReport(c *gin.Context, tx pgx.Tx, actor Actor, id int64, row reportRecord, status string, settled float64) error {
	ctx := c.Request.Context()
	switch status {
	case "escalated":
		if _, err := tx.Exec(ctx, `UPDATE report SET status='escalated',updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		return appendAudit(c, tx, "report.escalated", "report", strconv.FormatInt(id, 10),
			map[string]any{"status": "reviewing"}, map[string]any{"status": "escalated"}, nil)
	case "dismissed":
		if _, err := tx.Exec(ctx, `
			UPDATE report SET status='dismissed',final_score=0,decision_reason='两名复核人一致认为举报不成立',
			       decided_at=now(),updated_at=now() WHERE id=$1
		`, id); err != nil {
			return err
		}
		return appendAudit(c, tx, "report.dismissed", "report", strconv.FormatInt(id, 10),
			map[string]any{"status": "reviewing"}, map[string]any{"status": "dismissed"}, nil)
	default:
		if err := applyReportOutcome(ctx, tx, actor, row, settled); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE report SET status='applied',final_score=$2,decision_reason='两名复核人结论一致',
			       decided_at=now(),updated_at=now() WHERE id=$1
		`, id, settled); err != nil {
			return err
		}
		if err := invalidateLatestSettlement(ctx, tx, "student report applied a penalty"); err != nil {
			return err
		}
		return appendAudit(c, tx, "report.applied", "report", strconv.FormatInt(id, 10),
			map[string]any{"status": "reviewing"}, map[string]any{"status": "applied", "score": settled}, nil)
	}
}

// 落分。三种对象写的地方不同，但都只写一次、只写这一处：
//
//   - penalty    —— 累加到已有扣分上。同一个扣分项一学期可能被扣好几次，覆盖会把
//     之前那几次抹掉。小组提案那条路是覆盖式的（它每次都从计分卡上
//     读到当前值再改），举报这条路上复核人看到的是"这一次该扣多少"。
//   - base       —— 覆盖成认定的得分。基础项是一个状态，不是一串事件。
//   - submission —— 改写这条提交的认定分，状态保持 scored。
func applyReportOutcome(ctx context.Context, tx pgx.Tx, actor Actor, row reportRecord, settled float64) error {
	if row.Kind == "submission" {
		if row.TargetID == nil {
			return errors.New("举报缺少目标条目")
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM submission WHERE id=$1 FOR UPDATE`, *row.TargetID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE submission SET final_score=$1,status='scored',scored_at=now(),updated_at=now(),
			       lock_version=lock_version+1 WHERE id=$2
		`, settled, *row.TargetID)
		return err
	}

	basis := "学生举报，经两名审核人复核认定"
	if row.Kind == "base" {
		// 基础项的满分要从方案里取回来：base_score.full_score 是展示用的，不能留空。
		current, err := loadCurrentScheme(ctx, tx)
		if err != nil {
			return err
		}
		_, _, full, _, err := schemeItemLabels(current.Config, row.Category, row.ItemKey, "base")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO base_score (class_id,student_id,scheme_id,category_key,item_key,kind,full_score,score,basis,recorded_by)
			VALUES ($1,$2,$3,$4,$5,'base',$6,$7,$8,$9)
			ON CONFLICT (class_id,student_id,scheme_id,category_key,item_key)
			DO UPDATE SET score=EXCLUDED.score,full_score=EXCLUDED.full_score,basis=EXCLUDED.basis,
			              recorded_by=EXCLUDED.recorded_by,updated_at=now()
		`, actor.ClassID, row.StudentID, row.SchemeID, row.Category, row.ItemKey, full, settled, basis, actor.UserID)
		return err
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO base_score (class_id,student_id,scheme_id,category_key,item_key,kind,full_score,score,basis,recorded_by)
		VALUES ($1,$2,$3,$4,$5,'penalty',NULL,$6,$7,$8)
		ON CONFLICT (class_id,student_id,scheme_id,category_key,item_key)
		DO UPDATE SET score=base_score.score+EXCLUDED.score,basis=EXCLUDED.basis,
		              recorded_by=EXCLUDED.recorded_by,updated_at=now()
	`, actor.ClassID, row.StudentID, row.SchemeID, row.Category, row.ItemKey, settled, basis, actor.UserID)
	return err
}

/* ---- 班级管理员：分歧终裁 ---- */

func (s *Server) adminReports(c *gin.Context) {
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT r.id,r.student_id,student.sid,student.name,r.kind,r.category_key,r.item_key,
		       r.quantity::float8,r.current_score::float8,r.proposed_score::float8,r.basis,r.status,
		       r.final_score::float8,r.decision_reason,r.created_at,r.decided_at
		  FROM report r JOIN app_user student ON student.id=r.student_id
		 WHERE (NOT $1::boolean OR student.role='class_admin')
		 ORDER BY CASE r.status WHEN 'escalated' THEN 0 ELSE 1 END,r.created_at DESC,r.id DESC
	`, isDeputyAdjudication(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	// 复核人和附件要等这一趟读完再查。一条连接上同时开两个查询，pgx 会直接
	// 报 conn busy——所以先把整页读进内存，关掉游标，再逐条补。
	ids := make([]int64, 0)
	for rows.Next() {
		var id, studentID int64
		var sid, name, kind, category, itemKey, basis, status string
		var quantity, currentScore, finalScore *float64
		var proposed float64
		var decisionReason *string
		var createdAt time.Time
		var decidedAt *time.Time
		if err := rows.Scan(&id, &studentID, &sid, &name, &kind, &category, &itemKey, &quantity,
			&currentScore, &proposed, &basis, &status, &finalScore, &decisionReason, &createdAt, &decidedAt); err != nil {
			writeServiceError(c, err)
			return
		}
		categoryName, itemName := reportItemLabels(current.Config, kind, category, itemKey)
		ids = append(ids, id)
		items = append(items, withAdjudicationPermission(gin.H{
			"id": strconv.FormatInt(id, 10), "studentUserId": strconv.FormatInt(studentID, 10),
			"studentId": sid, "student": name, "kind": kind, "category": category, "categoryName": categoryName,
			"itemKey": itemKey, "itemName": itemName, "quantity": quantity,
			"currentScore": currentScore, "proposedScore": proposed,
			"basis": basis, "status": status, "finalScore": finalScore, "decisionReason": decisionReason,
			"createdAt": createdAt, "decidedAt": decidedAt,
		}, mustActor(c), studentID))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	for at, id := range ids {
		reviews, err := reportReviewerJSON(c.Request.Context(), tx, id)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		files, err := reportNoteEvidence(c.Request.Context(), tx, id)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		items[at]["reviews"], items[at]["evidence"] = reviews, files
		notes, err := reportReviewNoteEvidence(c.Request.Context(), tx, id, mustActor(c))
		if err != nil {
			writeServiceError(c, err)
			return
		}
		items[at]["noteEvidence"] = notes
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// 复核人姓名对班管可见——和双评对照、申诉复评一样，班管要能看到是谁给出了这个判断。
// 举报人依然不可见，那是另一回事：复核人是被派去干活的，举报人是自愿站出来的。
func reportReviewerJSON(ctx context.Context, tx pgx.Tx, reportID int64) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT u.name,u.sid,rr.decision,rr.score::float8,rr.reason,rr.spent_seconds,rr.decided_at
		  FROM report_reviewer rr JOIN app_user u ON u.id=rr.reviewer_id
		 WHERE rr.report_id=$1 ORDER BY rr.reviewer_id
	`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var name, sid string
		var decision, reason *string
		var score *float64
		var spent *int
		var decidedAt *time.Time
		if err := rows.Scan(&name, &sid, &decision, &score, &reason, &spent, &decidedAt); err != nil {
			return nil, err
		}
		items = append(items, gin.H{
			"reviewer": name, "reviewerSid": sid, "decision": decision, "score": score,
			"reason": reason, "spentSeconds": spent, "at": decidedAt, "decided": decidedAt != nil,
		})
	}
	return items, rows.Err()
}

type reportFinalInput struct {
	Score  *float64 `json:"score"`
	Reason string   `json:"reason"`
}

func validateReportFinalScore(row reportRecord, score float64) error {
	points := scheme.NewPoints(score)
	if row.Kind == "penalty" {
		if points > 0 {
			return errors.New("裁定分不能为正；举报不成立请填 0")
		}
		return nil
	}
	if row.Kind != "base" && row.Kind != "submission" {
		return errors.New("举报类型不正确")
	}
	if row.CurrentScore == nil {
		return errors.New("举报缺少原认定分")
	}
	if points < 0 || points > scheme.NewPoints(*row.CurrentScore) {
		return errors.New("裁定分必须在 0 与原认定分之间；举报不成立请填原认定分")
	}
	return nil
}

func (s *Server) finalizeReport(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input reportFinalInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "终裁参数不正确", nil)
		return
	}
	reason := strings.TrimSpace(input.Reason)
	if len(reason) < 6 || len(reason) > 5000 {
		writeError(c, http.StatusUnprocessableEntity, "decision_invalid", "终裁理由须为 6—5000 字", nil)
		return
	}
	if input.Score == nil {
		writeError(c, http.StatusUnprocessableEntity, "score_required", "必须给出裁定分", nil)
		return
	}
	score := scheme.NewPoints(*input.Score).Float64()
	actor := mustActor(c)
	tx := mustTx(c)
	var row reportRecord
	err := tx.QueryRow(c.Request.Context(), `
		SELECT r.id,r.student_id,r.scheme_id,r.kind,r.target_id,r.current_score::float8,
		       r.category_key,r.item_key,r.quantity::float8,r.proposed_score::float8,r.status
		  FROM report r WHERE r.id=$1 FOR UPDATE
	`, id).Scan(&row.ID, &row.StudentID, &row.SchemeID, &row.Kind, &row.TargetID, &row.CurrentScore,
		&row.Category, &row.ItemKey, &row.Quantity, &row.ProposedScore, &row.Status)
	if notFound(c, err, "举报") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if row.Status != "escalated" {
		writeError(c, http.StatusConflict, "report_not_escalated", "只有升上来的举报需要你终裁", nil)
		return
	}
	if !requireAdjudicationTarget(c, tx, actor, row.StudentID) {
		return
	}
	if err := validateReportFinalScore(row, score); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "score_invalid", err.Error(), nil)
		return
	}
	if scheme.NewPoints(score) != scheme.NewPoints(reportStatusQuo(row)) {
		if err := applyReportOutcome(c.Request.Context(), tx, actor, row, score); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := invalidateLatestSettlement(c.Request.Context(), tx, "class admin finalized a student report"); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE report SET status='final',final_score=$2,decided_by=$3,decision_reason=$4,
		       decided_at=now(),updated_at=now() WHERE id=$1
	`, id, score, actor.UserID, reason); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "report.finalized", "report", strconv.FormatInt(id, 10),
		map[string]any{"status": "escalated"},
		map[string]any{"status": "final", "score": score}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ReportDecidedEvent(), events.ReportDecidedPayload{
		ReportID: id, Status: "final", StudentID: row.StudentID,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": "final", "score": score})
}
