package notify

import (
	"context"
	"errors"
	"fmt"
	"testing"

	sdkerrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"

	"easygpa/backend/internal/events"
)

type auditTestRecorder struct {
	attempt             deliveryAttempt
	startErr, finishErr error
	finishCalls         int
	finishContextErr    error
	providerErr         error
}

func (r *auditTestRecorder) Lookup(context.Context, Message) (deliveryAttempt, error) {
	return r.attempt, r.startErr
}

func (r *auditTestRecorder) Start(context.Context, Message) (deliveryAttempt, error) {
	return r.attempt, r.startErr
}
func (r *auditTestRecorder) Finish(ctx context.Context, _ int64, _ string, err error) error {
	r.finishCalls++
	r.finishContextErr = ctx.Err()
	r.providerErr = err
	return r.finishErr
}
func (r *auditTestRecorder) Suppress(context.Context, Message, error) error { return nil }

type auditTestSender struct {
	calls  int
	err    error
	cancel context.CancelFunc
}

func (s *auditTestSender) Send(context.Context, Message) (string, error) {
	s.calls++
	if s.cancel != nil {
		s.cancel()
	}
	return "provider-message", s.err
}

func TestAuditedMailerNeverResendsAcceptedOrUnresolvedEvent(t *testing.T) {
	for _, status := range []string{"sent", "queued"} {
		t.Run(status, func(t *testing.T) {
			sender := &auditTestSender{}
			recorder := &auditTestRecorder{attempt: deliveryAttempt{ID: 1, Status: status, ProviderID: "accepted"}}
			m := &AuditedMailer{next: sender, recorder: recorder}
			id, err := m.Send(t.Context(), Message{EventID: "event"})
			if sender.calls != 0 || recorder.finishCalls != 0 {
				t.Fatal("existing attempt was sent again")
			}
			if status == "sent" && (err != nil || id != "accepted") {
				t.Fatal("accepted result was not preserved")
			}
			var deferred events.DeferredError
			if status == "queued" && !errors.As(err, &deferred) {
				t.Fatalf("unresolved attempt must wait: %v", err)
			}
		})
	}
}

func TestAuditedMailerRequiresLedgerBeforeProviderAndRecordsAfterDisconnect(t *testing.T) {
	sender := &auditTestSender{}
	recorder := &auditTestRecorder{startErr: errors.New("database unavailable")}
	m := &AuditedMailer{next: sender, recorder: recorder}
	if _, err := m.Send(t.Context(), Message{}); err == nil || sender.calls != 0 {
		t.Fatal("sent without durable reservation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sender.cancel = cancel
	recorder.startErr = nil
	recorder.attempt = deliveryAttempt{ID: 1, Status: "new"}
	if _, err := m.Send(ctx, Message{}); err != nil {
		t.Fatal(err)
	}
	if recorder.finishCalls != 1 || recorder.finishContextErr != nil {
		t.Fatal("client cancellation prevented recording outcome")
	}
}

func TestAuditedMailerPreservesFrequencyErrorAndDefersUnrecordedSuccess(t *testing.T) {
	limited := sdkerrors.NewTencentCloudSDKError("FailedOperation.FrequencyLimit", "sensitive provider detail", "request")
	sender := &auditTestSender{err: limited}
	recorder := &auditTestRecorder{attempt: deliveryAttempt{ID: 1, Status: "new"}}
	m := &AuditedMailer{next: sender, recorder: recorder}
	if _, err := m.Send(t.Context(), Message{}); !errors.Is(err, limited) || !errors.Is(recorder.providerErr, limited) {
		t.Fatal("provider classification was lost")
	}
	sender.err = nil
	recorder.finishErr = errors.New("database unavailable")
	_, err := m.Send(t.Context(), Message{EventID: "event"})
	var deferred events.DeferredError
	if !errors.As(err, &deferred) {
		t.Fatalf("unrecorded provider success must not trigger ordinary retry: %v", err)
	}
}

func TestAcceptedSynchronousMailSurvivesLedgerFailure(t *testing.T) {
	m := &AuditedMailer{next: &auditTestSender{}, recorder: &auditTestRecorder{attempt: deliveryAttempt{ID: 1, Status: "new"}, finishErr: errors.New("database unavailable")}}
	id, err := m.Send(t.Context(), Message{Template: TemplateVerificationCode})
	if err != nil || id != "provider-message" {
		t.Fatal("accepted synchronous mail must not invalidate the challenge")
	}
}

func TestUncertainProviderOutcomeIsNotAnOrdinaryRetry(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, errors.New("missing message id")} {
		if definitelyNotSubmitted(err) {
			t.Fatal("uncertain network outcome treated as rejection")
		}
		m := &AuditedMailer{next: &auditTestSender{err: err}, recorder: &auditTestRecorder{attempt: deliveryAttempt{ID: 1, Status: "new"}}}
		_, got := m.Send(t.Context(), Message{EventID: "event"})
		var deferred events.DeferredError
		if !errors.As(got, &deferred) {
			t.Fatal("uncertain event must defer for reconciliation")
		}
	}
	if !definitelyNotSubmitted(&NotSubmittedError{Err: errors.New("configuration missing")}) {
		t.Fatal("preflight failure must be retryable")
	}
}

func TestDeliveryFailureCodeNeverPersistsProviderMessage(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", sdkerrors.NewTencentCloudSDKError("FailedOperation.FrequencyLimit", "private message body", "request"))
	if got := deliveryFailureCode(err); got != "FailedOperation.FrequencyLimit" {
		t.Fatalf("unexpected code %q", got)
	}
	if got := deliveryFailureCode(errors.New("private message body")); got != "Client.SendFailed" {
		t.Fatalf("unexpected fallback %q", got)
	}
}
