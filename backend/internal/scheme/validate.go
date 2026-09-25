package scheme

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

var allowedCategories = map[string]bool{
	"major": true, "moral": true, "practice": true, "health": true,
}

// ValidateTimeline checks the per-class runtime envelope on its own. It lives
// apart from Validate because the timeline is no longer part of the published
// scheme: it is edited through its own endpoint, against its own table, and
// must be checkable without a scoring tree in hand.
func ValidateTimeline(window Window, capabilities Capabilities, honorRoll HonorRoll) error {
	if !window.Open.Before(window.Close) {
		return errors.New("开放时间必须早于封存截止时间")
	}
	// Equal is allowed: locking down the instant sealing closes is a legitimate
	// policy, it just leaves no review tail.
	if window.Lockdown != nil && window.Lockdown.Before(window.Close) {
		return errors.New("全系统封锁时间不能早于封存截止时间")
	}
	if p := window.Publicity; p != nil && (p.Open.IsZero() || p.Close.IsZero() || !p.Open.Before(p.Close)) {
		return errors.New("公示开始与结束时间须完整填写，且开始时间必须早于结束时间")
	}
	if capabilities.Export.Gate != "settlement" {
		return errors.New("导出须在结算完成后开放")
	}
	if honorRoll.TopPercent < 0 || honorRoll.TopPercent > 100 {
		return errors.New("评优排名比例须在 0% 至 100% 之间")
	}
	if len(honorRoll.Awards) > MaxAwards {
		return fmt.Errorf("奖项最多可设置 %d 个档位", MaxAwards)
	}
	// Tiers are cumulative when they are applied, so the running total is what
	// has to stay inside the class. An empty list is fine — it means the class
	// hands out no scholarship tiers at all.
	seen := make(map[string]bool, len(honorRoll.Awards))
	cumulative := 0
	for index, award := range honorRoll.Awards {
		name := strings.TrimSpace(award.Name)
		if name == "" {
			return fmt.Errorf("第 %d 个奖项档位缺少名称，请补充", index+1)
		}
		if seen[name] {
			return fmt.Errorf("奖项档位名称 %q 重复，请修改", name)
		}
		seen[name] = true
		if award.TopPercent < 0 || award.TopPercent > 100 {
			return fmt.Errorf("第 %d 个奖项档位的比例须在 0%% 至 100%% 之间", index+1)
		}
		cumulative += award.TopPercent
	}
	if cumulative > 100 {
		return fmt.Errorf("各奖项名额比例合计为 %d%%，不能超过全班人数的 100%%", cumulative)
	}
	return nil
}

// Validate checks the whole tree before publication. Drafts may be incomplete;
// published versions may not, because every submission snapshots this payload.
func Validate(c Config) error {
	if strings.TrimSpace(c.SchemeName) == "" {
		return errors.New("请填写方案名称")
	}
	if err := ValidateTimeline(c.Window, c.Capabilities, c.HonorRoll); err != nil {
		return err
	}

	var weightSum float64
	for key, weight := range c.Weights {
		if !allowedCategories[key] {
			return fmt.Errorf("权重中包含无法识别的大项 %q，请检查方案", key)
		}
		if !finite(weight) || weight < 0 || weight > 1 {
			return fmt.Errorf("大项 %q 的权重须在 0 至 1 之间", key)
		}
		weightSum += weight
	}
	if math.Abs(weightSum-1) > 0.000001 {
		return fmt.Errorf("各大项权重之和必须为 1，当前合计为 %g", weightSum)
	}

	categoryKeys := make(map[string]bool, len(c.Categories))
	itemKeys := make(map[string]bool)
	capGroups := make(map[string]float64)
	for _, category := range c.Categories {
		if !allowedCategories[category.Key] {
			return fmt.Errorf("无法识别大项 %q，请检查方案", category.Key)
		}
		if categoryKeys[category.Key] {
			return fmt.Errorf("大项标识 %q 重复，请修改", category.Key)
		}
		categoryKeys[category.Key] = true
		if strings.TrimSpace(category.Name) == "" || !finite(category.MaxTotal) || category.MaxTotal <= 0 {
			return fmt.Errorf("大项 %q 需要填写名称，且总分上限须大于 0", category.Key)
		}
		// Both fields are optional prose. Bounding them keeps a pathological
		// paste out of every submission snapshot that copies this config.
		if formula := category.Formula; formula != nil {
			if strings.TrimSpace(formula.Lhs) == "" {
				return fmt.Errorf("大项 %q 的公式缺少等号左侧内容", category.Key)
			}
			// A bar with only one side is a rendering bug waiting to happen.
			if (strings.TrimSpace(formula.Numerator) == "") != (strings.TrimSpace(formula.Denominator) == "") {
				return fmt.Errorf("大项 %q 的公式分子和分母须同时填写或同时留空", category.Key)
			}
			for _, part := range []string{formula.Lhs, formula.Numerator, formula.Denominator} {
				if len(part) > 500 {
					return fmt.Errorf("大项 %q 的公式各部分不能超过 500 字节", category.Key)
				}
			}
		}
		if len(category.Note) > 20_000 {
			return fmt.Errorf("大项 %q 的说明不能超过 20000 字节", category.Key)
		}

		localKeys := make(map[string]bool)
		for _, base := range category.BaseItems {
			if err := validateTreeKey(category.Key, base.Key, base.Name, localKeys, itemKeys); err != nil {
				return err
			}
			if !finite(base.Full) || base.Full < 0 {
				return fmt.Errorf("基础项 %q 的满分须为大于或等于 0 的有效数字", base.Key)
			}
			if base.StudentClaim != nil {
				if base.Full <= 0 || !finite(base.StudentClaim.Minimum) || base.StudentClaim.Minimum <= 0 || strings.TrimSpace(base.StudentClaim.Unit) == "" {
					return fmt.Errorf("需申报的基础项 %q 须填写计量单位，且满分和达标数量须大于 0", base.Key)
				}
				if base.StudentClaim.Evidence != nil && (base.StudentClaim.Evidence.MaxMB <= 0 || len(base.StudentClaim.Evidence.Types) == 0) {
					return fmt.Errorf("需申报的基础项 %q 须设置佐证文件类型和大小上限", base.Key)
				}
			}
		}
		for _, penalty := range category.PenaltyItems {
			if err := validateTreeKey(category.Key, penalty.Key, penalty.Name, localKeys, itemKeys); err != nil {
				return err
			}
			if !finite(penalty.Per) || penalty.Per >= 0 {
				return fmt.Errorf("扣分项 %q 的每次分值须为负数", penalty.Key)
			}
		}
		for _, item := range category.Items {
			if err := validateTreeKey(category.Key, item.Key, item.Name, localKeys, itemKeys); err != nil {
				return err
			}
			if err := validateScoreRule(item.Key, item.ScoreRule); err != nil {
				return err
			}
			if item.CapGroup != nil {
				if strings.TrimSpace(item.CapGroup.Key) == "" || !finite(item.CapGroup.Cap) || item.CapGroup.Cap < 0 {
					return fmt.Errorf("小项 %q 的共享封顶组须填写标识，且上限须为大于或等于 0 的有效数字", item.Key)
				}
				if prior, ok := capGroups[item.CapGroup.Key]; ok && prior != item.CapGroup.Cap {
					return fmt.Errorf("共享封顶组 %q 的上限不一致，请为组内小项设置相同上限", item.CapGroup.Key)
				}
				capGroups[item.CapGroup.Key] = item.CapGroup.Cap
			}
			if item.Evidence != nil {
				if item.Evidence.MaxMB <= 0 || len(item.Evidence.Types) == 0 {
					return fmt.Errorf("小项 %q 须设置佐证文件类型和大小上限", item.Key)
				}
			}
			if len(item.Activities) > MaxActivitiesPerItem {
				return fmt.Errorf("小项 %q 最多可列出 %d 个活动", item.Key, MaxActivitiesPerItem)
			}
			for index, activity := range item.Activities {
				if strings.TrimSpace(activity.Name) == "" {
					return fmt.Errorf("小项 %q 的第 %d 个活动缺少名称", item.Key, index+1)
				}
				for _, field := range []string{activity.Name, activity.Date, activity.Level, activity.Org, activity.Score} {
					if len(field) > 200 {
						return fmt.Errorf("小项 %q 的第 %d 个活动中有内容超过 200 字节，请缩短", item.Key, index+1)
					}
				}
			}
		}
	}

	for key, weight := range c.Weights {
		if weight > 0 && !categoryKeys[key] {
			return fmt.Errorf("已设置权重的大项 %q 缺少评分结构，请补充", key)
		}
	}
	return nil
}

func validateTreeKey(category, key, name string, local, global map[string]bool) error {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(name) == "" {
		return fmt.Errorf("大项 %q 中有小项未填写标识或名称，请补充", category)
	}
	if local[key] || global[key] {
		return fmt.Errorf("小项标识 %q 重复，请为每个小项设置不同标识", key)
	}
	local[key] = true
	global[key] = true
	return nil
}

func validateScoreRule(itemKey string, r ScoreRule) error {
	switch r.Type {
	case "per_unit":
		if strings.TrimSpace(r.Unit) == "" || !finite(r.Per) || r.Per == 0 {
			return fmt.Errorf("按数量计分的小项 %q 须填写计量单位，且单位分值不能为 0", itemKey)
		}
		if r.Cap != nil && (!finite(*r.Cap) || *r.Cap < 0) {
			return fmt.Errorf("按数量计分的小项 %q 的上限须为大于或等于 0 的有效数字", itemKey)
		}
	case "enum":
		if len(r.Options) == 0 {
			return fmt.Errorf("按档计分的小项 %q 至少需要一个档位", itemKey)
		}
		for _, level := range r.Levels {
			if strings.TrimSpace(level) == "" {
				return fmt.Errorf("按档计分的小项 %q 有未填写名称的层级，请补充", itemKey)
			}
		}
		seen := make(map[string]bool, len(r.Options))
		for _, option := range r.Options {
			if strings.TrimSpace(option.Label) == "" || !finite(option.Score) {
				return fmt.Errorf("按档计分的小项 %q 的档位须填写名称和有效分值", itemKey)
			}
			if seen[option.Label] {
				return fmt.Errorf("按档计分的小项 %q 的档位 %q 重复，请修改", itemKey, option.Label)
			}
			for _, part := range option.Path {
				if strings.TrimSpace(part) == "" {
					return fmt.Errorf("按档计分的小项 %q 的档位 %q 有未填写的层级名称，请补充", itemKey, option.Label)
				}
			}
			seen[option.Label] = true
		}
	case "free":
		if r.Min == nil || r.Max == nil || !finite(*r.Min) || !finite(*r.Max) || *r.Min > *r.Max {
			return fmt.Errorf("自报分小项 %q 须填写有效的最低分和最高分，且最低分不能大于最高分", itemKey)
		}
	case "threshold":
		if strings.TrimSpace(r.Unit) == "" || r.Minimum == nil || r.Award == nil || !finite(*r.Minimum) || !finite(*r.Award) || *r.Minimum <= 0 || *r.Award < 0 {
			return fmt.Errorf("达标计分的小项 %q 须填写单位、正数达标数量和不小于 0 的奖励分", itemKey)
		}
	default:
		return fmt.Errorf("小项 %q 的计分方式 %q 无法识别，请重新选择", itemKey, r.Type)
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
