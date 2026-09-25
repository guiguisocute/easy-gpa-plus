package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) adminAuditLog(c *gin.Context) {
	tx := mustTx(c)
	action := strings.TrimSpace(c.Query("action"))
	resourceType := strings.TrimSpace(c.Query("resourceType"))
	actorQuery := strings.TrimSpace(c.Query("actor"))
	var from, to *time.Time
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_time", "from 必须是 RFC3339 时间", nil)
			return
		}
		from = &value
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_time", "to 必须是 RFC3339 时间", nil)
			return
		}
		to = &value
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT a.id,a.actor_id,a.actor_role,actor.sid,actor.name,a.action,a.resource_type,a.resource_id,
		       a.before_data,a.after_data,a.metadata,a.ip_address::text,a.user_agent,a.created_at
		  FROM audit_log a LEFT JOIN app_user actor ON actor.id=a.actor_id
		 WHERE ($1='' OR a.action=$1 OR a.action LIKE $1||'.%')
		   AND ($2='' OR a.resource_type=$2)
		   AND ($3='' OR actor.sid ILIKE '%'||$3||'%' OR actor.name ILIKE '%'||$3||'%')
		   AND ($4::timestamptz IS NULL OR a.created_at >= $4)
		   AND ($5::timestamptz IS NULL OR a.created_at <= $5)
		 ORDER BY a.created_at DESC,a.id DESC LIMIT $6 OFFSET $7
	`, action, resourceType, actorQuery, from, to, pageSize, (page-1)*pageSize)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var actorID *int64
		var actorRole string
		var sid, name, resourceID, ip, userAgent *string
		var actionValue, resource string
		var before, after, metadata []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &actorID, &actorRole, &sid, &name, &actionValue, &resource, &resourceID,
			&before, &after, &metadata, &ip, &userAgent, &createdAt); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		item := gin.H{
			"id": strconv.FormatInt(id, 10), "actorRole": actorRole, "actorSid": sid, "actor": name,
			"action": actionValue, "resourceType": resource, "resourceId": resourceID,
			"metadata": json.RawMessage(metadata), "ip": ip, "userAgent": userAgent, "createdAt": createdAt,
		}
		if actorID != nil {
			item["actorId"] = strconv.FormatInt(*actorID, 10)
		}
		if len(before) > 0 {
			item["before"] = json.RawMessage(before)
		}
		if len(after) > 0 {
			item["after"] = json.RawMessage(after)
		}
		items = append(items, item)
	}
	rows.Close()
	var total int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FROM audit_log a LEFT JOIN app_user actor ON actor.id=a.actor_id
		 WHERE ($1='' OR a.action=$1 OR a.action LIKE $1||'.%')
		   AND ($2='' OR a.resource_type=$2)
		   AND ($3='' OR actor.sid ILIKE '%'||$3||'%' OR actor.name ILIKE '%'||$3||'%')
		   AND ($4::timestamptz IS NULL OR a.created_at >= $4)
		   AND ($5::timestamptz IS NULL OR a.created_at <= $5)
	`, action, resourceType, actorQuery, from, to).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "audit_log.read", "audit_log", "", nil, nil, map[string]any{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}
