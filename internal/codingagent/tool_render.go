package codingagent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// toolBodyRendererForCall derives display-only file metadata from the retained call and result. Upstream write previews and read highlighting use call arguments, not private runtime result details.
func toolBodyRendererForCall(call ai.ToolCall, result agent.AgentToolResult) func(int, bool) []string {
	path, _ := call.Arguments["file_path"].(string)
	if call.Arguments["file_path"] == nil {
		path, _ = call.Arguments["path"].(string)
	}
	switch call.Name {
	case "write":
		content, ok := call.Arguments["content"].(string)
		if !ok {
			return func(width int, _ bool) []string {
				return styleWrapRows("[invalid content arg - expected string]", tui.ActiveTheme().Error, tui.SGRFgReset, width)
			}
		}
		result.Details = &tools.WriteDetails{Path: path, Content: content}
	case "read":
		var details tools.ReadDetails
		switch d := result.Details.(type) {
		case *tools.ReadDetails:
			if d != nil {
				details = *d
			}
		case map[string]any:
			if data, err := json.Marshal(d); err == nil {
				_ = json.Unmarshal(data, &details)
			}
		}
		details.Path = path
		result.Details = &details
	}
	return toolBodyRenderer(call.Name, result, nil)
}

// toolBodyRenderer returns a per-tool body renderer based on the
// AgentToolResult.Details payload, or nil if the tool didn't supply
// typed details. The returned func is wired into the
// ToolExecutionComponent.BodyRenderer slot so Ctrl+O expanding the
// body shows a structured view (unified diff for edit and upstream read/write
// presentation) instead of raw text.
//
// Renderers return pre-styled visual rows without the `│ ` prefix. The
// component adds the lifecycle background and horizontal padding.
//
// took is the run's duration when the card saw it start, and nil when it
// did not (a result rebuilt from the transcript), like upstream's startedAt.
func toolBodyRenderer(toolName string, result agent.AgentToolResult, took *time.Duration) func(width int, expanded bool) []string {
	var elapsed time.Duration
	if took != nil {
		elapsed = *took
	}
	switch toolName {
	case "bash", "powershell":
		// Both shell tools share upstream createShellRenderers
		// (renderers/index.ts createAllToolRenderers).
		return makeShellBodyRenderer(result.Content, result.Details, false, took)
	case "edit":
		if result.IsError {
			return func(width int, _ bool) []string {
				return styleWrapRows(result.Content, tui.ActiveTheme().Error, tui.SGRFgReset, width)
			}
		}
		// Built-in edit tool: render the precomputed display diff (upstream
		// SDK contract EditToolDetails.diff).
		if d, ok := result.Details.(*tools.EditToolDetails); ok && d != nil {
			return func(width int, _ bool) []string { return renderDiffString(d.Diff, width) }
		}
		// Extension-provided tools named edit can send details as
		// JSON which arrives as map[string]any after detailsToAny. Extract
		// the diff string and render it with line-numbered coloring. This is
		// also the resume path: persisted EditToolDetails reload as a map.
		if diffStr := extractDiffString(result.Details); diffStr != "" {
			return func(width int, _ bool) []string { return renderDiffString(diffStr, width) }
		}
		return nil
	case "grep", "find", "ls":
		return makeListBodyRenderer(toolName, result.Content, result.Details)
	case "write":
		d, ok := result.Details.(*tools.WriteDetails)
		if !ok || d == nil {
			return nil
		}
		return func(width int, expanded bool) []string {
			out := renderWriteContent(d, width, expanded)
			if result.IsError && result.Content != "" {
				if len(out) > 0 {
					out = append(out, "")
				}
				out = append(out, styleWrapRows(result.Content, tui.ActiveTheme().Error, tui.SGRFgReset, width)...)
			}
			return out
		}
	case "read":
		d, ok := result.Details.(*tools.ReadDetails)
		if !ok || d == nil {
			return nil
		}
		return func(width int, expanded bool) []string {
			if result.IsError {
				return renderReadError(result.Content, width, expanded)
			}
			return renderReadLines(d, result.Content, width, expanded)
		}
	}

	// Extension-provided preview: when the tool result carries a Preview
	// string, use it as the collapsed view (instead of the generic
	// tail-of-output preview). The full Content is shown on Ctrl+O expand.
	// This gives subprocess extensions control over collapsed rendering
	// without implementing a full in-process BodyRenderer.
	if result.Preview != "" {
		return makePreviewBodyRenderer(result.Content, result.Preview, elapsed)
	}

	return nil
}

// extractDiffString retrieves a "diff" string from generic details
// returned by extension-provided tools. Extensions serialize details as
// JSON which arrives in Go as map[string]any after detailsToAny.
func extractDiffString(details any) string {
	m, ok := details.(map[string]any)
	if !ok {
		return ""
	}
	s, ok := m["diff"].(string)
	if !ok {
		return ""
	}
	return s
}

// renderDiffString renders a pre-generated diff string (from
// generateDiffString / upstream edit-diff.ts) with colored lines and
// intra-line word-level change highlighting. Mirrors upstream
// components/diff.ts::renderDiff.
func renderDiffString(diffText string, width int) []string {
	lines := strings.Split(diffText, "\n")
	var out []string

	i := 0
	for i < len(lines) {
		line := lines[i]
		prefix, lineNum, content, ok := parseDiffLine(line)
		if !ok {
			out = append(out, dimWrap(line, width))
			i++
			continue
		}

		type diffEntry struct {
			lineNum string
			content string
		}

		switch prefix {
		case '-':
			var removed []diffEntry
			for i < len(lines) {
				p, ln, c, ok2 := parseDiffLine(lines[i])
				if !ok2 || p != '-' {
					break
				}
				removed = append(removed, diffEntry{ln, c})
				i++
			}
			var added []diffEntry
			for i < len(lines) {
				p, ln, c, ok2 := parseDiffLine(lines[i])
				if !ok2 || p != '+' {
					break
				}
				added = append(added, diffEntry{ln, c})
				i++
			}
			if len(removed) == 1 && len(added) == 1 {
				removedHL, addedHL := tui.RenderIntraLineDiff(
					replaceTabs(removed[0].content),
					replaceTabs(added[0].content),
				)
				out = append(out, redWrap("-"+removed[0].lineNum+" "+removedHL, width))
				out = append(out, greenWrap("+"+added[0].lineNum+" "+addedHL, width))
			} else {
				for _, r := range removed {
					out = append(out, redWrap("-"+r.lineNum+" "+replaceTabs(r.content), width))
				}
				for _, a := range added {
					out = append(out, greenWrap("+"+a.lineNum+" "+replaceTabs(a.content), width))
				}
			}
		case '+':
			out = append(out, greenWrap("+"+lineNum+" "+replaceTabs(content), width))
			i++
		default:
			out = append(out, dimWrap(" "+lineNum+" "+replaceTabs(content), width))
			i++
		}
	}
	return out
}

// diffLineRe parses a generateDiffString output line. Faithful port of
// upstream components/diff.ts parseDiffLine regex /^([+-\s])(\s*\d*)\s(.*)$/:
// group 1 is the prefix, group 2 is the padded line number (leading spaces +
// digits, no trailing space), then a single separator space, then content.
// The `-` is escaped so RE2 does not read `+-\s` as a range.
var diffLineRe = regexp.MustCompile(`^([-+\s])(\s*\d*)\s(.*)$`)

// parseDiffLine extracts the prefix (+/-/space), line number, and content
// from a generateDiffString output line.
func parseDiffLine(line string) (prefix byte, lineNum string, content string, ok bool) {
	m := diffLineRe.FindStringSubmatch(line)
	if m == nil {
		return 0, "", "", false
	}
	return m[1][0], m[2], m[3], true
}

func replaceTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "   ")
}

// renderWriteContent shows written content using upstream write.ts presentation:
// ten logical lines while collapsed, every logical line while expanded, and
// Text-style wrapping for every visual row.
func renderWriteContent(d *tools.WriteDetails, width int, expanded bool) []string {
	body := strings.TrimRight(d.Content, "\n")
	if body == "" {
		return nil
	}
	// Upstream write.ts calls replaceTabs before display.
	body = strings.ReplaceAll(body, "\t", "   ")
	lines := strings.Split(body, "\n")

	// Syntax-highlight when we have a recognized language. Mirrors
	// write.ts:146-149 (`const renderedLines = lang ? ... : split`).
	var styled []string
	if lang := tui.LanguageFromPath(d.Path); lang != "" {
		hl := tui.HighlightCode(body, lang)
		if len(hl) == len(lines) {
			styled = hl
		}
	}

	const writePreviewLines = 10
	totalLines := len(lines)
	maxLines := totalLines
	if !expanded && totalLines > writePreviewLines {
		maxLines = writePreviewLines
	}

	var out []string
	for i := 0; i < maxLines; i++ {
		if styled != nil {
			out = append(out, widthx.WrapTextWithAnsi(styled[i], max(1, width))...)
		} else {
			out = append(out, toolOutputWrap(lines[i], width)...)
		}
	}
	if maxLines < totalLines {
		hint := fmt.Sprintf("... (%d more lines, %d total, ctrl+o to expand)", totalLines-maxLines, totalLines)
		out = append(out, mutedWrap(hint, width)...)
	}
	return out
}

// renderReadLines mirrors read.ts formatReadResult. Collapsed successful reads
// have no body. Expanded reads show every source line without an added gutter
// or summary header, wrap via Text semantics, and append the upstream
// truncation warning when the tool supplied truncation details.
func renderReadLines(d *tools.ReadDetails, content string, width int, expanded bool) []string {
	if !expanded {
		return nil
	}
	content = strings.ReplaceAll(content, "\t", "   ")
	trimmed := strings.TrimRight(content, "\n")
	var lines []string
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}

	var styled []string
	if lang := tui.LanguageFromPath(d.Path); lang != "" && len(lines) > 0 {
		hl := tui.HighlightCode(strings.Join(lines, "\n"), lang)
		if len(hl) == len(lines) {
			styled = hl
		}
	}

	out := make([]string, 0, len(lines)+1)
	for i, line := range lines {
		if styled != nil {
			out = append(out, widthx.WrapTextWithAnsi(styled[i], max(1, width))...)
		} else {
			out = append(out, toolOutputWrap(line, width)...)
		}
	}
	if d.Truncation != nil {
		if warning := tools.FormatTruncationWarning(*d.Truncation); warning != "" {
			out = append(out, warningWrap(warning, width)...)
		}
	}
	return out
}

func renderReadError(content string, width int, expanded bool) []string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	shown := len(lines)
	if !expanded {
		shown = min(shown, 10)
	}
	var out []string
	for _, line := range lines[:shown] {
		out = append(out, toolOutputWrap(line, width)...)
	}
	if shown < len(lines) {
		out = append(out, mutedWrap(fmt.Sprintf("... (%d more lines, ctrl+o to expand)", len(lines)-shown), width)...)
	}
	return out
}

// The wrap helpers preserve complete tool content across rows.
func dimWrap(s string, width int) string {
	return strings.Join(styleWrapRows(s, "\033[2m", tui.SGRBoldDimReset, width), "\n")
}

func mutedWrap(s string, width int) []string {
	th := tui.ActiveTheme()
	color := th.Muted
	if color == "" {
		color = "\033[38;2;128;128;128m"
	}
	return styleWrapRows(s, color, tui.SGRFgReset, width)
}

func warningWrap(s string, width int) []string {
	color := tui.ActiveTheme().Warning
	if color == "" {
		color = "\033[33m"
	}
	return styleWrapRows(s, color, tui.SGRFgReset, width)
}

func styleWrapRows(s, open, close string, width int) []string {
	wrapped := widthx.WrapTextWithAnsi(s, max(1, width))
	out := make([]string, len(wrapped))
	for i, line := range wrapped {
		out[i] = open + line + close
	}
	return out
}

// mutedTrunc applies the theme muted color (gray #808080) and truncates.
// Mirrors upstream `theme.fg("muted", ...)`.
func mutedTrunc(s string, width int) string {
	th := tui.ActiveTheme()
	c := th.Muted
	if c == "" {
		c = "\033[38;2;128;128;128m"
	}
	return styleAndTrunc(s, c, width)
}

// toolOutputTrunc applies the theme toolOutput color and truncates.
// Mirrors upstream `theme.fg("toolOutput", ...)`.
// toolOutputWrap styles a line with the tool-output color and wraps it
// to `width`, returning one or more lines. Mirrors upstream which renders
// bash output inside a Text component (word-wraps via wrapTextWithAnsi).
func toolOutputWrap(s string, width int) []string {
	th := tui.ActiveTheme()
	c := th.ToolOutput
	if c == "" {
		c = "\033[38;2;128;128;128m"
	}
	// Replace tabs before wrapping: tab stops differ across terminals.
	s = strings.ReplaceAll(s, "\t", "   ")
	lines := tui.WrapText(s, width)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = c + l + tui.SGRFgReset
	}
	return out
}

func redWrap(s string, width int) string {
	th := tui.ActiveTheme()
	c := th.ToolDiffRemoved
	if c == "" {
		c = th.Error
	}
	if c == "" {
		c = "\033[31m"
	}
	return styleAndWrap(s, c, width)
}
func greenWrap(s string, width int) string {
	th := tui.ActiveTheme()
	c := th.ToolDiffAdded
	if c == "" {
		c = th.Success
	}
	if c == "" {
		c = "\033[32m"
	}
	return styleAndWrap(s, c, width)
}

func styleAndWrap(s, ansi string, width int) string {
	wrapped := widthx.WrapTextWithAnsi(s, width)
	var out strings.Builder
	for i, line := range wrapped {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(ansi)
		out.WriteString(line)
		out.WriteString("\033[0m")
	}
	return out.String()
}

func styleAndTrunc(s, ansi string, width int) string {
	return styleAndTruncClose(s, ansi, tui.SGRFgReset, width)
}

func styleAndTruncClose(s, ansi, close string, width int) string {
	s = truncToWidth(s, width)
	return ansi + s + close
}

func truncToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if widthx.VisibleWidth(s) <= width {
		return s
	}
	// Plain-text ellipsis truncation measured by widthx (no SGR resets
	// inserted, so the caller's surrounding style also covers the ellipsis).
	return widthx.SliceByColumn(s, 0, width-1, true) + "\u2026"
}

// makePreviewBodyRenderer produces a body renderer that uses an
// extension-provided preview string for the collapsed view and the full
// content for the expanded view. This gives subprocess extensions the
// same collapsed/expanded UX as built-in tools without implementing a
// full in-process BodyRenderer. The preview is shown verbatim (one line
// per \n-separated segment); Ctrl+O reveals the full output.
func makePreviewBodyRenderer(content, preview string, elapsed time.Duration) func(width int, expanded bool) []string {
	return func(width int, expanded bool) []string {
		var out []string
		if expanded {
			output := strings.TrimRight(content, "\n")
			if output != "" {
				for line := range strings.SplitSeq(output, "\n") {
					out = append(out, toolOutputWrap(line, width)...)
				}
			}
		} else {
			// Show the extension-provided preview.
			for line := range strings.SplitSeq(strings.TrimRight(preview, "\n"), "\n") {
				out = append(out, toolOutputWrap(line, width)...)
			}
			// Add expand hint if the full content is longer than the preview.
			contentLines := strings.Count(content, "\n")
			previewLines := strings.Count(preview, "\n")
			if contentLines > previewLines {
				hint := fmt.Sprintf("... (%d more lines, ctrl+o to expand)", contentLines-previewLines)
				out = append(out, mutedTrunc(hint, width))
			}
		}

		if elapsed > 0 {
			label := fmt.Sprintf("Took %.1fs", elapsed.Seconds())
			out = append(out, "")
			out = append(out, mutedTrunc(label, width))
		}

		return out
	}
}
