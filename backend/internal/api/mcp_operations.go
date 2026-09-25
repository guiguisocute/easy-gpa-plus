package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func mcpDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func (s *Server) mcpState(c *gin.Context, spec mcpToolSpec) (string, error) {
	tx := mustTx(c)
	if spec.Resource == "submission" {
		var raw []byte
		err := tx.QueryRow(c.Request.Context(), `SELECT to_jsonb(submission) FROM submission WHERE id=$1 AND student_id=$2`, c.Param("id"), mustActor(c).UserID).Scan(&raw)
		if err != nil {
			return "", err
		}
		return mcpDigest(raw), nil
	}
	// Only fixed table names are used. A broad class fingerprint deliberately
	// expires prepared administrative changes if their supporting data changes.
	hash := sha256.New()
	for _, table := range []string{"submission", "review", "submission_reviewer", "appeal", "objection", "report", "scheme", "class_timeline", "student_gpa", "base_score", "seal", "whitelist", "classification_suggestion", "settlement_invalidation", "settlement_run"} {
		var part string
		err := tx.QueryRow(c.Request.Context(), `SELECT md5(COALESCE(string_agg(row_data::text,'|' ORDER BY row_data::text),'')) FROM (SELECT to_jsonb(t) AS row_data FROM `+table+` t) snapshot`).Scan(&part)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s:%s;", table, part)
	}
	var members string
	err := tx.QueryRow(c.Request.Context(), `SELECT md5(COALESCE(string_agg(concat_ws(':',id,sid,name,role,status,is_deputy),'|' ORDER BY id),'')) FROM app_user`).Scan(&members)
	if err != nil {
		return "", err
	}
	_, _ = hash.Write([]byte(members))
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Server) mcpPreview(c *gin.Context, spec mcpToolSpec, call mcpCall) (gin.H, error) {
	preview := gin.H{"title": spec.Title, "input": call.Arguments["input"], "targets": gin.H{}, "notice": "执行时重新核验业务条件；准备后资料变化会使本次操作失效。"}
	targets := preview["targets"].(gin.H)
	for _, p := range spec.Params() {
		targets[p] = call.Arguments[p]
	}
	if c.Param("id") != "" && (spec.Name == "class.arbitrate" || spec.Name == "class.force_score" || spec.Name == "class.force_reject" || spec.Deputy) {
		var studentID int64
		if err := mustTx(c).QueryRow(c.Request.Context(), "SELECT student_id FROM submission WHERE id=$1", c.Param("id")).Scan(&studentID); err != nil {
			return nil, err
		}
		if err := authorizeAdjudication(c.Request.Context(), mustTx(c), mustActor(c), studentID); err != nil {
			return nil, err
		}
		var raw []byte
		err := mustTx(c).QueryRow(c.Request.Context(), `SELECT jsonb_build_object('student',u.name,'sid',u.sid,'title',s.title,'status',s.status,'score',s.final_score) FROM submission s JOIN app_user u ON u.id=s.student_id WHERE s.id=$1`, c.Param("id")).Scan(&raw)
		if err != nil {
			return nil, err
		}
		preview["before"] = jsonRaw(raw)
	}
	if spec.Name == "class.settle" {
		status, raw := capturedMCPHandler(c, s.adminGate)
		if status >= 400 {
			return nil, errors.New("无法读取结算条件")
		}
		preview["before"] = jsonRaw(raw)
	}
	return preview, nil
}
func (s *Server) mcpPreparedResponse(c *gin.Context, id int64, status string, expires time.Time, preview []byte) {
	c.JSON(200, gin.H{"operationId": strconv.FormatInt(id, 10), "status": status, "expiresAt": expires, "preview": jsonRaw(preview), "approvalUrl": s.cfg.PublicURL + "/?v=stuAccount", "next": "账号本人在账号设置批准后，调用 operations.commit。"})
}

func (s *Server) executeMCPOperation(c *gin.Context, spec mcpToolSpec, call mcpCall) {
	key, ok := call.Arguments["idempotencyKey"].(string)
	if !ok || !mcpKeyPattern.MatchString(key) {
		writeError(c, 422, "idempotency_required", "写操作需要 8—100 位唯一幂等键", nil)
		return
	}
	tx := mustTx(c)
	args, _ := json.Marshal(call.Arguments)
	requestHash := mcpDigest(append([]byte(spec.Name+"\n"), args...))
	var id int64
	var oldTool, oldHash, status, stateHash string
	var result, preview []byte
	var resultStatus *int
	var expires time.Time
	err := tx.QueryRow(c.Request.Context(), `SELECT id,tool,request_hash,status,state_hash,result,result_status,expires_at,preview FROM agent_operation WHERE connection_id=$1 AND idempotency_key=$2 FOR UPDATE`, call.Connection.ID, key).Scan(&id, &oldTool, &oldHash, &status, &stateHash, &result, &resultStatus, &expires, &preview)
	existing := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	if existing && (oldTool != spec.Name || oldHash != requestHash) {
		writeError(c, 409, "idempotency_conflict", "同一个幂等键不能用于不同操作或内容", nil)
		return
	}
	if call.Mode == "commit" && (!existing || id != call.OperationID) {
		writeError(c, 404, "not_found", "准备结果不存在", nil)
		return
	}
	if existing && status == "applied" {
		code := 200
		if resultStatus != nil {
			code = *resultStatus
		}
		if code == 204 {
			result = nil
		}
		writeMCPJSON(c, code, result)
		return
	}
	if existing && !time.Now().Before(expires) {
		writeError(c, 409, "operation_expired", "准备结果已过期，请使用新幂等键重新准备", nil)
		return
	}
	if call.Mode == "prepare" {
		if existing {
			s.mcpPreparedResponse(c, id, status, expires, preview)
			return
		}
		state, err := s.mcpState(c, spec)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		view, err := s.mcpPreview(c, spec, call)
		if err != nil {
			if writeAdjudicationFailure(c, err) {
				return
			}
			writeError(c, 409, "preview_unavailable", "无法准备该对象，请先刷新业务资料", nil)
			return
		}
		preview, _ = json.Marshal(view)
		expires = time.Now().Add(10 * time.Minute)
		if call.Connection.Expires.Before(expires) {
			expires = call.Connection.Expires
		}
		err = tx.QueryRow(c.Request.Context(), `INSERT INTO agent_operation(class_id,connection_id,idempotency_key,tool,arguments,request_hash,state_hash,preview,status,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9) RETURNING id`, mustActor(c).ClassID, call.Connection.ID, key, spec.Name, args, requestHash, state, preview, expires).Scan(&id)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if err := appendAudit(c, tx, "mcp.operation_prepared", "agent_operation", strconv.FormatInt(id, 10), nil, nil, map[string]any{"tool": spec.Name}); err != nil {
			writeServiceError(c, err)
			return
		}
		s.mcpPreparedResponse(c, id, "pending", expires, preview)
		return
	}
	if spec.Approval {
		if call.Mode != "commit" || !existing || status != "approved" {
			writeError(c, 409, "approval_required", "请先由账号本人在网页批准这一份操作", nil)
			return
		}
		current, err := s.mcpState(c, spec)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if current != stateHash {
			writeError(c, 409, "operation_changed", "准备之后业务数据已变化，请重新准备并核对", nil)
			return
		}
	}
	if spec.RequireVersion {
		current, err := s.mcpState(c, spec)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(c, 404, "not_found", "材料不存在", nil)
			} else {
				writeServiceError(c, err)
			}
			return
		}
		if call.Arguments["expectedVersion"] != current {
			writeError(c, 409, "version_conflict", "材料已经变化，请重新读取后修改", nil)
			return
		}
	}
	if !existing {
		expires = call.Connection.Expires
		view, _ := json.Marshal(gin.H{"title": spec.Title, "targets": call.Arguments["id"]})
		err = tx.QueryRow(c.Request.Context(), `INSERT INTO agent_operation(class_id,connection_id,idempotency_key,tool,arguments,request_hash,preview,status,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,'approved',$8) RETURNING id`, mustActor(c).ClassID, call.Connection.ID, key, spec.Name, args, requestHash, view, expires).Scan(&id)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	c.Set("easygpa.mcp_operation", id)
	code, raw := capturedMCPHandler(c, spec.Handler)
	if code >= 400 || c.IsAborted() {
		writeMCPJSON(c, code, raw)
		return
	}
	if len(raw) > 4<<20 {
		writeError(c, 413, "result_too_large", "结果过大，请缩小操作范围", nil)
		return
	}
	result = raw
	if len(result) == 0 {
		result = []byte(`null`)
	}
	if !json.Valid(result) {
		writeError(c, 500, "invalid_result", "业务响应格式异常", nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE agent_operation SET status='applied',result=$2,result_status=$3 WHERE id=$1`, id, result, code); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "mcp.operation_applied", "agent_operation", strconv.FormatInt(id, 10), nil, nil, map[string]any{"tool": spec.Name}); err != nil {
		writeServiceError(c, err)
		return
	}
	writeMCPJSON(c, code, raw)
}
