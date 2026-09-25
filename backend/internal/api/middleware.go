package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/auth"
	"easygpa/backend/internal/classtimeline"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/ratelimit"
	"easygpa/backend/internal/store"
)

const (
	actorContextKey       = "easygpa.actor"
	txContextKey          = "easygpa.tx"
	afterCommitKey        = "easygpa.after_commit"
	maxRequestBytes       = 16 << 20
	maxRequestConcurrency = int64(512)
)

type Actor struct {
	UserID       int64
	ClassID      int64
	Role         string
	IsDeputy     bool
	TokenVersion int64
	SessionID    string
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if !validRequestID(id) {
			var raw [16]byte
			_, _ = rand.Read(raw[:])
			id = hex.EncodeToString(raw[:])
		}
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func securityHeadersMiddleware(production bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		c.Header("X-Frame-Options", "DENY")
		if production {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

func (s *Server) ipRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/healthz" || s.deps.Limiter == nil {
			c.Next()
			return
		}
		identifier := "ip:" + c.ClientIP()
		// Authenticated traffic gets a session bucket, so a campus NAT does not
		// make unrelated signed-in users consume one shared IP allowance.
		if s.deps.Tokens != nil {
			scheme, raw, ok := strings.Cut(c.GetHeader("Authorization"), " ")
			if ok && strings.EqualFold(scheme, "Bearer") {
				if claims, err := s.deps.Tokens.Parse(strings.TrimSpace(raw)); err == nil && claims.SessionID != "" {
					identifier = "session:" + claims.SessionID
				}
			}
		}
		if !s.enforceRateLimit(c, "api-client", identifier, s.deploymentAPIRateLimit()) {
			return
		}
		c.Next()
	}
}

func (s *Server) rateLimitByIP(scope string, rule ratelimit.Rule) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.deps.Limiter == nil || s.enforceRateLimit(c, scope, c.ClientIP(), rule) {
			c.Next()
		}
	}
}

func (s *Server) rateLimitByActor(scope string, rule ratelimit.Rule) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.deps.Limiter == nil {
			c.Next()
			return
		}
		actor := mustActor(c)
		identifier := c.ClientIP()
		if actor.UserID > 0 {
			identifier = "user:" + strconv.FormatInt(actor.UserID, 10)
		}
		if s.enforceRateLimit(c, scope, identifier, rule) {
			c.Next()
		}
	}
}

func (s *Server) enforceRateLimit(c *gin.Context, scope, identifier string, rule ratelimit.Rule) bool {
	if s.e2eRateLimitBypass(c) {
		return true
	}
	rule = s.configuredRateLimit(c.Request.Context(), scope, rule)
	decision, err := s.deps.Limiter.Allow(c.Request.Context(), ratelimit.Key(scope, identifier), rule)
	if err != nil {
		slog.Error("rate limiter unavailable", "error", err, "scope", scope)
		writeError(c, http.StatusServiceUnavailable, "rate_limiter_unavailable", "请求保护服务暂时不可用，请稍后重试", nil)
		return false
	}
	c.Header("RateLimit-Limit", strconv.Itoa(rule.Requests))
	c.Header("RateLimit-Remaining", strconv.Itoa(decision.Remaining))
	if decision.Allowed {
		return true
	}
	retry := ratelimit.RetryAfterSeconds(decision.RetryAfter)
	c.Header("Retry-After", strconv.Itoa(retry))
	writeError(c, http.StatusTooManyRequests, "rate_limit_exceeded", "请求过于频繁，请稍后重试", gin.H{"retryAfter": retry})
	return false
}

func (s *Server) e2eRateLimitBypass(c *gin.Context) bool {
	if s == nil || s.cfg == nil || s.cfg.AppEnv != "dev" || s.cfg.E2ETestToken == "" {
		return false
	}
	provided := c.GetHeader("X-EasyGPA-E2E-Token")
	return len(provided) == len(s.cfg.E2ETestToken) &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.E2ETestToken)) == 1
}

func (s *Server) deploymentAPIRateLimit() ratelimit.Rule {
	requests := s.cfg.APIRateLimitPerMinute
	if requests < 10 {
		requests = 180
	}
	burst := s.cfg.APIRateLimitBurst
	if burst < 1 || burst > requests {
		burst = min(60, requests)
	}
	return ratelimit.Rule{Requests: requests, Period: time.Minute, Burst: burst}
}

func (s *Server) configuredRateLimit(ctx context.Context, scope string, rule ratelimit.Rule) ratelimit.Rule {
	flags := s.runtimeFlags(ctx)
	configured := 0
	switch scope {
	case "api-client":
		configured = min(flags.APIRateLimitPerMinute, rule.Requests)
		if flags.APIRateLimitBurst > 0 {
			rule.Burst = min(rule.Burst, flags.APIRateLimitBurst)
		}
	case "auth-login":
		configured = flags.AuthLoginPerMinute
	case "auth-refresh":
		configured = flags.AuthRefreshPerMinute
	case "auth-register-check", "auth-register-complete":
		configured = flags.AuthRegisterPerHour
	case "auth-forgot":
		configured = flags.AuthForgotPerHour
	case "auth-reset":
		configured = flags.AuthResetPerHour
	case "evidence-upload-presign":
		configured = flags.EvidencePresignPerHour
	case "ai-upload-presign":
		configured = flags.AIPresignPerHour
	case "agent-upload-presign":
		configured = flags.AgentPresignPerHour
	case "knowledge-upload-presign":
		configured = flags.KnowledgePresignPerHour
	case "ai-batch-action":
		configured = flags.AIBatchActionsPerHour
	case "agent-message":
		configured = flags.AgentMessagesPerMinute
	case "export-request":
		configured = flags.ExportRequestsPerHour
	case "knowledge-reprocess":
		configured = flags.KnowledgeReprocessPerHour
	}
	if configured > 0 {
		rule.Requests = configured
	}
	rule.Burst = min(rule.Burst, rule.Requests)
	return rule
}

func (s *Server) passwordWorkMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := s.passwordHashConcurrency(c.Request.Context())
		active := s.passwordWork.Add(1)
		if active > int64(limit) {
			s.passwordWork.Add(-1)
			c.Header("Retry-After", "2")
			writeError(c, http.StatusServiceUnavailable, "password_work_busy", "登录与密码服务当前繁忙，请稍后重试", nil)
			return
		}
		defer s.passwordWork.Add(-1)
		c.Next()
	}
}

func (s *Server) passwordHashConcurrency(ctx context.Context) int {
	hard := s.deploymentPasswordHashConcurrency()
	configured := s.runtimeFlags(ctx).PasswordHashConcurrency
	if configured < 1 {
		configured = hard
	}
	return min(hard, configured)
}

func (s *Server) deploymentPasswordHashConcurrency() int {
	configured := s.cfg.PasswordHashConcurrency
	if configured < 1 || configured > 16 {
		return 4
	}
	return configured
}

func requestBodyLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := int64(maxRequestBytes)
		if c.Request.Method == http.MethodPut && strings.HasPrefix(c.Request.URL.Path, "/mcp/uploads/") {
			limit = 64 << 20
		}
		if c.Request.ContentLength > limit {
			writeError(c, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求内容不能超过 %d MB", limit>>20), nil)
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

func (s *Server) runtimeFlags(ctx context.Context) opsconfig.Flags {
	flags := opsconfig.DefaultFlags()
	if s.opsConfig == nil {
		return flags
	}
	loaded, err := s.opsConfig.Flags(ctx)
	if err != nil {
		slog.Error("load runtime flags", "error", err)
		return flags
	}
	return loaded
}

func (s *Server) requestConcurrencyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		active := s.activeRequests.Add(1)
		defer s.activeRequests.Add(-1)
		if active > maxRequestConcurrency {
			c.Header("Retry-After", "1")
			writeError(c, http.StatusServiceUnavailable, "request_concurrency_exceeded", "当前请求过多，请稍后重试", nil)
			return
		}
		limit := int64(s.runtimeFlags(c.Request.Context()).RequestConcurrency)
		if active > limit {
			c.Header("Retry-After", "1")
			writeError(c, http.StatusServiceUnavailable, "request_concurrency_exceeded", "当前请求过多，请稍后重试", nil)
			return
		}
		c.Next()
	}
}

func (s *Server) maintenanceModeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		value, ok := c.Get(actorContextKey)
		actor, actorOK := value.(Actor)
		if ok && actorOK && actor.Role != "ops" && s.runtimeFlags(c.Request.Context()).Maintenance {
			c.Header("Retry-After", "60")
			writeError(c, http.StatusServiceUnavailable, "maintenance_mode", "平台正在维护，业务写操作暂时关闭", nil)
			return
		}
		c.Next()
	}
}

/*
全系统封锁。

	封存截止（window.close）只关掉"再交新材料"，审核、申诉、异议、仲裁都还得
	继续跑完——那是 §2.2 一直以来的语义，不动它。封锁是它之后的第二道线：到点
	之后这个班的这个学期整体冻住，学生、综测小组、班级管理员一律不能改任何东西。

	放行的只有四条，每一条都有明确理由：
	  - 导出与下载：导出只是把已经定下来的结果静态化，正是封锁要保住的东西
	  - 结算：输入已经全部冻结，settle.Compute 对同一批输入逐字节确定，
	    且不放行的话，封锁前没结算过的班永远导不出任何东西
	  - 改时间线：班管唯一的逃生口。没有它，一个填错的时间戳就把班永久锁死
	其余一律 409。

	账号自助（改密码、邮箱）挂在 secured 上而不是 business 上，本来就不在这里；
	运维路由同理。封锁的是综测业务，不是登录。
*/
var lockdownAllowedRoutes = map[string]bool{
	"POST /api/v1/admin/export":     true,
	"GET /api/v1/admin/export/:job": true,
	"POST /api/v1/admin/settle":     true,
	"PUT /api/v1/admin/timeline":    true,
	"PUT /api/v1/admin/window":      true,
}

// lockdownExempt answers whether a request may run while the class is frozen,
// without needing the database. Reads never change anything, and the allowlist
// is keyed by route pattern rather than raw path so a URL with an id in it
// cannot be crafted to slip past.
func lockdownExempt(method, fullPath string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return true
	}
	return lockdownAllowedRoutes[method+" "+fullPath]
}

func (s *Server) classLockdownMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if lockdownExempt(c.Request.Method, c.FullPath()) {
			c.Next()
			return
		}
		lockdown, err := classtimeline.Lockdown(c.Request.Context(), mustTx(c), mustActor(c).ClassID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if lockdown == nil || time.Now().Before(*lockdown) {
			c.Next()
			return
		}
		exempt, e := governanceLockdownExempt(c)
		if e != nil {
			writeServiceError(c, e)
			return
		}
		if exempt {
			c.Next()
			return
		}
		writeError(c, http.StatusConflict, "class_locked",
			"本学期已全系统封锁，只能查看历史资料与导出", gin.H{"lockdown": *lockdown})
	}
}

func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		scheme, raw, ok := strings.Cut(header, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(raw) == "" {
			writeError(c, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
			return
		}
		claims, err := s.deps.Tokens.Parse(strings.TrimSpace(raw))
		if err != nil {
			var detail any
			if s.cfg.AppEnv == "dev" {
				// Local E2E failures must distinguish expiry, clock skew and an
				// invalid signature without ever echoing the bearer token itself.
				detail = err.Error()
			}
			writeError(c, http.StatusUnauthorized, "unauthenticated", "登录已过期，请重新登录", detail)
			return
		}
		actor := Actor{ClassID: claims.ClassID, Role: claims.Role, TokenVersion: claims.TokenVersion, SessionID: claims.SessionID}
		if claims.Role != "ops" {
			actor.UserID, err = strconv.ParseInt(claims.Subject, 10, 64)
			if err != nil || actor.UserID <= 0 {
				writeError(c, http.StatusUnauthorized, "unauthenticated", "登录凭证无效", nil)
				return
			}
		}
		c.Set(actorContextKey, actor)
		c.Next()
	}
}

// validateActorState makes role and account changes effective immediately for
// every business endpoint, instead of trusting authorization claims until the
// access token expires.
func (s *Server) validateActorState() gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := mustActor(c)
		if actor.Role == "ops" {
			c.Next()
			return
		}

		var role, status string
		var classArchived bool
		var tokenVersion int64
		err := mustTx(c).QueryRow(c.Request.Context(), `
			SELECT u.role,u.status,u.token_version,c.archived,u.is_deputy
			  FROM app_user u
			  JOIN class c ON c.id=u.class_id
			 WHERE u.id=$1
		`, actor.UserID).Scan(&role, &status, &tokenVersion, &classArchived, &actor.IsDeputy)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (classArchived || !actorStateMatches(actor, role, status, tokenVersion))) {
			writeError(c, http.StatusUnauthorized, "unauthenticated", "账号状态已变化，请重新登录", nil)
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
		c.Set(actorContextKey, actor)
		c.Next()
	}
}

func actorStateMatches(actor Actor, role, status string, tokenVersion int64) bool {
	return status == "active" && role == actor.Role && tokenVersion == actor.TokenVersion
}

// tenantTransaction delays response bytes until Commit succeeds. This avoids a
// misleading 2xx response when PostgreSQL rejects the final commit.
func (s *Server) tenantTransaction() gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := mustActor(c)
		if actor.Role == "ops" {
			c.Next()
			return
		}
		options := pgx.TxOptions{}
		if call, ok := c.Request.Context().Value(mcpCallKey{}).(mcpCall); ok {
			options.IsoLevel = pgx.RepeatableRead
			if call.Spec.Approval || call.Spec.RequireVersion {
				options.IsoLevel = pgx.Serializable
			}
		}
		if c.FullPath() == "/api/v1/me/scorecard" || c.FullPath() == "/api/v1/me/scorecard/confirm" {
			options.IsoLevel = pgx.RepeatableRead
		}
		tx, err := store.BeginTenantWithOptions(c.Request.Context(), s.deps.Pools.App, actor.ClassID, options)
		if err != nil {
			writeError(c, http.StatusServiceUnavailable, "database_unavailable", "数据库事务无法启动", nil)
			return
		}
		original := c.Writer
		buffer := newBufferedWriter(original)
		c.Writer = buffer
		c.Set(txContextKey, tx)
		finished := false
		defer func() {
			if !finished {
				_ = tx.Rollback(context.Background())
				c.Writer = original
			}
		}()

		c.Next()
		if c.IsAborted() || buffer.Status() >= http.StatusBadRequest {
			_ = tx.Rollback(c.Request.Context())
			finished = true
			c.Writer = original
			buffer.flush()
			return
		}
		if err := tx.Commit(c.Request.Context()); err != nil {
			finished = true
			c.Writer = original
			writeError(c, http.StatusInternalServerError, "commit_failed", "数据库提交失败", nil)
			return
		}
		runAfterCommit(c)
		finished = true
		c.Writer = original
		buffer.flush()
	}
}

func afterCommit(c *gin.Context, fn func(context.Context) error) {
	value, _ := c.Get(afterCommitKey)
	hooks, _ := value.([]func(context.Context) error)
	hooks = append(hooks, fn)
	c.Set(afterCommitKey, hooks)
}

func runAfterCommit(c *gin.Context) {
	value, _ := c.Get(afterCommitKey)
	hooks, _ := value.([]func(context.Context) error)
	for _, hook := range hooks {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hook(ctx); err != nil {
			slog.Error("after-commit hook failed", "error", err, "path", c.Request.URL.Path)
		}
		cancel()
	}
}

func (s *Server) requireBusiness() gin.HandlerFunc {
	return func(c *gin.Context) {
		if mustActor(c).Role == "ops" {
			// Deliberately 404: the platform identity has no business API surface.
			if s.deps.Pools != nil && s.deps.Pools.Ops != nil {
				_, _ = s.deps.Pools.Ops.Exec(c.Request.Context(), `
					INSERT INTO ops_audit (actor,action,resource_type,resource_id,metadata,ip_address)
					VALUES ($1,'business_access.denied','route',$2,jsonb_build_object('method',$3::text),NULLIF($4,'')::inet)
				`, s.cfg.OpsAccount, c.Request.URL.Path, c.Request.Method, c.ClientIP())
			}
			writeError(c, http.StatusNotFound, "not_found", "资源不存在", nil)
			return
		}
		c.Next()
	}
}

func requireMinimumRole(minimum string) gin.HandlerFunc {
	rank := map[string]int{"student": 0, "group": 1, "class_admin": 2}
	return func(c *gin.Context) {
		actor := mustActor(c)
		if actor.Role == "ops" || rank[actor.Role] < rank[minimum] {
			writeError(c, http.StatusForbidden, "forbidden", "当前身份无权执行此操作", nil)
			return
		}
		c.Next()
	}
}

func requireOps() gin.HandlerFunc {
	return func(c *gin.Context) {
		if mustActor(c).Role != "ops" {
			writeError(c, http.StatusNotFound, "not_found", "资源不存在", nil)
			return
		}
		c.Next()
	}
}

func mustActor(c *gin.Context) Actor {
	value, ok := c.Get(actorContextKey)
	if !ok {
		panic("api actor missing after authentication middleware")
	}
	return value.(Actor)
}

func mustTx(c *gin.Context) pgx.Tx {
	value, ok := c.Get(txContextKey)
	if !ok {
		panic("tenant transaction missing")
	}
	return value.(pgx.Tx)
}

type bufferedWriter struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
	size   int
}

func newBufferedWriter(original gin.ResponseWriter) *bufferedWriter {
	return &bufferedWriter{ResponseWriter: original, status: http.StatusOK}
}

func (w *bufferedWriter) WriteHeader(code int) {
	if w.Written() {
		return
	}
	w.status = code
}

func (w *bufferedWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *bufferedWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.body.Write(data)
	w.size += n
	return n, err
}

func (w *bufferedWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *bufferedWriter) Status() int   { return w.status }
func (w *bufferedWriter) Size() int     { return w.size }
func (w *bufferedWriter) Written() bool { return w.size > 0 || w.status != http.StatusOK }
func (w *bufferedWriter) Flush()        {}

func (w *bufferedWriter) flush() {
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(w.body.Bytes())
}

var _ gin.ResponseWriter = (*bufferedWriter)(nil)

func actorFromClaims(claims auth.Claims) (Actor, error) {
	actor := Actor{ClassID: claims.ClassID, Role: claims.Role, TokenVersion: claims.TokenVersion, SessionID: claims.SessionID}
	if claims.Role == "ops" {
		return actor, nil
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Actor{}, err
	}
	actor.UserID = userID
	return actor, nil
}
