package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"easygpa/backend/internal/agentcontext"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Opening the floating window only reads metadata. It does not enqueue a model
// request, consume question quota, download files, or issue a presigned URL.
func (s *Server) agentPageContext(c *gin.Context) {
	actor := mustActor(c)
	input := agentcontext.Context{View: c.Query("view"), ResourceKind: c.Query("resourceKind"), ResourceID: c.Query("resourceId"), EvidenceID: c.Query("evidenceId")}
	snapshot, err := agentcontext.Load(c.Request.Context(), mustTx(c), actor.ClassID, actor.UserID, input)
	if err != nil {
		writeError(c, http.StatusForbidden, "agent_context_invalid", agentcontext.ErrResource.Error(), nil)
		return
	}
	if snapshot.CheckObservedAt(c.Query("observedAt")) != nil {
		writeError(c, http.StatusConflict, "agent_context_stale", agentcontext.ErrStale.Error(), nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"context": snapshot.Context, "evidence": snapshot.Evidence, "evidenceCount": len(snapshot.Evidence), "canDraft": snapshot.CanDraft})
}

func (s *Server) agentBusinessSource(c *gin.Context, documentID, entryID string) {
	actor := mustActor(c)
	input, err := agentcontext.ParseDocumentID(documentID)
	if err != nil {
		writeError(c, 403, "source_forbidden", agentcontext.ErrResource.Error(), nil)
		return
	}
	snapshot, err := agentcontext.Load(c.Request.Context(), mustTx(c), actor.ClassID, actor.UserID, input)
	if err != nil {
		writeError(c, 403, "source_forbidden", agentcontext.ErrResource.Error(), nil)
		return
	}
	if revision := c.Query("revision"); revision != "" && snapshot.CheckRevision(revision) != nil {
		writeError(c, 409, "action_stale", "事项或来源已经变化，请在原页面核对最新内容", nil)
		return
	}
	response := gin.H{"documentId": documentID, "entryId": entryID, "filename": "事项与规则", "logicalPath": snapshot.Context.ResourceLabel,
		"locator": gin.H{}, "downloadUrl": "", "expiresIn": 0, "content": "", "mediaType": "text/markdown;charset=utf-8"}
	if strings.HasPrefix(entryID, "evidence-") {
		for _, file := range snapshot.Evidence {
			if entryID != "evidence-"+file.ID || file.Status != "ready" {
				continue
			}
			url, err := s.deps.Objects.PresignGet(c.Request.Context(), file.ObjectKey, 10*time.Minute, file.Filename, inlineRenderable(file.MediaType))
			if err != nil {
				writeServiceError(c, err)
				return
			}
			response["filename"] = file.Filename
			response["mediaType"] = file.MediaType
			response["downloadUrl"] = url.String()
			response["expiresIn"] = 600
			if err := appendAudit(c, mustTx(c), "agent.business_source_read", "evidence", file.ID, nil, nil, map[string]any{"resourceId": input.ResourceID, "resourceKind": input.ResourceKind}); err != nil {
				writeServiceError(c, err)
				return
			}
			c.JSON(200, response)
			return
		}
		writeError(c, 403, "source_forbidden", "这份原件已移除或不可读取", nil)
		return
	}
	var content json.RawMessage
	switch entryID {
	case "facts":
		content = snapshot.Facts
		response["filename"] = "申报与审核意见"
	case "rules":
		content = snapshot.Rules
		response["filename"] = "提交时与当前有效规则"
	default:
		writeError(c, 403, "source_forbidden", "来源不存在", nil)
		return
	}
	response["content"] = "### " + snapshot.Context.ResourceLabel + "\n\n```json\n" + prettyAgentJSON(content) + "\n```"
	c.JSON(200, response)
}

func prettyAgentJSON(raw json.RawMessage) string {
	var value any
	_ = json.Unmarshal(raw, &value)
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}

func agentResourceRevoked(ctx context.Context, tx pgx.Tx, ownerID int64, input agentcontext.Context) bool {
	if input.ResourceKind == "" {
		return false
	}
	var classID int64
	if tx.QueryRow(ctx, `SELECT class_id FROM app_user WHERE id=$1`, ownerID).Scan(&classID) != nil {
		return true
	}
	_, err := agentcontext.Load(ctx, tx, classID, ownerID, input)
	return err != nil
}
