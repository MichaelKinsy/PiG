// Package widthx implements upstream pi-tui's visibleWidth + ANSI escape
// extraction with byte-level fidelity to the TypeScript algorithm.
//
// Mirrors: .upstream/current/packages/tui/src/utils.ts
//   - extractAnsiCode (line 261)
//   - visibleWidth     (line 199)
//
// This package exists so the parity sweep can swap call sites one at a time
// without breaking the existing tui/tui.go stripANSI / runewidth
// surfaces. Once τ.2 lands, tui will delegate to this package.
package widthx

import (
	"strings"
	"unicode/utf8"
)

// ExtractAnsi returns the byte length of the ANSI escape starting at byte
// offset i in s, or 0 if none begins there. It is upstream extractAnsiCode
// exactly (via ExtractAnsiCode): a CSI runs to the first m|G|K|H|J, whatever
// comes between, and OSC/APC run to BEL or ST. Width must match Pi byte for
// byte because PiG adopts Pi's crash-on-overflow render check; the former
// D47 h/l extension and bail-on-unknown-final-byte rule made PiG measure some
// strings wider than Pi does.
func ExtractAnsi(s string, i int) int {
	_, n := ExtractAnsiCode(s, i)
	return n
}

// StripAnsi removes recognized ANSI/OSC/APC sequences from s. Other escape
// sequences (e.g. cursor movement CSI A/B/C/D) are left intact: matches
// upstream's behavior.
func StripAnsi(s string) string {
	if !strings.ContainsRune(s, 0x1B) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if n := ExtractAnsi(s, i); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// VisibleWidth returns the display column width of s, mirroring upstream's
// `visibleWidth`:
//
//   - Tabs expand to 3 spaces (NOT 8): matches upstream
//   - CSI/OSC/APC sequences stripped before measuring
//   - Width per grapheme via GraphemeWidth (upstream graphemeWidth)
func VisibleWidth(s string) int {
	if s == "" {
		return 0
	}
	// Fast path: ASCII printable (upstream isPrintableAscii).
	if isPrintableASCII(s) {
		return len(s)
	}
	if w, ok := asciiVisibleWidth(s); ok {
		return w
	}
	clean := s
	if strings.ContainsRune(clean, '\t') {
		clean = strings.ReplaceAll(clean, "\t", "   ")
	}
	if strings.ContainsRune(clean, 0x1B) {
		clean = StripAnsi(clean)
	}
	width := 0
	g := newGraphemeIter(clean)
	for g.Next() {
		width += g.Width()
	}
	return width
}

// asciiVisibleWidth is upstream visibleWidth specialized to text whose
// visible bytes (outside extractAnsiCode escapes) are all ASCII, computed in
// one pass without the tab replacement, stripping or segmentation copies.
// It is exact, not an approximation: after stripping, every ASCII byte is
// its own grapheme except CR LF (both zero width), a printable byte has
// graphemeWidth 1, a tab expands to three spaces, and every other ASCII
// control byte is \p{Control} with width 0. Replacing tabs before stripping
// cannot move an escape's terminator (m|G|K|H|J, BEL or ESC \), so the
// escapes found here are the ones upstream strips. ok is false as soon as a
// non-ASCII visible byte appears; pi_width_diff_test.go holds the result to
// upstream.
func asciiVisibleWidth(s string) (width int, ok bool) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= 0x20 && c <= 0x7E:
			width++
		case c == '\t':
			width += 3
		case c == 0x1B:
			if n := ExtractAnsi(s, i); n > 0 {
				i += n
				continue
			}
		case c >= utf8.RuneSelf:
			return 0, false
		}
		i++
	}
	return width, true
}

// NormalizeTerminalOutput rewrites Thai/Lao AM vowels to compatibility
// decompositions that preserve logical content while avoiding stale-cell
// artifacts in some terminal differential repaints. Visible tabs are expanded
// to the fixed layout width (3 spaces, matching VisibleWidth) so terminal tab
// stops cannot wrap a logical line; tabs inside ANSI sequences stay untouched.
func NormalizeTerminalOutput(s string) string {
	if containsThaiLaoAM(s) {
		s = strings.ReplaceAll(s, "\u0e33", "\u0e4d\u0e32")
		s = strings.ReplaceAll(s, "\u0eb3", "\u0ecd\u0eb2")
	}
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		if n := ExtractAnsi(s, i); n > 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		if s[i] == '\t' {
			b.WriteString("   ")
		} else {
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}

// needsGraphemeWidth reports whether runewidth.StringWidth would misreport a
// string, requiring the slower grapheme walk.
//
// runewidth measures code points, so it misses two cases that terminals and
// upstream both give two cells. An emoji presentation sequence (base + VS16)
// such as U+26A0 U+FE0F counts as 1 because the base is neutral-width and VS16
// is zero-width, and a regional-indicator flag pair counts as 1. Upstream
// returns 2 for both, via \p{RGI_Emoji} and an explicit regional-indicator
// branch (packages/tui/src/utils.ts:190,204).
//
// Undercounting is not cosmetic. A row measured as fitting the terminal but
// physically one cell wider wraps onto a second screen row, which desynchronizes
// every screen position below it, and the differential renderer's per-row
// erase reaches only the first physical row, so the wrapped remainder stays on
// screen until a full repaint. A single warning emoji in a session tree froze
// the rows above the selection through every scroll.
//
// Every code point it looks for is non-ASCII, so the ASCII prefix is skipped
// bytewise and the rest is decoded once. strings.ContainsAny with a non-ASCII
// set would instead search the set once per rune of s.
func needsGraphemeWidth(s string) bool {
	rest, ok := fromFirstNonASCII(s)
	if !ok {
		return false
	}
	for _, r := range rest {
		if r == '\u0e33' || r == '\u0eb3' || r == '\uFE0F' || (r >= 0x1F1E6 && r <= 0x1F1FF) {
			return true
		}
	}
	return false
}

// containsThaiLaoAM reports whether s contains U+0E33 or U+0EB3, with the same
// ASCII skip as needsGraphemeWidth.
func containsThaiLaoAM(s string) bool {
	rest, ok := fromFirstNonASCII(s)
	return ok && (strings.Contains(rest, "\u0e33") || strings.Contains(rest, "\u0eb3"))
}

// fromFirstNonASCII returns s from its first non-ASCII byte, or ok=false when s
// is all ASCII.
func fromFirstNonASCII(s string) (string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return s[i:], true
		}
	}
	return "", false
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7E {
			return false
		}
	}
	return true
}
