// Ports upstream pi-tui column slicing and overlay-segment extraction.
//
// Mirrors:
//   - sliceByColumn       utils.ts:954
//   - sliceWithWidth      utils.ts:959
//   - extractSegments     utils.ts:1014

package widthx

import (
	"strings"
)

// SliceResult is the return shape of SliceWithWidth.
type SliceResult struct {
	Text  string
	Width int
}

// SliceByColumn extracts a range of visible columns [startCol, startCol+length)
// from a line. ANSI codes are preserved. If `strict` is true, a wide character
// that would extend past the end column is excluded.
//
// Mirrors upstream `sliceByColumn`.
func SliceByColumn(line string, startCol, length int, strict bool) string {
	return SliceWithWidth(line, startCol, length, strict).Text
}

// SliceWithWidth is like SliceByColumn but also returns the actual visible
// width of the result. Mirrors upstream `sliceWithWidth`.
func SliceWithWidth(line string, startCol, length int, strict bool) SliceResult {
	if length <= 0 {
		return SliceResult{}
	}
	endCol := startCol + length
	var result strings.Builder
	resultWidth := 0
	currentCol := 0
	pendingAnsi := ""

	for i := 0; i < len(line); {
		if n := ExtractAnsi(line, i); n > 0 {
			code := line[i : i+n]
			if currentCol >= startCol && currentCol < endCol {
				result.WriteString(code)
			} else if currentCol < startCol {
				pendingAnsi += code
			}
			i += n
			continue
		}
		// Non-ANSI run until next ANSI or end.
		end := i
		for end < len(line) {
			if ExtractAnsi(line, end) > 0 {
				break
			}
			end++
		}
		run := line[i:end]
		gs := newGraphemeIter(run)
		broken := false
		for gs.Next() {
			seg := gs.Str()
			w := graphemeWidthFast(seg)
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strict || currentCol+w <= endCol
			if inRange && fits {
				if pendingAnsi != "" {
					result.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				result.WriteString(seg)
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				broken = true
				break
			}
		}
		i = end
		if broken {
			break
		}
	}
	return SliceResult{Text: result.String(), Width: resultWidth}
}

// SegmentsResult is the return shape of ExtractSegments.
type SegmentsResult struct {
	Before      string
	BeforeWidth int
	After       string
	AfterWidth  int
}

// ExtractSegments extracts the "before" and "after" segments around an
// overlay region in a single pass over `line`. SGR/hyperlink styling that
// was active just before the overlay's start column is prepended to the
// "after" segment so the overlay does not visually break style continuity.
//
// `beforeEnd`   = first column NOT in "before" segment
// `afterStart`  = first column of "after" segment
// `afterLen`    = number of columns in "after" segment (0 = before-only)
// `strictAfter` = exclude wide char at the end that would overrun afterEnd
//
// Mirrors upstream `extractSegments`. The shared pooled tracker in upstream
// is replaced here by a fresh local tracker per call: the function is
// already at the inner loop of overlay compositing and a stack-allocated
// tracker is essentially free.
func ExtractSegments(line string, beforeEnd, afterStart, afterLen int, strictAfter bool) SegmentsResult {
	var before, after strings.Builder
	beforeWidth, afterWidth := 0, 0
	pendingAnsiBefore := ""
	afterStarted := false
	afterEnd := afterStart + afterLen
	tr := &AnsiCodeTracker{}

	currentCol := 0

	for i := 0; i < len(line); {
		if n := ExtractAnsi(line, i); n > 0 {
			code := line[i : i+n]
			tr.Process(code)
			switch {
			case currentCol < beforeEnd:
				pendingAnsiBefore += code
			case currentCol >= afterStart && currentCol < afterEnd && afterStarted:
				after.WriteString(code)
			}
			i += n
			continue
		}

		end := i
		for end < len(line) {
			if ExtractAnsi(line, end) > 0 {
				break
			}
			end++
		}
		run := line[i:end]
		gs := newGraphemeIter(run)
		shouldBreak := false
		for gs.Next() {
			seg := gs.Str()
			w := graphemeWidthFast(seg)
			switch {
			case currentCol < beforeEnd && currentCol+w <= beforeEnd:
				// A wide grapheme straddling beforeEnd stays out of "before",
				// as upstream: including it made the composited row overrun
				// the overlay's start column.
				if pendingAnsiBefore != "" {
					before.WriteString(pendingAnsiBefore)
					pendingAnsiBefore = ""
				}
				before.WriteString(seg)
				beforeWidth += w
			case currentCol >= afterStart && currentCol < afterEnd:
				fits := !strictAfter || currentCol+w <= afterEnd
				if fits {
					if !afterStarted {
						after.WriteString(tr.ActiveCodes())
						afterStarted = true
					}
					after.WriteString(seg)
					afterWidth += w
				}
			}
			currentCol += w
			if afterLen <= 0 {
				if currentCol >= beforeEnd {
					shouldBreak = true
					break
				}
			} else if currentCol >= afterEnd {
				shouldBreak = true
				break
			}
		}
		i = end
		if shouldBreak {
			break
		}
	}

	return SegmentsResult{
		Before:      before.String(),
		BeforeWidth: beforeWidth,
		After:       after.String(),
		AfterWidth:  afterWidth,
	}
}

// graphemeWidthFast returns upstream graphemeWidth(seg) for one cluster.
func graphemeWidthFast(seg string) int { return GraphemeWidth(seg) }
