package auth

import (
	"context"
	"crypto/hmac"
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
	redis  *redis.Client
	ttl    time.Duration
	secret []byte
}

func NewSessionStore(client *redis.Client, ttl time.Duration, secret string) (*SessionStore, error) {
	if client == nil || ttl <= 0 {
		return nil, errors.New("Redis client and positive refresh TTL are required")
	}
	if len(secret) < 32 {
		return nil, errors.New("refresh signing secret must be at least 32 bytes")
	}
	return &SessionStore{redis: client, ttl: ttl, secret: []byte(secret)}, nil
}

func (s *SessionStore) Create(ctx context.Context, identity Identity) (token string, session RefreshSession, err error) {
	sessionID, err := randomHex(16)
	if err != nil {
		return "", RefreshSession{}, err
	}
	token, err = s.newRefreshToken(sessionID)
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
  if ARGV[5] == '1' then
    local active = redis.call('GET', KEYS[3])
    if active then redis.call('DEL', ARGV[3] .. active) end
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
redis.call('SADD', KEYS[4], ARGV[6])
redis.call('PEXPIRE', KEYS[4], ARGV[2])
return payload
`)

// A family ID is public in the access token. A missing full-token hash only
// proves replay when the token's signature proves that we issued it. Legacy
// unsigned credentials remain usable while their current hash is stored and
// are upgraded on rotation, without keeping unbounded per-rotation history.
func (s *SessionStore) Rotate(ctx context.Context, oldToken string) (newToken string, session RefreshSession, err error) {
	sessionID, err := refreshSessionID(oldToken)
	if err != nil {
		return "", RefreshSession{}, ErrInvalidRefresh
	}
	newToken, err = s.newRefreshToken(sessionID)
	if err != nil {
		return "", RefreshSession{}, err
	}
	oldHash := tokenHash(oldToken)
	newHash := tokenHash(newToken)
	// Read the stored identity in Go so bigint user IDs keep their precision.
	// The script rechecks the same immutable token hash before changing either
	// the family or its revocation index; a concurrent rotation cannot revive it.
	stored, err := s.redis.Get(ctx, refreshKey(oldHash)).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", RefreshSession{}, err
	}
	var indexedSession RefreshSession
	if err == nil && json.Unmarshal(stored, &indexedSession) != nil {
		return "", RefreshSession{}, errors.New("stored refresh session is invalid")
	}
	result, err := rotateScript.Run(ctx, s.redis,
		[]string{refreshKey(oldHash), refreshKey(newHash), familyKey(sessionID), userSessionsKey(indexedSession.Identity.UserID)},
		oldHash, s.ttl.Milliseconds(), "auth:refresh:", newHash, s.validRefreshProof(oldToken), sessionID,
	).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", RefreshSession{}, ErrInvalidRefresh
		}
		return "", RefreshSession{}, err
	}
	if result == nil || result == false {
		return "", RefreshSession{}, ErrInvalidRefresh
	}
	payload, ok := result.(string)
	if !ok || json.Unmarshal([]byte(payload), &session) != nil {
		return "", RefreshSession{}, errors.New("stored refresh session is invalid")
	}
	return newToken, session, nil
}

var revokeScript = redis.NewScript(`
local payload = redis.call('GET', KEYS[1])
if not payload and ARGV[2] ~= '1' then return false end
local active = redis.call('GET', KEYS[2])
if active then
  local current = redis.call('GET', ARGV[1] .. active)
  if current then payload = current end
  redis.call('DEL', ARGV[1] .. active)
end
redis.call('DEL', KEYS[1], KEYS[2])
return payload
`)

func (s *SessionStore) Revoke(ctx context.Context, token string) error {
	sessionID, err := refreshSessionID(token)
	if err != nil {
		return nil
	}
	hash := tokenHash(token)
	// Check possession and delete the current family token atomically, including
	// when logout races with a refresh that has just rotated the supplied token.
	payload, err := revokeScript.Run(ctx, s.redis,
		[]string{refreshKey(hash), familyKey(sessionID)}, "auth:refresh:", s.validRefreshProof(token),
	).Text()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	var session RefreshSession
	if json.Unmarshal([]byte(payload), &session) == nil {
		return s.redis.SRem(ctx, userSessionsKey(session.Identity.UserID), sessionID).Err()
	}
	return nil
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

func (s *SessionStore) newRefreshToken(sessionID string) (string, error) {
	token, err := newRefreshToken(sessionID)
	if err != nil {
		return "", err
	}
	return token + "." + base64.RawURLEncoding.EncodeToString(s.refreshProof(token)), nil
}

func (s *SessionStore) refreshProof(token string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	// Domain separation keeps this proof distinct from access-token signatures
	// even though both use the deployment's existing JWT secret.
	_, _ = mac.Write([]byte("easygpa-refresh-v1\x00"))
	_, _ = mac.Write([]byte(token))
	return mac.Sum(nil)
}

func (s *SessionStore) validRefreshProof(token string) bool {
	lastDot := strings.LastIndexByte(token, '.')
	if lastDot < 0 {
		return false
	}
	proof, err := base64.RawURLEncoding.DecodeString(token[lastDot+1:])
	return err == nil && hmac.Equal(proof, s.refreshProof(token[:lastDot]))
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
