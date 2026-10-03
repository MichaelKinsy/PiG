// Ports packages/durable/test/harness-submissions.test.ts.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

func openSqlite(t *testing.T, path string) durable.Storage {
	t.Helper()
	storage, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return storage
}

func liveOf(t *testing.T, harness Harness, id durable.ConversationId) *LiveState {
	t.Helper()
	return must(durable.Snapshot(testContext, harness, LiveDoc, id))
}

func TestSubmissions(t *testing.T) {
	t.Run("appends an idle write and settles it done without a turn", func(t *testing.T) {
		harness, root := openChat(t, newControlledStorage(), chatSetup(t))
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "x"}}}))
		settled := must(submission.Wait(testContext))
		if settled.Entry == nil {
			t.Fatal("settled write has no entry")
		}
		expectEqualJSON(t, settled, jsonText(t, map[string]any{"id": submission.Id(), "conversationId": root.Id(), "type": "write", "status": "done", "entry": *settled.Entry}))
		expectEqualJSON(t, allEntries(t, root), jsonText(t, []any{map[string]any{"id": *settled.Entry, "conversationId": root.Id(), "kind": "note", "data": map[string]any{"text": "x"}}}))
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		id := root.Id()
		tasks := commitValue(t, root, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(durable.TaskQuery{ConversationId: &id}, 10, nil)
		})
		if len(tasks.Items) != 0 {
			t.Fatalf("tasks = %v, want none", tasks.Items)
		}
		closeHarness(t, harness)
	})

	t.Run("places idle input, and rejects busy input with whenBusy reject without writing", func(t *testing.T) {
		storage := newControlledStorage()
		setup := chatSetup(t)
		setup.SetNow(func() float64 { return 42 })
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openChat(t, storage, setup)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		<-busy.reached
		record := must(submission.Status(testContext))
		if record.Status != durable.SubmissionPlaced {
			t.Fatalf("Unexpected %s", record.Status)
		}
		entry := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, durable.UserEntry, *record.Entry)
		})
		expectEqualJSON(t, entry.Model, `[{"role":"user","content":"hi","timestamp":42}]`)
		live := liveOf(t, harness, root.Id())
		expectEqualJSON(t, live.Run.Inputs, jsonText(t, []any{submission.Id()}))
		task := must(harness.GetTask(testContext, live.Run.TaskId))
		if task == nil || task.Kind != "pi.generation" {
			t.Fatalf("run task = %v, want pi.generation", task)
		}

		commits := storage.commitCount()
		_, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("again"), WhenBusy: durable.WhenBusyReject})
		var busyErr *durable.ConversationBusy
		if !errors.As(err, &busyErr) {
			t.Fatalf("error = %v, want ConversationBusy", err)
		}
		if busyErr.ConversationId != root.Id() {
			t.Fatalf("ConversationBusy.ConversationId = %d, want %d", busyErr.ConversationId, root.Id())
		}
		if storage.commitCount() != commits {
			t.Fatalf("commits = %d, want %d", storage.commitCount(), commits)
		}
		closeHarness(t, harness)
	})

	t.Run("deduplicates request IDs per conversation before any write", func(t *testing.T) {
		storage := newControlledStorage()
		setup := chatSetup(t)
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openChat(t, storage, setup)
		first := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), RequestId: new("r1")}))
		<-busy.reached
		commits := storage.commitCount()
		// Deduplication runs before the busy check.
		again := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("different"), RequestId: new("r1")}))
		if again.Id() != first.Id() {
			t.Fatalf("again = %d, want %d", again.Id(), first.Id())
		}
		if storage.commitCount() != commits {
			t.Fatalf("commits = %d, want %d", storage.commitCount(), commits)
		}
		_, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}, RequestId: new("r1")})
		expectError(t, err, "Request r1 already identifies a submission of type input")
		status := must(first.Status(testContext))
		if status.RequestId == nil || *status.RequestId != "r1" || status.Status != durable.SubmissionPlaced {
			t.Fatalf("status = %+v, want requestId r1 placed", status)
		}

		other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless}))
		write := must(other.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}, RequestId: new("r1")}))
		if write.Id() == first.Id() {
			t.Fatal("a request ID of another conversation deduplicated")
		}
		repeated := must(other.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}, RequestId: new("r1")}))
		if repeated.Id() != write.Id() {
			t.Fatalf("repeated = %d, want %d", repeated.Id(), write.Id())
		}
		closeHarness(t, harness)
	})

	t.Run("reports abort results and looks submissions up by conversation", func(t *testing.T) {
		setup := chatSetup(t)
		release := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(release, "answer")})
		harness, root := openChat(t, newControlledStorage(), setup)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		expectAbort := func(got durable.SubmissionAbortResult, err error, want durable.SubmissionAbortResult) {
			t.Helper()
			if err != nil || got != want {
				t.Fatalf("abort = %q, %v; want %q", got, err, want)
			}
		}
		result, err := submission.Abort(testContext)
		expectAbort(result, err, durable.SubmissionAlreadyPlaced)
		rootId := root.Id()
		result, err = harness.AbortSubmission(testContext, submission.Id(), &rootId)
		expectAbort(result, err, durable.SubmissionAlreadyPlaced)
		other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless}))
		otherId := other.Id()
		result, err = harness.AbortSubmission(testContext, submission.Id(), &otherId)
		expectAbort(result, err, durable.SubmissionNotFound)
		result, err = harness.AbortSubmission(testContext, 999_999, nil)
		expectAbort(result, err, durable.SubmissionNotFound)
		if missing := must(harness.Submission(testContext, 999_999)); missing != nil {
			t.Fatalf("submission 999999 = %v, want undefined", missing)
		}

		release.resolve()
		must(submission.Wait(testContext))
		result, err = submission.Abort(testContext)
		expectAbort(result, err, durable.SubmissionSettled)
		result, err = harness.AbortSubmission(testContext, submission.Id(), nil)
		expectAbort(result, err, durable.SubmissionSettled)
		closeHarness(t, harness)
	})

	t.Run("cancels only a wait and rejects pending waits on close", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{unanswered().step})
		harness, root := openChat(t, newControlledStorage(), setup)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		waitCtx, abort := cancelledContext(errors.New("stop waiting"))
		cancelled := make(chan error, 1)
		go func() { _, err := submission.Wait(waitCtx); cancelled <- err }()
		pending := make(chan error, 1)
		go func() { _, err := submission.Wait(testContext); pending <- err }()
		// Upstream's wait() enqueues its read on the Session line synchronously; wait until it registered.
		waitFor(t, func() bool {
			return slices.Contains(harness.(*harnessImpl).submissions.waiters.Keys(), submission.Id())
		})
		abort()
		expectError(t, <-cancelled, "stop waiting")
		if status := must(submission.Status(testContext)); status.Status != durable.SubmissionPlaced {
			t.Fatalf("status = %s, want placed", status.Status)
		}
		closeHarness(t, harness)
		expectError(t, <-pending, "Harness is closed")
	})

	t.Run("rejects a wait whose submission read spans the start of close", func(t *testing.T) {
		entered := deferred()
		release := deferred()
		storage := &heldSubmissionReads{controlledStorage: newControlledStorage(), entered: entered, release: release}
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{unanswered().step})
		harness, root := openChat(t, storage, setup)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		storage.hold.Store(true)
		waiting := make(chan error, 1)
		go func() { _, err := submission.Wait(testContext); waiting <- err }()
		if err := entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		closing := make(chan error, 1)
		go func() { closing <- harness.Close(testContext) }()
		release.resolve()
		expectError(t, <-waiting, "Harness is closed")
		if err := <-closing; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("reacquires a submission after reopen and settles it durably", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		// The first process never answers; the reopened one does.
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step, fauxAnswer("after reopen")})
		harness, root := openChat(t, openSqlite(t, path), setup)
		id := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), RequestId: new("print")})).Id()
		<-busy.reached
		closeHarness(t, harness)

		harness, _ = openChat(t, openSqlite(t, path), setup)
		submission := must(harness.Submission(testContext, id))
		if status := must(submission.Status(testContext)); status.Status != durable.SubmissionPlaced {
			t.Fatalf("status = %s, want placed", status.Status)
		}
		harness.Resume()
		settled := must(submission.Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("Unexpected %s", settled.Status)
		}
		closeHarness(t, harness)

		harness, root = openChat(t, openSqlite(t, path), setup)
		expectEqualJSON(t, must(must(harness.Submission(testContext, id)).Wait(testContext)), jsonText(t, settled))
		again := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), RequestId: new("print")}))
		if again.Id() != id {
			t.Fatalf("again = %d, want %d", again.Id(), id)
		}
		closeHarness(t, harness)
	})

	t.Run("enables scheduling when a caller submits or waits", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		harness, root := openChat(t, newControlledStorage(), setup)
		// No resume(): submitting asks for progress.
		submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		if settled := must(submission.Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("status = %s, want done", settled.Status)
		}
		closeHarness(t, harness)

		passive, passiveRoot := openChat(t, newControlledStorage(), chatSetup(t))
		taskId := commitValue(t, passiveRoot, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, GenerationTask, GenerationInput{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		})
		// A committed task alone does not start scheduling; waiting for it does.
		if task := must(passive.GetTask(testContext, taskId)); task.State.Status != durable.TaskPending {
			t.Fatalf("status = %s, want pending", task.State.Status)
		}
		if task := must(passive.WaitForTask(testContext, taskId)); task.State.Status != durable.TaskTerminal {
			t.Fatalf("status = %s, want terminal", task.State.Status)
		}
		closeHarness(t, passive)
	})

	t.Run("settles submissions by their current record in the transaction", func(t *testing.T) {
		harness, root := openChat(t, newControlledStorage(), chatSetup(t))
		line := harness.(*harnessImpl).SessionImpl
		rootId := root.Id()
		entry := commitValue(t, root, func(tx durable.Tx) (durable.EntryId, error) {
			appended, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "note"})
			return appended.Id, err
		})
		create := func(record durable.SubmissionCreate) durable.SubmissionId {
			return must(commitOnLine(testContext, line, session.TransactionScope{}, func(tx *session.Transaction) (durable.SubmissionId, error) {
				created, err := tx.CreateSubmission(record)
				return created.Id, err
			}))
		}
		queued := create(durable.SubmissionCreate{ConversationId: rootId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued})
		write := create(durable.SubmissionCreate{ConversationId: rootId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionQueued})
		answer := durable.SubmissionSettlement{Status: durable.SubmissionDone, Answer: entry}
		settle := func(id durable.SubmissionId, settlement durable.SubmissionSettlement) error {
			_, err := durable.Commit(testContext, root, func(tx durable.Tx) (struct{}, error) { return struct{}{}, tx.SettleSubmission(id, settlement) })
			return err
		}
		expectError(t, settle(queued, answer), "is not a placed input")
		expectError(t, settle(write, answer), "is not a placed input")
		expectError(t, settle(999_999, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "x"}), "does not exist")

		// A submission created earlier in the same commit settles; a second settlement leaves the first.
		placed := must(commitOnLine(testContext, line, session.TransactionScope{}, func(tx *session.Transaction) (durable.SubmissionId, error) {
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
		}))
		status := must(must(harness.Submission(testContext, placed)).Status(testContext))
		if status.Status != durable.SubmissionDone || status.Entry == nil || *status.Entry != entry || status.Answer == nil || *status.Answer != entry {
			t.Fatalf("status = %+v, want done with entry and answer %d", status, entry)
		}
		closeHarness(t, harness)
	})

	t.Run("appends and reads typed entries through tokens", func(t *testing.T) {
		type counterData struct {
			N float64 `json:"n"`
		}
		counterEntry := durable.DefineEntry[counterData]("app.counter")
		markerEntry := durable.DefineEntry[durable.Never]("app.marker")
		harness, root := openChat(t, newControlledStorage(), chatSetup(t))
		rootId := root.Id()
		counter := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[counterData], error) {
			return durable.TxAppendEntry(tx, counterEntry, rootId, durable.TypedEntryDraft[counterData]{Data: counterData{N: 1}})
		})
		if counter.TypedData.N != 1 {
			t.Fatalf("n = %v, want 1", counter.TypedData.N)
		}
		expectEqualJSON(t, counter.EntryRecord, jsonText(t, map[string]any{"id": counter.Id, "conversationId": rootId, "kind": "app.counter", "data": map[string]any{"n": 1}}))
		marker := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxAppendEntry(tx, markerEntry, rootId, durable.TypedEntryDraft[durable.Never]{})
		})
		if marker.Kind != "app.marker" {
			t.Fatalf("kind = %s, want app.marker", marker.Kind)
		}
		read := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[counterData], error) {
			return durable.TxEntry(tx, counterEntry, counter.Id)
		})
		expectEqualJSON(t, read.EntryRecord, jsonText(t, counter.EntryRecord))
		if other := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, markerEntry, counter.Id)
		}); other != nil {
			t.Fatalf("marker read of a counter = %v, want undefined", other)
		}
		if missing := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[counterData], error) {
			return durable.TxEntry(tx, counterEntry, 999_999)
		}); missing != nil {
			t.Fatalf("missing entry = %v, want undefined", missing)
		}
		if !counterEntry.Is(&counter.EntryRecord) || durable.AssistantEntry.Is(&counter.EntryRecord) {
			t.Fatal("entry guards disagree with kinds")
		}
		if durable.UserEntry.Kind != "pi.user" || durable.AssistantEntry.Kind != "pi.assistant" {
			t.Fatalf("kinds = %s, %s", durable.UserEntry.Kind, durable.AssistantEntry.Kind)
		}
		closeHarness(t, harness)
	})
}

// heldSubmissionReads holds Submission reads once hold is set (HeldReads in harness-submissions.test.ts).
type heldSubmissionReads struct {
	*controlledStorage
	hold    atomic.Bool
	entered *deferredGate
	release *deferredGate
}

func (storage *heldSubmissionReads) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	if storage.hold.Load() {
		storage.entered.resolve()
		if err := storage.release.wait(context.WithoutCancel(ctx)); err != nil {
			return nil, err
		}
	}
	return storage.controlledStorage.Submission(ctx, id)
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// types.test.ts:248 SubmissionDraft exclusivity literals (an input with an entry, a write with content, a write with a
// busy policy). Upstream's draft is a union only the compiler enforces; Harness submit reads `entry` for a write and
// `content`/`whenBusy` for an input (submissions.ts:166-197), so on the same inputs Pi 1.0.0 ignores the other
// member's fields. Go's SubmissionDraft is one struct holding every member, and Submit branches on Type the same way.
func TestSubmissionDraftIgnoresTheFieldsOfTheOtherType(t *testing.T) {
	t.Run("a write with content and a busy policy settles as the plain write", func(t *testing.T) {
		harness, root := openChat(t, newControlledStorage(), chatSetup(t))
		submission := must(root.Submit(testContext, durable.SubmissionDraft{
			Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "x"}},
			Content: ai.UserText("hi"), WhenBusy: durable.WhenBusyReject,
		}))
		settled := must(submission.Wait(testContext))
		if settled.Entry == nil || settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeWrite {
			t.Fatalf("settled = %+v", settled)
		}
		expectEqualJSON(t, allEntries(t, root), jsonText(t, []any{map[string]any{"id": *settled.Entry, "conversationId": root.Id(), "kind": "note", "data": map[string]any{"text": "x"}}}))
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		closeHarness(t, harness)
	})

	t.Run("an input with an entry places the input and appends no write entry", func(t *testing.T) {
		setup := chatSetup(t)
		setup.SetNow(func() float64 { return 42 })
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openChat(t, newControlledStorage(), setup)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{
			Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), Entry: &durable.EntryDraft{Kind: "note"},
		}))
		<-busy.reached
		record := must(submission.Status(testContext))
		if record.Status != durable.SubmissionPlaced || record.Entry == nil {
			t.Fatalf("record = %+v", record)
		}
		entry := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, durable.UserEntry, *record.Entry)
		})
		expectEqualJSON(t, entry.Model, `[{"role":"user","content":"hi","timestamp":42}]`)
		for _, recorded := range allEntries(t, root) {
			if recorded.Kind == "note" {
				t.Fatalf("the entry of an input draft was appended: %+v", recorded)
			}
		}
		closeHarness(t, harness)
	})

	t.Run("a write with a reject policy queues behind a busy conversation instead of failing", func(t *testing.T) {
		setup := chatSetup(t)
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openChat(t, newControlledStorage(), setup)
		must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
		<-busy.reached
		write := must(root.Submit(testContext, durable.SubmissionDraft{
			Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}, WhenBusy: durable.WhenBusyReject,
		}))
		if record := must(write.Status(testContext)); record.Status != durable.SubmissionQueued || record.Type != durable.SubmissionTypeWrite {
			t.Fatalf("record = %+v, want a queued write", record)
		}
		closeHarness(t, harness)
	})
}
