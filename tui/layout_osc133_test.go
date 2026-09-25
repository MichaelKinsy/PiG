package tui

import "testing"

// TestStripOsc133ZonePrefixMatchesRegexp pins the prefix fast path to the
// upstream OSC133_ZONE_PREFIX replacement for leading, repeated, mid-line,
// malformed and ST-terminated marks.
func TestStripOsc133ZonePrefixMatchesRegexp(t *testing.T) {
	cases := []struct{ line, want string }{
		{"", ""},
		{"plain", "plain"},
		{"\x1b[1mstyled\x1b[0m", "\x1b[1mstyled\x1b[0m"},
		{"\x1b]133;A\x07prompt", "prompt"},
		{"\x1b]133;A\x07\x1b]133;B\x1b\\input", "input"},
		{"\x1b]133;C\x07\x1b]133;D\x07out", "\x1b]133;D\x07out"},
		{"\x1b]133;Z\x07bad", "\x1b]133;Z\x07bad"},
		{"\x1b]133;A", "\x1b]133;A"},
		{"text\x1b]133;A\x07mid", "text\x1b]133;A\x07mid"},
		{"\t\x1b]133;A\x07indented", "\t\x1b]133;A\x07indented"},
	}
	for _, tc := range cases {
		if got := stripOsc133ZonePrefix(tc.line); got != tc.want {
			t.Errorf("stripOsc133ZonePrefix(%q) = %q, want %q", tc.line, got, tc.want)
		}
		if ref := osc133ZonePrefix.ReplaceAllString(tc.line, ""); ref != tc.want {
			t.Errorf("regexp reference for %q = %q, want %q", tc.line, ref, tc.want)
		}
	}
}
