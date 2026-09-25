package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// 匿名审计。班级管理员读得到本班审计日志，所以举报动作一旦按常规写进去，
// actor_id、IP 和 User-Agent 三样里随便哪一样都足够把举报人指出来——匿名也就没了。
// 这里显式写成无主体：actor_id 为空、不记 IP、不记 UA，只留"发生过这件事"。
// 谁报的仍然可查，但只能由平台运维去 report_reporter 里查，不在班级视野内。
func appendAnonymousAudit(c *gin.Context, tx pgx.Tx, action, resourceType, resourceID string, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := marshalAuditValue("metadata", metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,metadata)
		VALUES ($1,NULL,'anonymous',$2,$3,NULLIF($4,''),$5)
	`, mustActor(c).ClassID, action, resourceType, resourceID, metadataJSON)
	return err
}

func appendAudit(c *gin.Context, tx pgx.Tx, action, resourceType, resourceID string, before, after any, metadata map[string]any) error {
	actor := mustActor(c)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if actor.IsDeputy && strings.HasPrefix(c.FullPath(), "/api/v1/review/deputy/") {
		metadata["adjudicatorRole"] = "deputy"
	}
	if grant, ok := c.Request.Context().Value(governanceExecutionKey{}).(governanceExecution); ok {
		metadata["governanceProposalId"] = fmt.Sprint(grant.ID)
		metadata["adjudicatorRole"] = "collective"
	}
	if automatic, _ := c.Get("governance.automatic"); automatic == true {
		metadata["automaticExecution"] = true
	}
	if connectionID, ok := c.Get("easygpa.mcp_connection"); ok {
		metadata["channel"] = "mcp"
		metadata["connectionId"] = connectionID
		if operationID, ok := c.Get("easygpa.mcp_operation"); ok {
			metadata["operationId"] = operationID
		}
	}
	return appendAuditRecord(c.Request.Context(), tx, actor, c.ClientIP(), c.Request.UserAgent(), action, resourceType, resourceID, before, after, metadata)
}

func appendAuditRecord(ctx context.Context, tx pgx.Tx, actor Actor, ip, userAgent, action, resourceType, resourceID string, before, after any, metadata map[string]any) error {
	var beforeJSON, afterJSON []byte
	var err error
	if before != nil {
		beforeJSON, err = marshalAuditValue("before_data", before)
		if err != nil {
			return err
		}
	}
	if after != nil {
		afterJSON, err = marshalAuditValue("after_data", after)
		if err != nil {
			return err
		}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := marshalAuditValue("metadata", metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_log
		    (class_id,actor_id,actor_role,action,resource_type,resource_id,before_data,after_data,metadata,ip_address,user_agent)
		VALUES
		    ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,NULLIF($10,'')::inet,$11)
	`, actor.ClassID, actor.UserID, actor.Role, action, resourceType, resourceID, beforeJSON, afterJSON, metadataJSON, ip, userAgent)
	return err
}

func marshalAuditValue(field string, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal audit %s: %w", field, err)
	}
	return raw, nil
}
