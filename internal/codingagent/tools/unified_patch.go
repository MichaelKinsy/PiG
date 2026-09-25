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
// boundaries, so this ports jsdiff's own pipeline: line tokenizer → Myers
// diff (with jsdiff's diagonal tie-breaking) → structuredPatch hunking →
// formatPatch rendering.
//
// Source: node_modules/diff/libesm/{diff/base.js,diff/line.js,patch/create.js}.

import (
	"math"
	"strconv"
	"strings"
)

// GenerateUnifiedPatch returns a unified diff for path byte-identical to
// upstream's generateUnifiedPatch with the default 4 lines of context.
func GenerateUnifiedPatch(path, oldStr, newStr string) string {
	return generateUnifiedPatch(path, oldStr, newStr, 4)
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

// ─── line tokenizer (diff/line.js tokenize, line mode) ─────────────────────────

// tokenizeLines splits text into line tokens, each including its trailing
// newline (\n or \r\n); the final line has no terminator when text does not
// end in a newline. Mirrors jsdiff's line tokenizer after removeEmpty (no
// empty tokens arise from this input).
func tokenizeLines(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			out = append(out, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

// ─── Myers diff (diff/base.js) ─────────────────────────────────────────────────

type pathComponent struct {
	count   int
	added   bool
	removed bool
	prev    *pathComponent
}

type pathNode struct {
	oldPos int
	last   *pathComponent
}

func myersLineDiff(oldStr, newStr string) []diffComp {
	oldTokens := tokenizeLines(oldStr)
	newTokens := tokenizeLines(newStr)
	oldLen := len(oldTokens)
	newLen := len(newTokens)

	maxEditLength := newLen + oldLen
	bestPath := map[int]*pathNode{0: {oldPos: -1}}

	newPos := extractCommon(bestPath[0], newTokens, oldTokens, 0)
	if bestPath[0].oldPos+1 >= oldLen && newPos+1 >= newLen {
		return buildValues(bestPath[0].last, newTokens, oldTokens)
	}

	minDiagonal, maxDiagonal := math.MinInt, math.MaxInt
	for editLength := 1; editLength <= maxEditLength; editLength++ {
		lo := max(minDiagonal, -editLength)
		for diagonal := lo; diagonal <= min(maxDiagonal, editLength); diagonal += 2 {
			removePath := bestPath[diagonal-1]
			addPath := bestPath[diagonal+1]
			if removePath != nil {
				delete(bestPath, diagonal-1)
			}

			canAdd := false
			if addPath != nil {
				addPathNewPos := addPath.oldPos - diagonal
				canAdd = addPathNewPos >= 0 && addPathNewPos < newLen
			}
			canRemove := removePath != nil && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				delete(bestPath, diagonal)
				continue
			}

			var basePath *pathNode
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				basePath = addToPath(addPath, true, false, 0)
			} else {
				basePath = addToPath(removePath, false, true, 1)
			}

			newPos = extractCommon(basePath, newTokens, oldTokens, diagonal)
			if basePath.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return buildValues(basePath.last, newTokens, oldTokens)
			}
			bestPath[diagonal] = basePath
			if basePath.oldPos+1 >= oldLen {
				maxDiagonal = min(maxDiagonal, diagonal-1)
			}
			if newPos+1 >= newLen {
				minDiagonal = max(minDiagonal, diagonal+1)
			}
		}
	}
	return nil
}

func addToPath(path *pathNode, added, removed bool, oldPosInc int) *pathNode {
	last := path.last
	if last != nil && last.added == added && last.removed == removed {
		return &pathNode{
			oldPos: path.oldPos + oldPosInc,
			last:   &pathComponent{count: last.count + 1, added: added, removed: removed, prev: last.prev},
		}
	}
	return &pathNode{
		oldPos: path.oldPos + oldPosInc,
		last:   &pathComponent{count: 1, added: added, removed: removed, prev: last},
	}
}

func extractCommon(basePath *pathNode, newTokens, oldTokens []string, diagonal int) int {
	newLen := len(newTokens)
	oldLen := len(oldTokens)
	oldPos := basePath.oldPos
	newPos := oldPos - diagonal
	commonCount := 0
	for newPos+1 < newLen && oldPos+1 < oldLen && oldTokens[oldPos+1] == newTokens[newPos+1] {
		newPos++
		oldPos++
		commonCount++
	}
	if commonCount > 0 {
		basePath.last = &pathComponent{count: commonCount, prev: basePath.last}
	}
	basePath.oldPos = oldPos
	return newPos
}

func buildValues(last *pathComponent, newTokens, oldTokens []string) []diffComp {
	var comps []*pathComponent
	for last != nil {
		comps = append(comps, last)
		last = last.prev
	}
	// Reverse into forward order.
	for i, j := 0, len(comps)-1; i < j; i, j = i+1, j-1 {
		comps[i], comps[j] = comps[j], comps[i]
	}

	out := make([]diffComp, 0, len(comps))
	newPos, oldPos := 0, 0
	for _, c := range comps {
		var value string
		if !c.removed {
			value = strings.Join(newTokens[newPos:newPos+c.count], "")
			newPos += c.count
			if !c.added {
				oldPos += c.count
			}
		} else {
			value = strings.Join(oldTokens[oldPos:oldPos+c.count], "")
			oldPos += c.count
		}
		out = append(out, diffComp{value: value, added: c.added, removed: c.removed, count: c.count})
	}
	return out
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
