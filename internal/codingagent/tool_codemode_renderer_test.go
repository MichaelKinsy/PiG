package codingagent

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// renderCodemodeCard draws a codemode card through the interactive event path for a registered `codemode` definition.
func renderCodemodeCard(t *testing.T, definition extension.ToolDefinition, args string, result agent.AgentToolResult) string {
	t.Helper()
	definition.Name = "codemode"
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{{Name: "builtin:codemode", Tools: map[string]extension.RegisteredTool{"codemode": {Definition: definition}}}}, ""),
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		opts:          InteractiveModeOptions{CWD: t.TempDir()},
	}
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "c", ToolName: "codemode", Args: json.RawMessage(args)})
	card := m.toolByID["c"]
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "codemode", Result: result})
	return codemodeANSI.ReplaceAllString(strings.Join(card.Render(80), "\n"), "")
}

var codemodeCardResult = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{headerBlock(), ai.TextContent{Text: "hello"}}}

// The codemode definition carries its renderers (upstream tool.ts spreads codemodeRenderers into the definition), so
// the card hides the "Script completed" header.
func TestCodemodeCardUsesTheDefinitionsRenderers(t *testing.T) {
	definition := extension.ToolDefinition{RenderCall: CodemodeRenderers.RenderCall, RenderResult: CodemodeRenderers.RenderResult}
	got := renderCodemodeCard(t, definition, `{"code":"return 1"}`, codemodeCardResult)
	if strings.Contains(got, "Script completed") || !strings.Contains(got, "hello") {
		t.Errorf("card = %q, want the output without the script header", got)
	}
}

// Upstream createAllToolRenderers has no codemode entry, so another extension's `codemode` tool without renderers draws
// the default card, header included.
func TestAnotherCodemodeToolGetsNoBuiltInRenderers(t *testing.T) {
	if call, result := builtInToolRenderers("codemode"); call != nil || result != nil {
		t.Fatal("codemode has built-in renderers keyed by name")
	}
	got := renderCodemodeCard(t, extension.ToolDefinition{}, `{"code":"return 1"}`, codemodeCardResult)
	if !strings.Contains(got, "Script completed") {
		t.Errorf("card = %q, want the default rendering", got)
	}
}

// renderer.ts formatCall cuts collapsed arguments at 80 UTF-16 units (call.args.slice(0, 77)); astral characters count
// two units each, so 41 emoji are 82 units but 41 runes.
func TestCodemodeRendererCutsArgumentsInUTF16Units(t *testing.T) {
	args := strings.Repeat("😀", 41)
	got := renderCodemodeCollapsed(t, agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{headerBlock()},
		Details: map[string]any{"calls": []any{map[string]any{"name": "echo", "args": args, "status": "ok"}}},
	})
	// 77 units: 38 emoji and the high surrogate of the 39th, which a Go string holds as its WTF-8 encoding.
	want := "✓ echo " + strings.Repeat("😀", 38) + "\xed\xa0\xbd..."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// getTextOutput (core/tools/render-utils.ts) strips ANSI escapes, sanitizes binary output and drops carriage returns from
// every text block, then appends one fallback line per image after all the text.
func TestCodemodeRendererOutputIsSanitizedAndImagesFollowTheText(t *testing.T) {
	got := renderCodemodeCollapsed(t, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{
		headerBlock(),
		ai.TextContent{Text: "\x1b[31mred\x1b[0m\r"},
		ai.ImageContent{Data: "AAAA", MimeType: "image/png"},
		ai.TextContent{Text: "after"},
	}})
	if want := "red\nafter\n[Image: [image/png]]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// renderCall shows `[invalid arg]` only when args.code is present with a type other than string (str() returns "" for
// undefined and null).
func TestCodemodeRenderCallFlagsOnlyANonStringCode(t *testing.T) {
	render := func(args string) string {
		component := codemodeRenderCall(json.RawMessage(args), nil, extension.ToolRenderContext{})
		return codemodeANSI.ReplaceAllString(strings.Join(component.(interface{ Render(int) []string }).Render(80), "\n"), "")
	}
	for _, args := range []string{`{}`, `{"code":null}`, `{"other":1}`, `null`, `{"code":""}`} {
		if got := strings.TrimSpace(render(args)); got != "codemode" {
			t.Errorf("args %s: %q, want the bare title", args, got)
		}
	}
	if got := strings.TrimSpace(render(`{"code":5}`)); got != "codemode [invalid arg]" {
		t.Errorf("numeric code: %q", got)
	}
}

// The expand hints name the key bound to app.tools.expand (upstream keyHint), not a fixed ctrl+o.
func TestCodemodeRendererExpandHintNamesTheBoundKey(t *testing.T) {
	tui.SetAppKeyTextResolver(func(action string) string {
		if action == "app.tools.expand" {
			return "f9"
		}
		return ""
	})
	t.Cleanup(func() { tui.SetAppKeyTextResolver(nil) })
	got := renderCodemodeCollapsed(t, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{headerBlock(), ai.TextContent{Text: "1\n2\n3\n4\n5\n6"}}})
	if !strings.Contains(got, "... (1 more lines, f9 to expand)") {
		t.Errorf("got %q", got)
	}
}
