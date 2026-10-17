package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// builtinToolCardsScript is the Pi oracle's absolute path, taken before a test changes its working directory.
var builtinToolCardsScript, _ = filepath.Abs("testdata/builtin_tool_cards.mjs")

// toolCardClockStart is the Unix millisecond each probe's card clock starts at, on both sides (builtin_tool_cards.mjs).
const toolCardClockStart = 1_700_000_000_000

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
	// Settle waits for the edit call's diff preview before the card is drawn.
	Settle bool `json:"settle,omitempty"`
	// Bindings are user keybindings (the expand hint names the keys bound to app.tools.expand).
	Bindings map[string][]string `json:"bindings,omitempty"`
	// ElapsedMs is how far the card clock moves between the start event and the result: both sides fix the clock
	// (toolTimerNow, Date.now()) so the bash card's Elapsed and Took rows show this span, not the scheduler's.
	ElapsedMs int64 `json:"elapsedMs"`
}

func cardText(text string) []map[string]any { return []map[string]any{{"type": "text", "text": text}} }

// tool-execution.ts with the built-in tool definitions' renderCall and renderResult (core/tools/{read,write,bash,grep,find,ls}.ts)
// against pinned Pi: each card pending and with a result, collapsed and expanded, in both themes at two widths, as the interactive mode builds it from tool events.
// Rows are compared byte for byte: colours, backgrounds and padding included.
func TestBuiltinToolCardsMatchPi(t *testing.T) {
	probes, names := builtinToolCardProbes(t)
	compareToolCards(t, probes, names)
}

// compareToolCards renders each probe with Pi and with Pig and reports the first rows that differ.
func compareToolCards(t *testing.T, probes []toolCardProbe, names []string) {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", builtinToolCardsScript, pigversion.UpstreamPackageVersion)
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
				row := 0
				for row < min(len(got), len(expected[i])) && got[row] == expected[i][row] {
					row++
				}
				var pigRow, piRow string
				if row < len(got) {
					pigRow = got[row]
				}
				if row < len(expected[i]) {
					piRow = expected[i][row]
				}
				for r := max(0, row-2); r < min(len(got), len(expected[i])) && r <= row+2; r++ {
					t.Logf("row %d\n  Pig %q\n  Pi  %q", r, got[r], expected[i][r])
				}
				t.Errorf("%s (width %d, %d vs %d rows), first difference at row %d:\n  Pig %q\n  Pi  %q", names[i], probes[i].Width, len(got), len(expected[i]), row, pigRow, piRow)
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// edit.ts renderCall once its diff preview has settled: a Box in the success background holding the path and the diff the edits would make, or in the error background holding the error text, against pinned Pi
// (the pending card, before the preview lands, is the "edit pending" probes of TestBuiltinToolCardsMatchPi). The Pi probe waits for the preview and PiG's card draws once it lands.
func TestBuiltinEditCallPreviewMatchesPi(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "preview.txt"), []byte("head\nalpha\ntail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var probes []toolCardProbe
	var names []string
	for name, edits := range map[string][]any{
		"preview ok":     {map[string]any{"oldText": "alpha", "newText": "beta"}},
		"preview two":    {map[string]any{"oldText": "head", "newText": "HEAD"}, map[string]any{"oldText": "tail", "newText": "tail\nmore"}},
		"preview failed": {map[string]any{"oldText": "absent text", "newText": "x"}},
	} {
		for _, theme := range []string{"dark", "light"} {
			for _, width := range []int{80, 28} {
				probes = append(probes, toolCardProbe{Tool: "edit", Args: map[string]any{"path": "preview.txt", "edits": edits}, Width: width, Theme: theme, CWD: dir, Settle: true})
				names = append(names, fmt.Sprintf("edit %s theme=%s width=%d", name, theme, width))
			}
		}
	}
	compareToolCards(t, probes, names)
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
					probes = append(probes, toolCardProbe{Tool: tool, Args: args, Result: result, Partial: partial, Expanded: expanded, Width: width, Theme: theme, CWD: cwd, ElapsedMs: 4200})
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
	// The bash card's clock row at renderers/bash.ts formatDuration's boundaries: toFixed(1) rounding of whole
	// milliseconds below a minute, the switch to minutes at 60 s, and to hours, as the live Elapsed and the final Took.
	for _, ms := range []int64{0, 49, 50, 51, 99, 100, 149, 150, 151, 250, 1150, 9949, 9950, 59949, 59950, 59999, 60000, 61500, 3599999, 3600000, 3723000, 93599000} {
		for _, partial := range []bool{true, false} {
			result := &toolCardResult{Content: cardText("ok")}
			if partial {
				result = &toolCardResult{Content: cardText("")[:0]}
			}
			probes = append(probes, toolCardProbe{Tool: "bash", Args: map[string]any{"command": "make test"}, Result: result, Partial: partial, Width: 80, Theme: "dark", CWD: cwd, ElapsedMs: ms})
			names = append(names, fmt.Sprintf("bash clock %dms partial=%v", ms, partial))
		}
	}
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

	// The collapsed expand hint names the keys bound to app.tools.expand (render-utils keyHint), rebound, multi-key or unbound.
	for _, bindings := range []map[string][]string{{"app.tools.expand": {"ctrl+e"}}, {"app.tools.expand": {"ctrl+e", "alt+x"}}, {"app.tools.expand": {}}} {
		for _, tool := range []string{"read", "write", "edit", "bash", "grep", "find", "ls"} {
			args := map[string]map[string]any{"read": {"path": "notes.txt"}, "write": {"path": "out.txt", "content": lines(30, "w")}, "edit": editArgs, "bash": {"command": "seq 40"}, "grep": {"pattern": "needle"}, "find": {"pattern": "*.go"}, "ls": {"path": "."}}[tool]
			result := &toolCardResult{Content: cardText(lines(40, "row"))}
			if tool == "edit" {
				result = &toolCardResult{Content: cardText("Successfully replaced 1 block(s) in nonexistent-dir/x.txt."), Details: map[string]any{"diff": "  1 context\n-  2 alpha\n+  2 beta\n  3 tail", "firstChangedLine": 2}}
			}
			probes = append(probes, toolCardProbe{Tool: tool, Args: args, Result: result, Width: 80, Theme: "dark", CWD: cwd, Bindings: bindings})
			names = append(names, fmt.Sprintf("%s rebound expand %v", tool, bindings))
		}
	}
	return probes, names
}

// renderRequestWatcher is the TUI a probe's card asks for repaints. The edit call's preview computes off the UI loop and, once it has stored
// the preview the next draw shows, asks for a repaint (editRenderCall). That request is the signal Settle waits for: the preview can land
// before the card's first frame is drawn, so a frame that differs from the first one is not a signal that the preview ever lands.
type renderRequestWatcher struct {
	tui.TUI
	requests chan struct{}
}

func (w *renderRequestWatcher) RequestRender(force ...bool) {
	select {
	case w.requests <- struct{}{}:
	default:
	}
	w.TUI.RequestRender(force...)
}

// renderBuiltinToolCards drives one card per probe through the interactive mode's tool events and returns its rows.
func renderBuiltinToolCards(t *testing.T, probes []toolCardProbe) [][]string {
	t.Helper()
	// A preview that never lands is a hang, not a slow machine: the wait for its repaint ends at the test's hang bound.
	waitCtx := testbudget.Context(t)
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	t.Cleanup(func() { tui.SetTheme("dark") })
	t.Cleanup(func() { tui.SetAppKeyTextResolver(nil) })
	// The card clock reads cardClock, which stands at the probe's start until its result lands, as the Pi probe moves Date.now().
	var cardClock atomic.Int64
	previousNow := toolTimerNow
	t.Cleanup(func() { toolTimerNow = previousNow })
	toolTimerNow = func() time.Time { return time.UnixMilli(cardClock.Load()) }
	out := make([][]string, len(probes))
	for i, probe := range probes {
		tui.SetTheme(probe.Theme)
		installCardBindings(t, probe)
		raw, _ := json.Marshal(probe.Args)
		// A session registers bash, grep, find and ls as tool definitions; bindToolCard gives read, write and edit their built-in definitions.
		var definition *extension.ToolDefinition
		if slices.Contains([]string{"bash", "grep", "find", "ls"}, probe.Tool) {
			base := baseToolDefinition(probe.Tool)
			definition = &base
		}
		cardClock.Store(toolCardClockStart)
		f := toolComponentRaw(t, probe.Tool, "id1", raw, definition)
		var watcher *renderRequestWatcher
		if probe.Settle {
			watcher = &renderRequestWatcher{TUI: f.mode.tuiInst, requests: make(chan struct{}, 1)}
			f.mode.tuiInst = watcher
		}
		f.mode.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: f.id, ToolName: f.name, Args: raw})
		if probe.Settle {
			f.card.SetArgsComplete()
		}
		cardClock.Store(toolCardClockStart + probe.ElapsedMs)
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
		if watcher != nil {
			// Every request so far came from this goroutine. The first draw below starts the preview, whose request is the next one.
			select {
			case <-watcher.requests:
			default:
			}
		}
		out[i] = f.card.Render(probe.Width)
		if watcher != nil {
			select {
			case <-watcher.requests:
			case <-waitCtx.Done():
				t.Fatalf("probe %d: the edit preview never asked for a repaint: %v", i, context.Cause(waitCtx))
			}
			out[i] = f.card.Render(probe.Width)
		}
		if probe.Result == nil || probe.Partial {
			// A running bash card ticks its elapsed time until a final result renders (Pi's bash.ts setInterval); finish the card so its ticker does not outlive the test.
			t.Cleanup(func() {
				f.mode.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: f.id, ToolName: f.name, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}}})
				f.card.Render(probe.Width)
			})
		}
	}
	return out
}

// TestBuiltinToolCardsRandomMatchPi drives random tool calls and results through the same cards: arguments of every type, result text with wide, combining, ANSI, tab, CRLF and very long lines, from none to 70 rows, error and truncation details, widths from 3 to 120 (Pi pads the children of a Box to a content width of at least one column, which only shows below the two padding columns), both themes, collapsed and expanded.
func TestBuiltinToolCardsRandomMatchPi(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20261017))
	// The card clock's span draws from its own source so the rest of the corpus stays the one rng gives.
	clockRng := rand.New(rand.NewSource(20261013))
	pieces := []string{"", "x", "alpha beta", "日本語のテキスト", "e\u0301", "😀", "\ttabbed", "  indented", "\x1b[31mred\x1b[0m", "line with CR\r", "src/a.go:12: needle", "a/very/long/path/that/keeps/going/and/going/and/going/until/it/wraps/around/the/card.txt", strings.Repeat("w", 150), "{ \"k\": [1, 2] }", "package main", "# heading", "\x00bin\x07"}
	paths := []string{"notes.txt", "main.go", "README.md", "data.json", "日本語.txt", "a b/c.ts", "/abs/path/file.py", "~/home.txt", "", ".", "../up.sh", "dir/", "x.unknownext"}
	text := func(rows int) string {
		out := make([]string, rows)
		for i := range out {
			out[i] = pieces[rng.Intn(len(pieces))]
		}
		return strings.Join(out, "\n")
	}
	tools := []string{"read", "write", "edit", "bash", "grep", "find", "ls"}
	var probes []toolCardProbe
	var names []string
	for i := range 500 {
		tool := tools[rng.Intn(len(tools))]
		args := map[string]any{}
		maybe := func(key string, value any) {
			if rng.Intn(2) == 0 {
				args[key] = value
			}
		}
		maybe("path", paths[rng.Intn(len(paths))])
		maybe("pattern", pieces[rng.Intn(len(pieces))])
		maybe("command", pieces[rng.Intn(len(pieces))]+"\n"+pieces[rng.Intn(len(pieces))])
		maybe("content", text(rng.Intn(30)))
		maybe("glob", []string{"*.go", "**/*.ts", ""}[rng.Intn(3)])
		maybe("offset", []any{1, 10, 0, -1, "x"}[rng.Intn(5)])
		maybe("limit", []any{1, 100, 0, "y"}[rng.Intn(4)])
		maybe("timeout", []any{1, 30, 0.5, "z"}[rng.Intn(4)])
		maybe("edits", []any{map[string]any{"oldText": pieces[rng.Intn(len(pieces))], "newText": pieces[rng.Intn(len(pieces))]}})
		var result *toolCardResult
		if rng.Intn(5) != 0 {
			result = &toolCardResult{Content: cardText(text(rng.Intn(70))), IsError: rng.Intn(6) == 0}
			details := map[string]any{}
			if rng.Intn(4) == 0 {
				details["truncation"] = map[string]any{"truncated": true, "truncatedBy": []string{"lines", "bytes"}[rng.Intn(2)], "totalLines": 5000, "totalBytes": 90000, "outputLines": 2000, "outputBytes": 50000, "lastLinePartial": rng.Intn(2) == 0, "firstLineExceedsLimit": rng.Intn(4) == 0, "maxLines": 2000, "maxBytes": 51200}
				if rng.Intn(2) == 0 {
					details["fullOutputPath"] = "/tmp/full.log"
				}
			}
			if tool == "edit" && rng.Intn(2) == 0 {
				details["diff"] = "  1 context\n-  2 " + pieces[rng.Intn(len(pieces))] + "\n+  2 " + pieces[rng.Intn(len(pieces))] + "\n  3 tail"
				details["firstChangedLine"] = 2
			}
			for _, key := range []string{"matchLimitReached", "resultLimitReached", "entryLimitReached"} {
				if rng.Intn(8) == 0 {
					details[key] = 100
				}
			}
			if rng.Intn(8) == 0 {
				details["linesTruncated"] = true
			}
			if len(details) > 0 {
				result.Details = details
			}
		}
		partial := result == nil && tool == "bash"
		if partial {
			result = &toolCardResult{Content: cardText("")[:0]}
		}
		probes = append(probes, toolCardProbe{Tool: tool, Args: args, Result: result, Partial: partial, Expanded: rng.Intn(2) == 0, Width: 3 + rng.Intn(118), Theme: []string{"dark", "light"}[rng.Intn(2)], CWD: cwd, ElapsedMs: clockRng.Int63n([]int64{60_000, 2 * 3_600_000}[clockRng.Intn(2)])})
		names = append(names, fmt.Sprintf("random %d %s", i, tool))
	}
	compareToolCards(t, probes, names)
}

// rebindExpandCards copies each collapsed 80-column probe once per expand-key binding set: rebound, multi-key and unbound, which the hint names.
func rebindExpandCards(probes []toolCardProbe, names []string) ([]toolCardProbe, []string) {
	n := len(probes)
	for _, bindings := range []map[string][]string{{"app.tools.expand": {"ctrl+e"}}, {"app.tools.expand": {"ctrl+e", "alt+x"}}, {"app.tools.expand": {}}} {
		for i := range n {
			if probes[i].Expanded || probes[i].Width != 80 || probes[i].Result == nil {
				continue
			}
			probe := probes[i]
			probe.Bindings = bindings
			probes = append(probes, probe)
			names = append(names, fmt.Sprintf("%s rebound expand %v", names[i], bindings))
		}
	}
	return probes, names
}

// installCardBindings installs the interactive mode's merged tui and app table with the probe's user bindings, which the hints read.
func installCardBindings(t *testing.T, probe toolCardProbe) {
	t.Helper()
	userBindings := map[string][]KeyID{}
	for action, keys := range probe.Bindings {
		userBindings[action] = []KeyID{}
		for _, key := range keys {
			userBindings[action] = append(userBindings[action], KeyID(key))
		}
	}
	NewKeybindingsManagerFromBindings(userBindings, "")
}
