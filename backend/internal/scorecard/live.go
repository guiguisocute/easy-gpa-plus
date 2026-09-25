package scorecard

import (
	"context"
	"easygpa/backend/internal/scheme"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"sort"
	"strconv"
	"time"
)

type snapshotStudent struct {
	ID   int64  `json:"id"`
	SID  string `json:"sid"`
	Name string `json:"name"`
}

// appealEvidenceSQL renders the files hanging off one appeal. 'claim' is what
// the student attached to make the case; 'note' is what got embedded in the
// markdown of the reason, the rereviews and the ruling — that text references
// them by id, so leaving them out would render broken citations. Only closed
// appeals reach a snapshot, so the reviewer-side staging rule in
// appealNoteEvidenceForReviewer is already satisfied for every row here.
func appealEvidenceSQL(kind string) string {
	return `COALESCE((
		    SELECT jsonb_agg(jsonb_build_object(
		      'id',e.id::text,'name',e.filename,'mediaType',e.media_type,'sizeBytes',e.size_bytes,
		      'sha256',e.sha256,'status',e.status,'uploadedAt',e.created_at
		    ) ORDER BY e.id)
		      FROM evidence e WHERE e.appeal_id=a.id AND e.kind='` + kind + `' AND e.status='ready'
		  ),'[]'::jsonb)`
}

// appealHistorySQL renders one target's student appeals as JSON, earliest
// round first. Without it the frozen scorecard only carries the number an
// appeal ended at, which hides the two things a final reviewer and the class
// administrator most need: how many rounds it took, and whether the original
// pair disagreed on the way. Two rereviews that landed on different scores is
// exactly the case that went to the class administrator.
//
// target is a literal predicate written at the call site, never user input.
// Reviewer and handler names stay in the stored snapshot for the class
// administrator; the reviewer API strips them again on the way out.
func appealHistorySQL(target string) string {
	return `COALESCE((
		  SELECT jsonb_agg(jsonb_build_object(
		    'id',a.id::text,'round',a.round,'status',a.status,'reason',a.reason,
		    'filedAt',a.created_at,'resolvedAt',a.resolved_at,
		    'baselineScore',a.original_score::float8,
		    'originalCategory',a.original_category_key,'originalItemKey',a.original_item_key,
		    'resolutionScore',a.resolution_score::float8,'resolutionReason',a.resolution_reason,
		    'resolutionCategory',a.resolution_category_key,'resolutionItemKey',a.resolution_item_key,
		    'handlerId',a.handler_id::text,'handlerSid',handler.sid,'handler',handler.name,
		    'evidence',` + appealEvidenceSQL("claim") + `,
		    'noteEvidence',` + appealEvidenceSQL("note") + `,
		    'rereviews',COALESCE((
		      SELECT jsonb_agg(jsonb_build_object(
		        'position',ar.position,'reviewerId',ar.reviewer_id::text,'reviewerSid',rr.sid,'reviewer',rr.name,
		        'decision',ar.decision,'score',ar.score::float8,
		        'category',ar.category_key,'itemKey',ar.item_key,
		        'reason',ar.reason,'spentSeconds',ar.spent_seconds,'at',ar.decided_at
		      ) ORDER BY ar.position)
		        FROM appeal_reviewer ar JOIN app_user rr ON rr.id=ar.reviewer_id
		       WHERE ar.appeal_id=a.id AND ar.decided_at IS NOT NULL
		    ),'[]'::jsonb)
		  ) ORDER BY a.round,a.id)
		    FROM appeal a LEFT JOIN app_user handler ON handler.id=a.handler_id
		   WHERE a.kind='student_appeal' AND a.status IN ('resolved','final') AND ` + target + `
		),'[]'::jsonb)`
}

type scorecardSnapshot struct {
	Kind           string             `json:"kind"`
	Notice         string             `json:"notice"`
	BatchID        string             `json:"batchId"`
	SchemeID       string             `json:"schemeId"`
	SchemeVersion  string             `json:"schemeVersion"`
	InputHash      string             `json:"inputHash"`
	CapturedAt     time.Time          `json:"capturedAt"`
	Student        snapshotStudent    `json:"student"`
	CategoryScores map[string]float64 `json:"categoryScores"`
	Details        map[string]any     `json:"details"`
}

type auditStudentInput struct {
	UserID         int64
	SID            string
	Name           string
	CategoryScores map[string]scheme.Points
}

type auditRuleSnapshot struct {
	Item scheme.Item `json:"item"`
}

func Build(ctx context.Context, tx pgx.Tx, studentID, schemeID int64, cfg scheme.Config, now time.Time) ([]scorecardSnapshot, error) {
	inputs := make([]auditStudentInput, 0)
	details := make(map[int64]map[string]any)
	rows, err := tx.Query(ctx, `SELECT id,sid,name FROM app_user WHERE status='active' AND id=$1 ORDER BY sid,id`, studentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var input auditStudentInput
		if err := rows.Scan(&input.UserID, &input.SID, &input.Name); err != nil {
			rows.Close()
			return nil, err
		}
		input.CategoryScores = make(map[string]scheme.Points, len(cfg.Weights))
		for key := range cfg.Weights {
			if key == "major" {
				continue
			}
			input.CategoryScores[key] = 0
		}
		inputs = append(inputs, input)
		details[input.UserID] = map[string]any{"items": []any{}, "baseItems": []any{}}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	items := make(map[int64]map[string][]scheme.ScoredItem)
	rows, err = tx.Query(ctx, `
		SELECT s.id,s.student_id,s.filed_category_key,s.filed_item_key,s.category_key,s.item_key,s.title,
		       s.claim,s.requested_score::float8,s.final_score::float8,s.markdown_note,s.source,s.submitted_at,
		       s.filed_rule_snapshot,s.rule_snapshot,s.force_rejection,s.forced_score - 'actorName',
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',e.id::text,'name',e.filename,'mediaType',e.media_type,'sizeBytes',e.size_bytes,
		           'sha256',e.sha256,'status',e.status,'uploadedAt',e.created_at
		         ) ORDER BY e.id)
		           FROM evidence e WHERE e.submission_id=s.id AND e.kind='claim' AND e.status='ready'
		       ),'[]'::jsonb),
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',e.id::text,'name',e.filename,'mediaType',e.media_type,'sizeBytes',e.size_bytes,
		           'sha256',e.sha256,'status',e.status,'uploadedAt',e.created_at
		         ) ORDER BY e.id)
		           FROM evidence e WHERE e.submission_id=s.id AND e.kind='note' AND e.status='ready'
		       ),'[]'::jsonb),
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'id',r.id::text,'reviewerId',r.reviewer_id::text,'reviewerSid',reviewer.sid,'reviewer',reviewer.name,
		           'decision',r.decision,'score',r.score::float8,'reason',r.reason,
		           'spentSeconds',r.spent_seconds,'at',r.created_at
		         ) ORDER BY r.created_at,r.id)
		           FROM review r JOIN app_user reviewer ON reviewer.id=r.reviewer_id
		          WHERE r.submission_id=s.id AND r.superseded_at IS NULL AND s.status IN ('scored','locked')
		       ),'[]'::jsonb),
		       `+appealHistorySQL(`a.target_type='submission' AND a.target_id=s.id`)+`
		  FROM submission s WHERE s.student_id=$1 AND s.status IN ('scored','locked','appealing','arbitrating') AND s.final_score IS NOT NULL
		 ORDER BY s.student_id,s.category_key,s.id
	`, studentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, studentID int64
		var filedCategory, filedItem, category, itemKey, title, note, source string
		var requestedScore *float64
		var score float64
		var submittedAt *time.Time
		var claimRaw, filedRuleRaw, ruleRaw, evidenceRaw, noteEvidenceRaw, reviewsRaw, appealsRaw, forceRejectionRaw, forcedScoreRaw []byte
		if err := rows.Scan(
			&id, &studentID, &filedCategory, &filedItem, &category, &itemKey, &title,
			&claimRaw, &requestedScore, &score, &note, &source, &submittedAt,
			&filedRuleRaw, &ruleRaw, &forceRejectionRaw, &forcedScoreRaw, &evidenceRaw, &noteEvidenceRaw, &reviewsRaw, &appealsRaw,
		); err != nil {
			rows.Close()
			return nil, err
		}
		var snapshot auditRuleSnapshot
		if err := json.Unmarshal(ruleRaw, &snapshot); err != nil {
			rows.Close()
			return nil, fmt.Errorf("submission %d rule snapshot: %w", id, err)
		}
		row := scheme.ScoredItem{ID: id, ItemKey: itemKey, Score: scheme.NewPoints(score), ExclusiveGroup: snapshot.Item.ExclusiveGroup}
		if snapshot.Item.ScoreRule.Cap != nil {
			value := scheme.NewPoints(*snapshot.Item.ScoreRule.Cap)
			row.ItemCap = &value
		}
		if snapshot.Item.CapGroup != nil {
			row.CapGroupKey, row.CapGroupCap = snapshot.Item.CapGroup.Key, scheme.NewPoints(snapshot.Item.CapGroup.Cap)
		}
		if items[studentID] == nil {
			items[studentID] = make(map[string][]scheme.ScoredItem)
		}
		items[studentID][category] = append(items[studentID][category], row)
		details[studentID]["items"] = append(details[studentID]["items"].([]any), map[string]any{
			"id": strconv.FormatInt(id, 10), "title": title,
			"filedCategory": filedCategory, "filedItemKey": filedItem,
			"category": category, "itemKey": itemKey,
			"claim": json.RawMessage(claimRaw), "requestedScore": requestedScore, "score": score,
			"note": note, "source": source, "submittedAt": submittedAt,
			"filedRuleSnapshot": json.RawMessage(filedRuleRaw), "ruleSnapshot": json.RawMessage(ruleRaw),
			"evidence": json.RawMessage(evidenceRaw), "noteEvidence": json.RawMessage(noteEvidenceRaw),
			"reviews": json.RawMessage(reviewsRaw), "appeals": json.RawMessage(appealsRaw),
			"forceRejection": json.RawMessage(forceRejectionRaw),
			"forcedScore":    json.RawMessage(forcedScoreRaw),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	base := make(map[int64]map[string]map[string]scheme.RecordedBase)
	baseDetails := make(map[int64]map[string]map[string]any)
	baseDetailOrder := make([]string, 0)
	for _, category := range cfg.Categories {
		for _, item := range category.BaseItems {
			baseDetailOrder = append(baseDetailOrder, category.Key+"\x00base\x00"+item.Key)
		}
		for _, item := range category.PenaltyItems {
			baseDetailOrder = append(baseDetailOrder, category.Key+"\x00penalty\x00"+item.Key)
		}
	}
	for _, input := range inputs {
		base[input.UserID] = make(map[string]map[string]scheme.RecordedBase)
		baseDetails[input.UserID] = make(map[string]map[string]any)
		for _, category := range cfg.Categories {
			base[input.UserID][category.Key] = make(map[string]scheme.RecordedBase)
			for _, item := range category.BaseItems {
				key := category.Key + "\x00base\x00" + item.Key
				score, basis := 0.0, "尚无认定记录"
				if item.StudentClaim == nil {
					base[input.UserID][category.Key]["base\x00"+item.Key] = scheme.RecordedBase{ItemKey: item.Key, Kind: "base", Score: scheme.NewPoints(item.Full)}
					score, basis = item.Full, "方案默认满分"
				}
				baseDetails[input.UserID][key] = map[string]any{
					"id": nil, "category": category.Key, "categoryName": category.Name,
					"itemKey": item.Key, "itemName": item.Name, "kind": "base",
					"fullScore": item.Full, "perScore": nil, "score": score, "basis": basis, "recorded": false,
					"appeals": []any{},
				}
			}
			for _, item := range category.PenaltyItems {
				key := category.Key + "\x00penalty\x00" + item.Key
				baseDetails[input.UserID][key] = map[string]any{
					"id": nil, "category": category.Key, "categoryName": category.Name,
					"itemKey": item.Key, "itemName": item.Name, "kind": "penalty",
					"fullScore": nil, "perScore": item.Per, "score": 0, "basis": "无扣分记录", "recorded": false,
					"appeals": []any{},
				}
			}
		}
	}
	rows, err = tx.Query(ctx, `
		SELECT b.id,b.student_id,b.category_key,b.item_key,b.kind,b.score::float8,b.basis,b.updated_at,
		       `+appealHistorySQL(`a.target_type IN ('base_score','penalty_score') AND a.target_id=b.id`)+`
		  FROM base_score b WHERE b.scheme_id=$1 AND b.student_id=$2
		 ORDER BY b.student_id,b.category_key,b.item_key,b.id
	`, schemeID, studentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, studentID int64
		var category, itemKey, kind, basis string
		var score float64
		var updatedAt time.Time
		var appealsRaw []byte
		if err := rows.Scan(&id, &studentID, &category, &itemKey, &kind, &score, &basis, &updatedAt, &appealsRaw); err != nil {
			rows.Close()
			return nil, err
		}
		if base[studentID] == nil {
			continue
		}
		if base[studentID][category] == nil {
			base[studentID][category] = make(map[string]scheme.RecordedBase)
		}
		base[studentID][category][kind+"\x00"+itemKey] = scheme.RecordedBase{ItemKey: itemKey, Kind: kind, Score: scheme.NewPoints(score)}
		key := category + "\x00" + kind + "\x00" + itemKey
		if detail := baseDetails[studentID][key]; detail != nil {
			detail["id"], detail["score"], detail["basis"], detail["recorded"], detail["updatedAt"] = strconv.FormatInt(id, 10), score, basis, true, updatedAt
			detail["appeals"] = json.RawMessage(appealsRaw)
		}
	}
	rows.Close()
	for _, input := range inputs {
		for _, key := range baseDetailOrder {
			if detail := baseDetails[input.UserID][key]; detail != nil {
				details[input.UserID]["baseItems"] = append(details[input.UserID]["baseItems"].([]any), detail)
			}
		}
	}
	for inputIndex := range inputs {
		input := &inputs[inputIndex]
		categoryDetails := make(map[string]any)
		for _, category := range cfg.Categories {
			if category.Key == "major" {
				continue
			}
			keys := make([]string, 0, len(base[input.UserID][category.Key]))
			for key := range base[input.UserID][category.Key] {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			records := make([]scheme.RecordedBase, 0, len(keys))
			for _, key := range keys {
				records = append(records, base[input.UserID][category.Key][key])
			}
			breakdown := scheme.ComputeCategoryScore(items[input.UserID][category.Key], records, scheme.NewPoints(category.MaxTotal))
			input.CategoryScores[category.Key] = breakdown.Total
			categoryDetails[category.Key] = breakdown
		}
		details[input.UserID]["categories"] = categoryDetails
	}
	result := make([]scorecardSnapshot, 0, len(inputs))
	for _, row := range inputs {
		categoryScores := make(map[string]float64, len(row.CategoryScores))
		for key, value := range row.CategoryScores {
			categoryScores[key] = value.Float64()
		}
		result = append(result, scorecardSnapshot{
			Kind: "live_scorecard", Notice: "当前认定成绩",
			SchemeID: strconv.FormatInt(schemeID, 10), SchemeVersion: cfg.Version,
			CapturedAt: now.UTC(), Student: snapshotStudent{ID: row.UserID, SID: row.SID, Name: row.Name},
			CategoryScores: categoryScores,
			Details:        details[row.UserID],
		})
	}
	return result, nil
}
