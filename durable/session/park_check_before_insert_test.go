package session_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

type hopObserveHost struct {
	session.WriterHost
	onHop func()
}

func (h hopObserveHost) Hop() {
	if h.onHop != nil {
		h.onHop()
	}
	h.WriterHost.Hop()
}

// TestGroupCommitParkCheckBeforeInsertWindow pins the check-before-insert window
// in SessionImpl.Park: checking `<-done` and inserting into session.parks without
// holding parkMu across both steps lets a publication close done and run
// awaitReadied() against session.parks before the waiter's entry is inserted.
//
// Using the test-only beforeParkInsert hook (nil in production), this test
// pauses the waiter goroutine after Park's `<-done` check and before its insert
// into session.parks. While paused there, Call 2 ([one, two]) publishes part 0
// ("one"), whose observer closes waitDone via DeferUntilPublished.
//
// The test releases the pause once either (a) part 1 enters Hop() — which on the
// unfixed code happens immediately because part 0's awaitReadied() scanned an
// empty session.parks — OR (b) waitDone is closed AND the writer goroutine is
// blocked waiting for parkMu inside awaitReadied() (under the cure, where the
// pause sits inside Park's parkMu critical section, so the writer's scan cannot
// return until the pause is released). Waiters never deadlock under either order:
// on unfixed code part 0 already missed the waiter and part 1 publishes "two"
// while the continuation is still held; under the cure, releasing the pause lets
// Park finish its insert and unlock parkMu, after which awaitReadied() finds the
// closed entry and holds part 1 ("two") until turn.Entered().
func TestGroupCommitParkCheckBeforeInsertWindow(t *testing.T) {
	g := openGC(t, 2)
	// Clean up Session only on a non-timed-out path (a timeout inside a locked hook
	// could otherwise stall Close; releaseCheckGap and releaseCont are always closed
	// first via defers registered below).
	var timedOut bool
	defer func() {
		if !timedOut {
			g.close()
		}
	}()
	a, b := g.ids[0], g.ids[1]

	hopEntered := make(chan struct{})
	var hopOnce sync.Once
	session.WrapWriterHost(g.session, func(host session.WriterHost) session.WriterHost {
		return hopObserveHost{
			WriterHost: host,
			onHop: func() {
				hopOnce.Do(func() { close(hopEntered) })
			},
		}
	})

	waitDone := make(chan struct{})
	var closeOnce sync.Once
	onePublished := make(chan struct{})
	twoPublished := make(chan struct{})

	g.session.FollowCommits(func(_ context.Context, commit session.ObservedCommit) {
		if commit.Memory {
			return
		}
		for _, change := range commit.Changes {
			write, ok := change.(durable.EntryWrite)
			if !ok {
				continue
			}
			switch write.Value.Kind {
			case "one":
				g.session.DeferUntilPublished(func() {
					closeOnce.Do(func() { close(waitDone) })
				})
				select {
				case <-onePublished:
				default:
					close(onePublished)
				}
			case "two":
				select {
				case <-twoPublished:
				default:
					close(twoPublished)
				}
			}
		}
	})

	// Pause the waiter goroutine between Park's done check and its insert into session.parks.
	inCheckGap := make(chan struct{})
	releaseCheckGap := make(chan struct{})
	var hookOnce sync.Once
	session.SetBeforeParkInsert(g.session, func() {
		hookOnce.Do(func() {
			close(inCheckGap)
			<-releaseCheckGap
		})
	})
	defer func() {
		select {
		case <-releaseCheckGap:
		default:
			close(releaseCheckGap)
		}
	}()

	// Hold the waiter's continuation until after the assertion on "two".
	contEntered := make(chan struct{})
	releaseCont := make(chan struct{})
	defer func() {
		select {
		case <-releaseCont:
		default:
			close(releaseCont)
		}
	}()

	// Call 1: lead holds Storage.Commit so Call 2 ([one, two]) batches behind it.
	held := g.storage.hold()
	lead := g.commitAsync(appendChange(a, "lead"))
	select {
	case <-held.Entered():
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for lead commit to enter storage")
	}

	// Waiter goroutine with a Continuation: runs ReadOnLineCtx (fenced behind lead,
	// mirroring waitForTask), then calls Park(callCtx, waitDone).
	readRan := make(chan struct{})
	waiter := spawnContinuing(func(callCtx ctxT, turn *session.Continuation) (any, error) {
		_, err := g.session.ReadOnLineCtx(callCtx, func(_ durable.Storage) (any, error) {
			close(readRan)
			return nil, nil
		})
		if err != nil {
			return nil, err
		}
		parked := g.session.Park(callCtx, waitDone)
		<-waitDone
		parked.Resume()
		close(contEntered)
		<-releaseCont
		turn.Entered()
		return nil, nil
	})
	select {
	case <-readRan:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for waiter ReadOnLineCtx callback to run")
	}

	// Hold Call 2 ([one, two]) in Storage.Commit until the waiter is paused inside
	// Park between the done check and the session.parks insert.
	one := g.commitAsync(appendChange(a, "one"))
	two := g.commitAsync(appendChange(b, "two"))
	quiesce(one, two)
	g.awaitQueued(2)

	heldCall2 := g.storage.hold()
	held.Release()
	select {
	case <-heldCall2.Entered():
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for Call 2 ([one, two]) to enter storage")
	}
	select {
	case <-inCheckGap:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for waiter to enter beforeParkInsert gap")
	}

	// Release Call 2 so part 0 ("one") publishes and closes waitDone via
	// DeferUntilPublished, then enters awaitReadied().
	heldCall2.Release()
	select {
	case <-onePublished:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for part 0 (one) to publish")
	}
	// Wait until part 0's deferred wake has closed waitDone (does not require
	// awaitReadied() to return, so this cannot deadlock when beforeParkInsert runs
	// under parkMu). Then wait briefly for either hopEntered (unfixed: awaitReadied
	// returned without seeing the waiter and part 1 reached Hop) or a short grace
	// window (cured: awaitReadied is blocked on parkMu waiting for this pause to end).
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for part 0 (one) to close waitDone")
	}
	select {
	case <-hopEntered:
	case <-time.After(50 * time.Millisecond):
	}

	// Now release the waiter from beforeParkInsert.
	// - Unfixed: awaitReadied() already returned and part 1 is in Hop(); the waiter
	//   inserts the already-closed Parked, calls enter(turn) (unblocking deliverLoop
	//   and Hop), passes <-waitDone, calls parked.Resume(), and holds at <-releaseCont
	//   while part 1 ("two") publishes early.
	// - Cured: the waiter finishes inserting into session.parks and unlocks parkMu,
	//   unblocking awaitReadied(), which sees waitDone closed and waits for parked's
	//   acknowledgement; part 1 ("two") stays held until turn.Entered().
	close(releaseCheckGap)
	select {
	case <-contEntered:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for waiter continuation to enter")
	}

	// Contract: while the waiter's continuation is held before turn.Entered(),
	// part 1 ("two") must not be published. awaitWakeHop(2) returns once part 0 is
	// awaiting a wake in awaitReadied() or once part 1 ("two", commit 3) has been
	// published.
	hopDone := make(chan struct{})
	go func() {
		g.awaitWakeHop(2)
		close(hopDone)
	}()
	select {
	case <-hopDone:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for awaitWakeHop(2)")
	}
	if g.published.len() > 2 {
		t.Fatalf("the end of two was published before the continuation of the wait for one finished (Park check-before-insert window): published=%v", g.published.entryKinds())
	}
	select {
	case <-twoPublished:
		t.Fatalf("the end of two was published before the continuation of the wait for one finished (Park check-before-insert window)")
	default:
	}

	close(releaseCont)
	select {
	case <-twoPublished:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatalf("timed out after 5s waiting for part 1 (two) to publish after releasing continuation")
	}
	waitCall := func(c *call, label string) {
		t.Helper()
		select {
		case <-c.done:
			if c.err != nil {
				t.Fatalf("%s failed: %v", label, c.err)
			}
		case <-time.After(5 * time.Second):
			timedOut = true
			t.Fatalf("timed out after 5s waiting for %s", label)
		}
	}
	waitCall(lead, "lead commit")
	waitCall(one, "one commit")
	waitCall(two, "two commit")
	waitCall(waiter, "waiter")
}
