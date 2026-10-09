package vacation

// pi: packages/coding-agent/src/experimental/vacation/vacation.ts

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

func fastSearches(t *testing.T) {
	t.Helper()
	previous := searchStep
	searchStep = time.Millisecond
	t.Cleanup(func() { searchStep = previous })
}

func vacationRegistry(t *testing.T) harness.Registry {
	t.Helper()
	registry := harness.CreateRegistry()
	for _, extension := range []*durable.Extension{Vacation, Search} {
		if err := registry.Install(extension); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}

func toolResultText(message ai.ToolResultMessage) string {
	var text strings.Builder
	for _, content := range message.Content {
		if block, ok := content.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// weatherOutput is what a weather search in Vienna prints.
const weatherOutput = "searching weather in Vienna: source 1/3\nsearching weather in Vienna: source 2/3\nsearching weather in Vienna: source 3/3\n- Saturday: 24°C and sunny\n- Sunday: 21°C, a short shower around 3 pm\n"

func resultText(entries []durable.EntryRecord, call string) string {
	for _, entry := range entries {
		if len(entry.Model) == 0 {
			continue
		}
		if message, ok := entry.Model[0].(ai.ToolResultMessage); ok && message.ToolCallID == call {
			return toolResultText(message)
		}
	}
	return "<no result>"
}

func userTexts(entries []durable.EntryRecord) []string {
	var texts []string
	for _, entry := range slices.Backward(entries) {
		if len(entry.Model) == 0 {
			continue
		}
		if message, ok := entry.Model[0].(ai.UserMessage); ok {
			if text, ok := message.Content.(ai.UserText); ok {
				texts = append(texts, string(text))
			}
		}
	}
	return texts
}

// vacation.ts:34-53: a search prints one line per source while it works, then the canned results; the result is that output.
func TestSearchPrintsItsSourcesThenTheResults(t *testing.T) {
	fastSearches(t)
	agent := openFauxAgent(t, storage.NewMemoryStorage(), vacationRegistry(t), t.TempDir(),
		callStep("search", map[string]any{"topic": "weather", "city": "Vienna"}, "call-1"),
		answerStep("done"),
	)
	if settled := prompt(t, agent.root, "weather?"); settled.Status != durable.SubmissionDone {
		t.Fatalf("status = %s", settled.Status)
	}
	want := weatherOutput
	if got := resultText(entriesOf(t, agent.root), "call-1"); got != want {
		t.Fatalf("result = %q\nwant %q", got, want)
	}
}

// vacation.ts:20-31: the source counts are the topic's seconds over the two-second step, rounded up.
func TestSearchSourcesPerTopic(t *testing.T) {
	fastSearches(t)
	for topic, sources := range map[string]string{"museums": "5/5", "trains": "15/15"} {
		agent := openFauxAgent(t, storage.NewMemoryStorage(), vacationRegistry(t), t.TempDir(),
			callStep("search", map[string]any{"topic": topic, "city": "Graz"}, "c"),
			answerStep("done"),
		)
		prompt(t, agent.root, "search")
		if got := resultText(entriesOf(t, agent.root), "c"); !strings.Contains(got, "searching "+topic+" in Graz: source "+sources+"\n") {
			t.Errorf("%s: result = %q, want the last source %s", topic, got, sources)
		}
	}
}

// The search schema admits only its three topics: any other topic is an invalid call the Harness reports to the model, and nothing searches.
func TestSearchRejectsAnUnknownTopic(t *testing.T) {
	fastSearches(t)
	agent := openFauxAgent(t, storage.NewMemoryStorage(), vacationRegistry(t), t.TempDir(),
		callStep("search", map[string]any{"topic": "nightlife", "city": "Vienna"}, "bad"),
		answerStep("done"),
	)
	prompt(t, agent.root, "nightlife?")
	if got := resultText(entriesOf(t, agent.root), "bad"); strings.Contains(got, "searching") || got == "<no result>" {
		t.Fatalf("result = %q, want a validation failure", got)
	}
}

// requestSteps answers every request of the planner and its research subagent from the transcript, so the order of their requests does not matter: the planner starts the research, acknowledges it, and plans from the report; the subagent searches, then summarizes.
func requestSteps(count int, onSubagentRequest func(), plannerAck func(context.Context)) []ai.FauxResponseStep {
	step := ai.FauxFactoryStep(func(transcript ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		messages := transcript.Messages()
		system := ai.GetCurrentSystemPrompt(messages)
		last := messages[len(messages)-1]
		text := func(content ai.UserContent) string {
			if value, ok := content.(ai.UserText); ok {
				return string(value)
			}
			return ""
		}
		switch {
		case strings.Contains(system, "research subagent"):
			if onSubagentRequest != nil {
				onSubagentRequest()
			}
			if result, ok := last.(ai.ToolResultMessage); ok {
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(toolResultText(result))}}.AssistantMessage(), nil
			}
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("search", map[string]any{"topic": "weather", "city": "Vienna"}, &ai.FauxToolCallOptions{ID: "search-1"})}, StopReason: "toolUse"}.AssistantMessage(), nil
		case strings.Contains(system, "vacation planning assistant"):
			if user, ok := last.(ai.UserMessage); ok && strings.HasPrefix(text(user.Content), "[research report]") {
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Your plan: enjoy the sun.")}}.AssistantMessage(), nil
			}
			if _, ok := last.(ai.ToolResultMessage); ok {
				if plannerAck != nil {
					plannerAck(options.Signal)
				}
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("I started the research.")}}.AssistantMessage(), nil
			}
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("research", map[string]any{"task": "Vienna weekend weather"}, &ai.FauxToolCallOptions{ID: "research-1"})}, StopReason: "toolUse"}.AssistantMessage(), nil
		}
		return ai.FauxResponse{}.AssistantMessage(), context.Canceled
	})
	steps := make([]ai.FauxResponseStep, count)
	for i := range steps {
		steps[i] = step
	}
	return steps
}

func assistantTexts(entries []durable.EntryRecord) []string {
	var texts []string
	for _, entry := range slices.Backward(entries) {
		if len(entry.Model) == 0 {
			continue
		}
		if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
			for _, content := range message.Content {
				if block, ok := content.(ai.TextContent); ok {
					texts = append(texts, block.Text)
				}
			}
		}
	}
	return texts
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// vacation.ts:56-125: the research tool returns at once; a background task hands the request to a subagent that only has search, and posts the subagent's report to the planner as a new message that the planner turns into a plan.
func TestResearchRunsInTheBackgroundAndReportsBack(t *testing.T) {
	fastSearches(t)
	agent := openFauxAgent(t, storage.NewMemoryStorage(), vacationRegistry(t), t.TempDir(), requestSteps(5, nil, nil)...)
	// The planner is the root conversation of the vacation profile: only the vacation planner is selected.
	if err := agent.root.Configure(context.Background(), Profile.RootAgent(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	settled := prompt(t, agent.root, "Plan a weekend in Vienna")
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("status = %s", settled.Status)
	}
	main := entriesOf(t, agent.root)
	if got := resultText(main, "research-1"); got != "Research started in the background." {
		t.Fatalf("research result = %q", got)
	}
	eventually(t, "the plan", func() bool {
		return slices.Contains(userTexts(entriesOf(t, agent.root)), "[research report] "+weatherOutput)
	})
	eventually(t, "the planner's plan", func() bool {
		return slices.Contains(assistantTexts(entriesOf(t, agent.root)), "Your plan: enjoy the sun.")
	})
	// The research task completed with a null result, and the subagent's conversation holds only the search tool and its instructions.
	owners, err := durable.Commit(context.Background(), agent.harness, func(tx durable.Tx) ([]durable.ConversationRecord, error) {
		page, err := tx.ScanConversations(durable.ConversationQuery{}, 10, nil)
		return page.Items, err
	})
	if err != nil || len(owners) != 2 || owners[1].Id == 1 || owners[1].Owner == nil {
		t.Fatalf("conversations = %+v, %v; want the planner's and one owned by the research task", owners, err)
	}
	var research *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	eventually(t, "the research task to complete", func() bool {
		research, err = agent.harness.GetTask(context.Background(), owners[1].Owner.TaskId)
		return err == nil && research != nil && research.State.Status == durable.TaskTerminal
	})
	if research.Kind != "vacation.research" || !research.Background || research.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("research task = %+v", research)
	}
	// The planner itself has only research: search belongs to the subagent.
	planner, err := agent.root.Agent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var plannerTools []string
	for _, tool := range planner.Tools {
		plannerTools = append(plannerTools, tool.Name)
	}
	if !slices.Equal(plannerTools, []string{"research"}) {
		t.Fatalf("the planner's tools = %v, want only research", plannerTools)
	}
	child, err := agent.harness.Conversation(context.Background(), owners[1].Id)
	if err != nil || child == nil {
		t.Fatalf("the subagent's conversation: %v %v", child, err)
	}
	subagent, err := child.Agent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range subagent.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Equal(names, []string{"search"}) || subagent.Instructions == nil || !strings.HasPrefix(*subagent.Instructions, "You are a research subagent for a vacation planner.") {
		t.Fatalf("subagent tools %v instructions %v", names, subagent.Instructions)
	}
	if got := userTexts(entriesOf(t, child)); !slices.Equal(got, []string{"Vienna weekend weather"}) {
		t.Fatalf("the subagent received %v", got)
	}
}

// liveToolRunning reports whether any conversation of the Session has a tool call running.
func liveToolRunning(t *testing.T, agent *fauxAgent, ids ...durable.ConversationId) bool {
	t.Helper()
	for _, id := range ids {
		live, err := durable.Snapshot[harness.LiveState](context.Background(), agent.harness, harness.LiveDoc, id)
		if err != nil {
			t.Fatal(err)
		}
		if live == nil {
			continue
		}
		for _, slot := range live.Tools {
			if slot.Status == harness.ToolSlotRunning {
				return true
			}
		}
	}
	return false
}

func conversationIds(t *testing.T, agent *fauxAgent) []durable.ConversationId {
	t.Helper()
	records, err := durable.Commit(context.Background(), agent.harness, func(tx durable.Tx) ([]durable.ConversationRecord, error) {
		page, err := tx.ScanConversations(durable.ConversationQuery{}, 10, nil)
		return page.Items, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []durable.ConversationId
	for _, record := range records {
		ids = append(ids, record.Id)
	}
	return ids
}

// vacation.ts:84-98 and 100-105: a Harness closed while a search runs resumes after the next open. The search is safe to rerun and runs again; request IDs keep the restart from sending the request to the subagent or the report to the planner twice.
func TestResearchSurvivesARestart(t *testing.T) {
	database := filepath.Join(t.TempDir(), "session.sqlite")
	open := func(steps ...ai.FauxResponseStep) *fauxAgent {
		store, err := sqlitenode.OpenNodeSqliteStorage(database, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return openFauxAgent(t, store, vacationRegistry(t), t.TempDir(), steps...)
	}
	// The first search takes an hour: the Harness is closed in the middle of it.
	previous := searchStep
	searchStep = time.Hour
	t.Cleanup(func() { searchStep = previous })
	first := open(requestSteps(3, nil, nil)...)
	submission, err := first.root.Submit(context.Background(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Plan a weekend in Vienna")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := submission.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the subagent's search to run", func() bool { return liveToolRunning(t, first, conversationIds(t, first)...) })
	// Close writes no outcome: the interrupted search resumes with the next open.
	if err := first.harness.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	searchStep = time.Millisecond
	second := open(requestSteps(4, nil, nil)...)
	second.harness.Resume()
	eventually(t, "the report after the restart", func() bool {
		return slices.Contains(userTexts(entriesOf(t, second.root)), "[research report] "+weatherOutput)
	})
	eventually(t, "the planner's plan", func() bool {
		return slices.Contains(assistantTexts(entriesOf(t, second.root)), "Your plan: enjoy the sun.")
	})
	reports := 0
	for _, text := range userTexts(entriesOf(t, second.root)) {
		if strings.HasPrefix(text, "[research report]") {
			reports++
		}
	}
	if reports != 1 {
		t.Fatalf("%d reports, want exactly one", reports)
	}
	ids := conversationIds(t, second)
	child, err := second.harness.Conversation(context.Background(), ids[len(ids)-1])
	if err != nil || child == nil {
		t.Fatalf("subagent conversation: %v %v", child, err)
	}
	if got := userTexts(entriesOf(t, child)); !slices.Equal(got, []string{"Vienna weekend weather"}) {
		t.Fatalf("the subagent received %v, want its request once", got)
	}
}

// vacation.ts:109-111: the report is posted as a follow-up, so a planner that is answering when it arrives finishes that turn first.
func TestResearchReportQueuesAsAFollowUpWhileThePlannerIsBusy(t *testing.T) {
	fastSearches(t)
	gate := make(chan struct{})
	acknowledged := make(chan struct{}, 1)
	agent := openFauxAgent(t, storage.NewMemoryStorage(), vacationRegistry(t), t.TempDir(), requestSteps(5, nil, func(signal context.Context) {
		acknowledged <- struct{}{}
		select {
		case <-gate:
		case <-signal.Done():
		}
	})...)
	submission, err := agent.root.Submit(context.Background(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Plan a weekend in Vienna")})
	if err != nil {
		t.Fatal(err)
	}
	<-acknowledged
	// The planner is busy with its acknowledgement while the research finishes.
	var queued harness.InboxItem
	eventually(t, "the report in the planner's inbox", func() bool {
		inbox, err := durable.Snapshot[harness.InboxState](context.Background(), agent.harness, harness.InboxDoc, agent.root.Id())
		if err != nil {
			t.Fatal(err)
		}
		if inbox == nil || len(inbox.Items) == 0 {
			return false
		}
		queued = inbox.Items[0]
		return true
	})
	if queued.Mode != harness.InboxFollowUp {
		t.Fatalf("queued report mode = %q, want %q", queued.Mode, harness.InboxFollowUp)
	}
	close(gate)
	if _, err := submission.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the planner's plan", func() bool {
		return slices.Contains(assistantTexts(entriesOf(t, agent.root)), "Your plan: enjoy the sun.")
	})
}

// fakeReportRuntime is the part of the task runtime the report phase uses: it hands out a planner handle that records its submissions and takes the final commit.
type fakeReportRuntime struct {
	researchRuntime
	planner   *recordingHandle
	committed *researchNext
}

func (runtime *fakeReportRuntime) ConversationId() durable.ConversationId { return 7 }
func (runtime *fakeReportRuntime) Conversation(_ context.Context, id durable.ConversationId) (durable.ConversationHandle, error) {
	runtime.planner.asked = id
	return runtime.planner, nil
}
func (runtime *fakeReportRuntime) Commit(_ context.Context, change func(durable.Tx, researchRecord) (*researchNext, error)) error {
	next, err := change(nil, researchRecord{})
	runtime.committed = next
	return err
}

type recordingHandle struct {
	durable.ConversationHandle
	asked  durable.ConversationId
	drafts []durable.InputSubmissionDraft
}

func (handle *recordingHandle) Submit(_ context.Context, draft durable.InputSubmissionDraft) (durable.Submission, error) {
	handle.drafts = append(handle.drafts, draft)
	return nil, nil
}

// vacation.ts:107-114: the report phase posts the report to the conversation that owns the task as a follow-up with a request ID of the task, so a rerun after a crash does not post it twice, then completes the task with a null result.
func TestReportPhasePostsTheReportOnceAndCompletes(t *testing.T) {
	runtime := &fakeReportRuntime{planner: &recordingHandle{}}
	task := researchRecord{Id: 41, State: durable.TaskState[researchState, durable.JsonValue]{Checkpoint: &researchState{Phase: "report", Report: "[research report] sunny"}}}
	if err := report(context.Background(), task, runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.planner.asked != 7 {
		t.Fatalf("the report went to conversation %d, want the task's conversation 7", runtime.planner.asked)
	}
	if len(runtime.planner.drafts) != 1 {
		t.Fatalf("%d submissions, want one", len(runtime.planner.drafts))
	}
	draft := runtime.planner.drafts[0]
	if draft.Type != durable.SubmissionTypeInput || draft.Content != ai.UserContent(ai.UserText("[research report] sunny")) || draft.WhenBusy != durable.WhenBusyFollowUp || draft.RequestId == nil || *draft.RequestId != "research-report:41" {
		t.Fatalf("draft = %+v", draft)
	}
	if next := runtime.committed; next == nil || next.Status != durable.TaskTerminal || next.Outcome == nil || next.Outcome.Status != durable.OutcomeCompleted || next.Outcome.Result == nil || *next.Outcome.Result != nil {
		t.Fatalf("committed = %+v, want a terminal completed outcome with a null result", next)
	}
}

// harness-setup.ts:49-54: the registry has the vacation planner and its research subagent's search, and nothing else: no coding tools, no pi prompt.
func TestCreateVacationRegistry(t *testing.T) {
	registry, err := CreateVacationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, extension := range registry.Snapshot().Installed() {
		names = append(names, extension.Name)
	}
	if !slices.Equal(names, []string{"vacation", "vacation-search"}) {
		t.Fatalf("installed = %v", names)
	}
}
