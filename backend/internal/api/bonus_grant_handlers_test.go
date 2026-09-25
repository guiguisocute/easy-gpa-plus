package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestDirectBonusRequiresPositiveCompleteRuleBoundScore(t *testing.T) {
	current := testScheme()
	for _, tc := range []struct {
		category, item, claim string
		want                  float64
		valid                 bool
	}{
		{"moral", "moral_volunteer", `{"quantity":4}`, 2, true},
		{"moral", "moral_award", `{"option":"省级","score":4.125}`, 4.125, true},
		{"practice", "practice_paper", `{"score":3}`, 3, true},
		{"practice", "practice_participation", `{"score":2.5}`, 2.5, true},
		{"moral", "moral_volunteer", `{}`, 0, false},
		{"practice", "practice_paper", `{}`, 0, false},
		{"practice", "practice_paper", `{"score":0}`, 0, false},
		{"practice", "practice_paper", `{"score":-1}`, 0, false},
		{"practice", "practice_paper", `{"score":13}`, 0, false},
		{"moral", "moral_award", `{"option":"未知"}`, 0, false},
		{"moral", "moral_volunteer", `{"quantity":-2}`, 0, false},
		{"moral", "moral_volunteer", `{"quantity":999999999}`, 0, false},
		{"moral", "missing", `{"score":2}`, 0, false},
		{"major", "missing", `{"score":2}`, 0, false},
	} {
		input := bonusGrantInput{submissionInput: submissionInput{Category: tc.category, ItemKey: tc.item, Title: "统一活动", Note: "已核实参与", Claim: json.RawMessage(tc.claim)}}
		prepared, err := prepareBonusGrant(current, input, time.Now())
		if (err == nil) != tc.valid {
			t.Fatalf("%s/%s: %v", tc.item, tc.claim, err)
		}
		if err == nil && (prepared.Requested == nil || *prepared.Requested != tc.want) {
			t.Fatalf("%s: wrong score", tc.item)
		}
	}
	if lockdownExempt(http.MethodPost, "/api/v1/admin/bonus-grants") {
		t.Fatal("direct bonuses must respect lockdown")
	}
}

func TestNormalizeDirectBonusBatch(t *testing.T) {
	for _, ids := range [][]jsonID{nil, {0}, {1, 1}, make([]jsonID, 1001)} {
		input := bonusGrantInput{RequestID: "410d1cb8-d7c3-4233-91ec-d9e20b77c928", StudentIDs: ids, submissionInput: submissionInput{Note: "已核实参与"}}
		if normalizeBonusGrant(&input) == nil {
			t.Fatal("invalid member list accepted")
		}
	}
	input := bonusGrantInput{RequestID: "410d1cb8-d7c3-4233-91ec-d9e20b77c928", StudentIDs: []jsonID{3, 1, 2}, submissionInput: submissionInput{Note: "  已核实参与  "}}
	if err := normalizeBonusGrant(&input); err != nil {
		t.Fatal(err)
	}
	if input.Note != "已核实参与" || input.StudentIDs[0] != 1 {
		t.Fatal("request normalization unstable")
	}
	input.Note = "理由"
	if normalizeBonusGrant(&input) == nil {
		t.Fatal("reason length counts bytes instead of characters")
	}
	input.Note = "已核实参与"
	input.RequestID = "invalid"
	if normalizeBonusGrant(&input) == nil {
		t.Fatal("invalid replay key accepted")
	}
}
