package tui

import (
	"testing"
	"time"
)

// terminal.ts hands resolveEscapeTimeoutMs() to StdinBuffer, which passes it to setTimeout: Node truncates a
// fractional delay and waits 1ms for a sub-millisecond or overflowing one (TimeoutOverflowWarning).
func TestProcessStdinBufferEscapeTimeoutFollowsNodeSetTimeout(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want time.Duration
	}{
		{"25", 25 * time.Millisecond},
		{"25.9", 25 * time.Millisecond},
		{"0.5", time.Millisecond},
		{"3e9", time.Millisecond},
		{"1e30", time.Millisecond},
	} {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("PI_TUI_ESC_TIMEOUT", tc.env)
			if got := newProcessStdinBuffer().escapeTimeout; got != tc.want {
				t.Fatalf("PI_TUI_ESC_TIMEOUT=%s: escape timeout %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
