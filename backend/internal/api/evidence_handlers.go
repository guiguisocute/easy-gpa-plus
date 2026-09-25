package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/objectstore"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
)

type evidenceInput struct {
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256,omitempty"`
}

func (s *Server) presignSubmissionEvidence(c *gin.Context) {
	submissionID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var input evidenceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "文件参数不正确", nil)
		return
	}
	input.Filename = safeFilename(input.Filename)
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	if input.Filename == "" || input.SizeBytes <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_file", "文件名和大小不正确", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	item, err := loadOwnedSubmission(c.Request.Context(), tx, submissionID, actor.UserID, true)
	if notFound(c, err, "提交条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if item.Status != "draft" && item.Status != "pending" {
		writeError(c, http.StatusConflict, "not_editable", "该条目当前不能添加佐证", nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	var snapshot ruleSnapshot
	if err := json.Unmarshal(item.RuleSnapshot, &snapshot); err != nil {
		writeServiceError(c, err)
		return
	}
	flags := s.runtimeFlags(c.Request.Context())
	if err := validateEvidencePolicy(snapshot.Item.Evidence, input, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "evidence_invalid", err.Error(), nil)
		return
	}
	if err := reserveEvidenceQuota(c.Request.Context(), tx, actor.UserID, input.SizeBytes, flags.EvidenceDailyMB); err != nil {
		if errors.Is(err, errEvidenceDailyQuotaExceeded) {
			writeError(c, http.StatusTooManyRequests, "evidence_daily_quota", "今天创建的佐证与备注附件已达到平台字节上限", nil)
		} else {
			writeServiceError(c, err)
		}
		return
	}
	objectKey := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/submission-" + strconv.FormatInt(submissionID, 10) + "/" + randomObjectPart() + objectKeySuffix(input.Filename)
	upload, err := s.deps.Objects.PresignUpload(c.Request.Context(), objectKey, input.MediaType, input.SizeBytes, 15*time.Minute)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var evidenceID int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO evidence (class_id,submission_id,kind,object_key,filename,media_type,size_bytes,sha256,status,created_by)
		VALUES ($1,$2,'claim',$3,$4,$5,$6,NULLIF($7,''),'pending',$8) RETURNING id
	`, actor.ClassID, submissionID, objectKey, input.Filename, input.MediaType, input.SizeBytes, input.SHA256, actor.UserID).Scan(&evidenceID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "evidence.presigned", "evidence", strconv.FormatInt(evidenceID, 10), nil, map[string]any{"submissionId": submissionID, "filename": input.Filename, "sizeBytes": input.SizeBytes}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	response := uploadPolicyJSON(upload)
	response["evidenceId"] = strconv.FormatInt(evidenceID, 10)
	response["completeUrl"] = "/api/v1/submissions/" + strconv.FormatInt(submissionID, 10) + "/evidence/" + strconv.FormatInt(evidenceID, 10) + "/complete"
	c.JSON(http.StatusCreated, response)
}

func uploadPolicyJSON(upload objectstore.UploadPolicy) gin.H {
	return gin.H{"uploadUrl": upload.URL.String(), "uploadFields": upload.Fields, "expiresIn": 900, "method": "POST"}
}

func validateEvidence(rule *scheme.EvidenceRule, input evidenceInput, globalMaxMB int64) error {
	return validateEvidencePolicy(rule, input, globalMaxMB, opsconfig.DefaultEvidenceAllowedFormats())
}

func validateEvidencePolicy(rule *scheme.EvidenceRule, input evidenceInput, globalMaxMB int64, platformFormats []string) error {
	if globalMaxMB <= 0 {
		globalMaxMB = scheme.DefaultEvidenceMaxMB
	}
	maxMB := globalMaxMB
	allowed := scheme.EffectiveEvidenceTypes(rule)
	if rule != nil {
		if rule.MaxMB < maxMB {
			maxMB = rule.MaxMB
		}
	}
	if input.SizeBytes > maxMB*1024*1024 {
		return fmt.Errorf("文件不能超过 %d MB", maxMB)
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(input.Filename)), ".")
	if !evidenceFormatAllowed(platformFormats, ext) {
		return fmt.Errorf("文件类型 .%s 已被平台禁用", ext)
	}
	if !evidenceFormatAllowed(allowed, ext) {
		return fmt.Errorf("文件类型 .%s 不在允许列表中", ext)
	}
	if input.MediaType == "" {
		return errors.New("mediaType 不能为空")
	}
	if !evidenceMediaTypeMatches(ext, input.MediaType) {
		return errors.New("文件扩展名与 mediaType 不一致")
	}
	if input.SHA256 != "" {
		decoded, err := hex.DecodeString(input.SHA256)
		if err != nil || len(decoded) != 32 {
			return errors.New("sha256 必须是 64 位十六进制字符串")
		}
	}
	return nil
}

var evidenceMediaTypes = map[string][]string{
	"pdf":  {"application/pdf"},
	"jpg":  {"image/jpeg"},
	"jpeg": {"image/jpeg"},
	"png":  {"image/png"},
	"gif":  {"image/gif"},
	"webp": {"image/webp"},
	"heic": {"image/heic", "image/heif", "application/octet-stream"},
	"doc":  {"application/msword", "application/octet-stream"},
	"docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/octet-stream"},
	"xls":  {"application/vnd.ms-excel", "application/octet-stream"},
	"xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/octet-stream"},
	"ppt":  {"application/vnd.ms-powerpoint", "application/octet-stream"},
	"pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", "application/octet-stream"},
	"txt":  {"text/plain", "application/octet-stream"},
	"csv":  {"text/csv", "application/vnd.ms-excel", "text/plain", "application/octet-stream"},
	"wps":  {"application/vnd.ms-works", "application/octet-stream"},
	"et":   {"application/octet-stream"},
	"dps":  {"application/octet-stream"},
	"zip":  {"application/zip", "application/x-zip-compressed", "application/octet-stream"},
	"rar":  {"application/vnd.rar", "application/x-rar-compressed", "application/octet-stream"},
	"7z":   {"application/x-7z-compressed", "application/octet-stream"},
	"mp4":  {"video/mp4", "application/mp4", "application/octet-stream"},
}

func evidenceMediaTypeMatches(ext, mediaType string) bool {
	wanted, known := evidenceMediaTypes[ext]
	if !known {
		return false
	}
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	return slices.Contains(wanted, mediaType)
}

// evidenceFormatAllowed 判断扩展名在不在一份允许列表里。列表可能写成 ".PNG"
// 这种形式，而 .jpg 和 .jpeg 说的是同一种东西。
func evidenceFormatAllowed(formats []string, ext string) bool {
	for _, value := range formats {
		value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
		if value == ext || (value == "jpg" && ext == "jpeg") || (value == "jpeg" && ext == "jpg") {
			return true
		}
	}
	return false
}

// evidenceContent 是只看文件头认出来的真实格式：一个说给人听的名字，加上这种
// 内容可以挂哪些扩展名。exts 只有一项时才谈得上"照真实格式收下"——OLE 和 ZIP
// 一族里 .doc 和 .xls 的文件头一模一样，替学生猜是哪一种只会猜错。
type evidenceContent struct {
	label string
	exts  []string
}

// 手机拍的照片和录的视频用的是同一种盒式容器，都以 ftyp 开头，靠 brand 区分。
var evidenceHeifBrands = []string{"heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1"}

func sniffEvidenceContent(header []byte) (evidenceContent, bool) {
	prefix := func(magic string) bool { return bytes.HasPrefix(header, []byte(magic)) }
	switch {
	case prefix("%PDF-"):
		return evidenceContent{"PDF 文件", []string{"pdf"}}, true
	case prefix("\xff\xd8\xff"):
		return evidenceContent{"JPEG 图片", []string{"jpg", "jpeg"}}, true
	case prefix("\x89PNG\r\n\x1a\n"):
		return evidenceContent{"PNG 图片", []string{"png"}}, true
	case prefix("GIF87a"), prefix("GIF89a"):
		return evidenceContent{"GIF 图片", []string{"gif"}}, true
	// BMP 的魔数只有两个字节，光看它会把"BMW 车展志愿证明"这种 txt 也认成图片。
	// 后面那四个保留字节在真实的 BMP 里恒为 0，而纯文本里不会出现 0。
	case prefix("BM") && len(header) >= 14 && bytes.Equal(header[6:10], []byte{0, 0, 0, 0}):
		return evidenceContent{"BMP 图片", []string{"bmp"}}, true
	case prefix("II*\x00"), prefix("MM\x00*"):
		return evidenceContent{"TIFF 图片", []string{"tif", "tiff"}}, true
	case prefix("{\\rtf"):
		return evidenceContent{"RTF 文档", []string{"doc"}}, true
	case prefix("Rar!\x1a\x07"):
		return evidenceContent{"RAR 压缩包", []string{"rar"}}, true
	case prefix("7z\xbc\xaf\x27\x1c"):
		return evidenceContent{"7z 压缩包", []string{"7z"}}, true
	case prefix("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"):
		return evidenceContent{"Office 97-2003 文档", []string{"doc", "xls", "ppt", "wps", "et", "dps"}}, true
	case prefix("PK\x03\x04"), prefix("PK\x05\x06"):
		return evidenceContent{"ZIP 压缩包或新版 Office 文档", []string{"docx", "xlsx", "pptx", "zip", "wps", "et", "dps"}}, true
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		return evidenceContent{"WebP 图片", []string{"webp"}}, true
	case len(header) >= 12 && string(header[4:8]) == "ftyp":
		brand := string(header[8:12])
		switch {
		case slices.Contains(evidenceHeifBrands, brand):
			return evidenceContent{"HEIC 图片", []string{"heic"}}, true
		case brand == "avif", brand == "avis":
			return evidenceContent{"AVIF 图片", []string{"avif"}}, true
		default:
			return evidenceContent{"MP4 视频", []string{"mp4"}}, true
		}
	}
	return evidenceContent{}, false
}

// 文本文件没有文件头可认，只能反过来排除二进制。这里不再要求 UTF-8：学生手里的
// txt 和 csv 常常是 GBK，Excel 另存的「Unicode 文本」是带 BOM 的 UTF-16——
// 按 UTF-8 校验会把这两种都误判成"内容不对"。
func evidenceLooksLikeText(header []byte) bool {
	if bytes.HasPrefix(header, []byte{0xff, 0xfe}) || bytes.HasPrefix(header, []byte{0xfe, 0xff}) {
		return true
	}
	return !bytes.Contains(header, []byte{0})
}

// 只换扩展名，学生起的名字照原样留着——他们是按那个名字找文件的。
func replaceEvidenceExtension(filename, ext string) string {
	return strings.TrimSuffix(filename, path.Ext(filename)) + "." + ext
}

// resolveEvidenceContent 决定这份上传最终按什么格式落库。
//
// 扩展名和内容对不上是最常见的上传失败原因：微信、QQ 传过来的图片经常被重新
// 编码成 WebP 或 HEIC，文件名却还留着 .jpg。这种时候按真实格式收下，比让学生
// 自己去琢磨"文件内容与 .jpg 格式不一致"有用得多。小项自己的格式要求不在这里
// 再判一次：学生申报的扩展名在预签名那步已经过了那道关，手机用什么编码存照片
// 不是他们能看见、能选的事。
//
// 这里不需要黑名单。浏览器会直接打开的只有 PDF 和图片（见 inlineRenderable），
// 而这两类都必须对上文件头才收，网页和脚本没有伪装成它们的余地；txt 和 csv 走
// 的是附件下载，存进去也不会被当成网页执行。
func resolveEvidenceContent(filename, mediaType string, header []byte, platformFormats []string) (evidenceUploadResult, error) {
	declared := strings.TrimPrefix(strings.ToLower(path.Ext(filename)), ".")
	asDeclared := evidenceUploadResult{Filename: filename, MediaType: mediaType}
	content, known := sniffEvidenceContent(header)
	if !known {
		if (declared == "txt" || declared == "csv") && evidenceLooksLikeText(header) {
			return asDeclared, nil
		}
		return evidenceUploadResult{}, errors.New("认不出这份文件是什么格式，它可能损坏了或者没传完；请用能打开它的程序另存一份再传")
	}
	if slices.Contains(content.exts, declared) {
		return asDeclared, nil
	}
	if len(content.exts) > 1 {
		return evidenceUploadResult{}, fmt.Errorf("这份文件其实是%s，扩展名却写着 .%s；请把扩展名改成 .%s 里的一个再传",
			content.label, declared, strings.Join(content.exts, "、."))
	}
	corrected := content.exts[0]
	media, listed := evidenceMediaTypes[corrected]
	if !listed || !evidenceFormatAllowed(platformFormats, corrected) {
		return evidenceUploadResult{}, fmt.Errorf("这份文件其实是%s，平台不收这种格式；请用系统的「照片」或「画图」打开后另存为 JPG 或 PNG 再传", content.label)
	}
	return evidenceUploadResult{
		Filename:  replaceEvidenceExtension(filename, corrected),
		MediaType: media[0],
		Corrected: true,
	}, nil
}

func (s *Server) readEvidenceHeader(ctx context.Context, objectKey string) ([]byte, error) {
	reader, err := s.deps.Objects.Open(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 512)
	n, readErr := io.ReadFull(reader, header)
	if errors.Is(readErr, io.ErrUnexpectedEOF) || errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	return header[:n], nil
}

// evidenceUploadResult 是收尾检查过后真正落库的文件名与媒体类型。内容跟扩展名
// 对不上、但真实格式平台仍然收的时候，Corrected 为真，这两个字段是纠正后的值。
type evidenceUploadResult struct {
	Filename  string
	MediaType string
	ETag      string
	Corrected bool
}

// settleEvidenceUpload 是三条上传通道（申报佐证、申诉佐证、正文附件）共用的收尾：
// 对象在不在、字节数对不对、内容到底是什么格式，都对上了才把记录置为 ready。
// 检查没过时它已经写好了响应，调用方直接 return 即可。
func (s *Server) settleEvidenceUpload(c *gin.Context, evidenceID int64, objectKey, filename, mediaType string, declaredSize int64) (evidenceUploadResult, bool) {
	ctx := c.Request.Context()
	tx := mustTx(c)
	reject := func(code, message string, detail any) (evidenceUploadResult, bool) {
		// 这条 UPDATE 只在事务被提交时才算数，而 4xx 会让中间件回滚（middleware.go:464），
		// 所以记录实际停在 pending，由调用方或前端把它删掉。留着它是为了万一将来
		// 这条路径改成 2xx 返回，状态不会静悄悄地不对。
		_, _ = tx.Exec(ctx, `UPDATE evidence SET status='rejected' WHERE id=$1`, evidenceID)
		s.discardInvalidUpload(c, objectKey)
		writeError(c, http.StatusUnprocessableEntity, code, message, detail)
		return evidenceUploadResult{}, false
	}
	info, err := s.deps.Objects.Stat(ctx, objectKey)
	if err != nil {
		writeError(c, http.StatusConflict, "upload_incomplete", "对象尚未上传完成", nil)
		return evidenceUploadResult{}, false
	}
	if info.Size != declaredSize {
		return reject("size_mismatch", "实际文件大小与声明不一致", gin.H{"declared": declaredSize, "actual": info.Size})
	}
	if info.ContentType != "" && info.ContentType != "application/octet-stream" && !strings.EqualFold(info.ContentType, mediaType) {
		return reject("media_type_mismatch", "对象存储中的文件类型与声明不一致", nil)
	}
	header, err := s.readEvidenceHeader(ctx, objectKey)
	if err != nil {
		writeServiceError(c, err)
		return evidenceUploadResult{}, false
	}
	result, err := resolveEvidenceContent(filename, mediaType, header, s.runtimeFlags(ctx).EvidenceAllowedFormats)
	if err != nil {
		// 这一路只回 422，而 422 不进请求日志。上传被内容检查挡掉是要能查的事，
		// 单独记一条，免得下次只能靠审计表倒推。
		slog.Warn("evidence content rejected", "evidence_id", evidenceID, "filename", filename, "reason", err)
		return reject("content_mismatch", err.Error(), nil)
	}
	if result.Corrected {
		// 这条是判断"放宽之后到底救回了多少份"的唯一依据，别删。
		slog.Info("evidence stored by sniffed format", "evidence_id", evidenceID, "declared", filename, "stored", result.Filename)
	}
	result.ETag = strings.Trim(info.ETag, `"`)
	if _, err := tx.Exec(ctx, `UPDATE evidence SET status='ready',filename=$2,media_type=$3,object_etag=NULLIF($4,'') WHERE id=$1`,
		evidenceID, result.Filename, result.MediaType, result.ETag); err != nil {
		writeServiceError(c, err)
		return evidenceUploadResult{}, false
	}
	return result, true
}

func (s *Server) completeSubmissionEvidence(c *gin.Context) {
	submissionID, ok := pathID(c, "id")
	if !ok {
		return
	}
	evidenceID, ok := pathID(c, "eid")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey, filename, mediaType, status string
	var declaredSize int64
	err := tx.QueryRow(c.Request.Context(), `
		SELECT e.object_key,e.filename,e.media_type,e.size_bytes,e.status
		  FROM evidence e JOIN submission s ON s.id=e.submission_id
		 WHERE e.id=$1 AND e.submission_id=$2 AND e.kind='claim' AND s.student_id=$3
		 FOR UPDATE OF e
	`, evidenceID, submissionID, actor.UserID).Scan(&objectKey, &filename, &mediaType, &declaredSize, &status)
	if notFound(c, err, "佐证文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if status == "ready" {
		// 重复调用 complete 要和第一次返回同一副形状：调用方拿这里的文件名去显示，
		// 而库里存的可能已经是按真实格式纠正过的名字了。
		c.JSON(http.StatusOK, gin.H{"evidenceId": strconv.FormatInt(evidenceID, 10), "status": "ready", "filename": filename, "mediaType": mediaType})
		return
	}
	result, ok := s.settleEvidenceUpload(c, evidenceID, objectKey, filename, mediaType, declaredSize)
	if !ok {
		return
	}
	if err := appendAudit(c, tx, "evidence.completed", "evidence", strconv.FormatInt(evidenceID, 10), map[string]any{"status": status}, map[string]any{"status": "ready", "etag": result.ETag, "filename": result.Filename}, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	s.scheduleStorageReconcile(c, actor.ClassID)
	c.JSON(http.StatusOK, gin.H{"evidenceId": strconv.FormatInt(evidenceID, 10), "status": "ready", "filename": result.Filename, "mediaType": result.MediaType})
}

func (s *Server) deleteSubmissionEvidence(c *gin.Context) {
	submissionID, ok := pathID(c, "id")
	if !ok {
		return
	}
	evidenceID, ok := pathID(c, "eid")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey, filename, submissionStatus string
	err := tx.QueryRow(c.Request.Context(), `
		SELECT e.object_key,e.filename,s.status
		  FROM evidence e JOIN submission s ON s.id=e.submission_id
		 WHERE e.id=$1 AND e.submission_id=$2 AND e.kind='claim' AND s.student_id=$3
		 FOR UPDATE OF e,s
	`, evidenceID, submissionID, actor.UserID).Scan(&objectKey, &filename, &submissionStatus)
	if notFound(c, err, "佐证文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if submissionStatus != "draft" && submissionStatus != "pending" {
		writeError(c, http.StatusConflict, "not_deletable", "该条目已有审核进展，不能删除佐证", nil)
		return
	}
	if err := ensureNotSealed(c.Request.Context(), tx, actor.UserID); err != nil {
		writeError(c, http.StatusConflict, "sealed", err.Error(), nil)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM evidence WHERE id=$1`, evidenceID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "evidence.deleted", "evidence", strconv.FormatInt(evidenceID, 10), map[string]any{"filename": filename, "submissionId": submissionID}, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	// Keep the database transaction authoritative. An object-store failure after
	// commit is recoverable by the maintenance cleanup job; deleting first would
	// leave a committed evidence row pointing at a missing object if the DB later
	// rejects the transaction.
	s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	c.Status(http.StatusNoContent)
}

func (s *Server) evidenceURL(c *gin.Context) {
	evidenceID, ok := pathID(c, "eid")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	var objectKey, filename, mediaType, status, kind string
	// 举报附件不记上传人，所以这一列会是空的（000042）。
	var createdBy *int64
	var submissionID, appealID, objectionID, reportID *int64
	var blindAssignmentID, reviewReportID *int64
	var submissionStudent, appealStudent, appealHandler, appealFiler *int64
	var objectionStudent, objectionProposer *int64
	var reportStudent *int64
	var submittedMatter bool
	err := tx.QueryRow(c.Request.Context(), `
		SELECT e.object_key,e.filename,e.media_type,e.status,e.kind,e.created_by,
		       e.submission_id,e.appeal_id,e.objection_id,e.report_id,e.blind_assignment_id,e.review_report_id,
		       s.student_id,a.student_id,a.handler_id,a.filed_by,o.student_id,o.proposer_id,r.student_id,
		       COALESCE(s.status,a.status,o.status,r.status,'draft')<>'draft'
		         AND COALESCE(s.status IN ('arbitrating','scored','locked','appealing'),true)
		  FROM evidence e
		  LEFT JOIN submission s ON s.id=e.submission_id
		  LEFT JOIN appeal a ON a.id=e.appeal_id
		  LEFT JOIN objection o ON o.id=e.objection_id
		  LEFT JOIN report r ON r.id=e.report_id
		 WHERE e.id=$1
	`, evidenceID).Scan(&objectKey, &filename, &mediaType, &status, &kind, &createdBy,
		&submissionID, &appealID, &objectionID, &reportID, &blindAssignmentID, &reviewReportID, &submissionStudent, &appealStudent,
		&appealHandler, &appealFiler, &objectionStudent, &objectionProposer, &reportStudent, &submittedMatter)
	if notFound(c, err, "佐证文件") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	deputyReader := false
	if submittedMatter && actor.Role == "group" && actor.IsDeputy {
		for _, studentID := range []*int64{submissionStudent, appealStudent, objectionStudent, reportStudent} {
			if studentID == nil {
				continue
			}
			deputyReader, err = deputyCanReadTarget(c.Request.Context(), tx, actor, *studentID)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			break
		}
	}
	allowed := actor.Role == "class_admin" || deputyReader
	blindAuditor := false
	if blindAssignmentID != nil || reviewReportID != nil {
		if blindAssignmentID != nil {
			allowed, err = canReadBlindNote(c.Request.Context(), tx, actor, *blindAssignmentID)
		} else {
			allowed, err = canReadReportReviewNote(c.Request.Context(), tx, actor, *reviewReportID, createdBy)
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
	} else if submissionID != nil {
		allowed = allowed || (submissionStudent != nil && *submissionStudent == actor.UserID)
		if !allowed && actor.Role == "group" {
			if err := tx.QueryRow(c.Request.Context(), `
				SELECT EXISTS (
				  SELECT 1 FROM submission_reviewer sr
				   WHERE sr.submission_id=$2 AND sr.reviewer_id=$1
				  UNION ALL
				  SELECT 1 FROM review r
				   WHERE r.submission_id=$2 AND r.reviewer_id=$1
				  UNION ALL
				  SELECT 1 FROM appeal_reviewer ar JOIN appeal a ON a.id=ar.appeal_id
				   WHERE ar.reviewer_id=$1 AND a.target_type='submission' AND a.target_id=$2
				  UNION ALL
				  SELECT 1
				    FROM scorecard_audit_assignment audit_assignment
				    JOIN scorecard_audit_subject audit_subject ON audit_subject.id=audit_assignment.subject_id
				    JOIN scorecard_audit_batch audit_batch ON audit_batch.id=audit_subject.batch_id
				    JOIN submission audited_submission ON audited_submission.student_id=audit_subject.student_id
				   WHERE audit_assignment.reviewer_id=$1 AND audited_submission.id=$2
				     AND audit_assignment.status<>'superseded' AND audit_batch.status<>'stale'
				)
			`, actor.UserID, *submissionID).Scan(&allowed); err != nil {
				writeServiceError(c, err)
				return
			}
			if !allowed {
				// 「扣分与异议」上，小组要对任何人的任何一条已定分条目提异议，
				// 而判断"这条给得对不对"靠的就是学生当初交的材料。所以条目一旦定分，
				// 它的申报佐证对整个小组开放。定分之前仍然只有派到的人看得见——
				// 那一段是背靠背的，谁都能翻材料就不叫背靠背了。
				// 注意这只放开 claim，note 是复核人自己的备注，下面那段会再判一次。
				if err := tx.QueryRow(c.Request.Context(), `
					SELECT status IN ('scored','locked') FROM submission WHERE id=$1
				`, *submissionID).Scan(&allowed); err != nil {
					writeServiceError(c, err)
					return
				}
			}
			if allowed {
				if err := tx.QueryRow(c.Request.Context(), `
					SELECT EXISTS (
					  SELECT 1
					    FROM scorecard_audit_assignment audit_assignment
					    JOIN scorecard_audit_subject audit_subject ON audit_subject.id=audit_assignment.subject_id
					    JOIN scorecard_audit_batch audit_batch ON audit_batch.id=audit_subject.batch_id
					    JOIN submission audited_submission ON audited_submission.student_id=audit_subject.student_id
					   WHERE audit_assignment.reviewer_id=$1 AND audited_submission.id=$2
					     AND audit_assignment.status<>'superseded' AND audit_batch.status<>'stale'
					)
				`, actor.UserID, *submissionID).Scan(&blindAuditor); err != nil {
					writeServiceError(c, err)
					return
				}
			}
		}
	} else if appealID != nil {
		allowed = allowed || (appealStudent != nil && *appealStudent == actor.UserID) ||
			(appealHandler != nil && *appealHandler == actor.UserID) ||
			(appealFiler != nil && *appealFiler == actor.UserID)
		if actor.Role == "group" {
			// 终审快照包含已结束申诉的原件、复评与终裁正文，附件遵循同一读取范围。
			if err := tx.QueryRow(c.Request.Context(), `
				SELECT EXISTS (
				  SELECT 1 FROM appeal a
				  JOIN scorecard_audit_subject subject ON subject.student_id=a.student_id
				  JOIN scorecard_audit_batch batch ON batch.id=subject.batch_id
				  JOIN scorecard_audit_assignment assignment ON assignment.subject_id=subject.id
				  WHERE a.id=$1 AND a.kind='student_appeal' AND a.resolved_at IS NOT NULL
				    AND assignment.reviewer_id=$2 AND assignment.status<>'superseded' AND batch.status<>'stale'
				)`, *appealID, actor.UserID).Scan(&blindAuditor); err != nil {
				writeServiceError(c, err)
				return
			}
			allowed = allowed || blindAuditor
		}
		if !allowed && actor.Role == "group" {
			if err := tx.QueryRow(c.Request.Context(), `
				SELECT EXISTS (SELECT 1 FROM appeal_reviewer WHERE appeal_id=$1 AND reviewer_id=$2)
			`, *appealID, actor.UserID).Scan(&allowed); err != nil {
				writeServiceError(c, err)
				return
			}
		}
	} else if objectionID != nil {
		// Objection bodies are visible only to their proposer and the class
		// administrator, so embedded note files follow that same boundary.
		allowed = allowed || (objectionProposer != nil && *objectionProposer == actor.UserID)
	} else if reportID != nil {
		// 举报附件只发给判这条举报的人：两名复核人，和终裁的班级管理员。
		// 举报人自己也拿不到下载地址——不是防他，是因为发一次就要在审计里记一笔
		// 「某某取了这份文件的地址」，而这份文件挂在他报的那条举报上，
		// 班管翻一下审计就把人对出来了。他要确认传没传上，看文件名那一行就够了。
		if !allowed && actor.Role == "group" {
			if err := tx.QueryRow(c.Request.Context(), `
				SELECT EXISTS (SELECT 1 FROM report_reviewer WHERE report_id=$1 AND reviewer_id=$2)
			`, *reportID, actor.UserID).Scan(&allowed); err != nil {
				writeServiceError(c, err)
				return
			}
		}
	}
	// 举报附件不参与这一段：它没有上传人，groupCanReadNoteEvidence 那套
	// 「是不是我自己写的备注」的判断对它没有意义，上面已经判完了。
	if allowed && kind == "note" && actor.Role == "group" && !deputyReader && !blindAuditor && reportID == nil && blindAssignmentID == nil && reviewReportID == nil {
		owner := noteOwnerObjection
		var submissionAppealReviewer, appealAllDecided, appealCreatorReviewer bool
		if submissionID != nil {
			owner = noteOwnerSubmission
			if err := tx.QueryRow(c.Request.Context(), `
				SELECT EXISTS (
				  SELECT 1 FROM appeal_reviewer ar JOIN appeal a ON a.id=ar.appeal_id
				   WHERE ar.reviewer_id=$1 AND a.target_type='submission' AND a.target_id=$2
				)
			`, actor.UserID, *submissionID).Scan(&submissionAppealReviewer); err != nil {
				writeServiceError(c, err)
				return
			}
		} else if appealID != nil {
			owner = noteOwnerAppeal
			if err := tx.QueryRow(c.Request.Context(), `
				SELECT COALESCE(bool_and(decided_at IS NOT NULL),false),
				       COALESCE(bool_or(reviewer_id=$2),false)
				  FROM appeal_reviewer WHERE appeal_id=$1
			`, *appealID, createdBy).Scan(&appealAllDecided, &appealCreatorReviewer); err != nil {
				writeServiceError(c, err)
				return
			}
		}
		own := createdBy != nil && *createdBy == actor.UserID
		allowed = groupCanReadNoteEvidence(owner, own, submissionAppealReviewer, appealAllDecided, appealCreatorReviewer)
		if objectionID != nil && !own {
			// 提出人已在上层确认；终裁落定后才能读取班管理由里的附件。
			if err := tx.QueryRow(c.Request.Context(), `SELECT status IN ('applied','adjusted','dismissed') FROM objection WHERE id=$1`, *objectionID).Scan(&allowed); err != nil {
				writeServiceError(c, err)
				return
			}
		}
	}
	if actor.Role == "group" && !allowed && blindAssignmentID == nil && reviewReportID == nil && reportID == nil {
		allowed, err = groupScoreHistoryEvidence(c.Request.Context(), tx, evidenceID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	// Only the same published-file projection used by the score history may
	// be shared with ordinary classmates. Drafts and unrevealed peer notes
	// remain private, and each download rechecks the current class window.
	if !allowed {
		allowed, err = governanceEvidenceAllowed(c.Request.Context(), tx, actor, evidenceID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	ttl := 10 * time.Minute
	publicityReader := false
	if !allowed && actor.Role == "student" {
		current, loadErr := loadCurrentScheme(c.Request.Context(), tx)
		if loadErr != nil {
			writeServiceError(c, loadErr)
			return
		}
		if current.Config.Window.PublicityOpen(time.Now()) {
			allowed, err = groupScoreHistoryEvidence(c.Request.Context(), tx, evidenceID)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			if allowed {
				publicityReader = true
				ttl = min(ttl, time.Until(current.Config.Window.Publicity.Close))
			}
		}
	}
	// S3 durations are whole seconds; round down so a signed link cannot
	// outlive the scheduled publicity close instant.
	expiresIn := int64(ttl / time.Second)
	if !allowed || expiresIn < 1 {
		writeError(c, http.StatusForbidden, "forbidden", "无权读取这份佐证，或公示窗口已结束", nil)
		return
	}
	if status != "ready" {
		writeError(c, http.StatusConflict, "upload_incomplete", "佐证尚未完成上传", nil)
		return
	}
	// Whether the browser may render this in place is decided here from the
	// stored media type, never from the caller's query string.
	inline := c.Query("disposition") == "inline" && inlineRenderable(mediaType)
	downloadURL, err := s.deps.Objects.PresignGet(c.Request.Context(), objectKey, time.Duration(expiresIn)*time.Second, filename, inline)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "evidence.url_issued", "evidence", strconv.FormatInt(evidenceID, 10), nil, nil, map[string]any{"expiresIn": expiresIn, "kind": kind, "deputyAdjudication": deputyReader, "publicity": publicityReader}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"url": downloadURL.String(), "expiresIn": expiresIn, "filename": filename,
		"mediaType": mediaType, "inline": inline,
	})
}

// groupCanReadNoteEvidence keeps note attachments on the same reveal boundary
// as the reason that references them. Initial reviewers only see their own
// notes. Appeal reviewers may read the original reasons, while peer rereview
// notes become visible only after every reviewer has decided.
func groupCanReadNoteEvidence(owner noteOwnerKind, own, submissionAppealReviewer, appealAllDecided, appealCreatorReviewer bool) bool {
	if own {
		return true
	}
	switch owner {
	case noteOwnerSubmission:
		return submissionAppealReviewer
	case noteOwnerAppeal:
		return appealAllDecided && appealCreatorReviewer
	default:
		return false
	}
}

// inlineRenderable reports whether a browser can display the type in place
// without executing author-supplied script. SVG is excluded precisely because
// it is active content, even though it is an image/* type.
func inlineRenderable(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "application/pdf" {
		return true
	}
	// HEIC 浏览器普遍解不了，内联只会开出一张碎图；让它走下载，用系统看图程序打开。
	// 前端的缩略图早就是这么避开 HEIC 的（lib/evidence.ts 的 canThumbnail）。
	if mediaType == "image/heic" || mediaType == "image/heif" {
		return false
	}
	return strings.HasPrefix(mediaType, "image/") && mediaType != "image/svg+xml"
}

func safeFilename(value string) string {
	value = path.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, value)
	if len(value) > 180 {
		ext := path.Ext(value)
		value = strings.TrimSuffix(value, ext)
		if len(value) > 150 {
			// 按字节切会把一个汉字劈成两半，留下非法 UTF-8：文件名一路进
			// Content-Disposition 和归档包，坏在那里比在这里难查得多。
			end := 150
			for end > 0 && !utf8.ValidString(value[:end]) {
				end--
			}
			value = value[:end]
		}
		value += ext
	}
	return strings.TrimSpace(value)
}

// objectKeySuffix 返回可以直接拼在 randomObjectPart() 后面的一段，只含 ASCII；
// 没有可用字符时返回空串，对象键就退化成纯随机的一段。
//
// 对象键不能带非 ASCII：garage 把 multipart 里的 key 字段按 str 解析，中文名
// 一律回 400 InvalidHeaderValue（同一个请求把 key 换成 ASCII 就只剩签名报错，
// 说明卡的就是这里）。而学生最常见的来源就是微信导出的「微信图片_2025….jpg」，
// 于是这些文件在四条 presign 路径上全部传不上去。
//
// 展示用的文件名照旧原样存进数据库，下载时由 Content-Disposition 带回原名，
// 所以这里只影响对象键本身，不影响用户看到的名字。
func objectKeySuffix(filename string) string {
	// 扩展名单独留：整体过滤会把「获奖证书.pdf」压成 pdf，键上就再也看不出
	// 这是个什么文件了。
	rawExt := path.Ext(filename)
	ext := asciiKeyRunes(rawExt)
	if ext == "." {
		ext = ""
	}
	base := strings.Trim(asciiKeyRunes(strings.TrimSuffix(filename, rawExt)), ".-_")
	if len(base) > 60 {
		base = strings.Trim(base[:60], ".-_")
	}
	switch {
	case base != "":
		return "-" + base + ext
	case ext != "":
		return ext
	default:
		return ""
	}
}

func asciiKeyRunes(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return -1
		}
	}, value)
}

func randomObjectPart() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(raw[:])
}
