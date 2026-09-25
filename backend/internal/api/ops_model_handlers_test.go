package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestModelTestReasonKeepsTheDiagnosisAndDropsCredentials(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{{
		name: "容器 DNS 故障诊断",
		err:  errors.New(`Post "https://api.example.org/v1/chat/completions": lookup api.example.org on 127.0.0.11:53: server misbehaving`),
		want: `Post "https://api.example.org/v1/chat/completions": lookup api.example.org on 127.0.0.11:53: server misbehaving`,
	}, {
		name: "地址被 SSRF 守卫拦下",
		err:  errors.New("AI endpoint resolved to blocked address 198.18.7.55"),
		want: "AI endpoint resolved to blocked address 198.18.7.55",
	}, {
		name: "换行与多余空白压成一行",
		err:  errors.New("model not found:\n\n  deepseek-v4-flesh"),
		want: "model not found: deepseek-v4-flesh",
	}, {
		name: "回包里万一带出 key 也要擦掉",
		err:  errors.New("unauthorized for key sk-FAKE_TEST_KEY_NEVER_VALID_000000000"),
		want: "unauthorized for key ***",
	}, {
		name: "Authorization 头形态同样擦掉",
		err:  errors.New("rejected header Authorization: Bearer abc123def456"),
		want: "rejected header Authorization: ***",
	}}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := modelTestReason(testCase.err); got != testCase.want {
				t.Fatalf("modelTestReason()\n got %q\nwant %q", got, testCase.want)
			}
		})
	}
}

// 前端从 detail.reason 里取这句诊断（ops/ModelRouting.tsx 的 testReason），
// 所以这个字段名是接口契约，改名就等于把界面上的原因又变回空白。
func TestModelRouteTestFailureBodyCarriesReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/api/v1/ops/agent/routes/agent.text/test", nil)

	reason := modelTestReason(errors.New("AI endpoint resolved to blocked address 198.18.7.55"))
	writeError(c, http.StatusBadGateway, "model_route_test_failed", "模型路由连接或能力测试失败", gin.H{"statusCode": 0, "reason": reason})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", recorder.Code)
	}
	var body struct {
		Code   string `json:"code"`
		Detail struct {
			Reason string `json:"reason"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "model_route_test_failed" {
		t.Fatalf("code = %q", body.Code)
	}
	if body.Detail.Reason != "AI endpoint resolved to blocked address 198.18.7.55" {
		t.Fatalf("detail.reason = %q", body.Detail.Reason)
	}
}

func TestModelTestReasonIsEmptyForNilAndClamped(t *testing.T) {
	if got := modelTestReason(nil); got != "" {
		t.Fatalf("nil 应当得到空串，得到 %q", got)
	}
	long := modelTestReason(errors.New(strings.Repeat("很长的错误", 200)))
	if runes := []rune(long); len(runes) != 401 || !strings.HasSuffix(long, "…") {
		t.Fatalf("超长错误应当截到 400 runes 加省略号，得到 %d runes", len([]rune(long)))
	}
}

func TestSyntheticVisionPNGIsOrdinarySizedValidImage(t *testing.T) {
	data := syntheticVisionPNG()
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != 128 || config.Height != 128 {
		t.Fatalf("probe dimensions = %dx%d", config.Width, config.Height)
	}
}
