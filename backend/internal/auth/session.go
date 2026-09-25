package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrInvalidRefresh = errors.New("refresh token is invalid or has been rotated")

type RefreshSession struct {
	Identity  Identity  `json:"identity"`
	SessionID string    `json:"sessionId"`
	CreatedAt time.Time `json:"createdAt"`
}

type SessionStore struct {
	redis *redis.Client
	ttl   time.Duration
}

func NewSessionStore(client *redis.Client, ttl time.Duration) (*SessionStore, error) {
	if client == nil || ttl <= 0 {
		return nil, errors.New("Redis client and positive refresh TTL are required")
	}
	return &SessionStore{redis: client, ttl: ttl}, nil
}

func (s *SessionStore) Create(ctx context.Context, identity Identity) (token string, session RefreshSession, err error) {
	sessionID, err := randomHex(16)
	if err != nil {
		return "", RefreshSession{}, err
	}
	token, err = newRefreshToken(sessionID)
	if err != nil {
		return "", RefreshSession{}, err
	}
	session = RefreshSession{Identity: identity, SessionID: sessionID, CreatedAt: time.Now().UTC()}
	payload, err := json.Marshal(session)
	if err != nil {
		return "", RefreshSession{}, err
	}
	hash := tokenHash(token)
	pipe := s.redis.TxPipeline()
	pipe.Set(ctx, refreshKey(hash), payload, s.ttl)
	pipe.Set(ctx, familyKey(sessionID), hash, s.ttl)
	pipe.SAdd(ctx, userSessionsKey(identity.UserID), sessionID)
	pipe.Expire(ctx, userSessionsKey(identity.UserID), s.ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", RefreshSession{}, err
	}
	return token, session, nil
}

var rotateScript = redis.NewScript(`
local payload = redis.call('GET', KEYS[1])
if not payload then
  local active = redis.call('GET', KEYS[3])
  if active then
    redis.call('DEL', ARGV[3] .. active)
    redis.call('DEL', KEYS[3])
  end
  return false
end
local active = redis.call('GET', KEYS[3])
if active ~= ARGV[1] then
  if active then redis.call('DEL', ARGV[3] .. active) end
  redis.call('DEL', KEYS[1])
  redis.call('DEL', KEYS[3])
  return false
end
redis.call('DEL', KEYS[1])
redis.call('SET', KEYS[2], payload, 'PX', ARGV[2])
redis.call('SET', KEYS[3], ARGV[4], 'PX', ARGV[2])
return payload
`)

func (s *SessionStore) Rotate(ctx context.Context, oldToken string) (newToken string, session RefreshSession, err error) {
	sessionID, err := refreshSessionID(oldToken)
	if err != nil {
		return "", RefreshSession{}, ErrInvalidRefresh
	}
	newToken, err = newRefreshToken(sessionID)
	if err != nil {
		return "", RefreshSession{}, err
	}
	oldHash := tokenHash(oldToken)
	newHash := tokenHash(newToken)
	result, err := rotateScript.Run(ctx, s.redis,
		[]string{refreshKey(oldHash), refreshKey(newHash), familyKey(sessionID)},
		oldHash, s.ttl.Milliseconds(), "auth:refresh:", newHash,
	).Result()
	if errors.Is(err, redis.Nil) || result == nil || result == false {
		return "", RefreshSession{}, ErrInvalidRefresh
	}
	if err != nil {
		return "", RefreshSession{}, err
	}
	payload, ok := result.(string)
	if !ok || json.Unmarshal([]byte(payload), &session) != nil {
		return "", RefreshSession{}, errors.New("stored refresh session is invalid")
	}
	return newToken, session, nil
}

func (s *SessionStore) Revoke(ctx context.Context, token string) error {
	sessionID, err := refreshSessionID(token)
	if err != nil {
		return nil
	}
	hash := tokenHash(token)
	payload, _ := s.redis.Get(ctx, refreshKey(hash)).Bytes()
	pipe := s.redis.TxPipeline()
	pipe.Del(ctx, refreshKey(hash), familyKey(sessionID))
	var session RefreshSession
	if json.Unmarshal(payload, &session) == nil {
		pipe.SRem(ctx, userSessionsKey(session.Identity.UserID), sessionID)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (s *SessionStore) RevokeUser(ctx context.Context, userID int64, exceptSessionID string) error {
	key := userSessionsKey(userID)
	sessions, err := s.redis.SMembers(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	for _, sessionID := range sessions {
		if sessionID == exceptSessionID {
			continue
		}
		hash, err := s.redis.Get(ctx, familyKey(sessionID)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		pipe := s.redis.TxPipeline()
		if hash != "" {
			pipe.Del(ctx, refreshKey(hash))
		}
		pipe.Del(ctx, familyKey(sessionID))
		pipe.SRem(ctx, key, sessionID)
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func newRefreshToken(sessionID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return sessionID + "." + base64.RawURLEncoding.EncodeToString(raw), nil
}

func refreshSessionID(token string) (string, error) {
	sessionID, secret, ok := strings.Cut(token, ".")
	if !ok || len(sessionID) != 32 || len(secret) < 32 {
		return "", ErrInvalidRefresh
	}
	if _, err := hex.DecodeString(sessionID); err != nil {
		return "", ErrInvalidRefresh
	}
	return sessionID, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomHex(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func refreshKey(hash string) string       { return "auth:refresh:" + hash }
func familyKey(sessionID string) string   { return "auth:family:" + sessionID }
func userSessionsKey(userID int64) string { return fmt.Sprintf("auth:user:%d:sessions", userID) }
