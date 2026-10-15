// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// More of harness-tasks.test.ts through driver.OpenHarness over the real core.

func openTasksWith(t *testing.T, options harness.HarnessOptions, tasks ...durable.AnyTask) (harness.Harness, harness.Conversation) {
	t.Helper()
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: tasks}); err != nil {
		t.Fatal(err)
	}
	options.Models, options.Registry = ai.CreateModels(), registry
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, root
}

func quickTask(name string) durable.Task[durable.JsonValue, stepState, durable.JsonValue, any] {
	return defineSteps(name, nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
}

// harness-tasks.test.ts:856 "rejects unknown tasks and reports terminal tasks".
func TestCoreTaskRejectsUnknownTasksAndReportsTerminalOnes(t *testing.T) {
	done := quickTask("test.quick")
	h, root := openTasksHarness(t, done.Erased())
	const unknown durable.TaskId = 999_999
	if got, err := h.GetTask(t.Context(), unknown); err != nil || got != nil {
		t.Fatalf("getTask(unknown) = %v, %v", got, err)
	}
	if _, err := h.WaitForTask(t.Context(), unknown); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("waitForTask(unknown) = %v", err)
	}
	if _, err := h.AbortTask(t.Context(), unknown); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("abortTask(unknown) = %v", err)
	}
	id := startTask(t, root, done.Erased())
	h.Resume()
	waitSettled(t, h, id)
	if settled := waitSettled(t, h, id); settled.State.Status != durable.TaskTerminal {
		t.Fatalf("a second waitForTask = %+v", settled.State)
	}
	if result, err := h.AbortTask(t.Context(), id); err != nil || result != "terminal" {
		t.Fatalf("abortTask of a terminal task = %q, %v", result, err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if text := panicText(h.Resume); !strings.Contains(text, "closed") {
		t.Fatalf("resume after close panicked with %q", text)
	}
}

// harness-tasks.test.ts:310 "stamps startedAt at the first run and endedAt when terminal, keeping startedAt through a wait".
func TestCoreTaskStampsStartedAtAndEndedAtKeepingStartedAtThroughAWait(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1_000)
	gate := make(chan struct{})
	var on []durable.TaskId
	waiter := defineSteps("test.timed-waiter", abortingWith("test"), map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) {
				return &stepNext{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "resume"}, On: on, Policy: durable.JoinAllSettled}, nil
			})
		},
		"resume": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	held := defineSteps("test.timed-held", abortingWith("test"), map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	h, root := openTasksWith(t, harness.HarnessOptions{Now: func() float64 { return float64(clock.Load()) }}, waiter.Erased(), held.Erased())
	heldId := startTask(t, root, held.Erased())
	on = []durable.TaskId{heldId}
	waiterId := startTask(t, root, waiter.Erased())
	times := func(id durable.TaskId) (*float64, *float64) {
		t.Helper()
		record, err := h.GetTask(t.Context(), id)
		if err != nil || record == nil {
			t.Fatalf("getTask(%d) = %v, %v", id, record, err)
		}
		return record.StartedAt, record.EndedAt
	}
	same := func(what string, got *float64, want *float64) {
		t.Helper()
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Fatalf("%s = %v, want %v", what, ptrText(got), ptrText(want))
		}
	}
	at := func(v float64) *float64 { return &v }
	for _, id := range []durable.TaskId{heldId, waiterId} {
		started, ended := times(id)
		same("startedAt before running", started, nil)
		same("endedAt before running", ended, nil)
	}
	clock.Store(2_000)
	h.Resume()
	waitFor(t, "the waiter to wait", func() bool {
		record, _ := h.GetTask(t.Context(), waiterId)
		return record != nil && record.State.Status == durable.TaskWaiting
	})
	for _, id := range []durable.TaskId{heldId, waiterId} {
		started, ended := times(id)
		same("startedAt once running", started, at(2_000))
		same("endedAt while live", ended, nil)
	}
	clock.Store(5_000)
	close(gate)
	waitSettled(t, h, waiterId)
	for name, id := range map[string]durable.TaskId{"held": heldId, "waiter": waiterId} {
		started, ended := times(id)
		same(name+" startedAt", started, at(2_000))
		same(name+" endedAt", ended, at(5_000))
	}
}

func ptrText(v *float64) any {
	if v == nil {
		return "nil"
	}
	return *v
}

// harness-tasks.test.ts:257 "resumes a waiting task once every task in `on` is terminal, whatever the outcome".
func TestCoreTaskResumesAWaitingTaskOnceEveryTaskInOnIsTerminalWhateverTheOutcome(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	var order, outcomes []string
	note := func(s string) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}
	var on []durable.TaskId
	waiter := defineSteps("test.waiter", abortingWith("test"), map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			note("wait")
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) {
				return &stepNext{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "resume"}, On: on, Policy: durable.JoinAllSettled}, nil
			})
		},
		"resume": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			note("resume")
			got, err := rt.Outcomes(ctx, on)
			if err != nil {
				return err
			}
			mu.Lock()
			for _, outcome := range got {
				outcomes = append(outcomes, string(outcome.Status))
			}
			mu.Unlock()
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	first := defineSteps("test.first", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			note("first")
			<-gate
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	faulting := defineSteps("test.faulting", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(context.Context, stepRecord, stepRuntime) error {
			note("faulting")
			return errors.New("fails")
		},
	})
	h, root := openTasksHarness(t, first.Erased(), faulting.Erased(), waiter.Erased())
	firstId, faultingId := startTask(t, root, first.Erased()), startTask(t, root, faulting.Erased())
	on = []durable.TaskId{firstId, faultingId}
	waiterId := startTask(t, root, waiter.Erased())
	h.Resume()
	waitFor(t, "first, faulting and wait to have run", func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 3 })
	// The waiter stays parked while one task in `on` is live.
	waitFor(t, "the waiter to wait", func() bool {
		record, _ := h.GetTask(t.Context(), waiterId)
		return record != nil && record.State.Status == durable.TaskWaiting
	})
	mu.Lock()
	ran := slices.Sorted(slices.Values(order))
	mu.Unlock()
	if fmt.Sprint(ran) != "[faulting first wait]" {
		t.Fatalf("before the gate: %v", ran)
	}
	record, _ := h.GetTask(t.Context(), waiterId)
	if fmt.Sprint(record.State.On) != fmt.Sprint(on) {
		t.Fatalf("the waiting task waits on %v, want %v", record.State.On, on)
	}
	close(gate)
	waitSettled(t, h, waiterId)
	mu.Lock()
	defer mu.Unlock()
	if order[len(order)-1] != "resume" || fmt.Sprint(outcomes) != "[completed faulted]" {
		t.Fatalf("order %v, outcomes %v", order, outcomes)
	}
}

// harness-tasks.test.ts:1095 "keeps a terminal outcome committed by an abort handler that throws afterwards" (#10549).
func TestCoreTaskKeepsATerminalOutcomeCommittedByAnAbortHandlerThatThrowsAfterwards(t *testing.T) {
	reached := make(chan struct{})
	task := defineSteps("test.abort-then-throw", func(ctx context.Context, rt stepRuntime) error {
		if err := rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return abortedNext("done"), nil }); err != nil {
			return err
		}
		return errors.New("after terminal")
	}, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(_ context.Context, _ stepRecord, rt stepRuntime) error {
			close(reached)
			<-rt.Signal().Done()
			return nil
		},
	})
	h, root := openTasksHarness(t, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	<-reached
	if _, err := h.AbortTask(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	outcome := waitSettled(t, h, id).State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeAborted || outcome.Reason == nil || *outcome.Reason != "done" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// harness-tasks.test.ts:1055 "does not signal a running abort handler when aborted again, and a cancelled caller leaves the mark
// durable".
func TestCoreTaskDoesNotSignalARunningAbortHandlerWhenAbortedAgainAndACancelledCallerLeavesTheMarkDurable(t *testing.T) {
	started := make(chan struct{})
	reached := make(chan struct{})
	proceed := make(chan struct{})
	runRelease := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runRelease) }) }
	t.Cleanup(release) // a failed assertion must not leave the Harness's Close waiting on the run
	var abortSignalled atomic.Bool
	var aborts atomic.Int32
	task := defineSteps("test.abort-again", func(ctx context.Context, rt stepRuntime) error {
		aborts.Add(1)
		close(reached)
		<-proceed
		abortSignalled.Store(rt.Signal().Err() != nil)
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return abortedNext("once"), nil })
	}, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(context.Context, stepRecord, stepRuntime) error {
			close(started)
			<-runRelease // ignores the signal, so the first caller is still joining when it gives up
			return nil
		},
	})
	h, root := openTasksHarness(t, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	<-started
	// This caller gives up while joining; the mark and the abort invocation are unaffected.
	callerCtx, giveUp := context.WithCancelCause(t.Context())
	cancelled := make(chan error, 1)
	go func() {
		_, err := h.AbortTask(callerCtx, id)
		cancelled <- err
	}()
	waitFor(t, "the mark to be durable", func() bool {
		record, _ := h.GetTask(t.Context(), id)
		return record != nil && record.AbortRequested
	})
	giveUp(errors.New("caller gave up"))
	if err := awaitChan(t, "the cancelled abortTask", cancelled); err == nil || !strings.Contains(err.Error(), "caller gave up") {
		t.Fatalf("the cancelled caller's abortTask = %v", err)
	}
	release()
	awaitChan(t, "the abort handler to start", reached)
	if result, err := h.AbortTask(t.Context(), id); err != nil || result != "marked" {
		t.Fatalf("a second abortTask = %q, %v", result, err)
	}
	close(proceed)
	outcome := waitSettled(t, h, id).State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeAborted || outcome.Reason == nil || *outcome.Reason != "once" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if abortSignalled.Load() {
		t.Fatal("the running abort handler was signalled by a second abort")
	}
	if aborts.Load() != 1 {
		t.Fatalf("abort handlers started = %d, want 1", aborts.Load())
	}
}

// harness-tasks.test.ts:874 "rejects task waits cancelled or closed while queued on the line, and pending waits on close". The
// part of the Pi test that holds Storage commits so that waits queue on the line is not portable (the native Session has no
// replaceable Storage); this keeps the cancelled wait's reason and the pending waits rejected as closed.
func TestCoreTaskRejectsACancelledWaitWithItsReasonAndPendingWaitsOnClose(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)
	blocking := defineSteps("test.wait-close", abortingWith("test"), map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, _ stepRuntime) error {
			close(started)
			<-gate
			return ctx.Err()
		},
	})
	h, root := openTasksHarness(t, blocking.Erased())
	id := startTask(t, root, blocking.Erased())
	h.Resume()
	awaitChan(t, "the task to start", started)

	waitCtx, cancelWait := context.WithCancelCause(t.Context())
	cancelled := make(chan error, 1)
	go func() {
		_, err := h.WaitForTask(waitCtx, id)
		cancelled <- err
	}()
	cancelWait(errors.New("wait cancelled"))
	if err := awaitChan(t, "the cancelled wait", cancelled); err == nil || !strings.Contains(err.Error(), "wait cancelled") {
		t.Fatalf("a cancelled waitForTask = %v", err)
	}

	pending := make(map[string]chan error)
	for name, wait := range map[string]func() error{
		"waitForTask":              func() error { _, err := h.WaitForTask(t.Context(), id); return err },
		"waitForIdle":              func() error { return h.WaitForIdle(t.Context()) },
		"conversation.waitForIdle": func() error { return root.WaitForIdle(t.Context()) },
	} {
		done := make(chan error, 1)
		pending[name] = done
		go func() { done <- wait() }()
	}
	// A wait that registers before Close is rejected while pending, one that registers after it at the call: both as closed.
	closing := make(chan error, 1)
	go func() { closing <- h.Close(context.Background()) }()
	for name, done := range pending {
		if err := awaitChan(t, name, done); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("%s pending at close = %v", name, err)
		}
	}
	release()
	if err := awaitChan(t, "close", closing); err != nil {
		t.Fatal(err)
	}
	// After close the calls that ask for progress reject as closed; they do not panic as Harness.Resume does (harness-tasks.test.ts:909).
	for name, call := range map[string]func() error{
		"waitForIdle":              func() error { return h.WaitForIdle(t.Context()) },
		"conversation.waitForIdle": func() error { return root.WaitForIdle(t.Context()) },
		"waitForTask":              func() error { _, err := h.WaitForTask(t.Context(), id); return err },
		"abortTask":                func() error { _, err := h.AbortTask(t.Context(), id); return err },
		"conversation.abort":       func() error { return root.Abort(t.Context(), nil) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("%s after close = %v", name, err)
		}
	}
}
