package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) createBonusGrantUpload(c *gin.Context) {
	actor := mustActor(c)
	var id int64
	if err := mustTx(c).QueryRow(c.Request.Context(), `INSERT INTO bonus_grant_upload(class_id,created_by) VALUES ($1,$2) RETURNING id`, actor.ClassID, actor.UserID).Scan(&id); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10)})
}

type bonusEvidenceFile struct {
	evidenceInput
	ObjectKey string
}

// The same upload lock is held by presign, completion, removal and consumption.
// A changed or unfinished file therefore cannot enter a confirmed grant.
func (s *Server) prepareBonusEvidence(c *gin.Context, input bonusGrantInput, prepared preparedSubmission) ([]bonusEvidenceFile, bool) {
	if input.UploadID == nil {
		return nil, true
	}
	tx, ctx, actor := mustTx(c), c.Request.Context(), mustActor(c)
	if err := authorizeNoteOwner(ctx, tx, actor, noteOwnerBonusUpload, int64(*input.UploadID)); err != nil {
		if !writeNoteAuthorizationError(c, err) {
			writeServiceError(c, err)
		}
		return nil, false
	}
	rows, err := tx.Query(ctx, `SELECT object_key,filename,media_type,size_bytes,COALESCE(sha256,''),status FROM evidence WHERE bonus_upload_id=$1 ORDER BY id FOR UPDATE`, int64(*input.UploadID))
	if err != nil {
		writeServiceError(c, err)
		return nil, false
	}
	files := make([]bonusEvidenceFile, 0)
	flags := s.runtimeFlags(ctx)
	var size int64
	for rows.Next() {
		var file bonusEvidenceFile
		var status string
		if err := rows.Scan(&file.ObjectKey, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &status); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return nil, false
		}
		if status != "ready" {
			rows.Close()
			writeError(c, http.StatusConflict, "upload_incomplete", "请等全部佐证上传完成，或移除未完成的文件后重试", nil)
			return nil, false
		}
		if err := validateEvidencePolicy(prepared.Item.Evidence, file.evidenceInput, int64(flags.UploadMaxMB), flags.EvidenceAllowedFormats); err != nil {
			rows.Close()
			writeError(c, http.StatusUnprocessableEntity, "evidence_invalid", err.Error(), nil)
			return nil, false
		}
		size += file.SizeBytes
		files = append(files, file)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeServiceError(c, err)
		return nil, false
	}
	// Each member receives independent claim evidence, just like AI-applied originals.
	// Charge the actual copies to the uploader's existing daily byte budget.
	if size > 0 {
		if size > int64(flags.EvidenceDailyMB)*1024*1024/int64(len(input.StudentIDs)) {
			writeError(c, http.StatusTooManyRequests, "evidence_daily_quota", "本批成员的佐证总量超过每日额度，请减少文件大小或人数", nil)
			return nil, false
		}
		if err := reserveEvidenceQuota(ctx, tx, actor.UserID, size*int64(len(input.StudentIDs)), flags.EvidenceDailyMB); err != nil {
			if errors.Is(err, errEvidenceDailyQuotaExceeded) {
				writeError(c, http.StatusTooManyRequests, "evidence_daily_quota", "本批佐证超过今天剩余的上传额度", nil)
			} else {
				writeServiceError(c, err)
			}
			return nil, false
		}
	}
	return files, true
}

func (s *Server) copyBonusEvidence(c *gin.Context, submissionID int64, files []bonusEvidenceFile, copiedKeys *[]string) error {
	actor, tx, ctx := mustActor(c), mustTx(c), c.Request.Context()
	for _, file := range files {
		destination := "class-" + strconv.FormatInt(actor.ClassID, 10) + "/submission-" + strconv.FormatInt(submissionID, 10) + "/" + randomObjectPart() + objectKeySuffix(file.Filename)
		*copiedKeys = append(*copiedKeys, destination)
		if err := s.deps.Objects.Copy(ctx, file.ObjectKey, destination, file.MediaType, file.SizeBytes); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO evidence(class_id,submission_id,kind,object_key,filename,media_type,size_bytes,sha256,status,created_by)
            VALUES ($1,$2,'claim',$3,$4,$5,$6,NULLIF($7,''),'ready',$8)`, actor.ClassID, submissionID, destination, file.Filename, file.MediaType, file.SizeBytes, file.SHA256, actor.UserID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) consumeBonusEvidence(c *gin.Context, input bonusGrantInput, files []bonusEvidenceFile) error {
	if input.UploadID == nil {
		return nil
	}
	tx, ctx := mustTx(c), c.Request.Context()
	if _, err := tx.Exec(ctx, `UPDATE bonus_grant_upload SET used_by=$2::uuid WHERE id=$1`, int64(*input.UploadID), input.RequestID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM evidence WHERE bonus_upload_id=$1`, int64(*input.UploadID)); err != nil {
		return err
	}
	for _, file := range files {
		s.removeObjectAfterCommit(c, mustActor(c).ClassID, file.ObjectKey)
	}
	return appendAudit(c, tx, "bonus_grant.evidence_attached", "bonus_grant_batch", input.RequestID, nil, nil, gin.H{"files": len(files), "members": len(input.StudentIDs)})
}

// Copies created before a failed batch are not retained as unreferenced objects.
func (s *Server) discardBonusCopies(keys []string) {
	for _, key := range keys {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.deps.Objects.Remove(ctx, key)
		cancel()
	}
}
