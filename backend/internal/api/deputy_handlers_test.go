package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDeputyAppointmentRequiresExplicitSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{}`, `{"userId":0}`, `{"userId":-1}`, `{"userId":"invalid"}`, `{"userId":true}`} {
		t.Run(body, func(t *testing.T) {
			router := gin.New()
			router.PUT("/deputy", (&Server{}).updateClassDeputy)
			request := httptest.NewRequest(http.MethodPut, "/deputy", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid selection status=%d, want 400", response.Code)
			}
		})
	}
}

func TestDeputySurfaceRejectsUnappointedRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, actor := range []Actor{{Role: "student"}, {Role: "group"}, {Role: "class_admin"}, {Role: "student", IsDeputy: true}} {
		t.Run(actor.Role, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set(actorContextKey, actor) })
			router.GET("/deputy", requireDeputy(), func(c *gin.Context) { c.Status(http.StatusOK) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/deputy", nil))
			if response.Code != http.StatusForbidden {
				t.Fatalf("unappointed actor status=%d, want 403", response.Code)
			}
		})
	}
}
