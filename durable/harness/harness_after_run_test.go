// Ports packages/durable/test/harness-after-run.test.ts (Pi 7e15f960, #11141): the afterRun hook and the tasks it asks
// for. See harness_tasks_test.go for the Go mappings that apply to every case.
//
// Go mappings specific to this file. A crash is a killableStorage: after kill() every later storage call parks like a
// crashed process, so nothing more lands, as the TypeScript Proxy over SQLite does. A hook or task that waits on a gate
// also waits for the signal of its invocation, and for the end of the tests, so an abandoned Harness's goroutines end
// with them. "not-a-list" (`{ tasks: "x" }` from an afterRun hook) is not expressible: RunEndTasks.Tasks is a slice of
// RunEndTask, so the Go type system rejects what TypeScript's cast smuggles in; the rest of "reports broken requests
// and hooks and creates the rest" runs unchanged.

package harness

// pi: packages/durable/src/harness/generation.ts

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// killableStorage is storage whose calls park like a crashed process once kill() was called; a call already running
// finishes.
type killableStorage struct {
	durable.Storage
	dead atomic.Bool
}

func (store *killableStorage) kill() { store.dead.Store(true) }

func (store *killableStorage) parked() bool {
	if store.dead.Load() {
		_ = parkLikeACrashedProcess()
		return true
	}
	return false
}

func (store *killableStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	if store.parked() {
		return 0, context.Cause(crashedProcesses)
	}
	return store.Storage.Commit(ctx, writes, adopt)
}

func (store *killableStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.Conversation(ctx, id)
}

func (store *killableStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	if store.parked() {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, context.Cause(crashedProcesses)
	}
	return store.Storage.ScanConversations(ctx, query, limit, cursor)
}

func (store *killableStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.Entry(ctx, id)
}

func (store *killableStorage) VisibleEntry(ctx context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.VisibleEntry(ctx, conversationId, id)
}

func (store *killableStorage) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, atOrBefore *durable.EntryId) (*durable.EntryRecord, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.FindLatestHeadMarker(ctx, conversationId, atOrBefore)
}

func (store *killableStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	if store.parked() {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, context.Cause(crashedProcesses)
	}
	return store.Storage.ScanEntries(ctx, query, limit, cursor)
}

func (store *killableStorage) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.Task(ctx, id)
}

func (store *killableStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
	if store.parked() {
		return durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor]{}, context.Cause(crashedProcesses)
	}
	return store.Storage.ScanTasks(ctx, query, limit, cursor)
}

func (store *killableStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.Submission(ctx, id)
}

func (store *killableStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if store.parked() {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, context.Cause(crashedProcesses)
	}
	return store.Storage.ScanSubmissions(ctx, query, limit, cursor)
}

func (store *killableStorage) SubmissionByRequest(ctx context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.SubmissionByRequest(ctx, conversationId, requestId)
}

func (store *killableStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.FindDocument(ctx, address, at)
}

func (store *killableStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	if store.parked() {
		return nil, context.Cause(crashedProcesses)
	}
	return store.Storage.Document(ctx, id, at)
}

func (store *killableStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	if store.parked() {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, context.Cause(crashedProcesses)
	}
	return store.Storage.ScanDocuments(ctx, query, limit, cursor)
}

// arOpened is a chat over SQLite whose storage can be killed.
type arOpened struct {
	harness Harness
	root    Conversation
	store   *killableStorage
}

func (opened *arOpened) kill() { opened.store.kill() }

// arSqlitePath is a fresh session file.
func arSqlitePath(t *testing.T) string { return filepath.Join(t.TempDir(), "session.sqlite") }

// arOpenKillable opens a chat over SQLite at path; kill() simulates a crash: every later storage call parks, so nothing
// more lands.
func arOpenKillable(t *testing.T, path string, setup *chatState) *arOpened {
	t.Helper()
	inner := openSqlite(t, path)
	store := &killableStorage{Storage: inner}
	t.Cleanup(func() { _ = inner.Close(testContext) })
	harness, root := openChat(t, store, setup)
	return &arOpened{harness: harness, root: root, store: store}
}

// arProbe is what the after-run task and hook of a test did, and how they behave.
type arProbe struct {
	mu sync.Mutex
	// runs are the labels of the after-run task's runs, in order, reruns included.
	runs []string
	// asked are the summaries the afterRun hook was asked with.
	asked   []RunSummary
	aborted int
	// gate holds every run of the after-run task until resolved.
	gate *deferredGate
	// hookGate holds the hook until resolved.
	hookGate *deferredGate
	// admit is admitted in the after-run task's commit, before its own entry.
	admit func(handle durable.ConversationHandle, tx durable.Tx) error
}

func (state *arProbe) addRun(label string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.runs = append(state.runs, label)
}

func (state *arProbe) runList() []string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]string{}, state.runs...)
}

func (state *arProbe) addAsked(run RunSummary) int {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.asked = append(state.asked, run)
	return len(state.asked)
}

func (state *arProbe) askedList() []RunSummary {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]RunSummary{}, state.asked...)
}

func (state *arProbe) setGate(gate *deferredGate) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.gate = gate
}

func (state *arProbe) setHookGate(gate *deferredGate) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.hookGate = gate
}

func (state *arProbe) gates() (gate, hookGate *deferredGate) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.gate, state.hookGate
}

func (state *arProbe) abortedCount() int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.aborted
}

func arProbeOf() *arProbe { return &arProbe{} }

// arWait waits for gate, or for the signal (which fails like `aborted(signal)` does), or for the end of the tests.
func arWait(gate *deferredGate, signal context.Context) error {
	select {
	case <-gate.done:
		return nil
	case <-signal.Done():
		return abortedBy(signal)
	case <-crashedProcesses.Done():
		return parkLikeACrashedProcess()
	}
}

type arInput struct {
	Label string `json:"label"`
}

type arTestsTask = durable.Task[arInput, stepState, string, any]
type arTestsRuntime = durable.TaskRuntime[arInput, stepState, string, any]
type arTestsRecord = durable.RunningTask[arInput, stepState, string]

// arTests is the after-run task: it records its run, waits for the gate, then appends a `test.tests` entry and completes.
func arTests(state *arProbe) arTestsTask {
	return durable.DefineTask(durable.TaskDefinition[arInput, stepState, string, any]{
		Name:    "test.run-tests",
		Version: 1,
		Initial: func(arInput) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[arInput, stepState, string, any]{
			"run": func(ctx context.Context, task arTestsRecord, runtime arTestsRuntime) error {
				state.addRun(task.Input.Label)
				if gate, _ := state.gates(); gate != nil {
					if err := arWait(gate, runtime.Signal()); err != nil {
						return err
					}
				}
				var handle durable.ConversationHandle
				if state.admit != nil {
					var err error
					if handle, err = runtime.Conversation(ctx, runtime.ConversationId()); err != nil {
						return err
					}
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ arTestsRecord) (*durable.NextTaskState[stepState, string], error) {
					if state.admit != nil {
						if err := state.admit(handle, tx); err != nil {
							return nil, err
						}
					}
					if _, err := tx.AppendEntry(runtime.ConversationId(), durable.EntryDraft{Kind: "test.tests", Data: task.Input.Label}); err != nil {
						return nil, err
					}
					return completed[stepState, string](task.Input.Label), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ arTestsRecord, runtime arTestsRuntime) error {
			state.mu.Lock()
			state.aborted++
			state.mu.Unlock()
			return runtime.Commit(ctx, func(durable.Tx, arTestsRecord) (*durable.NextTaskState[stepState, string], error) {
				return &durable.NextTaskState[stepState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

// arInstall installs the after-run extension: its task and an afterRun hook, by default asking for one task per run.
// It returns the task.
func arInstall(t *testing.T, setup *chatState, state *arProbe, afterRun ...func(ctx context.Context, run RunSummary, api HookApi) (*RunEndTasks, error)) arTestsTask {
	t.Helper()
	tests := arTests(state)
	handler := func(ctx context.Context, run RunSummary, _ HookApi) (*RunEndTasks, error) {
		count := state.addAsked(run)
		if _, hookGate := state.gates(); hookGate != nil {
			if err := arWait(hookGate, ctx); err != nil {
				return nil, err
			}
		}
		return &RunEndTasks{Tasks: []RunEndTask{RunEndTaskOf(tests, arInput{Label: "run " + itoaString(count)})}}, nil
	}
	if len(afterRun) > 0 {
		handler = afterRun[0]
	}
	mustInstall(t, setup.Registry, new(durable.Extension{
		Name:  "after-run",
		Tasks: []durable.AnyTask{tests.Erased()},
		Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: handler})},
	}))
	return tests
}

// arTools are the tools of the tests: edit, finish (terminate) and handoff (terminate with a handoff).
func arTools() *durable.Extension {
	edit := ownTool("edit", "Edit a file", emptyObjectSchema(), func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return ownText("edited"), nil
	})
	finish := ownTool("finish", "End the run", emptyObjectSchema(), func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		result := ownText("finished")
		result.Control = &durable.ToolControl{Terminate: true}
		return result, nil
	})
	handoff := ownTool("handoff", "End the run in a new context", emptyObjectSchema(), func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		result := ownText("handed off")
		result.Control = &durable.ToolControl{Terminate: true, Handoff: new("fresh start")}
		return result, nil
	})
	return &durable.Extension{Name: "tools", Tools: []*durable.ToolRegistration{edit, finish, handoff}}
}

func arCall(name, id string) ai.FauxResponseStep {
	return tlCallsStep(tlCall{name: name, args: map[string]any{}, id: id})
}

func arKindOf(entries []durable.EntryRecord, kind string) []durable.EntryRecord {
	return slices.DeleteFunc(slices.Clone(entries), func(entry durable.EntryRecord) bool { return entry.Kind != kind })
}

func arKinds(entries []durable.EntryRecord) []string { return entryKinds(entries) }

func arKindsWithoutSystem(entries []durable.EntryRecord) []string {
	return slices.DeleteFunc(entryKinds(entries), func(kind string) bool { return kind == "pi.system" })
}

// arUserTexts are the texts of the user messages of the entries, in order.
func arUserTexts(entries []durable.EntryRecord) []string {
	texts := []string{}
	for _, entry := range entries {
		for _, message := range entry.Model {
			if user, ok := message.(ai.UserMessage); ok {
				text, _ := textOf(user)
				texts = append(texts, text)
			}
		}
	}
	return texts
}

func arLast(entries []durable.EntryRecord) durable.EntryRecord { return entries[len(entries)-1] }

func arSummary(reason RunEndReason, first, answer, last durable.EntryId) RunSummary {
	return RunSummary{Reason: reason, First: first, Answer: answer, Last: last}
}

func arExpectAsked(t *testing.T, state *arProbe, want ...RunSummary) {
	t.Helper()
	got := state.askedList()
	if !slices.Equal(got, want) {
		t.Fatalf("asked = %+v, want %+v", got, want)
	}
}

func arExpectRuns(t *testing.T, state *arProbe, want ...string) {
	t.Helper()
	if got := state.runList(); !slices.Equal(got, want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
}

func arExpectLiveEmpty(t *testing.T, harness Harness, root Conversation) {
	t.Helper()
	expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
}

func arIdle(root Conversation) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		_ = root.WaitForIdle(testContext)
		close(done)
	}()
	return done
}

func arWaitIdle(t *testing.T, root Conversation) {
	t.Helper()
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
}

func arSubmitWait(t *testing.T, root Conversation, draft durable.SubmissionDraft) durable.SettledSubmissionRecord {
	t.Helper()
	return must(ownSubmit(t, root, draft).Wait(testContext))
}

func arGo(t *testing.T, root Conversation) durable.SettledSubmissionRecord {
	t.Helper()
	return arSubmitWait(t, root, ownInput("go"))
}

func arInfosOf(setup *chatState, kind durable.ReportKind) []durable.ReportInfo {
	return slices.DeleteFunc(setup.Reports.allInfos(), func(info durable.ReportInfo) bool { return info.Kind != kind })
}

func arSetRestartLimit(setup *chatState, limit *float64) {
	setup.SetSettings(func(settings *HarnessSettings) { settings.RestartLimit = limit })
}

func arTaskState(t *testing.T, harness Harness, id durable.TaskId) durable.TaskState[durable.JsonValue, durable.JsonValue] {
	t.Helper()
	return tkTaskRecord(t, harness, id).State
}

func arExpectCompleted(t *testing.T, state durable.TaskState[durable.JsonValue, durable.JsonValue]) {
	t.Helper()
	if state.Status != durable.TaskTerminal || state.Outcome == nil || state.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("state = %s, want a terminal completed task", jsonText(t, state))
	}
}

func TestAfterRun(t *testing.T) {
	// #11141
	t.Run("waits for the tasks an answer's hooks ask for: inputs done at the answer, the run busy until they end", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		state.setGate(deferred())
		arInstall(t, setup, state)
		mustInstall(t, setup.Registry, arTools())
		setup.Faux.SetResponses([]ai.FauxResponseStep{arCall("edit", "a"), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		// The input settles with the answer, before the task finishes.
		settledRecord := must(submission.Wait(testContext))
		if settledRecord.Status != durable.SubmissionDone {
			t.Fatalf("status = %s, want done", settledRecord.Status)
		}
		eventually(t, func() bool { return len(state.runList()) == 1 })
		live := liveOf(t, harness, root.Id())
		if len(live.Run.Inputs) != 0 {
			t.Fatalf("run inputs = %v, want none", live.Run.Inputs)
		}
		if live.Run.AfterRun == nil || len(live.Run.AfterRun.Tasks) != 1 {
			t.Fatalf("run.afterRun = %+v, want one task", live.Run.AfterRun)
		}
		child := tkTaskRecord(t, harness, live.Run.AfterRun.Tasks[0])
		if child.Owner == nil || *child.Owner != live.Run.TaskId {
			t.Fatalf("owner = %v, want %d", child.Owner, live.Run.TaskId)
		}
		if child.Background {
			t.Fatal("the after-run task is background")
		}
		idle := arIdle(root)
		if settled(idle) {
			t.Fatal("the conversation is idle while the after-run task runs")
		}

		entries := allEntries(t, root)
		answer := arLast(arKindOf(entries, "pi.assistant"))
		arExpectAsked(t, state, arSummary(RunEndAnswer, arKindOf(entries, "pi.user")[0].Id, answer.Id, answer.Id))
		state.gate.resolve()
		<-idle
		if liveOf(t, harness, root.Id()).Run != nil {
			t.Fatal("run remains after the tasks ended")
		}
		if liveOf(t, harness, root.Id()).RunFailure != nil {
			t.Fatal("runFailure was recorded")
		}
		if got := arLast(allEntries(t, root)).Kind; got != "test.tests" {
			t.Fatalf("last entry = %s, want test.tests", got)
		}
		generation := arTaskState(t, harness, live.Run.TaskId)
		expectEqualJSON(t, generation, jsonText(t, map[string]any{"status": "terminal", "outcome": map[string]any{"status": "completed", "result": map[string]any{"entryId": answer.Id}}}))
		closeHarness(t, harness)
	})

	// #11141
	t.Run("asks after a terminate round, the run's last entry its newest result or its handoff's reset", func(t *testing.T) {
		for _, c := range []struct{ tool, lastKind string }{{"finish", "pi.tool-result"}, {"handoff", "pi.reset"}} {
			setup := chatSetup(t)
			state := arProbeOf()
			arInstall(t, setup, state)
			mustInstall(t, setup.Registry, arTools())
			setup.Faux.SetResponses([]ai.FauxResponseStep{arCall(c.tool, "a")})
			harness, root := openChat(t, storage.NewMemoryStorage(), setup)
			arGo(t, root)
			arWaitIdle(t, root)
			entries := allEntries(t, root)
			assistantEntry := arLast(arKindOf(entries, "pi.assistant"))
			arExpectAsked(t, state, arSummary(RunEndTerminate, arKindOf(entries, "pi.user")[0].Id, assistantEntry.Id, arLast(arKindOf(entries, c.lastKind)).Id))
			arExpectRuns(t, state, "run 1")
			if liveOf(t, harness, root.Id()).Run != nil {
				t.Fatalf("%s: run remains", c.tool)
			}
			closeHarness(t, harness)
		}
	})

	// #11141
	t.Run("ends the run in one commit without an afterRun hook, and after the hook when it asks for nothing", func(t *testing.T) {
		plain := chatSetup(t)
		state := arProbeOf()
		mustInstall(t, plain.Registry, new(durable.Extension{Name: "tasks-only", Tasks: []durable.AnyTask{arTests(state).Erased()}}))
		plain.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), plain)
		arGo(t, root)
		// No hook: the answer's commit ended the run.
		arExpectLiveEmpty(t, harness, root)
		closeHarness(t, harness)

		setup := chatSetup(t)
		arInstall(t, setup, state, func(_ context.Context, run RunSummary, _ HookApi) (*RunEndTasks, error) {
			state.addAsked(run)
			return nil, nil
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root = openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		arWaitIdle(t, root)
		if got := len(state.askedList()); got != 1 {
			t.Fatalf("asked %d times, want 1", got)
		}
		arExpectRuns(t, state)
		arExpectLiveEmpty(t, harness, root)
		closeHarness(t, harness)
	})

	// #11141
	t.Run("queues input during the tasks and places it after them: writes first, then steers and follow-ups", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		state.setGate(deferred())
		tests := arTests(state)
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "after-run",
			Tasks: []durable.AnyTask{tests.Erased()},
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(_ context.Context, run RunSummary, _ HookApi) (*RunEndTasks, error) {
				// Only the first run gets a task.
				if state.addAsked(run) == 1 {
					return &RunEndTasks{Tasks: []RunEndTask{RunEndTaskOf(tests, arInput{Label: "run 1"})}}, nil
				}
				return nil, nil
			}})},
		}))
		// The task admits its result for the model in its own commit.
		state.admit = func(handle durable.ConversationHandle, tx durable.Tx) error {
			_, err := handle.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "test.result", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("tests passed"), Timestamp: 1}}}})
			return err
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first"), fauxAnswer("second"), fauxAnswer("third")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		eventually(t, func() bool { return len(state.runList()) == 1 })

		steer := ownSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("steer"), WhenBusy: durable.WhenBusySteer})
		followUp := ownSubmit(t, root, ownInput("later"))
		var busy *durable.ConversationBusy
		if _, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("x"), WhenBusy: durable.WhenBusyReject}); !errors.As(err, &busy) {
			t.Fatalf("whenBusy reject: err = %v, want ConversationBusy", err)
		}
		if _, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue}); !errors.As(err, &busy) {
			t.Fatalf("continue: err = %v, want ConversationBusy", err)
		}
		// Busy, so not idle: it queues and starts the next run after the tasks.
		idleSteer := ownSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("steer 2"), WhenBusy: durable.WhenBusySteer, WhenIdle: durable.WhenIdleReject})
		note := ownSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "test.note"}})
		if status := must(note.Status(testContext)).Status; status != durable.SubmissionDone {
			t.Fatalf("note status = %s, want done", status)
		}
		if status := must(steer.Status(testContext)).Status; status != durable.SubmissionQueued {
			t.Fatalf("steer status = %s, want queued", status)
		}
		if inbox := must(durable.Snapshot(testContext, harness, InboxDoc, root.Id())); len(inbox.Items) != 3 {
			t.Fatalf("inbox = %+v, want 3 items", inbox.Items)
		}

		state.gate.resolve()
		arWaitIdle(t, root)
		for _, submission := range []durable.Submission{steer, followUp, idleSteer} {
			if status := must(submission.Wait(testContext)).Status; status != durable.SubmissionDone {
				t.Fatalf("status = %s, want done", status)
			}
		}
		entries := allEntries(t, root)
		want := []string{"pi.user", "pi.assistant", "test.note", "test.tests", "test.result", "pi.user", "pi.user", "pi.assistant", "pi.user", "pi.assistant"}
		if got := arKindsWithoutSystem(entries); !slices.Equal(got, want) {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
		if got, want := arUserTexts(entries), []string{"go", "tests passed", "steer", "later", "steer 2"}; !slices.Equal(got, want) {
			t.Fatalf("user texts = %v, want %v", got, want)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("places a reset queued during the tasks after them", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		state.setGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		eventually(t, func() bool { return len(state.runList()) == 1 })
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if resets := arKindOf(allEntries(t, root), "pi.reset"); len(resets) != 0 {
			t.Fatalf("reset entries = %v, want none yet", resets)
		}
		state.gate.resolve()
		arWaitIdle(t, root)
		kinds := arKinds(allEntries(t, root))
		if got, want := kinds[len(kinds)-2:], []string{"test.tests", "pi.reset"}; !slices.Equal(got, want) {
			t.Fatalf("last kinds = %v, want %v", got, want)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("asks once a run really ends: not while onYield continues it, and when a queued steer drops the continuation", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		arInstall(t, setup, state)
		var yields atomic.Int32
		addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			if yields.Add(1) <= 2 {
				return &YieldContinue{Continue: ai.UserText("again")}, nil
			}
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("1"), fauxAnswer("2"), fauxAnswer("3")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		arWaitIdle(t, root)
		entries := allEntries(t, root)
		lastAssistant := arLast(arKindOf(entries, "pi.assistant"))
		arExpectAsked(t, state, arSummary(RunEndAnswer, arKindOf(entries, "pi.user")[0].Id, lastAssistant.Id, lastAssistant.Id))
		closeHarness(t, harness)

		// A steer queued while the answer's hooks run: the continuation is dropped, and the run ends after its task.
		dropped := chatSetup(t)
		second := arProbeOf()
		arInstall(t, dropped, second)
		var root2 syncValue[Conversation]
		addHooks(t, dropped.Registry, GenerationTask.Erased(), &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
			if len(second.askedList()) > 0 {
				return nil, nil
			}
			conversation, _ := root2.get()
			if _, err := conversation.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("steer"), WhenBusy: durable.WhenBusySteer}); err != nil {
				return nil, err
			}
			return &YieldContinue{Continue: ai.UserText("again")}, nil
		}})
		dropped.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("1"), fauxAnswer("2")})
		harness, root = openChat(t, storage.NewMemoryStorage(), dropped)
		root2.put(root)
		arGo(t, root)
		arWaitIdle(t, root)
		if got := len(second.askedList()); got != 2 {
			t.Fatalf("asked %d times, want 2", got)
		}
		arExpectRuns(t, second, "run 1", "run 2")
		if got, want := arUserTexts(allEntries(t, root)), []string{"go", "steer"}; !slices.Equal(got, want) {
			t.Fatalf("user texts = %v, want %v", got, want)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("asks the hooks again after a crash before the tasks exist, and never once they do", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		state := arProbeOf()
		state.setHookGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		opened := arOpenKillable(t, path, setup)
		arGo(t, opened.root)
		eventually(t, func() bool { return len(state.askedList()) == 1 })
		opened.kill() // a crash while the hook runs, in `finish`
		state.setHookGate(nil)
		state.setGate(deferred())
		opened = arOpenKillable(t, path, setup)
		opened.harness.Resume()
		eventually(t, func() bool { return len(state.runList()) == 1 })
		if got := len(state.askedList()); got != 2 {
			t.Fatalf("asked %d times, want 2", got)
		}
		opened.kill() // a crash while the task runs, in `drain`
		state.setGate(nil)
		opened = arOpenKillable(t, path, setup)
		arWaitIdle(t, opened.root)
		// The task ran again; the hook was not asked again, and only one task exists.
		if got := len(state.askedList()); got != 2 {
			t.Fatalf("asked %d times, want 2", got)
		}
		arExpectRuns(t, state, "run 2", "run 2")
		if got := len(arKindOf(allEntries(t, opened.root), "test.tests")); got != 1 {
			t.Fatalf("test.tests entries = %d, want 1", got)
		}
		if tasks := must(opened.harness.Inspect(testContext)).Tasks; len(tasks) != 0 {
			t.Fatalf("live tasks = %+v, want none", tasks)
		}
		closeHarness(t, opened.harness)

		// An orderly close in `drain` resumes the same way.
		closing := arSqlitePath(t)
		other := arProbeOf()
		other.setGate(deferred())
		setup2 := chatSetup(t)
		arInstall(t, setup2, other)
		setup2.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		reopened := arOpenKillable(t, closing, setup2)
		arGo(t, reopened.root)
		eventually(t, func() bool { return len(other.runList()) == 1 })
		closeHarness(t, reopened.harness)
		other.setGate(nil)
		reopened = arOpenKillable(t, closing, setup2)
		arWaitIdle(t, reopened.root)
		if got := len(other.askedList()); got != 1 {
			t.Fatalf("asked %d times, want 1", got)
		}
		arExpectRuns(t, other, "run 1", "run 1")
		closeHarness(t, reopened.harness)
	})

	// #11141
	t.Run("ends an answered run without a failure when Stop comes while the hooks run or the tasks run", func(t *testing.T) {
		for _, stage := range []string{"finish", "drain"} {
			setup := chatSetup(t)
			state := arProbeOf()
			tests := arTests(state)
			state.setGate(deferred())
			mustInstall(t, setup.Registry, new(durable.Extension{
				Name:  "after-run",
				Tasks: []durable.AnyTask{tests.Erased()},
				Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(ctx context.Context, run RunSummary, _ HookApi) (*RunEndTasks, error) {
					state.addAsked(run)
					if stage == "finish" {
						<-ctx.Done()
						return nil, abortedBy(ctx)
					}
					return &RunEndTasks{Tasks: []RunEndTask{RunEndTaskOf(tests, arInput{Label: "run 1"})}}, nil
				}})},
			}))
			setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
			harness, root := openChat(t, storage.NewMemoryStorage(), setup)
			submission := ownSubmit(t, root, ownInput("go"))
			must(submission.Wait(testContext))
			eventually(t, func() bool {
				if stage == "finish" {
					return len(state.askedList()) == 1
				}
				return len(state.runList()) == 1
			})
			if err := root.Abort(testContext, nil); err != nil {
				t.Fatal(err)
			}
			live := liveOf(t, harness, root.Id())
			if live.Run != nil || live.RunFailure != nil {
				t.Fatalf("%s: live = %s, want no run and no failure", stage, jsonText(t, live))
			}
			if status := must(submission.Status(testContext)).Status; status != durable.SubmissionDone {
				t.Fatalf("%s: status = %s, want done", stage, status)
			}
			wantAborted, wantRuns := 0, []string{}
			if stage == "drain" {
				wantAborted, wantRuns = 1, []string{"run 1"}
			}
			if got := state.abortedCount(); got != wantAborted {
				t.Fatalf("%s: aborted = %d, want %d", stage, got, wantAborted)
			}
			arExpectRuns(t, state, wantRuns...)
			closeHarness(t, harness)
		}
	})

	// #11141
	t.Run("ends the run normally when one of its tasks is aborted on its own", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		state.setGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		eventually(t, func() bool { return len(state.runList()) == 1 })
		live := liveOf(t, harness, root.Id())
		must(harness.AbortTask(testContext, live.Run.AfterRun.Tasks[0]))
		arWaitIdle(t, root)
		if got := state.abortedCount(); got != 1 {
			t.Fatalf("aborted = %d, want 1", got)
		}
		arExpectLiveEmpty(t, harness, root)
		arExpectCompleted(t, arTaskState(t, harness, live.Run.TaskId))
		closeHarness(t, harness)
	})

	// #11141
	t.Run("reports broken requests and hooks and creates the rest", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		tests := arTests(state)
		throwing := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.throwing",
			Version: 1,
			// Its initial() throws.
			Initial: func(durable.JsonValue) stepState { panic(errors.New("initial failed")) },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"run": func(context.Context, stepRecordOf[durable.JsonValue], stepRuntimeOf[durable.JsonValue]) error {
					return nil
				},
			},
			Abort: func(context.Context, stepRecordOf[durable.JsonValue], stepRuntimeOf[durable.JsonValue]) error {
				return nil
			},
		})
		// Its initial() starts at a phase the definition does not have.
		badStart := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.bad-start",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "nowhere"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"run": func(context.Context, stepRecordOf[durable.JsonValue], stepRuntimeOf[durable.JsonValue]) error {
					return nil
				},
			},
			Abort: func(context.Context, stepRecordOf[durable.JsonValue], stepRuntimeOf[durable.JsonValue]) error {
				return nil
			},
		})
		unknown := *tests.AnyDefinition()
		unknown.Name = "test.unknown"
		requests := []RunEndTask{
			{Task: durable.AnyTask{Definition: &unknown}, Input: nil},
			{Task: GenerationTask.Erased(), Input: map[string]any{}},
			{Task: tests.Erased(), Input: map[string]any{"label": func() string { return "not JSON" }}},
			RunEndTaskOf(throwing, durable.JsonValue(nil)),
			RunEndTaskOf(badStart, durable.JsonValue(nil)),
			RunEndTaskOf(tests, arInput{Label: "ok"}),
		}
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "after-run",
			Tasks: []durable.AnyTask{tests.Erased(), throwing.Erased(), badStart.Erased()},
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(context.Context, RunSummary, HookApi) (*RunEndTasks, error) {
				return &RunEndTasks{Tasks: requests}, nil
			}})},
		}))
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name: "broken",
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(context.Context, RunSummary, HookApi) (*RunEndTasks, error) {
				return nil, errors.New("hook failed")
			}})},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		arWaitIdle(t, root)
		arExpectRuns(t, state, "ok")
		// One report per hook, against the hook's extension; the requests of one hook come together.
		infos := arInfosOf(setup, durable.ReportExtensionError)
		if len(infos) != 2 || infos[0].Extension != "after-run" || infos[1].Extension != "broken" {
			t.Fatalf("extension_error infos = %+v, want after-run then broken", infos)
		}
		reports := setup.Reports.all()
		var rejected *chord.AggregateError
		if !errors.As(reports[0], &rejected) {
			t.Fatalf("first report = %v, want an AggregateError", reports[0])
		}
		messages := []string{}
		for _, err := range rejected.Errors {
			messages = append(messages, err.Error())
		}
		want := []string{
			"Task test.unknown is not installed",
			"An afterRun hook cannot start the built-in task pi.generation",
			"Value contains a non-JSON function; expected strict JSON",
			"initial failed",
			"Task test.bad-start starts at the unknown phase nowhere",
		}
		if !slices.Equal(messages, want) {
			t.Fatalf("rejections = %q, want %q", messages, want)
		}
		if reports[1].Error() != "hook failed" {
			t.Fatalf("second report = %v, want hook failed", reports[1])
		}
		arExpectLiveEmpty(t, harness, root)
		closeHarness(t, harness)
	})

	// #11141
	t.Run("creates a requested task from the installed definition, calling its initial() once, off the line", func(t *testing.T) {
		setup := chatSetup(t)
		var installedCalls, tokenCalls atomic.Int32
		define := func(version int, count *atomic.Int32) durable.Task[durable.JsonValue, stepState, durable.JsonValue, any] {
			return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
				Name:    "test.versioned",
				Version: version,
				Initial: func(durable.JsonValue) stepState {
					count.Add(1)
					return stepState{Phase: "run"}
				},
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
					"run": func(ctx context.Context, _ stepRecordOf[durable.JsonValue], runtime stepRuntimeOf[durable.JsonValue]) error {
						return runtime.Commit(ctx, func(durable.Tx, stepRecordOf[durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
							return completed[stepState, durable.JsonValue](nil), nil
						})
					},
				},
				Abort: func(context.Context, stepRecordOf[durable.JsonValue], stepRuntimeOf[durable.JsonValue]) error {
					return nil
				},
			})
		}
		installed := define(2, &installedCalls)
		// The hook's extension holds an older copy of the task.
		stale := define(1, &tokenCalls)
		mustInstall(t, setup.Registry, new(durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{installed.Erased()}}))
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name: "asker",
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(context.Context, RunSummary, HookApi) (*RunEndTasks, error) {
				return &RunEndTasks{Tasks: []RunEndTask{RunEndTaskOf(stale, durable.JsonValue(nil))}}, nil
			}})},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		arWaitIdle(t, root)
		id, kind := root.Id(), "test.versioned"
		created := must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(durable.TaskQuery{ConversationId: &id, Kind: &kind}, 10, nil)
		}))
		if len(created.Items) != 1 || created.Items[0].Version != 2 || created.Items[0].State.Status != durable.TaskTerminal {
			t.Fatalf("created = %+v, want one terminal task of version 2", created.Items)
		}
		if installedCalls.Load() != 1 || tokenCalls.Load() != 0 {
			t.Fatalf("initial() calls = installed %d, token %d, want 1 and 0", installedCalls.Load(), tokenCalls.Load())
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("lets a hook read the run's history, tool results included", func(t *testing.T) {
		setup := chatSetup(t)
		var kinds syncList[string]
		mustInstall(t, setup.Registry, arTools())
		addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{AfterRun: func(ctx context.Context, run RunSummary, api HookApi) (*RunEndTasks, error) {
			page, err := api.Entries(ctx, durable.EntryQuery{MinEntryId: &run.First, MaxEntryId: &run.Last, Order: new(durable.ScanAscending)}, 100, nil)
			if err != nil {
				return nil, err
			}
			for _, entry := range page.Items {
				kinds.add(entry.Kind)
			}
			answer, err := api.Entry(ctx, run.Answer)
			if err != nil {
				return nil, err
			}
			if answer == nil || answer.Kind != "pi.assistant" {
				return nil, errors.New("the answer entry is not a pi.assistant")
			}
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{arCall("edit", "a"), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		arWaitIdle(t, root)
		withoutSystem := slices.DeleteFunc(kinds.all(), func(kind string) bool { return kind == "pi.system" })
		if want := []string{"pi.user", "pi.assistant", "pi.tool-result", "pi.assistant"}; !slices.Equal(withoutSystem, want) {
			t.Fatalf("kinds = %v, want %v", withoutSystem, want)
		}
		if reports := setup.Reports.all(); len(reports) != 0 {
			t.Fatalf("reports = %v, want none", reports)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("lets a hook read the run's history after a compaction summarized the start of the run", func(t *testing.T) {
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Compaction = &CompactionPolicyPatch{Enabled: new(false), ReserveTokens: new(1000), KeepRecentTokens: new(1), BackgroundTokens: new(0)}
		})
		gate, started := deferred(), deferred()
		var runs syncList[[]string]
		slow := ownTool("edit", "Edit a file", emptyObjectSchema(), func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			started.resolve()
			if err := arWait(gate, testContext); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return ownText("edited"), nil
		})
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "slow",
			Tools: []*durable.ToolRegistration{slow},
			Hooks: []durable.HookRegistration{
				Hook(CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
					return &CompactionDecision{Summary: new("earlier work")}, nil
				})),
				Hook(GenerationTask, &GenerationHooks{AfterRun: func(ctx context.Context, run RunSummary, api HookApi) (*RunEndTasks, error) {
					page, err := api.Entries(ctx, durable.EntryQuery{MinEntryId: &run.First, MaxEntryId: &run.Last, Order: new(durable.ScanAscending)}, 100, nil)
					if err != nil {
						return nil, err
					}
					runs.add(arKinds(page.Items))
					return nil, nil
				}}),
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{arCall("edit", "a"), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		// Its summary is placed at the round's post-tools boundary, in the middle of the run.
		compaction := must(root.Compact(testContext, nil))
		must(harness.WaitForTask(testContext, compaction))
		gate.resolve()
		must(submission.Wait(testContext))
		arWaitIdle(t, root)
		active := []string{}
		for _, entry := range must(root.Context(testContext, nil)).Entries {
			active = append(active, entry.Kind)
		}
		// The active context lost the run's input to the summary; the run's history did not.
		if slices.Contains(active, "pi.user") {
			t.Fatalf("active context = %v, want no pi.user", active)
		}
		if runs.len() != 1 {
			t.Fatalf("hook runs = %d, want 1", runs.len())
		}
		got := slices.DeleteFunc(runs.all()[0], func(kind string) bool { return kind == "pi.system" })
		if want := []string{"pi.user", "pi.assistant", "pi.tool-result", "pi.compaction", "pi.assistant"}; !slices.Equal(got, want) {
			t.Fatalf("run history = %v, want %v", got, want)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("ends an answered run completed, without a failure, once finish was cut off restartLimit times", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		arSetRestartLimit(setup, new(1.0))
		state := arProbeOf()
		state.setHookGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		opened := arOpenKillable(t, path, setup)
		arGo(t, opened.root)
		eventually(t, func() bool { return len(state.askedList()) == 1 })
		ownSubmit(t, opened.root, ownInput("later"))
		opened.kill()
		opened = arOpenKillable(t, path, setup)
		arWaitIdle(t, opened.root)
		if got := len(state.askedList()); got != 1 {
			t.Fatalf("asked %d times, want 1", got)
		}
		live := liveOf(t, opened.harness, opened.root.Id())
		if live.Run != nil || live.RunFailure != nil {
			t.Fatalf("live = %s, want no run and no failure", jsonText(t, live))
		}
		// Queued items stay, as after a failed run.
		if inbox := must(durable.Snapshot(testContext, opened.harness, InboxDoc, opened.root.Id())); len(inbox.Items) != 1 {
			t.Fatalf("inbox = %+v, want one item", inbox.Items)
		}
		interrupted := arInfosOf(setup, durable.ReportTaskInterrupted)
		if len(interrupted) == 0 || interrupted[0].Reason != "restart_limit" {
			t.Fatalf("task_interrupted reports = %+v, want restart_limit", interrupted)
		}
		closeHarness(t, opened.harness)
	})

	// #11141
	t.Run("keeps the run busy while an after-run task cannot run, until Stop orphans it", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		state := arProbeOf()
		state.setGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		opened := arOpenKillable(t, path, setup)
		arGo(t, opened.root)
		eventually(t, func() bool { return len(state.runList()) == 1 })
		opened.kill()
		// Reopened without the extension: its task is blocked.
		bare := chatSetup(t)
		opened = arOpenKillable(t, path, bare)
		opened.harness.Resume()
		live := liveOf(t, opened.harness, opened.root.Id())
		if live.Run == nil || live.Run.AfterRun == nil || len(live.Run.AfterRun.Tasks) != 1 {
			t.Fatalf("run = %+v, want one after-run task", live.Run)
		}
		eventually(t, func() bool {
			return slices.ContainsFunc(must(opened.harness.Inspect(testContext)).Tasks, func(task TaskInspection) bool { return task.State.Kind == TaskInspectionBlocked })
		})
		if settled(arIdle(opened.root)) {
			t.Fatal("the conversation is idle while an after-run task is blocked")
		}
		if err := opened.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		after := liveOf(t, opened.harness, opened.root.Id())
		if after.Run != nil || after.RunFailure != nil {
			t.Fatalf("live = %s, want no run and no failure", jsonText(t, after))
		}
		child := arTaskState(t, opened.harness, live.Run.AfterRun.Tasks[0])
		if child.Status != durable.TaskTerminal || child.Outcome == nil || child.Outcome.Status != durable.OutcomeOrphaned {
			t.Fatalf("child = %s, want orphaned", jsonText(t, child))
		}
		closeHarness(t, opened.harness)
	})

	// #11141
	t.Run("starts a fix run with a follow-up a task admits, after the tasks", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		tests := arTests(state)
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "after-run",
			Tasks: []durable.AnyTask{tests.Erased()},
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(_ context.Context, run RunSummary, _ HookApi) (*RunEndTasks, error) {
				if state.addAsked(run) == 1 {
					return &RunEndTasks{Tasks: []RunEndTask{RunEndTaskOf(tests, arInput{Label: "run 1"})}}, nil
				}
				return nil, nil
			}})},
		}))
		state.admit = func(handle durable.ConversationHandle, tx durable.Tx) error {
			_, err := handle.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("fix the tests"), RequestId: new("fix-1"), WhenBusy: durable.WhenBusyFollowUp})
			return err
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done"), fauxAnswer("fixed")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		arWaitIdle(t, root)
		entries := allEntries(t, root)
		if got, want := arKindsWithoutSystem(entries), []string{"pi.user", "pi.assistant", "test.tests", "pi.user", "pi.assistant"}; !slices.Equal(got, want) {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
		if got, want := arUserTexts(entries), []string{"go", "fix the tests"}; !slices.Equal(got, want) {
			t.Fatalf("user texts = %v, want %v", got, want)
		}
		asked := state.askedList()
		if len(asked) != 2 {
			t.Fatalf("asked %d times, want 2", len(asked))
		}
		// The fix run's history starts with its own input.
		if asked[1].First != arKindOf(entries, "pi.user")[1].Id {
			t.Fatalf("second run's first = %d, want its own input %d", asked[1].First, arKindOf(entries, "pi.user")[1].Id)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("gives a continue after a failed run the failed run's history", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		arInstall(t, setup, state)
		mustInstall(t, setup.Registry, arTools())
		setup.Faux.SetResponses([]ai.FauxResponseStep{arCall("edit", "a"), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(1.0)}); err != nil {
			t.Fatal(err)
		}
		failed := arGo(t, root)
		if failed.Status != durable.SubmissionUnanswered {
			t.Fatalf("status = %s, want unanswered", failed.Status)
		}
		user := arKindOf(allEntries(t, root), "pi.user")[0]
		failure := liveOf(t, harness, root.Id()).RunFailure
		if failure == nil || failure.Reason != RunTurnLimit || failure.First == nil || *failure.First != user.Id {
			t.Fatalf("runFailure = %s, want turn_limit with first %d", jsonText(t, failure), user.Id)
		}
		arExpectAsked(t, state)
		must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SubmissionId, error) {
			return root.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue})
		}))
		arWaitIdle(t, root)
		answer := arLast(arKindOf(allEntries(t, root), "pi.assistant"))
		arExpectAsked(t, state, arSummary(RunEndAnswer, user.Id, answer.Id, answer.Id))
		closeHarness(t, harness)
	})

	// #11141
	t.Run("ends the run without asking when the hooks are deselected before finish runs", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		state := arProbeOf()
		state.setHookGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		opened := arOpenKillable(t, path, setup)
		arGo(t, opened.root)
		eventually(t, func() bool { return len(state.askedList()) == 1 })
		opened.kill()
		setup.Registry.Uninstall(&durable.Extension{Name: "after-run"})
		opened = arOpenKillable(t, path, setup)
		arWaitIdle(t, opened.root)
		if got := len(state.askedList()); got != 1 {
			t.Fatalf("asked %d times, want 1", got)
		}
		arExpectRuns(t, state)
		arExpectLiveEmpty(t, opened.harness, opened.root)
		closeHarness(t, opened.harness)
	})

	// #11141
	t.Run("starts a fork of a conversation running its after-run tasks idle", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		state.setGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		arGo(t, root)
		eventually(t, func() bool { return len(state.runList()) == 1 })
		ownSubmit(t, root, ownInput("later"))
		answer := arLast(arKindOf(allEntries(t, root), "pi.assistant"))
		fork := must(root.Fork(testContext, answer.Id, ConversationCreateOptions{Ownership: ownerless}))
		if liveOf(t, harness, fork.Id()).Run != nil {
			t.Fatal("the fork has a run")
		}
		if inbox := must(durable.Snapshot(testContext, harness, InboxDoc, fork.Id())); inbox != nil && len(inbox.Items) != 0 {
			t.Fatalf("fork inbox = %+v, want empty", inbox.Items)
		}
		state.gate.resolve()
		arWaitIdle(t, root)
		closeHarness(t, harness)
	})

	// #11141
	t.Run("gives a run without a recorded first entry its answer as first", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		arInstall(t, setup, state)
		var root syncValue[Conversation]
		addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{
			// As a run started before `first` existed.
			OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
				conversation, _ := root.get()
				changeLive(t, conversation, func(live *delta.Object) error {
					live.Object("run").Delete("first")
					return nil
				})
				return nil, nil
			},
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, opened := openChat(t, storage.NewMemoryStorage(), setup)
		root.put(opened)
		arGo(t, opened)
		arWaitIdle(t, opened)
		answer := arLast(arKindOf(allEntries(t, opened), "pi.assistant"))
		arExpectAsked(t, state, arSummary(RunEndAnswer, answer.Id, answer.Id, answer.Id))
		closeHarness(t, harness)
	})

	// #11141
	t.Run("ends the run normally when its after-run task is cut off restartLimit times", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		arSetRestartLimit(setup, new(1.0))
		state := arProbeOf()
		state.setGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		opened := arOpenKillable(t, path, setup)
		arGo(t, opened.root)
		eventually(t, func() bool { return len(state.runList()) == 1 })
		generation := liveOf(t, opened.harness, opened.root.Id()).Run.TaskId
		// The task is cut off, and the waiting generation is not: the scheduler faults the task, and the run ends.
		opened.kill()
		state.setGate(nil)
		opened = arOpenKillable(t, path, setup)
		arWaitIdle(t, opened.root)
		arExpectCompleted(t, arTaskState(t, opened.harness, generation))
		if failure := liveOf(t, opened.harness, opened.root.Id()).RunFailure; failure != nil {
			t.Fatalf("runFailure = %+v, want none", failure)
		}
		arExpectRuns(t, state, "run 1")
		faulted := arInfosOf(setup, durable.ReportTaskFaulted)
		if len(faulted) == 0 || faulted[0].Reason != "restart_limit" {
			t.Fatalf("task_faulted reports = %+v, want restart_limit", faulted)
		}
		closeHarness(t, opened.harness)
	})

	// #11141
	t.Run("reports no task_interrupted for a task that ends normally after the limit was lowered", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		var started atomic.Int32
		worker := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.worker",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"run": func(ctx context.Context, _ stepRecordOf[durable.JsonValue], runtime stepRuntimeOf[durable.JsonValue]) error {
					if started.Add(1) == 1 {
						return parkLikeACrashedProcess() // the run a crash cuts off
					}
					// The host lowers the limit while the rerun runs.
					arSetRestartLimit(setup, new(1.0))
					return runtime.Commit(ctx, func(durable.Tx, stepRecordOf[durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(context.Context, stepRecordOf[durable.JsonValue], stepRuntimeOf[durable.JsonValue]) error {
				return nil
			},
		})
		mustInstall(t, setup.Registry, new(durable.Extension{Name: "worker", Tasks: []durable.AnyTask{worker.Erased()}}))
		opened := arOpenKillable(t, path, setup)
		id := tkStart(t, opened.root, worker.Erased())
		opened.harness.Resume()
		eventually(t, func() bool { return started.Load() == 1 })
		opened.kill()
		opened = arOpenKillable(t, path, setup)
		if status := tkWaitOutcome(t, opened.harness, id).Status; status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s, want completed", status)
		}
		if infos := arInfosOf(setup, durable.ReportTaskInterrupted); len(infos) != 0 {
			t.Fatalf("task_interrupted reports = %+v, want none", infos)
		}
		arSetRestartLimit(setup, nil)
		closeHarness(t, opened.harness)
	})

	// #11141
	t.Run("reports no task_interrupted for a generation that ends normally after the limit was lowered in finish", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		state := arProbeOf()
		state.setHookGate(deferred())
		arInstall(t, setup, state)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		opened := arOpenKillable(t, path, setup)
		arGo(t, opened.root)
		eventually(t, func() bool { return len(state.askedList()) == 1 })
		opened.kill() // `finish` cut off once
		rerun := deferred()
		state.setHookGate(rerun)
		opened = arOpenKillable(t, path, setup)
		opened.harness.Resume()
		eventually(t, func() bool { return len(state.askedList()) == 2 })
		// The host lowers the limit while the hook runs; the phase had started under the old one and ends normally.
		arSetRestartLimit(setup, new(1.0))
		rerun.resolve()
		arWaitIdle(t, opened.root)
		arExpectRuns(t, state, "run 2")
		if infos := arInfosOf(setup, durable.ReportTaskInterrupted); len(infos) != 0 {
			t.Fatalf("task_interrupted reports = %+v, want none", infos)
		}
		closeHarness(t, opened.harness)
	})

	// #11141
	t.Run("asks after a terminate round at the turn limit: terminate wins", func(t *testing.T) {
		setup := chatSetup(t)
		state := arProbeOf()
		arInstall(t, setup, state)
		mustInstall(t, setup.Registry, arTools())
		setup.Faux.SetResponses([]ai.FauxResponseStep{arCall("finish", "a")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(1.0)}); err != nil {
			t.Fatal(err)
		}
		if status := arGo(t, root).Status; status != durable.SubmissionDone {
			t.Fatalf("status = %s, want done", status)
		}
		arWaitIdle(t, root)
		asked := state.askedList()
		if len(asked) != 1 || asked[0].Reason != RunEndTerminate {
			t.Fatalf("asked = %+v, want one terminate", asked)
		}
		arExpectRuns(t, state, "run 1")
		if failure := liveOf(t, harness, root.Id()).RunFailure; failure != nil {
			t.Fatalf("runFailure = %+v, want none", failure)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("starts a continue's history at its first tool round when nothing failed and nothing was placed", func(t *testing.T) {
		setup := chatSetup(t)
		var kinds syncList[[]string]
		mustInstall(t, setup.Registry, arTools())
		addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{AfterRun: func(ctx context.Context, run RunSummary, api HookApi) (*RunEndTasks, error) {
			page, err := api.Entries(ctx, durable.EntryQuery{MinEntryId: &run.First, MaxEntryId: &run.Last, Order: new(durable.ScanAscending)}, 100, nil)
			if err != nil {
				return nil, err
			}
			kinds.add(arKinds(page.Items))
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{arCall("edit", "a"), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		// A host writes the user message itself, then continues.
		ownSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "app.prompt", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("go"), Timestamp: 1}}}})
		must(ownSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue}).Wait(testContext))
		arWaitIdle(t, root)
		got := kinds.all()
		if len(got) != 1 || !slices.Equal(got[0], []string{"pi.assistant", "pi.tool-result", "pi.assistant"}) {
			t.Fatalf("history kinds = %v, want [[pi.assistant pi.tool-result pi.assistant]]", got)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("leaves a summary queued during the tasks stale behind a terminate round's handoff", func(t *testing.T) {
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Compaction = &CompactionPolicyPatch{Enabled: new(false), ReserveTokens: new(1000), KeepRecentTokens: new(1), BackgroundTokens: new(0)}
		})
		state := arProbeOf()
		arInstall(t, setup, state)
		gate, started := deferred(), deferred()
		slowHandoff := ownTool("handoff", "End the run in a new context", emptyObjectSchema(), func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			started.resolve()
			if err := arWait(gate, testContext); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			result := ownText("handed off")
			result.Control = &durable.ToolControl{Terminate: true, Handoff: new("fresh start")}
			return result, nil
		})
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "slow-handoff",
			Tools: []*durable.ToolRegistration{slowHandoff},
			Hooks: []durable.HookRegistration{Hook(CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
				return &CompactionDecision{Summary: new("earlier work")}, nil
			}))},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{arCall("handoff", "a")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		compaction := must(root.Compact(testContext, nil))
		must(harness.WaitForTask(testContext, compaction))
		gate.resolve()
		must(submission.Wait(testContext))
		arWaitIdle(t, root)
		entries := allEntries(t, root)
		// The handoff's reset landed with the answer; the summary, cutting before it, settled stale at the run's end.
		if compactions := arKindOf(entries, "pi.compaction"); len(compactions) != 0 {
			t.Fatalf("compaction entries = %v, want none", compactions)
		}
		kinds := arKinds(entries)
		if got, want := kinds[len(kinds)-2:], []string{"pi.reset", "test.tests"}; !slices.Equal(got, want) {
			t.Fatalf("last kinds = %v, want %v", got, want)
		}
		asked := state.askedList()
		if len(asked) == 0 || asked[0].Reason != RunEndTerminate || asked[0].Last != arKindOf(entries, "pi.reset")[0].Id {
			t.Fatalf("asked = %+v, want a terminate ending at the reset", asked)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("stores the answer's held progress with commit A, and asks again after an orderly close in finish", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 200, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		infinite := infinity()
		setup.SetSettings(func(settings *HarnessSettings) { settings.Progress = &ProgressPolicyPatch{PersistWithinMs: &infinite} })
		state := arProbeOf()
		tests := arTests(state)
		hookGate := &syncValue[*deferredGate]{}
		hookGate.put(deferred())
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "after-run",
			Tasks: []durable.AnyTask{tests.Erased()},
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(ctx context.Context, run RunSummary, _ HookApi) (*RunEndTasks, error) {
				state.addAsked(run)
				if gate, _ := hookGate.get(); gate != nil {
					if err := arWait(gate, ctx); err != nil {
						return nil, err
					}
				}
				return &RunEndTasks{Tasks: []RunEndTask{RunEndTaskOf(tests, arInput{Label: "run"})}}, nil
			}})},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("x", 40))})
		opened := arOpenKillable(t, path, setup)
		arGo(t, opened.root)
		eventually(t, func() bool { return len(state.askedList()) == 1 })
		opened.kill()
		opened = arOpenKillable(t, path, setup)
		stored := liveOf(t, opened.harness, opened.root.Id())
		if stored.Generation != nil {
			t.Fatalf("stored generation = %+v, want none", stored.Generation)
		}
		expectEqualJSON(t, stored.Run.AfterRun, `{}`)
		if got, _ := textOf(arKindOf(allEntries(t, opened.root), "pi.assistant")[0].Model[0]); got != strings.Repeat("x", 40) {
			t.Fatalf("stored answer = %q, want the full answer", got)
		}
		opened.harness.Resume()
		eventually(t, func() bool { return len(state.askedList()) == 2 })
		closeHarness(t, opened.harness)
		hookGate.put(nil)
		opened = arOpenKillable(t, path, setup)
		arWaitIdle(t, opened.root)
		if got := len(state.askedList()); got != 3 {
			t.Fatalf("asked %d times, want 3", got)
		}
		arExpectRuns(t, state, "run")
		closeHarness(t, opened.harness)
	})
}

func TestEndRun(t *testing.T) {
	// #11141
	t.Run("leaves a run it does not own alone, and ends an answered run without a failure", func(t *testing.T) {
		live := liveDraftOf("run", delta.JsonObjectOf("taskId", float64(1), "inputs", []any{}, "turns", float64(0), "afterRun", delta.JsonObjectOf("tasks", []any{float64(5)})))
		tx := &settleRecorder{}
		if err := EndRun(tx, live, 2, RunEnd{Status: durable.SubmissionUnanswered, Reason: RunFaulted}); err != nil {
			t.Fatal(err)
		}
		expectEqualJSON(t, live.Object("run").Object("afterRun").Snapshot(), `{"tasks":[5]}`)
		if err := EndRun(tx, live, 1, RunEnd{Status: durable.SubmissionUnanswered, Reason: RunAborted}); err != nil {
			t.Fatal(err)
		}
		if live.Has("run") {
			t.Fatal("run remains")
		}
		if live.Has("runFailure") {
			t.Fatal("runFailure was recorded")
		}
	})

	// #11141
	t.Run("copies a failed run's first entry into runFailure", func(t *testing.T) {
		live := liveDraftOf("run", delta.JsonObjectOf("taskId", float64(1), "inputs", []any{}, "turns", float64(0), "first", float64(7)))
		if err := EndRun(&settleRecorder{}, live, 1, RunEnd{Status: durable.SubmissionUnanswered, Reason: RunModelError}); err != nil {
			t.Fatal(err)
		}
		expectMatch(t, live.Object("runFailure").Snapshot(), `{"reason":"model_error","first":7}`)
	})
}

// arFirstUser is the ID of the conversation's first pi.user entry: the first entry of its first run (`first:
// expect.any(Number)` of the runFailure and pi.live.run expectations, which this pins to the entry it must be).
func arFirstUser(t *testing.T, conversation Conversation) durable.EntryId {
	t.Helper()
	return arKindOf(allEntries(t, conversation), "pi.user")[0].Id
}
