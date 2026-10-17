package tools

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsdiff"
)

// The display diff and the unified patch as they were built before they read the lines of a jsdiff.LineDiff on demand: from the change
// values of jsdiff.DiffLines, split per part. TestDiffViewsMatchSplitPartReference compares the two on randomized edits.

type referencePart struct {
	value          string
	added, removed bool
}

func referenceParts(oldStr, newStr string) []referencePart {
	changes := jsdiff.DiffLines(oldStr, newStr)
	parts := make([]referencePart, len(changes))
	for i, c := range changes {
		parts[i] = referencePart{value: c.Value, added: c.Added, removed: c.Removed}
	}
	return parts
}

func referenceDiffString(oldContent, newContent string, contextLines int) (string, int) {
	parts := referenceParts(oldContent, newContent)
	maxLineNum := max(len(strings.Split(newContent, "\n")), len(strings.Split(oldContent, "\n")))
	lineNumWidth := len(strconv.Itoa(maxLineNum))
	padNum := func(n int) string { return leftPad(strconv.Itoa(n), lineNumWidth) }
	ellipsis := " " + leftPad("", lineNumWidth) + " ..."
	var output []string
	oldLineNum, newLineNum := 1, 1
	lastWasChange := false
	firstChangedLine := 0
	for i, part := range parts {
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
		hasLeadingChange := lastWasChange
		hasTrailingChange := i < len(parts)-1 && (parts[i+1].added || parts[i+1].removed)
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
				for _, line := range raw[:contextLines] {
					emit(line)
				}
				output = append(output, ellipsis)
				skipped := len(raw) - 2*contextLines
				oldLineNum += skipped
				newLineNum += skipped
				for _, line := range raw[len(raw)-contextLines:] {
					emit(line)
				}
			}
		case hasLeadingChange:
			shown := raw
			if len(raw) > contextLines {
				shown = raw[:contextLines]
			}
			for _, line := range shown {
				emit(line)
			}
			if skipped := len(raw) - len(shown); skipped > 0 {
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

// referenceSplitLines is patch/create.js splitLines.
func referenceSplitLines(text string) []string {
	parts := strings.Split(text, "\n")
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = p + "\n"
	}
	if strings.HasSuffix(text, "\n") {
		return result[:len(result)-1]
	}
	last := result[len(result)-1]
	result[len(result)-1] = last[:len(last)-1]
	return result
}

func referenceUnifiedPatch(path, oldStr, newStr string, context int) string {
	type comp struct {
		added, removed bool
		lines          []string
	}
	var diff []comp
	for _, part := range referenceParts(oldStr, newStr) {
		diff = append(diff, comp{added: part.added, removed: part.removed, lines: referenceSplitLines(part.value)})
	}
	diff = append(diff, comp{lines: []string{}})
	contextOf := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, e := range lines {
			out[i] = " " + e
		}
		return out
	}
	type hunk struct {
		oldStart, oldLines, newStart, newLines int
		lines                                  []string
	}
	var hunks []hunk
	oldRangeStart, newRangeStart := 0, 0
	var curRange []string
	oldLine, newLine := 1, 1
	for i, current := range diff {
		lines := current.lines
		if current.added || current.removed {
			if oldRangeStart == 0 {
				oldRangeStart, newRangeStart = oldLine, newLine
				curRange = nil
				if i > 0 && context > 0 {
					pl := diff[i-1].lines
					curRange = contextOf(pl[max(len(pl)-context, 0):])
				}
				oldRangeStart -= len(curRange)
				newRangeStart -= len(curRange)
			}
			prefix := "-"
			if current.added {
				prefix = "+"
			}
			for _, line := range lines {
				curRange = append(curRange, prefix+line)
			}
			if current.added {
				newLine += len(lines)
			} else {
				oldLine += len(lines)
			}
			continue
		}
		if oldRangeStart != 0 {
			if len(lines) <= context*2 && i < len(diff)-2 {
				curRange = append(curRange, contextOf(lines)...)
			} else {
				contextSize := min(len(lines), context)
				curRange = append(curRange, contextOf(lines[:contextSize])...)
				hunks = append(hunks, hunk{oldRangeStart, oldLine - oldRangeStart + contextSize, newRangeStart, newLine - newRangeStart + contextSize, curRange})
				oldRangeStart, newRangeStart = 0, 0
				curRange = nil
			}
		}
		oldLine += len(lines)
		newLine += len(lines)
	}
	ret := []string{"--- " + path, "+++ " + path}
	for _, h := range hunks {
		var lines []string
		for _, line := range h.lines {
			if strings.HasSuffix(line, "\n") {
				lines = append(lines, line[:len(line)-1])
			} else {
				lines = append(lines, line, `\ No newline at end of file`)
			}
		}
		oldStart, newStart := h.oldStart, h.newStart
		if h.oldLines == 0 {
			oldStart--
		}
		if h.newLines == 0 {
			newStart--
		}
		ret = append(ret, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, h.oldLines, newStart, h.newLines))
		ret = append(ret, lines...)
	}
	return strings.Join(ret, "\n") + "\n"
}

// TestDiffViewsMatchSplitPartReference checks the display diff and the unified patch, built from one jsdiff.LineDiff, against the
// split-part reference on randomized files and edits, for several context sizes.
func TestDiffViewsMatchSplitPartReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261012, 8))
	alphabet := []string{"a\n", "b\n", "\n", "c\r\n", "  indented\n", "x"}
	text := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		return b.String()
	}
	failures := 0
	for range 6000 {
		prefix, suffix := text(rng.IntN(30)), text(rng.IntN(30))
		oldStr := prefix + text(rng.IntN(5)) + suffix
		newStr := prefix + text(rng.IntN(5)) + suffix
		if rng.IntN(5) == 0 {
			newStr = text(rng.IntN(12))
		}
		context := rng.IntN(6)
		wantDiff, wantFirst := referenceDiffString(oldStr, newStr, context)
		gotDiff, gotFirst := generateDiffString(oldStr, newStr, context)
		wantPatch := referenceUnifiedPatch("f.txt", oldStr, newStr, context)
		gotPatch := generateUnifiedPatch("f.txt", oldStr, newStr, context)
		if gotDiff != wantDiff || gotFirst != wantFirst || gotPatch != wantPatch {
			if failures++; failures <= 3 {
				t.Errorf("old %q new %q context %d:\n diff  got %q (%d)\n       want %q (%d)\n patch got %q\n       want %q", oldStr, newStr, context, gotDiff, gotFirst, wantDiff, wantFirst, gotPatch, wantPatch)
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d edits differ from the reference", failures)
	}
}
