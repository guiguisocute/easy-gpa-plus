package api

import "testing"

func TestValidAcademicYear(t *testing.T) {
	for value, want := range map[string]bool{"2025-2026": true, "2026-2027": true, "2025-2027": false, "2026": false, "+025-0026": false, "": false} {
		if got := validAcademicYear(value); got != want {
			t.Errorf("validAcademicYear(%q) = %v, want %v", value, got, want)
		}
	}
}
