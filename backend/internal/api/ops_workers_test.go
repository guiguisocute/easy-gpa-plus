package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/workerhealth"
)

func TestApplyWorkerHeartbeatStatuses(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		raw    string
		err    error
		status string
		alive  bool
	}{
		{name: "alive", raw: now.Add(-20 * time.Second).Format(time.RFC3339Nano), status: workerhealth.Alive, alive: true},
		{name: "stale", raw: now.Add(-2 * time.Minute).Format(time.RFC3339Nano), status: workerhealth.Stale},
		{name: "missing", err: redis.Nil, status: workerhealth.Missing},
		{name: "redis error", err: errors.New("connection refused"), status: workerhealth.Error},
		{name: "bad timestamp", raw: "not-a-time", status: workerhealth.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := workerhealth.State{Name: "dispatch"}
			applyWorkerHeartbeat(&state, now, tc.raw, tc.err)
			if state.HeartbeatStatus != tc.status || state.HeartbeatAlive != tc.alive {
				t.Fatalf("status=%q alive=%v, want %q %v", state.HeartbeatStatus, state.HeartbeatAlive, tc.status, tc.alive)
			}
			if tc.status == workerhealth.Alive && state.LastHeartbeat == nil {
				t.Fatal("alive heartbeat missing timestamp")
			}
			if tc.status == workerhealth.Error && state.HeartbeatError == "" {
				t.Fatal("error heartbeat missing message")
			}
		})
	}
}

func TestWorkerRuntimeStateReportsMissingRedis(t *testing.T) {
	server := &Server{deps: Dependencies{}}
	state := server.workerRuntimeState(t.Context(), "notify")
	if state.Name != "notify" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if state.HeartbeatStatus != workerhealth.Error {
		t.Fatalf("missing redis should be heartbeat error, got %q", state.HeartbeatStatus)
	}
}

func TestWorkerRuntimeStateFromLiveRedis(t *testing.T) {
	addr := os.Getenv("EASYGPA_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set EASYGPA_TEST_REDIS_ADDR to run worker heartbeat redis regression")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	name := "dispatch-testfixture"
	// Seed the existing Redis key independently of the new shared helper.
	key := "easygpa:worker:" + name + ":heartbeat"
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = client.Del(cleanup, key).Err()
	})
	server := &Server{deps: Dependencies{Redis: client}}

	if err := client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	missing := server.workerRuntimeState(ctx, name)
	if missing.HeartbeatStatus != workerhealth.Missing || missing.HeartbeatAlive {
		t.Fatalf("missing = %#v", missing)
	}

	if err := client.Set(ctx, key, time.Now().UTC().Format(time.RFC3339Nano), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	alive := server.workerRuntimeState(ctx, name)
	if alive.HeartbeatStatus != workerhealth.Alive || !alive.HeartbeatAlive || alive.LastHeartbeat == nil {
		t.Fatalf("alive = %#v", alive)
	}

	if err := client.Set(ctx, key, time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339Nano), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	stale := server.workerRuntimeState(ctx, name)
	if stale.HeartbeatStatus != workerhealth.Stale || stale.HeartbeatAlive {
		t.Fatalf("stale = %#v", stale)
	}

	bad := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = bad.Close() })
	failCtx, failCancel := context.WithTimeout(t.Context(), time.Second)
	defer failCancel()
	failed := (&Server{deps: Dependencies{Redis: bad}}).workerRuntimeState(failCtx, name)
	if failed.HeartbeatStatus != workerhealth.Error || failed.HeartbeatError == "" {
		t.Fatalf("redis error = %#v", failed)
	}
}

func TestOpsWorkersRouteHidesFromNonOps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(actorContextKey, Actor{Role: "class_admin"}) })
	router.GET("/ops/workers", requireOps(), (&Server{}).opsWorkers)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ops/workers", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", response.Code)
	}
}
