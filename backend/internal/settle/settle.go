// Package settle builds immutable, deterministic score snapshots.
package settle

import (
	"errors"
	"math"
	"sort"

	"easygpa/backend/internal/scheme"
)

type StudentInput struct {
	UserID         int64
	SID            string
	Name           string
	CategoryScores map[string]scheme.Points
}

type Snapshot struct {
	UserID         int64              `json:"userId"`
	SID            string             `json:"sid"`
	Name           string             `json:"name"`
	CategoryScores map[string]float64 `json:"categoryScores"`
	// CategoryRanks holds this student's class rank inside each category. It is
	// part of the snapshot because 三好 is decided by these four ranks, not by
	// the total: a number that decides an award may not be re-derived per page.
	CategoryRanks map[string]int `json:"categoryRanks"`
	TotalScore    float64        `json:"totalScore"`
	ClassRank     int            `json:"classRank"`
	MajorRank     int            `json:"majorRank"`
	Honor         bool           `json:"honor"`
	// AwardTier names the scholarship tier this rank falls into, or is empty
	// when it falls outside every tier. It is computed here rather than in the
	// browser so the export, the student page and the admin preview all read
	// one frozen value instead of each deriving their own.
	AwardTier string        `json:"awardTier"`
	Total     scheme.Points `json:"-"`
	Major     scheme.Points `json:"-"`
}

// Compute converts the complete class input into snapshots.
//
// The ordering is 总分 → 专业素质分 → 学号, and it is the ordering the
// scholarship tiers are handed out along. SID last makes repeated runs
// byte-for-byte reproducible; 专业素质分 in the middle is what the 学院 asks
// for when two students tie on the total (§三.2 同分顺延) — deciding who takes
// 一等 by student number does not survive being read out loud.
func Compute(students []StudentInput, weights map[string]float64, honorRoll scheme.HonorRoll) ([]Snapshot, error) {
	if honorRoll.TopPercent < 0 || honorRoll.TopPercent > 100 {
		return nil, errors.New("honorRoll.topPercent must be between 0 and 100")
	}
	var sum float64
	for _, weight := range weights {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return nil, errors.New("weights must be finite and non-negative")
		}
		sum += weight
	}
	if math.Abs(sum-1) > 0.000001 {
		return nil, errors.New("weights must add up to one")
	}

	result := make([]Snapshot, 0, len(students))
	seenSID := make(map[string]bool, len(students))
	for _, student := range students {
		if student.UserID == 0 || student.SID == "" || seenSID[student.SID] {
			return nil, errors.New("students need unique non-empty SIDs and non-zero user IDs")
		}
		seenSID[student.SID] = true
		row := Snapshot{UserID: student.UserID, SID: student.SID, Name: student.Name, CategoryScores: make(map[string]float64)}
		for key, points := range student.CategoryScores {
			row.CategoryScores[key] = points.Float64()
			row.Total += scheme.NewPoints(points.Float64() * weights[key])
		}
		row.Major = student.CategoryScores["major"]
		row.TotalScore = row.Total.Float64()
		result = append(result, row)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Total != result[j].Total {
			return result[i].Total > result[j].Total
		}
		if result[i].Major != result[j].Major {
			return result[i].Major > result[j].Major
		}
		return result[i].SID < result[j].SID
	})
	// ClassRank stays a competition rank: two students on the same total are
	// both "第 2 名", because that is what happened. 顺延 shows up in the tier
	// column instead — same rank, different tier — which is exactly how it
	// reads on the 学院 forms.
	for i := range result {
		result[i].ClassRank = competitionRank(result, i, func(s Snapshot) scheme.Points { return s.Total })
	}

	ranked := make([]Ranked, len(result))
	for i := range result {
		ranked[i] = Ranked{UserID: result[i].UserID, Scores: result[i].CategoryScores}
	}
	ranks := CategoryRanks(ranked)

	honorLine := quota(len(result), honorRoll.TopPercent)
	// result is already in award order, so the tier list can be read by index.
	tiers := awardTiers(len(result), honorRoll.Awards)
	for i := range result {
		result[i].CategoryRanks = ranks[result[i].UserID]
		result[i].MajorRank = result[i].CategoryRanks["major"]
		result[i].Honor = honored(result[i].CategoryRanks, weights, honorLine)
		result[i].AwardTier = tiers[i]
	}
	return result, nil
}

// Ranked is the minimum a row needs in order to be ranked category by category.
type Ranked struct {
	UserID int64
	Scores map[string]float64
}

// CategoryRanks ranks the class once per category and returns
// userID → category → competition rank. Equal scores share a rank.
//
// 结算、学生页和导出以前各带一份自己的实现，三份在做同一件事。它们碰巧算得
// 一样，但没有任何东西保证这一点；而三好判据现在要读各维度名次——一个参与评定
// 的数不能有三个来源。
func CategoryRanks(rows []Ranked) map[int64]map[string]int {
	keys := make(map[string]bool)
	for _, row := range rows {
		for key := range row.Scores {
			keys[key] = true
		}
	}
	result := make(map[int64]map[string]int, len(rows))
	for _, row := range rows {
		result[row.UserID] = make(map[string]int, len(keys))
	}
	ordered := make([]Ranked, len(rows))
	for key := range keys {
		copy(ordered, rows)
		sort.SliceStable(ordered, func(i, j int) bool {
			left := scheme.NewPoints(ordered[i].Scores[key])
			right := scheme.NewPoints(ordered[j].Scores[key])
			if left != right {
				return left > right
			}
			return ordered[i].UserID < ordered[j].UserID
		})
		rank := 0
		var previous scheme.Points
		for index, row := range ordered {
			score := scheme.NewPoints(row.Scores[key])
			if index == 0 || score != previous {
				rank = index + 1
			}
			result[row.UserID][key] = rank
			previous = score
		}
	}
	return result
}

// honored decides 三好: every weighted category's class rank must be inside the
// line, not just the total. That is the 学院 rule — 专业素质、思想道德、实践创新、
// 身体心理四个维度均位列班级前 30%.
//
// The dimensions come from the weight table rather than from the category tree:
// Compute only ever knows weights, and 三好 does not get to break that boundary.
func honored(ranks map[string]int, weights map[string]float64, line int) bool {
	if line <= 0 {
		return false
	}
	for key, weight := range weights {
		if weight <= 0 {
			continue
		}
		if rank, ok := ranks[key]; !ok || rank > line {
			return false
		}
	}
	return true
}

// quota turns a percentage of the class into a head count, rounding half up.
//
// 学院通知对奖学金名额写死了"结果四舍五入"；三好线沿用同一个口径，因为一份
// 规则里不该有两种取整。这里以前是向上取整，改口径会让某些班级人数下的名额
// 和三好人数变化，这是有意的——44 人班原来一等发 3 个，学院口径是 2 个。
func quota(size, percent int) int {
	return int(math.Round(float64(size) * float64(percent) / 100))
}

// awardTiers lays each tier's head count out along the ranking order: the first
// tier's quota is filled, then the second's, then the third's.
//
// 名额按学院口径算——每档独立四舍五入(总人数×占比)，再相加。通知里明确写了
// "总获奖人数为一、二、三等相加，不采用总人数×30%的计算方式"，所以不能先合成
// 一个累计百分比再取一次整。
//
// 按序号发放而不是按名次发放，意味着同分不会让某一档超员：总分相同的两人由
// Compute 的排序（专业素质分优先）分出先后，前一个进上一档，后一个顺延。通知
// 给"三等最后两位同分"留了超报的口子，这里不实现——同分先看专业素质分，真要
// 走到超报那一步，两人得连专业素质分都完全相同。这是决定，不是遗漏。
func awardTiers(size int, awards []scheme.Award) []string {
	tiers := make([]string, size)
	next := 0
	for _, award := range awards {
		for count := quota(size, award.TopPercent); count > 0 && next < size; count-- {
			tiers[next] = award.Name
			next++
		}
	}
	return tiers
}

func competitionRank(rows []Snapshot, index int, score func(Snapshot) scheme.Points) int {
	if index == 0 {
		return 1
	}
	if score(rows[index]) == score(rows[index-1]) {
		return competitionRank(rows, index-1, score)
	}
	return index + 1
}
