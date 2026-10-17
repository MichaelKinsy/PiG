package ai

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A drain on one goroutine grants a coroutine turn that a serving goroutine claims (needsDriver), then ends. The claimed body pauses while that drain has not ended: its settle must not write the drain's flag back after the drain ended, or the executor is left draining with no loop on any stack, and the jobs queued behind it never run.
func TestContinuationSettleDoesNotResurrectEndedDrain(t *testing.T) {
	const wait = 20 * time.Second
	executor := &continuationExecutor{}
	gate := newContinuationPromise[int](executor)
	never := newContinuationPromise[int](executor)

	var armed, pauses atomic.Int32
	var releaseOnce sync.Once
	heldByDrain := make(chan struct{})
	releaseDrain := make(chan struct{})
	settleRan := make(chan struct{})
	drainDone := make(chan struct{})
	paused := make(chan struct{})
	release := func() { releaseOnce.Do(func() { close(releaseDrain) }) }
	executor.trace = &executorTrace{
		pause: func() {
			if armed.Load() != 0 && pauses.Add(1) == 1 {
				close(paused)
			}
		},
		afterReaction: func(settling bool) {
			if armed.Load() == 0 {
				return
			}
			switch {
			case settling:
				// The claimed body runs a reaction on its own stack: the drain that granted it ends meanwhile.
				close(settleRan)
				<-drainDone
			case armed.CompareAndSwap(1, 2):
				// The drain's own stack, right after the grant that handed execution to the body.
				close(heldByDrain)
				<-releaseDrain
			}
		},
	}

	bodyDone := make(chan struct{})
	go func() {
		defer close(bodyDone)
		executor.run(func(turn *continuationTurn) {
			awaitContinuation(turn, gate)
			executor.post(func() {})
			awaitContinuation(turn, never)
		})
	}()
	awaitExecutorState(t, executor, wait, "the body to park on its gate", func() bool {
		return len(executor.drivers) == 1 && executor.active == nil && !executor.draining
	})

	armed.Store(1)
	go func() {
		defer close(drainDone)
		gate.resolve(1)
	}()
	select {
	case <-heldByDrain:
	case <-time.After(wait):
		release()
		t.Fatal("the drain did not grant the body")
	}
	select {
	case <-settleRan:
	case <-paused:
	case <-time.After(wait):
		release()
		t.Fatal("the claimed body neither settled nor paused")
	}
	release()
	select {
	case <-drainDone:
	case <-time.After(wait):
		t.Fatal("the drain did not end")
	}
	awaitExecutorState(t, executor, wait, "every goroutine to park", func() bool {
		return len(executor.drivers) == 1 && executor.active == nil && !executor.settling
	})

	executor.mu.Lock()
	stranded := executor.draining
	if stranded {
		// Unstick it, so the body can finish and the test not leak its goroutine.
		executor.draining, executor.drainer = false, nil
	}
	executor.mu.Unlock()
	if stranded {
		t.Error("the executor is draining with no drain running: a reaction posted now never runs")
	}
	probe := false
	executor.post(func() { probe = true })
	if !probe && !stranded {
		t.Error("a reaction posted to the parked executor did not run")
	}
	never.resolve(1)
	select {
	case <-bodyDone:
	case <-time.After(wait):
		t.Fatal("the body did not finish")
	}
}

// awaitExecutorState polls ready under the executor's lock until it holds.
func awaitExecutorState(t *testing.T, executor *continuationExecutor, wait time.Duration, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		executor.mu.Lock()
		ok := ready()
		executor.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A coroutine turn a serving goroutine claims while another turn's settle loop is live (the grant ran in that loop, which skips the wake: settling) must not run a second reaction loop of its own beside it: its pause hands the queue to the loop that is live, or to its driver.
func TestContinuationSettleDoesNotRunSecondLoopForTurnGrantedFromSettle(t *testing.T) {
	const wait = 20 * time.Second
	executor := &continuationExecutor{}
	gateA := newContinuationPromise[int](executor)
	neverA := newContinuationPromise[int](executor)
	neverT := newContinuationPromise[int](executor)

	var turnA *continuationTurn
	aParked := make(chan struct{})
	var armed, pauses, inLoop, overlaps, settleCalls, tRan atomic.Int32
	var pausedOnce, loopOnce sync.Once
	pausedA := make(chan struct{})
	loopA := make(chan struct{})
	executor.trace = &executorTrace{
		pause: func() {
			if armed.Load() != 0 && pauses.Add(1) == 1 {
				pausedOnce.Do(func() { close(pausedA) })
			}
		},
		afterReaction: func(settling bool) {
			if !settling || armed.Load() == 0 {
				return
			}
			if inLoop.Add(1) > 1 {
				overlaps.Add(1)
				loopOnce.Do(func() { close(loopA) })
			}
			defer inLoop.Add(-1)
			if settleCalls.Add(1) != 1 {
				return
			}
			// The settle of T has run a reaction: its loop is live (draining, no drainer) and its unlocked window is open. The grant that loop ran gives A execution; a goroutine that is awake, not asleep in serve, claims it.
			turnA.grant()
			executor.mu.Lock()
			executor.wakeLocked(turnA)
			executor.mu.Unlock()
			select {
			case <-pausedA:
			case <-loopA:
			case <-time.After(wait):
				t.Error("the granted turn neither paused nor ran a loop")
			}
		},
	}

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		executor.run(func(turn *continuationTurn) {
			turnA = turn
			close(aParked)
			awaitContinuation(turn, gateA)
			executor.post(func() {})
			awaitContinuation(turn, neverA)
		})
	}()
	<-aParked
	awaitExecutorState(t, executor, wait, "A to park", func() bool {
		return len(executor.drivers) == 1 && executor.active == nil && !executor.draining
	})

	armed.Store(1)
	tDone := make(chan struct{})
	go func() {
		defer close(tDone)
		executor.run(func(turn *continuationTurn) {
			executor.post(func() { tRan.Store(1) })
			awaitContinuation(turn, neverT)
		})
	}()
	// T's reaction runs either in the settle loop of A's grant or in a loop of T's own, depending on which goroutine
	// the scheduler wakes first, so the wait asks that it ran, not where: settleCalls >= 2 held only for the first order.
	awaitExecutorState(t, executor, wait, "every goroutine to park", func() bool {
		return settleCalls.Load() >= 1 && tRan.Load() == 1 && len(executor.drivers) == 2 && executor.active == nil && !executor.settling
	})

	if overlaps.Load() != 0 {
		t.Error("two turns ran reaction loops at once")
	}
	executor.mu.Lock()
	stranded := executor.draining
	if stranded {
		executor.draining, executor.drainer = false, nil
	}
	executor.mu.Unlock()
	if stranded {
		t.Error("the executor is draining with no drain running")
	}
	neverA.resolve(1)
	neverT.resolve(1)
	for _, done := range []chan struct{}{aDone, tDone} {
		select {
		case <-done:
		case <-time.After(wait):
			t.Fatal("a body did not finish")
		}
	}
}
