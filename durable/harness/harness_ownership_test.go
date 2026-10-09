// Ports packages/durable/test/harness-ownership.test.ts: the "ownership" cases. See harness_tasks_test.go for the Go
// mappings that apply to every case.

package harness

// pi: packages/durable/src/harness/util.ts

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// ownEnding is the outcome a held task commits once its gate opens: "completed" or "failed".
type ownEnding string

type ownGate struct {
	once   sync.Once
	done   chan struct{}
	ending ownEnding
}

type holdInput struct {
	Name      string `json:"name"`
	SlowAbort bool   `json:"slowAbort,omitempty"`
}

type waiterInput struct {
	On []durable.TaskId `json:"on"`
}

type (
	holdRuntime   = durable.TaskRuntime[holdInput, stepState, durable.JsonValue, any]
	holdRecord    = durable.RunningTask[holdInput, stepState, durable.JsonValue]
	waiterRuntime = durable.TaskRuntime[waiterInput, stepState, durable.JsonValue, any]
	waiterRecord  = durable.RunningTask[waiterInput, stepState, durable.JsonValue]
)

// ownFixtures holds the gates and run counts of one test's Hold and Waiter tasks.
type ownFixtures struct {
	mu    sync.Mutex
	gates map[string]*ownGate
	runs  map[string]int
	hold  durable.Task[holdInput, stepState, durable.JsonValue, any]
}

func newOwnFixtures() *ownFixtures {
	fixtures := &ownFixtures{gates: map[string]*ownGate{}, runs: map[string]int{}}
	fixtures.hold = fixtures.holdTask()
	return fixtures
}

func (fixtures *ownFixtures) gate(name string) *ownGate {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	found := fixtures.gates[name]
	if found == nil {
		found = &ownGate{done: make(chan struct{})}
		fixtures.gates[name] = found
	}
	return found
}

func (fixtures *ownFixtures) open(name string, ending ownEnding) {
	gate := fixtures.gate(name)
	gate.once.Do(func() {
		gate.ending = ending
		close(gate.done)
	})
}

func (fixtures *ownFixtures) runCount(name string) int {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	return fixtures.runs[name]
}

// holdTask is a task that holds until its named gate opens or it is aborted. With SlowAbort, its abort handler first waits for the gate "abort.<name>".
func (fixtures *ownFixtures) holdTask() durable.Task[holdInput, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[holdInput, stepState, durable.JsonValue, any]{
		Name:    "test.hold",
		Version: 1,
		Initial: func(holdInput) stepState { return stepState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[holdInput, stepState, durable.JsonValue, any]{
			"hold": func(ctx context.Context, task holdRecord, runtime holdRuntime) error {
				fixtures.mu.Lock()
				fixtures.runs[task.Input.Name]++
				fixtures.mu.Unlock()
				gate := fixtures.gate(task.Input.Name)
				select {
				case <-gate.done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					if gate.ending == "completed" {
						return completed[stepState, durable.JsonValue](nil), nil
					}
					return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{
						Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "gate failed"},
					}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, task holdRecord, runtime holdRuntime) error {
			if task.Input.SlowAbort {
				// Upstream awaits the gate alone; there a Harness that closes right after its abortTask call seals before the
				// abort invocation starts. A goroutine can start it first, so the handler also ends with the invocation's
				// signal, which close cancels, instead of holding close's join forever.
				select {
				case <-fixtures.gate("abort." + task.Input.Name).done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
			}
			return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

// ownWaiter waits on the tasks in its input, then completes; its abort handler ends it aborted.
func ownWaiter() durable.Task[waiterInput, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[waiterInput, stepState, durable.JsonValue, any]{
		Name:    "test.waiter",
		Version: 1,
		Initial: func(waiterInput) stepState { return stepState{Phase: "wait"} },
		Phases: map[string]durable.PhaseHandler[waiterInput, stepState, durable.JsonValue, any]{
			"wait": func(ctx context.Context, task waiterRecord, runtime waiterRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, waiterRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return waitingState[stepState, durable.JsonValue](stepState{Phase: "done"}, task.Input.On, durable.JoinAllSettled), nil
				})
			},
			"done": func(ctx context.Context, _ waiterRecord, runtime waiterRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, waiterRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ waiterRecord, runtime waiterRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, waiterRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

// ownUnregistered is never registered: aborting it can only orphan it.
func ownUnregistered() durable.Task[holdInput, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[holdInput, stepState, durable.JsonValue, any]{
		Name:    "test.unregistered",
		Version: 1,
		Initial: func(holdInput) stepState { return stepState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[holdInput, stepState, durable.JsonValue, any]{
			"hold": func(context.Context, holdRecord, holdRuntime) error { return nil },
		},
		Abort: tkNoopAbort[holdInput, stepState, durable.JsonValue, any],
	})
}

type ownTree struct {
	owner durable.TaskId
	child durable.ConversationId
	inner durable.TaskId
}

type ownOptions struct {
	background bool
	slowInner  bool
	slowOwner  bool
}

// ownedChild stages, in parent, a task owner and a conversation it owns holding the task inner, in one commit.
func ownedChild(t *testing.T, fixtures *ownFixtures, parent Conversation, name string, options ...ownOptions) ownTree {
	t.Helper()
	var option ownOptions
	if len(options) > 0 {
		option = options[0]
	}
	tree, err := durable.Commit(testContext, parent, func(tx durable.Tx) (ownTree, error) {
		owner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: name, SlowAbort: option.slowOwner}, durable.TaskOptions{Ownership: conversationOwned, Background: option.background})
		if err != nil {
			return ownTree{}, err
		}
		child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
		if err != nil {
			return ownTree{}, err
		}
		inner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: name + ".inner", SlowAbort: option.slowInner}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &child.Id})
		return ownTree{owner: owner, child: child.Id, inner: inner}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// asyncCall is a call running on its own goroutine.
type asyncCall struct {
	done chan struct{}
	err  error
}

func goCall(call func() error) *asyncCall {
	running := &asyncCall{done: make(chan struct{})}
	go func() {
		running.err = call()
		close(running.done)
	}()
	return running
}

func (call *asyncCall) settled() bool {
	flush()
	select {
	case <-call.done:
		return true
	default:
		return false
	}
}

func (call *asyncCall) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-call.done:
		return call.err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a call to return")
		return nil
	}
}

func ownWaitUntil(t *testing.T, check func() bool) {
	t.Helper()
	for attempt := 0; attempt < 500 && !check(); attempt++ {
		time.Sleep(5 * time.Millisecond)
	}
	if !check() {
		t.Fatal("Condition was not reached")
	}
}

// ownBusy marks a conversation busy with task standing in for its run, so submissions queue.
func ownBusy(t *testing.T, conversation Conversation, task durable.TaskId) {
	t.Helper()
	_, err := conversation.Commit(testContext, func(tx durable.Tx) (any, error) {
		live, err := docDraft(tx, LiveDoc, conversation.Id())
		if err != nil {
			return nil, err
		}
		return nil, setJSON(live, "run", LiveRun{TaskId: task, Inputs: []durable.SubmissionId{}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func ownStatus(t *testing.T, harness Harness, id durable.TaskId) durable.TaskState[durable.JsonValue, durable.JsonValue] {
	t.Helper()
	return tkTaskRecord(t, harness, id).State
}

func ownOutcomeStatus(t *testing.T, harness Harness, id durable.TaskId) durable.TaskOutcomeStatus {
	t.Helper()
	return tkWaitOutcome(t, harness, id).Status
}

type ownOpened struct {
	harness Harness
	root    Conversation
	reports *reportLog
}

func ownOpenHarness(t *testing.T, fixtures *ownFixtures, store ...durable.Storage) ownOpened {
	t.Helper()
	var backing durable.Storage = storage.NewMemoryStorage()
	if len(store) > 0 {
		backing = store[0]
	}
	harness, _, reports := openTasks(t, backing, []durable.AnyTask{fixtures.hold, ownWaiter()})
	root := mustRoot(t, harness, nil)
	harness.Resume()
	return ownOpened{harness: harness, root: root, reports: reports}
}

func ownConversation(t *testing.T, harness Harness, id durable.ConversationId) Conversation {
	t.Helper()
	conversation, err := harness.Conversation(testContext, id)
	if err != nil || conversation == nil {
		t.Fatalf("Conversation(%d) %v %v", id, conversation, err)
	}
	return conversation
}

func ownInput(text string) durable.SubmissionDraft {
	return durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)}
}

func ownSubmit(t *testing.T, conversation Conversation, draft durable.SubmissionDraft) durable.Submission {
	t.Helper()
	submission, err := conversation.Submit(testContext, draft)
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

func ownSubmissionStatus(t *testing.T, harness Harness, id durable.SubmissionId) durable.SubmissionRecord {
	t.Helper()
	submission, err := harness.Submission(testContext, id)
	if err != nil || submission == nil {
		t.Fatalf("Submission(%d) %v %v", id, submission, err)
	}
	record, err := submission.Status(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func ownNotTerminal(t *testing.T, harness Harness, id durable.TaskId) {
	t.Helper()
	if status := ownStatus(t, harness, id).Status; status == durable.TaskTerminal {
		t.Fatalf("task %d is terminal", id)
	}
}

func ownExpectOutcome(t *testing.T, harness Harness, id durable.TaskId, want durable.TaskOutcomeStatus) {
	t.Helper()
	if got := ownOutcomeStatus(t, harness, id); got != want {
		t.Fatalf("task %d ended %s, want %s", id, got, want)
	}
}

// ownRejectingStorage fails the commit that writes the abort mark of the task in rejectMark once.
type ownRejectingStorage struct {
	durable.Storage
	rejectMark atomic.Int64
	// reservedFirst is set when a commit reserves the task in rejectMark before its mark commit was attempted.
	reservedFirst atomic.Bool
}

func (store *ownRejectingStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	target := store.rejectMark.Load()
	for _, write := range writes {
		task, ok := write.(durable.TaskWrite)
		if !ok || int64(task.Value.Id) != target || target == 0 {
			continue
		}
		if task.Value.AbortRequested {
			store.rejectMark.Store(0)
			return 0, durable.NewStorageRejected("rejected once", nil)
		}
		if task.Value.State.Status == durable.TaskRunning {
			store.reservedFirst.Store(true)
		}
	}
	return store.Storage.Commit(ctx, writes)
}

func TestOwnership(t *testing.T) {
	t.Run("keeps a conversation busy while its owned foreground subtree has live work, holding the completed owner", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "owner")
		fixtures.open("owner", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.owner).Status == durable.TaskCompleting })
		idle := goCall(func() error { return opened.root.WaitForIdle(testContext) })
		if idle.settled() {
			t.Fatal("the conversation is idle while the owner's subtree has live work")
		}
		if harnessIdle := goCall(func() error { return opened.harness.WaitForIdle(testContext) }); harnessIdle.settled() {
			t.Fatal("the Harness is idle while the owner's subtree has live work")
		}
		fixtures.open("owner.inner", "completed")
		if err := idle.wait(t); err != nil {
			t.Fatal(err)
		}
		if err := opened.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if outcome := ownStatus(t, opened.harness, tree.owner).Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("owner outcome %+v, want completed", outcome)
		}
		mustClose(t, opened.harness)
	})

	t.Run("stops idle traversal at a background owner", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "background", ownOptions{background: true})
		if err := opened.root.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if err := opened.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		// The background child is its own scope.
		child := ownConversation(t, opened.harness, tree.child)
		if goCall(func() error { return child.WaitForIdle(testContext) }).settled() {
			t.Fatal("the background child is idle while it has live work")
		}
		if err := child.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
	})

	t.Run("cascades an abort mark to the owned foreground subtree and withdraws its queued inputs", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "owner")
		nested := ownConversation(t, opened.harness, tree.child)
		deeper := ownedChild(t, fixtures, nested, "deeper")
		shielded := ownedChild(t, fixtures, nested, "shielded", ownOptions{background: true})
		// Busy conversations queue submissions; the inner task stands in for the child's run.
		ownBusy(t, nested, tree.inner)
		queued := ownSubmit(t, nested, ownInput("later")).Id()
		if result, err := opened.harness.AbortTask(testContext, tree.owner); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		for _, id := range []durable.TaskId{tree.owner, tree.inner, deeper.owner, deeper.inner} {
			ownExpectOutcome(t, opened.harness, id, durable.OutcomeAborted)
		}
		// A nested background owner is a boundary.
		ownNotTerminal(t, opened.harness, shielded.owner)
		ownNotTerminal(t, opened.harness, shielded.inner)
		record := ownSubmissionStatus(t, opened.harness, queued)
		if record.Status != durable.SubmissionUnanswered || record.Reason == nil || *record.Reason != "aborted" {
			t.Fatalf("queued input %+v, want unanswered: aborted", record)
		}
		if _, err := opened.harness.AbortTask(testContext, shielded.owner); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
	})

	t.Run("cascades a failed owner but not a completed one", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		failed := ownedChild(t, fixtures, opened.root, "failed")
		completedTree := ownedChild(t, fixtures, opened.root, "done")
		fixtures.open("failed", "failed")
		fixtures.open("done", "completed")
		ownExpectOutcome(t, opened.harness, failed.inner, durable.OutcomeAborted)
		ownExpectOutcome(t, opened.harness, failed.owner, durable.OutcomeFailed)
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, completedTree.owner).Status == durable.TaskCompleting })
		ownNotTerminal(t, opened.harness, completedTree.inner)
		fixtures.open("done.inner", "completed")
		ownExpectOutcome(t, opened.harness, completedTree.owner, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	t.Run("aborts a background task directly with its ordinary subtree", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "background", ownOptions{background: true})
		if _, err := opened.harness.AbortTask(testContext, tree.owner); err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, tree.inner, durable.OutcomeAborted)
		mustClose(t, opened.harness)
	})

	t.Run("aborts a conversation: queued inputs withdrawn, writes kept, foreground work aborted, background kept", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		foreground := ownedChild(t, fixtures, opened.root, "foreground")
		background := ownedChild(t, fixtures, opened.root, "background", ownOptions{background: true})
		ownBusy(t, opened.root, foreground.owner)
		input := ownSubmit(t, opened.root, ownInput("later")).Id()
		write := ownSubmit(t, opened.root, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}).Id()
		if err := opened.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		for _, id := range []durable.TaskId{foreground.owner, foreground.inner} {
			if status := ownStatus(t, opened.harness, id).Status; status != durable.TaskTerminal {
				t.Fatalf("task %d is %s, want terminal", id, status)
			}
		}
		ownNotTerminal(t, opened.harness, background.owner)
		ownNotTerminal(t, opened.harness, background.inner)
		if record := ownSubmissionStatus(t, opened.harness, input); record.Reason == nil || *record.Reason != "aborted" {
			t.Fatalf("queued input %+v, want reason aborted", record)
		}
		if record := ownSubmissionStatus(t, opened.harness, write); record.Status != durable.SubmissionQueued {
			t.Fatalf("queued write %+v, want queued", record)
		}
		if _, err := opened.harness.AbortTask(testContext, background.owner); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
	})

	t.Run("aborts work created below a held failed owner, but not below a terminal one", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "owner", ownOptions{slowInner: true})
		fixtures.open("owner", "failed")
		ownWaitUntil(t, func() bool { return tkTaskRecord(t, opened.harness, tree.inner).AbortRequested })
		if status := ownStatus(t, opened.harness, tree.owner).Status; status != durable.TaskCompleting {
			t.Fatalf("owner is %s, want completing", status)
		}
		child := ownConversation(t, opened.harness, tree.child)
		create := func(name string) durable.TaskId {
			id, err := durable.Commit(testContext, child, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, fixtures.hold, holdInput{Name: name}, durable.TaskOptions{Ownership: conversationOwned})
			})
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		during := create("during")
		ownExpectOutcome(t, opened.harness, during, durable.OutcomeAborted)
		fixtures.open("abort.owner.inner", "completed")
		ownExpectOutcome(t, opened.harness, tree.owner, durable.OutcomeFailed)
		// A terminal owner never cascades: interrogating its conversation runs normally.
		after := create("after")
		fixtures.open("after", "completed")
		ownExpectOutcome(t, opened.harness, after, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	t.Run("withdraws queued inputs below the aborted task but keeps its own conversation's queue and queued writes", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "owner")
		child := ownConversation(t, opened.harness, tree.child)
		ownBusy(t, opened.root, tree.owner)
		ownBusy(t, child, tree.inner)
		own := ownSubmit(t, opened.root, ownInput("own")).Id()
		below := ownSubmit(t, child, ownInput("below")).Id()
		write := ownSubmit(t, child, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}).Id()
		if _, err := opened.harness.AbortTask(testContext, tree.owner); err != nil {
			t.Fatal(err)
		}
		tkWaitOutcome(t, opened.harness, tree.inner)
		for id, want := range map[durable.SubmissionId]durable.SubmissionStatus{own: durable.SubmissionQueued, below: durable.SubmissionUnanswered, write: durable.SubmissionQueued} {
			if got := ownSubmissionStatus(t, opened.harness, id).Status; got != want {
				t.Fatalf("submission %d is %s, want %s", id, got, want)
			}
		}
		mustClose(t, opened.harness)
	})

	t.Run("keeps a nested background owner's subtree when its cancelled background owner is aborted", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		outer := ownedChild(t, fixtures, opened.root, "outer", ownOptions{background: true})
		inner := ownedChild(t, fixtures, ownConversation(t, opened.harness, outer.child), "inner", ownOptions{background: true})
		if _, err := opened.harness.AbortTask(testContext, outer.owner); err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, outer.inner, durable.OutcomeAborted)
		ownNotTerminal(t, opened.harness, inner.owner)
		ownNotTerminal(t, opened.harness, inner.inner)
		if _, err := opened.harness.AbortTask(testContext, inner.owner); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
	})

	t.Run("decides idle after reopen from owner edges it has to load first", func(t *testing.T) {
		path := sqlitePath(t)
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		foreground := ownedChild(t, fixtures, opened.root, "fg")
		deeper := ownedChild(t, fixtures, ownConversation(t, opened.harness, foreground.child), "fg2")
		background := ownedChild(t, fixtures, opened.root, "bg", ownOptions{background: true})
		fixtures.open("fg", "completed")
		fixtures.open("fg2", "completed")
		// Both owners hold their outcomes while the work below them runs.
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, foreground.owner).Status == durable.TaskCompleting })
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, deeper.owner).Status == durable.TaskCompleting })
		mustClose(t, opened.harness)

		fixtures = newOwnFixtures()
		opened = ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		// Two levels below completed foreground owners, the inner tasks keep the root busy.
		if goCall(func() error { return opened.root.WaitForIdle(testContext) }).settled() {
			t.Fatal("the root is idle while two levels of inner tasks live")
		}
		if goCall(func() error { return opened.harness.WaitForIdle(testContext) }).settled() {
			t.Fatal("the Harness is idle while two levels of inner tasks live")
		}
		fixtures.open("fg.inner", "completed")
		fixtures.open("fg2.inner", "completed")
		// The background subtree does not count.
		if err := opened.root.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if err := opened.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if status := ownStatus(t, opened.harness, foreground.owner).Status; status != durable.TaskTerminal {
			t.Fatalf("foreground owner is %s, want terminal", status)
		}
		ownNotTerminal(t, opened.harness, background.inner)
		if _, err := opened.harness.AbortTask(testContext, background.owner); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
	})

	crashCases := []struct {
		label          string
		abortRequested bool
		state          durable.TaskState[durable.JsonValue, durable.JsonValue]
	}{
		{"a held failed owner", false, durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status:  durable.TaskCompleting,
			Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "crash"}},
		}},
		{"an abort-marked owner", true, durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status: durable.TaskPending,
			Checkpoint: func() *durable.JsonValue {
				checkpoint := durable.JsonValue(map[string]any{"phase": "hold"})
				return &checkpoint
			}(),
		}},
	}
	for _, crash := range crashCases {
		t.Run("derives marks a crash left unapplied below "+crash.label+" at open", func(t *testing.T) {
			path := sqlitePath(t)
			fixtures := newOwnFixtures()
			// Without a Harness, nothing derives marks: a cancelled owner with a live task below it.
			kernel := session.CreateSession(tkOpenSqlite(t, path))
			value, err := kernel.Commit(testContext, func(tx durable.Tx) (any, error) {
				root, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownerless})
				if err != nil {
					return nil, err
				}
				owner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "gone"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &root.Id})
				if err != nil {
					return nil, err
				}
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
				if err != nil {
					return nil, err
				}
				inner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "orphan"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &child.Id})
				return ownTree{owner: owner, inner: inner}, err
			})
			if err != nil {
				t.Fatal(err)
			}
			tree := value.(ownTree)
			if _, err := kernel.Commit(testContext, func(tx durable.Tx) (any, error) {
				record, err := tx.Task(tree.owner)
				if err != nil {
					return nil, err
				}
				marked := *record
				marked.AbortRequested, marked.State = crash.abortRequested, crash.state
				return nil, tx.(*session.Transaction).SetTask(marked)
			}); err != nil {
				t.Fatal(err)
			}
			if err := kernel.Close(testContext); err != nil {
				t.Fatal(err)
			}

			opened := ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
			ownExpectOutcome(t, opened.harness, tree.inner, durable.OutcomeAborted)
			// The owner finishes only after the work below it.
			want := durable.OutcomeFailed
			if crash.abortRequested {
				want = durable.OutcomeAborted
			}
			ownExpectOutcome(t, opened.harness, tree.owner, want)
			mustClose(t, opened.harness)
		})
	}

	t.Run("withdraws an input queued below a held failed owner after its cascade", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "owner", ownOptions{slowInner: true})
		child := ownConversation(t, opened.harness, tree.child)
		// The inner task stands in for the child's run, which stays busy after the cascade.
		ownBusy(t, child, tree.inner)
		fixtures.open("owner", "failed")
		ownWaitUntil(t, func() bool { return tkTaskRecord(t, opened.harness, tree.inner).AbortRequested })
		late := ownSubmit(t, child, ownInput("late"))
		settled, err := late.Wait(testContext)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("late input %+v, want unanswered: aborted", settled)
		}
		fixtures.open("abort.owner.inner", "completed")
		tkWaitOutcome(t, opened.harness, tree.owner)
		mustClose(t, opened.harness)
	})

	t.Run("marks work admitted after reopen below a cancelled owner whose edge was not loaded", func(t *testing.T) {
		path := sqlitePath(t)
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		tree := ownedChild(t, fixtures, opened.root, "owner", ownOptions{slowOwner: true})
		fixtures.open("owner.inner", "completed")
		tkWaitOutcome(t, opened.harness, tree.inner)
		// The owner stays live and cancelled in its slow abort handler.
		if _, err := opened.harness.AbortTask(testContext, tree.owner); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)

		// The child is empty at open, so nothing loads its edge until new work arrives.
		fixtures = newOwnFixtures()
		opened = ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		child := ownConversation(t, opened.harness, tree.child)
		late, err := durable.Commit(testContext, child, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "late"}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, late, durable.OutcomeAborted)
		fixtures.open("abort.owner", "completed")
		ownExpectOutcome(t, opened.harness, tree.owner, durable.OutcomeAborted)
		mustClose(t, opened.harness)
	})

	t.Run("cascades from an owner the scheduler orphans", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		unregistered := ownUnregistered()
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (ownTree, error) {
			owner, err := durable.CreateTask(tx, unregistered, holdInput{Name: "unregistered"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return ownTree{}, err
			}
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
			if err != nil {
				return ownTree{}, err
			}
			inner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "below"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &child.Id})
			return ownTree{owner: owner, inner: inner}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := opened.harness.AbortTask(testContext, tree.owner); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		reason := "missing_task"
		expectOutcome(t, tkWaitOutcome(t, opened.harness, tree.owner), durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeOrphaned, Reason: &reason})
		ownExpectOutcome(t, opened.harness, tree.inner, durable.OutcomeAborted)
		mustClose(t, opened.harness)
	})

	t.Run("cancels only the caller's wait, never the shared work", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownedChild(t, fixtures, opened.root, "owner")
		waiting, stopWaiting := context.WithCancelCause(testContext)
		idle := goCall(func() error { return opened.root.WaitForIdle(waiting) })
		flush()
		stopWaiting(errors.New("stop waiting"))
		tkContainsError(t, idle.wait(t), "stop waiting")
		ownNotTerminal(t, opened.harness, tree.inner)

		// Cancelling an abort after its commit leaves the marks in place.
		slow := ownedChild(t, fixtures, opened.root, "slow")
		if _, err := opened.root.Commit(testContext, func(tx durable.Tx) (any, error) {
			record, err := tx.Task(slow.owner)
			if err != nil {
				return nil, err
			}
			changed := *record
			changed.Input = map[string]any{"name": "slow", "slowAbort": true}
			return nil, tx.(*session.Transaction).SetTask(changed)
		}); err != nil {
			t.Fatal(err)
		}
		aborting, stopAborting := context.WithCancelCause(testContext)
		abort := goCall(func() error { return opened.root.Abort(aborting, nil) })
		ownWaitUntil(t, func() bool { return tkTaskRecord(t, opened.harness, slow.owner).AbortRequested })
		stopAborting(errors.New("stop aborting"))
		tkContainsError(t, abort.wait(t), "stop aborting")
		fixtures.open("abort.slow", "completed")
		for _, id := range []durable.TaskId{tree.owner, tree.inner, slow.owner, slow.inner} {
			ownExpectOutcome(t, opened.harness, id, durable.OutcomeAborted)
		}
		mustClose(t, opened.harness)
	})

	t.Run("aborts a waiting child whose awaited task completes in the commit that marks its owner", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		waiter := ownWaiter()
		type family struct{ owner, dependency, blocked durable.TaskId }
		created, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (family, error) {
			owner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "owner"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return family{}, err
			}
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
			if err != nil {
				return family{}, err
			}
			dependency, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "dependency"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return family{}, err
			}
			blocked, err := durable.CreateTask(tx, waiter, waiterInput{On: []durable.TaskId{dependency}}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &child.Id})
			return family{owner: owner, dependency: dependency, blocked: blocked}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		ownWaitUntil(t, func() bool { return fixtures.runCount("dependency") == 1 && fixtures.runCount("owner") == 1 })
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, created.blocked).Status == durable.TaskWaiting })
		// One commit completes the dependency and marks the owner.
		if _, err := opened.root.Commit(testContext, func(tx durable.Tx) (any, error) {
			completedDependency, err := tx.Task(created.dependency)
			if err != nil {
				return nil, err
			}
			marked, err := tx.Task(created.owner)
			if err != nil {
				return nil, err
			}
			internal := tx.(*session.Transaction)
			finished := *completedDependency
			result := durable.JsonValue(nil)
			finished.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}
			if err := internal.SetTask(finished); err != nil {
				return nil, err
			}
			flagged := *marked
			flagged.AbortRequested = true
			return nil, internal.SetTask(flagged)
		}); err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, created.blocked, durable.OutcomeAborted)
		mustClose(t, opened.harness)
	})

	t.Run("retries marks found through an edge loaded after reopen when their commit is rejected", func(t *testing.T) {
		path := sqlitePath(t)
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		tree := ownedChild(t, fixtures, opened.root, "owner", ownOptions{slowOwner: true})
		fixtures.open("owner.inner", "completed")
		tkWaitOutcome(t, opened.harness, tree.inner)
		if _, err := opened.harness.AbortTask(testContext, tree.owner); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)

		rejecting := &ownRejectingStorage{Storage: tkOpenSqlite(t, path)}
		fixtures = newOwnFixtures()
		opened = ownOpenHarness(t, fixtures, rejecting)
		child := ownConversation(t, opened.harness, tree.child)
		late, err := durable.Commit(testContext, child, func(tx durable.Tx) (durable.TaskId, error) {
			id, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "late"}, durable.TaskOptions{Ownership: conversationOwned})
			rejecting.rejectMark.Store(int64(id))
			return id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		ownWaitUntil(t, func() bool { return rejecting.rejectMark.Load() == 0 })
		// Upstream queues the reconcile before the reservation, so the mark is attempted first.
		if rejecting.reservedFirst.Load() {
			t.Fatal("the new task was reserved before its mark commit was attempted")
		}
		// Any later commit, here the reservation of the new task, retries the cascade.
		ownExpectOutcome(t, opened.harness, late, durable.OutcomeAborted)
		fixtures.open("abort.owner", "completed")
		mustClose(t, opened.harness)
	})

	t.Run("retries a cascade whose commit the Storage rejected", func(t *testing.T) {
		fixtures := newOwnFixtures()
		rejecting := &ownRejectingStorage{Storage: storage.NewMemoryStorage()}
		opened := ownOpenHarness(t, fixtures, rejecting)
		tree := ownedChild(t, fixtures, opened.root, "owner")
		rejecting.rejectMark.Store(int64(tree.inner))
		fixtures.open("owner", "failed")
		ownWaitUntil(t, func() bool {
			for _, report := range opened.reports.all() {
				if _, ok := errors.AsType[*durable.StorageRejected](report); ok {
					return true
				}
			}
			return false
		})
		if status := ownStatus(t, opened.harness, tree.owner).Status; status != durable.TaskCompleting {
			t.Fatalf("owner is %s, want completing", status)
		}
		ownNotTerminal(t, opened.harness, tree.inner)
		// The next commit retries the cascade.
		if _, err := opened.root.Commit(testContext, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note"})
		}); err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, tree.inner, durable.OutcomeAborted)
		ownExpectOutcome(t, opened.harness, tree.owner, durable.OutcomeFailed)
		mustClose(t, opened.harness)
	})
}
