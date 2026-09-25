package widthx

// pi_width.go ports upstream pi-tui's per-grapheme width and grapheme
// segmentation (packages/tui/src/utils.ts graphemeWidth + Intl.Segmenter)
// onto tables generated from the same data Pi uses at runtime
// (unicode_tables.go, see gen/gen_tables.mjs). Nothing here is hand-tuned:
// parity with Pi is enforced by pi_width_diff_test.go, which runs upstream's
// own utils.ts under Node over a generated corpus.

import (
	"sort"
	"unicode/utf8"
)

const (
	pW2      = 1 << 0  // eastAsianWidth(cp) == 2
	pZW      = 1 << 1  // Default_Ignorable | Control | Mark | Surrogate
	pNP      = 1 << 2  // Default_Ignorable | Control | Format | Mark | Surrogate
	pMark    = 1 << 3  // \p{Mark}
	pTSM     = 1 << 4  // upstream terminalSpacingMarkRegex member
	pRGI1    = 1 << 5  // single code point is \p{RGI_Emoji}
	pExtPict = 1 << 6  // Extended_Pictographic
	pCJK     = 1 << 7  // upstream cjkBreakRegex (Script_Extensions Han/Hiragana/Katakana/Hangul/Bopomofo)
	pJSSpace = 1 << 14 // JavaScript \s (the String.prototype.trim set)
)

// IsCJKBreak reports whether s contains a code point matching upstream's
// cjkBreakRegex.
func IsCJKBreak(s string) bool {
	for _, r := range s {
		if props(r)&pCJK != 0 {
			return true
		}
	}
	return false
}

// IsJSSpace reports whether r matches JavaScript's \s.
func IsJSSpace(r rune) bool { return props(r)&pJSSpace != 0 }

// JSTrimEnd is String.prototype.trimEnd.
func JSTrimEnd(s string) string {
	for s != "" {
		r, n := utf8.DecodeLastRuneInString(s)
		if !IsJSSpace(r) {
			break
		}
		s = s[:len(s)-n]
	}
	return s
}

// JSTrim is String.prototype.trim.
func JSTrim(s string) string {
	s = JSTrimEnd(s)
	for s != "" {
		r, n := utf8.DecodeRuneInString(s)
		if !IsJSSpace(r) {
			break
		}
		s = s[n:]
	}
	return s
}

// Grapheme_Cluster_Break values (bits 8..11).
const (
	gbOther = iota
	gbCR
	gbLF
	gbControl
	gbExtend
	gbZWJ
	gbRI
	gbPrepend
	gbSpacingMark
	gbL
	gbV
	gbT
	gbLV
	gbLVT
)

// Indic_Conjunct_Break values (bits 12..13).
const (
	incbNone = iota
	incbConsonant
	incbExtend
	incbLinker
)

var (
	// bmpProps caches the properties of every Basic Multilingual Plane code
	// point (all property bits fit in 16 bits), so the per-rune lookup on the
	// segmentation and width paths is one load instead of a binary search.
	bmpProps [0x10000]uint16
	rgiSet   map[string]struct{}
)

func init() {
	for i := 0; i+2 < len(propRanges); i += 3 {
		lo, hi, p := propRanges[i], propRanges[i+1], propRanges[i+2]
		if p > 0xFFFF {
			panic("widthx: property bits exceed the 16-bit BMP cache")
		}
		if lo > 0xFFFF {
			break
		}
		for cp := lo; cp <= min(hi, 0xFFFF); cp++ {
			bmpProps[cp] = uint16(p)
		}
	}
	rgiSet = make(map[string]struct{}, len(rgiSequences))
	for _, s := range rgiSequences {
		rgiSet[s] = struct{}{}
	}
}

func lookupProps(r rune) uint32 {
	n := len(propRanges) / 3
	i := sort.Search(n, func(i int) bool { return propRanges[i*3+1] >= uint32(r) })
	if i >= n || propRanges[i*3] > uint32(r) {
		return 0
	}
	return propRanges[i*3+2]
}

func props(r rune) uint32 {
	if r >= 0 && r < 0x10000 {
		return uint32(bmpProps[r])
	}
	return lookupProps(r)
}

func gcbOf(p uint32) int  { return int(p>>8) & 0xF }
func incbOf(p uint32) int { return int(p>>12) & 0x3 }

// EastAsianWidth mirrors get-east-asian-width's eastAsianWidth(cp): 2 for
// Fullwidth/Wide, else 1.
func EastAsianWidth(r rune) int {
	if props(r)&pW2 != 0 {
		return 2
	}
	return 1
}

// FirstGrapheme returns the first extended grapheme cluster of s and the
// remainder, following UAX #29 as implemented by ICU's Intl.Segmenter
// (including GB9c Indic conjuncts), with the Unicode version of the tables.
func FirstGrapheme(s string) (cluster, rest string) {
	if s == "" {
		return "", ""
	}
	r, n := utf8.DecodeRuneInString(s)
	p := props(r)
	prev := gcbOf(p)
	// State for GB9c (InCB) and GB11 (emoji ZWJ) and GB12/13 (RI parity).
	incbState := 0 // 0 none, 1 seen Consonant (+Extend/Linker), 2 seen Linker after Consonant
	if incbOf(p) == incbConsonant {
		incbState = 1
	}
	emojiState := 0 // 0 none, 1 ExtPict Extend*, 2 ExtPict Extend* ZWJ
	if p&pExtPict != 0 {
		emojiState = 1
	}
	riCount := 0
	if prev == gbRI {
		riCount = 1
	}
	i := n
	for i < len(s) {
		r, n = utf8.DecodeRuneInString(s[i:])
		q := props(r)
		cur := gcbOf(q)
		brk := true
		switch {
		case prev == gbCR && cur == gbLF: // GB3
			brk = false
		case prev == gbControl || prev == gbCR || prev == gbLF: // GB4
			brk = true
		case cur == gbControl || cur == gbCR || cur == gbLF: // GB5
			brk = true
		case prev == gbL && (cur == gbL || cur == gbV || cur == gbLV || cur == gbLVT): // GB6
			brk = false
		case (prev == gbLV || prev == gbV) && (cur == gbV || cur == gbT): // GB7
			brk = false
		case (prev == gbLVT || prev == gbT) && cur == gbT: // GB8
			brk = false
		case cur == gbExtend || cur == gbZWJ: // GB9
			brk = false
		case cur == gbSpacingMark: // GB9a
			brk = false
		case prev == gbPrepend: // GB9b
			brk = false
		case incbState == 2 && incbOf(q) == incbConsonant: // GB9c
			brk = false
		case emojiState == 2 && q&pExtPict != 0: // GB11
			brk = false
		case prev == gbRI && cur == gbRI && riCount%2 == 1: // GB12/13
			brk = false
		}
		if brk {
			break
		}
		// Advance state.
		switch incbOf(q) {
		case incbConsonant:
			incbState = 1
		case incbLinker:
			if incbState >= 1 {
				incbState = 2
			}
		case incbExtend:
			// keeps state
		default:
			incbState = 0
		}
		switch {
		case q&pExtPict != 0:
			emojiState = 1
		case cur == gbExtend && emojiState == 1:
		case cur == gbZWJ && emojiState == 1:
			emojiState = 2
		default:
			emojiState = 0
		}
		if cur == gbRI {
			riCount++
		} else {
			riCount = 0
		}
		prev = cur
		i += n
	}
	return s[:i], s[i:]
}

// GraphemeWidth is upstream graphemeWidth(segment) for one cluster produced
// by FirstGrapheme.
func GraphemeWidth(seg string) int {
	if seg == "" {
		return 0
	}
	if seg == "\t" {
		return 3
	}
	if len(seg) == 1 && seg[0] >= 0x20 && seg[0] < 0x7F {
		return 1
	}
	// terminalSpacingMarkRegex: every code point is a terminal spacing mark.
	allTSM, allZW, count := true, true, 0
	for _, r := range seg {
		p := props(r)
		if p&pTSM == 0 {
			allTSM = false
		}
		if p&pZW == 0 {
			allZW = false
		}
		count++
	}
	if allTSM {
		return count
	}
	if allZW {
		return 0
	}
	if couldBeEmoji(seg) && isRGIEmoji(seg) {
		return 2
	}
	// Strip leading non-printing code points.
	base := seg
	for base != "" {
		r, n := utf8.DecodeRuneInString(base)
		if props(r)&pNP == 0 {
			break
		}
		base = base[n:]
	}
	if base == "" {
		return 0
	}
	cp, n := utf8.DecodeRuneInString(base)
	if cp >= 0x1f1e6 && cp <= 0x1f1ff {
		return 2
	}
	width := EastAsianWidth(cp)
	followsMark := false
	for _, c := range base[n:] {
		p := props(c)
		switch {
		case p&pTSM != 0:
			width++
			followsMark = false
		case p&pMark != 0:
			followsMark = true
		case p&pNP == 0:
			if followsMark || (c >= 0xff00 && c <= 0xffef) {
				width += EastAsianWidth(c)
			} else if c == 0x0e33 || c == 0x0eb3 {
				width++
			}
			followsMark = false
		}
	}
	return width
}

// couldBeEmoji mirrors upstream's prefilter, including its UTF-16 length test.
func couldBeEmoji(seg string) bool {
	cp, _ := utf8.DecodeRuneInString(seg)
	if (cp >= 0x1f000 && cp <= 0x1fbff) || (cp >= 0x2300 && cp <= 0x23ff) ||
		(cp >= 0x2600 && cp <= 0x27bf) || (cp >= 0x2b50 && cp <= 0x2b55) {
		return true
	}
	units := 0
	for _, r := range seg {
		if r == 0xFE0F {
			return true
		}
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return units > 2
}

func isRGIEmoji(seg string) bool {
	r, n := utf8.DecodeRuneInString(seg)
	if n == len(seg) {
		return props(r)&pRGI1 != 0
	}
	_, ok := rgiSet[seg]
	return ok
}
