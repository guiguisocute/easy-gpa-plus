package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/opsconfig"
)

const (
	recipientHourLimit    = 10
	notificationHourLimit = 8
	notificationInterval  = 8 * time.Minute
	frequentInterval      = time.Minute
	frequencyCooldown     = 65 * time.Minute
)

// Check every budget before reserving any of them. In particular, a recipient
// waiting for its next slot must not consume the global minute/day budget.
var mailRateScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local hour = 3600000
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now-hour)
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', now-hour)
local minute = tonumber(redis.call('GET', KEYS[1]) or '0')
local day = tonumber(redis.call('GET', KEYS[2]) or '0')
local wait = 0
local function consider(delay)
  if delay > wait then wait = delay end
end
if minute >= tonumber(ARGV[2]) then consider(tonumber(ARGV[4])) end
if day >= tonumber(ARGV[3]) then consider(tonumber(ARGV[5])) end
if ARGV[11] == '1' then consider(tonumber(redis.call('GET', KEYS[6]) or '0')-now) end
if ARGV[11] == '1' and redis.call('ZCARD', KEYS[3]) >= tonumber(ARGV[8]) then
  local first = redis.call('ZRANGE', KEYS[3], 0, 0, 'WITHSCORES')
  consider(tonumber(first[2])+hour-now)
end
if ARGV[11] == '1' and ARGV[6] == '1' then
  consider(tonumber(redis.call('GET', KEYS[5]) or '0')-now)
  if redis.call('ZCARD', KEYS[4]) >= tonumber(ARGV[9]) then
    local first = redis.call('ZRANGE', KEYS[4], 0, 0, 'WITHSCORES')
    consider(tonumber(first[2])+hour-now)
  end
end
if wait > 0 then return wait end
minute = redis.call('INCR', KEYS[1])
if minute == 1 then redis.call('EXPIRE', KEYS[1], 120) end
day = redis.call('INCR', KEYS[2])
if day == 1 then redis.call('EXPIRE', KEYS[2], 172800) end
if ARGV[11] == '1' then
  redis.call('ZADD', KEYS[3], now, ARGV[7])
  redis.call('EXPIRE', KEYS[3], 7200)
end
if ARGV[11] == '1' and ARGV[6] == '1' then
  redis.call('ZADD', KEYS[4], now, ARGV[7])
  redis.call('EXPIRE', KEYS[4], 7200)
  redis.call('SET', KEYS[5], now+tonumber(ARGV[10]), 'PX', ARGV[10])
end
return 0
`)

type DeliveryPolicy interface {
	BeforeSend(context.Context) error
}

type RuntimePolicy struct {
	config    mailSettingsSource
	redis     *redis.Client
	now       func() time.Time
	keyPrefix string
}

func NewRuntimePolicy(config *opsconfig.Store, client *redis.Client) (*RuntimePolicy, error) {
	if config == nil || client == nil {
		return nil, errors.New("mail runtime policy dependencies are required")
	}
	return &RuntimePolicy{config: config, redis: client, now: time.Now}, nil
}

func (p *RuntimePolicy) BeforeSend(ctx context.Context) error {
	// Retained for callers using the old worker constructor. Production applies
	// the policy once in PolicyMailer, where the actual recipient is available.
	return p.beforeMessage(ctx, Message{EventID: "legacy"}, false)
}

// MailRateLimitError is returned to synchronous callers without pretending the
// message was queued. Notification callers convert it to an observed deferral.
type MailRateLimitError struct {
	After  time.Duration
	Reason string
}

func (e *MailRateLimitError) Error() string { return e.Reason }

func (p *RuntimePolicy) BeforeMessage(ctx context.Context, msg Message) error {
	return p.beforeMessage(ctx, msg, true)
}

func (p *RuntimePolicy) beforeMessage(ctx context.Context, msg Message, recipientAware bool) error {
	config, err := p.config.Mail(ctx)
	if err != nil {
		return err
	}
	// Lua scores use milliseconds. Keep boundary delays on that same clock so
	// the final fraction of a millisecond cannot turn a full quota into wait=0.
	now := p.now().UTC().Truncate(time.Millisecond)
	if msg.EventID != "" {
		delay, err := quietDelay(now, config.QuietStart, config.QuietEnd)
		if err != nil {
			return err
		}
		if delay > 0 {
			return events.Defer(delay, "邮件处于静默时段")
		}
	}
	local := now.In(shanghaiLocation)
	minuteKey := p.prefix() + "minute:" + local.Format("200601021504")
	dayKey := p.prefix() + "day:" + local.Format("20060102")
	addressKey := "legacy"
	if recipientAware {
		addressKey, err = recipientHash(msg.To)
		if err != nil {
			return err
		}
	}
	base := p.prefix() + "recipient:" + addressKey + ":"
	nextMinute := local.Truncate(time.Minute).Add(time.Minute)
	nextDay := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, shanghaiLocation)
	isNotification := 0
	if msg.EventID != "" {
		isNotification = 1
	}
	withRecipient := 0
	if recipientAware {
		withRecipient = 1
	}
	interval := notificationInterval
	if msg.Frequent && msg.Template == TemplateNotificationAlert {
		interval = frequentInterval
	}
	result, err := mailRateScript.Run(ctx, p.redis,
		[]string{minuteKey, dayKey, base + "all", base + "notification", base + "next", base + "cooldown"},
		now.UnixMilli(), config.PerMinute, config.PerDay, nextMinute.Sub(local).Milliseconds(),
		nextDay.Sub(local).Milliseconds(), isNotification, uuid.NewString(), recipientHourLimit,
		notificationHourLimit, interval.Milliseconds(), withRecipient).Int64()
	if err != nil {
		return err
	}
	if result > 0 {
		limit := &MailRateLimitError{After: time.Duration(result) * time.Millisecond, Reason: "邮件发送频率达到限制，等待可用额度"}
		if !recipientAware {
			return events.Defer(limit.After, limit.Reason)
		}
		return limit
	}
	return nil
}

func (p *RuntimePolicy) prefix() string {
	if p.keyPrefix != "" {
		return p.keyPrefix
	}
	return "easygpa:mail-rate:"
}

func recipientHash(value string) (string, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("收件人邮箱地址不合法")
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(address.Address))))
	return hex.EncodeToString(sum[:]), nil
}

var mailCooldownScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
if current < tonumber(ARGV[1]) then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
end
return 1
`)

func (p *RuntimePolicy) coolRecipient(ctx context.Context, recipient string) error {
	hash, err := recipientHash(recipient)
	if err != nil {
		return err
	}
	return mailCooldownScript.Run(ctx, p.redis, []string{p.prefix() + "recipient:" + hash + ":cooldown"},
		p.now().Add(frequencyCooldown).UnixMilli(), frequencyCooldown.Milliseconds()).Err()
}

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func quietDelay(now time.Time, start, end string) (time.Duration, error) {
	startMinute, err := clockMinute(start)
	if err != nil {
		return 0, fmt.Errorf("quietStart: %w", err)
	}
	endMinute, err := clockMinute(end)
	if err != nil {
		return 0, fmt.Errorf("quietEnd: %w", err)
	}
	if startMinute == endMinute {
		return 0, nil
	}
	local := now.In(shanghaiLocation)
	currentMinute := local.Hour()*60 + local.Minute()
	inside := false
	endDayOffset := 0
	if startMinute < endMinute {
		inside = currentMinute >= startMinute && currentMinute < endMinute
	} else {
		inside = currentMinute >= startMinute || currentMinute < endMinute
		if currentMinute >= startMinute {
			endDayOffset = 1
		}
	}
	if !inside {
		return 0, nil
	}
	target := time.Date(local.Year(), local.Month(), local.Day()+endDayOffset, endMinute/60, endMinute%60, 0, 0, shanghaiLocation)
	return target.Sub(local), nil
}

func clockMinute(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, err
	}
	minutes := parsed.Hour()*60 + parsed.Minute()
	if minutes < 0 || minutes >= 24*60 {
		return 0, errors.New("time is outside a day: " + strconv.Itoa(minutes))
	}
	return minutes, nil
}
