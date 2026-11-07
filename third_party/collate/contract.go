package collate

import "unicode/utf8"

// A contraction may match a non-contiguous sequence of bytes in an input string. For example, if there is a contraction for <a, combining_ring>,
// it matches the sequence <a, combining_cedilla, combining_ring>, as combining_cedilla does not block combining_ring. ctScannerString does not
// automatically skip over non-blocking non-starters, but rather retains the state of the last match and leaves it up to the user to continue the
// match at the appropriate points.
type ctScannerString struct {
	states []contractionState
	s      string
	n      int
	index  int
	pindex int
	done   bool
}

type contractionState = struct{ L, H, N, I uint8 }

func scannerString(index, n int, str string) ctScannerString {
	return ctScannerString{s: str, states: contractionTries[index:], n: n}
}

// result returns the offset i and bytes consumed p so far. If no suffix matched, i and p will be 0.
func (s *ctScannerString) result() (i, p int) {
	return s.index, s.pindex
}

const (
	final   = 0
	noIndex = 0xFF
)

// scan matches the longest suffix at the current location in the input and returns the number of bytes consumed.
func (s *ctScannerString) scan(p int) int {
	pr := p // the p at the rune start
	str := s.s
	states, n := s.states, s.n
	for i := 0; i < n && p < len(str); {
		e := states[i]
		c := str[p]
		if c >= e.L {
			if e.L == c {
				p++
				if e.I != noIndex {
					s.index = int(e.I)
					s.pindex = p
				}
				if e.N != final {
					i, states, n = 0, states[int(e.H)+n:], int(e.N)
					if p >= len(str) || utf8.RuneStart(str[p]) {
						s.states, s.n, pr = states, n, p
					}
				} else {
					s.done = true
					return p
				}
				continue
			} else if e.N == final && c <= e.H {
				p++
				s.done = true
				s.index = int(c-e.L) + int(e.I)
				s.pindex = p
				return p
			}
		}
		i++
	}
	return pr
}
