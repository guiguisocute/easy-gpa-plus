package scheme

import (
	"testing"
	"time"
)

func TestPublicityWindowBoundaries(t *testing.T) {
	start := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	w := DefaultWindow()
	if w.PublicityOpen(start) {
		t.Fatal("publicity must default closed")
	}
	w.Publicity = &PublicityWindow{Open: start, Close: end}
	for _, tc := range []struct {
		at   time.Time
		want bool
	}{
		{start.Add(-time.Nanosecond), false}, {start, true}, {end.Add(-time.Nanosecond), true}, {end, false},
	} {
		if got := w.PublicityOpen(tc.at); got != tc.want {
			t.Fatalf("at %s: got %v, want %v", tc.at, got, tc.want)
		}
	}
	// Publication is read-only and may continue after the term is locked.
	w.Open, w.Close, w.Lockdown = start.Add(-3*time.Hour), start.Add(-2*time.Hour), &start
	if err := ValidateTimeline(w, DefaultCapabilities(), DefaultHonorRoll()); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []PublicityWindow{{Open: start}, {Close: end}, {Open: start, Close: start}, {Open: end, Close: start}} {
		w.Publicity = &invalid
		if err := ValidateTimeline(w, DefaultCapabilities(), DefaultHonorRoll()); err == nil {
			t.Fatal("invalid publicity window accepted")
		}
	}
}
