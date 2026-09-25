package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	deliveryLeaseTTL           = 2 * time.Minute
	deliveryAlertAfter         = 2 * time.Hour
	deliveryObservedKey        = "easygpa:stream-observed"
	deliveryObservedDetailsKey = "easygpa:stream-observed-details"
)

type deliveryLeaseContextKey struct{}

func deliveryLeaseKey(group, eventID string) string {
	return "easygpa:stream-processing:" + group + ":" + eventID
}

func deliveryAlertMember(group, eventID string) string { return group + ":" + eventID }

var acquireDeliveryScript = redis.NewScript(`
if #redis.call('XPENDING', KEYS[1], ARGV[1], ARGV[2], ARGV[2], 1) == 0 then return 0 end
if redis.call('SET', KEYS[2], ARGV[3], 'NX', 'PX', ARGV[4]) then return 1 end
return 0
`)

var renewDeliveryScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('PEXPIRE', KEYS[1], ARGV[2])
`)

var releaseDeliveryScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('DEL', KEYS[1])
`)

// A renewable lease prevents an autoclaimed message from running alongside its
// original handler. Replays also check this lease before resetting event state.
func acquireDelivery(ctx context.Context, client *redis.Client, group, eventID, messageID string) (context.Context, func(), bool) {
	token := uuid.NewString()
	key := deliveryLeaseKey(group, eventID)
	acquired, err := acquireDeliveryScript.Run(ctx, client, []string{StreamName, key}, group, messageID, token, deliveryLeaseTTL.Milliseconds()).Int()
	if err != nil {
		slog.Error("stream processing lease failed", "group", group, "event_id", eventID, "error", err)
	}
	if err != nil || acquired != 1 {
		return ctx, nil, false
	}
	leaseCtx, cancel := context.WithCancel(context.WithValue(ctx, deliveryLeaseContextKey{}, token))
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(deliveryLeaseTTL / 4)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewed, err := renewDeliveryScript.Run(leaseCtx, client, []string{key}, token, deliveryLeaseTTL.Milliseconds()).Int()
				if err != nil || renewed != 1 {
					slog.Error("stream processing lease lost", "group", group, "event_id", eventID, "error", err)
					cancel()
					return
				}
			}
		}
	}()
	return leaseCtx, func() {
		cancel()
		<-done
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = releaseDeliveryScript.Run(cleanupCtx, client, []string{key}, token).Err()
	}, true
}

var errDeliveryLeaseLost = errors.New("delivery lease lost")

func deliveryLeaseToken(ctx context.Context) string {
	token, _ := ctx.Value(deliveryLeaseContextKey{}).(string)
	return token
}

func leaseLost(err error) bool {
	return err != nil && strings.Contains(err.Error(), "delivery lease lost")
}

var incrDeliveryAttemptScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return redis.error_reply('delivery lease lost') end
local n = redis.call('INCR', KEYS[2])
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))
return n
`)

func incrDeliveryAttempt(ctx context.Context, client *redis.Client, group, eventID string) (int64, error) {
	attempts, err := incrDeliveryAttemptScript.Run(ctx, client, []string{deliveryLeaseKey(group, eventID), deliveryKey(group, eventID)}, deliveryLeaseToken(ctx), int64((7 * 24 * time.Hour).Seconds())).Int64()
	if leaseLost(err) {
		return 0, errDeliveryLeaseLost
	}
	return attempts, err
}

var markDeferredScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return redis.error_reply('delivery lease lost') end
if tonumber(ARGV[2]) <= 0 then
  return redis.call('DEL', KEYS[2])
end
return redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
`)

func markDeferredHeld(ctx context.Context, client *redis.Client, group, eventID string, after time.Duration) error {
	ttl := after + time.Minute
	if ttl > deferKeyTTL {
		ttl = deferKeyTTL
	}
	ms := ttl.Milliseconds()
	if after <= 0 {
		ms = 0
	}
	err := markDeferredScript.Run(ctx, client, []string{deliveryLeaseKey(group, eventID), deferredKey(group, eventID)}, deliveryLeaseToken(ctx), ms, strconv.FormatInt(time.Now().Add(after).UnixNano(), 10), ms).Err()
	if leaseLost(err) {
		return errDeliveryLeaseLost
	}
	return err
}

var finishDeliveryScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then return redis.error_reply('delivery lease lost') end
if #redis.call('XPENDING', KEYS[1], ARGV[2], ARGV[3], ARGV[3], 1) == 0 then return 0 end
if ARGV[4] == 'dead' then
  redis.call('XADD', KEYS[3], '*', unpack(ARGV, 6))
else
  redis.call('DEL', KEYS[4])
end
redis.call('XACK', KEYS[1], ARGV[2], ARGV[3])
redis.call('DEL', KEYS[5], KEYS[8])
redis.call('ZREM', KEYS[6], ARGV[5])
redis.call('HDEL', KEYS[7], ARGV[5])
return 1
`)

func finishDelivery(ctx context.Context, client *redis.Client, group, eventID string, message redis.XMessage, dead map[string]any) error {
	token, _ := ctx.Value(deliveryLeaseContextKey{}).(string)
	mode := "success"
	if dead != nil {
		mode = "dead"
	}
	args := []any{token, group, message.ID, mode, deliveryAlertMember(group, eventID)}
	for key, value := range dead {
		args = append(args, key, value)
	}
	return finishDeliveryScript.Run(ctx, client, []string{
		StreamName, deliveryLeaseKey(group, eventID), DeadStream, deliveryKey(group, eventID), deferredKey(group, eventID),
		deliveryObservedKey, deliveryObservedDetailsKey, deliveryAlertLogKey(group, eventID),
	}, args...).Err()
}

type DeliveryAlert struct {
	Group                string    `json:"group"`
	EventID              string    `json:"eventId"`
	EventType            string    `json:"eventType"`
	Reason               string    `json:"reason"`
	Since                time.Time `json:"since"`
	NextAttemptUnixMilli int64     `json:"nextAttemptUnixMilli,omitempty"`
}

func deliveryAlertLogKey(group, eventID string) string {
	return "easygpa:stream-alert-logged:" + group + ":" + eventID
}

var observeDeferredScript = redis.NewScript(`
if ARGV[4] ~= '' and redis.call('GET', KEYS[4]) ~= ARGV[4] then return redis.error_reply('delivery lease lost') end
if ARGV[2] == 'reset' then
  redis.call('ZREM', KEYS[1], ARGV[1])
  redis.call('HDEL', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
  return 0
end
local details = cjson.decode(ARGV[3])
local old = redis.call('HGET', KEYS[2], ARGV[1])
if old then
  local previous = cjson.decode(old)
  if previous.nextAttemptUnixMilli and tonumber(ARGV[2]) > previous.nextAttemptUnixMilli + 900000 then
    redis.call('ZREM', KEYS[1], ARGV[1])
    redis.call('DEL', KEYS[3])
  else
    details.since = previous.since
  end
end
redis.call('ZADD', KEYS[1], 'NX', ARGV[2], ARGV[1])
redis.call('HSET', KEYS[2], ARGV[1], cjson.encode(details))
return tonumber(redis.call('ZSCORE', KEYS[1], ARGV[1]))
`)

func observeDeferred(ctx context.Context, client *redis.Client, group string, event Event, deferred DeferredError) error {
	now := time.Now()
	alert := DeliveryAlert{Group: group, EventID: event.ID, EventType: event.Type, Reason: deferred.Reason, Since: now, NextAttemptUnixMilli: now.Add(deferred.After).UnixMilli()}
	raw, err := json.Marshal(alert)
	if err != nil {
		return err
	}
	var sinceArg any = now.UnixMilli()
	if !deferred.Observe {
		sinceArg = "reset"
	}
	since, err := observeDeferredScript.Run(ctx, client, []string{deliveryObservedKey, deliveryObservedDetailsKey, deliveryAlertLogKey(group, event.ID), deliveryLeaseKey(group, event.ID)}, deliveryAlertMember(group, event.ID), sinceArg, string(raw), deliveryLeaseToken(ctx)).Int64()
	if leaseLost(err) {
		return errDeliveryLeaseLost
	}
	if err != nil {
		return err
	}
	if since > 0 && now.Sub(time.UnixMilli(since)) >= deliveryAlertAfter {
		first, err := client.SetNX(ctx, deliveryAlertLogKey(group, event.ID), "1", deferKeyTTL).Result()
		if err != nil {
			return err
		}
		if first {
			slog.Error("stream delivery remains blocked", "group", group, "event_id", event.ID, "type", event.Type, "since", time.UnixMilli(since), "reason", deferred.Reason)
		}
	}
	return nil
}

// DeliveryAlerts returns unexpected deferrals only. Scheduled waits reset their
// observation window, so overnight quiet periods cannot trigger this alert.
func DeliveryAlerts(ctx context.Context, client *redis.Client) ([]DeliveryAlert, int64, error) {
	cutoff := strconv.FormatInt(time.Now().Add(-deliveryAlertAfter).UnixMilli(), 10)
	count, err := client.ZCount(ctx, deliveryObservedKey, "-inf", cutoff).Result()
	if err != nil {
		return nil, 0, err
	}
	members, err := client.ZRangeByScore(ctx, deliveryObservedKey, &redis.ZRangeBy{Min: "-inf", Max: cutoff, Offset: 0, Count: 100}).Result()
	if err != nil {
		return nil, 0, err
	}
	alerts := make([]DeliveryAlert, 0, len(members))
	if len(members) == 0 {
		return alerts, count, nil
	}
	rows, err := client.HMGet(ctx, deliveryObservedDetailsKey, members...).Result()
	if err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		var alert DeliveryAlert
		if err := json.Unmarshal([]byte(fmt.Sprint(row)), &alert); err != nil {
			return nil, 0, err
		}
		alerts = append(alerts, alert)
	}
	return alerts, count, nil
}

var ErrDeadLetterChanged = errors.New("dead letter changed or is being processed")

type Redelivery struct {
	DeadID  string `json:"deadId"`
	EventID string `json:"eventId"`
	Group   string `json:"group"`
}

var redeliverDeadLettersScript = redis.NewScript(`
local rows = {}
for i = 1, #ARGV, 3 do
  local found = redis.call('XRANGE', KEYS[1], ARGV[i], ARGV[i], 'COUNT', 1)
  if #found ~= 1 then return 0 end
  local fields = {}
  for j = 1, #found[1][2], 2 do fields[found[1][2][j]] = found[1][2][j+1] end
  if fields.event_id ~= ARGV[i+1] or fields.group ~= ARGV[i+2] then return 0 end
  local k = 5 + ((i-1) / 3) * 4
  if redis.call('EXISTS', KEYS[k+2]) == 1 then return 0 end
  rows[#rows+1] = fields
end
for n, fields in ipairs(rows) do
  local i = (n-1) * 3 + 1
  local k = 5 + (n-1) * 4
  redis.call('XADD', KEYS[2], '*', 'event_id', fields.event_id, 'class_id', fields.class_id,
    'type', fields.type, 'payload', fields.payload, 'target_group', fields.group)
  redis.call('DEL', KEYS[k], KEYS[k+1], KEYS[k+3])
  local member = fields.group .. ':' .. fields.event_id
  redis.call('ZREM', KEYS[3], member)
  redis.call('HDEL', KEYS[4], member)
  redis.call('XDEL', KEYS[1], ARGV[i])
end
return #rows
`)

// RedeliverDeadLetters atomically verifies the selected rows, resets only their
// original consumer group, and removes each dead letter as its replay is queued.
// The event ID remains unchanged so handler idempotency survives operator replay.
func RedeliverDeadLetters(ctx context.Context, client *redis.Client, messages []redis.XMessage) ([]Redelivery, error) {
	keys := []string{DeadStream, StreamName, deliveryObservedKey, deliveryObservedDetailsKey}
	args := make([]any, 0, len(messages)*3)
	items := make([]Redelivery, 0, len(messages))
	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		if seen[message.ID] {
			continue
		}
		seen[message.ID] = true
		event, err := decodeMessage(message)
		group, _ := message.Values["group"].(string)
		if err != nil || group == "" {
			return nil, fmt.Errorf("dead letter %s has invalid routing or payload", message.ID)
		}
		args = append(args, message.ID, event.ID, group)
		keys = append(keys, deliveryKey(group, event.ID), deferredKey(group, event.ID), deliveryLeaseKey(group, event.ID), deliveryAlertLogKey(group, event.ID))
		items = append(items, Redelivery{DeadID: message.ID, EventID: event.ID, Group: group})
	}
	if len(items) == 0 {
		return items, nil
	}
	count, err := redeliverDeadLettersScript.Run(ctx, client, keys, args...).Int()
	if err != nil {
		return nil, err
	}
	if count != len(items) {
		return nil, ErrDeadLetterChanged
	}
	return items, nil
}
