package llm

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

// TestOpenCodeGoContract is deliberately absent from ordinary CI. It sends
// only a synthetic image and runs when the operator explicitly supplies both
// OPENCODE_CONTRACT_TEST=1 and LLM_API_KEY.
func TestOpenCodeGoContract(t *testing.T) {
	if os.Getenv("OPENCODE_CONTRACT_TEST") != "1" {
		t.Skip("set OPENCODE_CONTRACT_TEST=1 to call the real provider")
	}
	key := strings.TrimSpace(os.Getenv("LLM_API_KEY"))
	if key == "" {
		t.Fatal("LLM_API_KEY is required for the contract test")
	}
	baseURL := strings.TrimSpace(os.Getenv("LLM_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://opencode.ai/zen/go/v1"
	}
	textModel := strings.TrimSpace(os.Getenv("LLM_TEXT_MODEL"))
	if textModel == "" {
		textModel = "deepseek-v4-flash"
	}
	visionModel := strings.TrimSpace(os.Getenv("LLM_VISION_MODEL"))
	if visionModel == "" {
		visionModel = "mimo-v2.5"
	}
	client, err := NewOpenAICompatible(Config{BaseURL: baseURL, APIKey: key, Timeout: 90 * time.Second, MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	textResponse, err := client.Call(ctx, Request{
		Model: textModel, JSON: true, MaxTokens: 512,
		Prompt: `只返回 JSON 对象 {"ok":true,"kind":"text"}。`,
	})
	if err != nil {
		t.Fatalf("text contract: %v", err)
	}
	var textResult struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(textResponse.Content), &textResult) != nil || !textResult.OK {
		t.Fatalf("text model did not honor JSON contract: %.160s", textResponse.Content)
	}

	imageData := synthetic42(t)
	visionResponse, err := client.Call(ctx, Request{
		Model: visionModel, JSON: true, MaxTokens: 1024,
		Prompt: `读取白底图中的两个黑色大数字，只返回 JSON 对象 {"digits":"..."}。`,
		Image:  &Image{MediaType: "image/png", Data: imageData},
	})
	if err != nil {
		t.Fatalf("vision contract: %v", err)
	}
	var visionResult struct {
		Digits string `json:"digits"`
	}
	if json.Unmarshal([]byte(visionResponse.Content), &visionResult) != nil || strings.TrimSpace(visionResult.Digits) != "42" {
		t.Fatalf("vision model did not read synthetic digits: %.160s", visionResponse.Content)
	}
}

func synthetic42(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 260, 130))
	fill(canvas, canvas.Bounds(), color.White)
	black := color.RGBA{A: 255}
	// Block-style 4.
	fill(canvas, image.Rect(34, 20, 52, 78), black)
	fill(canvas, image.Rect(82, 20, 100, 112), black)
	fill(canvas, image.Rect(34, 64, 100, 82), black)
	// Block-style 2.
	fill(canvas, image.Rect(134, 20, 216, 38), black)
	fill(canvas, image.Rect(198, 20, 216, 68), black)
	fill(canvas, image.Rect(134, 58, 216, 76), black)
	fill(canvas, image.Rect(134, 68, 152, 112), black)
	fill(canvas, image.Rect(134, 94, 216, 112), black)
	var buffer strings.Builder
	writer := stringWriter{builder: &buffer}
	if err := png.Encode(writer, canvas); err != nil {
		t.Fatal(err)
	}
	return []byte(buffer.String())
}

type stringWriter struct{ builder *strings.Builder }

func (w stringWriter) Write(value []byte) (int, error) { return w.builder.WriteString(string(value)) }

func fill(target *image.RGBA, rectangle image.Rectangle, value color.Color) {
	for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
		for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
			target.Set(x, y, value)
		}
	}
}
