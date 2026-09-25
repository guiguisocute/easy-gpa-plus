package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func (s *Server) mcpPrepareUpload(c *gin.Context) {
	call := c.Request.Context().Value(mcpCallKey{}).(mcpCall)
	body, _ := json.Marshal(call.Arguments["input"])
	var input evidenceInput
	if json.Unmarshal(body, &input) != nil || input.SizeBytes < 1 || input.SizeBytes > 64<<20 || len(input.SHA256) != 64 {
		writeError(c, 422, "invalid_file", "佐证须提供大小与 SHA-256，单文件最大 64 MB", nil)
		return
	}
	if _, err := hex.DecodeString(input.SHA256); err != nil {
		writeError(c, 422, "invalid_sha256", "SHA-256 格式不正确", nil)
		return
	}
	status, raw := capturedMCPHandler(c, s.presignSubmissionEvidence)
	if status >= 400 {
		writeMCPJSON(c, status, raw)
		return
	}
	var result struct {
		EvidenceID string `json:"evidenceId"`
	}
	if json.Unmarshal(raw, &result) != nil || result.EvidenceID == "" {
		writeError(c, 500, "invalid_upload", "无法准备佐证上传", nil)
		return
	}
	id := randomObjectPart()
	expires := time.Now().Add(15 * time.Minute)
	if call.Connection.Expires.Before(expires) {
		expires = call.Connection.Expires
	}
	_, err := mustTx(c).Exec(c.Request.Context(), `INSERT INTO agent_upload(id,class_id,connection_id,evidence_id,submission_id,sha256,size_bytes,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, mustActor(c).ClassID, call.Connection.ID, result.EvidenceID, c.Param("id"), strings.ToLower(input.SHA256), input.SizeBytes, expires)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(201, gin.H{"uploadId": id, "evidenceId": result.EvidenceID, "method": "PUT", "uploadUrl": s.mcpURL() + "/uploads/" + id, "expiresAt": expires, "authorization": "使用当前连接的 Authorization: Bearer 密钥，发送原始文件字节；然后调用 evidence.complete_upload。"})
}

type mcpUploadRecord struct {
	EvidenceID, SubmissionID, Size int64
	Hash, Status, Key, MediaType   string
	Expires                        time.Time
}

func loadMCPUpload(c *gin.Context) (mcpUploadRecord, error) {
	conn := c.Request.Context().Value(mcpCallKey{}).(mcpCall).Connection
	var row mcpUploadRecord
	err := mustTx(c).QueryRow(c.Request.Context(), `SELECT au.evidence_id,au.submission_id,au.size_bytes,au.sha256,au.status,au.expires_at,e.object_key,e.media_type FROM agent_upload au JOIN evidence e ON e.id=au.evidence_id AND e.class_id=au.class_id WHERE au.id=$1 AND au.connection_id=$2 FOR UPDATE OF au,e`, c.Param("upload"), conn.ID).Scan(&row.EvidenceID, &row.SubmissionID, &row.Size, &row.Hash, &row.Status, &row.Expires, &row.Key, &row.MediaType)
	return row, err
}
func (s *Server) mcpUpload(c *gin.Context) {
	row, err := loadMCPUpload(c)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, 404, "not_found", "上传许可不存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !time.Now().Before(row.Expires) {
		writeError(c, 410, "upload_expired", "上传许可已过期", nil)
		return
	}
	if row.Status != "pending" {
		c.JSON(200, gin.H{"status": row.Status, "replayed": true})
		return
	}
	item, err := loadOwnedSubmission(c.Request.Context(), mustTx(c), row.SubmissionID, mustActor(c).UserID, true)
	if err != nil {
		writeError(c, 404, "not_found", "材料不存在", nil)
		return
	}
	if item.Status != "draft" && item.Status != "pending" {
		writeError(c, 409, "not_editable", "材料状态已变化，不能上传", nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), mustTx(c), mustActor(c).UserID); err != nil {
		writeError(c, 409, "sealed", err.Error(), nil)
		return
	}
	current, err := loadCurrentScheme(c.Request.Context(), mustTx(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := ensureCapability(current.Config, "submit", time.Now()); err != nil {
		writeError(c, 409, "upload_closed", err.Error(), nil)
		return
	}
	if c.Request.ContentLength >= 0 && c.Request.ContentLength != row.Size {
		writeError(c, 422, "size_mismatch", "文件大小与上传许可不符", nil)
		return
	}
	file, err := os.CreateTemp("", "easygpa-mcp-upload-*")
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(c.Request.Body, row.Size+1))
	if err != nil || n != row.Size || hex.EncodeToString(digest.Sum(nil)) != row.Hash {
		writeError(c, 422, "integrity_mismatch", "文件大小或 SHA-256 不匹配，请重新传送原文件", nil)
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.deps.Objects.Put(c.Request.Context(), row.Key, row.MediaType, file, row.Size); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := mustTx(c).Exec(c.Request.Context(), `UPDATE agent_upload SET status='uploaded' WHERE id=$1`, c.Param("upload")); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, mustTx(c), "mcp.file_uploaded", "evidence", strconv.FormatInt(row.EvidenceID, 10), nil, nil, map[string]any{"sizeBytes": row.Size}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(200, gin.H{"status": "uploaded", "evidenceId": strconv.FormatInt(row.EvidenceID, 10)})
}
func (s *Server) mcpCompleteUpload(c *gin.Context) {
	row, err := loadMCPUpload(c)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, 404, "not_found", "上传许可不存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !time.Now().Before(row.Expires) {
		writeError(c, 410, "upload_expired", "上传许可已过期", nil)
		return
	}
	if row.Status == "completed" {
		c.JSON(200, gin.H{"status": "ready", "evidenceId": strconv.FormatInt(row.EvidenceID, 10)})
		return
	}
	if row.Status != "uploaded" {
		writeError(c, 409, "upload_required", "请先把文件字节传到上传地址", nil)
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "id", Value: strconv.FormatInt(row.SubmissionID, 10)}, gin.Param{Key: "eid", Value: strconv.FormatInt(row.EvidenceID, 10)})
	status, raw := capturedMCPHandler(c, s.completeSubmissionEvidence)
	if status >= 400 {
		writeMCPJSON(c, status, raw)
		return
	}
	if _, err := mustTx(c).Exec(c.Request.Context(), `UPDATE agent_upload SET status='completed' WHERE id=$1`, c.Param("upload")); err != nil {
		writeServiceError(c, err)
		return
	}
	writeMCPJSON(c, status, raw)
}

func (s *Server) mcpFileLink(c *gin.Context, kind, id, key string, handler gin.HandlerFunc) {
	status, raw := capturedMCPHandler(c, handler)
	if status >= 400 {
		writeMCPJSON(c, status, raw)
		return
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		writeError(c, 500, "invalid_result", "文件状态异常", nil)
		return
	}
	if _, ok := data[key]; ok {
		data[key] = s.mcpURL() + "/files/" + kind + "/" + id
		data["authorization"] = "使用当前连接的 Authorization: Bearer 临时密钥。每次下载重新鉴权。"
		delete(data, "expiresIn")
		delete(data, "downloadExpiresIn")
	}
	c.JSON(status, data)
}
func (s *Server) mcpEvidenceLink(c *gin.Context) {
	s.mcpFileLink(c, "evidence", c.Param("eid"), "url", s.evidenceURL)
}
func (s *Server) mcpResourceLink(c *gin.Context) {
	s.mcpFileLink(c, "resource", c.Param("id"), "url", s.classResourceURL)
}
func (s *Server) mcpExportStatus(c *gin.Context) {
	s.mcpFileLink(c, "export", c.Param("job"), "downloadUrl", s.exportJob)
}

func (s *Server) mcpDownload(c *gin.Context) {
	kind, id := c.Param("kind"), c.Param("id")
	var handler gin.HandlerFunc
	var query string
	switch kind {
	case "evidence":
		handler = s.evidenceURL
		c.Params = append(c.Params, gin.Param{Key: "eid", Value: id})
		query = `SELECT object_key,filename FROM evidence WHERE id=$1`
	case "resource":
		handler = s.classResourceURL
		query = `SELECT b.object_key,d.filename FROM knowledge_document d JOIN knowledge_blob b ON b.id=d.blob_id WHERE d.id=$1`
	case "export":
		handler = s.exportJob
		c.Params = append(c.Params, gin.Param{Key: "job", Value: id})
		query = `SELECT object_key,'easygpa-'||kind||CASE WHEN kind IN ('archive','college') THEN '.zip' ELSE '.xlsx' END FROM export_job WHERE id=$1::uuid AND status='complete'`
	default:
		writeError(c, 404, "not_found", "文件不存在", nil)
		return
	}
	status, raw := capturedMCPHandler(c, handler)
	if status >= 400 {
		writeMCPJSON(c, status, raw)
		return
	}
	var key, filename string
	err := mustTx(c).QueryRow(c.Request.Context(), query, id).Scan(&key, &filename)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, 409, "not_ready", "文件尚未就绪", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	info, err := s.deps.Objects.Stat(c.Request.Context(), key)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if info.Size > 64<<20 {
		writeError(c, 413, "file_too_large", "MCP 单文件下载上限为 64 MB，大型归档请在网页下载", nil)
		return
	}
	reader, err := s.deps.Objects.Open(c.Request.Context(), key)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer reader.Close()
	c.Header("Content-Type", info.ContentType)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": safeFilename(filename)}))
	c.Header("Cache-Control", "no-store")
	n, err := io.Copy(c.Writer, io.LimitReader(reader, info.Size+1))
	if err != nil || n != info.Size {
		c.Abort()
		c.Writer.(*bufferedWriter).body.Reset()
		c.Writer.(*bufferedWriter).size = 0
		c.Writer.(*bufferedWriter).status = 200
		writeError(c, 502, "file_read_failed", "读取文件失败，请重试", nil)
		return
	}
}
