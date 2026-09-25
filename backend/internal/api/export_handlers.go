package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/exportjob"
	"easygpa/backend/internal/ratelimit"
)

func (s *Server) requestExport(c *gin.Context) {
	var input struct {
		Kind string `json:"kind"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "导出参数不正确", nil)
		return
	}
	input.Kind = strings.TrimSpace(input.Kind)
	if !exportjob.ValidKind(input.Kind) {
		writeError(c, http.StatusUnprocessableEntity, "export_kind_invalid", "导出类型只能是 summary、detail、archive 或 college", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	state, err := evaluateGate(c.Request.Context(), tx, time.Now())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// Export is the one capability that class administrators cannot toggle.
	// The settlement gate is its single source of truth; Export.On remains in
	// legacy scheme JSON only for backwards-compatible decoding.
	if !state.Gate.Open {
		writeError(c, http.StatusConflict, "export_closed", "结算闸门尚未开启", gateResponse(state))
		return
	}
	var runID int64
	err = tx.QueryRow(c.Request.Context(), `
		SELECT r.id FROM settlement_run r
		 WHERE r.status='complete'
		   AND NOT EXISTS (SELECT 1 FROM settlement_invalidation i WHERE i.run_id=r.id)
		 ORDER BY r.created_at DESC,r.id DESC LIMIT 1
	`).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusConflict, "settlement_required", "请先执行或重新执行结算", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// Serialize requests for the same class so a double-click or a retry
	// cannot enqueue several identical full-class renders.
	if _, err := tx.Exec(c.Request.Context(), "SELECT pg_advisory_xact_lock($1::bigint)", int64(0x454750415f657870)+actor.ClassID); err != nil {
		writeServiceError(c, err)
		return
	}
	var existingID, existingStatus string
	var connectionID any
	if id, ok := c.Get("easygpa.mcp_connection"); ok {
		connectionID = id
	}
	err = tx.QueryRow(c.Request.Context(), `
		SELECT id::text,status FROM export_job
		 WHERE class_id=$1 AND run_id=$2 AND kind=$3
		   AND (agent_connection_id IS NULL OR agent_connection_id=$5)
		   AND (status IN ('queued','running') OR (status='complete' AND expires_at>now()))
		   AND (kind<>'college' OR (
		       (status IN ('queued','running') OR object_key LIKE $4)
		       AND created_at >= COALESCE((SELECT updated_at FROM class_timeline WHERE class_id=$1),'-infinity'::timestamptz)
		       AND created_at >= COALESCE((SELECT max(updated_at) FROM whitelist WHERE class_id=$1),'-infinity'::timestamptz)
		   ))
		 ORDER BY created_at DESC,id DESC LIMIT 1
	`, actor.ClassID, runID, input.Kind, "%"+exportjob.CollegeArchiveSuffix, connectionID).Scan(&existingID, &existingStatus)
	if err == nil {
		c.JSON(http.StatusAccepted, gin.H{"jobId": existingID, "status": existingStatus, "kind": input.Kind, "deduplicated": true})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	// Reusing an existing render is a read, including retries after a lost
	// response. Charge only new jobs, and allow all four products in one visit.
	if s.deps.Limiter != nil && !s.enforceRateLimit(c, "export-request", "user:"+strconv.FormatInt(actor.UserID, 10), ratelimit.Rule{Requests: 10, Period: time.Hour, Burst: 4}) {
		return
	}
	var jobID string
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO export_job (class_id,run_id,kind,status,requested_by,agent_connection_id)
		VALUES ($1,$2,$3,'queued',$4,$5) RETURNING id::text
	`, actor.ClassID, runID, input.Kind, actor.UserID, connectionID).Scan(&jobID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "export.requested", "export_job", jobID, nil, map[string]any{"kind": input.Kind, "runId": runID}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueTypedEvent(c.Request.Context(), tx, actor.ClassID, events.ExportRequestedEvent(),
		events.ExportRequestedPayload{JobID: jobID, RunID: runID, Kind: input.Kind}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"jobId": jobID, "status": "queued", "kind": input.Kind})
}

func (s *Server) exportJob(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("job"))
	if len(jobID) < 32 || len(jobID) > 40 {
		writeError(c, http.StatusBadRequest, "invalid_id", "导出任务 ID 不正确", nil)
		return
	}
	tx := mustTx(c)
	var kind, status string
	var objectKey, errorMessage *string
	var runID int64
	var createdAt time.Time
	var startedAt, finishedAt, expiresAt *time.Time
	err := tx.QueryRow(c.Request.Context(), `
		SELECT kind,status,object_key,error_message,run_id,created_at,started_at,finished_at,expires_at
		  FROM export_job WHERE id=$1::uuid
	`, jobID).Scan(&kind, &status, &objectKey, &errorMessage, &runID, &createdAt, &startedAt, &finishedAt, &expiresAt)
	if notFound(c, err, "导出任务") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status == "expired" {
		writeError(c, http.StatusGone, "export_expired", "导出文件已过期，请重新生成", nil)
		return
	}
	response := gin.H{
		"jobId": jobID, "kind": kind, "status": status, "runId": runID,
		"createdAt": createdAt, "startedAt": startedAt, "finishedAt": finishedAt,
		"expiresAt": expiresAt, "error": errorMessage,
	}
	if status == "complete" && objectKey != nil {
		if expiresAt != nil && !expiresAt.After(time.Now()) {
			writeError(c, http.StatusGone, "export_expired", "导出文件已过期，请重新生成", nil)
			return
		}
		filename := "easygpa-" + kind + ".xlsx"
		switch kind {
		case "archive":
			filename = "easygpa-archive.zip"
		case "college":
			filename = "学院报表.zip"
			// Historical job links still point at their original workbook. New
			// requests exclude those jobs above and produce the four-file package.
			if strings.HasSuffix(*objectKey, ".xlsx") {
				filename = "学院报表.xlsx"
			}
		}
		info, err := s.deps.Objects.Stat(c.Request.Context(), *objectKey)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		ttl := 10 * time.Minute
		if expiresAt != nil {
			ttl = min(ttl, time.Until(*expiresAt))
		}
		url, err := s.deps.Objects.PresignGet(c.Request.Context(), *objectKey, ttl, filename, false)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		response["downloadUrl"] = url.String()
		response["downloadExpiresIn"] = int(ttl.Seconds())
		response["filename"] = filename
		response["sizeBytes"] = info.Size
		response["etag"] = info.ETag
	}
	// Status polling and renewing a link do not change the export. Creation
	// and completion already have audit entries; polling must stay read-only.
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, response)
}
