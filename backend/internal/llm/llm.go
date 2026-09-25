// Package llm is the single transport boundary for OpenAI-compatible model
// providers. Business packages pass plain requests through this interface and
// never depend on provider SDK types.
package llm

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/respjson"
	"github.com/openai/openai-go/v3/shared"

	"easygpa/backend/internal/netguard"
)

var ErrDisabled = errors.New("llm transport not enabled")
var ErrResponseTooLarge = errors.New("llm response exceeded the configured safety limit")

const (
	maxResponseContentBytes = 4 << 20
	maxResponseRawBytes     = 8 << 20
	maxHTTPResponseBytes    = 16 << 20
	PurposeMaterialVision   = "material.vision"
	PurposeMaterialCompose  = "material.compose"
	PurposeKnowledgeOCR     = "knowledge.ocr"
	PurposeAgentText        = "agent.text"
	PurposeAgentVision      = "agent.vision"
)

type Image struct {
	MediaType string
	Data      []byte
}

// Message is the provider-neutral ordered chat history used by the knowledge
// agent.  Existing material-processing callers can continue to use Prompt.
type Message struct {
	Role    string  `json:"role"`
	Content string  `json:"content"`
	Images  []Image `json:"-"`
}

// Route freezes the non-secret part of a model binding for an asynchronous
// job. Provider credentials are always resolved at execution time.
type Route struct {
	Purpose          string         `json:"purpose"`
	ProviderID       string         `json:"providerId,omitempty"`
	ProviderName     string         `json:"providerName,omitempty"`
	BaseURL          string         `json:"baseUrl"`
	Model            string         `json:"model"`
	ProviderRevision int64          `json:"providerRevision,omitempty"`
	RouteRevision    int64          `json:"routeRevision,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	Legacy           bool           `json:"legacy,omitempty"`
}

type Request struct {
	// Purpose is the stable EasyGPA Plus business slot. A runtime router replaces
	// Model with the route's configured model before reaching this transport.
	Purpose     string
	Route       *Route
	Model       string
	System      string
	Prompt      string
	Messages    []Message
	Image       *Image
	JSON        bool
	MaxTokens   int64
	Temperature float64
	// Timeout 覆盖客户端的默认请求超时，留零表示沿用默认。逐图识别和一次性归组
	// 几十条观察不是一个量级的活儿，共用一个 LLM_TIMEOUT 只能取其中一个的合适值。
	Timeout time.Duration
}

type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	TotalTokens  int64 `json:"totalTokens"`
}

type Response struct {
	Content     string          `json:"content"`
	Model       string          `json:"model"`
	Purpose     string          `json:"purpose,omitempty"`
	ProviderID  string          `json:"providerId,omitempty"`
	Provider    string          `json:"provider,omitempty"`
	RequestHash string          `json:"requestHash"`
	Usage       Usage           `json:"usage"`
	DurationMS  int64           `json:"durationMs"`
	Raw         json.RawMessage `json:"raw"`
}

type Client interface {
	Call(context.Context, Request) (Response, error)
}

// Delta 是流式返回里的一次增量。推理模型会把思考过程和最终答案分成两路吐出，
// 前者在 OpenAI 兼容网关上是非标准的 reasoning_content 字段。分开报而不是拼成
// 一股：调用方要区分“模型在想”和“模型在写”，混在一起就再也分不开了。
//
// 一次回调里两者不会同时非空。
type Delta struct {
	Content   string
	Reasoning string
}

// StreamingClient 是可选能力，不并进 Client。业务侧用类型断言探测，探测不到
// 就退回 Call —— 这样既有的假客户端（测试、离线评测）不必为流式改签名，
// 而“拿不到增量”只影响观感，不影响结果正确性。
//
// onDelta 收到的是本次新增的片段；返回错误会中止这次流式调用。
type StreamingClient interface {
	Stream(ctx context.Context, req Request, onDelta func(delta Delta) error) (Response, error)
}

type Disabled struct{}

func (Disabled) Call(context.Context, Request) (Response, error) {
	return Response{}, ErrDisabled
}

func (Disabled) Stream(context.Context, Request, func(Delta) error) (Response, error) {
	return Response{}, ErrDisabled
}

type Config struct {
	BaseURL             string
	APIKey              string
	Timeout             time.Duration
	MaxRetries          int
	AllowPrivateNetwork bool
}

type OpenAICompatible struct {
	client openai.Client
}

func NewOpenAICompatible(cfg Config) (Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		return Disabled{}, ErrDisabled
	}
	// cfg.Timeout 是“没有自报超时的调用”的默认值，不是上限：它以 SDK 选项的形式
	// 挂在客户端上，Request.Timeout 会在每次调用时盖过它。逐图识别不设超时，走的
	// 就是这个默认值。
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	client := openai.NewClient(
		option.WithBaseURL(strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")),
		option.WithAPIKey(strings.TrimSpace(cfg.APIKey)),
		option.WithMaxRetries(cfg.MaxRetries),
		option.WithRequestTimeout(cfg.Timeout),
		option.WithHTTPClient(restrictedHTTPClient(cfg.Timeout, cfg.AllowPrivateNetwork)),
	)
	return &OpenAICompatible{client: client}, nil
}

type responseBody struct {
	io.Reader
	io.Closer
}

type responseLimitTransport struct{ next http.RoundTripper }

func (t responseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > maxHTTPResponseBytes {
		_ = response.Body.Close()
		return nil, ErrResponseTooLarge
	}
	response.Body = responseBody{Reader: io.LimitReader(response.Body, maxHTTPResponseBytes+1), Closer: response.Body}
	return response, nil
}

func restrictedHTTPClient(timeout time.Duration, allowPrivate bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Provider credentials must not be sent through ambient process proxies.
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: min(timeout, 30*time.Second), KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if allowPrivate {
			return dialer.DialContext(ctx, network, address)
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("AI endpoint address: %w", err)
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var dialErr error
		for _, candidate := range addresses {
			if netguard.Blocked(candidate.IP) {
				dialErr = errors.Join(dialErr, fmt.Errorf("AI endpoint resolved to blocked address %s", candidate.IP))
				continue
			}
			ip := candidate.IP.String()
			if candidate.Zone != "" {
				ip += "%" + candidate.Zone
			}
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			if err == nil {
				return connection, nil
			}
			dialErr = errors.Join(dialErr, err)
		}
		if dialErr == nil {
			dialErr = errors.New("AI endpoint did not resolve to a public address")
		}
		return nil, dialErr
	}
	/* http.Client.Timeout 覆盖的是“连上到读完响应体”的整段，SSE 也算在内，而且是
	   客户端级的、无法按请求放宽。它留在这里时，provider 里那个 90 秒会把每一次
	   长归组调用静默腰斩：实测同一批材料的归组要 74 秒才吐出第一个正文字符，90 秒
	   砍下来时一条候选都没成型，而 aiassist 精心算出来的 320 秒超时因为取两者较小
	   值而完全不生效。超时改为一律走 per-request 的 context 截止时间（客户端级
	   WithRequestTimeout 提供默认值，Request.Timeout 覆盖它），那个才是能按调用
	   类型区分长短的旋钮。 */
	return &http.Client{
		Transport:     responseLimitTransport{next: transport},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (c *OpenAICompatible) Call(ctx context.Context, req Request) (Response, error) {
	params, err := buildParams(req)
	if err != nil {
		return Response{}, err
	}

	extra := requestOptions(req)

	started := time.Now()
	completion, err := c.client.Chat.Completions.New(ctx, params, extra...)
	// A few OpenAI-compatible gateways implement chat completions but not
	// response_format. Keep JSON mode as the preferred contract and make one
	// compatibility retry without that optional field on a 400 response. The
	// system/user prompts still require a single JSON object, and aiassist does
	// strict decoding plus at most one repair call afterwards.
	if err != nil && req.JSON && responseFormatUnsupported(err) {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{}
		completion, err = c.client.Chat.Completions.New(ctx, params, extra...)
	}
	if err != nil {
		return Response{}, classifyError(err)
	}
	if len(completion.Choices) == 0 {
		return Response{}, errors.New("llm returned no choices")
	}
	content := completion.Choices[0].Message.Content
	if len(content) > maxResponseContentBytes {
		return Response{}, ErrResponseTooLarge
	}
	raw := json.RawMessage(completion.RawJSON())
	if !json.Valid(raw) {
		raw, _ = json.Marshal(completion)
	}
	if len(raw) > maxResponseRawBytes {
		raw = nil
	}
	return Response{
		Content:     content,
		Model:       completion.Model,
		RequestHash: RequestHash(req),
		Usage: Usage{
			InputTokens: completion.Usage.PromptTokens, OutputTokens: completion.Usage.CompletionTokens,
			TotalTokens: completion.Usage.TotalTokens,
		},
		DurationMS: time.Since(started).Milliseconds(),
		Raw:        raw,
	}, nil
}

// Stream 与 Call 请求同一个端点、返回同样形状的 Response，区别只是在生成过程中
// 把增量交给 onDelta。Raw 不填：SSE 下没有单一的原始响应体可留证，而调用方要留
// 存的是最终 Content 与 RequestHash。
func (c *OpenAICompatible) Stream(ctx context.Context, req Request, onDelta func(Delta) error) (Response, error) {
	params, err := buildParams(req)
	if err != nil {
		return Response{}, err
	}
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}

	/* Call 一直在按请求覆盖超时，Stream 却从来没把这组选项传下去，于是 Request.Timeout
	   对流式调用完全不起作用，拿的始终是客户端默认值。归组正是唯一走流式的长调用。 */
	extra := requestOptions(req)

	started := time.Now()
	response, emitted, err := c.consume(ctx, params, req, started, onDelta, extra)
	// 与 Call 相同的 response_format 兼容重试。只在还没吐出任何增量时才重试，
	// 否则调用方会收到同一段文字两次。
	if err != nil && !emitted && req.JSON && responseFormatUnsupported(err) {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{}
		response, _, err = c.consume(ctx, params, req, started, onDelta, extra)
	}
	return response, err
}

func (c *OpenAICompatible) consume(
	ctx context.Context, params openai.ChatCompletionNewParams, req Request, started time.Time,
	onDelta func(Delta) error, extra []option.RequestOption,
) (Response, bool, error) {
	stream := c.client.Chat.Completions.NewStreaming(ctx, params, extra...)
	defer stream.Close()

	var content strings.Builder
	usage := Usage{}
	model := req.Model
	emitted := false
	// partial 把已经收到的正文带出去，即使这一趟以错误收场。调用方要么拿它抢救出
	// 完整的前半段，要么至少把它留在进度里——原来这里一律返回空 Response，几十秒
	// 的输出连同它一起丢掉，故障现场也跟着没了。
	partial := func(err error) (Response, bool, error) {
		return Response{
			Content: content.String(), Model: model, RequestHash: RequestHash(req),
			Usage: usage, DurationMS: time.Since(started).Milliseconds(),
		}, emitted, err
	}
	for stream.Next() {
		chunk := stream.Current()
		if chunk.Model != "" {
			model = chunk.Model
		}
		// 带 include_usage 时用量本该只在最后一个（choices 为空的）分块里出现，但
		// 有的网关每一块都塞一份，所以这里是覆盖而不是累加。
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 {
			usage = Usage{
				InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens,
				TotalTokens: chunk.Usage.TotalTokens,
			}
		}
		for _, choice := range chunk.Choices {
			// 推理模型先吐 reasoning_content 再吐 content：实测归组这一步思考要占掉
			// 头 64 秒，只看 content 的话这段时间界面上什么都没有。思考不进 Content，
			// 它不是答案。
			if reasoning := reasoningDelta(choice.Delta); reasoning != "" && onDelta != nil {
				if err := onDelta(Delta{Reasoning: reasoning}); err != nil {
					return partial(err)
				}
			}
			if choice.Delta.Content == "" {
				continue
			}
			if content.Len() > maxResponseContentBytes-len(choice.Delta.Content) {
				return partial(ErrResponseTooLarge)
			}
			content.WriteString(choice.Delta.Content)
			emitted = true
			if onDelta == nil {
				continue
			}
			if err := onDelta(Delta{Content: choice.Delta.Content}); err != nil {
				return partial(err)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return partial(classifyError(err))
	}
	if content.Len() == 0 {
		return partial(errors.New("llm stream returned no content"))
	}
	return Response{
		Content:     content.String(),
		Model:       model,
		RequestHash: RequestHash(req),
		Usage:       usage,
		DurationMS:  time.Since(started).Milliseconds(),
	}, emitted, nil
}

func buildParams(req Request) (openai.ChatCompletionNewParams, error) {
	if strings.TrimSpace(req.Model) == "" {
		return openai.ChatCompletionNewParams{}, errors.New("llm model is required")
	}
	if strings.TrimSpace(req.Prompt) == "" && len(req.Messages) == 0 {
		return openai.ChatCompletionNewParams{}, errors.New("llm prompt or messages are required")
	}
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 2+len(req.Messages))
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}
	if req.Image == nil {
		for _, message := range req.Messages {
			content := strings.TrimSpace(message.Content)
			if content == "" {
				return openai.ChatCompletionNewParams{}, errors.New("llm message content is required")
			}
			switch message.Role {
			case "system":
				if len(message.Images) > 0 {
					return openai.ChatCompletionNewParams{}, errors.New("system messages do not accept images")
				}
				messages = append(messages, openai.SystemMessage(content))
			case "user":
				if len(message.Images) == 0 {
					messages = append(messages, openai.UserMessage(content))
					break
				}
				parts := make([]openai.ChatCompletionContentPartUnionParam, 0, 1+len(message.Images))
				parts = append(parts, openai.TextContentPart(content))
				for _, image := range message.Images {
					part, err := imageContentPart(image)
					if err != nil {
						return openai.ChatCompletionNewParams{}, err
					}
					parts = append(parts, part)
				}
				messages = append(messages, openai.UserMessage(parts))
			case "assistant":
				if len(message.Images) > 0 {
					return openai.ChatCompletionNewParams{}, errors.New("assistant messages do not accept images")
				}
				messages = append(messages, openai.AssistantMessage(content))
			default:
				return openai.ChatCompletionNewParams{}, fmt.Errorf("unsupported llm message role %q", message.Role)
			}
		}
		if strings.TrimSpace(req.Prompt) != "" {
			messages = append(messages, openai.UserMessage(req.Prompt))
		}
	} else {
		if len(req.Messages) > 0 {
			return openai.ChatCompletionNewParams{}, errors.New("image requests do not accept ordered messages")
		}
		part, err := imageContentPart(*req.Image)
		if err != nil {
			return openai.ChatCompletionNewParams{}, err
		}
		messages = append(messages, openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
			openai.TextContentPart(req.Prompt),
			part,
		}))
	}

	params := openai.ChatCompletionNewParams{
		Model:       shared.ChatModel(req.Model),
		Messages:    messages,
		Temperature: openai.Float(req.Temperature),
	}
	if req.MaxTokens > 0 {
		// Chat Completions compatible providers commonly accept max_tokens.
		params.MaxTokens = openai.Int(req.MaxTokens)
	}
	if req.JSON {
		format := shared.NewResponseFormatJSONObjectParam()
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{OfJSONObject: &format}
	}
	return params, nil
}

func imageContentPart(image Image) (openai.ChatCompletionContentPartUnionParam, error) {
	mediaType := strings.ToLower(strings.TrimSpace(image.MediaType))
	if !allowedImageType(mediaType) {
		return openai.ChatCompletionContentPartUnionParam{}, fmt.Errorf("unsupported model image type %q", mediaType)
	}
	if len(image.Data) == 0 {
		return openai.ChatCompletionContentPartUnionParam{}, errors.New("model image is empty")
	}
	dataURL := "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
	return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: dataURL, Detail: "high"}), nil
}

func RequestHash(req Request) string {
	type hashImage struct {
		MediaType string `json:"mediaType"`
		SHA256    string `json:"sha256"`
	}
	type hashMessage struct {
		Role    string      `json:"role"`
		Content string      `json:"content"`
		Images  []hashImage `json:"images,omitempty"`
	}
	type hashInput struct {
		Purpose     string        `json:"purpose,omitempty"`
		Route       *Route        `json:"route,omitempty"`
		Model       string        `json:"model"`
		System      string        `json:"system"`
		Prompt      string        `json:"prompt"`
		Messages    []hashMessage `json:"messages,omitempty"`
		ImageType   string        `json:"imageType,omitempty"`
		ImageSHA256 string        `json:"imageSha256,omitempty"`
		JSON        bool          `json:"json"`
		MaxTokens   int64         `json:"maxTokens"`
		Temperature float64       `json:"temperature"`
	}
	input := hashInput{
		Purpose: req.Purpose, Route: req.Route, Model: req.Model, System: req.System, Prompt: req.Prompt, JSON: req.JSON,
		MaxTokens: req.MaxTokens, Temperature: req.Temperature,
	}
	for _, message := range req.Messages {
		item := hashMessage{Role: message.Role, Content: message.Content}
		for _, image := range message.Images {
			digest := sha256.Sum256(image.Data)
			item.Images = append(item.Images, hashImage{MediaType: image.MediaType, SHA256: hex.EncodeToString(digest[:])})
		}
		input.Messages = append(input.Messages, item)
	}
	if req.Image != nil {
		imageHash := sha256.Sum256(req.Image.Data)
		input.ImageType = req.Image.MediaType
		input.ImageSHA256 = hex.EncodeToString(imageHash[:])
	}
	payload, _ := json.Marshal(input)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func allowedImageType(mediaType string) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

type CallError struct {
	StatusCode int
	Retryable  bool
	Err        error
}

func (e *CallError) Error() string { return e.Err.Error() }
func (e *CallError) Unwrap() error { return e.Err }

// requestOptions 把 Request 上的按调用覆盖项翻成 SDK 选项。这些在客户端级同名
// 选项之后应用，所以能盖过它——逐图识别用默认的短超时，归组用自己的长超时。
func requestOptions(req Request) []option.RequestOption {
	var extra []option.RequestOption
	if req.Timeout > 0 {
		extra = append(extra, option.WithRequestTimeout(req.Timeout))
	}
	return extra
}

// reasoningDelta 取出非标准的 reasoning_content。SDK 的结构体里没有这个字段，
// 但原始 JSON 都留在 ExtraFields 里；解不出来就当这一块没有思考。
//
// 注意不能用 Field.Valid() 把关：那个方法回答的是“结构体里这个已知字段有没有被
// 赋值”，对 ExtraFields 里的额外字段恒为 false。只能看 Raw()。
func reasoningDelta(delta openai.ChatCompletionChunkChoiceDelta) string {
	field, ok := delta.JSON.ExtraFields["reasoning_content"]
	if !ok {
		return ""
	}
	raw := field.Raw()
	if raw == "" || raw == respjson.Null {
		return ""
	}
	var text string
	if json.Unmarshal([]byte(raw), &text) != nil {
		return ""
	}
	return text
}

func classifyError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		status := apiErr.StatusCode
		return &CallError{StatusCode: status, Retryable: status == 429 || status >= 500, Err: err}
	}
	/* 上游在流中途掐断连接（实测同一个归组请求跑到 137 秒被切，没有 [DONE]）读出来
	   是一个裸的 EOF 或连接重置，既不是 *openai.Error 也不是超时，于是从前一路漏过
	   重试判定——一次网络抖动就等同于整块归组失败。 */
	if streamInterrupted(err) {
		return &CallError{Retryable: true, Err: err}
	}
	return err
}

// streamInterrupted 判断错误是不是“连接在响应读到一半时断了”。
//
// 连接被对端重置在不同平台上是不同的 errno（Windows 是 WSAECONNRESET，不是
// ECONNRESET），所以认 *net.OpError 这个类型而不是逐个比 errno——凡是读写 socket
// 时报出来的传输层错误，对一次模型调用而言都属于“再试一次可能就好了”。
// TLS 层的 unexpected EOF 没有可比较的哨兵值，只能认字符串。
//
// 注意 SSRF 守卫在 DialContext 里返回的是普通 fmt.Errorf，不是 OpError，所以
// “解析到被禁地址”不会被误判成可重试。
func streamInterrupted(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	return strings.Contains(err.Error(), "unexpected EOF")
}

func responseFormatUnsupported(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		return false
	}
	detail := strings.ToLower(apiErr.Param + " " + apiErr.Message)
	return strings.Contains(detail, "response_format") || strings.Contains(detail, "response format")
}

func IsRetryable(err error) bool {
	var callErr *CallError
	return errors.As(err, &callErr) && callErr.Retryable
}
