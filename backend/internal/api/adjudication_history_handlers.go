package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Each row is a decision event, not the current queue state. A later appeal or
// forced rejection must not replace the score shown for an earlier decision.
// Never join report_reporter: a history reader still cannot identify a reporter.
const adjudicationHistoryCTE = `WITH history AS (
	SELECT log.id::text AS id, log.created_at AS "createdAt",
	       CASE log.action
	         WHEN 'submission.arbitrated' THEN 'arbitration'
	         WHEN 'appeal.final' THEN 'appeal'
	         WHEN 'objection.applied' THEN 'objection'
	         WHEN 'objection.dismissed' THEN 'objection'
	         WHEN 'report.finalized' THEN 'report'
	         WHEN 'scorecard_audit.flag_decided' THEN 'scorecard'
	         WHEN 'classification.resolved' THEN 'classification'
	         WHEN 'submission.force_rejected' THEN 'force_reject' WHEN 'submission.force_scored' THEN 'force_score'
	       END AS kind,
	       COALESCE(student.sid,'') AS "studentId", COALESCE(student.name,'') AS student,
	       COALESCE(sub.title,base.item_key,obj.item_key,report.item_key,'整表问题') AS title,
	       COALESCE(log.after_data->>'category',resolution.after_category_key,sub.category_key,
	                base.category_key,obj.category_key,report.category_key,'') AS category,
	       COALESCE(log.before_data->>'category',resolution.before_category_key,
	                appeal.original_category_key,'') AS "beforeCategory",
	       COALESCE(log.after_data->>'itemKey',resolution.after_item_key,sub.item_key,
	                base.item_key,obj.item_key,report.item_key,'') AS "itemKey",
	       COALESCE(log.before_data->>'itemKey',resolution.before_item_key,
	                appeal.original_item_key,'') AS "beforeItemKey",
	       CASE WHEN log.before_data ? 'finalScore' THEN (log.before_data->>'finalScore')::float8
	            WHEN log.before_data ? 'score' THEN (log.before_data->>'score')::float8
	            ELSE COALESCE(resolution.before_score,appeal.original_score,obj.current_score,report.current_score)::float8
	       END AS "beforeScore",
	       COALESCE(log.after_data->>'finalScore',log.after_data->>'score')::float8 AS score,
	       COALESCE(log.after_data->>'status','scored') AS decision,
	       COALESCE(log.metadata->>'reason',appeal.resolution_reason,obj.decision_reason,
	                report.decision_reason,flag.resolution_reason,resolution.reason,'') AS reason,
	       COALESCE(appeal.reason,obj.basis,report.basis,flag.reason,suggestion.reason,'') AS "sourceReason",
	       COALESCE(sub.markdown_note,'') AS note,
	       sub.id::text AS "submissionId", appeal.id::text AS "appealId",
	       obj.id::text AS "objectionId", report.id::text AS "reportId",
	       COALESCE(flag.assignment_id,obj.blind_assignment_id,suggestion.blind_assignment_id)::text AS "blindAssignmentId",
	       sub.final_score::float8 AS "currentScore", sub.status AS "currentStatus"
	  FROM audit_log log
	  LEFT JOIN appeal ON log.resource_type='appeal' AND appeal.id::text=log.resource_id
	  LEFT JOIN objection obj ON log.resource_type='objection' AND obj.id::text=log.resource_id
	  LEFT JOIN report ON log.resource_type='report' AND report.id::text=log.resource_id
	  LEFT JOIN classification_resolution resolution
	    ON log.resource_type='classification_resolution' AND resolution.id::text=log.resource_id
	  LEFT JOIN classification_suggestion suggestion ON suggestion.id=resolution.suggestion_id
	  LEFT JOIN scorecard_audit_flag flag ON log.resource_type='scorecard_audit_flag' AND flag.id::text=log.resource_id
	  LEFT JOIN scorecard_audit_assignment assignment ON assignment.id=flag.assignment_id
	  LEFT JOIN scorecard_audit_subject subject ON subject.id=assignment.subject_id
	  LEFT JOIN submission sub ON sub.id::text=COALESCE(
	    CASE WHEN log.resource_type='submission' THEN log.resource_id END,
	    resolution.submission_id::text,
	    CASE WHEN appeal.target_type='submission' THEN appeal.target_id::text END,
	    CASE WHEN obj.kind='submission' THEN obj.target_id::text END,
	    CASE WHEN report.kind='submission' THEN report.target_id::text END,
	    flag.target_submission_id::text)
	  LEFT JOIN base_score base ON appeal.target_type IN ('base_score','penalty_score') AND base.id=appeal.target_id
	  LEFT JOIN app_user student ON student.id=COALESCE(sub.student_id,appeal.student_id,obj.student_id,report.student_id,subject.student_id)
	 WHERE log.actor_id=$1 AND log.class_id=$2
	   AND log.action IN ('submission.arbitrated','appeal.final','objection.applied','objection.dismissed',
	                      'report.finalized','scorecard_audit.flag_decided','classification.resolved','submission.force_rejected','submission.force_scored')
) `

const adjudicationHistoryFilter = ` WHERE ($3='' OR kind=$3)
	AND ($4='' OR student ILIKE '%'||$4||'%' OR "studentId" ILIKE '%'||$4||'%')
	AND ($5='' OR title ILIKE '%'||$5||'%' OR reason ILIKE '%'||$5||'%')
	AND ($6::timestamptz IS NULL OR "createdAt">=$6)
	AND ($7::timestamptz IS NULL OR "createdAt"<$7) `

func validAdjudicationHistoryKind(kind string) bool {
	switch kind {
	case "", "arbitration", "appeal", "objection", "report", "scorecard", "classification", "force_reject", "force_score":
		return true
	default:
		return false
	}
}

func (s *Server) adminAdjudicationHistory(c *gin.Context) {
	kind := strings.TrimSpace(c.Query("kind"))
	if !validAdjudicationHistoryKind(kind) {
		writeError(c, http.StatusBadRequest, "invalid_kind", "处理类型不正确", nil)
		return
	}
	var from, until *time.Time
	for key, target := range map[string]**time.Time{"from": &from, "until": &until} {
		if raw := strings.TrimSpace(c.Query(key)); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(c, http.StatusBadRequest, "invalid_time", "筛选时间格式不正确", nil)
				return
			}
			*target = &value
		}
	}
	if from != nil && until != nil && !from.Before(*until) {
		writeError(c, http.StatusBadRequest, "invalid_time", "开始日期不能晚于结束日期", nil)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	actor := mustActor(c)
	tx := mustTx(c)
	args := []any{actor.UserID, actor.ClassID, kind, strings.TrimSpace(c.Query("student")), strings.TrimSpace(c.Query("q")), from, until}
	var total int
	if err := tx.QueryRow(c.Request.Context(), adjudicationHistoryCTE+`SELECT count(*) FROM history`+adjudicationHistoryFilter, args...).Scan(&total); err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := tx.Query(c.Request.Context(), adjudicationHistoryCTE+
		`SELECT to_jsonb(history)-'note'-'sourceReason' FROM history`+adjudicationHistoryFilter+
		`ORDER BY "createdAt" DESC,id::bigint DESC LIMIT $8 OFFSET $9`, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]json.RawMessage, 0)
	for rows.Next() {
		var item json.RawMessage
		if err := rows.Scan(&item); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeServiceError(c, err)
		return
	}
	rows.Close()
	if err := appendAudit(c, tx, "adjudication.history_read", "audit_log", "", nil, nil, gin.H{"count": len(items)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": page, "pageSize": pageSize})
}

func (s *Server) adminAdjudicationHistoryDetail(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	actor := mustActor(c)
	tx := mustTx(c)
	ctx := c.Request.Context()
	var raw []byte
	err := tx.QueryRow(ctx, adjudicationHistoryCTE+`SELECT to_jsonb(history) FROM history WHERE id=$3`,
		actor.UserID, actor.ClassID, strconv.FormatInt(id, 10)).Scan(&raw)
	if notFound(c, err, "处理记录") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var item gin.H
	if err := json.Unmarshal(raw, &item); err != nil {
		writeServiceError(c, err)
		return
	}
	// Use only the already-authorized row's resource IDs. Attachment metadata does
	// not include object keys, uploader identities or a reporter's identity.
	var evidence json.RawMessage
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'id',id::text,'name',filename,'mediaType',media_type,'sizeBytes',size_bytes,
		'sha256',sha256,'status',status,'uploadedAt',created_at) ORDER BY id),'[]'::jsonb)
		FROM evidence WHERE class_id=$1 AND status='ready' AND
		(submission_id=$2::bigint OR appeal_id=$3::bigint OR objection_id=$4::bigint OR report_id=$5::bigint
		 OR review_report_id=$5::bigint OR blind_assignment_id=$6::bigint)`,
		actor.ClassID, item["submissionId"], item["appealId"], item["objectionId"], item["reportId"], item["blindAssignmentId"]).Scan(&evidence)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	item["evidence"] = evidence
	var reviews json.RawMessage
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.at),'[]'::jsonb) FROM (
		SELECT u.name AS reviewer,'原审核' AS stage,r.decision,r.score::float8,r.reason,r.created_at AS at,
		       r.superseded_at IS NOT NULL AS superseded
		  FROM review r JOIN app_user u ON u.id=r.reviewer_id WHERE r.class_id=$1 AND r.submission_id=$2::bigint
		UNION ALL
		SELECT u.name,'申诉复评',r.decision,r.score::float8,r.reason,r.decided_at,false
		  FROM appeal_reviewer r JOIN app_user u ON u.id=r.reviewer_id
		 WHERE r.class_id=$1 AND r.appeal_id=$3::bigint AND r.decided_at IS NOT NULL
		UNION ALL
		SELECT u.name,'举报复核',r.decision,r.score::float8,r.reason,r.decided_at,false
		  FROM report_reviewer r JOIN app_user u ON u.id=r.reviewer_id
		 WHERE r.class_id=$1 AND r.report_id=$4::bigint AND r.decided_at IS NOT NULL
	) r`, actor.ClassID, item["submissionId"], item["appealId"], item["reportId"]).Scan(&reviews)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	item["reviews"] = reviews
	if err := appendAudit(c, tx, "adjudication.history_detail_read", "audit_log", strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}
