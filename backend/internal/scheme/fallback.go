package scheme

import (
	"fmt"
	"strings"
	"time"
)

const otherSelfReportPrefix = "__other_self_report_"

// OtherSelfReportKey is reserved for the system-provided catch-all entry that
// appears at the top of every student category. It is derived from the fixed
// category key, so it stays stable across scheme versions and submissions.
func OtherSelfReportKey(categoryKey string) string {
	return otherSelfReportPrefix + categoryKey
}

// OtherSelfReportItem is intentionally generated at read/submit time instead
// of being persisted into every template. That makes the safety net available
// to already-published schemes as well as future ones.
func OtherSelfReportItem(category Category) Item {
	minScore := 0.0
	maxScore := category.MaxTotal
	return Item{
		Key:       OtherSelfReportKey(category.Key),
		Name:      "其他项目自报",
		ScoreRule: ScoreRule{Type: "free", Min: &minScore, Max: &maxScore},
		Evidence: &EvidenceRule{
			Required: true,
			Types:    CommonEvidenceTypes(),
			MaxMB:    DefaultEvidenceMaxMB,
		},
		Note: "- 仅在现有小项均不适用时使用。\n- 请写明项目类别、级别、时间、主办方及自报依据，最终分值由审核人认定。",
	}
}

// New classes start with an editable example, without institution-specific rules.
// DefaultWindow spans wide enough that a class which has never opened its
// timeline is neither closed nor locked. The administrator narrows it on the
// 「时间窗口」page; nothing here should read as a real academic term.
func DefaultWindow() Window {
	return Window{
		Open:  time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC),
		Close: time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
	}
}

func DefaultCapabilities() Capabilities {
	return Capabilities{
		Submit: true, Edit: true, Appeal: true, Review: true, Arbitrate: true,
		// 匿名举报默认关闭。一个班要不要开这个口子是班级管理员的决定，
		// 不是新建班级时替他们默认好的。
		StudentReport: false,
		Export:        ExportCapability{On: false, Gate: "settlement"},
	}
}

func DefaultHonorRoll() HonorRoll {
	return HonorRoll{TopPercent: 30, Awards: DefaultAwards()}
}

func DefaultSelfReportConfig(version string) Config {
	return Config{
		SchemeName:   "系统默认综合测评方案",
		Version:      version,
		Window:       DefaultWindow(),
		Capabilities: DefaultCapabilities(),
		Weights:      map[string]float64{"major": 0.6, "moral": 0.15, "practice": 0.15, "health": 0.1},
		HonorRoll:    DefaultHonorRoll(),
		Categories: []Category{
			defaultSelfReportCategory("major", "专业素质", nil, nil),
			defaultSelfReportCategory("moral", "思想道德素质", defaultMoralBaseItems(), defaultMoralPenaltyItems()),
			defaultSelfReportCategory("practice", "实践创新素质", defaultPracticeBaseItems(), nil),
			defaultSelfReportCategory("health", "身体心理素质", defaultHealthBaseItems(), defaultHealthPenaltyItems()),
		},
	}
}

func defaultSelfReportCategory(key, name string, base []BaseItem, penalties []PenaltyItem) Category {
	if base == nil {
		base = []BaseItem{}
	}
	if penalties == nil {
		penalties = []PenaltyItem{}
	}
	return Category{
		Key: key, Name: name, MaxTotal: 100,
		BaseItems: base, PenaltyItems: penalties, Items: []Item{},
	}
}

func defaultMoralBaseItems() []BaseItem {
	return []BaseItem{{Key: "moral_base", Name: "日常表现基础分（示例）", Full: 70, Note: "请按本班公开规则调整基础分及认定条件。"}}
}
func defaultMoralPenaltyItems() []PenaltyItem {
	return []PenaltyItem{{Key: "moral_penalty", Name: "已认定违规记录（示例）", Per: -5}}
}
func defaultPracticeBaseItems() []BaseItem {
	return []BaseItem{{Key: "practice_base", Name: "实践参与基础分（须申报）", Full: 40,
		StudentClaim: &BaseStudentClaim{Minimum: 2, Unit: "项", Evidence: &EvidenceRule{Required: true, Types: CommonEvidenceTypes(), MaxMB: DefaultEvidenceMaxMB}},
		Note: `- 示例条件：完成两项实践活动。请由班管调整活动范围、数量与分值。
- 学生提交材料后由审核人认定，未申报不计分。`}}
}
func defaultHealthBaseItems() []BaseItem {
	return []BaseItem{{Key: "health_base", Name: "文体参与基础分（示例）", Full: 70, Note: "请按本班公开规则调整基础分及认定条件。"}}
}
func defaultHealthPenaltyItems() []PenaltyItem { return []PenaltyItem{} }

// StudentClaimBaseItem adapts a material-backed base item to the same immutable
// submission snapshot shape used by regular claimable items. The configured
// minimum remains the full-score condition, but the student may self-report a
// partial score for incomplete yet valid material (for example one of two
// required competitions). Reviewers make the final determination within
// 0..Full, just as they do for other self-reported items.
func StudentClaimBaseItem(base BaseItem) (Item, bool) {
	if base.StudentClaim == nil {
		return Item{}, false
	}
	minScore := 0.0
	maxScore := base.Full
	return Item{
		Key:       base.Key,
		Name:      base.Name,
		ScoreRule: ScoreRule{Type: "free", Min: &minScore, Max: &maxScore},
		Evidence:  base.StudentClaim.Evidence,
		Note:      StudentClaimBaseNote(base),
	}, true
}

// StudentClaimBaseNote is the student-visible Markdown for a claimable base
// item. The generated full-score condition always comes first; an optional
// scheme note is appended as-is so class administrators can add extra rules.
func StudentClaimBaseNote(base BaseItem) string {
	if base.StudentClaim == nil {
		return strings.TrimSpace(base.Note)
	}
	generated := fmt.Sprintf(
		"- 完整达标：至少 %.3g %s可得 %.3g 分。\n- 未完全达标时，仍可依据已有材料在 0—%.3g 分内自报，最终由审核人认定。",
		base.StudentClaim.Minimum, base.StudentClaim.Unit, base.Full, base.Full,
	)
	extra := strings.TrimSpace(base.Note)
	if extra == "" {
		return generated
	}
	return generated + "\n\n" + extra
}
