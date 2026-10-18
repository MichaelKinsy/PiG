// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi's harness-tasks.test.ts cases that use ControlledStorage and have no native port here, with the reason for each:
//   - :1330 "starts no next phase when close seals during a step that decided to continue": the core holds the registry snapshot
//     and decides inside one step, so no native callback sits between the decision and the commit; the U-row run of the same
//     file (the unmodified Pi test over the core through the facade) is its coverage.
//   - :654 "stops a watch whose acquisition finishes after the invocation ended": needs a held pig.read (holdFindDocument); the
//     native store answers reads inside the step and has no hold for them.
//   - :745 "fails the Harness on a failed reservation commit; reopening runs the task" and :777 "fails the Harness instead of
//     running a task again when its fault write fails": they fail one chosen commit; OpenHarnessFault's onCommit hook can
//     hold or record a commit but not fail it, so durable/harness TestTaskScheduling ports them over ControlledStorage.
// The ports below hold the owner goroutine in the store between a step's commits (driver.OpenHarnessFault's OnCommit hook).

// harness-tasks.test.ts:1163 "stops without outcomes and starts no fresh phase or abort invocation while closing".
func TestCoreFaultCloseStopsWithoutOutcomesAndStartsNoFreshPhaseOrAbortInvocation(t *testing.T) {
	reached, proceed := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var phases []string
	var abortRan atomic.Bool
	two := defineSteps("test.two", func(context.Context, stepRuntime) error { abortRan.Store(true); return nil }, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			mu.Lock()
			phases = append(phases, "one")
			mu.Unlock()
			if err := rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return running("two"), nil }); err != nil {
				return err
			}
			close(reached)
			// Ignores signals and returns normally once released; the closing rule wins over the next phase.
			<-proceed
			return nil
		},
		"two": func(context.Context, stepRecord, stepRuntime) error {
			mu.Lock()
			phases = append(phases, "two")
			mu.Unlock()
			return nil
		},
	})
	store := &controlledStore{}
	h, root := openFault(t, store, harness.HarnessOptions{}, two.Erased())
	id := startTask(t, root, two.Erased())
	h.Resume()
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the first phase never committed")
	}
	// markDurably: the abort mark is durable before the abort joins the run.
	aborting := make(chan string, 1)
	go func() {
		result, aerr := h.AbortTask(context.Background(), id)
		if aerr != nil {
			result = "error: " + aerr.Error()
		}
		aborting <- result
	}()
	var record *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		got, err := h.GetTask(t.Context(), id)
		if err == nil && got != nil && got.AbortRequested {
			record = got
			break
		}
	}
	if record == nil {
		t.Fatal("the abort mark never became durable")
	}
	commits := store.count()
	closing := closeBegunFree(t, h)
	close(proceed)
	select {
	case err := <-closing:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Close never returned")
	}
	if marked := <-aborting; marked != "marked" {
		t.Fatalf("AbortTask = %q, want marked", marked)
	}
	// harness-tasks.test.ts:1148: no outcome; close writes only the stopped task back as pending, so a reopen counts no restart.
	if got := store.count(); got != commits+1 {
		t.Fatalf("commits after the mark = %d, want %d: closing writes the stopped task back as pending, and no outcome", got, commits+1)
	}
	if writes := store.lastCommitTaskWrites(); len(writes) != 1 || writes[0].id != id || !writes[0].abortRequested || writes[0].status != "pending" {
		t.Fatalf("last commit's task writes %+v, want the abort-marked task %d pending", writes, id)
	}
	mu.Lock()
	if !reflect.DeepEqual(phases, []string{"one"}) {
		t.Fatalf("phases %v, want [one]", phases)
	}
	mu.Unlock()
	if abortRan.Load() {
		t.Fatal("the abort handler ran while closing")
	}
	if record.State.Status != durable.TaskRunning || record.State.Checkpoint == nil {
		t.Fatalf("record %+v", record)
	}
	if _, err := h.Commit(t.Context(), func(durable.Tx) (any, error) { return nil, nil }); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("a commit after close = %v", err)
	}
}

// harness-tasks.test.ts:1243 "writes no fault when close seals while the step after a failed phase is queued".
func TestCoreFaultWritesNoFaultWhenCloseSealsWhileTheStepAfterAFailedPhaseIsQueued(t *testing.T) {
	store := &controlledStore{}
	var held atomic.Pointer[commitGate]
	var harnessRef atomic.Pointer[harness.Harness]
	queuedAtReturn := make(chan int, 1)
	throws := defineSteps("test.close-fault", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			gate := store.holdCommits()
			held.Store(gate)
			go func() {
				_, _ = (*harnessRef.Load()).Commit(context.Background(), func(tx durable.Tx) (any, error) {
					_, err := tx.AppendEntry(rt.ConversationId(), durable.EntryDraft{Kind: "blocker"})
					return nil, err
				})
			}()
			// The blocker is on the line before the phase's failure is: upstream's `void commit()` enqueues synchronously, and a
			// goroutine does not, so wait until the blocker is in the store.
			select {
			case <-gate.entered:
			case <-time.After(15 * time.Second):
			}
			queuedAtReturn <- driver.QueuedPosts(*harnessRef.Load())
			return errors.New("would fault")
		},
	})
	h, root := openFault(t, store, harness.HarnessOptions{}, throws.Erased())
	harnessRef.Store(&h)
	id := startTask(t, root, throws.Erased())
	h.Resume()
	var gate *commitGate
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && gate == nil; time.Sleep(5 * time.Millisecond) {
		gate = held.Load()
	}
	if gate == nil {
		t.Fatal("the phase never held the store")
	}
	gate.waitEntered(t)
	// The failed phase's completion is in the owner mailbox before close() runs, as Pi's microtask has queued the fault step.
	waitQueuedAbove(t, h, awaitChan(t, "the handler to return", queuedAtReturn))
	closing := closeBegun(t, h)
	gate.release()
	select {
	case err := <-closing:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Close never returned")
	}
	// harness-tasks.test.ts:1222: created, reserved, and written back as pending by close: no fault.
	if got, want := statuses(store.taskWrites(id)), []string{"pending", "running", "pending"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("task writes %v, want %v: close wrote a fault", got, want)
	}
}

// closeWith closes the Harness while the store holds a commit, releases it and waits for the close.
func closeWith(t *testing.T, h harness.Harness, gate *commitGate) {
	t.Helper()
	closing := closeBegun(t, h)
	gate.release()
	select {
	case err := <-closing:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Close never returned")
	}
}

// harness-tasks.test.ts:1273 "starts no abort handler whose reservation settles while closing".
func TestCoreFaultStartsNoAbortHandlerWhoseReservationSettlesWhileClosing(t *testing.T) {
	var ran atomic.Bool
	marked := defineSteps("test.close-abort-reservation", func(context.Context, stepRuntime) error { ran.Store(true); return nil },
		map[string]func(context.Context, stepRecord, stepRuntime) error{"start": func(context.Context, stepRecord, stepRuntime) error { return nil }})
	store := &controlledStore{}
	h, root := openFault(t, store, harness.HarnessOptions{}, marked.Erased())
	id := startTask(t, root, marked.Erased())
	if result, err := h.AbortTask(t.Context(), id); err != nil || result != "marked" {
		t.Fatalf("AbortTask %q %v", result, err)
	}
	gate := store.holdCommits()
	h.Resume()
	gate.waitEntered(t)
	closeWith(t, h, gate)
	if ran.Load() {
		t.Fatal("the abort handler started while closing")
	}
	// harness-tasks.test.ts:1250: the reservation landed; close wrote the task back as pending.
	reserved := store.commitTaskWrites(1)
	if len(reserved) != 1 || reserved[0].id != id || !reserved[0].abortRequested || reserved[0].status != "running" {
		t.Fatalf("reservation commit's task writes %+v, want one running write of task %d with the abort mark", reserved, id)
	}
	writes := store.lastCommitTaskWrites()
	if len(writes) != 1 || writes[0].id != id || !writes[0].abortRequested || writes[0].status != "pending" {
		t.Fatalf("last commit's task writes %+v, want one pending write of task %d with the abort mark", writes, id)
	}
}

// harness-tasks.test.ts:1374 "joins a reservation that settles while closing without starting its handler".
func TestCoreFaultJoinsAReservationThatSettlesWhileClosingWithoutStartingItsHandler(t *testing.T) {
	var ran atomic.Bool
	never := oneStepTask("test.close-reservation", func(context.Context, stepRuntime) error { ran.Store(true); return nil })
	store := &controlledStore{}
	h, root := openFault(t, store, harness.HarnessOptions{}, never.Erased())
	id := startTask(t, root, never.Erased())
	gate := store.holdCommits()
	h.Resume()
	gate.waitEntered(t)
	closeWith(t, h, gate)
	if ran.Load() {
		t.Fatal("the handler started while closing")
	}
	// harness-tasks.test.ts:1355: the reservation landed; close wrote the task back as pending.
	reserved := store.commitTaskWrites(1)
	if len(reserved) != 1 || reserved[0].id != id || reserved[0].status != "running" {
		t.Fatalf("reservation commit's task writes %+v, want one running write of task %d", reserved, id)
	}
	writes := store.lastCommitTaskWrites()
	if len(writes) != 1 || writes[0].id != id || writes[0].status != "pending" {
		t.Fatalf("last commit's task writes %+v, want one pending write of task %d", writes, id)
	}
}

// settledAborted checks the outcome a "test" abort leaves.
func settledAborted(t *testing.T, h harness.Harness, id durable.TaskId) {
	t.Helper()
	settled := waitSettled(t, h, id)
	if o := settled.State.Outcome; o == nil || o.Status != durable.OutcomeAborted || o.Reason == nil || *o.Reason != "test" {
		t.Fatalf("outcome = %+v, want aborted: test", settled.State.Outcome)
	}
}

// abortQueued starts AbortTask and returns once its request is in the owner mailbox behind the commit the store is holding, as
// Pi's abortTask enqueues synchronously.
func abortQueued(t *testing.T, h harness.Harness, id durable.TaskId) <-chan string {
	t.Helper()
	marking := make(chan string, 1)
	queuedBy(t, h, func() {
		go func() {
			result, err := h.AbortTask(context.Background(), id)
			if err != nil {
				result = "error: " + err.Error()
			}
			marking <- result
		}()
	})
	return marking
}

// harness-tasks.test.ts:1115 "never starts phase one when the abort lands while the reservation settles": the abort mark
// commits behind the reservation, so the invocation that starts sees the mark before phase one.
func TestCoreFaultNeverStartsPhaseOneWhenTheAbortLandsWhileTheReservationSettles(t *testing.T) {
	var ran atomic.Bool
	reserved := defineSteps("test.reserved", abortingWith("test"), map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, _ stepRuntime) error {
			ran.Store(true)
			<-ctx.Done()
			return nil
		},
	})
	store := &controlledStore{}
	h, root := openFault(t, store, harness.HarnessOptions{}, reserved.Erased())
	id := startTask(t, root, reserved.Erased())
	gate := store.holdCommits()
	h.Resume()
	gate.waitEntered(t)
	marking := abortQueued(t, h, id)
	gate.release()
	select {
	case got := <-marking:
		if got != "marked" {
			t.Fatalf("AbortTask = %q, want marked", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("AbortTask never returned")
	}
	settledAborted(t, h, id)
	if ran.Load() {
		t.Fatal("phase one started although the abort landed while the reservation settled")
	}
}

// harness-tasks.test.ts:1136 "lets an abort mark win over a fault that races it": the mark is in the store when the handler
// throws, so the fault commit queues behind it.
func TestCoreFaultLetsAnAbortMarkWinOverAFaultThatRacesIt(t *testing.T) {
	store := &controlledStore{}
	var h harness.Harness
	marking := make(chan (<-chan string), 1)
	gateOf := make(chan *commitGate, 1)
	racing := defineSteps("test.racing-fault", abortingWith("test"), map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, rec stepRecord, _ stepRuntime) error {
			gate := store.holdCommits()
			gateOf <- gate
			go func() {
				result, err := h.AbortTask(context.Background(), rec.Id)
				if err != nil {
					result = "error: " + err.Error()
				}
				ch := make(chan string, 1)
				ch <- result
				marking <- ch
			}()
			select {
			case <-gate.entered:
			case <-time.After(15 * time.Second):
				return errors.New("the abort mark never reached the store")
			}
			return errors.New("would fault")
		},
	})
	var root harness.Conversation
	h, root = openFault(t, store, harness.HarnessOptions{}, racing.Erased())
	id := startTask(t, root, racing.Erased())
	h.Resume()
	var gate *commitGate
	select {
	case gate = <-gateOf:
	case <-time.After(15 * time.Second):
		t.Fatal("the handler never ran")
	}
	gate.waitEntered(t)
	gate.release()
	settledAborted(t, h, id)
	select {
	case ch := <-marking:
		if got := <-ch; got != "marked" {
			t.Fatalf("AbortTask = %q, want marked", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("AbortTask never returned")
	}
}

// blockerCommit queues, from the handler, a Harness commit that appends an entry: the commit the held store keeps in flight.
// It returns once the blocker is in the store, so a commit queued next lines up behind it as in Pi, where the call order is the line order.
func blockerCommit(h harness.Harness, rt stepRuntime, gate *commitGate) {
	defer func() {
		select {
		case <-gate.entered:
		case <-time.After(15 * time.Second):
		}
	}()
	go func() {
		_, _ = h.Commit(context.Background(), func(tx durable.Tx) (any, error) {
			_, err := tx.AppendEntry(rt.ConversationId(), durable.EntryDraft{Kind: "blocker"})
			return nil, err
		})
	}()
}

// harness-tasks.test.ts:622 "orders runtime commits against the step: one queued before it lands, one after it rejects": the
// handler holds the line, queues a commit without awaiting it and returns, so the step queues behind that commit; a commit
// queued after the handler returned lands after the step, which has ended the invocation.
func TestCoreFaultOrdersRuntimeCommitsAgainstTheStep(t *testing.T) {
	store := &controlledStore{}
	var h harness.Harness
	gateOf := make(chan *commitGate, 1)
	runtimeOf := make(chan stepRuntime, 1)
	queuedAtReturn := make(chan int, 1)
	before := make(chan error, 1)
	detached := oneStepTask("test.detached", func(_ context.Context, rt stepRuntime) error {
		gate := store.holdCommits()
		gateOf <- gate
		runtimeOf <- rt
		blockerCommit(h, rt, gate)
		n := driver.QueuedPosts(h)
		go func() {
			before <- rt.Commit(context.Background(), func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("before"), nil })
		}()
		if !queuedAbove(h, n) {
			return errors.New("the commit queued before the step never reached the owner")
		}
		queuedAtReturn <- driver.QueuedPosts(h)
		return nil
	})
	var root harness.Conversation
	h, root = openFault(t, store, harness.HarnessOptions{}, detached.Erased())
	id := startTask(t, root, detached.Erased())
	h.Resume()
	gate := awaitChan(t, "the handler to hold the line", gateOf)
	rt := awaitChan(t, "the runtime", runtimeOf)
	gate.waitEntered(t)
	// The step's completion is in the owner mailbox behind the commit queued before it; the commit queued after lands behind both.
	waitQueuedAbove(t, h, awaitChan(t, "the handler to return", queuedAtReturn))
	after := make(chan error, 1)
	queuedBy(t, h, func() {
		go func() {
			after <- rt.Commit(context.Background(), func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("after"), nil })
		}()
	})
	gate.release()
	if err := awaitChan(t, "the commit queued before the step", before); err != nil {
		t.Fatalf("the commit queued before the step = %v", err)
	}
	settled := waitSettled(t, h, id)
	if o := settled.State.Outcome; o == nil || o.Status != durable.OutcomeCompleted || o.Result == nil || *o.Result != "before" {
		t.Fatalf("outcome = %+v, want completed: before", settled.State.Outcome)
	}
	if err := awaitChan(t, "the commit queued after the step", after); err == nil || (!strings.Contains(err.Error(), "invocation has ended") && !strings.Contains(err.Error(), "is terminal")) {
		t.Fatalf("the commit queued after the step = %v, want an ended invocation or terminal task", err)
	}
}

// harness-tasks.test.ts:1297 "rejects a runtime commit that was queued on the line when close sealed it".
func TestCoreFaultRejectsARuntimeCommitQueuedOnTheLineWhenCloseSealedIt(t *testing.T) {
	store := &controlledStore{}
	var h harness.Harness
	gateOf := make(chan *commitGate, 1)
	queued := make(chan error, 1)
	onTheLine := make(chan bool, 1)
	proceed := make(chan struct{})
	task := oneStepTask("test.close-queued-commit", func(_ context.Context, rt stepRuntime) error {
		gate := store.holdCommits()
		gateOf <- gate
		blockerCommit(h, rt, gate)
		n := driver.QueuedPosts(h)
		go func() {
			queued <- rt.Commit(context.Background(), func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		}()
		onTheLine <- queuedAbove(h, n)
		// Ignores the close signal, so the invocation is still alive when the queued commit reaches the line.
		<-proceed
		return nil
	})
	var root harness.Conversation
	h, root = openFault(t, store, harness.HarnessOptions{}, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	gate := awaitChan(t, "the handler to hold the line", gateOf)
	gate.waitEntered(t)
	if !awaitChan(t, "the runtime commit to queue", onTheLine) {
		t.Fatal("the runtime commit never reached the owner mailbox")
	}
	closing := closeBegun(t, h)
	gate.release()
	if err := awaitChan(t, "the queued runtime commit", queued); err == nil || !strings.Contains(err.Error(), "Harness is closed") {
		t.Fatalf("the queued runtime commit = %v, want Harness is closed", err)
	}
	close(proceed)
	if err := awaitChan(t, "close", closing); err != nil {
		t.Fatal(err)
	}
	// harness-tasks.test.ts:1288: created, reserved, and written back as pending by close; the queued outcome never landed.
	if got, want := statuses(store.taskWrites(id)), []string{"pending", "running", "pending"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("task writes %v, want %v", got, want)
	}
}
