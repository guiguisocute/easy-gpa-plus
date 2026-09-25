package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type mailReconciliationInput struct {
	Status    string `json:"status"`
	MessageID string `json:"messageId"`
}

func validateMailReconciliation(input *mailReconciliationInput) error {
	input.MessageID = strings.TrimSpace(input.MessageID)
	if input.Status != "sent" && input.Status != "failed" {
		return errors.New("请选择已受理或未受理")
	}
	if input.Status == "sent" && input.MessageID == "" {
		return errors.New("确认已受理时必须填写供应商 MessageId")
	}
	if input.Status == "failed" && input.MessageID != "" {
		return errors.New("确认未受理时不应填写 MessageId")
	}
	if len(input.MessageID) > 256 || strings.ContainsFunc(input.MessageID, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return errors.New("MessageId 不能超过 256 字节或包含空白和控制字符")
	}
	return nil
}

func (s *Server) reconcileOpsMail(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusUnprocessableEntity, "mail_delivery_id_invalid", "投递记录 ID 不正确", nil)
		return
	}
	var input mailReconciliationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "邮件对账参数不正确", nil)
		return
	}
	if err := validateMailReconciliation(&input); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "mail_reconciliation_invalid", err.Error(), nil)
		return
	}
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	eventID, tenantID, err := reconcileMailDelivery(c.Request.Context(), tx, s.cfg.OpsAccount, c.ClientIP(), id, input)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusConflict, "mail_delivery_changed", "只能对账超过 2 分钟且仍未确认结果的记录，请刷新后重试", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	var tenantValue any
	if tenantID != nil {
		tenantValue = strconv.FormatInt(*tenantID, 10)
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "status": input.Status, "eventId": eventID, "tenantId": tenantValue})
}

// The outcome correction and its audit share the caller's transaction. This
// never sends mail or creates another event; pending notifications keep their ID.
func reconcileMailDelivery(ctx context.Context, tx pgx.Tx, actor, ip string, id int64, input mailReconciliationInput) (*string, *int64, error) {
	var eventID *string
	var tenantID *int64
	if err := tx.QueryRow(ctx, `SELECT event_id::text,tenant_id FROM ops_reconcile_mail_delivery($1,$2,$3)`, id, input.Status, input.MessageID).Scan(&eventID, &tenantID); err != nil {
		return nil, nil, err
	}
	metadata, err := json.Marshal(map[string]any{"deliveryId": strconv.FormatInt(id, 10), "status": input.Status, "messageId": input.MessageID, "eventId": eventID})
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_audit (actor,action,resource_type,resource_id,metadata,ip_address)
		VALUES ($1,'mail.delivery_reconciled','mail_delivery',$2::bigint::text,$3,NULLIF($4,'')::inet)
	`, actor, id, metadata, ip); err != nil {
		return nil, nil, err
	}
	return eventID, tenantID, nil
}
