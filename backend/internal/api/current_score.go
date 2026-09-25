package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/settle"
)

// currentScore exposes only the caller's scores. Missing GPA is null, never a
// zero used to rank GPA or totals for a partially imported class. Other
// categories rank independently; awards belong to settlement.
type currentScore struct {
	SchemeID       string              `json:"schemeId"`
	CalculatedAt   time.Time           `json:"calculatedAt"`
	ClassSize      int                 `json:"classSize"`
	GPAImported    int                 `json:"gpaImported"`
	RankingReady   bool                `json:"rankingReady"` // GPA and total ranks are ready.
	CategoryScores map[string]*float64 `json:"categoryScores"`
	CategoryRanks  map[string]int      `json:"categoryRanks"`
	TotalScore     *float64            `json:"totalScore"`
	ClassRank      *int                `json:"classRank"`
	MajorRank      *int                `json:"majorRank"`
	PendingItems   int                 `json:"pendingItems"`
}

type currentScoreSubmission struct {
	ID            int64        `json:"id"`
	Category      string       `json:"category"`
	ItemKey       string       `json:"itemKey"`
	Status        string       `json:"status"`
	Score         *float64     `json:"score"`
	Rule          ruleSnapshot `json:"rule"`
	ForceRejected bool         `json:"forceRejected"`
}

type currentScoreBase struct {
	Category string  `json:"category"`
	ItemKey  string  `json:"itemKey"`
	Kind     string  `json:"kind"`
	Score    float64 `json:"score"`
}

type currentScoreMember struct {
	ID          int64
	SID         string
	GPA         *float64
	Submissions []currentScoreSubmission
	Base        []currentScoreBase
}

// One SQL statement reads GPA, active membership and score inputs from the same
// database snapshot. No review identities, evidence or student names are loaded.
func loadCurrentScoreMembers(ctx context.Context, tx pgx.Tx, current storedScheme) ([]currentScoreMember, error) {
	rows, err := tx.Query(ctx, `
		SELECT u.id,u.sid,g.score::float8,
		       COALESCE((SELECT jsonb_agg(jsonb_build_object(
		         'id',s.id,'category',s.category_key,'itemKey',s.item_key,
		         'status',s.status,'score',s.final_score,'rule',s.rule_snapshot,
		         'forceRejected',s.force_rejection IS NOT NULL) ORDER BY s.id)
		         FROM submission s WHERE s.student_id=u.id AND s.status<>'draft'),'[]'),
		       COALESCE((SELECT jsonb_agg(jsonb_build_object(
		         'category',b.category_key,'itemKey',b.item_key,'kind',b.kind,'score',b.score) ORDER BY b.id)
		         FROM base_score b WHERE b.student_id=u.id AND b.scheme_id=$1),'[]')
		  FROM app_user u
		  LEFT JOIN student_gpa g ON g.student_id=u.id AND g.scheme_id=$1
		 WHERE u.status='active' ORDER BY u.sid
	`, current.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []currentScoreMember
	for rows.Next() {
		var member currentScoreMember
		var submissions, base []byte
		if err := rows.Scan(&member.ID, &member.SID, &member.GPA, &submissions, &base); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(submissions, &member.Submissions); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(base, &member.Base); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return members, nil
}

func loadCurrentScore(ctx context.Context, tx pgx.Tx, current storedScheme, studentID int64) (currentScore, error) {
	members, err := loadCurrentScoreMembers(ctx, tx, current)
	if err != nil {
		return currentScore{}, err
	}
	return computeCurrentScore(current.Config, members, studentID)
}

func computeCurrentScore(cfg scheme.Config, members []currentScoreMember, studentID int64) (currentScore, error) {
	results, err := computeCurrentScores(cfg, members)
	if err != nil {
		return currentScore{}, err
	}
	result, ok := results[studentID]
	if !ok {
		return currentScore{}, errors.New("当前成员不在有效班级名单中")
	}
	return result, nil
}

func computeCurrentScores(cfg scheme.Config, members []currentScoreMember) (map[int64]currentScore, error) {
	results := make(map[int64]currentScore, len(members))
	gpaImported := 0
	pending := make(map[int64]int)
	byID := make(map[int64]*currentScoreMember)
	inputs := make([]settle.StudentInput, 0, len(members))
	for index := range members {
		member := &members[index]
		byID[member.ID] = member
		input := settle.StudentInput{UserID: member.ID, SID: member.SID, CategoryScores: make(map[string]scheme.Points)}
		if member.GPA != nil {
			gpaImported++
			input.CategoryScores["major"] = scheme.NewPoints(*member.GPA)
		}
		items := make(map[string][]scheme.ScoredItem)
		for _, sub := range member.Submissions {
			if sub.Status == "draft" {
				continue
			}
			if sub.Status != "scored" && sub.Status != "locked" && !sub.ForceRejected {
				pending[member.ID]++
			}
			// An appeal retains the existing decision until resolved. Pending
			// claims with no decision and forced rejections never add points.
			if sub.Score == nil || sub.ForceRejected || sub.Category == "major" {
				continue
			}
			if sub.Status != "scored" && sub.Status != "locked" && sub.Status != "appealing" && sub.Status != "arbitrating" {
				continue
			}
			item := scoredSubmissionItem(sub.ID, sub.ItemKey, *sub.Score, sub.Rule)
			items[sub.Category] = append(items[sub.Category], item)
		}
		for _, category := range cfg.Categories {
			if category.Key == "major" {
				continue
			}
			base := make(map[string]scheme.RecordedBase)
			claimable := make(map[string]bool)
			for _, item := range category.BaseItems {
				if item.StudentClaim != nil {
					claimable[item.Key] = true
					continue
				}
				base[item.Key] = scheme.RecordedBase{ItemKey: item.Key, Kind: "base", Score: scheme.NewPoints(item.Full)}
			}
			for _, record := range member.Base {
				if record.Category != category.Key || (record.Kind == "base" && claimable[record.ItemKey]) {
					continue
				}
				base[record.ItemKey] = scheme.RecordedBase{ItemKey: record.ItemKey, Kind: record.Kind, Score: scheme.NewPoints(record.Score)}
			}
			records := make([]scheme.RecordedBase, 0, len(base))
			for _, record := range base {
				records = append(records, record)
			}
			input.CategoryScores[category.Key] = scheme.ComputeCategoryScore(items[category.Key], records, scheme.NewPoints(category.MaxTotal)).Total
		}
		inputs = append(inputs, input)
	}
	// Share the exact fixed-point weighting and competition-rank implementation
	// with settlement; do not expose the computed honor or award fields.
	snapshots, err := settle.Compute(inputs, cfg.Weights, scheme.HonorRoll{})
	if err != nil {
		return nil, err
	}
	rankingReady := len(members) > 0 && gpaImported == len(members)
	for _, row := range snapshots {
		mine := byID[row.UserID]
		result := currentScore{ClassSize: len(members), GPAImported: gpaImported, RankingReady: rankingReady,
			PendingItems: pending[row.UserID], CategoryScores: make(map[string]*float64), CategoryRanks: make(map[string]int)}
		for key, score := range row.CategoryScores {
			result.CategoryScores[key] = &score
		}
		if mine.GPA == nil {
			result.CategoryScores["major"] = nil
		} else {
			result.TotalScore = &row.TotalScore
		}
		if result.RankingReady {
			result.ClassRank, result.MajorRank = &row.ClassRank, &row.MajorRank
		}
		for key, rank := range row.CategoryRanks {
			if key != "major" || result.RankingReady {
				result.CategoryRanks[key] = rank
			}
		}
		results[row.UserID] = result
	}
	return results, nil
}

func scoredSubmissionItem(id int64, key string, score float64, snapshot ruleSnapshot) scheme.ScoredItem {
	item := scheme.ScoredItem{ID: id, ItemKey: key, Score: scheme.NewPoints(score), ExclusiveGroup: snapshot.Item.ExclusiveGroup}
	if snapshot.Item.ScoreRule.Cap != nil {
		capValue := scheme.NewPoints(*snapshot.Item.ScoreRule.Cap)
		item.ItemCap = &capValue
	}
	if snapshot.Item.CapGroup != nil {
		item.CapGroupKey = snapshot.Item.CapGroup.Key
		item.CapGroupCap = scheme.NewPoints(snapshot.Item.CapGroup.Cap)
	}
	return item
}
