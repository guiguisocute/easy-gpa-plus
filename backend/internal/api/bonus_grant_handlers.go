package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/scheme"
)

type bonusGrantInput struct {
	submissionInput
	RequestID     string   `json:"requestId"`
	SchemeVersion string   `json:"schemeVersion"`
	StudentIDs    []jsonID `json:"studentIds"`
	UploadID      *jsonID  `json:"uploadId,omitempty"`
}

func normalizeBonusGrant(input *bonusGrantInput) error {
	if input.UploadID != nil && *input.UploadID <= 0 {
		return errors.New("佐证上传标识无效，请重新上传")
	}
	id, err := uuid.Parse(input.RequestID)
	if err != nil || id == uuid.Nil {
		return errors.New("加分批次标识无效，请刷新页面后重试")
	}
	input.RequestID = id.String()
	input.Title, input.Note = strings.TrimSpace(input.Title), strings.TrimSpace(input.Note)
	input.Category, input.ItemKey = strings.TrimSpace(input.Category), strings.TrimSpace(input.ItemKey)
	if n := utf8.RuneCountInString(input.Note); n < 4 || n > 2000 {
		return errors.New("请填写 4—2000 字的统一加分依据")
	}
	if len(input.StudentIDs) == 0 || len(input.StudentIDs) > 1000 {
		return errors.New("请选择 1—1000 名本班启用成员")
	}
	sort.Slice(input.StudentIDs, func(i, j int) bool { return input.StudentIDs[i] < input.StudentIDs[j] })
	for i, id := range input.StudentIDs {
		if id <= 0 || (i > 0 && input.StudentIDs[i-1] == id) {
			return errors.New("加分成员不能重复或为空")
		}
	}
	return nil
}

func prepareBonusGrant(current storedScheme, input bonusGrantInput, now time.Time) (preparedSubmission, error) {
	prepared, err := prepareSubmission(current, input.submissionInput, now, true)
	if err != nil {
		return prepared, err
	}
	// Unlike a student draft, a direct grant cannot leave the score to review.
	points, err := scheme.ScoreClaim(prepared.Item.ScoreRule, prepared.Claim)
	if err != nil {
		return prepared, err
	}
	score := points.Float64()
	if math.IsNaN(score) || math.IsInf(score, 0) || score <= 0 || score > 99999 {
		return prepared, errors.New("直接加分必须为大于 0 且不超过 99999 的有效分值")
	}
	if err := scoreWithinRule(prepared.Item.ScoreRule, score); err != nil {
		return prepared, err
	}
	prepared.Requested = &score
	return prepared, nil
}

func (s *Server) createBonusGrant(c *gin.Context) {
	var input bonusGrantInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "加分参数不正确", nil)
		return
	}
	if err := normalizeBonusGrant(&input); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "bonus_grant_invalid", err.Error(), nil)
		return
	}
	actor, tx, ctx := mustActor(c), mustTx(c), c.Request.Context()
	// Direct self grants are explicitly allowed; this does not grant self-adjudication.
	if actor.Role != "class_admin" && !governanceExecutionFor(c, "bonus") {
		writeError(c, http.StatusForbidden, "forbidden", "只有班级管理员可以直接加分", nil)
		return
	}
	// Match settlement and scorecard producers' lock order before changing scores.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('easygpa:settlement:' || $1::bigint::text,0))`, actor.ClassID); err != nil {
		writeServiceError(c, err)
		return
	}
	raw, _ := json.Marshal(input)
	var same bool
	var previousScore float64
	err := tx.QueryRow(ctx, `SELECT request=$3::jsonb,score::float8 FROM bonus_grant_batch WHERE class_id=$1 AND id=$2::uuid`, actor.ClassID, input.RequestID, raw).Scan(&same, &previousScore)
	if err == nil {
		if !same {
			writeError(c, http.StatusConflict, "bonus_grant_changed", "本批次已经提交，不能用相同批次修改加分内容", nil)
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": input.RequestID, "count": len(input.StudentIDs), "score": previousScore})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	current, err := loadCurrentScheme(ctx, tx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// Publishing retires the old row, so holding it prevents a rule switch mid-batch.
	var published bool
	if err := tx.QueryRow(ctx, `SELECT status='published' FROM scheme WHERE id=$1 FOR SHARE`, current.ID).Scan(&published); err != nil {
		writeServiceError(c, err)
		return
	}
	if !published || input.SchemeVersion != current.Config.Version {
		writeError(c, http.StatusConflict, "scheme_changed", "班级方案已更新，请刷新后重新确认加分项目", nil)
		return
	}
	if current.Config.Window.LockedAt(time.Now()) {
		writeError(c, http.StatusConflict, "class_locked_down", "本学期已全系统封锁", nil)
		return
	}
	prepared, err := prepareBonusGrant(current, input, time.Now())
	if err != nil {
		writeClaimError(c, err)
		return
	}
	for _, id := range input.StudentIDs {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('easygpa:blind-audit-student:' || $1::bigint::text || ':' || $2::bigint::text,0))`, actor.ClassID, int64(id)); err != nil {
			writeServiceError(c, err)
			return
		}
		var role, status string
		err := tx.QueryRow(ctx, `SELECT role,status FROM app_user WHERE class_id=$1 AND id=$2 FOR SHARE`, actor.ClassID, int64(id)).Scan(&role, &status)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !isScorableClassMember(role, status)) {
			writeError(c, http.StatusUnprocessableEntity, "bonus_member_invalid", "所选成员已停用或不属于本班，请刷新名单；本批次未加分", nil)
			return
		}
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	files, ok := s.prepareBonusEvidence(c, input, prepared)
	if !ok {
		return
	}
	copiedKeys := make([]string, 0)
	finished := false
	defer func() {
		if !finished {
			s.discardBonusCopies(copiedKeys)
		}
	}()
	if _, err := tx.Exec(ctx, `INSERT INTO bonus_grant_batch(class_id,id,created_by,request,score) VALUES ($1,$2::uuid,$3,$4,$5)`, actor.ClassID, input.RequestID, actor.UserID, raw, prepared.Requested); err != nil {
		writeServiceError(c, err)
		return
	}
	for _, studentID := range input.StudentIDs {
		source, auditAction := "admin_grant", "submission.admin_granted"
		if governanceExecutionFor(c, "bonus") {
			source, auditAction = "collective_grant", "submission.collective_granted"
		}
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO submission
            (class_id,student_id,scheme_id,scheme_version,category_key,item_key,filed_category_key,filed_item_key,
             title,claim,requested_score,final_score,status,source,rule_snapshot,filed_rule_snapshot,markdown_note,
             submitted_at,scored_at,bonus_grant_batch_id)
            VALUES ($1,$2,$3,$4,$5,$6,$5,$6,$7,$8,$9,$9,'scored',$13,$10,$10,$11,now(),now(),$12::uuid) RETURNING id`,
			actor.ClassID, int64(studentID), prepared.SchemeID, prepared.SchemeVersion, prepared.Category.Key, prepared.Item.Key,
			input.Title, []byte(input.Claim), prepared.Requested, prepared.Snapshot, input.Note, input.RequestID, source).Scan(&id)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if err := s.copyBonusEvidence(c, id, files, &copiedKeys); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := appendAudit(c, tx, auditAction, "submission", strconv.FormatInt(id, 10), nil,
			gin.H{"status": "scored", "finalScore": prepared.Requested, "source": source},
			gin.H{"reason": input.Note, "batchId": input.RequestID, "studentId": int64(studentID), "selfGranted": int64(studentID) == actor.UserID}); err != nil {
			writeServiceError(c, err)
			return
		}
		if err := invalidateStudentBlindAudit(ctx, tx, current.ID, int64(studentID), "", "班管直接加分，当前成绩版本已更新"); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if err := invalidateLatestSettlement(ctx, tx, "班管直接加分"); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.consumeBonusEvidence(c, input, files); err != nil {
		writeServiceError(c, err)
		return
	}
	finished = true
	if len(files) > 0 {
		s.scheduleStorageReconcile(c, actor.ClassID)
	}
	c.JSON(http.StatusCreated, gin.H{"id": input.RequestID, "count": len(input.StudentIDs), "score": prepared.Requested})
}

func (s *Server) bonusGrants(c *gin.Context) {
	rows, err := mustTx(c).Query(c.Request.Context(), `SELECT b.id::text,b.request->>'title',b.request->>'note',b.score::float8,b.created_at,
        COALESCE((SELECT jsonb_agg(jsonb_build_object('id',s.id::text,'studentId',u.id::text,'sid',u.sid,'name',u.name,
            'score',s.final_score,'status',s.status,'itemName',s.rule_snapshot->'item'->>'name') ORDER BY u.sid)
            FROM submission s JOIN app_user u ON u.id=s.student_id WHERE s.class_id=b.class_id AND s.bonus_grant_batch_id=b.id),'[]'::jsonb),
        COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id::text,'name',e.filename,'mediaType',e.media_type,'sizeBytes',e.size_bytes,'status',e.status,'uploadedAt',e.created_at) ORDER BY e.id)
            FROM evidence e WHERE e.submission_id=(SELECT s.id FROM submission s WHERE s.bonus_grant_batch_id=b.id AND s.class_id=b.class_id ORDER BY s.id LIMIT 1) AND e.kind='claim' AND e.status='ready'),'[]'::jsonb)
        FROM bonus_grant_batch b WHERE b.class_id=$1 ORDER BY b.created_at DESC,b.id DESC LIMIT 100`, mustActor(c).ClassID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, title, note string
		var score float64
		var at time.Time
		var members, evidence []byte
		if err := rows.Scan(&id, &title, &note, &score, &at, &members, &evidence); err != nil {
			writeServiceError(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "title": title, "note": note, "score": score, "createdAt": at, "members": json.RawMessage(members), "evidence": json.RawMessage(evidence)})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
