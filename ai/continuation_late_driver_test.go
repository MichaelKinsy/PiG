package ai

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A coroutine body that paused hands execution back to its driver, but the driver takes the executor's lock again only some time after the switch, and any other goroutine may run a turn in that time. The turn that runs meanwhile resumes in place a drain that has no loop on any stack (frameless): that drain belongs to the driver of the turn that holds it, and the late driver of another turn must not continue it. If it does, the other turn's body runs beside a reaction that has not returned, and the settle that owns the drain later writes its flags back over the drain the late driver ended, so the executor stays draining with no loop on any stack and the reactions queued behind it never run.
func TestContinuationLateDriverDoesNotContinueAnotherTurnsDrain(t *testing.T) {
	const wait = 20 * time.Second
	executor := &continuationExecutor{}
	gate := newContinuationPromise[int](executor)
	finished := newContinuationPromise[int](executor)

	var pauses atomic.Int32
	var reactionRunning, overlap atomic.Bool
	oPaused := make(chan struct{})
	releaseO := make(chan struct{})
	inReaction := make(chan struct{})
	releaseReaction := make(chan struct{})
	var releaseOnce, releaseOOnce sync.Once
	releaseDriver := func() { releaseOOnce.Do(func() { close(releaseO) }) }
	release := func() { releaseOnce.Do(func() { close(releaseReaction) }) }
	t.Cleanup(func() {
		releaseDriver()
		release()
	})
	executor.trace = &executorTrace{
		pause: func() {
			// The first body to pause is the one that must stay between its switch decision and its driver for as long as the other turn needs.
			if pauses.Add(1) == 1 {
				close(oPaused)
				<-releaseO
			}
		},
	}

	oDone := make(chan struct{})
	go func() {
		defer close(oDone)
		executor.run(func(turn *continuationTurn) {
			awaitContinuation(turn, gate)
			if reactionRunning.Load() {
				overlap.Store(true)
			}
			finished.resolve(1)
		})
	}()
	<-oPaused

	rDone := make(chan struct{})
	go func() {
		defer close(rDone)
		executor.run(func(turn *continuationTurn) {
			// This pause's drain resumes the body in place, and the body holds it from then on.
			step := newContinuationPromise[int](executor)
			executor.post(func() { step.resolve(1) })
			awaitContinuation(turn, step)
			// The body now pauses with a reaction that does not return until the test lets it, and the other turn's grant queued behind it.
			executor.post(func() {
				reactionRunning.Store(true)
				close(inReaction)
				<-releaseReaction
				reactionRunning.Store(false)
			})
			gate.resolve(1)
			awaitContinuation(turn, finished)
		})
	}()
	<-inReaction

	// The first turn's driver now takes the lock again while the second turn's reaction is running.
	releaseDriver()
	awaitExecutorState(t, executor, wait, "the first turn's driver to park or its body to run", func() bool {
		return overlap.Load() || len(executor.drivers) == 1
	})
	if overlap.Load() {
		release()
		t.Fatal("a turn's body ran while another turn's reaction had not returned: the late driver continued a drain that belongs to the other turn's driver")
	}
	release()

	for name, done := range map[string]chan struct{}{"first": oDone, "second": rDone} {
		select {
		case <-done:
		case <-time.After(wait):
			t.Fatalf("the %s turn did not finish: the executor is left draining with no drain running", name)
		}
	}
	executor.mu.Lock()
	stranded := executor.draining
	executor.mu.Unlock()
	if stranded {
		t.Error("the executor is draining with no drain running: a reaction posted now never runs")
	}
}
