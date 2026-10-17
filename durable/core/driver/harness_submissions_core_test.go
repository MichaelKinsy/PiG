// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports packages/durable/test/harness-submissions.test.ts through the real core and the native driver (chat-support.ts openChat).
//
// Not natively portable: ":149 rejects a wait whose submission read spans the start of close" holds a Storage.submission read open
// (HeldReads). The native store answers pig.read inside the core step and has no per-read hold, the same gap as harness-tasks :655.
// The in-process port (durable/harness/harness_submissions_test.go) covers it.

// chat is chat-support.ts chatSetup: the faux provider, its models, a registry and a settable clock.
type chat struct {
	faux     interface{ SetResponses([]ai.FauxResponseStep) }
	models   *ai.Models
	registry harness.Registry
	now      func() float64
}

func chatSetup() *chat {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return &chat{faux: faux, models: models, registry: harness.CreateRegistry()}
}

// openChat opens a Harness over the sqlite file at path and returns its root, configured with the faux model on first creation.
func openChat(t *testing.T, path string, store *controlledStore, setup *chat) (harness.Harness, harness.Conversation) {
	t.Helper()
	options := harness.HarnessOptions{Models: setup.models, Registry: setup.registry}
	if setup.now != nil {
		options.Now = setup.now
	}
	h := openConversationsWith(t, path, store, options)
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return h, root
}

func chatOpen(t *testing.T, setup *chat) (harness.Harness, harness.Conversation, *controlledStore) {
	t.Helper()
	store := &controlledStore{}
	h, root := openChat(t, filepath.Join(t.TempDir(), "s.db"), store, setup)
	return h, root, store
}

type unansweredFaux struct {
	step    ai.FauxResponseStep
	reached chan struct{}
}

// unanswered is a faux response that never answers; the run stays busy until its generation is cancelled. reached is closed once the
// request was sent, after the generation's preparation and request commits.
func unanswered() unansweredFaux {
	reached := make(chan struct{})
	var once sync.Once
	return unansweredFaux{reached: reached, step: ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		once.Do(func() { close(reached) })
		<-options.Signal.Done()
		return ai.AssistantMessage{}, context.Cause(options.Signal)
	})}
}

func fauxAnswer(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}

// fauxAfter answers text once release is closed.
func fauxAfter(release <-chan struct{}, text string) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		select {
		case <-release:
		case <-options.Signal.Done():
			return ai.AssistantMessage{}, context.Cause(options.Signal)
		}
		return ai.FauxAssistantMessage(ai.FauxText(text), ai.FauxAssistantMessageOptions{}), nil
	})
}

func submitInput(t *testing.T, c harness.Conversation, content string, requestId ...string) durable.Submission {
	t.Helper()
	draft := durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content)}
	if len(requestId) > 0 {
		draft.RequestId = &requestId[0]
	}
	submission, err := c.Submit(t.Context(), draft)
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

func submitNote(c harness.Conversation, requestId string) (durable.Submission, error) {
	draft := durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}
	if requestId != "" {
		draft.RequestId = &requestId
	}
	return c.Submit(context.Background(), draft)
}

func expectErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one containing %q", err, want)
	}
}

func chatEntries(t *testing.T, c harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := c.Entries(t.Context(), durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]durable.EntryRecord, 0, len(page.Items))
	for i := len(page.Items) - 1; i >= 0; i-- {
		out = append(out, page.Items[i])
	}
	return out
}

// harness-submissions.test.ts:38 "appends an idle write and settles it done without a turn".
func TestCoreSubmissionsAppendAnIdleWriteAndSettleItDoneWithoutATurn(t *testing.T) {
	h, root, _ := chatOpen(t, chatSetup())
	submission, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settled.Entry == nil || settled.Id != submission.Id() || settled.ConversationId != root.Id() || settled.Type != durable.SubmissionTypeWrite || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled = %+v", settled)
	}
	entries := chatEntries(t, root)
	if len(entries) != 1 || entries[0].Id != *settled.Entry || entries[0].ConversationId != root.Id() || entries[0].Kind != "note" || dataJSON(t, entries[0].Data) != `{"text":"x"}` {
		t.Fatalf("entries = %+v", entries)
	}
	if live := conversationSnapshot(t, h, harness.LiveDoc, root.Id()); !sameJson(live, delta.JsonObjectOf()) {
		t.Fatalf("live = %v", live)
	}
	id := root.Id()
	tasks, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{ConversationId: &id}, 10, nil)
	})
	if err != nil || len(tasks.Items) != 0 {
		t.Fatalf("tasks = %v, %v, want none", tasks.Items, err)
	}
}

// harness-submissions.test.ts:57 "places idle input, and rejects busy input with whenBusy reject without writing".
func TestCoreSubmissionsPlaceIdleInputAndRejectBusyInputWithoutWriting(t *testing.T) {
	setup := chatSetup()
	setup.now = func() float64 { return 42 }
	busy := unanswered()
	setup.faux.SetResponses([]ai.FauxResponseStep{busy.step})
	h, root, store := chatOpen(t, setup)
	submission := submitInput(t, root, "hi")
	awaitChan(t, "the request", busy.reached)
	record, err := submission.Status(t.Context())
	if err != nil || record.Status != durable.SubmissionPlaced || record.Entry == nil {
		t.Fatalf("record = %+v, %v", record, err)
	}
	entry, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
		return durable.TxEntry(tx, durable.UserEntry, *record.Entry)
	})
	if err != nil || entry == nil {
		t.Fatalf("entry = %v, %v", entry, err)
	}
	if got := entry.Model; len(got) != 1 || !reflect.DeepEqual(got[0], ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 42}) {
		t.Fatalf("model = %#v", got)
	}
	live := conversationSnapshot(t, h, harness.LiveDoc, root.Id())
	run, _ := live.Value("run").(durable.JsonObject)
	if inputs, _ := run.Value("inputs").([]any); !reflect.DeepEqual(inputs, []any{float64(submission.Id())}) {
		t.Fatalf("run inputs = %#v, want [%d]", run.Value("inputs"), submission.Id())
	}
	taskId, _ := run.Value("taskId").(float64)
	task, err := h.GetTask(t.Context(), durable.TaskId(taskId))
	if err != nil || task == nil || task.Kind != "pi.generation" {
		t.Fatalf("run task = %v, %v, want pi.generation", task, err)
	}

	commits := store.count()
	_, err = root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("again"), WhenBusy: durable.WhenBusyReject})
	var busyErr *durable.ConversationBusy
	if !errors.As(err, &busyErr) || busyErr.ConversationId != root.Id() {
		t.Fatalf("error = %v, want ConversationBusy for conversation %d", err, root.Id())
	}
	if store.count() != commits {
		t.Fatalf("commits = %d, want %d: a rejected submission wrote", store.count(), commits)
	}
}

// harness-submissions.test.ts:82 "deduplicates request IDs per conversation before any write".
func TestCoreSubmissionsDeduplicateRequestIDsPerConversationBeforeAnyWrite(t *testing.T) {
	setup := chatSetup()
	busy := unanswered()
	setup.faux.SetResponses([]ai.FauxResponseStep{busy.step})
	h, root, store := chatOpen(t, setup)
	first := submitInput(t, root, "hi", "r1")
	awaitChan(t, "the request", busy.reached)
	commits := store.count()
	// Deduplication runs before the busy check.
	again := submitInput(t, root, "different", "r1")
	if again.Id() != first.Id() {
		t.Fatalf("again = %d, want %d", again.Id(), first.Id())
	}
	if store.count() != commits {
		t.Fatalf("commits = %d, want %d: a deduplicated submission wrote", store.count(), commits)
	}
	_, err := submitNote(root, "r1")
	expectErrorContains(t, err, "Request r1 already identifies a submission of type input")
	status, err := first.Status(t.Context())
	if err != nil || status.RequestId == nil || *status.RequestId != "r1" || status.Status != durable.SubmissionPlaced {
		t.Fatalf("status = %+v, %v, want requestId r1 placed", status, err)
	}

	other, err := h.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	if err != nil {
		t.Fatal(err)
	}
	write, err := submitNote(other, "r1")
	if err != nil || write.Id() == first.Id() {
		t.Fatalf("write = %v, %v: a request ID of another conversation deduplicated", write, err)
	}
	repeated, err := submitNote(other, "r1")
	if err != nil || repeated.Id() != write.Id() {
		t.Fatalf("repeated = %v, %v, want %d", repeated, err, write.Id())
	}
}

// harness-submissions.test.ts:109 "reports abort results and looks submissions up by conversation".
func TestCoreSubmissionsReportAbortResultsAndLookUpSubmissionsByConversation(t *testing.T) {
	setup := chatSetup()
	release := make(chan struct{})
	setup.faux.SetResponses([]ai.FauxResponseStep{fauxAfter(release, "answer")})
	h, root, _ := chatOpen(t, setup)
	submission := submitInput(t, root, "hi")
	expect := func(got durable.SubmissionAbortResult, err error, want durable.SubmissionAbortResult) {
		t.Helper()
		if err != nil || got != want {
			t.Fatalf("abort = %q, %v; want %q", got, err, want)
		}
	}
	result, err := submission.Abort(t.Context())
	expect(result, err, durable.SubmissionAlreadyPlaced)
	rootId := root.Id()
	result, err = h.AbortSubmission(t.Context(), submission.Id(), &rootId)
	expect(result, err, durable.SubmissionAlreadyPlaced)
	other, err := h.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	if err != nil {
		t.Fatal(err)
	}
	otherId := other.Id()
	result, err = h.AbortSubmission(t.Context(), submission.Id(), &otherId)
	expect(result, err, durable.SubmissionNotFound)
	result, err = h.AbortSubmission(t.Context(), 999_999, nil)
	expect(result, err, durable.SubmissionNotFound)
	if missing, err := h.Submission(t.Context(), 999_999); err != nil || missing != nil {
		t.Fatalf("submission 999999 = %v, %v, want undefined", missing, err)
	}

	close(release)
	if _, err := submission.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err = submission.Abort(t.Context())
	expect(result, err, durable.SubmissionSettled)
	result, err = h.AbortSubmission(t.Context(), submission.Id(), nil)
	expect(result, err, durable.SubmissionSettled)
}

// harness-submissions.test.ts:134 "cancels only a wait and rejects pending waits on close".
func TestCoreSubmissionsCancelOnlyAWaitAndRejectPendingWaitsOnClose(t *testing.T) {
	setup := chatSetup()
	setup.faux.SetResponses([]ai.FauxResponseStep{unanswered().step})
	h, root, _ := chatOpen(t, setup)
	submission := submitInput(t, root, "hi")
	waitCtx, abort := context.WithCancelCause(t.Context())
	cancelled := make(chan error, 1)
	go func() { _, err := submission.Wait(waitCtx); cancelled <- err }()
	pending := make(chan error, 1)
	go func() { _, err := submission.Wait(t.Context()); pending <- err }()
	abort(errors.New("stop waiting"))
	expectErrorContains(t, awaitChan(t, "the cancelled wait", cancelled), "stop waiting")
	if status, err := submission.Status(t.Context()); err != nil || status.Status != durable.SubmissionPlaced {
		t.Fatalf("status = %+v, %v, want placed", status, err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	expectErrorContains(t, awaitChan(t, "the pending wait", pending), "Harness is closed")
}

// harness-submissions.test.ts:178 "reacquires a submission after reopen and settles it durably".
func TestCoreSubmissionsReacquireASubmissionAfterReopenAndSettleItDurably(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup()
	// The first process never answers; the reopened one does.
	busy := unanswered()
	setup.faux.SetResponses([]ai.FauxResponseStep{busy.step, fauxAnswer("after reopen")})
	h, root := openChat(t, path, &controlledStore{}, setup)
	id := submitInput(t, root, "hi", "print").Id()
	awaitChan(t, "the request", busy.reached)
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	h, _ = openChat(t, path, &controlledStore{}, setup)
	submission, err := h.Submission(t.Context(), id)
	if err != nil || submission == nil {
		t.Fatalf("submission = %v, %v", submission, err)
	}
	if status, err := submission.Status(t.Context()); err != nil || status.Status != durable.SubmissionPlaced {
		t.Fatalf("status = %+v, %v, want placed", status, err)
	}
	h.Resume()
	settled, err := submission.Wait(t.Context())
	if err != nil || settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
		t.Fatalf("settled = %+v, %v", settled, err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	h, root = openChat(t, path, &controlledStore{}, setup)
	reopened, err := h.Submission(t.Context(), id)
	if err != nil || reopened == nil {
		t.Fatalf("reopened = %v, %v", reopened, err)
	}
	again, err := reopened.Wait(t.Context())
	if err != nil || !reflect.DeepEqual(again, settled) {
		t.Fatalf("settled again = %+v, %v, want %+v", again, err, settled)
	}
	if resubmitted := submitInput(t, root, "hi", "print"); resubmitted.Id() != id {
		t.Fatalf("resubmitted = %d, want %d", resubmitted.Id(), id)
	}
}

// harness-submissions.test.ts:204 "enables scheduling when a caller submits or waits".
func TestCoreSubmissionsEnableSchedulingWhenACallerSubmitsOrWaits(t *testing.T) {
	setup := chatSetup()
	setup.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
	_, root, _ := chatOpen(t, setup)
	// No Resume: submitting asks for progress.
	settled, err := submitInput(t, root, "hi").Wait(t.Context())
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled = %+v, %v, want done", settled, err)
	}

	passive, passiveRoot, _ := chatOpen(t, chatSetup())
	taskId, err := durable.Commit(t.Context(), passiveRoot, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, harness.GenerationTask, harness.GenerationInput{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
	})
	if err != nil {
		t.Fatal(err)
	}
	// A committed task alone does not start scheduling; waiting for it does.
	if task, err := passive.GetTask(t.Context(), taskId); err != nil || task == nil || task.State.Status != durable.TaskPending {
		t.Fatalf("task = %+v, %v, want pending", task, err)
	}
	if task, err := passive.WaitForTask(t.Context(), taskId); err != nil || task.State.Status != durable.TaskTerminal {
		t.Fatalf("waited = %+v, %v, want terminal", task, err)
	}
}

// harness-submissions.test.ts:223 "settles submissions by their current record in the transaction".
func TestCoreSubmissionsSettleByTheirCurrentRecordInTheTransaction(t *testing.T) {
	h, root, _ := chatOpen(t, chatSetup())
	rootId := root.Id()
	entry, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.EntryId, error) {
		appended, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "note"})
		return appended.Id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	create := func(record durable.SubmissionCreate) durable.SubmissionId {
		id, err := durable.Commit(t.Context(), h, func(tx durable.Tx) (durable.SubmissionId, error) {
			created, err := tx.CreateSubmission(record)
			return created.Id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	queued := create(durable.SubmissionCreate{ConversationId: rootId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued})
	write := create(durable.SubmissionCreate{ConversationId: rootId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionQueued})
	answer := durable.SubmissionSettlement{Status: durable.SubmissionDone, Answer: entry}
	settle := func(id durable.SubmissionId, settlement durable.SubmissionSettlement) error {
		_, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (struct{}, error) { return struct{}{}, tx.SettleSubmission(id, settlement) })
		return err
	}
	expectErrorContains(t, settle(queued, answer), "is not a placed input")
	expectErrorContains(t, settle(write, answer), "is not a placed input")
	expectErrorContains(t, settle(999_999, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "x"}), "does not exist")

	// A submission created earlier in the same commit settles; a second settlement leaves the first.
	placed, err := durable.Commit(t.Context(), h, func(tx durable.Tx) (durable.SubmissionId, error) {
		created, err := tx.CreateSubmission(durable.SubmissionCreate{ConversationId: rootId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionPlaced, Entry: &entry})
		if err != nil {
			return 0, err
		}
		if err := tx.SettleSubmission(created.Id, answer); err != nil {
			return 0, err
		}
		if err := tx.SettleSubmission(created.Id, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "late"}); err != nil {
			return 0, err
		}
		return created.Id, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := h.Submission(t.Context(), placed)
	if err != nil || submission == nil {
		t.Fatalf("submission = %v, %v", submission, err)
	}
	status, err := submission.Status(t.Context())
	if err != nil || status.Status != durable.SubmissionDone || status.Entry == nil || *status.Entry != entry || status.Answer == nil || *status.Answer != entry {
		t.Fatalf("status = %+v, %v, want done with entry and answer %d", status, err, entry)
	}
}

// harness-submissions.test.ts:260 "appends and reads typed entries through tokens".
func TestCoreSubmissionsAppendAndReadTypedEntriesThroughTokens(t *testing.T) {
	type counterData struct {
		N float64 `json:"n"`
	}
	counterEntry := durable.DefineEntry[counterData]("app.counter")
	markerEntry := durable.DefineEntry[durable.Never]("app.marker")
	_, root, _ := chatOpen(t, chatSetup())
	rootId := root.Id()
	counter, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (*durable.TypedEntry[counterData], error) {
		return durable.TxAppendEntry(tx, counterEntry, rootId, durable.TypedEntryDraft[counterData]{Data: counterData{N: 1}})
	})
	if err != nil || counter.TypedData.N != 1 || counter.ConversationId != rootId || counter.Kind != "app.counter" || dataJSON(t, counter.Data) != `{"n":1}` {
		t.Fatalf("counter = %+v, %v", counter, err)
	}
	marker, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
		return durable.TxAppendEntry(tx, markerEntry, rootId, durable.TypedEntryDraft[durable.Never]{})
	})
	if err != nil || marker.Kind != "app.marker" {
		t.Fatalf("marker = %+v, %v", marker, err)
	}
	read, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (*durable.TypedEntry[counterData], error) {
		return durable.TxEntry(tx, counterEntry, counter.Id)
	})
	if err != nil || read == nil || !reflect.DeepEqual(read.EntryRecord, counter.EntryRecord) {
		t.Fatalf("read = %+v, %v, want %+v", read, err, counter.EntryRecord)
	}
	if other, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
		return durable.TxEntry(tx, markerEntry, counter.Id)
	}); err != nil || other != nil {
		t.Fatalf("marker read of a counter = %v, %v, want undefined", other, err)
	}
	if missing, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (*durable.TypedEntry[counterData], error) {
		return durable.TxEntry(tx, counterEntry, 999_999)
	}); err != nil || missing != nil {
		t.Fatalf("missing entry = %v, %v, want undefined", missing, err)
	}
	if !counterEntry.Is(&counter.EntryRecord) || durable.AssistantEntry.Is(&counter.EntryRecord) {
		t.Fatal("entry guards disagree with kinds")
	}
	if durable.UserEntry.Kind != "pi.user" || durable.AssistantEntry.Kind != "pi.assistant" {
		t.Fatalf("kinds = %s, %s", durable.UserEntry.Kind, durable.AssistantEntry.Kind)
	}
}

// scheduler.ts waitForTask and waitForIdle: a wait pending at close() rejects with closedError(), and a wait begun after close rejects
// at the call (the same contract as submission.wait, harness-submissions.test.ts:134).
func TestCoreWaitsRejectWithHarnessIsClosedWhenCloseEndsThemOrPrecedesThem(t *testing.T) {
	setup := chatSetup()
	setup.faux.SetResponses([]ai.FauxResponseStep{unanswered().step})
	h, root, _ := chatOpen(t, setup)
	submission := submitInput(t, root, "hi")
	status, err := submission.Status(t.Context())
	if err != nil || status.Entry == nil {
		t.Fatalf("status = %+v, %v", status, err)
	}
	live := conversationSnapshot(t, h, harness.LiveDoc, root.Id())
	run, _ := live.Value("run").(durable.JsonObject)
	taskId, _ := run.Value("taskId").(float64)
	pendingTask := make(chan error, 1)
	go func() { _, err := h.WaitForTask(t.Context(), durable.TaskId(taskId)); pendingTask <- err }()
	pendingIdle := make(chan error, 1)
	go func() { pendingIdle <- h.WaitForIdle(t.Context()) }()
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	expectErrorContains(t, awaitChan(t, "the pending waitForTask", pendingTask), "Harness is closed")
	expectErrorContains(t, awaitChan(t, "the pending waitForIdle", pendingIdle), "Harness is closed")
	_, err = h.WaitForTask(t.Context(), durable.TaskId(taskId))
	expectErrorContains(t, err, "Harness is closed")
	expectErrorContains(t, h.WaitForIdle(t.Context()), "Harness is closed")
	_, err = submission.Wait(t.Context())
	expectErrorContains(t, err, "Harness is closed")
}

// A wait that Close ends sees two events: its context ending (cause harness.ErrClosed) and the host closing (sqlhost.ErrClosed). Which
// one the wait observes first is a race, so the mapping must not depend on it: every outcome Close can produce reports Harness is closed
// (scheduler.ts :323 :725 reject with closedError()), and nothing else is rewritten. The end-to-end tests above cannot pin the
// host-closing-first outcome on every run, so this one drives each input of the mapping directly.
func TestCoreClosedOrReportsEveryOutcomeOfCloseAsHarnessIsClosed(t *testing.T) {
	h, _, _ := chatOpen(t, chatSetup())
	s := h.(*driver.Session)
	userCause := errors.New("stop waiting")
	cancelled := func(cause error) context.Context {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(cause)
		return ctx
	}
	check := func(name string, ctx context.Context, in, want error) {
		t.Helper()
		if got := s.ClosedOr(ctx, in); !errors.Is(got, want) || (want == nil) != (got == nil) {
			t.Errorf("%s: closedOr(%v) = %v, want %v", name, in, got, want)
		}
	}
	// Open: only the wait's own context ending by Close is rewritten.
	check("nil stays nil", t.Context(), nil, nil)
	check("a host failure of an open Harness stays", t.Context(), sqlhost.ErrClosed, sqlhost.ErrClosed)
	check("the caller's own cancellation stays", cancelled(userCause), context.Canceled, context.Canceled)
	check("context ended by Close, context error first", cancelled(harness.ErrClosed), context.Canceled, harness.ErrClosed)
	check("context ended by Close, host error first", cancelled(harness.ErrClosed), sqlhost.ErrClosed, harness.ErrClosed)

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Closed: the host closing first is Close's rejection; an unrelated failure is not.
	check("host closed first after Close", t.Context(), sqlhost.ErrClosed, harness.ErrClosed)
	check("an unrelated failure stays after Close", t.Context(), userCause, userCause)
}

// dataJSON is the JSON text of an entry's data. Stored objects decode to the insertion-ordered chordjson.Object, as JSON.parse
// keeps key order, so the data is compared as JSON text rather than as a Go map.
func dataJSON(t *testing.T, data any) string {
	t.Helper()
	text, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}
