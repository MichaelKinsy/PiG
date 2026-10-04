package codingagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

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
	mcp, err := mcpext.NewBuiltin(mcpext.Options{LoadConfig: func(mcpext.EventContext) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{} }})
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
	if got := stripANSITest(strings.Join(call.(tui.Component).Render(100), "\n")); !strings.Contains(got, `my_docs/search query="pi"`) {
		t.Fatalf("call = %q", got)
	}
	if got := resolve("not_mcp"); got != nil {
		t.Fatalf("not_mcp renderers = %+v", got)
	}
	// Registered tools keep their own renderers.
	if got := resolve("read"); got == nil || got.RenderCall == nil || strings.TrimSpace(stripANSITest(strings.Join(got.RenderCall(nil, tui.ActiveTheme(), extension.ToolRenderContext{}).(tui.Component).Render(100), ""))) != "own" {
		t.Fatalf("read renderers = %+v", got)
	}

	// The interactive card of an unregistered MCP tool draws with them, not the plain card.
	m := &InteractiveMode{newRunner: runner}
	card := tui.NewToolExecutionComponent("mcp__my_docs__search", "")
	m.applyToolPresentation(card, "call-1", "mcp__my_docs__search", json.RawMessage(`{"query":"pi"}`))
	if !card.HasDefinition() {
		t.Fatal("card has no renderers")
	}
	if got := stripANSITest(strings.Join(card.Render(100), "\n")); !strings.Contains(got, `my_docs/search query="pi"`) {
		t.Fatalf("card = %q", got)
	}
}

func TestRegression10285RendersThemInHTMLExportsToo(t *testing.T) {
	tui.SetTheme("dark")
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-10-03T12:00:00Z","cwd":"` + filepath.ToSlash(dir) + `"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-03T12:00:01Z","message":{"role":"user","content":"search","timestamp":1}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-03T12:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"mcp__my_docs__search","arguments":{"query":"pi"}}],"api":"anthropic-messages","provider":"anthropic","model":"test","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2}}
`
	if err := os.WriteFile(sessionPath, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ExportSessionToHTML(sessionPath, filepath.Join(dir, "export.html"), ExportToolRenderers(mcpRendererRunner(t)), dir, ShareState{})
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`<script id="session-data" type="application/json">([^<]*)</script>`).FindSubmatch(html)
	if match == nil {
		t.Fatal("session-data script not found")
	}
	data, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		RenderedTools map[string]struct {
			CallHTML string `json:"callHtml"`
		} `json:"renderedTools"`
	}
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	if got := stripANSITest(session.RenderedTools["call-1"].CallHTML); !strings.Contains(got, "my_docs/search") {
		t.Fatalf("callHtml = %q", got)
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
	card := tui.NewToolExecutionComponent("late_tool", "")
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
	card := tui.NewToolExecutionComponent("late_tool", "")
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
		m.recordToolCard(tui.NewToolExecutionComponent("bash", ""), "call-"+strconv.Itoa(i), "bash")
		if i%toolCardSweepMin == 0 {
			runtime.GC()
		}
	}
	live := tui.NewToolExecutionComponent("bash", "")
	m.recordToolCard(live, "live", "bash")
	runtime.GC()
	for i := range toolCardSweepMin + 1 {
		m.recordToolCard(tui.NewToolExecutionComponent("read", ""), "read-"+strconv.Itoa(i), "read")
	}
	if got := len(m.toolCards["bash"]); got > 2*toolCardSweepMin {
		t.Fatalf("bash records = %d after its cards were collected", got)
	}
	if _, ok := m.toolCards["bash"]["live"]; !ok {
		t.Fatal("the live card's record was swept")
	}
	runtime.KeepAlive(live)
}
