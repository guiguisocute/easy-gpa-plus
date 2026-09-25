package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOrderedHistoryAcceptsMultipleImagesOnUserMessage(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"test","object":"chat.completion","created":1,"model":"mimo-v2.5","choices":[{"index":0,"message":{"role":"assistant","content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	}))
	defer server.Close()
	client, err := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", APIKey: "test", Timeout: 5 * time.Second, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), Request{Model: "mimo-v2.5", Messages: []Message{
		{Role: "user", Content: "上一问"},
		{Role: "assistant", Content: `{"type":"final","answer":"上一答"}`},
		{Role: "user", Content: "比较这两张截图", Images: []Image{
			{MediaType: "image/png", Data: []byte("first")},
			{MediaType: "image/jpeg", Data: []byte("second")},
		}},
	}, JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	messages, ok := received["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v", received["messages"])
	}
	last := messages[2].(map[string]any)
	parts, ok := last["content"].([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("multimodal content = %#v", last["content"])
	}
	for index, raw := range parts[1:] {
		part := raw.(map[string]any)
		if part["type"] != "image_url" {
			t.Fatalf("part %d = %#v", index+1, part)
		}
		imageURL := part["image_url"].(map[string]any)["url"].(string)
		if len(imageURL) < 20 || imageURL[:5] != "data:" {
			t.Fatalf("image URL = %q", imageURL)
		}
	}
}

func TestClientBlocksLoopbackEndpointByDefault(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	client, err := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", APIKey: "test", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), Request{Model: "test", Prompt: "test"})
	if err == nil || calls != 0 {
		t.Fatalf("loopback request was not blocked: calls=%d error=%v", calls, err)
	}
}

func TestJSONFormatCompatibilityFallback(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, includesStore := body["store"]; includesStore {
			t.Error("optional store field must not be sent to compatibility gateways")
		}
		_, hasFormat := body["response_format"]
		if calls == 1 {
			if !hasFormat {
				t.Error("first call should prefer JSON response_format")
			}
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":{"message":"unsupported response_format","type":"invalid_request_error"}}`))
			return
		}
		if hasFormat {
			t.Error("fallback call should omit response_format")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"test","object":"chat.completion","created":1,"model":"text-test","choices":[{"index":0,"message":{"role":"assistant","content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	}))
	defer server.Close()

	client, err := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", APIKey: "test", Timeout: 5 * time.Second, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Call(context.Background(), Request{Model: "text-test", Prompt: "JSON", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || response.Content != `{"ok":true}` || response.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected response %#v after %d calls", response, calls)
	}
	if response.RequestHash == "" || response.RequestHash != RequestHash(Request{Model: "text-test", Prompt: "JSON", JSON: true}) {
		t.Fatalf("unexpected request hash %q", response.RequestHash)
	}
}

func TestRequestHashCoversNonSecretRequestShape(t *testing.T) {
	base := Request{Model: "model", System: "system", Prompt: "prompt", JSON: true, MaxTokens: 12, Image: &Image{MediaType: "image/png", Data: []byte("image")}}
	hash := RequestHash(base)
	if len(hash) != 64 {
		t.Fatalf("hash length = %d", len(hash))
	}
	changed := []Request{
		{Model: "other", System: base.System, Prompt: base.Prompt, JSON: base.JSON, MaxTokens: base.MaxTokens, Image: base.Image},
		{Model: base.Model, System: base.System, Prompt: base.Prompt, JSON: false, MaxTokens: base.MaxTokens, Image: base.Image},
		{Model: base.Model, System: base.System, Prompt: base.Prompt, JSON: base.JSON, MaxTokens: 13, Image: base.Image},
		{Model: base.Model, System: base.System, Prompt: base.Prompt, JSON: base.JSON, MaxTokens: base.MaxTokens, Image: &Image{MediaType: "image/png", Data: []byte("other")}},
	}
	for i, request := range changed {
		if RequestHash(request) == hash {
			t.Fatalf("change %d did not affect request hash", i)
		}
	}
}

func TestRequestHashCoversOrderedMessageHistory(t *testing.T) {
	request := Request{
		Model:  "agent-model",
		System: "system",
		Messages: []Message{
			{Role: "user", Content: "find the rule"},
			{Role: "assistant", Content: `{"type":"tool","tool":"grep"}`},
			{Role: "user", Content: "tool result"},
		},
		JSON: true,
	}
	base := RequestHash(request)
	if base == "" {
		t.Fatal("request hash is empty")
	}
	changedContent := request
	changedContent.Messages = append([]Message(nil), request.Messages...)
	changedContent.Messages[2].Content = "different tool result"
	if RequestHash(changedContent) == base {
		t.Fatal("message content did not affect request hash")
	}
	changedOrder := request
	changedOrder.Messages = []Message{request.Messages[2], request.Messages[1], request.Messages[0]}
	if RequestHash(changedOrder) == base {
		t.Fatal("message order did not affect request hash")
	}
	changedRole := request
	changedRole.Messages = append([]Message(nil), request.Messages...)
	changedRole.Messages[0].Role = "assistant"
	if RequestHash(changedRole) == base {
		t.Fatal("message role did not affect request hash")
	}
	changedImage := request
	changedImage.Messages = append([]Message(nil), request.Messages...)
	changedImage.Messages[0].Images = []Image{{MediaType: "image/png", Data: []byte("screenshot")}}
	if RequestHash(changedImage) == base {
		t.Fatal("message image did not affect request hash")
	}
}

func TestJSONFormatDoesNotRepeatUnrelatedBadRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":{"message":"invalid image","param":"messages","type":"invalid_request_error"}}`))
	}))
	defer server.Close()
	client, err := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", APIKey: "test", Timeout: 5 * time.Second, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), Request{Model: "vision-test", Prompt: "JSON", JSON: true})
	if err == nil || calls != 1 {
		t.Fatalf("error = %v, calls = %d; unrelated 400 must not be retried", err, calls)
	}
}
