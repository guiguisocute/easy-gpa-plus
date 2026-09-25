package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"easygpa/backend/internal/agentcontext"
	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/store"
	"github.com/jackc/pgx/v5"
)

type EvidenceContent struct {
	Text    string      `json:"text,omitempty"`
	Warning string      `json:"warning,omitempty"`
	Images  []llm.Image `json:"-"`
	Usage   llm.Usage   `json:"-"`
}
type EvidenceReader func(context.Context, agentcontext.Evidence) (EvidenceContent, error)
type readEvidenceInput struct {
	Sources []string `json:"sources"`
}

func (e *Executor) WithResource(input agentcontext.Context, reader EvidenceReader) {
	e.resourceContext = input
	e.readEvidenceFile = reader
	e.evidenceCache = map[string]EvidenceContent{}
	e.evidenceDelivered = map[string]bool{}
	e.evidenceReadBytes = 0
}

func (e *Executor) resource(ctx context.Context) (agentcontext.Snapshot, error) {
	var result agentcontext.Snapshot
	err := store.InTenantTx(ctx, e.pool, e.classID, func(tx pgx.Tx) error {
		var approved bool
		if err := tx.QueryRow(ctx, `SELECT external_processing_approved FROM knowledge_policy WHERE class_id=$1`, e.classID).Scan(&approved); err != nil || !approved {
			return errors.New("本班第三方处理授权已撤销")
		}
		var err error
		result, err = agentcontext.Load(ctx, tx, e.classID, e.userID, e.resourceContext)
		if err != nil {
			return err
		}
		return result.CheckRevision(e.resourceContext.Revision)
	})
	return result, err
}

func (e *Executor) CheckResource(ctx context.Context) error {
	_, err := e.resource(ctx)
	return err
}

func (e *Executor) businessSource(snapshot agentcontext.Snapshot, entry, title, excerptText string, read bool) Source {
	return Source{DocumentID: snapshot.Context.DocumentID(), EntryID: entry, Scope: "business", SourceKind: "business",
		Downloadable: true, AudienceRole: e.role, Filename: title, LogicalPath: snapshot.Context.ResourceLabel,
		Locator: map[string]any{"view": snapshot.Context.View, "resourceKind": snapshot.Context.ResourceKind,
			"resourceId": snapshot.Context.ResourceID, "revision": snapshot.Context.Revision, "section": title}, Excerpt: excerpt(excerptText, 1000), kind: "business", read: read}
}

func (e *Executor) readCurrentContext(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input struct{}
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	snapshot, err := e.resource(ctx)
	if err != nil {
		return Result{}, err
	}
	sources := []Source{}
	facts := e.businessSource(snapshot, "facts", "申报与审核意见", string(snapshot.Facts), true)
	factHandle := e.register(facts)
	sources = append(sources, *e.handles[factHandle])
	rules := e.businessSource(snapshot, "rules", "提交时与当前有效规则", string(snapshot.Rules), true)
	ruleHandle := e.register(rules)
	sources = append(sources, *e.handles[ruleHandle])
	files := []map[string]any{}
	for _, file := range snapshot.Evidence {
		source := e.businessSource(snapshot, "evidence-"+file.ID, file.Filename, "", false)
		handle := e.register(source)
		files = append(files, map[string]any{"source": handle, "filename": file.Filename, "mediaType": file.MediaType, "sizeBytes": file.SizeBytes, "status": file.Status, "selected": file.ID == snapshot.Context.EvidenceID, "read": false})
	}
	return marshalResult(map[string]any{"context": snapshot.Context, "facts": snapshot.Facts, "factsSource": factHandle,
		"rules": snapshot.Rules, "rulesSource": ruleHandle, "evidence": files, "canDraftReason": snapshot.CanDraft,
		"notice": "文件清单不代表已读取。请调用 read_evidence 查看与问题相关的原件。filed 是提交时规则，effective 是该事项当前有效规则，都不等同于后来发布的方案。draft 仅为用户未提交的表单内容。"},
		fmt.Sprintf("已读取本条申报、审核意见与规则，发现 %d 份附件（原件待读取）", len(files)), sources)
}

func (e *Executor) readEvidence(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input readEvidenceInput
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	if len(input.Sources) == 0 || len(input.Sources) > 4 {
		return Result{}, errors.New("每次请读取 1 至 4 份本条材料")
	}
	snapshot, err := e.resource(ctx)
	if err != nil {
		return Result{}, err
	}
	if e.readEvidenceFile == nil {
		return Result{}, errors.New("原件读取暂不可用")
	}
	return e.readEvidenceSnapshot(ctx, snapshot, input.Sources)
}

func (e *Executor) readEvidenceSnapshot(ctx context.Context, snapshot agentcontext.Snapshot, requested []string) (Result, error) {
	// Validate the entire batch before downloading or converting any file.
	seen := map[string]bool{}
	for _, handle := range requested {
		source, ok := e.handles[handle]
		if !ok || source.DocumentID != snapshot.Context.DocumentID() || !strings.HasPrefix(source.EntryID, "evidence-") || seen[source.EntryID] {
			return Result{}, errors.New("只能使用 read_current_context 返回的本条佐证 handle，不可传入路径、URL 或其他事项编号")
		}
		seen[source.EntryID] = true
	}
	items := []map[string]any{}
	sources := []Source{}
	images := []llm.Image{}
	usage := llm.Usage{}
	for _, handle := range requested {
		source := e.handles[handle]
		var file agentcontext.Evidence
		for _, candidate := range snapshot.Evidence {
			if source.EntryID == "evidence-"+candidate.ID {
				file = candidate
				break
			}
		}
		item := map[string]any{"source": handle, "filename": file.Filename, "read": false}
		items = append(items, item)
		if file.ID == "" || file.Status != "ready" {
			item["warning"] = "文件尚未上传完成或已经移除"
			continue
		}
		if e.evidenceDelivered[file.ID] {
			source.read = true
			item["read"], item["alreadyInContext"] = true, true
			item["notice"] = "本轮前面的工具结果已包含该原件，不重复附加图片"
			sources = append(sources, *source)
			continue
		}
		cached, found := e.evidenceCache[file.ID]
		if !found {
			if file.SizeBytes <= 0 || file.SizeBytes > 12<<20 || e.evidenceReadBytes+file.SizeBytes > 24<<20 {
				item["warning"] = "已达到本轮原件读取大小上限，未读取此文件"
				continue
			}
			e.evidenceReadBytes += file.SizeBytes
			var err error
			cached, err = e.readEvidenceFile(ctx, file)
			if err != nil {
				item["warning"] = "原件读取或转换失败，请人工查看该文件"
				continue
			}
			usage.InputTokens += cached.Usage.InputTokens
			usage.OutputTokens += cached.Usage.OutputTokens
			usage.TotalTokens += cached.Usage.TotalTokens
			e.evidenceCache[file.ID] = cached
		}
		item["warning"] = cached.Warning
		if cached.Text == "" && len(cached.Images) == 0 {
			continue
		}
		source.read = true
		item["read"] = true
		// Leave room for metadata and JSON escaping when several converted
		// files share a tool result. A partial extract is explicitly labelled.
		textBudget := max(0, (e.maxBytes-4096)/max(1, len(requested))/2)
		text := clipEvidenceText(cached.Text, textBudget)
		source.Excerpt = excerpt(text, 1000)
		item["text"] = text
		if len(text) < len(cached.Text) {
			item["warning"] = strings.TrimSpace(cached.Warning + " 本次只返回部分文本，后续内容未包含。")
		}
		if len(cached.Images) > 0 {
			item["imageIndex"] = len(images) + 1
			images = append(images, cached.Images...)
		}
		sources = append(sources, *source)
	}
	result, err := marshalResult(map[string]any{"files": items, "readCount": len(sources), "requestedCount": len(requested), "notice": "仅 read=true 的材料已读取；图片按 imageIndex 顺序随本条工具结果附上。warning 非空时说明存在限制，不得声称完整读完全部材料。"},
		fmt.Sprintf("读取原件 %d/%d 份", len(sources), len(requested)), sources)
	if err == nil && len(result.JSON) <= e.maxBytes {
		for _, source := range sources {
			e.evidenceDelivered[strings.TrimPrefix(source.EntryID, "evidence-")] = true
		}
	}
	result.Images = images
	result.Usage = usage
	return result, err
}

func clipEvidenceText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
