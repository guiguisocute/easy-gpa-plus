package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	m, err := NewTokenManager("test-secret-which-is-at-least-32-bytes-long", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := m.Issue(Identity{UserID: 7, ClassID: 9, Role: "group", TokenVersion: 3}, "session")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := m.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ClassID != 9 || claims.Role != "group" || claims.TokenVersion != 3 || claims.SessionID != "session" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestTokenManagerToleratesBoundedClockSkew(t *testing.T) {
	manager, err := NewTokenManager(strings.Repeat("s", 32), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return issuedAt }
	raw, _, err := manager.Issue(Identity{UserID: 7, ClassID: 9, Role: "student", TokenVersion: 1}, "session")
	if err != nil {
		t.Fatal(err)
	}

	manager.now = func() time.Time { return issuedAt.Add(-20 * time.Second) }
	if _, err := manager.Parse(raw); err != nil {
		t.Fatalf("Parse() rejected bounded clock skew: %v", err)
	}

	manager.now = func() time.Time { return issuedAt.Add(-40 * time.Second) }
	if _, err := manager.Parse(raw); err == nil {
		t.Fatal("Parse() accepted clock skew beyond the configured leeway")
	}
}
