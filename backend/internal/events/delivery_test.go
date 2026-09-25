package events

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// These tests use a dedicated logical Redis database and remove only the event
// IDs/groups they created. They never FLUSHDB or touch development worker data.
func deliveryTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("EASYGPA_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set EASYGPA_TEST_REDIS_ADDR to run stream delivery integration tests")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func deliveryTestMessage(t *testing.T, client *redis.Client, groups ...string) redis.XMessage {
	t.Helper()
	ctx := context.Background()
	for _, group := range groups {
		if err := client.XGroupCreateMkStream(ctx, StreamName, group, "$").Err(); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]any{"event_id": uuid.NewString(), "class_id": "1", "type": ReviewDecided, "payload": `{}`}
	id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: StreamName, Values: values}).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		deliveryRead(t, client, group)
	}
	t.Cleanup(func() {
		for _, group := range groups {
			_ = client.XGroupDestroy(ctx, StreamName, group).Err()
			eventID := values["event_id"].(string)
			_ = client.Del(ctx, deliveryKey(group, eventID), deferredKey(group, eventID), deliveryLeaseKey(group, eventID), deliveryAlertLogKey(group, eventID)).Err()
			_ = client.ZRem(ctx, deliveryObservedKey, deliveryAlertMember(group, eventID)).Err()
			_ = client.HDel(ctx, deliveryObservedDetailsKey, deliveryAlertMember(group, eventID)).Err()
			dead, _ := client.XRange(ctx, DeadStream, "-", "+").Result()
			for _, row := range dead {
				if row.Values["group"] == group {
					_ = client.XDel(ctx, DeadStream, row.ID).Err()
				}
			}
		}
		_ = client.XDel(ctx, StreamName, id).Err()
	})
	return redis.XMessage{ID: id, Values: values}
}

func deliveryRead(t *testing.T, client *redis.Client, group string) redis.XMessage {
	t.Helper()
	streams, err := client.XReadGroup(context.Background(), &redis.XReadGroupArgs{Group: group, Consumer: "test", Streams: []string{StreamName, ">"}, Count: 1, Block: -1}).Result()
	if err != nil || len(streams) != 1 || len(streams[0].Messages) != 1 {
		t.Fatalf("read %s: %v, %v", group, streams, err)
	}
	return streams[0].Messages[0]
}

func deliveryTestGroup() string { return "delivery-test-" + uuid.NewString() }

func TestDeferredDeliveryDoesNotExhaustAttempts(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	calls := 0
	for i := 0; i < maxDeliveries+3; i++ {
		handleMessage(ctx, client, group, message, func(context.Context, Event) error { calls++; return DeferObserved(0, "test frequency limit") })
	}
	if calls != maxDeliveries+3 {
		t.Fatalf("handler calls = %d", calls)
	}
	if count := client.Exists(ctx, deliveryKey(group, message.Values["event_id"].(string))).Val(); count != 0 {
		t.Fatal("temporary deferral consumed an attempt")
	}
	if pending := client.XPending(ctx, StreamName, group).Val().Count; pending != 1 {
		t.Fatalf("pending = %d", pending)
	}
	handleMessage(ctx, client, group, message, func(context.Context, Event) error { return nil })
	if pending := client.XPending(ctx, StreamName, group).Val().Count; pending != 0 {
		t.Fatalf("success pending = %d", pending)
	}
}

func TestDeadLetterReplayTargetsOriginalGroupAndResetsOnlyItsState(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group, other := deliveryTestGroup(), deliveryTestGroup()
	message := deliveryTestMessage(t, client, group, other)
	eventID := message.Values["event_id"].(string)
	for i := 0; i < maxDeliveries; i++ {
		handleMessage(ctx, client, group, message, func(context.Context, Event) error { return errors.New("permanent failure") })
	}
	if pending := client.XPending(ctx, StreamName, group).Val().Count; pending != 0 {
		t.Fatal("dead message was not acknowledged")
	}
	_ = client.Set(ctx, deliveryKey(other, eventID), "3", time.Hour).Err()
	_ = client.Set(ctx, deferredKey(other, eventID), "other-deferred", time.Hour).Err()
	_ = markDeferred(ctx, client, group, eventID, time.Hour)
	allDead, err := client.XRange(ctx, DeadStream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var dead redis.XMessage
	for _, item := range allDead {
		if item.Values["group"] == group {
			dead = item
		}
	}
	if dead.ID == "" {
		t.Fatal("dead letter missing")
	}
	replays, err := RedeliverDeadLetters(ctx, client, []redis.XMessage{dead})
	if err != nil || len(replays) != 1 || replays[0].EventID != eventID || replays[0].Group != group {
		t.Fatalf("replay: %#v %v", replays, err)
	}
	if remaining := client.Exists(ctx, deliveryKey(group, eventID), deferredKey(group, eventID)).Val(); remaining != 0 {
		t.Fatal("original delivery state was not reset")
	}
	if client.Get(ctx, deliveryKey(other, eventID)).Val() != "3" || client.Get(ctx, deferredKey(other, eventID)).Val() != "other-deferred" {
		t.Fatal("unrelated group state changed")
	}
	if _, err := RedeliverDeadLetters(ctx, client, []redis.XMessage{dead}); !errors.Is(err, ErrDeadLetterChanged) {
		t.Fatalf("duplicate replay error = %v", err)
	}
	staleCalls := 0
	handleMessage(ctx, client, group, message, func(context.Context, Event) error { staleCalls++; return nil })
	if staleCalls != 0 {
		t.Fatal("stale original message ran after replay")
	}
	replay := deliveryRead(t, client, group)
	t.Cleanup(func() { _ = client.XDel(ctx, StreamName, replay.ID).Err() })
	if replay.Values["event_id"] != eventID || replay.Values["target_group"] != group {
		t.Fatalf("bad replay routing: %#v", replay.Values)
	}
	otherReplay := deliveryRead(t, client, other)
	handleMessage(ctx, client, other, otherReplay, func(context.Context, Event) error { t.Fatal("non-target group handler ran"); return nil })
	if client.Get(ctx, deliveryKey(other, eventID)).Val() != "3" || client.Get(ctx, deferredKey(other, eventID)).Val() != "other-deferred" {
		t.Fatal("skip reset unrelated state")
	}
	handleMessage(ctx, client, group, replay, func(context.Context, Event) error { return errors.New("one new failure") })
	if attempts := client.Get(ctx, deliveryKey(group, eventID)).Val(); attempts != "1" {
		t.Fatalf("replay attempts = %s", attempts)
	}
}

func TestConcurrentReplayAndMissingBatchCannotDuplicateOrPartiallyReset(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	eventID := message.Values["event_id"].(string)
	values := map[string]any{"event_id": eventID, "class_id": "1", "type": ReviewDecided, "payload": `{}`, "group": group}
	id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: DeadStream, Values: values}).Result()
	if err != nil {
		t.Fatal(err)
	}
	dead := redis.XMessage{ID: id, Values: values}
	_ = client.Set(ctx, deliveryKey(group, eventID), "5", time.Hour).Err()
	missing := redis.XMessage{ID: "1-0", Values: values}
	if _, err := RedeliverDeadLetters(ctx, client, []redis.XMessage{dead, missing}); !errors.Is(err, ErrDeadLetterChanged) {
		t.Fatalf("missing batch err=%v", err)
	}
	if client.Get(ctx, deliveryKey(group, eventID)).Val() != "5" {
		t.Fatal("partial retry state reset")
	}
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := RedeliverDeadLetters(ctx, client, []redis.XMessage{dead}); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrDeadLetterChanged) {
				t.Errorf("concurrent replay: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful replay count=%d", successes.Load())
	}
	replay := deliveryRead(t, client, group)
	t.Cleanup(func() { _ = client.XDel(ctx, StreamName, replay.ID).Err() })
}

func TestAutoclaimCannotRunSameEventConcurrently(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	entered, proceed, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	go func() {
		defer close(done)
		handleMessage(ctx, client, group, message, func(context.Context, Event) error { calls.Add(1); close(entered); <-proceed; return nil })
	}()
	<-entered
	handleMessage(ctx, client, group, message, func(context.Context, Event) error { calls.Add(1); return nil })
	close(proceed)
	<-done
	if calls.Load() != 1 {
		t.Fatalf("concurrent handler calls=%d", calls.Load())
	}
}

func TestExpiredDeliveryCannotFinishOrReleaseReplacementLease(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	eventID := message.Values["event_id"].(string)
	leaseCtx, release, acquired := acquireDelivery(ctx, client, group, eventID, message.ID)
	if !acquired {
		t.Fatal("initial lease was not acquired")
	}
	var releaseOnce sync.Once
	stop := func() { releaseOnce.Do(release) }
	defer stop()
	leaseKey := deliveryLeaseKey(group, eventID)
	// Model expiry followed by another consumer acquiring the same event. The
	// old handler is still running and has not observed its next renewal tick.
	newToken := uuid.NewString()
	if err := client.Set(ctx, leaseKey, newToken, deliveryLeaseTTL).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, deliveryKey(group, eventID), "2", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, deferredKey(group, eventID), "replacement-deferral", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	deadBefore, err := client.XLen(ctx, DeadStream).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, dead := range []map[string]any{nil, {
		"event_id": eventID, "group": group, "stream_id": message.ID,
		"class_id": "1", "type": ReviewDecided, "payload": `{}`, "error": "stale failure",
	}} {
		if err := finishDelivery(leaseCtx, client, group, eventID, message, dead); err == nil {
			t.Fatal("stale lease was allowed to complete a delivery")
		}
	}
	if pending := client.XPending(ctx, StreamName, group).Val().Count; pending != 1 {
		t.Fatalf("stale lease acknowledged the message: pending=%d", pending)
	}
	if client.Get(ctx, deliveryKey(group, eventID)).Val() != "2" || client.Get(ctx, deferredKey(group, eventID)).Val() != "replacement-deferral" {
		t.Fatal("stale completion cleared the replacement consumer state")
	}
	if deadAfter := client.XLen(ctx, DeadStream).Val(); deadAfter != deadBefore {
		t.Fatal("stale lease created a dead letter")
	}
	stop()
	if !errors.Is(leaseCtx.Err(), context.Canceled) {
		t.Fatal("releasing a lease did not cancel its handler context")
	}
	if got := client.Get(ctx, leaseKey).Val(); got != newToken {
		t.Fatal("old lease release deleted the replacement lease")
	}
}

func TestActiveDeliveryLeaseBlocksDeadLetterReplayWithoutResettingState(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	eventID := message.Values["event_id"].(string)
	_, release, acquired := acquireDelivery(ctx, client, group, eventID, message.ID)
	if !acquired {
		t.Fatal("active lease was not acquired")
	}
	defer release()
	values := map[string]any{"event_id": eventID, "class_id": "1", "type": ReviewDecided, "payload": `{}`, "group": group}
	deadID, err := client.XAdd(ctx, &redis.XAddArgs{Stream: DeadStream, Values: values}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, deliveryKey(group, eventID), "5", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, deferredKey(group, eventID), "active-deferral", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	streamBefore, err := client.XLen(ctx, StreamName).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RedeliverDeadLetters(ctx, client, []redis.XMessage{{ID: deadID, Values: values}}); !errors.Is(err, ErrDeadLetterChanged) {
		t.Fatalf("active lease replay error=%v", err)
	}
	if streamAfter := client.XLen(ctx, StreamName).Val(); streamAfter != streamBefore {
		t.Fatal("active delivery was queued for replay")
	}
	if dead, err := client.XRangeN(ctx, DeadStream, deadID, deadID, 1).Result(); err != nil || len(dead) != 1 {
		t.Fatalf("blocked replay removed its dead letter: %v", err)
	}
	if client.Get(ctx, deliveryKey(group, eventID)).Val() != "5" || client.Get(ctx, deferredKey(group, eventID)).Val() != "active-deferral" {
		t.Fatal("blocked replay reset the active delivery state")
	}
}

func TestDeliveryRenewalOnlyExtendsCurrentTokenAndDoesNotReviveExpiry(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	key := deliveryLeaseKey(deliveryTestGroup(), uuid.NewString())
	t.Cleanup(func() { _ = client.Del(ctx, key).Err() })
	oldToken, newToken := uuid.NewString(), uuid.NewString()
	if err := client.Set(ctx, key, newToken, 10*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	renewed, err := renewDeliveryScript.Run(ctx, client, []string{key}, oldToken, deliveryLeaseTTL.Milliseconds()).Int()
	if err != nil || renewed != 0 {
		t.Fatalf("stale token renewed lease: result=%d error=%v", renewed, err)
	}
	if ttl := client.PTTL(ctx, key).Val(); ttl <= 0 || ttl > 10*time.Second {
		t.Fatalf("stale token changed expiry: %s", ttl)
	}
	renewed, err = renewDeliveryScript.Run(ctx, client, []string{key}, newToken, deliveryLeaseTTL.Milliseconds()).Int()
	if err != nil || renewed != 1 {
		t.Fatalf("current token could not renew: result=%d error=%v", renewed, err)
	}
	if ttl := client.PTTL(ctx, key).Val(); ttl <= time.Minute || ttl > deliveryLeaseTTL {
		t.Fatalf("current token did not extend expiry: %s", ttl)
	}
	if got := client.Get(ctx, key).Val(); got != newToken {
		t.Fatal("renewal changed the lease owner")
	}
	if err := client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	renewed, err = renewDeliveryScript.Run(ctx, client, []string{key}, newToken, deliveryLeaseTTL.Milliseconds()).Int()
	if err != nil || renewed != 0 || client.Exists(ctx, key).Val() != 0 {
		t.Fatalf("renewal revived expired lease: result=%d error=%v", renewed, err)
	}
}

func TestDeliveryAlertExcludesScheduledWaitsAndInterruptedObservations(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	event, err := decodeMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	member := deliveryAlertMember(group, event.ID)
	observed := DeferredError{After: time.Minute, Reason: "frequency limit", Observe: true}
	if err := observeDeferred(ctx, client, group, event, observed); err != nil {
		t.Fatal(err)
	}
	_ = client.ZAdd(ctx, deliveryObservedKey, redis.Z{Score: float64(time.Now().Add(-3 * time.Hour).UnixMilli()), Member: member}).Err()
	alerts, count, err := DeliveryAlerts(ctx, client)
	if err != nil || count != 1 || len(alerts) != 1 || alerts[0].EventID != event.ID {
		t.Fatalf("alerts=%v count=%d err=%v", alerts, count, err)
	}
	if err := observeDeferred(ctx, client, group, event, DeferredError{After: 8 * time.Hour, Reason: "quiet hours"}); err != nil {
		t.Fatal(err)
	}
	_, count, err = DeliveryAlerts(ctx, client)
	if err != nil || count != 0 {
		t.Fatalf("quiet alert count=%d, err=%v", count, err)
	}
	if err := observeDeferred(ctx, client, group, event, observed); err != nil {
		t.Fatal(err)
	}
	_ = client.ZAdd(ctx, deliveryObservedKey, redis.Z{Score: float64(time.Now().Add(-3 * time.Hour).UnixMilli()), Member: member}).Err()
	_ = client.HSet(ctx, deliveryObservedDetailsKey, member, fmt.Sprintf(`{"group":%q,"eventId":%q,"since":%q,"nextAttemptUnixMilli":%d}`, group, event.ID, time.Now().Add(-3*time.Hour).Format(time.RFC3339Nano), time.Now().Add(-time.Hour).UnixMilli())).Err()
	if err := observeDeferred(ctx, client, group, event, observed); err != nil {
		t.Fatal(err)
	}
	_, count, err = DeliveryAlerts(ctx, client)
	if err != nil || count != 0 {
		t.Fatalf("interrupted observation count=%d err=%v", count, err)
	}
}

func TestLongRunningHandlerKeepsReplacementFromDoubleApplying(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	var applied atomic.Int32
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleMessage(ctx, client, group, message, func(context.Context, Event) error {
			applied.Add(1)
			close(started)
			time.Sleep(400 * time.Millisecond)
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	handleMessage(ctx, client, group, message, func(context.Context, Event) error {
		applied.Add(1)
		return nil
	})
	<-done
	if applied.Load() != 1 {
		t.Fatalf("side effects = %d, want 1", applied.Load())
	}
	if pending := client.XPending(ctx, StreamName, group).Val().Count; pending != 0 {
		t.Fatalf("pending after exclusive handler = %d", pending)
	}
}

func TestDecodeMessageRejectsMissingID(t *testing.T) {
	_, err := decodeMessage(redis.XMessage{Values: map[string]any{"class_id": "1", "type": ReviewDecided, "payload": `{}`}})
	if err == nil {
		t.Fatal("missing event_id accepted")
	}
}

func TestCanceledHandlerDoesNotCountAsFailure(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	eventID := message.Values["event_id"].(string)
	handleMessage(ctx, client, group, message, func(context.Context, Event) error { return context.Canceled })
	if client.Exists(ctx, deliveryKey(group, eventID)).Val() != 0 {
		t.Fatal("canceled handler incremented attempts")
	}
	if pending := client.XPending(ctx, StreamName, group).Val().Count; pending != 1 {
		t.Fatalf("canceled handler acked the message: pending=%d", pending)
	}
}

func TestLostLeaseCannotIncrementAttemptsOrDefer(t *testing.T) {
	client := deliveryTestRedis(t)
	ctx := context.Background()
	group := deliveryTestGroup()
	message := deliveryTestMessage(t, client, group)
	eventID := message.Values["event_id"].(string)
	leaseCtx, release, acquired := acquireDelivery(ctx, client, group, eventID, message.ID)
	if !acquired {
		t.Fatal("initial lease was not acquired")
	}
	defer release()
	if err := client.Set(ctx, deliveryLeaseKey(group, eventID), uuid.NewString(), deliveryLeaseTTL).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := incrDeliveryAttempt(leaseCtx, client, group, eventID); !errors.Is(err, errDeliveryLeaseLost) {
		t.Fatalf("incr err=%v", err)
	}
	if err := markDeferredHeld(leaseCtx, client, group, eventID, time.Hour); !errors.Is(err, errDeliveryLeaseLost) {
		t.Fatalf("defer err=%v", err)
	}
	event, err := decodeMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := observeDeferred(leaseCtx, client, group, event, DeferredError{After: time.Minute, Reason: "stale", Observe: true}); !errors.Is(err, errDeliveryLeaseLost) {
		t.Fatalf("observe err=%v", err)
	}
	if client.Exists(ctx, deliveryKey(group, eventID), deferredKey(group, eventID)).Val() != 0 {
		t.Fatal("stale handler mutated replacement delivery state")
	}
}
