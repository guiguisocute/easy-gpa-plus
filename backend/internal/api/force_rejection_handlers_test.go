package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestForceRejectPermissionKeepsRecusalAndAllowsClosedReviewStates(t *testing.T) {
	actor := Actor{UserID: 1, ClassID: 9, Role: "class_admin"}
	for _, status := range []string{"pending", "consensus", "scored", "appealing", "arbitrating", "locked"} {
		if allowed, reason := forceRejectPermission(actor, 2, status, false); !allowed || reason != "" {
			t.Fatalf("%s: allowed=%t, reason=%q", status, allowed, reason)
		}
	}
	for _, test := range []struct {
		studentID int64
		status    string
		rejected  bool
	}{{1, "locked", false}, {2, "draft", false}, {2, "scored", true}} {
		if allowed, reason := forceRejectPermission(actor, test.studentID, test.status, test.rejected); allowed || reason == "" {
			t.Fatalf("%+v: allowed=%t, reason=%q", test, allowed, reason)
		}
	}
	actor.Role = "group"
	if allowed, _ := forceRejectPermission(actor, 2, "scored", false); allowed {
		t.Fatal("ordinary group member may not force reject")
	}
}

func TestForceRejectDatabaseGuardsReturnConflictWithoutLeakingSQL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, constraint := range []string{"submission_force_rejection_score_check", "submission_force_rejection_immutable", "submission_force_rejection_workflow"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		writeServiceError(c, fmt.Errorf("update: %w", &pgconn.PgError{Code: "23514", ConstraintName: constraint, Detail: "private SQL row"}))
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "submission_force_rejected") || strings.Contains(recorder.Body.String(), "private SQL") {
			t.Fatalf("%s: %d %s", constraint, recorder.Code, recorder.Body.String())
		}
	}
}

func TestSelfForceRejectRequiresExistingNonnegativeScore(t *testing.T) {
	positive, zero, negative := 4.0, 0.0, -2.0
	for _, test := range []struct {
		name     string
		status   string
		score    *float64
		rejected bool
		want     bool
	}{
		{"scored", "scored", &positive, false, true},
		{"settled", "locked", &positive, false, true},
		{"pending appeal keeps earlier score", "appealing", &positive, false, true},
		{"pending arbitration keeps earlier score", "arbitrating", &positive, false, true},
		{"zero", "scored", &zero, false, true},
		{"draft", "draft", nil, false, false},
		{"pending", "pending", nil, false, false},
		{"consensus", "consensus", nil, false, false},
		{"unscored arbitration", "arbitrating", nil, false, false},
		{"missing score", "scored", nil, false, false},
		{"negative cannot raise own score", "scored", &negative, false, false},
		{"immutable rejection", "locked", &zero, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			allowed, reason := selfForceRejectPermission(test.status, test.score, test.rejected)
			if allowed != test.want || (reason == "") != test.want {
				t.Fatalf("allowed=%t, reason=%q, want=%t", allowed, reason, test.want)
			}
		})
	}
}

func TestForceRejectStillHonorsClassLockdown(t *testing.T) {
	for _, path := range []string{"/api/v1/admin/submissions/:id/force-reject", "/api/v1/review/deputy/submissions/:id/force-reject", "/api/v1/submissions/:id/force-reject"} {
		if lockdownExempt(http.MethodPost, path) {
			t.Fatalf("force rejection must honor lockdown: %s", path)
		}
	}
}

func TestForcedCorrectionKeepsDecisionButHidesSignerFromBlindReviewers(t *testing.T) {
	got, err := anonymousBlindAuditSnapshot([]byte(`{"details":{"items":[{"forceRejection":{"reason":"证书无效","actorName":"终裁人姓名","previousScore":4,"rejectedAt":"2026-09-05T00:00:00Z"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "actorName") || strings.Contains(string(got), "终裁人姓名") {
		t.Fatalf("blind reviewer saw the forced correction signer: %s", got)
	}
	if !strings.Contains(string(got), "证书无效") || !strings.Contains(string(got), `"previousScore":4`) {
		t.Fatalf("blind review lost the correction decision: %s", got)
	}
}
