// Package review owns the submission review state machine. Keeping transition
// rules in one place prevents HTTP handlers and workers from drifting apart.
package review

import (
	"errors"
	"fmt"

	"easygpa/backend/internal/scheme"
)

type Status string

const (
	StatusDraft       Status = "draft"
	StatusPending     Status = "pending"
	StatusConsensus   Status = "consensus"
	StatusScored      Status = "scored"
	StatusAppealing   Status = "appealing"
	StatusArbitrating Status = "arbitrating"
	StatusLocked      Status = "locked"
)

type Decision string

const (
	DecisionAccept Decision = "accepted"
	DecisionAdjust Decision = "adjusted"
	DecisionReject Decision = "rejected"
)

type Result struct {
	ReviewerID int64
	Decision   Decision
	Score      scheme.Points
	Reason     string
}

type Outcome struct {
	Status     Status
	FinalScore *scheme.Points
	Conflict   bool
}

var ErrIllegalTransition = errors.New("illegal submission state transition")

func CanTransition(from, to Status) bool {
	allowed := map[Status]map[Status]bool{
		StatusDraft:       {StatusPending: true},
		StatusPending:     {StatusDraft: true, StatusConsensus: true, StatusScored: true, StatusArbitrating: true},
		StatusConsensus:   {StatusScored: true, StatusArbitrating: true},
		StatusScored:      {StatusAppealing: true, StatusArbitrating: true, StatusLocked: true},
		StatusAppealing:   {StatusScored: true, StatusArbitrating: true, StatusLocked: true},
		StatusArbitrating: {StatusScored: true, StatusAppealing: true, StatusLocked: true},
	}
	return allowed[from][to]
}

func Transition(from, to Status) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
	}
	return nil
}

// Reconcile preserves the back-to-back rule: callers pass only persisted
// results after a reviewer submits; no endpoint needs to reveal the peer result.
func Reconcile(expectedReviewers int, results []Result) (Outcome, error) {
	if expectedReviewers != 1 && expectedReviewers != 2 {
		return Outcome{}, errors.New("expectedReviewers must be one or two")
	}
	seen := make(map[int64]bool, len(results))
	for _, result := range results {
		if result.ReviewerID == 0 || seen[result.ReviewerID] {
			return Outcome{}, errors.New("reviewer results must have distinct non-zero IDs")
		}
		seen[result.ReviewerID] = true
		if result.Decision != DecisionAccept && result.Decision != DecisionAdjust && result.Decision != DecisionReject {
			return Outcome{}, fmt.Errorf("unknown decision %q", result.Decision)
		}
		if result.Decision == DecisionReject && result.Score != 0 {
			return Outcome{}, errors.New("a rejected result must score zero")
		}
	}
	if len(results) < expectedReviewers {
		return Outcome{Status: StatusConsensus}, nil
	}
	if len(results) > expectedReviewers {
		return Outcome{}, errors.New("more results than expected reviewers")
	}
	if expectedReviewers == 1 {
		score := results[0].Score
		return Outcome{Status: StatusScored, FinalScore: &score}, nil
	}
	if results[0].Decision == results[1].Decision && results[0].Score == results[1].Score {
		score := results[0].Score
		return Outcome{Status: StatusScored, FinalScore: &score}, nil
	}
	return Outcome{Status: StatusArbitrating, Conflict: true}, nil
}
