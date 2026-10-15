// SPDX-License-Identifier: MIT

package codingagent

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// idleCollectDeadline bounds how long after a run ends the idle collection may
// wait: Pi-Bolt's 500 ms (idle-gc.ts IDLE_GC_DELAY_MS, 4ee61e8a), plus one
// 80 ms spinner tick and margin.
const idleCollectDeadline = 700 * time.Millisecond

// idleCollectEarliest is the least idle time before the collection: it must
// not run while the user may still be reading the run's last frame.
const idleCollectEarliest = 400 * time.Millisecond

// drainOwnerLoop runs the owner loop's queued tasks and scheduled renders for
// d of fake time, as the interactive input loop does.
func drainOwnerLoop(m *InteractiveMode, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		for {
			select {
			case fn := <-m.uiTaskCh:
				fn()
				continue
			case <-m.renderWakeCh:
				m.runScheduledRender()
				continue
			default:
			}
			break
		}
	}
}

// startIdleCollectHarness starts a lifecycle mode whose renders go through the
// owner loop, with its spinner ticker parked idle and an idle collector that
// sees heap growth on its next run.
func startIdleCollectHarness(t *testing.T) (*InteractiveMode, context.Context) {
	t.Helper()
	m, ctx, _ := newLifecycleMode(t)
	m.installRenderDispatcher()
	m.isIdle = true
	m.idleCollector.lastHeap = 0
	spinCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.tickSpinner(spinCtx) }()
	t.Cleanup(func() { cancel(); <-done })
	drainOwnerLoop(m, 200*time.Millisecond) // the first tick parks the ticker
	return m, ctx
}

// runUntilIdle starts a run that delivers agent_start, then after busy of
// owner-loop time delivers agent_end and returns; it drains the owner loop
// until the run's cleanup marks the session idle and returns when that
// happened.
func runUntilIdle(t *testing.T, m *InteractiveMode, ctx context.Context, busy time.Duration) time.Time {
	t.Helper()
	release := make(chan struct{})
	m.runTurn(ctx, "", func(runCtx context.Context) ([]agent.AgentMessage, error) {
		m.runOnMain(runCtx, func() { m.handleAgentEvent(agent.AgentStartEvent{}) })
		<-release
		m.runOnMain(runCtx, func() { m.handleAgentEvent(agent.AgentEndEvent{}) })
		return nil, nil
	})
	if busy > 0 {
		drainOwnerLoop(m, busy)
	}
	idleCollectSink = append(idleCollectSink, make([]byte, 1<<20)) // the run's retained growth
	close(release)
	for start := time.Now(); !m.isIdle; {
		if time.Since(start) > time.Second {
			t.Fatal("run cleanup never marked the session idle")
		}
		drainOwnerLoop(m, 10*time.Millisecond)
	}
	return time.Now()
}

// waitForCollect drains the owner loop until the idle collector has run want
// times or limit passes, and returns how long that took (limit if it never did).
func waitForCollect(m *InteractiveMode, want int32, limit time.Duration) time.Duration {
	start := time.Now()
	for m.idleCollector.collects.Load() < want && time.Since(start) < limit {
		drainOwnerLoop(m, 10*time.Millisecond)
	}
	return time.Since(start)
}

// A run that ends before the spinner's first tick still returns the heap once
// after it: the run's end arms the collection, not a busy spinner frame.
func TestIdleCollectionAfterRunShorterThanOneSpinnerTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer func() { idleCollectSink = nil }()
		m, ctx := startIdleCollectHarness(t)
		start := time.Now()
		end := runUntilIdle(t, m, ctx, 0)
		if took := end.Sub(start); took >= 80*time.Millisecond {
			t.Fatalf("run took %v, want shorter than one 80ms spinner tick", took)
		}
		drainOwnerLoop(m, 5*time.Second)
		if got := m.idleCollector.collects.Load(); got != 1 {
			t.Fatalf("idle collections in 5s after a %v run = %d, want 1", end.Sub(start), got)
		}
	})
}

// After a run the idle collection runs about 500 ms into the idle period, as
// in Pi-Bolt, not 2 s; a run that starts before then cancels it, and that
// run's end arms it again. Further idle time collects nothing more.
func TestIdleCollectionRunsHalfASecondAfterRunEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer func() { idleCollectSink = nil }()
		m, ctx := startIdleCollectHarness(t)
		runUntilIdle(t, m, ctx, 500*time.Millisecond)
		drainOwnerLoop(m, 300*time.Millisecond)
		if got := m.idleCollector.collects.Load(); got != 0 {
			t.Fatalf("idle collections 300ms after a run = %d, want 0 before %v", got, idleCollectEarliest)
		}
		// The next run starts before the delay and runs past it.
		runUntilIdle(t, m, ctx, time.Second)
		if got := m.idleCollector.collects.Load(); got != 0 {
			t.Fatalf("idle collections by the end of the next run = %d, want 0", got)
		}
		after := waitForCollect(m, 1, 3*time.Second)
		if got := m.idleCollector.collects.Load(); got != 1 {
			t.Fatalf("idle collections in %v after the run ended = %d, want 1 within %v", after, got, idleCollectDeadline)
		}
		if after < idleCollectEarliest || after > idleCollectDeadline {
			t.Fatalf("idle collection ran %v after the run ended, want %v..%v", after, idleCollectEarliest, idleCollectDeadline)
		}
		drainOwnerLoop(m, 5*time.Second)
		if got := m.idleCollector.collects.Load(); got != 1 {
			t.Fatalf("idle collections over 5s more idle = %d, want 1", got)
		}
	})
}

// Typed input after a run restarts the idle wait instead of dropping the
// collection, as Pi-Bolt's ui.addInputListener restarts idle-gc's timer on every
// keystroke: typing defers it, and it still runs about 500 ms after the typing
// stops. A key for the editor goes to the main loop and a key for a selector to
// its modal route; neither requests a frame on its own, and both restart it.
func TestIdleCollectionWaitRestartsOnInput(t *testing.T) {
	for _, route := range []string{"main loop", "modal"} {
		t.Run(route, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				defer func() { idleCollectSink = nil }()
				m, ctx := startIdleCollectHarness(t)
				runUntilIdle(t, m, ctx, 500*time.Millisecond)
				drainOwnerLoop(m, 300*time.Millisecond)
				typeKey(m, ctx, route == "modal")
				drainOwnerLoop(m, 300*time.Millisecond)
				if got := m.idleCollector.collects.Load(); got != 0 {
					t.Fatalf("idle collections 300ms after a key = %d, want 0 before %v", got, idleCollectEarliest)
				}
				after := 300*time.Millisecond + waitForCollect(m, 1, 3*time.Second)
				if got := m.idleCollector.collects.Load(); got != 1 {
					t.Fatalf("idle collections in %v after a key following a run = %d, want 1 within %v", after, got, idleCollectDeadline)
				}
				if after > idleCollectDeadline {
					t.Fatalf("idle collection ran %v after a key, want within %v", after, idleCollectDeadline)
				}
			})
		})
	}
}

// typeKey routes one typed key through the terminal reader's input router to
// the main loop, or to an armed modal selector, and has the receiver take it.
func typeKey(m *InteractiveMode, ctx context.Context, modal bool) {
	readCh := make(chan inputChunk, 1)
	if modal {
		modalCh, release := m.acquireModalInputChannel()
		defer release()
		go m.routeInputChunk(ctx, []byte("x"), readCh)
		<-modalCh
		return
	}
	m.routeInputChunk(ctx, []byte("x"), readCh)
	(<-readCh).ticket.settle()
}
