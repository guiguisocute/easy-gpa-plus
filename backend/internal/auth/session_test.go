package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type sessionTestStore struct {
	*SessionStore
	identity Identity
	tokens   []string
}

func newSessionTestStore(t *testing.T) *sessionTestStore {
	t.Helper()
	addr := os.Getenv("EASYGPA_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set EASYGPA_TEST_REDIS_ADDR to run refresh session integration tests")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	store, err := NewSessionStore(client, time.Hour, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &sessionTestStore{SessionStore: store, identity: Identity{UserID: -time.Now().UnixNano(), ClassID: 1, Role: "student"}}
	// Only remove this test's randomly issued credentials. Do not flush the
	// logical database or interfere with other Redis integration tests.
	t.Cleanup(func() {
		keys := []string{userSessionsKey(f.identity.UserID)}
		for _, token := range f.tokens {
			sessionID, _ := refreshSessionID(token)
			keys = append(keys, refreshKey(tokenHash(token)), familyKey(sessionID))
		}
		if err := client.Del(context.Background(), keys...).Err(); err != nil {
			t.Errorf("clean up refresh test keys: %v", err)
		}
	})
	return f
}

func (f *sessionTestStore) create(t *testing.T) (string, RefreshSession) {
	t.Helper()
	token, session, err := f.Create(t.Context(), f.identity)
	if err != nil {
		t.Fatal(err)
	}
	f.tokens = append(f.tokens, token)
	return token, session
}

func (f *sessionTestStore) rotate(t *testing.T, token string) string {
	t.Helper()
	rotated, session, err := f.Rotate(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	f.tokens = append(f.tokens, rotated)
	if session.Identity != f.identity {
		t.Fatalf("rotation changed session identity: %+v", session.Identity)
	}
	return rotated
}

func TestRefreshSessionRejectsForgedSecretWithoutRevokingFamily(t *testing.T) {
	for _, operation := range []string{"refresh", "logout"} {
		t.Run(operation, func(t *testing.T) {
			f := newSessionTestStore(t)
			token, session := f.create(t)
			forged := session.SessionID + "." + strings.Repeat("a", 43)
			if operation == "refresh" {
				if _, _, err := f.Rotate(t.Context(), forged); !errors.Is(err, ErrInvalidRefresh) {
					t.Fatalf("forged refresh error = %v, want invalid refresh", err)
				}
			} else if err := f.Revoke(t.Context(), forged); err != nil {
				t.Fatal(err)
			}
			// Knowledge of the public session ID must not destroy the owner's
			// real credential; verify by using it through the normal rotation.
			f.rotate(t, token)
		})
	}
}

func TestRefreshSessionReplayRevokesRotatedFamily(t *testing.T) {
	f := newSessionTestStore(t)
	token, session := f.create(t)
	current := f.rotate(t, token)
	if _, _, err := f.Rotate(t.Context(), token); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("replayed refresh error = %v, want invalid refresh", err)
	}
	if _, _, err := f.Rotate(t.Context(), current); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("family survived refresh replay: %v", err)
	}
	if n, err := f.redis.Exists(t.Context(), refreshKey(tokenHash(current)), familyKey(session.SessionID)).Result(); err != nil || n != 0 {
		t.Fatalf("replay left current credential or family behind: count=%d, err=%v", n, err)
	}
}

func TestRefreshSessionLogoutRevokesCurrentAndRotatedTokens(t *testing.T) {
	for _, rotated := range []bool{false, true} {
		name := "current"
		if rotated {
			name = "rotated"
		}
		t.Run(name, func(t *testing.T) {
			f := newSessionTestStore(t)
			token, session := f.create(t)
			current := token
			if rotated {
				current = f.rotate(t, token)
			}
			if err := f.Revoke(t.Context(), token); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.Rotate(t.Context(), current); !errors.Is(err, ErrInvalidRefresh) {
				t.Fatalf("logged-out token still usable: %v", err)
			}
			if n, err := f.redis.Exists(t.Context(), refreshKey(tokenHash(current)), familyKey(session.SessionID)).Result(); err != nil || n != 0 {
				t.Fatalf("logout left current credential or family behind: count=%d, err=%v", n, err)
			}
			if member, err := f.redis.SIsMember(t.Context(), userSessionsKey(f.identity.UserID), session.SessionID).Result(); err != nil || member {
				t.Fatalf("logout left user session index behind: member=%t, err=%v", member, err)
			}
			if err := f.Revoke(t.Context(), token); err != nil {
				t.Fatalf("repeat logout failed: %v", err)
			}
		})
	}
}

func TestRefreshSessionRotationPreservesRedisErrors(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewSessionStore(client, time.Hour, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	token, err := newRefreshToken(strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Rotate(t.Context(), token); err == nil || errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("Redis failure was reported as an invalid credential: %v", err)
	}
}

func TestRefreshSessionUpgradesLegacyCredential(t *testing.T) {
	f := newSessionTestStore(t)
	signed, session := f.create(t)
	legacy, err := newRefreshToken(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	f.tokens = append(f.tokens, legacy)
	payload, err := f.redis.Get(t.Context(), refreshKey(tokenHash(signed))).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pipe := f.redis.TxPipeline()
	pipe.Del(t.Context(), refreshKey(tokenHash(signed)))
	pipe.Set(t.Context(), refreshKey(tokenHash(legacy)), payload, f.ttl)
	pipe.Set(t.Context(), familyKey(session.SessionID), tokenHash(legacy), f.ttl)
	if _, err := pipe.Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	current := f.rotate(t, legacy)
	if !f.validRefreshProof(current) {
		t.Fatal("legacy credential did not upgrade to a signed token")
	}
	// An unsigned, already-consumed legacy cookie has no proof of issuance.
	// Reject it without granting it authority over the upgraded family.
	if _, _, err := f.Rotate(t.Context(), legacy); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("consumed legacy token error = %v, want invalid refresh", err)
	}
	f.rotate(t, current)
}

func TestRefreshProofBindsEntireCredential(t *testing.T) {
	store := &SessionStore{secret: []byte(strings.Repeat("s", 32))}
	token, err := store.newRefreshToken(strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	if !store.validRefreshProof(token) {
		t.Fatal("new credential has an invalid proof")
	}
	parts := strings.Split(token, ".")
	cases := []string{
		strings.Repeat("b", 32) + "." + parts[1] + "." + parts[2],
		parts[0] + "." + parts[1] + "a." + parts[2],
		parts[0] + "." + parts[1],
		token + "a",
	}
	for _, forged := range cases {
		if store.validRefreshProof(forged) {
			t.Fatal("forged credential passed proof verification")
		}
	}
	otherDeployment := &SessionStore{secret: []byte(strings.Repeat("t", 32))}
	if otherDeployment.validRefreshProof(token) {
		t.Fatal("credential accepted with a different signing secret")
	}
}

func TestRefreshSessionRenewsRevocationIndex(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "expiring index"
		if expired {
			name = "already expired index"
		}
		t.Run(name, func(t *testing.T) {
			f := newSessionTestStore(t)
			f.ttl = 10 * time.Second
			token, session := f.create(t)
			index := userSessionsKey(f.identity.UserID)
			// Model a user index reaching its original expiry while the actual
			// session has kept rotating and remains active.
			if expired {
				if err := f.redis.Del(t.Context(), index).Err(); err != nil {
					t.Fatal(err)
				}
			} else if err := f.redis.PExpire(t.Context(), index, time.Second).Err(); err != nil {
				t.Fatal(err)
			}
			current := f.rotate(t, token)
			if ttl, err := f.redis.PTTL(t.Context(), index).Result(); err != nil || ttl <= 5*time.Second || ttl > f.ttl {
				t.Fatalf("user index did not follow rotated session TTL: ttl=%s, err=%v", ttl, err)
			}
			if member, err := f.redis.SIsMember(t.Context(), index, session.SessionID).Result(); err != nil || !member {
				t.Fatalf("rotated session missing from revocation index: member=%t, err=%v", member, err)
			}
			if err := f.RevokeUser(t.Context(), f.identity.UserID, ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.Rotate(t.Context(), current); !errors.Is(err, ErrInvalidRefresh) {
				t.Fatalf("long-lived session escaped user revocation: %v", err)
			}
		})
	}
}
