// Ports packages/durable/test/harness-input-entries.test.ts.

package harness

// pi: packages/durable/src/harness/inbox.ts
// pi: packages/durable/src/harness/submissions.ts

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// requestLog is a faux response that records the role and text of each request message, as the provider receives them.
func requestLog(requests *[][]string, mu *sync.Mutex, reply ai.FauxResponseStep) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(transcript ai.TranscriptContext, options ai.StreamOptions, state *ai.FauxProviderState, model *ai.Model) (ai.AssistantMessage, error) {
		lines := []string{}
		for _, message := range transcript.Messages() {
			text, _ := textOf(message)
			lines = append(lines, string(message.MessageRole())+":"+text)
		}
		mu.Lock()
		*requests = append(*requests, lines)
		mu.Unlock()
		if response, static := reply.(ai.FauxResponse); static {
			return response.AssistantMessage(), nil
		}
		return reply.(ai.FauxResponseFactory)(transcript, options, state, model)
	})
}

// dataTranscript is the kind, application data and model text of each entry, skipping system entries.
func dataTranscript(t *testing.T, entries []durable.EntryRecord) []string {
	t.Helper()
	lines := []string{}
	for _, entry := range entries {
		if entry.Kind == "pi.system" {
			continue
		}
		line := entry.Kind
		if entry.Data != nil && !strings.HasPrefix(entry.Kind, "pi.") {
			line += "|" + jsonText(t, entry.Data)
		}
		if len(entry.Model) > 0 {
			if _, isResult := entry.Model[0].(ai.ToolResultMessage); !isResult {
				if text, ok := textOf(entry.Model[0]); ok {
					line += "|" + text
				}
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func mention(text string) *durable.InputEntry {
	return &durable.InputEntry{Kind: "chat.mention", Data: map[string]any{"author": "ann", "text": text}}
}

func inputWith(content string, entry *durable.InputEntry, whenBusy durable.WhenBusy) durable.SubmissionDraft {
	return durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content), InputEntry: entry, WhenBusy: whenBusy}
}

func inboxItemsOf(t *testing.T, harness Harness, conversation Conversation) []InboxItem {
	t.Helper()
	return must(durable.Snapshot(testContext, harness, InboxDoc, conversation.Id())).Items
}

func inboxIds(items []InboxItem) []durable.SubmissionId {
	ids := []durable.SubmissionId{}
	for _, item := range items {
		ids = append(ids, item.Id)
	}
	return ids
}

// TestInputEntries: an input places as the entry it names (harness-input-entries.test.ts "input entries").
func TestInputEntries(t *testing.T) {
	// harness-input-entries.test.ts:99 it("places an idle input as its entry: the given kind and data, with the content as its user message")
	t.Run("places an idle input as its entry: the given kind and data, with the content as its user message", func(t *testing.T) {
		setup := chatSetup(t)
		setup.SetNow(func() float64 { return 42 })
		var requests [][]string
		var mu sync.Mutex
		setup.Faux.SetResponses([]ai.FauxResponseStep{requestLog(&requests, &mu, fauxAnswer("hi ann"))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := submit(t, root, inputWith("ann: hi", mention("hi"), ""))
		settled := must(submission.Wait(testContext))
		entries := allEntries(t, root)
		expectEqualJSON(t, dataTranscript(t, entries), `["chat.mention|{\"author\":\"ann\",\"text\":\"hi\"}|ann: hi","pi.assistant|hi ann"]`)
		var placed durable.EntryRecord
		for _, entry := range entries {
			if entry.Kind == "chat.mention" {
				placed = entry
			}
		}
		expectEqualJSON(t, placed.Model, `[{"role":"user","content":"ann: hi","timestamp":42}]`)
		if settled.Status != durable.SubmissionDone || settled.Entry == nil || *settled.Entry != placed.Id || settled.Answer == nil || *settled.Answer != entries[len(entries)-1].Id {
			t.Fatalf("settled = %s, want done with entry %d and the last entry as the answer", jsonText(t, settled), placed.Id)
		}
		if len(requests) != 1 || strings.Join(requests[0], "|") != "user:ann: hi" {
			t.Fatalf("requests = %v, want [[user:ann: hi]]", requests)
		}
		// The context of a fork at the input is the same as for a pi.user entry.
		view := must(root.Context(testContext, &durable.ContextOptions{At: &placed.Id}))
		var messages []ai.Message
		for _, message := range view.Messages {
			if message.MessageRole() != "system" {
				messages = append(messages, message)
			}
		}
		expectEqualJSON(t, messages, jsonText(t, placed.Model))
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:122 it("rejects an input entry whose kind is not a non-empty string or starts with pi., writing nothing").
	// The typed InputEntry.Kind is a string, so Pi's kind 123 has no form here; durable/core/inbox
	// TestAnInputEntryWhoseKindIsNotANonEmptyStringOutsidePiIsATypeErrorThatWritesNothing rejects it on the JSON draft path.
	t.Run("rejects an input entry whose kind is empty or starts with pi., writing nothing", func(t *testing.T) {
		setup := chatSetup(t)
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		before := len(allEntries(t, root))
		for _, kind := range []string{"", "pi.user", "pi.compaction"} {
			_, err := root.Submit(testContext, inputWith("x", &durable.InputEntry{Kind: kind}, ""))
			var typeError *TypeError
			if want := `An input entry's kind must be a non-empty string not starting with "pi.", got ` + kind; err == nil || err.Error() != want || !errors.As(err, &typeError) {
				t.Fatalf("kind %q: err = %v, want the TypeError %q", kind, err, want)
			}
		}
		// The kind is checked before a known request ID returns its submission (submissions.ts admitSubmission).
		known := durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: "n"}, RequestId: new("r-known")}
		if _, err := root.Submit(testContext, known); err != nil {
			t.Fatal(err)
		}
		before++
		again := inputWith("x", &durable.InputEntry{Kind: "pi.user"}, "")
		again.RequestId = new("r-known")
		var typeError *TypeError
		if _, err := root.Submit(testContext, again); !errors.As(err, &typeError) {
			t.Fatalf("a known request ID with a pi. kind: err = %v, want the TypeError", err)
		}
		if after := len(allEntries(t, root)); after != before {
			t.Fatalf("entries = %d, want %d", after, before)
		}
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		// Writes keep their kinds: reset() writes pi.reset.
		resetTo(t, root, nil)
		if entries := allEntries(t, root); entries[len(entries)-1].Kind != "pi.reset" {
			t.Fatalf("last entry = %s, want pi.reset", entries[len(entries)-1].Kind)
		}
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:138 it("takes only kind and data from an untyped caller's entry")
	t.Run("takes only kind and data from an untyped caller's entry", func(t *testing.T) {
		t.Skip("Pi harness-input-entries.test.ts:138: the typed InputEntry carries only Kind and Data, so no caller can add model, head or edits; durable/core/inbox TestAnUntypedCallersInputEntryGivesOnlyItsKindAndData ports the case on the JSON draft path")
	})

	// harness-input-entries.test.ts:172 it("queues busy inputs with their entries in the inbox and places them as those entries at boundaries")
	t.Run("queues busy inputs with their entries in the inbox and places them as those entries at boundaries", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		holdTool(t, setup, gate)
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep(), fauxAnswer("after tools"), fauxAnswer("follow-up")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		toolRunning(t, harness, root)
		draft := inputWith("s", mention("s"), durable.WhenBusySteer)
		draft.RequestId = new("r-s")
		steer := submit(t, root, draft)
		followUp := submit(t, root, inputWith("f", &durable.InputEntry{Kind: "p2.input"}, durable.WhenBusyFollowUp))
		items := inboxItemsOf(t, harness, root)
		expectEqualJSON(t, items, jsonText(t, []any{
			map[string]any{"id": steer.Id(), "mode": "steer", "content": "s", "entry": map[string]any{"kind": "chat.mention", "data": map[string]any{"author": "ann", "text": "s"}}},
			map[string]any{"id": followUp.Id(), "mode": "followUp", "content": "f", "entry": map[string]any{"kind": "p2.input"}},
		}))
		// The request ID deduplicates a queued input with an entry as any other.
		again := inputWith("other", mention("other"), durable.WhenBusySteer)
		again.RequestId = new("r-s")
		if got := submit(t, root, again); got.Id() != steer.Id() {
			t.Fatalf("again = %d, want %d", got.Id(), steer.Id())
		}
		_, err := root.Submit(testContext, inputWith("x", mention("x"), durable.WhenBusyReject))
		if _, ok := errors.AsType[*durable.ConversationBusy](err); !ok {
			t.Fatalf("err = %v, want ConversationBusy", err)
		}
		if got := len(inboxItemsOf(t, harness, root)); got != 2 {
			t.Fatalf("inbox = %d items, want 2", got)
		}

		gate.resolve()
		must(followUp.Wait(testContext))
		entries := allEntries(t, root)
		expectEqualJSON(t, dataTranscript(t, entries), `["pi.user|a","pi.assistant","pi.tool-result","chat.mention|{\"author\":\"ann\",\"text\":\"s\"}|s","pi.assistant|after tools","p2.input|f","pi.assistant|follow-up"]`)
		answers := assistants(t, root)
		placedId := func(kind string) durable.EntryId {
			for _, entry := range entries {
				if entry.Kind == kind {
					return entry.Id
				}
			}
			t.Fatalf("no %s entry", kind)
			return 0
		}
		expectAnswered(t, status(t, input), answers[1].Id)
		if record := status(t, steer); record.Status != durable.SubmissionDone || record.Entry == nil || *record.Entry != placedId("chat.mention") || record.Answer == nil || *record.Answer != answers[1].Id {
			t.Fatalf("steer = %s", jsonText(t, record))
		}
		if record := status(t, followUp); record.Status != durable.SubmissionDone || record.Entry == nil || *record.Entry != placedId("p2.input") || record.Answer == nil || *record.Answer != answers[2].Id {
			t.Fatalf("followUp = %s", jsonText(t, record))
		}
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:236 it("withdraws queued inputs with entries, one by one and by conversation abort")
	t.Run("withdraws queued inputs with entries, one by one and by conversation abort", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		withdrawn := submit(t, root, inputWith("w", mention("w"), ""))
		aborted := submit(t, root, inputWith("x", mention("x"), ""))
		if result := must(withdrawn.Abort(testContext)); result != durable.SubmissionAborted {
			t.Fatalf("abort = %s", result)
		}
		if ids := inboxIds(inboxItemsOf(t, harness, root)); len(ids) != 1 || ids[0] != aborted.Id() {
			t.Fatalf("inbox = %v, want [%d]", ids, aborted.Id())
		}
		mustOK(t, root.Abort(testContext, nil))
		for _, submission := range []durable.Submission{withdrawn, aborted, input} {
			expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		}
		if items := inboxItemsOf(t, harness, root); len(items) != 0 {
			t.Fatalf("inbox = %v, want empty", items)
		}
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "chat.mention" {
				t.Fatalf("a withdrawn input was placed: %+v", entry)
			}
		}
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:391 it("keeps a queued input's entry across reopen and places it as that entry") (Pi's "data-only writes" describe)
	t.Run("keeps a queued input's entry across reopen and places it as that entry", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("first again"), fauxAnswer("for f")})
		harness, root := openAt(t, path, setup)
		submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		f := submit(t, root, inputWith("f", mention("f"), "")).Id()
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		for _, item := range inboxItemsOf(t, harness, root) {
			if item.Id == f {
				expectEqualJSON(t, item.Entry, `{"kind":"chat.mention","data":{"author":"ann","text":"f"}}`)
			}
		}
		settled := must(submissionOfHarness(t, harness, f).Wait(testContext))
		var placed durable.EntryRecord
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "chat.mention" {
				placed = entry
			}
		}
		if settled.Status != durable.SubmissionDone || settled.Entry == nil || *settled.Entry != placed.Id {
			t.Fatalf("settled = %s, want done with entry %d", jsonText(t, settled), placed.Id)
		}
		expectEqualJSON(t, placed.Data, `{"author":"ann","text":"f"}`)
		if len(placed.Model) != 1 || placed.Model[0].MessageRole() != "user" {
			t.Fatalf("model = %s, want one user message", jsonText(t, placed.Model))
		}
		if text, _ := textOf(placed.Model[0]); text != "f" {
			t.Fatalf("model text = %q, want f", text)
		}
		closeHarness(t, harness)
	})
}

// TestDataOnlyWrites: a write of only kind and data is appended at once (harness-input-entries.test.ts "data-only writes").
func TestDataOnlyWrites(t *testing.T) {
	// harness-input-entries.test.ts:259 it("appends a data-only write mid-run in time order, leaving the model's messages unchanged")
	t.Run("appends a data-only write mid-run in time order, leaving the model's messages unchanged", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		holdTool(t, setup, gate, durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "held"}}})
		var requests [][]string
		var mu sync.Mutex
		setup.Faux.SetResponses([]ai.FauxResponseStep{requestLog(&requests, &mu, holdStep()), requestLog(&requests, &mu, fauxAnswer("done"))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		var events []AgentEvent
		stream := must(WatchEvents(testContext, harness, root.Id()))
		stream.Start(func(_ context.Context, batch []AgentEvent) error {
			mu.Lock()
			events = append(events, batch...)
			mu.Unlock()
			return nil
		})
		input := submit(t, root, inputDraft("a"))
		toolRunning(t, harness, root)
		chat := durable.EntryDraft{Kind: "chat.message", Data: map[string]any{"author": "bob", "text": "lunch?"}}
		draft := writeDraft(chat)
		draft.RequestId = new("bob:1")
		write := submit(t, root, draft)
		// Settled and appended in its own commit, between the call and its still-running tool's result.
		settled := status(t, write)
		if settled.Status != durable.SubmissionDone || settled.Entry == nil {
			t.Fatalf("write = %s, want done", jsonText(t, settled))
		}
		if items := inboxItemsOf(t, harness, root); len(items) != 0 {
			t.Fatalf("inbox = %v, want empty", items)
		}
		expectEqualJSON(t, dataTranscript(t, allEntries(t, root)), `["pi.user|a","pi.assistant","chat.message|{\"author\":\"bob\",\"text\":\"lunch?\"}"]`)
		count := func(kind string) int {
			mu.Lock()
			defer mu.Unlock()
			n := 0
			for _, event := range events {
				if event.EventType() == kind {
					n++
				}
			}
			return n
		}
		waitFor(t, func() bool { return count("entry_appended") > 0 })
		mu.Lock()
		var appended *EntryAppendedEvent
		for _, event := range events {
			if each, ok := event.(EntryAppendedEvent); ok {
				appended = &each
				break
			}
		}
		mu.Unlock()
		if appended == nil || appended.Entry.Kind != "chat.message" || appended.Entry.Id != *settled.Entry {
			t.Fatalf("entry_appended = %+v, want the chat.message entry %d", appended, *settled.Entry)
		}
		if count("run_end") != 0 {
			t.Fatal("the run ended")
		}
		// A repeated request ID returns the same write without a second entry.
		if again := submit(t, root, draft); again.Id() != write.Id() {
			t.Fatalf("again = %d, want %d", again.Id(), write.Id())
		}

		gate.resolve()
		must(input.Wait(testContext))
		expectEqualJSON(t, dataTranscript(t, allEntries(t, root)), `["pi.user|a","pi.assistant","chat.message|{\"author\":\"bob\",\"text\":\"lunch?\"}","pi.tool-result","pi.assistant|done"]`)
		mu.Lock()
		got := requests
		mu.Unlock()
		expectEqualJSON(t, got, `[["system:","user:a"],["system:","user:a","assistant:","toolResult:held"]]`)
		_, _ = stream.Stop()
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:310 it("queues a data-only write behind a queued reset, so it lands after the reset and stays visible")
	t.Run("queues a data-only write behind a queued reset, so it lands after the reset and stays visible", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		resetTo(t, root, nil)
		chat := submit(t, root, writeDraft(durable.EntryDraft{Kind: "chat.message", Data: "hi"}))
		if got := status(t, chat).Status; got != durable.SubmissionQueued {
			t.Fatalf("status = %s, want queued", got)
		}
		first.release()
		must(input.Wait(testContext))
		settled := must(chat.Wait(testContext))
		var reset durable.EntryRecord
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "pi.reset" {
				reset = entry
			}
		}
		if settled.Status != durable.SubmissionDone || settled.Entry == nil || *settled.Entry <= reset.Id {
			t.Fatalf("settled = %s, want done after the reset %d", jsonText(t, settled), reset.Id)
		}
		view := must(root.Context(testContext, nil))
		kinds := []string{}
		for _, entry := range view.Entries {
			kinds = append(kinds, entry.Kind)
		}
		expectEqualJSON(t, kinds, `["pi.reset","chat.message"]`)
		// Once the reset is placed, display-only writes go in at once again.
		later := submit(t, root, writeDraft(durable.EntryDraft{Kind: "chat.message", Data: "again"}))
		if got := status(t, later).Status; got != durable.SubmissionDone {
			t.Fatalf("later = %s, want done", got)
		}
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:333 it("leaves the request unchanged by a data-only write that lands while the generation prepares")
	t.Run("leaves the request unchanged by a data-only write that lands while the generation prepares", func(t *testing.T) {
		setup := chatSetup(t)
		rendering, render := deferred(), deferred()
		renders := 0
		addSection(t, setup.Registry, "slow", func(ctx context.Context, _ durable.PromptInput) (*string, error) {
			renders++
			if renders == 2 {
				rendering.resolve()
				if err := render.wait(ctx); err != nil {
					return nil, err
				}
			}
			return new("SLOW"), nil
		})
		var requests [][]string
		var mu sync.Mutex
		setup.Faux.SetResponses([]ai.FauxResponseStep{requestLog(&requests, &mu, fauxAnswer("one")), requestLog(&requests, &mu, fauxAnswer("two"))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(submit(t, root, inputDraft("a")).Wait(testContext))
		second := submit(t, root, inputDraft("b"))
		if err := rendering.wait(testContext); err != nil {
			t.Fatal(err)
		}
		submit(t, root, writeDraft(durable.EntryDraft{Kind: "chat.message", Data: "hi"}))
		render.resolve()
		must(second.Wait(testContext))
		mu.Lock()
		second2 := requests[1]
		mu.Unlock()
		if got := strings.Join(second2, "|"); got != "system:|user:a|assistant:one|user:b" {
			t.Fatalf("request = %s", got)
		}
		kinds := []string{}
		for _, entry := range allEntries(t, root) {
			kinds = append(kinds, entry.Kind)
		}
		// The unchanged prompt adds no second pi.system entry.
		expectEqualJSON(t, kinds, `["pi.user","pi.system","pi.assistant","pi.user","chat.message","pi.assistant"]`)
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:361 it("appends a data-only write while idle after a failed run without placing the queued items")
	t.Run("appends a data-only write while idle after a failed run without placing the queued items", func(t *testing.T) {
		setup := chatSetup(t)
		failing := gatedResponse(ai.FauxResponse{StopReason: "error", ErrorMessage: "invalid request"})
		setup.Faux.SetResponses([]ai.FauxResponseStep{failing.step, fauxAnswer("for f")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		failing.awaitReached(t)
		f := submit(t, root, inputWith("f", mention("f"), ""))
		failing.release()
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionUnanswered, "model_error")
		mustOK(t, harness.WaitForIdle(testContext))
		note := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Data: "n"}))
		if got := status(t, note).Status; got != durable.SubmissionDone {
			t.Fatalf("note = %s, want done", got)
		}
		mustOK(t, harness.WaitForIdle(testContext))
		if got := status(t, f).Status; got != durable.SubmissionQueued {
			t.Fatalf("f = %s, want queued", got)
		}
		if ids := inboxIds(inboxItemsOf(t, harness, root)); len(ids) != 1 || ids[0] != f.Id() {
			t.Fatalf("inbox = %v, want [%d]", ids, f.Id())
		}
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		// The next input places the waiting mention first.
		g := submit(t, root, inputDraft("g"))
		must(g.Wait(testContext))
		lines := []string{}
		for _, line := range dataTranscript(t, allEntries(t, root)) {
			if !strings.HasPrefix(line, "pi.assistant") {
				lines = append(lines, line)
			}
		}
		expectEqualJSON(t, lines, `["pi.user|a","note|\"n\"","chat.mention|{\"author\":\"ann\",\"text\":\"f\"}|f","pi.user|g"]`)
		closeHarness(t, harness)
	})

	// harness-input-entries.test.ts:418 it("appends a data-only write at once behind a non-empty inbox, and still queues writes with model or edits")
	t.Run("appends a data-only write at once behind a non-empty inbox, and still queues writes with model or edits", func(t *testing.T) {
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		followUp := submit(t, root, inputDraft("f"))
		model := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("m")}}}))
		var target durable.EntryRecord
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "pi.user" {
				target = entry
			}
		}
		edit := submit(t, root, writeDraft(durable.EntryDraft{Kind: "redact", Edits: []durable.ContextEdit{{Target: target.Id, Action: "omit"}}}))
		note := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Data: "n"}))
		if got := status(t, note).Status; got != durable.SubmissionDone {
			t.Fatalf("note = %s, want done", got)
		}
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{{followUp.Id(), "followUp"}, {model.Id(), "write"}, {edit.Id(), "write"}}))
		first.release()
		must(followUp.Wait(testContext))
		expectEqualJSON(t, dataTranscript(t, allEntries(t, root)), `["pi.user|a","note|\"n\"","pi.assistant|first","note|m","redact","pi.user|f","pi.assistant|second"]`)
		expectSettled(t, status(t, input), durable.SubmissionDone, "")
		closeHarness(t, harness)
	})
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
