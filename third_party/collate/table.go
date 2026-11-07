package collate

import (
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The trie associates the first full character of a UTF-8 string with a collation element. All but the last byte of a UTF-8 byte sequence
// name the window to read for the next byte; the last byte indexes a window of elements. x/text's trie indexes windows of one array of
// 64-entry blocks by the byte itself (0x80..0xBF); tables.go holds each window apart, so a continuation byte less 0x80 is the position.

const (
	t1 = 0x00 // 0000 0000
	tx = 0x80 // 1000 0000
	t2 = 0xC0 // 1100 0000
	t3 = 0xE0 // 1110 0000
	t4 = 0xF0 // 1111 0000
	t5 = 0xF8 // 1111 1000
)

// lookupString returns the trie value for the first UTF-8 encoding in s and the width in bytes of this encoding. The size is 0 if s does not hold
// enough bytes to complete the encoding. len(s) must be greater than 0. The bytes need not be valid UTF-8.
func lookupString(s string) (v Elem, sz int) {
	c0 := s[0]
	switch {
	case c0 < tx:
		return Elem(asciiValues[c0]), 1
	case c0 < t2:
		return 0, 1
	case c0 < t3:
		if len(s) < 2 {
			return 0, 0
		}
		i := leadWindows[c0-t2]
		c1 := s[1]
		if c1 < tx || t2 <= c1 {
			return 0, 1
		}
		return Elem(valueWindows[i][c1-tx]), 2
	case c0 < t4:
		if len(s) < 3 {
			return 0, 0
		}
		i := leadWindows[c0-t2]
		c1 := s[1]
		if c1 < tx || t2 <= c1 {
			return 0, 1
		}
		i = indexWindows[i][c1-tx]
		c2 := s[2]
		if c2 < tx || t2 <= c2 {
			return 0, 2
		}
		return Elem(valueWindows[i][c2-tx]), 3
	case c0 < t5:
		if len(s) < 4 {
			return 0, 0
		}
		i := leadWindows[c0-t2]
		c1 := s[1]
		if c1 < tx || t2 <= c1 {
			return 0, 1
		}
		i = indexWindows[i][c1-tx]
		c2 := s[2]
		if c2 < tx || t2 <= c2 {
			return 0, 2
		}
		i = indexWindows[i][c2-tx]
		c3 := s[3]
		if c3 < tx || t2 <= c3 {
			return 0, 3
		}
		return Elem(valueWindows[i][c3-tx]), 4
	}
	// Illegal rune
	return 0, 1
}

// appendNext appends the weights corresponding to the next rune or contraction in s. If a contraction is matched to a discontinuous sequence of
// runes, the weights for the interstitial runes are appended as well. It returns a new slice that includes the appended weights and the number
// of bytes consumed from s.
func appendNext(w []Elem, s string) (res []Elem, n int) {
	ce, sz := lookupString(s)
	tp := ce.ctype()
	if tp == ceNormal {
		if ce == 0 {
			r, _ := utf8.DecodeRuneInString(s)
			const (
				hangulSize  = 3
				firstHangul = 0xAC00
				lastHangul  = 0xD7A3
			)
			if r >= firstHangul && r <= lastHangul {
				// TODO: performance can be considerably improved here.
				n = sz
				var buf [16]byte // Used for decomposing Hangul.
				for b := norm.NFD.AppendString(buf[:0], s[:hangulSize]); len(b) > 0; b = b[sz:] {
					ce, sz = lookupString(string(b))
					w = append(w, ce)
				}
				return w, n
			}
			ce = makeImplicitCE(implicitPrimary(r))
		}
		w = append(w, ce)
	} else if tp == ceExpansionIndex {
		w = appendExpansion(w, ce)
	} else if tp == ceContractionIndex {
		n := 0
		w, n = matchContractionString(w, ce, s[sz:])
		sz += n
	} else if tp == ceDecompose {
		// Decompose using NFKD and replace tertiary weights.
		t1, t2 := splitDecompose(ce)
		i := len(w)
		nfkd := norm.NFKD.PropertiesString(s).Decomposition()
		for p := 0; len(nfkd) > 0; nfkd = nfkd[p:] {
			w, p = appendNext(w, string(nfkd))
		}
		w[i] = w[i].updateTertiary(t1)
		if i++; i < len(w) {
			w[i] = w[i].updateTertiary(t2)
			for i++; i < len(w); i++ {
				w[i] = w[i].updateTertiary(maxTertiary)
			}
		}
	}
	return w, sz
}

func appendExpansion(w []Elem, ce Elem) []Elem {
	i := splitExpandIndex(ce)
	n := int(expansions[i])
	i++
	for _, ce := range expansions[i : i+n] {
		w = append(w, Elem(ce))
	}
	return w
}

func matchContractionString(w []Elem, ce Elem, suffix string) ([]Elem, int) {
	index, n, offset := splitContractIndex(ce)

	scan := scannerString(index, n, suffix)
	buf := [norm.MaxSegmentSize]byte{}
	bufp := 0
	p := scan.scan(0)

	if !scan.done && p < len(suffix) && suffix[p] >= utf8.RuneSelf {
		// By now we should have filtered most cases.
		p0 := p
		bufn := 0
		rune := norm.NFD.PropertiesString(suffix[p:])
		p += rune.Size()
		if rune.LeadCCC() != 0 {
			prevCC := rune.TrailCCC()
			// A gap may only occur in the last normalization segment.
			// This also ensures that len(scan.s) < norm.MaxSegmentSize.
			if end := norm.NFD.FirstBoundaryInString(suffix[p:]); end != -1 {
				scan.s = suffix[:p+end]
			}
			for p < len(suffix) && !scan.done && suffix[p] >= utf8.RuneSelf {
				rune = norm.NFD.PropertiesString(suffix[p:])
				if ccc := rune.LeadCCC(); ccc == 0 || prevCC >= ccc {
					break
				}
				prevCC = rune.TrailCCC()
				if pp := scan.scan(p); pp != p {
					// Copy the interstitial runes for later processing.
					bufn += copy(buf[bufn:], suffix[p0:p])
					if scan.pindex == pp {
						bufp = bufn
					}
					p, p0 = pp, pp
				} else {
					p += rune.Size()
				}
			}
		}
	}
	// Append weights for the matched contraction, which may be an expansion.
	i, n := scan.result()
	ce = Elem(contractionElems[i+offset])
	if ce.ctype() == ceNormal {
		w = append(w, ce)
	} else {
		w = appendExpansion(w, ce)
	}
	// Append weights for the runes in the segment not part of the contraction.
	for b, p := buf[:bufp], 0; len(b) > 0; b = b[p:] {
		w, p = appendNext(w, string(b))
	}
	return w, n
}
