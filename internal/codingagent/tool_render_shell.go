package codingagent

// tool_render_shell.go: result-side presentation for the built-in shell tools.
//
// Ports upstream packages/coding-agent/src/core/tools/renderers/bash.ts
// rebuildBashResultRenderComponent, shared by bash and powershell through
// createShellRenderers. The call header and duration formatting live in
// tui/shell_renderers.go.

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
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
	switch d := details.(type) {
	case *tools.BashDetails:
		if d == nil {
			return shellResultDetails{}
		}
		return shellResultDetails{Truncation: d.Truncation, FullOutputPath: d.FullOutputPath}
	case map[string]any:
		out := shellResultDetails{}
		out.FullOutputPath, _ = d["fullOutputPath"].(string)
		if raw, ok := d["truncation"].(map[string]any); ok {
			var wire struct {
				Truncated   bool   `json:"truncated"`
				TruncatedBy string `json:"truncatedBy"`
				TotalLines  int    `json:"totalLines"`
				OutputLines int    `json:"outputLines"`
				MaxBytes    int    `json:"maxBytes"`
			}
			if data, err := json.Marshal(raw); err == nil && json.Unmarshal(data, &wire) == nil {
				out.Truncation = &tools.TruncationResult{
					Truncated:   wire.Truncated,
					TruncatedBy: wire.TruncatedBy,
					TotalLines:  wire.TotalLines,
					OutputLines: wire.OutputLines,
					MaxBytes:    wire.MaxBytes,
				}
			}
		}
		return out
	}
	return shellResultDetails{}
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
	theme := tui.ActiveTheme()
	output := jsTrim(shellTextOutput(content))
	truncated := d.Truncation != nil && d.Truncation.Truncated
	if !isPartial && truncated && d.FullOutputPath != "" && strings.HasSuffix(output, "]") {
		if footerStart := strings.LastIndex(output, "\n\n["); footerStart != -1 && strings.Contains(output[footerStart:], d.FullOutputPath) {
			output = jsTrimEnd(output[:footerStart])
		}
	}

	var lines []string
	if output != "" {
		styled := strings.Split(output, "\n")
		for i, line := range styled {
			styled[i] = themeFg(theme.ToolOutput, line)
		}
		styledOutput := strings.Join(styled, "\n")
		if expanded {
			lines = append(lines, tui.NewPaddedText("\n"+styledOutput, 0, 0, nil).Render(width)...)
		} else {
			preview := tui.TruncateToVisualLines(styledOutput, bashPreviewLines, width)
			lines = append(lines, "")
			if preview.SkippedCount > 0 {
				hint := themeFg(theme.Muted, "... ("+strconv.Itoa(preview.SkippedCount)+" earlier lines,") +
					" " + expandKeyHint() + themeFg(theme.Muted, ")")
				lines = append(lines, widthx.TruncateToWidth(hint, width, "...", false))
			}
			lines = append(lines, preview.VisualLines...)
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
		warning := themeFg(theme.Warning, "["+strings.Join(warnings, ". ")+"]")
		lines = append(lines, tui.NewPaddedText("\n"+warning, 0, 0, nil).Render(width)...)
	}
	return lines
}

// shellTextOutput mirrors upstream render-utils getTextOutput for a text
// result: strip ANSI, sanitize binary output, and drop carriage returns.
func shellTextOutput(content string) string {
	text := tools.SanitizeBinaryOutput(string(tools.StripANSI([]byte(content))))
	return strings.ReplaceAll(text, "\r", "")
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
	return color + text + tui.SGRFgReset
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
