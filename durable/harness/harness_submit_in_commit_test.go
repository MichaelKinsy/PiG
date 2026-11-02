// Ports packages/durable/test/harness-submit-in-commit.test.ts: admission for hosts that must not start runs
// (`whenIdle: "reject"`), and `submit(tx, draft)` inside the caller's commit (#11149). See harness_tasks_test.go for the
// Go mappings that apply to every case; the Go form of `submit(tx, draft)` is Conversation.SubmitIn.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

type sicSent struct {
	Ids []durable.SubmissionId `json:"ids"`
}

// sicSentDoc is a host's own conversation document.
var sicSentDoc = durable.DefineDoc(durable.DocDefinition[sicSent]{
	CommonDocDefinition: durable.CommonDocDefinition[sicSent]{
		Kind: "test.sent", Version: 1,
		Initial:        func() sicSent { return sicSent{Ids: []durable.SubmissionId{}} },
		CheckpointWhen: func(sicSent, []durable.Op, durable.CheckpointInfo) bool { return false },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
})

func sicFailure() ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"})
}

type sicBusy struct {
	harness Harness
	root    Conversation
	storage *controlledStorage
	first   durable.Submission
}

// sicOpenBusy opens a chat whose run hangs until aborted, and waits until it works on its first input.
func sicOpenBusy(t *testing.T) sicBusy {
	t.Helper()
	store := newControlledStorage()
	setup := chatSetup(t)
	busy := unanswered()
	setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
	harness, root := openChat(t, store, setup)
	first := submitInput(t, root, "work")
	<-busy.reached
	return sicBusy{harness: harness, root: root, storage: store, first: first}
}

// sicHold installs tool hold, which reports its call and waits for release; its first call answers `call`.
func sicHold(t *testing.T, setup *chatState) (reached, release *deferredGate, call ai.FauxResponseStep) {
	t.Helper()
	reached, release = deferred(), deferred()
	addTool(t, setup.Registry, ownTool("hold", "Holds until released", emptyObjectSchema(), func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		reached.resolve()
		if err := release.wait(ctx); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
	}))
	return reached, release, ownCall("hold", map[string]any{}, "c1")
}

// sicSettledStatus polls the settled status of submission id without asking for progress (Submission.Wait would).
func sicSettledStatus(t *testing.T, harness Harness, id durable.SubmissionId) durable.SubmissionRecord {
	t.Helper()
	submission := submissionOfHarness(t, harness, id)
	waitFor(t, func() bool {
		status := must(submission.Status(testContext)).Status
		return status == durable.SubmissionDone || status == durable.SubmissionUnanswered
	})
	return must(submission.Status(testContext))
}

// sicSubmitIn admits draft through Conversation.SubmitIn in a harness commit.
func sicSubmitIn(harness Harness, root Conversation, draft durable.SubmissionDraft) (durable.SubmissionId, error) {
	return durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SubmissionId, error) {
		return root.SubmitIn(tx, draft)
	})
}

func sicInboxIds(t *testing.T, harness Harness, root Conversation) []durable.SubmissionId {
	t.Helper()
	ids := []durable.SubmissionId{}
	for _, item := range must(durable.Snapshot(testContext, harness, InboxDoc, root.Id())).Items {
		ids = append(ids, item.Id)
	}
	return ids
}

func sicSteerDraft(content string, extra ...func(*durable.SubmissionDraft)) durable.SubmissionDraft {
	draft := inputDraft(content, durable.WhenBusySteer)
	for _, edit := range extra {
		edit(&draft)
	}
	return draft
}

func sicReject(draft *durable.SubmissionDraft) { draft.WhenIdle = durable.WhenIdleReject }

func sicExpectIdle(t *testing.T, err error, id durable.ConversationId) {
	t.Helper()
	var idle *durable.ConversationIdle
	if !errors.As(err, &idle) || idle.ConversationId != id {
		t.Fatalf("error = %v, want ConversationIdle for %d", err, id)
	}
}

func sicExpectNoRun(t *testing.T, harness Harness, id durable.ConversationId) {
	t.Helper()
	if live := liveOf(t, harness, id); live.Run != nil {
		t.Fatalf("run = %+v, want none", live.Run)
	}
}

func TestWhenIdleReject(t *testing.T) {
	// #11149
	t.Run("rejects input to an idle conversation without writing", func(t *testing.T) {
		store := newControlledStorage()
		harness, root := openChat(t, store, chatSetup(t))
		commits := store.commitCount()
		_, err := root.Submit(testContext, sicSteerDraft("hi", sicReject))
		sicExpectIdle(t, err, root.Id())
		if store.commitCount() != commits {
			t.Fatalf("commits %d, want %d", store.commitCount(), commits)
		}
		sicExpectNoRun(t, harness, root.Id())
		closeHarness(t, harness)
	})

	// #11149
	t.Run("rejects input to an idle conversation that keeps queued items after a failed run", func(t *testing.T) {
		setup := chatSetup(t)
		failing, requested := deferred(), deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				requested.resolve()
				if err := failing.wait(options.Signal); err != nil {
					return ai.AssistantMessage{}, err
				}
				return ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"}.AssistantMessage(), nil
			}),
		})
		harness, root := openChat(t, newControlledStorage(), setup)
		first := submitInput(t, root, "first")
		if err := requested.wait(testContext); err != nil {
			t.Fatal(err)
		}
		queued := submitInput(t, root, "queued")
		failing.resolve()
		if settled := must(first.Wait(testContext)); settled.Status != durable.SubmissionUnanswered || *settled.Reason != "model_error" {
			t.Fatalf("first = %s", jsonText(t, settled))
		}
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		_, err := root.Submit(testContext, sicSteerDraft("steer", sicReject))
		sicExpectIdle(t, err, root.Id())
		if status := must(queued.Status(testContext)).Status; status != durable.SubmissionQueued {
			t.Fatalf("queued = %s", status)
		}
		sicExpectNoRun(t, harness, root.Id())
		closeHarness(t, harness)
	})

	// #11149
	t.Run("steers a working run", func(t *testing.T) {
		busy := sicOpenBusy(t)
		steer := must(busy.root.Submit(testContext, sicSteerDraft("also this", sicReject)))
		record := must(steer.Status(testContext))
		if record.Type != durable.SubmissionTypeInput || record.Status != durable.SubmissionQueued {
			t.Fatalf("steer = %s", jsonText(t, record))
		}
		inbox := must(durable.Snapshot(testContext, busy.harness, InboxDoc, busy.root.Id()))
		expectEqualJSON(t, inbox.Items, `[{"id":`+jsonText(t, steer.Id())+`,"mode":"steer","content":"also this"}]`)
		if live := liveOf(t, busy.harness, busy.root.Id()); !reflect.DeepEqual(live.Run.Inputs, []durable.SubmissionId{busy.first.Id()}) {
			t.Fatalf("run inputs %v", live.Run.Inputs)
		}
		closeHarness(t, busy.harness)
	})

	// #11149
	t.Run("returns the submission of a known request ID, also once idle", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, newControlledStorage(), setup)
		first := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("work"), RequestId: new("r1")}))
		must(first.Wait(testContext))
		draft := sicSteerDraft("work", sicReject, func(draft *durable.SubmissionDraft) { draft.RequestId = new("r1") })
		if again := must(root.Submit(testContext, draft)); again.Id() != first.Id() {
			t.Fatalf("id = %d, want %d", again.Id(), first.Id())
		}
		// Deduplication runs before the idle rejection in the caller's commit too.
		if id := must(sicSubmitIn(harness, root, draft)); id != first.Id() {
			t.Fatalf("id in commit = %d, want %d", id, first.Id())
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("rejects through a tool's conversation handle", func(t *testing.T) {
		setup := chatSetup(t)
		var mu sync.Mutex
		var other Conversation
		var rejection error
		addTool(t, setup.Registry, ownTool("nudge", "Steers another conversation", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			mu.Lock()
			target := other
			mu.Unlock()
			handle, err := api.Conversation(ctx, target.Id())
			if err != nil || handle == nil {
				return durable.ToolExecutionResult{}, errors.New("no handle")
			}
			_, err = handle.Submit(ctx, sicSteerDraft("nudge", sicReject))
			mu.Lock()
			rejection = err
			mu.Unlock()
			return ownText("nudged"), nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("nudge", map[string]any{}, "c1"), fauxAnswer("ok")})
		harness, root := openChat(t, newControlledStorage(), setup)
		created := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}, Agent: &fauxModel}))
		mu.Lock()
		other = created
		mu.Unlock()
		must(submitInput(t, root, "go").Wait(testContext))
		mu.Lock()
		sicExpectIdle(t, rejection, created.Id())
		mu.Unlock()
		sicExpectNoRun(t, harness, created.Id())
		closeHarness(t, harness)
	})
}

func TestSubmitInsideTheCallersCommit(t *testing.T) {
	// #11149
	t.Run("admits with the host's own check and writes in one commit", func(t *testing.T) {
		busy := sicOpenBusy(t)
		commits := busy.storage.commitCount()
		id := must(durable.Commit(testContext, busy.harness, func(tx durable.Tx) (durable.SubmissionId, error) {
			live, err := durable.TxDoc(tx, LiveDoc, busy.root.Id())
			if err != nil {
				return 0, err
			}
			if !live.Has("run") {
				return 0, errors.New("not working")
			}
			// The host's own document write may come before the admission.
			sent, err := durable.TxDoc(tx, sicSentDoc, busy.root.Id())
			if err != nil {
				return 0, err
			}
			admitted, err := busy.root.SubmitIn(tx, sicSteerDraft("steer"))
			if err != nil {
				return 0, err
			}
			if err := pushJSON(sent.Array("ids"), admitted); err != nil {
				return 0, err
			}
			// The host's own table write comes after it.
			if _, err := tx.AppendEntry(busy.root.Id(), durable.EntryDraft{Kind: "app.sent", Data: map[string]any{"submission": float64(admitted)}}); err != nil {
				return 0, err
			}
			return admitted, nil
		}))
		if busy.storage.commitCount() != commits+1 {
			t.Fatalf("commits %d, want %d", busy.storage.commitCount(), commits+1)
		}
		submissionWritten, entryWritten := false, false
		for _, write := range busy.storage.commitAt(commits) {
			switch typed := write.(type) {
			case durable.SubmissionWrite:
				submissionWritten = submissionWritten || typed.Value.Id == id
			case durable.EntryWrite:
				entryWritten = entryWritten || typed.Value.Kind == "app.sent"
			}
		}
		if !submissionWritten || !entryWritten {
			t.Fatalf("commit wrote submission %v, entry %v", submissionWritten, entryWritten)
		}
		if got := must(durable.Snapshot(testContext, busy.harness, sicSentDoc, busy.root.Id())).Ids; !reflect.DeepEqual(got, []durable.SubmissionId{id}) {
			t.Fatalf("sent %v", got)
		}
		inbox := must(durable.Snapshot(testContext, busy.harness, InboxDoc, busy.root.Id()))
		expectEqualJSON(t, inbox.Items, `[{"id":`+jsonText(t, id)+`,"mode":"steer","content":"steer"}]`)
		if live := liveOf(t, busy.harness, busy.root.Id()); !reflect.DeepEqual(live.Run.Inputs, []durable.SubmissionId{busy.first.Id()}) {
			t.Fatalf("run inputs %v", live.Run.Inputs)
		}
		closeHarness(t, busy.harness)
	})

	// #11149
	t.Run("is rolled back with the commit when the callback throws later", func(t *testing.T) {
		store := newControlledStorage()
		harness, root := openChat(t, store, chatSetup(t))
		commits := store.commitCount()
		_, err := durable.Commit(testContext, harness, func(tx durable.Tx) (struct{}, error) {
			if _, err := root.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), RequestId: new("r1")}); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, errors.New("host check failed")
		})
		expectErrorContains(t, err, "host check failed")
		if store.commitCount() != commits {
			t.Fatalf("commits %d, want %d", store.commitCount(), commits)
		}
		sicExpectNoRun(t, harness, root.Id())
		found := must(durable.Commit(testContext, harness, func(tx durable.Tx) (*durable.SubmissionRecord, error) {
			return tx.SubmissionByRequest(root.Id(), "r1")
		}))
		if found != nil {
			t.Fatalf("submission = %+v, want none", found)
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("leaves no submission and no run when Storage fails the commit, also after reopening", func(t *testing.T) {
		store := newControlledStorage()
		setup := chatSetup(t)
		harness, root := openChat(t, store, setup)
		failure := errors.New("disk full")
		store.failNextCommit(failure)
		_, err := durable.Commit(testContext, harness, func(tx durable.Tx) (struct{}, error) {
			if _, err := root.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), RequestId: new("r1")}); err != nil {
				return struct{}{}, err
			}
			_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.sent"})
			return struct{}{}, err
		})
		if !errors.Is(err, failure) {
			t.Fatalf("error = %v, want %v", err, failure)
		}
		<-harness.Closed()
		store.Reopen()
		reopened, reopenedRoot := openChat(t, store, setup)
		if reopenedRoot.Id() != root.Id() {
			t.Fatalf("root %d, want %d", reopenedRoot.Id(), root.Id())
		}
		type found struct {
			submission *durable.SubmissionRecord
			tasks      int
			entries    int
		}
		got := must(durable.Commit(testContext, reopened, func(tx durable.Tx) (found, error) {
			submission, err := tx.SubmissionByRequest(root.Id(), "r1")
			if err != nil {
				return found{}, err
			}
			tasks, err := tx.ScanTasks(durable.TaskQuery{ConversationId: new(root.Id())}, 10, nil)
			if err != nil {
				return found{}, err
			}
			entries, err := tx.ScanEntries(durable.EntryQuery{ConversationId: root.Id()}, 10, nil)
			if err != nil {
				return found{}, err
			}
			return found{submission, len(tasks.Items), len(entries.Items)}, nil
		}))
		if got != (found{}) {
			t.Fatalf("found %+v, want nothing", got)
		}
		sicExpectNoRun(t, reopened, root.Id())
		closeHarness(t, reopened)
	})

	// #11149
	t.Run("starts the run of an idle input once the commit is stored", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		// No Resume: the admission asks for progress itself.
		harness, root := openChat(t, newControlledStorage(), setup)
		id := must(sicSubmitIn(harness, root, inputDraft("hi")))
		if record := sicSettledStatus(t, harness, id); record.Type != durable.SubmissionTypeInput || record.Status != durable.SubmissionDone {
			t.Fatalf("record = %s", jsonText(t, record))
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("queues a model-visible write while busy and places it at the next boundary", func(t *testing.T) {
		setup := chatSetup(t)
		reached, release, call := sicHold(t, setup)
		setup.Faux.SetResponses([]ai.FauxResponseStep{call, fauxAnswer("answer")})
		harness, root := openChat(t, newControlledStorage(), setup)
		first := submitInput(t, root, "work")
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		note := ai.UserMessage{Content: ai.UserText("a note the model sees"), Timestamp: 1}
		id := must(sicSubmitIn(harness, root, writeDraft(durable.EntryDraft{Kind: "test.note", Model: []ai.Message{note}})))
		if got := sicInboxIds(t, harness, root); !reflect.DeepEqual(got, []durable.SubmissionId{id}) {
			t.Fatalf("inbox %v, want [%d]", got, id)
		}
		release.resolve()
		if settled := must(first.Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("first = %s", jsonText(t, settled))
		}
		record := sicSettledStatus(t, harness, id)
		if record.Type != durable.SubmissionTypeWrite || record.Status != durable.SubmissionDone || record.Entry == nil {
			t.Fatalf("record = %s", jsonText(t, record))
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("deduplicates request IDs", func(t *testing.T) {
		busy := sicOpenBusy(t)
		draft := sicSteerDraft("steer", func(draft *durable.SubmissionDraft) { draft.RequestId = new("r1") })
		first := must(sicSubmitIn(busy.harness, busy.root, draft))
		commits := busy.storage.commitCount()
		again := must(sicSubmitIn(busy.harness, busy.root, draft))
		if again != first {
			t.Fatalf("again = %d, want %d", again, first)
		}
		if busy.storage.commitCount() != commits {
			t.Fatalf("commits %d, want %d", busy.storage.commitCount(), commits)
		}
		closeHarness(t, busy.harness)
	})

	// #11149
	t.Run("rejects before any write, so the callback may catch the rejection", func(t *testing.T) {
		store := newControlledStorage()
		harness, root := openChat(t, store, chatSetup(t))
		commits := store.commitCount()
		outcome := must(durable.Commit(testContext, harness, func(tx durable.Tx) (string, error) {
			if _, err := root.SubmitIn(tx, sicSteerDraft("hi", sicReject)); err != nil {
				if _, ok := errors.AsType[*durable.ConversationIdle](err); ok {
					return "idle", nil
				}
				return "other", nil
			}
			return "admitted", nil
		}))
		if outcome != "idle" {
			t.Fatalf("outcome = %s, want idle", outcome)
		}
		if store.commitCount() != commits {
			t.Fatalf("commits %d, want %d", store.commitCount(), commits)
		}
		sicExpectNoRun(t, harness, root.Id())
		closeHarness(t, harness)
	})

	// #11149
	t.Run("throws ReadAfterWrite after a table write of the commit, on every path", func(t *testing.T) {
		busy := sicOpenBusy(t)
		expectReadAfterWrite := func(err error) {
			t.Helper()
			if _, ok := errors.AsType[*durable.ReadAfterWrite](err); !ok {
				t.Fatalf("error = %v, want ReadAfterWrite", err)
			}
		}
		// A busy input without request ID or trace reads no table, and is still refused.
		_, err := durable.Commit(testContext, busy.harness, func(tx durable.Tx) (struct{}, error) {
			if _, err := tx.AppendEntry(busy.root.Id(), durable.EntryDraft{Kind: "app.note"}); err != nil {
				return struct{}{}, err
			}
			_, err := busy.root.SubmitIn(tx, sicSteerDraft("steer"))
			return struct{}{}, err
		})
		expectReadAfterWrite(err)
		expectErrorContains(t, err, "submit(tx, draft) cannot read tables after the first table write")
		_, err = durable.Commit(testContext, busy.harness, func(tx durable.Tx) (struct{}, error) {
			if _, err := busy.root.SubmitIn(tx, sicSteerDraft("one")); err != nil {
				return struct{}{}, err
			}
			_, err := busy.root.SubmitIn(tx, sicSteerDraft("two"))
			return struct{}{}, err
		})
		expectReadAfterWrite(err)
		if got := sicInboxIds(t, busy.harness, busy.root); len(got) != 0 {
			t.Fatalf("inbox %v, want none", got)
		}

		idleHarness, idleRoot := openChat(t, newControlledStorage(), chatSetup(t))
		_, err = durable.Commit(testContext, idleHarness, func(tx durable.Tx) (struct{}, error) {
			if _, err := tx.AppendEntry(idleRoot.Id(), durable.EntryDraft{Kind: "app.note"}); err != nil {
				return struct{}{}, err
			}
			_, err := idleRoot.SubmitIn(tx, inputDraft("hi"))
			return struct{}{}, err
		})
		expectReadAfterWrite(err)
		closeHarness(t, idleHarness)
		closeHarness(t, busy.harness)
	})

	// #11149
	t.Run("continues a failed run, and must precede the commit's table writes", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{sicFailure(), fauxAnswer("recovered")})
		harness, root := openChat(t, newControlledStorage(), setup)
		first := submitInput(t, root, "Analyze this")
		if settled := must(first.Wait(testContext)); settled.Status != durable.SubmissionUnanswered || *settled.Reason != "model_error" {
			t.Fatalf("first = %s", jsonText(t, settled))
		}

		_, err := durable.Commit(testContext, harness, func(tx durable.Tx) (struct{}, error) {
			if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.note"}); err != nil {
				return struct{}{}, err
			}
			_, err := root.SubmitIn(tx, continueDraft())
			return struct{}{}, err
		})
		if _, ok := errors.AsType[*durable.ReadAfterWrite](err); !ok {
			t.Fatalf("late continue = %v, want ReadAfterWrite", err)
		}

		id := must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SubmissionId, error) {
			continued, err := root.SubmitIn(tx, continueDraft())
			if err != nil {
				return 0, err
			}
			_, err = tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.note"})
			return continued, err
		}))
		if record := sicSettledStatus(t, harness, id); record.Type != durable.SubmissionTypeContinue || record.Status != durable.SubmissionDone {
			t.Fatalf("record = %s", jsonText(t, record))
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("rejects a busy continue and steers with whenIdle inside the commit", func(t *testing.T) {
		busy := sicOpenBusy(t)
		_, err := sicSubmitIn(busy.harness, busy.root, continueDraft())
		if _, ok := errors.AsType[*durable.ConversationBusy](err); !ok {
			t.Fatalf("busy continue = %v, want ConversationBusy", err)
		}
		steer := must(sicSubmitIn(busy.harness, busy.root, sicSteerDraft("steer", sicReject)))
		if got := sicInboxIds(t, busy.harness, busy.root); !reflect.DeepEqual(got, []durable.SubmissionId{steer}) {
			t.Fatalf("inbox %v, want [%d]", got, steer)
		}
		closeHarness(t, busy.harness)
	})

	// #11149
	t.Run("throws for a transaction of another Session, or a wrapped one", func(t *testing.T) {
		aHarness, aRoot := openChat(t, newControlledStorage(), chatSetup(t))
		bHarness, _ := openChat(t, newControlledStorage(), chatSetup(t))
		draft := inputDraft("hi")
		const belongs = "The transaction belongs to another Session"
		_, err := durable.Commit(testContext, bHarness, func(tx durable.Tx) (durable.SubmissionId, error) { return aRoot.SubmitIn(tx, draft) })
		expectErrorContains(t, err, belongs)
		// A wrapper of a transaction is not one.
		type wrapped struct{ durable.Tx }
		_, err = durable.Commit(testContext, aHarness, func(tx durable.Tx) (durable.SubmissionId, error) {
			return aRoot.SubmitIn(wrapped{tx}, draft)
		})
		expectErrorContains(t, err, belongs)
		_, err = durable.Commit(testContext, aHarness, func(tx durable.Tx) (durable.SubmissionId, error) {
			return aRoot.SubmitIn(wrapped{}, draft)
		})
		expectErrorContains(t, err, belongs)
		sicExpectNoRun(t, aHarness, aRoot.Id())
		closeHarness(t, aHarness)
		closeHarness(t, bHarness)
	})

	// #11149
	t.Run("records the trace carrier and links a steer to its caller and the running trace", func(t *testing.T) {
		setup := chatSetup(t)
		recorder := telemetry.NewInMemoryTelemetryContext()
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
		submitInput(t, root, "work")
		<-busy.reached
		var run durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		for _, task := range must(harness.Inspect(testContext)).Tasks {
			if task.Record.Kind == "pi.generation" {
				run = task.Record
			}
		}
		id := must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(caller telemetry.TelemetrySpan) (durable.SubmissionId, error) {
			return durable.Commit(chordctx.WithTelemetry(testContext, caller), harness, func(tx durable.Tx) (durable.SubmissionId, error) {
				return root.SubmitIn(tx, sicSteerDraft("steer"))
			})
		}))
		callers := telemetrySpans(recorder, "caller")
		var submitSpan telemetry.RecordedTelemetrySpan
		for _, span := range telemetrySpans(recorder, "pi.durable.submit") {
			if span.Attributes.Value("submission.id") == numberOf(id) {
				submitSpan = span
			}
		}
		if submitSpan.Name == "" || submitSpan.ParentID != nil || submitSpan.Attributes.Value("result") != "queued" {
			t.Fatalf("submit span %+v", submitSpan)
		}
		if len(submitSpan.Links) != 2 || !reflect.DeepEqual(submitSpan.Links[0].Context, callers[0].Context) || !reflect.DeepEqual(submitSpan.Links[1].Context, run.Trace) {
			t.Fatalf("submit links %+v, want the caller's context and the run's trace %v", submitSpan.Links, run.Trace)
		}
		if record := must(must(harness.Submission(testContext, id)).Status(testContext)); !reflect.DeepEqual(record.Trace, submitSpan.Context) {
			t.Fatalf("record trace %v, want %v", record.Trace, submitSpan.Context)
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("starts an idle input's run in the admission's trace", func(t *testing.T) {
		setup := chatSetup(t)
		recorder := telemetry.NewInMemoryTelemetryContext()
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
		id := must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(caller telemetry.TelemetrySpan) (durable.SubmissionId, error) {
			return durable.Commit(chordctx.WithTelemetry(testContext, caller), harness, func(tx durable.Tx) (durable.SubmissionId, error) {
				return root.SubmitIn(tx, inputDraft("hi"))
			})
		}))
		<-busy.reached
		var submitSpan telemetry.RecordedTelemetrySpan
		for _, span := range telemetrySpans(recorder, "pi.durable.submit") {
			if span.Attributes.Value("submission.id") == numberOf(id) {
				submitSpan = span
			}
		}
		record := must(must(harness.Submission(testContext, id)).Status(testContext))
		if submitSpan.Name == "" || !reflect.DeepEqual(record.Trace, submitSpan.Context) {
			t.Fatalf("record trace %v, want %v", record.Trace, submitSpan.Context)
		}
		for _, task := range must(harness.Inspect(testContext)).Tasks {
			if task.Record.Kind == "pi.generation" && !reflect.DeepEqual(task.Record.Trace, record.Trace) {
				t.Fatalf("run trace %v, want %v", task.Record.Trace, record.Trace)
			}
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("makes a write a child of its caller and leaves the commit's events to the caller's commit", func(t *testing.T) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		harness, root := openTracked(t, storage.NewMemoryStorage(), chatSetup(t), recorder)
		id := must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(caller telemetry.TelemetrySpan) (durable.SubmissionId, error) {
			return durable.Commit(chordctx.WithTelemetry(testContext, caller), harness, func(tx durable.Tx) (durable.SubmissionId, error) {
				return root.SubmitIn(tx, writeDraft(durable.EntryDraft{Kind: "app.note"}))
			})
		}))
		caller := telemetrySpans(recorder, "caller")[0]
		submitSpan := telemetrySpans(recorder, "pi.durable.submit")[0]
		if !reflect.DeepEqual(submitSpan.ParentContext, caller.Context) || !submitSpan.Settled || submitSpan.Attributes.Value("submission.id") != numberOf(id) || submitSpan.Attributes.Value("result") != "done" || len(submitSpan.Events) != 0 {
			t.Fatalf("submit span %+v", submitSpan)
		}
		names := []string{}
		for _, event := range caller.Events {
			names = append(names, event.Name)
		}
		if !reflect.DeepEqual(names, []string{"pi.durable.entry", "pi.durable.submission"}) {
			t.Fatalf("caller events %v", names)
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("ends its span before the commit, and a later throw leaves no submission", func(t *testing.T) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		harness, root := openTracked(t, storage.NewMemoryStorage(), chatSetup(t), recorder)
		must(telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(caller telemetry.TelemetrySpan) (struct{}, error) {
			_, err := durable.Commit(chordctx.WithTelemetry(testContext, caller), harness, func(tx durable.Tx) (struct{}, error) {
				if _, err := root.SubmitIn(tx, writeDraft(durable.EntryDraft{Kind: "app.note"})); err != nil {
					return struct{}{}, err
				}
				return struct{}{}, errors.New("later")
			})
			expectErrorContains(t, err, "later")
			return struct{}{}, nil
		}))
		submitSpan := telemetrySpans(recorder, "pi.durable.submit")[0]
		// The span reported an admission the commit then discarded.
		if !submitSpan.Settled || submitSpan.Attributes.Value("result") != "done" {
			t.Fatalf("submit span %+v", submitSpan)
		}
		id := durable.SubmissionId(submitSpan.Attributes.Value("submission.id").(float64))
		if submission := must(harness.Submission(testContext, id)); submission != nil {
			t.Fatalf("submission %d exists", id)
		}
		if events := telemetrySpans(recorder, "caller")[0].Events; len(events) != 0 {
			t.Fatalf("caller events %+v, want none", events)
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("admits atomically from a tool's commit through its conversation handle", func(t *testing.T) {
		setup := chatSetup(t)
		var mu sync.Mutex
		var other Conversation
		var toolTask durable.TaskId
		addTool(t, setup.Registry, ownTool("delegate", "Delegates to another conversation", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			mu.Lock()
			target := other
			toolTask = api.TaskId()
			mu.Unlock()
			handle, err := api.Conversation(ctx, target.Id())
			if err != nil || handle == nil {
				return durable.ToolExecutionResult{}, errors.New("no handle")
			}
			value, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				admitted, err := handle.SubmitIn(tx, inputDraft("from the tool"))
				if err != nil {
					return nil, err
				}
				if _, err := tx.AppendEntry(target.Id(), durable.EntryDraft{Kind: "app.delegated", Data: map[string]any{"submission": float64(admitted)}}); err != nil {
					return nil, err
				}
				return admitted, nil
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return ownText("submitted " + jsonText(t, value)), nil
		}))
		reply := ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			if messages := request.Messages(); len(messages) > 0 {
				if user, ok := messages[len(messages)-1].(ai.UserMessage); ok && user.Content == ai.UserContent(ai.UserText("delegate")) {
					return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("delegate", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}.AssistantMessage(), nil
				}
			}
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}}.AssistantMessage(), nil
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{reply, reply, reply})
		harness, root := openChat(t, newControlledStorage(), setup)
		created := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}, Agent: &fauxModel}))
		mu.Lock()
		other = created
		mu.Unlock()
		run := submitInput(t, root, "delegate")
		if settled := must(run.Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("run = %s", jsonText(t, settled))
		}
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		var delegated *durable.EntryRecord
		for _, entry := range allEntries(t, created) {
			if entry.Kind == "app.delegated" {
				delegated = &entry
			}
		}
		if delegated == nil {
			t.Fatal("no app.delegated entry")
		}
		var data struct {
			Submission durable.SubmissionId `json:"submission"`
		}
		if err := json.Unmarshal([]byte(jsonText(t, delegated.Data)), &data); err != nil {
			t.Fatal(err)
		}
		submission := data.Submission
		settled := sicSettledStatus(t, harness, submission)
		if settled.Type != durable.SubmissionTypeInput || settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		// The placed input carries the tool task's byTaskId, as the commit's own appends do.
		user := commitValue(t, created, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(*settled.Entry) })
		mu.Lock()
		defer mu.Unlock()
		if user.ByTaskId == nil || *user.ByTaskId != toolTask {
			t.Fatalf("byTaskId = %v, want %d", user.ByTaskId, toolTask)
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("admits inside a task's terminal commit", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		admitting := tkOneStep("test.admitting", func(ctx context.Context, _ stepRecordOf[durable.SubmissionId], runtime stepRuntimeOf[durable.SubmissionId]) error {
			handle, err := runtime.Conversation(ctx, runtime.ConversationId())
			if err != nil || handle == nil {
				return errors.New("no handle")
			}
			return runtime.Commit(ctx, func(tx durable.Tx, _ stepRecordOf[durable.SubmissionId]) (*durable.NextTaskState[stepState, durable.SubmissionId], error) {
				id, err := handle.SubmitIn(tx, inputDraft("from the task"))
				if err != nil {
					return nil, err
				}
				return completed[stepState, durable.SubmissionId](id), nil
			})
		})
		addTask(t, setup.Registry, admitting.Erased())
		harness, root := openChat(t, newControlledStorage(), setup)
		task := tkStart(t, root, admitting.Erased())
		harness.Resume()
		settled := must(harness.WaitForTask(testContext, task))
		if settled.State.Outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", settled.State.Outcome.Status)
		}
		id, err := durable.FromJsonValue[durable.SubmissionId](*settled.State.Outcome.Result)
		if err != nil {
			t.Fatal(err)
		}
		status := sicSettledStatus(t, harness, id)
		if status.Type != durable.SubmissionTypeInput || status.Status != durable.SubmissionDone {
			t.Fatalf("status = %s", jsonText(t, status))
		}
		user := commitValue(t, root, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(*status.Entry) })
		if user.ByTaskId == nil || *user.ByTaskId != task {
			t.Fatalf("byTaskId = %v, want %d", user.ByTaskId, task)
		}
		closeHarness(t, harness)
	})

	// #11149
	t.Run("rejects through a handle once its invocation is aborted, and once it ended", func(t *testing.T) {
		setup := chatSetup(t)
		var mu sync.Mutex
		var other Conversation
		var handle durable.ConversationHandle
		started, abortSeen, release := deferred(), deferred(), deferred()
		addTool(t, setup.Registry, ownTool("hold", "Holds until released, also when aborted", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			mu.Lock()
			target := other
			mu.Unlock()
			bound, err := api.Conversation(ctx, target.Id())
			if err != nil || bound == nil {
				return durable.ToolExecutionResult{}, errors.New("no handle")
			}
			mu.Lock()
			handle = bound
			mu.Unlock()
			go func() {
				<-ctx.Done()
				abortSeen.resolve()
			}()
			started.resolve()
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, release.wait(context.Background())
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("hold", map[string]any{}, "c1"), fauxAnswer("ok")})
		harness, root := openChat(t, newControlledStorage(), setup)
		created := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}, Agent: &fauxModel}))
		mu.Lock()
		other = created
		mu.Unlock()
		run := submitInput(t, root, "go")
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		var toolId durable.TaskId
		for _, task := range must(harness.Inspect(testContext)).Tasks {
			if task.Record.Kind == "pi.tool" {
				toolId = task.Record.Id
			}
		}
		aborting := async(func() (string, error) { return harness.AbortTask(testContext, toolId) })
		if err := abortSeen.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		bound := handle
		mu.Unlock()
		draft := inputDraft("late")
		if _, err := bound.Submit(testContext, draft); err == nil {
			t.Fatal("Submit through an aborted handle succeeded")
		}
		if _, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SubmissionId, error) { return bound.SubmitIn(tx, draft) }); err == nil {
			t.Fatal("SubmitIn through an aborted handle succeeded")
		}
		sicExpectNoRun(t, harness, created.Id())
		release.resolve()
		if _, err := aborting.wait(); err != nil {
			t.Fatal(err)
		}
		must(run.Wait(testContext))
		if _, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SubmissionId, error) { return bound.SubmitIn(tx, draft) }); err == nil {
			t.Fatal("SubmitIn through an ended handle succeeded")
		}
		sicExpectNoRun(t, harness, created.Id())
		closeHarness(t, harness)
	})
}
