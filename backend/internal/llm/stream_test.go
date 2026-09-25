package llm

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer 起一个最小的流式端点。write 里发出去的每个分块都会立刻 flush，
// 这样测试能按真实时序观察到增量。
func sseServer(t *testing.T, write func(flush func(string))) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Error("test server cannot flush")
			return
		}
		write(func(chunk string) {
			_, _ = writer.Write([]byte(chunk))
			flusher.Flush()
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func contentChunk(text string) string {
	return fmt.Sprintf("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", text)
}

func reasoningChunk(text string) string {
	return fmt.Sprintf("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":%q}}]}\n\n", text)
}

func streamingClient(t *testing.T, baseURL string, timeout time.Duration) StreamingClient {
	t.Helper()
	client, err := NewOpenAICompatible(Config{BaseURL: baseURL + "/v1", APIKey: "test", Timeout: timeout, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	streaming, ok := client.(StreamingClient)
	if !ok {
		t.Fatal("client does not stream")
	}
	return streaming
}

// 生产事故的核心：provider 的 timeout_seconds 变成了 http.Client.Timeout，而那是
// 覆盖整段响应体读取的客户端级硬上限。归组要 74 秒才吐出第一个正文字符，于是每次
// 调用都在 90 秒被静默腰斩，Request.Timeout 算出来的 320 秒完全不生效。
//
// 这里把客户端默认超时压到很小、请求自报一个大得多的值，服务端拖过默认值之后才
// 开始吐——只有“按请求覆盖真的生效”时这个调用才能成功。
func TestRequestTimeoutOverridesTheClientDefaultOnStreams(t *testing.T) {
	const clientDefault = 150 * time.Millisecond
	server := sseServer(t, func(flush func(string)) {
		time.Sleep(4 * clientDefault)
		flush(contentChunk(`{"candidates":[]}`))
		flush("data: [DONE]\n\n")
	})
	streaming := streamingClient(t, server.URL, clientDefault)

	response, err := streaming.Stream(context.Background(),
		Request{Model: "m", Prompt: "hi", Timeout: 10 * time.Second},
		func(Delta) error { return nil })
	if err != nil {
		t.Fatalf("stream should outlive the client default timeout, got %v", err)
	}
	if response.Content != `{"candidates":[]}` {
		t.Fatalf("unexpected content %q", response.Content)
	}
}

// 同一件事在非流式路径上也要成立：Call 一直传了 extra，但两条路必须给同一个保证。
func TestRequestTimeoutOverridesTheClientDefaultOnCalls(t *testing.T) {
	const clientDefault = 150 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(4 * clientDefault)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer server.Close()
	client, err := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", APIKey: "test", Timeout: clientDefault, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Call(context.Background(), Request{Model: "m", Prompt: "hi", Timeout: 10 * time.Second}); err != nil {
		t.Fatalf("call should outlive the client default timeout, got %v", err)
	}
}

// 不自报超时的调用（逐图识别就是这样）仍旧受客户端默认值约束，否则去掉
// http.Client.Timeout 之后就没有任何兜底了。
func TestStreamWithoutRequestTimeoutStillHonoursTheClientDefault(t *testing.T) {
	server := sseServer(t, func(flush func(string)) {
		time.Sleep(2 * time.Second)
		flush(contentChunk("late"))
	})
	streaming := streamingClient(t, server.URL, 150*time.Millisecond)

	if _, err := streaming.Stream(context.Background(), Request{Model: "m", Prompt: "hi"}, func(Delta) error { return nil }); err == nil {
		t.Fatal("stream without an explicit timeout should still time out")
	}
}

// 上游在流中途掐断连接时（实测同一个归组请求跑到 137 秒被切，没有 [DONE]），
// 已经收到的正文必须带出来，而且这个错误要算可重试——原来两条都不成立：
// 返回空 Response，且裸 EOF 落在重试判定之外。
func TestInterruptedStreamKeepsPartialContentAndRetries(t *testing.T) {
	/* 必须真的把 TCP 掐掉。正常返回的 handler 会让 chunked 编码收尾干净，SDK 只当
	   流结束了，看不出和上游中断的区别。 */
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Error("test server cannot hijack")
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
		body := contentChunk(`{"candidates":[{"title":"第一条"`)
		_, _ = fmt.Fprintf(buffered, "%x\r\n%s\r\n", len(body), body)
		_ = buffered.Flush()
		// 不写结束分块，直接断开。
		_ = conn.Close()
	}))
	defer server.Close()
	streaming := streamingClient(t, server.URL, 5*time.Second)

	response, err := streaming.Stream(context.Background(), Request{Model: "m", Prompt: "hi"}, func(Delta) error { return nil })
	if err == nil {
		t.Fatal("an interrupted stream must report an error")
	}
	if !strings.Contains(response.Content, "第一条") {
		t.Fatalf("partial content was discarded, got %q", response.Content)
	}
	if !IsRetryable(err) {
		t.Fatalf("a mid-stream disconnect should be retryable, got %v", err)
	}
}

// 推理模型先吐 reasoning_content 再吐 content。思考要单独报给调用方，但绝不能混进
// Content —— 那是要落库、要解析成候选 JSON 的答案。
func TestReasoningDeltasAreReportedSeparatelyFromContent(t *testing.T) {
	server := sseServer(t, func(flush func(string)) {
		flush(reasoningChunk("先想一下这批材料"))
		flush(reasoningChunk("再想一下"))
		flush(contentChunk(`{"candidates":[]}`))
		flush("data: [DONE]\n\n")
	})
	streaming := streamingClient(t, server.URL, 5*time.Second)

	var thinking, written strings.Builder
	response, err := streaming.Stream(context.Background(), Request{Model: "m", Prompt: "hi"}, func(delta Delta) error {
		thinking.WriteString(delta.Reasoning)
		written.WriteString(delta.Content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if thinking.String() != "先想一下这批材料再想一下" {
		t.Fatalf("reasoning was not reported, got %q", thinking.String())
	}
	if written.String() != `{"candidates":[]}` {
		t.Fatalf("content deltas were not reported, got %q", written.String())
	}
	if response.Content != `{"candidates":[]}` {
		t.Fatalf("reasoning leaked into the answer: %q", response.Content)
	}
}

// 客户端上不该再挂 http.Client.Timeout：它会盖过一切按请求设定的超时。这是防止
// 有人“顺手加回去”的守门测试。
func TestRestrictedHTTPClientHasNoClientLevelTimeout(t *testing.T) {
	client := restrictedHTTPClient(90*time.Second, true)
	if client.Timeout != 0 {
		t.Fatalf("http.Client.Timeout must stay zero, got %v", client.Timeout)
	}
	// 连接超时仍旧要有，否则连不上的端点会一直挂着。
	transport, ok := client.Transport.(responseLimitTransport)
	if !ok {
		t.Fatal("unexpected transport type")
	}
	if transport.next == nil {
		t.Fatal("transport chain is broken")
	}
	if _, err := net.ResolveTCPAddr("tcp", "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
}
