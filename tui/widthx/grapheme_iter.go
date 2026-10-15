package widthx

import (
	"unicode/utf8"
)

// graphemeIter walks the extended grapheme clusters of a string (FirstGrapheme, Intl.Segmenter's boundaries) for the per-frame width, slice and wrap paths. It computes only grapheme boundaries and widths and lives on the caller's stack.
//
// A printable ASCII byte followed by another ASCII byte, or by the end of the
// string, is always a complete one-cell cluster: every rule that can join a
// following code point (Extend, ZWJ, SpacingMark, Prepend, regional-indicator
// and emoji sequences) involves only non-ASCII code points, and CR LF starts
// with a control byte. Those bytes skip the Unicode property lookups entirely.
// The boundary state after such a cluster equals the start-of-text state, so
// the walk resumes with state -1.
type graphemeIter struct {
	remaining string
	cluster   string
	width     int
	state     int
}

func newGraphemeIter(s string) graphemeIter {
	return graphemeIter{remaining: s, state: -1}
}

// Next advances to the next cluster and reports whether there was one.
func (g *graphemeIter) Next() bool {
	s := g.remaining
	if s == "" {
		g.cluster = ""
		g.width = 0
		return false
	}
	if c := s[0]; c >= 0x20 && c <= 0x7E && (len(s) == 1 || s[1] < utf8.RuneSelf) {
		g.cluster, g.remaining, g.width, g.state = s[:1], s[1:], 1, -1
		return true
	}
	g.cluster, g.remaining = FirstGrapheme(s)
	g.width, g.state = GraphemeWidth(g.cluster), -1
	return true
}

// Str returns the current cluster.
func (g *graphemeIter) Str() string { return g.cluster }

// Width returns upstream graphemeWidth of the current cluster.
func (g *graphemeIter) Width() int { return g.width }
