package aijob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/aiassist"
	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/store"
)

/*
归组断点的行为只有连着真库才说得清：RLS、ON CONFLICT、事务边界都在库里。

	跑法（指向本地开发栈的库即可）：

	    EASYGPA_TEST_DATABASE_URL='postgres://easygpa_app:easygpa-app-dev@127.0.0.1:35432/easygpa?sslmode=disable' \
	    go test ./internal/aijob/ -run Compose -v

	测试复用库里现成的班级、学生和方案——建这三样要牵出白名单、创建人等一串外键，
	对这个测试没有意义——只建自己的 ai_batch，跑完删掉。
*/
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("EASYGPA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("设置 EASYGPA_TEST_DATABASE_URL 才会跑连库的归组断点测试")
	}
	pools, err := store.Open(context.Background(), url, "")
	if err != nil {
		t.Fatalf("连库失败：%v", err)
	}
	t.Cleanup(pools.Close)
	return pools.App
}

// chunkFixture 借用库里现成的班级/学生/方案，建一个本次测试专用的批次。
func chunkFixture(t *testing.T, pool *pgxpool.Pool, observations int) (classID, batchID int64, cfg scheme.Config) {
	t.Helper()
	ctx := context.Background()
	cfg = scheme.Config{Categories: []scheme.Category{{Key: "moral", Name: "思想道德素质", MaxTotal: 100}}}
	snapshot, _ := json.Marshal(cfg)

	/* 班级得先给：RLS 靠 app_current_class_id() 过滤，没设租户上下文时 easygpa_app
	   连 class 表都是空的，没法先查后定。 */
	classID = 1
	if raw := os.Getenv("EASYGPA_TEST_CLASS_ID"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatalf("EASYGPA_TEST_CLASS_ID 不是数字：%v", err)
		}
		classID = parsed
	}

	var studentID, schemeID int64
	var schemeVersion int
	if err := store.InTenantTx(ctx, pool, classID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT u.id,s.id,s.version
			  FROM app_user u JOIN scheme s ON s.class_id=u.class_id
			 WHERE u.role='student' ORDER BY u.id LIMIT 1
		`).Scan(&studentID, &schemeID, &schemeVersion)
	}); err != nil {
		t.Skipf("班级 %d 里没有可用的学生与方案：%v", classID, err)
	}

	if err := store.InTenantTx(ctx, pool, classID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO ai_batch (class_id,student_id,scheme_id,scheme_version,scheme_snapshot,status,total_count,processed_count,
			                      vision_model,text_model,prompt_version)
			VALUES ($1,$2,$3,$4,$5,'processing',$6,$6,'v','t',$7) RETURNING id
		`, classID, studentID, schemeID, schemeVersion, snapshot, observations, aiassist.PromptVersion).Scan(&batchID)
	}); err != nil {
		t.Fatalf("建批次失败：%v", err)
	}
	t.Cleanup(func() {
		_ = store.InTenantTx(context.Background(), pool, classID, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `DELETE FROM ai_batch WHERE id=$1`, batchID)
			return err
		})
	})
	return classID, batchID, cfg
}

func chunkObservations(count int) []aiassist.Observation {
	observations := make([]aiassist.Observation, 0, count)
	for index := range count {
		observations = append(observations, aiassist.Observation{
			AssetID:      fmt.Sprint(index),
			MaterialType: "活动截图",
			RawText:      fmt.Sprintf("活动 %d 的原文", index),
			Fields:       aiassist.Fields{Title: fmt.Sprintf("活动 %d", index), Date: "2024-10-01"},
		})
	}
	return observations
}

// scriptedPipeline 按块序号决定这一块成功还是失败。
type scriptedPipeline struct {
	fail  map[int]error
	calls []int
	seen  int
}

func (p *scriptedPipeline) Perceive(context.Context, aiassist.Asset) (aiassist.Observation, error) {
	return aiassist.Observation{}, errors.New("not used")
}

func (p *scriptedPipeline) ComposeBatch(context.Context, []aiassist.Observation, scheme.Config, aiassist.ComposeProgress) (aiassist.BatchDraft, error) {
	return aiassist.BatchDraft{}, errors.New("not used")
}

func (p *scriptedPipeline) ComposeChunk(_ context.Context, chunk []aiassist.Observation, _ scheme.Config, progress aiassist.ComposeProgress) (aiassist.BatchDraft, error) {
	index := p.seen
	p.seen++
	p.calls = append(p.calls, index)
	if err, bad := p.fail[index]; bad {
		return aiassist.BatchDraft{}, err
	}
	if progress.OnThinking != nil {
		progress.OnThinking(fmt.Sprintf("正在想第 %d 块", index))
	}
	candidates := make([]aiassist.Candidate, 0, len(chunk))
	for _, observation := range chunk {
		candidates = append(candidates, aiassist.Candidate{
			ID: "candidate-1", Title: observation.Fields.Title, Note: "我参加了" + observation.Fields.Title,
			Assets: []aiassist.AssetRef{{AssetID: observation.AssetID}},
		})
	}
	return aiassist.BatchDraft{Candidates: candidates, Model: "t", Usage: llm.Usage{TotalTokens: 10}}, nil
}

func (p *scriptedPipeline) Verify(candidate aiassist.Candidate, _ scheme.Config, _ map[string]struct{}) aiassist.Candidate {
	return candidate
}

func newTestWorker(pool *pgxpool.Pool) *Worker {
	return &Worker{pool: pool, wait: func(context.Context, time.Duration) error { return nil }}
}

func chunkStatuses(t *testing.T, pool *pgxpool.Pool, classID, batchID int64) map[int]string {
	t.Helper()
	statuses := map[int]string{}
	if err := store.InTenantTx(context.Background(), pool, classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(),
			`SELECT seq,status FROM ai_compose_chunk WHERE batch_id=$1 ORDER BY seq`, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var seq int
			var status string
			if err := rows.Scan(&seq, &status); err != nil {
				return err
			}
			statuses[seq] = status
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return statuses
}

// 一块失败绝不能影响其余的块。这正是生产上丢掉整批的那个性质。
func TestComposeChunkFailureDoesNotLoseOtherChunks(t *testing.T) {
	pool := testPool(t)
	classID, batchID, cfg := chunkFixture(t, pool, 18)
	observations := chunkObservations(18)
	worker := newTestWorker(pool)

	pipeline := &scriptedPipeline{fail: map[int]error{1: errors.New("模型这一块没返回内容")}}
	outcome, err := worker.composeInChunks(context.Background(), classID, batchID, pipeline, observations, cfg)
	if err != nil {
		t.Fatalf("composeInChunks 不该整体失败：%v", err)
	}
	if outcome.failedChunks != 1 {
		t.Fatalf("failedChunks = %d", outcome.failedChunks)
	}
	if len(outcome.draft.Candidates) == 0 {
		t.Fatal("其余块的候选全丢了")
	}
	statuses := chunkStatuses(t, pool, classID, batchID)
	if statuses[1] != "failed" {
		t.Fatalf("第 1 块应当是 failed，得到 %q", statuses[1])
	}
	for seq, status := range statuses {
		if seq != 1 && status != "complete" {
			t.Fatalf("第 %d 块应当是 complete，得到 %q", seq, status)
		}
	}
}

// worker 被重新部署掐掉之后重新入队：已经归好的块不能重跑，也不能重新计费。
func TestComposeResumeSkipsCompletedChunks(t *testing.T) {
	pool := testPool(t)
	classID, batchID, cfg := chunkFixture(t, pool, 18)
	observations := chunkObservations(18)
	worker := newTestWorker(pool)

	first := &scriptedPipeline{fail: map[int]error{2: errors.New("这一轮挂了")}}
	if _, err := worker.composeInChunks(context.Background(), classID, batchID, first, observations, cfg); err != nil {
		t.Fatal(err)
	}
	total := len(first.calls)
	if total < 3 {
		t.Fatalf("第一轮只跑了 %d 块", total)
	}

	second := &scriptedPipeline{}
	outcome, err := worker.composeInChunks(context.Background(), classID, batchID, second, observations, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.calls) != 1 {
		t.Fatalf("第二轮应当只补 1 块，实际跑了 %d 块", len(second.calls))
	}
	if outcome.failedChunks != 0 {
		t.Fatalf("第二轮不该再有失败块：%+v", outcome)
	}
	for seq, status := range chunkStatuses(t, pool, classID, batchID) {
		if status != "complete" {
			t.Fatalf("第 %d 块仍是 %q", seq, status)
		}
	}
	// 补跑之后候选要凑齐，包括第一轮就归好、这一轮直接带走的那些。
	if len(outcome.draft.Candidates) != len(observations) {
		t.Fatalf("候选 %d 条，观察 %d 条", len(outcome.draft.Candidates), len(observations))
	}
	ids := map[string]struct{}{}
	for _, candidate := range outcome.draft.Candidates {
		if _, clash := ids[candidate.ID]; clash {
			t.Fatalf("候选编号撞车：%s", candidate.ID)
		}
		ids[candidate.ID] = struct{}{}
	}
}

// 点了「停止归组」之后，剩下的块不跑，已经归好的照常带走。
func TestComposeStopRequestKeepsFinishedChunks(t *testing.T) {
	pool := testPool(t)
	classID, batchID, cfg := chunkFixture(t, pool, 18)
	observations := chunkObservations(18)
	worker := newTestWorker(pool)

	if err := store.InTenantTx(context.Background(), pool, classID, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`UPDATE ai_batch SET compose_stop_requested=true WHERE id=$1`, batchID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	pipeline := &scriptedPipeline{}
	outcome, err := worker.composeInChunks(context.Background(), classID, batchID, pipeline, observations, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.stopped {
		t.Fatal("outcome 没有标记 stopped")
	}
	if len(pipeline.calls) != 0 {
		t.Fatalf("停止之后不该再调用模型，实际调了 %d 次", len(pipeline.calls))
	}
}
