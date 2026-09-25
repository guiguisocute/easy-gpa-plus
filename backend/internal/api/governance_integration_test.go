package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"easygpa/backend/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func governanceTestRequest(t *testing.T, pool *pgxpool.Pool, actor Actor, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	server := &Server{}
	return serveClassHTTP(t, pool, actor, func(r *gin.Engine) {
		r.Use(server.governanceGuard())
		r.GET("/api/v1/governance", server.governanceState)
		r.PUT("/api/v1/governance/config", server.configureGovernance)
		r.PUT("/api/v1/governance/membership", server.joinGovernance)
		r.POST("/api/v1/governance/proposals", server.createGovernanceProposal)
		r.POST("/api/v1/governance/proposals/:id/vote", server.voteGovernance)
		r.POST("/api/v1/governance/proposals/:id/close", server.closeGovernanceProposal)
		r.GET("/api/v1/governance/proposals", server.governanceProposals)
		r.GET("/api/v1/governance/cases/:id", server.governanceCaseDetail)
		r.POST("/api/v1/governance/cases/:id/opinion", server.decideGovernanceCase)
		r.POST("/api/v1/governance/cases/:id/ballot", server.ballotGovernanceCase)
		r.POST("/api/v1/appeals/draft", server.createAppealDraft)
		r.GET("/api/v1/appeals", server.appeals)
		r.GET("/api/v1/appeals/:id", server.appeal)
		r.POST("/api/v1/appeals/:id/withdraw", server.withdrawAppeal)
		r.POST("/api/v1/appeals/:id/submit", server.collectiveAfter(server.submitAppeal))
		r.POST("/api/v1/admin/settle", server.runSettlement)
		r.POST("/api/v1/review/deputy/submissions/:id/arbitrate", server.arbitrateSubmission)
	}, method, path, string(raw))
}
func governanceExpect(t *testing.T, r *httptest.ResponseRecorder, status int) map[string]json.RawMessage {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status %d want %d: %s", r.Code, status, r.Body.String())
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func governanceFixture(t *testing.T, admin *pgxpool.Pool) (settlementFixture, []int64) {
	t.Helper()
	fx := seedSettlementClass(t, admin, true)
	ids := []int64{fx.AdminID, fx.StudentID}
	for i := 3; i < 40; i++ {
		sid := fmt.Sprintf("GOV-%d-%02d", fx.ClassID, i)
		if _, err := admin.Exec(t.Context(), `INSERT INTO whitelist(class_id,sid,name,role) VALUES($1,$2,$3,'student')`, fx.ClassID, sid, fmt.Sprintf("合成成员%d", i)); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := admin.QueryRow(t.Context(), `SELECT id FROM app_user WHERE class_id=$1 AND sid=$2`, fx.ClassID, sid).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Exactly twelve opt-ins, all with zero material submissions. Others retain
	// their stable roster identities and are still included in settlement/GPA.
	ids = ids[:12]
	if _, err := admin.Exec(t.Context(), `UPDATE app_user SET password_hash='synthetic-test-hash' WHERE class_id=$1 AND id=ANY($2::bigint[])`, fx.ClassID, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `INSERT INTO class_governance(class_id,mode,profile,created_by) VALUES($1,'collective','standard',$2)`, fx.ClassID, fx.AdminID); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := admin.Exec(t.Context(), `INSERT INTO governance_member(class_id,user_id,joined_at,reviewer) VALUES($1,$2,now()-interval '2 days',true)`, fx.ClassID, id); err != nil {
			t.Fatal(err)
		}
	}
	return fx, ids
}
func TestGovernanceOptInSnapshotAndWholeRosterProtection(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL"))
	fx, ids := governanceFixture(t, admin)
	actor := Actor{UserID: ids[0], ClassID: fx.ClassID, Role: "class_admin"}
	state := governanceExpect(t, governanceTestRequest(t, app, actor, "GET", "/api/v1/governance", nil), 200)
	var counts map[string]int
	_ = json.Unmarshal(state["counts"], &counts)
	if counts["roster"] != 40 || counts["registered"] != 12 || counts["electorate"] != 12 || counts["submitted"] != 0 {
		t.Fatalf("membership conflated with materials: %v", counts)
	}
	makeProposal := func(kind string) *httptest.ResponseRecorder {
		return governanceTestRequest(t, app, actor, "POST", "/api/v1/governance/proposals", map[string]any{"requestId": uuid.NewString(), "kind": kind, "action": "motion", "title": "统一核验说明", "body": "按既定规则核对全部成员的资料。", "payload": map[string]any{}})
	}
	governanceExpect(t, makeProposal("protected"), 409) // 14 votes cannot be obtained from 12.
	result := governanceExpect(t, makeProposal("ordinary"), 201)
	var proposal string
	_ = json.Unmarshal(result["id"], &proposal)
	if _, err := admin.Exec(t.Context(), `UPDATE governance_proposal SET opens_at=now()-interval '1 hour',closes_at=now()+interval '2 hours' WHERE id=$1`, proposal); err != nil {
		t.Fatal(err)
	}
	governanceExpect(t, governanceTestRequest(t, app, actor, "PUT", "/api/v1/governance/membership", map[string]any{"join": false}), 200)
	for i := 0; i < 7; i++ {
		voter := Actor{UserID: ids[i], ClassID: fx.ClassID, Role: "student"}
		governanceExpect(t, governanceTestRequest(t, app, voter, "POST", "/api/v1/governance/proposals/"+proposal+"/vote", map[string]any{"choice": "yes"}), 200)
	}
	// A newly registered late joiner cannot enter a ballot already in progress.
	if _, err := admin.Exec(t.Context(), `UPDATE app_user SET password_hash='synthetic-test-hash' WHERE id=$1`, fx.UnregisteredID); err != nil {
		t.Fatal(err)
	}
	late := Actor{UserID: fx.UnregisteredID, ClassID: fx.ClassID, Role: "student"}
	governanceExpect(t, governanceTestRequest(t, app, late, "PUT", "/api/v1/governance/membership", map[string]any{"join": true, "reviewer": true}), 200)
	governanceExpect(t, governanceTestRequest(t, app, late, "POST", "/api/v1/governance/proposals/"+proposal+"/vote", map[string]any{"choice": "yes"}), 403)
	governanceExpect(t, governanceTestRequest(t, app, actor, "POST", "/api/v1/governance/proposals/"+proposal+"/close", nil), 409)
	if _, err := admin.Exec(t.Context(), `UPDATE governance_proposal SET closes_at=now()-interval '1 minute' WHERE id=$1`, proposal); err != nil {
		t.Fatal(err)
	}
	governanceExpect(t, governanceTestRequest(t, app, actor, "POST", "/api/v1/governance/proposals/"+proposal+"/close", nil), 200)
	var status string
	var denominator, needed int
	if err := admin.QueryRow(t.Context(), `SELECT status,electorate_count,required_yes FROM governance_proposal WHERE id=$1`, proposal).Scan(&status, &denominator, &needed); err != nil {
		t.Fatal(err)
	}
	if status != "applied" || denominator != 12 || needed != 7 {
		t.Fatalf("snapshot changed: %s %d %d", status, denominator, needed)
	}
	governanceExpect(t, governanceTestRequest(t, app, actor, "POST", "/api/v1/admin/settle", nil), 403)
	governanceExpect(t, governanceTestRequest(t, app, actor, "POST", "/api/v1/review/deputy/submissions/1/arbitrate", nil), 403)
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		gate, e := evaluateGate(t.Context(), tx, time.Now())
		if e == nil && (gate.Gate.Forced || gate.StudentCount != 40) {
			t.Fatalf("collective bypassed whole roster: %+v", gate)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	other := seedSettlementClass(t, admin, false)
	outsider := Actor{UserID: other.StudentID, ClassID: other.ClassID, Role: "student"}
	governanceExpect(t, governanceTestRequest(t, app, outsider, "POST", "/api/v1/governance/proposals/"+proposal+"/vote", map[string]any{"choice": "yes"}), 404)
}

func TestGovernanceInitialModeChoiceIsFinalForOrdinaryClass(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL"))
	fx := seedSettlementClass(t, admin, false)
	actor := settlementActor(fx)
	state := governanceExpect(t, governanceTestRequest(t, app, actor, "GET", "/api/v1/governance", nil), 200)
	if string(state["canConfigure"]) != "true" {
		t.Fatal("new class must offer the initial choice")
	}
	student := Actor{UserID: fx.StudentID, ClassID: fx.ClassID, Role: "student"}
	governanceExpect(t, governanceTestRequest(t, app, student, "PUT", "/api/v1/governance/config", map[string]string{"mode": "collective"}), 403)
	state = governanceExpect(t, governanceTestRequest(t, app, actor, "PUT", "/api/v1/governance/config", map[string]string{"mode": "centralized"}), 200)
	if string(state["canConfigure"]) != "false" {
		t.Fatal("chosen ordinary mode must not continue offering mode selection")
	}
	governanceExpect(t, governanceTestRequest(t, app, actor, "PUT", "/api/v1/governance/config", map[string]string{"mode": "collective"}), 409)
	var mode string
	if err := admin.QueryRow(t.Context(), `SELECT mode FROM class_governance WHERE class_id=$1`, fx.ClassID).Scan(&mode); err != nil || mode != "centralized" {
		t.Fatalf("ordinary class changed mode: %s %v", mode, err)
	}
}

func TestGovernanceReviewMajorityAndIndependentAppealPool(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL"))
	fx, _ := governanceFixture(t, admin)
	sub := insertScoredSubmission(t, admin, fx, fx.StudentID, 4)
	if _, err := admin.Exec(t.Context(), `UPDATE submission SET status='pending',final_score=NULL WHERE id=$1`, sub); err != nil {
		t.Fatal(err)
	}
	subject := Actor{UserID: fx.StudentID, ClassID: fx.ClassID, Role: "student"}
	server := &Server{}
	governanceExpect(t, serveClassHTTP(t, app, subject, func(r *gin.Engine) {
		r.POST("/assign", func(c *gin.Context) {
			if err := server.ensureGovernanceCase(c, "submission", sub, false); err != nil {
				writeServiceError(c, err)
				return
			}
			c.JSON(200, gin.H{"ok": true})
		})
	}, "POST", "/assign", "{}"), 200)
	var id int64
	if err := admin.QueryRow(t.Context(), `SELECT id FROM governance_proposal WHERE class_id=$1 AND action='submission' AND target_id=$2`, fx.ClassID, sub).Scan(&id); err != nil {
		t.Fatal(err)
	}
	seats := func(pid int64) []int64 {
		t.Helper()
		rows, err := admin.Query(t.Context(), `SELECT user_id FROM governance_voter WHERE proposal_id=$1 AND active ORDER BY assigned_at,user_id`, pid)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var user int64
			if err = rows.Scan(&user); err != nil {
				t.Fatal(err)
			}
			out = append(out, user)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	initial := seats(id)
	if len(initial) != 2 || initial[0] == fx.StudentID || initial[1] == fx.StudentID {
		t.Fatalf("bad assignment: %v", initial)
	}
	vote := func(user int64, score float64) {
		t.Helper()
		actor := Actor{UserID: user, ClassID: fx.ClassID, Role: "student"}
		path := "/api/v1/governance/cases/" + strconv.FormatInt(id, 10)
		detail := governanceExpect(t, governanceTestRequest(t, app, actor, "GET", path, nil), 200)
		var version string
		_ = json.Unmarshal(detail["version"], &version)
		governanceExpect(t, governanceTestRequest(t, app, actor, "POST", path+"/opinion", map[string]any{"score": score, "reason": "按材料原件和适用规则认定。", "expectedVersion": version}), 200)
	}
	vote(initial[0], 2)
	before := governanceExpect(t, governanceTestRequest(t, app, Actor{UserID: initial[1], ClassID: fx.ClassID, Role: "student"}, "GET", fmt.Sprintf("/api/v1/governance/cases/%d", id), nil), 200)
	if _, leaked := before["opinions"]; leaked {
		t.Fatal("peer opinions leaked before independent decision")
	}
	// The desk needs the subject and what they claimed to score the case at all,
	// and needs to know how many seats have answered — but not who, and not what.
	var submitted int
	_ = json.Unmarshal(before["submitted"], &submitted)
	if submitted != 1 {
		t.Fatalf("submitted count wrong: %d", submitted)
	}
	for _, key := range []string{"student", "studentId", "claim", "requestedScore", "submittedAt"} {
		if _, ok := before[key]; !ok {
			t.Fatalf("case detail missing %s", key)
		}
	}
	vote(initial[1], 4)
	expanded := seats(id)
	if len(expanded) != 3 {
		t.Fatalf("conflict did not retain both original seats: %v", expanded)
	}
	var third int64
	for _, user := range expanded {
		if user != initial[0] && user != initial[1] {
			third = user
		}
	}
	if third == 0 {
		t.Fatal("third reviewer missing")
	}
	vote(third, 4)
	var score float64
	var status string
	if err := admin.QueryRow(t.Context(), `SELECT final_score::float8,status FROM submission WHERE id=$1`, sub).Scan(&score, &status); err != nil {
		t.Fatal(err)
	}
	if score != 4 || status != "scored" {
		t.Fatalf("majority not executed: %.3f %s", score, status)
	}
	draft := governanceExpect(t, governanceTestRequest(t, app, subject, "POST", "/api/v1/appeals/draft", map[string]any{"targetType": "submission", "targetId": strconv.FormatInt(sub, 10)}), 201)
	var appeal string
	_ = json.Unmarshal(draft["id"], &appeal)
	governanceExpect(t, governanceTestRequest(t, app, subject, "POST", "/api/v1/appeals/"+appeal+"/submit", map[string]any{"reason": "申请独立小组根据新的证明再次核对。", "score": 6}), 200)
	var appealCase int64
	if err := admin.QueryRow(t.Context(), `SELECT id FROM governance_proposal WHERE class_id=$1 AND action='appeal' AND target_id=$2`, fx.ClassID, appeal).Scan(&appealCase); err != nil {
		t.Fatal(err)
	}
	independent := seats(appealCase)
	governanceExpect(t, governanceTestRequest(t, app, subject, "POST", "/api/v1/appeals/"+appeal+"/withdraw", nil), 409)
	if len(independent) != 5 {
		t.Fatalf("independent appeal needs five seats: %v", independent)
	}
	for _, user := range independent {
		if user == fx.StudentID {
			t.Fatal("self appeal reviewer")
		}
		for _, old := range expanded {
			if user == old {
				t.Fatal("original reviewer assigned to appeal")
			}
		}
	}
	id = appealCase
	for _, user := range independent {
		vote(user, 6)
	}
	if err := admin.QueryRow(t.Context(), `SELECT final_score::float8,status FROM submission WHERE id=$1`, sub).Scan(&score, &status); err != nil {
		t.Fatal(err)
	}
	if score != 6 || status != "scored" {
		t.Fatalf("independent appeal not applied: %v %s", score, status)
	}
	detail := governanceExpect(t, governanceTestRequest(t, app, subject, "GET", "/api/v1/appeals/"+appeal, nil), 200)
	if string(detail["handler"]) != `"共治评审小组"` || string(detail["collective"]) != "true" || detail["handlerId"] != nil || string(detail["handlers"]) != "[]" {
		t.Fatalf("collective appeal exposed execution identity: %v", detail)
	}
	listing := governanceExpect(t, governanceTestRequest(t, app, subject, "GET", "/api/v1/appeals", nil), 200)
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(listing["items"], &items); err != nil || len(items) != 1 || string(items[0]["handler"]) != `"共治评审小组"` || items[0]["handlerId"] != nil {
		t.Fatalf("collective appeal list exposed execution identity: %s", listing["items"])
	}
}

func TestGovernanceActivationWaitsForNoticeAndVotes(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL"))
	fx, ids := governanceFixture(t, admin)
	actor := Actor{UserID: ids[0], ClassID: fx.ClassID, Role: "class_admin"}
	if _, err := admin.Exec(t.Context(), `UPDATE class_governance SET mode='enrolling',profile=NULL,enrollment_open_at=now()-interval '8 days',enrollment_close_at=now()-interval '1 day' WHERE class_id=$1`, fx.ClassID); err != nil {
		t.Fatal(err)
	}
	created := governanceExpect(t, governanceTestRequest(t, app, actor, "POST", "/api/v1/governance/proposals", map[string]any{"requestId": uuid.NewString(), "kind": "activate", "action": "activate", "title": "启动本周期共治", "body": "成员明确同意章程，保留完整独立申诉。", "payload": map[string]any{}}), 201)
	var id string
	_ = json.Unmarshal(created["id"], &id)
	path := "/api/v1/governance/proposals/" + id
	governanceExpect(t, governanceTestRequest(t, app, actor, "POST", path+"/vote", map[string]any{"choice": "yes", "lock": true}), 409)
	if _, err := admin.Exec(t.Context(), `UPDATE governance_proposal SET opens_at=now()-interval '1 hour' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for _, uid := range ids {
		a := actor
		a.UserID = uid
		a.Role = "student"
		governanceExpect(t, governanceTestRequest(t, app, a, "POST", path+"/vote", map[string]any{"choice": "yes", "lock": true}), 200)
	}
	r := governanceExpect(t, governanceTestRequest(t, app, actor, "POST", path+"/close", nil), 200)
	if string(r["status"]) != `"passed"` {
		t.Fatalf("must wait for notice: %s", r["status"])
	}
	var mode string
	if err := admin.QueryRow(t.Context(), `SELECT mode FROM class_governance WHERE class_id=$1`, fx.ClassID).Scan(&mode); err != nil || mode != "enrolling" {
		t.Fatalf("activated too early: %s %v", mode, err)
	}
	if _, err := admin.Exec(t.Context(), `UPDATE governance_proposal SET decided_at=now()-interval '25 hours' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	r = governanceExpect(t, governanceTestRequest(t, app, actor, "POST", path+"/close", nil), 200)
	if string(r["status"]) != `"applied"` {
		t.Fatalf("activation not applied: %s", r["status"])
	}
	if err := admin.QueryRow(t.Context(), `SELECT mode FROM class_governance WHERE class_id=$1`, fx.ClassID).Scan(&mode); err != nil || mode != "collective" {
		t.Fatalf("activation failed: %s %v", mode, err)
	}
	governanceExpect(t, governanceTestRequest(t, app, actor, "POST", "/api/v1/admin/settle", nil), 403)
}
