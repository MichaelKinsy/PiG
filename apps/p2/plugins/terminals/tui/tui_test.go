package tui_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi has no test of terminals/tui.ts; these hold its card to what the file says.

type keys struct{ theme *theme.ThemeRef }

func (k keys) Theme() *theme.ThemeRef { return k.theme }
func (keys) KeyText(id string) string { return "ctrl+o" }
func (keys) RequestRender()           {}

func newContext(t *testing.T, isError bool) (*theme.Theme, *tools.ToolRenderContext) {
	t.Helper()
	dark, _ := theme.BuiltinTheme("dark")
	built, err := theme.CreateTheme(dark, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	var ui render.RenderContext = keys{theme.NewThemeRef(built)}
	return built, &tools.ToolRenderContext{UI: ui, IsError: isError, State: map[string]any{}}
}

func plain(component pitui.Component) string {
	var rows []string
	for _, line := range component.Render(100) {
		rows = append(rows, strings.TrimRight(widthx.StripTerminalSequences(line), " "))
	}
	return strings.Join(rows, "\n")
}

func TestTheCallShowsTheTmuxCommandLine(t *testing.T) {
	built, context := newContext(t, false)
	args := jsvalue.Obj("command", "send-keys", "args", []any{"-t", "dev", "echo\nhi", float64(3)})
	got := plain(tui.Renderers.RenderCall(args, built, context))
	if !strings.Contains(got, "terminal send-keys -t dev echo hi ") {
		t.Fatalf("call = %q", got)
	}
	if got := plain(tui.Renderers.RenderCall(jsvalue.Obj(), built, context)); !strings.Contains(got, "terminal ...") {
		t.Fatalf("empty call = %q", got)
	}
}

func TestTheResultShowsTheLastLinesAndExpandsToAll(t *testing.T) {
	built, context := newContext(t, false)
	lines := make([]string, 12)
	for index := range lines {
		lines[index] = fmt.Sprintf("line-%02d", index+1)
	}
	result := tools.ToolResultView{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Join(lines, "\n")}}}
	collapsed := plain(tui.Renderers.RenderResult(result, tools.ToolRenderResultOptions{}, built, context))
	if !strings.Contains(collapsed, "... (4 earlier lines, ctrl+o to expand)") || strings.Contains(collapsed, "line-04") ||
		!strings.Contains(collapsed, "line-05") || !strings.Contains(collapsed, "line-12") {
		t.Fatalf("collapsed = %q", collapsed)
	}
	expanded := plain(tui.Renderers.RenderResult(result, tools.ToolRenderResultOptions{Expanded: true}, built, context))
	if strings.Contains(expanded, "earlier lines") || !strings.Contains(expanded, "line-01") || !strings.Contains(expanded, "line-12") {
		t.Fatalf("expanded = %q", expanded)
	}
}
