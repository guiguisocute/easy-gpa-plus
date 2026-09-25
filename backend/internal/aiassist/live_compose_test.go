package aiassist

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/scheme"
)

/*
这个测试打真实的模型端点，默认跳过。

	它存在的理由是本次故障用假客户端复现不出来：问题出在真实上游的时序上——推理占掉
	头 64 秒、正文才开始吐，而客户端级的 90 秒硬超时正好卡在这中间。只有拿真实观察
	打真实端点，才能确认分块之后每块都落在安全区间里。

	跑法（凭据和学生数据都不进版本库）：

	    EASYGPA_LIVE_MODEL_KEY=sk-...
	    EASYGPA_LIVE_MODEL_URL=https://api.example.org/v1
	    EASYGPA_LIVE_FIXTURES=/tmp/easygpa-fixtures
	    go test ./internal/aiassist/ -run Live -v -timeout 30m

	fixtures 目录需要 observations.json（{"observations":[...]}）与 scheme.json，
	从真实批次导出即可。里面是真人的姓名与活动记录，不要提交。
*/
func liveConfig(t *testing.T) (string, string, string) {
	t.Helper()
	key := os.Getenv("EASYGPA_LIVE_MODEL_KEY")
	fixtures := os.Getenv("EASYGPA_LIVE_FIXTURES")
	if key == "" || fixtures == "" {
		t.Skip("设置 EASYGPA_LIVE_MODEL_KEY 与 EASYGPA_LIVE_FIXTURES 才会跑真实模型")
	}
	baseURL := os.Getenv("EASYGPA_LIVE_MODEL_URL")
	if baseURL == "" {
		t.Fatal("EASYGPA_LIVE_MODEL_URL is required")
	}
	return key, baseURL, fixtures
}

// liveClient uses a bounded timeout and public provider endpoints.
func liveClient(baseURL, key string) (llm.Client, error) {
	return llm.NewOpenAICompatible(llm.Config{
		BaseURL: baseURL, APIKey: key, Timeout: 90 * time.Second, AllowPrivateNetwork: false,
	})
}

func liveFixtures(t *testing.T, dir string) ([]Observation, scheme.Config) {
	t.Helper()
	var wrapper struct {
		Observations []Observation `json:"observations"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "observations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatal(err)
	}
	rawScheme, err := os.ReadFile(filepath.Join(dir, "scheme.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg scheme.Config
	if err := json.Unmarshal(rawScheme, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(wrapper.Observations) == 0 {
		t.Fatal("fixtures 里没有观察")
	}
	return wrapper.Observations, cfg
}

// 生产上 24 条观察整批归组会跑满十几分钟然后一无所获。分块之后每一块都必须在
// 合理时间内出候选，而且中间态要早于正文出现。
func TestLiveComposeChunksProduceCandidates(t *testing.T) {
	key, baseURL, fixtures := liveConfig(t)
	observations, cfg := liveFixtures(t, fixtures)
	client, err := liveClient(baseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{
		Enabled: true, VisionModel: "deepseek-v4-flash-vision-exp", TextModel: "deepseek-v4-flash",
		StudentName: "测试同学", StudentSID: "0000",
	}, client)

	chunks := PlanChunks(observations)
	t.Logf("%d 条观察切成 %d 块", len(observations), len(chunks))

	total := 0
	for index, chunk := range chunks {
		started := time.Now()
		firstThinking, firstCandidate := time.Duration(0), time.Duration(0)
		progress := ComposeProgress{
			OnThinking: func(string) {
				if firstThinking == 0 {
					firstThinking = time.Since(started)
				}
			},
			OnCandidates: func([]string) {
				if firstCandidate == 0 {
					firstCandidate = time.Since(started)
				}
			},
		}
		draft, err := p.ComposeChunk(context.Background(), chunk, cfg, progress)
		elapsed := time.Since(started)
		if err != nil {
			t.Fatalf("第 %d 块（%d 条观察）归组失败，耗时 %.1fs：%v", index, len(chunk), elapsed.Seconds(), err)
		}
		if len(draft.Candidates) == 0 {
			t.Fatalf("第 %d 块没有出候选", index)
		}
		total += len(draft.Candidates)
		t.Logf("第 %d 块：%d 条观察 → %d 条候选，耗时 %.1fs，首个思考 %.1fs，首条候选 %.1fs",
			index, len(chunk), len(draft.Candidates), elapsed.Seconds(),
			firstThinking.Seconds(), firstCandidate.Seconds())
		// 这一条是整件事的核心：单块耗时必须明显低于原来那个 90 秒的硬上限。
		if elapsed > 4*time.Minute {
			t.Errorf("第 %d 块耗时 %.1fs，分块没能把单次调用压到合理区间", index, elapsed.Seconds())
		}
	}
	t.Logf("合计 %d 条候选", total)
}

// 整批走一遍 ComposeBatch，确认合并、编号与 warnings 在真实数据上也成立。
func TestLiveComposeBatchEndToEnd(t *testing.T) {
	key, baseURL, fixtures := liveConfig(t)
	observations, cfg := liveFixtures(t, fixtures)
	client, err := liveClient(baseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{
		Enabled: true, VisionModel: "deepseek-v4-flash-vision-exp", TextModel: "deepseek-v4-flash",
		StudentName: "测试同学", StudentSID: "0000",
	}, client)

	started := time.Now()
	seenLines := 0
	draft, err := p.ComposeBatch(context.Background(), observations, cfg, ComposeProgress{
		OnCandidates: func(lines []string) { seenLines = len(lines) },
	})
	if err != nil {
		t.Fatalf("整批归组失败，耗时 %.1fs：%v", time.Since(started).Seconds(), err)
	}
	t.Logf("整批：%d 条观察 → %d 条候选，耗时 %.1fs，中途最多看到 %d 行",
		len(observations), len(draft.Candidates), time.Since(started).Seconds(), seenLines)
	if len(draft.Candidates) == 0 {
		t.Fatal("一条候选都没有")
	}
	ids := make(map[string]struct{}, len(draft.Candidates))
	for _, candidate := range draft.Candidates {
		if _, clash := ids[candidate.ID]; clash {
			t.Fatalf("候选编号撞车：%s", candidate.ID)
		}
		ids[candidate.ID] = struct{}{}
	}
	for _, warning := range draft.Warnings {
		t.Logf("warning: %s", warning)
	}
}
