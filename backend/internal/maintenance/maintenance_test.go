package maintenance

import (
	"testing"
	"time"
)

func localTime(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, shanghai)
}

func TestReminderDue(t *testing.T) {
	tests := []struct {
		name  string
		now   time.Time
		close time.Time
		days  int
		due   bool
	}{
		{name: "seven days at nine", now: localTime(2026, 8, 1, 9), close: localTime(2026, 8, 8, 18), days: 7, due: true},
		{name: "three days", now: localTime(2026, 8, 5, 14), close: localTime(2026, 8, 8, 18), days: 3, due: true},
		{name: "one day", now: localTime(2026, 8, 7, 12), close: localTime(2026, 8, 8, 18), days: 1, due: true},
		{name: "too early", now: localTime(2026, 8, 7, 8), close: localTime(2026, 8, 8, 18), due: false},
		{name: "other day", now: localTime(2026, 8, 6, 12), close: localTime(2026, 8, 8, 18), days: 2, due: false},
		{name: "closed", now: localTime(2026, 8, 8, 19), close: localTime(2026, 8, 8, 18), due: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			days, due := reminderDue(test.now, test.close)
			if days != test.days || due != test.due {
				t.Fatalf("got (%d,%v), want (%d,%v)", days, due, test.days, test.due)
			}
		})
	}
}
