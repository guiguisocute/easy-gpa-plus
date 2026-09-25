package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
)

const sealPhrase = "全部提交完成"

func (s *Server) myBaseItems(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	appealAvailable := ensureCapability(current.Config, "appeal", time.Now()) == nil
	byKey := make(map[string]gin.H)
	order := make([]string, 0)
	claimableBaseKeys := make(map[string]bool)
	for _, category := range current.Config.Categories {
		for _, item := range category.BaseItems {
			if item.StudentClaim != nil {
				claimableBaseKeys[category.Key+"\x00"+item.Key] = true
				continue
			}
			key := category.Key + "\x00base\x00" + item.Key
			order = append(order, key)
			byKey[key] = gin.H{
				"id": nil, "category": category.Key, "categoryName": category.Name,
				"itemKey": item.Key, "itemName": item.Name, "kind": "base",
				"fullScore": item.Full, "score": item.Full, "basis": "方案默认满分",
				"canAppeal": false, "appealsUsed": 0, "recorded": false,
			}
		}
		for _, item := range category.PenaltyItems {
			key := category.Key + "\x00penalty\x00" + item.Key
			order = append(order, key)
			byKey[key] = gin.H{
				"id": nil, "category": category.Key, "categoryName": category.Name,
				"itemKey": item.Key, "itemName": item.Name, "kind": "penalty",
				"fullScore": nil, "score": 0, "basis": "暂无扣分记录",
				"canAppeal": false, "appealsUsed": 0, "recorded": false,
			}
		}
	}
	rows, err := tx.Query(c.Request.Context(), `
		SELECT b.id,b.category_key,b.item_key,b.kind,b.full_score::float8,b.score::float8,b.basis,b.updated_at,
		       (SELECT count(*) FROM appeal a
		         WHERE a.kind='student_appeal' AND a.student_id=b.student_id AND a.target_id=b.id
		           AND a.target_type=CASE WHEN b.kind='base' THEN 'base_score' ELSE 'penalty_score' END
		           AND a.status<>'draft'),
		       EXISTS (SELECT 1 FROM appeal a
		         WHERE a.target_id=b.id
		           AND a.target_type=CASE WHEN b.kind='base' THEN 'base_score' ELSE 'penalty_score' END
		           AND a.status IN ('filed','reviewing','escalated')),
		       EXISTS (SELECT 1 FROM appeal a
		         WHERE a.kind='student_appeal' AND a.target_id=b.id
		           AND a.target_type=CASE WHEN b.kind='base' THEN 'base_score' ELSE 'penalty_score' END
		           AND a.status='final'),
		       EXISTS (SELECT 1 FROM objection o
		         WHERE o.student_id=b.student_id AND o.scheme_id=b.scheme_id AND o.category_key=b.category_key
		           AND o.item_key=b.item_key AND o.kind=b.kind AND o.status='submitted')
		  FROM base_score b
		 WHERE b.student_id=$1 AND b.scheme_id=$2
		 ORDER BY b.category_key,b.kind,b.item_key
	`, actor.UserID, current.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var category, itemKey, kind, basis string
		var full *float64
		var score float64
		var appealCount int
		var appealPending, appealFinal, objectionPending bool
		var updated time.Time
		if err := rows.Scan(&id, &category, &itemKey, &kind, &full, &score, &basis, &updated, &appealCount, &appealPending, &appealFinal, &objectionPending); err != nil {
			writeServiceError(c, err)
			return
		}
		if kind == "base" && claimableBaseKeys[category+"\x00"+itemKey] {
			continue
		}
		key := category + "\x00" + kind + "\x00" + itemKey
		item := byKey[key]
		if item == nil {
			order = append(order, key)
			item = gin.H{"category": category, "itemKey": itemKey, "kind": kind}
		}
		item["id"] = strconv.FormatInt(id, 10)
		item["fullScore"] = full
		item["score"] = score
		item["basis"] = basis
		item["canAppeal"] = appealAvailable && appealCount < 2 && !appealPending && !appealFinal && !objectionPending
		item["appealsUsed"] = appealCount
		item["recorded"] = true
		item["updatedAt"] = updated
		byKey[key] = item
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]gin.H, 0, len(order))
	for _, key := range order {
		items = append(items, byKey[key])
	}
	if err := appendAudit(c, tx, "base_score.list", "base_score", "", nil, nil, map[string]any{"count": len(items), "schemeId": current.ID}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) mySeal(c *gin.Context) {
	actor := mustActor(c)
	tx := mustTx(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	if err == nil && !time.Now().Before(current.Config.Window.Close) {
		if err := ensureAutomaticSeal(c, tx, actor.UserID, current.Config.Window.Close); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	var id *int64
	var source *string
	var sealedAt *time.Time
	err = tx.QueryRow(c.Request.Context(), `
		SELECT id,source,sealed_at FROM seal WHERE student_id=$1 AND unsealed_at IS NULL ORDER BY sealed_at DESC LIMIT 1
	`, actor.UserID).Scan(&id, &source, &sealedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var drafts, submitted int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT count(*) FILTER (WHERE status='draft'),count(*) FILTER (WHERE status<>'draft')
		  FROM submission WHERE student_id=$1
	`, actor.UserID).Scan(&drafts, &submitted); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "seal.read", "seal", "", nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	response := gin.H{"sealed": id != nil, "source": source, "sealedAt": sealedAt, "draftCount": drafts, "submittedCount": submitted, "confirmationPhrase": sealPhrase}
	if current.ID != 0 {
		response["windowClose"] = current.Config.Window.Close
	}
	c.JSON(http.StatusOK, response)
}

func ensureAutomaticSeal(c *gin.Context, tx pgx.Tx, userID int64, closedAt time.Time) error {
	actor := mustActor(c)
	command, err := tx.Exec(c.Request.Context(), `
		INSERT INTO seal (class_id,student_id,source,sealed_at)
		VALUES ($1,$2,'auto',$3)
		ON CONFLICT (class_id,student_id) WHERE unsealed_at IS NULL DO NOTHING
	`, actor.ClassID, userID, closedAt)
	if err != nil || command.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (class_id,actor_id,actor_role,action,resource_type,resource_id,metadata)
		VALUES ($1,NULL,'system','seal.auto','user',$2::bigint::text,jsonb_build_object('windowClose',$3::timestamptz))
	`, actor.ClassID, userID, closedAt)
	if err != nil {
		return err
	}
	return enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.SealConfirmed, map[string]any{"studentId": userID, "source": "auto"})
}

func (s *Server) sealMySubmissions(c *gin.Context) {
	var input struct {
		Phrase string `json:"phrase"`
		SID    string `json:"sid"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "封存确认参数不正确", nil)
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	var sid string
	if err := tx.QueryRow(c.Request.Context(), `SELECT sid FROM app_user WHERE id=$1`, actor.UserID).Scan(&sid); err != nil {
		writeServiceError(c, err)
		return
	}
	if input.Phrase != sealPhrase || input.SID != sid {
		writeError(c, http.StatusUnprocessableEntity, "confirmation_mismatch", "确认短语或学号不一致", nil)
		return
	}
	var sealID int64
	err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO seal (class_id,student_id,source)
		VALUES ($1,$2,'manual')
		ON CONFLICT (class_id,student_id) WHERE unsealed_at IS NULL
		DO UPDATE SET student_id=EXCLUDED.student_id
		RETURNING id
	`, actor.ClassID, actor.UserID).Scan(&sealID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var submitted, drafts int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FILTER (WHERE status<>'draft'),count(*) FILTER (WHERE status='draft') FROM submission WHERE student_id=$1`, actor.UserID).Scan(&submitted, &drafts); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "seal.confirmed", "seal", strconv.FormatInt(sealID, 10), nil, map[string]any{"source": "manual"}, map[string]any{"submitted": submitted, "draftsExcluded": drafts}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.SealConfirmed, map[string]any{"studentId": actor.UserID, "source": "manual", "submitted": submitted, "draftsExcluded": drafts}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": strconv.FormatInt(sealID, 10), "sealed": true, "source": "manual", "draftsExcluded": drafts})
}

func (s *Server) adminSeals(c *gin.Context) {
	tx := mustTx(c)
	rows, err := tx.Query(c.Request.Context(), `SELECT u.id,u.sid,u.name,u.role,
 count(s.id) FILTER(WHERE s.status='draft'),count(s.id) FILTER(WHERE s.status<>'draft'),
 count(s.id) FILTER(WHERE s.final_score IS NOT NULL),active.source,active.sealed_at,max(s.updated_at)
 FROM app_user u LEFT JOIN submission s ON s.student_id=u.id
 LEFT JOIN LATERAL(SELECT source,sealed_at FROM seal WHERE student_id=u.id AND unsealed_at IS NULL ORDER BY sealed_at DESC LIMIT 1) active ON true
 WHERE u.status='active' GROUP BY u.id,active.source,active.sealed_at ORDER BY u.sid`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var sid, name, role string
		var drafts, submitted, scored int
		var source *string
		var sealedAt, lastActivity *time.Time
		if err := rows.Scan(&id, &sid, &name, &role, &drafts, &submitted, &scored, &source, &sealedAt, &lastActivity); err != nil {
			writeServiceError(c, err)
			return
		}
		state := "active"
		if sealedAt != nil {
			state = "sealed"
		} else if submitted == 0 {
			state = "none"
		}
		items = append(items, gin.H{"userId": strconv.FormatInt(id, 10), "sid": sid, "name": name, "role": role, "drafts": drafts, "submitted": submitted, "scored": scored, "sealState": state, "sealSource": source, "sealedAt": sealedAt, "lastActivity": lastActivity})
	}
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) remindUnsealed(c *gin.Context) {
	var input struct {
		UserID int64 `json:"userId,string"`
	}
	_ = c.ShouldBindJSON(&input)
	tx := mustTx(c)
	actor := mustActor(c)
	rows, err := tx.Query(c.Request.Context(), `
		SELECT u.id FROM app_user u
		 WHERE u.status='active'
		   AND ($1=0 OR u.id=$1)
		   AND NOT EXISTS (SELECT 1 FROM seal s WHERE s.student_id=u.id AND s.unsealed_at IS NULL)
		 ORDER BY u.id
	`, input.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var userIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		userIDs = append(userIDs, id)
	}
	rows.Close()
	if err := enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.WindowReminder, map[string]any{"studentIds": userIDs, "triggeredBy": actor.UserID}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "seal.reminder_requested", "seal", "", nil, nil, map[string]any{"recipientCount": len(userIDs)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"recipientCount": len(userIDs)})
}

func (s *Server) unsealStudent(c *gin.Context) {
	userID, ok := pathID(c, "uid")
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || utf8.RuneCountInString(strings.TrimSpace(input.Reason)) < 4 {
		writeError(c, http.StatusBadRequest, "reason_required", "解封理由至少需要 4 个字符", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	// Serialize with settlement before changing the seal, so a concurrent run
	// cannot publish the old, confirmed scorecard after invalidation.
	if _, err := tx.Exec(c.Request.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('easygpa:settlement:' || $1::bigint::text,0))`, actor.ClassID); err != nil {
		writeServiceError(c, err)
		return
	}
	var sealID int64
	err := tx.QueryRow(c.Request.Context(), `
		UPDATE seal SET unsealed_at=now(),unsealed_by=$1,unseal_reason=$2
		 WHERE class_id=$4 AND id=(SELECT id FROM seal WHERE class_id=$4 AND student_id=$3 AND unsealed_at IS NULL ORDER BY sealed_at DESC LIMIT 1)
		 RETURNING id
	`, actor.UserID, strings.TrimSpace(input.Reason), userID, actor.ClassID).Scan(&sealID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(c, http.StatusConflict, "not_sealed", "该学生当前未封存", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// Keep the same seal -> student-lock order as sealMySubmissions. A worker
	// that saw the previous seal must finish generating before we invalidate;
	// later workers will see this student's unsealed state and wait.
	if _, err := tx.Exec(c.Request.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('easygpa:blind-audit-student:' || $1::bigint::text || ':' || $2::bigint::text,0))`, actor.ClassID, userID); err != nil {
		writeServiceError(c, err)
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		WITH stale AS (
			UPDATE scorecard_audit_batch
			   SET status='stale',stale_at=now(),stale_reason='学生解封',invalidated_at=now(),invalidated_reason='学生解封'
			 WHERE class_id=$1 AND student_id=$2
			   AND status IN ('generating','blocked','open','resolving','complete')
			 RETURNING class_id,id
		)
		INSERT INTO outbox_event (class_id,type,payload)
		SELECT class_id,$3,jsonb_build_object('batchId',id::text,'studentId',$2,'reason','学生解封') FROM stale
	`, actor.ClassID, userID, events.ScorecardAuditStale); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := invalidateLatestSettlement(c.Request.Context(), tx, "学生解封，重新封存并处理完单项事项后可再次结算"); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "seal.unsealed", "seal", strconv.FormatInt(sealID, 10), map[string]any{"sealed": true}, map[string]any{"sealed": false}, map[string]any{"studentId": userID, "reason": strings.TrimSpace(input.Reason)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
