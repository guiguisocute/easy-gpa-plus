// Package governance defines class participation independently of registration
// and score claims. Only explicit enrollment creates voting/reviewer rights.
package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

const EnrollmentNotice = 7 * 24 * time.Hour
const MembershipCooling = 24 * time.Hour
const ReviewTimeout = 48 * time.Hour

type Member struct {
	ID         int64      `json:"id,string"`
	Registered bool       `json:"registered"`
	Active     bool       `json:"active"`
	JoinedAt   *time.Time `json:"joinedAt"`
	LeftAt     *time.Time `json:"leftAt"`
	Reviewer   bool       `json:"reviewer"`
	Submitted  bool       `json:"submitted"`
	Load       int        `json:"load"`
}

// Submitted intentionally has no effect on either voting or reviewing.
func Eligible(m Member, at time.Time) bool {
	return m.ID > 0 && m.Active && m.Registered && m.JoinedAt != nil && !at.Before(m.JoinedAt.Add(MembershipCooling)) && (m.LeftAt == nil || at.Before(*m.LeftAt))
}

func Electorate(members []Member, at time.Time, excluded map[int64]bool) []int64 {
	ids := make([]int64, 0)
	seen := map[int64]bool{}
	for _, m := range members {
		if Eligible(m, at) && !excluded[m.ID] && !seen[m.ID] {
			ids = append(ids, m.ID)
			seen[m.ID] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Threshold is an absolute number of affirmative votes. Missing, unregistered
// and abstaining users never become affirmative votes. A deliberately
// unreachable protected threshold is returned instead of silently lowering it.
func Threshold(kind string, electorate, roster int) (int, error) {
	if electorate < 1 || roster < electorate {
		return 0, errors.New("投票名单不完整")
	}
	switch kind {
	case "ordinary", "bonus":
		return max(3, electorate/2+1), nil
	case "activate":
		return max(3, (2*electorate+2)/3), nil
	case "protected":
		return max(5, (2*electorate+2)/3, (roster+2)/3), nil
	default:
		return 0, errors.New("不支持的表决类型")
	}
}

// Compact classes use independent three-person appeal panels. The choice is
// frozen at activation, never reduced in response to an inconvenient dispute.
type Profile struct {
	Name    string `json:"name"`
	Maximum int    `json:"maximum"`
	Appeal  int    `json:"appeal"`
	Minimum int    `json:"minimum"`
}

func ReviewProfile(reviewers int) (Profile, error) {
	if reviewers >= 11 {
		return Profile{"standard", 5, 5, 11}, nil
	}
	if reviewers >= 7 {
		return Profile{"compact", 3, 3, 7}, nil
	}
	return Profile{}, errors.New("至少需要 7 名主动加入的评审成员，才能保留本人回避和独立申诉席位")
}

func Draw(members []Member, at time.Time, excluded map[int64]bool, count int, seed string) ([]int64, error) {
	if count < 1 || len(seed) < 32 {
		return nil, errors.New("抽签参数无效")
	}
	pool := make([]Member, 0)
	seen := map[int64]bool{}
	for _, m := range members {
		if Eligible(m, at) && m.Reviewer && !excluded[m.ID] && !seen[m.ID] {
			pool = append(pool, m)
			seen[m.ID] = true
		}
	}
	if len(pool) < count {
		return nil, errors.New("回避后没有足够的独立评审成员，等待补位")
	}
	hash := func(id int64) string {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", seed, id)))
		return hex.EncodeToString(sum[:])
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].Load != pool[j].Load {
			return pool[i].Load < pool[j].Load
		}
		return hash(pool[i].ID) < hash(pool[j].ID)
	})
	result := make([]int64, count)
	for i := range result {
		result[i] = pool[i].ID
	}
	return result, nil
}

type Opinion struct {
	Reviewer int64
	Decision string
	Score    int64
	Category string
	Item     string
}
type Outcome struct {
	State   string
	Opinion *Opinion
	Seats   int
}

func Reconcile(opinions []Opinion, seats, maximum int) (Outcome, error) {
	if (maximum != 3 && maximum != 5) || (seats != 2 && seats != 3 && seats != 5) || seats > maximum || len(opinions) > seats {
		return Outcome{}, errors.New("评审席位无效")
	}
	seen := map[int64]bool{}
	counts := map[string]int{}
	for _, o := range opinions {
		if o.Reviewer <= 0 || seen[o.Reviewer] || (o.Decision != "accepted" && o.Decision != "rejected") || (o.Decision == "rejected" && o.Score != 0) {
			return Outcome{}, errors.New("评审意见无效或重复")
		}
		seen[o.Reviewer] = true
	}
	if len(opinions) < seats {
		return Outcome{State: "waiting", Seats: seats}, nil
	}
	for _, o := range opinions {
		key := fmt.Sprintf("%s:%d:%s:%s", o.Decision, o.Score, o.Category, o.Item)
		counts[key]++
		if counts[key] >= seats/2+1 {
			copy := o
			return Outcome{State: "decided", Opinion: &copy, Seats: seats}, nil
		}
	}
	if seats == 2 {
		return Outcome{State: "expand", Seats: 3}, nil
	}
	if seats < maximum {
		return Outcome{State: "expand", Seats: 5}, nil
	}
	return Outcome{State: "deliberating", Seats: seats}, nil
}
