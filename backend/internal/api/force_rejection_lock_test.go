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

// A temporary draft is enough to exercise both snapshot locks before the
// business guard returns. No persistent roster, submission or score is read
// or changed. Check PostgreSQL's waiting lock, not an arbitrary sleep window.
func TestForcedCorrectionWaitsForSnapshotProducers(t *testing.T) {
	url := os.Getenv("EASYGPA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_DATABASE_URL to test snapshot lock serialization")
	}
	for _, scenario := range []string{"reject/settlement", "reject/blind-audit", "score/settlement", "score/blind-audit"} {
		operation, producer, _ := strings.Cut(scenario, "/")
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			blocker, err := pgx.Connect(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Close(context.Background())
			writer, err := pgx.Connect(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close(context.Background())
			hold, err := blocker.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback(context.Background())
			tx, err := writer.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			classID := time.Now().UnixNano()
			if _, err := tx.Exec(ctx, `CREATE TEMP TABLE submission (
			 id bigint,class_id bigint,student_id bigint,scheme_id bigint,status text,title text,final_score numeric,force_rejection jsonb
			) ON COMMIT DROP`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO submission VALUES(1,$1,2,3,'draft','synthetic draft',NULL,NULL)`, classID); err != nil {
				t.Fatal(err)
			}
			key := fmt.Sprintf("easygpa:settlement:%d", classID)
			if producer == "blind-audit" {
				key = fmt.Sprintf("easygpa:blind-audit-student:%d:2", classID)
			}
			if _, err := hold.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(actorContextKey, Actor{ClassID: classID, UserID: 4, Role: "class_admin"})
				c.Set(txContextKey, tx)
			})
			server := &Server{}
			handler := server.forceRejectSubmission
			expectedCode := "submission_not_submitted"
			if operation == "score" {
				handler = server.forceScoreSubmission
				expectedCode = "force_score_unavailable"
			}
			router.POST("/submissions/:id/force-reject", handler)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/submissions/1/force-reject", strings.NewReader(`{"reason":"材料核对","score":3,"previousScore":4}`)).WithContext(ctx))
			}()
			waiting := false
			for !waiting {
				if err := hold.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, writer.PgConn().PID()).Scan(&waiting); err != nil {
					cancel()
					<-done
					t.Fatal(err)
				}
				select {
				case <-done:
					t.Fatalf("request bypassed %s snapshot lock: %d %s", producer, response.Code, response.Body)
				default:
				}
				if !waiting {
					time.Sleep(5 * time.Millisecond)
				}
			}
			if err := hold.Rollback(ctx); err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}
			<-done
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), expectedCode) {
				t.Fatalf("request did not resume after %s lock released: %d %s", producer, response.Code, response.Body)
			}
		})
	}
}
