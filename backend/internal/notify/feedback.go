package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	ses "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ses/v20201002"
)

type sesStatusAPI interface {
	GetSendEmailStatusWithContext(context.Context, *ses.GetSendEmailStatusRequest) (*ses.GetSendEmailStatusResponse, error)
}

// Poll authenticated SES status, including while business mail is paused.
// Untrusted callbacks only wake this reader; they never change consent.
func (m *RuntimeSESMailer) RunFeedback(ctx context.Context, pool *pgxpool.Pool) error {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		if err := m.SyncFeedback(ctx, pool); err != nil && ctx.Err() == nil {
			slog.Warn("SES feedback sync unavailable")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (m *RuntimeSESMailer) SyncFeedback(ctx context.Context, pool *pgxpool.Pool) error {
	provider, err := m.resolve(ctx)
	if err != nil {
		return err
	}
	concrete, ok := provider.(*SESMailer)
	if !ok {
		return nil // Other providers' receipts are not Tencent message IDs.
	}
	client, ok := concrete.api.(sesStatusAPI)
	if !ok {
		return errors.New("SES feedback API unavailable")
	}
	rows, err := pool.Query(ctx, `SELECT id,provider_id,recipient,created_at FROM mail_delivery
        WHERE provider='tencent_ses' AND status='sent' AND provider_id<>'' AND provider_id NOT LIKE 'smtp:%' AND provider_id NOT LIKE 'aliyun:%' AND provider_id NOT LIKE 'resend:%' AND feedback_next_at<=now()
        ORDER BY feedback_next_at,id LIMIT 30`)
	if err != nil {
		return err
	}
	type pending struct {
		id               int64
		message, address string
		created          time.Time
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.message, &item.address, &item.created); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		status, err := fetchSESStatus(ctx, client, item.message, item.address, item.created)
		if err != nil {
			// Back off errors as well, so an old failed lookup cannot starve
			// feedback for other recipients. Never log the provider's text.
			if _, updateErr := pool.Exec(ctx, `UPDATE mail_delivery SET feedback_next_at=now()+interval '30 minutes' WHERE id=$1`, item.id); updateErr != nil {
				return updateErr
			}
			continue
		}
		label, scope, reason := classifySESStatus(status)
		if scope != "" {
			if err = saveSuppression(ctx, pool, item.address, scope, reason); err != nil {
				return err
			}
		}
		delay := 6 * time.Hour
		if time.Since(item.created) < 48*time.Hour {
			delay = 10 * time.Minute
		}
		if time.Since(item.created) > 30*24*time.Hour {
			delay = 24 * time.Hour
		}
		if _, err = pool.Exec(ctx, `UPDATE mail_delivery SET feedback_status=$2,feedback_checked_at=now(),feedback_next_at=$3 WHERE id=$1`, item.id, label, time.Now().Add(delay)); err != nil {
			return err
		}
	}
	return nil
}
func fetchSESStatus(ctx context.Context, api sesStatusAPI, message, address string, created time.Time) (*ses.SendEmailStatus, error) {
	// Tencent uses a calendar date; cover both documented region-local and
	// UTC boundaries without assuming which one an account's index uses.
	dates := []string{created.In(mailLocation).Format("2006-01-02")}
	if utc := created.UTC().Format("2006-01-02"); utc != dates[0] {
		dates = append(dates, utc)
	}
	for _, date := range dates {
		request := ses.NewGetSendEmailStatusRequest()
		request.RequestDate = common.StringPtr(date)
		request.MessageId = common.StringPtr(message)
		request.Offset = common.Uint64Ptr(0)
		request.Limit = common.Uint64Ptr(100)
		response, err := api.GetSendEmailStatusWithContext(ctx, request)
		if err != nil {
			return nil, err
		}
		if response == nil || response.Response == nil {
			continue
		}
		for _, status := range response.Response.EmailStatusList {
			if status != nil && status.MessageId != nil && *status.MessageId == message && status.ToEmailAddress != nil && strings.EqualFold(strings.TrimSpace(*status.ToEmailAddress), strings.TrimSpace(address)) {
				return status, nil
			}
		}
	}
	return nil, nil
}
func classifySESStatus(s *ses.SendEmailStatus) (string, string, string) {
	if s == nil {
		return "not_found", "", ""
	}
	if (s.UserComplained != nil && *s.UserComplained) || (s.UserComplainted != nil && *s.UserComplainted) {
		return "complaint", "business", "provider_complaint"
	}
	if s.UserUnsubscribed != nil && *s.UserUnsubscribed {
		return "unsubscribed", "business", "provider_unsubscribed"
	}
	if s.SendStatus != nil {
		switch *s.SendStatus {
		case 1013:
			return "unsubscribed", "business", "provider_unsubscribed"
		case 1007, 1008:
			return "rejected", "business", "provider_rejected"
		case 3024:
			return "invalid_address", "all", "invalid_address"
		}
	}
	if s.DeliverStatus != nil {
		switch *s.DeliverStatus {
		case 1:
			return "delivered", "", ""
		case 2:
			return "dropped", "", ""
		case 3:
			// A rejection is not always a nonexistent mailbox. Suppress all
			// mail only for the explicit SMTP enhanced invalid-address code.
			if s.DeliverMessage != nil && strings.Contains(*s.DeliverMessage, "5.1.1") {
				return "hard_bounce", "all", "invalid_address"
			}
			return "rejected", "", ""
		case 8:
			return "deferred", "", ""
		}
	}
	return "accepted", "", ""
}
