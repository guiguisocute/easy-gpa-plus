// 学院报表：把同一份结算快照按学院下发的 5 份附件排一遍。
//
// 这里不算分、不排名、不判档，一个数都不新造——全部读 settlement 快照。它只做
// 三件其余产物不做的事：
//
//  1. 折算。系统内部各大项按 0–100 记分，权重只在合成总分时用一次；附件要的是
//     折算后的分（"学业成绩原始 95.00、占比 60%，填 57.00"），且保留两位小数。
//  2. 合成「操行素质分」。附件2 与附件5 要这一列，它先汇总非专业项的未舍入折算值，
//     再统一舍入；系统内部没有这个维度。
//  3. 换名。方案里档位叫"一等综合素质奖学金"，附件要"壹等"。
//
// 身份证号、银行卡号、开户行、获奖金额不采集，生成时留空。
// Excel 的长数字列预设文本格式；Word 按原表格留出可编辑的空格。
package exportjob

import (
	"maps"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/settle"
)

// 附件4 与附件5 的样例把等级写死成「壹等/贰等/叁等」。按档位在方案里的下标
// 映射，不按中文名匹配：档位名是班级可配的，按名字反查会在改名那天悄悄错掉。
var tierOrdinals = []string{"壹等", "贰等", "叁等", "肆等"}

// 教务班级名形如「24级计算机科学与技术2班」，也允许四位年份写法。
var enrollmentPattern = regexp.MustCompile(`^(\d{4}|\d{2})级(.+?)\d*班$`)

// 填报日期使用北京时间。
var chinaZone = time.FixedZone("CST", 8*3600)

// Inputs are finite scores and weights from the validated settlement snapshot.
// Parse their decimal representation before arithmetic: binary 0.29*0.5 can
// fall below 0.145 and otherwise round the wrong way at a half-cent boundary.
func collegeDecimal(value float64) *big.Rat {
	decimal, _ := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	return decimal
}

func collegeRound(decimal *big.Rat) float64 {
	// Rat.FloatString rounds decimal halves away from zero.
	value, _ := strconv.ParseFloat(decimal.FloatString(2), 64)
	if value == 0 {
		return 0 // Avoid displaying a negative zero.
	}
	return value
}

func round2(value float64) float64 { return collegeRound(collegeDecimal(value)) }

// collegeRow 是一名学生在附件口径下的样子：折算分、操行素质分、中文档位。
type collegeRow struct {
	snapshotRow
	Weighted map[string]float64
	Total    float64
	Conduct  float64
	Tier     string
	Ranks    map[string]int
}

// collegeWeighted rounds each output independently to two decimal places.
// Never adjust an individual score to absorb the sum's rounding difference.
// The total still comes from the frozen settlement; conduct is rounded once
// after summing the unrounded non-major components.
func collegeWeighted(row snapshotRow, config scheme.Config) (map[string]float64, float64, float64) {
	parts := make(map[string]float64, len(config.Weights))
	conduct := new(big.Rat)
	for _, key := range categoryOrder(config) {
		weighted := new(big.Rat).Mul(collegeDecimal(row.CategoryScores[key]), collegeDecimal(config.Weights[key]))
		parts[key] = collegeRound(weighted)
		if key != "major" {
			conduct.Add(conduct, weighted)
		}
	}
	return parts, round2(row.TotalScore), collegeRound(conduct)
}

// collegeTier 把方案里的档位名换成附件用的中文序数。超出已知序数时原样交出班级
// 自己起的名字——与其编一个"伍等"，不如让人一眼看出这里超纲了。
func collegeTier(name string, order []string) string {
	if name == "" {
		return ""
	}
	for index, candidate := range order {
		if candidate == name && index < len(tierOrdinals) {
			return tierOrdinals[index]
		}
	}
	return name
}

// parseEnrollment 从教务班级名里取年级和专业。解析不出来就都留空——报表里空着
// 一格，好过写一个猜出来的专业名。
func parseEnrollment(name string) (grade, major string) {
	match := enrollmentPattern.FindStringSubmatch(strings.TrimSpace(name))
	if match == nil {
		return "", ""
	}
	grade = match[1]
	if len(grade) == 2 {
		grade = "20" + grade
	}
	return grade + "级", match[2]
}

// collegeRows 复现结算器的发奖次序：总分降序 → 专业素质分降序 → 学号升序。
// 附件2、附件3、附件4 都要求"按奖学金等级顺序"填，而等级正是沿这条次序切出来
// 的；按名次排会在同分处和实际发的档位对不上。
func collegeRows(data jobData) []collegeRow {
	rows := make([]collegeRow, 0, len(data.Rows))
	ranked := make([]settle.Ranked, 0, len(data.Rows))
	for _, row := range data.Rows {
		weighted, total, conduct := collegeWeighted(row, data.Config)
		rows = append(rows, collegeRow{
			snapshotRow: row, Weighted: weighted, Total: total, Conduct: conduct,
			Tier: collegeTier(row.AwardTier, data.AwardOrder),
		})
		// 操行素质排名和四个大项的排名走同一个函数，附件5 要的就是这两组。
		scores := make(map[string]float64, len(row.CategoryScores)+1)
		maps.Copy(scores, row.CategoryScores)
		scores["conduct"] = conduct
		ranked = append(ranked, settle.Ranked{UserID: row.UserID, Scores: scores})
	}
	ranks := settle.CategoryRanks(ranked)
	for index := range rows {
		rows[index].Ranks = ranks[rows[index].UserID]
	}
	sortCollegeRows(rows)
	return rows
}

func sortCollegeRows(rows []collegeRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if left.TotalScore != right.TotalScore {
			return left.TotalScore > right.TotalScore
		}
		if left.CategoryScores["major"] != right.CategoryScores["major"] {
			return left.CategoryScores["major"] > right.CategoryScores["major"]
		}
		return left.SID < right.SID
	})
}

func awarded(rows []collegeRow) []collegeRow {
	result := make([]collegeRow, 0, len(rows))
	for _, row := range rows {
		if row.Tier != "" {
			result = append(result, row)
		}
	}
	return result
}

func honored(rows []collegeRow) []collegeRow {
	result := make([]collegeRow, 0, len(rows))
	for _, row := range rows {
		if row.Honor {
			result = append(result, row)
		}
	}
	return result
}
