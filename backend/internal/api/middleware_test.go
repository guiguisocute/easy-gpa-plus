package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/config"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/ratelimit"
)

type fakeRuntimeConfig struct {
	flags opsconfig.Flags
}

func (f fakeRuntimeConfig) Flags(context.Context) (opsconfig.Flags, error) { return f.flags, nil }
func (fakeRuntimeConfig) Lifecycle(context.Context) (opsconfig.Lifecycle, error) {
	return opsconfig.DefaultLifecycle(), nil
}
func (f fakeRuntimeConfig) Mail(context.Context) (opsconfig.Mail, error) {
	return opsconfig.DefaultMail(), nil
}
func (f fakeRuntimeConfig) AI(context.Context) (opsconfig.AI, error) {
	return opsconfig.DefaultAI(), nil
}
func (fakeRuntimeConfig) BackupRemote(context.Context) (opsconfig.BackupRemote, error) {
	return opsconfig.DefaultBackupRemote(), nil
}
func (fakeRuntimeConfig) Invalidate(string) {}

func TestActorStateMatches(t *testing.T) {
	actor := Actor{UserID: 7, ClassID: 9, Role: "group", TokenVersion: 3}
	tests := []struct {
		name         string
		role         string
		status       string
		tokenVersion int64
		want         bool
	}{
		{name: "current", role: "group", status: "active", tokenVersion: 3, want: true},
		{name: "disabled", role: "group", status: "disabled", tokenVersion: 3},
		{name: "role changed", role: "student", status: "active", tokenVersion: 3},
		{name: "token revoked", role: "group", status: "active", tokenVersion: 4},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := actorStateMatches(actor, test.role, test.status, test.tokenVersion); got != test.want {
				t.Fatalf("actorStateMatches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSecurityHeadersOnlyEnableHSTSInProduction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		production bool
		wantHSTS   bool
	}{{"development", false, false}, {"production", true, true}} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(securityHeadersMiddleware(test.production))
			router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			if got := response.Header().Get("Strict-Transport-Security") != ""; got != test.wantHSTS {
				t.Fatalf("HSTS present = %v, want %v", got, test.wantHSTS)
			}
			if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("X-Frame-Options") != "DENY" {
				t.Fatal("security headers are incomplete")
			}
		})
	}
}

func TestRequestIDRejectsLogUnsafeValues(t *testing.T) {
	for _, value := range []string{"", "has space", "bad/slash", strings.Repeat("x", 65)} {
		if validRequestID(value) {
			t.Fatalf("validRequestID(%q) = true", value)
		}
	}
	for _, value := range []string{"request-123", "trace_id.4"} {
		if !validRequestID(value) {
			t.Fatalf("validRequestID(%q) = false", value)
		}
	}
}

func TestRequestBodyLimitRejectsKnownOversizeBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(requestBodyLimitMiddleware())
	router.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x"))
	request.ContentLength = maxRequestBytes + 1
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestMaintenanceModeBlocksBusinessWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{opsConfig: fakeRuntimeConfig{flags: opsconfig.Flags{Maintenance: true, Registration: true, RequestConcurrency: 64}}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, Actor{UserID: 1, ClassID: 1, Role: "student"})
		c.Next()
	}, server.maintenanceModeMiddleware())
	router.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestMaintenanceModeAllowsOpsWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{opsConfig: fakeRuntimeConfig{flags: opsconfig.Flags{Maintenance: true, Registration: true, RequestConcurrency: 64}}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, Actor{Role: "ops"})
		c.Next()
	}, server.maintenanceModeMiddleware())
	router.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

// 封锁的白名单如果和真实路由对不上，后果是静默的：export 明明写在白名单里，
// 却因为路径写错一个字而在封锁后被拒——而这恰恰是封锁期唯一还要用的功能。
// 所以拿真实注册的路由表来核对，而不是各写各的字符串。
func TestLockdownAllowlistMatchesRegisteredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registered := make(map[string]bool)
	for _, route := range NewRouter(&config.Config{}, Dependencies{}).Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for route := range lockdownAllowedRoutes {
		if !registered[route] {
			t.Fatalf("封锁白名单里的 %q 并不存在于路由表", route)
		}
	}
}

func TestLockdownExemptsReadsAndTheEscapeHatches(t *testing.T) {
	for _, route := range []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/v1/submissions", true},
		{http.MethodHead, "/api/v1/window", true},
		{http.MethodPost, "/api/v1/admin/export", true},
		{http.MethodGet, "/api/v1/admin/export/:job", true},
		{http.MethodPost, "/api/v1/admin/settle", true},
		{http.MethodPut, "/api/v1/admin/timeline", true},
		// 班管仍能改时间线，但不能借道别的管理接口继续办事。
		{http.MethodPost, "/api/v1/admin/gate/force", false},
		{http.MethodPost, "/api/v1/submissions", false},
		{http.MethodPost, "/api/v1/review/tasks/:id/decision", false},
		{http.MethodPost, "/api/v1/appeals", false},
		{http.MethodDelete, "/api/v1/submissions/:id", false},
	} {
		if got := lockdownExempt(route.method, route.path); got != route.want {
			t.Fatalf("lockdownExempt(%s %s) = %v, want %v", route.method, route.path, got, route.want)
		}
	}
}

func TestRequestConcurrencyRejectsAboveConfiguredLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{opsConfig: fakeRuntimeConfig{flags: opsconfig.Flags{Registration: true, RequestConcurrency: 1}}}
	server.activeRequests.Store(1)
	router := gin.New()
	router.Use(server.requestConcurrencyMiddleware())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := server.activeRequests.Load(); got != 1 {
		t.Fatalf("active requests = %d, want 1", got)
	}
}

func TestConfiguredRateLimitsRespectOpsValuesAndDeploymentCeiling(t *testing.T) {
	server := &Server{
		cfg: &config.Config{APIRateLimitPerMinute: 100, APIRateLimitBurst: 30},
		opsConfig: fakeRuntimeConfig{flags: opsconfig.Flags{
			APIRateLimitPerMinute: 200,
			APIRateLimitBurst:     50,
			AuthLoginPerMinute:    20,
		}},
	}
	global := server.configuredRateLimit(context.Background(), "api-client", server.deploymentAPIRateLimit())
	if global.Requests != 100 || global.Burst != 30 {
		t.Fatalf("global limit = %+v, want 100 requests and burst 30", global)
	}
	login := server.configuredRateLimit(context.Background(), "auth-login", ratelimit.Rule{Requests: 10, Period: time.Minute, Burst: 5})
	if login.Requests != 20 || login.Burst != 5 {
		t.Fatalf("login limit = %+v, want 20 requests and burst 5", login)
	}
}

func TestE2ERateLimitBypassRequiresDevelopmentAndExactToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-EasyGPA-E2E-Token", "local-e2e-token")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	for _, test := range []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "development exact token", cfg: config.Config{AppEnv: "dev", E2ETestToken: "local-e2e-token"}, want: true},
		{name: "production", cfg: config.Config{AppEnv: "prod", E2ETestToken: "local-e2e-token"}},
		{name: "missing configured token", cfg: config.Config{AppEnv: "dev"}},
		{name: "different token", cfg: config.Config{AppEnv: "dev", E2ETestToken: "another-token"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{cfg: &test.cfg}
			if got := server.e2eRateLimitBypass(context); got != test.want {
				t.Fatalf("e2eRateLimitBypass() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPasswordWorkUsesHotConfiguredLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{
		cfg:       &config.Config{PasswordHashConcurrency: 4},
		opsConfig: fakeRuntimeConfig{flags: opsconfig.Flags{PasswordHashConcurrency: 1}},
	}
	server.passwordWork.Store(1)
	router := gin.New()
	router.Use(server.passwordWorkMiddleware())
	router.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if active := server.passwordWork.Load(); active != 1 {
		t.Fatalf("active password work = %d, want 1", active)
	}
}
