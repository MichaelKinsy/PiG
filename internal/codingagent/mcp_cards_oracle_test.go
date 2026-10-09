package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// The MCP tool card (mcp/tools.ts createMcpToolRenderers: the `server/tool` call line with its arguments, the output preview, the full-output
// path) through tool-execution.ts against pinned Pi, collapsed and expanded, pending and with results.
func TestMcpToolCardsMatchPi(t *testing.T) {
	lines := func(n int, prefix string) string {
		rows := make([]string, n)
		for i := range rows {
			rows[i] = fmt.Sprintf("%s %d", prefix, i+1)
		}
		return strings.Join(rows, "\n")
	}
	var probes []toolCardProbe
	var names []string
	add := func(name string, args map[string]any, result *toolCardResult) {
		for _, width := range []int{80, 28} {
			for _, expanded := range []bool{false, true} {
				probes = append(probes, toolCardProbe{Tool: "mcp__srv__tool", Args: args, Result: result, Expanded: expanded, Width: width, CWD: "/tmp"})
				names = append(names, fmt.Sprintf("%s width=%d expanded=%v", name, width, expanded))
			}
		}
	}
	add("pending no args", map[string]any{}, nil)
	add("pending args", map[string]any{"query": "needle", "limit": 5}, nil)
	add("pending nested args", map[string]any{"filter": map[string]any{"a": []any{1, 2, 3}, "b": "x"}}, nil)
	add("pending long args", map[string]any{"text": strings.Repeat("word ", 80)}, nil)
	add("pending multiline arg", map[string]any{"text": "a\nb\tc"}, nil)
	add("pending unicode", map[string]any{"emoji": "😀😀😀", "cjk": "日本語"}, nil)
	add("ok", map[string]any{"q": 1}, &toolCardResult{Content: cardText("hello")})
	add("empty output", map[string]any{"q": 1}, &toolCardResult{Content: cardText("")})
	add("whitespace output", map[string]any{"q": 1}, &toolCardResult{Content: cardText("  \n ")})
	add("long output", map[string]any{"q": 1}, &toolCardResult{Content: cardText(lines(30, "row"))})
	add("one long line", map[string]any{"q": 1}, &toolCardResult{Content: cardText(strings.Repeat(`{"key":"value"},`, 40))})
	add("tabs", map[string]any{"q": 1}, &toolCardResult{Content: cardText("a\tb\n\tc")})
	add("error", map[string]any{"q": 1}, &toolCardResult{Content: cardText("MCP error -32000: failed"), IsError: true})
	add("full output path", map[string]any{"q": 1}, &toolCardResult{Content: cardText(lines(30, "o")), Details: map[string]any{"server": "srv", "tool": "tool", "fullOutputPath": "/tmp/full.log"}})

	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/mcp_cards.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil))
	renderers := mcpext.CreateMcpToolRenderers("srv/tool")
	definition := extension.ToolDefinition{Name: "mcp__srv__tool", RenderCall: renderers.RenderCall, RenderResult: renderers.RenderResult}
	failures := 0
	for i, probe := range probes {
		raw, _ := json.Marshal(probe.Args)
		f := toolComponentRaw(t, "mcp__srv__tool", "id1", raw, &definition)
		f.mode.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: f.id, ToolName: f.name, Args: raw})
		if r := probe.Result; r != nil {
			var content []ai.ToolResultMessageContent
			for _, block := range r.Content {
				content = append(content, ai.TextContent{Text: block["text"].(string)})
			}
			var details any
			if r.Details != nil {
				details = r.Details
			}
			f.mode.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: f.id, ToolName: f.name, IsError: r.IsError, Result: agent.AgentToolResult{Content: content, Details: details, IsError: r.IsError}})
		}
		f.card.SetExpanded(probe.Expanded)
		got := f.card.Render(probe.Width)
		if strings.Join(got, "\n") != strings.Join(expected[i], "\n") {
			if failures++; failures <= 4 {
				t.Errorf("%s:\n  Pig %q\n  Pi  %q", names[i], got, expected[i])
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
