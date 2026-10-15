// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports packages/durable/test/harness-input-entries.test.ts through the real core and the native driver (Pi e6b9a6af):
// inputs placed as application entries, and writes of only kind and data appended at once.

// entriesChat opens a Harness over the core with the faux model and returns its root (chat-support.ts openChat).
func entriesChat(t *testing.T, now func() float64, responses ...ai.FauxResponseStep) (harness.Harness, harness.Conversation) {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses(responses)
	options := harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry()}
	if now != nil {
		options.Now = now
	}
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return h, root
}

func entriesOf(t *testing.T, c harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := c.Entries(t.Context(), durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]durable.EntryRecord, 0, len(page.Items))
	for _, v := range slices.Backward(page.Items) {
		out = append(out, v)
	}
	return out
}

func answerStep(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}

func inboxSnapshot(t *testing.T, h harness.Harness, root harness.Conversation) []any {
	t.Helper()
	value, err := h.SnapshotErased(t.Context(), harness.InboxDoc, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	items, _ := value.Value("items").([]any)
	return items
}

func entriesInput(t *testing.T, c harness.Conversation, content string) durable.Submission {
	t.Helper()
	submission, err := c.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content)})
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

func entriesErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one containing %q", err, want)
	}
}

func inputAsEntry(content string, entry *durable.InputEntry, whenBusy durable.WhenBusy) durable.SubmissionDraft {
	return durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content), InputEntry: entry, WhenBusy: whenBusy}
}

func chatMention(text string) *durable.InputEntry {
	return &durable.InputEntry{Kind: "chat.mention", Data: map[string]any{"author": "ann", "text": text}}
}

// held is a faux response that reports reaching the request and answers once release is closed.
func held(text string) (step ai.FauxResponseStep, reached chan struct{}, release chan struct{}) {
	reached, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	step = ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		once.Do(func() { close(reached) })
		select {
		case <-release:
		case <-options.Signal.Done():
			return ai.AssistantMessage{}, context.Cause(options.Signal)
		}
		return ai.FauxAssistantMessage(ai.FauxText(text), ai.FauxAssistantMessageOptions{}), nil
	})
	return step, reached, release
}

func kindsOf(entries []durable.EntryRecord) string {
	kinds := []string{}
	for _, entry := range entries {
		if entry.Kind != "pi.system" {
			kinds = append(kinds, entry.Kind)
		}
	}
	return strings.Join(kinds, ",")
}

func inboxIdsOf(t *testing.T, h harness.Harness, root harness.Conversation) []durable.SubmissionId {
	t.Helper()
	items := inboxSnapshot(t, h, root)
	ids := []durable.SubmissionId{}
	for _, item := range items {
		id, _ := item.(durable.JsonObject).Value("id").(float64)
		ids = append(ids, durable.SubmissionId(id))
	}
	return ids
}

// "places an idle input as its entry: the given kind and data, with the content as its user message".
func TestCoreAnIdleInputIsPlacedAsItsEntry(t *testing.T) {
	_, root := entriesChat(t, func() float64 { return 42 }, answerStep("hi ann"))
	submission, err := root.Submit(t.Context(), inputAsEntry("ann: hi", chatMention("hi"), ""))
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entries := entriesOf(t, root)
	if got := kindsOf(entries); got != "chat.mention,pi.assistant" {
		t.Fatalf("kinds = %s", got)
	}
	placed := entries[0]
	for _, entry := range entries {
		if entry.Kind == "chat.mention" {
			placed = entry
		}
	}
	model, _ := json.Marshal(placed.Model)
	data, _ := json.Marshal(placed.Data)
	if string(model) != `[{"role":"user","content":"ann: hi","timestamp":42}]` || string(data) != `{"author":"ann","text":"hi"}` {
		t.Fatalf("placed = %s %s", model, data)
	}
	if settled.Status != durable.SubmissionDone || settled.Entry == nil || *settled.Entry != placed.Id {
		t.Fatalf("settled = %+v, want done with entry %d", settled, placed.Id)
	}
}

// "rejects an input entry whose kind ... writing nothing": a TypeError before any commit.
func TestCoreAnInputEntryWithAnEmptyOrPiKindIsRejectedWritingNothing(t *testing.T) {
	_, root := entriesChat(t, nil)
	before := len(entriesOf(t, root))
	for _, kind := range []string{"", "pi.user", "pi.compaction"} {
		_, err := root.Submit(t.Context(), inputAsEntry("x", &durable.InputEntry{Kind: kind}, ""))
		entriesErrContains(t, err, `An input entry's kind must be a non-empty string not starting with "pi.", got `+kind)
	}
	if after := len(entriesOf(t, root)); after != before {
		t.Fatalf("entries = %d, want %d: a rejected input wrote", after, before)
	}
}

// "queues busy inputs with their entries in the inbox and places them as those entries at boundaries", and the request ID
// deduplicates a queued input with an entry.
func TestCoreABusyInputQueuesWithItsEntryAndIsPlacedAsThatEntry(t *testing.T) {
	first, reached, release := held("first")
	h, root := entriesChat(t, nil, first, answerStep("second"))
	input := entriesInput(t, root, "a")
	awaitChan(t, "the request", reached)
	draft := inputAsEntry("f", chatMention("f"), "")
	draft.RequestId = new("r-f")
	followUp, err := root.Submit(t.Context(), draft)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(inboxSnapshot(t, h, root))
	want := `[{"id":` + jsonNumber(followUp.Id()) + `,"mode":"followUp","content":"f","entry":{"kind":"chat.mention","data":{"author":"ann","text":"f"}}}]`
	if string(encoded) != want {
		t.Fatalf("inbox = %s, want %s", encoded, want)
	}
	again := inputAsEntry("other", chatMention("other"), "")
	again.RequestId = new("r-f")
	if dup, err := root.Submit(t.Context(), again); err != nil || dup.Id() != followUp.Id() {
		t.Fatalf("again = %v, %v, want %d", dup, err, followUp.Id())
	}
	_, err = root.Submit(t.Context(), inputAsEntry("x", chatMention("x"), durable.WhenBusyReject))
	entriesErrContains(t, err, "is busy")
	close(release)
	if _, err := followUp.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(entriesOf(t, root)); got != "pi.user,pi.assistant,chat.mention,pi.assistant" {
		t.Fatalf("kinds = %s", got)
	}
	if _, err := input.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func jsonNumber(id durable.SubmissionId) string {
	encoded, _ := json.Marshal(id)
	return string(encoded)
}

// "appends a data-only write mid-run in time order" and "queues a data-only write behind a queued reset".
func TestCoreADataOnlyWriteIsAppendedAtOnceMidRunExceptBehindAQueuedReset(t *testing.T) {
	first, reached, release := held("first")
	h, root := entriesChat(t, nil, first)
	input := entriesInput(t, root, "a")
	awaitChan(t, "the request", reached)
	chat := func(data string) durable.SubmissionDraft {
		return durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "chat.message", Data: data}}
	}
	atOnce, err := root.Submit(t.Context(), chat("lunch?"))
	if err != nil {
		t.Fatal(err)
	}
	if record, err := atOnce.Status(t.Context()); err != nil || record.Status != durable.SubmissionDone {
		t.Fatalf("record = %+v, %v, want done at once", record, err)
	}
	if ids := inboxIdsOf(t, h, root); len(ids) != 0 {
		t.Fatalf("inbox = %v, want empty", ids)
	}
	if got := kindsOf(entriesOf(t, root)); got != "pi.user,chat.message" {
		t.Fatalf("kinds = %s, want the write in time order after the user entry", got)
	}
	// A write with an empty model still affects model context: it queues.
	withModel, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Model: []ai.Message{}}})
	if err != nil {
		t.Fatal(err)
	}
	if record, _ := withModel.Status(t.Context()); record.Status != durable.SubmissionQueued {
		t.Fatalf("a write with an empty model is %s, want queued", record.Status)
	}
	if err := root.Reset(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	held2, err := root.Submit(t.Context(), chat("after"))
	if err != nil {
		t.Fatal(err)
	}
	if record, _ := held2.Status(t.Context()); record.Status != durable.SubmissionQueued {
		t.Fatalf("a data-only write behind a queued reset is %s, want queued", record.Status)
	}
	close(release)
	if _, err := input.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	settled, err := held2.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var reset durable.EntryRecord
	for _, entry := range entriesOf(t, root) {
		if entry.Kind == "pi.reset" {
			reset = entry
		}
	}
	if settled.Entry == nil || *settled.Entry <= reset.Id {
		t.Fatalf("settled = %+v, want an entry after the reset %d", settled, reset.Id)
	}
}
