package codingagent

import (
	"testing"
	"time"
)

// TestCtrlCExits locks the upstream handleCtrlC escalation: the second
// Ctrl+C within 500ms exits; otherwise it only clears the editor.
func TestCtrlCExits(t *testing.T) {
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		now  time.Time
		want bool
	}{
		{"no prior press never exits", time.Time{}, base, false},
		{"second press well within window exits", base, base.Add(200 * time.Millisecond), true},
		{"second press just under 500ms exits", base, base.Add(499 * time.Millisecond), true},
		{"exactly 500ms does not exit", base, base.Add(500 * time.Millisecond), false},
		{"slow second press clears, no exit", base, base.Add(2 * time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ctrlCExits(tc.last, tc.now); got != tc.want {
				t.Errorf("ctrlCExits = %v, want %v", got, tc.want)
			}
		})
	}
}
