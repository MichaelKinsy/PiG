package tui_test

// P2 of p2-tui-port.md: pi's message and tool rendering, on components without a runtime; the plugins' tool cards are those their TUI facets register.
//
// pi: apps/p2/test/p2/tui-render.test.ts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	codemodetui "github.com/MichaelKinsy/PiG/apps/p2/plugins/codemode/tui"
	codingtui "github.com/MichaelKinsy/PiG/apps/p2/plugins/coding-tools/tui"
	powershelltui "github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func plainLines(lines []string) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(widthx.StripTerminalSequences(line), " ")
	}
	return strings.Join(out, "\n")
}

func darkRef(t *testing.T) *theme.ThemeRef {
	t.Helper()
	dark, _ := theme.BuiltinTheme("dark")
	built, err := theme.CreateTheme(dark, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	return theme.NewThemeRef(built)
}

// cardContext is the context of a component outside a runtime: pi's `ctx()` of the test.
type cardContext struct{ ref *theme.ThemeRef }

func (c cardContext) Theme() *theme.ThemeRef { return c.ref }
func (c cardContext) KeyText(id string) string {
	if id == "app.tools.expand" {
		return "ctrl+o"
	}
	return ""
}
func (cardContext) RequestRender()                                     {}
func (cardContext) Cwd() string                                        { return "/work" }
func (cardContext) MarkdownTransformers() []render.MarkdownTransformer { return nil }
func (cardContext) Settings() *prefs.TuiSettings                       { return nil }
func (cardContext) OutputPad() int                                     { return 1 }

// pluginRenderers are the tool cards of the plugins' TUI facets by tool name (`PLUGIN_TUI_RENDERERS`).
func pluginRenderers() map[string]*tools.ToolRenderers {
	renderers := map[string]*tools.ToolRenderers{"powershell": powershelltui.Renderers, "codemode": codemodetui.Card}
	for _, entry := range codingtui.Renderers() {
		renderers[entry.Key] = entry.Value
	}
	return renderers
}

func mustMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("%q does not match:\n%s", pattern, text)
	}
}

func mustLack(t *testing.T, text, part string) {
	t.Helper()
	if strings.Contains(text, part) {
		t.Fatalf("%q in:\n%s", part, text)
	}
}

func TestPisMessageComponentsMarkdownHiddenThinkingErrorNoticesSanitizedText(t *testing.T) {
	ctx := cardContext{darkRef(t)}
	message := tuitest.Assistant(ai.StopReasonStop, 1, ai.ThinkingContent{Thinking: "private reasoning"}, ai.TextContent{Text: "**Answer** with `code`\x1b]52;c;evil\x07"})
	hiddenComponent := components.NewAssistantMessageComponent(ctx, &message, true, "")
	hidden := plainLines(hiddenComponent.Render(60))
	mustMatch(t, `Answer with code`, hidden)
	mustMatch(t, `Thinking\.\.\.`, hidden)
	mustLack(t, hidden, "private reasoning")
	mustLack(t, strings.Join(hiddenComponent.Render(60), ""), "52;c")
	mustMatch(t, `private reasoning`, plainLines(components.NewAssistantMessageComponent(ctx, &message, false, "").Render(60)))
	failed := message
	failed.StopReason, failed.ErrorMessage = ai.StopReasonError, "fixture error"
	mustMatch(t, `Error: fixture error`, plainLines(components.NewAssistantMessageComponent(ctx, &failed, false, "").Render(60)))
	aborted := message
	aborted.Content, aborted.StopReason = nil, ai.StopReasonAborted
	mustMatch(t, `Operation aborted`, plainLines(components.NewAssistantMessageComponent(ctx, &aborted, false, "").Render(60)))
	truncated := message
	truncated.StopReason = ai.StopReasonLength
	mustMatch(t, `Response was truncated before completion\.`, plainLines(components.NewAssistantMessageComponent(ctx, &truncated, false, "").Render(60)))
	if got := render.SafeText("safe\x1b]52;c;evil\x07\x1b[2J\x00 text"); got != "safe text" {
		t.Fatalf("SafeText = %q", got)
	}
}

func TestToolCardsPisBashReadEditDiffGenericFallbackCodeModeWithNestedCallsThemeSwap(t *testing.T) {
	renderers := pluginRenderers()
	lines := make([]string, 15)
	for i := range lines {
		lines[i] = "output-" + string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	output := strings.Join(lines, "\n")
	// Two digits per line: "output-14" must not be a prefix of another line.
	output = strings.NewReplacer("output-00", "output-0", "output-01", "output-1", "output-02", "output-2", "output-03", "output-3", "output-04", "output-4",
		"output-05", "output-5", "output-06", "output-6", "output-07", "output-7", "output-08", "output-8", "output-09", "output-9").Replace(output)
	tool := func(patch func(*transcript.ToolView)) transcript.ToolView {
		view := transcript.ToolView{ID: "call", Name: "bash", Args: jsvalue.Obj("command", "pwd"), Status: transcript.ToolDone, Output: output}
		if patch != nil {
			patch(&view)
		}
		return view
	}
	ctx := cardContext{darkRef(t)}
	// bash: the newest 5 lines collapsed, everything expanded, the recorded duration.
	took := 1500.0
	bash := components.NewToolExecutionComponent(tool(func(v *transcript.ToolView) { v.DurationMs = &took }), renderers["bash"], ctx, true)
	collapsed := plainLines(bash.Render(60))
	mustMatch(t, `\$ pwd`, collapsed)
	mustMatch(t, `10 earlier lines, ctrl\+o to expand`, collapsed)
	mustMatch(t, `output-14`, collapsed)
	mustLack(t, collapsed, "output-9")
	mustMatch(t, `Took 1\.5s`, collapsed)
	bash.SetExpanded(true)
	mustMatch(t, `output-0\n`, plainLines(bash.Render(60)))
	for _, line := range bash.Render(60) {
		if widthx.VisibleWidth(line) > 60 {
			t.Fatalf("wider than 60: %q", line)
		}
	}
	// A running call: pending background, elapsed clock; the card's timer stops on dispose.
	running := components.NewToolExecutionComponent(tool(func(v *transcript.ToolView) { v.Status, v.Output = transcript.ToolRunning, "partial" }), renderers["bash"], ctx, true)
	mustMatch(t, `(?s)partial.*Elapsed`, plainLines(running.Render(60)))
	running.Dispose()
	// Unknown tools: pi's generic call line and the first 10 result lines.
	generic := plainLines(components.NewToolExecutionComponent(tool(func(v *transcript.ToolView) { v.Name, v.Args = "mcp_thing", jsvalue.Obj("q", 1.0) }), nil, ctx, true).Render(80))
	mustMatch(t, `mcp_thing q=1`, generic)
	mustMatch(t, `5 more lines, ctrl\+o to expand`, generic)
	mustLack(t, generic, "output-14")
	// read: path relative to the session cwd; content only when expanded.
	read := components.NewToolExecutionComponent(tool(func(v *transcript.ToolView) {
		v.Name, v.Args, v.Output = "read", jsvalue.Obj("path", "/work/src/a.ts", "offset", 3.0, "limit", 2.0), "const a = 1;"
	}), renderers["read"], ctx, true)
	mustMatch(t, `read src/a\.ts:3-4`, plainLines(read.Render(80)))
	mustLack(t, plainLines(read.Render(80)), "const a")
	read.SetExpanded(true)
	mustMatch(t, `const a = 1;`, plainLines(read.Render(80)))
	// edit: the result's diff, no local preview.
	edit := plainLines(components.NewToolExecutionComponent(tool(func(v *transcript.ToolView) {
		v.Name, v.Args, v.Output = "edit", jsvalue.Obj("path", "a.ts"), "Successfully replaced 1 block(s) in a.ts."
		v.Details = jsvalue.Obj("diff", "-1 const a = 1;\n+1 const a = 2;")
	}), renderers["edit"], ctx, true).Render(80))
	mustMatch(t, `edit a\.ts`, edit)
	mustMatch(t, `-1 const a = 1;\s*\n\s*\+1 const a = 2;`, edit)
	// Code mode: the script, nested calls (depth, live output, failures) and output without Durable's header.
	code := "// read every file\nconst files = await tools.read({ path: 'a.ts' });\nreturn files.length;"
	forty := 40.0
	codemode := components.NewToolExecutionComponent(tool(func(v *transcript.ToolView) {
		v.Name, v.Args, v.Status, v.Output = "codemode", jsvalue.Obj("code", code), transcript.ToolRunning, ""
		v.Calls = []transcript.NestedCall{
			{ID: "c1", Name: "read", Args: jsvalue.Obj("path", "a.ts"), Status: transcript.ToolDone, DurationMs: &forty},
			{ID: "c2", Name: "read", Args: jsvalue.Obj("path", "f3.ts"), Status: transcript.ToolError, Error: "ENOENT: f3.ts"},
			{ID: "c3", Name: "bash", Args: jsvalue.Obj("command", "npm test"), Status: transcript.ToolRunning, Depth: 1, Output: "build\nrunning tests\n"},
		}
	}), renderers["codemode"], ctx, true)
	live := plainLines(codemode.Render(100))
	mustMatch(t, `codemode\s*\n\s*// read every file`, live)
	mustMatch(t, `✓ read \{"path":"a\.ts"\} 40ms`, live)
	mustMatch(t, `✗ read \{"path":"f3\.ts"\}\n +ENOENT: f3\.ts`, live)
	mustMatch(t, `  … bash \{"command":"npm test"\}\n +running tests`, live)
	codemode.Update(tool(func(v *transcript.ToolView) {
		v.Name, v.Args, v.Status = "codemode", jsvalue.Obj("code", code), transcript.ToolDone
		v.Output = "Script completed\nWall time 0.1 seconds\nOutput:\n\n3"
		v.Calls = []transcript.NestedCall{{ID: "c1", Name: "read", Args: jsvalue.Obj("path", "a.ts"), Status: transcript.ToolDone, DurationMs: &forty}}
	}), true)
	done := plainLines(codemode.Render(100))
	mustMatch(t, `\n +3\n`, done)
	mustLack(t, done, "Script completed")
	// Baked colors follow a theme swap (section 5.4).
	ref := darkRef(t)
	card := components.NewToolExecutionComponent(tool(nil), renderers["bash"], cardContext{ref}, true)
	dark := strings.Join(card.Render(60), "\n")
	lightJSON, _ := theme.BuiltinTheme("light")
	light, err := theme.CreateTheme(lightJSON, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	ref.Set(light)
	if strings.Join(card.Render(60), "\n") == dark {
		t.Fatal("the card did not follow the theme swap")
	}
}

func TestTranscriptStreamingAnswerBecomesItsEntryInPlaceToolLifecycleMarkersAndP2Notices(t *testing.T) {
	call := ai.ToolCall{ID: "t1", Name: "bash", Arguments: ai.JsonObject{"command": "ls"}}
	whole := func(docs harness.ViewDocs, entries ...durable.EntryRecord) transcript.Transcript {
		view := harness.TranscriptView{Docs: docs}
		for _, entry := range entries {
			view.Items = append(view.Items, harness.TranscriptEntry{Entry: entry})
		}
		return transcript.BuildTranscript(view, nil, nil)
	}
	options := tui.TranscriptOptions{Renderers: pluginRenderers()}
	partial := tuitest.Assistant(ai.StopReasonStop, 7, ai.TextContent{Text: "Work"}, call)
	streaming := whole(tuitest.DocsOf(t, "pi.live", harness.LiveState{Run: &harness.LiveRun{TaskId: 1}, Generation: &harness.LiveGeneration{Message: &partial}}), tuitest.UserEntry(1, "hi"))
	component, err := tui.TranscriptComponent(&streaming, false, false, options)
	if err != nil {
		t.Fatal(err)
	}
	mustMatch(t, `(?s)hi.*Work.*\$ ls`, plainLines(component.Render(80)))
	toolUse := tuitest.Assistant(ai.StopReasonToolUse, 7, ai.TextContent{Text: "Work"}, call)
	answerEntry := tuitest.Entry(2, "pi.assistant", toolUse)
	output := "a.txt"
	slot := harness.ToolSlot{CallId: "t1", Name: "bash", Status: harness.ToolSlotRunning, Output: &output}
	running := whole(tuitest.DocsOf(t, "pi.live", harness.LiveState{Run: &harness.LiveRun{TaskId: 1}, Tools: []harness.ToolSlot{slot}}), tuitest.UserEntry(1, "hi"), answerEntry)
	component.Sync(&running, nil)
	mustMatch(t, `(?s)\$ ls.*a\.txt.*Elapsed`, plainLines(component.Render(80)))
	took := int64(20)
	result := tuitest.Entry(3, "pi.tool-result", ai.ToolResultMessage{ToolCallID: "t1", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "a.txt\nb.txt"}}, DurationMs: &took, Timestamp: 9})
	modelError := tuitest.Entry(5, "app.model-error")
	modelError.Data = delta.JsonObjectOf("detail", "provider down")
	settled := whole(harness.ViewDocs{}, tuitest.UserEntry(1, "hi"), answerEntry, result,
		tuitest.Entry(4, "pi.compaction", ai.UserMessage{Content: ai.UserText("Summary of earlier work"), Timestamp: 10}), modelError,
		tuitest.Entry(6, "pi.reset", ai.UserMessage{Content: ai.UserText("Handoff note"), Timestamp: 11}))
	component.Sync(&settled, nil)
	shown := plainLines(component.Render(80))
	mustMatch(t, `(?s)b\.txt.*Took 0\.0s`, shown)
	mustMatch(t, `(?s)\[compaction\].*Earlier context summarized \(`, shown)
	mustMatch(t, `The model could not answer this input\. provider down`, shown)
	mustMatch(t, `(?s)\[new context\].*Handoff note`, shown)
	component.SetState(components.ViewState{Expanded: true})
	mustMatch(t, `Summary of earlier work`, plainLines(component.Render(80)))
	// An answer that never ran its calls: interrupted cards.
	aborted := whole(harness.ViewDocs{}, tuitest.Entry(1, "pi.assistant", tuitest.Assistant(ai.StopReasonAborted, 1, call)))
	interrupted, _ := tui.TranscriptComponent(&aborted, false, false, options)
	mustMatch(t, `Not run: the answer was interrupted\.`, plainLines(interrupted.Render(80)))
	// Compaction failures are state notices.
	failure := whole(tuitest.DocsOf(t, "pi.live", harness.LiveState{CompactionFailure: &harness.CompactionFailure{TaskId: 3, Error: "too big"}}))
	failed, _ := tui.TranscriptComponent(&failure, false, false, options)
	mustMatch(t, `Compaction failed: too big`, plainLines(failed.Render(80)))
}
