package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type noteOwnerKind string

const (
	noteOwnerSubmission   noteOwnerKind = "submission"
	noteOwnerAppeal       noteOwnerKind = "appeal"
	noteOwnerObjection    noteOwnerKind = "objection"
	noteOwnerReport       noteOwnerKind = "report"
	noteOwnerBlindAudit   noteOwnerKind = "blind-audit"
	noteOwnerReportReview noteOwnerKind = "report-review"
	noteOwnerBonusUpload  noteOwnerKind = "bonus-grant-upload"
)

// 举报附件不记上传人——记了就等于把举报人写进了库（000042 里有一条约束顶着）。
// 其余三种都必须记：那三种台面上的附件是实名的，谁传的要查得到。
//
// 有一处按人记账躲不掉：每日上传字节配额是按 user_id 累加的，不按人算就防不住刷。
// 它只存"某人今天传了多少字节"，不存传了哪一份，所以接口层拿不到对应关系；
// 真要把配额的时间戳和附件的时间戳对起来，得直接连库——那种权限本来就能读
// report_reporter，不是这里新开的口子。
func noteRecordsUploader(owner noteOwnerKind) bool { return owner != noteOwnerReport }

func (s *Server) presignSubmissionNote(c *gin.Context) { s.presignNoteEvidence(c, noteOwnerSubmission) }
func (s *Server) completeSubmissionNote(c *gin.Context) {
	s.completeNoteEvidence(c, noteOwnerSubmission)
}
func (s *Server) deleteSubmissionNote(c *gin.Context)  { s.deleteNoteEvidence(c, noteOwnerSubmission) }
func (s *Server) presignAppealNote(c *gin.Context)     { s.presignNoteEvidence(c, noteOwnerAppeal) }
func (s *Server) completeAppealNote(c *gin.Context)    { s.completeNoteEvidence(c, noteOwnerAppeal) }
func (s *Server) deleteAppealNote(c *gin.Context)      { s.deleteNoteEvidence(c, noteOwnerAppeal) }
func (s *Server) presignObjectionNote(c *gin.Context)  { s.presignNoteEvidence(c, noteOwnerObjection) }
func (s *Server) completeObjectionNote(c *gin.Context) { s.completeNoteEvidence(c, noteOwnerObjection) }
func (s *Server) deleteObjectionNote(c *gin.Context)   { s.deleteNoteEvidence(c, noteOwnerObjection) }
func (s *Server) presignReportNote(c *gin.Context)     { s.presignNoteEvidence(c, noteOwnerReport) }
func (s *Server) completeReportNote(c *gin.Context)    { s.completeNoteEvidence(c, noteOwnerReport) }
func (s *Server) deleteReportNote(c *gin.Context)      { s.deleteNoteEvidence(c, noteOwnerReport) }

func authorizeNoteOwner(ctx context.Context, tx pgx.Tx, actor Actor, owner noteOwnerKind, ownerID int64) error {
	var allowed bool
	switch owner {
	case noteOwnerBonusUpload:
		ownerUser := actor.UserID
		grant, executing := ctx.Value(governanceExecutionKey{}).(governanceExecution)
		if executing && (grant.Action == "bonus" || grant.Action == "gpa") {
			ownerUser = grant.Author
		}
		if actor.Role != "class_admin" && !executing {
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governance_member m JOIN class_governance g USING(class_id) WHERE m.user_id=$1 AND g.mode='collective' AND m.left_at IS NULL AND m.joined_at<=now()-interval '24 hours')`, actor.UserID).Scan(&allowed); err != nil {
				return err
			}
			if !allowed {
				return errNoteEvidenceForbidden
			}
		}
		if err := tx.QueryRow(ctx, `SELECT created_by=$2 AND used_by IS NULL FROM bonus_grant_upload WHERE id=$1 FOR UPDATE`, ownerID, ownerUser).Scan(&allowed); err != nil {
			return err
		}
		if allowed && !executing {
			var frozen bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governance_proposal WHERE action IN ('bonus','gpa') AND payload->>'uploadId'=$1 AND status NOT IN ('rejected','stale'))`, strconv.FormatInt(ownerID, 10)).Scan(&frozen); err != nil {
				return err
			}
			allowed = !frozen
		}
	case noteOwnerSubmission:
		var studentID int64
		err := tx.QueryRow(ctx, `
			SELECT s.student_id, s.student_id<>$2 AND EXISTS (
				SELECT 1 FROM submission_reviewer sr
				 WHERE sr.submission_id=s.id AND sr.reviewer_id=$2 AND sr.active
			)
			  FROM submission s WHERE s.id=$1
		`, ownerID, actor.UserID).Scan(&studentID, &allowed)
		if err != nil {
			return err
		}
		if actor.Role == "class_admin" || (!allowed && actor.IsDeputy) {
			return authorizeAdjudicationNote(ctx, tx, actor, studentID)
		}
	case noteOwnerAppeal:
		var studentID int64
		err := tx.QueryRow(ctx, `
			SELECT a.student_id, COALESCE(a.handler_id=$2,false) OR EXISTS (
				SELECT 1 FROM appeal_reviewer ar WHERE ar.appeal_id=a.id AND ar.reviewer_id=$2
			)
			  FROM appeal a WHERE a.id=$1 AND a.kind='student_appeal' AND a.status<>'draft'
		`, ownerID, actor.UserID).Scan(&studentID, &allowed)
		if err != nil {
			return err
		}
		if actor.Role == "class_admin" || (!allowed && actor.IsDeputy) {
			return authorizeAdjudicationNote(ctx, tx, actor, studentID)
		}
	case noteOwnerObjection:
		var studentID int64
		var status string
		err := tx.QueryRow(ctx, `
			SELECT proposer_id=$2 AND status='draft',student_id,status FROM objection WHERE id=$1 FOR UPDATE
		`, ownerID, actor.UserID).Scan(&allowed, &studentID, &status)
		if err != nil {
			return err
		}
		if !allowed && status == "submitted" {
			return authorizeAdjudicationNote(ctx, tx, actor, studentID)
		}
	case noteOwnerBlindAudit:
		row, err := loadBlindAssignment(ctx, tx, ownerID, actor.UserID, true)
		if err != nil {
			return err
		}
		allowed = (actor.Role == "group" || actor.Role == "class_admin") && row.StudentID != actor.UserID && blindAssignmentEditable(row)
	case noteOwnerReportReview:
		var studentID int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT student_id,status FROM report WHERE id=$1 FOR UPDATE`, ownerID).Scan(&studentID, &status); err != nil {
			return err
		}
		if status == "escalated" {
			return authorizeAdjudicationNote(ctx, tx, actor, studentID)
		} else if status == "reviewing" && (actor.Role == "group" || actor.Role == "class_admin") && studentID != actor.UserID {
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM report_reviewer WHERE report_id=$1 AND reviewer_id=$2 AND decided_at IS NULL)`, ownerID, actor.UserID).Scan(&allowed); err != nil {
				return err
			}
		}
	case noteOwnerReport:
		// 只有举报人本人能给自己的举报加附件，而"谁是举报人"只能从人查到举报，
		// 不能反过来——report_evidence_is_mine 是 000042 里的单向函数。
		// 时限是"还没有人开始判"：复核人已经按现有材料下过结论之后再补料，
		// 两个人看到的就不是同一份东西了。
		err := tx.QueryRow(ctx, `
			SELECT report_evidence_is_mine($3,$2,$1)
			   AND r.status='reviewing'
			   AND NOT EXISTS (SELECT 1 FROM report_reviewer rr
			                    WHERE rr.report_id=r.id AND rr.decided_at IS NOT NULL)
			  FROM report r WHERE r.id=$1
		`, ownerID, actor.UserID, actor.ClassID).Scan(&allowed)
		if err != nil {
			return err
		}
		if allowed {
			var judged bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governance_voter v JOIN governance_proposal p ON p.id=v.proposal_id WHERE p.action='report' AND p.target_id=$1 AND v.opinion IS NOT NULL)`, ownerID).Scan(&judged); err != nil {
				return err
			}
			allowed = !judged
		}
	default:
		return errors.New("附件所属内容类型不正确，请返回原页面重新上传")
	}
	if !allowed {
		return errNoteEvidenceForbidden
	}
	return nil
}

var errNoteEvidenceForbidden = errors.New("note evidence forbidden")

func writeNoteAuthorizationError(c *gin.Context, err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusNotFound, "not_found", "正文记录不存在", nil)
		return true
	}
	if errors.Is(err, errNoteEvidenceForbidden) {
		writeError(c, http.StatusForbidden, "forbidden", "无权给这条正文添加附件", nil)
		return true
	}
	return false
}

// 给举报加附件这件事本身也不能实名记账：审计日志里带上 actor_id、IP、User-Agent
// 任何一样，班级管理员翻一下就知道是谁报的，匿名当场作废。举报的三个动作
// （建举报、传附件、删附件）走的是同一条匿名通道。
func appendNoteEvidenceAudit(c *gin.Context, tx pgx.Tx, owner noteOwnerKind, action, ownerID string, metadata map[string]any) error {
	if owner == noteOwnerReport {
		return appendAnonymousAudit(c, tx, action, string(owner), ownerID, metadata)
	}
	return appendAudit(c, tx, action, string(owner), ownerID, nil, nil, metadata)
}

func noteOwnerColumn(owner noteOwnerKind) string {
	switch owner {
	case noteOwnerBonusUpload:
		return "bonus_upload_id"
	case noteOwnerSubmission:
		return "submission_id"
	case noteOwnerAppeal:
		return "appeal_id"
	case noteOwnerObjection:
		return "objection_id"
	case noteOwnerReport:
		return "report_id"
	case noteOwnerBlindAudit:
		return "blind_assignment_id"
	case noteOwnerReportReview:
		return "review_report_id"
	default:
		return ""
	}
}

// complete / delete 用来认"这一份是不是我传的"的那半句 WHERE。举报附件没有上传人可认，
// 认的就只是"它确实是一份匿名附件"——这一份归不归我，authorizeNoteOwner 已经问过了。
// 后半句 $3>0 是凑参数：两条语句共用同一组实参，$3 不出现的话 Postgres 会嫌
// 绑定的参数比语句要的多，直接报错。actor.UserID 恒为正，这半句不改变结果。
func noteUploaderPredicate(owner noteOwnerKind) string {
	if noteRecordsUploader(owner) {
		return "created_by=$3"
	}
	return "created_by IS NULL AND $3>0"
}

func (s *Server) presignNoteEvidence(c *gin.Context, owner noteOwnerKind) {
	ownerID, ok := pathID(c, "id")
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
	if err := authorizeNoteOwner(c.Request.Context(), tx, actor, owner, ownerID); err != nil {
		if !writeNoteAuthorizationError(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	flags := s.runtimeFlags(c.Request.Context())
	if err := validateEvidencePolicy(nil, input, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
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
	objectKey := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/" + string(owner) + "-" + strconv.FormatInt(ownerID, 10) + "/notes/" + randomObjectPart() + objectKeySuffix(input.Filename)
	upload, err := s.deps.Objects.PresignUpload(c.Request.Context(), objectKey, input.MediaType, input.SizeBytes, 15*time.Minute)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	column := noteOwnerColumn(owner)
	// 举报附件传 NULL：谁传的不入库。000042 的约束会挡住反过来写的写法。
	var uploader *int64
	if noteRecordsUploader(owner) {
		uploader = &actor.UserID
	}
	query := `INSERT INTO evidence (class_id,` + column + `,kind,object_key,filename,media_type,size_bytes,sha256,status,created_by)
		VALUES ($1,$2,'note',$3,$4,$5,$6,NULLIF($7,''),'pending',$8) RETURNING id`
	var evidenceID int64
	if err := tx.QueryRow(c.Request.Context(), query, actor.ClassID, ownerID, objectKey, input.Filename, input.MediaType, input.SizeBytes, input.SHA256, uploader).Scan(&evidenceID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendNoteEvidenceAudit(c, tx, owner, "note_evidence.presigned", strconv.FormatInt(ownerID, 10),
		map[string]any{"evidenceId": evidenceID, "filename": input.Filename}); err != nil {
		writeServiceError(c, err)
		return
	}
	base := "/api/v1/" + string(owner) + "s/" + strconv.FormatInt(ownerID, 10) + "/notes/" + strconv.FormatInt(evidenceID, 10) + "/complete"
	if owner == noteOwnerBonusUpload {
		base = "/api/v1/admin/" + strings.TrimPrefix(base, "/api/v1/")
		if strings.HasPrefix(c.FullPath(), "/api/v1/governance/") {
			base = strings.Replace(base, "/api/v1/admin/", "/api/v1/governance/", 1)
		}
	}
	response := uploadPolicyJSON(upload)
	response["evidenceId"] = strconv.FormatInt(evidenceID, 10)
	response["completeUrl"] = base
	c.JSON(http.StatusCreated, response)
}

func (s *Server) completeNoteEvidence(c *gin.Context, owner noteOwnerKind) {
	ownerID, ok := pathID(c, "id")
	if !ok {
		return
	}
	evidenceID, ok := pathID(c, "eid")
	if !ok {
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	if err := authorizeNoteOwner(c.Request.Context(), tx, actor, owner, ownerID); err != nil {
		if !writeNoteAuthorizationError(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	column := noteOwnerColumn(owner)
	query := `SELECT object_key,filename,media_type,size_bytes,status FROM evidence
		WHERE id=$1 AND ` + column + `=$2 AND kind='note' AND ` + noteUploaderPredicate(owner) + ` FOR UPDATE`
	var objectKey, filename, mediaType, status string
	var declaredSize int64
	if err := tx.QueryRow(c.Request.Context(), query, evidenceID, ownerID, actor.UserID).Scan(&objectKey, &filename, &mediaType, &declaredSize, &status); err != nil {
		if notFound(c, err, "正文附件") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if status == "ready" {
		c.JSON(http.StatusOK, gin.H{"evidenceId": strconv.FormatInt(evidenceID, 10), "status": "ready", "filename": filename, "mediaType": mediaType})
		return
	}
	result, ok := s.settleEvidenceUpload(c, evidenceID, objectKey, filename, mediaType, declaredSize)
	if !ok {
		return
	}
	if err := appendNoteEvidenceAudit(c, tx, owner, "note_evidence.completed", strconv.FormatInt(ownerID, 10),
		map[string]any{"evidenceId": evidenceID, "filename": result.Filename}); err != nil {
		writeServiceError(c, err)
		return
	}
	s.scheduleStorageReconcile(c, actor.ClassID)
	c.JSON(http.StatusOK, gin.H{"evidenceId": strconv.FormatInt(evidenceID, 10), "status": "ready", "filename": result.Filename, "mediaType": result.MediaType})
}

func (s *Server) deleteNoteEvidence(c *gin.Context, owner noteOwnerKind) {
	ownerID, ok := pathID(c, "id")
	if !ok {
		return
	}
	evidenceID, ok := pathID(c, "eid")
	if !ok {
		return
	}
	tx, actor := mustTx(c), mustActor(c)
	if err := authorizeNoteOwner(c.Request.Context(), tx, actor, owner, ownerID); err != nil {
		if !writeNoteAuthorizationError(c, err) {
			writeServiceError(c, err)
		}
		return
	}
	column := noteOwnerColumn(owner)
	statusPredicate := " AND status<>'ready'"
	if owner == noteOwnerBonusUpload {
		statusPredicate = ""
	}
	query := `SELECT object_key FROM evidence
		WHERE id=$1 AND ` + column + `=$2 AND kind='note' AND ` + noteUploaderPredicate(owner) + statusPredicate + ` FOR UPDATE`
	var objectKey string
	if err := tx.QueryRow(c.Request.Context(), query, evidenceID, ownerID, actor.UserID).Scan(&objectKey); err != nil {
		if notFound(c, err, "未完成的正文附件") {
			return
		}
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM evidence WHERE id=$1`, evidenceID); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendNoteEvidenceAudit(c, tx, owner, "note_evidence.deleted", strconv.FormatInt(ownerID, 10),
		map[string]any{"evidenceId": evidenceID}); err != nil {
		writeServiceError(c, err)
		return
	}
	s.removeObjectAfterCommit(c, actor.ClassID, objectKey)
	c.Status(http.StatusNoContent)
}

func objectionNoteEvidence(ctx context.Context, tx pgx.Tx, objectionID int64, draftCreator *int64) ([]gin.H, error) {
	files, err := noteEvidenceWhere(ctx, tx, "objection_id=$1 AND kind='note' AND ($2::bigint IS NULL OR created_by=$2)", objectionID, draftCreator)
	if err != nil {
		return nil, err
	}
	var assignmentID *int64
	if err := tx.QueryRow(ctx, `SELECT blind_assignment_id FROM objection WHERE id=$1`, objectionID).Scan(&assignmentID); err != nil {
		return nil, err
	}
	if assignmentID != nil {
		additional, err := noteEvidenceOf(ctx, tx, noteOwnerBlindAudit, *assignmentID)
		if err != nil {
			return nil, err
		}
		files = append(files, additional...)
	}
	return files, nil
}

func reportNoteEvidence(ctx context.Context, tx pgx.Tx, reportID int64) ([]gin.H, error) {
	return noteEvidenceOf(ctx, tx, noteOwnerReport, reportID)
}

// 附件清单。这里一列都不带上传人：举报那一路根本没有，另外几路有但没人需要看。
func noteEvidenceOf(ctx context.Context, tx pgx.Tx, owner noteOwnerKind, ownerID int64) ([]gin.H, error) {
	column := noteOwnerColumn(owner)
	if column == "" {
		return nil, errors.New("附件所属内容类型不正确，请返回原页面重新上传")
	}
	return noteEvidenceWhere(ctx, tx, column+"=$1 AND kind='note'", ownerID)
}

func noteEvidenceWhere(ctx context.Context, tx pgx.Tx, predicate string, args ...any) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,filename,media_type,size_bytes,sha256,status,created_at
		  FROM evidence WHERE `+predicate+` ORDER BY id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, size int64
		var filename, mediaType, status string
		var sha *string
		var createdAt time.Time
		if err := rows.Scan(&id, &filename, &mediaType, &size, &sha, &status, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"id": strconv.FormatInt(id, 10), "name": filename, "mediaType": mediaType, "sizeBytes": size, "sha256": sha, "status": status, "uploadedAt": createdAt})
	}
	return items, rows.Err()
}
