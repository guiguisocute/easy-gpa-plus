package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/notify"
	"easygpa/backend/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("账号或密码错误")
	ErrInvalidChallenge   = errors.New("验证码无效或已过期")
	ErrRegistrationDenied = errors.New("学号与姓名未命中可用白名单")
	ErrAlreadyRegistered  = errors.New("该白名单账号已注册")
	ErrAccountDisabled    = errors.New("账号已被停用")
	ErrConflict           = errors.New("账号或邮箱已被使用")
)

type ServiceConfig struct {
	PublicURL   string
	OpsAccount  string
	OpsPassword string
}

type Service struct {
	db              *pgxpool.Pool
	redis           *redis.Client
	tokens          *TokenManager
	sessions        *SessionStore
	mailer          notify.Mailer
	cfg             ServiceConfig
	opsPasswordHash string
	now             func() time.Time
}

func NewService(db *pgxpool.Pool, redisClient *redis.Client, tokens *TokenManager, sessions *SessionStore, mailer notify.Mailer, cfg ServiceConfig) (*Service, error) {
	if db == nil || redisClient == nil || tokens == nil || sessions == nil || mailer == nil {
		return nil, errors.New("auth service dependencies are required")
	}
	if cfg.OpsAccount == "" || cfg.OpsPassword == "" {
		return nil, errors.New("ops credentials are required")
	}
	if err := ValidatePassword(cfg.OpsPassword); err != nil {
		return nil, fmt.Errorf("ops password: %w", err)
	}
	opsPasswordHash, err := HashPassword(cfg.OpsPassword)
	if err != nil {
		return nil, fmt.Errorf("hash ops password: %w", err)
	}
	// Retain only the slow hash after startup. It also provides dummy password
	// work for unknown accounts, closing the cheap account-timing path.
	cfg.OpsPassword = ""
	return &Service{
		db: db, redis: redisClient, tokens: tokens, sessions: sessions, mailer: mailer,
		cfg: cfg, opsPasswordHash: opsPasswordHash, now: time.Now,
	}, nil
}

/* 注册分三步（§10）：
   1. RegisterCheck    校验 (学号, 姓名) 命中白名单，发一张短期票据；不碰密码、不碰邮箱、不发信。
   2. RegisterComplete 凭票据设密码，此时账号才真正建出来，并直接换取会话。
   3. 绑定邮箱是登录后的可选动作，走已有的 /me/emails，不在本文件里。

   把建号从"邮箱验证通过"挪到"密码设置完成"，是这次改动的要害：
   原先账号只在 VerifyEmail 成功后才由 activate 建出来，等于流程上强绑定邮箱——
   而数据库其实一直不要求邮箱（email 在独立的 user_email 表里，app_user 上没有这一列）。 */

type RegisterCheckInput struct {
	SID  string `json:"sid"`
	Name string `json:"name"`
}

type RegisterTicket struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int64  `json:"expires_in"`
	// 回显白名单里的姓名与角色，让用户确认自己没进错班。
	Name string `json:"name"`
	Role string `json:"role"`
}

type RegisterCompleteInput struct {
	Ticket   string `json:"ticket"`
	Password string `json:"password"`
}

type pendingRegistration struct {
	SID          string    `json:"sid"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"passwordHash,omitempty"`
	ClassID      int64     `json:"classId"`
	WhitelistID  int64     `json:"whitelistId"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"createdAt"`
}

const registrationTTL = 15 * time.Minute

// RegisterCheck 只做身份核对。票据本身建不出账号，还要再过一次 RegisterComplete。
func (s *Service) RegisterCheck(ctx context.Context, input RegisterCheckInput) (RegisterTicket, error) {
	input.SID = strings.TrimSpace(input.SID)
	input.Name = strings.TrimSpace(input.Name)
	if input.SID == "" || input.Name == "" || len(input.SID) > 100 || len([]rune(input.Name)) > 200 {
		return RegisterTicket{}, errors.New("学号和姓名不能为空")
	}

	pending := pendingRegistration{SID: input.SID, Name: input.Name, CreatedAt: s.now().UTC()}
	var registeredAt *time.Time
	var active bool
	err := s.db.QueryRow(ctx, "SELECT whitelist_id, class_id, role, registered_at, active FROM auth_find_whitelist($1,$2)", input.SID, input.Name).
		Scan(&pending.WhitelistID, &pending.ClassID, &pending.Role, &registeredAt, &active)
	if errors.Is(err, pgx.ErrNoRows) || !active {
		return RegisterTicket{}, ErrRegistrationDenied
	}
	if err != nil {
		return RegisterTicket{}, fmt.Errorf("lookup whitelist: %w", err)
	}
	if registeredAt != nil {
		return RegisterTicket{}, ErrAlreadyRegistered
	}

	ticket, err := randomHex(24)
	if err != nil {
		return RegisterTicket{}, err
	}
	payload, _ := json.Marshal(pending)
	if err := s.redis.Set(ctx, registrationKey(ticket), payload, registrationTTL).Err(); err != nil {
		return RegisterTicket{}, err
	}
	return RegisterTicket{Ticket: ticket, ExpiresIn: int64(registrationTTL.Seconds()), Name: pending.Name, Role: pending.Role}, nil
}

// RegisterComplete 设密码并建号，直接返回会话——注册完即登录态，绑邮箱在登录后做。
func (s *Service) RegisterComplete(ctx context.Context, input RegisterCompleteInput) (SessionTokens, error) {
	ticket := strings.TrimSpace(input.Ticket)
	if !validHexToken(ticket, 48) {
		return SessionTokens{}, ErrInvalidChallenge
	}
	if err := ValidatePassword(input.Password); err != nil {
		return SessionTokens{}, err
	}

	/* 同一张票据并发提交时只放一个进去，否则两个请求会同时走到 activate。 */
	lockKey := "auth:register-lock:" + ticket
	locked, err := s.redis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil {
		return SessionTokens{}, err
	}
	if !locked {
		return SessionTokens{}, errors.New("该注册请求正在处理中")
	}
	defer s.redis.Del(context.Background(), lockKey)

	pending, err := s.loadRegistration(ctx, ticket)
	if err != nil {
		return SessionTokens{}, err
	}
	pending.PasswordHash, err = HashPassword(input.Password)
	if err != nil {
		return SessionTokens{}, err
	}

	identity, className, err := s.activate(ctx, pending)
	if err != nil {
		return SessionTokens{}, err
	}
	_ = s.redis.Del(ctx, registrationKey(ticket)).Err()
	return s.issueSession(ctx, identity, className)
}

func (s *Service) activate(ctx context.Context, pending pendingRegistration) (Identity, string, error) {
	tx, err := store.BeginTenant(ctx, s.db, pending.ClassID)
	if err != nil {
		return Identity{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	whitelistID := pending.WhitelistID
	var registeredAt *time.Time
	var active bool
	var role string
	if err := tx.QueryRow(ctx, `SELECT registered_at,active,role FROM whitelist WHERE id=$1 FOR UPDATE`, whitelistID).Scan(&registeredAt, &active, &role); err != nil {
		return Identity{}, "", ErrRegistrationDenied
	}
	if registeredAt != nil {
		return Identity{}, "", ErrAlreadyRegistered
	}
	if !active || role != pending.Role {
		return Identity{}, "", ErrRegistrationDenied
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM app_user WHERE whitelist_id=$1 FOR UPDATE`, whitelistID).Scan(&status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, "", err
	}
	if err == nil && status != "active" {
		return Identity{}, "", ErrAccountDisabled
	}

	identity := Identity{ClassID: pending.ClassID, SID: pending.SID, Name: pending.Name, Role: pending.Role}
	err = tx.QueryRow(ctx, `
		INSERT INTO app_user (class_id,whitelist_id,sid,name,password_hash,role)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (whitelist_id) DO UPDATE
		   SET password_hash=EXCLUDED.password_hash,
		       token_version=app_user.token_version+1,
		       updated_at=now()
		 WHERE app_user.password_hash IS NULL AND app_user.status='active'
		RETURNING id,token_version,sid,name,role,is_deputy
	`, pending.ClassID, whitelistID, pending.SID, pending.Name, pending.PasswordHash, pending.Role).
		Scan(&identity.UserID, &identity.TokenVersion, &identity.SID, &identity.Name, &identity.Role, &identity.IsDeputy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, "", ErrAlreadyRegistered
	}
	if err != nil {
		return Identity{}, "", mapConflict(err)
	}
	// First registration leaves email binding optional; resetting a password
	// reuses the existing identity and preserves all previously bound emails.
	if _, err := tx.Exec(ctx, "UPDATE whitelist SET registered_at=now(),updated_at=now() WHERE id=$1", whitelistID); err != nil {
		return Identity{}, "", err
	}
	var className string
	if err := tx.QueryRow(ctx, "SELECT name FROM class WHERE id=$1", pending.ClassID).Scan(&className); err != nil {
		return Identity{}, "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,metadata)
		VALUES ($1,$2,$3,'auth.registered','user',$2::bigint::text,'{}'::jsonb)
	`, pending.ClassID, identity.UserID, identity.Role); err != nil {
		return Identity{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Identity{}, "", err
	}
	return identity, className, nil
}

type LoginInput struct {
	Account  string `json:"account"`
	Password string `json:"password"`
}

func (s *Service) Login(ctx context.Context, input LoginInput) (SessionTokens, error) {
	account := strings.TrimSpace(input.Account)
	if account == "" || len(account) > 320 || input.Password == "" || len(input.Password) > 128 {
		return SessionTokens{}, ErrInvalidCredentials
	}
	if strings.EqualFold(account, s.cfg.OpsAccount) {
		ok, err := VerifyPassword(input.Password, s.opsPasswordHash)
		if err != nil || !ok {
			return SessionTokens{}, ErrInvalidCredentials
		}
		return s.issueSession(ctx, Identity{Role: "ops", Name: "运维超管", SID: s.cfg.OpsAccount, TokenVersion: 1}, "运维平台")
	}
	var identity Identity
	var passwordHash *string
	var status string
	err := s.db.QueryRow(ctx, `
		SELECT user_id,class_id,sid,name,password_hash,role,status,token_version
		FROM auth_find_user($1)
	`, account).Scan(&identity.UserID, &identity.ClassID, &identity.SID, &identity.Name, &passwordHash, &identity.Role, &status, &identity.TokenVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword(input.Password, s.opsPasswordHash)
		return SessionTokens{}, ErrInvalidCredentials
	}
	if err != nil {
		return SessionTokens{}, err
	}
	if passwordHash == nil {
		_, _ = VerifyPassword(input.Password, s.opsPasswordHash)
		return SessionTokens{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(input.Password, *passwordHash)
	if err != nil || !ok {
		return SessionTokens{}, ErrInvalidCredentials
	}
	if status != "active" {
		return SessionTokens{}, ErrAccountDisabled
	}
	tx, err := store.BeginTenant(ctx, s.db, identity.ClassID)
	if err != nil {
		return SessionTokens{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var className string
	if err := tx.QueryRow(ctx, "SELECT name FROM class WHERE id=$1", identity.ClassID).Scan(&className); err != nil {
		return SessionTokens{}, err
	}
	if err := tx.QueryRow(ctx, "UPDATE app_user SET last_login_at=now() WHERE id=$1 RETURNING is_deputy", identity.UserID).Scan(&identity.IsDeputy); err != nil {
		return SessionTokens{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionTokens{}, err
	}
	return s.issueSession(ctx, identity, className)
}

type UserView struct {
	SID       string `json:"sid"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	IsDeputy  bool   `json:"isDeputy"`
	Initial   string `json:"initial"`
	Sub       string `json:"sub"`
	ClassID   int64  `json:"classId"`
	ClassName string `json:"className"`
}

type SessionTokens struct {
	AccessToken  string   `json:"access_token"`
	ExpiresIn    int64    `json:"expires_in"`
	User         UserView `json:"user"`
	RefreshToken string   `json:"-"`
}

func (s *Service) issueSession(ctx context.Context, identity Identity, className string) (SessionTokens, error) {
	refresh, session, err := s.sessions.Create(ctx, identity)
	if err != nil {
		return SessionTokens{}, err
	}
	access, expires, err := s.tokens.Issue(identity, session.SessionID)
	if err != nil {
		_ = s.sessions.Revoke(ctx, refresh)
		return SessionTokens{}, err
	}
	return SessionTokens{
		AccessToken: access, ExpiresIn: int64(time.Until(expires).Seconds()), RefreshToken: refresh,
		User: userView(identity, className),
	}, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (SessionTokens, error) {
	rotated, session, err := s.sessions.Rotate(ctx, refreshToken)
	if err != nil {
		return SessionTokens{}, err
	}
	if session.Identity.Role != "ops" {
		current, status, err := s.lookupIdentity(ctx, session.Identity.SID)
		if err != nil {
			_ = s.sessions.Revoke(ctx, rotated)
			if errors.Is(err, pgx.ErrNoRows) {
				return SessionTokens{}, ErrInvalidRefresh
			}
			return SessionTokens{}, err
		}
		if !identityIsCurrent(session.Identity, current, status) {
			_ = s.sessions.Revoke(ctx, rotated)
			return SessionTokens{}, ErrInvalidRefresh
		}
		session.Identity = current
	}
	access, expires, err := s.tokens.Issue(session.Identity, session.SessionID)
	if err != nil {
		_ = s.sessions.Revoke(ctx, rotated)
		return SessionTokens{}, err
	}
	return SessionTokens{AccessToken: access, ExpiresIn: int64(time.Until(expires).Seconds()), RefreshToken: rotated}, nil
}

func (s *Service) lookupIdentity(ctx context.Context, account string) (Identity, string, error) {
	var identity Identity
	var passwordHash *string
	var status string
	err := s.db.QueryRow(ctx, `
		SELECT user_id,class_id,sid,name,password_hash,role,status,token_version
		  FROM auth_find_user($1)
	`, strings.TrimSpace(account)).Scan(
		&identity.UserID, &identity.ClassID, &identity.SID, &identity.Name, &passwordHash,
		&identity.Role, &status, &identity.TokenVersion,
	)
	return identity, status, err
}

func identityIsCurrent(cached, current Identity, status string) bool {
	return status == "active" &&
		cached.UserID == current.UserID &&
		cached.ClassID == current.ClassID &&
		cached.Role == current.Role &&
		cached.TokenVersion == current.TokenVersion
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.sessions.Revoke(ctx, refreshToken)
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID int64, exceptSessionID string) error {
	return s.sessions.RevokeUser(ctx, userID, exceptSessionID)
}

func (s *Service) ForgotPassword(ctx context.Context, account string) error {
	account = strings.TrimSpace(account)
	if account == "" || len(account) > 320 {
		return nil
	}
	var identity Identity
	var passwordHash *string
	var status string
	err := s.db.QueryRow(ctx, `SELECT user_id,class_id,sid,name,password_hash,role,status,token_version FROM auth_find_user($1)`, account).
		Scan(&identity.UserID, &identity.ClassID, &identity.SID, &identity.Name, &passwordHash, &identity.Role, &status, &identity.TokenVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "active" || passwordHash == nil {
		return nil
	}
	tx, err := store.BeginTenant(ctx, s.db, identity.ClassID)
	if err != nil {
		return err
	}
	var email string
	err = tx.QueryRow(ctx, "SELECT email FROM user_email WHERE user_id=$1 AND is_primary AND verified_at IS NOT NULL", identity.UserID).Scan(&email)
	_ = tx.Rollback(ctx)
	if err != nil {
		return nil
	}
	token, err := newOpaqueToken()
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(identity)
	if err := s.redis.Set(ctx, resetKey(token), payload, 30*time.Minute).Err(); err != nil {
		return err
	}
	link := strings.TrimRight(s.cfg.PublicURL, "/") + "/?reset_token=" + url.QueryEscape(token)
	if _, err := s.mailer.Send(ctx, notify.Message{
		ClassID: identity.ClassID,
		To:      email, Subject: "EasyGPA Plus 重置密码", Text: "请在 30 分钟内打开以下链接重置密码：\n" + link,
		Template: notify.TemplatePasswordReset,
		Data:     map[string]any{"reset_token": token, "expire_minutes": 30},
	}); err != nil {
		slog.Error("send password reset mail", "error", err)
	}
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	token = strings.TrimSpace(token)
	if !validHexToken(token, 64) {
		return ErrInvalidChallenge
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	payload, err := s.redis.GetDel(ctx, resetKey(token)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrInvalidChallenge
	}
	if err != nil {
		return err
	}
	var identity Identity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return ErrInvalidChallenge
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	err = store.InTenantTx(ctx, s.db, identity.ClassID, func(tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `
			UPDATE app_user
			   SET password_hash=$1,token_version=token_version+1,updated_at=now()
			 WHERE id=$2 AND class_id=$3 AND token_version=$4 AND password_hash IS NOT NULL
		`, hash, identity.UserID, identity.ClassID, identity.TokenVersion)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrInvalidChallenge
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id) VALUES ($1,$2,$3,'auth.password_reset','user',$2::bigint::text)`, identity.ClassID, identity.UserID, identity.Role)
		return err
	})
	if err != nil {
		return err
	}
	return s.sessions.RevokeUser(ctx, identity.UserID, "")
}

// 票据没有重试计数，也就不需要把 TTL 回传给调用方续写——过期即失效，重走第一步。
func (s *Service) loadRegistration(ctx context.Context, ticket string) (pendingRegistration, error) {
	payload, err := s.redis.Get(ctx, registrationKey(ticket)).Bytes()
	if errors.Is(err, redis.Nil) {
		return pendingRegistration{}, ErrInvalidChallenge
	}
	if err != nil {
		return pendingRegistration{}, err
	}
	var pending pendingRegistration
	if json.Unmarshal(payload, &pending) != nil {
		return pendingRegistration{}, ErrInvalidChallenge
	}
	return pending, nil
}

func userView(identity Identity, className string) UserView {
	roleLabel := map[string]string{"student": "学生", "group": "综测小组", "class_admin": "班级管理员", "ops": "运维超管"}[identity.Role]
	return UserView{
		SID: identity.SID, Name: identity.Name, Role: identity.Role, Initial: firstRune(identity.Name),
		Sub: roleLabel, ClassID: identity.ClassID, ClassName: className, IsDeputy: identity.IsDeputy,
	}
}

func firstRune(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "?"
	}
	r, _ := utf8.DecodeRuneInString(value)
	return string(r)
}

func newOpaqueToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func validHexToken(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func registrationKey(challengeID string) string { return "auth:register:" + challengeID }
func resetKey(token string) string              { return "auth:reset:" + tokenHash(token) }

func mapConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}
