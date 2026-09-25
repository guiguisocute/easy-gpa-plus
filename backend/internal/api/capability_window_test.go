package api

import (
	"testing"
	"time"

	"easygpa/backend/internal/scheme"
)

func TestCapabilityWindowSeparatesSealingFromLockdown(t *testing.T) {
	open := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	closeAt := open.Add(24 * time.Hour)
	lockdown := closeAt.Add(24 * time.Hour)
	cfg := scheme.Config{
		Window: scheme.Window{Open: open, Close: closeAt, Lockdown: &lockdown},
		Capabilities: scheme.Capabilities{
			Submit: true, Edit: true, Review: true, Appeal: true, Arbitrate: true, StudentReport: true,
		},
	}
	for _, capability := range []struct {
		key       string
		afterSeal bool
	}{
		{"submit", false}, {"edit", false}, {"studentReport", false},
		{"review", true}, {"appeal", true}, {"arbitrate", true},
	} {
		t.Run(capability.key, func(t *testing.T) {
			for _, phase := range []struct {
				name string
				now  time.Time
				want bool
			}{
				{"before open", open.Add(-time.Nanosecond), false},
				{"at open", open, true},
				{"before seal", closeAt.Add(-time.Nanosecond), true},
				{"at seal", closeAt, capability.afterSeal},
				{"after seal", closeAt.Add(time.Hour), capability.afterSeal},
				{"before lockdown", lockdown.Add(-time.Nanosecond), capability.afterSeal},
				{"at lockdown", lockdown, false},
				{"after lockdown", lockdown.Add(time.Hour), false},
			} {
				t.Run(phase.name, func(t *testing.T) {
					err := ensureCapability(cfg, capability.key, phase.now)
					if got := err == nil; got != phase.want {
						t.Fatalf("allowed = %v, want %v (error: %v)", got, phase.want, err)
					}
				})
			}
			withoutLockdown := cfg
			withoutLockdown.Window.Lockdown = nil
			if got := ensureCapability(withoutLockdown, capability.key, lockdown.AddDate(1, 0, 0)) == nil; got != capability.afterSeal {
				t.Fatalf("allowed without lockdown = %v, want %v", got, capability.afterSeal)
			}
			disabled := withoutLockdown
			disabled.Capabilities = scheme.Capabilities{}
			for _, now := range []time.Time{open, closeAt, lockdown} {
				if ensureCapability(disabled, capability.key, now) == nil {
					t.Fatal("a disabled capability must remain closed")
				}
			}
		})
	}
	if ensureCapability(cfg, "unknown", open) == nil || ensureCapability(cfg, "unknown", closeAt) == nil {
		t.Fatal("unknown capabilities must remain closed")
	}
	if !reviewEditingOpen(cfg, closeAt) || reviewEditingOpen(cfg, lockdown) {
		t.Fatal("review history editing must follow the review window")
	}
}
