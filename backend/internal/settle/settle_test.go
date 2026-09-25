package settle

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"easygpa/backend/internal/scheme"
)

func TestComputeIsByteStable(t *testing.T) {
	students := []StudentInput{
		{UserID: 2, SID: "20230002", Name: "乙", CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(80), "moral": scheme.NewPoints(90)}},
		{UserID: 1, SID: "20230001", Name: "甲", CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(80), "moral": scheme.NewPoints(90)}},
		{UserID: 3, SID: "20230003", Name: "丙", CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(70), "moral": scheme.NewPoints(80)}},
	}
	weights := map[string]float64{"major": 0.6, "moral": 0.4}
	honorRoll := scheme.DefaultHonorRoll()
	first, err := Compute(students, weights, honorRoll)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compute([]StudentInput{students[2], students[0], students[1]}, weights, honorRoll)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatalf("settlement differs:\n%s\n%s", a, b)
	}
	if first[0].SID != "20230001" || first[0].ClassRank != 1 || first[1].ClassRank != 1 {
		t.Fatalf("unexpected tie ranking: %#v", first)
	}
}

// 名额必须按学院口径算：每档独立四舍五入(总人数×占比)，再相加。
//
// 通知里写死了"一等 5%、二等 10%、三等 15%，结果四舍五入""总获奖人数为一、二、
// 三等相加，不采用总人数×30%的计算方式"。以前的实现是按累计百分比(5/15/30)各
// 向上取整一次，在 28–49 人区间里 22 种班额有 15 种和学院算得不一样，这四个人数
// 是差异最典型的几档。
func TestAwardQuotasMatchCollegeRule(t *testing.T) {
	for _, tt := range []struct {
		size   int
		first  int
		second int
		third  int
	}{
		{size: 30, first: 2, second: 3, third: 5},
		{size: 41, first: 2, second: 4, third: 6},
		{size: 44, first: 2, second: 4, third: 7},
		{size: 45, first: 2, second: 5, third: 7},
	} {
		snapshots, err := Compute(classOf(tt.size), map[string]float64{"major": 1}, scheme.DefaultHonorRoll())
		if err != nil {
			t.Fatal(err)
		}
		counts := make(map[string]int)
		for _, row := range snapshots {
			counts[row.AwardTier]++
		}
		first := counts["一等综合素质奖学金"]
		second := counts["二等综合素质奖学金"]
		third := counts["三等综合素质奖学金"]
		if first != tt.first || second != tt.second || third != tt.third {
			t.Fatalf("%d 人班名额 %d/%d/%d，学院口径是 %d/%d/%d", tt.size, first, second, third, tt.first, tt.second, tt.third)
		}
		// 档位必须发给排在前面的人，中间不能有空洞。
		for index, row := range snapshots {
			if index < first+second+third && row.AwardTier == "" {
				t.Fatalf("%d 人班第 %d 位在名额内却没有档位", tt.size, index+1)
			}
			if index >= first+second+third && row.AwardTier != "" {
				t.Fatalf("%d 人班第 %d 位超出名额却拿到 %q", tt.size, index+1, row.AwardTier)
			}
		}
	}

	// 一档都不设时没人进档，也不该崩。
	none, err := Compute(classOf(5), map[string]float64{"major": 1}, scheme.HonorRoll{TopPercent: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range none {
		if row.AwardTier != "" || row.Honor {
			t.Fatalf("空档位仍然发了奖：%#v", row)
		}
	}
}

// 三好看四个维度各自的班级名次，不看总分名次。
//
// 学院要求"专业素质、思想道德、实践创新、身体心理四个维度均位列班级前 30%"，
// 所以一个总分第一但某一维垫底的人不是三好——这正是旧实现（只比总分名次）会
// 判错的情形。
func TestHonorNeedsEveryDimension(t *testing.T) {
	weights := map[string]float64{"major": 0.6, "moral": 0.15, "practice": 0.15, "health": 0.1}
	rows := make([]StudentInput, 0, 10)
	for i := range 10 {
		score := float64(80 - i)
		health := score
		if i == 0 {
			// 三项拉满、身心垫底：总分第一，但三好不该给他。
			score, health = 100, 0
		}
		rows = append(rows, StudentInput{
			UserID: int64(i + 1),
			SID:    fmt.Sprintf("2023%04d", i+1),
			Name:   fmt.Sprintf("学生%d", i+1),
			CategoryScores: map[string]scheme.Points{
				"major": scheme.NewPoints(score), "moral": scheme.NewPoints(score),
				"practice": scheme.NewPoints(score), "health": scheme.NewPoints(health),
			},
		})
	}
	snapshots, err := Compute(rows, weights, scheme.DefaultHonorRoll())
	if err != nil {
		t.Fatal(err)
	}
	if snapshots[0].SID != "20230001" || snapshots[0].ClassRank != 1 {
		t.Fatalf("前提没了：第一名应是 20230001，实际 %q 第 %d 名", snapshots[0].SID, snapshots[0].ClassRank)
	}
	if snapshots[0].CategoryRanks["health"] != 10 {
		t.Fatalf("身心名次应是第 10，实际 %d", snapshots[0].CategoryRanks["health"])
	}
	if snapshots[0].Honor {
		t.Fatal("总分第一但身心垫底，不该判三好")
	}
	if !snapshots[1].Honor {
		t.Fatalf("四维都进前 30%% 的第二名应判三好：%#v", snapshots[1].CategoryRanks)
	}
}

// 总分同分时，专业素质分高的进上一档，另一人顺延；名次仍然并列。
func TestTieBreaksOnMajorNotSid(t *testing.T) {
	// 0.6×100+0.4×50 = 0.6×90+0.4×65 = 80，两人总分相同、专业分不同，
	// 而专业分高的那个学号更大——学号次序在这里必须让位。
	rows := []StudentInput{
		{UserID: 1, SID: "20230001", Name: "甲", CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(100), "moral": scheme.NewPoints(100)}},
		{UserID: 2, SID: "20230002", Name: "乙", CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(90), "moral": scheme.NewPoints(65)}},
		{UserID: 3, SID: "20230003", Name: "丙", CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(100), "moral": scheme.NewPoints(50)}},
		{UserID: 4, SID: "20230004", Name: "丁", CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(10), "moral": scheme.NewPoints(10)}},
	}
	honorRoll := scheme.HonorRoll{TopPercent: 50, Awards: []scheme.Award{{Name: "一等", TopPercent: 50}, {Name: "二等", TopPercent: 50}}}
	snapshots, err := Compute(rows, map[string]float64{"major": 0.6, "moral": 0.4}, honorRoll)
	if err != nil {
		t.Fatal(err)
	}
	bySID := make(map[string]Snapshot, len(snapshots))
	for _, row := range snapshots {
		bySID[row.SID] = row
	}
	if bySID["20230003"].AwardTier != "一等" {
		t.Fatalf("专业分高的 20230003 应进一等，实际 %q", bySID["20230003"].AwardTier)
	}
	if bySID["20230002"].AwardTier != "二等" {
		t.Fatalf("同分顺延的 20230002 应进二等，实际 %q", bySID["20230002"].AwardTier)
	}
	if bySID["20230002"].ClassRank != 2 || bySID["20230003"].ClassRank != 2 {
		t.Fatalf("同分两人的名次应并列第 2，实际 %d 与 %d", bySID["20230002"].ClassRank, bySID["20230003"].ClassRank)
	}
}

func BenchmarkComputeClassSize200(b *testing.B) {
	students := classOf(200)
	weights := map[string]float64{"major": 0.6, "moral": 0.15, "practice": 0.15, "health": 0.1}
	honor := scheme.DefaultHonorRoll()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Compute(students, weights, honor); err != nil {
			b.Fatal(err)
		}
	}
}

// classOf 造一个分数互不相同、名次稳定的班，便于按名次断言。
func classOf(size int) []StudentInput {
	rows := make([]StudentInput, 0, size)
	for i := range size {
		rows = append(rows, StudentInput{
			UserID:         int64(i + 1),
			SID:            fmt.Sprintf("2023%04d", i+1),
			Name:           fmt.Sprintf("学生%d", i+1),
			CategoryScores: map[string]scheme.Points{"major": scheme.NewPoints(float64(size - i))},
		})
	}
	return rows
}

func TestEvaluateGate(t *testing.T) {
	now := time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)
	ready := GateInput{StudentCount: 32, SealedCount: 32, WindowClose: now.Add(24 * time.Hour), Now: now, GPAImportedCount: 32, BlindAuditComplete: true, ConfirmedCount: 32}
	gate := EvaluateGate(ready)
	if !gate.Open {
		t.Fatalf("gate should be open: %#v", gate)
	}
	blocked := EvaluateGate(GateInput{StudentCount: 32, SealedCount: 31, WindowClose: now.Add(24 * time.Hour), Now: now, PendingConflicts: 1, GPAImportedCount: 32, BlindAuditComplete: true})
	if blocked.Open {
		t.Fatalf("gate should be closed: %#v", blocked)
	}
	// PendingConflicts 由 API 把未结申诉、仲裁中提交和 submitted 异议加总后传入。
	objectionPending := EvaluateGate(GateInput{StudentCount: 32, SealedCount: 32, WindowClose: now.Add(24 * time.Hour), Now: now, PendingConflicts: 1, GPAImportedCount: 32, BlindAuditComplete: true})
	if objectionPending.Open {
		t.Fatalf("submitted objections must keep the gate closed: %#v", objectionPending)
	}
	forced := blocked
	forced = EvaluateGate(GateInput{StudentCount: 32, SealedCount: 31, WindowClose: now.Add(24 * time.Hour), Now: now, PendingConflicts: 1, GPAImportedCount: 0, Forced: true})
	if !forced.Open || !forced.Forced {
		t.Fatalf("forced gate should be open: %#v", forced)
	}
	for _, tt := range []struct {
		name string
		key  string
		set  func(*GateInput)
	}{
		{name: "unfinished item review", key: "allReviewsFinal", set: func(in *GateInput) { in.UnfinalizedReviews = 1 }},
		{name: "pending classification", key: "classificationResolved", set: func(in *GateInput) { in.PendingClassifications = 1 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := ready
			tt.set(&input)
			result := EvaluateGate(input)
			if result.Open {
				t.Fatalf("gate should be blocked: %#v", result)
			}
			found := false
			for _, condition := range result.Conditions {
				if condition.Key == tt.key {
					found = true
					if condition.OK {
						t.Fatalf("condition %s should be false", tt.key)
					}
				}
			}
			if !found {
				t.Fatalf("condition %s missing", tt.key)
			}
		})
	}
}
