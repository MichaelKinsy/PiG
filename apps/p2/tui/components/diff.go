package components

// Copied from coding-agent interactive/components/diff.ts; the theme is a parameter (section 5.4).
//
// pi: apps/p2/src/tui/components/diff.ts

import (
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/internal/jsdiff"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// diffLineRe is parseDiffLine's /^([+-\s])(\s*\d*)\s(.*)$/ over JavaScript's whitespace and line terminators.
var diffLineRe = lazyregexp.New(`^([-+\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}])([\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*\d*)[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]([^\n\r\x{2028}\x{2029}]*)$`)

type diffLine struct{ prefix, lineNum, content string }

// parseDiffLine extracts the prefix, line number and content of "+123 content", "-123 content", " 123 content" or "     ...".
func parseDiffLine(line string) *diffLine {
	match := diffLineRe.FindStringSubmatch(line)
	if match == nil {
		return nil
	}
	return &diffLine{prefix: match[1], lineNum: match[2], content: match[3]}
}

func replaceTabs(text string) string { return strings.ReplaceAll(text, "\t", "   ") }

// renderIntraLineDiff computes the word-level diff and renders it with inverse on the changed parts, leaving the leading whitespace of the first
// removed and first added part outside the highlight.
func renderIntraLineDiff(t *theme.Theme, oldContent, newContent string) (removedLine, addedLine string) {
	var removed, added strings.Builder
	firstRemoved, firstAdded := true, true
	for _, part := range jsdiff.DiffWords(oldContent, newContent) {
		value := part.Value
		switch {
		case part.Removed:
			if firstRemoved {
				trimmed := strings.TrimLeftFunc(value, jsstring.IsSpace)
				removed.WriteString(value[:len(value)-len(trimmed)])
				value, firstRemoved = trimmed, false
			}
			if value != "" {
				removed.WriteString(t.Inverse(value))
			}
		case part.Added:
			if firstAdded {
				trimmed := strings.TrimLeftFunc(value, jsstring.IsSpace)
				added.WriteString(value[:len(value)-len(trimmed)])
				value, firstAdded = trimmed, false
			}
			if value != "" {
				added.WriteString(t.Inverse(value))
			}
		default:
			removed.WriteString(value)
			added.WriteString(value)
		}
	}
	return removed.String(), added.String()
}

// RenderDiff renders a diff string with colored lines and intra-line change highlighting: context lines dim, removed lines red and added lines green,
// with inverse on changed tokens.
func RenderDiff(diffText string, t *theme.Theme) string {
	lines := strings.Split(diffText, "\n")
	var result []string
	for i := 0; i < len(lines); {
		parsed := parseDiffLine(lines[i])
		if parsed == nil {
			result = append(result, t.Fg("toolDiffContext", lines[i]))
			i++
			continue
		}
		switch parsed.prefix {
		case "-":
			var removed, added []diffLine
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "-" {
					break
				}
				removed = append(removed, *p)
				i++
			}
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "+" {
					break
				}
				added = append(added, *p)
				i++
			}
			// Only do intra-line diffing when there's exactly one removed and one added line (a single line modification). Otherwise, show lines as-is.
			if len(removed) == 1 && len(added) == 1 {
				removedLine, addedLine := renderIntraLineDiff(t, replaceTabs(removed[0].content), replaceTabs(added[0].content))
				result = append(result, t.Fg("toolDiffRemoved", "-"+removed[0].lineNum+" "+removedLine), t.Fg("toolDiffAdded", "+"+added[0].lineNum+" "+addedLine))
			} else {
				for _, r := range removed {
					result = append(result, t.Fg("toolDiffRemoved", "-"+r.lineNum+" "+replaceTabs(r.content)))
				}
				for _, a := range added {
					result = append(result, t.Fg("toolDiffAdded", "+"+a.lineNum+" "+replaceTabs(a.content)))
				}
			}
		case "+":
			result = append(result, t.Fg("toolDiffAdded", "+"+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		default:
			result = append(result, t.Fg("toolDiffContext", " "+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		}
	}
	return strings.Join(result, "\n")
}
