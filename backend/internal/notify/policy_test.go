package notify

import (
	"testing"
	"time"
)

func TestQuietDelayOvernight(t *testing.T) {
	tests := []struct {
		name string
		now  string
		want time.Duration
	}{
		{name: "before quiet", now: "2026-08-08T21:59:00+08:00", want: 0},
		{name: "evening", now: "2026-08-08T22:30:00+08:00", want: 8*time.Hour + 30*time.Minute},
		{name: "morning", now: "2026-08-09T06:30:00+08:00", want: 30 * time.Minute},
		{name: "at end", now: "2026-08-09T07:00:00+08:00", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, test.now)
			if err != nil {
				t.Fatal(err)
			}
			got, err := quietDelay(now, "22:00", "07:00")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("quietDelay() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestQuietDelayDaytime(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-08-08T12:30:00+08:00")
	got, err := quietDelay(now, "12:00", "13:15")
	if err != nil {
		t.Fatal(err)
	}
	if got != 45*time.Minute {
		t.Fatalf("quietDelay() = %s, want 45m", got)
	}
}
