package governance

import (
	"slices"
	"testing"
	"time"
)

func TestParticipationDoesNotDependOnMaterials(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	joined := now.Add(-48 * time.Hour)
	members := []Member{
		{ID: 1, Active: true},
		{ID: 2, Active: true, Registered: true},
		{ID: 3, Active: true, Registered: true, JoinedAt: &joined},
		{ID: 4, Active: true, Registered: true, JoinedAt: &joined, Submitted: true},
	}
	if got := Electorate(members, now, nil); !slices.Equal(got, []int64{3, 4}) {
		t.Fatalf("eligibility %v", got)
	}
}
func TestElectorateUsesStartSnapshot(t *testing.T) {
	now := time.Now()
	before := now.Add(-48 * time.Hour)
	after := now.Add(time.Hour)
	members := []Member{{ID: 1, Active: true, Registered: true, JoinedAt: &before, LeftAt: &after}, {ID: 2, Active: true, Registered: true, JoinedAt: &after}}
	if got := Electorate(members, now, nil); !slices.Equal(got, []int64{1}) {
		t.Fatal(got)
	}
	if got := Electorate(members, now, map[int64]bool{1: true}); len(got) != 0 {
		t.Fatal(got)
	}
}
func TestQuorumProtectsInactiveMembers(t *testing.T) {
	cases := []struct {
		kind       string
		e, n, want int
	}{{"ordinary", 12, 40, 7}, {"protected", 12, 40, 14}, {"ordinary", 3, 40, 3}, {"bonus", 2, 40, 3}, {"activate", 7, 40, 5}}
	for _, tt := range cases {
		got, err := Threshold(tt.kind, tt.e, tt.n)
		if err != nil || got != tt.want {
			t.Fatalf("%+v got %d %v", tt, got, err)
		}
	}
}
func TestReviewPanelsPreserveIndependentAppeals(t *testing.T) {
	for n := 1; n <= 40; n++ {
		p, err := ReviewProfile(n)
		if n < 7 {
			if err == nil {
				t.Fatal(n)
			}
			continue
		}
		if err != nil || p.Maximum+p.Appeal+1 > n {
			t.Fatalf("%d %+v %v", n, p, err)
		}
	}
}
func TestDrawExcludesSelfAndPreviousReviewers(t *testing.T) {
	now := time.Now()
	joined := now.Add(-48 * time.Hour)
	var pool []Member
	for id := int64(1); id <= 12; id++ {
		pool = append(pool, Member{ID: id, Active: true, Registered: true, JoinedAt: &joined, Reviewer: true})
	}
	excluded := map[int64]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true}
	ids, err := Draw(pool, now, excluded, 5, "0123456789012345678901234567890123")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if excluded[id] {
			t.Fatal(id)
		}
	}
	again, _ := Draw(pool, now, excluded, 5, "0123456789012345678901234567890123")
	if !slices.Equal(ids, again) {
		t.Fatal("non-reproducible draw")
	}
	if _, err = Draw(pool, now, excluded, 7, "0123456789012345678901234567890123"); err == nil {
		t.Fatal("recusal silently relaxed")
	}
}
func TestThirdReviewerDoesNotBecomeSoleArbiter(t *testing.T) {
	a := Opinion{Reviewer: 1, Decision: "accepted", Score: 2000}
	b := Opinion{Reviewer: 2, Decision: "accepted", Score: 4000}
	c := Opinion{Reviewer: 3, Decision: "accepted", Score: 4000}
	result, err := Reconcile([]Opinion{a, b}, 2, 5)
	if err != nil || result.Seats != 3 || result.State != "expand" {
		t.Fatal(result, err)
	}
	result, err = Reconcile([]Opinion{a, b, c}, 3, 5)
	if err != nil || result.State != "decided" || result.Opinion.Score != 4000 {
		t.Fatal(result, err)
	}
	c.Score = 6000
	result, _ = Reconcile([]Opinion{a, b, c}, 3, 5)
	if result.Seats != 5 || result.State != "expand" {
		t.Fatal(result)
	}
	result, _ = Reconcile([]Opinion{a, b, c}, 3, 3)
	if result.State != "deliberating" || result.Opinion != nil {
		t.Fatal("invented mean score", result)
	}
}
