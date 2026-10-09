package tools

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
// tokenizer and Myers diff, then structuredPatch hunking and formatPatch
// rendering.
//
// Source: node_modules/diff/libesm/{diff/base.js,diff/line.js,patch/create.js}.

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsdiff"
)

// GenerateUnifiedPatch returns a unified diff for path byte-identical to
// upstream's generateUnifiedPatch; contextLines is its optional fourth
// parameter (default 4).
func GenerateUnifiedPatch(path, oldStr, newStr string, contextLines ...int) string {
	return generateUnifiedPatch(path, oldStr, newStr, optionalContextLines(contextLines))
}

func generateUnifiedPatch(path, oldStr, newStr string, context int) string {
	diff := myersLineDiff(oldStr, newStr)
	hunks := diffLinesResultToPatch(diff, context)
	return formatPatch(path, hunks)
}

// ─── diff components ──────────────────────────────────────────────────────────

type diffComp struct {
	value   string
	added   bool
	removed bool
	count   int
	lines   []string // nil until computed; the sentinel carries an empty slice
}

// myersLineDiff is jsdiff's diffLines (internal/jsdiff) as the diff components the hunking below reads.
func myersLineDiff(oldStr, newStr string) []diffComp {
	changes := jsdiff.DiffLines(oldStr, newStr)
	comps := make([]diffComp, len(changes))
	for i, change := range changes {
		comps[i] = diffComp{value: change.Value, added: change.Added, removed: change.Removed, count: change.Count}
	}
	return comps
}

// ─── structuredPatch hunking (patch/create.js diffLinesResultToPatch) ──────────

type patchHunk struct {
	oldStart int
	oldLines int
	newStart int
	newLines int
	lines    []string
}

// splitLines mirrors patch/create.js's splitLines (distinct from the diff
// tokenizer): split on "\n", re-append "\n" to each part, then drop the
// synthetic trailing empty line or strip the terminator off the last line
// when the value has no trailing newline.
func splitLines(text string) []string {
	hasTrailingNL := strings.HasSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = p + "\n"
	}
	if hasTrailingNL {
		result = result[:len(result)-1]
	} else {
		last := result[len(result)-1]
		result[len(result)-1] = last[:len(last)-1]
	}
	return result
}

func diffLinesResultToPatch(diff []diffComp, context int) []patchHunk {
	// Append an empty sentinel value to make hunk close-out uniform.
	diff = append(diff, diffComp{value: "", lines: []string{}})
	for i := range diff {
		if diff[i].lines == nil {
			diff[i].lines = splitLines(diff[i].value)
		}
	}

	contextLines := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, e := range lines {
			out[i] = " " + e
		}
		return out
	}

	var hunks []patchHunk
	oldRangeStart, newRangeStart := 0, 0
	var curRange []string
	oldLine, newLine := 1, 1

	for i := 0; i < len(diff); i++ {
		current := diff[i]
		lines := current.lines
		if current.added || current.removed {
			if oldRangeStart == 0 {
				oldRangeStart = oldLine
				newRangeStart = newLine
				if i > 0 {
					prev := diff[i-1]
					var prevCtx []string
					if context > 0 {
						pl := prev.lines
						start := max(len(pl)-context, 0)
						prevCtx = contextLines(pl[start:])
					}
					curRange = prevCtx
					oldRangeStart -= len(curRange)
					newRangeStart -= len(curRange)
				} else {
					curRange = nil
				}
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
		} else {
			if oldRangeStart != 0 {
				if len(lines) <= context*2 && i < len(diff)-2 {
					curRange = append(curRange, contextLines(lines)...)
				} else {
					contextSize := min(len(lines), context)
					curRange = append(curRange, contextLines(lines[:contextSize])...)
					hunks = append(hunks, patchHunk{
						oldStart: oldRangeStart,
						oldLines: oldLine - oldRangeStart + contextSize,
						newStart: newRangeStart,
						newLines: newLine - newRangeStart + contextSize,
						lines:    curRange,
					})
					oldRangeStart, newRangeStart = 0, 0
					curRange = nil
				}
			}
			oldLine += len(lines)
			newLine += len(lines)
		}
	}

	// Strip the trailing newline from each hunk line; insert the
	// "\ No newline at end of file" marker after any line lacking one.
	for h := range hunks {
		lines := hunks[h].lines
		for i := 0; i < len(lines); i++ {
			if strings.HasSuffix(lines[i], "\n") {
				lines[i] = lines[i][:len(lines[i])-1]
			} else {
				lines = append(lines[:i+1], append([]string{`\ No newline at end of file`}, lines[i+1:]...)...)
				i++
			}
		}
		hunks[h].lines = lines
	}
	return hunks
}

// ─── formatPatch (patch/create.js formatPatch, FILE_HEADERS_ONLY) ──────────────

func formatPatch(path string, hunks []patchHunk) string {
	ret := []string{"--- " + path, "+++ " + path}
	for _, h := range hunks {
		oldStart, newStart := h.oldStart, h.newStart
		// Unified-diff quirk: a zero-length side starts one lower.
		if h.oldLines == 0 {
			oldStart--
		}
		if h.newLines == 0 {
			newStart--
		}
		ret = append(ret, "@@ -"+strconv.Itoa(oldStart)+","+strconv.Itoa(h.oldLines)+
			" +"+strconv.Itoa(newStart)+","+strconv.Itoa(h.newLines)+" @@")
		ret = append(ret, h.lines...)
	}
	return strings.Join(ret, "\n") + "\n"
}
