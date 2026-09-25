package notify

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SuppressedError struct{ Reason string }

func (e *SuppressedError) Error() string { return "邮件已停止发送：" + e.Reason }
func (e *SuppressedError) Unwrap() error { return &NotSubmittedError{Err: errors.New(e.Reason)} }

type SuppressionMailer struct {
	next Mailer
	pool *pgxpool.Pool
}

func NewSuppressionMailer(next Mailer, pool *pgxpool.Pool) *SuppressionMailer {
	return &SuppressionMailer{next: next, pool: pool}
}
func (m *SuppressionMailer) Ready(ctx context.Context) error {
	if ready, ok := m.next.(interface{ Ready(context.Context) error }); ok {
		return ready.Ready(ctx)
	}
	return nil
}
func (m *SuppressionMailer) Send(ctx context.Context, msg Message) (string, error) {
	if m.pool == nil {
		return "", &NotSubmittedError{Err: errors.New("邮件拒收记录暂不可用")}
	}
	var reason string
	err := m.pool.QueryRow(ctx, `SELECT reason FROM mail_suppression WHERE recipient_hash=md5(lower(trim($1))) AND (scope='all' OR $2) ORDER BY scope LIMIT 1`, msg.To, isBusinessMail(msg)).Scan(&reason)
	if err == nil {
		return "", &SuppressedError{Reason: reason}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", &NotSubmittedError{Err: errors.New("邮件拒收记录暂不可用")}
	}
	id, sendErr := m.next.Send(ctx, msg)
	scope, reason := permanentSESFailure(sendErr)
	if scope == "" {
		return id, sendErr
	}
	if err := saveSuppression(ctx, m.pool, msg.To, scope, reason); err != nil {
		return id, errors.Join(sendErr, errors.New("邮件拒收记录保存失败"))
	}
	return id, &SuppressedError{Reason: reason}
}
func permanentSESFailure(err error) (string, string) {
	code := deliveryFailureCode(err)
	switch code {
	case "FailedOperation.ReceiverHasUnsubscribed":
		return "business", "provider_unsubscribed"
	case "FailedOperation.RejectedByRecipients", "FailedOperation.EmailAddrInBlacklist":
		return "business", "provider_rejected"
	case "InvalidParameterValue.ReceiverEmailAddress":
		return "all", "invalid_address"
	}
	return "", ""
}
func saveSuppression(ctx context.Context, pool *pgxpool.Pool, address, scope, reason string) error {
	_, err := pool.Exec(ctx, `INSERT INTO mail_suppression(recipient_hash,scope,reason) VALUES(md5(lower(trim($1))),$2,$3)
        ON CONFLICT(recipient_hash,scope) DO UPDATE SET updated_at=now(),reason=CASE WHEN mail_suppression.reason='provider_complaint' THEN mail_suppression.reason ELSE EXCLUDED.reason END`, strings.TrimSpace(address), scope, reason)
	return err
}
