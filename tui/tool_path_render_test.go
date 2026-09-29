package tui

import "testing"

func TestBoldTextMatchesChalkLineAndResetScopes(t *testing.T) {
	// Pi 0.87.1 theme.ts:335-337 delegates bold to chalk, which reopens nested closes and closes/reopens at LF and CRLF boundaries.
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"title", "\x1b[1mtitle\x1b[22m"},
		{"a\nb", "\x1b[1ma\x1b[22m\n\x1b[1mb\x1b[22m"},
		{"a\r\nb", "\x1b[1ma\x1b[22m\r\n\x1b[1mb\x1b[22m"},
		{"a\x1b[22mb", "\x1b[1ma\x1b[1mb\x1b[22m"},
	} {
		if got := boldText(tc.input); got != tc.want {
			t.Errorf("bold(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
