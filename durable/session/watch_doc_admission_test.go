package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// session.ts watchDoc (268-302): the address is resolved before the job is queued (271), and the job re-checks the Session's health (281) and the
// caller's cancellation (282) when it takes its turn on the mutation line, before it reads Storage, so a watch queued behind a commit sees what that
// commit did. DocumentObserver.WatchDocErased is the Go form of the overloaded watchDoc.

// The queued watches below ask for a document that does not exist, so their job reaches Storage only if it passes both checks: the document the held
// commit loaded is served from the Session's cache and would hide a skipped check.
var absentWatchDoc = defineDoc("watch.absent", 1, sessionScope, func() obj { return delta.JsonObjectOf() })

// settleQueuedBehindPoisoningCommit runs call on the mutation line behind a commit whose Storage write fails uncertainly and so poisons the Session, and
// returns call's error and whether it looked a document up in Storage. call asks for a document that does not exist, so it reaches Storage only if it skipped
// its health check: the document the held commit loaded is served from the Session's cache and would hide that.
func settleQueuedBehindPoisoningCommit(t *testing.T, harness sessiontest.Harness, call func() error) (err error, lookedUp bool) {
	t.Helper()
	must(t, harness.Session.UnloadDocuments())
	harness.Storage.FailNextCommit(errors.New("disk vanished"))
	commits := harness.Storage.HoldCommits()
	committed := make(chan error, 1)
	go func() {
		committed <- tryCommit(harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, watchStateDoc).Set("value", 1) })
	}()
	waitClosed(t, commits.Entered())
	finds := harness.Storage.HoldFindDocument() // after the commit loaded its document
	defer finds.Release()
	called := make(chan error, 1)
	go func() { called <- call() }()
	waitLineJobs(harness, 2)
	commits.Release()
	expectErrorContains(t, <-committed, "disk vanished")
	select {
	case err = <-called:
	case <-time.After(10 * time.Second):
		t.Fatal("the queued call did not settle: it read Storage instead of rejecting the poisoned Session")
	}
	select {
	case <-finds.Entered():
		lookedUp = true
	default:
	}
	return err, lookedUp
}

func TestWatchDocQueuedBehindACommitThatPoisonsTheSessionRejectsWithoutReadingStorage(t *testing.T) {
	harness := createWatchHarness(t)
	err, lookedUp := settleQueuedBehindPoisoningCommit(t, harness, func() error {
		_, err := harness.Session.WatchDocErased(context.Background(), absentWatchDoc)
		return err
	})
	expectErrorContains(t, err, "Session failed after a storage error")
	if lookedUp {
		t.Fatal("the rejected watch looked the document up")
	}
}

func TestWatchDocCancelledWhileQueuedRejectsBeforeReadingStorage(t *testing.T) {
	harness := createWatchHarness(t)
	must(t, harness.Session.UnloadDocuments())
	commits := harness.Storage.HoldCommits()
	committed := make(chan error, 1)
	go func() {
		committed <- tryCommit(harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, watchStateDoc).Set("value", 1) })
	}()
	waitClosed(t, commits.Entered())
	finds := harness.Storage.HoldFindDocument() // after the commit loaded its document
	defer finds.Release()
	child, cancel := context.WithCancelCause(context.Background())
	watched := make(chan error, 1)
	go func() {
		_, err := harness.Session.WatchDocErased(child, absentWatchDoc)
		watched <- err
	}()
	waitLineJobs(harness, 2)
	cancel(errors.New("cancel while queued"))
	commits.Release()
	must(t, <-committed)
	select {
	case err := <-watched:
		expectErrorContains(t, err, "cancel while queued")
	case <-time.After(10 * time.Second):
		t.Fatal("the queued watch did not settle: it read Storage before checking its cancellation")
	}
	select {
	case <-finds.Entered():
		t.Fatal("the cancelled watch looked the document up")
	default:
	}
}

func TestWatchDocRejectsAnAddressItCannotResolveBeforeQueueing(t *testing.T) {
	harness := open()
	commits := harness.Storage.HoldCommits()
	defer commits.Release()
	committed := make(chan error, 1)
	go func() {
		committed <- tryCommit(harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, watchStateDoc).Set("value", 1) })
	}()
	waitClosed(t, commits.Entered())
	jobs := harness.Session.LineJobs()
	_, err := harness.Session.WatchDocErased(context.Background(), liveDoc)
	expectErrorContains(t, err, "test.live requires a conversation ID")
	if after := harness.Session.LineJobs(); after != jobs {
		t.Fatalf("the unresolvable address took a place on the mutation line: %d jobs, was %d", after, jobs)
	}
	commits.Release()
	must(t, <-committed)
}

func TestWatchReleasesItsCommitSubscriptionWhenItEnds(t *testing.T) {
	harness := createWatchHarness(t)
	baseline, closeBaseline := harness.Session.CommitSubscriptions(), session.CloseSubscriptions(harness.Session)
	stopped := watchDoc(t, harness, context.Background(), watchStateDoc)
	if got := harness.Session.CommitSubscriptions(); got != baseline+1 {
		t.Fatalf("a live watch holds %d commit subscriptions, want %d", got, baseline+1)
	}
	if _, err := stopped.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := session.CloseSubscriptions(harness.Session); got != closeBaseline {
		t.Fatalf("a stopped watch holds %d close subscriptions, want %d", got, closeBaseline)
	}
	if got := harness.Session.CommitSubscriptions(); got != baseline {
		t.Fatalf("a stopped watch holds %d commit subscriptions, want %d", got, baseline)
	}
	child, cancel := context.WithCancel(context.Background())
	cancelled := watchDoc(t, harness, child, watchStateDoc)
	cancel()
	closedEnd(t, cancelled)
	if got := harness.Session.CommitSubscriptions(); got != baseline {
		t.Fatalf("a cancelled watch holds %d commit subscriptions, want %d", got, baseline)
	}
}
