package mcpext

// Ports the renderers of packages/coding-agent/src/extensions/mcp/tools.ts (renderCall, renderResult).

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// outputPreviewLines is OUTPUT_PREVIEW_LINES: the visual (wrapped) result lines shown before the output is expanded.
// upstream: packages/coding-agent/src/extensions/mcp/tools.ts:52
const outputPreviewLines = 5

// themeOf is the theme a renderer draws with; the interactive mode passes its active theme.
func themeOf(theme extension.Theme) *tui.Theme {
	if t, ok := theme.(*tui.Theme); ok && t != nil {
		return t
	}
	return tui.ActiveTheme()
}

// reusableText is the card's reusable Text: the last component of the renderer, or a new empty one.
func reusableText(last extension.Component) *tui.Text {
	if text, ok := last.(*tui.Text); ok && text != nil {
		return text
	}
	return tui.NewPaddedText("", 0, 0, nil)
}

// renderCall shows the call as `server/tool` and its arguments.
// upstream: packages/coding-agent/src/extensions/mcp/tools.ts:276-280
func renderCall(label string) extension.ToolRenderCallFunc {
	return func(args json.RawMessage, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
		component := reusableText(context.LastComponent)
		component.SetText(tui.FormatToolCallWithArgs(label, args, themeOf(theme), context.Expanded))
		return component
	}
}

// renderResult shows the output in the toolOutput or error color: all of it expanded, else a preview limited to five wrapped lines with the path of the full output.
// upstream: packages/coding-agent/src/extensions/mcp/tools.ts:283-311
func renderResult(result extension.AgentToolResult, options extension.ToolRenderResultOptions, theme extension.Theme, context extension.ToolRenderContext) extension.Component {
	th := themeOf(theme)
	var value agent.AgentToolResult
	switch r := result.(type) {
	case agent.AgentToolResult:
		value = r
	case *agent.AgentToolResult:
		if r != nil {
			value = *r
		}
	}
	component, _ := context.LastComponent.(*tui.Container)
	if component == nil {
		component = tui.NewContainer()
	}
	component.Clear()
	output := strings.TrimFunc(tools.GetTextOutput(value.Content, context.ShowImages), isJSWhitespace)
	if output == "" {
		return component
	}
	color := "toolOutput"
	if context.IsError {
		color = "error"
	}
	lines := strings.Split(strings.ReplaceAll(output, "\t", "   "), "\n")
	for i, line := range lines {
		lines[i] = th.FgText(color, line)
	}
	styled := strings.Join(lines, "\n")
	component.Add(tui.NewSpacer(1))
	if options.Expanded {
		component.Add(tui.NewPaddedText(styled, 0, 0, nil))
		return component
	}
	// Limit wrapped lines, not logical ones: MCP results are often one long JSON line.
	component.Add(tui.NewVisualLinePreview(tui.VisualLinePreviewOptions{
		Text:           styled,
		MaxVisualLines: outputPreviewLines,
		Keep:           tui.VisualKeepStart,
		FormatHint: func(hidden int) string {
			return th.FgText("muted", fmt.Sprintf("... (%d more lines,", hidden)) + " " +
				tui.KeyHint(tui.AppKeyText("app.tools.expand", "ctrl+o"), "to expand") + th.FgText("muted", ")")
		},
	}))
	if path := fullOutputPathOf(value.Details); path != "" {
		component.Add(tui.NewPaddedText(th.FgText("muted", "Full output: "+path), 0, 0, nil))
	}
	return component
}

// fullOutputPathOf is `result.details?.fullOutputPath`: the details of a live result are an [McpToolDetails]; those of a reloaded session are the decoded JSON.
func fullOutputPathOf(details any) string {
	switch d := details.(type) {
	case McpToolDetails:
		return d.FullOutputPath
	case *McpToolDetails:
		if d != nil {
			return d.FullOutputPath
		}
	case map[string]any:
		path, _ := d["fullOutputPath"].(string)
		return path
	}
	return ""
}

// isJSWhitespace reports whether r is JavaScript WhiteSpace or LineTerminator, the set String.prototype.trim removes. It
// differs from unicode.IsSpace in U+FEFF (JS whitespace) and U+0085 (not JS whitespace).
func isJSWhitespace(r rune) bool {
	switch r {
	case '\uFEFF':
		return true
	case '\u0085':
		return false
	}
	return unicode.IsSpace(r)
}
