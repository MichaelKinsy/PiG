// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/session"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Ports packages/durable/test/harness-ownership.test.ts through driver.OpenHarness over the real core: the "ownership" cases that need no
// Storage fault seam. A wait that must stay pending is observed positively: parkedIdleWaits polls until its goroutine is parked in the
// idle loop with work live, which a wait that wrongly sees the scope idle never reaches.
//
// Not ported natively: :492 stages two raw setTask writes in one commit, which the Tx surface does not offer; :522 and :559 reject a
// commit, which is fatal to the handle in the core (the :746 :774 :797 case of harness-tasks, questions/dcore-yield.md). Several marks
// are derived twice (at open, and when a reserve pass loads the chain), so mutating one of the two paths alone leaves :357 and :410
// green: both paths together turn :410 red, and the cancellation predicates turn every case red.

type holdInput struct {
	Name      string `json:"name"`
	SlowAbort bool   `json:"slowAbort,omitempty"`
}

type (
	holdRuntime = durable.TaskRuntime[holdInput, stepState, durable.JsonValue, any]
	holdRecord  = durable.RunningTask[holdInput, stepState, durable.JsonValue]
	holdNext    = durable.NextTaskState[stepState, durable.JsonValue]
)

type ownGate struct {
	once   sync.Once
	done   chan struct{}
	ending string
}

// ownFixtures holds the gates and run counts of one test's hold task.
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

func (f *ownFixtures) gate(name string) *ownGate {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := f.gates[name]
	if found == nil {
		found = &ownGate{done: make(chan struct{})}
		f.gates[name] = found
	}
	return found
}

func (f *ownFixtures) open(name, ending string) {
	gate := f.gate(name)
	gate.once.Do(func() {
		gate.ending = ending
		close(gate.done)
	})
}

func abortedOutcome() *holdNext {
	return &holdNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}
}

// holdTask holds until its named gate opens or it is aborted. With SlowAbort its abort handler first waits for the gate "abort.<name>".
func (f *ownFixtures) holdTask() durable.Task[holdInput, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[holdInput, stepState, durable.JsonValue, any]{
		Name: "test.hold", Version: 1,
		Initial: func(holdInput) stepState { return stepState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[holdInput, stepState, durable.JsonValue, any]{
			"hold": func(ctx context.Context, task holdRecord, runtime holdRuntime) error {
				f.mu.Lock()
				f.runs[task.Input.Name]++
				f.mu.Unlock()
				gate := f.gate(task.Input.Name)
				select {
				case <-gate.done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*holdNext, error) {
					if gate.ending == "completed" {
						return &holdNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted}}, nil
					}
					return &holdNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "gate failed"}}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, task holdRecord, runtime holdRuntime) error {
			if task.Input.SlowAbort {
				select {
				case <-f.gate("abort." + task.Input.Name).done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
			}
			return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*holdNext, error) { return abortedOutcome(), nil })
		},
	})
}

type ownTree struct {
	owner durable.TaskId
	child durable.ConversationId
	inner durable.TaskId
}

type ownOptions struct{ background, slowInner, slowOwner bool }

var ownedByConversation = durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}

// ownedChild stages, in parent, a task owner and a conversation it owns holding the task inner, in one commit.
func ownedChild(t *testing.T, f *ownFixtures, parent harness.Conversation, name string, options ...ownOptions) ownTree {
	t.Helper()
	var option ownOptions
	if len(options) > 0 {
		option = options[0]
	}
	tree, err := durable.Commit(t.Context(), parent, func(tx durable.Tx) (ownTree, error) {
		owner, err := durable.CreateTask(tx, f.hold, holdInput{Name: name, SlowAbort: option.slowOwner}, durable.TaskOptions{Ownership: ownedByConversation, Background: option.background})
		if err != nil {
			return ownTree{}, err
		}
		child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
		if err != nil {
			return ownTree{}, err
		}
		inner, err := durable.CreateTask(tx, f.hold, holdInput{Name: name + ".inner", SlowAbort: option.slowInner}, durable.TaskOptions{Ownership: ownedByConversation, ConversationId: &child.Id})
		return ownTree{owner: owner, child: child.Id, inner: inner}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

type ownOpened struct {
	h    harness.Harness
	root harness.Conversation
	s    *driver.Session
}

func ownOpen(t *testing.T, f *ownFixtures) ownOpened {
	t.Helper()
	h, root := openTasksHarness(t, f.hold.Erased())
	h.Resume()
	return ownOpened{h: h, root: root, s: h.(*driver.Session)}
}

func (o ownOpened) conversation(t *testing.T, id durable.ConversationId) harness.Conversation {
	t.Helper()
	conversation, err := o.h.Conversation(t.Context(), id)
	if err != nil || conversation == nil {
		t.Fatalf("Conversation(%d) = %v, %v", id, conversation, err)
	}
	return conversation
}

func (o ownOpened) record(t *testing.T, id durable.TaskId) durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	record, err := o.h.GetTask(t.Context(), id)
	if err != nil || record == nil {
		t.Fatalf("GetTask(%d) = %v, %v", id, record, err)
	}
	return *record
}

func (o ownOpened) status(t *testing.T, id durable.TaskId) durable.TaskStatus {
	t.Helper()
	return o.record(t, id).State.Status
}

func (o ownOpened) notTerminal(t *testing.T, id durable.TaskId) {
	t.Helper()
	if status := o.status(t, id); status == durable.TaskTerminal {
		t.Fatalf("task %d is terminal", id)
	}
}

func (o ownOpened) expectOutcome(t *testing.T, id durable.TaskId, want durable.TaskOutcomeStatus) {
	t.Helper()
	settled := waitSettled(t, o.h, id)
	if settled.State.Outcome == nil {
		t.Fatalf("task %d settled without an outcome (%s)", id, settled.State.Status)
	}
	if got := settled.State.Outcome.Status; got != want {
		t.Fatalf("task %d ended %s, want %s", id, got, want)
	}
}

// busy marks a conversation busy with task standing in for its run, so submissions queue.
func (o ownOpened) busy(t *testing.T, conversation harness.Conversation, task durable.TaskId) {
	t.Helper()
	_, err := conversation.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		live, err := tx.Doc(harness.LiveDoc, conversation.Id())
		if err != nil {
			return nil, err
		}
		encoded, err := durable.ToJsonValue(harness.LiveRun{TaskId: task, Inputs: []durable.SubmissionId{}})
		if err != nil {
			return nil, err
		}
		return nil, live.Set("run", encoded)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (o ownOpened) submission(t *testing.T, id durable.SubmissionId) durable.SubmissionRecord {
	t.Helper()
	submission, err := o.h.Submission(t.Context(), id)
	if err != nil || submission == nil {
		t.Fatalf("Submission(%d) = %v, %v", id, submission, err)
	}
	record, err := submission.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func ownSubmit(t *testing.T, conversation harness.Conversation, draft durable.SubmissionDraft) durable.SubmissionId {
	t.Helper()
	submission, err := conversation.Submit(t.Context(), draft)
	if err != nil {
		t.Fatal(err)
	}
	return submission.Id()
}

func ownInput(text string) durable.SubmissionDraft {
	return durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)}
}

func ownNote() durable.SubmissionDraft {
	// A write that affects model context queues while busy; a data-only write would be appended at once.
	return durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("w")}}}}
}

func (o ownOpened) abortTask(t *testing.T, id durable.TaskId) {
	t.Helper()
	if _, err := o.h.AbortTask(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

// pendingWait is a wait running on its own goroutine. The caller calls release just before it lets the awaited work end; a wait that
// returned before that call returned early, and it noted that the instant it returned, so the check needs no sleep and cannot fail a
// correct wait.
type pendingWait struct {
	done     chan error
	released atomic.Bool
	early    atomic.Bool
}

// parkedIdleWaits counts goroutines parked in a WaitForIdle loop waiting for a task notice. A wait is pending exactly when its goroutine is
// parked there: it read the task graph, found work live and registered for the next notice. The state persists until a notice wakes it, so
// polling for it is deterministic, and a wait that wrongly sees the scope idle never reaches it.
func parkedIdleWaits() int {
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			buffer = buffer[:n]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	count := 0
	for stack := range strings.SplitSeq(string(buffer), "\n\n") {
		if strings.Contains(stack, "driver.(*coreSession).idleLoop") && strings.Contains(stack, "sqlhost.(*Expectation).Wait") {
			count++
		}
	}
	return count
}

func (o ownOpened) wait(t *testing.T, wait func(context.Context) error) *pendingWait {
	t.Helper()
	before := parkedIdleWaits()
	pending := &pendingWait{done: make(chan error, 1)}
	go func() {
		err := wait(t.Context())
		if !pending.released.Load() {
			pending.early.Store(true)
		}
		pending.done <- err
	}()
	pollUntil(t, "the wait to park with work live", func() bool { return parkedIdleWaits() > before })
	return pending
}

// live is the idle decision from one task-graph read: deterministic where a wait that must stay pending can only be observed
// probabilistically.
func (o ownOpened) live(t *testing.T, root *durable.ConversationId) bool {
	t.Helper()
	live, err := o.s.IdleLive(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	return live
}

func (p *pendingWait) release() { p.released.Store(true) }

// result waits for the wait to return and fails when it returned before release.
func (p *pendingWait) result(t *testing.T) error {
	t.Helper()
	err := awaitChan(t, "the wait", p.done)
	if p.early.Load() {
		t.Error("the wait returned while the awaited work was still live")
	}
	return err
}

func ownWaitStatus(t *testing.T, o ownOpened, id durable.TaskId, want durable.TaskStatus) {
	t.Helper()
	pollUntil(t, "task status", func() bool { return o.status(t, id) == want })
}

// harness-ownership.test.ts:173.
func TestCoreOwnershipKeepsAConversationBusyWhileItsOwnedForegroundSubtreeHasLiveWork(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "owner")
	f.open("owner", "completed")
	ownWaitStatus(t, o, tree.owner, durable.TaskCompleting)
	rootId := o.root.Id()
	if !o.live(t, &rootId) || !o.live(t, nil) {
		t.Fatal("the conversation and the Harness are idle while the owner's subtree has live work")
	}
	idle := o.wait(t, o.root.WaitForIdle)
	idle.release()
	f.open("owner.inner", "completed")
	if err := idle.result(t); err != nil {
		t.Fatal(err)
	}
	if err := o.h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if outcome := o.record(t, tree.owner).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("owner outcome %+v, want completed", outcome)
	}
}

// harness-ownership.test.ts:188.
func TestCoreOwnershipStopsIdleTraversalAtABackgroundOwner(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "background", ownOptions{background: true})
	rootId, childId := o.root.Id(), tree.child
	if o.live(t, &rootId) || o.live(t, nil) {
		t.Fatal("the root conversation or the Harness is not idle although only background work is live")
	}
	if !o.live(t, &childId) {
		t.Fatal("the background child is idle while it has live work")
	}
	if err := o.root.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := o.h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	child := o.conversation(t, tree.child)
	idle := o.wait(t, child.WaitForIdle)
	idle.release()
	if err := child.Abort(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := idle.result(t); err != nil {
		t.Fatal(err)
	}
}

// harness-ownership.test.ts:200.
func TestCoreOwnershipCascadesAnAbortMarkToTheOwnedForegroundSubtreeAndWithdrawsItsQueuedInputs(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "owner")
	nested := o.conversation(t, tree.child)
	deeper := ownedChild(t, f, nested, "deeper")
	shielded := ownedChild(t, f, nested, "shielded", ownOptions{background: true})
	o.busy(t, nested, tree.inner)
	queued := ownSubmit(t, nested, ownInput("later"))
	if result, err := o.h.AbortTask(t.Context(), tree.owner); err != nil || result != "marked" {
		t.Fatalf("AbortTask = %q, %v", result, err)
	}
	for _, id := range []durable.TaskId{tree.owner, tree.inner, deeper.owner, deeper.inner} {
		o.expectOutcome(t, id, durable.OutcomeAborted)
	}
	o.notTerminal(t, shielded.owner)
	o.notTerminal(t, shielded.inner)
	if record := o.submission(t, queued); record.Status != durable.SubmissionUnanswered || record.Reason == nil || *record.Reason != "aborted" {
		t.Fatalf("queued input %+v, want unanswered: aborted", record)
	}
	o.abortTask(t, shielded.owner)
}

// harness-ownership.test.ts:224.
func TestCoreOwnershipCascadesAFailedOwnerButNotACompletedOne(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	failed := ownedChild(t, f, o.root, "failed")
	done := ownedChild(t, f, o.root, "done")
	f.open("failed", "failed")
	f.open("done", "completed")
	o.expectOutcome(t, failed.inner, durable.OutcomeAborted)
	o.expectOutcome(t, failed.owner, durable.OutcomeFailed)
	ownWaitStatus(t, o, done.owner, durable.TaskCompleting)
	o.notTerminal(t, done.inner)
	f.open("done.inner", "completed")
	o.expectOutcome(t, done.owner, durable.OutcomeCompleted)
}

// harness-ownership.test.ts:239.
func TestCoreOwnershipAbortsABackgroundTaskDirectlyWithItsOrdinarySubtree(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "background", ownOptions{background: true})
	o.abortTask(t, tree.owner)
	o.expectOutcome(t, tree.inner, durable.OutcomeAborted)
}

// harness-ownership.test.ts:247.
func TestCoreOwnershipAbortsAConversationWithQueuedInputsWithdrawnWritesKeptForegroundAbortedBackgroundKept(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	foreground := ownedChild(t, f, o.root, "foreground")
	background := ownedChild(t, f, o.root, "background", ownOptions{background: true})
	o.busy(t, o.root, foreground.owner)
	input := ownSubmit(t, o.root, ownInput("later"))
	write := ownSubmit(t, o.root, ownNote())
	if err := o.root.Abort(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []durable.TaskId{foreground.owner, foreground.inner} {
		if status := o.status(t, id); status != durable.TaskTerminal {
			t.Fatalf("task %d is %s, want terminal", id, status)
		}
	}
	o.notTerminal(t, background.owner)
	o.notTerminal(t, background.inner)
	if record := o.submission(t, input); record.Reason == nil || *record.Reason != "aborted" {
		t.Fatalf("queued input %+v, want reason aborted", record)
	}
	if record := o.submission(t, write); record.Status != durable.SubmissionQueued {
		t.Fatalf("queued write %+v, want queued", record)
	}
	o.abortTask(t, background.owner)
}

// harness-ownership.test.ts:266.
func TestCoreOwnershipAbortsWorkCreatedBelowAHeldFailedOwnerButNotBelowATerminalOne(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "owner", ownOptions{slowInner: true})
	f.open("owner", "failed")
	pollUntil(t, "the inner task's abort mark", func() bool { return o.record(t, tree.inner).AbortRequested })
	if status := o.status(t, tree.owner); status != durable.TaskCompleting {
		t.Fatalf("owner is %s, want completing", status)
	}
	child := o.conversation(t, tree.child)
	create := func(name string) durable.TaskId {
		id, err := durable.Commit(t.Context(), child, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, f.hold, holdInput{Name: name}, durable.TaskOptions{Ownership: ownedByConversation})
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	during := create("during")
	o.expectOutcome(t, during, durable.OutcomeAborted)
	f.open("abort.owner.inner", "completed")
	o.expectOutcome(t, tree.owner, durable.OutcomeFailed)
	after := create("after")
	f.open("after", "completed")
	o.expectOutcome(t, after, durable.OutcomeCompleted)
}

// harness-ownership.test.ts:286.
func TestCoreOwnershipWithdrawsQueuedInputsBelowTheAbortedTaskButKeepsItsOwnConversationsQueueAndQueuedWrites(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "owner")
	child := o.conversation(t, tree.child)
	o.busy(t, o.root, tree.owner)
	o.busy(t, child, tree.inner)
	own := ownSubmit(t, o.root, ownInput("own"))
	below := ownSubmit(t, child, ownInput("below"))
	write := ownSubmit(t, child, ownNote())
	o.abortTask(t, tree.owner)
	o.expectOutcome(t, tree.inner, durable.OutcomeAborted)
	for id, want := range map[durable.SubmissionId]durable.SubmissionStatus{own: durable.SubmissionQueued, below: durable.SubmissionUnanswered, write: durable.SubmissionQueued} {
		if got := o.submission(t, id).Status; got != want {
			t.Fatalf("submission %d is %s, want %s", id, got, want)
		}
	}
}

// harness-ownership.test.ts:305.
func TestCoreOwnershipKeepsANestedBackgroundOwnersSubtreeWhenItsCancelledBackgroundOwnerIsAborted(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	outer := ownedChild(t, f, o.root, "outer", ownOptions{background: true})
	inner := ownedChild(t, f, o.conversation(t, outer.child), "inner", ownOptions{background: true})
	o.abortTask(t, outer.owner)
	o.expectOutcome(t, outer.inner, durable.OutcomeAborted)
	o.notTerminal(t, inner.owner)
	o.notTerminal(t, inner.inner)
	o.abortTask(t, inner.owner)
}

// ownOpenAt opens a Harness over the sqlite file at path, running the hold task of f.
func ownOpenAt(t *testing.T, f *ownFixtures, path string, extra ...durable.AnyTask) ownOpened {
	t.Helper()
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: append([]durable.AnyTask{f.hold.Erased()}, extra...)}); err != nil {
		t.Fatal(err)
	}
	h, err := driver.OpenHarness(t.Context(), path, harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = h.Close(context.Background())
		}
	})
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	_ = closed
	return ownOpened{h: h, root: root, s: h.(*driver.Session)}
}

func (o ownOpened) close(t *testing.T) {
	t.Helper()
	if err := o.h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// harness-ownership.test.ts:319.
func TestCoreOwnershipDecidesIdleAfterReopenFromOwnerEdgesItHasToLoadFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	f := newOwnFixtures()
	o := ownOpenAt(t, f, path)
	foreground := ownedChild(t, f, o.root, "fg")
	deeper := ownedChild(t, f, o.conversation(t, foreground.child), "fg2")
	background := ownedChild(t, f, o.root, "bg", ownOptions{background: true})
	f.open("fg", "completed")
	f.open("fg2", "completed")
	ownWaitStatus(t, o, foreground.owner, durable.TaskCompleting)
	ownWaitStatus(t, o, deeper.owner, durable.TaskCompleting)
	o.close(t)

	f = newOwnFixtures()
	o = ownOpenAt(t, f, path)
	rootId := o.root.Id()
	// Two levels below completed foreground owners, the inner tasks keep the root busy.
	if !o.live(t, &rootId) || !o.live(t, nil) {
		t.Fatal("the reopened Harness is idle while inner tasks below completed owners are live")
	}
	idle := o.wait(t, o.root.WaitForIdle)
	idle.release()
	f.open("fg.inner", "completed")
	f.open("fg2.inner", "completed")
	if err := idle.result(t); err != nil {
		t.Fatal(err)
	}
	if err := o.h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status := o.status(t, foreground.owner); status != durable.TaskTerminal {
		t.Fatalf("foreground owner is %s, want terminal", status)
	}
	o.notTerminal(t, background.inner)
	o.abortTask(t, background.owner)
	o.close(t)
}

// harness-ownership.test.ts:357: marks a crash left unapplied are derived at open. The store is written by the in-process kernel (no
// Harness derives marks there) and opened by the native core.
func TestCoreOwnershipDerivesMarksACrashLeftUnappliedBelowACancelledOwnerAtOpen(t *testing.T) {
	cases := []struct {
		label          string
		abortRequested bool
		state          durable.TaskState[durable.JsonValue, durable.JsonValue]
		want           durable.TaskOutcomeStatus
	}{
		{"a held failed owner", false, durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskCompleting, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "crash"}}}, durable.OutcomeFailed},
		{"an abort-marked owner", true, durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: new(durable.JsonValue(map[string]any{"phase": "hold"}))}, durable.OutcomeAborted},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.sqlite")
			f := newOwnFixtures()
			store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			kernel := session.CreateSession(store)
			value, err := kernel.Commit(t.Context(), func(tx durable.Tx) (any, error) {
				root, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
				if err != nil {
					return nil, err
				}
				owner, err := durable.CreateTask(tx, f.hold, holdInput{Name: "gone"}, durable.TaskOptions{Ownership: ownedByConversation, ConversationId: &root.Id})
				if err != nil {
					return nil, err
				}
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
				if err != nil {
					return nil, err
				}
				inner, err := durable.CreateTask(tx, f.hold, holdInput{Name: "orphan"}, durable.TaskOptions{Ownership: ownedByConversation, ConversationId: &child.Id})
				return ownTree{owner: owner, inner: inner}, err
			})
			if err != nil {
				t.Fatal(err)
			}
			tree := value.(ownTree)
			if _, err := kernel.Commit(t.Context(), func(tx durable.Tx) (any, error) {
				record, err := tx.Task(tree.owner)
				if err != nil {
					return nil, err
				}
				marked := *record
				marked.AbortRequested, marked.State = c.abortRequested, c.state
				return nil, tx.(*session.Transaction).SetTask(marked)
			}); err != nil {
				t.Fatal(err)
			}
			if err := kernel.Close(t.Context()); err != nil {
				t.Fatal(err)
			}

			o := ownOpenAt(t, f, path)
			o.expectOutcome(t, tree.inner, durable.OutcomeAborted)
			// The owner finishes only after the work below it.
			o.expectOutcome(t, tree.owner, c.want)
			o.close(t)
		})
	}
}

// harness-ownership.test.ts:395.
func TestCoreOwnershipWithdrawsAnInputQueuedBelowAHeldFailedOwnerAfterItsCascade(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "owner", ownOptions{slowInner: true})
	child := o.conversation(t, tree.child)
	// The inner task stands in for the child's run, which stays busy after the cascade.
	o.busy(t, child, tree.inner)
	f.open("owner", "failed")
	pollUntil(t, "the inner task's abort mark", func() bool { return o.record(t, tree.inner).AbortRequested })
	submission, err := child.Submit(t.Context(), ownInput("late"))
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
		t.Fatalf("late input %+v, want unanswered: aborted", settled)
	}
	f.open("abort.owner.inner", "completed")
	o.expectOutcome(t, tree.owner, durable.OutcomeFailed)
}

// harness-ownership.test.ts:410.
func TestCoreOwnershipMarksWorkAdmittedAfterReopenBelowACancelledOwnerWhoseEdgeWasNotLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	f := newOwnFixtures()
	o := ownOpenAt(t, f, path)
	tree := ownedChild(t, f, o.root, "owner", ownOptions{slowOwner: true})
	f.open("owner.inner", "completed")
	o.expectOutcome(t, tree.inner, durable.OutcomeCompleted)
	// The owner stays live and cancelled in its slow abort handler.
	o.abortTask(t, tree.owner)
	o.close(t)

	// The child is empty at open, so nothing loads its edge until new work arrives.
	f = newOwnFixtures()
	o = ownOpenAt(t, f, path)
	child := o.conversation(t, tree.child)
	late, err := durable.Commit(t.Context(), child, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, f.hold, holdInput{Name: "late"}, durable.TaskOptions{Ownership: ownedByConversation})
	})
	if err != nil {
		t.Fatal(err)
	}
	o.expectOutcome(t, late, durable.OutcomeAborted)
	f.open("abort.owner", "completed")
	o.expectOutcome(t, tree.owner, durable.OutcomeAborted)
	o.close(t)
}

// harness-ownership.test.ts:435: a task kind no extension registers is orphaned by its abort, and the work below it is cascaded.
func TestCoreOwnershipCascadesFromAnOwnerTheSchedulerOrphans(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	unregistered := durable.DefineTask(durable.TaskDefinition[holdInput, stepState, durable.JsonValue, any]{
		Name: "test.unregistered", Version: 1,
		Initial: func(holdInput) stepState { return stepState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[holdInput, stepState, durable.JsonValue, any]{
			"hold": func(context.Context, holdRecord, holdRuntime) error { return nil },
		},
		Abort: func(context.Context, holdRecord, holdRuntime) error { return nil },
	})
	tree, err := durable.Commit(t.Context(), o.root, func(tx durable.Tx) (ownTree, error) {
		owner, err := durable.CreateTask(tx, unregistered, holdInput{Name: "unregistered"}, durable.TaskOptions{Ownership: ownedByConversation})
		if err != nil {
			return ownTree{}, err
		}
		child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
		if err != nil {
			return ownTree{}, err
		}
		inner, err := durable.CreateTask(tx, f.hold, holdInput{Name: "below"}, durable.TaskOptions{Ownership: ownedByConversation, ConversationId: &child.Id})
		return ownTree{owner: owner, inner: inner}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := o.h.AbortTask(t.Context(), tree.owner); err != nil || result != "marked" {
		t.Fatalf("AbortTask = %q, %v", result, err)
	}
	settled := waitSettled(t, o.h, tree.owner)
	if outcome := settled.State.Outcome; outcome == nil || outcome.Status != durable.OutcomeOrphaned || outcome.Reason == nil || *outcome.Reason != "missing_task" {
		t.Fatalf("owner outcome %+v, want orphaned: missing_task", outcome)
	}
	o.expectOutcome(t, tree.inner, durable.OutcomeAborted)
}

// harness-ownership.test.ts:462: cancelling a caller's wait or abort never cancels the shared work. The Pi test's raw setTask that makes
// the second owner's abort handler slow becomes the slowOwner option here.
func TestCoreOwnershipCancelsOnlyTheCallersWaitNeverTheSharedWork(t *testing.T) {
	f := newOwnFixtures()
	o := ownOpen(t, f)
	tree := ownedChild(t, f, o.root, "owner")
	waiting, stopWaiting := context.WithCancelCause(t.Context())
	idle := o.wait(t, func(context.Context) error { return o.root.WaitForIdle(waiting) })
	idle.release()
	stopWaiting(errors.New("stop waiting"))
	ownErrorContains(t, idle.result(t), "stop waiting")
	o.notTerminal(t, tree.inner)

	// Cancelling an abort after its commit leaves the marks in place.
	slow := ownedChild(t, f, o.root, "slow", ownOptions{slowOwner: true})
	aborting, stopAborting := context.WithCancelCause(t.Context())
	abortDone := make(chan error, 1)
	go func() { abortDone <- o.root.Abort(aborting, nil) }()
	pollUntil(t, "the slow owner's abort mark", func() bool { return o.record(t, slow.owner).AbortRequested })
	stopAborting(errors.New("stop aborting"))
	ownErrorContains(t, awaitChan(t, "the cancelled abort", abortDone), "stop aborting")
	f.open("abort.slow", "completed")
	for _, id := range []durable.TaskId{tree.owner, tree.inner, slow.owner, slow.inner} {
		o.expectOutcome(t, id, durable.OutcomeAborted)
	}
}

func ownErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one containing %q", err, want)
	}
}
