package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReviewableSnapshotExcludesRejectionsBeforeAndAfterCapture(t *testing.T) {
	raw := []byte(`{"student":{"id":9223372036854775806},"categoryScores":{"moral":5},"details":{"baseItems":[],"items":[{"id":"1","score":0,"forceRejection":{"reason":"rejected"}},{"id":"2","score":5},{"id":"3","score":0,"forceRejection":null},{"id":"4","score":-2},{"id":"5","score":0,"forcedScore":{"previousScore":5}}]}}`)
	before := string(raw)
	filtered, err := filterRejectedSnapshotItems(raw, map[string]bool{"2": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{`"id":"1"`, `"id":"2"`} {
		if bytes.Contains(filtered, []byte(excluded)) {
			t.Fatalf("rejected item retained: %s", filtered)
		}
	}
	for _, retained := range []string{`"id":"3"`, `"id":"4"`, `"id":"5"`, `9223372036854775806`, `"moral":5`} {
		if !bytes.Contains(filtered, []byte(retained)) {
			t.Fatalf("snapshot lost %s", retained)
		}
	}
	if string(raw) != before {
		t.Fatal("stored snapshot must not change")
	}
}

func TestAnonymousBlindAuditSnapshotRemovesReviewIdentities(t *testing.T) {
	raw := []byte(`{
		"student":{"id":9223372036854775806,"sid":"20260001","name":"被复核学生"},
		"details":{"items":[{"reviews":[
			{"id":"91","reviewerId":"81","reviewerSid":"G001","reviewer":"初审甲","decision":"accepted","score":5,"reason":"材料完整","spentSeconds":90,"at":"2026-08-03T08:00:00Z"},
			{"id":"92","reviewerId":"82","reviewerSid":"G002","reviewer":"初审乙","decision":"adjusted","score":4,"reason":"按规则下调","spentSeconds":80,"at":"2026-08-03T08:05:00Z"}
		]}]}}
	`)

	got, err := anonymousBlindAuditSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{`"reviewer"`, `"reviewerSid"`, `"reviewerId"`, `"id":"91"`, "初审甲", "G001"} {
		if bytes.Contains(got, []byte(secret)) {
			t.Fatalf("anonymous snapshot still contains %q: %s", secret, got)
		}
	}
	for _, retained := range []string{`"decision":"accepted"`, `"score":5`, `"reason":"材料完整"`, `"spentSeconds":90`, `9223372036854775806`} {
		if !bytes.Contains(got, []byte(retained)) {
			t.Fatalf("anonymous snapshot lost %q: %s", retained, got)
		}
	}
	if !json.Valid(got) {
		t.Fatalf("anonymous snapshot is invalid JSON: %s", got)
	}
}

// An appealed item carries a second and third set of names: the pair who
// reconsidered it and the administrator who ended it. A final reviewer must
// read what they concluded without learning who any of them were.
func TestAnonymousBlindAuditSnapshotRemovesAppealIdentities(t *testing.T) {
	raw := []byte(`{
		"details":{
			"items":[{"appeals":[
				{"id":"7","round":1,"status":"escalated","reason":"证书等次记错了","resolutionScore":null,
				 "handlerId":null,"handlerSid":null,"handler":null,
				 "rereviews":[
					{"position":1,"reviewerId":"81","reviewerSid":"G001","reviewer":"初审甲","decision":"uphold","score":4,"reason":"仍按学院口径","spentSeconds":120,"at":"2026-08-05T08:00:00Z"},
					{"position":2,"reviewerId":"82","reviewerSid":"G002","reviewer":"初审乙","decision":"adjust","score":6,"reason":"证书确为省级","spentSeconds":150,"at":"2026-08-05T09:00:00Z"}
				 ]},
				{"id":"9","round":2,"status":"final","reason":"仍不服","resolutionScore":6,"resolutionReason":"按省级认定",
				 "handlerId":"70","handlerSid":"A001","handler":"曾班管","rereviews":[]}
			]}],
			"baseItems":[{"appeals":[
				{"id":"11","round":1,"status":"resolved","reason":"缺席记录有误","resolutionScore":18,
				 "handlerId":"70","handlerSid":"A001","handler":"曾班管",
				 "rereviews":[{"position":1,"reviewerId":"83","reviewerSid":"G003","reviewer":"录入丙","decision":"adjust","score":18,"reason":"考勤表核对无误","spentSeconds":60,"at":"2026-08-06T08:00:00Z"}]}
			]}]
		}}
	`)

	got, err := anonymousBlindAuditSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		`"handler"`, `"handlerSid"`, `"handlerId"`, `"reviewer"`, `"reviewerSid"`, `"reviewerId"`,
		"曾班管", "初审甲", "录入丙", "A001", "G003", `"id":"7"`, `"id":"11"`,
	} {
		if bytes.Contains(got, []byte(secret)) {
			t.Fatalf("anonymous snapshot still contains %q: %s", secret, got)
		}
	}
	for _, retained := range []string{
		`"round":1`, `"round":2`, `"status":"escalated"`, `"status":"final"`,
		`"decision":"uphold"`, `"decision":"adjust"`, `"reason":"证书确为省级"`,
		`"resolutionReason":"按省级认定"`, `"position":2`, `"spentSeconds":150`,
	} {
		if !bytes.Contains(got, []byte(retained)) {
			t.Fatalf("anonymous snapshot lost %q: %s", retained, got)
		}
	}
	if !json.Valid(got) {
		t.Fatalf("anonymous snapshot is invalid JSON: %s", got)
	}
}
