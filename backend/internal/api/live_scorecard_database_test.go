package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"easygpa/backend/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func TestLiveScorecardAcknowledgementWithoutFinalReview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, false)
	other := seedSettlementClass(t, admin, false)
	submissionID := insertScoredSubmission(t, admin, fx, fx.StudentID, 3)
	insertScoredSubmission(t, admin, fx, fx.AdminID, 7)
	insertScoredSubmission(t, admin, other, other.StudentID, 9)
	actor := Actor{UserID: fx.StudentID, ClassID: fx.ClassID, Role: "student"}
	server := &Server{}
	register := func(r *gin.Engine) {
		r.GET("/me/scorecard", server.myScorecard)
		r.POST("/appeals/draft", server.createAppealDraft)
		r.POST("/me/scorecard/confirm", server.confirmMyResult)
	}
	type responseCard struct {
		Revision     string                            `json:"revision"`
		State        string                            `json:"state"`
		Confirmation struct{ Confirmed, Recheck bool } `json:"confirmation"`
		Scorecard    struct {
			Items []myScorecardItem `json:"items"`
		} `json:"scorecard"`
	}
	read := func() responseCard {
		t.Helper()
		response := serveClassHTTP(t, app, actor, register, http.MethodGet, "/me/scorecard", "")
		if response.Code != http.StatusOK {
			t.Fatalf("live result: %d %s", response.Code, response.Body.String())
		}
		var card responseCard
		if err := json.Unmarshal(response.Body.Bytes(), &card); err != nil {
			t.Fatal(err)
		}
		if len(card.Revision) != 64 || len(card.Scorecard.Items) != 1 {
			t.Fatalf("missing live result or leaked another member: %s", response.Body.String())
		}
		if strings.Contains(response.Body.String(), "reviewerId") || strings.Contains(response.Body.String(), "reviewerSid") {
			t.Fatal("reviewer identity leaked")
		}
		return card
	}
	first := read()
	if first.State != "confirmable" || first.Confirmation.Confirmed {
		t.Fatal("unsealed student must be able to acknowledge")
	}
	if read().Revision != first.Revision {
		t.Fatal("unchanged reads must have a stable revision")
	}
	confirm := func(revision string, want int) {
		t.Helper()
		response := serveClassHTTP(t, app, actor, register, http.MethodPost, "/me/scorecard/confirm", fmt.Sprintf(`{"revision":%q}`, revision))
		if response.Code != want {
			t.Fatalf("confirm: %d %s", response.Code, response.Body.String())
		}
	}
	confirm(first.Revision, http.StatusOK)
	confirm(first.Revision, http.StatusOK)
	if !read().Confirmation.Confirmed {
		t.Fatal("confirmation not retained")
	}
	if countClassRows(t, admin, `SELECT count(*) FROM score_acknowledgement WHERE class_id=$1`, fx.ClassID) != 1 {
		t.Fatal("duplicate acknowledgement")
	}
	if countClassRows(t, admin, `SELECT count(*) FROM scorecard_audit_batch WHERE class_id=$1`, fx.ClassID) != 0 {
		t.Fatal("reading/confirming must not create final-review work")
	}
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		var leaked int
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM score_acknowledgement WHERE class_id=$1`, other.ClassID).Scan(&leaked); err != nil {
			return err
		}
		if leaked != 0 {
			t.Fatal("tenant isolation failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `UPDATE submission SET final_score=4 WHERE id=$1`, submissionID); err != nil {
		t.Fatal(err)
	}
	changed := read()
	if changed.Revision == first.Revision || changed.Confirmation.Confirmed || !changed.Confirmation.Recheck {
		t.Fatal("changed score must require rechecking")
	}
	confirm(first.Revision, http.StatusConflict)
	confirm(changed.Revision, http.StatusOK)
	if _, err := admin.Exec(t.Context(), `INSERT INTO seal(class_id,student_id,source) SELECT class_id,id,'manual' FROM app_user WHERE class_id=$1`, fx.ClassID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `INSERT INTO student_gpa(class_id,student_id,scheme_id,score,details,import_batch,imported_by) SELECT class_id,id,$2,80,'{}',gen_random_uuid(),$3 FROM app_user WHERE class_id=$1`, fx.ClassID, fx.SchemeID, fx.AdminID); err != nil {
		t.Fatal(err)
	}
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		gate, err := evaluateGate(t.Context(), tx, time.Now())
		if err == nil && !gate.Gate.Open {
			t.Fatalf("missing final reviews or other members' confirmations blocked settlement: %#v", gate.Gate)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	confirm(read().Revision, http.StatusOK)
	appeal := serveClassHTTP(t, app, actor, register, http.MethodPost, "/appeals/draft", fmt.Sprintf(`{"targetType":"submission","targetId":"%d","reason":"核对成绩后仍可申诉"}`, submissionID))
	if appeal.Code != http.StatusCreated {
		t.Fatalf("acknowledgement blocked appeal: %d %s", appeal.Code, appeal.Body.String())
	}
}
