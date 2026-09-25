package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// These PostgreSQL tests use transaction-local temporary tables and synthetic
// members only; they never read or change the development class roster.
func passwordResetTestTx(t *testing.T) pgx.Tx {
	t.Helper()
	url := os.Getenv("EASYGPA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set EASYGPA_TEST_DATABASE_URL to run password reset database tests")
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
		CREATE TEMP TABLE whitelist (
		 id bigint PRIMARY KEY, class_id bigint, registered_at timestamptz, updated_at timestamptz
		) ON COMMIT DROP;
		CREATE TEMP TABLE app_user (
		 id bigint PRIMARY KEY, whitelist_id bigint, class_id bigint, sid text, name text,
		 role text, status text, password_hash text, token_version bigint,
		 last_login_at timestamptz, updated_at timestamptz
		) ON COMMIT DROP;
		CREATE TEMP TABLE user_email (user_id bigint, verified_at timestamptz) ON COMMIT DROP;
		CREATE TEMP TABLE audit_log (
		 class_id bigint, actor_id bigint, actor_role text, action text, resource_type text,
		 resource_id text, before_data jsonb, after_data jsonb, metadata jsonb,
		 ip_address inet, user_agent text
		) ON COMMIT DROP;
		INSERT INTO app_user
		 SELECT id,id,CASE WHEN id=6 THEN 20 ELSE 10 END,'test-'||id,'Test member '||id,
		        CASE WHEN id IN (2,6) THEN 'group' WHEN id=3 THEN 'class_admin' ELSE 'student' END,
		        CASE WHEN id=4 THEN 'disabled' ELSE 'active' END,
		        CASE WHEN id=5 THEN NULL ELSE 'existing-hash' END,7,'2026-01-02 UTC',now()
		 FROM generate_series(1,6) AS id;
		INSERT INTO whitelist
		 SELECT id,class_id,CASE WHEN password_hash IS NOT NULL THEN '2026-01-01 UTC'::timestamptz END,now()
		 FROM app_user;
		INSERT INTO user_email SELECT id,'2026-01-01 UTC' FROM app_user;
	`)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func callPasswordReset(t *testing.T, tx pgx.Tx, route, path string, bulk bool) *httptest.ResponseRecorder {
	t.Helper()
	server := &Server{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, Actor{ClassID: 10, UserID: 3, Role: "class_admin"})
		c.Set(txContextKey, tx)
	})
	if bulk {
		router.POST(route, server.resetClassUserPasswords)
	} else {
		router.POST(route, server.resetUserPassword)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
	return response
}

func assertPasswordResetMembers(t *testing.T, tx pgx.Tx, resetIDs ...int64) {
	t.Helper()
	for id := int64(1); id <= 6; id++ {
		reset := false
		for _, resetID := range resetIDs {
			reset = reset || id == resetID
		}
		var passwordSet, registered, lastLoginPreserved bool
		var version, emailCount int64
		var role, status string
		err := tx.QueryRow(t.Context(), `
		 SELECT u.password_hash IS NOT NULL,w.registered_at IS NOT NULL,u.token_version,
		        u.role,u.status,u.last_login_at='2026-01-02 UTC'::timestamptz,
		        (SELECT count(*) FROM user_email e WHERE e.user_id=u.id)
		 FROM app_user u JOIN whitelist w ON w.id=u.whitelist_id WHERE u.id=$1
		`, id).Scan(&passwordSet, &registered, &version, &role, &status, &lastLoginPreserved, &emailCount)
		if err != nil {
			t.Fatal(err)
		}
		wantRole, wantStatus, wantVersion := "student", "active", int64(7)
		if id == 2 || id == 6 {
			wantRole = "group"
		} else if id == 3 {
			wantRole = "class_admin"
		} else if id == 4 {
			wantStatus = "disabled"
		}
		if reset {
			wantVersion++
		}
		wantRegistered := id != 5 && !reset
		if passwordSet != wantRegistered || registered != wantRegistered || version != wantVersion || role != wantRole || status != wantStatus || !lastLoginPreserved || emailCount != 1 {
			t.Fatalf("member %d changed unexpectedly: password=%v registered=%v version=%d role=%s status=%s lastLoginPreserved=%v emails=%d", id, passwordSet, registered, version, role, status, lastLoginPreserved, emailCount)
		}
	}
	var auditCount int
	if err := tx.QueryRow(t.Context(), `
	 SELECT count(*) FROM audit_log WHERE action='user.password_reset_by_admin'
	 AND before_data->>'role'=after_data->>'role'
	 AND before_data->>'emailCount'=after_data->>'emailCount'
	 AND after_data->>'registered'='false'
	`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != len(resetIDs) {
		t.Fatalf("password reset audit count=%d, want %d", auditCount, len(resetIDs))
	}
}

func TestAdminPasswordResetSingle(t *testing.T) {
	for _, test := range []struct {
		name string
		id   int64
		code int
		body string
	}{
		{"student", 1, http.StatusOK, `"reset":1`},
		{"group", 2, http.StatusOK, `"reset":1`},
		{"class admin protected", 3, http.StatusConflict, `"code":"member_account_required"`},
		{"disabled protected", 4, http.StatusConflict, `"code":"account_disabled"`},
		{"unregistered protected", 5, http.StatusConflict, `"code":"account_unregistered"`},
		{"other class protected", 6, http.StatusNotFound, `"code":"not_found"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := passwordResetTestTx(t)
			response := callPasswordReset(t, tx, "/users/:id/reset-password", "/users/"+strconv.FormatInt(test.id, 10)+"/reset-password", false)
			if response.Code != test.code || !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("response=%d %s", response.Code, response.Body)
			}
			if test.code == http.StatusOK {
				assertPasswordResetMembers(t, tx, test.id)
			} else {
				assertPasswordResetMembers(t, tx)
			}
		})
	}
}

func TestAdminPasswordResetBulk(t *testing.T) {
	for _, test := range []struct {
		path string
		body string
		ids  []int64
	}{
		{"/users/reset-password", `"reset":2`, []int64{1, 2}},
		{"/users/initialize", `"initialized":1`, []int64{1}},
	} {
		t.Run(test.path, func(t *testing.T) {
			tx := passwordResetTestTx(t)
			response := callPasswordReset(t, tx, test.path, test.path, true)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("response=%d %s", response.Code, response.Body)
			}
			assertPasswordResetMembers(t, tx, test.ids...)
		})
	}
}
