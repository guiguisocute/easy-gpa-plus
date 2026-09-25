package agentcontext

import "testing"

func TestCollectivePagesUseMemberContextWithoutAdminPrivilege(t *testing.T) {
	for _, role := range []string{"student", "group", "class_admin"} {
		for _, view := range []string{"govHome", "govReviews", "govProposals", "govGpa", "govSettle"} {
			got, err := Resolve(role, view, "")
			if err != nil || got.EffectiveRole != "student" || got.View != view {
				t.Fatalf("collective page context: %#v %v", got, err)
			}
		}
	}
	if _, err := Resolve("student", "govSetupRoster", ""); err == nil {
		t.Fatal("member cannot assume initialization responsibility")
	}
}

func TestResolveUsesPageRoleAsEffectiveView(t *testing.T) {
	got, err := Resolve("class_admin", "stuSubmit", "draft-7")
	if err != nil {
		t.Fatal(err)
	}
	if got.ActualRole != "class_admin" || got.EffectiveRole != "student" || got.View != "stuSubmit" || got.ViewLabel != "提交材料" || got.ResourceID != "draft-7" {
		t.Fatalf("context = %#v", got)
	}
}

func TestResolveRejectsEscalationAndUnknownViews(t *testing.T) {
	for _, tc := range []struct{ role, view string }{
		{"student", "revTasks"},
		{"group", "admBoard"},
		{"class_admin", "opsAgent"},
		{"ops", "stuHome"},
		{"student", "not-a-view"},
	} {
		if got, err := Resolve(tc.role, tc.view, ""); err == nil {
			t.Fatalf("Resolve(%q, %q) accepted escalation as %#v", tc.role, tc.view, got)
		}
	}
}

func TestResolveDefaultsToAuthenticatedRoleWithoutPage(t *testing.T) {
	got, err := Resolve("group", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectiveRole != "group" || got.View != "" || got.ViewLabel != "未指定页面" {
		t.Fatalf("context = %#v", got)
	}
}

func TestRemovedFinalReviewViews(t *testing.T) {
	for _, view := range []string{"admFinals", "revFinal"} {
		if _, err := Resolve("class_admin", view, ""); err == nil {
			t.Fatalf("removed view accepted: %s", view)
		}
	}
}
