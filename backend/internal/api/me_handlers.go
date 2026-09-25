package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/auth"
	"easygpa/backend/internal/notify"
)

func (s *Server) me(c *gin.Context) {
	actor := mustActor(c)
	if actor.Role == "ops" {
		c.JSON(http.StatusOK, gin.H{
			"sid": s.cfg.OpsAccount, "name": "运维超管", "role": "ops", "initial": "运", "sub": "运维超管",
			"classId": 0, "className": "运维平台",
		})
		return
	}
	tx := mustTx(c)
	var sid, name, role, status, className string
	var tokenVersion int64
	err := tx.QueryRow(c.Request.Context(), `
		SELECT u.sid,u.name,u.role,u.status,u.token_version,c.name
		  FROM app_user u JOIN class c ON c.id=u.class_id
		 WHERE u.id=$1
	`, actor.UserID).Scan(&sid, &name, &role, &status, &tokenVersion, &className)
	if err != nil || status != "active" || role != actor.Role || tokenVersion != actor.TokenVersion {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "账号状态已变化，请重新登录", nil)
		return
	}
	_, _ = tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id)
		VALUES ($1,$2,$3,'me.read','user',$2::bigint::text)
	`, actor.ClassID, actor.UserID, actor.Role)
	c.JSON(http.StatusOK, gin.H{
		"sid": sid, "name": name, "role": role, "initial": initial(name), "sub": roleLabel(role),
		"classId": actor.ClassID, "className": className, "isDeputy": actor.IsDeputy,
	})
}

func (s *Server) changePassword(c *gin.Context) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "修改密码参数不正确", nil)
		return
	}
	if err := auth.ValidatePassword(input.NewPassword); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "password_invalid", err.Error(), nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var currentHash string
	if err := tx.QueryRow(c.Request.Context(), `SELECT password_hash FROM app_user WHERE id=$1 FOR UPDATE`, actor.UserID).Scan(&currentHash); err != nil {
		writeServiceError(c, err)
		return
	}
	ok, err := auth.VerifyPassword(input.CurrentPassword, currentHash)
	if err != nil || !ok {
		writeError(c, http.StatusUnauthorized, "password_mismatch", "当前密码不正确", nil)
		return
	}
	same, _ := auth.VerifyPassword(input.NewPassword, currentHash)
	if same {
		writeError(c, http.StatusUnprocessableEntity, "password_unchanged", "新密码不能与当前密码相同", nil)
		return
	}
	hash, err := auth.HashPassword(input.NewPassword)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE app_user SET password_hash=$1,updated_at=now() WHERE id=$2`, hash, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "auth.password_changed", "user", strconv.FormatInt(actor.UserID, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	afterCommit(c, func(ctx context.Context) error {
		return s.deps.Auth.RevokeOtherSessions(ctx, actor.UserID, actor.SessionID)
	})
	c.Status(http.StatusNoContent)
}

type pendingEmail struct {
	UserID       int64  `json:"userId"`
	ClassID      int64  `json:"classId"`
	TokenVersion int64  `json:"tokenVersion"`
	Email        string `json:"email"`
	CodeHash     string `json:"codeHash"`
	Attempts     int    `json:"attempts"`
}

func normalizeUserEmail(value string) (string, error) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil || !strings.Contains(parsed.Address, "@") {
		return "", errors.New("邮箱格式不正确")
	}
	return strings.ToLower(parsed.Address), nil
}

func randomChallenge() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func emailVerificationCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func emailCodeHash(challenge, code string) string {
	sum := sha256.Sum256([]byte(challenge + ":" + code))
	return hex.EncodeToString(sum[:])
}

func emailChallengeKey(id string) string { return "auth:add-email:" + id }

func (s *Server) myEmails(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT id,email,is_primary,verified_at,created_at FROM user_email WHERE user_id=$1 ORDER BY is_primary DESC,created_at,id
	`, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var email string
		var primary bool
		var verifiedAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &primary, &verifiedAt, &createdAt); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "email": email, "primary": primary, "verified": verifiedAt != nil, "verifiedAt": verifiedAt, "createdAt": createdAt})
	}
	if err := appendAudit(c, tx, "email.list", "user_email", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) addEmail(c *gin.Context) {
	var input struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "邮箱参数不正确", nil)
		return
	}
	email, err := normalizeUserEmail(input.Email)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "email_invalid", err.Error(), nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var count int
	var exists bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*),COALESCE(bool_or(email_normalized=$2),false) FROM user_email WHERE user_id=$1`, actor.UserID, email).Scan(&count, &exists); err != nil {
		writeServiceError(c, err)
		return
	}
	if exists {
		writeError(c, http.StatusConflict, "email_exists", "该邮箱已在你的账号中", nil)
		return
	}
	if count >= 5 {
		writeError(c, http.StatusConflict, "email_limit", "一个账号最多保留 5 个邮箱", nil)
		return
	}
	challenge, err := randomChallenge()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	code, err := emailVerificationCode()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	pending := pendingEmail{UserID: actor.UserID, ClassID: actor.ClassID, TokenVersion: actor.TokenVersion, Email: email, CodeHash: emailCodeHash(challenge, code)}
	raw, _ := json.Marshal(pending)
	if err := s.deps.Redis.Set(c.Request.Context(), emailChallengeKey(challenge), raw, 10*time.Minute).Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	var recipientName string
	if err := tx.QueryRow(c.Request.Context(), `SELECT name FROM app_user WHERE id=$1`, actor.UserID).Scan(&recipientName); err != nil {
		_ = s.deps.Redis.Del(c.Request.Context(), emailChallengeKey(challenge)).Err()
		writeServiceError(c, err)
		return
	}
	if _, err := s.deps.Mailer.Send(c.Request.Context(), notify.Message{
		ClassID: actor.ClassID,
		To:      email, Subject: "EasyGPA Plus 验证邮箱", Text: "你的验证码是：" + code + "\n\n10 分钟内有效。",
		Template: notify.TemplateVerificationCode,
		Data:     map[string]any{"name": recipientName, "code": code, "expire_minutes": 10},
	}); err != nil {
		_ = s.deps.Redis.Del(c.Request.Context(), emailChallengeKey(challenge)).Err()
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "email.verification_sent", "user_email", "", nil, nil, map[string]any{"email": email}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"challengeId": challenge, "expiresIn": 600})
}

func (s *Server) verifyAddedEmail(c *gin.Context) {
	var input struct {
		ChallengeID string `json:"challengeId"`
		Code        string `json:"code"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.ChallengeID) == "" {
		writeError(c, http.StatusBadRequest, "invalid_request", "验证码参数不正确", nil)
		return
	}
	key := emailChallengeKey(strings.TrimSpace(input.ChallengeID))
	raw, err := s.deps.Redis.Get(c.Request.Context(), key).Bytes()
	if errors.Is(err, redis.Nil) {
		writeError(c, http.StatusUnprocessableEntity, "code_invalid", "验证码无效或已过期", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var pending pendingEmail
	if json.Unmarshal(raw, &pending) != nil {
		writeError(c, http.StatusUnprocessableEntity, "code_invalid", "验证码无效或已过期", nil)
		return
	}
	actor := mustActor(c)
	if pending.UserID != actor.UserID || pending.ClassID != actor.ClassID || pending.TokenVersion != actor.TokenVersion || subtle.ConstantTimeCompare([]byte(emailCodeHash(input.ChallengeID, strings.TrimSpace(input.Code))), []byte(pending.CodeHash)) != 1 {
		pending.Attempts++
		if pending.Attempts >= 5 {
			_ = s.deps.Redis.Del(c.Request.Context(), key).Err()
		} else {
			ttl, _ := s.deps.Redis.TTL(c.Request.Context(), key).Result()
			next, _ := json.Marshal(pending)
			_ = s.deps.Redis.Set(c.Request.Context(), key, next, ttl).Err()
		}
		writeError(c, http.StatusUnprocessableEntity, "code_invalid", "验证码无效或已过期", nil)
		return
	}
	tx := mustTx(c)
	var id int64
	var isPrimary bool
	// 手上一个已验证主邮箱都没有时，这一封直接成为主邮箱。
	//
	// 以前这里恒为 false，于是"没有主邮箱"的学生要走三步：加邮箱 → 验证 →
	// 再点一次「设为主邮箱」。中间那个状态没有任何意义——一个通知都收不到，
	// 而他并没有第二个邮箱可选。user_email_one_primary 这个部分唯一索引保证
	// 了已经有主邮箱时不会被这里顶掉。
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO user_email (class_id,user_id,email,email_normalized,is_primary,verified_at)
		VALUES ($1,$2,$3,$3,
		        NOT EXISTS (SELECT 1 FROM user_email WHERE user_id=$2 AND is_primary AND verified_at IS NOT NULL),
		        now())
		RETURNING id,is_primary
	`, actor.ClassID, actor.UserID, pending.Email).Scan(&id, &isPrimary)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "email_exists", "该邮箱已被使用", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "email.added", "user_email", strconv.FormatInt(id, 10), nil, map[string]any{"email": pending.Email, "primary": isPrimary}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	afterCommit(c, func(ctx context.Context) error { return s.deps.Redis.Del(ctx, key).Err() })
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10), "email": pending.Email, "primary": isPrimary, "verified": true})
}

func (s *Server) setPrimaryEmail(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var email string
	var verifiedAt *time.Time
	if err := tx.QueryRow(c.Request.Context(), `SELECT email,verified_at FROM user_email WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, actor.UserID).Scan(&email, &verifiedAt); err != nil {
		if notFound(c, err, "邮箱") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if verifiedAt == nil {
		writeError(c, http.StatusConflict, "email_unverified", "邮箱验证后才能设为主邮箱", nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE user_email SET is_primary=false WHERE user_id=$1 AND is_primary`, actor.UserID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE user_email SET is_primary=true WHERE id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "email.primary_changed", "user_email", strconv.FormatInt(id, 10), nil, map[string]any{"email": email, "primary": true}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteEmail(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var email string
	var primary bool
	err := tx.QueryRow(c.Request.Context(), `SELECT email,is_primary FROM user_email WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, actor.UserID).Scan(&email, &primary)
	if notFound(c, err, "邮箱") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if primary {
		writeError(c, http.StatusConflict, "primary_email", "主邮箱不能删除，请先设置另一个主邮箱", nil)
		return
	}
	if err := appendAudit(c, tx, "email.deleted", "user_email", strconv.FormatInt(id, 10), map[string]any{"email": email}, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM user_email WHERE id=$1`, id); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) viewAs(c *gin.Context) {
	var input struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "视角参数不正确", nil)
		return
	}
	actor := mustActor(c)
	rank := map[string]int{"student": 0, "group": 1, "class_admin": 2}
	if _, ok := rank[input.Role]; !ok || rank[input.Role] > rank[actor.Role] {
		writeError(c, http.StatusForbidden, "forbidden", "视角只能降级，不能升级", nil)
		return
	}
	_, err := mustTx(c).Exec(c.Request.Context(), `
		INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,metadata)
		VALUES ($1,$2,$3,'view.changed','user',$2::bigint::text,jsonb_build_object('viewAs',$4::text))
	`, actor.ClassID, actor.UserID, actor.Role, input.Role)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func initial(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "?"
	}
	r, _ := utf8.DecodeRuneInString(value)
	return string(r)
}

func roleLabel(role string) string {
	return map[string]string{"student": "学生", "group": "综测小组", "class_admin": "班级管理员"}[role]
}
