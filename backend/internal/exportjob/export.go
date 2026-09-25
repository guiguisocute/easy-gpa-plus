// Package exportjob renders the fixed, non-AI export products from an
// immutable settlement snapshot and stores them in the S3-compatible store.
package exportjob

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"easygpa/backend/internal/classtimeline"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/objectstore"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/richtext"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/settle"
	"easygpa/backend/internal/store"
)

type Worker struct {
	pool    *pgxpool.Pool
	objects *objectstore.Client
	limiter Limiter
	config  *opsconfig.Store
}

func NewWorker(pool *pgxpool.Pool, objects *objectstore.Client) (*Worker, error) {
	return NewWorkerWithLimiter(pool, objects, nil)
}

func NewWorkerWithLimiter(pool *pgxpool.Pool, objects *objectstore.Client, limiter Limiter) (*Worker, error) {
	return NewWorkerWithRuntime(pool, objects, limiter, nil)
}

func NewWorkerWithRuntime(pool *pgxpool.Pool, objects *objectstore.Client, limiter Limiter, config *opsconfig.Store) (*Worker, error) {
	if pool == nil || objects == nil {
		return nil, errors.New("export worker dependencies are required")
	}
	return &Worker{pool: pool, objects: objects, limiter: limiter, config: config}, nil
}

type snapshotRow struct {
	UserID         int64
	SID            string
	Name           string
	CategoryScores map[string]float64
	TotalScore     float64
	ClassRank      int
	MajorRank      int
	Honor          bool
	// AwardTier is empty both when the student missed every tier and when the
	// run predates configurable tiers. The export shows a blank cell either
	// way, which is the honest rendering of "this run did not award one".
	AwardTier string
	// Gender is empty when the roster was imported without a 性别 column. The
	// 学院 report then leaves the cell blank, which is the honest rendering of
	// "we were not told" — nothing here guesses from a name.
	Gender  string
	Details map[string]any
}

type evidenceFile struct {
	ID         int64
	SID        string
	ObjectKey  string
	Filename   string
	SizeBytes  int64
	ObjectETag string
}

type jobData struct {
	ID                string
	ClassID           int64
	ClassName         string
	RunID             int64
	Kind              string
	RequestedBy       int64
	AgentConnectionID *int64
	Config            scheme.Config
	TriggerKind       string
	GateSnapshot      map[string]any
	Rows              []snapshotRow
	Evidence          []evidenceFile
	// 下面四项只喂学院报表。分数一律来自冻结的快照，这几项是标签：
	// 学院名和教务班级名读班级当前设置，不读快照——管理员事后把学院全称的
	// 错别字改对，不该逼着全班重新结算一次才能出对的报表。
	CollegeName     string
	EnrollmentClass string
	// AwardOrder 是档位在方案里的排列次序，用来映射壹/贰/叁。档位名是班级可配
	// 的，按中文名反查会在改名的那天悄悄错掉，所以只认下标。
	AwardOrder   []string
	AcademicYear string
}

func (w *Worker) Handle(ctx context.Context, event events.Event) error {
	if event.Type != events.ExportRequested {
		return nil
	}
	payload, err := events.Decode(event, events.ExportRequestedEvent())
	if err != nil || payload.JobID == "" {
		return errors.New("export event has no jobId")
	}
	if w.limiter != nil {
		lease, err := w.limiter.Acquire(ctx)
		if err != nil {
			return err
		}
		defer lease.Release(context.Background())
	}
	data, complete, err := w.loadJob(ctx, event.ClassID, payload.JobID)
	if errors.Is(err, errAgentConnectionRevoked) {
		_ = w.markFailed(ctx, event.ClassID, payload.JobID, err)
		return nil
	}
	if err != nil || complete {
		return err
	}
	key := "class-" + strconv.FormatInt(data.ClassID, 10) + "/exports/" + data.ID
	if data.Kind == "college" {
		key += CollegeArchiveSuffix
	} else if data.Kind == "archive" {
		key += "-archive.zip"
	} else {
		key += "-" + data.Kind + ".xlsx"
	}
	retentionDays := opsconfig.DefaultLifecycle().ExportRetentionDays
	if w.config != nil {
		policy, policyErr := w.config.Lifecycle(ctx)
		if policyErr != nil {
			return policyErr
		}
		retentionDays = policy.ExportRetentionDays
	}
	if err := w.renderAndUpload(ctx, data, key); err != nil {
		_ = w.markFailed(context.Background(), data.ClassID, data.ID, err)
		return err
	}
	err = store.InTenantTx(ctx, w.pool, data.ClassID, func(tx pgx.Tx) error {
		if err := validateJobAgentConnection(ctx, tx, data.AgentConnectionID, data.RequestedBy); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `
			UPDATE export_job SET status='complete',object_key=$1,error_message=NULL,finished_at=now(),expires_at=now()+$3*interval '1 day'
			 WHERE id=$2::uuid AND status<>'complete'
		`, key, data.ID, retentionDays)
		if err != nil || command.RowsAffected() == 0 {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"jobId": data.ID, "runId": data.RunID, "kind": data.Kind})
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_event (class_id,type,payload) VALUES ($1,$2,$3)`, data.ClassID, events.ExportDone, payload); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,after_data)
			VALUES ($1,NULL,'system','export.completed','export_job',$2,
			        jsonb_build_object('kind',$3::text,'runId',$4::bigint))
		`, data.ClassID, data.ID, data.Kind, data.RunID)
		return err
	})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = w.objects.Remove(cleanupCtx, key)
		_ = w.markFailed(cleanupCtx, data.ClassID, data.ID, err)
		cancel()
	}
	return err
}

func (w *Worker) loadJob(ctx context.Context, classID int64, jobID string) (jobData, bool, error) {
	var data jobData
	data.ID = jobID
	data.ClassID = classID
	complete := false
	err := store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		var status string
		var configRaw, gateRaw []byte
		err := tx.QueryRow(ctx, `
			SELECT j.run_id,j.kind,j.status,j.requested_by,c.name,r.config_snapshot,r.trigger_kind,r.gate_snapshot,j.agent_connection_id
			  FROM export_job j JOIN settlement_run r ON r.id=j.run_id JOIN class c ON c.id=j.class_id
			 WHERE j.id=$1::uuid FOR UPDATE OF j
		`, jobID).Scan(&data.RunID, &data.Kind, &status, &data.RequestedBy, &data.ClassName, &configRaw, &data.TriggerKind, &gateRaw, &data.AgentConnectionID)
		if err != nil {
			return err
		}
		if status == "complete" {
			complete = true
			return nil
		}
		if err := validateJobAgentConnection(ctx, tx, data.AgentConnectionID, data.RequestedBy); err != nil {
			return err
		}
		if err := json.Unmarshal(configRaw, &data.Config); err != nil {
			return err
		}
		if err := json.Unmarshal(gateRaw, &data.GateSnapshot); err != nil {
			return err
		}
		// scheme.Config 把 window 和 honorRoll 标成 json:"-"（它们属于
		// class_timeline，不属于方案版本），所以这两项要单独从快照里取——
		// api.settlementConfigSnapshot 写进去的就是这两个键。
		var envelope struct {
			Window struct {
				Close time.Time `json:"close"`
			} `json:"window"`
			HonorRoll struct {
				Awards []struct {
					Name string `json:"name"`
				} `json:"awards"`
			} `json:"honorRoll"`
		}
		if err := json.Unmarshal(configRaw, &envelope); err != nil {
			return err
		}
		for _, award := range envelope.HonorRoll.Awards {
			data.AwardOrder = append(data.AwardOrder, award.Name)
		}
		timeline, err := classtimeline.Load(ctx, tx, classID)
		if err != nil {
			return err
		}
		data.CollegeName = timeline.CollegeName
		data.EnrollmentClass = timeline.EnrollmentClass
		data.AcademicYear = timeline.AcademicYear
		if _, err := tx.Exec(ctx, `UPDATE export_job SET status='running',started_at=COALESCE(started_at,now()),error_message=NULL WHERE id=$1::uuid`, jobID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT s.student_id,u.sid,u.name,s.category_scores,s.total_score::float8,s.class_rank,s.major_rank,s.honor,
			       COALESCE(s.award_tier,''),COALESCE(w.gender,''),s.details
			  FROM settlement s
			  JOIN app_user u ON u.id=s.student_id
			  JOIN whitelist w ON w.id=u.whitelist_id
			 WHERE s.run_id=$1 ORDER BY s.class_rank,u.sid
		`, data.RunID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var row snapshotRow
			var categoriesRaw, detailsRaw []byte
			if err := rows.Scan(&row.UserID, &row.SID, &row.Name, &categoriesRaw, &row.TotalScore, &row.ClassRank, &row.MajorRank, &row.Honor, &row.AwardTier, &row.Gender, &detailsRaw); err != nil {
				rows.Close()
				return err
			}
			if err := json.Unmarshal(categoriesRaw, &row.CategoryScores); err != nil {
				rows.Close()
				return err
			}
			if err := json.Unmarshal(detailsRaw, &row.Details); err != nil {
				rows.Close()
				return err
			}
			data.Rows = append(data.Rows, row)
		}
		rows.Close()
		if data.Kind == "archive" {
			rows, err := tx.Query(ctx, `
				SELECT e.id,u.sid,e.object_key,e.filename,e.size_bytes,COALESCE(e.object_etag,'')
				  FROM evidence e
				  LEFT JOIN submission s ON s.id=e.submission_id
				  LEFT JOIN appeal a ON a.id=e.appeal_id
				  JOIN app_user u ON u.id=COALESCE(s.student_id,a.student_id)
				 WHERE e.status='ready' AND e.kind='claim' ORDER BY u.sid,e.id
			`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var file evidenceFile
				if err := rows.Scan(&file.ID, &file.SID, &file.ObjectKey, &file.Filename, &file.SizeBytes, &file.ObjectETag); err != nil {
					rows.Close()
					return err
				}
				data.Evidence = append(data.Evidence, file)
			}
			rows.Close()
		}
		return nil
	})
	return data, complete, err
}

func (w *Worker) markFailed(ctx context.Context, classID int64, jobID string, cause error) error {
	message := cause.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	return store.InTenantTx(ctx, w.pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE export_job SET status='failed',error_message=$1,finished_at=now() WHERE id=$2::uuid AND status<>'complete'`, message, jobID)
		return err
	})
}

// ValidKind is the single list of export products. It used to be spelled out
// as the same three string comparisons in three packages, which is a shape that
// only ever grows a fourth place to forget.
func ValidKind(kind string) bool {
	switch kind {
	case "summary", "detail", "archive", "college":
		return true
	default:
		return false
	}
}

func (w *Worker) renderAndUpload(ctx context.Context, data jobData, key string) error {
	if data.Kind == "college" {
		archive, err := collegeArchive(data)
		if err != nil {
			return err
		}
		return w.objects.Put(ctx, key, "application/zip", bytes.NewReader(archive), int64(len(archive)))
	}
	summary, err := summaryWorkbook(data)
	if err != nil {
		return err
	}
	if data.Kind == "summary" {
		return w.objects.Put(ctx, key, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", bytes.NewReader(summary), int64(len(summary)))
	}
	detail, err := detailWorkbook(data)
	if err != nil {
		return err
	}
	if data.Kind == "detail" {
		return w.objects.Put(ctx, key, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", bytes.NewReader(detail), int64(len(detail)))
	}
	return w.uploadArchive(ctx, data, key, summary, detail)
}

func categoryOrder(config scheme.Config) []string {
	preferred := []string{"major", "moral", "practice", "health"}
	result := make([]string, 0, len(config.Weights))
	seen := make(map[string]bool)
	for _, key := range preferred {
		if _, ok := config.Weights[key]; ok {
			result = append(result, key)
			seen[key] = true
		}
	}
	var rest []string
	for key := range config.Weights {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(result, rest...)
}

func categoryName(config scheme.Config, key string) string {
	if key == "major" {
		return "专业素质"
	}
	for _, category := range config.Categories {
		if category.Key == key {
			return category.Name
		}
	}
	return key
}

func summaryWorkbook(data jobData) ([]byte, error) {
	book := excelize.NewFile()
	defer book.Close()
	sheet := "汇总"
	if err := book.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	keys := categoryOrder(data.Config)
	ranks := categoryRanks(data.Rows)
	headers := []any{"学号", "姓名"}
	for _, key := range keys {
		name := categoryName(data.Config, key)
		headers = append(headers, name, name+"排名")
	}
	headers = append(headers, "总分", "班级排名", "三好标记", "奖学金档位")
	if err := setRow(book, sheet, 1, headers); err != nil {
		return nil, err
	}
	for index, row := range rowsBySID(data.Rows) {
		values := []any{row.SID, row.Name}
		for _, key := range keys {
			values = append(values, row.CategoryScores[key], ranks[row.UserID][key])
		}
		values = append(values, row.TotalScore, row.ClassRank, boolText(row.Honor), row.AwardTier)
		if err := setRow(book, sheet, index+2, values); err != nil {
			return nil, err
		}
	}
	_ = book.SetColWidth(sheet, "A", "A", 16)
	_ = book.SetColWidth(sheet, "B", "B", 12)
	last, _ := excelize.ColumnNumberToName(len(headers))
	_ = book.SetColWidth(sheet, "C", last, 14)
	style, _ := book.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Color: []string{"E8EEF7"}, Pattern: 1}})
	_ = book.SetCellStyle(sheet, "A1", last+"1", style)
	if err := addForcedWarning(book, data); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func detailWorkbook(data jobData) ([]byte, error) {
	book := excelize.NewFile()
	defer book.Close()
	used := make(map[string]bool)
	for index, student := range rowsBySID(data.Rows) {
		sheet := uniqueSheetName(student.SID, used)
		if index == 0 {
			if err := book.SetSheetName("Sheet1", sheet); err != nil {
				return nil, err
			}
		} else if _, err := book.NewSheet(sheet); err != nil {
			return nil, err
		}
		row := 1
		for _, values := range [][]any{
			{"学号", student.SID, "姓名", student.Name},
			{"总分", student.TotalScore, "班级排名", student.ClassRank, "专业排名", student.MajorRank, "三好", boolText(student.Honor), "奖学金档位", student.AwardTier},
			{},
			{"类别", "类别得分"},
		} {
			if err := setRow(book, sheet, row, values); err != nil {
				return nil, err
			}
			row++
		}
		for _, key := range categoryOrder(data.Config) {
			if err := setRow(book, sheet, row, []any{categoryName(data.Config, key), student.CategoryScores[key]}); err != nil {
				return nil, err
			}
			row++
		}
		row++
		if err := setRow(book, sheet, row, []any{"提交条目", "原始大项", "原始小项", "有效大项", "有效小项", "标题", "认定分", "状态", "分类轨迹", "审核记录", "申诉记录", "规则快照"}); err != nil {
			return nil, err
		}
		row++
		for _, raw := range anySlice(student.Details["items"]) {
			item, _ := raw.(map[string]any)
			values := []any{
				item["id"], item["filedCategory"], item["filedItemKey"], item["category"], item["itemKey"], item["title"], item["score"], item["status"],
				classificationHistoryText(item["classificationHistory"]), reviewsText(item["reviews"]), appealsText(item["appeals"]), ruleSnapshotText(item["ruleSnapshot"]),
			}
			if err := setRow(book, sheet, row, values); err != nil {
				return nil, err
			}
			row++
		}
		row++
		if err := setRow(book, sheet, row, []any{"基础/扣分项", "大项", "小项", "名称", "类型", "满分", "实得", "依据", "录入人", "申诉记录"}); err != nil {
			return nil, err
		}
		row++
		for _, raw := range anySlice(student.Details["baseItems"]) {
			item, _ := raw.(map[string]any)
			values := []any{
				item["id"], item["category"], item["itemKey"], item["name"], item["kind"], item["fullScore"],
				item["score"], richtext.PlainText(valueString(item["basis"])), item["recorder"], appealsText(item["appeals"]),
			}
			if err := setRow(book, sheet, row, values); err != nil {
				return nil, err
			}
			row++
		}
		_ = book.SetColWidth(sheet, "A", "E", 16)
		_ = book.SetColWidth(sheet, "F", "F", 30)
		_ = book.SetColWidth(sheet, "I", "L", 42)
		wrap, _ := book.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
		_ = book.SetCellStyle(sheet, "I1", "L"+strconv.Itoa(row), wrap)
	}
	if err := addForcedWarning(book, data); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func addForcedWarning(book *excelize.File, data jobData) error {
	if data.TriggerKind != "forced" {
		return nil
	}
	const sheet = "强制结算警告"
	index, err := book.NewSheet(sheet)
	if err != nil {
		return err
	}
	rows := [][]any{
		{"警告：本次成绩由紧急强制开闸生成"},
		{"班级", data.ClassName},
		{"强制理由", data.GateSnapshot["forceReason"]},
		{"未完成单项审核", data.GateSnapshot["unfinalizedReviews"]},
		{"待处理分类建议", data.GateSnapshot["pendingClassifications"]},
		{"待终裁/申诉/异议", data.GateSnapshot["pendingConflicts"]},
		{"盲审批次状态", data.GateSnapshot["blindAuditStatus"]},
		{},
		{"结算条件快照"},
	}
	for rowIndex, values := range rows {
		if err := setRow(book, sheet, rowIndex+1, values); err != nil {
			return err
		}
	}
	start := len(rows) + 1
	for index, raw := range anySlice(data.GateSnapshot["conditions"]) {
		condition := objectMap(raw)
		if err := setRow(book, sheet, start+index, []any{condition["label"], condition["ok"], condition["detail"]}); err != nil {
			return err
		}
	}
	_ = book.MergeCell(sheet, "A1", "F1")
	_ = book.SetColWidth(sheet, "A", "A", 28)
	_ = book.SetColWidth(sheet, "B", "F", 24)
	style, _ := book.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "9C0006", Size: 14}, Fill: excelize.Fill{Type: "pattern", Color: []string{"FFC7CE"}, Pattern: 1}, Alignment: &excelize.Alignment{Horizontal: "center"}})
	_ = book.SetCellStyle(sheet, "A1", "F1", style)
	book.SetActiveSheet(index)
	return nil
}

func setRow(book *excelize.File, sheet string, row int, values []any) error {
	for index, value := range values {
		cell, _ := excelize.CoordinatesToCellName(index+1, row)
		if err := book.SetCellValue(sheet, cell, value); err != nil {
			return err
		}
	}
	return nil
}

func anySlice(value any) []any {
	if value == nil {
		return nil
	}
	if rows, ok := value.([]any); ok {
		return rows
	}
	return nil
}

func objectMap(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return nil
}

func valueString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func valueNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numberText(value any) string {
	number, ok := valueNumber(value)
	if !ok {
		return "—"
	}
	return strconv.FormatFloat(number, 'f', -1, 64)
}

func reviewsText(value any) string {
	rows := anySlice(value)
	if len(rows) == 0 {
		return "无审核记录"
	}
	parts := make([]string, 0, len(rows))
	for index, raw := range rows {
		row := objectMap(raw)
		if row == nil {
			continue
		}
		reviewer := valueString(row["reviewer"])
		if sid := valueString(row["reviewerSid"]); sid != "" {
			reviewer += "（" + sid + "）"
		}
		decision := map[string]string{"accepted": "通过", "adjusted": "调分", "rejected": "驳回"}[valueString(row["decision"])]
		if decision == "" {
			decision = valueString(row["decision"])
		}
		line := fmt.Sprintf("%d. %s｜%s｜%s 分", index+1, reviewer, decision, numberText(row["score"]))
		if reason := richtext.PlainText(valueString(row["reason"])); reason != "" {
			line += "｜" + reason
		}
		if at := valueString(row["at"]); at != "" {
			line += "｜" + at
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n")
}

func appealsText(value any) string {
	rows := anySlice(value)
	if len(rows) == 0 {
		return "无申诉记录"
	}
	parts := make([]string, 0, len(rows))
	for index, raw := range rows {
		row := objectMap(raw)
		if row == nil {
			continue
		}
		round := numberText(row["round"])
		if round == "—" {
			round = strconv.Itoa(index + 1)
		}
		line := fmt.Sprintf("第 %s 轮｜%s", round, valueString(row["status"]))
		if original, ok := valueNumber(row["originalScore"]); ok {
			line += "｜原认定 " + strconv.FormatFloat(original, 'f', -1, 64)
		}
		if proposed, ok := valueNumber(row["proposedScore"]); ok {
			line += " → 申请 " + strconv.FormatFloat(proposed, 'f', -1, 64)
		}
		if resolved, ok := valueNumber(row["resolutionScore"]); ok {
			line += " → 裁定 " + strconv.FormatFloat(resolved, 'f', -1, 64)
		}
		if reason := richtext.PlainText(valueString(row["reason"])); reason != "" {
			line += "｜申请理由：" + reason
		}
		if reason := richtext.PlainText(valueString(row["resolutionReason"])); reason != "" {
			line += "｜处理理由：" + reason
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n")
}

func classificationHistoryText(value any) string {
	rows := anySlice(value)
	if len(rows) == 0 {
		return "未调整"
	}
	parts := make([]string, 0, len(rows))
	for index, raw := range rows {
		row := objectMap(raw)
		if row == nil {
			continue
		}
		line := fmt.Sprintf("%d. %s/%s → %s/%s", index+1,
			valueString(row["beforeCategory"]), valueString(row["beforeItemKey"]),
			valueString(row["afterCategory"]), valueString(row["afterItemKey"]))
		if before, ok := valueNumber(row["beforeScore"]); ok {
			line += "｜" + strconv.FormatFloat(before, 'f', -1, 64)
		}
		if after, ok := valueNumber(row["afterScore"]); ok {
			line += " → " + strconv.FormatFloat(after, 'f', -1, 64)
		}
		if reason := richtext.PlainText(valueString(row["reason"])); reason != "" {
			line += "｜" + reason
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n")
}

func ruleSnapshotText(value any) string {
	snapshot := objectMap(value)
	if snapshot == nil {
		return "规则快照不可读"
	}
	item := objectMap(snapshot["item"])
	rule := objectMap(item["scoreRule"])
	parts := []string{
		"版本 " + valueString(snapshot["version"]),
		valueString(snapshot["categoryName"]) + " / " + valueString(item["name"]),
	}
	switch valueString(rule["type"]) {
	case "per_unit":
		text := fmt.Sprintf("按量：每 %s %s 分", valueString(rule["unit"]), numberText(rule["per"]))
		if _, ok := valueNumber(rule["cap"]); ok {
			text += "，上限 " + numberText(rule["cap"]) + " 分"
		}
		parts = append(parts, text)
	case "enum":
		options := make([]string, 0)
		for _, raw := range anySlice(rule["options"]) {
			option := objectMap(raw)
			if option != nil {
				// 档位分是选中时带入的建议值，学生可改、审核人可再定。写成 "=N 分"
				// 会让导出件读起来像这一档就是定死的分，和界面上的口径也对不上。
				options = append(options, valueString(option["label"])+" · 建议 "+numberText(option["score"])+" 分")
			}
		}
		parts = append(parts, "按档："+strings.Join(options, "；"))
	case "free":
		parts = append(parts, fmt.Sprintf("自报：%s—%s 分", numberText(rule["min"]), numberText(rule["max"])))
	case "threshold":
		parts = append(parts, fmt.Sprintf("条件：至少 %s %s，达标 %s 分", numberText(rule["minimum"]), valueString(rule["unit"]), numberText(rule["award"])))
	default:
		parts = append(parts, "计分规则："+valueString(rule["type"]))
	}
	if note := valueString(item["note"]); note != "" {
		parts = append(parts, "备注："+note)
	}
	return strings.Join(parts, "｜")
}

func boolText(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func uniqueSheetName(value string, used map[string]bool) string {
	value = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\\`, r) || r < 32 {
			return '_'
		}
		return r
	}, strings.TrimSpace(value))
	if value == "" {
		value = "学生"
	}
	if len([]rune(value)) > 25 {
		value = string([]rune(value)[:25])
	}
	base := value
	for suffix := 1; used[value]; suffix++ {
		value = base + "-" + strconv.Itoa(suffix)
	}
	used[value] = true
	return value
}

func archiveFilename(value string) string {
	value = filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, value)
	if value == "" || value == "." {
		return "evidence"
	}
	return value
}

func rowsBySID(rows []snapshotRow) []snapshotRow {
	result := append([]snapshotRow(nil), rows...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].SID != result[j].SID {
			return result[i].SID < result[j].SID
		}
		return result[i].UserID < result[j].UserID
	})
	return result
}

// categoryRanks 从结算行重算各维度名次。名次是 category_scores 的确定性函数，
// 而 category_scores 已经冻在快照里，所以读时重算和写时冻结是同一个数——但只有
// 一份实现：三好判据要读它，它就不能有第二个来源。
func categoryRanks(rows []snapshotRow) map[int64]map[string]int {
	ranked := make([]settle.Ranked, len(rows))
	for index, row := range rows {
		ranked[index] = settle.Ranked{UserID: row.UserID, Scores: row.CategoryScores}
	}
	return settle.CategoryRanks(ranked)
}

func evidenceDedupKey(file evidenceFile) string {
	if etag := strings.ToLower(strings.Trim(strings.TrimSpace(file.ObjectETag), `"`)); etag != "" {
		return file.SID + "\x00etag\x00" + etag + "\x00" + strconv.FormatInt(file.SizeBytes, 10)
	}
	return file.SID + "\x00metadata\x00" + strings.ToLower(strings.TrimSpace(file.Filename)) + "\x00" + strconv.FormatInt(file.SizeBytes, 10)
}

func deduplicateEvidence(files []evidenceFile) []evidenceFile {
	seen := make(map[string]bool, len(files))
	result := make([]evidenceFile, 0, len(files))
	for _, file := range files {
		key := evidenceDedupKey(file)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, file)
	}
	return result
}

func (w *Worker) uploadArchive(ctx context.Context, data jobData, key string, summary, detail []byte) error {
	temporary, err := os.CreateTemp("", "easygpa-export-*.zip")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	zipper := zip.NewWriter(temporary)
	writeBytes := func(name string, content []byte) error {
		entry, err := zipper.Create(name)
		if err != nil {
			return err
		}
		_, err = entry.Write(content)
		return err
	}
	if err := writeBytes("汇总.xlsx", summary); err != nil {
		_ = zipper.Close()
		_ = temporary.Close()
		return err
	}
	if err := writeBytes("逐人明细.xlsx", detail); err != nil {
		_ = zipper.Close()
		_ = temporary.Close()
		return err
	}
	for _, file := range deduplicateEvidence(data.Evidence) {
		entryName := "佐证/" + archiveFilename(file.SID) + "/" + strconv.FormatInt(file.ID, 10) + "-" + archiveFilename(file.Filename)
		entry, err := zipper.Create(entryName)
		if err != nil {
			_ = zipper.Close()
			_ = temporary.Close()
			return err
		}
		object, err := w.objects.Open(ctx, file.ObjectKey)
		if err != nil {
			_ = zipper.Close()
			_ = temporary.Close()
			return err
		}
		_, copyErr := io.Copy(entry, object)
		closeErr := object.Close()
		if copyErr != nil {
			_ = zipper.Close()
			_ = temporary.Close()
			return copyErr
		}
		if closeErr != nil {
			_ = zipper.Close()
			_ = temporary.Close()
			return closeErr
		}
	}
	if err := zipper.Close(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return w.objects.Put(ctx, key, "application/zip", file, info.Size())
}
