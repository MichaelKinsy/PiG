package hljs

import (
	"slices"
	"strings"
)

// Text is code and its UTF-16 code units. A text that grows, as a streaming file does, converts only the text it gained: Grow
// keeps the units of the part of the code that a longer text cannot change.
// pig additive (D112): see hlCacheMax in the tui package. A Text holds the units a fresh conversion of its code returns, so a
// run over it is the run over the code.
type Text struct {
	code  string
	units []rune
	// settled is the length of the code, and settledUnits the length of the units, that a longer text starting with the code
	// converts the same way: the code without a trailing UTF-8 (or WTF-8) sequence that is still incomplete.
	settled      int
	settledUnits int
}

// NewText is the Text of code.
func NewText(code string) *Text { return new(Text).Grow(code) }

// Code is the text's code.
func (t *Text) Code() string { return t.code }

// Grow makes t the text of code, which starts with t's code, and returns it; code that does not start with it is converted
// whole. Only the code after the settled part is converted, and the units of t are appended to in place, so t is no longer
// the text it was and must not be used by anyone else.
func (t *Text) Grow(code string) *Text {
	if !strings.HasPrefix(code, t.code) {
		*t = Text{}
	}
	units := t.units[:t.settledUnits]
	units = slices.Grow(units, len(code)-t.settled)
	settled := settledLen(code)
	units = appendUnits(units, code[t.settled:settled])
	settledUnits := len(units)
	units = appendUnits(units, code[settled:])
	*t = Text{code: code, units: units, settled: settled, settledUnits: settledUnits}
	return t
}

// settledLen is the length of the code without a trailing sequence that more bytes would complete: a lead byte followed by
// fewer continuation bytes than it needs. The code before it converts the same whatever follows.
func settledLen(code string) int {
	for back := 1; back <= 3 && back <= len(code); back++ {
		i := len(code) - back
		c := code[i]
		switch {
		case c < 0x80:
			return len(code)
		case c < 0xc0:
			// a continuation byte: the lead byte is before it
		default:
			need := 2
			if c >= 0xf0 {
				need = 4
			} else if c >= 0xe0 {
				need = 3
			}
			if back < need {
				return i
			}
			return len(code)
		}
	}
	return len(code)
}
