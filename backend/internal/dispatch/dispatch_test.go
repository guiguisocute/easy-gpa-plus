package dispatch

import (
	"reflect"
	"testing"
	"time"
)

func reviewers(count int) []Reviewer {
	result := make([]Reviewer, count)
	for i := range result {
		result[i] = Reviewer{UserID: int64(i + 1), Name: "reviewer"}
	}
	return result
}

func submissions(from, count int) []Submission {
	result := make([]Submission, count)
	for i := range result {
		result[i] = Submission{ID: int64(from + i), StudentID: 10_000 + int64(i), SubmittedAt: time.Unix(int64(from+i), 0)}
	}
	return result
}

func assignedLoads(plan Plan) map[int64]Load {
	loads := make(map[int64]Load)
	for _, assignment := range plan.Assignments {
		for _, id := range assignment.Reviewers {
			value := loads[id]
			value.Assigned++
			value.Pending++
			loads[id] = value
		}
	}
	return loads
}

func TestBalanceReplacesWorkAndKeepsFixedAssignments(t *testing.T) {
	pool := reviewers(3)
	targets := submissions(1, 2)
	for i := range targets {
		targets[i].ReplacedReviewers = []int64{1, 2}
	}
	existing := map[int64]Load{1: {Assigned: 6, Pending: 2}, 2: {Assigned: 2, Pending: 2}}
	plan, err := Balance(targets, pool, existing, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	// Reviewer 1 retains four completed assignments and should receive no new
	// work. Both replaceable tasks now belong to reviewers 2 and 3.
	for _, assignment := range plan.Assignments {
		for _, id := range assignment.Reviewers {
			if id == 1 {
				t.Fatal("fixed completed workload was ignored")
			}
		}
	}
	want := []PlanRow{
		{ReviewerID: "1", Name: "reviewer", Before: 6, After: 4, Delta: -2},
		{ReviewerID: "2", Name: "reviewer", Before: 2, After: 2, Delta: 0},
		{ReviewerID: "3", Name: "reviewer", Before: 0, After: 2, Delta: 2},
	}
	if !reflect.DeepEqual(plan.Rows, want) || plan.SpreadBefore != 6 || plan.SpreadAfter != 2 {
		t.Fatalf("incorrect replacement preview: %+v", plan)
	}
	if existing[1].Assigned != 6 || existing[2].Assigned != 2 {
		t.Fatal("Balance mutated the caller's workload snapshot")
	}
}

func TestBalanceKeepsBlockedAssignmentsAndRemovesPausedReplacement(t *testing.T) {
	pool := reviewers(3)
	pool[2].Paused = true
	targets := submissions(1, 2)
	targets[0].StudentID = 1 // Only one eligible reviewer remains after self-exclusion.
	targets[0].ReplacedReviewers = []int64{2, 3}
	targets[1].ReplacedReviewers = []int64{1, 3}
	existing := map[int64]Load{
		1: {Assigned: 1, Pending: 1},
		2: {Assigned: 1, Pending: 1},
		3: {Assigned: 2, Pending: 2},
	}
	plan, err := Balance(targets, pool, existing, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocked) != 1 || len(plan.Assignments) != 1 {
		t.Fatalf("unexpected eligibility result: %+v", plan)
	}
	wantAfter := []int{1, 2, 1}
	for i, row := range plan.Rows {
		if row.After != wantAfter[i] {
			t.Fatalf("blocked work must survive, including the paused holder: %+v", plan.Rows)
		}
	}
}

func TestBalanceIsDeterministicAndBalanced(t *testing.T) {
	pool := reviewers(10)
	targets := submissions(1, 100)
	first, err := Balance(targets, pool, nil, 20260808, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Balance(append([]Submission(nil), targets...), append([]Reviewer(nil), pool...), nil, 20260808, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Assignments, second.Assignments) {
		t.Fatal("same seed and inputs produced different per-submission assignments")
	}
	if first.SpreadAfter > 1 {
		t.Fatalf("workload spread = %d, want <= 1", first.SpreadAfter)
	}
	different, err := Balance(targets, pool, nil, 20260809, true)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(first.Assignments, different.Assignments) {
		t.Fatal("different seeds produced identical assignments; seed is not influencing ties")
	}
}

func TestBalanceAvoidsSelfAndUsesPendingAsSecondKey(t *testing.T) {
	pool := reviewers(3)
	target := Submission{ID: 1, StudentID: 1}
	plan, err := Balance([]Submission{target}, pool, map[int64]Load{
		2: {Assigned: 5, Pending: 4},
		3: {Assigned: 5, Pending: 1},
	}, 9, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) != 1 || plan.Assignments[0].Reviewers != [2]int64{3, 2} {
		t.Fatalf("unexpected assignment: %#v", plan.Assignments)
	}
}

func TestBalanceBlockedReasons(t *testing.T) {
	tests := []struct {
		name string
		pool []Reviewer
		sub  Submission
		want string
	}{
		{name: "pool short", pool: reviewers(1), sub: Submission{ID: 1, StudentID: 99}, want: "可用审核人不足 2 名"},
		{name: "self exclusion", pool: reviewers(2), sub: Submission{ID: 1, StudentID: 1}, want: "除本人外只剩 1 名可用审核人"},
		{name: "already reviewed", pool: reviewers(2), sub: Submission{ID: 1, StudentID: 99, ReviewedBy: map[int64]bool{1: true, 2: true}}, want: "候选人都已就这条给出结论"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Balance([]Submission{tt.sub}, tt.pool, nil, 1, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Assignments) != 0 || len(plan.Blocked) != 1 || plan.Blocked[0].Reason != tt.want {
				t.Fatalf("unexpected plan: %#v", plan)
			}
		})
	}
}

func TestBalanceIncrementalCarriesExistingLoad(t *testing.T) {
	pool := reviewers(10)
	first, err := Balance(submissions(1, 50), pool, nil, 77, true)
	if err != nil {
		t.Fatal(err)
	}
	loads := assignedLoads(first)
	second, err := Balance(submissions(51, 50), pool, loads, 77, true)
	if err != nil {
		t.Fatal(err)
	}
	for id, secondLoad := range assignedLoads(second) {
		value := loads[id]
		value.Assigned += secondLoad.Assigned
		value.Pending += secondLoad.Pending
		loads[id] = value
	}
	minimum, maximum := 1<<30, 0
	for _, reviewer := range pool {
		value := loads[reviewer.UserID].Assigned
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	if maximum-minimum > 1 {
		t.Fatalf("incremental spread = %d, loads=%v", maximum-minimum, loads)
	}
}

func TestBalanceKeepsPausedReviewerVisibleWithoutAssigningThem(t *testing.T) {
	pool := reviewers(4)
	pool[0].Paused = true
	loads := map[int64]Load{1: {Assigned: 2, Pending: 1}}
	plan, err := Balance(submissions(1, 5), pool, loads, 91, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, assignment := range plan.Assignments {
		if assignment.Reviewers[0] == 1 || assignment.Reviewers[1] == 1 {
			t.Fatalf("paused reviewer received assignment: %#v", assignment)
		}
	}
	if len(plan.Rows) != 4 || plan.Rows[0].ReviewerID != "1" || plan.Rows[0].Before != 2 || plan.Rows[0].After != 2 {
		t.Fatalf("paused reviewer must remain visible with unchanged load: %#v", plan.Rows)
	}
}
