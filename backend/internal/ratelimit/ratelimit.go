// Package ratelimit provides a distributed token bucket backed by Redis.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Rule struct {
	Requests int
	Period   time.Duration
	Burst    int
}

func (r Rule) validate() error {
	if r.Requests <= 0 || r.Period <= 0 || r.Burst <= 0 {
		return errors.New("rate limit requests, period and burst must be positive")
	}
	return nil
}

type Decision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

type Limiter struct {
	redis *redis.Client
}

func New(client *redis.Client) (*Limiter, error) {
	if client == nil {
		return nil, errors.New("Redis client is required")
	}
	return &Limiter{redis: client}, nil
}

// Key hashes identifiers so Redis keys do not disclose account names or IPs.
func Key(scope, identifier string) string {
	scope = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, strings.TrimSpace(scope))
	if scope == "" {
		scope = "default"
	}
	sum := sha256.Sum256([]byte(identifier))
	return "ratelimit:" + scope + ":" + hex.EncodeToString(sum[:])
}

var tokenBucketScript = redis.NewScript(`
local clock = redis.call('TIME')
local now_ms = (tonumber(clock[1]) * 1000) + math.floor(tonumber(clock[2]) / 1000)
local values = redis.call('HMGET', KEYS[1], 'tokens', 'updated')
local capacity = tonumber(ARGV[1])
local requests = tonumber(ARGV[2])
local period_ms = tonumber(ARGV[3])
local tokens = tonumber(values[1]) or capacity
local updated = tonumber(values[2]) or now_ms
local elapsed = math.max(0, now_ms - updated)
tokens = math.min(capacity, tokens + (elapsed * requests / period_ms))

local allowed = 0
local retry_ms = 0
if tokens >= 1 then
  allowed = 1
  tokens = tokens - 1
else
  retry_ms = math.ceil((1 - tokens) * period_ms / requests)
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'updated', now_ms)
local ttl_ms = math.max(period_ms, math.ceil(capacity * period_ms / requests) * 2)
redis.call('PEXPIRE', KEYS[1], ttl_ms)
return {allowed, math.floor(tokens), retry_ms}
`)

func (l *Limiter) Allow(ctx context.Context, key string, rule Rule) (Decision, error) {
	if l == nil || l.redis == nil {
		return Decision{}, errors.New("rate limiter is unavailable")
	}
	if err := rule.validate(); err != nil {
		return Decision{}, err
	}
	result, err := tokenBucketScript.Run(ctx, l.redis, []string{key}, rule.Burst, rule.Requests, rule.Period.Milliseconds()).Slice()
	if err != nil {
		return Decision{}, err
	}
	if len(result) != 3 {
		return Decision{}, fmt.Errorf("unexpected rate limit result length %d", len(result))
	}
	allowed, ok := resultInt64(result[0])
	if !ok {
		return Decision{}, errors.New("invalid rate limit allowed result")
	}
	remaining, ok := resultInt64(result[1])
	if !ok {
		return Decision{}, errors.New("invalid rate limit remaining result")
	}
	retryMS, ok := resultInt64(result[2])
	if !ok {
		return Decision{}, errors.New("invalid rate limit retry result")
	}
	return Decision{
		Allowed: allowed == 1, Remaining: max(0, int(remaining)),
		RetryAfter: time.Duration(max(int64(0), retryMS)) * time.Millisecond,
	}, nil
}

func RetryAfterSeconds(value time.Duration) int {
	return max(1, int(math.Ceil(value.Seconds())))
}

func resultInt64(value any) (int64, bool) {
	switch item := value.(type) {
	case int64:
		return item, true
	case int:
		return int64(item), true
	default:
		return 0, false
	}
}
