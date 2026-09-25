package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	"easygpa/backend/internal/auth"
	"easygpa/backend/internal/notify"
	"easygpa/backend/internal/scheme"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
}

func writeError(c *gin.Context, status int, code, message string, detail any) {
	c.AbortWithStatusJSON(status, errorBody{Code: code, Message: message, Detail: detail})
}

func writeClaimError(c *gin.Context, err error) {
	var claimErr *scheme.ClaimError
	var detail any
	if errors.As(err, &claimErr) {
		detail = gin.H{"reason": claimErr.Code, "params": claimErr.Params}
	}
	writeError(c, http.StatusUnprocessableEntity, "submission_invalid", err.Error(), detail)
}

func writeServiceError(c *gin.Context, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" && (pgErr.ConstraintName == "submission_force_rejection_score_check" ||
		pgErr.ConstraintName == "submission_force_rejection_immutable" ||
		pgErr.ConstraintName == "submission_force_rejection_workflow") {
		writeError(c, http.StatusConflict, "submission_force_rejected", "该项已被强制驳回，不能继续审核或改分", nil)
		return
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23514" && (pgErr.ConstraintName == "submission_forced_score_check" || pgErr.ConstraintName == "submission_forced_score_immutable" || pgErr.ConstraintName == "submission_forced_score_workflow") {
		writeError(c, http.StatusConflict, "submission_force_scored", "该项已强制改分终裁；请刷新查看最新认定分，后续调整须使用强制改分", nil)
		return
	}
	var mailLimited *notify.MailRateLimitError
	if errors.As(err, &mailLimited) {
		seconds := max(1, int((mailLimited.After+time.Second-1)/time.Second))
		c.Header("Retry-After", strconv.Itoa(seconds))
		writeError(c, http.StatusTooManyRequests, "mail_rate_limited", "邮件发送暂时受限，请稍后重试", gin.H{"retryAfter": seconds})
		return
	}
	switch {
	case errors.Is(err, auth.ErrInvalidRefresh):
		writeError(c, http.StatusUnauthorized, "unauthenticated", "登录已过期，请重新登录", nil)
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(c, http.StatusUnauthorized, "invalid_credentials", err.Error(), nil)
	case errors.Is(err, auth.ErrAccountDisabled):
		writeError(c, http.StatusForbidden, "account_disabled", err.Error(), nil)
	case errors.Is(err, auth.ErrRegistrationDenied):
		writeError(c, http.StatusForbidden, "registration_denied", err.Error(), nil)
	case errors.Is(err, auth.ErrInvalidChallenge):
		writeError(c, http.StatusBadRequest, "invalid_challenge", err.Error(), nil)
	case errors.Is(err, auth.ErrAlreadyRegistered), errors.Is(err, auth.ErrConflict):
		writeError(c, http.StatusConflict, "conflict", err.Error(), nil)
	default:
		slog.Error("api request failed", "error", err, "request_id", c.Writer.Header().Get("X-Request-ID"), "path", c.Request.URL.Path)
		writeError(c, http.StatusInternalServerError, "internal_error", "服务器处理失败", nil)
	}
}

func notImplemented(code, message string) gin.HandlerFunc {
	return func(c *gin.Context) { writeError(c, http.StatusNotImplemented, code, message, nil) }
}
