package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// The codemode tool card (codemode/renderer.ts codemodeRenderers: code preview, nested call rows, model-call cost total, output preview,
// full-output path) through tool-execution.ts against pinned Pi, collapsed and expanded, pending and with results; rows are compared byte for byte.
func TestCodemodeCardsMatchPi(t *testing.T) {
	probes, names := codemodeCardProbes(t)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/codemode_cards.mjs", pigversion.UpstreamVersion)
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
	failures := 0
	for i, got := range renderCodemodeCards(t, probes) {
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

// TestCodemodeCardsProbeDump prints the corpus for the Pi side of the tools/25 parity scenario.
func TestCodemodeCardsProbeDump(t *testing.T) {
	probes, _ := codemodeCardProbes(t)
	line, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("codemodecard-probes:%s\n", line)
}

// TestCodemodeCardsParity prints Pig's rows, one JSON line per probe, for the tools/25 parity scenario.
func TestCodemodeCardsParity(t *testing.T) {
	probes, _ := codemodeCardProbes(t)
	for _, rows := range renderCodemodeCards(t, probes) {
		var line strings.Builder
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(rows); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("codemodecard-observation:%s", line.String())
	}
}

func codemodeCardProbes(t *testing.T) ([]toolCardProbe, []string) {
	t.Helper()
	header := "Script completed\nWall time 0.1 seconds\nOutput:\n"
	failedHeader := "Script failed\nWall time 2.5 seconds\nOutput:\n"
	lines := func(n int, prefix string) string {
		rows := make([]string, n)
		for i := range rows {
			rows[i] = fmt.Sprintf("%s %d", prefix, i+1)
		}
		return strings.Join(rows, "\n")
	}
	call := func(name, args, status string, extra map[string]any) map[string]any {
		c := map[string]any{"name": name, "args": args, "status": status}
		maps.Copy(c, extra)
		return c
	}
	calls := func(items ...map[string]any) map[string]any {
		list := make([]any, len(items))
		for i, item := range items {
			list[i] = item
		}
		return map[string]any{"calls": list}
	}
	var probes []toolCardProbe
	var names []string
	add := func(name string, code any, result *toolCardResult) {
		for _, width := range []int{80, 28} {
			for _, expanded := range []bool{false, true} {
				probes = append(probes, toolCardProbe{Tool: "codemode", Args: map[string]any{"code": code}, Result: result, Expanded: expanded, Width: width, CWD: "/tmp"})
				names = append(names, fmt.Sprintf("%s width=%d expanded=%v", name, width, expanded))
			}
		}
	}
	add("pending short", "return 1", nil)
	add("pending long", lines(30, "const x ="), nil)
	add("pending tabs and CR", "if (a) {\r\n\treturn 1;\r\n}\r\n", nil)
	add("pending empty", "", nil)
	add("pending no code arg", nil, nil)
	add("pending number code", 5, nil)
	add("ok output", "return 1", &toolCardResult{Content: cardText(header + "hello")})
	add("ok no output", "return 1", &toolCardResult{Content: cardText(header)})
	add("failed output", "throw 1", &toolCardResult{Content: cardText(failedHeader + "Error: boom"), IsError: true})
	add("no header", "x", &toolCardResult{Content: cardText("Invalid options: bad")})
	add("long output", "x", &toolCardResult{Content: cardText(header + lines(30, "out"))})
	add("one long line", "x", &toolCardResult{Content: cardText(header + strings.Repeat("abcdefghij ", 60))})
	add("tabs in output", "x", &toolCardResult{Content: cardText(header + "a\tb\n\tc")})
	add("output with full path", "x", &toolCardResult{Content: cardText(header + lines(30, "o")), Details: map[string]any{"fullOutputPath": "/tmp/full.log", "calls": []any{}}})
	add("calls statuses", "x", &toolCardResult{Content: cardText(header + "done"), Details: calls(
		call("read", `{"path":"a.txt"}`, "ok", map[string]any{"durationMs": 12.4}),
		call("write", `{"path":"b.txt"}`, "error", map[string]any{"durationMs": 1500, "error": "EACCES\nsecond line"}),
		call("bash", "", "running", nil),
		call("grep", `{"pattern":"x"}`, "cancelled", map[string]any{"durationMs": 999.5}),
	)})
	manyCalls := make([]map[string]any, 12)
	for i := range manyCalls {
		manyCalls[i] = call(fmt.Sprintf("tool%d", i), strings.Repeat("a", 100), "ok", map[string]any{"durationMs": 1000 * (i + 1)})
	}
	add("many calls", "x", &toolCardResult{Content: cardText(header), Details: calls(manyCalls...)})
	add("costs", "x", &toolCardResult{Content: cardText(header + "ok"), Details: calls(
		call("model.a", "{}", "ok", map[string]any{"cost": 0.0012345, "durationMs": 40}),
		call("model.b", "{}", "ok", map[string]any{"cost": 0.5, "durationMs": 40}),
		call("model.c", "{}", "ok", map[string]any{"cost": 0.0049}),
		call("plain", "{}", "ok", nil),
	)})
	add("single cost", "x", &toolCardResult{Content: cardText(header), Details: calls(call("model.a", "{}", "ok", map[string]any{"cost": 0.02}))})
	add("empty calls", "x", &toolCardResult{Content: cardText(header + "z"), Details: calls()})
	add("error without header", "x", &toolCardResult{Content: cardText("Script rejected"), IsError: true})

	return probes, names
}

// renderCodemodeCards drives one card per probe through the interactive mode's tool events and returns its rows.
func renderCodemodeCards(t *testing.T, probes []toolCardProbe) [][]string {
	t.Helper()
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil))
	definition := extension.ToolDefinition{Name: "codemode", RenderCall: CodemodeRenderers.RenderCall, RenderResult: CodemodeRenderers.RenderResult}
	out := make([][]string, len(probes))
	for i, probe := range probes {
		raw, _ := json.Marshal(probe.Args)
		f := toolComponentRaw(t, "codemode", "id1", raw, &definition)
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
		out[i] = f.card.Render(probe.Width)
	}
	return out
}
