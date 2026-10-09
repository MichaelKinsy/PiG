package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// kitTestToolCards installs tool cards drawn as a definition without
// renderers (the coding agent installs the built-in ones in production) and
// counts the cards made and disposed.
func kitTestToolCards(t *testing.T) (made, disposed *atomic.Int64) {
	t.Helper()
	previous := conversationRenderers.Load()
	t.Cleanup(func() { conversationRenderers.Store(previous) })
	made, disposed = &atomic.Int64{}, &atomic.Int64{}
	SetConversationRenderers(ConversationRenderers{
		NewToolCard: func(_ context.Context, toolName, _, cwd string, args json.RawMessage, _ string, _ func()) (*tui.ToolExecutionComponent, func()) {
			made.Add(1)
			return kitDirectToolCard(toolName, cwd, args), func() { disposed.Add(1) }
		},
		ToolDiff: func(string, map[string]any, any) *frontend.Diff { return nil },
	})
	return made, disposed
}

func kitDirectToolCard(toolName, cwd string, args json.RawMessage) *tui.ToolExecutionComponent {
	return tui.NewToolExecutionComponent(toolName, "", args, tui.ToolExecutionOptions{}, &tui.ToolDefinitionRenderers{}, nil, cwd)
}

// kitConversationView holds every conversation kind (spec §2.1).
const kitConversationView = `{"root":{"kind":"container","children":[` +
	`{"kind":"user-message","text":"Fix **the** kit build\n\n- one\n- two"},` +
	`{"kind":"assistant-message","id":"a1","isStreaming":true,"message":{"content":[{"type":"thinking","thinking":"Reading the *kit* file"},{"type":"text","text":"Done. **Bold** reply"}]}},` +
	`{"kind":"assistant-message","hideThinkingBlock":true,"hiddenThinkingLabel":"Pondering kit...","outputPad":0,"message":{"content":[{"type":"thinking","thinking":"secret"},{"type":"text","text":"Visible kit"}],"stopReason":"error","errorMessage":"kit-boom-7"}},` +
	`{"kind":"tool-execution","id":"t1","toolName":"kit_tool","toolCallId":"call-1","args":{"q":"x"},"cwd":"/work/kit","argsComplete":true,"executionStarted":true,"result":{"content":[{"type":"text","text":"answer 42"}]},"isPartial":false},` +
	`{"kind":"bash-execution","command":"ls -la","output":"a.txt\nb.txt","complete":{"exitCode":2}},` +
	`{"kind":"bash-execution","command":"seq 25","excludeFromContext":true,"output":"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n16\n17\n18\n19\n20\n21\n22\n23\n24\n25","complete":{"exitCode":0}},` +
	`{"kind":"diff","diff":" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail","filePath":"kit.go"}]}}`

// directConversation builds kitConversationView from the ports the main
// transcript draws with.
func directConversation() tui.Component {
	user := tui.NewUserMessageComponent("Fix **the** kit build\n\n- one\n- two", nil, 1, nil)
	streaming := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	streaming.SetContent([]tui.AssistantSegment{{Thinking: true, Text: "Reading the *kit* file"}, {Text: "Done. **Bold** reply"}})
	streaming.SetTerminalError("stop", "")
	failed := tui.NewAssistantMessageComponent(nil, true, nil, "", nil, nil)
	failed.SetHiddenThinkingLabel("Pondering kit...")
	failed.SetOutputPad(0)
	failed.SetContent([]tui.AssistantSegment{{Thinking: true, Text: "secret"}, {Text: "Visible kit"}})
	failed.SetTerminalError("error", "kit-boom-7")
	tool := kitDirectToolCard("kit_tool", "/work/kit", json.RawMessage(`{"q":"x"}`))
	tool.SetArgsComplete()
	tool.MarkExecutionStarted()
	tool.SetResult("answer 42", false, 0)
	two, zero := 2, 0
	exited := tui.NewBashExecutionComponent("ls -la", nil, false, 1)
	exited.AppendOutput("a.txt\nb.txt")
	exited.SetCompleteWithOutput(&two, false, false, exited.GetOutput(), "")
	seq := tui.NewBashExecutionComponent("seq 25", nil, true, 1)
	numbers := make([]string, 25)
	for i := range numbers {
		numbers[i] = strconv.Itoa(i + 1)
	}
	seq.AppendOutput(strings.Join(numbers, "\n"))
	seq.SetCompleteWithOutput(&zero, false, false, seq.GetOutput(), "")
	diff := tui.NewPaddedText(tui.RenderDiff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail"), 0, 0, nil)
	return tui.NewContainer(user, streaming, failed, tool, exited, seq, diff)
}

func TestViewKitDrawsConversationKindsWithTheTranscriptPorts(t *testing.T) {
	kitTestToolCards(t)
	s := newViewSurface("k", newViewImageStore())
	if err := s.accept(json.RawMessage(kitConversationView), nil); err != nil {
		t.Fatal(err)
	}
	direct := directConversation()
	for _, width := range []int{30, 72, 120} {
		if got, want := s.Render(width), direct.Render(width); !slices.Equal(got, want) {
			t.Fatalf("width %d:\n got %q\nwant %q", width, got, want)
		}
	}
}

func TestViewKitReportsConversationTranscriptNodes(t *testing.T) {
	kitTestToolCards(t)
	s := newViewSurface("k", newViewImageStore())
	if err := s.accept(json.RawMessage(kitConversationView), nil); err != nil {
		t.Fatal(err)
	}
	view := s.FrontendView(72)
	children := view.Root.Children
	if len(children) != 7 {
		t.Fatalf("children = %d, want 7", len(children))
	}
	want := map[int][]frontend.Node{
		0: {frontend.MarkdownText{Role: frontend.RoleUser, Text: "Fix **the** kit build\n\n- one\n- two"}},
		1: {frontend.Thinking{Text: "Reading the *kit* file"}, frontend.MarkdownText{Role: frontend.RoleAssistant, Text: "Done. **Bold** reply", Streaming: true}},
	}
	for i, nodes := range want {
		if !reflect.DeepEqual(children[i].Transcript, nodes) {
			t.Fatalf("child %d transcript = %#v, want %#v", i, children[i].Transcript, nodes)
		}
	}
	failed := children[2].Transcript
	if len(failed) != 3 || !reflect.DeepEqual(failed[0], frontend.Thinking{Text: "secret", Hidden: true}) || !strings.Contains(strings.Join(failed[2].(frontend.Lines).Lines, ""), "kit-boom-7") {
		t.Fatalf("failed message transcript = %#v", failed)
	}
	card, ok := children[3].Transcript[0].(frontend.ToolCard)
	if !ok || card.Name != "kit_tool" || card.Status != frontend.ToolDone || card.Output != "answer 42" || card.Arguments["q"] != "x" {
		t.Fatalf("tool transcript = %#v", children[3].Transcript)
	}
	if children[4].Kind != frontend.ViewKindBashExecution || children[4].Transcript != nil || children[4].Rows == 0 {
		t.Fatalf("bash node = %#v, want rows and no transcript nodes", children[4])
	}
	if d := children[6].Diff; d == nil || d.Path != "kit.go" || !strings.Contains(d.Text, "+2 new kit line") {
		t.Fatalf("diff node = %#v", children[6])
	}
}

// A node with an id keeps its component, as a Pi author keeps one and calls
// its update methods (spec §2.1): a thinking run the user clicked stays as
// clicked while the sender repeats itself, and changes apply as updates.
func TestViewKitKeepsConversationComponentsAcrossFrames(t *testing.T) {
	made, _ := kitTestToolCards(t)
	s := newViewSurface("k", newViewImageStore())
	s.sendEvent = func(ViewEventPayload) {}
	frame := func(raw string) {
		t.Helper()
		if err := s.accept(json.RawMessage(raw), nil); err != nil && !errors.Is(err, errViewUnchanged) {
			t.Fatal(err)
		}
	}
	assistant := func(text, extra string) string {
		return `{"root":{"kind":"assistant-message","id":"a"` + extra + `,"message":{"content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"` + text + `"}]}}}`
	}
	frame(assistant("one", ""))
	msg := s.instances["a"].assistant
	rows := s.Render(40)
	thinkingRow := slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, "plan") })
	if thinkingRow < 0 {
		t.Fatalf("no thinking row in %q", rows)
	}
	click := extension.RemoteMouseEvent{Type: "click", Button: "left", X: 2, Y: thinkingRow, Width: 40, Height: len(rows)}
	if result := s.HandleViewMouse(click); !result.Handled {
		t.Fatal("the thinking run did not take the click")
	}
	frame(assistant("one two", ""))
	if s.instances["a"].assistant != msg {
		t.Fatal("updated content did not keep the assistant component")
	}
	if got := strings.Join(s.Render(40), "\n"); strings.Contains(got, "plan") || !strings.Contains(got, "Thinking...") || !strings.Contains(got, "one two") {
		t.Fatalf("the update did not keep the clicked run hidden:\n%s", got)
	}
	frame(assistant("one two", `,"hideThinkingBlock":true`))
	if got := strings.Join(s.Render(40), "\n"); strings.Contains(got, "plan") || !strings.Contains(got, "Thinking...") {
		t.Fatalf("setHideThinkingBlock did not hide the run:\n%s", got)
	}
	frame(assistant("one two", ""))
	if got := strings.Join(s.Render(40), "\n"); !strings.Contains(got, "plan") {
		t.Fatalf("setHideThinkingBlock did not clear the click:\n%s", got)
	}
	if s.instances["a"].assistant != msg {
		t.Fatal("hideThinkingBlock did not keep the assistant component")
	}

	bash := func(output, extra string) string {
		return `{"root":{"kind":"bash-execution","id":"b","command":"build","output":"` + output + `"` + extra + `}}`
	}
	kept := func(output, complete string, exit *int) {
		t.Helper()
		want := tui.NewBashExecutionComponent("build", nil, false, 1)
		want.AppendOutput(output)
		if complete != "" {
			want.SetCompleteWithOutput(exit, false, false, want.GetOutput(), "")
		}
		if got := s.Render(40); !slices.Equal(got, want.Render(40)) {
			t.Fatalf("bash rows:\n got %q\nwant %q", got, want.Render(40))
		}
	}
	frame(bash("a\\n", ""))
	first := s.instances["b"].bash
	frame(bash("a\\nb", `,"complete":{"exitCode":1}`))
	one := 1
	kept("a\nb", "complete", &one)
	if s.instances["b"].bash != first {
		t.Fatal("growing output did not keep the bash component")
	}
	frame(bash("x", ""))
	if s.instances["b"].bash == first {
		t.Fatal("output that does not extend the last one kept the bash component")
	}
	kept("x", "", nil)

	tool := func(extra string) string {
		return `{"root":{"kind":"tool-execution","id":"t","toolName":"kit_tool","args":{"q":1}` + extra + `}}`
	}
	frame(tool(`,"executionStarted":true`))
	card := s.instances["t"].tool
	frame(tool(`,"executionStarted":true,"result":{"content":[{"type":"text","text":"done"}]},"isPartial":false`))
	if s.instances["t"].tool != card || made.Load() != 1 {
		t.Fatalf("a result did not keep the tool card (cards made: %d)", made.Load())
	}
	frame(tool(""))
	if s.instances["t"].tool == card {
		t.Fatal("executionStarted turning back off kept the tool card")
	}
}

func TestViewKitDisposesToolCardsItNoLongerDraws(t *testing.T) {
	_, disposed := kitTestToolCards(t)
	s := newViewSurface("k", newViewImageStore())
	frame := func(raw string) {
		t.Helper()
		if err := s.accept(json.RawMessage(raw), nil); err != nil {
			t.Fatal(err)
		}
	}
	cards := func(n string) string {
		return `{"root":{"kind":"container","children":[{"kind":"tool-execution","toolName":"a","args":{"n":` + n + `}},{"kind":"tool-execution","id":"kept","toolName":"b"}]}}`
	}
	frame(cards("1"))
	frame(cards("2"))
	if got := disposed.Load(); got != 1 {
		t.Fatalf("after a second frame %d cards were disposed, want the first frame's unkept card", got)
	}
	s.close()
	if got := disposed.Load(); got != 3 {
		t.Fatalf("after close %d cards were disposed, want all 3", got)
	}
}

func TestViewKitRejectsInvalidConversationNodes(t *testing.T) {
	kitTestToolCards(t)
	for _, raw := range []string{
		`{"root":{"kind":"user-message","text":"x","outputPad":2}}`,
		`{"root":{"kind":"assistant-message","message":{"content":[{"type":"image"}]}}}`,
		`{"root":{"kind":"assistant-message","message":{"content":[],"stopReason":"done"}}}`,
		`{"root":{"kind":"tool-execution"}}`,
		`{"root":{"kind":"tool-execution","toolName":"x","toolDefinition":"custom"}}`,
		`{"root":{"kind":"tool-execution","toolName":"x","imageWidthCells":0}}`,
		`{"root":{"kind":"tool-execution","toolName":"x","result":{"content":[{"type":"image","mimeType":"image/png"}]}}}`,
		`{"root":{"kind":"tool-execution","toolName":"x","result":{"content":[{"type":"thinking"}]}}}`,
		`{"root":{"kind":"loader","message":{"content":[]}}}`,
		`{"root":{"kind":"diff","children":[{"kind":"spacer"}]}}`,
	} {
		var v ViewPayload
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if _, err := validateView(&v, false); err == nil {
			t.Errorf("%s: accepted", raw)
		}
	}
	conversationRenderers.Store(nil)
	var v ViewPayload
	if err := json.Unmarshal([]byte(`{"root":{"kind":"tool-execution","toolName":"x"}}`), &v); err != nil {
		t.Fatal(err)
	}
	if _, err := validateView(&v, false); err == nil {
		t.Error("a tool-execution node was accepted without the coding agent's tool cards")
	}
}

// The wire's "message" is a loader's string or an assistant-message's
// object, and both survive a round trip.
func TestViewNodeMessageRoundTrips(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"loader","message":"Working"}`,
		`{"kind":"assistant-message","message":{"content":[{"type":"text","text":"hi"}],"stopReason":"length"}}`,
	} {
		var n ViewNode
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != raw {
			t.Errorf("round trip of %s = %s", raw, out)
		}
	}
}
