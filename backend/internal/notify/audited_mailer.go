package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"easygpa/backend/internal/events"
)

type deliveryAttempt struct {
	ID         int64
	Status     string
	ProviderID string
}

type deliveryRecorder interface {
	Lookup(context.Context, Message) (deliveryAttempt, error)
	Start(context.Context, Message) (deliveryAttempt, error)
	Finish(context.Context, int64, string, error) error
	Suppress(context.Context, Message, error) error
}

func (m *AuditedMailer) DeliveryResult(ctx context.Context, msg Message) (string, bool, error) {
	if msg.EventID == "" {
		return "", false, nil
	}
	attempt, err := m.recorder.Lookup(ctx, msg)
	if err != nil {
		return "", false, err
	}
	if attempt.Status == "sent" {
		return attempt.ProviderID, true, nil
	}
	if attempt.Status == "queued" {
		return "", true, events.DeferObserved(time.Minute, "邮件投递结果待确认，已暂停重复发送")
	}
	return "", false, nil
}

// AuditedMailer records the attempt before calling the provider. An unresolved
// attempt is never blindly resent: the provider may already have accepted it.
// Only metadata is persisted; tokens, rendered bodies and template variables
// must never enter the delivery ledger.
type AuditedMailer struct {
	next     Mailer
	recorder deliveryRecorder
}

func NewAuditedMailer(next Mailer, pool *pgxpool.Pool) (*AuditedMailer, error) {
	if next == nil || pool == nil {
		return nil, errors.New("audited mail delivery requires provider and ops database")
	}
	return &AuditedMailer{next: next, recorder: postgresDeliveryRecorder{pool: pool}}, nil
}

func (m *AuditedMailer) Send(ctx context.Context, msg Message) (string, error) {
	attempt, err := m.recorder.Start(ctx, msg)
	if err != nil {
		return "", fmt.Errorf("reserve mail delivery: %w", err)
	}
	if attempt.Status == "sent" {
		return attempt.ProviderID, nil
	}
	if attempt.Status == "queued" {
		slog.Error("mail delivery outcome needs reconciliation", "delivery_id", attempt.ID, "event_id", msg.EventID)
		return "", events.DeferObserved(time.Minute, "邮件投递结果待确认，已暂停重复发送")
	}
	providerID, sendErr := m.next.Send(ctx, msg)
	// A client disconnect must not erase the outcome of an already submitted mail.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.recorder.Finish(recordCtx, attempt.ID, providerID, sendErr); err != nil {
		slog.Error("mail delivery outcome could not be recorded", "delivery_id", attempt.ID, "event_id", msg.EventID)
		if sendErr != nil {
			return providerID, errors.Join(sendErr, fmt.Errorf("record mail delivery: %w", err))
		}
		if msg.EventID == "" {
			// An accepted verification code must remain valid even when the
			// ledger needs reconciliation after this synchronous request.
			return providerID, nil
		}
		return providerID, events.DeferObserved(time.Minute, "邮件已提交，等待投递记录确认")
	}
	if sendErr != nil && !definitelyNotSubmitted(sendErr) && msg.EventID != "" {
		return providerID, events.DeferObserved(time.Minute, "邮件服务商响应不明确，请核对投递结果")
	}
	return providerID, sendErr
}

func (m *AuditedMailer) Ready(ctx context.Context) error {
	if checker, ok := m.next.(interface{ Ready(context.Context) error }); ok {
		return checker.Ready(ctx)
	}
	return nil
}

// Synchronous mail rejected before a provider call remains visible to operators.
func (m *AuditedMailer) RecordSuppressed(ctx context.Context, msg Message, reason error) error {
	return m.recorder.Suppress(ctx, msg, reason)
}

type postgresDeliveryRecorder struct{ pool *pgxpool.Pool }

func (r postgresDeliveryRecorder) Lookup(ctx context.Context, msg Message) (deliveryAttempt, error) {
	var result deliveryAttempt
	err := r.pool.QueryRow(ctx, `SELECT delivery_id,delivery_status,message_id FROM ops_lookup_mail_delivery($1::uuid,$2)`, msg.EventID, strings.ToLower(strings.TrimSpace(msg.To))).Scan(&result.ID, &result.Status, &result.ProviderID)
	return result, err
}

func (r postgresDeliveryRecorder) Start(ctx context.Context, msg Message) (deliveryAttempt, error) {
	var result deliveryAttempt
	err := r.pool.QueryRow(ctx, `SELECT delivery_id,delivery_status,message_id
	  FROM ops_start_mail_delivery(NULLIF($1,0),NULLIF($2,'')::uuid,$3,$4)`,
		msg.ClassID, msg.EventID, strings.ToLower(strings.TrimSpace(msg.To)), msg.Template).
		Scan(&result.ID, &result.Status, &result.ProviderID)
	return result, err
}

func (r postgresDeliveryRecorder) Finish(ctx context.Context, id int64, providerID string, sendErr error) error {
	status, code, detail := "sent", "", ""
	if sendErr != nil {
		status = "queued"
		if definitelyNotSubmitted(sendErr) {
			status = "failed"
		}
		var suppressed *SuppressedError
		if errors.As(sendErr, &suppressed) {
			status = "suppressed"
		}
		// Provider error messages can contain an address or request data. Store
		// only the structured code, never arbitrary provider error text.
		code = deliveryFailureCode(sendErr)
		detail = code
	}
	_, err := r.pool.Exec(ctx, `SELECT ops_finish_mail_delivery($1,$2,$3,$4,$5)`, id, status, providerID, code, detail)
	return err
}

func (r postgresDeliveryRecorder) Suppress(ctx context.Context, msg Message, reason error) error {
	attempt, err := r.Start(ctx, msg)
	if err != nil {
		return err
	}
	if attempt.Status != "new" {
		return nil
	}
	code := "Policy.Unavailable"
	var limited *MailRateLimitError
	if errors.As(reason, &limited) {
		code = "Policy.RateLimited"
	}
	_, err = r.pool.Exec(ctx, `SELECT ops_finish_mail_delivery($1,'suppressed','',$2,$2)`, attempt.ID, code)
	return err
}

func deliveryFailureCode(err error) string {
	var suppressed *SuppressedError
	if errors.As(err, &suppressed) {
		return "Suppression." + suppressed.Reason
	}
	var coded interface{ GetCode() string }
	if errors.As(err, &coded) {
		return coded.GetCode()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Client.Timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "Client.Canceled"
	}
	return "Client.SendFailed"
}

func definitelyNotSubmitted(err error) bool {
	var local *NotSubmittedError
	if errors.As(err, &local) {
		return true
	}
	var coded interface{ GetCode() string }
	if !errors.As(err, &coded) {
		return false
	}
	code := coded.GetCode()
	for _, prefix := range []string{"FailedOperation", "LimitExceeded", "InvalidParameter", "UnauthorizedOperation", "AuthFailure", "ResourceNotFound", "OperationDenied", "UnsupportedOperation", "MissingParameter", "RequestLimitExceeded"} {
		if code == prefix || strings.HasPrefix(code, prefix+".") {
			return true
		}
	}
	return false
}
