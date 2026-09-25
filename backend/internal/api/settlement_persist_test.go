package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/store"
)

func settlementAdminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL")
	if raw == "" {
		t.Skip("set EASYGPA_TEST_ADMIN_DATABASE_URL to run settlement database regression")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/easygpa_e2e" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		t.Fatal("settlement regression requires the disposable loopback easygpa_e2e database")
	}
	pool, err := pgxpool.New(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func settlementAppPool(t *testing.T, adminURL string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE easygpa_app")
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type settlementFixture struct {
	ClassID        int64
	AdminID        int64
	StudentID      int64
	UnregisteredID int64
	SchemeID       int64
}

func seedSettlementClass(t *testing.T, admin *pgxpool.Pool, force bool) settlementFixture {
	t.Helper()
	ctx := t.Context()
	var classID int64
	if err := admin.QueryRow(ctx, `INSERT INTO class(name,archived) VALUES('settlement fixture',false) RETURNING id`).Scan(&classID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := admin.Begin(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = tx.Rollback(cleanup) }()
		if _, err := tx.Exec(cleanup, `SET LOCAL session_replication_role = replica`); err != nil {
			t.Error(err)
			return
		}
		// Replica mode bypasses immutable-history guards AND FK cascades. Delete
		// every tenant row explicitly before its class, otherwise fixtures leak
		// orphaned members, snapshots, audits and outbox events between tests.
		rows, err := tx.Query(cleanup, `SELECT table_name FROM information_schema.columns
			WHERE table_schema='public' AND column_name='class_id' ORDER BY table_name`)
		if err != nil {
			t.Error(err)
			return
		}
		tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Error(err)
			return
		}
		for _, table := range tables {
			if _, err := tx.Exec(cleanup, "DELETE FROM "+pgx.Identifier{"public", table}.Sanitize()+" WHERE class_id=$1", classID); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := tx.Exec(cleanup, `DELETE FROM class WHERE id=$1`, classID); err != nil {
			t.Error(err)
			return
		}
		if err := tx.Commit(cleanup); err != nil {
			t.Error(err)
		}
	})
	adminSID := fmt.Sprintf("SETTLE-ADMIN-%d", classID)
	studentSID := fmt.Sprintf("SETTLE-STU-%d", classID)
	unregSID := fmt.Sprintf("SETTLE-UNREG-%d", classID)
	var adminID, studentID, unregisteredID int64
	if err := admin.QueryRow(ctx, `INSERT INTO whitelist(class_id,sid,name,role) VALUES($1,$2,'结算班管','class_admin') RETURNING id`, classID, adminSID).Scan(new(int64)); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT id FROM app_user WHERE class_id=$1 AND sid=$2`, classID, adminSID).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `INSERT INTO whitelist(class_id,sid,name,role) VALUES($1,$2,'结算学生','student') RETURNING id`, classID, studentSID).Scan(new(int64)); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT id FROM app_user WHERE class_id=$1 AND sid=$2`, classID, studentSID).Scan(&studentID); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `INSERT INTO whitelist(class_id,sid,name,role) VALUES($1,$2,'未注册成员','student') RETURNING id`, classID, unregSID).Scan(new(int64)); err != nil {
		t.Fatal(err)
	}
	var unregisteredHasNoPassword bool
	if err := admin.QueryRow(ctx, `SELECT id,password_hash IS NULL FROM app_user WHERE class_id=$1 AND sid=$2`, classID, unregSID).Scan(&unregisteredID, &unregisteredHasNoPassword); err != nil {
		t.Fatal(err)
	}
	if !unregisteredHasNoPassword {
		t.Fatal("unregistered fixture must have no password")
	}
	cfg := scheme.DefaultSelfReportConfig("v1")
	min, max := 0.0, 10.0
	for i := range cfg.Categories {
		if cfg.Categories[i].Key == "moral" {
			cfg.Categories[i].Items = []scheme.Item{{
				Key: "fixture_activity", Name: "合成加分项",
				ScoreRule: scheme.ScoreRule{Type: "free", Min: &min, Max: &max},
			}}
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var schemeID int64
	if err := admin.QueryRow(ctx, `
		INSERT INTO scheme (class_id,name,version,status,config,created_by,published_at)
		VALUES ($1,'settlement fixture',1,'published',$2,$3,now()) RETURNING id
	`, classID, raw, adminID).Scan(&schemeID); err != nil {
		t.Fatal(err)
	}
	if force {
		if _, err := admin.Exec(ctx, `
			INSERT INTO gate_override (class_id,scheme_id,reason,created_by)
			VALUES ($1,$2,'fixture forced gate for settlement persist test',$3)
		`, classID, schemeID, adminID); err != nil {
			t.Fatal(err)
		}
	}
	return settlementFixture{ClassID: classID, AdminID: adminID, StudentID: studentID, UnregisteredID: unregisteredID, SchemeID: schemeID}
}

func settlementActor(fx settlementFixture) Actor {
	return Actor{UserID: fx.AdminID, ClassID: fx.ClassID, Role: "class_admin"}
}

func persistForcedSettlement(t *testing.T, app *pgxpool.Pool, fx settlementFixture) settlementResult {
	t.Helper()
	actor := settlementActor(fx)
	var result settlementResult
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		var runErr error
		result, runErr = runSettlementTx(t.Context(), tx, actor, "127.0.0.1", "settlement-test", time.Now())
		return runErr
	}); err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.RunID == 0 {
		t.Fatalf("first persist = %#v", result)
	}
	return result
}

func assertSettlementReuse(t *testing.T, app *pgxpool.Pool, fx settlementFixture, previous int64, wantReuse bool) settlementResult {
	t.Helper()
	actor := settlementActor(fx)
	var result settlementResult
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		var runErr error
		result, runErr = runSettlementTx(t.Context(), tx, actor, "127.0.0.1", "settlement-test", time.Now())
		return runErr
	}); err != nil {
		t.Fatal(err)
	}
	if wantReuse {
		if !result.Reused || result.RunID != previous {
			t.Fatalf("want reuse of %d, got %#v", previous, result)
		}
		return result
	}
	if result.Reused || result.RunID == 0 || result.RunID == previous {
		t.Fatalf("want a new run after %d, got %#v", previous, result)
	}
	return result
}

func insertScoredSubmission(t *testing.T, admin *pgxpool.Pool, fx settlementFixture, studentID int64, score float64) int64 {
	t.Helper()
	min, max := 0.0, 10.0
	snapshot, err := json.Marshal(ruleSnapshot{
		SchemeID: strconv.FormatInt(fx.SchemeID, 10), Version: "v1",
		CategoryKey: "moral", CategoryName: "思想道德素质",
		Item:       scheme.Item{Key: "fixture_activity", Name: "合成加分项", ScoreRule: scheme.ScoreRule{Type: "free", Min: &min, Max: &max}},
		CapturedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := json.Marshal(map[string]float64{"score": score})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := admin.QueryRow(t.Context(), `
		INSERT INTO submission (
			class_id,student_id,scheme_id,scheme_version,category_key,item_key,
			filed_category_key,filed_item_key,title,claim,requested_score,final_score,
			status,source,rule_snapshot,filed_rule_snapshot,markdown_note,submitted_at,scored_at
		) VALUES ($1,$2,$3,1,'moral','fixture_activity','moral','fixture_activity','合成材料',$4,$5,$5,'scored','manual',$6,$6,'fixture',now(),now())
		RETURNING id
	`, fx.ClassID, studentID, fx.SchemeID, claim, score, snapshot).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func serveClassHTTP(t *testing.T, app *pgxpool.Pool, actor Actor, register func(*gin.Engine), method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	tx, err := store.BeginTenant(t.Context(), app, actor.ClassID)
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(t.Context())
		}
	}()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, actor)
		c.Set(txContextKey, tx)
	})
	register(router)
	response := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, req)
	if response.Code >= 400 {
		return response
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	committed = true
	return response
}

func countClassRows(t *testing.T, admin *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertClassTrail(t *testing.T, admin *pgxpool.Pool, classID int64, action, eventType, resourceID string, want int) {
	t.Helper()
	audits := countClassRows(t, admin, `SELECT count(*) FROM audit_log WHERE class_id=$1 AND action=$2 AND ($3='' OR resource_id=$3)`, classID, action, resourceID)
	events := 0
	if eventType != "" {
		events = countClassRows(t, admin, `SELECT count(*) FROM outbox_event WHERE class_id=$1 AND type=$2`, classID, eventType)
	}
	if audits != want || (eventType != "" && events != want) {
		t.Fatalf("trail action=%s type=%s audits=%d events=%d want=%d", action, eventType, audits, events, want)
	}
}

func TestSettlementFixtureCleanupKeepsOtherClasses(t *testing.T) {
	admin := settlementAdminPool(t)
	kept := seedSettlementClass(t, admin, true)
	var removed settlementFixture
	t.Run("disposable", func(t *testing.T) {
		removed = seedSettlementClass(t, admin, true)
		app := settlementAppPool(t, os.Getenv("EASYGPA_TEST_ADMIN_DATABASE_URL"))
		persistForcedSettlement(t, app, removed)
	})
	for _, table := range []string{"app_user", "whitelist", "scheme", "settlement_run", "settlement", "audit_log", "outbox_event"} {
		query := "SELECT count(*) FROM " + pgx.Identifier{table}.Sanitize() + " WHERE class_id=$1"
		if n := countClassRows(t, admin, query, removed.ClassID); n != 0 {
			t.Errorf("fixture cleanup left %d rows in %s", n, table)
		}
	}
	if n := countClassRows(t, admin, "SELECT count(*) FROM app_user WHERE class_id=$1", kept.ClassID); n != 3 {
		t.Fatalf("fixture cleanup changed the other class: %d members", n)
	}
}

func TestRunSettlementTxRejectsClosedGate(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, false)
	err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		_, runErr := runSettlementTx(t.Context(), tx, Actor{UserID: fx.AdminID, ClassID: fx.ClassID, Role: "class_admin"}, "127.0.0.1", "settlement-test", time.Now())
		return runErr
	})
	var closed *gateClosedError
	if !errors.As(err, &closed) {
		t.Fatalf("err=%v, want gateClosedError", err)
	}
}

func TestRunSettlementTxPersistsReusesAndRollsBack(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	classID, adminID, studentID := fx.ClassID, fx.AdminID, fx.StudentID
	actor := Actor{UserID: adminID, ClassID: classID, Role: "class_admin"}
	now := time.Now()

	tx, err := store.BeginTenant(t.Context(), app, classID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runSettlementTx(t.Context(), tx, actor, "127.0.0.1", "settlement-test", now); err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatal(err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var leftover int
	if err := admin.QueryRow(t.Context(), `SELECT count(*) FROM settlement_run WHERE class_id=$1`, classID).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("rolled-back run left %d settlement_run rows", leftover)
	}

	var committed settlementResult
	if err := store.InTenantTx(t.Context(), app, classID, func(tx pgx.Tx) error {
		var runErr error
		committed, runErr = runSettlementTx(t.Context(), tx, actor, "127.0.0.1", "settlement-test", now)
		return runErr
	}); err != nil {
		t.Fatal(err)
	}
	if committed.Reused || committed.RunID == 0 || len(committed.Items) != 3 {
		t.Fatalf("first persist = %#v", committed)
	}

	var reused settlementResult
	if err := store.InTenantTx(t.Context(), app, classID, func(tx pgx.Tx) error {
		var runErr error
		reused, runErr = runSettlementTx(t.Context(), tx, actor, "127.0.0.1", "settlement-test", now)
		return runErr
	}); err != nil {
		t.Fatal(err)
	}
	if !reused.Reused || reused.RunID != committed.RunID {
		t.Fatalf("reuse = %#v, first=%#v", reused, committed)
	}

	var runs, scores, events, audits int
	if err := admin.QueryRow(t.Context(), `
		SELECT
		  (SELECT count(*) FROM settlement_run WHERE class_id=$1 AND status='complete'),
		  (SELECT count(*) FROM settlement WHERE class_id=$1),
		  (SELECT count(*) FROM outbox_event WHERE class_id=$1 AND type='settlement.done'),
		  (SELECT count(*) FROM audit_log WHERE class_id=$1 AND action='settlement.completed')
	`, classID).Scan(&runs, &scores, &events, &audits); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || scores != 3 || events != 1 || audits != 1 {
		t.Fatalf("persist counts run=%d scores=%d events=%d audits=%d", runs, scores, events, audits)
	}
	var scoredStudent, scoredUnreg int64
	if err := admin.QueryRow(t.Context(), `SELECT student_id FROM settlement WHERE class_id=$1 AND student_id=$2`, classID, studentID).Scan(&scoredStudent); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(t.Context(), `SELECT student_id FROM settlement WHERE class_id=$1 AND student_id=$2`, classID, fx.UnregisteredID).Scan(&scoredUnreg); err != nil {
		t.Fatal(err)
	}
}

func TestRunSettlementTxForcedMissingGPAKeepsUnregisteredAndMarksGap(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	if _, err := admin.Exec(t.Context(), `
		INSERT INTO student_gpa (class_id,student_id,scheme_id,score,details,import_batch,imported_by)
		VALUES ($1,$2,$3,88,'{"source":"fixture"}',gen_random_uuid(),$4)
	`, fx.ClassID, fx.StudentID, fx.SchemeID, fx.AdminID); err != nil {
		t.Fatal(err)
	}
	var result settlementResult
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		var runErr error
		result, runErr = runSettlementTx(t.Context(), tx, Actor{UserID: fx.AdminID, ClassID: fx.ClassID, Role: "class_admin"}, "127.0.0.1", "settlement-test", time.Now())
		return runErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items=%d, want 3 including unregistered member", len(result.Items))
	}
	var missingAdmin, missingUnreg, missingStudent bool
	rows, err := admin.Query(t.Context(), `SELECT student_id,details FROM settlement WHERE class_id=$1`, fx.ClassID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		var details map[string]any
		if err := json.Unmarshal(raw, &details); err != nil {
			t.Fatal(err)
		}
		_, gap := details["gpaMissing"]
		switch id {
		case fx.AdminID:
			missingAdmin = gap
		case fx.UnregisteredID:
			missingUnreg = gap
		case fx.StudentID:
			missingStudent = gap
		}
	}
	if !missingAdmin || !missingUnreg || missingStudent {
		t.Fatalf("gpaMissing admin=%v unreg=%v student=%v", missingAdmin, missingUnreg, missingStudent)
	}
}

func TestRunSettlementTxUnsealInvalidatesReuse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	actor := Actor{UserID: fx.AdminID, ClassID: fx.ClassID, Role: "class_admin"}

	var first settlementResult
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		var runErr error
		first, runErr = runSettlementTx(t.Context(), tx, actor, "127.0.0.1", "settlement-test", time.Now())
		return runErr
	}); err != nil {
		t.Fatal(err)
	}
	if first.Reused || first.RunID == 0 {
		t.Fatalf("first persist = %#v", first)
	}

	if _, err := admin.Exec(t.Context(), `INSERT INTO seal (class_id,student_id,source) VALUES ($1,$2,'manual')`, fx.ClassID, fx.StudentID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `
		INSERT INTO scorecard_audit_batch (class_id,scheme_id,student_id,input_hash,status,random_seed,detail)
		VALUES ($1,$2,$3,repeat('a',64),'complete',1,'{}')
	`, fx.ClassID, fx.SchemeID, fx.StudentID); err != nil {
		t.Fatal(err)
	}

	tx, err := store.BeginTenant(t.Context(), app, fx.ClassID)
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(t.Context())
		}
	}()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, actor)
		c.Set(txContextKey, tx)
	})
	router.POST("/seals/:uid/unseal", (&Server{}).unsealStudent)
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/seals/%d/unseal", fx.StudentID), strings.NewReader(`{"reason":"补交证明材料"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("unseal: %d %s", response.Code, response.Body.String())
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	committed = true

	var invalidations, staleBatches int
	if err := admin.QueryRow(t.Context(), `
		SELECT
		  (SELECT count(*) FROM settlement_invalidation WHERE class_id=$1 AND run_id=$2),
		  (SELECT count(*) FROM scorecard_audit_batch WHERE class_id=$1 AND student_id=$3 AND status='stale' AND invalidated_at IS NOT NULL)
	`, fx.ClassID, first.RunID, fx.StudentID).Scan(&invalidations, &staleBatches); err != nil {
		t.Fatal(err)
	}
	if invalidations != 1 || staleBatches != 1 {
		t.Fatalf("invalidations=%d staleBatches=%d", invalidations, staleBatches)
	}

	var second settlementResult
	if err := store.InTenantTx(t.Context(), app, fx.ClassID, func(tx pgx.Tx) error {
		var runErr error
		second, runErr = runSettlementTx(t.Context(), tx, actor, "127.0.0.1", "settlement-test", time.Now())
		return runErr
	}); err != nil {
		t.Fatal(err)
	}
	if second.Reused || second.RunID == 0 || second.RunID == first.RunID {
		t.Fatalf("after unseal reuse=%v run=%d first=%d", second.Reused, second.RunID, first.RunID)
	}
}

func TestRunSettlementTxForceRejectAndScoreInvalidateReuse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	rejectID := insertScoredSubmission(t, admin, fx, fx.StudentID, 4)
	first := persistForcedSettlement(t, app, fx)

	rejected := serveClassHTTP(t, app, settlementActor(fx), func(r *gin.Engine) {
		r.POST("/api/v1/admin/submissions/:id/force-reject", (&Server{}).forceRejectSubmission)
	}, http.MethodPost, fmt.Sprintf("/api/v1/admin/submissions/%d/force-reject", rejectID), `{"reason":"材料与事实不符"}`)
	if rejected.Code != http.StatusOK {
		t.Fatalf("force reject: %d %s", rejected.Code, rejected.Body.String())
	}
	var invalidations int
	var score float64
	if err := admin.QueryRow(t.Context(), `
		SELECT (SELECT count(*) FROM settlement_invalidation WHERE class_id=$1 AND run_id=$2),
		       (SELECT final_score::float8 FROM submission WHERE id=$3)
	`, fx.ClassID, first.RunID, rejectID).Scan(&invalidations, &score); err != nil {
		t.Fatal(err)
	}
	if invalidations != 1 || score != 0 {
		t.Fatalf("after reject invalidations=%d score=%v", invalidations, score)
	}
	assertClassTrail(t, admin, fx.ClassID, "submission.force_rejected", "submission.force_rejected", strconv.FormatInt(rejectID, 10), 1)
	second := assertSettlementReuse(t, app, fx, first.RunID, false)

	scoreID := insertScoredSubmission(t, admin, fx, fx.UnregisteredID, 4)
	scored := serveClassHTTP(t, app, settlementActor(fx), func(r *gin.Engine) {
		r.POST("/api/v1/admin/submissions/:id/force-score", (&Server{}).forceScoreSubmission)
	}, http.MethodPost, fmt.Sprintf("/api/v1/admin/submissions/%d/force-score", scoreID), `{"reason":"核对后改分","score":3,"previousScore":4}`)
	if scored.Code != http.StatusOK {
		t.Fatalf("force score: %d %s", scored.Code, scored.Body.String())
	}
	assertClassTrail(t, admin, fx.ClassID, "submission.force_scored", "submission.force_scored", strconv.FormatInt(scoreID, 10), 1)
	assertSettlementReuse(t, app, fx, second.RunID, false)
}

func TestRunSettlementTxRecusalKeepsSettlementAndSelfRejectInvalidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	ownID := insertScoredSubmission(t, admin, fx, fx.AdminID, 4)
	studentID := insertScoredSubmission(t, admin, fx, fx.StudentID, 4)
	first := persistForcedSettlement(t, app, fx)

	recused := serveClassHTTP(t, app, settlementActor(fx), func(r *gin.Engine) {
		r.POST("/api/v1/admin/submissions/:id/force-reject", (&Server{}).forceRejectSubmission)
	}, http.MethodPost, fmt.Sprintf("/api/v1/admin/submissions/%d/force-reject", ownID), `{"reason":"班管不能驳回自己"}`)
	if recused.Code != http.StatusForbidden || !strings.Contains(recused.Body.String(), "avoid_self") {
		t.Fatalf("recusal: %d %s", recused.Code, recused.Body.String())
	}
	assertClassTrail(t, admin, fx.ClassID, "submission.force_rejected", "submission.force_rejected", strconv.FormatInt(ownID, 10), 0)
	assertSettlementReuse(t, app, fx, first.RunID, true)

	self := serveClassHTTP(t, app, Actor{UserID: fx.StudentID, ClassID: fx.ClassID, Role: "student"}, func(r *gin.Engine) {
		r.POST("/api/v1/submissions/:id/force-reject", (&Server{}).selfForceRejectSubmission)
	}, http.MethodPost, fmt.Sprintf("/api/v1/submissions/%d/force-reject", studentID), `{"reason":"本人放弃该项分数"}`)
	if self.Code != http.StatusOK {
		t.Fatalf("self reject: %d %s", self.Code, self.Body.String())
	}
	if countClassRows(t, admin, `SELECT count(*) FROM audit_log WHERE class_id=$1 AND action='submission.force_rejected' AND resource_id=$2 AND metadata->>'selfRejected'='true'`, fx.ClassID, strconv.FormatInt(studentID, 10)) != 1 {
		t.Fatal("self reject missing selfRejected audit")
	}
	assertClassTrail(t, admin, fx.ClassID, "submission.force_rejected", "submission.force_rejected", strconv.FormatInt(studentID, 10), 1)
	assertSettlementReuse(t, app, fx, first.RunID, false)
}

func TestRunSettlementTxBonusGrantInvalidatesAndIncludesUnregistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	first := persistForcedSettlement(t, app, fx)
	body := fmt.Sprintf(`{"requestId":%q,"schemeVersion":"v1","studentIds":[%d,%d],"category":"moral","itemKey":"fixture_activity","title":"统一活动","note":"已核实本批成员参与活动","claim":{"score":4}}`,
		uuid.NewString(), fx.StudentID, fx.UnregisteredID)
	granted := serveClassHTTP(t, app, settlementActor(fx), func(r *gin.Engine) {
		r.POST("/api/v1/admin/bonus-grants", (&Server{}).createBonusGrant)
	}, http.MethodPost, "/api/v1/admin/bonus-grants", body)
	if granted.Code != http.StatusCreated {
		t.Fatalf("bonus grant: %d %s", granted.Code, granted.Body.String())
	}
	var grants, invalidations int
	if err := admin.QueryRow(t.Context(), `
		SELECT (SELECT count(*) FROM submission WHERE class_id=$1 AND source='admin_grant'),
		       (SELECT count(*) FROM settlement_invalidation WHERE class_id=$1 AND run_id=$2)
	`, fx.ClassID, first.RunID).Scan(&grants, &invalidations); err != nil {
		t.Fatal(err)
	}
	if grants != 2 || invalidations != 1 {
		t.Fatalf("grants=%d invalidations=%d", grants, invalidations)
	}
	assertClassTrail(t, admin, fx.ClassID, "submission.admin_granted", "", "", 2)
	assertSettlementReuse(t, app, fx, first.RunID, false)
}

func TestRunSettlementTxLockdownBlocksForceRejectAllowsSettle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	submissionID := insertScoredSubmission(t, admin, fx, fx.StudentID, 4)
	first := persistForcedSettlement(t, app, fx)
	if _, err := admin.Exec(t.Context(), `
		UPDATE class_timeline
		   SET open_at=now()-interval '3 days', close_at=now()-interval '2 hours', lockdown_at=now()-interval '1 hour'
		 WHERE class_id=$1
	`, fx.ClassID); err != nil {
		t.Fatal(err)
	}

	tx, err := store.BeginTenant(t.Context(), app, fx.ClassID)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, settlementActor(fx))
		c.Set(txContextKey, tx)
	})
	group := router.Group("/api/v1")
	group.Use((&Server{}).classLockdownMiddleware())
	group.POST("/admin/submissions/:id/force-reject", (&Server{}).forceRejectSubmission)
	group.POST("/admin/settle", func(c *gin.Context) {
		result, runErr := runSettlementTx(c.Request.Context(), mustTx(c), mustActor(c), "127.0.0.1", "settlement-test", time.Now())
		if runErr != nil {
			writeServiceError(c, runErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"runId": result.RunID, "reused": result.Reused})
	})
	rejected := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/admin/submissions/%d/force-reject", submissionID), strings.NewReader(`{"reason":"封锁后仍想驳回"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rejected, req)
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "class_locked") {
		t.Fatalf("lockdown reject: %d %s", rejected.Code, rejected.Body.String())
	}
	settled := httptest.NewRecorder()
	settleReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/settle", nil)
	router.ServeHTTP(settled, settleReq)
	if settled.Code != http.StatusOK || !strings.Contains(settled.Body.String(), `"reused":true`) {
		t.Fatalf("lockdown settle: %d %s", settled.Code, settled.Body.String())
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertSettlementReuse(t, app, fx, first.RunID, true)
}
