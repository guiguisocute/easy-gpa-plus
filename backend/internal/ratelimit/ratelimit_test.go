package ratelimit

import (
	"strings"
	"testing"
	"time"
)

func TestKeyDoesNotDiscloseIdentifier(t *testing.T) {
	identifier := "student@example.edu"
	key := Key("auth:login", identifier)
	if strings.Contains(key, identifier) || strings.Contains(key, ":login:") {
		t.Fatalf("rate limit key discloses input: %q", key)
	}
	if key != Key("auth:login", identifier) || key == Key("auth:login", "other@example.edu") {
		t.Fatal("rate limit key is not stable and identifier-specific")
	}
}

func TestRuleValidation(t *testing.T) {
	if (Rule{Requests: 1, Period: time.Minute, Burst: 1}).validate() != nil {
		t.Fatal("valid rule rejected")
	}
	for _, rule := range []Rule{{Period: time.Minute, Burst: 1}, {Requests: 1, Burst: 1}, {Requests: 1, Period: time.Minute}} {
		if rule.validate() == nil {
			t.Fatalf("invalid rule accepted: %+v", rule)
		}
	}
}

func TestRetryAfterSecondsRoundsUp(t *testing.T) {
	if got := RetryAfterSeconds(1100 * time.Millisecond); got != 2 {
		t.Fatalf("RetryAfterSeconds() = %d, want 2", got)
	}
	if got := RetryAfterSeconds(0); got != 1 {
		t.Fatalf("RetryAfterSeconds(0) = %d, want 1", got)
	}
}
