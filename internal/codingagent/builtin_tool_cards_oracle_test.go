package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type toolCardResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError"`
	Details map[string]any   `json:"details,omitempty"`
}

type toolCardProbe struct {
	Tool     string          `json:"tool"`
	Args     map[string]any  `json:"args"`
	Result   *toolCardResult `json:"result"`
	Partial  bool            `json:"partial"`
	NoImages bool            `json:"noImages"`
	Expanded bool            `json:"expanded"`
	Width    int             `json:"width"`
	Theme    string          `json:"theme"`
	CWD      string          `json:"cwd"`
}

func cardText(text string) []map[string]any { return []map[string]any{{"type": "text", "text": text}} }

// tool-execution.ts with the built-in tool definitions' renderCall and renderResult (core/tools/{read,write,bash,grep,find,ls}.ts)
// against pinned Pi: each card pending and with a result, collapsed and expanded, in both themes at two widths, as the interactive mode builds it from tool events.
// Rows are compared byte for byte: colours, backgrounds and padding included.
func TestBuiltinToolCardsMatchPi(t *testing.T) {
	probes, names := builtinToolCardProbes(t)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/builtin_tool_cards.mjs", pigversion.UpstreamVersion)
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
	for i, got := range renderBuiltinToolCards(t, probes) {
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

// TestBuiltinToolCardsProbeDump prints the corpus for the Pi side of the tools/24 parity scenario.
func TestBuiltinToolCardsProbeDump(t *testing.T) {
	probes, _ := builtinToolCardProbes(t)
	line, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("toolcard-probes:%s\n", line)
}

// TestBuiltinToolCardsParity prints Pig's rows, one JSON line per probe, for the tools/24 parity scenario.
func TestBuiltinToolCardsParity(t *testing.T) {
	probes, _ := builtinToolCardProbes(t)
	for _, rows := range renderBuiltinToolCards(t, probes) {
		var line strings.Builder
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(rows); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("toolcard-observation:%s", line.String())
	}
}

func builtinToolCardProbes(t *testing.T) ([]toolCardProbe, []string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	lines := func(n int, prefix string) string {
		rows := make([]string, n)
		for i := range rows {
			rows[i] = fmt.Sprintf("%s %d", prefix, i+1)
		}
		return strings.Join(rows, "\n")
	}
	truncated := map[string]any{"truncated": true, "truncatedBy": "lines", "totalLines": 5000, "totalBytes": 90000, "outputLines": 2000, "outputBytes": 50000, "lastLinePartial": false, "firstLineExceedsLimit": false, "maxLines": 2000, "maxBytes": 51200}
	var probes []toolCardProbe
	var names []string
	add := func(name, tool string, args map[string]any, result *toolCardResult) {
		// Pi's bash tool reports an empty partial result as soon as it starts (bash.ts:318-319), which is what shows the elapsed time.
		partial := result == nil && tool == "bash"
		if partial {
			result = &toolCardResult{Content: cardText("")[:0]}
		}
		for _, theme := range []string{"dark", "light"} {
			for _, width := range []int{80, 28} {
				for _, expanded := range []bool{false, true} {
					probes = append(probes, toolCardProbe{Tool: tool, Args: args, Result: result, Partial: partial, Expanded: expanded, Width: width, Theme: theme, CWD: cwd})
					names = append(names, fmt.Sprintf("%s theme=%s width=%d expanded=%v", name, theme, width, expanded))
				}
			}
		}
	}
	for _, tool := range []string{"read", "write", "bash", "grep", "find", "ls"} {
		args := map[string]map[string]any{"read": {"path": "notes.txt"}, "write": {"path": "out.txt", "content": "hello"}, "bash": {"command": "make test"}, "grep": {"pattern": "needle"}, "find": {"pattern": "*.go"}, "ls": {"path": "."}}[tool]
		add(tool+" pending", tool, args, nil)
	}
	add("read ok", "read", map[string]any{"path": "notes.txt"}, &toolCardResult{Content: cardText(lines(3, "line"))})
	add("read long", "read", map[string]any{"path": "notes.txt"}, &toolCardResult{Content: cardText(lines(30, "line"))})
	add("read range", "read", map[string]any{"path": "notes.txt", "offset": 10, "limit": 20}, &toolCardResult{Content: cardText(lines(20, "row"))})
	add("read truncated", "read", map[string]any{"path": "notes.txt"}, &toolCardResult{Content: cardText(lines(12, "t")), Details: map[string]any{"truncation": truncated}})
	add("read error", "read", map[string]any{"path": "missing.txt"}, &toolCardResult{Content: cardText("ENOENT: no such file"), IsError: true})
	add("write ok", "write", map[string]any{"path": "out.txt", "content": lines(3, "w")}, &toolCardResult{Content: cardText("Successfully wrote 12 bytes to out.txt")})
	add("write long", "write", map[string]any{"path": "out.txt", "content": lines(30, "w")}, &toolCardResult{Content: cardText("Successfully wrote 400 bytes to out.txt")})
	add("write error", "write", map[string]any{"path": "out.txt", "content": "x"}, &toolCardResult{Content: cardText("EACCES: permission denied"), IsError: true})
	add("bash ok", "bash", map[string]any{"command": "echo hi"}, &toolCardResult{Content: cardText("hi")})
	add("bash long", "bash", map[string]any{"command": "seq 40"}, &toolCardResult{Content: cardText(lines(40, "n"))})
	add("bash error", "bash", map[string]any{"command": "false"}, &toolCardResult{Content: cardText("boom\n\nCommand exited with code 1"), IsError: true})
	add("bash timeout arg", "bash", map[string]any{"command": "sleep 1", "timeout": 5}, &toolCardResult{Content: cardText("")})
	add("bash truncated", "bash", map[string]any{"command": "yes"}, &toolCardResult{Content: cardText(lines(5, "y")), Details: map[string]any{"truncation": truncated, "fullOutputPath": "/tmp/full.log"}})
	add("grep ok", "grep", map[string]any{"pattern": "needle", "path": "src"}, &toolCardResult{Content: cardText(lines(4, "src/a.go:1: needle"))})
	add("grep long", "grep", map[string]any{"pattern": "needle", "glob": "*.go", "limit": 100}, &toolCardResult{Content: cardText(lines(30, "m"))})
	add("grep limit", "grep", map[string]any{"pattern": "x"}, &toolCardResult{Content: cardText(lines(5, "g")), Details: map[string]any{"matchLimitReached": 100, "linesTruncated": true}})
	add("find ok", "find", map[string]any{"pattern": "*.go", "path": "src"}, &toolCardResult{Content: cardText(lines(4, "src/f"))})
	add("find limit", "find", map[string]any{"pattern": "*.go"}, &toolCardResult{Content: cardText(lines(25, "f")), Details: map[string]any{"resultLimitReached": 1000}})
	add("ls ok", "ls", map[string]any{"path": "src"}, &toolCardResult{Content: cardText(lines(4, "entry"))})
	add("ls limit", "ls", map[string]any{"path": "."}, &toolCardResult{Content: cardText(lines(25, "e")), Details: map[string]any{"entryLimitReached": 500}})
	editArgs := map[string]any{"path": "nonexistent-dir/x.txt", "edits": []any{map[string]any{"oldText": "alpha", "newText": "beta"}}}
	add("edit pending", "edit", editArgs, nil)
	add("edit diff", "edit", editArgs, &toolCardResult{Content: cardText("Successfully replaced 1 block(s) in nonexistent-dir/x.txt."), Details: map[string]any{"diff": "  1 context\n-  2 alpha\n+  2 beta\n  3 tail", "firstChangedLine": 2}})
	add("edit error", "edit", editArgs, &toolCardResult{Content: cardText("Could not find the exact text in nonexistent-dir/x.txt."), IsError: true})
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	withImage := func(text string) *toolCardResult {
		return &toolCardResult{Content: append(cardText(text), map[string]any{"type": "image", "data": png, "mimeType": "image/png"})}
	}
	for _, noImages := range []bool{false, true} {
		for _, tool := range []string{"read", "bash", "grep", "find", "ls"} {
			args := map[string]map[string]any{"read": {"path": "pic.png"}, "bash": {"command": "cat pic.png"}, "grep": {"pattern": "png"}, "find": {"pattern": "*.png"}, "ls": {"path": "."}}[tool]
			for _, width := range []int{80, 28} {
				for _, text := range []string{"Read image file [image/png]", ""} {
					probes = append(probes, toolCardProbe{Tool: tool, Args: args, Result: withImage(text), NoImages: noImages, Expanded: true, Width: width, Theme: "dark", CWD: cwd})
					names = append(names, fmt.Sprintf("%s image text=%q noImages=%v width=%d", tool, text, noImages, width))
				}
			}
		}
	}
	add("find limit arg", "find", map[string]any{"pattern": "*.go", "path": "src", "limit": 50}, &toolCardResult{Content: cardText(lines(3, "src/g"))})
	add("ls limit arg", "ls", map[string]any{"path": "src", "limit": 10}, &toolCardResult{Content: cardText(lines(3, "dir"))})
	// Arguments of the wrong type or still streaming render the invalid-arg and placeholder text.
	for _, tool := range []string{"read", "write", "edit", "bash", "grep", "find", "ls"} {
		for _, args := range []map[string]any{{}, {"path": 5, "command": 5, "pattern": 5, "content": 5}, {"path": "", "command": "", "pattern": "", "content": ""}, {"path": "a.txt", "content": 5, "edits": 5, "limit": "x", "offset": "y", "timeout": "z"}} {
			add(fmt.Sprintf("%s odd args %v", tool, args), tool, args, nil)
		}
	}
	// A known file extension syntax-highlights the written content and the read text.
	goSource := "package main\n\nimport \"fmt\"\n\n// main prints.\nfunc main() {\n\tfmt.Println(\"hi\", 42)\n}\n"
	add("write go pending", "write", map[string]any{"path": "main.go", "content": goSource}, nil)
	add("write go ok", "write", map[string]any{"path": "main.go", "content": goSource}, &toolCardResult{Content: cardText("Successfully wrote 90 bytes to main.go")})
	add("write json long", "write", map[string]any{"path": "data.json", "content": "{\n" + lines(20, "  \"k\": 1,") + "\n}"}, nil)
	add("read go", "read", map[string]any{"path": "main.go"}, &toolCardResult{Content: cardText(goSource)})
	add("read md", "read", map[string]any{"path": "README.md"}, &toolCardResult{Content: cardText("# Title\n\nsome `code` and **bold**\n")})
	add("ls empty", "ls", map[string]any{}, &toolCardResult{Content: cardText("(empty directory)")})

	return probes, names
}

// renderBuiltinToolCards drives one card per probe through the interactive mode's tool events and returns its rows.
func renderBuiltinToolCards(t *testing.T, probes []toolCardProbe) [][]string {
	t.Helper()
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	t.Cleanup(func() { tui.SetTheme("dark") })
	tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil))
	out := make([][]string, len(probes))
	for i, probe := range probes {
		tui.SetTheme(probe.Theme)
		raw, _ := json.Marshal(probe.Args)
		// A session registers bash, grep, find and ls as tool definitions; bindToolCard gives read, write and edit their built-in definitions.
		var definition *extension.ToolDefinition
		if slices.Contains([]string{"bash", "grep", "find", "ls"}, probe.Tool) {
			base := baseToolDefinition(probe.Tool)
			definition = &base
		}
		f := toolComponentRaw(t, probe.Tool, "id1", raw, definition)
		f.mode.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: f.id, ToolName: f.name, Args: raw})
		if r := probe.Result; r != nil {
			var content []ai.ToolResultMessageContent
			for _, block := range r.Content {
				if block["type"] == "image" {
					content = append(content, ai.ImageContent{Data: block["data"].(string), MimeType: block["mimeType"].(string)})
					continue
				}
				content = append(content, ai.TextContent{Text: block["text"].(string)})
			}
			var details any
			if r.Details != nil {
				details = r.Details
			}
			if probe.Partial {
				f.mode.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: f.id, ToolName: f.name, PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}}})
			} else {
				f.mode.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: f.id, ToolName: f.name, IsError: r.IsError, Result: agent.AgentToolResult{Content: content, Details: details, IsError: r.IsError}})
			}
		}
		if probe.NoImages {
			f.card.SetShowImages(false)
		}
		f.card.SetExpanded(probe.Expanded)
		out[i] = f.card.Render(probe.Width)
	}
	return out
}
