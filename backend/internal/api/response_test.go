package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"easygpa/backend/internal/notify"
	"github.com/gin-gonic/gin"
)

func TestMailRateLimitResponseHasRetryAfterAndSafeMessage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	writeServiceError(c, fmt.Errorf("provider: %w", &notify.MailRateLimitError{After: 65 * time.Minute, Reason: "private provider detail"}))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "3900" {
		t.Fatalf("status=%d retry=%s", w.Code, w.Header().Get("Retry-After"))
	}
	if !strings.Contains(w.Body.String(), "mail_rate_limited") || strings.Contains(w.Body.String(), "private") {
		t.Fatal("response must contain safe actionable error")
	}
}
