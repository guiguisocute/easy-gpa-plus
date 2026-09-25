package api

import (
	"errors"
	"testing"

	"easygpa/backend/internal/scheme"
)

func TestIsScorableClassMemberIncludesOfficers(t *testing.T) {
	tests := []struct {
		role, status string
		want         bool
	}{
		{"student", "active", true},
		{"group", "active", true},
		{"class_admin", "active", true},
		{"student", "disabled", false},
		{"group", "paused", false},
		{"ops", "active", false},
		{"", "active", false},
	}
	for _, tt := range tests {
		if got := isScorableClassMember(tt.role, tt.status); got != tt.want {
			t.Fatalf("isScorableClassMember(%q, %q) = %v, want %v", tt.role, tt.status, got, tt.want)
		}
	}
}

func TestRejectIfNotObjectionTargetAllowsOfficers(t *testing.T) {
	if err := rejectIfNotObjectionTarget("group", "active"); err != nil {
		t.Fatalf("group should be an objection target: %v", err)
	}
	if err := rejectIfNotObjectionTarget("class_admin", "active"); err != nil {
		t.Fatalf("class_admin should be an objection target: %v", err)
	}
	err := rejectIfNotObjectionTarget("student", "disabled")
	var problem objectionProblem
	if err == nil || !errors.As(err, &problem) || problem.code != "not_student" {
		t.Fatalf("inactive member should be rejected as not_student, got %v", err)
	}
}

func TestDefaultBaseItemCountExcludesMaterialBackedItems(t *testing.T) {
	cfg := scheme.Config{Categories: []scheme.Category{
		{
			Key: "moral",
			BaseItems: []scheme.BaseItem{
				{Key: "conduct", Name: "日常表现", Full: 20},
				{Key: "activity", Name: "至少参加两项活动", Full: 40, StudentClaim: &scheme.BaseStudentClaim{Minimum: 2, Unit: "项"}},
			},
		},
		{Key: "practice", BaseItems: []scheme.BaseItem{{Key: "practice_base", Name: "实践基础分", Full: 10}}},
	}}
	if got := defaultBaseItemCount(cfg); got != 2 {
		t.Fatalf("defaultBaseItemCount() = %d, want 2", got)
	}
}
