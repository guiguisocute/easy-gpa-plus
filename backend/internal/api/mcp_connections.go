package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"easygpa/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type mcpScope struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Role        string `json:"-"`
}

var mcpScopes = []mcpScope{
	{"read", "读取与分析", "读取账号可见的规则、成绩、材料与任务", "student"},
	{"draft", "草稿与上传", "创建、修改材料草稿和上传佐证", "student"},
	{"submit", "学生代办", "正式提交、撤回、申诉及核对成绩", "student"},
	{"review", "审核代办", "提交本人审核与复评结论、异议提案", "group"},
	{"manage", "班级管理", "准备班级变更；本人在网页批准后才能执行", "class_admin"},
	{"export", "统计与导出", "创建和读取本班导出任务", "class_admin"},
}

func mcpRoleAllows(role, minimum string) bool {
	rank := map[string]int{"student": 1, "group": 2, "class_admin": 3}
	return rank[role] > 0 && rank[role] >= rank[minimum]
}
func availableMCPScopes(role string) []mcpScope {
	out := []mcpScope{}
	for _, scope := range mcpScopes {
		if mcpRoleAllows(role, scope.Role) {
			out = append(out, scope)
		}
	}
	return out
}
func (s *Server) mcpMaxTTL() time.Duration {
	if s.cfg.MCPMaxTTLHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(s.cfg.MCPMaxTTLHours) * time.Hour
}
func (s *Server) mcpURL() string { return strings.TrimRight(s.cfg.PublicURL, "/") + "/mcp" }
func mcpSecret() (string, []byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	token := "egm_" + hex.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}

func (s *Server) myAgentConnections(c *gin.Context) {
	actor := mustActor(c)
	rows, err := mustTx(c).Query(c.Request.Context(), `SELECT id,name,token_prefix,scopes,expires_at,revoked_at,created_at,last_used_at FROM agent_connection WHERE user_id=$1 ORDER BY id DESC LIMIT 100`, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id int64
		var name, prefix string
		var scopes []string
		var expires, created time.Time
		var revoked, used *time.Time
		if err := rows.Scan(&id, &name, &prefix, &scopes, &expires, &revoked, &created, &used); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "name": name, "prefix": prefix, "scopes": scopes, "expiresAt": expires, "revokedAt": revoked, "createdAt": created, "lastUsedAt": used})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": items, "url": s.mcpURL(), "enabled": !s.cfg.MCPDisabled, "maxTTLMinutes": int(s.mcpMaxTTL().Minutes()), "scopes": availableMCPScopes(actor.Role)})
}
func (s *Server) createAgentConnection(c *gin.Context) {
	if s.cfg.MCPDisabled {
		writeError(c, 403, "mcp_disabled", "部署者已关闭外部 Agent 接入", nil)
		return
	}
	var input struct {
		Name       string   `json:"name"`
		Password   string   `json:"password"`
		TTLMinutes int      `json:"ttlMinutes"`
		Scopes     []string `json:"scopes"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, 400, "invalid_request", "连接参数不正确", nil)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 80 || input.TTLMinutes < 1 || input.TTLMinutes > int(s.mcpMaxTTL().Minutes()) || len(input.Password) > 1024 {
		writeError(c, 422, "invalid_connection", "请填写名称、当前密码和有效期", nil)
		return
	}
	actor := mustActor(c)
	allowed := availableMCPScopes(actor.Role)
	selected := []string{}
	for _, key := range input.Scopes {
		if !slices.ContainsFunc(allowed, func(x mcpScope) bool { return x.Key == key }) {
			writeError(c, 403, "invalid_scope", "账号无权授予此权限", nil)
			return
		}
		if !slices.Contains(selected, key) {
			selected = append(selected, key)
		}
	}
	if len(selected) == 0 {
		writeError(c, 422, "invalid_scope", "至少选择一种权限", nil)
		return
	}
	slices.Sort(selected)
	tx := mustTx(c)
	var hash *string
	if err := tx.QueryRow(c.Request.Context(), `SELECT password_hash FROM app_user WHERE id=$1 FOR UPDATE`, actor.UserID).Scan(&hash); err != nil {
		writeServiceError(c, err)
		return
	}
	verified := false
	if hash != nil {
		verified, _ = auth.VerifyPassword(input.Password, *hash)
	}
	if !verified {
		writeError(c, 403, "password_mismatch", "当前密码不正确", nil)
		return
	}
	var count int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM agent_connection WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>now()`, actor.UserID).Scan(&count); err != nil {
		writeServiceError(c, err)
		return
	}
	if count >= 10 {
		writeError(c, 409, "connection_limit", "最多保留 10 条有效连接，请先撤销不再使用的连接", nil)
		return
	}
	token, digest, err := mcpSecret()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	expires := time.Now().Add(time.Duration(input.TTLMinutes) * time.Minute)
	var id int64
	err = tx.QueryRow(c.Request.Context(), `INSERT INTO agent_connection(class_id,user_id,name,token_prefix,token_hash,scopes,auth_version,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, actor.ClassID, actor.UserID, input.Name, token[:12], digest, selected, actor.TokenVersion, expires).Scan(&id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "mcp.connection_created", "agent_connection", strconv.FormatInt(id, 10), nil, nil, map[string]any{"name": input.Name, "scopes": selected, "expiresAt": expires}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10), "name": input.Name, "token": token, "url": s.mcpURL(), "expiresAt": expires, "scopes": selected})
}
func (s *Server) revokeAgentConnection(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	tag, err := tx.Exec(c.Request.Context(), `UPDATE agent_connection SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND user_id=$2`, id, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(c, 404, "not_found", "连接不存在", nil)
		return
	}
	if err := appendAudit(c, tx, "mcp.connection_revoked", "agent_connection", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(204)
}
func (s *Server) myAgentOperations(c *gin.Context) {
	rows, err := mustTx(c).Query(c.Request.Context(), `SELECT o.id,o.connection_id,ac.name,o.tool,o.status,o.preview,o.created_at,o.expires_at,o.approved_at,ac.revoked_at,ac.expires_at FROM agent_operation o JOIN agent_connection ac ON ac.id=o.connection_id WHERE ac.user_id=$1 ORDER BY o.id DESC LIMIT 100`, mustActor(c).UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, cid int64
		var name, tool, status string
		var preview []byte
		var created, expires, connectionExpiry time.Time
		var approved, revoked *time.Time
		if err := rows.Scan(&id, &cid, &name, &tool, &status, &preview, &created, &expires, &approved, &revoked, &connectionExpiry); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "connectionId": strconv.FormatInt(cid, 10), "connectionName": name, "tool": tool, "status": status, "preview": jsonRaw(preview), "createdAt": created, "expiresAt": expires, "approvedAt": approved, "connectionActive": revoked == nil && time.Now().Before(connectionExpiry)})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) decideAgentOperation(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Approve *bool `json:"approve"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Approve == nil {
		writeError(c, 400, "invalid_request", "请选择批准或拒绝", nil)
		return
	}
	tx := mustTx(c)
	var status string
	var expires time.Time
	err := tx.QueryRow(c.Request.Context(), `SELECT o.status,LEAST(o.expires_at,ac.expires_at) FROM agent_operation o JOIN agent_connection ac ON ac.id=o.connection_id WHERE o.id=$1 AND ac.user_id=$2 AND ac.revoked_at IS NULL FOR UPDATE OF ac,o`, id, mustActor(c).UserID).Scan(&status, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, 404, "not_found", "操作不存在或连接已撤销", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status != "pending" || !time.Now().Before(expires) {
		writeError(c, 409, "operation_stale", "操作已处理或过期，请重新准备", nil)
		return
	}
	status = "rejected"
	if *input.Approve {
		status = "approved"
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE agent_operation SET status=$2,approved_at=CASE WHEN $2='approved' THEN now() ELSE NULL END WHERE id=$1`, id, status); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "mcp.operation_"+status, "agent_operation", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"status": status})
}
