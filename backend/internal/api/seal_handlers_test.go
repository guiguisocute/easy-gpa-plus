package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func TestUnsealRequiresFourCharacters(t *testing.T) {
	for _, reason := range []string{"", "   ", "补交", "补交呀", "abc"} {
		router := gin.New()
		router.POST("/seals/:uid/unseal", (&Server{}).unsealStudent)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/seals/2/unseal", strings.NewReader(fmt.Sprintf(`{"reason":%q}`, reason))))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("reason %q: got %d", reason, response.Code)
		}
	}
}

// Only temporary tables are touched; the real database is used for SQL and
// transaction semantics, never for fixture business state.
func unsealTestTx(t *testing.T) (pgx.Tx, *pgx.Conn) {
	t.Helper()
	url := os.Getenv("EASYGPA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_DATABASE_URL to test unseal transactions")
	}
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err = tx.Exec(t.Context(), `
		SELECT set_config('app.current_class','1',true);
		CREATE TEMP TABLE seal (id bigint,class_id bigint,student_id bigint,sealed_at timestamptz DEFAULT now(),unsealed_at timestamptz,unsealed_by bigint,unseal_reason text) ON COMMIT DROP;
		CREATE TEMP TABLE scorecard_audit_batch (id uuid,class_id bigint,student_id bigint,status text,created_at timestamptz DEFAULT now(),stale_at timestamptz,stale_reason text,invalidated_at timestamptz,invalidated_reason text) ON COMMIT DROP;
		CREATE TEMP TABLE outbox_event (class_id bigint,type text,payload jsonb) ON COMMIT DROP;
		CREATE TEMP TABLE audit_log (class_id bigint,actor_id bigint,actor_role text,action text,resource_type text,resource_id text,before_data jsonb,after_data jsonb,metadata jsonb,ip_address inet,user_agent text) ON COMMIT DROP;
		CREATE TEMP TABLE settlement_run (id bigint,class_id bigint,status text,created_at timestamptz DEFAULT now()) ON COMMIT DROP;
		CREATE TEMP TABLE settlement_invalidation (class_id bigint,run_id bigint,reason text) ON COMMIT DROP;
		INSERT INTO seal (id,class_id,student_id) VALUES (1,1,2),(2,1,3),(3,2,2);
		INSERT INTO settlement_run (id,class_id,status) VALUES (1,1,'complete');
		INSERT INTO scorecard_audit_batch (id,class_id,student_id,status)
		SELECT lpad(n::text,32,'0')::uuid,1,2,s FROM unnest(ARRAY['generating','blocked','open','resolving','complete','stale','failed']) WITH ORDINALITY AS x(s,n);
		INSERT INTO scorecard_audit_batch (id,class_id,student_id,status) VALUES
		('00000000-0000-0000-0000-000000000008',1,3,'complete'),
		('00000000-0000-0000-0000-000000000009',2,2,'complete');
	`)
	if err != nil {
		t.Fatal(err)
	}
	return tx, conn
}

func unsealTestRouter(tx pgx.Tx) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, Actor{ClassID: 1, UserID: 4, Role: "class_admin"})
		c.Set(txContextKey, tx)
	})
	router.POST("/seals/:uid/unseal", (&Server{}).unsealStudent)
	return router
}

func TestUnsealInvalidatesOnlyTargetIncludingCompleted(t *testing.T) {
	tx, _ := unsealTestTx(t)
	response := httptest.NewRecorder()
	unsealTestRouter(tx).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/seals/2/unseal", strings.NewReader(`{"reason":"  补交证明  "}`)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unseal: %d %s", response.Code, response.Body)
	}
	var invalidated, untouched, events, audits, settlements int
	var storedReason string
	err := tx.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM scorecard_audit_batch WHERE class_id=1 AND student_id=2 AND invalidated_at IS NOT NULL AND status='stale'),
		(SELECT count(*) FROM scorecard_audit_batch WHERE (class_id=2 OR student_id=3) AND status='complete'),
		(SELECT count(*) FROM outbox_event WHERE type='scorecard_audit.stale'),
		(SELECT count(*) FROM audit_log WHERE action='seal.unsealed'),
		(SELECT count(*) FROM settlement_invalidation WHERE run_id=1),
		(SELECT unseal_reason FROM seal WHERE id=1)
	`).Scan(&invalidated, &untouched, &events, &audits, &settlements, &storedReason)
	if err != nil {
		t.Fatal(err)
	}
	if invalidated != 5 || untouched != 2 || events != 5 || audits != 1 || settlements != 1 || storedReason != "补交证明" {
		t.Fatalf("unexpected invalidation: %d %d %d %d %d %q", invalidated, untouched, events, audits, settlements, storedReason)
	}
	duplicate := httptest.NewRecorder()
	unsealTestRouter(tx).ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/seals/2/unseal", strings.NewReader(`{"reason":"重复解封"}`)))
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate unseal: %d %s", duplicate.Code, duplicate.Body)
	}
}

func TestUnsealWaitsForSnapshotProducers(t *testing.T) {
	for _, key := range []string{"easygpa:settlement:1", "easygpa:blind-audit-student:1:2"} {
		t.Run(key, func(t *testing.T) {
			tx, writer := unsealTestTx(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			blocker, err := pgx.Connect(ctx, os.Getenv("EASYGPA_TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Close(context.Background())
			hold, err := blocker.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback(context.Background())
			if _, err := hold.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				unsealTestRouter(tx).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/seals/2/unseal", strings.NewReader(`{"reason":"补交证明"}`)).WithContext(ctx))
			}()
			for {
				var waiting bool
				if err := hold.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, writer.PgConn().PID()).Scan(&waiting); err != nil {
					cancel()
					<-done
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case <-done:
					t.Fatalf("unseal bypassed snapshot lock: %d", response.Code)
				default:
					time.Sleep(5 * time.Millisecond)
				}
			}
			if err := hold.Rollback(ctx); err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}
			<-done
			if response.Code != http.StatusNoContent {
				t.Fatalf("unseal after snapshot lock: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestUnsealRepairMigrationPreservesNewRoundsAndOtherStudents(t *testing.T) {
	tx, _ := unsealTestTx(t)
	_, err := tx.Exec(t.Context(), `
		UPDATE scorecard_audit_batch SET created_at=now()-interval '2 hours';
		UPDATE seal SET unsealed_at=now()-interval '1 hour' WHERE id=1;
		-- A new valid round after resealing must remain intact.
		UPDATE scorecard_audit_batch SET created_at=now() WHERE id='00000000-0000-0000-0000-000000000001';
	`)
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../db/migrations/000057_unseal_invalidates_completed_audits.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := tx.Exec(t.Context(), string(script)); err != nil {
			t.Fatal(err)
		}
	}
	var invalidated, preserved, logs, settlements int
	err = tx.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM scorecard_audit_batch WHERE invalidated_at IS NOT NULL),
		(SELECT count(*) FROM scorecard_audit_batch WHERE status IN ('generating','complete')),
		(SELECT count(*) FROM audit_log WHERE metadata->>'migration'='57'),
		(SELECT count(*) FROM settlement_invalidation)
	`).Scan(&invalidated, &preserved, &logs, &settlements)
	if err != nil {
		t.Fatal(err)
	}
	if invalidated != 4 || preserved != 3 || logs != 4 || settlements != 1 {
		t.Fatalf("migration scope/idempotence: %d %d %d %d", invalidated, preserved, logs, settlements)
	}
}
