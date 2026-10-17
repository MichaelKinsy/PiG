package tools

// Byte-exact port of upstream generateDiffString (edit-diff.ts): a
// display-oriented, line-numbered diff with elided context, plus the
// first changed line number in the new file. Reads the lines of a
// jsdiff.LineDiff, which GenerateUnifiedPatch also reads.

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsdiff"
)

// DiffStringResult is upstream's `{ diff: string; firstChangedLine: number | undefined }`.
// FirstChangedLine is nil when there was no change (upstream `undefined`).
type DiffStringResult struct {
	Diff             string
	FirstChangedLine *int
}

// GenerateDiffString returns the line-numbered display diff and the first
// changed line number in the new file, byte-identical to upstream
// generateDiffString. contextLines is upstream's optional third parameter
// (default 4).
func GenerateDiffString(oldContent, newContent string, contextLines ...int) DiffStringResult {
	diff, first := generateDiffString(oldContent, newContent, optionalContextLines(contextLines))
	result := DiffStringResult{Diff: diff}
	if first != 0 {
		result.FirstChangedLine = &first
	}
	return result
}

// optionalContextLines is upstream's `contextLines = 4` default parameter.
func optionalContextLines(contextLines []int) int {
	if len(contextLines) > 0 {
		return contextLines[0]
	}
	return 4
}

func generateDiffString(oldContent, newContent string, contextLines int) (string, int) {
	return diffStringOf(jsdiff.DiffLineRuns(oldContent, newContent), oldContent, newContent, contextLines)
}

// diffStringOf is generateDiffString over the line diff d of oldContent and newContent. Upstream splits every part's value into lines;
// this reads only the lines it shows, so unchanged regions cost their line count.
func diffStringOf(d *jsdiff.LineDiff, oldContent, newContent string, contextLines int) (string, int) {
	parts := d.Runs
	// text.split("\n").length of each side.
	maxLineNum := max(strings.Count(newContent, "\n"), strings.Count(oldContent, "\n")) + 1
	lineNumWidth := len(strconv.Itoa(maxLineNum))
	ellipsis := " " + leftPad("", lineNumWidth) + " ..."

	var output strings.Builder
	numBuf := make([]byte, 0, 20)
	// writeLine appends one output line: marker, padded number, a space and the line without its "\n".
	writeLine := func(marker byte, n int, line string) {
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteByte(marker)
		numBuf = strconv.AppendInt(numBuf[:0], int64(n), 10)
		for range lineNumWidth - len(numBuf) {
			output.WriteByte(' ')
		}
		output.Write(numBuf)
		output.WriteByte(' ')
		output.WriteString(strings.TrimSuffix(line, "\n"))
	}
	writeEllipsis := func() {
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(ellipsis)
	}
	oldLineNum, newLineNum := 1, 1
	lastWasChange := false
	firstChangedLine := 0

	for i, part := range parts {
		if part.Added || part.Removed {
			if firstChangedLine == 0 {
				firstChangedLine = newLineNum
			}
			for k := range part.Count {
				if part.Added {
					writeLine('+', newLineNum, d.Line(part, k))
					newLineNum++
				} else {
					writeLine('-', oldLineNum, d.Line(part, k))
					oldLineNum++
				}
			}
			lastWasChange = true
			continue
		}

		// Context part: show a few lines adjacent to changes, elide the rest.
		nextPartIsChange := i < len(parts)-1 && (parts[i+1].Added || parts[i+1].Removed)
		hasLeadingChange := lastWasChange
		hasTrailingChange := nextPartIsChange

		emit := func(from, to int) {
			for k := from; k < to; k++ {
				writeLine(' ', oldLineNum, d.Line(part, k))
				oldLineNum++
				newLineNum++
			}
		}
		skip := func(skipped int) {
			writeEllipsis()
			oldLineNum += skipped
			newLineNum += skipped
		}

		count := part.Count
		switch {
		case hasLeadingChange && hasTrailingChange:
			if count <= contextLines*2 {
				emit(0, count)
			} else {
				emit(0, contextLines)
				skip(count - 2*contextLines)
				emit(count-contextLines, count)
			}
		case hasLeadingChange:
			shown := min(count, contextLines)
			emit(0, shown)
			if count > shown {
				skip(count - shown)
			}
		case hasTrailingChange:
			skipped := max(count-contextLines, 0)
			if skipped > 0 {
				skip(skipped)
			}
			emit(skipped, count)
		default:
			oldLineNum += count
			newLineNum += count
		}
		lastWasChange = false
	}

	return output.String(), firstChangedLine
}

func leftPad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// diffAndFirstLine unpacks GenerateDiffString for details structs that carry the first changed line as a number, 0 meaning none.
func diffAndFirstLine(oldContent, newContent string) (string, int) {
	result := GenerateDiffString(oldContent, newContent)
	if result.FirstChangedLine == nil {
		return result.Diff, 0
	}
	return result.Diff, *result.FirstChangedLine
}
