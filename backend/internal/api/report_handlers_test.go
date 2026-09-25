package api

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"easygpa/backend/internal/scheme"
)

func TestValidateReportDecisionRejectsHarsherAdjust(t *testing.T) {
	// 举报主张扣 2 分。复核人可以往轻里改，不能往重里改。
	row := reportRecord{Kind: "penalty", ProposedScore: -2}
	lighter := -1.0
	if _, score, _, err := validateReportDecision(row, reportDecisionInput{Decision: "adjust", Score: &lighter, Reason: "只核实到一次"}); err != nil || score != -1 {
		t.Fatalf("adjust to a lighter penalty should pass, got score=%v err=%v", score, err)
	}
	harsher := -3.0
	_, _, _, err := validateReportDecision(row, reportDecisionInput{Decision: "adjust", Score: &harsher, Reason: "其实缺了三次"})
	if err == nil || !strings.Contains(err.Error(), "不能判得比举报主张的还重") {
		t.Fatalf("adjust beyond the reported penalty must be refused, got %v", err)
	}
}

func TestReportRecordAllowsQuantityToBeAbsentForBaseReport(t *testing.T) {
	var row reportRecord
	if err := pgtype.NewMap().Scan(pgtype.Float8OID, pgtype.BinaryFormatCode, nil, &row.Quantity); err != nil {
		t.Fatalf("scan nullable report quantity: %v", err)
	}
	if row.Quantity != nil {
		t.Fatalf("base report quantity = %v, want nil", row.Quantity)
	}
}

func TestValidateReportFinalScoreSupportsAllReportKinds(t *testing.T) {
	current := 15.0
	for _, test := range []struct {
		name  string
		row   reportRecord
		score float64
		ok    bool
	}{
		{name: "penalty deduction", row: reportRecord{Kind: "penalty"}, score: -3, ok: true},
		{name: "penalty rejected", row: reportRecord{Kind: "penalty"}, score: 0, ok: true},
		{name: "penalty cannot award", row: reportRecord{Kind: "penalty"}, score: 1},
		{name: "base adjusted", row: reportRecord{Kind: "base", CurrentScore: &current}, score: 8, ok: true},
		{name: "base rejected", row: reportRecord{Kind: "base", CurrentScore: &current}, score: 15, ok: true},
		{name: "submission adjusted", row: reportRecord{Kind: "submission", CurrentScore: &current}, score: 9, ok: true},
		{name: "cannot improve score", row: reportRecord{Kind: "submission", CurrentScore: &current}, score: 16},
		{name: "cannot make score negative", row: reportRecord{Kind: "base", CurrentScore: &current}, score: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateReportFinalScore(test.row, test.score)
			if (err == nil) != test.ok {
				t.Fatalf("validateReportFinalScore() error = %v, want ok %v", err, test.ok)
			}
		})
	}
}

func TestValidateReportDecisionCannotImproveTheScore(t *testing.T) {
	// 扣分项的"现状"是再扣 0：改成正数等于给对方送分。
	penalty := reportRecord{Kind: "penalty", ProposedScore: -2}
	positive := 1.0
	if _, _, _, err := validateReportDecision(penalty, reportDecisionInput{Decision: "adjust", Score: &positive, Reason: "这是六个字的理由"}); err == nil {
		t.Fatal("举报复核不能把扣分改成加分")
	}
	// 基础项本来 15 分，举报主张改成 8。复核人不能借机改成 20。
	current := 15.0
	base := reportRecord{Kind: "base", ProposedScore: 8, CurrentScore: &current}
	higher := 20.0
	if _, _, _, err := validateReportDecision(base, reportDecisionInput{Decision: "adjust", Score: &higher, Reason: "这是六个字的理由"}); err == nil {
		t.Fatal("举报复核不能把基础分改得比现在还高")
	}
	middle := 12.0
	if _, score, _, err := validateReportDecision(base, reportDecisionInput{Decision: "adjust", Score: &middle, Reason: "只核实到一部分"}); err != nil || score != 12 {
		t.Fatalf("落在举报主张与现状之间应当放行, got %v %v", score, err)
	}
}

func TestValidateReportDecisionUpholdAndReject(t *testing.T) {
	current := 15.0
	row := reportRecord{Kind: "base", ProposedScore: 8, CurrentScore: &current}
	decision, score, _, err := validateReportDecision(row, reportDecisionInput{Decision: "uphold", Reason: "材料完全对得上"})
	if err != nil || decision != "uphold" || score != 8 {
		t.Fatalf("uphold should take the reported score, got %v %v %v", decision, score, err)
	}
	// 驳回基础项举报＝维持 15，而不是把分打成 0。
	decision, score, _, err = validateReportDecision(row, reportDecisionInput{Decision: "reject", Reason: "查无此事，材料对不上"})
	if err != nil || decision != "reject" || score != 15 {
		t.Fatalf("reject on a base item must keep the current score, got %v %v %v", decision, score, err)
	}
	penalty := reportRecord{Kind: "penalty", ProposedScore: -2.5}
	if _, score, _, err := validateReportDecision(penalty, reportDecisionInput{Decision: "reject", Reason: "查无此事，材料对不上"}); err != nil || score != 0 {
		t.Fatalf("reject on a penalty means no extra deduction, got %v %v", score, err)
	}
	if _, _, _, err := validateReportDecision(penalty, reportDecisionInput{Decision: "uphold", Reason: "太短"}); err == nil {
		t.Fatal("a reason under six characters must be refused")
	}
}

func TestReportOutcomeAgreesOnScoreNotOnWording(t *testing.T) {
	// 一个人 uphold、另一个人 adjust 到同一个分，对学生是同一个结果，不该升给班管。
	status, score, conflict := reportOutcome([]float64{-2, -2}, 0)
	if status != "applied" || score != -2 || conflict {
		t.Fatalf("matching scores should apply, got %v %v %v", status, score, conflict)
	}
	status, _, conflict = reportOutcome([]float64{0, 0}, 0)
	if status != "dismissed" || conflict {
		t.Fatalf("two rejections should dismiss, got %v %v", status, conflict)
	}
	// 基础项：两人都判"维持 15"就是驳回；都判 8 才是改分生效。
	status, _, _ = reportOutcome([]float64{15, 15}, 15)
	if status != "dismissed" {
		t.Fatalf("agreeing on the status quo is a dismissal, got %v", status)
	}
	status, score, _ = reportOutcome([]float64{8, 8}, 15)
	if status != "applied" || score != 8 {
		t.Fatalf("agreeing below the status quo applies, got %v %v", status, score)
	}
	// 把基础分判成 0 是一个真实结果，不能被当成驳回。
	status, score, _ = reportOutcome([]float64{0, 0}, 15)
	if status != "applied" || score != 0 {
		t.Fatalf("zero is a real outcome for a base item, got %v %v", status, score)
	}
	status, _, conflict = reportOutcome([]float64{-2, -1}, 0)
	if status != "escalated" || !conflict {
		t.Fatalf("differing scores must escalate, got %v %v", status, conflict)
	}
	if status, _, _ := reportOutcome([]float64{-2}, 0); status != "reviewing" {
		t.Fatalf("one decision is not an outcome, got %v", status)
	}
}

func TestRejectIfNotWorseBlocksScoreGifts(t *testing.T) {
	current := 10.0
	if err := rejectIfNotWorse(12, &current); err == nil {
		t.Fatal("举报主张把分提高，必须挡住")
	}
	if err := rejectIfNotWorse(10, &current); err == nil {
		t.Fatal("举报主张维持原分，没有意义，也该挡住")
	}
	if err := rejectIfNotWorse(6, &current); err != nil {
		t.Fatalf("往低里报是允许的: %v", err)
	}
	if err := rejectIfNotWorse(-3, nil); err != nil {
		t.Fatalf("没有当前分时不判方向: %v", err)
	}
}

func TestStudentReportCapabilityIsOffByDefault(t *testing.T) {
	config := scheme.DefaultSelfReportConfig("test")
	if config.Capabilities.StudentReport {
		t.Fatal("匿名举报必须默认关闭，由班级管理员显式打开")
	}
	// 关闭时 ensureCapability 必须挡住，即使窗口开着。
	config.Window.Open = config.Window.Open.Add(0)
	if err := ensureCapability(config, "studentReport", config.Window.Open.Add(1)); err == nil {
		t.Fatal("studentReport 关闭时 ensureCapability 必须报错")
	}
	config.Capabilities.StudentReport = true
	if err := ensureCapability(config, "studentReport", config.Window.Open.Add(1)); err != nil {
		t.Fatalf("打开之后应当放行: %v", err)
	}
}

func TestReportFailureCarriesHTTPStatus(t *testing.T) {
	err := reportFailure(409, "report_exists", "已经举报过")
	var problem objectionProblem
	if !errors.As(err, &problem) || problem.status != 409 || problem.code != "report_exists" {
		t.Fatalf("reportFailure should surface as an objectionProblem, got %v", err)
	}
}

// 匿名是这个功能唯一不能出错的地方，而最容易出错的方式是有人在改动里顺手把
// reporter 拼进某个 JSON。这条测试直接读源码：举报相关的 handler 里不允许出现
// 举报人字段名，也不允许 SELECT report_reporter —— 业务连接对那张表本来就只有
// INSERT 权限，写了也只会在生产上炸。
func TestReportHandlersNeverSerializeTheReporter(t *testing.T) {
	for _, name := range []string{"report_handlers.go", "report_review_handlers.go"} {
		source, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(source)
		for _, banned := range []string{`"reporter"`, `"reporterId"`, `"reporterName"`, `"reporterSid"`} {
			if strings.Contains(text, banned) {
				t.Fatalf("%s 下发了举报人字段 %s，匿名被破坏", name, banned)
			}
		}
		// INSERT 是允许的，SELECT 不是。
		selectReporter := regexp.MustCompile(`(?is)select[^;]*\breport_reporter\b`)
		if selectReporter.MatchString(text) {
			t.Fatalf("%s 直接查询了 report_reporter；请改用 report_ids_by_reporter 这类单向函数", name)
		}
	}
}

// 审计里同样不能留下举报人。appendAudit 会写 actor_id、IP 和 UA，而班级管理员
// 读得到本班审计，所以举报的落库动作必须走 appendAnonymousAudit。
func TestReportFilingUsesAnonymousAudit(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(".", "report_handlers.go"))
	if err != nil {
		t.Fatalf("read report_handlers.go: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "appendAnonymousAudit(c, tx, \"report.filed\"") {
		t.Fatal("report.filed 必须走匿名审计")
	}
	if strings.Contains(text, "appendAudit(c, tx, \"report.filed\"") {
		t.Fatal("report.filed 走了实名审计，班管能从审计日志里看出举报人")
	}
}

// 举报附件走的是和小组提案同一条 notes 通道，但绝不能跟着记上传人：
// evidence.created_by 一旦写进举报人，班管读一行附件就知道是谁报的。
// 000042 里有一条 CHECK 顶着，这里再从代码这一侧钉一遍。
func TestReportNoteEvidenceStaysAnonymous(t *testing.T) {
	if noteRecordsUploader(noteOwnerReport) {
		t.Fatal("举报附件不能记上传人")
	}
	for _, owner := range []noteOwnerKind{noteOwnerSubmission, noteOwnerAppeal, noteOwnerObjection} {
		if !noteRecordsUploader(owner) {
			t.Fatalf("%s 的附件是实名的，必须记上传人", owner)
		}
		if !strings.Contains(noteUploaderPredicate(owner), "created_by=$3") {
			t.Fatalf("%s 的 complete/delete 必须认上传人", owner)
		}
	}
	// 举报那一路不能按上传人筛，否则等于承认库里存了上传人。
	if strings.Contains(noteUploaderPredicate(noteOwnerReport), "created_by=$3") {
		t.Fatal("举报附件不该按上传人筛选")
	}
	if !strings.Contains(noteUploaderPredicate(noteOwnerReport), "created_by IS NULL") {
		t.Fatal("举报附件必须要求 created_by 为空")
	}
}

// 传附件、删附件这两个动作本身也会暴露举报人——审计里记的是 actor_id 和 IP。
func TestReportNoteEvidenceAuditIsAnonymous(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(".", "note_evidence_handlers.go"))
	if err != nil {
		t.Fatalf("read note_evidence_handlers.go: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "appendAnonymousAudit(c, tx, action, string(owner), ownerID, metadata)") {
		t.Fatal("举报附件的审计必须走匿名通道")
	}
	// 三个动作都要经过分流函数，不能有谁绕过去直接 appendAudit。
	direct := regexp.MustCompile(`appendAudit\(c, tx, "note_evidence\.`)
	if direct.MatchString(text) {
		t.Fatal("note_evidence 的审计要统一走 appendNoteEvidenceAudit，否则举报那一路会漏成实名")
	}
}
