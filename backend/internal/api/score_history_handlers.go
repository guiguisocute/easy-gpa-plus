package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"easygpa/backend/internal/richtext"
	"easygpa/backend/internal/scheme"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// A separate, allowlisted projection: never send audit payloads, user IDs,
// reviewer IDs or storage keys to readers of another member's score history.
type scoreHistoryEvent struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	Status      string    `json:"status"`
	Score       *float64  `json:"score"`
	BeforeScore *float64  `json:"beforeScore"`
	Reason      string    `json:"reason"`
	Round       int       `json:"round"`
	Superseded  bool      `json:"superseded"`
	source      string
	sourceID    int64
	authorID    *int64
}

type scoreHistoryTarget struct {
	ID, StudentID, SchemeID int64
	Kind, Category, ItemKey string
}

func loadScoreHistoryTarget(ctx context.Context, tx pgx.Tx, kind string, id int64) (scoreHistoryTarget, error) {
	target := scoreHistoryTarget{ID: id, Kind: kind}
	var query string
	switch kind {
	case "submission":
		query = `SELECT s.student_id,s.scheme_id,s.category_key,s.item_key
		  FROM submission s JOIN app_user u ON u.id=s.student_id
		 WHERE s.id=$1 AND s.final_score IS NOT NULL
		   AND s.status IN ('scored','locked','appealing','arbitrating')`
	case "base", "penalty":
		query = `SELECT s.student_id,s.scheme_id,s.category_key,s.item_key
		  FROM base_score s JOIN app_user u ON u.id=s.student_id
		  JOIN scheme cfg ON cfg.id=s.scheme_id AND cfg.status='published'
		 WHERE s.id=$1 AND s.kind=$2
		 AND s.scheme_id=(SELECT id FROM scheme WHERE status='published' ORDER BY version DESC LIMIT 1)`
	default:
		return target, pgx.ErrNoRows
	}
	query += ` AND u.status='active' AND u.role IN ('student','group','class_admin')`
	args := []any{id}
	if kind != "submission" {
		args = append(args, kind)
	}
	err := tx.QueryRow(ctx, query, args...).Scan(&target.StudentID, &target.SchemeID, &target.Category, &target.ItemKey)
	return target, err
}

const scoreHistorySQL = `WITH appeals AS (
	SELECT * FROM appeal WHERE kind='student_appeal' AND target_id=$1
	 AND target_type=CASE $2 WHEN 'submission' THEN 'submission' WHEN 'base' THEN 'base_score' ELSE 'penalty_score' END
	 AND status<>'draft'
), objections AS (
	SELECT * FROM objection WHERE kind=$2 AND student_id=$3
	 AND (($2='submission' AND target_id=$1) OR ($2<>'submission' AND scheme_id=$4 AND category_key=$5 AND item_key=$6))
	 AND status IN ('applied','adjusted','dismissed')
), events AS (
	SELECT 'review'::text AS kind,r.created_at AS at,r.decision AS status,r.score::float8 AS score,
	 NULL::float8 AS before_score,r.reason,0 AS round,r.superseded_at IS NOT NULL AS superseded,
	 'submission'::text AS source,r.submission_id AS source_id,r.id AS sequence,r.reviewer_id AS author_id
	 FROM review r WHERE $2='submission' AND r.submission_id=$1
	 UNION ALL
	SELECT CASE l.action WHEN 'submission.collective_granted' THEN 'collective_grant' WHEN 'submission.admin_granted' THEN 'admin_grant' WHEN 'submission.arbitrated' THEN CASE WHEN l.metadata->>'adjudicatorRole'='collective' THEN 'collective_decision' ELSE 'arbitration' END WHEN 'submission.force_scored' THEN 'force_score'
	 WHEN 'submission.force_rejected' THEN CASE WHEN l.metadata->>'selfRejected'='true' THEN 'self_reject' ELSE 'force_reject' END END,
	 l.created_at,COALESCE(l.after_data->>'status','scored'),(l.after_data->>'finalScore')::float8,
	 (l.before_data->>'finalScore')::float8,COALESCE(l.metadata->>'reason',''),0,false,'submission',$1,l.id,l.actor_id
	 FROM audit_log l WHERE $2='submission' AND l.resource_type='submission' AND l.resource_id=$1::text
	 AND l.action IN ('submission.arbitrated','submission.force_rejected','submission.force_scored','submission.admin_granted','submission.collective_granted')
	 UNION ALL
	SELECT 'appeal_filed',a.created_at,a.status,a.proposed_score::float8,a.original_score::float8,
	 a.reason,a.round,false,'appeal',a.id,a.id,a.filed_by FROM appeals a
	 UNION ALL
	SELECT 'appeal_review',r.decided_at,r.decision,r.score::float8,a.original_score::float8,
	 r.reason,a.round,false,'appeal',a.id,r.position::bigint,r.reviewer_id
	 FROM appeals a JOIN appeal_reviewer r ON r.appeal_id=a.id
	 WHERE r.decided_at IS NOT NULL AND NOT EXISTS (
	 SELECT 1 FROM appeal_reviewer pending WHERE pending.appeal_id=a.id AND pending.decided_at IS NULL)
	 UNION ALL
	SELECT CASE WHEN a.status='withdrawn' THEN 'appeal_withdrawn' WHEN a.status='final' THEN 'appeal_final' ELSE 'appeal_resolved' END,
	 COALESCE(a.resolved_at,a.updated_at),a.status,a.resolution_score::float8,a.original_score::float8,
	 COALESCE(a.resolution_reason,''),a.round,false,'appeal',a.id,a.id,a.handler_id FROM appeals a
	 WHERE a.status IN ('resolved','final','withdrawn')
	 UNION ALL
	SELECT 'objection_filed',o.submitted_at,o.status,o.proposed_score::float8,o.current_score::float8,
	 o.basis,0,false,'objection',o.id,o.id,o.proposer_id FROM objections o
	 UNION ALL
	SELECT 'objection_decided',o.decided_at,o.status,o.decided_score::float8,o.current_score::float8,
	 COALESCE(o.decision_reason,''),0,false,'objection',o.id,o.id,o.decided_by FROM objections o
)
SELECT kind,at,status,score,before_score,reason,round,superseded,source,source_id,author_id
 FROM events ORDER BY at,source,source_id,sequence,kind`

func loadScoreHistory(ctx context.Context, tx pgx.Tx, target scoreHistoryTarget) ([]scoreHistoryEvent, error) {
	rows, err := tx.Query(ctx, scoreHistorySQL, target.ID, target.Kind, target.StudentID, target.SchemeID, target.Category, target.ItemKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]scoreHistoryEvent, 0)
	for rows.Next() {
		var event scoreHistoryEvent
		if err := rows.Scan(&event.Kind, &event.At, &event.Status, &event.Score, &event.BeforeScore, &event.Reason, &event.Round, &event.Superseded, &event.source, &event.sourceID, &event.authorID); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

var scoreHistoryEvidenceRef = regexp.MustCompile(`evidence:([0-9]+)`)
var scoreHistoryMarkdownLink = regexp.MustCompile(`!?\[[^\n]*?\]\([^\n]*?\)`)
var scoreHistoryMarkdownReference = regexp.MustCompile(`!?\[[^\n]*?\]\[[^\n]*?\]`)
var scoreHistoryReferenceLink = regexp.MustCompile(`(?m)^\s{0,3}\[[^\]\n]+\]:[^\n]*$`)
var scoreHistoryURL = regexp.MustCompile(`(?i)(?:https?://|evidence:)[^\s<>\)\]]+`)
var scoreHistoryHTMLTag = regexp.MustCompile(`<[^>\n]*>`)

// Student readers get no embedded file names, attachment IDs or external file
// URLs either. Publicity files are returned separately from these plain reasons.
func studentScoreHistoryReason(reason string) string {
	reason = scoreHistoryMarkdownLink.ReplaceAllString(reason, "（附件或链接已隐藏）")
	reason = scoreHistoryMarkdownReference.ReplaceAllString(reason, "（附件或链接已隐藏）")
	reason = scoreHistoryReferenceLink.ReplaceAllString(reason, "（附件或链接已隐藏）")
	reason = scoreHistoryHTMLTag.ReplaceAllString(reason, "（附件或链接已隐藏）")
	return richtext.PlainText(scoreHistoryURL.ReplaceAllString(reason, "（附件或链接已隐藏）"))
}

func scoreHistoryEvidence(ctx context.Context, tx pgx.Tx, target scoreHistoryTarget, events []scoreHistoryEvent) ([]gin.H, error) {
	appealIDs, objectionIDs := make([]int64, 0), make([]int64, 0)
	reasons := make(map[string]string)
	for _, event := range events {
		if event.authorID != nil {
			key := event.source + ":" + strconv.FormatInt(event.sourceID, 10) + ":" + strconv.FormatInt(*event.authorID, 10)
			reasons[key] += "\n" + event.Reason
		}
		if event.source == "appeal" {
			appealIDs = append(appealIDs, event.sourceID)
		} else if event.source == "objection" {
			objectionIDs = append(objectionIDs, event.sourceID)
		}
	}
	rows, err := tx.Query(ctx, `SELECT id,filename,media_type,size_bytes,status,created_at,kind,
	 CASE WHEN submission_id IS NOT NULL THEN 'submission' WHEN appeal_id IS NOT NULL THEN 'appeal' ELSE 'objection' END,
	 COALESCE(submission_id,appeal_id,objection_id),created_by
	 FROM evidence WHERE status='ready' AND (
	 ($2='submission' AND submission_id=$1) OR appeal_id=ANY($3::bigint[]) OR objection_id=ANY($4::bigint[]))
	 ORDER BY id`, target.ID, target.Kind, appealIDs, objectionIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]gin.H, 0)
	for rows.Next() {
		var id, size, sourceID int64
		var creatorID *int64
		var name, mediaType, status, kind, source string
		var at time.Time
		if err := rows.Scan(&id, &name, &mediaType, &size, &status, &at, &kind, &source, &sourceID, &creatorID); err != nil {
			return nil, err
		}
		if kind == "note" {
			if creatorID == nil {
				continue
			}
			published := false
			// A reader cannot publish somebody else's draft by guessing its ID
			// in their own reason, even when it belongs to the same matter.
			key := source + ":" + strconv.FormatInt(sourceID, 10) + ":" + strconv.FormatInt(*creatorID, 10)
			for _, match := range scoreHistoryEvidenceRef.FindAllStringSubmatch(reasons[key], -1) {
				if match[1] == strconv.FormatInt(id, 10) {
					published = true
					break
				}
			}
			if !published {
				continue
			}
		}
		files = append(files, gin.H{"id": strconv.FormatInt(id, 10), "name": name, "mediaType": mediaType, "sizeBytes": size, "status": status, "uploadedAt": at})
	}
	return files, rows.Err()
}

func (s *Server) studentScoreHistory(c *gin.Context)  { s.itemScoreHistory(c, false) }
func (s *Server) reviewerScoreHistory(c *gin.Context) { s.itemScoreHistory(c, true) }

func (s *Server) itemScoreHistory(c *gin.Context, attachments bool) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	tx := mustTx(c)
	var publicity *scheme.PublicityWindow
	now := time.Now()
	if !attachments {
		current, err := loadCurrentScheme(c.Request.Context(), tx)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		if current.Config.Window.PublicityOpen(now) {
			publicity = current.Config.Window.Publicity
		}
		if !current.Config.Capabilities.StudentReport && publicity == nil {
			writeError(c, http.StatusForbidden, "report_closed", "本班没有开放学生举报", nil)
			return
		}
	}
	target, err := loadScoreHistoryTarget(c.Request.Context(), tx, c.Param("kind"), id)
	if notFound(c, err, "计分条目") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	events, err := loadScoreHistory(c.Request.Context(), tx, target)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	files := make([]gin.H, 0)
	if attachments || publicity != nil {
		files, err = scoreHistoryEvidence(c.Request.Context(), tx, target, events)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	if !attachments {
		for i := range events {
			events[i].Reason = studentScoreHistoryReason(events[i].Reason)
		}
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "evidence": files, "publicity": publicity, "serverNow": now})
}

// Download authorization uses exactly the same published-file projection as
// the history response, including the peer-review reveal boundary.
func groupScoreHistoryEvidence(ctx context.Context, tx pgx.Tx, evidenceID int64) (bool, error) {
	var id int64
	var kind string
	err := tx.QueryRow(ctx, `SELECT COALESCE(e.submission_id,a.target_id,o.target_id,b.id,0),
	 CASE WHEN e.submission_id IS NOT NULL THEN 'submission'
	 WHEN a.id IS NOT NULL THEN CASE a.target_type WHEN 'submission' THEN 'submission' WHEN 'base_score' THEN 'base' ELSE 'penalty' END
	 ELSE o.kind END
	 FROM evidence e LEFT JOIN appeal a ON a.id=e.appeal_id
	 LEFT JOIN objection o ON o.id=e.objection_id
	 LEFT JOIN base_score b ON o.kind<>'submission' AND b.student_id=o.student_id AND b.scheme_id=o.scheme_id
	 AND b.category_key=o.category_key AND b.item_key=o.item_key AND b.kind=o.kind
	 WHERE e.id=$1 AND (e.submission_id IS NOT NULL OR a.kind='student_appeal' OR o.status IN ('applied','adjusted','dismissed'))`, evidenceID).Scan(&id, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	target, err := loadScoreHistoryTarget(ctx, tx, kind, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	events, err := loadScoreHistory(ctx, tx, target)
	if err != nil {
		return false, err
	}
	files, err := scoreHistoryEvidence(ctx, tx, target, events)
	for _, file := range files {
		if file["id"] == strconv.FormatInt(evidenceID, 10) {
			return true, nil
		}
	}
	return false, err
}
