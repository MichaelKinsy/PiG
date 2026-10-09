// Ports packages/durable/test/harness-inbox.test.ts.

package harness

// pi: packages/durable/src/harness/inbox.ts

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// gatedAnswer is a faux response held until release or cancellation; reached resolves when the request is sent.
type gatedAnswer struct {
	step    ai.FauxResponseStep
	reached *deferredGate
	gate    *deferredGate
}

func gatedReply(text string) gatedAnswer {
	return gatedResponse(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}

func gatedResponse(response ai.FauxResponse) gatedAnswer {
	reached, gate := deferred(), deferred()
	return gatedAnswer{
		reached: reached,
		gate:    gate,
		step: ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			reached.resolve()
			if err := gate.wait(options.Signal); err != nil {
				return ai.FauxResponse{}.AssistantMessage(), err
			}
			return response.AssistantMessage(), nil
		}),
	}
}

func (answer gatedAnswer) awaitReached(t *testing.T) {
	t.Helper()
	if err := answer.reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
}

func (answer gatedAnswer) release() { answer.gate.resolve() }

// holdTool registers a hold tool whose calls wait for gate and then return result.
func holdTool(t *testing.T, setup *chatState, gate *deferredGate, result ...durable.ToolExecutionResult) {
	t.Helper()
	returned := durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}
	if len(result) > 0 {
		returned = result[0]
	}
	addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "hold", Description: "Waits for the test", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if err := gate.wait(ctx); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return returned, nil
		},
	}))
}

func holdStep() ai.FauxResponseStep {
	return toolCallStep(ai.FauxToolCall("hold", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"}))
}

func status(t *testing.T, submission durable.Submission) durable.SubmissionRecord {
	t.Helper()
	return must(submission.Status(testContext))
}

// transcript is the kind and text of each entry, skipping system entries.
func transcript(entries []durable.EntryRecord) []string {
	lines := []string{}
	for _, entry := range entries {
		if entry.Kind == "pi.system" {
			continue
		}
		line := entry.Kind
		if len(entry.Model) > 0 {
			if _, isResult := entry.Model[0].(ai.ToolResultMessage); !isResult {
				if text, ok := textOf(entry.Model[0]); ok {
					line += ":" + text
				}
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func expectTranscript(t *testing.T, conversation Conversation, want ...string) {
	t.Helper()
	expectEqualJSON(t, transcript(allEntries(t, conversation)), jsonText(t, want))
}

func inboxOf(t *testing.T, harness Harness, conversation Conversation) [][]any {
	t.Helper()
	state := must(durable.Snapshot(testContext, harness, InboxDoc, conversation.Id()))
	items := [][]any{}
	for _, item := range state.Items {
		items = append(items, []any{item.Id, item.Mode})
	}
	return items
}

func toolRunning(t *testing.T, harness Harness, conversation Conversation) {
	t.Helper()
	waitFor(t, func() bool {
		state := liveOf(t, harness, conversation.Id())
		return len(state.Tools) > 0 && state.Tools[0].Status == ToolSlotRunning
	})
}

func assistants(t *testing.T, conversation Conversation) []durable.EntryRecord {
	t.Helper()
	found := []durable.EntryRecord{}
	for _, entry := range allEntries(t, conversation) {
		if entry.Kind == "pi.assistant" {
			found = append(found, entry)
		}
	}
	return found
}

func submit(t *testing.T, conversation Conversation, draft durable.SubmissionDraft) durable.Submission {
	t.Helper()
	return must(conversation.Submit(testContext, draft))
}

func inputDraft(content string, whenBusy ...durable.WhenBusy) durable.SubmissionDraft {
	draft := durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content)}
	if len(whenBusy) > 0 {
		draft.WhenBusy = whenBusy[0]
	}
	return draft
}

func writeDraft(entry durable.EntryDraft) durable.SubmissionDraft {
	return durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &entry}
}

func expectAnswered(t *testing.T, record durable.SubmissionRecord, answer durable.EntryId) {
	t.Helper()
	if record.Status != durable.SubmissionDone || record.Answer == nil || *record.Answer != answer {
		t.Fatalf("record = %s, want done with answer %d", jsonText(t, record), answer)
	}
}

func resetTo(t *testing.T, conversation Conversation, handoff *string) {
	t.Helper()
	if err := conversation.Reset(testContext, handoff); err != nil {
		t.Fatal(err)
	}
}

func appendNote(t *testing.T, conversation Conversation, kind string) durable.EntryRecord {
	t.Helper()
	id := conversation.Id()
	return commitValue(t, conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(id, durable.EntryDraft{Kind: kind})
	})
}

// TestInbox Conversation.reset admits a pi.reset write that starts a new context, carrying the handoff as a user message (packages/durable/src/harness/types.ts:519-523).
// mutation-checked: Conversation.Reset returning without admitting the write fails it.
func TestInbox(t *testing.T) {
	t.Run("queues busy submissions and places writes before user items at the final boundary, one follow-up per run", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second"), fauxAnswer("third")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		f1 := submit(t, root, inputDraft("f1"))
		write := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Data: "w"}))
		f2 := submit(t, root, inputDraft("f2", durable.WhenBusyFollowUp))
		for _, submission := range []durable.Submission{f1, write, f2} {
			if record := status(t, submission); record.Status != durable.SubmissionQueued {
				t.Fatalf("status = %s, want queued", record.Status)
			}
		}
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{{f1.Id(), "followUp"}, {write.Id(), "write"}, {f2.Id(), "followUp"}}))
		expectTranscript(t, root, "pi.user:a")

		first.release()
		must(f2.Wait(testContext))
		expectTranscript(t, root, "pi.user:a", "pi.assistant:first", "note", "pi.user:f1", "pi.assistant:second", "pi.user:f2", "pi.assistant:third")
		answers := assistants(t, root)
		expectAnswered(t, status(t, input), answers[0].Id)
		expectSettled(t, status(t, write), durable.SubmissionDone, "")
		expectAnswered(t, status(t, f1), answers[1].Id)
		expectAnswered(t, status(t, f2), answers[2].Id)
		expectEqualJSON(t, inboxOf(t, harness, root), `[]`)
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		closeHarness(t, harness)
	})

	t.Run("places every follow-up in one successor run with followUpMode all", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("both")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		setup.SetSettings(func(settings *HarnessSettings) { settings.FollowUpMode = durable.QueueAll })
		submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		f1 := submit(t, root, inputDraft("f1"))
		f2 := submit(t, root, inputDraft("f2"))
		first.release()
		settled := must(f2.Wait(testContext))
		expectSameSettlement(t, status(t, f1), settled, f1.Id())
		expectTranscript(t, root, "pi.user:a", "pi.assistant:first", "pi.user:f1", "pi.user:f2", "pi.assistant:both")
		if calls := setup.Faux.CallCount(); calls != 2 {
			t.Fatalf("calls = %d, want 2", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("reads queue modes when the final boundary's commit runs on the Session line", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("both")})
		yielded := deferred()
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			yielded.resolve()
			return nil, nil
		}})
		store := newControlledStorage()
		harness, root := openChat(t, store, setup)
		submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		f1 := submit(t, root, inputDraft("f1"))
		f2 := submit(t, root, inputDraft("f2"))
		// Occupy the line, let the answer queue its boundary commit behind it, then change the mode.
		held := store.holdCommits()
		markerDoc := durable.DefineDoc(durable.DocDefinition[markerState]{
			CommonDocDefinition: durable.CommonDocDefinition[markerState]{Kind: "test.marker", Version: 1, Initial: func() markerState { return markerState{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
		})
		occupying := make(chan error, 1)
		go func() {
			_, err := durable.Commit(testContext, root, func(tx durable.Tx) (struct{}, error) {
				marker, err := durable.TxDoc[markerState](tx, markerDoc)
				if err != nil {
					return struct{}{}, err
				}
				n, _ := marker.Get("n").(float64)
				return struct{}{}, marker.Set("n", n+1)
			})
			occupying <- err
		}()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		first.release()
		if err := yielded.wait(testContext); err != nil {
			t.Fatal(err)
		}
		flush()
		setup.SetSettings(func(settings *HarnessSettings) { settings.FollowUpMode = durable.QueueAll })
		held.release()
		if err := <-occupying; err != nil {
			t.Fatal(err)
		}
		settled := must(f2.Wait(testContext))
		expectSameSettlement(t, status(t, f1), settled, f1.Id())
		if calls := setup.Faux.CallCount(); calls != 2 {
			t.Fatalf("calls = %d, want 2", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("adds steers to the run at the postTools boundary and holds follow-ups for the final boundary", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		holdTool(t, setup, gate)
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep(), fauxAnswer("after tools"), fauxAnswer("follow-up")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		toolRunning(t, harness, root)
		steer := submit(t, root, inputDraft("s", durable.WhenBusySteer))
		followUp := submit(t, root, inputDraft("f"))
		gate.resolve()
		must(followUp.Wait(testContext))
		expectTranscript(t, root, "pi.user:a", "pi.assistant", "pi.tool-result", "pi.user:s", "pi.assistant:after tools", "pi.user:f", "pi.assistant:follow-up")
		answers := assistants(t, root)
		expectAnswered(t, status(t, input), answers[1].Id)
		expectAnswered(t, status(t, steer), answers[1].Id)
		expectAnswered(t, status(t, followUp), answers[2].Id)
		closeHarness(t, harness)
	})

	t.Run("ends the run at a queued reset after tools and runs earlier follow-ups in the new context", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		holdTool(t, setup, gate)
		var mu sync.Mutex
		requests := [][]string{}
		record := ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			lines := []string{}
			for _, message := range request.Messages() {
				text, _ := textOf(message)
				lines = append(lines, roles([]ai.Message{message})[0]+":"+text)
			}
			mu.Lock()
			requests = append(requests, lines)
			mu.Unlock()
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("fresh")}}.AssistantMessage(), nil
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep(), record})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		toolRunning(t, harness, root)
		followUp := submit(t, root, inputDraft("f"))
		resetTo(t, root, nil)
		gate.resolve()
		must(followUp.Wait(testContext))
		expectSettled(t, status(t, input), durable.SubmissionUnanswered, "reset")
		expectTranscript(t, root, "pi.user:a", "pi.assistant", "pi.tool-result", "pi.reset", "pi.user:f", "pi.assistant:fresh")
		for _, entry := range allEntries(t, root) {
			if durable.ResetEntry.Is(&entry) && (entry.Head == nil || *entry.Head != entry.Id) {
				t.Fatalf("reset head = %v, want itself", entry.Head)
			}
		}
		// The follow-up's request starts at the reset: the complete system baseline after the cut leads the follow-up.
		mu.Lock()
		expectEqualJSON(t, requests, `[["system:","user:f"]]`)
		mu.Unlock()
		if calls := setup.Faux.CallCount(); calls != 2 {
			t.Fatalf("calls = %d, want 2", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("places a queued reset after the answer at the final boundary", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		resetTo(t, root, new("handoff"))
		first.release()
		must(input.Wait(testContext))
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		expectSettled(t, status(t, input), durable.SubmissionDone, "")
		expectTranscript(t, root, "pi.user:a", "pi.assistant:first", "pi.reset:handoff")
		expectLike(t, must(root.Context(testContext, nil)).Messages, `[{"role":"user","content":"handoff","timestamp":"$number"}]`)
		closeHarness(t, harness)
	})

	t.Run("resets an idle conversation at once, with or without handoff text", func(t *testing.T) {
		setup := chatSetup(t)
		setup.SetNow(func() float64 { return 7 })
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		appendNote(t, root, "note")
		resetTo(t, root, nil)
		view := must(root.Context(testContext, nil))
		if view.Head == nil || view.Head.Kind != "pi.reset" || view.Head.Model != nil {
			t.Fatalf("head = %s", jsonText(t, view.Head))
		}
		expectEqualJSON(t, view.Messages, `[]`)
		resetTo(t, root, new("carry on"))
		view = must(root.Context(testContext, nil))
		if view.Head.Head == nil || *view.Head.Head != view.Head.Id {
			t.Fatalf("head = %s", jsonText(t, view.Head))
		}
		expectEqualJSON(t, view.Messages, `[{"role":"user","content":"carry on","timestamp":7}]`)
		closeHarness(t, harness)
	})

	t.Run("makes a queued head write stale when it targets an entry before the active range", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		old := appendNote(t, root, "note")
		resetTo(t, root, nil)
		reset := must(root.Context(testContext, nil)).Head
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		stale := submit(t, root, writeDraft(durable.EntryDraft{Kind: "summary", Head: &old.Id}))
		fresh := submit(t, root, writeDraft(durable.EntryDraft{Kind: "summary", Head: &reset.Id}))
		first.release()
		must(input.Wait(testContext))
		expectSettled(t, status(t, stale), durable.SubmissionUnanswered, "stale")
		expectSettled(t, status(t, fresh), durable.SubmissionDone, "")
		expectEqualJSON(t, inboxOf(t, harness, root), `[]`)

		// The fresh summary's marker starts the range at the reset: a target inside the range is not stale, even when
		// it is older than the marker itself. A reset placed earlier in the same boundary makes it stale.
		var inside durable.EntryRecord
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "pi.user" {
				inside = entry
				break
			}
		}
		second := submit(t, root, inputDraft("b"))
		kept := submit(t, root, writeDraft(durable.EntryDraft{Kind: "summary", Head: &inside.Id}))
		must(second.Wait(testContext))
		expectSettled(t, status(t, kept), durable.SubmissionDone, "")
		closeHarness(t, harness)
	})

	t.Run("makes a head write stale behind a reset placed earlier in the same boundary", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		target := allEntries(t, root)[0]
		resetTo(t, root, nil)
		summary := submit(t, root, writeDraft(durable.EntryDraft{Kind: "summary", Head: &target.Id}))
		first.release()
		must(input.Wait(testContext))
		expectSettled(t, status(t, summary), durable.SubmissionUnanswered, "stale")
		closeHarness(t, harness)
	})

	t.Run("ends the run with a pi.reset entry when a tool requests a handoff", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		gate.resolve()
		holdTool(t, setup, gate, durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{Handoff: new("continue here")}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep()})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		settled := must(submit(t, root, inputDraft("a")).Wait(testContext))
		entries := allEntries(t, root)
		expectAnswered(t, settled, assistants(t, root)[0].Id)
		expectEqualJSON(t, transcript(entries), `["pi.user:a","pi.assistant","pi.tool-result","pi.reset:continue here"]`)
		last := entries[len(entries)-1]
		if last.Head == nil || *last.Head != last.Id {
			t.Fatalf("reset head = %v, want itself", last.Head)
		}
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		closeHarness(t, harness)
	})

	t.Run("drops an onYield continuation when the final boundary selects a follow-up", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second")})
		addHooks(t, setup.Registry, GenerationTask, continueOnce("more"))
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		followUp := submit(t, root, inputDraft("f"))
		first.release()
		must(followUp.Wait(testContext))
		expectSettled(t, status(t, input), durable.SubmissionDone, "")
		expectTranscript(t, root, "pi.user:a", "pi.assistant:first", "pi.user:f", "pi.assistant:second")
		closeHarness(t, harness)
	})

	t.Run("withdraws a queued submission and removes its item", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		kept := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note"}))
		withdrawn := submit(t, root, inputDraft("f"))
		if result := must(withdrawn.Abort(testContext)); result != durable.SubmissionAborted {
			t.Fatalf("abort = %s, want aborted", result)
		}
		expectSettled(t, must(withdrawn.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{{kept.Id(), "write"}}))
		first.release()
		must(input.Wait(testContext))
		expectSettled(t, status(t, kept), durable.SubmissionDone, "")
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("leaves queued items after a failed run until the next submission places them in order", func(t *testing.T) {
		setup := chatSetup(t)
		failing := gatedResponse(ai.FauxResponse{StopReason: "error", ErrorMessage: "invalid request"})
		setup.Faux.SetResponses([]ai.FauxResponseStep{failing.step, fauxAnswer("for f"), fauxAnswer("for g")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		failing.awaitReached(t)
		f := submit(t, root, inputDraft("f"))
		failing.release()
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionUnanswered, "model_error")
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if record := status(t, f); record.Status != durable.SubmissionQueued {
			t.Fatalf("status = %s, want queued", record.Status)
		}
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{{f.Id(), "followUp"}}))

		// Idle with a queued item: the new input queues behind it, and a final boundary places the older one first.
		g := submit(t, root, inputDraft("g", durable.WhenBusyReject))
		must(g.Wait(testContext))
		expectSettled(t, status(t, f), durable.SubmissionDone, "")
		lines := transcript(allEntries(t, root))
		expectEqualJSON(t, lines[len(lines)-4:], `["pi.user:f","pi.assistant:for f","pi.user:g","pi.assistant:for g"]`)
		closeHarness(t, harness)
	})

	t.Run("keeps queued submissions across reopen and settles them afterwards", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("first again"), fauxAnswer("f")})
		harness, root := openAt(t, path, setup)
		submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		f := submit(t, root, inputDraft("f")).Id()
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		expectSettled(t, must(submissionOfHarness(t, harness, f).Wait(testContext)), durable.SubmissionDone, "")
		lines := transcript(allEntries(t, root))
		expectEqualJSON(t, lines[len(lines)-2:], `["pi.user:f","pi.assistant:f"]`)
		closeHarness(t, harness)
	})

	t.Run("commits inbox changes as positional Chord operations and a base when empty", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		var mu sync.Mutex
		ops := [][]durable.Op{}
		harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication) {
				if change.Record.Kind == "pi.inbox" && len(change.Ops) > 0 {
					mu.Lock()
					ops = append(ops, slices.Clone(change.Ops))
					mu.Unlock()
				}
			}
		})
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		w1 := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note"}))
		f1 := submit(t, root, inputDraft("f1"))
		w2 := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note"}))
		f2 := submit(t, root, inputDraft("f2"))
		w3 := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note"}))
		write := func(index int, id durable.SubmissionId) []any {
			return []any{[]any{"p", []any{"items"}, index, 0, []any{map[string]any{"id": id, "mode": "write", "entry": map[string]any{"kind": "note"}}}}}
		}
		followUp := func(index int, id durable.SubmissionId, content string) []any {
			return []any{[]any{"p", []any{"items"}, index, 0, []any{map[string]any{"id": id, "mode": "followUp", "content": content}}}}
		}
		mu.Lock()
		expectEqualJSON(t, ops, jsonText(t, []any{write(0, w1.Id()), followUp(1, f1.Id(), "f1"), write(2, w2.Id()), followUp(3, f2.Id(), "f2"), write(4, w3.Id())}))
		mu.Unlock()
		first.release()
		must(input.Wait(testContext))
		// Commit listeners all run before upstream's wait continuation; here the waiter wakes concurrently with them.
		waitFor(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(ops) > 5
		})
		// Every write and the first follow-up leave; only f2 at index 3 remains. No retained value is carried.
		mu.Lock()
		removal := ops[5]
		mu.Unlock()
		for _, op := range removal {
			inserted, _ := op[4].([]any)
			if op[0] != "p" || len(inserted) != 0 {
				t.Fatalf("removal = %s, want only empty positional splices", jsonText(t, removal))
			}
		}
		expectContainsLike(t, removal, `[["p",["items"],4,1,[]]]`)
		if strings.Contains(jsonText(t, removal), "f2") {
			t.Fatalf("removal %s carries f2", jsonText(t, removal))
		}
		must(f2.Wait(testContext))
		closeHarness(t, harness)
	})

	t.Run("settles a stale write at once while idle, and a queued one behind waiting items", func(t *testing.T) {
		setup := chatSetup(t)
		failing := gatedResponse(ai.FauxResponse{StopReason: "error", ErrorMessage: "invalid request"})
		setup.Faux.SetResponses([]ai.FauxResponseStep{failing.step, fauxAnswer("for f")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		old := appendNote(t, root, "note")
		resetTo(t, root, nil)
		before := len(allEntries(t, root))
		idle := submit(t, root, writeDraft(durable.EntryDraft{Kind: "summary", Head: &old.Id}))
		expectSettled(t, must(idle.Wait(testContext)), durable.SubmissionUnanswered, "stale")
		if after := len(allEntries(t, root)); after != before {
			t.Fatalf("entries = %d, want %d", after, before)
		}

		// After a failed run, a follow-up waits; a stale write queues behind it and the boundary rejects it.
		input := submit(t, root, inputDraft("a"))
		failing.awaitReached(t)
		f := submit(t, root, inputDraft("f"))
		failing.release()
		must(input.Wait(testContext))
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		queued := submit(t, root, writeDraft(durable.EntryDraft{Kind: "summary", Head: &old.Id}))
		expectSettled(t, must(queued.Wait(testContext)), durable.SubmissionUnanswered, "stale")
		expectSettled(t, must(f.Wait(testContext)), durable.SubmissionDone, "")
		closeHarness(t, harness)
	})

	t.Run("keeps an onYield continuation across a queued plain write, with the run's original input", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second")})
		addHooks(t, setup.Registry, GenerationTask, continueOnce("more"))
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		write := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note"}))
		first.release()
		must(input.Wait(testContext))
		entries := allEntries(t, root)
		expectEqualJSON(t, transcript(entries), `["pi.user:a","pi.assistant:first","note","pi.user:more","pi.assistant:second"]`)
		expectAnswered(t, status(t, input), entries[len(entries)-1].Id)
		expectSettled(t, status(t, write), durable.SubmissionDone, "")
		closeHarness(t, harness)
	})

	t.Run("drops an onYield continuation for a queued reset", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			return &YieldContinue{Continue: ai.UserText("more")}, nil
		}})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		resetTo(t, root, nil)
		first.release()
		must(input.Wait(testContext))
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		expectSettled(t, status(t, input), durable.SubmissionDone, "")
		expectTranscript(t, root, "pi.user:a", "pi.assistant:first", "pi.reset")
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("adds every steer to the run at the postTools boundary with steeringMode all", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		holdTool(t, setup, gate)
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep(), fauxAnswer("after tools"), fauxAnswer("follow-up")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		setup.SetSettings(func(settings *HarnessSettings) { settings.SteeringMode = durable.QueueAll })
		input := submit(t, root, inputDraft("a"))
		toolRunning(t, harness, root)
		s1 := submit(t, root, inputDraft("s1", durable.WhenBusySteer))
		f := submit(t, root, inputDraft("f"))
		s2 := submit(t, root, inputDraft("s2", durable.WhenBusySteer))
		gate.resolve()
		must(f.Wait(testContext))
		expectTranscript(t, root, "pi.user:a", "pi.assistant", "pi.tool-result", "pi.user:s1", "pi.user:s2", "pi.assistant:after tools", "pi.user:f", "pi.assistant:follow-up")
		answers := assistants(t, root)
		for _, submission := range []durable.Submission{input, s1, s2} {
			expectAnswered(t, status(t, submission), answers[1].Id)
		}
		closeHarness(t, harness)
	})

	t.Run("queues an idle steer behind waiting items and places it with the first follow-up in ID order", func(t *testing.T) {
		setup := chatSetup(t)
		failing := gatedResponse(ai.FauxResponse{StopReason: "error", ErrorMessage: "invalid request"})
		setup.Faux.SetResponses([]ai.FauxResponseStep{failing.step, fauxAnswer("both")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		failing.awaitReached(t)
		f := submit(t, root, inputDraft("f"))
		failing.release()
		must(input.Wait(testContext))
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		steer := submit(t, root, inputDraft("s", durable.WhenBusySteer))
		settled := must(steer.Wait(testContext))
		if settled.Answer == nil {
			t.Fatalf("steer = %s, want done", jsonText(t, settled))
		}
		expectAnswered(t, status(t, f), *settled.Answer)
		lines := transcript(allEntries(t, root))
		expectEqualJSON(t, lines[len(lines)-3:], `["pi.user:f","pi.user:s","pi.assistant:both"]`)
		closeHarness(t, harness)
	})

	t.Run("starts a queued follow-up after a terminating round", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		holdTool(t, setup, gate, durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{Terminate: true}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep(), fauxAnswer("follow-up")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		toolRunning(t, harness, root)
		f := submit(t, root, inputDraft("f"))
		gate.resolve()
		must(f.Wait(testContext))
		expectAnswered(t, status(t, input), assistants(t, root)[0].Id)
		expectTranscript(t, root, "pi.user:a", "pi.assistant", "pi.tool-result", "pi.user:f", "pi.assistant:follow-up")
		closeHarness(t, harness)
	})

	t.Run("writes the last handoff in call order and then runs queued follow-ups in the new context", func(t *testing.T) {
		setup := chatSetup(t)
		firstGate, secondGate := deferred(), deferred()
		holdTool(t, setup, firstGate, durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{Handoff: new("one")}})
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "later", Description: "Finishes first", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
			Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				if err := secondGate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{Handoff: new("two")}}, nil
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("hold", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"}), ai.FauxToolCall("later", map[string]any{}, &ai.FauxToolCallOptions{ID: "c2"})), fauxAnswer("follow-up")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		toolRunning(t, harness, root)
		f := submit(t, root, inputDraft("f"))
		secondGate.resolve()
		waitFor(t, func() bool {
			state := liveOf(t, harness, root.Id())
			return len(state.Tools) > 1 && state.Tools[1].Status == ToolSlotDone
		})
		firstGate.resolve()
		must(f.Wait(testContext))
		expectSettled(t, status(t, input), durable.SubmissionDone, "")
		lines := transcript(allEntries(t, root))
		expectEqualJSON(t, lines[len(lines)-4:], `["pi.tool-result","pi.reset:two","pi.user:f","pi.assistant:follow-up"]`)
		closeHarness(t, harness)
	})

	t.Run("leaves the inbox alone when the run's task is aborted", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("never")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		f := submit(t, root, inputDraft("f"))
		must(harness.AbortTask(testContext, liveOf(t, harness, root.Id()).Run.TaskId))
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if record := status(t, f); record.Status != durable.SubmissionQueued {
			t.Fatalf("status = %s, want queued", record.Status)
		}
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{{f.Id(), "followUp"}}))
		closeHarness(t, harness)
	})

	t.Run("returns a queued submission for its repeated request ID without a second item", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		draft := inputDraft("f")
		draft.RequestId = new("r")
		queued := submit(t, root, draft)
		again := submit(t, root, draft)
		if again.Id() != queued.Id() {
			t.Fatalf("again = %d, want %d", again.Id(), queued.Id())
		}
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{{queued.Id(), "followUp"}}))
		closeHarness(t, harness)
	})

	t.Run("withdraws a middle item with one positional removal", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		items := []durable.Submission{}
		for _, text := range []string{"x", "y", "z"} {
			items = append(items, submit(t, root, inputDraft(text)))
		}
		var mu sync.Mutex
		ops := [][]durable.Op{}
		harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication) {
				if change.Record.Kind == "pi.inbox" && len(change.Ops) > 0 {
					mu.Lock()
					ops = append(ops, slices.Clone(change.Ops))
					mu.Unlock()
				}
			}
		})
		must(items[1].Abort(testContext))
		mu.Lock()
		expectEqualJSON(t, ops, `[[["p",["items"],1,1,[]]]]`)
		mu.Unlock()
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{{items[0].Id(), "followUp"}, {items[2].Id(), "followUp"}}))
		closeHarness(t, harness)
	})

	t.Run("stores the inbox as a base exactly when it becomes empty", func(t *testing.T) {
		store := &inboxRecordingStorage{MemoryStorage: storage.NewMemoryStorage()}
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second")})
		harness, root := openChat(t, store, setup)
		harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication) {
				if change.Record.Kind == "pi.inbox" {
					store.setInbox(change.Record.Id)
				}
			}
		})
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		submit(t, root, inputDraft("f1"))
		f2 := submit(t, root, inputDraft("f2"))
		first.release()
		must(input.Wait(testContext))
		must(f2.Wait(testContext))
		// The inbox ID is learned from the f1 push; later writes: the f2 push, removing f1, and emptying the inbox.
		store.mu.Lock()
		written := slices.Clone(store.written)
		store.mu.Unlock()
		expectEqualJSON(t, written, `[{"kind":"delta","empty":false},{"kind":"delta","empty":false},{"kind":"base","empty":true}]`)
		closeHarness(t, harness)
	})

	t.Run("keeps a complete inbox base exactly while it is empty, and a usage base on every change", func(t *testing.T) {
		info := durable.CheckpointInfo{DeltasSinceBase: 1000}
		checkpoint := func(token durable.AnyDocToken, value string) bool {
			return must(token.AnyDefinition().CheckpointWhen(must(delta.DecodeJson([]byte(value))).(*delta.JsonObject), nil, info))
		}
		if !checkpoint(InboxDoc, `{"items":[]}`) || checkpoint(InboxDoc, `{"items":[{"id":1,"mode":"followUp","content":"x"}]}`) || !checkpoint(UsageDoc, `{"models":{},"tools":{}}`) {
			t.Fatal("checkpoint predicates disagree with upstream")
		}
	})
}

func TestUsage(t *testing.T) {
	t.Run("totals assistant usage per model and tool usage per tool, and sums the Session", func(t *testing.T) {
		setup := chatSetup(t)
		spent := ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1}}
		gate := deferred()
		gate.resolve()
		holdTool(t, setup, gate, durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Usage: &spent})
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep(), fauxAnswer("done"), fauxAnswer("other")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(submit(t, root, inputDraft("a")).Wait(testContext))
		entries := allEntries(t, root)
		expected := map[string]float64{}
		for _, entry := range assistants(t, root) {
			usage := entry.Model[0].(ai.AssistantMessage).Usage
			expected["input"] += float64(usage.Input)
			expected["output"] += float64(usage.Output)
			expected["totalTokens"] += float64(usage.TotalTokens)
		}
		usage := must(durable.Snapshot(testContext, harness, UsageDoc, root.Id()))
		expectMatch(t, usage.Models["faux/faux-1"], jsonText(t, expected))
		expectEqualJSON(t, usage.Tools, jsonText(t, map[string]any{"hold": spent}))
		for _, entry := range entries {
			if entry.Kind == "pi.tool-result" {
				expectEqualJSON(t, entry.Model[0].(ai.ToolResultMessage).Usage, jsonText(t, spent))
			}
		}

		// A fork starts at zero; the Session total adds every conversation once.
		fork := must(root.Fork(testContext, entries[len(entries)-1].Id, ConversationCreateOptions{Ownership: ownerless}))
		expectEqualJSON(t, must(durable.Snapshot(testContext, harness, UsageDoc, fork.Id())), `{"models":{},"tools":{}}`)
		must(submit(t, fork, inputDraft("b")).Wait(testContext))
		forkUsage := must(durable.Snapshot(testContext, harness, UsageDoc, fork.Id())).Models["faux/faux-1"]
		total := must(harness.Usage(testContext))
		if got, want := float64(total.Models["faux/faux-1"].Output), expected["output"]+float64(forkUsage.Output); got != want {
			t.Fatalf("total output = %v, want %v", got, want)
		}
		expectEqualJSON(t, total.Tools, jsonText(t, map[string]any{"hold": spent}))
		closeHarness(t, harness)
	})

	t.Run("counts failed attempts, converted partials, and tool usage replaced by afterTool", func(t *testing.T) {
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 200, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		spent := ai.Usage{Input: 5, TotalTokens: 5}
		gate := deferred()
		gate.resolve()
		holdTool(t, setup, gate)
		addHooks(t, setup.Registry, ToolTask, &ToolHooks{AfterTool: func(_ context.Context, _ ai.ToolCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
			result.Usage = &spent
			return &result, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{error503(), holdStep(), fauxAnswer("done"), fauxAnswer(strings.Repeat("x", 400))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(1), BaseDelayMs: new(1)}
		})
		must(submit(t, root, inputDraft("a")).Wait(testContext))
		expectEqualJSON(t, must(durable.Snapshot(testContext, harness, UsageDoc, root.Id())).Tools, jsonText(t, map[string]any{"hold": spent}))

		// A partial committed while streaming becomes an aborted entry on abort; its usage counts too.
		input := submit(t, root, inputDraft("b"))
		waitFor(t, func() bool {
			state := liveOf(t, harness, root.Id())
			return state.Generation != nil && state.Generation.Message != nil
		})
		must(harness.AbortTask(testContext, liveOf(t, harness, root.Id()).Run.TaskId))
		must(input.Wait(testContext))
		stops := []string{}
		output := 0
		for _, entry := range assistants(t, root) {
			message := entry.Model[0].(ai.AssistantMessage)
			stops = append(stops, string(message.StopReason))
			output += message.Usage.Output
		}
		expectEqualJSON(t, stops, `["error","toolUse","stop","aborted"]`)
		if got := must(durable.Snapshot(testContext, harness, UsageDoc, root.Id())).Models["faux/faux-1"].Output; got != output {
			t.Fatalf("ledger output = %d, want %d", got, output)
		}
		closeHarness(t, harness)
	})

	t.Run("keeps tools named like object prototype keys in the ledger and the Session total", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		usage := ai.Usage{Input: 1, Output: 1, TotalTokens: 2}
		names := []string{"constructor", "__proto__", "toString"}
		id := root.Id()
		for _, name := range names {
			for range 2 {
				commitValue(t, root, func(tx durable.Tx) (struct{}, error) { return struct{}{}, RecordUsage(tx, id, UsageTools, name, usage) })
			}
		}
		total := must(harness.Usage(testContext))
		// Go maps have no insertion order, so the key order of Object.keys is not asserted; the set and values are.
		if len(total.Tools) != len(names) {
			t.Fatalf("tools = %s", jsonText(t, total.Tools))
		}
		for _, name := range names {
			if total.Tools[name].Output != 2 {
				t.Fatalf("%s output = %d, want 2", name, total.Tools[name].Output)
			}
		}
		closeHarness(t, harness)
	})

	t.Run("keeps usage totals exact across reopen, counting a partial converted after reopen once", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 200, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first"), fauxAnswer(strings.Repeat("x", 400)), fauxAnswer("again")})
		harness, root := openAt(t, path, setup)
		must(submit(t, root, inputDraft("a")).Wait(testContext))
		submit(t, root, inputDraft("b"))
		waitFor(t, func() bool {
			state := liveOf(t, harness, root.Id())
			return state.Generation != nil && state.Generation.Message != nil
		})
		// Closing mid-stream keeps the committed partial; the reopened request converts it into an aborted entry.
		closeHarness(t, harness)
		harness, root = openAt(t, path, setup)
		harness.Resume()
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		stops := []string{}
		var input, output, totalTokens int
		for _, entry := range assistants(t, root) {
			message := entry.Model[0].(ai.AssistantMessage)
			stops = append(stops, string(message.StopReason))
			input += message.Usage.Input
			output += message.Usage.Output
			totalTokens += message.Usage.TotalTokens
		}
		expectEqualJSON(t, stops, `["stop","aborted","stop"]`)
		total := must(harness.Usage(testContext)).Models["faux/faux-1"]
		if total.Input != input || total.Output != output || total.TotalTokens != totalTokens {
			t.Fatalf("total = %+v, want %d/%d/%d", total, input, output, totalTokens)
		}
		closeHarness(t, harness)
	})

	t.Run("records usage as numeric sets on the ledger in the entry's commit", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("one"), fauxAnswer("two")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		type usageCommit struct {
			entries int
			ops     []durable.Op
		}
		var mu sync.Mutex
		commits := []usageCommit{}
		harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication) {
				if change.Record.Kind != "pi.usage" {
					continue
				}
				entries := 0
				for _, other := range publication.Changes {
					if _, ok := other.(durable.EntryWrite); ok {
						entries++
					}
				}
				mu.Lock()
				commits = append(commits, usageCommit{entries: entries, ops: change.Ops})
				mu.Unlock()
			}
		})
		must(submit(t, root, inputDraft("a")).Wait(testContext))
		must(submit(t, root, inputDraft("b")).Wait(testContext))
		waitFor(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(commits) >= 2
		})
		mu.Lock()
		defer mu.Unlock()
		if len(commits) != 2 {
			t.Fatalf("usage commits = %d, want 2", len(commits))
		}
		expectLike(t, commits[0].ops, `[["s",["models","faux/faux-1"],"$object"]]`)
		for _, op := range commits[1].ops {
			path, _ := op[1].([]any)
			if op[0] != "s" || len(path) < 2 || path[1] != "faux/faux-1" {
				t.Fatalf("second usage commit = %s, want numeric sets under faux/faux-1", jsonText(t, commits[1].ops))
			}
		}
		for _, commit := range commits {
			if commit.entries < 1 {
				t.Fatal("a usage commit carries no entry")
			}
		}
		closeHarness(t, harness)
	})
}

type markerState struct {
	N float64 `json:"n"`
}

// expectSameSettlement is toEqual({...settled, id, entry: expect.any(Number)}).
func expectSameSettlement(t *testing.T, got, settled durable.SubmissionRecord, id durable.SubmissionId) {
	t.Helper()
	want := settled
	want.Id = id
	if got.Entry == nil {
		t.Fatalf("record %s has no entry", jsonText(t, got))
	}
	want.Entry = got.Entry
	expectEqualJSON(t, got, jsonText(t, want))
}

// continueOnce is an onYield hook that continues with text on its first call only.
func continueOnce(text string) *GenerationHooks {
	var mu sync.Mutex
	yields := 0
	return &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
		mu.Lock()
		defer mu.Unlock()
		yields++
		if yields == 1 {
			return &YieldContinue{Continue: ai.UserText(text)}, nil
		}
		return nil, nil
	}}
}

type inboxWrite struct {
	Kind  string `json:"kind"`
	Empty bool   `json:"empty"`
}

// inboxRecordingStorage records each pi.inbox write as a base or a delta, and whether a base is empty.
type inboxRecordingStorage struct {
	*storage.MemoryStorage
	mu      sync.Mutex
	inboxId *durable.DocumentId
	written []inboxWrite
}

func (store *inboxRecordingStorage) setInbox(id durable.DocumentId) {
	store.mu.Lock()
	store.inboxId = &id
	store.mu.Unlock()
}

func (store *inboxRecordingStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	store.mu.Lock()
	for _, write := range writes {
		change, ok := write.(durable.DocumentChangeWrite)
		if !ok || store.inboxId == nil || change.Id != *store.inboxId {
			continue
		}
		items, _ := change.Content.Value.Value("items").([]any)
		empty := change.Content.Kind == "base" && len(items) == 0
		store.written = append(store.written, inboxWrite{Kind: string(change.Content.Kind), Empty: empty})
	}
	store.mu.Unlock()
	return store.MemoryStorage.Commit(ctx, writes)
}
