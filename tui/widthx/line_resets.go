// Line-level rendering helpers ported from upstream pi-tui.
// Mirrors:
//   - SEGMENT_RESET constant      : tui.ts:425
//   - applyLineResets             : tui.ts:427 (TUI.applyLineResets)
//   - isImageLine                 : terminal-image.ts:106

package widthx

import (
	"math/bits"
	"strings"
)

// SegmentReset is the hard ANSI/OSC barrier appended to non-image lines after
// rendering. It resets all SGR attributes and closes any open OSC 8 hyperlink.
// Mirrors upstream TUI.SEGMENT_RESET (`"\x1b[0m\x1b]8;;\x07"`).
const SegmentReset = "\x1b[0m\x1b]8;;\x07"

const (
	kittyImagePrefix  = "\x1b_G"
	iTerm2ImagePrefix = "\x1b]1337;File="
)

// IsImageLine reports whether a rendered line contains a Kitty or iTerm2
// image graphics sequence. Image lines MUST NOT receive the
// `SegmentReset` suffix because the trailing reset can corrupt the
// image protocol payload.
//
// Mirrors upstream `isImageLine` (terminal-image.ts:106). Checks both the
// fast prefix path (single-row images) and a contains-scan (multi-row
// images that begin with a cursor-up movement before the graphics
// sequence).
//
// Both prefixes start with ESC, so one pass over the line's ESC bytes answers
// both contains-scans; styled rows otherwise pay two full searches each time
// the frame asks (clamp, reset, scrollbar and image-redraw checks).
func IsImageLine(line string) bool {
	for i := 0; ; {
		j := strings.IndexByte(line[i:], 0x1B)
		if j < 0 {
			return false
		}
		rest := line[i+j:]
		if strings.HasPrefix(rest, kittyImagePrefix) || strings.HasPrefix(rest, iTerm2ImagePrefix) {
			return true
		}
		i += j + 1
	}
}

// ApplyLineReset applies terminal normalization and appends the
// `SegmentReset` barrier to a single non-image line. Image lines are
// returned unchanged. This is the per-row body of upstream
// `TUI.applyLineResets` (tui.ts:427), factored out so callers can apply
// it selectively (e.g. an index-aligned reuse cache) without duplicating
// the rule.
//
// Every cursor marker is stripped from the line before it is normalized:
// markers are internal render metadata and must never reach terminal output
// (Pi main 25490ea49). The result is
// NormalizeTerminalOutput(strings.ReplaceAll(line, CursorMarker, "")) + SegmentReset
// for a line that is not an image line, in one allocation unless the line
// needs the rewrites of NormalizeTerminalOutput.
func ApplyLineReset(line string) string {
	marker, image := scanImageAndMarker(line)
	if image {
		return line
	}
	if marker >= 0 {
		return stripMarkersAndReset(line, marker)
	}
	thai := containsThaiLaoAM(line)
	if !thai && strings.IndexByte(line, '\t') < 0 {
		return line + SegmentReset
	}
	return rewriteTerminalOutput(line, thai, SegmentReset)
}

// scanImageAndMarker is IsImageLine and the position of the first CursorMarker (-1 for none) in one pass over the line's ESC bytes: the
// image prefixes and the marker all start with ESC, so the pass that answers the image question finds the marker without a search of its
// own. The image answer needs the whole line, so the scan goes on after a marker.
func scanImageAndMarker(line string) (marker int, image bool) {
	marker = -1
	for i := 0; ; {
		var j int
		if i == 0 {
			j = strings.IndexByte(line, 0x1B)
		} else {
			j = indexESCNear(line[i:])
		}
		if j < 0 {
			return marker, false
		}
		at := i + j
		rest := line[at:]
		if len(rest) > 1 {
			switch rest[1] {
			case '_':
				if strings.HasPrefix(rest, kittyImagePrefix) {
					return marker, true
				}
				if marker < 0 && strings.HasPrefix(rest, CursorMarker) {
					marker = at
				}
			case ']':
				if strings.HasPrefix(rest, iTerm2ImagePrefix) {
					return marker, true
				}
			}
		}
		i = at + 1
	}
}

// indexESCNear is strings.IndexByte(s, 0x1B) for a search that probably ends soon: a styled row holds an ESC every few bytes, where a call to
// the vectorized search per ESC costs more than looking at two words. A longer gap goes to the vectorized search.
func indexESCNear(s string) int {
	const lo, hi = 0x0101010101010101, 0x8080808080808080
	i := 0
	for ; i < 16 && len(s)-i >= 8; i += 8 {
		w := load64(s[i:]) ^ (lo * 0x1B)
		// The lowest set bit of this zero-byte test is exact: only the bytes above a zero byte can be false positives.
		if t := (w - lo) &^ w & hi; t != 0 {
			return i + bits.TrailingZeros64(t)>>3
		}
	}
	if j := strings.IndexByte(s[i:], 0x1B); j >= 0 {
		return i + j
	}
	return -1
}

// load64 reads the first eight bytes of s, least significant first; len(s) must be at least eight.
func load64(s string) uint64 {
	_ = s[7]
	return uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 |
		uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
}

// stripMarkersAndReset is NormalizeTerminalOutput(strings.ReplaceAll(line, CursorMarker, "")) + SegmentReset for a line whose first
// CursorMarker is at first. The stripped line and the reset share one buffer, so a line that needs no normalization is one allocation
// (the rewrites run on the stripped line, not the original: removing a marker can join the bytes of a UTF-8 sequence around it).
func stripMarkersAndReset(line string, first int) string {
	var b strings.Builder
	b.Grow(len(line) + len(SegmentReset))
	b.WriteString(line[:first])
	for rest := line[first+len(CursorMarker):]; ; {
		at := strings.Index(rest, CursorMarker)
		if at < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:at])
		rest = rest[at+len(CursorMarker):]
	}
	stripped := b.String()
	if thai := containsThaiLaoAM(stripped); thai || strings.IndexByte(stripped, '\t') >= 0 {
		return rewriteTerminalOutput(stripped, thai, SegmentReset)
	}
	b.WriteString(SegmentReset)
	return b.String()
}

// ApplyLineResets returns a new slice where every non-image line has the
// `SegmentReset` barrier appended. Image lines are returned unchanged.
//
// Mirrors upstream `TUI.applyLineResets` (tui.ts:427). The reset suffix is
// required to prevent SGR / OSC 8 hyperlink state from bleeding between
// adjacent rendered lines.
//
// Returns a new slice; the input slice is not mutated.
func ApplyLineResets(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = ApplyLineReset(line)
	}
	return out
}
