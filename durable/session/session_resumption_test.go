package session_test

// The handshake between a publication and the goroutines it readied (resumption.go): the waits of the Session register before
// they block and acknowledge when they resumed, a caller that needs its continuation ordered whole brings a Continuation, and
// nothing depends on the number of goroutines in the process.

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

func TestParkOnAChannelThatIsClosedRegistersNothing(t *testing.T) {
	g := openGC(t, 1)
	closed := make(chan struct{})
	close(closed)
	parked := g.session.Park(ctx, closed)
	if parked != nil {
		t.Fatal("a wait whose channel is closed already was registered")
	}
	parked.Resume()
	if waits := session.ParkedWaits(g.session); waits != 0 {
		t.Fatalf("%d waits registered, want none", waits)
	}
	g.close()
}

func TestAWaitDeregistersWhenItResumesHoweverItReturned(t *testing.T) {
	g := openGC(t, 1)
	// By its wake.
	wake := make(chan struct{})
	woken := make(chan struct{})
	go func() {
		defer close(woken)
		g.session.Await(ctx, wake)
	}()
	for session.ParkedWaits(g.session) == 0 {
		runtime.Gosched()
	}
	close(wake)
	<-woken
	if waits := session.ParkedWaits(g.session); waits != 0 {
		t.Fatalf("%d waits registered after its wake, want none", waits)
	}
	// Abandoned, by the cancellation of its context: it never saw its channel close.
	abandonedCtx, cancel := context.WithCancel(ctx)
	never := make(chan struct{})
	abandoned := make(chan struct{})
	registered := make(chan struct{})
	go func() {
		defer close(abandoned)
		parked := g.session.Park(abandonedCtx, never)
		defer parked.Resume()
		close(registered)
		<-abandonedCtx.Done()
	}()
	<-registered
	cancel()
	<-abandoned
	if waits := session.ParkedWaits(g.session); waits != 0 {
		t.Fatalf("%d waits registered after it was abandoned, want none", waits)
	}
	g.close()
}

// A continuation that goes on to wait for a gate has entered: the gate is the one of the part the hop is about to publish, so
// holding the hop until the continuation is done would hold it forever.
func TestAContinuationThatWaitsForAGateHasEntered(t *testing.T) {
	g := openGC(t, 1)
	id := g.ids[0]
	held := g.storage.hold()
	lead := g.commitAsync(appendChange(id, "lead"))
	<-held.Entered()
	ticket := g.session.TakeLineTicket()
	touched := make(chan error, 1)
	one := spawn(func(ctx ctxT) (any, error) {
		value, err := g.session.CommitWithTicket(ticket, ctx, func(tx *session.Transaction) (any, error) { return appendChange(id, "one")(tx) }, session.TransactionScope{})
		if err != nil {
			return value, err
		}
		// `two` is not published: the append key waits for it, in the Session.
		touched <- g.session.AwaitTouch(ctx, session.AppendKey(id))
		return value, nil
	})
	two := g.commitAsync(appendChange(id, "two"))
	quiesce(one, two)
	g.awaitQueued(2)
	held.Release()
	select {
	case err := <-touched:
		must(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the publication of the part after `one` waited for the continuation of `one`, which waits for that part's gate")
	}
	for _, started := range []*call{lead, one, two} {
		if _, err := started.wait(); err != nil {
			t.Fatal(err)
		}
	}
	expectNames(t, g.published.entryKinds(), "lead", "one", "two")
	g.close()
}

// A continuation that goes on to wait for the line has entered too: the line is held by a job that waits for the part the hop
// is about to publish.
func TestAContinuationThatWaitsForTheLineHasEntered(t *testing.T) {
	g := openGC(t, 1)
	id := g.ids[0]
	held := g.storage.hold()
	lead := g.commitAsync(appendChange(id, "lead"))
	<-held.Entered()
	ticket := g.session.TakeLineTicket()
	again := make(chan error, 1)
	one := spawn(func(ctx ctxT) (any, error) {
		value, err := g.session.CommitWithTicket(ticket, ctx, func(tx *session.Transaction) (any, error) { return appendChange(id, "one")(tx) }, session.TransactionScope{})
		if err != nil {
			return value, err
		}
		_, err = g.session.CommitWith(ctx, func(*session.Transaction) (any, error) { return nil, nil }, session.TransactionScope{})
		again <- err
		return value, nil
	})
	two := g.commitAsync(appendChange(id, "two"))
	// Holds the line, behind `two`, until the entry of `two` is published.
	blocker := g.readAsync(func(line durable.Storage) (any, error) {
		_, err := line.ScanEntries(ctx, durable.EntryQuery{ConversationId: id}, 1, nil)
		return nil, err
	})
	quiesce(one, two, blocker)
	g.awaitQueued(2)
	held.Release()
	select {
	case err := <-again:
		must(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the publication of the part after `one` waited for the continuation of `one`, which waits for the line")
	}
	for _, started := range []*call{lead, one, two} {
		if _, err := started.wait(); err != nil {
			t.Fatal(err)
		}
	}
	expectNames(t, g.published.entryKinds(), "lead", "one", "two")
	g.close()
}

// A caller whose callback panicked never waits for its release, so it has resumed as far as the delivery goroutine can tell: the
// outcomes after it are still delivered.
func TestAPanickingCallbackDoesNotHoldTheOutcomesAfterIt(t *testing.T) {
	g := openGC(t, 1)
	id := g.ids[0]
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_, _ = g.session.CommitWith(ctx, func(*session.Transaction) (any, error) { panic("callback bug") }, session.TransactionScope{})
	}()
	if got := <-panicked; got != "callback bug" {
		t.Fatalf("the caller recovered %v, want its callback's panic", got)
	}
	later := make(chan error, 1)
	go func() {
		_, err := g.session.Commit(ctx, func(tx durable.Tx) (any, error) { return bumpChange(id)(tx) })
		later <- err
	}()
	select {
	case err := <-later:
		must(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("a commit after one whose callback panicked never got its outcome")
	}
	g.close()
}

// Pi's guarantee does not depend on how many goroutines a process has, and neither does the Session's: every ordering case
// again, in a process with more than a hundred and twenty-eight live goroutines, above which the previous mechanism, which read
// the state of every goroutine, gave the order up.
func TestOrderingHoldsAboveAHundredAndTwentyEightGoroutines(t *testing.T) {
	parkGoroutines(t, 300)
	if live := runtime.NumGoroutine(); live <= 128 {
		t.Fatalf("%d goroutines live, want more than 128", live)
	}
	for _, ordering := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"TestGroupCommitBatching", TestGroupCommitBatching},
		{"TestGroupCommitOutcomes", TestGroupCommitOutcomes},
		{"TestGroupCommitPublication", TestGroupCommitPublication},
		{"TestGroupCommitTheStoragesPublicationScope", TestGroupCommitTheStoragesPublicationScope},
		{"TestGroupCommitAFailureBetweenTwoPublications", TestGroupCommitAFailureBetweenTwoPublications},
		{"TestAWakeADeferredPublicationRunsIsEnteredBeforeTheNextPartIsPublished", TestAWakeADeferredPublicationRunsIsEnteredBeforeTheNextPartIsPublished},
		{"TestAContinuationThatWaitsForAGateHasEntered", TestAContinuationThatWaitsForAGateHasEntered},
		{"TestAContinuationThatWaitsForTheLineHasEntered", TestAContinuationThatWaitsForTheLineHasEntered},
	} {
		t.Run(ordering.name, ordering.run)
	}
}

// A wait that ended without a publication waking it (a cancelled context, a close) is acknowledged by Abandon at once, whatever
// Continuation it belongs to: Pi orders what a publication resolves, not a rejected wait.
func TestAnAbandonedWaitOfAContinuationIsAcknowledgedAtOnce(t *testing.T) {
	g := openGC(t, 1)
	continued, _ := session.WithContinuation(ctx)
	never := make(chan struct{})
	parked := g.session.Park(continued, never)
	if waits := session.ParkedWaits(g.session); waits != 1 {
		t.Fatalf("%d waits registered, want 1", waits)
	}
	parked.Abandon()
	if waits := session.ParkedWaits(g.session); waits != 0 {
		t.Fatalf("%d waits registered after the wait was abandoned, want none: the continuation never entered", waits)
	}
	g.close()
}
