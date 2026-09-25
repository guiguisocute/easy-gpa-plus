package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Identity struct {
	UserID       int64
	ClassID      int64
	SID          string
	Name         string
	Role         string
	IsDeputy     bool
	TokenVersion int64
}

type Claims struct {
	ClassID      int64  `json:"class_id"`
	Role         string `json:"role"`
	TokenVersion int64  `json:"token_version"`
	SessionID    string `json:"session_id"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

const accessTokenClockSkew = 30 * time.Second

func NewTokenManager(secret string, ttl time.Duration) (*TokenManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must be at least 32 bytes")
	}
	if ttl <= 0 {
		return nil, errors.New("access token TTL must be positive")
	}
	return &TokenManager{secret: []byte(secret), ttl: ttl, now: time.Now}, nil
}

func (m *TokenManager) Issue(identity Identity, sessionID string) (string, time.Time, error) {
	now := m.now().UTC()
	expires := now.Add(m.ttl)
	subject := strconv.FormatInt(identity.UserID, 10)
	if identity.Role == "ops" {
		subject = "ops"
	}
	claims := Claims{
		ClassID: identity.ClassID, Role: identity.Role, TokenVersion: identity.TokenVersion, SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "easygpa", Subject: subject, Audience: jwt.ClaimStrings{"easygpa-web"},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)), ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	return signed, expires, err
}

func (m *TokenManager) Parse(raw string) (Claims, error) {
	claims := Claims{}
	token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected JWT signing method %s", token.Method.Alg())
		}
		return m.secret, nil
	},
		jwt.WithIssuer("easygpa"),
		jwt.WithAudience("easygpa-web"),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		// Docker Desktop and multi-node deployments can briefly disagree on the
		// current time after a host sleep or clock synchronization. A small,
		// bounded leeway prevents a freshly issued token from becoming "not yet
		// valid" without materially extending its configured lifetime.
		jwt.WithLeeway(accessTokenClockSkew),
		jwt.WithTimeFunc(m.now),
	)
	if err != nil || !token.Valid {
		return Claims{}, fmt.Errorf("invalid access token: %w", err)
	}
	if claims.Role == "ops" {
		if claims.ClassID != 0 || claims.Subject != "ops" {
			return Claims{}, errors.New("invalid ops claims")
		}
		return claims, nil
	}
	if claims.ClassID <= 0 || claims.Role == "" {
		return Claims{}, errors.New("access token lacks tenant claims")
	}
	return claims, nil
}

func (m *TokenManager) TTL() time.Duration { return m.ttl }
