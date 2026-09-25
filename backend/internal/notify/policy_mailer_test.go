package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	sdkerrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
)

func redisMailPolicy(t *testing.T) (*RuntimePolicy, *time.Time) {
	t.Helper()
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR is required for real Redis mail policy tests")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("TEST_REDIS_PASSWORD")})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 7, 0, 0, 0, shanghaiLocation)
	config := opsconfig.DefaultMail()
	config.PerMinute, config.PerDay = 1000, 10000
	policy := &RuntimePolicy{config: stubMailSettings{mail: config}, redis: client,
		now: func() time.Time { return now }, keyPrefix: "easygpa:test:mail:" + uuid.NewString() + ":"}
	t.Cleanup(func() {
		ctx := context.Background()
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, policy.prefix()+"*", 100).Result()
			if err != nil {
				t.Error(err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
				}
			}
			if next == 0 {
				break
			}
			cursor = next
		}
		_ = client.Close()
	})
	return policy, &now
}

func expectMailLimit(t *testing.T, err error, after time.Duration) {
	t.Helper()
	var limited *MailRateLimitError
	if !errors.As(err, &limited) || limited.After != after {
		t.Fatalf("limit = %v, want delay %v", err, after)
	}
}

func TestMailRecipientIntervalIsSharedAcrossClassesAndNormalizedAddresses(t *testing.T) {
	policy, now := redisMailPolicy(t)
	ctx := context.Background()
	msg := Message{To: "Student <student@example.test>", EventID: "event-1", ClassID: 1}
	if err := policy.BeforeMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(notificationInterval - time.Millisecond)
	msg.To, msg.ClassID = "STUDENT@example.test", 2
	expectMailLimit(t, policy.BeforeMessage(ctx, msg), time.Millisecond)
	msg.To = "another@example.test"
	if err := policy.BeforeMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Millisecond)
	msg.To = "student@example.test"
	if err := policy.BeforeMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	count, err := policy.redis.Get(ctx, policy.prefix()+"day:"+now.Format("20060102")).Int()
	if err != nil || count != 3 {
		t.Fatalf("daily reservations=%d, error=%v; rejected sends must not count", count, err)
	}
	keys, err := policy.redis.Keys(ctx, policy.prefix()+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if strings.Contains(key, "student") || strings.Contains(key, "example.test") {
			t.Fatalf("recipient exposed in Redis key")
		}
	}
}

func TestFrequentChoiceRetainsMinuteAndRollingHourProtection(t *testing.T) {
	policy, now := redisMailPolicy(t)
	msg := Message{To: "frequent@example.test", EventID: "chosen-event", Template: TemplateNotificationAlert, Frequent: true}
	for i := range notificationHourLimit {
		if err := policy.BeforeMessage(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
		wait := frequentInterval
		if i == notificationHourLimit-1 {
			wait = time.Hour - time.Duration(i)*frequentInterval
		}
		expectMailLimit(t, policy.BeforeMessage(t.Context(), msg), wait)
		*now = now.Add(frequentInterval)
		if i == notificationHourLimit-1 {
			expectMailLimit(t, policy.BeforeMessage(t.Context(), msg), time.Hour-time.Duration(notificationHourLimit)*frequentInterval)
		}
	}
}

func TestMailRollingHourBudgetIsAtomicAndDoesNotResetAtClockHour(t *testing.T) {
	policy, now := redisMailPolicy(t)
	*now = now.Add(59 * time.Minute)
	var accepted, limited atomic.Int32
	var group sync.WaitGroup
	for range 24 {
		group.Go(func() {
			err := policy.BeforeMessage(context.Background(), Message{To: "same@example.test"})
			if err == nil {
				accepted.Add(1)
				return
			}
			var limit *MailRateLimitError
			if errors.As(err, &limit) {
				limited.Add(1)
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	group.Wait()
	if accepted.Load() != 10 || limited.Load() != 14 {
		t.Fatalf("accepted=%d, limited=%d", accepted.Load(), limited.Load())
	}
	*now = now.Add(time.Minute)
	expectMailLimit(t, policy.BeforeMessage(context.Background(), Message{To: "same@example.test"}), 59*time.Minute)
	*now = now.Add(59*time.Minute - time.Millisecond)
	expectMailLimit(t, policy.BeforeMessage(context.Background(), Message{To: "same@example.test"}), time.Millisecond)
	*now = now.Add(time.Millisecond)
	if err := policy.BeforeMessage(context.Background(), Message{To: "same@example.test"}); err != nil {
		t.Fatal(err)
	}
}

func TestMailNotificationsReserveTwoHourlySlotsForSynchronousMessages(t *testing.T) {
	policy, now := redisMailPolicy(t)
	ctx := context.Background()
	msg := Message{To: "same@example.test", EventID: "notification"}
	for index := range notificationHourLimit {
		if index > 0 {
			*now = now.Add(notificationInterval)
		}
		if err := policy.BeforeMessage(ctx, msg); err != nil {
			t.Fatalf("notification %d: %v", index, err)
		}
	}
	expectMailLimit(t, policy.BeforeMessage(ctx, msg), notificationInterval)
	msg.EventID = ""
	for range 2 {
		if err := policy.BeforeMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	expectMailLimit(t, policy.BeforeMessage(ctx, msg), 4*time.Minute)
	*now = now.Add(notificationInterval)
	msg.EventID = "next-notification"
	if err := policy.BeforeMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
}

func TestMailSynchronousMessagesBypassQuietHoursButStillShareProviderBudget(t *testing.T) {
	policy, now := redisMailPolicy(t)
	*now = time.Date(2026, 9, 4, 23, 0, 0, 0, shanghaiLocation)
	ctx := context.Background()
	for range recipientHourLimit {
		if err := policy.BeforeMessage(ctx, Message{To: "same@example.test"}); err != nil {
			t.Fatal(err)
		}
	}
	expectMailLimit(t, policy.BeforeMessage(ctx, Message{To: "same@example.test"}), time.Hour)
	var deferred events.DeferredError
	err := policy.BeforeMessage(ctx, Message{To: "other@example.test", EventID: "notification"})
	if !errors.As(err, &deferred) || deferred.After != 8*time.Hour {
		t.Fatalf("quiet hours error=%v", err)
	}
}

type policyRecordingMailer struct {
	calls int
	err   error
}

func (m *policyRecordingMailer) Send(context.Context, Message) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	return "provider-id", nil
}

type policySuppressionRecorder struct {
	policyRecordingMailer
	suppressed int
}

type policyResultLookup struct {
	policyRecordingMailer
	id        string
	done      bool
	lookupErr error
}

func (m *policyResultLookup) DeliveryResult(context.Context, Message) (string, bool, error) {
	return m.id, m.done, m.lookupErr
}

func TestMailExistingDeliveryDoesNotReserveQuota(t *testing.T) {
	for _, status := range []string{"sent", "queued", "lookup-failure"} {
		t.Run(status, func(t *testing.T) {
			policy, now := redisMailPolicy(t)
			next := &policyResultLookup{id: "accepted", done: true}
			if status == "queued" {
				next.id = ""
				next.lookupErr = events.DeferObserved(time.Minute, "邮件投递结果待确认")
			}
			if status == "lookup-failure" {
				next.id = ""
				next.done = false
				next.lookupErr = errors.New("database unavailable")
			}
			mailer, err := NewPolicyMailer(next, policy)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			for range 9 {
				id, err := mailer.Send(ctx, Message{To: "same@example.test", EventID: "event"})
				if id != next.id || !errors.Is(err, next.lookupErr) {
					t.Fatalf("lookup result id=%q err=%v", id, err)
				}
				*now = now.Add(notificationInterval)
			}
			keys, err := policy.redis.Keys(ctx, policy.prefix()+"*").Result()
			if err != nil || len(keys) != 0 || next.calls != 0 {
				t.Fatalf("existing outcome consumed quota: keys=%d provider calls=%d err=%v", len(keys), next.calls, err)
			}
			next.id, next.done, next.lookupErr = "", false, nil
			if _, err := mailer.Send(ctx, Message{To: "same@example.test", EventID: "new-event"}); err != nil {
				t.Fatal(err)
			}
			if next.calls != 1 {
				t.Fatalf("new event provider calls=%d", next.calls)
			}
		})
	}
}

func (m *policySuppressionRecorder) RecordSuppressed(context.Context, Message, error) error {
	m.suppressed++
	return nil
}

func TestMailPolicyRecordsSynchronousSuppressionWithoutRecordingDeferredNotifications(t *testing.T) {
	policy, _ := redisMailPolicy(t)
	next := &policySuppressionRecorder{}
	mailer, err := NewPolicyMailer(next, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range recipientHourLimit {
		if _, err := mailer.Send(ctx, Message{To: "same@example.test"}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = mailer.Send(ctx, Message{To: "same@example.test"})
	expectMailLimit(t, err, time.Hour)
	if next.calls != recipientHourLimit || next.suppressed != 1 {
		t.Fatalf("sent=%d suppressed=%d", next.calls, next.suppressed)
	}
	_, err = mailer.Send(ctx, Message{To: "same@example.test", EventID: "notification"})
	var deferred events.DeferredError
	if !errors.As(err, &deferred) || next.suppressed != 1 {
		t.Fatalf("notification error=%v suppressed=%d", err, next.suppressed)
	}
}

func TestMailPolicyLeavesPermanentProviderErrorsUnchanged(t *testing.T) {
	policy, _ := redisMailPolicy(t)
	providerErr := sdkerrors.NewTencentCloudSDKError("FailedOperation.TemplateNotApproved", "rejected", "request")
	next := &policyRecordingMailer{err: providerErr}
	mailer, err := NewPolicyMailer(next, policy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mailer.Send(context.Background(), Message{To: "same@example.test", EventID: "notification"})
	if !errors.Is(err, providerErr) {
		t.Fatalf("provider error changed: %v", err)
	}
}

func TestMailSixteenNightNotificationsDrainWithoutProviderBurst(t *testing.T) {
	policy, now := redisMailPolicy(t)
	next := &policyRecordingMailer{}
	mailer, err := NewPolicyMailer(next, policy)
	if err != nil {
		t.Fatal(err)
	}
	*now = time.Date(2026, 9, 3, 23, 0, 0, 0, shanghaiLocation)
	pending := make([]Message, 16)
	for index := range pending {
		pending[index] = Message{To: "same@example.test", EventID: fmt.Sprintf("event-%d", index), ClassID: 764}
		_, err := mailer.Send(context.Background(), pending[index])
		var deferred events.DeferredError
		if !errors.As(err, &deferred) || deferred.After != 8*time.Hour {
			t.Fatalf("night event %d: %v", index, err)
		}
	}
	if next.calls != 0 {
		t.Fatalf("provider called during quiet hours: %d", next.calls)
	}
	*now = time.Date(2026, 9, 4, 7, 0, 0, 0, shanghaiLocation)
	for slot := 0; len(pending) > 0 && slot < 16; slot++ {
		remaining := make([]Message, 0, len(pending))
		before := next.calls
		for _, msg := range pending {
			_, err := mailer.Send(context.Background(), msg)
			if err == nil {
				continue
			}
			var deferred events.DeferredError
			if !errors.As(err, &deferred) {
				t.Fatalf("notification consumed a failure instead of deferring: %v", err)
			}
			remaining = append(remaining, msg)
		}
		if next.calls-before != 1 {
			t.Fatalf("slot %d attempted %d sends", slot, next.calls-before)
		}
		pending = remaining
		*now = now.Add(notificationInterval)
	}
	if len(pending) != 0 || next.calls != 16 {
		t.Fatalf("pending=%d provider calls=%d", len(pending), next.calls)
	}
}

func TestMailProviderFrequencyLimitCreatesSharedCooldownWithoutFailureBudget(t *testing.T) {
	policy, now := redisMailPolicy(t)
	providerErr := fmt.Errorf("wrapped: %w", sdkerrors.NewTencentCloudSDKError("FailedOperation.FrequencyLimit", "limited", "request"))
	next := &policyRecordingMailer{err: providerErr}
	mailer, err := NewPolicyMailer(next, policy)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{To: "same@example.test", EventID: "event"}
	for range 7 {
		_, err := mailer.Send(context.Background(), msg)
		var deferred events.DeferredError
		if !errors.As(err, &deferred) || deferred.After != frequencyCooldown {
			t.Fatalf("error=%v, want cooldown deferral", err)
		}
	}
	if next.calls != 1 {
		t.Fatalf("cooldown retried provider %d times", next.calls)
	}
	msg.EventID = ""
	_, err = mailer.Send(context.Background(), msg)
	expectMailLimit(t, err, frequencyCooldown)
	*now = now.Add(frequencyCooldown)
	next.err = nil
	if _, err := mailer.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
}

func TestMailSynchronousProviderFrequencyLimitRemainsAnError(t *testing.T) {
	policy, _ := redisMailPolicy(t)
	providerErr := sdkerrors.NewTencentCloudSDKError("FailedOperation.FrequencyLimit", "limited", "request")
	next := &policyRecordingMailer{err: providerErr}
	mailer, err := NewPolicyMailer(next, policy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mailer.Send(context.Background(), Message{To: "same@example.test"})
	var deferred events.DeferredError
	expectMailLimit(t, err, frequencyCooldown)
	if errors.As(err, &deferred) {
		t.Fatalf("synchronous error=%v", err)
	}
}

func TestMailGlobalBudgetsStillApplyWithoutConsumingRecipientReservations(t *testing.T) {
	for _, daily := range []bool{false, true} {
		t.Run(fmt.Sprintf("daily=%v", daily), func(t *testing.T) {
			policy, now := redisMailPolicy(t)
			config := opsconfig.DefaultMail()
			config.PerMinute, config.PerDay = 1, 100
			if daily {
				config.PerDay = 1
			}
			policy.config = stubMailSettings{mail: config}
			ctx := context.Background()
			if err := policy.BeforeMessage(ctx, Message{To: "first@example.test"}); err != nil {
				t.Fatal(err)
			}
			expected := time.Minute
			if daily {
				expected = 17 * time.Hour
			}
			expectMailLimit(t, policy.BeforeMessage(ctx, Message{To: "second@example.test"}), expected)
			hash, _ := recipientHash("second@example.test")
			if count, err := policy.redis.ZCard(ctx, policy.prefix()+"recipient:"+hash+":all").Result(); err != nil || count != 0 {
				t.Fatalf("rejected recipient count=%d err=%v", count, err)
			}
			*now = now.Add(expected - time.Microsecond)
			expectMailLimit(t, policy.BeforeMessage(ctx, Message{To: "second@example.test"}), time.Millisecond)
			*now = now.Add(time.Microsecond)
			if err := policy.BeforeMessage(ctx, Message{To: "second@example.test"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
