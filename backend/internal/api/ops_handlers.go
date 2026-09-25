package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/notify"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
)

func (s *Server) registerOpsRoutes(secured *gin.RouterGroup) {
	ops := secured.Group("/ops")
	ops.Use(requireOps())
	ops.PUT("/branding", s.updateBranding)
	ops.GET("/tenants", s.opsTenants)
	ops.GET("/tenants/:id/members", s.opsTenantMembers)
	ops.PUT("/tenants/:id/report-identity", s.importReportIdentity)
	ops.POST("/tenants", s.createTenant)
	ops.PUT("/tenants/:id", s.updateTenant)
	ops.PUT("/tenants/:id/admin", s.appointTenantAdmin)
	ops.GET("/templates", s.opsTemplates)
	ops.GET("/template-share-requests", s.opsTemplateShareRequests)
	ops.POST("/template-share-requests/:id/review", s.reviewTemplateShareRequest)
	ops.POST("/templates", s.createTemplate)
	ops.DELETE("/templates/:id", s.retireTemplate)
	ops.POST("/templates/:id/restore", s.restoreTemplate)
	ops.GET("/agent", s.opsAgent)
	ops.PUT("/agent", s.updateOpsAgent)
	ops.DELETE("/agent", s.clearOpsAgent)
	ops.DELETE("/agent/key", s.clearOpsAgentKey)
	ops.POST("/agent/test", s.testOpsAgent)
	ops.GET("/agent/knowledge", s.opsPlatformKnowledge)
	ops.POST("/agent/knowledge/documents/presign", s.presignOpsPlatformKnowledge)
	ops.POST("/agent/knowledge/documents/:id/complete", s.completeOpsPlatformKnowledge)
	ops.GET("/agent/knowledge/documents/:id", s.opsPlatformKnowledgeDocument)
	ops.PUT("/agent/knowledge/documents/:id", s.updateOpsPlatformKnowledge)
	ops.POST("/agent/knowledge/documents/:id/reprocess", s.reprocessOpsPlatformKnowledge)
	ops.DELETE("/agent/knowledge/documents/:id", s.deleteOpsPlatformKnowledge)
	ops.GET("/agent/providers", s.opsAIProviders)
	ops.POST("/agent/providers", s.createOpsAIProvider)
	ops.PUT("/agent/providers/:id", s.updateOpsAIProvider)
	ops.DELETE("/agent/providers/:id", s.deleteOpsAIProvider)
	ops.DELETE("/agent/providers/:id/key", s.clearOpsAIProviderKey)
	ops.GET("/agent/routes", s.opsModelRoutes)
	ops.PUT("/agent/routes/:purpose", s.updateOpsModelRoute)
	ops.DELETE("/agent/routes/:purpose", s.deleteOpsModelRoute)
	ops.POST("/agent/routes/:purpose/test", s.testOpsModelRoute)
	ops.GET("/mail", s.opsMail)
	ops.PUT("/mail", s.updateOpsMail)
	ops.POST("/mail/test", s.testOpsMail)
	ops.POST("/mail/rotate-key", s.rotateOpsMailKey)
	ops.GET("/mail/log", s.opsMailLog)
	ops.POST("/mail/log/:id/reconcile", s.reconcileOpsMail)
	ops.GET("/queues", s.opsQueues)
	ops.GET("/queues/dead", s.opsDeadLetters)
	ops.POST("/queues/:stream/redeliver", s.redeliverDeadLetters)
	ops.GET("/workers", s.opsWorkers)
	ops.GET("/cron", s.opsCron)
	ops.GET("/backups", s.opsBackups)
	ops.POST("/backups", s.createBackup)
	ops.POST("/backups/restore-drill", s.createRestoreDrill)
	ops.GET("/backups/remote", s.opsBackupRemote)
	ops.PUT("/backups/remote", s.updateOpsBackupRemote)
	ops.DELETE("/backups/remote", s.clearOpsBackupRemote)
	ops.POST("/backups/remote/test", s.testOpsBackupRemote)
	ops.POST("/backups/remote/parse", s.parseOpsBucketURL)
	ops.GET("/lifecycle", s.opsLifecycle)
	ops.PUT("/lifecycle", s.updateOpsLifecycle)
	ops.POST("/tenants/:id/storage/reconcile", s.reconcileTenantStorage)
	ops.GET("/flags", s.opsFlags)
	ops.PUT("/flags/:key", s.updateOpsFlag)
	ops.GET("/health", s.opsHealth)
	ops.GET("/deploy", s.opsDeploy)
	ops.GET("/audit", s.opsAuditLog)
}

func (s *Server) opsPool(c *gin.Context) bool {
	if s.deps.Pools == nil || s.deps.Pools.Ops == nil {
		writeError(c, http.StatusServiceUnavailable, "ops_database_unavailable", "运维数据库连接未配置", nil)
		return false
	}
	return true
}

func (s *Server) appendOpsAudit(ctx context.Context, c *gin.Context, action, resourceType, resourceID string, metadata any) error {
	if s.deps.Pools == nil || s.deps.Pools.Ops == nil {
		return errors.New("ops database unavailable")
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.deps.Pools.Ops.Exec(ctx, `
		INSERT INTO ops_audit (actor,action,resource_type,resource_id,metadata,ip_address)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,'')::inet)
	`, s.cfg.OpsAccount, action, resourceType, resourceID, raw, c.ClientIP())
	return err
}

func (s *Server) opsDenied(c *gin.Context) {
	if s.opsPool(c) {
		_ = s.appendOpsAudit(c.Request.Context(), c, "route.denied", "route", c.Request.URL.Path, map[string]any{"method": c.Request.Method})
	}
	writeError(c, http.StatusNotFound, "not_found", "资源不存在", nil)
}

func (s *Server) noRoute(c *gin.Context) {
	header := c.GetHeader("Authorization")
	schemeName, raw, ok := strings.Cut(header, " ")
	if ok && strings.EqualFold(schemeName, "Bearer") {
		if claims, err := s.deps.Tokens.Parse(strings.TrimSpace(raw)); err == nil && claims.Role == "ops" && s.deps.Pools != nil && s.deps.Pools.Ops != nil {
			_ = s.appendOpsAudit(c.Request.Context(), c, "route.denied", "route", c.Request.URL.Path, map[string]any{"method": c.Request.Method})
		}
	}
	writeError(c, http.StatusNotFound, "not_found", "资源不存在", nil)
}

func (s *Server) opsTenants(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `
		SELECT c.id,c.name,c.slug,c.archived,c.storage_bytes,c.storage_calibrated_at,c.created_at,c.updated_at,
		       ops_class_admins(c.id)
		  FROM class c
		 ORDER BY c.created_at DESC,c.id DESC
	`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, storage int64
		var name string
		var slug *string
		var archived bool
		var calibratedAt *time.Time
		var createdAt, updatedAt time.Time
		var admins json.RawMessage
		if err := rows.Scan(&id, &name, &slug, &archived, &storage, &calibratedAt, &createdAt, &updatedAt, &admins); err != nil {
			writeServiceError(c, err)
			return
		}
		state := "running"
		if archived {
			state = "archived"
		}
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "name": name, "slug": slug, "state": state, "archived": archived,
			"storageBytes": storage, "storageCalibratedAt": calibratedAt, "admins": admins,
			"createdAt": createdAt, "updatedAt": updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "tenant.list", "tenant", "", map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) opsTenantMembers(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var exists bool
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM class WHERE id=$1)`, id).Scan(&exists); err != nil {
		writeServiceError(c, err)
		return
	}
	if !exists {
		writeError(c, http.StatusNotFound, "not_found", "班级不存在", nil)
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `
		SELECT whitelist_id,user_id,sid,name,role,roster_active,registered,account_status,registered_at,last_login_at
		  FROM ops_tenant_members($1)
	`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var whitelistID int64
		var userID *int64
		var sid, name, role string
		var rosterActive, registered bool
		var accountStatus *string
		var registeredAt, lastLoginAt *time.Time
		if err := rows.Scan(&whitelistID, &userID, &sid, &name, &role, &rosterActive, &registered, &accountStatus, &registeredAt, &lastLoginAt); err != nil {
			writeServiceError(c, err)
			return
		}
		var userIDValue any
		if userID != nil {
			userIDValue = strconv.FormatInt(*userID, 10)
		}
		items = append(items, gin.H{
			"whitelistId":   strconv.FormatInt(whitelistID, 10),
			"userId":        userIDValue,
			"sid":           sid,
			"name":          name,
			"role":          role,
			"rosterActive":  rosterActive,
			"registered":    registered,
			"accountStatus": accountStatus,
			"registeredAt":  registeredAt,
			"lastLoginAt":   lastLoginAt,
		})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "tenant.members_listed", "tenant", strconv.FormatInt(id, 10), map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

var (
	slugPattern     = regexp.MustCompile(`[^a-z0-9]+`)
	backupIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func normalizedSlug(value string) string {
	value = strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-"), "-")
	if len(value) > 60 {
		value = strings.Trim(value[:60], "-")
	}
	return value
}

func (s *Server) createTenant(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input struct {
		Name      string `json:"name"`
		Slug      string `json:"slug"`
		AdminSID  string `json:"adminSid"`
		AdminName string `json:"adminName"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "租户参数不正确", nil)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = normalizedSlug(input.Slug)
	input.AdminSID = strings.TrimSpace(input.AdminSID)
	input.AdminName = strings.TrimSpace(input.AdminName)
	if input.Name == "" || len(input.Name) > 200 {
		writeError(c, http.StatusUnprocessableEntity, "tenant_invalid", "班级名称不能为空且不能超过 200 字符", nil)
		return
	}
	if input.AdminSID == "" || input.AdminName == "" || len(input.AdminSID) > 100 || len(input.AdminName) > 200 {
		writeError(c, http.StatusUnprocessableEntity, "admin_invalid", "班级管理员的学号和姓名不能为空", nil)
		return
	}
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var id int64
	err = tx.QueryRow(c.Request.Context(), `INSERT INTO class (name,slug) VALUES ($1,NULLIF($2,'')) RETURNING id`, input.Name, input.Slug).Scan(&id)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "slug_exists", "租户标识已存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if input.Slug == "" {
		input.Slug = "class-" + strconv.FormatInt(id, 10)
		if _, err := tx.Exec(c.Request.Context(), `UPDATE class SET slug=$1 WHERE id=$2`, input.Slug, id); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	var whitelistID int64
	var registered bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT whitelist_id,registered FROM ops_appoint_class_admin($1,$2,$3)`, id, input.AdminSID, input.AdminName).Scan(&whitelistID, &registered); uniqueViolation(err) {
		writeError(c, http.StatusConflict, "sid_assigned", "该学号已属于另一个班级", nil)
		return
	} else if err != nil {
		writeServiceError(c, err)
		return
	}
	metadata, _ := json.Marshal(map[string]any{"name": input.Name, "slug": input.Slug, "adminAssigned": true})
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO ops_audit (actor,action,resource_type,resource_id,metadata,ip_address)
		VALUES ($1,'tenant.created','tenant',$2::bigint::text,$3,NULLIF($4,'')::inet)
	`, s.cfg.OpsAccount, id, metadata, c.ClientIP()); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id": strconv.FormatInt(id, 10), "name": input.Name, "slug": input.Slug,
		"admin": gin.H{"sid": input.AdminSID, "name": input.AdminName, "registered": registered},
	})
}

func (s *Server) updateTenant(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Name     *string `json:"name"`
		Archived *bool   `json:"archived"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || (input.Name == nil && input.Archived == nil) {
		writeError(c, http.StatusBadRequest, "invalid_request", "租户更新参数不正确", nil)
		return
	}
	name := ""
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" || len(name) > 200 {
			writeError(c, http.StatusUnprocessableEntity, "tenant_invalid", "班级名称不能为空且不能超过 200 字符", nil)
			return
		}
	}
	var priorName string
	var priorArchived bool
	var rosterChanged int64
	archived, setArchived := false, input.Archived != nil
	if setArchived {
		archived = *input.Archived
	}
	err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		SELECT updated_name,updated_archived,roster_changed
		  FROM ops_update_tenant($1,$2,$3,$4)
	`, id, name, archived, setArchived).Scan(&priorName, &priorArchived, &rosterChanged)
	if notFound(c, err, "租户") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "tenant.updated", "tenant", strconv.FormatInt(id, 10), map[string]any{"name": priorName, "archived": priorArchived, "rosterChanged": rosterChanged}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatInt(id, 10), "name": priorName, "archived": priorArchived, "rosterChanged": rosterChanged})
}

func (s *Server) appointTenantAdmin(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input struct {
		SID  string `json:"sid"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "班级管理员参数不正确", nil)
		return
	}
	input.SID = strings.TrimSpace(input.SID)
	input.Name = strings.TrimSpace(input.Name)
	if input.SID == "" || input.Name == "" || len(input.SID) > 100 || len(input.Name) > 200 {
		writeError(c, http.StatusUnprocessableEntity, "admin_invalid", "班级管理员的学号和姓名不能为空", nil)
		return
	}
	tx, err := s.deps.Pools.Ops.Begin(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var exists bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM class WHERE id=$1)`, id).Scan(&exists); err != nil {
		writeServiceError(c, err)
		return
	}
	if !exists {
		writeError(c, http.StatusNotFound, "not_found", "租户不存在", nil)
		return
	}
	var whitelistID int64
	var registered bool
	if err := tx.QueryRow(c.Request.Context(), `SELECT whitelist_id,registered FROM ops_appoint_class_admin($1,$2,$3)`, id, input.SID, input.Name).Scan(&whitelistID, &registered); uniqueViolation(err) {
		writeError(c, http.StatusConflict, "sid_assigned", "该学号已属于另一个班级", nil)
		return
	} else if err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO ops_audit (actor,action,resource_type,resource_id,metadata,ip_address)
		VALUES ($1,'tenant.admin_appointed','tenant',$2::bigint::text,jsonb_build_object('registered',$3::boolean),NULLIF($4,'')::inet)
	`, s.cfg.OpsAccount, id, registered, c.ClientIP()); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"whitelistId": strconv.FormatInt(whitelistID, 10),
		"sid":         input.SID, "name": input.Name, "registered": registered,
	})
}

func (s *Server) opsTemplates(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `SELECT id,name,source_class_id,active,created_at,retired_at FROM platform_template ORDER BY created_at DESC,id DESC`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var name string
		var sourceClassID *int64
		var active bool
		var createdAt time.Time
		var retiredAt *time.Time
		if err := rows.Scan(&id, &name, &sourceClassID, &active, &createdAt, &retiredAt); err != nil {
			writeServiceError(c, err)
			return
		}
		item := gin.H{"id": strconv.FormatInt(id, 10), "name": name, "active": active, "createdAt": createdAt, "retiredAt": retiredAt}
		if sourceClassID != nil {
			item["sourceTenantId"] = strconv.FormatInt(*sourceClassID, 10)
		}
		items = append(items, item)
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "template.list", "template", "", map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) createTemplate(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Config) == 0 || len(input.Config) > 2<<20 || !json.Valid(input.Config) {
		writeError(c, http.StatusBadRequest, "invalid_template", "模板必须包含合法且不超过 2 MB 的 JSON 配置", nil)
		return
	}

	template, err := scheme.DecodeTemplate(input.Config)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_template", "模板配置无法解析", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = strings.TrimSpace(template.SchemeName)
	}
	if input.Name == "" || len([]rune(input.Name)) > 200 {
		writeError(c, http.StatusBadRequest, "invalid_name", "模板名称不能为空且不能超过 200 个字符", nil)
		return
	}
	if err := scheme.ValidateTemplate(template); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "template_invalid", "模板整体校验未通过", err.Error())
		return
	}
	// Store one canonical scoring-only shape even when the uploaded file is a
	// legacy full scheme. Class runtime is applied only when the template is
	// copied into a draft.
	canonical, err := json.Marshal(template)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	cfg := scheme.ApplyTemplate(scheme.DefaultSelfReportConfig("template"), template)
	flags := s.runtimeFlags(c.Request.Context())
	if err := validateSchemeEvidencePolicy(cfg, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "template_invalid", "模板佐证上限超过平台限制", err.Error())
		return
	}

	// JSONB 相等比较让重复点击导入保持幂等；若同一份模板曾下架，导入等价于重新上架。
	var id int64
	var active bool
	err = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		SELECT id,active FROM platform_template
		 WHERE name=$1 AND config=$2::jsonb
		 ORDER BY id DESC LIMIT 1
	`, input.Name, canonical).Scan(&id, &active)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}

	created := false
	restored := false
	updated := false
	if errors.Is(err, pgx.ErrNoRows) {
		var priorActive bool
		nameErr := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
			SELECT id,active FROM platform_template WHERE name=$1 ORDER BY id DESC LIMIT 1
		`, input.Name).Scan(&id, &priorActive)
		if nameErr == nil {
			if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
				UPDATE platform_template SET config=$2,active=true,retired_at=NULL WHERE id=$1
			`, id, canonical); err != nil {
				writeServiceError(c, err)
				return
			}
			updated = true
			restored = !priorActive
		} else if errors.Is(nameErr, pgx.ErrNoRows) {
			if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
				INSERT INTO platform_template (name,config) VALUES ($1,$2) RETURNING id
			`, input.Name, canonical).Scan(&id); err != nil {
				writeServiceError(c, err)
				return
			}
			created = true
		} else {
			writeServiceError(c, nameErr)
			return
		}
	} else if !active {
		if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
			UPDATE platform_template SET active=true,retired_at=NULL WHERE id=$1
		`, id); err != nil {
			writeServiceError(c, err)
			return
		}
		restored = true
	}

	action := "template.import_reused"
	if created {
		action = "template.created"
	} else if updated {
		action = "template.updated"
	} else if restored {
		action = "template.restored"
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, action, "template", strconv.FormatInt(id, 10), map[string]any{"name": input.Name}); err != nil {
		writeServiceError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"id": strconv.FormatInt(id, 10), "name": input.Name, "active": true, "created": created, "restored": restored, "updated": updated})
}

func (s *Server) retireTemplate(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	command, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `UPDATE platform_template SET active=false,retired_at=COALESCE(retired_at,now()) WHERE id=$1 AND active`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if command.RowsAffected() == 0 {
		writeError(c, http.StatusConflict, "already_retired", "模板不存在或已经下架", nil)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "template.retired", "template", strconv.FormatInt(id, 10), nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) restoreTemplate(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	command, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `UPDATE platform_template SET active=true,retired_at=NULL WHERE id=$1 AND NOT active`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if command.RowsAffected() == 0 {
		writeError(c, http.StatusConflict, "already_active", "模板不存在或已经上架", nil)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "template.restored", "template", strconv.FormatInt(id, 10), nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) opsMail(c *gin.Context) {
	config, err := s.opsConfig.Mail(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "mail.read", "mail_config", "mail", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	config, secretSource, secretIDSet, secretKeySet := redactOpsMailConfig(config)
	var suppressedRecipients, pendingBatches, failedBatches int
	var lastFeedback *time.Time
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT
		(SELECT count(DISTINCT recipient_hash) FROM mail_suppression),
		(SELECT count(*) FROM mail_batch WHERE status='pending'),
		(SELECT count(*) FROM mail_batch WHERE status='failed'),
		(SELECT max(feedback_checked_at) FROM mail_delivery)`).Scan(&suppressedRecipients, &pendingBatches, &failedBatches, &lastFeedback); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"config": config, "activeProvider": config.Provider, "secretSource": secretSource,
		"sesSecretIdSet": secretIDSet, "sesSecretKeySet": secretKeySet, "secretKeyReady": s.deps.MailCipher != nil,
		"notificationStats": gin.H{"suppressedRecipients": suppressedRecipients, "pendingBatches": pendingBatches, "failedBatches": failedBatches, "lastFeedbackAt": lastFeedback},
	})
}

func redactOpsMailConfig(config opsconfig.Mail) (opsconfig.Mail, string, bool, bool) {
	secretIDSet := strings.TrimSpace(config.SESSecretID) != ""
	secretKeySet := strings.TrimSpace(config.SESSecretKey) != ""
	secretSource := "environment"
	if config.SESConfigured() {
		secretSource = "database"
	}
	// 密文也不下发：浏览器永远只知道"设没设过"，不知道内容。
	config.SESSecretID = ""
	config.SESSecretKey = ""
	return config, secretSource, secretIDSet, secretKeySet
}

func (s *Server) updateOpsMail(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil || len(input) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "邮件配置参数不正确", nil)
		return
	}
	allowed := map[string]bool{
		"notificationsEnabled": true, "sesNotificationFrom": true, "sesNotificationFromName": true,
		"provider": true, "perMinute": true, "perDay": true, "quietStart": true, "quietEnd": true,
		"sesRegion": true, "sesSecretId": true, "sesSecretKey": true,
		"sesFrom": true, "sesFromName": true, "sesReplyTo": true, "sesTemplateIds": true,
	}
	for key := range input {
		if !allowed[key] {
			writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", "邮件配置包含不允许的字段", gin.H{"field": key})
			return
		}
	}
	if err := normalizeMailSES(input, s.deps.MailCipher); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", err.Error(), nil)
		return
	}
	if provider, ok := input["provider"].(string); ok && provider != "tencent_ses" {
		writeError(c, http.StatusUnprocessableEntity, "provider_unsupported", "当前只启用腾讯云 SES API 通道", nil)
		return
	}
	if enabled, exists := input["notificationsEnabled"]; exists {
		if _, ok := enabled.(bool); !ok {
			writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", "业务邮件开关必须为布尔值", nil)
			return
		}
	}
	current, err := s.opsConfig.Mail(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	merged, _ := json.Marshal(current)
	var candidate map[string]any
	_ = json.Unmarshal(merged, &candidate)
	for key, value := range input {
		candidate[key] = value
	}
	merged, _ = json.Marshal(candidate)
	var next opsconfig.Mail
	if err = json.Unmarshal(merged, &next); err != nil {
		writeServiceError(c, err)
		return
	}
	if next.NotificationsEnabled {
		check := notify.SESConfig{NotificationsEnabled: true, From: next.SESFrom, NotificationFrom: next.SESNotificationFrom, NotificationFromName: next.SESNotificationFromName, TemplateIDs: next.SESTemplateIDs}
		if err = check.NotificationReady(); err != nil {
			writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", err.Error(), nil)
			return
		}
		if next.SESNotificationDomain != "" && !strings.HasSuffix(strings.ToLower(next.SESNotificationFrom), "@"+strings.ToLower(next.SESNotificationDomain)) {
			writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", "请使用已设置的通知发件域名", nil)
			return
		}
	}
	if next.NotificationsEnabled != current.NotificationsEnabled {
		input["notificationsSince"] = time.Now().UTC()
	}
	for _, key := range []string{"perMinute", "perDay"} {
		if value, exists := input[key]; exists {
			number, ok := value.(float64)
			if !ok || number < 1 || number > 100000 || number != math.Trunc(number) {
				writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", "邮件限额必须是 1—100000 的整数", gin.H{"field": key})
				return
			}
		}
	}
	for _, key := range []string{"quietStart", "quietEnd"} {
		if value, exists := input[key]; exists {
			clock, ok := value.(string)
			if !ok {
				writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", "静默时间必须使用 HH:mm 格式", gin.H{"field": key})
				return
			}
			if _, err := time.Parse("15:04", clock); err != nil {
				writeError(c, http.StatusUnprocessableEntity, "mail_config_invalid", "静默时间必须使用 HH:mm 格式", gin.H{"field": key})
				return
			}
		}
	}
	raw, _ := json.Marshal(input)
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `UPDATE ops_config SET value=value||$1::jsonb,updated_at=now() WHERE key='mail'`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("mail")
	if err := s.appendOpsAudit(c.Request.Context(), c, "mail.updated", "mail_config", "mail", map[string]any{"fields": mapKeys(input)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// normalizeMailSES 就地校验并加密运维填的腾讯云 SES API 参数。
//
// SecretId/SecretKey 是"写得进、读不出"的字段：加密后落库，任何接口都不回显。
// 空字符串表示显式清空（回退到部署环境变量），不是"没改动"——前端不填就不要带这个键。
func normalizeMailSES(input map[string]any, cipher *opsconfig.Cipher) error {
	for _, key := range []string{"sesRegion", "sesFrom", "sesFromName", "sesReplyTo", "sesNotificationFrom", "sesNotificationFromName"} {
		if value, exists := input[key]; exists {
			text, ok := value.(string)
			if !ok {
				return errors.New(key + " 必须是字符串")
			}
			input[key] = strings.TrimSpace(text)
		}
	}
	if value, exists := input["sesRegion"]; exists {
		mode, _ := value.(string)
		mode = strings.ToLower(strings.TrimSpace(mode))
		switch mode {
		case "ap-guangzhou", "ap-hongkong":
		default:
			return errors.New("腾讯云 SES 地域只能是 ap-guangzhou 或 ap-hongkong")
		}
		input["sesRegion"] = mode
	}
	for _, key := range []string{"sesFrom", "sesReplyTo", "sesNotificationFrom"} {
		value, exists := input[key]
		if !exists || value == "" {
			continue
		}
		text := value.(string)
		if strings.ContainsAny(text, "<>\r\n") {
			return errors.New(key + " 必须是纯邮箱地址，显示名请单独填写")
		}
		parsed, err := mail.ParseAddress(text)
		if err != nil || !strings.EqualFold(parsed.Address, text) {
			return errors.New(key + " 格式不正确")
		}
	}
	if name, exists := input["sesFromName"].(string); exists && strings.ContainsAny(name, ":<>\r\n") {
		return errors.New("发件人显示名不能包含冒号、尖括号或换行")
	}
	if name, exists := input["sesNotificationFromName"].(string); exists && strings.ContainsAny(name, ":<>\r\n") {
		return errors.New("通知发件人显示名格式不正确")
	}
	if value, exists := input["sesTemplateIds"]; exists {
		raw, err := json.Marshal(value)
		if err != nil {
			return errors.New("模板 ID 必须是模板名到正整数 ID 的对象")
		}
		ids := make(map[string]uint64)
		if err := json.Unmarshal(raw, &ids); err != nil {
			return errors.New("模板 ID 必须是模板名到正整数 ID 的对象")
		}
		required := make(map[string]bool, len(opsconfig.RequiredMailTemplates))
		for _, name := range opsconfig.RequiredMailTemplates {
			required[name] = true
			if ids[name] == 0 {
				return errors.New("缺少腾讯云模板 ID：" + name)
			}
		}
		for _, name := range opsconfig.AllMailTemplates() {
			required[name] = true
		}
		for name, id := range ids {
			if !required[name] {
				return errors.New("未知的邮件模板名：" + name)
			}
			if id == 0 {
				return errors.New("腾讯云模板 ID 必须是正整数：" + name)
			}
		}
		input["sesTemplateIds"] = ids
	}
	for _, key := range []string{"sesSecretId", "sesSecretKey"} {
		value, exists := input[key]
		if !exists {
			continue
		}
		secret, ok := value.(string)
		if !ok {
			return errors.New(key + " 必须是字符串")
		}
		if strings.TrimSpace(secret) == "" {
			input[key] = ""
			continue
		}
		if cipher == nil {
			return errors.New("服务端未配置 MAIL_SECRET_KEY，无法安全保存腾讯云凭据；请先设置该环境变量再重试")
		}
		sealed, err := cipher.Seal(secret)
		if err != nil {
			return err
		}
		input[key] = sealed
	}
	return nil
}

func (s *Server) testOpsMail(c *gin.Context) {
	var input struct {
		To string `json:"to"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "测试邮件参数不正确", nil)
		return
	}
	address, err := mail.ParseAddress(strings.TrimSpace(input.To))
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "email_invalid", "收件邮箱格式不正确", nil)
		return
	}
	messageID, err := s.deps.Mailer.Send(c.Request.Context(), notify.Message{
		To: address.Address, Subject: "[综测] 邮件通道测试", Text: "EasyGPA Plus 邮件通道工作正常。",
		Template: notify.TemplateMailTest,
		Data: map[string]any{
			"email": address.Address, "provider": "腾讯云 SES API", "sent_at": time.Now().Format("2006-01-02 15:04:05 MST"),
		},
	})
	if err != nil {
		_ = s.appendOpsAudit(c.Request.Context(), c, "mail.test_failed", "mail_config", "mail", map[string]any{"error": err.Error()})
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "mail.test_sent", "mail_config", "mail", map[string]any{"recipientHash": hashText(address.Address), "messageId": messageID}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true, "messageId": messageID})
}

// 清空库里存的发信通道，回退到部署环境变量。
//
// 这个入口以前恒定返回 409（密钥只在 Secret 里）。现在通道可以在网页上配，
// "撤销我刚填的那套、回到部署给的默认"就成了运维真正需要的动作。
func (s *Server) rotateOpsMailKey(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	const clear = `{"provider":"tencent_ses","notificationsEnabled":false,"sesNotificationFrom":"","sesNotificationFromName":"","sesRegion":"ap-guangzhou","sesSecretId":"","sesSecretKey":"","sesFrom":"","sesFromName":"","sesReplyTo":"","sesTemplateIds":{}}`
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `UPDATE ops_config SET value=value||$1::jsonb,updated_at=now() WHERE key='mail'`, clear); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("mail")
	if err := s.appendOpsAudit(c.Request.Context(), c, "mail.channel_cleared", "mail_config", "mail", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cleared": true})
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(sum[:])
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *Server) opsMailLog(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	page, pageSize := opsPage(c, 50, 200)
	status := strings.TrimSpace(c.Query("status"))
	var tenantID *int64
	if raw := strings.TrimSpace(c.Query("tenantId")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			writeError(c, http.StatusBadRequest, "tenant_invalid", "租户 ID 格式不正确", nil)
			return
		}
		tenantID = &value
	}
	from, to, ok := opsTimeRange(c)
	if !ok {
		return
	}
	var total int64
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		SELECT ops_mail_delivery_log_count_v2($1,$2,$3,$4)
	`, status, tenantID, from, to).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `
		SELECT l.delivery_id,l.tenant_id,l.event_id,l.recipient_hash,l.provider,l.status,l.attempt,l.error_message,l.created_at,l.total_count,l.template,l.error_code,l.message_id,d.feedback_status
		  FROM ops_mail_delivery_log_v3($1,$2,$3,$4,$5,$6) l JOIN mail_delivery d ON d.id=l.delivery_id
		 ORDER BY l.created_at DESC,l.delivery_id DESC
	`, status, tenantID, from, to, pageSize, (page-1)*pageSize)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var tenantID *int64
		var eventID, errorMessage *string
		var recipientHash, provider, status string
		var template, feedbackStatus string
		var errorCode, messageID *string
		var attempt int
		var createdAt time.Time
		var rowTotal int64
		if err := rows.Scan(&id, &tenantID, &eventID, &recipientHash, &provider, &status, &attempt, &errorMessage, &createdAt, &rowTotal, &template, &errorCode, &messageID, &feedbackStatus); err != nil {
			writeServiceError(c, err)
			return
		}
		var tenantValue any
		if tenantID != nil {
			tenantValue = strconv.FormatInt(*tenantID, 10)
		}
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "tenantId": tenantValue, "eventId": eventID,
			"recipientHash": recipientHash, "provider": provider, "status": status, "attempt": attempt,
			"error": errorMessage, "createdAt": createdAt,
			"template": template, "errorCode": errorCode, "messageId": messageID,
			"feedbackStatus": feedbackStatus,
		})
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "mail.log_read", "mail_delivery", "", map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

func (s *Server) opsQueues(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	length, err := s.deps.Redis.XLen(c.Request.Context(), events.StreamName).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		writeServiceError(c, err)
		return
	}
	groups, err := s.deps.Redis.XInfoGroups(c.Request.Context(), events.StreamName).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		writeServiceError(c, err)
		return
	}
	items := make([]gin.H, 0, len(groups))
	for _, group := range groups {
		items = append(items, gin.H{
			"stream": events.StreamName, "group": group.Name, "length": length, "pending": group.Pending,
			"lag": group.Lag, "consumers": group.Consumers, "lastDeliveredId": group.LastDeliveredID,
		})
	}
	dead, _ := s.deps.Redis.XLen(c.Request.Context(), events.DeadStream).Result()
	alerts, alertCount, err := events.DeliveryAlerts(c.Request.Context(), s.deps.Redis)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	outboxPending := int64(0)
	_ = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT ops_outbox_pending()`).Scan(&outboxPending)
	if err := s.appendOpsAudit(c.Request.Context(), c, "queue.read", "queue", "", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "deadLetters": dead, "outboxPending": outboxPending, "deliveryAlerts": alerts, "deliveryAlertCount": alertCount})
}

func (s *Server) redeliverDeadLetters(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	stream := strings.TrimSpace(c.Param("stream"))
	if stream != "dead" && stream != events.DeadStream {
		writeError(c, http.StatusNotFound, "not_found", "死信流不存在", nil)
		return
	}
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "死信重投参数不正确", nil)
		return
	}
	if len(input.IDs) < 1 || len(input.IDs) > 100 {
		writeError(c, http.StatusUnprocessableEntity, "dead_letter_ids_invalid", "请选择 1—100 条死信后再重投", nil)
		return
	}
	messages := make([]redis.XMessage, 0)
	seen := make(map[string]struct{}, len(input.IDs))
	for _, rawID := range input.IDs {
		id := strings.TrimSpace(rawID)
		if !redisStreamID(id) {
			writeError(c, http.StatusUnprocessableEntity, "dead_letter_id_invalid", "死信 ID 格式不正确", gin.H{"id": rawID})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		found, err := s.deps.Redis.XRangeN(c.Request.Context(), events.DeadStream, id, id, 1).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			writeServiceError(c, err)
			return
		}
		if len(found) != 1 {
			writeError(c, http.StatusConflict, "dead_letter_missing", "选中的死信已不存在，请刷新后重试", gin.H{"id": id})
			return
		}
		messages = append(messages, found[0])
	}
	requested := make([]events.Redelivery, 0, len(messages))
	for _, message := range messages {
		requested = append(requested, events.Redelivery{DeadID: message.ID, EventID: fmt.Sprint(message.Values["event_id"]), Group: fmt.Sprint(message.Values["group"])})
	}
	// Persist the operator's selected IDs before changing Redis, so a database
	// outage after the replay cannot erase the recovery's audit trail.
	if err := s.appendOpsAudit(c.Request.Context(), c, "queue.redelivery_requested", "queue", events.DeadStream, map[string]any{"items": requested}); err != nil {
		writeServiceError(c, err)
		return
	}
	redeliveries, err := events.RedeliverDeadLetters(c.Request.Context(), s.deps.Redis, messages)
	if errors.Is(err, events.ErrDeadLetterChanged) {
		writeError(c, http.StatusConflict, "dead_letter_changed", "选中的死信已被处理或仍有任务执行中，请刷新后重试", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	redelivered := len(redeliveries)
	if err := s.appendOpsAudit(c.Request.Context(), c, "queue.redelivered", "queue", events.DeadStream, map[string]any{"count": redelivered, "items": redeliveries}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"redelivered": redelivered})
}

func (s *Server) opsCron(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var lastBackup *time.Time
	var lastBackupStatus, lastBackupError *string
	var lastBackupDuration *int64
	err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		SELECT COALESCE(finished_at,created_at),status,
		       CASE WHEN finished_at IS NULL THEN NULL ELSE (extract(epoch FROM finished_at-created_at)*1000)::bigint END,
		       NULLIF(detail->>'error','')
		  FROM backup_job WHERE kind='backup' ORDER BY created_at DESC,id DESC LIMIT 1
	`).Scan(&lastBackup, &lastBackupStatus, &lastBackupDuration, &lastBackupError)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	policy, err := s.opsConfig.Lifecycle(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	nextFive := now.Truncate(5 * time.Minute).Add(5 * time.Minute)
	items := []gin.H{
		{"name": "window-reminders", "schedule": "daily after 09:00 Asia/Shanghai", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:window-reminders:last"), "next": nextDaily(now, "09:00")},
		{"name": "auto-seal", "schedule": "every 5 minutes", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:auto-seal:last"), "next": nextFive},
		{"name": "review-sla", "schedule": "daily after 08:00 Asia/Shanghai", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:review-sla:last"), "next": nextDaily(now, "08:00")},
		{"name": "export-expiry", "schedule": "every 5 minutes", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:export-expiry:last"), "next": nextFive, "config": gin.H{"retentionDays": policy.ExportRetentionDays}},
		{"name": "ai-asset-expiry", "schedule": "every 5 minutes", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:ai-asset-expiry:last"), "next": nextFive},
		{"name": "agent-attachment-expiry", "schedule": "every 5 minutes", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:agent-attachment-expiry:last"), "next": nextFive, "config": gin.H{"graceHours": policy.AgentAttachmentGraceHours}},
		{"name": "knowledge-blob-expiry", "schedule": "every 5 minutes", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:knowledge-blob-expiry:last"), "next": nextFive, "config": gin.H{"graceHours": policy.KnowledgeDeleteGraceHours}},
		{"name": "storage-reconcile", "schedule": "every maintenance tick, gated by configured interval", "handler": "worker:maintenance", "last": redisString(s.deps.Redis, c.Request.Context(), "easygpa:cron:storage-reconcile:last"), "next": now.Add(time.Duration(policy.StorageReconcileMinutes) * time.Minute), "config": gin.H{"intervalMinutes": policy.StorageReconcileMinutes}},
		{"name": "daily-backup", "schedule": "daily after " + policy.BackupSchedule + " Asia/Shanghai", "handler": "worker:backup", "last": lastBackup, "next": nextDaily(now, policy.BackupSchedule), "config": gin.H{"enabled": policy.BackupEnabled, "retentionDays": policy.BackupRetentionDays}},
	}
	for _, item := range items[:len(items)-1] {
		mergeCronStatus(s.deps.Redis, c.Request.Context(), item)
	}
	if lastBackupStatus != nil {
		backup := items[len(items)-1]
		backup["result"] = *lastBackupStatus
		backup["durationMs"] = lastBackupDuration
		backup["error"] = lastBackupError
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "cron.read", "cron", "", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func mergeCronStatus(client *redis.Client, ctx context.Context, item gin.H) {
	name, _ := item["name"].(string)
	if name == "" {
		return
	}
	raw, err := client.Get(ctx, "easygpa:cron:"+name+":status").Bytes()
	if err != nil {
		return
	}
	var status struct {
		Last       time.Time `json:"last"`
		DurationMs int64     `json:"durationMs"`
		Result     string    `json:"result"`
		Error      string    `json:"error"`
	}
	if json.Unmarshal(raw, &status) != nil {
		return
	}
	item["last"] = status.Last
	item["durationMs"] = status.DurationMs
	item["result"] = status.Result
	if status.Error != "" {
		item["error"] = status.Error
	}
}

func nextDaily(now time.Time, value string) time.Time {
	clock, err := time.Parse("15:04", value)
	if err != nil {
		clock, _ = time.Parse("15:04", "00:00")
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func redisString(client *redis.Client, ctx context.Context, key string) any {
	value, err := client.Get(ctx, key).Result()
	if err != nil {
		return nil
	}
	return value
}

func (s *Server) opsBackups(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	kind := strings.TrimSpace(c.Query("kind"))
	status := strings.TrimSpace(c.Query("status"))
	var total int
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM backup_job WHERE ($1='' OR kind=$1) AND ($2='' OR status=$2)
	`, kind, status).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `
		SELECT id::text,kind,status,offsite,detail,created_at,finished_at
		  FROM backup_job WHERE ($1='' OR kind=$1) AND ($2='' OR status=$2)
		 ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4
	`, kind, status, pageSize, (page-1)*pageSize)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, kind, status string
		var offsite bool
		var detail []byte
		var createdAt time.Time
		var finishedAt *time.Time
		if err := rows.Scan(&id, &kind, &status, &offsite, &detail, &createdAt, &finishedAt); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "kind": kind, "status": status, "offsite": offsite, "detail": json.RawMessage(detail), "createdAt": createdAt, "finishedAt": finishedAt})
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "backup.list", "backup", "", map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	policy, err := s.opsConfig.Lifecycle(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	remote, err := s.opsConfig.BackupRemote(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	lastPushAt, lastPushOk, err := s.lastRemotePush(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items, "total": total, "page": page, "pageSize": pageSize,
		"policy": gin.H{
			"enabled": s.cfg.BackupDir != "", "automatic": policy.BackupEnabled, "schedule": "daily " + policy.BackupSchedule + " Asia/Shanghai",
			"retentionDays": policy.BackupRetentionDays, "offsite": s.cfg.BackupOffsiteDir != "",
			"remote": gin.H{
				"enabled": remote.Enabled && remote.Configured(), "bucket": remote.Bucket,
				"retentionDays": remote.RetentionDays, "lastPushAt": lastPushAt, "lastPushOk": lastPushOk,
			},
		},
	})
}

// lastRemotePush 找最近一条已结束的远程副本任务。界面上要能一眼看出"异地那份
// 到底还在不在更新"，翻列表找 remote_push 太慢。
func (s *Server) lastRemotePush(ctx context.Context) (*time.Time, bool, error) {
	var finishedAt *time.Time
	var status string
	err := s.deps.Pools.Ops.QueryRow(ctx, `
		SELECT finished_at,status FROM backup_job
		 WHERE kind='remote_push' AND status IN ('complete','failed')
		 ORDER BY finished_at DESC NULLS LAST,id DESC LIMIT 1
	`).Scan(&finishedAt, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return finishedAt, status == "complete", nil
}

func (s *Server) createRestoreDrill(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	if s.cfg.BackupDir == "" {
		writeError(c, http.StatusServiceUnavailable, "backup_unavailable", "备份 Worker 尚未配置 BACKUP_DIR", nil)
		return
	}
	var input struct {
		BackupID string `json:"backupId"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "恢复演练参数不正确", nil)
			return
		}
	}
	input.BackupID = strings.TrimSpace(input.BackupID)
	var sourceID string
	if input.BackupID == "" {
		err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
			SELECT id::text FROM backup_job WHERE kind='backup' AND status='complete'
			 ORDER BY finished_at DESC,id DESC LIMIT 1
		`).Scan(&sourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, http.StatusConflict, "backup_unavailable", "当前没有可用于恢复演练的成功备份", nil)
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
	} else {
		if !backupIDPattern.MatchString(input.BackupID) {
			writeError(c, http.StatusBadRequest, "backup_id_invalid", "备份任务 ID 格式不正确", nil)
			return
		}
		err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
			SELECT id::text FROM backup_job WHERE id=$1::uuid AND kind='backup' AND status='complete'
		`, input.BackupID).Scan(&sourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, http.StatusConflict, "backup_unavailable", "指定备份不存在或尚未完成", nil)
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	detail, _ := json.Marshal(map[string]any{"sourceBackupId": sourceID, "requestedBy": s.cfg.OpsAccount})
	var id string
	err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		INSERT INTO backup_job (kind,status,offsite,detail) VALUES ('restore_drill','queued',false,$1) RETURNING id::text
	`, detail).Scan(&id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "backup.restore_drill_requested", "backup", id, map[string]any{"sourceBackupId": sourceID}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": id, "status": "queued"})
}

var opsFlagNumberBounds = map[string][2]int{
	"uploadMaxMb":               {1, 50},
	"requestConcurrency":        {1, 512},
	"exportConcurrency":         {1, 8},
	"passwordHashConcurrency":   {1, 16},
	"apiRateLimitPerMinute":     {10, 100000},
	"apiRateLimitBurst":         {1, 100000},
	"authLoginPerMinute":        {1, 120},
	"authRefreshPerMinute":      {1, 600},
	"authRegisterPerHour":       {1, 1000},
	"authForgotPerHour":         {1, 120},
	"authResetPerHour":          {1, 120},
	"evidencePresignPerHour":    {1, 5000},
	"evidenceDailyMb":           {1, 10000},
	"aiPresignPerHour":          {1, 5000},
	"agentPresignPerHour":       {1, 5000},
	"knowledgePresignPerHour":   {1, 5000},
	"aiBatchActionsPerHour":     {1, 500},
	"agentMessagesPerMinute":    {1, 600},
	"exportRequestsPerHour":     {1, 500},
	"knowledgeReprocessPerHour": {1, 500},
}

func (s *Server) opsFlags(c *gin.Context) {
	flags, err := s.opsConfig.Flags(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "flags.read", "flags", "", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	apiLimit := s.deploymentAPIRateLimit()
	flags.APIRateLimitPerMinute = min(flags.APIRateLimitPerMinute, apiLimit.Requests)
	flags.APIRateLimitBurst = min(flags.APIRateLimitBurst, apiLimit.Burst, flags.APIRateLimitPerMinute)
	passwordConcurrency := s.deploymentPasswordHashConcurrency()
	flags.PasswordHashConcurrency = min(flags.PasswordHashConcurrency, passwordConcurrency)
	c.JSON(http.StatusOK, gin.H{
		"flags": flags, "locked": []string{},
		"deploymentLimits": gin.H{
			"apiRateLimitPerMinute":   apiLimit.Requests,
			"apiRateLimitBurst":       apiLimit.Burst,
			"passwordHashConcurrency": passwordConcurrency,
		},
	})
}

func (s *Server) updateOpsFlag(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	key := strings.TrimSpace(c.Param("key"))
	allowed := map[string]string{
		"maintenance": "bool", "registration": "bool", "aiEnabled": "bool",
		"knowledgeEnabled": "bool", "agentActionsEnabled": "bool", "knowledgeEgressEnabled": "bool",
		"nativeToolsEnabled":     "bool",
		"evidenceAllowedFormats": "list",
	}
	for numberKey := range opsFlagNumberBounds {
		allowed[numberKey] = "number"
	}
	kind, ok := allowed[key]
	if !ok {
		writeError(c, http.StatusNotFound, "not_found", "功能开关不存在", nil)
		return
	}
	var input struct {
		Value any `json:"value"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "功能开关参数不正确", nil)
		return
	}
	if kind == "bool" {
		if _, ok := input.Value.(bool); !ok {
			writeError(c, http.StatusUnprocessableEntity, "flag_invalid", "该开关需要布尔值", nil)
			return
		}
	} else if kind == "number" {
		value, ok := input.Value.(float64)
		bounds := opsFlagNumberBounds[key]
		maximum := bounds[1]
		apiLimit := s.deploymentAPIRateLimit()
		if key == "apiRateLimitPerMinute" {
			maximum = min(maximum, apiLimit.Requests)
		} else if key == "apiRateLimitBurst" {
			maximum = min(maximum, apiLimit.Burst)
		} else if key == "passwordHashConcurrency" {
			maximum = min(maximum, s.deploymentPasswordHashConcurrency())
		}
		if !ok || value < float64(bounds[0]) || value > float64(maximum) || value != math.Trunc(value) {
			writeError(c, http.StatusUnprocessableEntity, "flag_invalid", fmt.Sprintf("该阈值需要 %d—%d 的整数", bounds[0], maximum), nil)
			return
		}
		flags, err := s.opsConfig.Flags(c.Request.Context())
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if key == "apiRateLimitPerMinute" && int(value) < min(flags.APIRateLimitBurst, apiLimit.Burst) {
			writeError(c, http.StatusUnprocessableEntity, "flag_invalid", "API 每分钟限额不能低于当前突发额度，请先调低突发额度", nil)
			return
		}
		if key == "apiRateLimitBurst" && int(value) > min(flags.APIRateLimitPerMinute, apiLimit.Requests) {
			writeError(c, http.StatusUnprocessableEntity, "flag_invalid", "API 突发额度不能高于每分钟限额", nil)
			return
		}
	} else {
		raw, ok := input.Value.([]any)
		if !ok {
			writeError(c, http.StatusUnprocessableEntity, "flag_invalid", "允许格式需要字符串列表", nil)
			return
		}
		values := make([]string, 0, len(raw))
		for _, item := range raw {
			value, ok := item.(string)
			if !ok {
				writeError(c, http.StatusUnprocessableEntity, "flag_invalid", "允许格式需要字符串列表", nil)
				return
			}
			values = append(values, value)
		}
		normalized, err := opsconfig.NormalizeEvidenceAllowedFormats(values)
		if err != nil {
			writeError(c, http.StatusUnprocessableEntity, "flag_invalid", err.Error(), nil)
			return
		}
		input.Value = normalized
	}
	if key == "aiEnabled" {
		enable, _ := input.Value.(bool)
		if enable {
			if err := s.runtimeModelBindingsReady(c.Request.Context(), opsconfig.PurposeMaterialVision, opsconfig.PurposeMaterialCompose); err != nil {
				writeError(c, http.StatusUnprocessableEntity, "ai_config_incomplete", err.Error(), nil)
				return
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{key: input.Value})
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `UPDATE ops_config SET value=value||$1::jsonb,updated_at=now() WHERE key='flags'`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("flags")
	if err := s.appendOpsAudit(c.Request.Context(), c, "flag.updated", "flag", key, map[string]any{"value": input.Value}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) opsHealth(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	type check struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
	}
	checks := make([]check, 0, 5)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	if err := s.deps.Pools.App.Ping(ctx); err != nil {
		checks = append(checks, check{Name: "postgresApp", Status: "down", Detail: err.Error()})
	} else {
		checks = append(checks, check{Name: "postgresApp", Status: "ok"})
	}
	if err := s.deps.Pools.Ops.Ping(ctx); err != nil {
		checks = append(checks, check{Name: "postgresOps", Status: "down", Detail: err.Error()})
	} else {
		checks = append(checks, check{Name: "postgresOps", Status: "ok"})
	}
	if err := s.deps.Redis.Ping(ctx).Err(); err != nil {
		checks = append(checks, check{Name: "redis", Status: "down", Detail: err.Error()})
	} else {
		checks = append(checks, check{Name: "redis", Status: "ok"})
	}
	if err := s.deps.Objects.Ready(ctx); err != nil {
		checks = append(checks, check{Name: "objectStore", Status: "down", Detail: err.Error()})
	} else {
		checks = append(checks, check{Name: "objectStore", Status: "ok"})
	}
	if checker, ok := s.deps.Mailer.(interface{ Ready(context.Context) error }); ok {
		if err := checker.Ready(ctx); err != nil {
			checks = append(checks, check{Name: "ses", Status: "down", Detail: err.Error()})
		} else {
			checks = append(checks, check{Name: "ses", Status: "ok"})
		}
	} else {
		checks = append(checks, check{Name: "ses", Status: "unknown", Detail: "mailer does not expose a readiness check"})
	}
	overall := "ok"
	for _, item := range checks {
		if item.Status == "down" {
			overall = "degraded"
		}
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "health.read", "health", "", map[string]any{"status": overall}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": overall, "components": checks})
}

func (s *Server) opsDeploy(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var migrationVersion int64
	_ = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT COALESCE(max(version),0) FROM schema_migration`).Scan(&migrationVersion)
	businessRole, err := inspectDatabaseRole(c.Request.Context(), s.deps.Pools.App)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	opsRole, err := inspectDatabaseRole(c.Request.Context(), s.deps.Pools.Ops)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "deploy.read", "deploy", "", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"appEnv": s.cfg.AppEnv, "imageTag": s.cfg.ImageTag, "gitSha": s.cfg.GitSHA,
		"migrationVersion": migrationVersion,
		"databaseRole":     businessRole,
		"opsDatabaseRole":  opsRole,
		"aiEnabled":        s.aiSwitchEnabled(c.Request.Context()),
	})
}

func inspectDatabaseRole(ctx context.Context, pool *pgxpool.Pool) (gin.H, error) {
	var name string
	var superuser, bypassRLS bool
	err := pool.QueryRow(ctx, `
		SELECT current_user,r.rolsuper,r.rolbypassrls FROM pg_roles r WHERE r.rolname=current_user
	`).Scan(&name, &superuser, &bypassRLS)
	if err != nil {
		return nil, err
	}
	return gin.H{"name": name, "superuser": superuser, "bypassRls": bypassRLS}, nil
}

func (s *Server) opsAuditLog(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	page, pageSize := opsPage(c, 50, 200)
	action := strings.TrimSpace(c.Query("action"))
	resourceType := strings.TrimSpace(c.Query("resourceType"))
	actorQuery := strings.TrimSpace(c.Query("actor"))
	from, to, ok := opsTimeRange(c)
	if !ok {
		return
	}
	var total int
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM ops_audit
		 WHERE ($1='' OR action=$1 OR action LIKE $1||'.%')
		   AND ($2='' OR resource_type=$2)
		   AND ($3='' OR actor ILIKE '%'||$3||'%')
		   AND ($4::timestamptz IS NULL OR created_at >= $4)
		   AND ($5::timestamptz IS NULL OR created_at <= $5)
	`, action, resourceType, actorQuery, from, to).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := s.deps.Pools.Ops.Query(c.Request.Context(), `
		SELECT id,actor,action,resource_type,resource_id,metadata,ip_address::text,created_at
		  FROM ops_audit
		 WHERE ($1='' OR action=$1 OR action LIKE $1||'.%')
		   AND ($2='' OR resource_type=$2)
		   AND ($3='' OR actor ILIKE '%'||$3||'%')
		   AND ($4::timestamptz IS NULL OR created_at >= $4)
		   AND ($5::timestamptz IS NULL OR created_at <= $5)
		 ORDER BY created_at DESC,id DESC LIMIT $6 OFFSET $7
	`, action, resourceType, actorQuery, from, to, pageSize, (page-1)*pageSize)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var actor, action, resourceType string
		var resourceID, ip *string
		var metadata []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &actor, &action, &resourceType, &resourceID, &metadata, &ip, &createdAt); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{
			"id": strconv.FormatInt(id, 10), "actor": actor, "action": action, "resourceType": resourceType,
			"resourceId": resourceID, "metadata": json.RawMessage(metadata), "ip": ip, "createdAt": createdAt,
		})
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "audit.read", "ops_audit", "", map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

func opsPage(c *gin.Context, defaultSize, maxSize int) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(defaultSize)))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > maxSize {
		pageSize = defaultSize
	}
	return page, pageSize
}

func opsTimeRange(c *gin.Context) (*time.Time, *time.Time, bool) {
	parse := func(key string) (*time.Time, bool) {
		raw := strings.TrimSpace(c.Query(key))
		if raw == "" {
			return nil, true
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_time", key+" 必须是 RFC3339 时间", nil)
			return nil, false
		}
		return &value, true
	}
	from, ok := parse("from")
	if !ok {
		return nil, nil, false
	}
	to, ok := parse("to")
	if !ok {
		return nil, nil, false
	}
	if from != nil && to != nil && from.After(*to) {
		writeError(c, http.StatusBadRequest, "invalid_time", "开始时间不能晚于结束时间", nil)
		return nil, nil, false
	}
	return from, to, true
}
