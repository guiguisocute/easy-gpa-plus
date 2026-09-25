// Package aievaluation provides an offline-only model evaluation harness. It
// never opens the business database and never sends local paths or filenames to
// a model; every run uses fresh opaque IDs and stores its sensitive report in a
// caller-controlled, Git-ignored directory.
package aievaluation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"easygpa/backend/internal/aiassist"
	"easygpa/backend/internal/config"
	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/scheme"
)

const (
	defaultSampleSize     = 50
	minimumStudentBatches = 15
)

type labelFile struct {
	Items []labelItem `json:"items"`
}

type labelItem struct {
	SHA256      string      `json:"sha256"`
	CategoryKey string      `json:"categoryKey"`
	ItemKey     string      `json:"itemKey"`
	GroupID     string      `json:"groupId"`
	Fields      labelFields `json:"fields"`
}

type labelFields struct {
	Date     string   `json:"date"`
	Issuer   string   `json:"issuer"`
	Level    string   `json:"level"`
	Quantity *float64 `json:"quantity"`
}

type localAsset struct {
	Path      string
	SHA256    string
	MediaType string
	BatchKey  string
	AssetID   string
	BatchID   string
	SizeBytes int64
}

type reportAsset struct {
	AssetID   string `json:"assetId"`
	BatchID   string `json:"batchId"`
	SHA256    string `json:"sha256"`
	LocalPath string `json:"localPath"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
}

type perceptionResult struct {
	AssetID     string                `json:"assetId"`
	SHA256      string                `json:"sha256"`
	Observation *aiassist.Observation `json:"observation,omitempty"`
	Error       string                `json:"error,omitempty"`
}

type batchResult struct {
	BatchID      string               `json:"batchId"`
	AssetIDs     []string             `json:"assetIds"`
	Perceptions  []perceptionResult   `json:"perceptions"`
	Draft        *aiassist.BatchDraft `json:"draft,omitempty"`
	ComposeError string               `json:"composeError,omitempty"`
}

type callRecord struct {
	Model       string          `json:"model"`
	HasImage    bool            `json:"hasImage"`
	RequestHash string          `json:"requestHash"`
	Attempt     int             `json:"attempt"`
	DurationMS  int64           `json:"durationMs"`
	Usage       llm.Usage       `json:"usage"`
	Response    json.RawMessage `json:"response,omitempty"`
	Error       string          `json:"error,omitempty"`
}

type metricRate struct {
	Correct int     `json:"correct"`
	Total   int     `json:"total"`
	Rate    float64 `json:"rate"`
}

type fieldMetrics struct {
	Date     metricRate `json:"date"`
	Issuer   metricRate `json:"issuer"`
	Level    metricRate `json:"level"`
	Quantity metricRate `json:"quantity"`
	Overall  metricRate `json:"overall"`
}

type metrics struct {
	JSONSuccess                metricRate   `json:"jsonSuccess"`
	Classification             metricRate   `json:"classification"`
	Grouping                   metricRate   `json:"grouping"`
	Fields                     fieldMetrics `json:"fields"`
	SilentHighConfidenceErrors int          `json:"silentHighConfidenceErrors"`
	Calls                      int          `json:"calls"`
	Retries                    int          `json:"retries"`
	InputTokens                int64        `json:"inputTokens"`
	OutputTokens               int64        `json:"outputTokens"`
	TotalTokens                int64        `json:"totalTokens"`
	AverageLatencyMS           float64      `json:"averageLatencyMs"`
	FailureReasons             []string     `json:"failureReasons"`
}

type gate struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons"`
}

type Report struct {
	GeneratedAt    time.Time     `json:"generatedAt"`
	PromptVersion  string        `json:"promptVersion"`
	VisionModel    string        `json:"visionModel"`
	TextModel      string        `json:"textModel"`
	SampleTarget   int           `json:"sampleTarget"`
	Sampled        int           `json:"sampled"`
	StudentBatches int           `json:"studentBatches"`
	DuplicateFiles int           `json:"duplicateFiles"`
	Assets         []reportAsset `json:"assets"`
	Batches        []batchResult `json:"batches"`
	Calls          []callRecord  `json:"calls"`
	Metrics        metrics       `json:"metrics"`
	Gate           gate          `json:"gate"`
}

type recordingClient struct {
	inner   llm.Client
	mu      sync.Mutex
	records []callRecord
}

func (c *recordingClient) Call(ctx context.Context, request llm.Request) (llm.Response, error) {
	hash := llm.RequestHash(request)
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		started := time.Now()
		response, err := c.inner.Call(ctx, request)
		record := callRecord{
			Model: request.Model, HasImage: request.Image != nil, RequestHash: hash, Attempt: attempt,
			DurationMS: time.Since(started).Milliseconds(), Usage: response.Usage, Response: response.Raw,
		}
		if err != nil {
			record.Error = publicModelError(err)
		}
		c.mu.Lock()
		c.records = append(c.records, record)
		c.mu.Unlock()
		if err == nil {
			return response, nil
		}
		last = err
		if !retryable(err) || attempt == 3 {
			break
		}
		timer := time.NewTimer(time.Duration(1<<(attempt-1)) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return llm.Response{}, ctx.Err()
		case <-timer.C:
		}
	}
	return llm.Response{}, last
}

func (c *recordingClient) snapshot() []callRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]callRecord(nil), c.records...)
}

func Run(ctx context.Context, cfg *config.Config, args []string) error {
	flags := flag.NewFlagSet("eval:ai", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	inputDir := flags.String("input", "", "历史材料目录")
	schemePath := flags.String("scheme", "", "方案 JSON 文件")
	labelsPath := flags.String("labels", "", "人工标注 JSON 文件")
	outputPath := flags.String("output", "", "评测报告路径（默认 .ai-eval）")
	sampleSize := flags.Int("sample", defaultSampleSize, "去重后的抽样图片数")
	allowRaw := flags.Bool("allow-raw-third-party", false, "确认有权把原始材料传给第三方模型")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*allowRaw {
		return errors.New("拒绝传输原始学生材料：请确认已获授权后显式添加 --allow-raw-third-party")
	}
	if strings.TrimSpace(*inputDir) == "" || strings.TrimSpace(*schemePath) == "" || strings.TrimSpace(*labelsPath) == "" {
		return errors.New("--input、--scheme 和 --labels 都是必填参数")
	}
	if *sampleSize < 1 || *sampleSize > 200 {
		return errors.New("--sample 必须在 1—200 之间")
	}
	if strings.TrimSpace(cfg.LLMAPIKey) == "" {
		return errors.New("评测需要后端环境变量 LLM_API_KEY")
	}

	schemeConfig, err := readScheme(*schemePath)
	if err != nil {
		return err
	}
	labels, err := readLabels(*labelsPath)
	if err != nil {
		return err
	}
	allAssets, duplicates, err := inventory(*inputDir)
	if err != nil {
		return err
	}
	selected := sampleByBatch(allAssets, *sampleSize)
	if len(selected) == 0 {
		return errors.New("历史材料目录中没有 JPEG、PNG 或 WebP 图片")
	}
	assignOpaqueIDs(selected)

	transport, err := llm.NewOpenAICompatible(llm.Config{BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey, Timeout: cfg.LLMTimeout, MaxRetries: 0, AllowPrivateNetwork: cfg.AllowPrivateAINetwork()})
	if err != nil {
		return err
	}
	recorder := &recordingClient{inner: transport}
	pipeline := aiassist.New(aiassist.Config{Enabled: true, VisionModel: cfg.LLMVisionModel, TextModel: cfg.LLMTextModel}, recorder)
	grouped := groupSelected(selected)
	if *sampleSize >= minimumStudentBatches && len(grouped) < minimumStudentBatches {
		return fmt.Errorf("抽样只覆盖 %d 个学生目录；默认评测至少需要 %d 个，请确认 --input 指向按学生分目录的历史材料", len(grouped), minimumStudentBatches)
	}
	batchKeys := make([]string, 0, len(grouped))
	for key := range grouped {
		batchKeys = append(batchKeys, key)
	}
	sort.Strings(batchKeys)

	report := Report{
		GeneratedAt: time.Now().UTC(), PromptVersion: aiassist.PromptVersion,
		VisionModel: cfg.LLMVisionModel, TextModel: cfg.LLMTextModel,
		SampleTarget: *sampleSize, Sampled: len(selected), StudentBatches: len(grouped), DuplicateFiles: duplicates,
		Assets: make([]reportAsset, 0, len(selected)), Batches: make([]batchResult, 0, len(grouped)),
	}
	for _, asset := range selected {
		report.Assets = append(report.Assets, reportAsset{
			AssetID: asset.AssetID, BatchID: asset.BatchID, SHA256: asset.SHA256,
			LocalPath: asset.Path, MediaType: asset.MediaType, SizeBytes: asset.SizeBytes,
		})
	}
	for _, key := range batchKeys {
		assets := grouped[key]
		batch := evaluateBatch(ctx, pipeline, assets, schemeConfig)
		report.Batches = append(report.Batches, batch)
	}
	report.Calls = recorder.snapshot()
	report.Metrics = calculateMetrics(report.Batches, selected, labels, report.Calls)
	report.Gate = evaluateGate(report.Metrics)

	if strings.TrimSpace(*outputPath) == "" {
		*outputPath = filepath.Join(".ai-eval", "report-"+time.Now().UTC().Format("20060102T150405Z")+".json")
	}
	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outputPath, raw, 0o600); err != nil {
		return err
	}
	fmt.Printf("评测完成：%d 张图片，%d 个学生批次\n报告：%s\n分类准确率：%.1f%%，关键字段准确率：%.1f%%，上线门槛：%v\n",
		report.Sampled, report.StudentBatches, *outputPath, report.Metrics.Classification.Rate*100,
		report.Metrics.Fields.Overall.Rate*100, report.Gate.Passed)
	return nil
}

func evaluateBatch(ctx context.Context, pipeline aiassist.Pipeline, assets []*localAsset, cfg scheme.Config) batchResult {
	result := batchResult{BatchID: assets[0].BatchID, AssetIDs: make([]string, len(assets)), Perceptions: make([]perceptionResult, len(assets))}
	observations := make([]aiassist.Observation, len(assets))
	valid := make([]bool, len(assets))
	sem := make(chan struct{}, 2)
	var group sync.WaitGroup
	for index, asset := range assets {
		index, asset := index, asset
		result.AssetIDs[index] = asset.AssetID
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				result.Perceptions[index] = perceptionResult{AssetID: asset.AssetID, SHA256: asset.SHA256, Error: "评测已取消"}
				return
			}
			data, err := os.ReadFile(asset.Path)
			if err != nil {
				result.Perceptions[index] = perceptionResult{AssetID: asset.AssetID, SHA256: asset.SHA256, Error: "本地图片读取失败"}
				return
			}
			observation, err := pipeline.Perceive(ctx, aiassist.Asset{ID: asset.AssetID, MediaType: asset.MediaType, Data: data})
			if err != nil {
				result.Perceptions[index] = perceptionResult{AssetID: asset.AssetID, SHA256: asset.SHA256, Error: publicModelError(err)}
				return
			}
			observations[index] = observation
			valid[index] = true
			copy := observation
			result.Perceptions[index] = perceptionResult{AssetID: asset.AssetID, SHA256: asset.SHA256, Observation: &copy}
		}()
	}
	group.Wait()
	ready := make([]aiassist.Observation, 0, len(assets))
	for index := range observations {
		if valid[index] {
			ready = append(ready, observations[index])
		}
	}
	if len(ready) == 0 {
		result.ComposeError = "没有成功的识图结果"
		return result
	}
	draft, err := pipeline.ComposeBatch(ctx, ready, cfg, aiassist.ComposeProgress{})
	if err != nil {
		result.ComposeError = publicModelError(err)
		return result
	}
	result.Draft = &draft
	return result
}

func inventory(root string) ([]*localAsset, int, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]struct{})
	assets := make([]*localAsset, 0)
	duplicates := 0
	err = filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		mediaType := imageMediaType(filename)
		if mediaType == "" {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if _, duplicate := seen[hash]; duplicate {
			duplicates++
			return nil
		}
		seen[hash] = struct{}{}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		components := strings.Split(filepath.ToSlash(relative), "/")
		batchKey := "."
		if len(components) > 1 {
			batchKey = components[0]
		}
		assets = append(assets, &localAsset{Path: filename, SHA256: hash, MediaType: mediaType, BatchKey: batchKey, SizeBytes: int64(len(data))})
		return nil
	})
	return assets, duplicates, err
}

func sampleByBatch(assets []*localAsset, target int) []*localAsset {
	groups := make(map[string][]*localAsset)
	for _, asset := range assets {
		groups[asset.BatchKey] = append(groups[asset.BatchKey], asset)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
		sort.Slice(groups[key], func(i, j int) bool { return groups[key][i].SHA256 < groups[key][j].SHA256 })
	}
	sort.Slice(keys, func(i, j int) bool {
		left := sha256.Sum256([]byte(keys[i]))
		right := sha256.Sum256([]byte(keys[j]))
		return string(left[:]) < string(right[:])
	})
	if target > len(assets) {
		target = len(assets)
	}
	result := make([]*localAsset, 0, target)
	for round := 0; len(result) < target; round++ {
		added := false
		for _, key := range keys {
			if round < len(groups[key]) {
				result = append(result, groups[key][round])
				added = true
				if len(result) == target {
					break
				}
			}
		}
		if !added {
			break
		}
	}
	return result
}

func assignOpaqueIDs(assets []*localAsset) {
	batchIDs := make(map[string]string)
	for _, asset := range assets {
		if batchIDs[asset.BatchKey] == "" {
			batchIDs[asset.BatchKey] = opaqueID("batch")
		}
		asset.BatchID = batchIDs[asset.BatchKey]
		asset.AssetID = opaqueID("asset")
	}
}

func groupSelected(assets []*localAsset) map[string][]*localAsset {
	result := make(map[string][]*localAsset)
	for _, asset := range assets {
		result[asset.BatchKey] = append(result[asset.BatchKey], asset)
	}
	return result
}

func readScheme(filename string) (scheme.Config, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return scheme.Config{}, fmt.Errorf("读取方案: %w", err)
	}
	var cfg scheme.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return scheme.Config{}, fmt.Errorf("方案必须是合法 JSON: %w", err)
	}
	if err := scheme.Validate(cfg); err != nil {
		return scheme.Config{}, fmt.Errorf("方案校验失败: %w", err)
	}
	return cfg, nil
}

func readLabels(filename string) (map[string]labelItem, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取标注: %w", err)
	}
	var file labelFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("标注必须是合法 JSON: %w", err)
	}
	result := make(map[string]labelItem, len(file.Items))
	for _, item := range file.Items {
		item.SHA256 = strings.ToLower(strings.TrimSpace(item.SHA256))
		if len(item.SHA256) != 64 {
			return nil, errors.New("每条标注都必须使用图片的 64 位 sha256")
		}
		if _, duplicate := result[item.SHA256]; duplicate {
			return nil, fmt.Errorf("标注 sha256 重复: %s", item.SHA256)
		}
		result[item.SHA256] = item
	}
	return result, nil
}

func calculateMetrics(batches []batchResult, assets []*localAsset, labels map[string]labelItem, calls []callRecord) metrics {
	var result metrics
	assetByID := make(map[string]*localAsset, len(assets))
	for _, asset := range assets {
		assetByID[asset.AssetID] = asset
	}
	predictedItem := make(map[string][2]string)
	predictedGroup := make(map[string]string)
	for _, batch := range batches {
		for _, perception := range batch.Perceptions {
			result.JSONSuccess.Total++
			if perception.Observation != nil {
				result.JSONSuccess.Correct++
				asset := assetByID[perception.AssetID]
				if asset != nil {
					if label, ok := labels[asset.SHA256]; ok {
						compareFields(&result.Fields, perception.Observation.Fields, label.Fields)
					}
				}
			} else if perception.Error != "" {
				result.FailureReasons = appendUnique(result.FailureReasons, perception.Error)
			}
		}
		result.JSONSuccess.Total++
		if batch.Draft != nil {
			result.JSONSuccess.Correct++
			for _, candidate := range batch.Draft.Candidates {
				wrongHighConfidence := false
				for _, reference := range candidate.Assets {
					predictedItem[reference.AssetID] = [2]string{candidate.CategoryKey, candidate.ItemKey}
					predictedGroup[reference.AssetID] = batch.BatchID + "/" + candidate.ID
					asset := assetByID[reference.AssetID]
					if asset != nil {
						if label, ok := labels[asset.SHA256]; ok && label.ItemKey != "" && (candidate.CategoryKey != label.CategoryKey || candidate.ItemKey != label.ItemKey) {
							wrongHighConfidence = true
						}
					}
				}
				if wrongHighConfidence && candidate.Confidence >= .75 && !candidate.NeedsReview {
					result.SilentHighConfidenceErrors++
				}
			}
		} else if batch.ComposeError != "" {
			result.FailureReasons = appendUnique(result.FailureReasons, batch.ComposeError)
		}
	}
	for _, asset := range assets {
		label, ok := labels[asset.SHA256]
		if !ok || label.ItemKey == "" {
			continue
		}
		result.Classification.Total++
		predicted := predictedItem[asset.AssetID]
		if predicted[0] == label.CategoryKey && predicted[1] == label.ItemKey {
			result.Classification.Correct++
		}
	}
	groups := groupSelected(assets)
	for _, batchAssets := range groups {
		for i := 0; i < len(batchAssets); i++ {
			left, leftOK := labels[batchAssets[i].SHA256]
			if !leftOK || left.GroupID == "" {
				continue
			}
			for j := i + 1; j < len(batchAssets); j++ {
				right, rightOK := labels[batchAssets[j].SHA256]
				if !rightOK || right.GroupID == "" {
					continue
				}
				result.Grouping.Total++
				expectedSame := left.GroupID == right.GroupID
				predictedSame := predictedGroup[batchAssets[i].AssetID] != "" && predictedGroup[batchAssets[i].AssetID] == predictedGroup[batchAssets[j].AssetID]
				if expectedSame == predictedSame {
					result.Grouping.Correct++
				}
			}
		}
	}
	result.JSONSuccess.finish()
	result.Classification.finish()
	result.Grouping.finish()
	result.Fields.Date.finish()
	result.Fields.Issuer.finish()
	result.Fields.Level.finish()
	result.Fields.Quantity.finish()
	result.Fields.Overall.finish()
	var latency int64
	for _, call := range calls {
		result.Calls++
		if call.Attempt > 1 {
			result.Retries++
		}
		result.InputTokens += call.Usage.InputTokens
		result.OutputTokens += call.Usage.OutputTokens
		result.TotalTokens += call.Usage.TotalTokens
		latency += call.DurationMS
		if call.Error != "" {
			result.FailureReasons = appendUnique(result.FailureReasons, call.Error)
		}
	}
	if result.Calls > 0 {
		result.AverageLatencyMS = float64(latency) / float64(result.Calls)
	}
	return result
}

func compareFields(result *fieldMetrics, got aiassist.Fields, want labelFields) {
	if strings.TrimSpace(want.Date) != "" {
		compareRate(&result.Date, normalizeDate(got.Date) == normalizeDate(want.Date))
		compareRate(&result.Overall, normalizeDate(got.Date) == normalizeDate(want.Date))
	}
	if strings.TrimSpace(want.Issuer) != "" {
		correct := normalizeText(got.Issuer) == normalizeText(want.Issuer)
		compareRate(&result.Issuer, correct)
		compareRate(&result.Overall, correct)
	}
	if strings.TrimSpace(want.Level) != "" {
		correct := normalizeText(got.Level) == normalizeText(want.Level)
		compareRate(&result.Level, correct)
		compareRate(&result.Overall, correct)
	}
	if want.Quantity != nil {
		correct := got.Quantity != nil && abs(*got.Quantity-*want.Quantity) < .000001
		compareRate(&result.Quantity, correct)
		compareRate(&result.Overall, correct)
	}
}

func evaluateGate(value metrics) gate {
	result := gate{Passed: true}
	if value.Classification.Total == 0 || value.Classification.Rate < .85 {
		result.Passed = false
		result.Reasons = append(result.Reasons, "小项分类准确率低于 85% 或尚无有效标注")
	}
	if value.Fields.Overall.Total == 0 || value.Fields.Overall.Rate < .90 {
		result.Passed = false
		result.Reasons = append(result.Reasons, "关键字段准确率低于 90% 或尚无有效标注")
	}
	if value.SilentHighConfidenceErrors > 0 {
		result.Passed = false
		result.Reasons = append(result.Reasons, "存在高置信但归错类的静默错误")
	}
	return result
}

func (r *metricRate) finish() {
	if r.Total > 0 {
		r.Rate = float64(r.Correct) / float64(r.Total)
	}
}

func compareRate(rate *metricRate, correct bool) {
	rate.Total++
	if correct {
		rate.Correct++
	}
}

func imageMediaType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func opaqueID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}

func retryable(err error) bool {
	return llm.IsRetryable(err) || errors.Is(err, context.DeadlineExceeded)
}

func publicModelError(err error) string {
	var callErr *llm.CallError
	if errors.As(err, &callErr) && callErr.StatusCode > 0 {
		return fmt.Sprintf("模型服务 HTTP %d", callErr.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "模型服务调用超时"
	}
	return "模型输出无法解析或调用失败"
}

func normalizeText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
}

func normalizeDate(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r < '0' || r > '9' })
	if len(parts) >= 3 {
		year, yearErr := strconv.Atoi(parts[0])
		month, monthErr := strconv.Atoi(parts[1])
		day, dayErr := strconv.Atoi(parts[2])
		if yearErr == nil && monthErr == nil && dayErr == nil {
			return fmt.Sprintf("%04d%02d%02d", year, month, day)
		}
	}
	return strings.Join(parts, "")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
