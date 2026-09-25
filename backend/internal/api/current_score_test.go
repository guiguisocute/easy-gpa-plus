package api

import (
	"encoding/json"
	"strings"
	"testing"

	"easygpa/backend/internal/scheme"
)

func scorePointer(value float64) *float64 { return &value }

func currentTestConfig() scheme.Config {
	return scheme.Config{
		Weights: map[string]float64{"major": .6, "moral": .15, "practice": .15, "health": .1},
		Categories: []scheme.Category{
			{Key: "major", MaxTotal: 100},
			{Key: "moral", MaxTotal: 100, BaseItems: []scheme.BaseItem{{Key: "ordinary", Full: 70}, {Key: "claim", Full: 10, StudentClaim: &scheme.BaseStudentClaim{Minimum: 1}}}},
			{Key: "practice", MaxTotal: 100},
			{Key: "health", MaxTotal: 100, BaseItems: []scheme.BaseItem{{Key: "ordinary", Full: 70}}},
		},
	}
}

func TestCurrentScoreRequiresCompleteGPAForBothRanks(t *testing.T) {
	members := []currentScoreMember{{ID: 1, SID: "synthetic-a"}, {ID: 2, SID: "synthetic-b", GPA: scorePointer(80)}, {ID: 3, SID: "synthetic-c", GPA: scorePointer(90)}}
	missing, err := computeCurrentScore(currentTestConfig(), members, 1)
	if err != nil {
		t.Fatal(err)
	}
	if missing.TotalScore != nil || missing.CategoryScores["major"] != nil || missing.ClassRank != nil || missing.MajorRank != nil || missing.RankingReady {
		t.Fatal("missing GPA must not appear as a zero or a ranked total")
	}
	if missing.GPAImported != 2 || missing.ClassSize != 3 || *missing.CategoryScores["moral"] != 70 {
		t.Fatalf("wrong progress or default base score: %+v", missing)
	}
	partial, err := computeCurrentScore(currentTestConfig(), members, 2)
	if err != nil || partial.TotalScore == nil || *partial.TotalScore != 65.5 || partial.ClassRank != nil || partial.MajorRank != nil {
		t.Fatalf("personal scores should be visible before all GPA is imported: %+v, %v", partial, err)
	}
	for _, current := range []currentScore{missing, partial} {
		if _, exists := current.CategoryRanks["major"]; exists || len(current.CategoryRanks) != 3 {
			t.Fatalf("incomplete GPA must hide only the professional category rank: %+v", current.CategoryRanks)
		}
		for _, key := range []string{"moral", "practice", "health"} {
			if current.CategoryRanks[key] != 1 {
				t.Fatalf("non-GPA categories must rank independently: %+v", current.CategoryRanks)
			}
		}
	}
	members[0].GPA = scorePointer(90)
	for _, id := range []int64{1, 3} {
		got, err := computeCurrentScore(currentTestConfig(), members, id)
		if err != nil || !got.RankingReady || *got.ClassRank != 1 || *got.MajorRank != 1 {
			t.Fatalf("equal scores must share rank 1: %+v, %v", got, err)
		}
		for _, key := range []string{"major", "moral", "practice", "health"} {
			if got.CategoryRanks[key] != 1 {
				t.Fatalf("equal category scores must share rank 1: %+v", got.CategoryRanks)
			}
		}
	}
	got, err := computeCurrentScore(currentTestConfig(), members, 2)
	if err != nil || *got.ClassRank != 3 || *got.MajorRank != 3 || got.CategoryRanks["major"] != 3 {
		t.Fatalf("competition ranks must skip rank 2 after a tie: %+v, %v", got, err)
	}
	// Imported zero is a real score and counts towards completeness.
	members[0].GPA = scorePointer(0)
	got, err = computeCurrentScore(currentTestConfig(), members, 1)
	if err != nil || !got.RankingReady || got.TotalScore == nil || *got.CategoryScores["major"] != 0 {
		t.Fatalf("zero GPA must not be confused with missing GPA: %+v, %v", got, err)
	}
}

func TestCurrentCategoryRanksChangeWithRecognizedScores(t *testing.T) {
	// No GPA has been imported for any member; these three ranks still work.
	members := []currentScoreMember{
		{ID: 1, SID: "synthetic-a"},
		{ID: 2, SID: "synthetic-b", Base: []currentScoreBase{{Category: "moral", ItemKey: "penalty", Kind: "penalty", Score: -5}}},
		{ID: 3, SID: "synthetic-c"},
	}
	got, err := computeCurrentScore(currentTestConfig(), members, 2)
	if err != nil || got.CategoryRanks["moral"] != 3 || got.CategoryRanks["health"] != 1 || got.MajorRank != nil || got.ClassRank != nil || got.RankingReady {
		t.Fatalf("each category must have its own rank: %+v, %v", got.CategoryRanks, err)
	}
	members[2].Submissions = []currentScoreSubmission{{ID: 1, Category: "practice", Status: "appealing", Score: scorePointer(10)}}
	got, err = computeCurrentScore(currentTestConfig(), members, 2)
	if err != nil || got.CategoryRanks["practice"] != 2 || got.MajorRank != nil || got.ClassRank != nil {
		t.Fatalf("another member's recognized score must update the category rank: %+v, %v", got.CategoryRanks, err)
	}
	members[2].Submissions[0].ForceRejected = true
	got, err = computeCurrentScore(currentTestConfig(), members, 2)
	if err != nil || got.CategoryRanks["practice"] != 1 {
		t.Fatalf("forced rejection must update category ranks too: %+v, %v", got.CategoryRanks, err)
	}
}

func TestCurrentScoreUsesDecisionsCapsAndRecordedBase(t *testing.T) {
	rule := ruleSnapshot{Item: scheme.Item{ScoreRule: scheme.ScoreRule{Cap: scorePointer(5)}, ExclusiveGroup: "one", CapGroup: &scheme.CapGroup{Key: "shared", Cap: 6}}}
	member := currentScoreMember{ID: 1, SID: "synthetic-private-sid", GPA: scorePointer(80),
		Base: []currentScoreBase{
			{Category: "moral", ItemKey: "ordinary", Kind: "base", Score: 65},
			{Category: "moral", ItemKey: "penalty", Kind: "penalty", Score: -2},
			{Category: "moral", ItemKey: "claim", Kind: "base", Score: 10}, // legacy automatic row must be ignored
		},
		Submissions: []currentScoreSubmission{
			{ID: 1, Category: "practice", Status: "scored", Score: scorePointer(9), Rule: rule},
			{ID: 2, Category: "practice", Status: "scored", Score: scorePointer(4), Rule: rule},
			{ID: 3, Category: "practice", Status: "appealing", Score: scorePointer(3), Rule: ruleSnapshot{Item: scheme.Item{CapGroup: rule.Item.CapGroup}}},
			{ID: 4, Category: "practice", Status: "arbitrating", Score: scorePointer(2)},
			{ID: 5, Category: "practice", Status: "pending", Score: scorePointer(99)},
			{ID: 6, Category: "practice", Status: "consensus"},
			{ID: 7, Category: "practice", Status: "draft", Score: scorePointer(99)},
			{ID: 8, Category: "practice", Status: "locked", Score: scorePointer(99), ForceRejected: true},
			{ID: 9, Category: "moral", ItemKey: "claim", Status: "scored", Score: scorePointer(4)},
		},
	}
	got, err := computeCurrentScore(currentTestConfig(), []currentScoreMember{member}, 1)
	if err != nil {
		t.Fatal(err)
	}
	// practice: max(5,4)+3 capped at 6, plus existing arbitration score 2.
	// moral: recorded base 65 - penalty 2 + recognized claim 4; no automatic claim.
	if *got.CategoryScores["practice"] != 8 || *got.CategoryScores["moral"] != 67 || *got.TotalScore != 66.25 || got.PendingItems != 4 {
		t.Fatalf("wrong recognized current scores: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"synthetic-private-sid", "awardTier", "honor", "reviewer", "submissions"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("current score leaked %q", forbidden)
		}
	}
}

func TestCurrentScoreRejectsUnknownMember(t *testing.T) {
	if _, err := computeCurrentScore(currentTestConfig(), []currentScoreMember{{ID: 1, SID: "synthetic-a"}}, 2); err == nil {
		t.Fatal("unknown member must not receive another member's score")
	}
}
