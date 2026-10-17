package codingagent

// tool_render_shell.go: result-side presentation for the built-in shell tools.
//
// Ports upstream packages/coding-agent/src/core/tools/renderers/bash.ts
// rebuildBashResultRenderComponent, shared by bash and powershell through
// createShellRenderers. The call header and duration formatting live in
// tui/shell_renderers.go.

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/text"
	"github.com/MichaelKinsy/PiG/tui"
)

// bashPreviewLines mirrors upstream renderers/bash.ts BASH_PREVIEW_LINES.
const bashPreviewLines = 5

// shellResultDetails is the part of BashToolDetails the renderer reads.
type shellResultDetails struct {
	Truncation     *tools.TruncationResult
	FullOutputPath string
}

// shellDetailsFrom reads BashToolDetails from a live result (*tools.BashDetails)
// or from a persisted or extension-supplied result, whose details arrive as the
// decoded upstream JSON shape ({truncation, fullOutputPath}).
func shellDetailsFrom(details any) shellResultDetails {
	switch d := detailsObject(details).(type) {
	case *tools.BashDetails:
		if d == nil {
			return shellResultDetails{}
		}
		return shellResultDetails{Truncation: d.Truncation, FullOutputPath: d.FullOutputPath}
	case map[string]any:
		out := shellResultDetails{}
		out.FullOutputPath, _ = d["fullOutputPath"].(string)
		if raw, ok := d["truncation"].(map[string]any); ok {
			out.Truncation = truncationFromMap(raw)
		}
		return out
	}
	return shellResultDetails{}
}

// truncationFromMap reads the truncation fields the renderer shows from a decoded truncation object by their exact keys, as
// bash.ts reads truncation.truncated, truncation.truncatedBy, truncation.outputLines, truncation.totalLines and
// truncation.maxBytes: a key that matches a field only case-insensitively is another property. A field of the wrong type,
// or a count that is not an integer that fits an int, reads as no truncation object.
func truncationFromMap(raw map[string]any) *tools.TruncationResult {
	out := &tools.TruncationResult{}
	if value, present := raw["truncated"]; present && value != nil {
		truncated, ok := value.(bool)
		if !ok {
			return nil
		}
		out.Truncated = truncated
	}
	if value, present := raw["truncatedBy"]; present && value != nil {
		by, ok := value.(string)
		if !ok {
			return nil
		}
		out.TruncatedBy = by
	}
	for _, field := range [...]struct {
		key    string
		target *int
	}{{"totalLines", &out.TotalLines}, {"outputLines", &out.OutputLines}, {"maxBytes", &out.MaxBytes}} {
		value, present := raw[field.key]
		if !present || value == nil {
			continue
		}
		count, ok := truncationCount(value)
		if !ok {
			return nil
		}
		*field.target = count
	}
	return out
}

// truncationCount reads a count member: a decoded JSON number (float64, or json.Number from a UseNumber decode) or a Go
// number an in-process extension put in its details, when it is an integer that fits an int.
func truncationCount(value any) (int, bool) {
	var number float64
	switch v := value.(type) {
	case float64:
		number = v
	case float32:
		number = float64(v)
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		return int(v), v <= math.MaxInt
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		return int(v), true
	case uint64:
		return int(v), v <= math.MaxInt
	default:
		return 0, false
	}
	if number != math.Trunc(number) || number < math.MinInt || number >= -math.MinInt {
		return 0, false
	}
	//portlint:allow numbers the guard above admits only integral values within int range, so the conversion is exact; NaN fails `number != math.Trunc(number)`
	return int(number), true
}

// makeShellBodyRenderer produces the body of a bash or powershell tool card.
// It mirrors upstream rebuildBashResultRenderComponent: the trimmed output
// (minus the full-output footer the tool appends once it finishes), shown in
// full when expanded or as the last BASH_PREVIEW_LINES visual lines behind an
// "earlier lines" hint when collapsed; then the truncation warning; then the
// "Took" footer for a finished run that recorded its start (took non-nil, even
// when the clock measured 0), as upstream shows it whenever startedAt is set.
// isPartial marks a live update: the component draws the running "Elapsed"
// footer itself.
//
// Upstream's children each begin with a blank line; the tool card draws the
// first one as its separator row, so the returned lines omit it.
func makeShellBodyRenderer(content string, details any, isPartial bool, took *time.Duration) func(width int, expanded bool) []string {
	d := shellDetailsFrom(details)
	return func(width int, expanded bool) []string {
		lines := shellResultLines(content, d, isPartial, expanded, width)
		if !isPartial && took != nil {
			footer := themeFg(tui.ActiveTheme().Muted, "Took "+tui.FormatToolDuration(*took))
			lines = append(lines, tui.NewPaddedText("\n"+footer, 0, 0, nil).Render(width)...)
		}
		if len(lines) > 0 {
			lines = lines[1:]
		}
		return lines
	}
}

func shellResultLines(content string, d shellResultDetails, isPartial, expanded bool, width int) []string {
	return newShellResultView(content, d, isPartial, expanded).render(width)
}

// shellResultView is the children upstream rebuildBashResultRenderComponent builds for one update of a shell card: the
// output (a Text when expanded, a VisualLinePreview when collapsed) and the warning Text. Built once per update, they keep
// their rows per width, so drawing the card again costs no pass over the output.
type shellResultView struct {
	output  tui.Component
	preview bool
	warning *tui.Text
}

func newShellResultView(content string, d shellResultDetails, isPartial, expanded bool) shellResultView {
	theme := tui.ActiveTheme()
	output := jsTrim(shellTextOutput(content))
	truncated := d.Truncation != nil && d.Truncation.Truncated
	if !isPartial && truncated && d.FullOutputPath != "" && strings.HasSuffix(output, "]") {
		if footerStart := strings.LastIndex(output, "\n\n["); footerStart != -1 && strings.Contains(output[footerStart:], d.FullOutputPath) {
			output = jsTrimEnd(output[:footerStart])
		}
	}

	var view shellResultView
	if output != "" {
		styled := strings.Split(output, "\n")
		for i, line := range styled {
			styled[i] = themeFg(theme.ToolOutput, line)
		}
		styledOutput := strings.Join(styled, "\n")
		if expanded {
			view.output = tui.NewPaddedText("\n"+styledOutput, 0, 0, nil)
		} else {
			view.preview = true
			view.output = tui.NewVisualLinePreview(tui.VisualLinePreviewOptions{
				Text:           styledOutput,
				MaxVisualLines: bashPreviewLines,
				Keep:           tui.VisualKeepEnd,
				FormatHint: func(hidden int) string {
					return themeFg(theme.Muted, "... ("+strconv.Itoa(hidden)+" earlier lines,") + " " + expandKeyHint() + themeFg(theme.Muted, ")")
				},
			})
		}
	}

	if truncated || d.FullOutputPath != "" {
		var warnings []string
		if d.FullOutputPath != "" {
			warnings = append(warnings, "Full output: "+d.FullOutputPath)
		}
		if truncated {
			tr := d.Truncation
			if tr.TruncatedBy == "lines" {
				warnings = append(warnings, "Truncated: showing "+strconv.Itoa(tr.OutputLines)+" of "+strconv.Itoa(tr.TotalLines)+" lines")
			} else {
				maxBytes := tr.MaxBytes
				if maxBytes == 0 {
					maxBytes = tools.DefaultMaxBytesUpstream
				}
				warnings = append(warnings, "Truncated: "+strconv.Itoa(tr.OutputLines)+" lines shown ("+tools.FormatSize(maxBytes)+" limit)")
			}
		}
		view.warning = tui.NewPaddedText("\n"+themeFg(theme.Warning, "["+strings.Join(warnings, ". ")+"]"), 0, 0, nil)
	}
	return view
}

// render is the view's rows for width.
func (view shellResultView) render(width int) []string {
	var lines []string
	if view.output != nil {
		if view.preview {
			lines = append(lines, "")
		}
		lines = append(lines, view.output.Render(width)...)
	}
	if view.warning != nil {
		lines = append(lines, view.warning.Render(width)...)
	}
	return lines
}

// Ports packages/coding-agent/src/core/tools/render-utils.ts (getTextOutput: shellTextOutput, toolResultTextOutput).

// shellTextOutput mirrors upstream render-utils getTextOutput for a text
// result: strip ANSI, sanitize binary output, and drop carriage returns.
func shellTextOutput(content string) string {
	return strings.ReplaceAll(tools.SanitizeBinaryOutput(text.StripANSIString(content)), "\r", "")
}

// toolResultTextOutput is upstream render-utils getTextOutput for a tool result.
func toolResultTextOutput(result agent.AgentToolResult, showImages bool) string {
	return tools.GetTextOutput(result.Content, showImages)
}

// expandKeyHint mirrors upstream keyHint("app.tools.expand", "to expand").
func expandKeyHint() string {
	theme := tui.ActiveTheme()
	return themeFg(theme.Dim, tui.AppKeyText("app.tools.expand", "ctrl+o")) + themeFg(theme.Muted, " to expand")
}

// themeFg mirrors upstream theme.fg: color, text, then the foreground reset.
func themeFg(color, text string) string {
	if color == "" {
		return text
	}
	return color + text + tui.FgClose(color)
}

// isJSWhitespace reports whether r is JavaScript WhiteSpace or LineTerminator,
// the set String.prototype.trim removes. It differs from unicode.IsSpace in
// U+FEFF (JS whitespace) and U+0085 (not JS whitespace).
func isJSWhitespace(r rune) bool {
	switch r {
	case '\uFEFF':
		return true
	case '\u0085':
		return false
	}
	return unicode.IsSpace(r)
}

func jsTrim(s string) string    { return strings.TrimFunc(s, isJSWhitespace) }
func jsTrimEnd(s string) string { return strings.TrimRightFunc(s, isJSWhitespace) }
