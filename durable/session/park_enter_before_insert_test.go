package session_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// TestGroupCommitParkEnterBeforeInsertWindow pins Sub-Window 2 of the group-commit
// wait continuation window: SessionImpl.Park (resumption.go:125-148) calls
// enter(continuationOf(ctx)) at line 131 BEFORE inserting the *Parked entry into
// session.parks at line 145.
//
// Using the test-only afterParkEnter hook (nil in production), this test pauses
// the waiter goroutine right after enter(turn) acknowledges its preceding
// ReadOnLine delivery (allowing deliverLoop to go idle). While the waiter is
// paused there, Call 2 ([one, two]) commits and publishes part 0 ("one"), whose
// observer closes the waiter's done channel via DeferUntilPublished.
//
// Contract: part 0's endPublication must wait in awaitReadied() for the waiter
// (whose continuation has entered Park) before publishing part 1 ("two"). On the
// unfixed enter-before-insert order, the waiter is not yet in session.parks when
// part 0 runs awaitReadied(), and deliverLoop is already idle because enter(turn)
// already ran, so Hop() returns immediately and part 1 ("two") is published while
// the waiter is still in the enter-before-insert gap.
func TestGroupCommitParkEnterBeforeInsertWindow(t *testing.T) {
	g := openGC(t, 2)
	defer g.close()
	a, b := g.ids[0], g.ids[1]

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

	// Pause the parking goroutine immediately after enter(continuationOf(ctx)).
	inParkGap := make(chan struct{})
	releaseParkGap := make(chan struct{})
	var hookOnce sync.Once
	session.SetAfterParkEnter(g.session, func() {
		hookOnce.Do(func() {
			close(inParkGap)
			<-releaseParkGap
		})
	})
	defer func() {
		select {
		case <-releaseParkGap:
		default:
			close(releaseParkGap)
		}
	}()

	// Call 1: lead holds Storage.Commit so Call 2 ([one, two]) batches behind it.
	held := g.storage.hold()
	lead := g.commitAsync(appendChange(a, "lead"))
	select {
	case <-held.Entered():
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after 5s waiting for lead commit to enter storage")
	}

	// Waiter goroutine with a Continuation: runs ReadOnLineCtx (fenced behind lead,
	// mirroring waitForTask / Submissions.waitSettled), then calls Park(callCtx, waitDone).
	// When lead settles, deliverLoop releases ReadOnLineCtx; the waiter calls Park,
	// which calls enter(turn) (unblocking deliverLoop -> idle) and pauses at inParkGap.
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
		turn.Entered()
		return nil, nil
	})
	select {
	case <-readRan:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after 5s waiting for waiter ReadOnLineCtx callback to run")
	}

	// Hold Call 2 ([one, two]) in Storage.Commit until the waiter is paused in the
	// afterParkEnter hook, so Call 2 publishes part 0 ("one") strictly while the
	// waiter has entered and has not returned from Park.
	one := g.commitAsync(appendChange(a, "one"))
	two := g.commitAsync(appendChange(b, "two"))
	quiesce(one, two)
	g.awaitQueued(2)

	heldCall2 := g.storage.hold()
	held.Release()
	select {
	case <-heldCall2.Entered():
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after 5s waiting for Call 2 ([one, two]) to enter storage")
	}
	select {
	case <-inParkGap:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after 5s waiting for waiter to enter afterParkEnter gap")
	}

	// Waiter has now called enter(turn) (acknowledging ReadOnLineCtx so deliverLoop is
	// idle) and is paused at afterParkEnter. Release Call 2 so part 0 ("one")
	// publishes and settles waitDone.
	heldCall2.Release()

	select {
	case <-onePublished:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after 5s waiting for part 0 (one) to publish")
	}

	// Contract: part 0's endPublication must wait in awaitReadied() for the waiter
	// while the waiter is still paused at afterParkEnter. awaitWakeHop(2) returns
	// once part 0 ("one", published commit 2 after lead) is awaiting a wake in
	// awaitReadied(), or once part 1 ("two", commit 3) has already been published.
	g.awaitWakeHop(2)
	select {
	case <-twoPublished:
		t.Fatalf("the next part was published while a waiter had entered and was not yet parked (resumption.go: enter before insert)")
	default:
	}

	close(releaseParkGap)
	select {
	case <-twoPublished:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after 5s waiting for part 1 (two) to publish after releasing park gap")
	}
	if _, err := lead.wait(); err != nil {
		t.Fatalf("lead commit failed: %v", err)
	}
	if _, err := one.wait(); err != nil {
		t.Fatalf("one commit failed: %v", err)
	}
	if _, err := two.wait(); err != nil {
		t.Fatalf("two commit failed: %v", err)
	}
	if _, err := waiter.wait(); err != nil {
		t.Fatalf("waiter failed: %v", err)
	}
}
