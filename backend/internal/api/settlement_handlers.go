package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/events"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/settle"
)

// settlementConfigSnapshot is the only place the runtime envelope is written
// back out as JSON. scheme.Config drops those three fields on purpose (they
// belong to class_timeline, not to a scheme version), but a settlement is a
// frozen record and has to state which window and which 评优 policy produced
// it. Embedding re-adds them beside the scoring rules under their usual keys,
// so an old snapshot and a new one deserialize identically.
type settlementConfigSnapshot struct {
	scheme.Config
	Window       scheme.Window       `json:"window"`
	Capabilities scheme.Capabilities `json:"capabilities"`
	HonorRoll    scheme.HonorRoll    `json:"honorRoll"`
}

func marshalSettlementValue(name string, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal settlement %s: %w", name, err)
	}
	return raw, nil
}

type gateState struct {
	Gate                       settle.Gate
	Current                    storedScheme
	StudentCount               int
	SealedCount                int
	PendingCount               int
	UnfinalizedReviewCount     int
	PendingClassificationCount int
	BlindAuditStatus           string
	BlindAuditComplete         bool
	ConfirmedCount             int
	GPACount                   int
	OverrideID                 *int64
	ForceReason                *string
	ForcedAt                   *time.Time
}

func evaluateGate(ctx context.Context, tx pgx.Tx, now time.Time) (gateState, error) {
	current, err := loadCurrentScheme(ctx, tx)
	if err != nil {
		return gateState{}, err
	}
	state := gateState{Current: current}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM app_user WHERE status='active'`).Scan(&state.StudentCount); err != nil {
		return gateState{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT u.id)
		  FROM app_user u JOIN seal s ON s.student_id=u.id AND s.unsealed_at IS NULL
		 WHERE u.status='active'
	`).Scan(&state.SealedCount); err != nil {
		return gateState{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)
		     FROM appeal a
		     LEFT JOIN submission target_submission
		       ON a.target_type='submission' AND target_submission.id=a.target_id
		     LEFT JOIN base_score target_base
		       ON a.target_type IN ('base_score','penalty_score') AND target_base.id=a.target_id
		    WHERE a.status IN ('filed','reviewing','escalated')
		      AND (target_submission.id IS NOT NULL OR target_base.scheme_id=$1))
		  +
		  (SELECT count(*) FROM submission s
		    WHERE s.status='arbitrating'
		      AND NOT EXISTS (
		        SELECT 1 FROM appeal a
		         WHERE a.target_type='submission' AND a.target_id=s.id
		           AND a.status IN ('filed','reviewing','escalated')
		      ))
		  +
		  (SELECT count(*) FROM objection o
		    WHERE (o.kind='submission' OR o.scheme_id=$1) AND o.status='submitted')
        + (SELECT count(*) FROM report WHERE status IN ('reviewing','escalated'))
	`, current.ID).Scan(&state.PendingCount); err != nil {
		return gateState{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM student_gpa WHERE scheme_id=$1`, current.ID).Scan(&state.GPACount); err != nil {
		return gateState{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM submission
		 WHERE status<>'draft' AND (status NOT IN ('scored','locked') OR final_score IS NULL)
	`).Scan(&state.UnfinalizedReviewCount); err != nil {
		return gateState{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM classification_suggestion WHERE status='pending'`).Scan(&state.PendingClassificationCount); err != nil {
		return gateState{}, err
	}
	err = tx.QueryRow(ctx, `
		SELECT id,reason,created_at FROM gate_override WHERE scheme_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1
	`, current.ID).Scan(&state.OverrideID, &state.ForceReason, &state.ForcedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return gateState{}, err
	}
	forced := state.OverrideID != nil
	var collective bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM class_governance WHERE mode IN ('enrolling','collective'))`).Scan(&collective); err != nil {
		return gateState{}, err
	}
	if collective {
		forced = false
		state.OverrideID, state.ForceReason, state.ForcedAt = nil, nil, nil
		var pending int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM governance_proposal WHERE status IN ('discussion','voting','passed','blocked','deliberating') AND action NOT IN ('motion','export')`).Scan(&pending); err != nil {
			return gateState{}, err
		}
		state.PendingCount += pending
	}
	state.Gate = settle.EvaluateGate(settle.GateInput{
		StudentCount: state.StudentCount, SealedCount: state.SealedCount,
		WindowClose: current.Config.Window.Close, Now: now,
		UnfinalizedReviews:     state.UnfinalizedReviewCount,
		PendingClassifications: state.PendingClassificationCount,
		BlindAuditComplete:     state.BlindAuditComplete,
		ConfirmedCount:         state.ConfirmedCount,
		PendingConflicts:       state.PendingCount, GPAImportedCount: state.GPACount, Forced: forced,
	})
	return state, nil
}

func gateResponse(state gateState) gin.H {
	return gin.H{
		"open": state.Gate.Open, "forced": state.Gate.Forced, "conditions": state.Gate.Conditions,
		"studentCount": state.StudentCount, "sealedCount": state.SealedCount,
		"pendingConflicts": state.PendingCount, "gpaImportedCount": state.GPACount,
		"unfinalizedReviews":     state.UnfinalizedReviewCount,
		"pendingClassifications": state.PendingClassificationCount,
		"blindAuditStatus":       state.BlindAuditStatus, "blindAuditComplete": state.BlindAuditComplete,
		"confirmedCount": state.ConfirmedCount,
		"schemeId":       strconv.FormatInt(state.Current.ID, 10), "schemeVersion": state.Current.Config.Version,
		"forceReason": state.ForceReason, "forcedAt": state.ForcedAt,
	}
}

func (s *Server) adminGate(c *gin.Context) {
	tx := mustTx(c)
	state, err := evaluateGate(c.Request.Context(), tx, time.Now())
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "gate.read", "gate", strconv.FormatInt(state.Current.ID, 10), nil, nil, map[string]any{"open": state.Gate.Open}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gateResponse(state))
}

func (s *Server) forceGate(c *gin.Context) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "强制开闸参数不正确", nil)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if len(input.Reason) < 8 || len(input.Reason) > 2000 {
		writeError(c, http.StatusUnprocessableEntity, "reason_required", "强制开闸必须填写 8—2000 字理由", nil)
		return
	}
	tx := mustTx(c)
	actor := mustActor(c)
	state, err := evaluateGate(c.Request.Context(), tx, time.Now())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if state.OverrideID != nil {
		writeError(c, http.StatusConflict, "already_forced", "当前方案已经强制开闸", nil)
		return
	}
	var id int64
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO gate_override (class_id,scheme_id,reason,created_by) VALUES ($1,$2,$3,$4) RETURNING id
	`, actor.ClassID, state.Current.ID, input.Reason, actor.UserID).Scan(&id); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := appendAudit(c, tx, "gate.forced", "gate", strconv.FormatInt(state.Current.ID, 10), gateResponse(state), map[string]any{"forced": true}, map[string]any{"reason": input.Reason, "overrideId": id}); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := enqueueEvent(c.Request.Context(), tx, actor.ClassID, events.GateForced, map[string]any{"schemeId": state.Current.ID, "overrideId": id, "reason": input.Reason}); err != nil {
		writeServiceError(c, err)
		return
	}
	state.OverrideID = &id
	state.ForceReason = &input.Reason
	state.Gate.Forced = true
	state.Gate.Open = true
	c.JSON(http.StatusOK, gateResponse(state))
}

type settlementBuild struct {
	Inputs  []settle.StudentInput
	Details map[int64]map[string]any
}

func buildSettlement(ctx context.Context, tx pgx.Tx, current storedScheme) (settlementBuild, error) {
	build := settlementBuild{Details: make(map[int64]map[string]any)}
	rows, err := tx.Query(ctx, `SELECT id,sid,name FROM app_user WHERE status='active' ORDER BY sid`)
	if err != nil {
		return build, err
	}
	studentIndex := make(map[int64]int)
	for rows.Next() {
		var input settle.StudentInput
		if err := rows.Scan(&input.UserID, &input.SID, &input.Name); err != nil {
			rows.Close()
			return build, err
		}
		input.CategoryScores = make(map[string]scheme.Points, len(current.Config.Weights))
		for key := range current.Config.Weights {
			input.CategoryScores[key] = 0
		}
		studentIndex[input.UserID] = len(build.Inputs)
		build.Inputs = append(build.Inputs, input)
		build.Details[input.UserID] = map[string]any{"categories": map[string]any{}, "items": []any{}}
	}
	rows.Close()
	if len(build.Inputs) == 0 {
		return build, errors.New("班级没有可结算的有效账号")
	}

	gpaSeen := make(map[int64]bool)
	rows, err = tx.Query(ctx, `SELECT student_id,score::float8,details FROM student_gpa WHERE scheme_id=$1`, current.ID)
	if err != nil {
		return build, err
	}
	for rows.Next() {
		var userID int64
		var score float64
		var details []byte
		if err := rows.Scan(&userID, &score, &details); err != nil {
			rows.Close()
			return build, err
		}
		if index, ok := studentIndex[userID]; ok {
			build.Inputs[index].CategoryScores["major"] = scheme.NewPoints(score)
			build.Details[userID]["gpa"] = json.RawMessage(details)
			gpaSeen[userID] = true
		}
	}
	rows.Close()
	for _, input := range build.Inputs {
		if !gpaSeen[input.UserID] {
			build.Details[input.UserID]["gpaMissing"] = true
		}
	}

	items := make(map[int64]map[string][]scheme.ScoredItem)
	rows, err = tx.Query(ctx, `
		SELECT id,student_id,filed_category_key,filed_item_key,category_key,item_key,title,final_score::float8,status,rule_snapshot,force_rejection,forced_score - 'actorName',
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'reviewer',u.name,'reviewerSid',u.sid,'decision',r.decision,'score',r.score::float8,
		           'reason',r.reason,'spentSeconds',r.spent_seconds,'at',r.created_at
		         ) ORDER BY r.created_at,r.id)
		           FROM review r JOIN app_user u ON u.id=r.reviewer_id
		          WHERE r.submission_id=submission.id AND r.superseded_at IS NULL
		       ),'[]'::jsonb),
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',a.id::text,'kind',a.kind,'round',a.round,'reason',a.reason,'status',a.status,
		           'originalScore',a.original_score::float8,'proposedScore',a.proposed_score::float8,
		           'resolutionScore',a.resolution_score::float8,
		           'resolutionReason',a.resolution_reason,'createdAt',a.created_at,'resolvedAt',a.resolved_at
		         ) ORDER BY a.created_at,a.id)
		           FROM appeal a WHERE a.target_type='submission' AND a.target_id=submission.id AND a.status<>'draft'
		       ),'[]'::jsonb),
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',cr.id::text,'beforeCategory',cr.before_category_key,'beforeItemKey',cr.before_item_key,
		           'afterCategory',cr.after_category_key,'afterItemKey',cr.after_item_key,
		           'beforeScore',cr.before_score::float8,'afterScore',cr.after_score::float8,
		           'reason',cr.reason,'decidedBy',u.name,'createdAt',cr.created_at
		         ) ORDER BY cr.created_at,cr.id)
		           FROM classification_resolution cr JOIN app_user u ON u.id=cr.decided_by
		          WHERE cr.submission_id=submission.id
		       ),'[]'::jsonb)
		  FROM submission
		 WHERE status IN ('scored','locked') AND final_score IS NOT NULL
		 ORDER BY student_id,category_key,id
	`)
	if err != nil {
		return build, err
	}
	for rows.Next() {
		var id, userID int64
		var filedCategory, filedItem, category, itemKey, title, status string
		var scoreValue float64
		var snapshotRaw, reviewsRaw, appealsRaw, classificationRaw, forceRejectionRaw, forcedScoreRaw []byte
		if err := rows.Scan(&id, &userID, &filedCategory, &filedItem, &category, &itemKey, &title, &scoreValue, &status, &snapshotRaw, &forceRejectionRaw, &forcedScoreRaw, &reviewsRaw, &appealsRaw, &classificationRaw); err != nil {
			rows.Close()
			return build, err
		}
		if _, ok := studentIndex[userID]; !ok || category == "major" {
			continue
		}
		var snapshot ruleSnapshot
		if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
			rows.Close()
			return build, err
		}
		item := scoredSubmissionItem(id, itemKey, scoreValue, snapshot)
		if items[userID] == nil {
			items[userID] = make(map[string][]scheme.ScoredItem)
		}
		items[userID][category] = append(items[userID][category], item)
		detailItems := build.Details[userID]["items"].([]any)
		build.Details[userID]["items"] = append(detailItems, map[string]any{
			"id": strconv.FormatInt(id, 10), "filedCategory": filedCategory, "filedItemKey": filedItem,
			"category": category, "itemKey": itemKey, "title": title,
			"score": scoreValue, "status": status, "ruleSnapshot": json.RawMessage(snapshotRaw),
			"reviews": json.RawMessage(reviewsRaw), "appeals": json.RawMessage(appealsRaw),
			"classificationHistory": json.RawMessage(classificationRaw),
			"forceRejection":        json.RawMessage(forceRejectionRaw),
			"forcedScore":           json.RawMessage(forcedScoreRaw),
		})
	}
	rows.Close()

	base := make(map[int64]map[string]map[string]scheme.RecordedBase)
	baseDetails := make(map[int64]map[string]map[string]map[string]any)
	claimableBaseKeys := make(map[string]bool)
	for _, category := range current.Config.Categories {
		for _, item := range category.BaseItems {
			if item.StudentClaim != nil {
				claimableBaseKeys[category.Key+"\x00"+item.Key] = true
			}
		}
	}
	for _, input := range build.Inputs {
		base[input.UserID] = make(map[string]map[string]scheme.RecordedBase)
		baseDetails[input.UserID] = make(map[string]map[string]map[string]any)
		for _, category := range current.Config.Categories {
			base[input.UserID][category.Key] = make(map[string]scheme.RecordedBase)
			baseDetails[input.UserID][category.Key] = make(map[string]map[string]any)
			for _, item := range category.BaseItems {
				if item.StudentClaim != nil {
					continue
				}
				base[input.UserID][category.Key][item.Key] = scheme.RecordedBase{ItemKey: item.Key, Kind: "base", Score: scheme.NewPoints(item.Full)}
				baseDetails[input.UserID][category.Key]["base\x00"+item.Key] = map[string]any{
					"category": category.Key, "itemKey": item.Key, "name": item.Name, "kind": "base",
					"fullScore": item.Full, "score": item.Full, "basis": "方案默认满分", "recorded": false,
				}
			}
			for _, item := range category.PenaltyItems {
				baseDetails[input.UserID][category.Key]["penalty\x00"+item.Key] = map[string]any{
					"category": category.Key, "itemKey": item.Key, "name": item.Name, "kind": "penalty",
					"fullScore": nil, "score": 0, "basis": "暂无扣分记录", "recorded": false,
				}
			}
		}
	}
	rows, err = tx.Query(ctx, `
		SELECT b.id,b.student_id,b.category_key,b.item_key,b.kind,b.full_score::float8,b.score::float8,b.basis,
		       recorder.name,recorder.sid,
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',a.id::text,'kind',a.kind,'round',a.round,'reason',a.reason,'status',a.status,
		           'originalScore',a.original_score::float8,'proposedScore',a.proposed_score::float8,
		           'resolutionScore',a.resolution_score::float8,
		           'resolutionReason',a.resolution_reason,'createdAt',a.created_at,'resolvedAt',a.resolved_at
		         ) ORDER BY a.created_at,a.id)
		           FROM appeal a
		          WHERE a.target_id=b.id AND a.target_type=CASE WHEN b.kind='base' THEN 'base_score' ELSE 'penalty_score' END
		            AND a.status<>'draft'
		       ),'[]'::jsonb)
		  FROM base_score b JOIN app_user recorder ON recorder.id=b.recorded_by
		 WHERE b.scheme_id=$1 ORDER BY b.student_id,b.category_key,b.item_key
	`, current.ID)
	if err != nil {
		return build, err
	}
	for rows.Next() {
		var id, userID int64
		var category, itemKey, kind, basisText, recorderName, recorderSID string
		var fullScore *float64
		var scoreValue float64
		var appealsRaw []byte
		if err := rows.Scan(&id, &userID, &category, &itemKey, &kind, &fullScore, &scoreValue, &basisText, &recorderName, &recorderSID, &appealsRaw); err != nil {
			rows.Close()
			return build, err
		}
		if base[userID] == nil {
			continue
		}
		if kind == "base" && claimableBaseKeys[category+"\x00"+itemKey] {
			continue
		}
		if base[userID][category] == nil {
			base[userID][category] = make(map[string]scheme.RecordedBase)
		}
		base[userID][category][itemKey] = scheme.RecordedBase{ItemKey: itemKey, Kind: kind, Score: scheme.NewPoints(scoreValue)}
		prior := baseDetails[userID][category][kind+"\x00"+itemKey]
		name := itemKey
		if prior != nil {
			if value, ok := prior["name"].(string); ok {
				name = value
			}
		}
		baseDetails[userID][category][kind+"\x00"+itemKey] = map[string]any{
			"id": strconv.FormatInt(id, 10), "category": category, "itemKey": itemKey, "name": name,
			"kind": kind, "fullScore": fullScore, "score": scoreValue, "basis": basisText, "recorded": true,
			"recorder": recorderName, "recorderSid": recorderSID, "appeals": json.RawMessage(appealsRaw),
		}
	}
	rows.Close()

	for index := range build.Inputs {
		input := &build.Inputs[index]
		categoryDetails := build.Details[input.UserID]["categories"].(map[string]any)
		baseDetailRows := make([]any, 0)
		for _, category := range current.Config.Categories {
			if category.Key == "major" {
				continue
			}
			recordMap := base[input.UserID][category.Key]
			records := make([]scheme.RecordedBase, 0, len(recordMap))
			keys := make([]string, 0, len(recordMap))
			for key := range recordMap {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				records = append(records, recordMap[key])
			}
			breakdown := scheme.ComputeCategoryScore(items[input.UserID][category.Key], records, scheme.NewPoints(category.MaxTotal))
			input.CategoryScores[category.Key] = breakdown.Total
			categoryDetails[category.Key] = map[string]any{
				"itemTotal": breakdown.ItemTotal.Float64(), "baseTotal": breakdown.BaseTotal.Float64(),
				"penaltyTotal": breakdown.PenaltyTotal.Float64(), "beforeCap": breakdown.BeforeCap.Float64(),
				"total": breakdown.Total.Float64(), "maxTotal": category.MaxTotal,
			}
			detailKeys := make([]string, 0, len(baseDetails[input.UserID][category.Key]))
			for key := range baseDetails[input.UserID][category.Key] {
				detailKeys = append(detailKeys, key)
			}
			sort.Strings(detailKeys)
			for _, key := range detailKeys {
				baseDetailRows = append(baseDetailRows, baseDetails[input.UserID][category.Key][key])
			}
		}
		build.Details[input.UserID]["baseItems"] = baseDetailRows
	}
	return build, nil
}

func settlementRows(ctx context.Context, tx pgx.Tx, runID int64) ([]gin.H, error) {
	rows, err := tx.Query(ctx, `
		SELECT s.student_id,u.sid,u.name,s.category_scores,s.total_score::float8,s.class_rank,s.major_rank,s.honor,
		       COALESCE(s.award_tier,''),s.details
		  FROM settlement s JOIN app_user u ON u.id=s.student_id
		 WHERE s.run_id=$1 ORDER BY s.class_rank,u.sid
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var userID int64
		var sid, name, awardTier string
		var categories, details []byte
		var total float64
		var classRank, majorRank int
		var honor bool
		if err := rows.Scan(&userID, &sid, &name, &categories, &total, &classRank, &majorRank, &honor, &awardTier, &details); err != nil {
			return nil, err
		}
		items = append(items, gin.H{
			"userId": strconv.FormatInt(userID, 10), "sid": sid, "name": name,
			"categoryScores": json.RawMessage(categories), "totalScore": total,
			"classRank": classRank, "majorRank": majorRank, "honor": honor,
			"awardTier": awardTier, "details": json.RawMessage(details),
		})
	}
	return items, rows.Err()
}

type settlementResult struct {
	RunID       int64
	CompletedAt time.Time
	Reused      bool
	Items       []gin.H
}

type gateClosedError struct {
	Details gin.H
}

func (e *gateClosedError) Error() string { return "settlement gate is closed" }

func (s *Server) runSettlement(c *gin.Context) {
	result, err := runSettlementTx(c.Request.Context(), mustTx(c), mustActor(c), c.ClientIP(), c.Request.UserAgent(), time.Now())
	if err != nil {
		var closed *gateClosedError
		if errors.As(err, &closed) {
			writeError(c, http.StatusConflict, "gate_closed", "结算闸门尚未开启", closed.Details)
			return
		}
		writeServiceError(c, err)
		return
	}
	body := gin.H{"runId": strconv.FormatInt(result.RunID, 10), "completedAt": result.CompletedAt, "reused": result.Reused, "items": result.Items}
	if result.Reused {
		c.JSON(http.StatusOK, body)
		return
	}
	c.JSON(http.StatusCreated, body)
}

func runSettlementTx(ctx context.Context, tx pgx.Tx, actor Actor, ip, userAgent string, now time.Time) (settlementResult, error) {
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended('easygpa:settlement:' || $1::bigint::text, 0))
	`, actor.ClassID); err != nil {
		return settlementResult{}, err
	}
	state, err := evaluateGate(ctx, tx, now)
	if err != nil {
		return settlementResult{}, err
	}
	if !state.Gate.Open {
		return settlementResult{}, &gateClosedError{Details: gateResponse(state)}
	}
	var existingID int64
	var existingAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT r.id,r.completed_at
		  FROM settlement_run r
		 WHERE r.scheme_id=$1 AND r.status='complete'
		   AND NOT EXISTS (SELECT 1 FROM settlement_invalidation i WHERE i.run_id=r.id)
		 ORDER BY r.created_at DESC,r.id DESC LIMIT 1
	`, state.Current.ID).Scan(&existingID, &existingAt)
	if err == nil {
		items, err := settlementRows(ctx, tx, existingID)
		if err != nil {
			return settlementResult{}, err
		}
		return settlementResult{RunID: existingID, CompletedAt: existingAt, Reused: true, Items: items}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return settlementResult{}, err
	}
	build, err := buildSettlement(ctx, tx, state.Current)
	if err != nil {
		return settlementResult{}, err
	}
	snapshots, err := settle.Compute(build.Inputs, state.Current.Config.Weights, state.Current.Config.HonorRoll)
	if err != nil {
		return settlementResult{}, err
	}
	triggerKind := "automatic"
	if state.Gate.Forced {
		triggerKind = "forced"
	}
	var runID int64
	gateSnapshot, err := marshalSettlementValue("gate snapshot", gateResponse(state))
	if err != nil {
		return settlementResult{}, err
	}
	// 快照要连着"这次结算用的是哪条三好线、哪几档奖学金、什么窗口"一起冻住。
	// state.Current.Raw 只是 scheme.config，里面已经没有这些了——运行期设置在
	// class_timeline 上，会随时被改。所以这里显式合成，而不是存原始 JSONB。
	configSnapshot, err := marshalSettlementValue("config snapshot", settlementConfigSnapshot{
		Config:       state.Current.Config,
		Window:       state.Current.Config.Window,
		Capabilities: state.Current.Config.Capabilities,
		HonorRoll:    state.Current.Config.HonorRoll,
	})
	if err != nil {
		return settlementResult{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO settlement_run (class_id,scheme_id,status,trigger_kind,triggered_by,config_snapshot,gate_snapshot)
		VALUES ($1,$2,'running',$3,$4,$5,$6) RETURNING id
	`, actor.ClassID, state.Current.ID, triggerKind, actor.UserID, configSnapshot, gateSnapshot).Scan(&runID)
	if err != nil {
		return settlementResult{}, err
	}
	for _, snapshot := range snapshots {
		categories, err := marshalSettlementValue("category scores", snapshot.CategoryScores)
		if err != nil {
			return settlementResult{}, err
		}
		details, err := marshalSettlementValue("student details", build.Details[snapshot.UserID])
		if err != nil {
			return settlementResult{}, err
		}
		// 空档位存 NULL 而不是空串：这一行"没进档"，和"档位未知"是同一种事实的
		// 两种说法，别在库里造出第三种。
		var awardTier *string
		if snapshot.AwardTier != "" {
			awardTier = &snapshot.AwardTier
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO settlement
			    (class_id,run_id,student_id,category_scores,total_score,class_rank,major_rank,honor,award_tier,details)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, actor.ClassID, runID, snapshot.UserID, categories, snapshot.TotalScore,
			snapshot.ClassRank, snapshot.MajorRank, snapshot.Honor, awardTier, details); err != nil {
			return settlementResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE submission SET status='locked',locked_at=now(),updated_at=now(),lock_version=lock_version+1
		 WHERE status='scored'
	`); err != nil {
		return settlementResult{}, err
	}
	var completedAt time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE settlement_run SET status='complete',completed_at=now() WHERE id=$1 RETURNING completed_at
	`, runID).Scan(&completedAt); err != nil {
		return settlementResult{}, err
	}
	if err := appendAuditRecord(ctx, tx, actor, ip, userAgent, "settlement.completed", "settlement_run", strconv.FormatInt(runID, 10), nil, map[string]any{"students": len(snapshots), "triggerKind": triggerKind}, gateResponse(state)); err != nil {
		return settlementResult{}, err
	}
	if err := enqueueEvent(ctx, tx, actor.ClassID, events.SettlementDone, map[string]any{"runId": runID, "students": len(snapshots), "triggerKind": triggerKind}); err != nil {
		return settlementResult{}, err
	}
	items, err := settlementRows(ctx, tx, runID)
	if err != nil {
		return settlementResult{}, err
	}
	return settlementResult{RunID: runID, CompletedAt: completedAt, Items: items}, nil
}

func (s *Server) myScore(c *gin.Context) {
	tx := mustTx(c)
	actor := mustActor(c)
	current, err := loadCurrentScheme(c.Request.Context(), tx)
	if notFound(c, err, "当前方案") {
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	live, err := loadCurrentScore(c.Request.Context(), tx, current, actor.UserID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	live.SchemeID = strconv.FormatInt(current.ID, 10)
	live.CalculatedAt = time.Now()
	var runID int64
	var schemeID int64
	var completedAt time.Time
	var configRaw []byte
	var stale bool
	err = tx.QueryRow(c.Request.Context(), `
		SELECT r.id,r.scheme_id,r.completed_at,r.config_snapshot,
		       EXISTS (SELECT 1 FROM settlement_invalidation i WHERE i.run_id=r.id)
		  FROM settlement_run r WHERE r.status='complete' AND r.scheme_id=$1
		 ORDER BY r.created_at DESC,r.id DESC LIMIT 1
	`, current.ID).Scan(&runID, &schemeID, &completedAt, &configRaw, &stale)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, gin.H{"settled": false, "current": live})
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var categoriesRaw, detailsRaw []byte
	var total float64
	var classRank, majorRank int
	var honor bool
	var awardTier string
	err = tx.QueryRow(c.Request.Context(), `
		SELECT category_scores,total_score::float8,class_rank,major_rank,honor,COALESCE(award_tier,''),details
		  FROM settlement WHERE run_id=$1 AND student_id=$2
	`, runID, actor.UserID).Scan(&categoriesRaw, &total, &classRank, &majorRank, &honor, &awardTier, &detailsRaw)
	// A member added after settlement has current scores but no frozen row.
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, gin.H{"settled": false, "current": live})
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// 各维度名次由 settle 一处算定，学生页、导出和结算器读的是同一个函数。
	rows, err := tx.Query(c.Request.Context(), `
		SELECT student_id,category_scores FROM settlement s JOIN app_user u ON u.id=s.student_id
		 WHERE run_id=$1 ORDER BY total_score DESC,COALESCE((category_scores->>'major')::numeric,0) DESC,u.sid
	`, runID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	all := make([]settle.Ranked, 0)
	awardPosition := 0
	for rows.Next() {
		var row settle.Ranked
		var raw []byte
		if err := rows.Scan(&row.UserID, &raw); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		if err := json.Unmarshal(raw, &row.Scores); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		all = append(all, row)
		if row.UserID == actor.UserID {
			awardPosition = len(all)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	var frozenPolicy struct {
		HonorRoll scheme.HonorRoll `json:"honorRoll"`
	}
	if err := json.Unmarshal(configRaw, &frozenPolicy); err != nil {
		writeServiceError(c, err)
		return
	}
	categoryRanks := settle.CategoryRanks(all)[actor.UserID]
	// awardQuota 是这次结算实际发出去的获奖人数，不是"班级人数 × 三好线"。
	// 两者在多数班额下相等，但名额是每档四舍五入后相加的，前端再推一遍就会
	// 在某些人数上和快照对不上——所以这里直接数快照。
	var minimum, median, maximum float64
	var awardQuota int
	if err := tx.QueryRow(c.Request.Context(), `
		SELECT min(total_score)::float8,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY total_score)::float8,
		       max(total_score)::float8,
		       count(*) FILTER (WHERE award_tier IS NOT NULL)::int
		  FROM settlement WHERE run_id=$1
	`, runID).Scan(&minimum, &median, &maximum, &awardQuota); err != nil {
		writeServiceError(c, err)
		return
	}
	studentDetails, err := studentSettlementDetails(detailsRaw)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// Reading the overview does not change scores or create audit entries.
	c.JSON(http.StatusOK, gin.H{
		"current": live,
		"settled": true, "stale": stale, "runId": strconv.FormatInt(runID, 10), "schemeId": strconv.FormatInt(schemeID, 10),
		"completedAt": completedAt, "categoryScores": json.RawMessage(categoriesRaw), "categoryRanks": categoryRanks,
		"totalScore": total, "classRank": classRank, "majorRank": majorRank, "honor": honor,
		"awardTier": awardTier,
		"classSize": len(all), "awardQuota": awardQuota,
		"awardPosition": awardPosition, "honorTopPercent": frozenPolicy.HonorRoll.TopPercent,
		"distribution": gin.H{"min": minimum, "median": median, "max": maximum},
		"details":      studentDetails, "configSnapshot": json.RawMessage(configRaw),
	})
}

func (s *Server) adminStats(c *gin.Context) {
	tx := mustTx(c)
	state, err := evaluateGate(c.Request.Context(), tx, time.Now())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	statusCounts := make(map[string]int)
	rows, err := tx.Query(c.Request.Context(), `SELECT status,count(*) FROM submission GROUP BY status ORDER BY status`)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			writeServiceError(c, err)
			return
		}
		statusCounts[status] = count
	}
	rows.Close()
	var reviewCount, activeAppeals int
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM review WHERE superseded_at IS NULL`).Scan(&reviewCount); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM appeal WHERE status IN ('filed','reviewing','escalated')`).Scan(&activeAppeals); err != nil {
		writeServiceError(c, err)
		return
	}
	var latestRunID *int64
	var latestCompletedAt *time.Time
	var settlementStale bool
	err = tx.QueryRow(c.Request.Context(), `
		SELECT r.id,r.completed_at,EXISTS (SELECT 1 FROM settlement_invalidation i WHERE i.run_id=r.id)
		  FROM settlement_run r WHERE r.status='complete' ORDER BY r.created_at DESC,r.id DESC LIMIT 1
	`).Scan(&latestRunID, &latestCompletedAt, &settlementStale)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeServiceError(c, err)
		return
	}
	var distribution any
	if latestRunID != nil {
		var minimum, average, median, maximum float64
		if err := tx.QueryRow(c.Request.Context(), `
			SELECT min(total_score)::float8,avg(total_score)::float8,
			       percentile_cont(0.5) WITHIN GROUP (ORDER BY total_score)::float8,max(total_score)::float8
			  FROM settlement WHERE run_id=$1
		`, *latestRunID).Scan(&minimum, &average, &median, &maximum); err != nil {
			writeServiceError(c, err)
			return
		}
		distribution = gin.H{"min": minimum, "average": math.Round(average*1000) / 1000, "median": median, "max": maximum}
	}
	settlementInfo := gin.H{"runId": nil, "completedAt": nil, "stale": false}
	if latestRunID != nil {
		settlementInfo["runId"] = strconv.FormatInt(*latestRunID, 10)
		settlementInfo["completedAt"] = latestCompletedAt
		settlementInfo["stale"] = settlementStale
	}
	itemProgress := gin.H{}
	progressRows, progressErr := tx.Query(c.Request.Context(), `
		SELECT CASE
		 WHEN s.status IN ('scored','locked') THEN 'complete'
		 WHEN NOT EXISTS (SELECT 1 FROM submission_reviewer sr WHERE sr.submission_id=s.id AND sr.active) THEN 'unassigned'
		 WHEN s.status='arbitrating' THEN 'pending_admin'
		 ELSE CONCAT((SELECT count(*) FROM review r WHERE r.submission_id=s.id AND r.superseded_at IS NULL),'/2') END, count(*)
		  FROM submission s WHERE s.status<>'draft' GROUP BY 1
	`)
	if progressErr == nil {
		for progressRows.Next() {
			var status string
			var count int
			if err := progressRows.Scan(&status, &count); err == nil {
				itemProgress[status] = count
			}
		}
		progressRows.Close()
	}
	var itemOverdue int
	_ = tx.QueryRow(c.Request.Context(), `
		SELECT count(DISTINCT s.id) FROM submission s
		 JOIN submission_reviewer sr ON sr.submission_id=s.id AND sr.active
		 WHERE s.status IN ('pending','consensus')
		   AND NOT EXISTS (SELECT 1 FROM review r WHERE r.submission_id=s.id AND r.reviewer_id=sr.reviewer_id AND r.superseded_at IS NULL)
		   AND sr.assigned_at + make_interval(hours => COALESCE((SELECT item_hours FROM review_sla_config WHERE class_id=s.class_id),24))<=now()
	`).Scan(&itemOverdue)
	members, err := loadCurrentScoreMembers(c.Request.Context(), tx, state.Current)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	scores, err := computeCurrentScores(state.Current.Config, members)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	scoresBySID := make(map[string]currentScore, len(members))
	gpaImported := 0
	for _, member := range members {
		scoresBySID[member.SID] = scores[member.ID]
		if member.GPA != nil {
			gpaImported++
		}
	}
	rankingItems := make([]gin.H, 0)
	var rankingScored, rankingTotal, rankingPendingFinal int
	rankRows, rankErr := tx.Query(c.Request.Context(), `
		SELECT u.sid,u.name,
		       COUNT(*) FILTER (WHERE s.status IN ('scored','locked'))::int,
		       COUNT(*) FILTER (WHERE s.status IN ('pending','consensus','arbitrating','appealing'))::int,
		       COUNT(*) FILTER (WHERE s.status='arbitrating')::int
		  FROM app_user u
		  LEFT JOIN submission s ON s.student_id=u.id AND s.status<>'draft'
		 WHERE u.status='active'
		 GROUP BY u.id,u.sid,u.name
		 ORDER BY u.sid
	`)
	if rankErr != nil {
		writeServiceError(c, rankErr)
		return
	}
	for rankRows.Next() {
		var sid, name string
		var scored, pending, conflicts int
		if err := rankRows.Scan(&sid, &name, &scored, &pending, &conflicts); err != nil {
			rankRows.Close()
			writeServiceError(c, err)
			return
		}
		current, active := scoresBySID[sid]
		if !active {
			continue
		}
		rankingItems = append(rankingItems, gin.H{
			"sid": sid, "name": name, "total": current.TotalScore,
			"categoryScores": current.CategoryScores, "categoryRanks": current.CategoryRanks, "classRank": current.ClassRank,
			"scored": scored, "pending": pending, "conflicts": conflicts,
		})
	}
	rankRows.Close()
	if err := rankRows.Err(); err != nil {
		writeServiceError(c, err)
		return
	}
	for status, count := range statusCounts {
		if status != "draft" {
			rankingTotal += count
		}
		if status == "scored" || status == "locked" {
			rankingScored += count
		}
		if status == "arbitrating" {
			rankingPendingFinal += count
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"users": state.StudentCount, "sealed": state.SealedCount, "gpaImported": state.GPACount,
		"submissions": statusCounts, "reviews": reviewCount, "activeAppeals": activeAppeals,
		"pendingConflicts": state.PendingCount, "gate": gateResponse(state), "settlement": settlementInfo,
		"reviewProgress": gin.H{"item": itemProgress, "scorecard": gin.H{}, "overdue": gin.H{"item": itemOverdue, "scorecard": 0}},
		"distribution":   distribution,
		"ranking": gin.H{
			"scoredItems": rankingScored, "totalItems": rankingTotal, "pendingFinal": rankingPendingFinal,
			"items": rankingItems, "calculatedAt": time.Now().UTC(),
			"rankingReady": len(members) > 0 && gpaImported == len(members),
			"classSize":    len(members), "gpaImported": gpaImported,
		},
	})
}
