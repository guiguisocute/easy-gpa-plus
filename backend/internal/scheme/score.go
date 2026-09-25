package scheme

import (
	"fmt"
	"math"
)

// Points stores one thousandth of a point. Fixed-point arithmetic makes caps,
// comparisons and repeated settlements independent of binary float rounding.
type Points int64

const pointsScale = 1000

func NewPoints(v float64) Points  { return Points(math.Round(v * pointsScale)) }
func (p Points) Float64() float64 { return float64(p) / pointsScale }

// ScoreClaim computes the student's expected score. Reviewers may later adjust
// it, but this function is also reused to validate deterministic submissions.
func ScoreClaim(rule ScoreRule, claim Claim) (Points, error) {
	switch rule.Type {
	case "per_unit":
		if claim.Quantity == nil {
			return 0, claimError("claim_quantity_required", "请填写已完成的数量", nil)
		}
		if !finite(*claim.Quantity) || *claim.Quantity < 0 {
			return 0, claimError("claim_quantity_invalid", "数量须为大于或等于 0 的有效数字", nil)
		}
		score := NewPoints(*claim.Quantity * rule.Per)
		if rule.Cap != nil {
			score = minPoints(score, NewPoints(*rule.Cap))
		}
		return score, nil
	case "enum":
		var selected *EnumOption
		for _, option := range rule.Options {
			if option.Label == claim.Option {
				selected = &option
				break
			}
		}
		if selected == nil {
			return 0, claimError("claim_option_invalid", "请选择当前方案中的档位；不确定时可先保存草稿", nil)
		}
		// Old submissions only stored the selected option. Keep those snapshots
		// readable while allowing new submissions to adjust the suggested score.
		if claim.Score == nil {
			return NewPoints(selected.Score), nil
		}
		if !finite(*claim.Score) {
			return 0, claimError("claim_score_invalid", "请填写有效的期望分数", nil)
		}
		maxScore := Points(0)
		for _, option := range rule.Options {
			maxScore = max(maxScore, NewPoints(option.Score))
		}
		score := NewPoints(*claim.Score)
		if score < 0 || score > maxScore {
			return 0, claimError("claim_score_out_of_range", fmt.Sprintf("期望分数须在 0 至 %g 分之间", maxScore.Float64()), map[string]any{"min": 0, "max": maxScore.Float64()})
		}
		return score, nil
	case "free":
		if claim.Score == nil {
			return 0, claimError("claim_score_required", "请填写期望分数", nil)
		}
		if !finite(*claim.Score) {
			return 0, claimError("claim_score_invalid", "请填写有效的期望分数", nil)
		}
		score := NewPoints(*claim.Score)
		if rule.Min != nil && score < NewPoints(*rule.Min) {
			return 0, claimError("claim_score_below_minimum", fmt.Sprintf("期望分数不能低于 %g 分", *rule.Min), map[string]any{"min": *rule.Min})
		}
		if rule.Max != nil && score > NewPoints(*rule.Max) {
			return 0, claimError("claim_score_above_maximum", fmt.Sprintf("期望分数不能超过 %g 分", *rule.Max), map[string]any{"max": *rule.Max})
		}
		return score, nil
	case "threshold":
		if claim.Quantity == nil {
			return 0, claimError("claim_quantity_required", "请填写已完成的数量", nil)
		}
		if !finite(*claim.Quantity) || *claim.Quantity < 0 || rule.Minimum == nil || rule.Award == nil {
			return 0, claimError("claim_quantity_invalid", "数量须为大于或等于 0 的有效数字", nil)
		}
		if *claim.Quantity < *rule.Minimum {
			return 0, nil
		}
		return NewPoints(*rule.Award), nil
	default:
		return 0, claimError("claim_rule_invalid", "当前小项的计分规则不可用，请联系班级管理员检查方案", nil)
	}
}

// ScoredItem is an already-decided submission interpreted with the immutable
// item rule captured at submission time.
type ScoredItem struct {
	ID             int64
	ItemKey        string
	Score          Points
	ItemCap        *Points
	ExclusiveGroup string
	CapGroupKey    string
	CapGroupCap    Points
}

type RecordedBase struct {
	ItemKey string
	Kind    string // base | penalty
	Score   Points
}

type CategoryBreakdown struct {
	ItemTotal    Points
	BaseTotal    Points
	PenaltyTotal Points
	BeforeCap    Points
	Total        Points
}

// ComputeCategoryScore applies the locked order from the design document:
// item cap -> exclusive group -> shared cap group -> base/penalty -> category cap.
func ComputeCategoryScore(items []ScoredItem, base []RecordedBase, maxTotal Points) CategoryBreakdown {
	chosenExclusive := make(map[string]ScoredItem)
	plain := make([]ScoredItem, 0, len(items))
	for _, item := range items {
		if item.ItemCap != nil {
			item.Score = minPoints(item.Score, *item.ItemCap)
		}
		if item.ExclusiveGroup == "" {
			plain = append(plain, item)
			continue
		}
		prior, ok := chosenExclusive[item.ExclusiveGroup]
		if !ok || item.Score > prior.Score || (item.Score == prior.Score && item.ID < prior.ID) {
			chosenExclusive[item.ExclusiveGroup] = item
		}
	}
	for _, item := range chosenExclusive {
		plain = append(plain, item)
	}

	var itemTotal Points
	groupTotals := make(map[string]Points)
	groupCaps := make(map[string]Points)
	for _, item := range plain {
		if item.CapGroupKey == "" {
			itemTotal += item.Score
			continue
		}
		groupTotals[item.CapGroupKey] += item.Score
		groupCaps[item.CapGroupKey] = item.CapGroupCap
	}
	for key, total := range groupTotals {
		itemTotal += minPoints(total, groupCaps[key])
	}

	result := CategoryBreakdown{ItemTotal: itemTotal}
	for _, record := range base {
		switch record.Kind {
		case "base":
			result.BaseTotal += record.Score
		case "penalty":
			result.PenaltyTotal += record.Score
		}
	}
	result.BeforeCap = result.ItemTotal + result.BaseTotal + result.PenaltyTotal
	result.Total = minPoints(result.BeforeCap, maxTotal)
	return result
}

func minPoints(a, b Points) Points {
	if a < b {
		return a
	}
	return b
}
