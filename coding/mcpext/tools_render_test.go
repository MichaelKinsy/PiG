package mcpext_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/tui"
)

// The renderers of an MCP tool definition
// (.upstream/v0.99.1/packages/coding-agent/src/extensions/mcp/tools.ts:276-296). Upstream has no unit test for them;
// each case follows one branch of renderCall or renderResult.

func mcpRenderDefinition() extension.ToolDefinition {
	return mcpext.CreateMcpToolDefinition(mcpext.McpToolOptions{Server: "docs", Tool: mcp.Tool{Name: "search"}, Name: "mcp__docs__search"})
}

type renderedText interface{ Render(width int) []string }

func renderRows(t *testing.T, component extension.Component, width int) []string {
	t.Helper()
	rendered, ok := component.(renderedText)
	if !ok {
		t.Fatalf("the renderer returned %T, not a component", component)
	}
	rows := append([]string(nil), rendered.Render(width)...)
	for i, row := range rows {
		rows[i] = strings.TrimRight(stripSGR(row), " ")
	}
	return rows
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// coloredTheme is the built-in dark theme, whose tokens emit escapes, passed to the renderers as the theme parameter.
func coloredTheme(t *testing.T) *tui.Theme {
	t.Helper()
	theme, err := tui.LoadBuiltinTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	return theme.WithColorMode(tui.TerminalColorModeTrueColor)
}

func textResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
}

func numberedLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line-" + string(rune('a'+i))
	}
	return strings.Join(lines, "\n")
}

// tools.ts:277-279: the call shows the label `server/tool`, not the provider-facing name, with formatToolCallWithArgs.
func TestMCPToolCallRendererTitlesTheCallServerSlashTool(t *testing.T) {
	def := mcpRenderDefinition()
	if def.RenderCall == nil {
		t.Fatal("an MCP tool definition has no renderCall")
	}
	theme := tui.ActiveTheme()
	args := json.RawMessage(`{"query":"pi","limit":3}`)
	collapsed := def.RenderCall(args, theme, extension.ToolRenderContext{})
	if got := strings.Join(renderRows(t, collapsed, 200), "\n"); got != `docs/search query="pi" limit=3` {
		t.Fatalf("collapsed call = %q", got)
	}
	expanded := def.RenderCall(args, theme, extension.ToolRenderContext{Expanded: true})
	if got := strings.Join(renderRows(t, expanded, 200), "\n"); got != "docs/search\n  query: pi\n  limit: 3" {
		t.Fatalf("expanded call = %q", got)
	}
	if got := strings.Join(renderRows(t, def.RenderCall(nil, theme, extension.ToolRenderContext{}), 200), "\n"); got != "docs/search" {
		t.Fatalf("call without arguments = %q", got)
	}
}

// tools.ts:277: the call reuses `context.lastComponent` and sets its text.
func TestMCPToolCallRendererReusesTheLastComponent(t *testing.T) {
	def := mcpRenderDefinition()
	if def.RenderCall == nil {
		t.Fatal("an MCP tool definition has no renderCall")
	}
	theme := tui.ActiveTheme()
	first := def.RenderCall(json.RawMessage(`{"a":1}`), theme, extension.ToolRenderContext{})
	second := def.RenderCall(json.RawMessage(`{"a":2}`), theme, extension.ToolRenderContext{LastComponent: first})
	if first != second {
		t.Fatalf("the renderer built a new component: %p, %p", first, second)
	}
	if got := strings.Join(renderRows(t, second, 200), "\n"); got != "docs/search a=2" {
		t.Fatalf("reused component shows %q", got)
	}
}

// tools.ts:281-295 with OUTPUT_PREVIEW_LINES = 5 (tools.ts:52): collapsed results show five lines and a hint for the rest.
func TestMCPToolResultRendererCollapsesAfterFiveLines(t *testing.T) {
	def := mcpRenderDefinition()
	if def.RenderResult == nil {
		t.Fatal("an MCP tool definition has no renderResult")
	}
	theme := tui.ActiveTheme()
	text := numberedLines(12)
	collapsed := def.RenderResult(textResult(text), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{})
	want := "\nline-a\nline-b\nline-c\nline-d\nline-e\n... (7 more lines, ctrl+o to expand)"
	if got := strings.Join(renderRows(t, collapsed, 200), "\n"); got != want {
		t.Fatalf("collapsed result = %q, want %q", got, want)
	}
	expanded := def.RenderResult(textResult(text), extension.ToolRenderResultOptions{Expanded: true}, theme, extension.ToolRenderContext{Expanded: true})
	if got := strings.Join(renderRows(t, expanded, 200), "\n"); got != "\n"+text {
		t.Fatalf("expanded result = %q", got)
	}
	exact := def.RenderResult(textResult(numberedLines(5)), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{})
	if got := strings.Join(renderRows(t, exact, 200), "\n"); got != "\n"+numberedLines(5) {
		t.Fatalf("five lines = %q", got)
	}
	one := def.RenderResult(textResult(numberedLines(6)), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{})
	if got := strings.Join(renderRows(t, one, 200), "\n"); !strings.HasSuffix(got, "... (1 more lines, ctrl+o to expand)") {
		t.Fatalf("six lines = %q", got)
	}
}

// tools.ts:282-291: the output is trimmed, tabs become three spaces, the color is toolOutput or error, and empty output renders nothing.
func TestMCPToolResultRendererStylesAndTrimsTheOutput(t *testing.T) {
	def := mcpRenderDefinition()
	if def.RenderResult == nil {
		t.Fatal("an MCP tool definition has no renderResult")
	}
	theme := coloredTheme(t)
	ok := def.RenderResult(textResult("  \n\tindented\ttext\n\n"), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{})
	rows := strings.Join(renderRows(t, ok, 200), "\n")
	if rows != "\nindented   text" {
		t.Fatalf("trimmed result = %q", rows)
	}
	joined := strings.Join(ok.(renderedText).Render(200), "\n")
	if theme.Fg("toolOutput", "x") == "x" {
		t.Fatal("the test theme has no colors")
	}
	if !strings.Contains(joined, theme.Fg("toolOutput", "indented   text")) {
		t.Fatalf("result is not toolOutput: %q", joined)
	}
	failed := def.RenderResult(textResult("boom"), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{IsError: true})
	if joined := strings.Join(failed.(renderedText).Render(200), "\n"); !strings.Contains(joined, theme.Fg("error", "boom")) {
		t.Fatalf("error result is not error colored: %q", joined)
	}
	empty := def.RenderResult(textResult(" \n "), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{})
	if rows := renderRows(t, empty, 200); len(rows) != 0 {
		t.Fatalf("empty output rendered %q", rows)
	}
	first := def.RenderResult(textResult("one"), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{})
	second := def.RenderResult(textResult("two"), extension.ToolRenderResultOptions{}, theme, extension.ToolRenderContext{LastComponent: first})
	if first != second {
		t.Fatal("the result renderer built a new component instead of reusing lastComponent")
	}
}

// tools.ts:282 getTextOutput(result, context.showImages): image blocks that are not shown become their text fallback (render-utils.ts:44-56).
func TestMCPToolResultRendererNamesImagesItDoesNotShow(t *testing.T) {
	def := mcpRenderDefinition()
	if def.RenderResult == nil {
		t.Fatal("an MCP tool definition has no renderResult")
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "caption"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}}}
	component := def.RenderResult(result, extension.ToolRenderResultOptions{}, tui.ActiveTheme(), extension.ToolRenderContext{ShowImages: false})
	rows := strings.Join(renderRows(t, component, 200), "\n")
	if !strings.Contains(rows, "caption\n[Image: [image/png]") {
		t.Fatalf("result = %q", rows)
	}
}

// tools.ts:282 trims the output with String.prototype.trim, which removes U+FEFF and keeps U+0085.
func TestMCPToolResultRendererTrimsJavaScriptWhitespace(t *testing.T) {
	def := mcpRenderDefinition()
	component := def.RenderResult(textResult("\uFEFF result \u0085"), extension.ToolRenderResultOptions{}, tui.ActiveTheme(), extension.ToolRenderContext{})
	if got := strings.Join(renderRows(t, component, 200), "\n"); got != "\nresult \u0085" {
		t.Fatalf("trimmed result = %q, want %q", got, "\nresult \u0085")
	}
}

// tools.ts renderResult (0.99.2): the collapsed preview is limited to wrapped lines, like bash output, and names the
// file with the full output. A single long line, such as minified JSON, must not fill the screen. Upstream has no unit
// test for this renderer; codemode-renderer.test.ts "limits collapsed output to wrapped lines" has the same inputs.
func TestMCPToolResultRendererLimitsCollapsedOutputToWrappedLinesNotLogicalLines(t *testing.T) {
	def := mcpRenderDefinition()
	result := textResult(strings.Repeat("x", 1000))
	result.Details = mcpext.McpToolDetails{Server: "docs", Tool: "search", FullOutputPath: "/tmp/out.txt"}
	collapsed := def.RenderResult(result, extension.ToolRenderResultOptions{}, tui.ActiveTheme(), extension.ToolRenderContext{})
	rows := renderRows(t, collapsed, 50)
	if len(rows) != 8 || rows[0] != "" {
		t.Fatalf("got %d rows, want a spacer, five wrapped lines, the hint and the path: %q", len(rows), rows)
	}
	for i, row := range rows[1:6] {
		if row != strings.Repeat("x", 50) {
			t.Errorf("rows[%d] = %q", i+1, row)
		}
	}
	if rows[6] != "... (15 more lines, ctrl+o to expand)" || rows[7] != "Full output: /tmp/out.txt" {
		t.Errorf("tail = %q", rows[6:])
	}
	// Expanded results show everything and no path line.
	expanded := def.RenderResult(result, extension.ToolRenderResultOptions{Expanded: true}, tui.ActiveTheme(), extension.ToolRenderContext{Expanded: true})
	if rows := renderRows(t, expanded, 50); len(rows) != 21 || strings.Contains(strings.Join(rows, "\n"), "Full output") {
		t.Errorf("expanded rows = %q", rows)
	}
}
