package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

type toolComponentFixture struct {
	mode     *InteractiveMode
	card     *tui.ToolExecutionComponent
	id, name string
}

func baseToolDefinition(name string) extension.ToolDefinition {
	return extension.ToolDefinition{Name: name, Label: name, Description: "custom tool", Parameters: json.RawMessage(`{}`), Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{}}, nil
	}}
}
func toolComponent(t *testing.T, name, id string, args map[string]any, definition *extension.ToolDefinition) toolComponentFixture {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveOptions{CWD: cwd}, chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 120, 40), toolByID: make(map[string]*tui.ToolExecutionComponent), toolStarts: make(map[string]time.Time)}
	m.tuiInst.SetRenderDispatcher(func(func()) {})
	t.Cleanup(m.tuiInst.CancelPendingRender)
	if definition != nil {
		m.newRunner = inproc.NewRunner([]extension.Extension{{Name: "fixture", Tools: map[string]extension.RegisteredTool{name: {Definition: *definition}}}}, cwd)
	}
	card := tui.NewToolExecutionComponent(name, tui.HeaderForTool(name, raw, cwd))
	card.Cwd = cwd
	card.SetHeaderArgs(raw)
	m.applyToolPresentation(card, id, name, raw)
	m.toolByID[id] = card
	m.chatContainer.Add(card)
	m.tuiInst.Add(m.chatContainer)
	return toolComponentFixture{m, card, id, name}
}
func (f toolComponentFixture) update(result agent.AgentToolResult, partial bool) {
	f.card.SetResultValue(result)
	if partial {
		f.card.SetStreaming(result.Text())
	} else {
		f.card.SetResult(result.Text(), result.IsError, 0)
	}
}
func (f toolComponentFixture) plain(width int) string { return plainRows(f.card.Render(width)) }
func assertToolContains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %q", want, text)
		}
	}
}

func TestToolExecutionComponentUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:85
	t.Run("stacks custom call and result renderers like the old implementation", func(t *testing.T) {
		def := baseToolDefinition("custom_tool")
		def.RenderCall = func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("custom call")
		}
		def.RenderResult = func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("custom result")
		}
		f := toolComponent(t, def.Name, "tool-1", map[string]any{}, &def)
		assertToolContains(t, f.plain(120), "custom call")
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, false)
		assertToolContains(t, f.plain(120), "custom call", "custom result")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:117
	t.Run("self-rendered empty tool rows take no layout space", func(t *testing.T) {
		def := baseToolDefinition("custom_tool")
		def.RenderShell = extension.ToolRenderShellSelf
		def.RenderCall = func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("")
		}
		def.RenderResult = func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("")
		}
		f := toolComponent(t, def.Name, "tool-empty-self-render", map[string]any{}, &def)
		if rows := f.card.Render(120); len(rows) != 0 {
			t.Fatalf("pending rows: %q", rows)
		}
		f.update(agent.AgentToolResult{Details: map[string]any{}}, false)
		if rows := f.card.Render(120); len(rows) != 0 {
			t.Fatalf("settled rows: %q", rows)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:148
	t.Run("uses built-in rendering for built-in overrides without custom renderers", func(t *testing.T) {
		def := baseToolDefinition("edit")
		f := toolComponent(t, "edit", "tool-2", map[string]any{"path": "README.md", "oldText": "before", "newText": "after"}, &def)
		f.update(agent.AgentToolResult{Details: &tools.EditToolDetails{Diff: "+1 after", FirstChangedLine: 1}}, false)
		text := f.plain(120)
		assertToolContains(t, text, "edit", "README.md")
		if strings.Contains(text, ":1") {
			t.Fatalf("unexpected line suffix: %q", text)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:169
	t.Run("preserves legacy file_path rendering compatibility for built-in tools", func(t *testing.T) {
		f := toolComponent(t, "read", "tool-3", map[string]any{"file_path": "README.md"}, nil)
		assertToolContains(t, f.plain(120), "read", "README.md")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:282
	t.Run("does not duplicate built-in headers when passed the active built-in definition", func(t *testing.T) {
		def := withBuiltInRenderers("read", baseToolDefinition("read"))
		f := toolComponent(t, "read", "tool-4", map[string]any{"path": "README.md"}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "hello"}}}, false)
		if text := f.plain(120); len(regexp.MustCompile(`\bread\b`).FindAllString(text, -1)) != 1 {
			t.Fatalf("read header duplicated: %q", text)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:297
	t.Run("inherits missing built-in result renderer slot from the built-in tool", func(t *testing.T) {
		def := baseToolDefinition("read")
		def.RenderCall = func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("override call")
		}
		f := toolComponent(t, "read", "tool-4b", map[string]any{"path": "notes.txt"}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "hello"}}}, false)
		f.card.SetExpanded(true)
		assertToolContains(t, f.plain(120), "override call", "hello")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:319
	t.Run("inherits missing built-in call renderer slot from the built-in tool", func(t *testing.T) {
		def := baseToolDefinition("read")
		def.RenderResult = func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("override result")
		}
		f := toolComponent(t, "read", "tool-4c", map[string]any{"path": "README.md"}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "hello"}}}, false)
		assertToolContains(t, f.plain(120), "read", "README.md", "override result")
	})
	for _, tc := range []struct{ name, prefix, id string }{
		// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:341
		{"uses custom renderers for built-in overrides that reuse built-in definition parameters", "override", "tool-4d"},
		// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:363
		{"uses custom renderers for built-in overrides that reuse wrapped built-in tool parameters", "wrapped override", "tool-4e"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := baseToolDefinition("read")
			parameters, err := json.Marshal((&tools.ReadTool{}).Schema().Parameters)
			if err != nil {
				t.Fatal(err)
			}
			def.Parameters = parameters
			def.RenderCall = func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
				return tui.NewText(tc.prefix + " call")
			}
			def.RenderResult = func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
				return tui.NewText(tc.prefix + " result")
			}
			f := toolComponent(t, "read", tc.id, map[string]any{"path": "README.md"}, &def)
			f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "hello"}}}, false)
			text := f.plain(120)
			assertToolContains(t, text, tc.prefix+" call", tc.prefix+" result")
			if tc.id == "tool-4d" && strings.Contains(text, "read README.md") {
				t.Fatalf("custom call lost: %q", text)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:385
	t.Run("shares renderer state across custom call and result slots", func(t *testing.T) {
		def := baseToolDefinition("custom_tool")
		def.RenderCall = func(_ json.RawMessage, _ extension.Theme, ctx extension.ToolRenderContext) extension.Component {
			state := ctx.State.(map[string]any)
			if state["token"] == nil {
				state["token"] = "shared-token"
			}
			return tui.NewText("custom call " + state["token"].(string))
		}
		def.RenderResult = func(_ extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, ctx extension.ToolRenderContext) extension.Component {
			return tui.NewText("custom result " + ctx.State.(map[string]any)["token"].(string))
		}
		f := toolComponent(t, def.Name, "tool-5", map[string]any{}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, false)
		assertToolContains(t, f.plain(120), "custom call shared-token", "custom result shared-token")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:413
	t.Run("exposes args in render result context", func(t *testing.T) {
		def := baseToolDefinition("custom_tool")
		def.RenderCall = func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("call")
		}
		def.RenderResult = func(_ extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, ctx extension.ToolRenderContext) extension.Component {
			var args struct {
				Foo string `json:"foo"`
			}
			raw, err := json.Marshal(ctx.Args)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				t.Fatal(err)
			}
			return tui.NewText("arg:" + args.Foo)
		}
		f := toolComponent(t, def.Name, "tool-5b", map[string]any{"foo": "bar"}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, false)
		assertToolContains(t, f.plain(120), "arg:bar")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:435
	t.Run("collapses fallback results until expanded", func(t *testing.T) {
		def := baseToolDefinition("custom_tool")
		fixture := toolComponent(t, "custom_tool", "tool-6", map[string]any{"foo": "bar"}, &def)
		var lines []string
		for i := 1; i <= 15; i++ {
			lines = append(lines, fmt.Sprintf("line-%d", i))
		}
		fixture.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Join(lines, "\n")}}, Details: map[string]any{}}, false)
		collapsed := fixture.plain(120)
		assertToolContains(t, collapsed, "custom_tool", "line-10", "5 more lines", "to expand")
		if strings.Contains(collapsed, "line-11") {
			t.Fatalf("fallback is not collapsed: %q", collapsed)
		}
		fixture.card.SetExpanded(true)
		expanded := fixture.plain(120)
		assertToolContains(t, expanded, "line-15")
		if strings.Contains(expanded, "more lines") {
			t.Fatalf("expanded fallback remained truncated: %q", expanded)
		}
		fmt.Printf("TOOL_FALLBACK [%t,%t,%t,%t,%t,%t,%t]\n", strings.Contains(collapsed, "custom_tool"), strings.Contains(collapsed, "line-10"), !strings.Contains(collapsed, "line-11"), strings.Contains(collapsed, "5 more lines"), strings.Contains(collapsed, "to expand"), strings.Contains(expanded, "line-15"), !strings.Contains(expanded, "more lines"))
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:465
	t.Run("trims trailing blank display lines from write previews", func(t *testing.T) {
		def := withBuiltInRenderers("write", baseToolDefinition("write"))
		f := toolComponent(t, "write", "tool-7", map[string]any{"path": "README.md", "content": "one\ntwo\n"}, &def)
		text := f.plain(120)
		assertToolContains(t, text, "one", "two")
		if strings.Contains(text, "two\n\n") {
			t.Fatalf("trailing blank preview: %q", text)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:481
	t.Run("trims trailing blank display lines from read results", func(t *testing.T) {
		def := withBuiltInRenderers("read", baseToolDefinition("read"))
		f := toolComponent(t, "read", "tool-8", map[string]any{"path": "notes.txt"}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "one\ntwo\n"}}}, false)
		f.card.SetExpanded(true)
		text := f.plain(120)
		assertToolContains(t, text, "one", "two")
		if strings.Contains(text, "two\n\n") {
			t.Fatalf("trailing blank result: %q", text)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:502
	t.Run("does not syntax-highlight read errors based on the requested file path", func(t *testing.T) {
		def := withBuiltInRenderers("read", baseToolDefinition("read"))
		f := toolComponent(t, "read", "tool-read-error-highlighting", map[string]any{"path": "config.exs", "offset": 120, "limit": 130}, &def)
		message := "Offset 120 is beyond end of file (96 lines total)"
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, IsError: true}, false)
		raw := strings.Join(f.card.Render(120), "\n")
		assertToolContains(t, stripANSITest(raw), message)
		assertToolContains(t, raw, tui.ActiveTheme().FgText("toolOutput", message))
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:520
	t.Run("expands a collapsed tool result when clicked", func(t *testing.T) {
		def := withBuiltInRenderers("read", baseToolDefinition("read"))
		f := toolComponent(t, "read", "tool-click-expand", map[string]any{"path": "notes.txt"}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "hidden content"}}}, false)
		lines := f.card.Render(120)
		row := -1
		for i, line := range lines {
			if strings.Contains(stripANSITest(line), "notes.txt") {
				row = i
				break
			}
		}
		if row < 0 {
			t.Fatal("missing result row")
		}
		result := tui.DispatchMouseEvent(f.card, tui.TuiMouseEvent{Type: tui.MouseClick, Button: tui.MouseButtonLeft, X: 2, Y: row, ScreenX: 2, ScreenY: row, Width: 120, Height: len(lines), ClickCount: 1})
		if result == nil || !result.Handled {
			t.Fatal("click not handled")
		}
		assertToolContains(t, f.plain(120), "hidden content")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:556
	t.Run("collapses ordinary read results until expanded", func(t *testing.T) {
		def := withBuiltInRenderers("read", baseToolDefinition("read"))
		f := toolComponent(t, "read", "tool-ordinary-read-collapsed", map[string]any{"path": "notes.txt"}, &def)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "hidden content"}}}, false)
		text := f.plain(120)
		assertToolContains(t, text, "read", "notes.txt")
		if strings.Contains(text, "hidden content") {
			t.Fatalf("collapsed result visible: %q", text)
		}
		f.card.SetExpanded(true)
		assertToolContains(t, f.plain(120), "hidden content")
	})
}

func TestNativeReadToolClickUsesProductionBody(t *testing.T) {
	f := toolComponent(t, "read", "native-read", map[string]any{"path": "notes.txt"}, nil)
	f.mode.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: f.id, ToolName: f.name, Args: json.RawMessage(`{"path":"notes.txt"}`)})
	f.mode.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: f.id, ToolName: f.name, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "hidden content"}}, Details: &tools.ReadDetails{Path: "notes.txt", StartLine: 1, TotalLines: 1}}})
	lines := f.mode.chatContainer.Render(120)
	if strings.Contains(plainRows(lines), "hidden content") {
		t.Fatal("native read was not collapsed")
	}
	row := -1
	for i, line := range lines {
		if strings.Contains(stripANSITest(line), "notes.txt") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("native read header missing")
	}
	result := tui.DispatchMouseEvent(f.mode.chatContainer, tui.TuiMouseEvent{Type: tui.MouseClick, Button: tui.MouseButtonLeft, X: 2, Y: row, ScreenX: 2, ScreenY: row, Width: 120, Height: len(lines), ClickCount: 1})
	if result == nil || !result.Handled {
		t.Fatal("native read click was not handled")
	}
	assertToolContains(t, f.plain(120), "hidden content")
}

func TestToolExecutionCompactReadsUpstream(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(ConfigRoot(), "docs", "README.md")
	for _, tc := range []struct{ title, path, content, compact, hidden, absent string }{
		{"SKILL.md", filepath.Join(cwd, "attio", "SKILL.md"), "---\nname: attio\ndescription: CRM helper\n---\n\n# Hidden skill instructions", "[skill] attio", "Hidden skill instructions", "read skill attio"},
		{"AGENTS.md", filepath.Join(cwd, ".pi", "AGENTS.md"), "Hidden resource instructions", "read resource .pi/AGENTS.md", "Hidden resource instructions", ""},
		{"AGENTS.override.md", filepath.Join(cwd, ".pi", "AGENTS.override.md"), "Hidden override instructions", "read resource .pi/AGENTS.override.md", "Hidden override instructions", ""},
		{"outside AGENTS.md", filepath.Join(cwd, "..", "AGENTS.md"), "Hidden outside resource instructions", "read resource " + filepath.ToSlash(filepath.Join(cwd, "..", "AGENTS.md")), "Hidden outside resource instructions", ""},
		{"Pi documentation", readme, "Hidden docs content", "read docs README.md", "Hidden docs content", ""},
	} {
		// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:623 (all five rows at 581-621).
		t.Run("renders "+tc.title+" read results compactly until expanded", func(t *testing.T) {
			def := withBuiltInRenderers("read", baseToolDefinition("read"))
			f := toolComponent(t, "read", "tool-compact-"+tc.title, map[string]any{"path": tc.path}, &def)
			f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: tc.content}}}, false)
			text := f.plain(120)
			assertToolContains(t, text, tc.compact)
			if strings.Contains(text, tc.hidden) || (tc.absent != "" && strings.Contains(text, tc.absent)) {
				t.Fatalf("expanded/invalid collapsed label: %q", text)
			}
			f.card.SetExpanded(true)
			assertToolContains(t, f.plain(120), tc.hidden)
		})
	}
	for _, tc := range []struct{ title, path, compact string }{{"SKILL.md", filepath.Join(cwd, "attio", "SKILL.md"), "[skill] attio:120-329"}, {"Pi documentation", readme, "read docs README.md:120-329"}} {
		// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:655 (both rows at 651-654).
		t.Run("shows the read line range in compact "+tc.title+" reads before the expand hint", func(t *testing.T) {
			def := withBuiltInRenderers("read", baseToolDefinition("read"))
			f := toolComponent(t, "read", "tool-compact-range-"+tc.title, map[string]any{"path": tc.path, "offset": 120, "limit": 210}, &def)
			text := f.plain(120)
			assertToolContains(t, text, tc.compact)
			if strings.Index(text, ":120-329") >= strings.Index(text, "to expand") {
				t.Fatalf("range not before hint: %q", text)
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:242 (all ten rows).
func TestToolExecutionBashDurationUpstream(t *testing.T) {
	for _, tc := range []struct {
		ms        int
		formatted string
	}{{0, "0.0s"}, {4200, "4.2s"}, {59900, "59.9s"}, {59999, "60.0s"}, {60000, "1m 0s"}, {90900, "1m 30s"}, {1592200, "26m 32s"}, {3599999, "59m 59s"}, {3600000, "1h 0m 0s"}, {7384900, "2h 3m 4s"}} {
		t.Run(fmt.Sprintf("bash renderer formats %d ms as %s while running and after completion", tc.ms, tc.formatted), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				def := withBuiltInRenderers("bash", baseToolDefinition("bash"))
				f := toolComponent(t, "bash", "tool-bash-duration", map[string]any{"command": "long-running-command"}, &def)
				f.mode.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: f.id, ToolName: f.name, Args: json.RawMessage(`{"command":"long-running-command"}`)})
				f.mode.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: f.id, ToolName: f.name, Content: ""})
				time.Sleep(time.Duration(tc.ms) * time.Millisecond)
				f.card.Invalidate()
				running := f.plain(120)
				f.mode.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: f.id, ToolName: f.name, Result: agent.AgentToolResult{}})
				completed := f.plain(120)
				time.Sleep(time.Second)
				f.card.Invalidate()
				if got := f.plain(120); got != completed {
					t.Fatalf("completed duration changed: %q -> %q", completed, got)
				}
				assertToolContains(t, running, "Elapsed "+tc.formatted)
				assertToolContains(t, completed, "Took "+tc.formatted)
			})
		})
	}
}

type componentBashOperations struct {
	exec func(context.Context, string, string, tools.BashOperationsExecOptions) (tools.BashOperationsResult, error)
}

func (o componentBashOperations) Exec(ctx context.Context, command, cwd string, options tools.BashOperationsExecOptions) (tools.BashOperationsResult, error) {
	return o.exec(ctx, command, cwd, options)
}

// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:184
func TestBashEmitsInitialEmptyPartialUpdateUpstream(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	type update struct {
		content string
		details any
	}
	var updates []update
	tool := &tools.BashTool{CWD: t.TempDir(), HideSessionEnvironment: true, Operations: componentBashOperations{exec: func(_ context.Context, command, _ string, _ tools.BashOperationsExecOptions) (tools.BashOperationsResult, error) {
		if command != "sleep 10" {
			t.Errorf("command=%q", command)
		}
		close(started)
		<-release
		return tools.BashOperationsResult{ExitCode: new(0)}, nil
	}}}
	done := make(chan struct{})
	var result agent.AgentToolResult
	var executeErr error
	go func() {
		result, executeErr = tool.Execute(t.Context(), "tool-bash-1", json.RawMessage(`{"command":"sleep 10"}`), func(content string, details any) { updates = append(updates, update{content, details}) })
		close(done)
	}()
	t.Cleanup(func() { finish(); <-done })
	select {
	case <-started:
	case <-done:
		t.Fatalf("tool ended before reaching injected operations: %v %+v", executeErr, result)
	}
	if !reflect.DeepEqual(updates, []update{{"", nil}}) {
		t.Fatalf("initial updates=%#v", updates)
	}
	finish()
	<-done
	if executeErr != nil || result.IsError {
		t.Fatalf("tool failed: %v %+v", executeErr, result)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/tool-execution-component.test.ts:204
func TestBashDoesNotDuplicateFinalTruncationDetailsUpstream(t *testing.T) {
	tool := &tools.BashTool{CWD: t.TempDir(), HideSessionEnvironment: true, Operations: componentBashOperations{exec: func(_ context.Context, command, _ string, options tools.BashOperationsExecOptions) (tools.BashOperationsResult, error) {
		if command != "generate output" {
			t.Errorf("command=%q", command)
		}
		for i := 1; i <= 4000; i++ {
			options.OnData([]byte(fmt.Sprintf("line-%04d\n", i)))
		}
		return tools.BashOperationsResult{ExitCode: new(0)}, nil
	}}}
	result, err := tool.Execute(t.Context(), "tool-bash-1b", json.RawMessage(`{"command":"generate output"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("bash output failed: %v %+v", err, result)
	}
	if details, ok := result.Details.(*tools.BashDetails); ok && details.FullOutputPath != "" {
		t.Cleanup(func() { _ = os.Remove(details.FullOutputPath) })
	}
	def := withBuiltInRenderers("bash", baseToolDefinition("bash"))
	f := toolComponent(t, "bash", "tool-bash-1b", map[string]any{"command": "generate output"}, &def)
	f.card.SetExpanded(true)
	f.update(result, false)
	text := f.plain(200)
	if strings.Count(text, "Full output:") != 1 {
		t.Fatalf("full output details count=%d", strings.Count(text, "Full output:"))
	}
	if !regexp.MustCompile(`line-4000[^\n]*\n[^\S\n]*\n \[Full output:`).MatchString(text) {
		t.Fatalf("wrong details spacing: %q", text)
	}
	if regexp.MustCompile(`line-4000[^\n]*\n[^\S\n]*\n[^\S\n]*\n \[Full output:`).MatchString(text) {
		t.Fatal("extra blank line before output details")
	}
	assertToolContains(t, text, "Truncated: showing 2000 of 4000 lines")
	if strings.Contains(text, "[Showing lines 2001-4000 of 4000. Full output:") {
		t.Fatal("raw execution truncation notice leaked into rendered output")
	}
}
