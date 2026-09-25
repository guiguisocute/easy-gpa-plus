package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForceScoreRequiresRecognizedScoreAndAdjudicationPermission(t *testing.T) {
	admin := Actor{UserID: 1, ClassID: 9, Role: "class_admin"}
	for _, status := range []string{"scored", "locked", "appealing", "arbitrating"} {
		if ok, reason := forceScorePermission(admin, 2, status, scorePointer(0), false); !ok {
			t.Fatalf("%s: %s", status, reason)
		}
		if ok, _ := forceScorePermission(admin, 2, status, nil, false); ok {
			t.Fatalf("unscored %s allowed", status)
		}
	}
	for _, status := range []string{"draft", "pending", "consensus"} {
		if ok, _ := forceScorePermission(admin, 2, status, scorePointer(4), false); ok {
			t.Fatalf("%s allowed", status)
		}
	}
	for _, actor := range []Actor{admin, {UserID: 2, Role: "student"}, {UserID: 2, Role: "group"}, {UserID: 2, Role: "group", IsDeputy: true}} {
		if ok, _ := forceScorePermission(actor, actor.UserID, "scored", scorePointer(4), false); ok {
			t.Fatal("self adjudication allowed")
		}
	}
	if ok, _ := forceScorePermission(Actor{UserID: 3, Role: "group"}, 2, "scored", scorePointer(4), false); ok {
		t.Fatal("ordinary reviewer allowed")
	}
	if ok, _ := forceScorePermission(Actor{UserID: 3, Role: "group", IsDeputy: true}, 1, "scored", scorePointer(4), false); !ok {
		t.Fatal("deputy cannot handle admin")
	}
	if ok, _ := forceScorePermission(admin, 2, "scored", scorePointer(0), true); ok {
		t.Fatal("rejected score restored")
	}
	for _, path := range []string{"/api/v1/admin/submissions/:id/force-score", "/api/v1/review/deputy/submissions/:id/force-score"} {
		if lockdownExempt(http.MethodPost, path) {
			t.Fatal("force score bypasses lockdown")
		}
	}
}

func TestForceScoreRejectsMalformedOrIncompleteInputsBeforeMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{}`, `{"reason":"理由"}`, `{"reason":"理由","score":2}`, `{"reason":"理由","score":100000,"previousScore":4}`, `{"reason":"理由","score":null,"previousScore":4}`} {
		router := gin.New()
		server := &Server{}
		router.POST("/submissions/:id/force-score", server.forceScoreSubmission)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/submissions/1/force-score", strings.NewReader(body)))
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d", body, recorder.Code)
		}
	}
}
