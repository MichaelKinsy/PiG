package tools

// Ports packages/durable/src/tools/edit-diff.ts (generateUnifiedPatch).
//
// Byte-exact port of jsdiff 8.0.4's unified-patch generation, matching
// upstream generateUnifiedPatch (edit-diff.ts):
//
//	Diff.createTwoFilesPatch(path, path, old, new, undefined, undefined,
//	  { context: 4, headerOptions: Diff.FILE_HEADERS_ONLY })
//
// The `patch` field of the edit tool's details is a machine-readable SDK
// contract, so it must match pi byte-for-byte. Go's diff libraries use
// different algorithms (difflib/Myers variants) that pick different hunk
// boundaries, so this ports jsdiff's own pipeline: internal/jsdiff's line
// diff, then structuredPatch hunking and formatPatch rendering.
//
// Source: node_modules/diff/libesm/{diff/base.js,diff/line.js,patch/create.js}.

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsdiff"
)

// GenerateUnifiedPatch returns a unified diff for path byte-identical to
// upstream's generateUnifiedPatch with the default 4 lines of context.
func GenerateUnifiedPatch(path, oldStr, newStr string) string {
	return generateUnifiedPatch(path, oldStr, newStr, 4)
}

func generateUnifiedPatch(path, oldStr, newStr string, context int) string {
	return unifiedPatchOf(path, jsdiff.DiffLineRuns(oldStr, newStr), context)
}

// unifiedPatchOf is generateUnifiedPatch over the line diff d.
func unifiedPatchOf(path string, d *jsdiff.LineDiff, context int) string {
	return formatPatch(path, diffLinesResultToPatch(d, context))
}

// ─── structuredPatch hunking (patch/create.js diffLinesResultToPatch) ──────────

type patchHunk struct {
	oldStart int
	oldLines int
	newStart int
	newLines int
	lines    []string
}

// hunkLine appends one hunk line: prefix and the line token without its "\n". A token without one (the last line of a text that does
// not end in a newline) is followed by "\ No newline at end of file", as the step that strips the newlines inserts it.
func hunkLine(lines []string, prefix byte, token string) []string {
	if body, ok := strings.CutSuffix(token, "\n"); ok {
		return append(lines, string(prefix)+body)
	}
	return append(lines, string(prefix)+token, `\ No newline at end of file`)
}

// diffLinesResultToPatch reads only the lines a hunk shows: patch/create.js splits every part's value into lines (splitLines) and
// re-appends their "\n", which are exactly the line tokens of the diff.
func diffLinesResultToPatch(d *jsdiff.LineDiff, context int) []patchHunk {
	runs := d.Runs
	// jsdiff appends an empty sentinel part to make hunk close-out uniform; index len(runs) is it.
	partCount := len(runs) + 1

	var hunks []patchHunk
	oldRangeStart, newRangeStart := 0, 0
	var curRange []string
	oldLine, newLine := 1, 1

	for i := range partCount {
		var current jsdiff.LineRun
		if i < len(runs) {
			current = runs[i]
		}
		if current.Added || current.Removed {
			if oldRangeStart == 0 {
				oldRangeStart = oldLine
				newRangeStart = newLine
				curRange = nil
				if i > 0 && context > 0 {
					prev := runs[i-1]
					for k := max(prev.Count-context, 0); k < prev.Count; k++ {
						curRange = hunkLine(curRange, ' ', d.Line(prev, k))
					}
					shown := min(prev.Count, context)
					oldRangeStart -= shown
					newRangeStart -= shown
				}
			}
			prefix := byte('-')
			if current.Added {
				prefix = '+'
			}
			for k := range current.Count {
				curRange = hunkLine(curRange, prefix, d.Line(current, k))
			}
			if current.Added {
				newLine += current.Count
			} else {
				oldLine += current.Count
			}
			continue
		}
		if oldRangeStart != 0 {
			shown := current.Count
			closes := current.Count > context*2 || i >= partCount-2
			if closes {
				shown = min(current.Count, context)
			}
			for k := range shown {
				curRange = hunkLine(curRange, ' ', d.Line(current, k))
			}
			if closes {
				hunks = append(hunks, patchHunk{
					oldStart: oldRangeStart,
					oldLines: oldLine - oldRangeStart + shown,
					newStart: newRangeStart,
					newLines: newLine - newRangeStart + shown,
					lines:    curRange,
				})
				oldRangeStart, newRangeStart = 0, 0
				curRange = nil
			}
		}
		oldLine += current.Count
		newLine += current.Count
	}
	return hunks
}

// ─── formatPatch (patch/create.js formatPatch, FILE_HEADERS_ONLY) ──────────────

func formatPatch(path string, hunks []patchHunk) string {
	var b strings.Builder
	b.WriteString("--- ")
	b.WriteString(path)
	b.WriteString("\n+++ ")
	b.WriteString(path)
	b.WriteByte('\n')
	for _, h := range hunks {
		oldStart, newStart := h.oldStart, h.newStart
		// Unified-diff quirk: a zero-length side starts one lower.
		if h.oldLines == 0 {
			oldStart--
		}
		if h.newLines == 0 {
			newStart--
		}
		b.WriteString("@@ -")
		b.WriteString(strconv.Itoa(oldStart))
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(h.oldLines))
		b.WriteString(" +")
		b.WriteString(strconv.Itoa(newStart))
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(h.newLines))
		b.WriteString(" @@\n")
		for _, line := range h.lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
