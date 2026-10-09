// Ports packages/durable/test/harness-events.test.ts.

package harness

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type listening struct {
	stream  *AgentEventStream
	mu      sync.Mutex
	batches [][]AgentEvent
}

func (l *listening) allBatches() [][]AgentEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.batches)
}

func (l *listening) events() []AgentEvent {
	return slices.Concat(l.allBatches()...)
}

func (l *listening) stop(t *testing.T) {
	t.Helper()
	if _, err := l.stream.Stop(); err != nil {
		t.Fatal(err)
	}
}

// listen attaches and starts an event stream that records every delivered batch (harness-events.test.ts:33).
func listen(t *testing.T, harness Harness, conversation Conversation) *listening {
	t.Helper()
	stream := must(WatchEvents(testContext, harness, conversation.Id()))
	recorded := &listening{stream: stream}
	stream.Start(func(_ context.Context, events []AgentEvent) error {
		recorded.mu.Lock()
		defer recorded.mu.Unlock()
		recorded.batches = append(recorded.batches, slices.Clone(events))
		return nil
	})
	return recorded
}

func slowSetup(t *testing.T) *chatState {
	return chatSetup(t, ai.FauxConfig{TokensPerSecond: 400, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
}

func eventTypes(events []AgentEvent) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.EventType()
	}
	return types
}

func eventsOfType(events []AgentEvent, kind string) []AgentEvent {
	var matching []AgentEvent
	for _, event := range events {
		if event.EventType() == kind {
			matching = append(matching, event)
		}
	}
	return matching
}

func isAssistantStart(event AgentEvent) bool {
	start, ok := event.(MessageStartEvent)
	if !ok {
		return false
	}
	_, assistant := start.Message.(ai.AssistantMessage)
	return assistant
}

// streamedText rebuilds the streamed text of block 0 from message_start and the text deltas that follow it (harness-events.test.ts:51).
func streamedText(events []AgentEvent) string {
	text := ""
	for _, event := range events {
		if start, ok := event.(MessageStartEvent); ok {
			if message, assistant := start.Message.(ai.AssistantMessage); assistant {
				text = ""
				if len(message.Content) > 0 {
					if block, isText := message.Content[0].(ai.TextContent); isText {
						text = block.Text
					}
				}
			}
		}
		update, ok := event.(MessageUpdateEvent)
		if !ok {
			continue
		}
		for _, change := range update.Changes {
			if change.Type == "text_start" && change.ContentIndex == 0 {
				if block, isText := change.Block.(ai.TextContent); isText {
					text = block.Text
				}
			}
			if change.Type == "text_delta" && change.ContentIndex == 0 {
				text += change.Delta
			}
		}
	}
	return text
}

// applyChanges applies message changes to a copy of message's JSON form, as an events-only consumer would (harness-events.test.ts:72).
func applyChanges(t *testing.T, message any, changes []MessageChange) map[string]any {
	t.Helper()
	next := jsonOf(t, message).(map[string]any)
	for _, change := range changes {
		content, _ := next["content"].([]any)
		switch {
		case change.Type == "message":
			next = jsonOf(t, change.Message).(map[string]any)
		case change.Type == "block":
			content[change.ContentIndex] = jsonOf(t, change.Block)
		case strings.HasSuffix(change.Type, "_start"):
			next["content"] = slices.Insert(content, change.ContentIndex, jsonOf(t, change.Block))
		case change.Type == "text_delta" || change.Type == "thinking_delta":
			block := content[change.ContentIndex].(map[string]any)
			field := "text"
			if change.Type == "thinking_delta" {
				field = "thinking"
			}
			block[field] = block[field].(string) + change.Delta
		case change.Type == "toolcall_delta":
			target := content[change.ContentIndex].(map[string]any)["arguments"].(map[string]any)
			for _, segment := range change.Path[:len(change.Path)-1] {
				target = target[segment.(string)].(map[string]any)
			}
			last := change.Path[len(change.Path)-1].(string)
			target[last] = target[last].(string) + change.Delta
		}
	}
	return next
}

type partialLog struct {
	mu       sync.Mutex
	partials []any
}

func (log *partialLog) all() []any {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.partials)
}

func livePartial(change durable.DocumentChange) (any, bool) {
	if change.Record.Kind != "pi.live" || change.Value == nil {
		return nil, false
	}
	generation, _ := change.Value.Value("generation").(*delta.JsonObject)
	if generation == nil {
		return nil, false
	}
	return generation.Get("message")
}

// partialsOf records the committed partials of the generation, one per commit that has one (harness-events.test.ts:94).
func partialsOf(harness Harness) *partialLog {
	log := &partialLog{}
	harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		for _, change := range documentChanges(publication) {
			if message, ok := livePartial(change); ok {
				log.mu.Lock()
				log.partials = append(log.partials, message)
				log.mu.Unlock()
			}
		}
	})
	return log
}

// rebuildOutput rebuilds a tool slot's retained window from output events.
func rebuildOutput(events []AgentEvent, each func(text string)) string {
	text := ""
	for _, event := range events {
		update, ok := event.(ToolExecutionUpdateEvent)
		if !ok || update.Output == nil {
			continue
		}
		if update.Output.Set != nil {
			text = *update.Output.Set
		} else {
			trim := 0
			if update.Output.TrimStart != nil {
				trim = *update.Output.TrimStart
			}
			text = text[trim:]
			if update.Output.Append != nil {
				text += *update.Output.Append
			}
		}
		if each != nil {
			each(text)
		}
	}
	return text
}

// messageRole is a message's role, as upstream reads message.role.
func messageRole(message ai.Message) string {
	switch message.(type) {
	case ai.SystemMessage:
		return "system"
	case ai.UserMessage:
		return "user"
	case ai.AssistantMessage:
		return "assistant"
	case ai.ToolResultMessage:
		return "toolResult"
	}
	return ""
}

func emptyObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func submitWhenBusy(t *testing.T, conversation Conversation, content string, whenBusy ...durable.WhenBusy) durable.Submission {
	t.Helper()
	draft := durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content)}
	if len(whenBusy) > 0 {
		draft.WhenBusy = whenBusy[0]
	}
	return must(conversation.Submit(testContext, draft))
}

// changeLive edits pi.live through its draft, as upstream's tx.doc(LiveDoc) mutations.
func changeLive(t *testing.T, conversation Conversation, edit func(live *delta.Object) error) {
	t.Helper()
	commitValue(t, conversation, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc(tx, LiveDoc, conversation.Id())
		if err != nil {
			return nil, err
		}
		return nil, edit(draft)
	})
}

// Pi: packages/durable/src/harness/events.ts:97 (closed).
func TestAgentEvents(t *testing.T) {
	t.Run("streams a run as lifecycle events and text deltas that rebuild the answer", func(t *testing.T) {
		setup := slowSetup(t)
		text := strings.Repeat("streamed answer text ", 20)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(text)})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		listened := listen(t, harness, root)
		snapshot := jsonOf(t, listened.stream.Snapshot).(map[string]any)
		if snapshot["type"] != "snapshot" || len(snapshot["entries"].([]any)) != 0 || len(snapshot["tools"].([]any)) != 0 || len(snapshot["inbox"].([]any)) != 0 {
			t.Fatalf("snapshot %v", snapshot)
		}
		partials := partialsOf(harness)
		submission := submitWhenBusy(t, root, "hi")
		must(submission.Wait(testContext))
		drained(harness)
		all := listened.events()
		var types []string
		for i, kind := range eventTypes(all) {
			if i == 0 || kind != eventTypes(all)[i-1] {
				types = append(types, kind)
			}
		}
		expectStrings(t, types, []string{
			"message_start",
			"message_end",
			"submission",
			"run_start",
			"turn_start",
			"message_start",
			"message_update",
			"message_end",
			"turn_end",
			"run_end",
			"submission",
			"usage_changed",
		})
		expectSameJSON(t, eventsOfType(all, "run_start")[0], map[string]any{"type": "run_start", "inputs": []any{float64(submission.Id())}})
		// After each event, the rebuilt text equals the committed partial of that commit.
		var rebuilt []string
		for index, event := range all {
			if isAssistantStart(event) || event.EventType() == "message_update" {
				rebuilt = append(rebuilt, streamedText(all[:index+1]))
			}
		}
		var committedTexts []string
		for _, partial := range partials.all() {
			value, _ := textOf(*decodeAssistant(partial))
			committedTexts = append(committedTexts, value)
		}
		expectStrings(t, rebuilt, committedTexts)
		if len(committedTexts) <= 1 {
			t.Fatalf("%d partials", len(committedTexts))
		}
		ends := eventsOfType(all, "message_end")
		end := ends[len(ends)-1].(MessageEndEvent)
		if value, _ := textOf(end.Entry.Model[0]); value != text {
			t.Fatalf("answer %q", value)
		}
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("reports tool start, output appends, and the result entry", func(t *testing.T) {
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
		listened := listen(t, harness, root)
		submission := submitWhenBusy(t, root, "go")
		// The output events rebuild the slot's retained window.
		waitFor(t, func() bool { return rebuildOutput(listened.events(), nil) == "one\ntwo\n" })
		gate.resolve()
		must(submission.Wait(testContext))
		drained(harness)
		all := listened.events()
		var tool []AgentEvent
		for _, event := range all {
			if strings.HasPrefix(event.EventType(), "tool_execution") {
				tool = append(tool, event)
			}
		}
		expectSameJSON(t, tool[0], map[string]any{"type": "tool_execution_start", "toolCallId": "c1", "toolName": "print", "args": map[string]any{"n": float64(1)}})
		end, ok := tool[len(tool)-1].(ToolExecutionEndEvent)
		if !ok || end.ToolCallId != "c1" || end.Entry == nil || end.Entry.Kind != "pi.tool-result" {
			t.Fatalf("end %+v", tool[len(tool)-1])
		}
		// As in the coding agent, the tool ends directly before its result message.
		endIndex := slices.IndexFunc(all, func(event AgentEvent) bool {
			candidate, isEnd := event.(ToolExecutionEndEvent)
			return isEnd && candidate.ToolCallId == "c1"
		})
		expectStrings(t, eventTypes(all[endIndex:endIndex+3]), []string{"tool_execution_end", "message_start", "message_end"})
		result, isResult := all[endIndex+1].(MessageStartEvent).Message.(ai.ToolResultMessage)
		if !isResult || result.ToolCallID != "c1" {
			t.Fatalf("result start %+v", all[endIndex+1])
		}
		// Two turns: the tool round, and the answer.
		if starts, ends := len(eventsOfType(all, "turn_start")), len(eventsOfType(all, "turn_end")); starts != 2 || ends != 2 {
			t.Fatalf("turns %d/%d", starts, ends)
		}
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("reports queued submissions, inbox changes, and retries", func(t *testing.T) {
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
		listened := listen(t, harness, root)
		submitWhenBusy(t, root, "a")
		followUp := submitWhenBusy(t, root, "f")
		drained(harness)
		expectSameJSON(t, eventsOfType(listened.events(), "inbox_update"), []any{
			map[string]any{"type": "inbox_update", "items": []any{map[string]any{"id": float64(followUp.Id()), "mode": "followUp"}}},
		})
		release.resolve()
		must(followUp.Wait(testContext))
		drained(harness)
		types := eventTypes(listened.events())
		if !slices.Contains(types, "auto_retry_start") || !slices.Contains(types, "auto_retry_end") {
			t.Fatalf("types %v", types)
		}
		if runs := len(eventsOfType(listened.events(), "run_start")); runs != 2 {
			t.Fatalf("%d runs", runs)
		}
		var statuses []string
		for _, event := range eventsOfType(listened.events(), "submission") {
			statuses = append(statuses, string(event.(SubmissionEvent).Record.Status))
		}
		expectStrings(t, statuses, []string{"placed", "queued", "done", "placed", "done"})
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("replaces undelivered batches with one snapshot after 100 pending batches", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		stream := must(WatchEvents(testContext, harness, root.Id()))
		for range 101 {
			noteEntry(t, root, "note")
		}
		var mu sync.Mutex
		var batches [][]AgentEvent
		stream.Start(func(_ context.Context, events []AgentEvent) error {
			mu.Lock()
			defer mu.Unlock()
			batches = append(batches, slices.Clone(events))
			return nil
		})
		drained(harness)
		mu.Lock()
		got := slices.Clone(batches)
		mu.Unlock()
		if len(got) != 1 || len(got[0]) != 1 {
			t.Fatalf("batches %v", got)
		}
		snapshot, ok := got[0][0].(SnapshotEvent)
		if !ok || len(snapshot.Entries) != 101 {
			t.Fatalf("snapshot %+v", got[0][0])
		}
		must(stream.Stop())
		closeHarness(t, harness)
	})

	t.Run("rebuilds every committed partial of thinking, text, and tool-call arguments from message changes", func(t *testing.T) {
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
		listened := listen(t, harness, root)
		must(submitWhenBusy(t, root, "go").Wait(testContext))
		drained(harness)
		var rebuilt []any
		var current map[string]any
		// The streamed tool-calling message, up to its end; the short final answer commits no partial.
		for _, event := range listened.events() {
			if end, ok := event.(MessageEndEvent); ok && end.Entry.Kind == "pi.assistant" {
				break
			}
			if isAssistantStart(event) {
				current = jsonOf(t, event.(MessageStartEvent).Message).(map[string]any)
			} else if update, ok := event.(MessageUpdateEvent); ok {
				current = applyChanges(t, current, update.Changes)
			} else {
				continue
			}
			rebuilt = append(rebuilt, current["content"])
		}
		committedPartials := partials.all()
		if len(committedPartials) <= 2 {
			t.Fatalf("%d partials", len(committedPartials))
		}
		var want []any
		for _, partial := range committedPartials {
			want = append(want, partial.(*delta.JsonObject).Value("content"))
		}
		expectSameJSON(t, rebuilt, want)
		types := map[string]bool{}
		for _, event := range eventsOfType(listened.events(), "message_update") {
			for _, change := range event.(MessageUpdateEvent).Changes {
				types[change.Type] = true
			}
		}
		if !types["thinking_delta"] && !types["text_delta"] {
			t.Fatalf("change types %v", types)
		}
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("rebuilds a sliding tail window from output trims and appends", func(t *testing.T) {
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
		harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication) {
				if change.Record.Kind != "pi.live" || change.Value == nil {
					continue
				}
				tools, _ := change.Value.Value("tools").([]any)
				if len(tools) == 0 {
					continue
				}
				output, ok := tools[0].(*delta.JsonObject).Value("output").(string)
				mu.Lock()
				if ok && (len(outputs) == 0 || outputs[len(outputs)-1] != output) {
					outputs = append(outputs, output)
				}
				mu.Unlock()
			}
		})
		listened := listen(t, harness, root)
		submission := submitWhenBusy(t, root, "go")
		waitFor(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(outputs) > 0 && strings.HasSuffix(outputs[len(outputs)-1], "line 5\n")
		})
		gate.resolve()
		must(submission.Wait(testContext))
		drained(harness)
		var rebuilt []string
		rebuildOutput(listened.events(), func(text string) { rebuilt = append(rebuilt, text) })
		mu.Lock()
		expectStrings(t, rebuilt, outputs)
		mu.Unlock()
		if !slices.ContainsFunc(listened.events(), func(event AgentEvent) bool {
			update, ok := event.(ToolExecutionUpdateEvent)
			return ok && update.Output != nil && update.Output.TrimStart != nil
		}) {
			t.Fatal("no trim")
		}
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("emits one exact batch when a run ends and a queued follow-up starts the next", func(t *testing.T) {
		setup := chatSetup(t)
		release := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(release, "first"), fauxAnswer("second")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submitWhenBusy(t, root, "a")
		followUp := submitWhenBusy(t, root, "f")
		listened := listen(t, harness, root)
		release.resolve()
		must(followUp.Wait(testContext))
		drained(harness)
		var boundary []AgentEvent
		for _, batch := range listened.allBatches() {
			if slices.Contains(eventTypes(batch), "run_end") {
				boundary = batch
				break
			}
		}
		expectStrings(t, eventTypes(boundary), []string{
			"message_start",
			"message_end",
			"message_start",
			"message_end",
			"turn_end",
			"run_end",
			"submission",
			"submission",
			"inbox_update",
			"usage_changed",
			"run_start",
			"turn_start",
		})
		var ids []durable.SubmissionId
		for _, event := range eventsOfType(boundary, "submission") {
			ids = append(ids, event.(SubmissionEvent).Record.Id)
		}
		expectIds(t, ids, []durable.SubmissionId{input.Id(), followUp.Id()})
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("ends a call that never runs and a tool aborted with its generation", func(t *testing.T) {
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
		listened := listen(t, harness, root)
		submission := submitWhenBusy(t, root, "go")
		waitFor(t, func() bool { return slices.Contains(eventTypes(listened.events()), "tool_execution_start") })
		// Aborting the generation aborts its round: the tool ends with its aborted result first (spec §8.5).
		live := must(durable.Snapshot(testContext, harness, LiveDoc, root.Id()))
		must(harness.AbortTask(testContext, live.Run.TaskId))
		must(submission.Wait(testContext))
		drained(harness)
		// The call not offered ends after its calling message and directly before its result message.
		var round []string
		for _, event := range listened.events() {
			switch typed := event.(type) {
			case MessageEndEvent:
				round = append(round, "end:"+typed.Entry.Kind)
			case MessageStartEvent:
				round = append(round, "start:"+messageRole(typed.Message))
			default:
				round = append(round, event.EventType())
			}
		}
		ghostEnd := slices.Index(round, "tool_execution_end")
		expectStrings(t, round[ghostEnd-1:ghostEnd+3], []string{"end:pi.assistant", "tool_execution_end", "start:toolResult", "end:pi.tool-result"})
		var tool []string
		for _, event := range listened.events() {
			switch typed := event.(type) {
			case ToolExecutionStartEvent:
				tool = append(tool, "start "+typed.ToolCallId)
			case ToolExecutionEndEvent:
				tool = append(tool, "end "+typed.ToolCallId+" "+map[bool]string{true: "entry", false: "none"}[typed.Entry != nil])
			case ToolExecutionUpdateEvent:
				tool = append(tool, "update "+typed.ToolCallId)
			}
		}
		expectStrings(t, tool, []string{"end c1 entry", "start c2", "end c2 entry"})
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("reports a steer without run events, a reset as an appended entry, and nothing for other conversations", func(t *testing.T) {
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
		listened := listen(t, harness, root)
		input := submitWhenBusy(t, root, "a")
		waitFor(t, func() bool { return slices.Contains(eventTypes(listened.events()), "tool_execution_start") })
		submitWhenBusy(t, root, "s", durable.WhenBusySteer)
		drained(harness)
		before := len(listened.allBatches())
		noteEntry(t, other, "note")
		drained(harness)
		if after := len(listened.allBatches()); after != before {
			t.Fatalf("batches %d -> %d", before, after)
		}
		gate.resolve()
		must(input.Wait(testContext))
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		drained(harness)
		all := listened.events()
		if starts, ends := len(eventsOfType(all, "run_start")), len(eventsOfType(all, "run_end")); starts != 1 || ends != 1 {
			t.Fatalf("runs %d/%d", starts, ends)
		}
		batches := listened.allBatches()
		expectStrings(t, eventTypes(batches[len(batches)-1]), []string{"entry_appended", "submission"})
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("rejects an attachment cancelled while it waits for the Session line", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		release := deferred()
		started := make(chan struct{})
		blocking := asyncErr(func() error {
			_, err := root.Commit(testContext, func(durable.Tx) (any, error) {
				close(started)
				return nil, release.wait(context.Background())
			})
			return err
		})
		<-started
		cancelled, cancel := cancelledContext(errors.New("cancelled"))
		var attaching *future[struct{}]
		queueOnLine(t, harness, func() <-chan struct{} {
			attaching = asyncErr(func() error {
				_, err := WatchEvents(cancelled, harness, root.Id())
				return err
			})
			return attaching.done
		})
		cancel()
		release.resolve()
		if _, err := blocking.wait(); err != nil {
			t.Fatal(err)
		}
		_, err := attaching.wait()
		expectError(t, err, "cancelled")
		closeHarness(t, harness)
	})

	t.Run("applies deltas after an overflow snapshot that holds an in-flight partial", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		partial := jsonOf(t, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hel"}}})
		stream := must(WatchEvents(testContext, harness, root.Id()))
		changeLive(t, root, func(live *delta.Object) error {
			return live.Set("generation", map[string]any{"attempt": float64(1), "message": partial})
		})
		for range 101 {
			noteEntry(t, root, "note")
		}
		var mu sync.Mutex
		var batches [][]AgentEvent
		stream.Start(func(_ context.Context, events []AgentEvent) error {
			mu.Lock()
			defer mu.Unlock()
			batches = append(batches, slices.Clone(events))
			return nil
		})
		drained(harness)
		mu.Lock()
		snapshot, ok := batches[0][0].(SnapshotEvent)
		mu.Unlock()
		if !ok {
			t.Fatalf("first event %v", batches[0][0].EventType())
		}
		commitValue(t, root, func(tx durable.Tx) (any, error) {
			live, err := durable.TxDoc(tx, LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			first := live.Object("generation").Object("message").Array("content").Object(0)
			return nil, first.Set("text", first.Get("text").(string)+"lo")
		})
		drained(harness)
		mu.Lock()
		update, ok := batches[len(batches)-1][0].(MessageUpdateEvent)
		mu.Unlock()
		if !ok {
			t.Fatalf("unexpected %v", batches[len(batches)-1][0].EventType())
		}
		expectSameJSON(t, update.Changes, []any{map[string]any{"type": "text_delta", "contentIndex": float64(0), "delta": "lo"}})
		rebuilt := applyChanges(t, snapshot.Generation.Message, update.Changes)
		expectSameJSON(t, rebuilt["content"], []any{map[string]any{"type": "text", "text": "hello"}})
		must(stream.Stop())
		closeHarness(t, harness)
	})

	t.Run("reports usage-only updates, cleared tool progress, tools ending without entries, and deferred polls", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		listened := listen(t, harness, root)
		partial := jsonOf(t, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}}).(map[string]any)
		changeLive(t, root, func(live *delta.Object) error {
			return live.Set("generation", map[string]any{"attempt": float64(1), "message": partial})
		})
		// A usage-only change sends the usage and no changes.
		changeLive(t, root, func(live *delta.Object) error {
			return live.Object("generation").Object("message").Object("usage").Set("input", float64(42))
		})
		changeLive(t, root, func(live *delta.Object) error {
			return live.Set("generation", map[string]any{"attempt": float64(1), "deferred": map[string]any{"pollAt": float64(1)}})
		})
		changeLive(t, root, func(live *delta.Object) error {
			return live.Object("generation").Object("deferred").Set("pollAt", float64(2))
		})
		changeLive(t, root, func(live *delta.Object) error {
			return live.Set("tools", []any{map[string]any{"callId": "c1", "name": "t", "status": "running", "details": map[string]any{"n": float64(1)}, "diagnostics": []any{}}})
		})
		// A safe replay clears the running slot's progress.
		changeLive(t, root, func(live *delta.Object) error {
			slot := live.Array("tools").Object(0)
			slot.Delete("details")
			slot.Delete("diagnostics")
			return nil
		})
		// A fault marks the slot done without an entry.
		changeLive(t, root, func(live *delta.Object) error {
			return live.Array("tools").Object(0).Set("status", "done")
		})
		commitValue(t, root, func(tx durable.Tx) (any, error) { return nil, tx.RetireDoc(UsageDoc, root.Id()) })
		commitValue(t, root, func(tx durable.Tx) (any, error) { return nil, tx.RetireDoc(AgentDoc, root.Id()) })
		drained(harness)
		var got [][]AgentEvent
		for _, batch := range listened.allBatches() {
			got = append(got, slices.DeleteFunc(slices.Clone(batch), func(event AgentEvent) bool { return event.EventType() == "task_failed" }))
		}
		// ai.Usage's JSON form fills a zero totalTokens, so the usage-only update is compared as the typed value upstream's {...partial.usage, input: 42} decodes to.
		if len(got) < 2 || len(got[1]) != 1 {
			t.Fatalf("batches %v", got)
		}
		update, ok := got[1][0].(MessageUpdateEvent)
		if want := (ai.Usage{Input: 42}); !ok || update.Usage != want || update.Changes == nil || len(update.Changes) != 0 {
			t.Fatalf("usage-only update %+v", got[1][0])
		}
		got = slices.Delete(got, 1, 2)
		expectSameJSON(t, got, []any{
			[]any{map[string]any{"type": "message_start", "message": partial}},
			[]any{map[string]any{"type": "deferred_poll", "pollAt": float64(1)}},
			[]any{map[string]any{"type": "deferred_poll", "pollAt": float64(2)}},
			[]any{map[string]any{"type": "tool_execution_start", "toolCallId": "c1", "toolName": "t", "args": map[string]any{}}},
			[]any{map[string]any{"type": "tool_execution_update", "toolCallId": "c1", "toolName": "t", "details": nil, "diagnostics": []any{}}},
			[]any{map[string]any{"type": "tool_execution_end", "toolCallId": "c1", "toolName": "t"}},
			// Retired documents read as their initial values.
			[]any{map[string]any{"type": "usage_changed", "usage": map[string]any{"models": map[string]any{}, "tools": map[string]any{}}}},
			[]any{map[string]any{"type": "agent_changed", "agent": map[string]any{}}},
		})
		listened.stop(t)
		closeHarness(t, harness)
	})

	t.Run("ends the stream with the Harness", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		stream := must(WatchEvents(testContext, harness, root.Id()))
		closeHarness(t, harness)
		<-stream.Closed()
		if end := stream.End(); end.Reason != durable.WatchSessionClosed || end.Error != nil {
			t.Fatalf("end %+v", end)
		}
	})

	t.Run("starts a message at the first committed partial and ends it with the converted entry on abort", func(t *testing.T) {
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 100, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("x", 400))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		listened := listen(t, harness, root)
		submission := submitWhenBusy(t, root, "hi")
		waitFor(t, func() bool { return slices.ContainsFunc(listened.events(), isAssistantStart) })
		live := must(durable.Snapshot(testContext, harness, LiveDoc, root.Id()))
		must(harness.AbortTask(testContext, live.Run.TaskId))
		must(submission.Wait(testContext))
		drained(harness)
		assistantEnds := slices.DeleteFunc(listened.events(), func(event AgentEvent) bool {
			end, ok := event.(MessageEndEvent)
			return !ok || end.Entry.Kind != "pi.assistant"
		})
		if len(assistantEnds) != 1 {
			t.Fatalf("%d assistant ends", len(assistantEnds))
		}
		if starts := slices.DeleteFunc(listened.events(), func(event AgentEvent) bool { return !isAssistantStart(event) }); len(starts) != 1 {
			t.Fatalf("%d assistant starts", len(starts))
		}
		listened.stop(t)
		closeHarness(t, harness)
	})
}
