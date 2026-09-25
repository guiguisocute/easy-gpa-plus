// Package scheme defines the published scoring tree and the deterministic
// functions that interpret it. Database and HTTP concerns deliberately stay
// outside this package so the scoring rules can be reviewed and tested alone.
package scheme

import "time"

// Config is the immutable payload stored in scheme.config after publication.
// JSON field names match the TypeScript domain types used by the frontend.
//
// Window, Capabilities and HonorRoll are `json:"-"` on purpose. They are the
// class's runtime envelope, they live in class_timeline, and they are layered
// onto every Config as it is read. Excluding them from the JSON is what makes
// that invariant enforceable rather than merely intended: no marshal anywhere
// can put a stale window back into a scheme row. Anything that genuinely needs
// to persist the envelope — the settlement snapshot — says so explicitly.
type Config struct {
	SchemeName   string             `json:"schemeName"`
	Version      string             `json:"version"`
	Window       Window             `json:"-"`
	Capabilities Capabilities       `json:"-"`
	Weights      map[string]float64 `json:"weights"`
	HonorRoll    HonorRoll          `json:"-"`
	Categories   []Category         `json:"categories"`
}

// Window carries two different deadlines on purpose.
//
// Close is the sealing deadline: no new material after it, and every account
// is sealed automatically. Review, appeal, objection and arbitration all keep
// running — that is the long-standing §2.2 behaviour.
//
// Lockdown comes after Close and freezes the whole term: nobody, class admin
// included, may change anything. Only reading and exporting stay open. It is
// optional, so a class that never needs a hard freeze simply leaves it unset.
type Window struct {
	Open  time.Time `json:"open"`
	Close time.Time `json:"close"`
	// Lockdown is serialized even when unset, so a client can tell "no freeze
	// configured" from "this build does not know about freezing".
	Lockdown  *time.Time       `json:"lockdown"`
	Publicity *PublicityWindow `json:"publicity,omitempty"`
}

// Publicity opens read access to published score evidence within this class.
// It is independent of submission, reporting and the write-only lockdown.
type PublicityWindow struct {
	Open  time.Time `json:"open"`
	Close time.Time `json:"close"`
}

func (w Window) PublicityOpen(now time.Time) bool {
	return w.Publicity != nil && !now.Before(w.Publicity.Open) && now.Before(w.Publicity.Close)
}

// LockedAt reports whether the whole term is frozen at the given instant.
func (w Window) LockedAt(now time.Time) bool {
	return w.Lockdown != nil && !now.Before(*w.Lockdown)
}

type Capabilities struct {
	Submit    bool `json:"submit"`
	Edit      bool `json:"edit"`
	Appeal    bool `json:"appeal"`
	Review    bool `json:"review"`
	Arbitrate bool `json:"arbitrate"`
	// StudentReport opens anonymous reporting to students outside the review
	// group. It defaults off: the class administrator turns it on deliberately
	// and can turn it back off if it starts breeding suspicion instead of
	// accountability. Turning it off only closes the entry point — reports
	// already under review still run to a conclusion.
	StudentReport bool             `json:"studentReport"`
	Export        ExportCapability `json:"export"`
}

type ExportCapability struct {
	On   bool   `json:"on"`
	Gate string `json:"gate"`
}

// HonorRoll holds the two evaluation lines a class draws over the final
// ranking. TopPercent is the 三好学生 line. Awards are the scholarship tiers,
// each carrying its own share of the class rather than a cumulative one:
// 一等 5 / 二等 10 / 三等 15 means the top 5% take tier one, the next 10% take
// tier two, and so on. Both lines are configured per class and neither is
// derived from the other — a class may hand out scholarships to a wider or
// narrower band than it names 三好.
type HonorRoll struct {
	TopPercent int     `json:"topPercent"`
	Awards     []Award `json:"awards"`
}

type Award struct {
	Name string `json:"name"`
	// TopPercent is this tier's own quota share, not the running total.
	TopPercent int `json:"topPercent"`
}

// MaxAwards bounds the tier list so a malformed payload cannot turn the
// settlement loop into a long scan. Eight is far past any real 评优 policy.
const MaxAwards = 8

// MaxActivitiesPerItem bounds one item's activity roster. Every submission
// snapshots the item it was filed against, so an unbounded roster would be
// copied into each row.
const MaxActivitiesPerItem = 200

// DefaultAwards reproduces the tiers that used to be hard-coded in the student
// overview page, so an existing class keeps the exact behaviour it had before
// the tiers became configurable.
func DefaultAwards() []Award {
	return []Award{
		{Name: "一等综合素质奖学金", TopPercent: 5},
		{Name: "二等综合素质奖学金", TopPercent: 10},
		{Name: "三等综合素质奖学金", TopPercent: 15},
	}
}

type Category struct {
	Key      string  `json:"key"`
	Name     string  `json:"name"`
	MaxTotal float64 `json:"maxTotal"`
	// Formula shows how this category's score is produced when it is not a sum
	// of claims — 专业素质 is a credit-weighted average computed outside the
	// system, and a student who cannot see the formula cannot check the number
	// they were handed.
	Formula *Formula `json:"formula,omitempty"`
	// Note is category-level Markdown shown next to Formula: which courses
	// count, which do not, how letter grades convert. Per-scheme data rather
	// than hard-coded copy, because every college words this differently.
	Note         string        `json:"note,omitempty"`
	BaseItems    []BaseItem    `json:"baseItems"`
	PenaltyItems []PenaltyItem `json:"penaltyItems"`
	Items        []Item        `json:"items"`
}

// Formula is deliberately a fraction and nothing else: a left-hand name over a
// numerator and denominator. It is not a math language.
//
// The alternative was storing TeX and shipping a renderer for it, which buys an
// expression grammar nobody asked for — every 综测 formula in this domain is
// "weighted sum over total weight". Three plain strings render with a border
// and a flex column, inherit the theme, and cannot fail to parse. Text goes in
// as-is (「各门课程的分数」, 「∑」); there is no markup to escape.
//
// A category whose formula is not a fraction leaves Numerator and Denominator
// empty and puts the whole statement in Lhs.
type Formula struct {
	Lhs         string `json:"lhs"`
	Numerator   string `json:"numerator,omitempty"`
	Denominator string `json:"denominator,omitempty"`
}

// ImportedCategoryKey is the one category whose score never comes from student
// submissions: 专业素质 is imported from the registrar's grades. Settlement and
// the scorecard already skip it by key; submission intake and the student tree
// consult this so the three agree instead of drifting apart.
const ImportedCategoryKey = "major"

// AcceptsSubmissions reports whether students may file into this category.
func (c Category) AcceptsSubmissions() bool { return c.Key != ImportedCategoryKey }

type BaseItem struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Full         float64           `json:"full"`
	StudentClaim *BaseStudentClaim `json:"studentClaim,omitempty"`
	Note         string            `json:"note,omitempty"`
}

// BaseStudentClaim turns an otherwise reviewer-maintained base item into a
// material-backed student submission. Minimum describes the full-score
// condition; the generated submission also permits a partial self-report in
// the 0..Full range, subject to reviewer determination.
type BaseStudentClaim struct {
	Minimum  float64       `json:"minimum"`
	Unit     string        `json:"unit"`
	Evidence *EvidenceRule `json:"evidence,omitempty"`
}

type PenaltyItem struct {
	Key  string  `json:"key"`
	Name string  `json:"name"`
	Per  float64 `json:"per"`
}

type Item struct {
	Key            string        `json:"key"`
	Name           string        `json:"name"`
	ScoreRule      ScoreRule     `json:"scoreRule"`
	ExclusiveGroup string        `json:"exclusiveGroup,omitempty"`
	CapGroup       *CapGroup     `json:"capGroup,omitempty"`
	Evidence       *EvidenceRule `json:"evidence,omitempty"`
	Note           string        `json:"note,omitempty"`
	// Activities is this academic year's roster of concrete events that fall
	// under this item — the 活动汇总表, split up and hung on the item it belongs
	// to. A student filling in 志愿服务 needs to see that 书香晨光 is 1h/0.25 分
	// right there; folding the same 70 rows into Note would bury the rules.
	//
	// Deliberately not part of the reusable template: TemplateFromConfig strips
	// it, because next year's class inherits the scoring rules, not this year's
	// events.
	Activities []Activity `json:"activities,omitempty"`
}

// Activity is one row of the class activity roster. Score stays free text
// because the roster words it differently per row (「1h/0.25 分」, 「组织者 2 分」,
// 「省级一等奖(10分/5分)」); normalizing it here would mean inventing a rule the
// 学院 did not write.
type Activity struct {
	Name  string `json:"name"`
	Date  string `json:"date,omitempty"`
	Level string `json:"level,omitempty"`
	Org   string `json:"org,omitempty"`
	Score string `json:"score,omitempty"`
}

// ScoreRule uses one struct rather than interface-based polymorphism. That
// keeps JSON decoding explicit: Type selects which fields are meaningful.
type ScoreRule struct {
	Type    string       `json:"type"` // per_unit | enum | free
	Unit    string       `json:"unit,omitempty"`
	Per     float64      `json:"per,omitempty"`
	Cap     *float64     `json:"cap,omitempty"`
	Min     *float64     `json:"min,omitempty"`
	Max     *float64     `json:"max,omitempty"`
	Options []EnumOption `json:"options,omitempty"`
	Levels  []string     `json:"levels,omitempty"`
	Minimum *float64     `json:"minimum,omitempty"`
	Award   *float64     `json:"award,omitempty"`
}

type EnumOption struct {
	Label string   `json:"label"`
	Score float64  `json:"score"`
	Path  []string `json:"path,omitempty"`
}

type CapGroup struct {
	Key string  `json:"key"`
	Cap float64 `json:"cap"`
}

type EvidenceRule struct {
	Required bool     `json:"required"`
	Types    []string `json:"types"`
	MaxMB    int64    `json:"maxMb"`
}

// Claim contains the student-entered part that a deterministic rule can use.
// Enum rules use Option plus an optional editable Score; legacy enum claims
// without Score fall back to the option's suggested score.
type Claim struct {
	Quantity *float64 `json:"quantity,omitempty"`
	Option   string   `json:"option,omitempty"`
	Score    *float64 `json:"score,omitempty"`
}
