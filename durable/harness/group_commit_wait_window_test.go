package harness

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// TestGroupCommitWaitRegisteredButNotYetParkedIsWaitedFor pins Sub-Window 1 of the
// group-commit wait continuation window: waitForTask registers its waiter in
// s.taskWaiters inside readOnLine (scheduler.go:929), but parks it in
// session.parks only after readOnLine returns (scheduler.go:956 -> Waiter.Wait ->
// SessionImpl.Park).
//
// Seam 1 (hook-free): blocker carries its own Continuation whose Entered() is held
// on a test gate. Once Call 1 ([blocker]) publishes, deliverLoop releases blocker
// and blocks at session.go:621 on blockerTurn, deterministically pinning waited at
// session.go:540 (registered in s.taskWaiters, not yet returned from readOnLine,
// hence absent from session.parks) while Call 2 ([one, two]) commits and publishes
// part 0 (one).
//
// Because waited is registered for one before one publishes, one's endPublication
// must wait for waited (via awaitReadied) before finishing part 0 and moving on to
// part 1 (two). While blockerTurn is still held, waited cannot return from
// readOnLine to acknowledge its wake, so one's publication must remain blocked in
// endPublication (part 0's Publishing scope does not complete, and two is not
// published yet) until blockerTurn is released.
func TestGroupCommitWaitRegisteredButNotYetParkedIsWaitedFor(t *testing.T) {
	fixtures := newGCFixtures()
	base := newControlledStorage()
	// Track each part's publication boundary (part 0 = blocker, part 1 = one,
	// part 2 = two) through Storage.Publishing, which wraps beginPublication ..
	// endPublication (session.go:356-362).
	pubEntered := make(chan int, 8)
	pubFinished := make(chan int, 8)
	var pubCount int
	store := &seam1Storage{
		controlledStorage: base,
		onPublishing: func(ctx context.Context, publish func()) {
			pubCount++
			n := pubCount
			pubEntered <- n
			publish()
			pubFinished <- n
		},
	}

	harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
	defer closeHarness(t, harness)
	root := mustRoot(t, harness, nil)
	other := must(harness.CreateConversation(testContext, ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless},
	}))
	one := must(gcCreateHold(fixtures, root, "one")())
	two := must(gcCreateHold(fixtures, other, "two")())
	harness.Resume()
	eventually(t, func() bool {
		return gcStatus(t, harness, one) == "running" && gcStatus(t, harness, two) == "running"
	})
	ended := gcFollowStoredTasks(harness, func(record anyTaskRecord) bool {
		return record.State.Status == durable.TaskTerminal
	})

	// Drain any publications from setup above before holding Call 1.
	drainPubs := func() {
		for {
			select {
			case <-pubEntered:
			case <-pubFinished:
			default:
				return
			}
		}
	}
	drainPubs()
	pubCount = 0

	// Call 1: blocker holds Storage.Commit and carries its own Continuation held on
	// releaseBlocker so deliverLoop blocks at session.go:621 after releasing blocker,
	// keeping waited fenced at session.go:540 while Call 2 publishes part 0 (one).
	held := store.holdCommits()
	blockerCtx, blockerTurn := session.WithContinuation(testContext)
	blockerReleased := deferred()
	releaseBlocker := deferred()
	blocker := gcGo(func() (any, error) {
		val, err := root.Commit(blockerCtx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		blockerReleased.resolve()
		_ = releaseBlocker.wait(testContext)
		blockerTurn.Entered()
		return val, err
	})
	defer func() {
		releaseBlocker.resolve()
		blockerTurn.Entered()
	}()
	gcEntered(t, held)

	// Register waited behind blocker on the line. No WithContinuation on waited:
	// waited is blocked at session.go:540 (fenced behind blocker) with its waiter
	// already registered in tasks.taskWaiters, and has not returned from readOnLine
	// to call waiter.Wait() -> Park().
	waited := gcGo(func() (struct{}, error) {
		_, err := harness.WaitForTask(testContext, one)
		return struct{}{}, err
	})
	tasks := harness.(*harnessImpl).tasks
	eventually(t, func() bool {
		return len(tasks.taskWaiters.Keys()) > 0
	})
	gcFlush(t)

	// Queue one and two behind blocker so both commit in Call 2.
	fixtures.gate("one").resolve()
	gcFlush(t)
	fixtures.gate("two").resolve()
	gcFlush(t)

	// Release Call 1 (blocker). Call 1 publishes (pub 1), settles blocker, and kicks
	// deliverLoop, which releases blocker and blocks at session.go:621 waiting for
	// blockerTurn.Entered(). Meanwhile the writer immediately sends Call 2 ([one, two])
	// and publishes part 0 (one, pub 2).
	held.release()

	// Bound every wait explicitly to 5s (never hang until go test -timeout).
	waitChan := func(ch <-chan int, label string) int {
		t.Helper()
		select {
		case n := <-ch:
			return n
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out after 5s waiting for %s", label)
			return 0
		}
	}
	waitGate := func(g *deferredGate, label string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
		defer cancel()
		if err := g.wait(ctx); err != nil {
			t.Fatalf("timed out after 5s waiting for %s: %v", label, err)
		}
	}

	// Wait for Call 1 (pub 1 = blocker) to finish and blocker to be released by deliverLoop
	// (so deliverLoop is now blocked on blockerTurn, pinning waited at session.go:540).
	if n := waitChan(pubEntered, "pub 1 (blocker) enter"); n != 1 {
		t.Fatalf("unexpected pub enter %d, want 1", n)
	}
	if n := waitChan(pubFinished, "pub 1 (blocker) finish"); n != 1 {
		t.Fatalf("unexpected pub finish %d, want 1", n)
	}
	waitGate(blockerReleased, "blocker released by deliverLoop")

	// Wait for Call 2 part 0 (pub 2 = one) to enter publication.
	if n := waitChan(pubEntered, "pub 2 (one) enter"); n != 2 {
		t.Fatalf("unexpected pub enter %d, want 2", n)
	}
	// Let pub 2 settle one's waiter and reach endPublication -> awaitReadied().
	// Wait until ended.has(one) is observed.
	deadline := time.Now().Add(5 * time.Second)
	for !ended.has(one) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after 5s waiting for ended.has(one)")
		}
		time.Sleep(time.Millisecond)
	}
	gcFlush(t)

	// At this point:
	// - blockerTurn has NOT been released, so deliverLoop is blocked at session.go:621
	//   and waited is still pinned at session.go:540 (inside readOnLine, registered in
	//   s.taskWaiters before one published, not yet reached waiter.Wait() -> Park()).
	// - one's end has been published (ended.has(one) == true), closing waiter.done.
	//
	// Contract: one's endPublication must wait in awaitReadied() for waited to resume
	// before pub 2 finishes and before part 1 (two) can be published. Because waited
	// cannot leave session.go:540 until blockerTurn is released, pub 2 MUST still be
	// blocked in endPublication right now.
	select {
	case n := <-pubFinished:
		t.Fatalf("pub %d (one) finished endPublication without waiting for registered-but-unparked waiter (ended.has(one)=%v, ended.has(two)=%v)", n, ended.has(one), ended.has(two))
	default:
	}

	// Now release blockerTurn so deliverLoop releases waited, waited resumes and
	// acknowledges its wake, allowing pub 2 to finish and pub 3 (two) to publish.
	releaseBlocker.resolve()
	blocker.wait(t)
	if n := waitChan(pubFinished, "pub 2 (one) finish after releaseBlocker"); n != 2 {
		t.Fatalf("unexpected pub finish %d, want 2", n)
	}
	waited.wait(t)
	tkWaitOutcome(t, harness, two)
}

type seam1Storage struct {
	*controlledStorage
	onPublishing func(ctx context.Context, publish func())
}

func (s *seam1Storage) Publishing(ctx context.Context, publish func()) {
	if s.onPublishing != nil {
		s.onPublishing(ctx, publish)
		return
	}
	publish()
}

// TestGroupCommitSettledWaitRegisteredButNotYetParkedIsWaitedFor pins Sub-Window 1
// for Submissions.waitSettled (submissions.go:211-246): the settled-wait registers
// its waiter in submissions.waiters inside readOnLine (submissions.go:237), and
// parks it in session.parks only after readOnLine returns (submissions.go:245 ->
// Waiter.Wait -> SessionImpl.Park).
//
// Seam 1 (hook-free): blocker carries its own Continuation held on releaseBlocker,
// pinning waited at session.go:540 (registered in submissions.waiters, not yet
// parked) while Call 2 publishes part 0 (Abort settling the queued submission) and
// part 1 (an unrelated note commit). Because waited is registered before part 0
// publishes, part 0's endPublication must wait in awaitReadied() until waited
// acknowledges its wake, so part 0's Publishing scope must not finish while
// blockerTurn is still held.
func TestGroupCommitSettledWaitRegisteredButNotYetParkedIsWaitedFor(t *testing.T) {
	base := newControlledStorage()
	pubEntered := make(chan int, 16)
	pubFinished := make(chan int, 16)
	var pubCount int
	store := &seam1Storage{
		controlledStorage: base,
		onPublishing: func(ctx context.Context, publish func()) {
			pubCount++
			n := pubCount
			pubEntered <- n
			publish()
			pubFinished <- n
		},
	}

	harness, _, _ := openTasks(t, store, nil)
	defer closeHarness(t, harness)
	root := mustRoot(t, harness, nil)
	other := must(harness.CreateConversation(testContext, ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless},
	}))

	// First input is placed immediately on the idle conversation (and starts a run
	// task); the second input is queued behind it so Abort can settle it in one commit.
	// Wait for the scheduler's background work from submitInput to settle before we
	// hold Call 1, so Call 2 part 0 is Abort itself.
	_ = submitInput(t, root, "first")
	sub := submitInput(t, root, "second")
	subId := sub.Id()
	if rec := must(sub.Status(testContext)); rec.Status != durable.SubmissionQueued {
		t.Fatalf("submission status = %q, want queued", rec.Status)
	}
	// Let any background scheduler commits triggered by submitInput finish before we
	// start tracking publications.
	eventually(t, func() bool {
		return len(pubEntered) == len(pubFinished) && len(pubEntered) > 0
	})
	gcFlush(t)

	settledPub := make(chan int, 4)
	harness.(*harnessImpl).SessionImpl.FollowCommits(func(_ context.Context, pub session.ObservedCommit) {
		if pub.Memory {
			return
		}
		for _, change := range pub.Changes {
			write, ok := change.(durable.SubmissionWrite)
			if ok && write.Value.Id == subId && isSettledSubmission(write.Value) {
				select {
				case settledPub <- pubCount:
				default:
				}
			}
		}
	})

	// Drain setup publications before holding Call 1.
	for {
		select {
		case <-pubEntered:
		case <-pubFinished:
		default:
			goto drained
		}
	}
drained:
	pubCount = 0

	// Call 1: blocker holds Storage.Commit on other and carries its own Continuation
	// held on releaseBlocker so deliverLoop blocks at session.go:621 after releasing
	// blocker, keeping waited fenced at session.go:540 while Call 2 publishes part 0.
	held := store.holdCommits()
	blockerCtx, blockerTurn := session.WithContinuation(testContext)
	blockerReleased := deferred()
	releaseBlocker := deferred()
	blocker := gcGo(func() (any, error) {
		val, err := other.Commit(blockerCtx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(other.Id(), durable.EntryDraft{Kind: "note"})
		})
		blockerReleased.resolve()
		_ = releaseBlocker.wait(testContext)
		blockerTurn.Entered()
		return val, err
	})
	defer func() {
		releaseBlocker.resolve()
		blockerTurn.Entered()
	}()
	gcEntered(t, held)

	// Register waited behind blocker on the line via sub.Wait -> Submissions.Wait ->
	// Submissions.waitSettled. waited is pinned at session.go:540 with its waiter
	// registered in submissions.waiters (submissions.go:237) and not yet parked.
	waited := gcGo(func() (durable.SettledSubmissionRecord, error) {
		return sub.Wait(testContext)
	})
	subs := harness.(*harnessImpl).submissions
	eventually(t, func() bool {
		return len(subs.waiters.Keys()) > 0
	})
	gcFlush(t)

	// Queue Abort (settling sub) and a trailing commit behind blocker so both commit
	// in Call 2.
	aborted := gcGo(func() (durable.SubmissionAbortResult, error) {
		return sub.Abort(testContext)
	})
	gcFlush(t)
	trailing := gcGo(gcAppendNote(other))
	gcFlush(t)

	// Release Call 1 (blocker). Call 1 publishes (pub 1), settles blocker, and kicks
	// deliverLoop, which releases blocker and blocks at session.go:621 waiting for
	// blockerTurn.Entered(). Meanwhile the writer sends Call 2 and publishes Abort.
	held.release()

	waitChan := func(ch <-chan int, label string) int {
		t.Helper()
		select {
		case n := <-ch:
			return n
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out after 5s waiting for %s", label)
			return 0
		}
	}
	waitGate := func(g *deferredGate, label string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
		defer cancel()
		if err := g.wait(ctx); err != nil {
			t.Fatalf("timed out after 5s waiting for %s: %v", label, err)
		}
	}

	if n := waitChan(pubEntered, "pub 1 (blocker) enter"); n != 1 {
		t.Fatalf("unexpected pub enter %d, want 1", n)
	}
	if n := waitChan(pubFinished, "pub 1 (blocker) finish"); n != 1 {
		t.Fatalf("unexpected pub finish %d, want 1", n)
	}
	waitGate(blockerReleased, "blocker released by deliverLoop")

	// Wait for the publication that settles sub (Abort) to run its FollowCommits
	// observer. Record which publication number settled sub.
	settleN := waitChan(settledPub, "settled submission publication")
	gcFlush(t)

	// Drain any pubFinished for earlier publications (< settleN), then check whether
	// settleN's Publishing scope already finished while blockerTurn is still held.
	for {
		select {
		case n := <-pubFinished:
			if n >= settleN {
				t.Fatalf("the publication that settled the submission finished endPublication without waiting for the registered-but-unparked settled-wait")
			}
		default:
			goto checked
		}
	}
checked:

	// Release blockerTurn so deliverLoop releases waited, waited resumes and
	// acknowledges its wake, allowing settleN's publication to finish.
	releaseBlocker.resolve()
	blocker.wait(t)
	for {
		if n := waitChan(pubFinished, "settle publication finish after releaseBlocker"); n == settleN {
			break
		}
	}
	aborted.wait(t)
	trailing.wait(t)
	waited.wait(t)
}
