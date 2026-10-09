package harness

// pi: packages/durable/src/harness/context.ts

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
)

// The generation handlers read context views that share memory with the context cache and the SQLite working set.
// Everything else that receives a view or a request's messages must receive its own copy: a change it makes must
// not reach a later read, as it cannot in Pi, where every read decodes the stored entries again.

// damageMessages overwrites, in place, everything the messages reach.
func damageMessages(messages []ai.Message) {
	for _, message := range messages {
		switch typed := message.(type) {
		case ai.AssistantMessage:
			for index, block := range typed.Content {
				if call, ok := block.(ai.ToolCall); ok {
					call.Arguments["n"] = 99.0
					continue
				}
				typed.Content[index] = ai.TextContent{Text: "damaged"}
			}
		case ai.ToolResultMessage:
			for index := range typed.Content {
				typed.Content[index] = ai.TextContent{Text: "damaged"}
			}
		}
	}
}

func damageView(view durable.ContextView) {
	for index := range view.Entries {
		view.Entries[index].Kind = "damaged"
		damageMessages(view.Entries[index].Model)
	}
	for _, contribution := range view.Contributions {
		damageMessages(contribution)
	}
	damageMessages(view.Messages)
}

// Pi TaskRuntime.context: packages/durable/src/types.ts:223.
func TestTaskRuntimeContextIsDetachedFromLaterReads(t *testing.T) {
	var first, again durable.ContextView
	reader := tkOneStep("test.context-damage", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		view, err := runtime.Context(ctx, runtime.ConversationId(), nil)
		if err != nil {
			return err
		}
		first = detach.ContextView(view)
		damageView(view)
		if again, err = runtime.Context(ctx, runtime.ConversationId(), nil); err != nil {
			return err
		}
		return tkComplete(ctx, runtime)
	})
	opened := tkOpenRoot(t, []durable.AnyTask{reader}, tkOptions{storage: cacheTestStorage(t)})
	for _, model := range [][]ai.Message{
		{ai.UserMessage{Content: ai.UserText("question"), Timestamp: 1}},
		{ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}, ai.ToolCall{ID: "c1", Name: "lookup", Arguments: ai.JsonObject{"n": 1.0}}}, StopReason: ai.StopReasonToolUse, Timestamp: 2}},
		{ai.ToolResultMessage{ToolCallID: "c1", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "found"}}, Timestamp: 3}},
	} {
		if _, err := opened.root.Commit(testContext, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "message", Model: model})
		}); err != nil {
			t.Fatal(err)
		}
	}
	id := tkStart(t, opened.root, reader)
	opened.harness.Resume()
	tkWaitOutcome(t, opened.harness, id)
	if len(first.Entries) != 3 {
		t.Fatalf("the task read %d entries, want 3", len(first.Entries))
	}
	if !reflect.DeepEqual(again, first) {
		t.Fatal("a task handler's changes to its context view reached a later read")
	}
	mustClose(t, opened.harness)
}

// TestBeforeRequestHookChangesDoNotReachTheStoredContext Conversation.submit durably admits user input (packages/durable/src/harness/types.ts:514-518).
// mutation-checked: Conversation.Submit returning no submission fails it.
func TestBeforeRequestHookChangesDoNotReachTheStoredContext(t *testing.T) {
	setup := chatSetup(t)
	addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{
		BeforeRequest: func(_ context.Context, request GenerationRequest, _ HookApi) (*GenerationRequest, error) {
			damageMessages(request.Messages)
			return nil, nil
		},
	})
	setup.Faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("first answer")}, StopReason: "stop"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("second answer")}, StopReason: "stop"}),
	})
	harness, root := openChat(t, cacheTestStorage(t), setup)
	harness.Resume()
	for _, text := range []string{"first", "second"} {
		if settled := must(submitInput(t, root, text).Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("turn %q settled %s", text, settled.Status)
		}
	}
	view := must(root.Context(testContext, nil))
	var answers []string
	for _, message := range view.Messages {
		if assistant, ok := message.(ai.AssistantMessage); ok {
			text, _ := textOf(assistant)
			answers = append(answers, text)
		}
	}
	if want := []string{"first answer", "second answer"}; !reflect.DeepEqual(answers, want) {
		t.Fatalf("assistant answers after a hook changed its request: %q, want %q", answers, want)
	}
	closeHarness(t, harness)
}

// TestConversationViewEntriesAreDetachedFromTheStoredContext Conversation.viewState and Conversation.watch serve the structural view (packages/durable/src/harness/types.ts:551-554).
// mutation-checked: Conversation.ViewState and Conversation.Watch returning nil fail it.
func TestConversationViewEntriesAreDetachedFromTheStoredContext(t *testing.T) {
	harness, _ := openHarness(t, cacheTestStorage(t), nil)
	root := must(harness.Root(testContext, nil))
	for _, model := range [][]ai.Message{
		{ai.UserMessage{Content: ai.UserText("question"), Timestamp: 1}},
		{ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}, ai.ToolCall{ID: "c1", Name: "lookup", Arguments: ai.JsonObject{"n": 1.0}}}, StopReason: ai.StopReasonToolUse, Timestamp: 2}},
	} {
		if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: model})
		}); err != nil {
			t.Fatal(err)
		}
	}
	pristine := must(root.Context(testContext, nil))
	watch := must(root.Watch(testContext))
	for _, entry := range watch.Value().Entries {
		damageMessages(entry.Model)
	}
	if _, err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
	state := must(root.ViewState(testContext))
	for _, entry := range state.Value().Entries {
		damageMessages(entry.Model)
	}
	state.Dispose()
	if again := must(root.Context(testContext, nil)); !reflect.DeepEqual(again, pristine) {
		t.Fatal("a view observer's changes to its entries reached the stored context")
	}
	mustClose(t, harness)
}

// DeriveContext and ActiveEntries are exported: what they return is the caller's own, whatever the storage retains.
func TestExportedContextReadsAreDetachedFromTheStorage(t *testing.T) {
	s := newCacheScenario(t, 5)
	for range 3 {
		s.turn(1)
	}
	bounds := must(CaptureContextBounds(testContext, s.storage, 1, nil))
	// A deep copy, so damage that reached the storage cannot reach the expectation too.
	pristine := detach.ContextView(must(DeriveContext(testContext, s.storage, 1, bounds)))
	damageView(must(DeriveContext(testContext, s.storage, 1, bounds)))
	for _, entry := range must(ActiveEntries(testContext, s.storage, 1, bounds)) {
		damageMessages(entry.Model)
	}
	if again := must(DeriveContext(testContext, s.storage, 1, bounds)); !reflect.DeepEqual(again, pristine) {
		t.Fatal("a caller's changes to an exported read reached the storage")
	}
}
