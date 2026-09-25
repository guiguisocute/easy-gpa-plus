// Package dispatch contains the reproducible per-submission reviewer policy.
package dispatch

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"
)

const Policy = "balanced_per_submission"

type Reviewer struct {
	UserID   int64  `json:"userId,string"`
	SID      string `json:"sid"`
	Name     string `json:"name"`
	Role     string `json:"role,omitempty"`
	Paused   bool   `json:"paused,omitempty"`
	Assigned int    `json:"assigned"`
	Pending  int    `json:"pending"`
	Done     int    `json:"done"`
}

type Submission struct {
	ID                int64
	StudentID         int64
	Title             string
	Student           string
	SubmittedAt       time.Time
	ReviewedBy        map[int64]bool
	AssignedReviewers map[int64]bool
	// ReplacedReviewers are active assignments that will be retired if this
	// submission is successfully rebalanced. They remain intact when blocked.
	ReplacedReviewers []int64
}

type Load struct {
	Assigned int
	Pending  int
}

type Assignment struct {
	SubmissionID int64
	Reviewers    [2]int64
}

type PlanRow struct {
	ReviewerID string `json:"reviewerId"`
	Name       string `json:"name"`
	SID        string `json:"sid"`
	Before     int    `json:"before"`
	After      int    `json:"after"`
	Delta      int    `json:"delta"`
}

type Blocked struct {
	SubmissionID string `json:"submissionId"`
	Title        string `json:"title"`
	Student      string `json:"student"`
	Reason       string `json:"reason"`
}

type Plan struct {
	Policy       string       `json:"policy"`
	Seed         int64        `json:"seed,string"`
	AvoidSelf    bool         `json:"avoidSelf"`
	Targets      int          `json:"targets"`
	Rows         []PlanRow    `json:"rows"`
	SpreadBefore int          `json:"spreadBefore"`
	SpreadAfter  int          `json:"spreadAfter"`
	Blocked      []Blocked    `json:"blocked"`
	Assignments  []Assignment `json:"-"`
}

var ErrInvalidPool = errors.New("审核人名单包含重复或不可用的成员，请检查后重新选择")

// Balance assigns two reviewers per submission. Tie-breaking uses a SHA-256
// digest over three fixed-width integers, so a stored seed remains reproducible
// across Go versions and when an arbitrary subset is retried.
func Balance(submissions []Submission, pool []Reviewer, existing map[int64]Load, seed int64, avoidSelf bool) (Plan, error) {
	active := make([]Reviewer, 0, len(pool))
	all := make([]Reviewer, 0, len(pool))
	seen := make(map[int64]bool, len(pool))
	for _, reviewer := range pool {
		if reviewer.UserID <= 0 || seen[reviewer.UserID] {
			return Plan{}, ErrInvalidPool
		}
		seen[reviewer.UserID] = true
		all = append(all, reviewer)
		if !reviewer.Paused {
			active = append(active, reviewer)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].UserID < active[j].UserID })
	sort.Slice(all, func(i, j int) bool { return all[i].UserID < all[j].UserID })

	loads := make(map[int64]Load, len(all))
	before := make(map[int64]int, len(all))
	for _, reviewer := range all {
		load := existing[reviewer.UserID]
		loads[reviewer.UserID] = load
		before[reviewer.UserID] = load.Assigned
	}
	ordered := append([]Submission(nil), submissions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].SubmittedAt.Equal(ordered[j].SubmittedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].SubmittedAt.Before(ordered[j].SubmittedAt)
	})

	plan := Plan{
		Policy: Policy, Seed: seed, AvoidSelf: avoidSelf, Targets: len(ordered),
		Blocked: make([]Blocked, 0), Assignments: make([]Assignment, 0, len(ordered)),
		SpreadBefore: spread(active, loads),
	}
	// Rebalancing replaces work rather than adding another copy. Remove all
	// replaceable assignments before choosing anyone, while retaining the real
	// pre-run counts for preview rows. Blocked submissions keep their old work.
	for _, submission := range ordered {
		candidates, _ := dispatchCandidates(submission, active, avoidSelf)
		if len(candidates) < 2 {
			continue
		}
		for _, reviewerID := range submission.ReplacedReviewers {
			load, exists := loads[reviewerID]
			if !exists {
				continue
			}
			load.Assigned--
			load.Pending--
			loads[reviewerID] = load
		}
	}
	for _, submission := range ordered {
		if submission.ID <= 0 || submission.StudentID <= 0 {
			return Plan{}, fmt.Errorf("submission and student IDs must be positive")
		}
		candidates, reason := dispatchCandidates(submission, active, avoidSelf)
		if reason != "" {
			plan.Blocked = append(plan.Blocked, blocked(submission, reason))
			continue
		}
		sort.Slice(candidates, func(i, j int) bool {
			left, right := loads[candidates[i].UserID], loads[candidates[j].UserID]
			if left.Assigned != right.Assigned {
				return left.Assigned < right.Assigned
			}
			if left.Pending != right.Pending {
				return left.Pending < right.Pending
			}
			leftHash := tieHash(seed, submission.ID, candidates[i].UserID)
			rightHash := tieHash(seed, submission.ID, candidates[j].UserID)
			if leftHash != rightHash {
				return leftHash < rightHash
			}
			return candidates[i].UserID < candidates[j].UserID
		})
		selected := [2]int64{candidates[0].UserID, candidates[1].UserID}
		plan.Assignments = append(plan.Assignments, Assignment{SubmissionID: submission.ID, Reviewers: selected})
		for _, reviewerID := range selected {
			load := loads[reviewerID]
			load.Assigned++
			load.Pending++
			loads[reviewerID] = load
		}
	}
	plan.SpreadAfter = spread(active, loads)
	plan.Rows = make([]PlanRow, 0, len(all))
	for _, reviewer := range all {
		after := loads[reviewer.UserID].Assigned
		plan.Rows = append(plan.Rows, PlanRow{
			ReviewerID: fmt.Sprintf("%d", reviewer.UserID), Name: reviewer.Name, SID: reviewer.SID,
			Before: before[reviewer.UserID], After: after, Delta: after - before[reviewer.UserID],
		})
	}
	return plan, nil
}

func dispatchCandidates(submission Submission, active []Reviewer, avoidSelf bool) ([]Reviewer, string) {
	if len(active) < 2 {
		return nil, "可用审核人不足 2 名"
	}
	withoutSelf := 0
	candidates := make([]Reviewer, 0, len(active))
	for _, reviewer := range active {
		if avoidSelf && reviewer.UserID == submission.StudentID {
			continue
		}
		withoutSelf++
		if !submission.ReviewedBy[reviewer.UserID] && !submission.AssignedReviewers[reviewer.UserID] {
			candidates = append(candidates, reviewer)
		}
	}
	if withoutSelf < 2 {
		return nil, fmt.Sprintf("除本人外只剩 %d 名可用审核人", withoutSelf)
	}
	if len(candidates) < 2 {
		return nil, "候选人都已就这条给出结论"
	}
	return candidates, ""
}

func tieHash(seed, submissionID, userID int64) uint64 {
	var raw [24]byte
	binary.BigEndian.PutUint64(raw[0:8], uint64(seed))
	binary.BigEndian.PutUint64(raw[8:16], uint64(submissionID))
	binary.BigEndian.PutUint64(raw[16:24], uint64(userID))
	sum := sha256.Sum256(raw[:])
	return binary.BigEndian.Uint64(sum[:8])
}

// PickOne uses the same ordering as Balance for handover operations, where the
// other reviewer keeps their slot and only one replacement is needed.
func PickOne(submission Submission, pool []Reviewer, existing map[int64]Load, seed int64, avoidSelf bool) (int64, bool, error) {
	candidates := make([]Reviewer, 0, len(pool))
	seen := make(map[int64]bool, len(pool))
	for _, reviewer := range pool {
		if reviewer.UserID <= 0 || seen[reviewer.UserID] {
			return 0, false, ErrInvalidPool
		}
		seen[reviewer.UserID] = true
		if reviewer.Paused {
			continue
		}
		if avoidSelf && reviewer.UserID == submission.StudentID {
			continue
		}
		if submission.ReviewedBy[reviewer.UserID] || submission.AssignedReviewers[reviewer.UserID] {
			continue
		}
		candidates = append(candidates, reviewer)
	}
	if len(candidates) == 0 {
		return 0, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := existing[candidates[i].UserID], existing[candidates[j].UserID]
		if left.Assigned != right.Assigned {
			return left.Assigned < right.Assigned
		}
		if left.Pending != right.Pending {
			return left.Pending < right.Pending
		}
		leftHash := tieHash(seed, submission.ID, candidates[i].UserID)
		rightHash := tieHash(seed, submission.ID, candidates[j].UserID)
		if leftHash != rightHash {
			return leftHash < rightHash
		}
		return candidates[i].UserID < candidates[j].UserID
	})
	return candidates[0].UserID, true, nil
}

func spread(pool []Reviewer, loads map[int64]Load) int {
	if len(pool) == 0 {
		return 0
	}
	minimum, maximum := loads[pool[0].UserID].Assigned, loads[pool[0].UserID].Assigned
	for _, reviewer := range pool[1:] {
		value := loads[reviewer.UserID].Assigned
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	return maximum - minimum
}

func blocked(submission Submission, reason string) Blocked {
	return Blocked{SubmissionID: fmt.Sprintf("%d", submission.ID), Title: submission.Title, Student: submission.Student, Reason: reason}
}
