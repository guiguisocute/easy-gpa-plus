package notify

import (
	"context"
	"errors"
	"log/slog"

	"easygpa/backend/internal/events"
)

// PolicyMailer applies provider-wide recipient budgets to every mail channel.
// Only event notifications may be deferred; synchronous authentication mail
// returns an error so an expired challenge is never silently queued for later.
type PolicyMailer struct {
	next   Mailer
	policy *RuntimePolicy
}

func NewPolicyMailer(next Mailer, policy *RuntimePolicy) (*PolicyMailer, error) {
	if next == nil || policy == nil {
		return nil, errors.New("mail policy dependencies are required")
	}
	return &PolicyMailer{next: next, policy: policy}, nil
}

func (m *PolicyMailer) Send(ctx context.Context, msg Message) (string, error) {
	// Accepted and unresolved attempts must be resolved before reserving any
	// quota: polling an existing delivery is not another provider request.
	if lookup, ok := m.next.(interface {
		DeliveryResult(context.Context, Message) (string, bool, error)
	}); ok {
		id, done, err := lookup.DeliveryResult(ctx, msg)
		if err != nil || done {
			return id, err
		}
	}
	if err := m.policy.BeforeMessage(ctx, msg); err != nil {
		var limited *MailRateLimitError
		if msg.EventID != "" && errors.As(err, &limited) {
			return "", events.DeferObserved(limited.After, limited.Reason)
		}
		if msg.EventID == "" {
			if recorder, ok := m.next.(interface {
				RecordSuppressed(context.Context, Message, error) error
			}); ok {
				if recordErr := recorder.RecordSuppressed(ctx, msg, err); recordErr != nil {
					return "", errors.Join(err, recordErr)
				}
			}
		}
		return "", err
	}
	id, err := m.next.Send(ctx, msg)
	if !isSESFrequencyLimit(err) {
		return id, err
	}
	if cooldownErr := m.policy.coolRecipient(ctx, msg.To); cooldownErr != nil {
		// The event still backs off even if Redis cannot persist the shared
		// cooldown. The next policy check fails closed until Redis recovers.
		slog.Error("mail recipient cooldown could not be saved", "error", cooldownErr)
	}
	if msg.EventID != "" {
		return "", events.DeferObserved(frequencyCooldown, "邮件服务商按收件人限流，等待冷却")
	}
	return id, &MailRateLimitError{After: frequencyCooldown, Reason: "邮件服务商按收件人限流，请稍后重试"}
}

func (m *PolicyMailer) Ready(ctx context.Context) error {
	if checker, ok := m.next.(interface{ Ready(context.Context) error }); ok {
		return checker.Ready(ctx)
	}
	return nil
}
