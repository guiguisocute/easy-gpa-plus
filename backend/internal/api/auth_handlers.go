package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/auth"
	"easygpa/backend/internal/ratelimit"
)

const refreshCookieName = "easygpa_refresh"

func (s *Server) registerAuthRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/auth")
	/* 注册三步：check(核对身份) → complete(设密码建号) → 登录后可选绑邮箱(/me/emails)。
	   原先的 verify-email / resend-code 属于"先验邮箱才建号"的旧流程，已随之移除。 */
	/* 按 IP 限流在校园网里是按整栋楼限流：一个班从同一个出口 NAT 出来，原来
	   register 每小时 20 次、login 每分钟 10 次（burst 5），意味着一个班同时注册
	   或开放窗口那天集体登录，绝大多数人直接吃 429，而他们没有任何办法。

	   真正拦爆破的不是这里，是下面 login 里那道按账号的 auth-login-account
	   （15 分钟 20 次、burst 5）——它跟着账号走，换多少个 IP 都绕不开。注册那两条
	   则由白名单兜底：学号不在名单里，请求再多也注册不出账号。
	   所以这里放宽到"一个班同时操作也够用"，把爆破防护留给那两道。 */
	g.POST("/register/check", s.rateLimitByIP("auth-register-check", ratelimit.Rule{Requests: 120, Period: time.Hour, Burst: 40}), s.registerCheck)
	g.POST("/register/complete", s.rateLimitByIP("auth-register-complete", ratelimit.Rule{Requests: 120, Period: time.Hour, Burst: 40}), s.passwordWorkMiddleware(), s.registerComplete)
	g.POST("/login", s.rateLimitByIP("auth-login", ratelimit.Rule{Requests: 120, Period: time.Minute, Burst: 60}), s.passwordWorkMiddleware(), s.login)
	g.POST("/refresh", s.rateLimitByIP("auth-refresh", ratelimit.Rule{Requests: 60, Period: time.Minute, Burst: 15}), s.refresh)
	g.POST("/logout", s.logout)
	g.POST("/forgot-password", s.rateLimitByIP("auth-forgot", ratelimit.Rule{Requests: 5, Period: time.Hour, Burst: 2}), s.forgotPassword)
	g.POST("/reset-password", s.rateLimitByIP("auth-reset", ratelimit.Rule{Requests: 10, Period: time.Hour, Burst: 3}), s.passwordWorkMiddleware(), s.resetPassword)
}

// 第一步：核对 (学号, 姓名) 是否命中白名单，命中就发一张短期票据。不涉及密码与邮箱。
func (s *Server) registerCheck(c *gin.Context) {
	if !s.runtimeFlags(c.Request.Context()).Registration {
		writeError(c, http.StatusServiceUnavailable, "registration_closed", "平台暂时关闭新账号注册", nil)
		return
	}
	var input auth.RegisterCheckInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "注册信息格式不正确", nil)
		return
	}
	response, err := s.deps.Auth.RegisterCheck(c.Request.Context(), input)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, response)
}

// 第二步：凭票据设密码，账号在这里建出来并直接进入登录态。
func (s *Server) registerComplete(c *gin.Context) {
	if !s.runtimeFlags(c.Request.Context()).Registration {
		writeError(c, http.StatusServiceUnavailable, "registration_closed", "平台暂时关闭新账号注册", nil)
		return
	}
	var input auth.RegisterCompleteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "注册信息格式不正确", nil)
		return
	}
	response, err := s.deps.Auth.RegisterComplete(c.Request.Context(), input)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	s.setRefreshCookie(c, response.RefreshToken)
	c.JSON(http.StatusCreated, response)
}

func (s *Server) login(c *gin.Context) {
	var input auth.LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "登录信息格式不正确", nil)
		return
	}
	if s.deps.Limiter != nil && !s.enforceRateLimit(c, "auth-login-account", strings.ToLower(strings.TrimSpace(input.Account)), ratelimit.Rule{Requests: 20, Period: 15 * time.Minute, Burst: 5}) {
		return
	}
	response, err := s.deps.Auth.Login(c.Request.Context(), input)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	s.setRefreshCookie(c, response.RefreshToken)
	c.JSON(http.StatusOK, response)
}

func (s *Server) refresh(c *gin.Context) {
	token, err := c.Cookie(refreshCookieName)
	if err != nil || token == "" {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "刷新凭证不存在", nil)
		return
	}
	response, err := s.deps.Auth.Refresh(c.Request.Context(), token)
	if err != nil {
		s.clearRefreshCookie(c)
		writeServiceError(c, err)
		return
	}
	s.setRefreshCookie(c, response.RefreshToken)
	c.JSON(http.StatusOK, response)
}

func (s *Server) logout(c *gin.Context) {
	token, _ := c.Cookie(refreshCookieName)
	if token != "" {
		_ = s.deps.Auth.Logout(c.Request.Context(), token)
	}
	s.clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

func (s *Server) forgotPassword(c *gin.Context) {
	var input struct {
		Account string `json:"account"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "账号格式不正确", nil)
		return
	}
	if s.deps.Limiter != nil && !s.enforceRateLimit(c, "auth-forgot-account", strings.ToLower(strings.TrimSpace(input.Account)), ratelimit.Rule{Requests: 5, Period: time.Hour, Burst: 2}) {
		return
	}
	if err := s.deps.Auth.ForgotPassword(c.Request.Context(), input.Account); err != nil {
		writeServiceError(c, err)
		return
	}
	// Same response whether the account exists, preventing address enumeration.
	c.Status(http.StatusNoContent)
}

func (s *Server) resetPassword(c *gin.Context) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "重置请求格式不正确", nil)
		return
	}
	if err := s.deps.Auth.ResetPassword(c.Request.Context(), input.Token, input.Password); err != nil {
		writeServiceError(c, err)
		return
	}
	s.clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

func (s *Server) setRefreshCookie(c *gin.Context, value string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: refreshCookieName, Value: value, Path: "/api/v1/auth", MaxAge: int(s.cfg.RefreshTokenTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearRefreshCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: refreshCookieName, Value: "", Path: "/api/v1/auth", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}
