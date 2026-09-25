// Package tools: edit-diff helpers.
//
// Mirrors upstream packages/coding-agent/src/core/tools/edit-diff.ts:
// line-ending detection, BOM stripping, fuzzy matching, and the
// position-based multi-file edit application algorithm.
package tools

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// detectLineEnding returns "\r\n" if content uses CRLF, else "\n".
// Matches upstream detectLineEnding.
func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// normalizeToLF converts \r\n and stray \r to \n.
func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// restoreLineEndings converts \n back to the original ending.
func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// fuzzyMatchResult mirrors upstream FuzzyMatchResult.
type fuzzyMatchResult struct {
	found                 bool
	index                 int
	matchLength           int
	usedFuzzyMatch        bool
	contentForReplacement string
}

// fuzzyFindText tries exact match first, then fuzzy. When fuzzy is used,
// contentForReplacement is the normalized content.
func fuzzyFindText(content, oldText string) fuzzyMatchResult {
	if i := strings.Index(content, oldText); i != -1 {
		return fuzzyMatchResult{
			found:                 true,
			index:                 i,
			matchLength:           len(oldText),
			usedFuzzyMatch:        false,
			contentForReplacement: content,
		}
	}
	fc := normalizeForFuzzyMatch(content)
	fo := normalizeForFuzzyMatch(oldText)
	i := strings.Index(fc, fo)
	if i == -1 {
		return fuzzyMatchResult{contentForReplacement: content}
	}
	return fuzzyMatchResult{
		found:                 true,
		index:                 i,
		matchLength:           len(fo),
		usedFuzzyMatch:        true,
		contentForReplacement: fc,
	}
}

// countOccurrences counts occurrences via fuzzy-normalized strings, matching upstream.
func countOccurrences(content, oldText string) int {
	fc := normalizeForFuzzyMatch(content)
	fo := normalizeForFuzzyMatch(oldText)
	if fo == "" {
		return 0
	}
	return strings.Count(fc, fo)
}

// matchedEdit is the internal edit-with-position record.
type matchedEdit struct {
	editIndex   int
	matchIndex  int
	matchLength int
	newText     string
}

// appliedEditsResult mirrors upstream AppliedEditsResult.
type appliedEditsResult struct {
	baseContent string
	newContent  string
}

// applyEditsToNormalizedContent ports upstream's algorithm:
//   - normalize each edit's old/new text to LF
//   - reject empty oldText
//   - probe matches against the original normalized content; if any edit
//     needs fuzzy matching, switch baseContent to the fuzzy-normalized form
//   - error: not found / duplicate / overlap / no-change
//   - apply edits in reverse-position order so indices stay valid
func applyEditsToNormalizedContent(normalizedContent string, edits []editEntry, path string) (appliedEditsResult, error) {
	normEdits := make([]editEntry, len(edits))
	for i, e := range edits {
		normEdits[i] = editEntry{
			OldText: normalizeToLF(e.OldText),
			NewText: normalizeToLF(e.NewText),
		}
	}

	for i, e := range normEdits {
		if e.OldText == "" {
			return appliedEditsResult{}, emptyOldTextErr(path, i, len(normEdits))
		}
	}

	// First probe: decide whether any edit needs fuzzy matching.
	usedFuzzyAny := false
	for _, e := range normEdits {
		m := fuzzyFindText(normalizedContent, e.OldText)
		if m.usedFuzzyMatch {
			usedFuzzyAny = true
		}
	}
	baseContent := normalizedContent
	if usedFuzzyAny {
		baseContent = normalizeForFuzzyMatch(normalizedContent)
	}

	// Second probe: locate each edit in baseContent.
	matched := make([]matchedEdit, 0, len(normEdits))
	for i, e := range normEdits {
		m := fuzzyFindText(baseContent, e.OldText)
		if !m.found {
			return appliedEditsResult{}, notFoundErr(path, i, len(normEdits))
		}
		// Count in baseContent (or its fuzzy normalization, same as upstream).
		occ := countOccurrences(baseContent, e.OldText)
		if occ > 1 {
			return appliedEditsResult{}, duplicateErr(path, i, len(normEdits), occ)
		}
		matched = append(matched, matchedEdit{
			editIndex:   i,
			matchIndex:  m.index,
			matchLength: m.matchLength,
			newText:     e.NewText,
		})
	}

	// Sort ascending by position, check overlaps.
	for i := 1; i < len(matched); i++ {
		for j := i; j > 0 && matched[j-1].matchIndex > matched[j].matchIndex; j-- {
			matched[j-1], matched[j] = matched[j], matched[j-1]
		}
	}
	for i := 1; i < len(matched); i++ {
		prev := matched[i-1]
		cur := matched[i]
		if prev.matchIndex+prev.matchLength > cur.matchIndex {
			return appliedEditsResult{}, fmt.Errorf(
				"edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				prev.editIndex, cur.editIndex, path)
		}
	}

	// Upstream diffs against the original content. A fuzzy match rewrites
	// only the lines its replacements touch, from the normalized base; every
	// other line keeps its original bytes.
	var newContent string
	if usedFuzzyAny {
		var err error
		newContent, err = applyReplacementsPreservingUnchangedLines(normalizedContent, baseContent, matched)
		if err != nil {
			return appliedEditsResult{}, err
		}
	} else {
		newContent = applyReplacements(baseContent, matched, 0)
	}

	if normalizedContent == newContent {
		return appliedEditsResult{}, noChangeErr(path, len(normEdits))
	}

	return appliedEditsResult{baseContent: normalizedContent, newContent: newContent}, nil
}

// splitLinesWithEndings mirrors upstream splitLinesWithEndings
// (content.match(/[^\n]*\n|[^\n]+/g)): each line keeps its "\n".
func splitLinesWithEndings(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lineSpan mirrors upstream LineSpan.
type lineSpan struct{ start, end int }

// getLineSpans mirrors upstream getLineSpans.
func getLineSpans(content string) []lineSpan {
	lines := splitLinesWithEndings(content)
	spans := make([]lineSpan, len(lines))
	offset := 0
	for i, line := range lines {
		spans[i] = lineSpan{start: offset, end: offset + len(line)}
		offset += len(line)
	}
	return spans
}

var errReplacementOutsideBase = errors.New("Replacement range is outside the base content.")

// getReplacementLineRange mirrors upstream getReplacementLineRange: the
// half-open line range [startLine, endLine) a replacement touches.
func getReplacementLineRange(lines []lineSpan, r matchedEdit) (int, int, error) {
	replacementStart := r.matchIndex
	replacementEnd := r.matchIndex + r.matchLength
	startLine := -1
	for i, line := range lines {
		if replacementStart >= line.start && replacementStart < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, errReplacementOutsideBase
	}
	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, errReplacementOutsideBase
	}
	return startLine, endLine + 1, nil
}

// applyReplacements mirrors upstream applyReplacements: replacements sorted
// by position, applied from the end so offsets stay valid.
func applyReplacements(content string, replacements []matchedEdit, offset int) string {
	result := content
	for _, r := range slices.Backward(replacements) {
		i := r.matchIndex - offset
		result = result[:i] + r.newText + result[i+r.matchLength:]
	}
	return result
}

// applyReplacementsPreservingUnchangedLines mirrors the upstream function of
// the same name: each replacement matched against baseContent (a normalized
// view of originalContent) is widened to the lines it touches; those lines
// are rewritten from the base, and all other lines are copied from the
// original.
func applyReplacementsPreservingUnchangedLines(originalContent, baseContent string, replacements []matchedEdit) (string, error) {
	originalLines := splitLinesWithEndings(originalContent)
	baseLines := getLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return "", errors.New("Cannot preserve unchanged lines because the base content has a different line count.")
	}
	type group struct {
		startLine, endLine int
		replacements       []matchedEdit
	}
	var groups []*group
	sorted := slices.Clone(replacements)
	slices.SortStableFunc(sorted, func(a, b matchedEdit) int { return a.matchIndex - b.matchIndex })
	for _, r := range sorted {
		startLine, endLine, err := getReplacementLineRange(baseLines, r)
		if err != nil {
			return "", err
		}
		if n := len(groups); n > 0 && startLine < groups[n-1].endLine {
			current := groups[n-1]
			current.endLine = max(current.endLine, endLine)
			current.replacements = append(current.replacements, r)
			continue
		}
		groups = append(groups, &group{startLine: startLine, endLine: endLine, replacements: []matchedEdit{r}})
	}
	var b strings.Builder
	originalLineIndex := 0
	for _, g := range groups {
		b.WriteString(strings.Join(originalLines[originalLineIndex:g.startLine], ""))
		groupStart := baseLines[g.startLine].start
		groupEnd := baseLines[g.endLine-1].end
		b.WriteString(applyReplacements(baseContent[groupStart:groupEnd], g.replacements, groupStart))
		originalLineIndex = g.endLine
	}
	b.WriteString(strings.Join(originalLines[originalLineIndex:], ""))
	return b.String(), nil
}

// ─── error messages (match upstream wording) ──────────────────────────────────

func notFoundErr(path string, i, total int) error {
	if total == 1 {
		return fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
	}
	return fmt.Errorf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", i, path)
}

func duplicateErr(path string, i, total, occ int) error {
	if total == 1 {
		return fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occ, path)
	}
	return fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", occ, i, path)
}

func emptyOldTextErr(path string, i, total int) error {
	if total == 1 {
		return fmt.Errorf("oldText must not be empty in %s.", path)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s.", i, path)
}

func noChangeErr(path string, total int) error {
	if total == 1 {
		return fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
	}
	return fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
}
