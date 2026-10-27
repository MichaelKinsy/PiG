// Ports packages/durable/test/harness-view-run.test.ts: what a renderer sees of a run through the conversation view, the
// transcript window and pi.live, every change a commit makes, rebuilt from operations alone. It replaced the event stream
// (watchEvents, Pi #11327).
//
// Go mappings forced by language mechanics. An in-flight partial is compared by content where Pi compares object identity, as a
// view revision's partial is decoded per read. The two cases that measure V8's heap (a cost per commit that follows the new text,
// and a watcher's retained memory) have no Go form: a Go revision holds its own copy of each string, so the allocation per commit and
// the memory of recorded frames grow with the strings (the append behavior they protect is asserted by
// partial_changes_upstream_test.go).

package harness

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// viewListening is a started watch of a conversation that records every call and, with check, checks that its operations turn the
// value before it into its value. The frames are what a renderer that keeps up gets.
type viewListening struct {
	watch      ConversationWatch
	mu         sync.Mutex
	frames     []viewFrame
	mismatches int
}

func listenView(t *testing.T, conversation Conversation, check bool) *viewListening {
	t.Helper()
	watch := must(conversation.Watch(testContext, nil))
	listening := &viewListening{watch: watch}
	replayed := orderedJSONOf(t, watch.Value())
	watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
		listening.mu.Lock()
		defer listening.mu.Unlock()
		if check {
			ops := orderedJSONOf(t, change.Ops).([]any)
			converted := make([]durable.Op, len(ops))
			for index, op := range ops {
				converted[index] = op.([]any)
			}
			next, err := delta.ApplyImmutable(replayed, converted)
			if err != nil || !reflect.DeepEqual(jsonOf(t, next), jsonOf(t, change.Value)) {
				listening.mismatches++
			} else {
				replayed = next
			}
		}
		listening.frames = append(listening.frames, viewFrame{value: change.Value, ops: change.Ops})
		return nil
	})
	return listening
}

func (listening *viewListening) snapshot() ([]viewFrame, int) {
	listening.mu.Lock()
	defer listening.mu.Unlock()
	return slices.Clone(listening.frames), listening.mismatches
}

func slowSetup(t *testing.T) *chatState {
	return chatSetup(t, ai.FauxConfig{TokensPerSecond: 400, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
}

// partialLog is the committed partials of a generation, one per commit that has one.
type partialLog struct {
	mu       sync.Mutex
	partials []ai.AssistantMessage
}

func (log *partialLog) all() []ai.AssistantMessage {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.partials)
}

// assistantFromJSON decodes the generation message of a pi.live document.
func assistantFromJSON(value any) ai.AssistantMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var message ai.AssistantMessage
	if err := json.Unmarshal(encoded, &message); err != nil {
		panic(err)
	}
	return message
}

// partialsOf records the committed partials of the Harness's generations (harness-view-run.test.ts partialsOf).
func partialsOf(harness Harness) *partialLog {
	log := &partialLog{}
	observeCommits(harness, func(_ context.Context, publication session.ObservedCommit) {
		for _, change := range documentChanges(publication.Changes) {
			if change.Record.Kind != "pi.live" || change.Value == nil {
				continue
			}
			generation, _ := change.Value.Value("generation").(*delta.JsonObject)
			if generation == nil {
				continue
			}
			if message, present := generation.Get("message"); present {
				log.mu.Lock()
				log.partials = append(log.partials, assistantFromJSON(message))
				log.mu.Unlock()
			}
		}
	})
	return log
}

// partialsIn returns the distinct in-flight partials in states, in order.
func partialsIn(states []LiveState) []ai.AssistantMessage {
	var partials []ai.AssistantMessage
	for _, state := range states {
		if state.Generation == nil || state.Generation.Message == nil {
			continue
		}
		if len(partials) == 0 || !reflect.DeepEqual(*state.Generation.Message, partials[len(partials)-1]) {
			partials = append(partials, *state.Generation.Message)
		}
	}
	return partials
}

func messageTexts(messages []ai.AssistantMessage) []string {
	texts := make([]string, len(messages))
	for index, message := range messages {
		texts[index], _ = textOf(message)
	}
	return texts
}

func lastEntry(view ConversationView) durable.EntryRecord { return view.Entries[len(view.Entries)-1] }

// inboxIn returns the queued inputs of a view's pi.inbox document, as id and mode.
func inboxIn(t *testing.T, view ConversationView) []string {
	t.Helper()
	document, present := view.Docs.Get("pi.inbox")
	if !present {
		return nil
	}
	var inbox struct {
		Items []struct {
			Id   durable.SubmissionId `json:"id"`
			Mode string               `json:"mode"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(jsonText(t, document)), &inbox); err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, item := range inbox.Items {
		items = append(items, jsonText(t, map[string]any{"id": item.Id, "mode": item.Mode}))
	}
	return items
}

func lastSlot(slots []*slotView) *slotView {
	if len(slots) == 0 {
		return nil
	}
	return slots[len(slots)-1]
}

func TestARunAsTheConversationViewShowsIt(t *testing.T) {
	t.Run("streams the answer as operations that rebuild every committed partial, then appends it", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:81
		setup := slowSetup(t)
		text := strings.Repeat("streamed answer text ", 20)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(text)})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		partials := partialsOf(harness)
		history := viewHistory(t, harness, root.Id())
		listening := listenView(t, root, true)
		submission := submit(t, root, inputDraft("hi"))
		must(submission.Wait(testContext))
		drained(harness)
		history.Detach()
		if _, mismatches := listening.snapshot(); mismatches != 0 {
			t.Fatalf("%d frames whose operations did not rebuild their value", mismatches)
		}
		states := history.Live()
		committed := partials.all()
		if len(committed) <= 1 {
			t.Fatalf("%d committed partials, want more than one", len(committed))
		}
		expectSameJSON(t, messageTexts(partialsIn(states)), messageTexts(committed))
		// The run shows from the input's commit until the commit that appends the answer and clears the partial.
		views := history.All()
		started := slices.IndexFunc(states, func(state LiveState) bool { return state.Run != nil })
		if started < 0 || states[started].Run.TaskId == 0 || !slices.Equal(states[started].Run.Inputs, []durable.SubmissionId{submission.Id()}) {
			t.Fatalf("run at %d = %+v, want the submission's", started, states[started].Run)
		}
		if kind := lastEntry(views[started]).Kind; kind != "pi.user" {
			t.Fatalf("the run starts with an entry of kind %s, want pi.user", kind)
		}
		ended := liveIndexAfter(states, started, func(state LiveState) bool { return state.Run == nil })
		if ended < 0 || states[ended].Generation != nil {
			t.Fatalf("run ends at %d with generation %+v", ended, states[ended].Generation)
		}
		if answer, _ := textOf(lastEntry(views[ended]).Model[0]); answer != text {
			t.Fatalf("the ending commit appends %q, want the answer", answer)
		}
		expectEqualJSON(t, states[len(states)-1], `{}`)
		must(listening.watch.Stop())
		closeHarness(t, harness)
	})

	t.Run("shows a tool call's slot from pending through its running output to its result entry", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:111
		setup := chatSetup(t)
		gate := deferred()
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "print", Description: "Prints", Parameters: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}, "required": []any{"n"}}},
			Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				api.Output("one\n")
				time.Sleep(150 * time.Millisecond)
				api.Output("two\n")
				if err := gate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{}, nil
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("print", map[string]any{"n": 1}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
			fauxAnswer("done"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		history := viewHistory(t, harness, root.Id())
		submission := submit(t, root, inputDraft("go"))
		waitFor(t, func() bool {
			slot := lastSlot(slotsOf(history.Live(), "c1"))
			return slot != nil && slot.Output != nil && *slot.Output == "one\ntwo\n"
		})
		gate.resolve()
		must(submission.Wait(testContext))
		history.Detach()
		views := history.All()
		slots := slotsOf(history.Live(), "c1")
		listed := slices.IndexFunc(slots, func(slot *slotView) bool { return slot != nil })
		// Listed pending with the tool-calling answer, in the same commit.
		if slot := slots[listed]; slot.CallId != "c1" || slot.Name != "print" || slot.Status != ToolSlotPending || slot.TaskId == nil || slot.Output != nil || slot.Entry != nil {
			t.Fatalf("slot when listed = %+v", slot)
		}
		if kind := lastEntry(views[listed]).Kind; kind != "pi.assistant" {
			t.Fatalf("the slot is listed with an entry of kind %s, want pi.assistant", kind)
		}
		var outputs []string
		for _, slot := range slots {
			if slot != nil && slot.Status == ToolSlotRunning {
				output := ""
				if slot.Output != nil {
					output = *slot.Output
				}
				if !slices.Contains(outputs, output) {
					outputs = append(outputs, output)
				}
			}
		}
		expectStrings(t, outputs, []string{"", "one\n", "one\ntwo\n"})
		// Done with its result entry, appended in the same commit.
		done := slices.IndexFunc(slots, func(slot *slotView) bool { return slot != nil && slot.Status == ToolSlotDone })
		if slot := slots[done]; slot.CallId != "c1" || slot.Name != "print" || slot.TaskId == nil || slot.Entry == nil || *slot.Entry != lastEntry(views[done]).Id {
			t.Fatalf("done slot = %+v, want the result entry %d", slot, lastEntry(views[done]).Id)
		}
		expectMatch(t, lastEntry(views[done]).Model[0], `{"role":"toolResult","toolCallId":"c1"}`)
		// The run's two generations, the tool round and the answer, hand the run over.
		runTasks := map[durable.TaskId]bool{}
		for _, state := range history.Live() {
			if state.Run != nil {
				runTasks[state.Run.TaskId] = true
			}
		}
		if len(runTasks) != 2 {
			t.Fatalf("%d run tasks, want 2", len(runTasks))
		}
		closeHarness(t, harness)
	})

	t.Run("shows queued inputs in the inbox, a retry backoff, and the next run", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:158
		setup := chatSetup(t)
		release := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			fauxAfter(release, "first"),
			ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "503 Service Unavailable"}),
			fauxAnswer("second"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(1), BaseDelayMs: new(1)}
		})
		history := viewHistory(t, harness, root.Id())
		first := submit(t, root, inputDraft("a"))
		followUp := submit(t, root, inputDraft("f"))
		views := history.All()
		expectSameJSON(t, inboxIn(t, views[len(views)-1]), []string{jsonText(t, map[string]any{"id": followUp.Id(), "mode": "followUp"})})
		release.resolve()
		must(followUp.Wait(testContext))
		history.Detach()
		states := history.Live()
		if !slices.ContainsFunc(states, func(state LiveState) bool {
			return state.Generation != nil && state.Generation.Retry != nil && state.Generation.Retry.Error == "503 Service Unavailable"
		}) {
			t.Fatal("no view shows the retry backoff")
		}
		retried := slices.IndexFunc(states, func(state LiveState) bool { return state.Generation != nil && state.Generation.Retry != nil })
		if !slices.ContainsFunc(states[retried:], func(state LiveState) bool { return state.Generation != nil && state.Generation.Attempt == 2 }) {
			t.Fatal("no view shows the second attempt")
		}
		var runs [][]durable.SubmissionId
		for index, state := range states {
			if state.Run != nil && (index == 0 || states[index-1].Run == nil || !slices.Equal(state.Run.Inputs, states[index-1].Run.Inputs)) {
				runs = append(runs, state.Run.Inputs)
			}
		}
		if len(runs) == 0 || !slices.Equal(runs[0], []durable.SubmissionId{first.Id()}) || !slices.Equal(runs[len(runs)-1], []durable.SubmissionId{followUp.Id()}) {
			t.Fatalf("runs %v, want the first input's, then the follow-up's", runs)
		}
		if items := inboxIn(t, views[len(views)-1]); len(items) != 0 {
			t.Fatalf("inbox %v at the start, want none after the run", items)
		}
		closeHarness(t, harness)
	})

	t.Run("rebuilds every committed partial of thinking, text, and tool-call arguments from the view's operations", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:197
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 150, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
				ai.FauxThinking(strings.Repeat("thinking about it ", 10)),
				ai.FauxText(strings.Repeat("some text ", 10)),
				ai.FauxToolCall("missing", map[string]any{"path": strings.Repeat("a/long/path/", 10), "note": strings.Repeat("x", 60)}, &ai.FauxToolCallOptions{ID: "c1"}),
			}, StopReason: "toolUse"}),
			fauxAnswer("done"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		partials := partialsOf(harness)
		recording := &opsRecording{}
		observation := must(harness.ObserveConversation(testContext, root.Id(), recording, nil))
		must(submit(t, root, inputDraft("go")).Wait(testContext))
		observation.Detach()
		// Replaying the operations alone, from the observation's value, gives every committed partial.
		replayed := orderedJSONOf(t, observation.Value())
		var rebuilt []any
		for _, batch := range recording.batches() {
			ops := orderedJSONOf(t, batch.ops).([]any)
			converted := make([]durable.Op, len(ops))
			for index, op := range ops {
				converted[index] = op.([]any)
			}
			next, err := delta.ApplyImmutable(replayed, converted)
			if err != nil {
				t.Fatal(err)
			}
			replayed = next
			if partial := generationMessageOf(t, replayed); partial != nil && (len(rebuilt) == 0 || !reflect.DeepEqual(partial, rebuilt[len(rebuilt)-1])) {
				rebuilt = append(rebuilt, partial)
			}
		}
		batches := recording.batches()
		expectSameJSON(t, replayed, batches[len(batches)-1].value)
		committed := partials.all()
		if len(committed) <= 2 {
			t.Fatalf("%d committed partials, want more than two", len(committed))
		}
		var rebuiltContent, committedContent []any
		for _, partial := range rebuilt {
			rebuiltContent = append(rebuiltContent, partial.(map[string]any)["content"])
		}
		for _, partial := range committed {
			committedContent = append(committedContent, jsonOf(t, partial.Content))
		}
		expectSameJSON(t, rebuiltContent, committedContent)
		// Growing text and thinking travel as appends.
		appends := 0
		for _, batch := range batches {
			for _, op := range batch.ops {
				if path, _ := op[1].([]any); op[0] == "a" && len(path) > 2 && path[2] == "generation" {
					appends++
				}
			}
		}
		if appends <= 2 {
			t.Fatalf("%d appends on the generation, want more than two", appends)
		}
		closeHarness(t, harness)
	})

	t.Run("rebuilds a sliding tail window from output trims and appends", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:262
		setup := chatSetup(t)
		gate := deferred()
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema:   ai.ToolSchema{Name: "tail", Description: "Prints lines", Parameters: emptyObjectSchema()},
			OutputLimits: &durable.ToolOutputLimits{MaxLines: new(3), Retain: durable.RetainTail},
			Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				for line := range 6 {
					api.Output("line " + string(rune('0'+line)) + "\n")
					time.Sleep(120 * time.Millisecond)
				}
				if err := gate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{}, nil
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("tail", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
			fauxAnswer("done"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		var mu sync.Mutex
		var outputs []string
		observeCommits(harness, func(_ context.Context, publication session.ObservedCommit) {
			for _, change := range documentChanges(publication.Changes) {
				if change.Record.Kind != "pi.live" || change.Value == nil {
					continue
				}
				tools, _ := change.Value.Value("tools").([]any)
				if len(tools) == 0 {
					continue
				}
				output, present := tools[0].(*delta.JsonObject).Value("output").(string)
				mu.Lock()
				if present && (len(outputs) == 0 || outputs[len(outputs)-1] != output) {
					outputs = append(outputs, output)
				}
				mu.Unlock()
			}
		})
		listening := listenView(t, root, true)
		submission := submit(t, root, inputDraft("go"))
		waitFor(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(outputs) > 0 && strings.HasSuffix(outputs[len(outputs)-1], "line 5\n")
		})
		gate.resolve()
		must(submission.Wait(testContext))
		drained(harness)
		must(listening.watch.Stop())
		frames, mismatches := listening.snapshot()
		if mismatches != 0 {
			t.Fatalf("%d frames whose operations did not rebuild their value", mismatches)
		}
		var shown []string
		for _, frame := range frames {
			if tools := liveValue(t, frame.value).Tools; len(tools) > 0 && tools[0].Output != nil && (len(shown) == 0 || shown[len(shown)-1] != *tools[0].Output) {
				shown = append(shown, *tools[0].Output)
			}
		}
		mu.Lock()
		expectStrings(t, shown, outputs)
		mu.Unlock()
		if !slices.ContainsFunc(frames, func(frame viewFrame) bool {
			return slices.ContainsFunc(frame.ops, func(op durable.Op) bool { return op[0] == "t" })
		}) {
			t.Fatal("no frame trims the output")
		}
		closeHarness(t, harness)
	})

	t.Run("ends a run and starts the queued follow-up's in one value", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:307
		setup := chatSetup(t)
		release := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(release, "first"), fauxAnswer("second")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		followUp := submit(t, root, inputDraft("f"))
		history := viewHistory(t, harness, root.Id())
		release.resolve()
		must(followUp.Wait(testContext))
		history.Detach()
		states := history.Live()
		boundary := slices.IndexFunc(states, func(state LiveState) bool {
			return state.Run != nil && len(state.Run.Inputs) > 0 && state.Run.Inputs[0] == followUp.Id()
		})
		if boundary < 1 || states[boundary-1].Run == nil || !slices.Equal(states[boundary-1].Run.Inputs, []durable.SubmissionId{input.Id()}) {
			t.Fatalf("the follow-up's run starts at %d, after %+v", boundary, states[boundary-1].Run)
		}
		views := history.All()
		before, after := views[boundary-1], views[boundary]
		var appended []string
		for _, entry := range after.Entries[len(before.Entries):] {
			text, _ := textOf(entry.Model[0])
			appended = append(appended, entry.Kind+":"+text)
		}
		expectStrings(t, appended, []string{"pi.assistant:first", "pi.user:f"})
		if items := inboxIn(t, after); len(items) != 0 {
			t.Fatalf("inbox %v after the boundary, want none", items)
		}
		closeHarness(t, harness)
	})

	t.Run("ends a call that never runs with its result at once, and a tool aborted with its generation", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:331
		setup := chatSetup(t)
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "wait", Description: "Waits until aborted", Parameters: emptyObjectSchema()},
			Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return durable.ToolExecutionResult{}, abortedBy(ctx)
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("ghost", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"}), ai.FauxToolCall("wait", map[string]any{}, &ai.FauxToolCallOptions{ID: "c2"})}, StopReason: "toolUse"}),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		history := viewHistory(t, harness, root.Id())
		submission := submit(t, root, inputDraft("go"))
		waitFor(t, func() bool {
			return slices.ContainsFunc(slotsOf(history.Live(), "c2"), func(slot *slotView) bool { return slot != nil && slot.Status == ToolSlotRunning })
		})
		// Aborting the generation aborts its round: the tool ends with its aborted result first (spec §8.5).
		must(harness.AbortTask(testContext, liveOf(t, harness, root.Id()).Run.TaskId))
		must(submission.Wait(testContext))
		history.Detach()
		states := history.Live()
		views := history.All()
		// The call not offered is listed done, with its result entry, in the commit of its calling answer.
		ghostSlots := slotsOf(states, "c1")
		listed := slices.IndexFunc(ghostSlots, func(slot *slotView) bool { return slot != nil })
		ghost := ghostSlots[listed]
		if ghost.Status != ToolSlotDone {
			t.Fatalf("the call not offered is %s when listed, want done", ghost.Status)
		}
		listing := views[listed].Entries
		kinds := []string{listing[len(listing)-2].Kind, listing[len(listing)-1].Kind}
		expectStrings(t, kinds, []string{"pi.assistant", "pi.tool-result"})
		if ghost.Entry == nil || *ghost.Entry != listing[len(listing)-1].Id {
			t.Fatalf("the call not offered names entry %v, want %d", ghost.Entry, listing[len(listing)-1].Id)
		}
		// The aborted tool ends with its result entry, then the run ends without an answer.
		waitSlots := slotsOf(states, "c2")
		done := slices.IndexFunc(waitSlots, func(slot *slotView) bool { return slot != nil && slot.Status == ToolSlotDone })
		if waitSlots[done].Entry == nil || *waitSlots[done].Entry != lastEntry(views[done]).Id {
			t.Fatalf("the aborted tool names entry %v, want %d", waitSlots[done].Entry, lastEntry(views[done]).Id)
		}
		expectMatch(t, states[len(states)-1].RunFailure, jsonText(t, map[string]any{"inputs": []durable.SubmissionId{submission.Id()}, "reason": "aborted"}))
		closeHarness(t, harness)
	})

	t.Run("keeps a steer in the current run, shows nothing for other conversations, and appends a reset", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:372
		setup := chatSetup(t)
		gate := deferred()
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "hold", Description: "Waits", Parameters: emptyObjectSchema()},
			Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return durable.ToolExecutionResult{}, gate.wait(ctx)
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("hold", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
			fauxAnswer("done"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
		history := viewHistory(t, harness, root.Id())
		input := submit(t, root, inputDraft("a"))
		waitFor(t, func() bool {
			return slices.ContainsFunc(slotsOf(history.Live(), "c1"), func(slot *slotView) bool { return slot != nil && slot.Status == ToolSlotRunning })
		})
		steer := submit(t, root, inputDraft("s", durable.WhenBusySteer))
		count := len(history.All())
		noteEntry(t, other, "note")
		drained(harness)
		if after := len(history.All()); after != count {
			t.Fatalf("%d views, then %d after a commit of another conversation", count, after)
		}
		gate.resolve()
		must(input.Wait(testContext))
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		history.Detach()
		states := history.Live()
		// One run: the steer joined it.
		var runs []durable.SubmissionId
		for _, state := range states {
			if state.Run != nil && !slices.Contains(runs, state.Run.Inputs[0]) {
				runs = append(runs, state.Run.Inputs[0])
			}
		}
		expectIds(t, submissionIds(runs), submissionIds([]durable.SubmissionId{input.Id()}))
		if !slices.ContainsFunc(states, func(state LiveState) bool { return state.Run != nil && slices.Contains(state.Run.Inputs, steer.Id()) }) {
			t.Fatal("no view shows the steer in the run")
		}
		// The reset is appended in place, with no run.
		views := history.All()
		if kind := lastEntry(views[len(views)-1]).Kind; kind != "pi.reset" {
			t.Fatalf("the last entry is %s, want pi.reset", kind)
		}
		if states[len(states)-1].Run != nil {
			t.Fatal("the run outlived the reset")
		}
		closeHarness(t, harness)
	})

	t.Run("converts the streamed partial into one aborted answer entry when the run is aborted", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:408
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 100, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("x", 400))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		history := viewHistory(t, harness, root.Id())
		submission := submit(t, root, inputDraft("hi"))
		waitFor(t, func() bool {
			return slices.ContainsFunc(history.Live(), func(state LiveState) bool { return state.Generation != nil && state.Generation.Message != nil })
		})
		must(harness.AbortTask(testContext, liveOf(t, harness, root.Id()).Run.TaskId))
		must(submission.Wait(testContext))
		history.Detach()
		states := history.Live()
		views := history.All()
		ended := liveIndexAfter(states, 0, func(state LiveState) bool { return state.Run == nil })
		if ended < 1 || states[ended-1].Generation == nil || states[ended-1].Generation.Message == nil {
			t.Fatalf("run ends at %d without a partial before it", ended)
		}
		var appended []durable.EntryRecord
		appended = append(appended, views[ended].Entries[len(views[ended-1].Entries):]...)
		if len(appended) != 1 || appended[0].Kind != "pi.assistant" {
			t.Fatalf("the ending commit appends %+v, want one pi.assistant", appended)
		}
		expectMatch(t, appended[0].Model[0], `{"role":"assistant","stopReason":"aborted"}`)
		expectMatch(t, states[ended].RunFailure, `{"reason":"aborted"}`)
		closeHarness(t, harness)
	})

	t.Run("applies later operations to an overflow replacement that holds an in-flight partial", func(t *testing.T) {
		// upstream: packages/durable/test/harness-view-run.test.ts:430
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		watch := must(root.Watch(testContext, &ConversationWindow{Min: new(1), Max: new(1)}))
		change := func(edit func(live *delta.Object) error) { changeLive(t, root, edit) }
		change(func(live *delta.Object) error {
			return live.Set("generation", must(durable.ToJsonValue(map[string]any{"attempt": 1, "message": jsonOf(t, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hel"}}, API: "test", Provider: "test", Model: "test", StopReason: ai.StopReasonPending})})))
		})
		// Past 10,000 pending operations: an append, a splice, and a `before` per commit.
		for range 3400 {
			noteEntry(t, root, "note")
		}
		var mu sync.Mutex
		var frames []viewFrame
		watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
			mu.Lock()
			defer mu.Unlock()
			frames = append(frames, viewFrame{value: change.Value, ops: change.Ops})
			return nil
		})
		drained(harness)
		mu.Lock()
		first := frames[0]
		mu.Unlock()
		expectSameJSON(t, first.ops, []any{[]any{"r", first.value}})
		change(func(live *delta.Object) error {
			block := live.Object("generation").Object("message").Array("content").Object(0)
			return delta.AppendText(block, "text", "lo")
		})
		drained(harness)
		mu.Lock()
		update := frames[len(frames)-1]
		mu.Unlock()
		expectSameJSON(t, update.ops, []any{[]any{"a", []any{"docs", "pi.live", "generation", "message", "content", 0, "text"}, "lo"}})
		rebuilt := must(delta.ApplyImmutable(orderedJSONOf(t, first.value), opsOf(t, update.ops)))
		expectSameJSON(t, jsonOf(t, rebuilt).(map[string]any)["docs"].(map[string]any)["pi.live"].(map[string]any)["generation"].(map[string]any)["message"].(map[string]any)["content"], []any{map[string]any{"type": "text", "text": "hello"}})
		must(watch.Stop())
		closeHarness(t, harness)
	})
}

// opsOf converts operations to the form ApplyImmutable takes.
func opsOf(t *testing.T, ops []durable.Op) []durable.Op {
	t.Helper()
	decoded := orderedJSONOf(t, ops).([]any)
	converted := make([]durable.Op, len(decoded))
	for index, op := range decoded {
		converted[index] = op.([]any)
	}
	return converted
}

// generationMessageOf is the in-flight partial of a replayed view, as JSON; nil when there is none.
func generationMessageOf(t *testing.T, replayed any) map[string]any {
	t.Helper()
	view, _ := jsonOf(t, replayed).(map[string]any)
	docs, _ := view["docs"].(map[string]any)
	live, _ := docs["pi.live"].(map[string]any)
	generation, _ := live["generation"].(map[string]any)
	message, _ := generation["message"].(map[string]any)
	return message
}

func submissionIds(ids []durable.SubmissionId) []durable.EntryId {
	converted := make([]durable.EntryId, len(ids))
	for index, id := range ids {
		converted[index] = durable.EntryId(id)
	}
	return converted
}

// opsRecording is a synchronous observer that records every change with its operations.
type opsRecording struct {
	mu      sync.Mutex
	changes []viewFrame
}

func (recording *opsRecording) Advance(_ context.Context, change chord.WatchChange[ConversationView]) {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	recording.changes = append(recording.changes, viewFrame{value: change.Value, ops: change.Ops})
}

func (recording *opsRecording) End(error) {}

func (recording *opsRecording) batches() []viewFrame {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	return slices.Clone(recording.changes)
}
