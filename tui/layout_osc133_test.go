package tui

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// osc133ZonePrefix is upstream's OSC133_ZONE_PREFIX, the reference stripOsc133ZonePrefix is held to.
var osc133ZonePrefix = lazyregexp.New("^(?:\x1b\\]133;[ABC](?:\x07|\x1b\\\\))+")

// TestStripOsc133ZonePrefixMatchesRegexp pins the prefix fast path to the
// upstream OSC133_ZONE_PREFIX replacement for leading, repeated, mid-line,
// malformed and ST-terminated marks.
func TestStripOsc133ZonePrefixMatchesRegexp(t *testing.T) {
	cases := []struct{ line, want string }{
		{"", ""},
		{"\x1b]133;", "\x1b]133;"},
		{"plain", "plain"},
		{"\x1b[1mstyled\x1b[0m", "\x1b[1mstyled\x1b[0m"},
		{"\x1b]133;A\x07prompt", "prompt"},
		{"\x1b]133;A\x07\x1b]133;B\x1b\\input", "input"},
		{"\x1b]133;C\x07\x1b]133;D\x07out", "\x1b]133;D\x07out"},
		{"\x1b]133;Z\x07bad", "\x1b]133;Z\x07bad"},
		{"\x1b]133;A", "\x1b]133;A"},
		{"text\x1b]133;A\x07mid", "text\x1b]133;A\x07mid"},
		{"\t\x1b]133;A\x07indented", "\t\x1b]133;A\x07indented"},
		{"\x1b]133;A\x07", ""},
		{"\x1b]133;A\x1b\\", ""},
		{"\x1b]133;A\x1b", "\x1b]133;A\x1b"},
		{"\x1b]133;A\x1b]", "\x1b]133;A\x1b]"},
		{"\x1b]133;A\x07\x1b]133;B\x07\x1b]133;C\x07\x1b]133;A\x1b\\\x1b[0m", "\x1b[0m"},
		{"\x1b]133;A\x07\x1b]133;D\x07\x1b]133;B\x07", "\x1b]133;D\x07\x1b]133;B\x07"},
		{"\x1b]133;A\x07\x1b]133;", "\x1b]133;"},
		{"\x1b]133;A\x07\x1b]133;B", "\x1b]133;B"},
		{"\x1b]133;a\x07lower", "\x1b]133;a\x07lower"},
		{"\x1b]133;\x07", "\x1b]133;\x07"},
		{"\x1b]133;AB\x07", "\x1b]133;AB\x07"},
		{"\x1b]1330;A\x07", "\x1b]1330;A\x07"},
		{"\x1b]133;A\x07é世\xff", "é世\xff"},
		{"\xff\x1b]133;A\x07", "\xff\x1b]133;A\x07"},
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

// TestStripOsc133ZonePrefixEqualsTheRegexpOnGeneratedLines compares the strip to the
// upstream OSC133_ZONE_PREFIX replacement on 5,000 fixed-seed lines built from the
// pieces the pattern is sensitive to: the introducer and its fragments, the zone letters
// (the valid A B C, others), both terminators and their fragments, repeated and partial
// marks, escape sequences, and multi-byte and invalid UTF-8.
func TestStripOsc133ZonePrefixEqualsTheRegexpOnGeneratedLines(t *testing.T) {
	pieces := []string{
		"\x1b", "]", "133;", "\x1b]", "\x1b]133;", "A", "B", "C", "D", "Z", "a", "\x07", "\x1b\\", "\\", ";", "1", "33",
		"\x1b]133;A\x07", "\x1b]133;B\x1b\\", "\x1b]133;C\x07", "\x1b]133;D\x07", "\x1b]133;A\x1b\\",
		"\x1b[0m", "\x1b[1;31m", "\x1b_Gi=1;\x1b\\", "text", " ", "\t", "\n", "é", "世", "😀", "\xff", "\xc3", "\x00",
	}
	marks := []string{"\x1b]133;A\x07", "\x1b]133;B\x07", "\x1b]133;C\x07", "\x1b]133;A\x1b\\", "\x1b]133;B\x1b\\", "\x1b]133;C\x1b\\"}
	rng := rand.New(rand.NewPCG(133, 2026))
	pick := func(from []string) string { return from[rng.IntN(len(from))] }
	stripped := 0
	for i := range 5000 {
		var b strings.Builder
		if i%2 == 0 {
			for range rng.IntN(4) {
				b.WriteString(pick(marks))
			}
		}
		for range rng.IntN(9) {
			b.WriteString(pick(pieces))
		}
		line := b.String()
		want := osc133ZonePrefix.ReplaceAllString(line, "")
		if got := stripOsc133ZonePrefix(line); got != want {
			t.Fatalf("line %d %q: stripOsc133ZonePrefix = %q, regexp = %q", i, line, got, want)
		}
		if want != line {
			stripped++
		}
	}
	if stripped < 1000 {
		t.Fatalf("only %d of 5000 generated lines start with a zone mark: the generator no longer covers the strip", stripped)
	}
}

// TestStripOsc133ZonePrefixAllocatesNothing pins that the strip is a substring of its
// line, a marked line included: it runs for every line a frame rebuilds, and the
// regexp's ReplaceAllString allocated its machine and result for each marked one.
func TestStripOsc133ZonePrefixAllocatesNothing(t *testing.T) {
	for _, line := range []string{
		"plain",
		"\x1b]133;Z\x07bad",
		"\x1b]133;A",
		"\x1b]133;A\x07prompt",
		"\x1b]133;A\x07\x1b]133;B\x1b\\input",
		"\x1b]133;C\x07\x1b]133;D\x07out",
	} {
		if got := testing.AllocsPerRun(100, func() { _ = stripOsc133ZonePrefix(line) }); got != 0 {
			t.Errorf("stripOsc133ZonePrefix(%q) allocates %v times, want 0", line, got)
		}
	}
}
