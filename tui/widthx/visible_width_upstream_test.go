package widthx

import "testing"

// .upstream/v0.99.2/packages/tui/test/visible-width.test.ts:5.
func TestUpstreamVisibleWidthStyledText(t *testing.T) {
	for _, group := range []struct {
		name  string
		cases []struct {
			input string
			want  int
		}
	}{
		// visible-width.test.ts:6.
		{"measures styled ASCII without counting escape sequences", []struct {
			input string
			want  int
		}{
			{"\x1b[38;5;4mhello\x1b[39m world", 11},
			{"\x1b]8;;https://example.com\x07link\x1b]8;;\x07", 4},
			{"\x1b]133;A\x1b\\prompt", 6},
			{"\x1b_pi:c\x07cursor", 6},
		}},
		// visible-width.test.ts:13.
		{"counts tabs as three columns in styled text", []struct {
			input string
			want  int
		}{{"\x1b[1ma\tb\x1b[22m", 5}}},
		// visible-width.test.ts:17.
		{"measures styled non-ASCII text", []struct {
			input string
			want  int
		}{
			{"\x1b[31m日本\x1b[39m ok", 7},
			{"\x1b[31m─→\x1b[39m", 2},
		}},
		// visible-width.test.ts:22.
		{"treats unterminated escape sequences as zero-width control characters", []struct {
			input string
			want  int
		}{
			{"\x1b[31", 3},
			{"a\x1b", 1},
		}},
	} {
		t.Run(group.name, func(t *testing.T) {
			for _, tc := range group.cases {
				if got := VisibleWidth(tc.input); got != tc.want {
					t.Errorf("VisibleWidth(%q) = %d, want %d", tc.input, got, tc.want)
				}
			}
		})
	}
}
