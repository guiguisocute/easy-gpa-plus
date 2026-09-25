package aiassist

import (
	"fmt"
	"strings"
	"testing"
)

func observation(assetID, title, date string) Observation {
	return Observation{
		AssetID: assetID, MaterialType: "活动截图",
		RawText: "活动详情 " + title,
		Fields:  Fields{Title: title, Date: date},
	}
}

func chunkAssetIDs(chunk []Observation) []string {
	ids := make([]string, 0, len(chunk))
	for _, item := range chunk {
		ids = append(ids, item.AssetID)
	}
	return ids
}

// 同一场活动被切到两块里，两块各自不知道对方存在，于是出两条本该合并成一条的
// 候选，学生还得自己去合。分组是为了让这件事不可能发生。
func TestPlanChunksKeepsOneActivityInOneChunk(t *testing.T) {
	observations := []Observation{
		observation("1", "校运会", "2024-10-01"),
		observation("2", "读书节", "2024-11-02"),
		observation("3", "校运会", "2024-10-01"),
		observation("4", "志愿服务", "2024-12-03"),
		observation("5", "校运会", "2024-10-01"),
		observation("6", "辩论赛", "2024-09-09"),
		observation("7", "科技节", "2024-08-08"),
		observation("8", "校运会", "2024-10-01"),
	}
	chunks := PlanChunks(observations)
	if len(chunks) < 2 {
		t.Fatalf("8 条观察应当不止一块，得到 %d 块", len(chunks))
	}
	seen := -1
	for index, chunk := range chunks {
		for _, item := range chunk {
			if item.Fields.Title != "校运会" {
				continue
			}
			if seen >= 0 && seen != index {
				t.Fatalf("校运会被拆到了第 %d 块和第 %d 块：%v", seen, index, chunks)
			}
			seen = index
		}
	}
	if seen < 0 {
		t.Fatal("校运会不见了")
	}
}

// 标题在不同截图里差一个空格是常事，不能因此当成两场活动。
func TestPlanChunksIgnoresWhitespaceAndCaseInTitles(t *testing.T) {
	chunks := PlanChunks([]Observation{
		observation("1", "Coding Marathon 2024", "2024-10-01"),
		observation("2", "codingmarathon2024", "2024-10-01"),
		observation("3", "Coding　Marathon　2024", "2024-10-01"),
	})
	if len(chunks) != 1 {
		t.Fatalf("同一场活动应当归成一块，得到 %d 块", len(chunks))
	}
}

// 日期不同就是两场活动，不能只看标题合并——「例会」这种名字每周都有一次。
// 各 4 条、上限 6 条，只有当成两个组才会被分到两块；当成一个组就会是一块 8 条。
func TestPlanChunksSeparatesSameTitleOnDifferentDates(t *testing.T) {
	observations := make([]Observation, 0, 8)
	for index := range 4 {
		observations = append(observations, observation(fmt.Sprintf("a%d", index), "例会", "2024-10-01"))
	}
	for index := range 4 {
		observations = append(observations, observation(fmt.Sprintf("b%d", index), "例会", "2024-10-08"))
	}
	chunks := PlanChunks(observations)
	if len(chunks) != 2 {
		t.Fatalf("两个日期应当是两个组、切成两块，得到 %d 块", len(chunks))
	}
	for index, chunk := range chunks {
		dates := map[string]struct{}{}
		for _, item := range chunk {
			dates[item.Fields.Date] = struct{}{}
		}
		if len(dates) != 1 {
			t.Fatalf("第 %d 块混进了 %d 个日期", index, len(dates))
		}
	}
}

// 一场活动的材料多到超过上限时，宁可这一块慢一点，也不能把它拆开。
func TestPlanChunksKeepsAnOversizedActivityWhole(t *testing.T) {
	observations := make([]Observation, 0, maxChunkObservations+3)
	for index := range maxChunkObservations + 3 {
		observations = append(observations, observation(fmt.Sprint(index), "百团大战", "2024-10-01"))
	}
	chunks := PlanChunks(observations)
	if len(chunks) != 1 {
		t.Fatalf("超限的同一场活动仍应是一块，得到 %d 块：%v", len(chunks), chunks)
	}
	if len(chunks[0]) != maxChunkObservations+3 {
		t.Fatalf("这一块少了材料：%v", chunkAssetIDs(chunks[0]))
	}
}

func TestPlanChunksRespectsTheObservationLimit(t *testing.T) {
	observations := make([]Observation, 0, 20)
	for index := range 20 {
		observations = append(observations, observation(fmt.Sprint(index), fmt.Sprintf("活动 %d", index), "2024-10-01"))
	}
	chunks := PlanChunks(observations)
	if len(chunks) < 4 {
		t.Fatalf("20 场不同活动应当切成至少 4 块，得到 %d", len(chunks))
	}
	for index, chunk := range chunks {
		if len(chunk) > maxChunkObservations {
			t.Fatalf("第 %d 块有 %d 条，超过上限 %d", index, len(chunk), maxChunkObservations)
		}
	}
}

// 单条观察很大时按字节数提前收口，不能光看条数。生产上的观察单条约 440 字，
// 但汇总表那类材料的 rawText 会长得多。
func TestPlanChunksRespectsTheByteBudget(t *testing.T) {
	bulky := strings.Repeat("汇总表原文", 200)
	observations := make([]Observation, 0, 4)
	for index := range 4 {
		item := observation(fmt.Sprint(index), fmt.Sprintf("活动 %d", index), "2024-10-01")
		item.RawText = bulky
		observations = append(observations, item)
	}
	chunks := PlanChunks(observations)
	if len(chunks) < 2 {
		t.Fatalf("大观察应当按字节数提前切块，得到 %d 块", len(chunks))
	}
}

// 每一条观察都必须原样出现在某一块里，一条不能少、不能重。丢材料是这个功能
// 最不能犯的错。
func TestPlanChunksLosesNothing(t *testing.T) {
	observations := make24()
	chunks := PlanChunks(observations)
	seen := make(map[string]int, len(observations))
	for _, chunk := range chunks {
		for _, item := range chunk {
			seen[item.AssetID]++
		}
	}
	if len(seen) != len(observations) {
		t.Fatalf("装箱后只剩 %d 条，原本 %d 条", len(seen), len(observations))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("观察 %s 出现了 %d 次", id, count)
		}
	}
}

func TestPlanChunksOnEmptyInput(t *testing.T) {
	if chunks := PlanChunks(nil); chunks != nil {
		t.Fatalf("空输入应当没有块，得到 %v", chunks)
	}
}

// 取不到标题时各归各的：宁可不合并，也不要把无关材料粘在一起。
func TestPlanChunksDoesNotGroupUntitledObservations(t *testing.T) {
	chunks := PlanChunks([]Observation{
		observation("1", "", ""),
		observation("2", "", ""),
		observation("3", "", ""),
		observation("4", "", ""),
		observation("5", "", ""),
		observation("6", "", ""),
		observation("7", "", ""),
	})
	if len(chunks) < 2 {
		t.Fatalf("7 条无标题观察应当按条数切开，得到 %d 块", len(chunks))
	}
}

// 候选行的形状要和流式预览一致，否则已经归好的块和正在写的那块拼不成一列。
func TestCandidateLinesPairTitleWithNote(t *testing.T) {
	lines := CandidateLines([]Candidate{
		{Title: "校运会 100 米", Note: "我参加了校运会。"},
		{Title: "读书节", Note: "   "},
	})
	if len(lines) != 2 {
		t.Fatalf("得到 %d 行", len(lines))
	}
	if lines[0] != "校运会 100 米\n我参加了校运会。" {
		t.Fatalf("第一行拼错了：%q", lines[0])
	}
	if lines[1] != "读书节" {
		t.Fatalf("空说明不该留下换行：%q", lines[1])
	}
}

// 每块都从 candidate-1 开始编号，合并时不重排就会撞车。
func TestMergeDraftRenumbersCandidates(t *testing.T) {
	merged := BatchDraft{}
	MergeDraft(&merged, BatchDraft{Candidates: []Candidate{{ID: "candidate-1"}, {ID: "candidate-2"}}, Model: "m"})
	MergeDraft(&merged, BatchDraft{Candidates: []Candidate{{ID: "candidate-1"}}, Model: "other"})
	ids := make([]string, 0, len(merged.Candidates))
	for _, candidate := range merged.Candidates {
		ids = append(ids, candidate.ID)
	}
	want := []string{"candidate-1", "candidate-2", "candidate-3"}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("编号是 %v，应当是 %v", ids, want)
		}
	}
	if merged.Model != "m" {
		t.Fatalf("第一块的模型名应当保留，得到 %q", merged.Model)
	}
}

func make24() []Observation {
	observations := make([]Observation, 0, 24)
	for index := range 24 {
		observations = append(observations, observation(
			fmt.Sprint(index), fmt.Sprintf("活动 %d", index/2), "2024-10-01"))
	}
	return observations
}
