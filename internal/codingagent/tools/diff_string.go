package tools

// Byte-exact port of upstream generateDiffString (edit-diff.ts): a
// display-oriented, line-numbered diff with elided context, plus the
// first changed line number in the new file. Shares the jsdiff line-diff
// core (myersLineDiff) with GenerateUnifiedPatch.

import (
	"strconv"
	"strings"
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
	parts := myersLineDiff(oldContent, newContent)

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLineNum := max(len(newLines), len(oldLines))
	lineNumWidth := len(strconv.Itoa(maxLineNum))
	padNum := func(n int) string { return leftPad(strconv.Itoa(n), lineNumWidth) }
	ellipsis := " " + leftPad("", lineNumWidth) + " ..."

	var output []string
	oldLineNum, newLineNum := 1, 1
	lastWasChange := false
	firstChangedLine := 0

	for i := range parts {
		part := parts[i]
		raw := strings.Split(part.value, "\n")
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}

		if part.added || part.removed {
			if firstChangedLine == 0 {
				firstChangedLine = newLineNum
			}
			for _, line := range raw {
				if part.added {
					output = append(output, "+"+padNum(newLineNum)+" "+line)
					newLineNum++
				} else {
					output = append(output, "-"+padNum(oldLineNum)+" "+line)
					oldLineNum++
				}
			}
			lastWasChange = true
			continue
		}

		// Context part: show a few lines adjacent to changes, elide the rest.
		nextPartIsChange := i < len(parts)-1 && (parts[i+1].added || parts[i+1].removed)
		hasLeadingChange := lastWasChange
		hasTrailingChange := nextPartIsChange

		emit := func(line string) {
			output = append(output, " "+padNum(oldLineNum)+" "+line)
			oldLineNum++
			newLineNum++
		}

		switch {
		case hasLeadingChange && hasTrailingChange:
			if len(raw) <= contextLines*2 {
				for _, line := range raw {
					emit(line)
				}
			} else {
				leading := raw[:contextLines]
				trailing := raw[len(raw)-contextLines:]
				skipped := len(raw) - len(leading) - len(trailing)
				for _, line := range leading {
					emit(line)
				}
				output = append(output, ellipsis)
				oldLineNum += skipped
				newLineNum += skipped
				for _, line := range trailing {
					emit(line)
				}
			}
		case hasLeadingChange:
			shown := raw
			if len(raw) > contextLines {
				shown = raw[:contextLines]
			}
			skipped := len(raw) - len(shown)
			for _, line := range shown {
				emit(line)
			}
			if skipped > 0 {
				output = append(output, ellipsis)
				oldLineNum += skipped
				newLineNum += skipped
			}
		case hasTrailingChange:
			skipped := max(len(raw)-contextLines, 0)
			if skipped > 0 {
				output = append(output, ellipsis)
				oldLineNum += skipped
				newLineNum += skipped
			}
			for _, line := range raw[skipped:] {
				emit(line)
			}
		default:
			oldLineNum += len(raw)
			newLineNum += len(raw)
		}
		lastWasChange = false
	}

	return strings.Join(output, "\n"), firstChangedLine
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
