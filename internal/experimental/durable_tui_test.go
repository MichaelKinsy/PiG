package experimental

// pi: packages/coding-agent/src/experimental/durable/tui.ts

// pi: packages/coding-agent/src/experimental/vacation/tui.ts

import (
	"context"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// durableTuiFixture is a durableTui on a recording renderer, with the calls its handlers received.
type durableTuiFixture struct {
	t        *testing.T
	view     *durableTui
	keys     *codingagent.KeybindingsManager
	calls    []string
	forced   int
	observed *clientTuiObservation
}

func (fixture *durableTuiFixture) ForceFullRender() { fixture.forced++ }

func newDurableTuiFixture(t *testing.T) *durableTuiFixture {
	t.Helper()
	isolateExperimentalTest(t)
	tui.SetTUIKeybindings(tui.NewTUIKeybindingsManager(nil))
	// The theme is process-global; an earlier test may leave the system theme (no colors) active.
	tui.SetTheme("dark")
	fixture := &durableTuiFixture{t: t, observed: newClientTuiObservation(t), keys: codingagent.NewKeybindingsManager(codingagent.AgentDir())}
	record := func(name string) func() { return func() { fixture.calls = append(fixture.calls, name) } }
	fixture.view = newDurableTui(t.Context(), durableTuiOptions{
		CWD: t.TempDir(), UI: fixture, Keybindings: fixture.keys,
		Handlers: durableTuiHandlers{
			submit:        func(text string) { fixture.calls = append(fixture.calls, "submit:"+text) },
			followUp:      func(text string) { fixture.calls = append(fixture.calls, "followUp:"+text) },
			abort:         record("abort"),
			exit:          record("exit"),
			selectModel:   record("selectModel"),
			cycleThinking: record("cycleThinking"),
		},
		RequestRender: fixture.observed.RequestRender, RunOnMain: fixture.observed.Executor.RunOnMain,
	})
	t.Cleanup(fixture.view.Stop)
	return fixture
}

// lines is the document, ANSI stripped, trimmed of trailing blanks per line.
func (fixture *durableTuiFixture) lines() []string {
	fixture.t.Helper()
	var lines []string
	for _, line := range fixture.view.Render(200) {
		lines = append(lines, strings.TrimRight(widthx.StripAnsi(line), " "))
	}
	return lines
}

func (fixture *durableTuiFixture) text() string { return strings.Join(fixture.lines(), "\n") }

func (fixture *durableTuiFixture) apply(state durableagent.DurableView) {
	fixture.t.Helper()
	if err := fixture.view.Apply(state); err != nil {
		fixture.t.Fatal(err)
	}
}

func stateOf(entries []durable.EntryRecord, live *harness.LiveState, inbox *harness.InboxState) durableagent.DurableView {
	view := conversationView(entries, live, inbox)
	view.Docs = harness.ViewDocsOf(view.Docs, "pi.agent", jsonObjectOf(map[string]any{"model": map[string]any{"provider": "scripted", "modelId": "model"}, "thinkingLevel": "low"}))
	return durableagent.DurableView{
		Session:       durableagent.SessionSummary{ID: "s", Directory: "/sessions/s", CWD: "/work/project"},
		Conversation:  view,
		Conversations: []durableagent.ConversationSummary{{ID: durable.ROOT_CONVERSATION_ID, Label: "main"}},
		Models:        []durableagent.ModelSummary{{ModelRef: durable.ModelRef{Provider: "scripted", ModelId: "model"}, Name: "Plain", ContextWindow: 1000}},
	}
}

func textMessage(text string) ai.AssistantMessage {
	return ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop}
}

func contains(lines []string, want string) bool {
	return slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, want) })
}

// tui.ts:290-360 and 420-470: new entries are appended; the streaming answer's component becomes its entry's, so the answer is shown once.
func TestDurableTuiShowsTheTranscriptAndFollowsTheStreamingAnswer(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	partial := ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streaming words"}}}
	fixture.apply(stateOf([]durable.EntryRecord{userEntry(1, "a question")}, &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}, Generation: generationOf(partial)}, nil))
	text := fixture.text()
	if !strings.Contains(text, "a question") || !strings.Contains(text, "streaming words") {
		t.Fatalf("transcript:\n%s", text)
	}
	before := fixture.view.chat.ChildCount()
	fixture.apply(stateOf([]durable.EntryRecord{userEntry(1, "a question"), assistantEntry(2, textMessage("streaming words done"))}, nil, nil))
	if got := strings.Count(fixture.text(), "streaming words"); got != 1 {
		t.Fatalf("the answer is shown %d times:\n%s", got, fixture.text())
	}
	if fixture.view.chat.ChildCount() != before {
		t.Fatal("the settled answer did not reuse the streaming component")
	}
	if !reflect.DeepEqual(fixture.view.renderedEntryIds, []durable.EntryId{1, 2}) {
		t.Fatalf("rendered ids %v", fixture.view.renderedEntryIds)
	}
	if fixture.forced != 0 {
		t.Fatalf("an append forced %d full repaints", fixture.forced)
	}
}

// tui.ts:343-348 and 405-425: compaction and a reset replace the head of the active transcript, which rebuilds it, repaints the whole screen and shows its end.
func TestDurableTuiRebuildsWhenTheHeadChanges(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	fixture.apply(stateOf([]durable.EntryRecord{userEntry(1, "old question"), assistantEntry(2, textMessage("old answer"))}, nil, nil))
	forcedBefore := fixture.forced
	fixture.apply(stateOf([]durable.EntryRecord{resetEntry(7), userEntry(8, "new question")}, nil, nil))
	text := fixture.text()
	if strings.Contains(text, "old question") || !strings.Contains(text, "[new context]") || !strings.Contains(text, "new question") {
		t.Fatalf("transcript:\n%s", text)
	}
	if fixture.forced != forcedBefore+1 {
		t.Fatalf("forced repaints %d, want one more than %d", fixture.forced, forcedBefore)
	}
	// The next unchanged view does not repaint again.
	fixture.apply(stateOf([]durable.EntryRecord{resetEntry(7), userEntry(8, "new question")}, nil, nil))
	if fixture.forced != forcedBefore+1 {
		t.Fatalf("an unchanged view forced a repaint")
	}
}

func compactionEntry(id durable.EntryId, summary string) durable.EntryRecord {
	return durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.compaction", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(summary)}}}
}

// tui.ts:115-140 and 175-186: a compaction summary is one line with the expand key until expanded, then its text; the expand key toggles it.
func TestDurableTuiCollapsesCompactionSummariesUntilExpanded(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	fixture.apply(stateOf([]durable.EntryRecord{compactionEntry(3, "the summary body"), userEntry(4, "next")}, nil, nil))
	key := fixture.keys.KeyText("app.tools.expand")
	collapsed := fixture.text()
	if !strings.Contains(collapsed, "[compaction]") || !strings.Contains(collapsed, "Earlier context summarized ("+key+" to expand)") || strings.Contains(collapsed, "the summary body") {
		t.Fatalf("collapsed:\n%s", collapsed)
	}
	fixture.view.HandleInput("\x0f")
	expanded := fixture.text()
	if !strings.Contains(expanded, "the summary body") || strings.Contains(expanded, "to expand)") {
		t.Fatalf("expanded:\n%s", expanded)
	}
	// A summary added while expanded starts expanded.
	fixture.apply(stateOf([]durable.EntryRecord{compactionEntry(3, "the summary body"), userEntry(4, "next"), compactionEntry(5, "second summary")}, nil, nil))
	if !strings.Contains(fixture.text(), "second summary") {
		t.Fatalf("a summary added while expanded is collapsed:\n%s", fixture.text())
	}
	fixture.view.HandleInput("\x0f")
	if strings.Contains(fixture.text(), "the summary body") || strings.Contains(fixture.text(), "second summary") {
		t.Fatalf("the second toggle did not collapse:\n%s", fixture.text())
	}
}

func graphOf(nodes ...harness.TaskGraphNode) *harness.TaskGraph {
	graph := &harness.TaskGraph{Tasks: map[string]harness.TaskGraphNode{}}
	for _, node := range nodes {
		graph.Tasks[itoa(int(node.Id))] = node
	}
	return graph
}

// tui.ts:215-243: the task panel lists the live tasks, a conversation-owned task under the task that owns its conversation and owned tasks under their owner.
func TestDurableTuiTaskPanelNestsTasksUnderTheirOwners(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	owner := durable.TaskId(2)
	state := stateOf(nil, nil, nil)
	state.Tasks = graphOf(
		harness.TaskGraphNode{Id: 10, Kind: "pi.generation", ConversationId: 1, State: harness.TaskGraphState{Status: durable.TaskRunning, Phase: "request"}, Conversations: nil},
		harness.TaskGraphNode{Id: 2, Kind: "pi.tool", ConversationId: 1, Background: true, State: harness.TaskGraphState{Status: durable.TaskWaiting, On: []durable.TaskId{3, 4}}, Conversations: []durable.ConversationId{5, 6}},
		harness.TaskGraphNode{Id: 3, Kind: "pi.generation", ConversationId: 5, State: harness.TaskGraphState{Status: durable.TaskRunning, Phase: "stream"}},
		harness.TaskGraphNode{Id: 4, Kind: "pi.compaction", ConversationId: 1, Owner: &owner, AbortRequested: true, State: harness.TaskGraphState{Status: durable.TaskCompleting, Outcome: durable.OutcomeFailed}},
	)
	fixture.apply(state)
	var panel []string
	for _, line := range fixture.lines() {
		if strings.Contains(line, "Tasks (") || strings.Contains(line, "#") && strings.Contains(line, ":") && strings.Contains(line, "pi.") {
			panel = append(panel, line)
		}
	}
	want := []string{
		" Tasks (4 live, /tasks to hide)",
		"   pi.tool #2: waiting on 3, 4 [background] owns conversation 5, 6",
		"     pi.generation #3: running stream",
		"     pi.compaction #4: completing (failed) [aborting]",
		"   pi.generation #10: running request",
	}
	if !reflect.DeepEqual(panel, want) {
		t.Fatalf("task panel:\n%q\nwant\n%q", panel, want)
	}
	// Closing the panel removes it.
	state.Tasks = nil
	fixture.apply(state)
	if contains(fixture.lines(), "Tasks (") {
		t.Fatal("the task panel stays after it closed")
	}
}

// tui.ts:245-266: queued items show their mode and text; only the newest four notices show, by level.
func TestDurableTuiShowsTheQueueAndTheNewestNotices(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	inbox := &harness.InboxState{Items: []harness.InboxItem{
		{Id: 1, Mode: harness.InboxFollowUp, Content: "after   this"},
		{Id: 2, Mode: harness.InboxWrite, Entry: delta.JsonObjectOf("kind", "pi.reset")},
	}}
	state := stateOf(nil, nil, inbox)
	for index, message := range []string{"one", "two", "three", "four", "five"} {
		level := []durableagent.NoticeLevel{durableagent.NoticeInfo, durableagent.NoticeWarning, durableagent.NoticeError}[index%3]
		state.Notices = append(state.Notices, durableagent.Notice{ID: index + 1, Level: level, Message: message})
	}
	fixture.apply(state)
	lines := fixture.lines()
	if !contains(lines, "[followUp] after   this") || !contains(lines, "[write] <pi.reset>") {
		t.Fatalf("queue not shown:\n%s", fixture.text())
	}
	if contains(lines, "one") || !contains(lines, "two") || !contains(lines, "five") {
		t.Fatalf("notices:\n%s", fixture.text())
	}
}

// tui.ts:268-290: the editor's border carries what the conversation waits for.
func TestDurableTuiStatusInTheEditorBorder(t *testing.T) {
	for _, test := range []struct {
		name string
		live *harness.LiveState
		want string
	}{
		{"working", &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}}, "Working... (esc to abort)"},
		{"running tool", &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}, Tools: []harness.ToolSlot{{CallId: "c", Name: "bash", Status: harness.ToolSlotRunning}}}, "Running bash... (esc to abort)"},
		{"deferred", &harness.LiveState{Generation: &harness.LiveGeneration{Deferred: &harness.DeferredStatus{}}}, "Waiting for deferred response..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDurableTuiFixture(t)
			fixture.apply(stateOf(nil, test.live, nil))
			if !contains(fixture.lines(), test.want) {
				t.Fatalf("no %q in:\n%s", test.want, fixture.text())
			}
			// The status clears with the work.
			fixture.apply(stateOf(nil, nil, nil))
			if contains(fixture.lines(), test.want) {
				t.Fatalf("%q stays after the work ended", test.want)
			}
		})
	}
}

// tui.ts:292-325: the footer shows the tokens, cost and context fill of the conversation, and the conversation, model, thinking level and keys.
func TestDurableTuiFooter(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	answer := textMessage("hi")
	answer.Usage = ai.Usage{Input: 800, Output: 12_500, TotalTokens: 920}
	state := stateOf([]durable.EntryRecord{userEntry(1, "q"), assistantEntry(2, answer)}, nil, nil)
	state.Conversation.Docs = harness.ViewDocsOf(state.Conversation.Docs, "pi.usage", jsonObjectOf(harness.UsageState{
		Models: map[string]ai.Usage{"scripted/model": {Input: 800, Output: 12_500, CacheRead: 1250, Cost: ai.UsageCost{Total: 0.0125}}},
		Tools:  map[string]ai.Usage{"bash": {Input: 200, Cost: ai.UsageCost{Total: 0.0005}}},
	}))
	fixture.apply(state)
	lines := fixture.lines()
	// 920 of 1000 context tokens is over ninety percent.
	if !contains(lines, "↑1.0k ↓13k R1.3k $0.013 92.0%/1.0k  /work/project") {
		t.Fatalf("footer stats:\n%s", fixture.text())
	}
	hints := " · scripted/model · thinking:low (" + fixture.keys.KeyText("app.thinking.cycle") + ") · " + fixture.keys.KeyText("app.model.select") + " or /model · /agents · /compact · /tasks · " + fixture.keys.KeyText("app.message.followUp") + " follow-up · " + fixture.keys.KeyText("app.clear") + " exit"
	if !contains(lines, "main"+hints) {
		t.Fatalf("footer hints:\n%s\nwant main%s", fixture.text(), hints)
	}
	// Before an answer the context fill is unknown.
	fresh := stateOf(nil, nil, nil)
	fixture.apply(fresh)
	if !contains(fixture.lines(), "$0.000 ?%/1.0k") {
		t.Fatalf("footer without an answer:\n%s", fixture.text())
	}
	// A subagent's conversation is named by its label.
	fresh.Conversation.Conversation.Id = 9
	fresh.Conversations = append(fresh.Conversations, durableagent.ConversationSummary{ID: 9, Label: "subagent 9"})
	fixture.apply(fresh)
	if !contains(fixture.lines(), "subagent 9 · scripted/model") {
		t.Fatalf("footer label:\n%s", fixture.text())
	}
}

// tui.ts:166-186: a running call that started a subagent says so until it has output.
func TestDurableTuiNamesTheSubagentOfARunningCall(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	details := durable.JsonValue(map[string]any{"conversationId": float64(5)})
	live := &harness.LiveState{Run: &harness.LiveRun{TaskId: 1}, Tools: []harness.ToolSlot{{CallId: "c1", Name: "subagent", Status: harness.ToolSlotRunning, Details: &details}}}
	fixture.apply(stateOf(nil, live, nil))
	if !contains(fixture.lines(), "Subagent 5 is working. /agents switches to it.") {
		t.Fatalf("transcript:\n%s", fixture.text())
	}
}

type recordingController struct {
	mu    sync.Mutex
	calls []string
}

func (controller *recordingController) record(call string) durableagent.Completion {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.calls = append(controller.calls, call)
	done := make(chan struct{})
	close(done)
	return done
}

func (controller *recordingController) Submit(text string, whenBusy durable.WhenBusy) durableagent.Completion {
	return controller.record("submit:" + text + ":" + string(whenBusy))
}
func (controller *recordingController) Compact(instructions *string) durableagent.Completion {
	if instructions == nil {
		return controller.record("compact:<none>")
	}
	return controller.record("compact:" + *instructions)
}
func (controller *recordingController) Abort() durableagent.Completion {
	return controller.record("abort")
}
func (controller *recordingController) CycleThinking() durableagent.Completion {
	return controller.record("cycleThinking")
}
func (controller *recordingController) SetModel(model durable.ModelRef) durableagent.Completion {
	return controller.record("setModel:" + model.Provider + "/" + model.ModelId)
}
func (controller *recordingController) ToggleTasks() durableagent.Completion {
	return controller.record("toggleTasks")
}
func (controller *recordingController) SwitchConversation(id durable.ConversationId) durableagent.Completion {
	return controller.record("switch:" + itoa(int(id)))
}

type staticSource struct{ state durableagent.DurableView }

func (source staticSource) Current() durableagent.DurableView { return source.state }
func (staticSource) Subscribe(func()) func()                  { return func() {} }

func newCommandFixture(t *testing.T) (*durableTuiFixture, *recordingController, durableCommands) {
	t.Helper()
	fixture := newDurableTuiFixture(t)
	controller := &recordingController{}
	state := stateOf([]durable.EntryRecord{userEntry(1, "q")}, nil, nil)
	state.Models = []durableagent.ModelSummary{
		{ModelRef: durable.ModelRef{Provider: "alpha", ModelId: "small"}, Name: "Small"},
		{ModelRef: durable.ModelRef{Provider: "scripted", ModelId: "model"}, Name: "Plain"},
		{ModelRef: durable.ModelRef{Provider: "beta", ModelId: "large/x"}, Name: "Large"},
	}
	title := "look into  the weather"
	state.Conversations = []durableagent.ConversationSummary{{ID: 1, Label: "main"}, {ID: 7, Label: "subagent 7", Title: &title}, {ID: 9, Label: "subagent 9"}}
	return fixture, controller, durableCommands{source: staticSource{state}, controller: controller, view: func() *durableTui { return fixture.view }}
}

// tui.ts:560-585: the prompt line routes slash commands, and anything else steers the busy conversation.
func TestDurableTuiSubmitRoutesSlashCommands(t *testing.T) {
	fixture, controller, commands := newCommandFixture(t)
	for _, line := range []string{"  ", "/tasks", "/compact", "/compact   keep the plan  ", "/compactx", "hello there ", "/models"} {
		commands.submit(line)
	}
	want := []string{"toggleTasks", "compact:<none>", "compact:keep the plan", "submit:/compactx:steer", "submit:hello there:steer", "submit:/models:steer"}
	if !reflect.DeepEqual(controller.calls, want) {
		t.Fatalf("calls = %q, want %q", controller.calls, want)
	}
	if fixture.view.selector != nil {
		t.Fatal("no selector was asked for")
	}
}

// tui.ts:518-558: /model lists the models with the current one first; the choice splits at the first slash; Esc restores the editor.
func TestDurableTuiModelSelector(t *testing.T) {
	fixture, controller, commands := newCommandFixture(t)
	commands.submit("/model")
	if fixture.view.selector == nil {
		t.Fatal("/model did not open the selector")
	}
	text := fixture.text()
	if !strings.Contains(text, "Select model:") {
		t.Fatalf("selector:\n%s", text)
	}
	order := []string{}
	for _, line := range fixture.lines() {
		for _, id := range []string{"small", "model", "large/x"} {
			if strings.Contains(line, id) && !strings.Contains(line, "Select") {
				order = append(order, id)
			}
		}
	}
	if !reflect.DeepEqual(order, []string{"model", "small", "large/x"}) {
		t.Fatalf("model order %v, want the current model first:\n%s", order, text)
	}
	// Type to filter, then choose the match.
	for _, key := range []string{"l", "a", "r"} {
		fixture.view.HandleInput(key)
	}
	fixture.view.HandleInput("\r")
	if !reflect.DeepEqual(controller.calls, []string{"setModel:beta/large/x"}) {
		t.Fatalf("calls = %q", controller.calls)
	}
	if fixture.view.selector != nil {
		t.Fatal("the editor was not restored after the choice")
	}
	commands.submit("/model")
	fixture.view.HandleInput("\x1b")
	if fixture.view.selector != nil || len(controller.calls) != 1 {
		t.Fatalf("cancel: selector %v, calls %q", fixture.view.selector, controller.calls)
	}
}

// tui.ts:540-558: /agents lists the conversations newest first, marks the shown one and gives each its task; the choice switches to it.
func TestDurableTuiConversationSelector(t *testing.T) {
	fixture, controller, commands := newCommandFixture(t)
	commands.submit("/agents")
	lines := fixture.lines()
	var rows []string
	for _, line := range lines {
		if strings.Contains(line, "subagent") || strings.Contains(line, "main") && strings.Contains(line, "(shown)") {
			rows = append(rows, strings.TrimSpace(line))
		}
	}
	if len(rows) != 3 || !strings.Contains(rows[0], "subagent 9") || !strings.Contains(rows[1], "subagent 7") || !strings.Contains(rows[2], "main") {
		t.Fatalf("rows %q in:\n%s", rows, fixture.text())
	}
	if !strings.Contains(rows[2], "(shown)") || !strings.Contains(rows[1], "look into  the weather") {
		t.Fatalf("rows %q", rows)
	}
	// Down once, then confirm: subagent 7.
	fixture.view.HandleInput("\x1b[B")
	fixture.view.HandleInput("\r")
	if !reflect.DeepEqual(controller.calls, []string{"switch:7"}) {
		t.Fatalf("calls = %q", controller.calls)
	}
}

// custom-editor.ts:56-109: the app keys run their handlers; the interrupt and exit keys only when they apply; everything else edits.
func TestDurableTuiKeys(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	for _, key := range []string{"\x1b", "\x04", "\x03", "\x1b[Z", "\x0c"} {
		fixture.view.HandleInput(key)
	}
	want := []string{"abort", "exit", "exit", "cycleThinking", "selectModel"}
	if !reflect.DeepEqual(fixture.calls, want) {
		t.Fatalf("calls = %q, want %q", fixture.calls, want)
	}
	// Ctrl+D with text deletes forward instead of exiting; follow-up sends the text and clears the editor.
	fixture.calls = nil
	for _, key := range []string{"h", "i"} {
		fixture.view.HandleInput(key)
	}
	fixture.view.HandleInput("\x04")
	if len(fixture.calls) != 0 || fixture.view.editor.Text() != "hi" {
		t.Fatalf("ctrl+d with text: calls %q text %q", fixture.calls, fixture.view.editor.Text())
	}
	followUpKey := "\x1b\r" // Pi's Windows default follow-up key is ctrl+q.
	if runtime.GOOS == "windows" {
		followUpKey = "\x11"
	}
	fixture.view.HandleInput(followUpKey)
	if !reflect.DeepEqual(fixture.calls, []string{"followUp:hi"}) || fixture.view.editor.Text() != "" {
		t.Fatalf("follow-up: calls %q text %q", fixture.calls, fixture.view.editor.Text())
	}
	// Follow-up with an empty editor does nothing.
	fixture.calls = nil
	fixture.view.HandleInput(followUpKey)
	if len(fixture.calls) != 0 {
		t.Fatalf("an empty follow-up called %q", fixture.calls)
	}
	// Enter submits the line through the handler.
	for _, key := range []string{"o", "k", "\r"} {
		fixture.view.HandleInput(key)
	}
	if !reflect.DeepEqual(fixture.calls, []string{"submit:ok"}) {
		t.Fatalf("submit: %q", fixture.calls)
	}
	_ = context.Background
}

// tui.ts:327-345: the context size is that of the newest successful answer after the newest compaction.
func TestContextTokens(t *testing.T) {
	answer := func(id durable.EntryId, usage ai.Usage, stop ai.StopReason) durable.EntryRecord {
		message := textMessage("a")
		message.Usage, message.StopReason = usage, stop
		return assistantEntry(id, message)
	}
	for _, test := range []struct {
		name    string
		entries []durable.EntryRecord
		want    int
		known   bool
	}{
		{"no answer", []durable.EntryRecord{userEntry(1, "q")}, 0, false},
		{"the newest answer", []durable.EntryRecord{answer(1, ai.Usage{TotalTokens: 100}, ai.StopReasonStop), answer(2, ai.Usage{TotalTokens: 250}, ai.StopReasonStop)}, 250, true},
		{"the sum of the parts without a total", []durable.EntryRecord{answer(1, ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40}, ai.StopReasonStop)}, 100, true},
		{"an aborted or failed answer does not measure", []durable.EntryRecord{answer(1, ai.Usage{TotalTokens: 100}, ai.StopReasonStop), answer(2, ai.Usage{TotalTokens: 999}, ai.StopReasonAborted), answer(3, ai.Usage{TotalTokens: 998}, ai.StopReasonError)}, 100, true},
		// Kept entries follow the summary in the view but are older than it.
		{"nothing after a compaction is unknown", []durable.EntryRecord{compactionEntry(5, "s"), answer(2, ai.Usage{TotalTokens: 400}, ai.StopReasonStop)}, 0, false},
		{"an answer after the compaction measures the new context", []durable.EntryRecord{compactionEntry(5, "s"), answer(2, ai.Usage{TotalTokens: 400}, ai.StopReasonStop), answer(6, ai.Usage{TotalTokens: 70}, ai.StopReasonStop)}, 70, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, known := contextTokens(test.entries)
			if got != test.want || known != test.known {
				t.Fatalf("contextTokens = %d, %v; want %d, %v", got, known, test.want, test.known)
			}
		})
	}
}

// tui.ts:312-316: a context over ninety percent full is shown in the error color, and not below it.
func TestDurableTuiFooterColorsAFullContext(t *testing.T) {
	for _, test := range []struct {
		tokens  int
		colored bool
	}{{900, false}, {901, true}} {
		fixture := newDurableTuiFixture(t)
		answer := textMessage("a")
		answer.Usage = ai.Usage{TotalTokens: test.tokens}
		fixture.apply(stateOf([]durable.EntryRecord{assistantEntry(1, answer)}, nil, nil))
		var raw string
		for _, line := range fixture.view.Render(200) {
			if strings.Contains(widthx.StripAnsi(line), "/work/project") {
				raw = line
			}
		}
		percent := tui.JSToFixed(float64(test.tokens)/10, 1) + "%/1.0k"
		colored := tui.ActiveTheme().Fg("error", percent)
		if got := strings.Contains(raw, colored); got != test.colored {
			t.Fatalf("%d tokens: error color %v, want %v in %q", test.tokens, got, test.colored, raw)
		}
	}
}

// tui.ts:185-190 and 345-350: the editor's border takes the color of the conversation's thinking level.
func TestDurableTuiEditorBorderFollowsTheThinkingLevel(t *testing.T) {
	fixture := newDurableTuiFixture(t)
	theme := tui.ActiveTheme()
	border := func(level string) string {
		state := stateOf(nil, nil, nil)
		state.Conversation.Docs = harness.ViewDocsOf(state.Conversation.Docs, "pi.agent", jsonObjectOf(map[string]any{"thinkingLevel": level}))
		fixture.apply(state)
		for _, line := range fixture.view.editorSlot.Render(80) {
			if strings.Contains(widthx.StripAnsi(line), "───") {
				return line
			}
		}
		t.Fatalf("no border for %s", level)
		return ""
	}
	low, high := border("low"), border("high")
	if !strings.Contains(low, theme.GetFgAnsi("thinkingLow")) || !strings.Contains(high, theme.GetFgAnsi("thinkingHigh")) || strings.Contains(high, theme.GetFgAnsi("thinkingLow")) {
		t.Fatalf("borders %q and %q do not carry the colors of their levels", low, high)
	}
}

// tui.ts:473-496: a card created while tool output is expanded starts expanded.
func TestDurableTuiNewToolCardsFollowTheExpandedState(t *testing.T) {
	var output strings.Builder
	for line := 1; line <= 40; line++ {
		output.WriteString("output line " + itoa(line) + "\n")
	}
	entries := func() []durable.EntryRecord {
		call := ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c1", Name: "bash", Arguments: map[string]any{"command": "seq 40"}}}, StopReason: ai.StopReasonToolUse}
		result := ai.ToolResultMessage{ToolCallID: "c1", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output.String()}}}
		return []durable.EntryRecord{
			assistantEntry(1, call),
			{Id: 2, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.tool-result", Model: []ai.Message{result}},
		}
	}
	collapsed := newDurableTuiFixture(t)
	collapsed.apply(stateOf(entries(), nil, nil))
	if contains(collapsed.lines(), "output line 5\n") || contains(collapsed.lines(), "output line 2 ") {
		t.Fatalf("collapsed output:\n%s", collapsed.text())
	}
	expanded := newDurableTuiFixture(t)
	expanded.view.HandleInput("\x0f")
	expanded.apply(stateOf(entries(), nil, nil))
	if !contains(expanded.lines(), "output line 1") || !contains(expanded.lines(), "output line 5") {
		t.Fatalf("expanded output:\n%s", expanded.text())
	}
	if contains(collapsed.lines(), "output line 5") {
		t.Fatalf("the collapsed card shows its fifth line:\n%s", collapsed.text())
	}
}
