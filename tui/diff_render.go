package tui

// diff_render.go: intra-line word-level diff rendering.
//
// Ports upstream diff.ts (147 LOC). Produces colored diff output with:
// - Context lines: dim/gray
// - Removed lines: red with inverse on changed tokens
// - Added lines: green with inverse on changed tokens
//
// Word-level diffing is jsdiff's diffWords (internal/jsdiff).
//
// Upstream reference: components/diff.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsdiff"
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

// RenderDiffOptions mirrors upstream RenderDiffOptions (diff.ts:68). The file path is unused and kept for API compatibility.
type RenderDiffOptions struct {
	FilePath string
}

// RenderDiff renders a diff string with colored lines and intra-line highlighting, as upstream renderDiff does: each row is theme.fg of the toolDiffContext, toolDiffRemoved or toolDiffAdded token.
func RenderDiff(diffText string, _ ...RenderDiffOptions) string {
	th := ActiveTheme()
	lines := strings.Split(diffText, "\n")
	var result []string

	i := 0
	for i < len(lines) {
		line := lines[i]
		parsed := parseDiffLine(line)

		if parsed == nil {
			// Unparseable line: show as context.
			result = append(result, th.Fg("toolDiffContext", line))
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

				result = append(result, th.Fg("toolDiffRemoved", "-"+removed[0].lineNum+" "+removedLine))
				result = append(result, th.Fg("toolDiffAdded", "+"+added[0].lineNum+" "+addedLine))
			} else {
				for _, r := range removed {
					result = append(result, th.Fg("toolDiffRemoved", "-"+r.lineNum+" "+replaceTabs(r.content)))
				}
				for _, a := range added {
					result = append(result, th.Fg("toolDiffAdded", "+"+a.lineNum+" "+replaceTabs(a.content)))
				}
			}
		case "+":
			result = append(result, th.Fg("toolDiffAdded", "+"+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		default:
			result = append(result, th.Fg("toolDiffContext", " "+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		}
	}

	return strings.Join(result, "\n")
}

// RenderIntraLineDiff is diff.ts renderIntraLineDiff: the word-level diff of the two lines (jsdiff's diffWords), with inverse on the
// changed parts and the leading whitespace of the first removed and first added part left outside the highlight.
func RenderIntraLineDiff(oldContent, newContent string) (removedLine, addedLine string) {
	inverse := func(value string) string { return "\x1b[7m" + value + "\x1b[27m" }
	var removed, added strings.Builder
	firstRemoved, firstAdded := true, true
	for _, part := range jsdiff.DiffWords(oldContent, newContent) {
		value := part.Value
		switch {
		case part.Removed:
			if firstRemoved {
				trimmed := strings.TrimLeftFunc(value, isJSWhitespace)
				removed.WriteString(value[:len(value)-len(trimmed)])
				value, firstRemoved = trimmed, false
			}
			if value != "" {
				removed.WriteString(inverse(value))
			}
		case part.Added:
			if firstAdded {
				trimmed := strings.TrimLeftFunc(value, isJSWhitespace)
				added.WriteString(value[:len(value)-len(trimmed)])
				value, firstAdded = trimmed, false
			}
			if value != "" {
				added.WriteString(inverse(value))
			}
		default:
			removed.WriteString(value)
			added.WriteString(value)
		}
	}
	return removed.String(), added.String()
}
