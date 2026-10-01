package tui

// diff_render.go: intra-line word-level diff rendering.
//
// Ports upstream diff.ts (147 LOC). Produces colored diff output with:
// - Context lines: dim/gray
// - Removed lines: red with inverse on changed tokens
// - Added lines: green with inverse on changed tokens
//
// Word-level diffing uses a simple LCS-based approach (no external dep).
//
// Upstream reference: components/diff.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// diffLineRe is upstream components/diff.ts parseDiffLine's /^([+-\s])(\s*\d*)\s(.*)$/: group 1 is the prefix, group 2 the padded line number, then one separator space and the content. The classes are JavaScript's: `\s` includes the Unicode spaces, and `.` excludes CR, LS and PS, so a line holding U+2028 or U+2029 does not parse.
var diffLineRe = lazyregexp.New(`^([-+\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}])([\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*\d*)[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]([^\n\r\x{2028}\x{2029}]*)$`)

type diffLineParsed struct {
	prefix  string
	lineNum string
	content string
}

func parseDiffLine(line string) *diffLineParsed {
	m := diffLineRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	return &diffLineParsed{prefix: m[1], lineNum: m[2], content: m[3]}
}

// replaceTabs replaces tabs with 3 spaces (matching upstream).
func replaceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

// RenderDiff renders a diff string with colored lines and intra-line highlighting, as upstream renderDiff does: each row is theme.fg of the toolDiffContext, toolDiffRemoved or toolDiffAdded token.
func RenderDiff(diffText string) string {
	th := ActiveTheme()
	lines := strings.Split(diffText, "\n")
	var result []string

	i := 0
	for i < len(lines) {
		line := lines[i]
		parsed := parseDiffLine(line)

		if parsed == nil {
			// Unparseable line: show as context.
			result = append(result, th.FgText("toolDiffContext", line))
			i++
			continue
		}

		switch parsed.prefix {
		case "-":
			// Collect consecutive removed lines.
			var removed []diffLineParsed
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "-" {
					break
				}
				removed = append(removed, *p)
				i++
			}
			// Collect consecutive added lines.
			var added []diffLineParsed
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "+" {
					break
				}
				added = append(added, *p)
				i++
			}

			// 1:1 modification: do intra-line word diff.
			if len(removed) == 1 && len(added) == 1 {
				oldContent := replaceTabs(removed[0].content)
				newContent := replaceTabs(added[0].content)
				removedLine, addedLine := RenderIntraLineDiff(oldContent, newContent)

				result = append(result, th.FgText("toolDiffRemoved", "-"+removed[0].lineNum+" "+removedLine))
				result = append(result, th.FgText("toolDiffAdded", "+"+added[0].lineNum+" "+addedLine))
			} else {
				for _, r := range removed {
					result = append(result, th.FgText("toolDiffRemoved", "-"+r.lineNum+" "+replaceTabs(r.content)))
				}
				for _, a := range added {
					result = append(result, th.FgText("toolDiffAdded", "+"+a.lineNum+" "+replaceTabs(a.content)))
				}
			}
		case "+":
			result = append(result, th.FgText("toolDiffAdded", "+"+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		default:
			result = append(result, th.FgText("toolDiffContext", " "+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		}
	}

	return strings.Join(result, "\n")
}

// RenderIntraLineDiff produces word-level diff with inverse highlighting on changes.
// Matches upstream's renderIntraLineDiff behavior.
func RenderIntraLineDiff(oldContent, newContent string) (removedLine, addedLine string) {
	oldWords := splitWords(oldContent)
	newWords := splitWords(newContent)

	diffs := diffWords(oldWords, newWords)

	inverse := "\x1b[7m"
	noInverse := "\x1b[27m"

	var remBuf, addBuf strings.Builder
	for _, d := range diffs {
		text := d.text
		switch d.op {
		case diffEqual:
			remBuf.WriteString(text)
			addBuf.WriteString(text)
		case diffRemove:
			remBuf.WriteString(inverse)
			remBuf.WriteString(text)
			remBuf.WriteString(noInverse)
		case diffAdd:
			addBuf.WriteString(inverse)
			addBuf.WriteString(text)
			addBuf.WriteString(noInverse)
		}
	}

	return remBuf.String(), addBuf.String()
}

// splitWords splits text into words preserving whitespace as separate tokens.
func splitWords(s string) []string {
	var tokens []string
	i := 0
	for i < len(s) {
		if s[i] == ' ' || s[i] == '\t' {
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			tokens = append(tokens, s[i:j])
			i = j
		} else {
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' {
				j++
			}
			tokens = append(tokens, s[i:j])
			i = j
		}
	}
	return tokens
}

type diffOp int

const (
	diffEqual diffOp = iota
	diffRemove
	diffAdd
)

type diffChunk struct {
	op   diffOp
	text string
}

// diffWords computes word-level diff using LCS.
func diffWords(old, new []string) []diffChunk {
	m, n := len(old), len(new)

	// Build the (m+1)x(n+1) LCS table. Each dimension is allocated at the
	// input length and grown by one, so no size is computed arithmetically.
	dp := append(make([][]int, m), nil)
	for i := range dp {
		dp[i] = append(make([]int, n), 0)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack to produce diff.
	var result []diffChunk
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && old[i-1] == new[j-1]:
			result = append(result, diffChunk{diffEqual, old[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			result = append(result, diffChunk{diffAdd, new[j-1]})
			j--
		default:
			result = append(result, diffChunk{diffRemove, old[i-1]})
			i--
		}
	}

	// Reverse (backtrack produces results in reverse order).
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}

	return result
}
