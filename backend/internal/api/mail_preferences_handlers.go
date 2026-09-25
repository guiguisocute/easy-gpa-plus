package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"easygpa/backend/internal/notify"
	"github.com/gin-gonic/gin"
)

func (s *Server) myMailPreferences(c *gin.Context) {
	actor, tx := mustActor(c), mustTx(c)
	p, _, err := notify.LoadPreferences(c.Request.Context(), tx, actor.ClassID, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var address string
	if err = tx.QueryRow(c.Request.Context(), `SELECT coalesce((SELECT email_normalized FROM user_email WHERE user_id=$1 AND is_primary AND verified_at IS NOT NULL LIMIT 1),'')`, actor.UserID).Scan(&address); err != nil {
		writeServiceError(c, err)
		return
	}
	state := "ready"
	if address == "" {
		state = "no_primary_email"
	} else if s.deps.Pools.Ops != nil {
		if err = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT coalesce((SELECT reason FROM mail_suppression WHERE recipient_hash=md5(lower(trim($1))) ORDER BY scope LIMIT 1),'ready')`, address).Scan(&state); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	paused := true
	if s.opsConfig != nil {
		cfg, err := s.opsConfig.Mail(c.Request.Context())
		if err != nil {
			writeServiceError(c, err)
			return
		}
		paused = !cfg.NotificationsEnabled
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"preferences": p, "notificationsPaused": paused, "deliveryState": state})
}

func (s *Server) updateMyMailPreferences(c *gin.Context) {
	var p notify.Preferences
	if err := c.ShouldBindJSON(&p); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "邮件偏好参数不正确", nil)
		return
	}
	if err := p.Validate(); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "mail_preferences_invalid", err.Error(), nil)
		return
	}
	actor := mustActor(c)
	if err := notify.SavePreferences(c.Request.Context(), mustTx(c), actor.ClassID, actor.UserID, p); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, mustTx(c), "mail.preferences_updated", "user", strconv.FormatInt(actor.UserID, 10), nil, nil, map[string]any{"enabled": p.Enabled, "dailyLimit": p.DailyLimit}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) optOutMail(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	var input struct {
		Token    string `json:"token"`
		Category string `json:"category"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "退订请求不正确", nil)
		return
	}
	token, err := base64.RawURLEncoding.DecodeString(input.Token)
	if err != nil || len(token) != 32 {
		writeError(c, http.StatusNotFound, "opt_out_invalid", "退订链接无效，请从原邮件重新打开，或登录账号设置关闭邮件", nil)
		return
	}
	var ok bool
	if err = s.deps.Pools.App.QueryRow(c.Request.Context(), `SELECT mail_opt_out($1,$2)`, input.Token, input.Category).Scan(&ok); err != nil {
		writeServiceError(c, err)
		return
	}
	if !ok {
		writeError(c, http.StatusNotFound, "opt_out_invalid", "退订链接无效，请从原邮件重新打开，或登录账号设置关闭邮件", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unsubscribed": true})
}

// No address, event label or bounce reason supplied by a webhook is trusted.
// Only a known MessageId can wake the authenticated SES polling reader.
func (s *Server) mailSESEvent(c *gin.Context) {
	var input struct {
		MessageID string `json:"bulkId"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.MessageID) > 256 {
		c.Status(http.StatusBadRequest)
		return
	}
	if s.deps.Pools.Ops != nil && strings.TrimSpace(input.MessageID) != "" {
		if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `UPDATE mail_delivery SET feedback_next_at=now() WHERE provider_id=$1 AND status='sent' AND (feedback_checked_at IS NULL OR feedback_checked_at<now()-interval '2 minutes')`, input.MessageID); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.Status(http.StatusNoContent)
}
