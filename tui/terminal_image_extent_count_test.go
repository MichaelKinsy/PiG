package tui

import "testing"

// Pi's getImageLineExtent reads its counts with Number.parseInt(capture, 10) (terminal-image.ts at 787005a40: `c=` of a
// Kitty command, the cursor-up count and `width=` of an iTerm2 line). parseImageExtentCount is that parse as a cell count:
// a count at or beyond imageExtentUnbounded, and Pi's NaN, is imageExtentUnbounded. Each capture is (\d+), so the rows a
// line can deliver are the runs of digits; the others pin the parse itself.
func TestParseImageExtentCountIsNumberParseInt(t *testing.T) {
	cases := []struct {
		name, digits string
		want         int
	}{
		{"plain digits", "12", 12},
		{"zero", "0", 0},
		{"leading zeros", "007", 7},
		{"last count below the cap", "1073741823", 1073741823},
		{"the cap", "1073741824", imageExtentUnbounded},
		{"past int32: Pi 4294967296", "4294967296", imageExtentUnbounded},
		{"past 2^53: Pi 9007199254740992", "9007199254740993", imageExtentUnbounded},
		{"past int64: Pi 1e+23", "99999999999999999999999", imageExtentUnbounded},
		{"leading plus", "+12", 12},
		{"leading minus", "-12", -12},
		{"past int64 below zero: Pi -1e+23", "-99999999999999999999999", -imageExtentUnbounded},
		{"leading space", " 12", 12},
		{"leading tab and newline", "\t\n12", 12},
		{"trailing space", "12 ", 12},
		{"trailing text", "12px", 12},
		{"decimal point", "1.5", 1},
		{"empty: Pi NaN", "", imageExtentUnbounded},
		{"no digits: Pi NaN", "px", imageExtentUnbounded},
	}
	for _, tc := range cases {
		if got := parseImageExtentCount(tc.digits); got != tc.want {
			t.Errorf("%s: parseImageExtentCount(%q) = %d, want %d", tc.name, tc.digits, got, tc.want)
		}
	}
}
