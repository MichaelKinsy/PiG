package main

import (
	"runtime"
	"testing"
)

// Cases follow chalk 6.0.0 vendor/supports-color/index.js _supportsColor.
func TestChalkColorLevelMatchesSupportsColor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("supports-color derives Windows levels from the OS build")
	}
	for _, tc := range []struct {
		name  string
		env   map[string]string
		argv  []string
		isTTY bool
		want  int
	}{
		{"xterm-256color", map[string]string{"TERM": "xterm-256color"}, nil, true, 2},
		{"force-color-0", map[string]string{"TERM": "xterm-256color", "FORCE_COLOR": "0"}, nil, true, 0},
		{"force-color-false", map[string]string{"TERM": "xterm-256color", "FORCE_COLOR": "false"}, nil, true, 0},
		{"term-dumb", map[string]string{"TERM": "dumb"}, nil, true, 0},
		{"term-dumb-forced", map[string]string{"TERM": "dumb", "FORCE_COLOR": "true"}, nil, true, 1},
		{"no-color-ignored", map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"}, nil, true, 2},
		{"not-tty", map[string]string{"TERM": "xterm-256color"}, nil, false, 0},
		{"force-color-numeric", map[string]string{"FORCE_COLOR": "9"}, nil, false, 3},
		{"no-color-flag", map[string]string{"TERM": "xterm-256color"}, []string{"--no-color"}, true, 0},
		{"no-color-flag-after-terminator", map[string]string{"TERM": "xterm-256color"}, []string{"--", "--no-color"}, true, 2},
		{"ci-plain", map[string]string{"TERM": "xterm-256color", "CI": "1"}, nil, true, 0},
		{"truecolor", map[string]string{"TERM": "xterm", "COLORTERM": "truecolor"}, nil, true, 3},
		{"empty-term", map[string]string{}, nil, true, 0},
	} {
		if got := chalkColorLevel(tc.env, tc.argv, tc.isTTY); got != tc.want {
			t.Errorf("%s: level = %d, want %d", tc.name, got, tc.want)
		}
	}
}
