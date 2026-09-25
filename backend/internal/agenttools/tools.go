// Package agenttools implements the complete, closed tool surface available to
// the knowledge model.  It deliberately exposes neither SQL nor storage keys;
// handles are random and valid only for one message execution.
package agenttools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/agentcontext"
	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/store"
)

var allowedTools = map[string]bool{
	"find_files": true, "grep": true, "read_text": true,
	"inspect_table": true, "file_stats": true, "current_scheme": true, "search_product_help": true,
	"read_current_context": true, "read_evidence": true,
}

/* 每个工具的参数结构都是具名类型，而不是写在函数里的匿名 struct。参数名是工具
   契约的一部分：模型猜错一个字段就白白多一次模型往返，所以「有哪些字段」必须
   能被别处读到——报错时要说清楚，提示词里的 schema 也要按它对照。 */

type findFilesInput struct {
	Query     string `json:"query"`
	Extension string `json:"extension"`
	Status    string `json:"status"`
	SortBy    string `json:"sortBy"`
	Desc      bool   `json:"descending"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

type grepInput struct {
	Query         string   `json:"query"`
	Mode          string   `json:"mode"`
	Sources       []string `json:"sources"`
	Glob          string   `json:"glob"`
	CaseSensitive bool     `json:"caseSensitive"`
	ContextLines  int      `json:"contextLines"`
	Limit         int      `json:"limit"`
}

type readTextInput struct {
	Source    string `json:"source"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

type inspectTableInput struct {
	Source     string `json:"source"`
	Sheet      string `json:"sheet"`
	Range      string `json:"range"`
	Contains   string `json:"contains"`
	SortByCell string `json:"sortByCell"`
	Descending bool   `json:"descending"`
	Limit      int    `json:"limit"`
}

type fileStatsInput struct {
	GroupBy    string `json:"groupBy"`
	Descending bool   `json:"descending"`
	Limit      int    `json:"limit"`
}

type searchProductHelpInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

var toolInputShapes = map[string]any{
	"find_files": findFilesInput{}, "grep": grepInput{}, "read_text": readTextInput{},
	"inspect_table": inspectTableInput{}, "file_stats": fileStatsInput{}, "current_scheme": struct{}{},
	"search_product_help":  searchProductHelpInput{},
	"read_current_context": struct{}{}, "read_evidence": readEvidenceInput{},
}

// ArgumentFields 返回某个工具接受的参数名，未知工具返回 nil。agentjob 的提示词
// 测试拿它逐个比对，保证提示词里写的 schema 不会和这里的结构体各说各话。
func ArgumentFields(tool string) []string {
	shape, ok := toolInputShapes[tool]
	if !ok {
		return nil
	}
	return jsonFields(reflect.TypeOf(shape))
}

func jsonFields(shape reflect.Type) []string {
	fields := make([]string, 0, shape.NumField())
	for field := range shape.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	return fields
}

type Source struct {
	Handle       string         `json:"-"`
	DocumentID   string         `json:"documentId"`
	EntryID      string         `json:"entryId"`
	Scope        string         `json:"scope,omitempty"`
	SourceKind   string         `json:"sourceKind,omitempty"`
	Downloadable bool           `json:"downloadable"`
	AudienceRole string         `json:"audienceRole,omitempty"`
	Filename     string         `json:"filename"`
	LogicalPath  string         `json:"logicalPath"`
	Locator      map[string]any `json:"locator"`
	Excerpt      string         `json:"excerpt"`

	documentID int64
	entryID    int64
	blobID     int64
	kind       string
	score      int
	read       bool
}

type Result struct {
	JSON    json.RawMessage
	Summary string
	Hash    string
	Sources []Source
	Images  []llm.Image
	Usage   llm.Usage
}

type Executor struct {
	pool              *pgxpool.Pool
	classID           int64
	userID            int64
	role              string
	actualRole        string
	view              string
	opsPool           *pgxpool.Pool
	maxBytes          int
	maxScanBytes      int64
	scannedBytes      int64
	handles           map[string]*Source
	newHandle         func() string
	resourceContext   agentcontext.Context
	readEvidenceFile  EvidenceReader
	evidenceReadBytes int64
	evidenceCache     map[string]EvidenceContent
	evidenceDelivered map[string]bool
}

func New(pool *pgxpool.Pool, classID, userID int64, role string, maxBytes int, maxScanBytes int64) (*Executor, error) {
	return NewWithPlatform(pool, nil, classID, userID, role, role, "", maxBytes, maxScanBytes)
}

func NewWithPlatform(pool, opsPool *pgxpool.Pool, classID, userID int64, actualRole, effectiveRole, view string, maxBytes int, maxScanBytes int64) (*Executor, error) {
	if pool == nil || classID <= 0 || userID <= 0 {
		return nil, errors.New("agent tools require a tenant actor")
	}
	if effectiveRole != "student" && effectiveRole != "group" && effectiveRole != "class_admin" {
		return nil, errors.New("agent tools received an invalid role")
	}
	if maxBytes < 1024 {
		maxBytes = 32 * 1024
	}
	if maxScanBytes < 1<<20 {
		maxScanBytes = 64 << 20
	}
	return &Executor{pool: pool, opsPool: opsPool, classID: classID, userID: userID, role: effectiveRole, actualRole: actualRole, view: view, maxBytes: maxBytes, maxScanBytes: maxScanBytes, handles: make(map[string]*Source), newHandle: randomHandle}, nil
}

func Allowed(tool string) bool { return allowedTools[tool] }

func (e *Executor) Execute(ctx context.Context, tool string, arguments json.RawMessage) (output Result, callErr error) {
	// A failed/oversized business result was never delivered to the model. Do
	// not leave citable handles or mark its images as already delivered.
	if tool == "read_current_context" || tool == "read_evidence" {
		before := make(map[string]Source, len(e.handles))
		for handle, source := range e.handles {
			before[handle] = *source
		}
		delivered := make(map[string]bool, len(e.evidenceDelivered))
		for id, value := range e.evidenceDelivered {
			delivered[id] = value
		}
		defer func() {
			if callErr == nil {
				return
			}
			e.handles = make(map[string]*Source, len(before))
			for handle, source := range before {
				e.handles[handle] = &source
			}
			e.evidenceDelivered = delivered
		}()
	}
	if !Allowed(tool) {
		return Result{}, fmt.Errorf("工具 %q 不在允许列表中", tool)
	}
	if len(arguments) == 0 || !json.Valid(arguments) {
		arguments = json.RawMessage(`{}`)
	}
	var result Result
	var err error
	switch tool {
	case "find_files":
		result, err = e.findFiles(ctx, arguments)
	case "grep":
		result, err = e.grep(ctx, arguments)
	case "read_text":
		result, err = e.readText(ctx, arguments)
	case "inspect_table":
		result, err = e.inspectTable(ctx, arguments)
	case "file_stats":
		result, err = e.fileStats(ctx, arguments)
	case "current_scheme":
		result, err = e.currentScheme(ctx)
	case "search_product_help":
		result, err = e.searchProductHelp(ctx, arguments)
	case "read_current_context":
		result, err = e.readCurrentContext(ctx, arguments)
	case "read_evidence":
		result, err = e.readEvidence(ctx, arguments)
	}
	if err != nil {
		return Result{}, err
	}
	if len(result.JSON) > e.maxBytes {
		return Result{}, fmt.Errorf("工具结果超过 %d KiB，请缩小范围", e.maxBytes/1024)
	}
	sum := sha256.Sum256(result.JSON)
	result.Hash = hex.EncodeToString(sum[:])
	return result, nil
}

func (e *Executor) searchProductHelp(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input searchProductHelpInput
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || utf8.RuneCountInString(input.Query) > 300 {
		return Result{}, errors.New("产品帮助查询不能为空或超过 300 字符")
	}
	if input.Limit <= 0 || input.Limit > 12 {
		input.Limit = 6
	}
	if e.opsPool == nil {
		return marshalResult(map[string]any{"matches": []any{}, "count": 0}, "平台产品知识尚未配置", nil)
	}
	type hit struct {
		source                   Source
		filename, title, excerpt string
		locator                  map[string]any
	}
	hits := make([]hit, 0)
	rows, err := e.opsPool.Query(ctx, `
		SELECT d.id,d.source_kind,d.filename,d.display_name,d.page_tags,d.keywords,
		       e.id,e.locator,e.plain_text
		  FROM platform_knowledge_document d
		  JOIN platform_knowledge_entry e ON e.document_id=d.id
		 WHERE d.deleted_at IS NULL AND d.enabled AND d.searchable
		   AND d.status IN ('ready','partial') AND $1=ANY(d.allowed_roles)
		 ORDER BY d.sort_order,d.id,e.id
	`, e.role)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	terms := productTerms(input.Query)
	for rows.Next() {
		var documentID, entryID int64
		var sourceKind, filename, title, text string
		var pageTags, keywords []string
		var locatorRaw []byte
		if err := rows.Scan(&documentID, &sourceKind, &filename, &title, &pageTags, &keywords, &entryID, &locatorRaw, &text); err != nil {
			return Result{}, err
		}
		if err := e.chargeScan(len(text)); err != nil {
			return Result{}, err
		}
		score, index := productScore(input.Query, terms, title, pageTags, keywords, text, e.view, sourceKind)
		if score == 0 {
			continue
		}
		locator := map[string]any{}
		_ = json.Unmarshal(locatorRaw, &locator)
		excerptText := productExcerpt(text, terms, index)
		audience := e.role
		source := Source{DocumentID: "platform-" + strconv.FormatInt(documentID, 10), EntryID: strconv.FormatInt(entryID, 10), Scope: "platform", SourceKind: sourceKind, Downloadable: true, AudienceRole: audience, Filename: filename, LogicalPath: "平台产品知识/" + title, Locator: locator, Excerpt: excerptText, documentID: documentID, entryID: entryID, kind: "platform-entry", read: true, score: score}
		hits = append(hits, hit{source: source, filename: filename, title: title, excerpt: excerptText, locator: locator})
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].source.score > hits[j].source.score })
	if len(hits) > input.Limit {
		hits = hits[:input.Limit]
	}
	matches := make([]map[string]any, 0, len(hits))
	sources := make([]Source, 0, len(hits))
	for _, item := range hits {
		handle := e.register(item.source)
		item.source.Handle = handle
		matches = append(matches, map[string]any{"source": handle, "filename": item.filename, "title": item.title, "excerpt": item.excerpt, "locator": item.locator})
		sources = append(sources, *e.handles[handle])
	}
	return marshalResult(map[string]any{"matches": matches, "count": len(matches), "role": e.role, "view": e.view}, fmt.Sprintf("平台产品知识命中 %d 条", len(matches)), sources)
}

func productTerms(query string) []string {
	lower := strings.ToLower(strings.TrimSpace(query))
	parts := strings.Fields(lower)
	terms := append([]string(nil), parts...)
	runes := []rune(lower)
	for i := 0; i+1 < len(runes); i++ {
		left, right := runes[i], runes[i+1]
		if left > 127 || right > 127 {
			terms = append(terms, string(runes[i:i+2]))
		}
	}
	return terms
}

func productScore(query string, terms []string, title string, pageTags, keywords []string, text, view, sourceKind string) (int, int) {
	haystack := strings.ToLower(title + "\n" + strings.Join(keywords, " ") + "\n" + strings.Join(pageTags, " ") + "\n" + text)
	queryLower := strings.ToLower(strings.TrimSpace(query))
	score := 0
	if strings.Contains(haystack, queryLower) {
		score += 500
	}
	index := strings.Index(strings.ToLower(text), strings.ToLower(query))
	for _, term := range terms {
		if term != "" && strings.Contains(haystack, term) {
			score += 40
		}
		for _, keyword := range keywords {
			if strings.Contains(strings.ToLower(keyword), term) {
				score += 80
			}
		}
	}
	for _, tag := range pageTags {
		if tag == view {
			score += 220
		}
	}
	if sourceKind == "custom" {
		score += 1000
	}
	return score, index
}

func productExcerpt(text string, terms []string, index int) string {
	runes := []rune(text)
	runeIndex := 0
	if index > 0 {
		runeIndex = utf8.RuneCountInString(text[:min(index, len(text))])
	}
	start := runeIndex - 120
	if start < 0 {
		start = 0
	}
	end := runeIndex + 300
	if end > len(runes) {
		end = len(runes)
	}
	return excerpt(string(runes[start:end]), 850)
}

// ResolvedCitation 判断一个 handle 能不能作为引用交出去。
//
// 段落级引用必须真读过：模型不能拿一个只在搜索结果里见过的条目当证据。文件级
// 引用（find_files 给的整份文件）则不需要——它指向的是"这份文件"本身，用户点开
// 拿到的就是原件，模型没有借它断言任何内容。这条放宽是为了让"把这份文件给我"
// 一步就能满足，而不必先为了拿个可引用的 handle 去 grep 一次。
func (e *Executor) ResolvedCitation(handle string) (Source, bool) {
	source, ok := e.handles[handle]
	if !ok || (!source.read && source.kind != "document") {
		return Source{}, false
	}
	copy := *source
	return copy, true
}

func (e *Executor) findFiles(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input findFilesInput
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	input.Query = strings.TrimSpace(input.Query)
	input.Extension = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(input.Extension)), ".")
	// 按字符数而不是字节数：一个中文文件名 67 个字就超过 200 字节，会被当成"过长"拒掉。
	if utf8.RuneCountInString(input.Query) > 200 || utf8.RuneCountInString(input.Extension) > 20 {
		return Result{}, errors.New("文件搜索条件过长")
	}
	if input.Limit <= 0 || input.Limit > 100 {
		input.Limit = 30
	}
	if input.Offset < 0 || input.Offset > 10000 {
		return Result{}, errors.New("文件搜索 offset 无效")
	}
	patterns := likePatterns(input.Query)
	orders := map[string]string{"name": "lower(d.display_name)", "size": "b.size_bytes", "updatedAt": "d.updated_at", "status": "d.status"}
	order := orders[input.SortBy]
	if order == "" {
		order = "d.updated_at"
	}
	direction := "ASC"
	if input.Desc || input.SortBy == "" {
		direction = "DESC"
	}
	query := `
		SELECT d.id,d.blob_id,d.filename,d.logical_path,d.display_name,d.visibility,d.status,
		       d.searchable,b.media_type,b.size_bytes,d.updated_at
		  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id
		 WHERE d.deleted_at IS NULL
		   AND (d.visibility='class' OR ($1 IN ('group','class_admin') AND d.visibility='review') OR ($1='class_admin' AND d.visibility='admin'))
		   AND (cardinality($2::text[])=0 OR lower(d.display_name||'/'||d.logical_path||'/'||d.filename) ~~ ALL ($2::text[]))
		   AND ($3='' OR lower(right(d.filename,length($3)+1))='.'||$3)
		   AND ($4='' OR d.status=$4)
		 ORDER BY ` + order + ` ` + direction + `,d.id ` + direction + ` LIMIT $5 OFFSET $6`
	type item struct {
		Handle, Filename, LogicalPath, DisplayName, Visibility, Status, MediaType string
		Searchable                                                                bool
		SizeBytes                                                                 int64
		UpdatedAt                                                                 time.Time
	}
	items := make([]item, 0)
	err := store.InTenantTx(ctx, e.pool, e.classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, e.role, patterns, input.Extension, strings.TrimSpace(input.Status), input.Limit, input.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var documentID, blobID int64
			var filename, logicalPath, displayName, visibility, status, mediaType string
			var searchable bool
			var size int64
			var updated time.Time
			if err := rows.Scan(&documentID, &blobID, &filename, &logicalPath, &displayName, &visibility, &status, &searchable, &mediaType, &size, &updated); err != nil {
				return err
			}
			handle := e.register(Source{DocumentID: strconv.FormatInt(documentID, 10), EntryID: "", Scope: "class", SourceKind: "class", Downloadable: true, AudienceRole: e.role, Filename: displayName, LogicalPath: logicalPath, Locator: map[string]any{}, documentID: documentID, blobID: blobID, kind: "document"})
			items = append(items, item{Handle: handle, Filename: filename, LogicalPath: logicalPath, DisplayName: displayName, Visibility: visibility, Status: status, Searchable: searchable, MediaType: mediaType, SizeBytes: size, UpdatedAt: updated})
		}
		return rows.Err()
	})
	if err != nil {
		return Result{}, err
	}
	payload := map[string]any{"files": items, "count": len(items), "offset": input.Offset}
	return marshalResult(payload, fmt.Sprintf("按文件名命中 %d 个", len(items)), nil)
}

// 关键词按空白拆开，每个都要出现才算命中。模型写的是「示例学院 学生综合
// 素质考评实施细则」，而文件名里没有那个空格——整串当一个子串去 LIKE 必定零命中，
// 线上表现就是「明明有这份 PDF 却说找不到」，和 extension 那个正则是同一类故障。
// `%` `_` 不转义：本来就是给检索用的通配符，这里没有必要改掉既有行为。
func likePatterns(query string) []string {
	terms := strings.Fields(strings.ToLower(query))
	patterns := make([]string, len(terms))
	for index, term := range terms {
		patterns[index] = "%" + term + "%"
	}
	return patterns
}

func (e *Executor) grep(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input grepInput
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || len(input.Query) > 300 {
		return Result{}, errors.New("grep 查询不能为空或超过 300 字符")
	}
	if input.Mode == "" {
		input.Mode = "literal"
	}
	if input.Mode != "literal" && input.Mode != "regex" {
		return Result{}, errors.New("grep mode 只能是 literal 或 regex")
	}
	if input.ContextLines < 0 || input.ContextLines > 10 {
		return Result{}, errors.New("grep 上下文只能是 0—10 行")
	}
	if input.Limit <= 0 || input.Limit > 100 {
		input.Limit = 30
	}
	var expression *regexp.Regexp
	if input.Mode == "regex" {
		pattern := input.Query
		if !input.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		var err error
		expression, err = regexp.Compile(pattern)
		if err != nil {
			return Result{}, errors.New("grep 正则表达式无效")
		}
	}
	documentIDs, err := e.documentHandles(input.Sources)
	if err != nil {
		return Result{}, err
	}
	if len(documentIDs) == 0 {
		return Result{}, errors.New("grep 必须使用 find_files 返回的 source handle")
	}
	type match struct {
		Source, Filename, LogicalPath, Excerpt string
		Locator                                map[string]any
	}
	matches := make([]match, 0)
	sources := make([]Source, 0)
	err = store.InTenantTx(ctx, e.pool, e.classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT d.id,d.display_name,d.logical_path,e.id,e.blob_id,e.locator,e.plain_text
			  FROM knowledge_document d JOIN knowledge_entry e ON e.blob_id=d.blob_id
			 WHERE d.id=ANY($1) AND d.deleted_at IS NULL
			   AND (d.visibility='class' OR ($2 IN ('group','class_admin') AND d.visibility='review') OR ($2='class_admin' AND d.visibility='admin'))
			 ORDER BY d.id,e.id
		`, documentIDs, e.role)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() && len(matches) < input.Limit {
			var documentID, entryID, blobID int64
			var filename, logicalPath, text string
			var locatorRaw []byte
			if err := rows.Scan(&documentID, &filename, &logicalPath, &entryID, &blobID, &locatorRaw, &text); err != nil {
				return err
			}
			if err := e.chargeScan(len(text)); err != nil {
				return err
			}
			if input.Glob != "" {
				ok, matchErr := path.Match(input.Glob, strings.ReplaceAll(logicalPath, "\\", "/"))
				if matchErr != nil {
					return errors.New("grep glob 无效")
				}
				if !ok {
					continue
				}
			}
			lines := strings.Split(text, "\n")
			for index, line := range lines {
				if !matchesLine(line, input.Query, input.CaseSensitive, expression) {
					continue
				}
				start := max(0, index-input.ContextLines)
				end := min(len(lines), index+input.ContextLines+1)
				locator := map[string]any{}
				_ = json.Unmarshal(locatorRaw, &locator)
				locator["startLine"] = start + 1
				locator["endLine"] = end
				excerpt := strings.Join(lines[start:end], "\n")
				handle := e.register(Source{DocumentID: strconv.FormatInt(documentID, 10), EntryID: strconv.FormatInt(entryID, 10), Scope: "class", SourceKind: "class", Downloadable: true, AudienceRole: e.role, Filename: filename, LogicalPath: logicalPath, Locator: locator, Excerpt: excerpt, documentID: documentID, entryID: entryID, blobID: blobID, kind: "entry", read: true})
				source := *e.handles[handle]
				sources = append(sources, source)
				matches = append(matches, match{Source: handle, Filename: filename, LogicalPath: logicalPath, Locator: locator, Excerpt: excerpt})
				if len(matches) >= input.Limit {
					break
				}
			}
		}
		return rows.Err()
	})
	if err != nil {
		return Result{}, err
	}
	return marshalResult(map[string]any{"matches": matches, "count": len(matches)}, fmt.Sprintf("grep %q 命中 %d 处", input.Query, len(matches)), sources)
}

func (e *Executor) chargeScan(size int) error {
	if size < 0 || e.maxScanBytes <= 0 || int64(size) > e.maxScanBytes-e.scannedBytes {
		e.scannedBytes = e.maxScanBytes
		return fmt.Errorf("Agent 工具文本扫描量超过 %d MiB，请减少 source 数量或缩小文件范围", e.maxScanBytes>>20)
	}
	e.scannedBytes += int64(size)
	return nil
}

func (e *Executor) readText(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input readTextInput
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	known, ok := e.handles[input.Source]
	if !ok || known.kind != "entry" || known.entryID <= 0 {
		// 把 find_files 的文件 handle 直接丢给 read_text 是最常见的一种错法，
		// 所以这里直接给出正确路径，而不是只说一句「无效」。
		return Result{}, errors.New("read_text 只接受 grep 或 inspect_table 返回的条目 handle；find_files 给的是文件 handle，请先用 grep 定位到条目")
	}
	if input.StartLine <= 0 {
		input.StartLine = 1
	}
	if input.EndLine <= 0 {
		input.EndLine = input.StartLine + 199
	}
	if input.EndLine < input.StartLine || input.EndLine-input.StartLine > 499 {
		return Result{}, errors.New("read_text 单次最多读取 500 行")
	}
	var text, filename, logicalPath string
	var locatorRaw []byte
	err := store.InTenantTx(ctx, e.pool, e.classID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT e.plain_text,d.display_name,d.logical_path,e.locator
			  FROM knowledge_entry e JOIN knowledge_document d ON d.blob_id=e.blob_id
			 WHERE e.id=$1 AND d.id=$2 AND d.deleted_at IS NULL
			   AND (d.visibility='class' OR ($3 IN ('group','class_admin') AND d.visibility='review') OR ($3='class_admin' AND d.visibility='admin'))
		`, known.entryID, known.documentID, e.role).Scan(&text, &filename, &logicalPath, &locatorRaw)
	})
	if err != nil {
		return Result{}, err
	}
	if err := e.chargeScan(len(text)); err != nil {
		return Result{}, err
	}
	lines := strings.Split(text, "\n")
	start := min(max(input.StartLine-1, 0), len(lines))
	end := min(input.EndLine, len(lines))
	selected := ""
	if start < end {
		selected = strings.Join(lines[start:end], "\n")
	}
	locator := map[string]any{}
	_ = json.Unmarshal(locatorRaw, &locator)
	locator["startLine"] = start + 1
	locator["endLine"] = end
	known.read = true
	known.Excerpt = excerpt(selected, 700)
	known.Locator = locator
	source := *known
	return marshalResult(map[string]any{"source": input.Source, "locator": locator, "text": selected}, fmt.Sprintf("读取 %s 第 %d—%d 行", filename, start+1, end), []Source{source})
}

func (e *Executor) inspectTable(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input inspectTableInput
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	known, ok := e.handles[input.Source]
	if !ok || (known.kind != "document" && known.kind != "entry") {
		return Result{}, errors.New("inspect_table source handle 无效")
	}
	if input.Limit <= 0 || input.Limit > 500 {
		input.Limit = 100
	}
	type table struct {
		Source, Sheet, Range string
		Rows                 []string
		Metadata             map[string]any
	}
	items := make([]table, 0)
	sources := make([]Source, 0)
	err := store.InTenantTx(ctx, e.pool, e.classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT d.id,d.display_name,d.logical_path,e.id,e.blob_id,e.locator,e.plain_text,e.metadata
			  FROM knowledge_document d JOIN knowledge_entry e ON e.blob_id=d.blob_id
			 WHERE d.id=$1 AND d.deleted_at IS NULL AND e.kind='sheet'
			   AND (d.visibility='class' OR ($2 IN ('group','class_admin') AND d.visibility='review') OR ($2='class_admin' AND d.visibility='admin'))
			 ORDER BY e.id
		`, known.documentID, e.role)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var documentID, entryID, blobID int64
			var filename, logicalPath, text string
			var locatorRaw, metadataRaw []byte
			if err := rows.Scan(&documentID, &filename, &logicalPath, &entryID, &blobID, &locatorRaw, &text, &metadataRaw); err != nil {
				return err
			}
			if err := e.chargeScan(len(text)); err != nil {
				return err
			}
			locator := map[string]any{}
			metadata := map[string]any{}
			_ = json.Unmarshal(locatorRaw, &locator)
			_ = json.Unmarshal(metadataRaw, &metadata)
			sheet, _ := locator["sheet"].(string)
			if input.Sheet != "" && sheet != input.Sheet {
				continue
			}
			lines := strings.Split(text, "\n")
			selected := make([]string, 0, len(lines))
			for _, line := range lines {
				if input.Contains == "" || strings.Contains(strings.ToLower(line), strings.ToLower(input.Contains)) {
					selected = append(selected, line)
				}
			}
			if input.SortByCell != "" {
				needle := strings.ToUpper(input.SortByCell) + "="
				sort.SliceStable(selected, func(i, j int) bool {
					left, right := cellValue(selected[i], needle), cellValue(selected[j], needle)
					if input.Descending {
						return left > right
					}
					return left < right
				})
			}
			if len(selected) > input.Limit {
				selected = selected[:input.Limit]
			}
			handle := e.register(Source{DocumentID: strconv.FormatInt(documentID, 10), EntryID: strconv.FormatInt(entryID, 10), Scope: "class", SourceKind: "class", Downloadable: true, AudienceRole: e.role, Filename: filename, LogicalPath: logicalPath, Locator: locator, Excerpt: excerpt(strings.Join(selected, "\n"), 700), documentID: documentID, entryID: entryID, blobID: blobID, kind: "entry", read: true})
			source := *e.handles[handle]
			sources = append(sources, source)
			rangeName, _ := locator["range"].(string)
			if input.Range != "" {
				rangeName = input.Range
			}
			items = append(items, table{Source: handle, Sheet: sheet, Range: rangeName, Rows: selected, Metadata: metadata})
		}
		return rows.Err()
	})
	if err != nil {
		return Result{}, err
	}
	return marshalResult(map[string]any{"tables": items, "count": len(items)}, fmt.Sprintf("检查了 %d 个工作表", len(items)), sources)
}

func (e *Executor) fileStats(ctx context.Context, raw json.RawMessage) (Result, error) {
	var input fileStatsInput
	if err := strictJSON(raw, &input); err != nil {
		return Result{}, err
	}
	if input.GroupBy == "" {
		input.GroupBy = "extension"
	}
	if input.GroupBy != "extension" && input.GroupBy != "directory" && input.GroupBy != "status" {
		return Result{}, errors.New("file_stats groupBy 只能是 extension、directory 或 status")
	}
	if input.Limit <= 0 || input.Limit > 200 {
		input.Limit = 100
	}
	type stat struct {
		Key       string `json:"key"`
		Count     int    `json:"count"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	groups := map[string]*stat{}
	var firstDocumentID, firstBlobID int64
	var firstPath string
	err := store.InTenantTx(ctx, e.pool, e.classID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT d.id,d.blob_id,d.filename,d.logical_path,d.status,b.size_bytes
			  FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id
			 WHERE d.deleted_at IS NULL
			   AND (d.visibility='class' OR ($1 IN ('group','class_admin') AND d.visibility='review') OR ($1='class_admin' AND d.visibility='admin'))
		`, e.role)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var documentID, blobID, size int64
			var filename, logicalPath, status string
			if err := rows.Scan(&documentID, &blobID, &filename, &logicalPath, &status, &size); err != nil {
				return err
			}
			if firstDocumentID == 0 {
				firstDocumentID, firstBlobID, firstPath = documentID, blobID, logicalPath
			}
			key := status
			switch input.GroupBy {
			case "extension":
				key = strings.TrimPrefix(strings.ToLower(path.Ext(filename)), ".")
				if key == "" {
					key = "(none)"
				}
			case "directory":
				key = path.Dir(strings.ReplaceAll(logicalPath, "\\", "/"))
			}
			item := groups[key]
			if item == nil {
				item = &stat{Key: key}
				groups[key] = item
			}
			item.Count++
			item.SizeBytes += size
		}
		return rows.Err()
	})
	if err != nil {
		return Result{}, err
	}
	items := make([]stat, 0, len(groups))
	for _, item := range groups {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Key < items[j].Key
		}
		if input.Descending {
			return items[i].Count > items[j].Count
		}
		return items[i].Count < items[j].Count
	})
	if len(items) > input.Limit {
		items = items[:input.Limit]
	}
	sources := []Source{}
	statsHandle := ""
	if firstDocumentID > 0 {
		statsHandle = e.register(Source{DocumentID: strconv.FormatInt(firstDocumentID, 10), EntryID: "", Scope: "class", SourceKind: "class", Downloadable: true, AudienceRole: e.role, Filename: "班级知识库索引", LogicalPath: firstPath, Locator: map[string]any{"groupBy": input.GroupBy}, Excerpt: fmt.Sprintf("按 %s 聚合，共 %d 组", input.GroupBy, len(items)), documentID: firstDocumentID, blobID: firstBlobID, kind: "document", read: true})
		sources = append(sources, *e.handles[statsHandle])
	}
	return marshalResult(map[string]any{"source": statsHandle, "groups": items, "count": len(items)}, fmt.Sprintf("按 %s 统计并排序，共 %d 组", input.GroupBy, len(items)), sources)
}

func (e *Executor) currentScheme(ctx context.Context) (Result, error) {
	var id int64
	var name string
	var version int
	var config []byte
	err := store.InTenantTx(ctx, e.pool, e.classID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,name,version,config FROM scheme WHERE status='published' ORDER BY version DESC LIMIT 1`).Scan(&id, &name, &version, &config)
	})
	if err != nil {
		return Result{}, err
	}
	handle := e.register(Source{DocumentID: "scheme-" + strconv.FormatInt(id, 10), EntryID: "0", Scope: "scheme", SourceKind: "scheme", Downloadable: false, AudienceRole: e.role, Filename: name, LogicalPath: "系统/当前已发布方案", Locator: map[string]any{"version": version}, Excerpt: excerpt(string(config), 700), kind: "scheme", read: true})
	var decoded any
	if json.Unmarshal(config, &decoded) != nil {
		decoded = string(config)
	}
	return marshalResult(map[string]any{"source": handle, "id": strconv.FormatInt(id, 10), "name": name, "version": version, "config": decoded}, "读取当前已发布方案", []Source{*e.handles[handle]})
}

func (e *Executor) documentHandles(handles []string) ([]int64, error) {
	seen := make(map[int64]bool)
	ids := make([]int64, 0, len(handles))
	for _, handle := range handles {
		source, ok := e.handles[handle]
		if !ok || source.documentID <= 0 {
			return nil, errors.New("source handle 无效或已过期")
		}
		if !seen[source.documentID] {
			seen[source.documentID] = true
			ids = append(ids, source.documentID)
		}
	}
	return ids, nil
}

func (e *Executor) register(source Source) string {
	handle := e.newHandle()
	for e.handles[handle] != nil {
		handle = e.newHandle()
	}
	source.Handle = handle
	e.handles[handle] = &source
	return handle
}

func randomHandle() string {
	var raw [15]byte
	_, _ = rand.Read(raw[:])
	return "src_" + base64.RawURLEncoding.EncodeToString(raw[:])
}

// strictJSON 解参数。出错时必须说清楚错在哪、有哪些字段可用：这条错误会原样回
// 给模型，而它每猜错一次就是一整轮模型往返。此前这里只回一句「参数不符合约定」，
// 模型拿不到任何可纠正的信息，实测出现过一条消息连猜 10 次直到撞满处理时限。
//
// 只回显字段名与解码器的判断，不回显参数值——参数是模型写的，值可能带上它从
// 资料里抄来的内容。
func strictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		return nil
	}
	fields := jsonFields(reflect.TypeOf(target).Elem())
	if unknown, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return fmt.Errorf("参数 %q 类型不对，应为 %s；本工具接受：%s", unknown.Field, unknown.Type, strings.Join(fields, "、"))
	}
	return fmt.Errorf("参数不符合约定（%s）；本工具接受：%s", err, strings.Join(fields, "、"))
}

func marshalResult(value any, summary string, sources []Source) (Result, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return Result{}, err
	}
	return Result{JSON: raw, Summary: summary, Sources: sources}, nil
}

func matchesLine(line, query string, sensitive bool, expression *regexp.Regexp) bool {
	if expression != nil {
		return expression.MatchString(line)
	}
	if sensitive {
		return strings.Contains(line, query)
	}
	return strings.Contains(strings.ToLower(line), strings.ToLower(query))
}

func cellValue(line, needle string) string {
	upper := strings.ToUpper(line)
	index := strings.Index(upper, needle)
	if index < 0 {
		return ""
	}
	value := line[index+len(needle):]
	if end := strings.IndexAny(value, "\t["); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func excerpt(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
