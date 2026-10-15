package codingagent

import (
	"context"
	"encoding/json"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports .upstream/v1.0.1/packages/coding-agent/test/suite/regressions/10285-mcp-tool-renderers.test.ts: a resumed
// session renders MCP tool calls before their server connected, if it ever does. They render with the MCP renderers
// anyway, instead of the expanded fallback.

func mcpRendererRunner(t *testing.T, extra ...extension.Extension) *inproc.Runner {
	t.Helper()
	mcp, err := factoryload.LoadExtensionFromFactory(mcpext.CreateMcpExtension(mcpext.Options{LoadConfig: func(context.Context) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{} }}), ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "builtin:mcp")
	if err != nil {
		t.Fatal(err)
	}
	return inproc.NewRunner(append(extra, mcp), t.TempDir())
}

func TestRegression10285RendersCallsToMCPToolsThatAreNotRegistered(t *testing.T) {
	tui.SetTheme("dark")
	ownCall := func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
		return tui.NewPaddedText("own", 0, 0, nil)
	}
	runner := mcpRendererRunner(t, extension.Extension{Name: "tools", Tools: map[string]extension.RegisteredTool{
		"read": {Definition: extension.ToolDefinition{Name: "read", RenderCall: ownCall}},
	}})
	resolve := func(toolName string) *extension.ToolRenderers {
		return runner.ResolveToolRenderers(toolName, func() *extension.ToolRenderers {
			definition, ok := runner.GetToolDefinition(toolName)
			if !ok {
				return nil
			}
			return &extension.ToolRenderers{RenderShell: definition.RenderShell, RenderCall: definition.RenderCall, RenderResult: definition.RenderResult}
		})
	}

	renderers := resolve("mcp__my_docs__search")
	if renderers == nil || renderers.RenderCall == nil {
		t.Fatalf("mcp renderers = %+v", renderers)
	}
	call := renderers.RenderCall(json.RawMessage(`{"query":"pi"}`), tui.ActiveTheme(), extension.ToolRenderContext{})
	if got := stripANSITest(strings.Join(call.Render(100), "\n")); !strings.Contains(got, `my_docs/search query="pi"`) {
		t.Fatalf("call = %q", got)
	}
	if got := resolve("not_mcp"); got != nil {
		t.Fatalf("not_mcp renderers = %+v", got)
	}
	// Registered tools keep their own renderers.
	if got := resolve("read"); got == nil || got.RenderCall == nil || strings.TrimSpace(stripANSITest(strings.Join(got.RenderCall(nil, tui.ActiveTheme(), extension.ToolRenderContext{}).Render(100), ""))) != "own" {
		t.Fatalf("read renderers = %+v", got)
	}

	// The interactive card of an unregistered MCP tool draws with them, not the plain card.
	m := &InteractiveMode{newRunner: runner}
	card := newToolCardForTest("mcp__my_docs__search", "")
	m.applyToolPresentation(card, "call-1", "mcp__my_docs__search", json.RawMessage(`{"query":"pi"}`))
	if !card.HasDefinition() {
		t.Fatal("card has no renderers")
	}
	if got := stripANSITest(strings.Join(card.Render(100), "\n")); !strings.Contains(got, `my_docs/search query="pi"`) {
		t.Fatalf("card = %q", got)
	}
}

// D89: a subprocess extension answers a tool renderer resolution after the card was drawn with next()'s renderers;
// the host's notification draws the card again with the answer.
func TestReapplyToolPresentationDrawsCardsWithALaterResolution(t *testing.T) {
	tui.SetTheme("dark")
	var answered atomic.Bool
	resolver := func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		if !answered.Load() {
			return next()
		}
		return &extension.ToolRenderers{RenderCall: func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewPaddedText("answered "+toolName, 0, 0, nil)
		}}
	}
	m := &InteractiveMode{newRunner: inproc.NewRunner([]extension.Extension{{Name: "late", ToolRenderers: []extension.ToolRendererResolver{resolver}}}, t.TempDir())}
	card := newToolCardForTest("late_tool", "")
	m.applyToolPresentation(card, "call-1", "late_tool", json.RawMessage(`{}`))
	if got := stripANSITest(strings.Join(card.Render(80), "\n")); strings.Contains(got, "answered") {
		t.Fatalf("card before the answer = %q", got)
	}
	answered.Store(true)
	m.reapplyToolPresentation("late_tool")
	if got := stripANSITest(strings.Join(card.Render(80), "\n")); !strings.Contains(got, "answered late_tool") {
		t.Fatalf("card after the answer = %q", got)
	}
}

// D89: an extension process may answer that a tool has no renderers after its card was drawn with next()'s. The card
// then draws as a tool without renderers does (upstream hasRendererDefinition false), not with the renderers next()
// returned.
func TestReapplyToolPresentationReturnsACardWithoutRenderersToThePlainCard(t *testing.T) {
	tui.SetTheme("dark")
	var answered atomic.Bool
	resolver := func(_ string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		if answered.Load() {
			return nil
		}
		return next()
	}
	registered := extension.ToolDefinition{Name: "late_tool", RenderCall: func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
		return tui.NewPaddedText("registered renderer", 0, 0, nil)
	}}
	m := &InteractiveMode{newRunner: inproc.NewRunner([]extension.Extension{{
		Name:          "late",
		Tools:         map[string]extension.RegisteredTool{"late_tool": {Definition: registered}},
		ToolRenderers: []extension.ToolRendererResolver{resolver},
	}}, t.TempDir())}
	card := newToolCardForTest("late_tool", "")
	m.applyToolPresentation(card, "call-1", "late_tool", json.RawMessage(`{}`))
	if got := stripANSITest(strings.Join(card.Render(80), "\n")); !strings.Contains(got, "registered renderer") || !card.HasDefinition() {
		t.Fatalf("card before the answer = %q", got)
	}
	answered.Store(true)
	m.reapplyToolPresentation("late_tool")
	if got := stripANSITest(strings.Join(card.Render(80), "\n")); strings.Contains(got, "registered renderer") || card.HasDefinition() {
		t.Fatalf("card after the answer = %q (definition %t)", got, card.HasDefinition())
	}
}

// The records reapplyToolPresentation draws again stay proportional to the live cards: records of collected cards
// are swept instead of accumulating for every tool call of a session.
func TestToolCardRecordsAreSweptWhenTheirCardsAreCollected(t *testing.T) {
	m := &InteractiveMode{}
	for i := range 4 * toolCardSweepMin {
		m.recordToolCard(newToolCardForTest("bash", ""), "call-"+strconv.Itoa(i), "bash")
		if i%toolCardSweepMin == 0 {
			runtime.GC()
		}
	}
	live := newToolCardForTest("bash", "")
	m.recordToolCard(live, "live", "bash")
	runtime.GC()
	for i := range toolCardSweepMin + 1 {
		m.recordToolCard(newToolCardForTest("read", ""), "read-"+strconv.Itoa(i), "read")
	}
	if got := len(m.toolCards["bash"]); got > 2*toolCardSweepMin {
		t.Fatalf("bash records = %d after its cards were collected", got)
	}
	if _, ok := m.toolCards["bash"]["live"]; !ok {
		t.Fatal("the live card's record was swept")
	}
	runtime.KeepAlive(live)
}

// tool-execution.ts:65-73: the card receives the tool's definition in its constructor and draws it from the first render, without a later
// binding step; the definition's renderers read the card they belong to, and a tool with no renderers gets the plain card.
func TestNewToolCardIsBuiltWithTheRegisteredDefinition(t *testing.T) {
	tui.SetTheme("dark")
	resolver := func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		if toolName != "drawn_tool" {
			return next()
		}
		return &extension.ToolRenderers{
			RenderCall: func(args json.RawMessage, _ extension.Theme, ctx extension.ToolRenderContext) extension.Component {
				return tui.NewPaddedText("call "+string(args)+" "+ctx.ToolCallID, 0, 0, nil)
			},
			RenderResult: func(result agent.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, _ extension.ToolRenderContext) extension.Component {
				return tui.NewPaddedText("result "+result.Content[0].(ai.TextContent).Text, 0, 0, nil)
			},
		}
	}
	m := &InteractiveMode{newRunner: inproc.NewRunner([]extension.Extension{{Name: "drawn", ToolRenderers: []extension.ToolRendererResolver{resolver}}}, t.TempDir())}

	card := m.newToolCard("drawn_tool", "call-7", json.RawMessage(`{"q":1}`))
	if !card.HasDefinition() {
		t.Fatal("the card was built without the registered definition")
	}
	card.UpdateResult(ToolExecutionResultOf(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, 0), false)
	got := stripANSITest(strings.Join(card.Render(80), "\n"))
	if !strings.Contains(got, `call {"q":1} call-7`) || !strings.Contains(got, "result done") {
		t.Fatalf("definition not drawn from construction: %q", got)
	}
	if plain := m.newToolCard("unregistered_tool", "call-8", nil); plain.HasDefinition() {
		t.Fatal("a tool without renderers got a definition card")
	}
	if len(m.toolCards["drawn_tool"]) != 1 || len(m.toolCards["unregistered_tool"]) != 1 {
		t.Fatalf("cards not recorded for a later resolution: %v", m.toolCards)
	}
}
