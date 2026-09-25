package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	StreamName    = "easygpa.events"
	DeadStream    = "easygpa.events.dead"
	maxDeliveries = 5
	deferKeyTTL   = 7 * 24 * time.Hour
)

type Event struct {
	ID      string
	ClassID int64
	Type    string
	Payload json.RawMessage
}

type Handler func(context.Context, Event) error

type DeferredError struct {
	After   time.Duration
	Reason  string
	Observe bool
}

func (e DeferredError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return "event delivery deferred"
}

func Defer(after time.Duration, reason string) error {
	if after < 0 {
		after = 0
	}
	return DeferredError{After: after, Reason: reason}
}

// DeferObserved marks an unexpected temporary delivery block for operations.
// Scheduled quiet hours and digest waits must continue to use Defer.
func DeferObserved(after time.Duration, reason string) error {
	if after < 0 {
		after = 0
	}
	return DeferredError{After: after, Reason: reason, Observe: true}
}

// RunRelay implements the transactional-outbox half of event delivery. A
// crash between XADD and the database update can duplicate a message; consumer
// handlers therefore use Event.ID as their idempotency key.
func RunRelay(ctx context.Context, pool *pgxpool.Pool, client *redis.Client) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		published, err := relayOne(ctx, pool, client)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("outbox relay failed", "error", err)
		}
		if published {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func relayOne(ctx context.Context, pool *pgxpool.Pool, client *redis.Client) (bool, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var event Event
	err = tx.QueryRow(ctx, `
		SELECT id::text,COALESCE(class_id,0),type,payload
		  FROM outbox_event
		 WHERE published_at IS NULL AND available_at<=now()
		 ORDER BY created_at,id
		 FOR UPDATE SKIP LOCKED LIMIT 1
	`).Scan(&event.ID, &event.ClassID, &event.Type, &event.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, publishErr := client.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamName, MaxLen: 100000, Approx: true,
		Values: map[string]any{
			"event_id": event.ID, "class_id": strconv.FormatInt(event.ClassID, 10),
			"type": event.Type, "payload": string(event.Payload),
		},
	}).Result()
	if publishErr != nil {
		_, updateErr := tx.Exec(ctx, `
			UPDATE outbox_event
			   SET attempts=attempts+1,last_error=$1,
			       available_at=now()+(LEAST(300,power(2,LEAST(attempts,8)))::text||' seconds')::interval
			 WHERE id=$2::uuid
		`, publishErr.Error(), event.ID)
		if updateErr != nil {
			return false, updateErr
		}
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox_event SET published_at=now(),attempts=attempts+1,last_error=NULL WHERE id=$1::uuid`, event.ID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func RunConsumer(ctx context.Context, client *redis.Client, group string, handler Handler) error {
	if strings.TrimSpace(group) == "" || handler == nil {
		return errors.New("stream consumer needs a group and handler")
	}
	err := client.XGroupCreateMkStream(ctx, StreamName, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	consumer := consumerName(group)
	claimStart := "0-0"
	retryWait := time.Second
	for {
		claimed, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: StreamName, Group: group, Consumer: consumer,
			MinIdle: 30 * time.Second, Start: claimStart, Count: 20,
		}).Result()
		if err != nil && !errors.Is(err, redis.Nil) && !errors.Is(err, context.Canceled) {
			slog.Error("stream autoclaim failed", "group", group, "error", err)
			if waitErr := waitStreamRetry(ctx, &retryWait); waitErr != nil {
				return waitErr
			}
		} else {
			retryWait = time.Second
			for _, message := range claimed {
				handleMessage(ctx, client, group, message, handler)
			}
			if next == "0-0" || next == "" {
				claimStart = "0-0"
			} else {
				claimStart = next
			}
		}

		streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: group, Consumer: consumer, Streams: []string{StreamName, ">"},
			Count: 20, Block: 5 * time.Second,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return ctx.Err()
			}
			slog.Error("stream read failed", "group", group, "error", err)
			if waitErr := waitStreamRetry(ctx, &retryWait); waitErr != nil {
				return waitErr
			}
			continue
		}
		retryWait = time.Second
		for _, stream := range streams {
			for _, message := range stream.Messages {
				handleMessage(ctx, client, group, message, handler)
			}
		}
	}
}

func waitStreamRetry(ctx context.Context, wait *time.Duration) error {
	delay := *wait
	if delay <= 0 {
		delay = time.Second
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	next := delay * 2
	if next > 30*time.Second {
		next = 30 * time.Second
	}
	*wait = next
	return nil
}

func handleMessage(ctx context.Context, client *redis.Client, group string, message redis.XMessage, handler Handler) {
	if target, ok := message.Values["target_group"]; ok && fmt.Sprint(target) != "" && fmt.Sprint(target) != group {
		if err := client.XAck(ctx, StreamName, group, message.ID).Err(); err != nil {
			slog.Error("stream targeted delivery skip failed", "group", group, "stream_id", message.ID, "error", err)
		}
		return
	}
	event, err := decodeMessage(message)
	eventID := event.ID
	if eventID == "" {
		eventID = message.ID
	}
	leaseCtx, release, acquired := acquireDelivery(ctx, client, group, eventID, message.ID)
	if !acquired {
		return
	}
	defer release()
	ctx = leaseCtx
	if err == nil {
		if deferred, checkErr := deferredUntil(ctx, client, group, event.ID); checkErr != nil {
			slog.Error("stream deferred state lookup failed", "group", group, "event_id", event.ID, "error", checkErr)
			return
		} else if deferred.After(time.Now()) {
			return
		}
		err = handler(ctx, event)
	}
	if errors.Is(err, context.Canceled) {
		slog.Info("stream handler interrupted", "group", group, "event_id", eventID)
		return
	}
	if err == nil {
		if ackErr := finishDelivery(ctx, client, group, eventID, message, nil); ackErr != nil {
			slog.Error("stream acknowledgement failed", "group", group, "event_id", eventID, "error", ackErr)
		}
		return
	}
	var deferred DeferredError
	if errors.As(err, &deferred) {
		if observeErr := observeDeferred(ctx, client, group, event, deferred); observeErr != nil {
			if errors.Is(observeErr, errDeliveryLeaseLost) {
				slog.Info("stream deferred observation skipped, lease lost", "group", group, "event_id", eventID)
				return
			}
			slog.Error("stream deferred observation failed", "group", group, "event_id", eventID, "error", observeErr)
		}
		if event.ID != "" {
			if setErr := markDeferredHeld(ctx, client, group, event.ID, deferred.After); setErr != nil {
				if errors.Is(setErr, errDeliveryLeaseLost) {
					slog.Info("stream deferred state skipped, lease lost", "group", group, "event_id", event.ID)
					return
				}
				slog.Error("stream deferred state update failed", "group", group, "event_id", event.ID, "error", setErr)
			}
		}
		return
	}
	attempts, countErr := incrDeliveryAttempt(ctx, client, group, eventID)
	if errors.Is(countErr, errDeliveryLeaseLost) {
		slog.Info("stream attempt skipped, lease lost", "group", group, "event_id", eventID)
		return
	}
	if countErr != nil || attempts < maxDeliveries {
		slog.Error("stream handler failed", "group", group, "event_id", eventID, "attempt", attempts, "error", err)
		return
	}
	deadErr := finishDelivery(ctx, client, group, eventID, message, map[string]any{
		"event_id": eventID, "group": group, "stream_id": message.ID, "error": err.Error(),
		"class_id": strconv.FormatInt(event.ClassID, 10), "type": event.Type, "payload": string(event.Payload),
	})
	if deadErr != nil {
		slog.Error("stream dead letter write failed", "group", group, "event_id", eventID, "error", deadErr)
	} else {
		slog.Error("stream event moved to dead letters", "group", group, "event_id", eventID, "type", event.Type, "attempt", attempts, "error", err)
	}
}

func deferredUntil(ctx context.Context, client *redis.Client, group, eventID string) (time.Time, error) {
	if eventID == "" {
		return time.Time{}, nil
	}
	raw, err := client.Get(ctx, deferredKey(group, eventID)).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	nanos, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid deferred timestamp: %w", err)
	}
	return time.Unix(0, nanos), nil
}

func markDeferred(ctx context.Context, client *redis.Client, group, eventID string, after time.Duration) error {
	if after <= 0 {
		return client.Del(ctx, deferredKey(group, eventID)).Err()
	}
	ttl := after + time.Minute
	if ttl > deferKeyTTL {
		ttl = deferKeyTTL
	}
	return client.Set(ctx, deferredKey(group, eventID), strconv.FormatInt(time.Now().Add(after).UnixNano(), 10), ttl).Err()
}

func decodeMessage(message redis.XMessage) (Event, error) {
	value := func(key string) string {
		if v, ok := message.Values[key]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	classID, err := strconv.ParseInt(value("class_id"), 10, 64)
	if err != nil || classID < 0 {
		return Event{}, errors.New("stream message has invalid class_id")
	}
	event := Event{ID: value("event_id"), ClassID: classID, Type: value("type"), Payload: json.RawMessage(value("payload"))}
	if event.ClassID == 0 && event.Type != PlatformKnowledgeDocumentCreated {
		return event, errors.New("only platform knowledge events may omit class_id")
	}
	if event.ID == "" || event.Type == "" || !json.Valid(event.Payload) {
		return event, errors.New("stream message is incomplete")
	}
	return event, nil
}

func deliveryKey(group, eventID string) string {
	return "easygpa:stream-attempt:" + group + ":" + eventID
}

func deferredKey(group, eventID string) string {
	return "easygpa:stream-deferred:" + group + ":" + eventID
}

func consumerName(group string) string {
	host, _ := os.Hostname()
	if host == "" {
		host = "worker"
	}
	return host + "-" + strconv.Itoa(os.Getpid()) + "-" + group
}
