package tools

// Windowed edit diff, ported from packages/coding-agent/src/core/tools/edit-diff.ts (generateEditDiff, getDiffWindows).

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsdiff"
)

// diffWindowMarginLines is the unchanged lines diffed on each side of a replacement. Well above the 4 context lines shown, so the line
// diff aligns a change within its window as it would over the whole file.
const diffWindowMarginLines = 32

// displaySkipMarker is the display diff line that stands for skipped unchanged lines.
func displaySkipMarker(lineNumWidth int) string {
	return " " + leftPad("", lineNumWidth) + " ..."
}

// editDiff is the display diff and unified patch of an edit, as returned in the edit tool's details (upstream EditDiff).
type editDiff struct {
	diff             string
	patch            string
	firstChangedLine int
}

// generateEditDiff diffs an edit only around its replacements, so time and memory follow the size of the change instead of the file.
// The result matches generateDiffString and generateUnifiedPatch over the whole contents, except that the line diff may align runs of
// repeated lines differently within a window. replacements are sorted and disjoint replacements of baseContent that give newContent.
func generateEditDiff(path, baseContent, newContent string, replacements []matchedEdit, contextLines int) editDiff {
	lineNumWidth := len(itoa(max(countLines(baseContent), countLines(newContent))))
	skipMarker := displaySkipMarker(lineNumWidth)
	var output strings.Builder
	var hunks []patchHunk
	firstChangedLine := 0
	for _, window := range getDiffWindows(baseContent, replacements) {
		oldText := baseContent[window.oldStart:window.oldEnd]
		newText := newContent[window.newStart:window.newEnd]
		d := jsdiff.DiffLineRuns(oldText, newText)
		// Both windows around a run of unchanged lines mark it as skipped; the whole-file diff shows one marker.
		shown := output.String()
		dropLeading := strings.HasSuffix(shown, skipMarker) && (len(shown) == len(skipMarker) || shown[len(shown)-len(skipMarker)-1] == '\n')
		first := writeDisplayDiff(&output, d, window.oldLine+1, window.newLine+1, lineNumWidth, contextLines, dropLeading)
		if firstChangedLine == 0 {
			firstChangedLine = first
		}
		for _, hunk := range diffLinesResultToPatch(d, contextLines) {
			hunk.oldStart += window.oldLine
			hunk.newStart += window.newLine
			hunks = append(hunks, hunk)
		}
	}
	return editDiff{diff: output.String(), patch: formatPatch(path, hunks), firstChangedLine: firstChangedLine}
}

// countLines is the line count text.split("\n").length gives.
func countLines(text string) int {
	return strings.Count(text, "\n") + 1
}

// diffWindow is the matching ranges of the old and new contents around one or more replacements: offsets and 0-based first lines.
type diffWindow struct {
	oldStart, oldEnd, oldLine int
	newStart, newEnd, newLine int
}

// lineStartBefore is the start of the line that contains offset.
func lineStartBefore(text string, offset int) int {
	return strings.LastIndexByte(text[:offset], '\n') + 1
}

// getDiffWindows is the whole-line windows with margin around the replacements, which are sorted and disjoint; overlapping windows merge.
func getDiffWindows(baseContent string, replacements []matchedEdit) []diffWindow {
	var windows []diffWindow
	// Offset and line deltas of the replacements before the current one, and a line-counting cursor.
	offsetDelta, lineDelta := 0, 0
	cursorOffset, cursorLine := 0, 0
	for _, replacement := range replacements {
		matchEnd := replacement.matchIndex + replacement.matchLength
		start := lineStartBefore(baseContent, replacement.matchIndex)
		for i := 0; i < diffWindowMarginLines && start > 0; i++ {
			start = lineStartBefore(baseContent, start-1)
		}
		end := matchEnd
		for i := 0; i <= diffWindowMarginLines && end < len(baseContent); i++ {
			from := end
			if i == 0 {
				from = max(matchEnd-1, replacement.matchIndex)
			}
			if lineFeed := strings.IndexByte(baseContent[from:], '\n'); lineFeed == -1 {
				end = len(baseContent)
			} else {
				end = from + lineFeed + 1
			}
		}
		replacementOffsetDelta := len(replacement.newText) - replacement.matchLength
		replacementLineDelta := strings.Count(replacement.newText, "\n") - strings.Count(baseContent[replacement.matchIndex:matchEnd], "\n")
		if n := len(windows); n > 0 && start <= windows[n-1].oldEnd {
			previous := &windows[n-1]
			previous.oldEnd = max(previous.oldEnd, end)
			previous.newEnd = previous.oldEnd + offsetDelta + replacementOffsetDelta
		} else {
			cursorLine += strings.Count(baseContent[cursorOffset:start], "\n")
			cursorOffset = start
			windows = append(windows, diffWindow{
				oldStart: start,
				oldEnd:   end,
				oldLine:  cursorLine,
				newStart: start + offsetDelta,
				newEnd:   end + offsetDelta + replacementOffsetDelta,
				newLine:  cursorLine + lineDelta,
			})
		}
		offsetDelta += replacementOffsetDelta
		lineDelta += replacementLineDelta
	}
	return windows
}
