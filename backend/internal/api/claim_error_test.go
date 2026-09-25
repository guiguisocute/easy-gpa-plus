package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/scheme"
)

func TestClaimErrorResponseKeepsTranslationReasonAndRange(t *testing.T) {
	_, err := scheme.ScoreClaim(scheme.ScoreRule{Type: "free", Min: new(0.0), Max: new(5.0)}, scheme.Claim{Score: new(6.0)})
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	writeClaimError(c, err)
	var body struct {
		Code, Message string
		Detail        struct {
			Reason string
			Params map[string]float64
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 422 || body.Code != "submission_invalid" || body.Detail.Reason != "claim_score_above_maximum" || body.Detail.Params["max"] != 5 || body.Message != "期望分数不能超过 5 分" {
		t.Fatalf("unexpected validation response: %s", response.Body.String())
	}
}
