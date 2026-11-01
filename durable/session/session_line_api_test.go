package session_test

// The Go forms of what Pi's session.ts adds for the Harness's scheduler and context reads beside the group commit tests:
// commitInternal's exemption, onJobEnd, touch and drained, knownLatestEntryId, and the hop between two publications that waits
// for what a publication's deferred wakes woke (Go's form of "the continuations of what one part resolves start before the next
// is published"). Pi tests them through the Harness (test/harness-group-commit.test.ts); these cases pin the Session side.

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

func TestCommitInternalDeliversAJobThatWroteNothingAndWaitedAtNoGateAheadOfUnpublishedCommits(t *testing.T) {
	g := openGC(t, 2)
	a, b := g.ids[0], g.ids[1]
	held := g.storage.hold()
	first := g.bump(a)
	<-held.Entered()
	ticketed := func(internal bool, change func(tx *session.Transaction) (any, error)) *call {
		ticket := g.session.TakeLineTicket()
		return spawn(func(ctx ctxT) (any, error) {
			if internal {
				return g.session.CommitInternalWithTicket(ticket, ctx, change)
			}
			return g.session.CommitWithTicket(ticket, ctx, change, session.TransactionScope{})
		})
	}
	nothing := func(*session.Transaction) (any, error) { return "none", nil }
	clean := ticketed(true, nothing)
	ordinary := ticketed(false, nothing)
	// Reads what the commit in flight wrote: a hit at a gate, so not exempt.
	hit := ticketed(true, func(tx *session.Transaction) (any, error) {
		_, err := tx.Doc(gcNote, a)
		return "read", err
	})
	// Writes: never exempt, whatever it waited for.
	writes := ticketed(true, bumpChangeTx(b))
	quiesce(clean, ordinary, hit, writes)
	if !clean.settled() {
		t.Fatal("an internal commit that wrote nothing and waited at no gate did not settle ahead of the commit in flight")
	}
	for name, started := range map[string]*call{"ordinary": ordinary, "hit": hit, "writes": writes} {
		if started.settled() {
			t.Fatalf("the %s commit settled before the commit in flight was published", name)
		}
	}
	held.Release()
	mustBump(t, first)
	for _, started := range []*call{ordinary, hit, writes} {
		if _, err := started.wait(); err != nil {
			t.Fatal(err)
		}
	}
	g.close()
}

func bumpChangeTx(id durable.ConversationId) func(tx *session.Transaction) (any, error) {
	return func(tx *session.Transaction) (any, error) { return bumpChange(id)(tx) }
}

func TestOnJobEndRunsOnceTheJobsOutcomeSettles(t *testing.T) {
	g := openGC(t, 1)
	id := g.ids[0]
	var (
		mu     sync.Mutex
		events []string
	)
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	g.session.FollowCommits(func(_ ctxT, commit session.ObservedCommit) { record("published") })
	// Off the line it runs at once.
	g.session.OnJobEnd(func() { record("off the line") })
	// A commit with writes ends at its publication; one without, and a callback's error, when the job ends.
	held := g.storage.hold()
	writing := g.commitAsync(func(tx durable.Tx) (any, error) {
		g.session.OnJobEnd(func() { record("write ended") })
		return appendChange(id, "x")(tx)
	})
	<-held.Entered()
	readEnded := make(chan struct{})
	reading := g.commitAsync(func(durable.Tx) (any, error) {
		g.session.OnJobEnd(func() { record("read ended"); close(readEnded) })
		return nil, nil
	})
	<-readEnded
	mu.Lock()
	before := slices.Clone(events)
	mu.Unlock()
	// The read's outcome settled with the job; the write's waits for its publication.
	expectNames(t, before, "off the line", "read ended")
	held.Release()
	if _, err := writing.wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := reading.wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	expectNames(t, events, "off the line", "read ended", "published", "write ended")
	g.close()
}

func TestKnownLatestEntryIdFollowsTheSessionsOwnCommits(t *testing.T) {
	g := openGC(t, 2)
	id, other := g.ids[0], g.ids[1]
	// A conversation created by this Session has no entries, known without a read.
	if latest, known := g.session.KnownLatestEntryId(id); !known || latest != nil {
		t.Fatalf("known latest of a new conversation = %v, %v; want none, known", latest, known)
	}
	if latest, known := g.session.KnownLatestEntryId(durable.ConversationId(99_999)); known || latest != nil {
		t.Fatalf("known latest of an unknown conversation = %v, %v; want unknown", latest, known)
	}
	appended, err := g.session.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(id, durable.EntryDraft{Kind: "note"}) })
	must(t, err)
	entry := appended.(durable.EntryRecord)
	latest, known := g.session.KnownLatestEntryId(id)
	if !known || latest == nil || *latest != entry.Id {
		t.Fatalf("known latest after an append = %v, %v; want %d", latest, known, entry.Id)
	}
	if latest, known := g.session.KnownLatestEntryId(other); !known || latest != nil {
		t.Fatalf("known latest of the other conversation = %v, %v; want none, known", latest, known)
	}
	// Read through a transaction, the same answer without a Storage read.
	read, err := g.session.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.LatestEntryId(id) })
	must(t, err)
	if got, _ := read.(*durable.EntryId); got == nil || *got != entry.Id {
		t.Fatalf("Tx.LatestEntryId = %v, want %d", read, entry.Id)
	}
	g.close()
}

func TestTouchGatesAKeyUntilItsWriterIsPublished(t *testing.T) {
	g := openGC(t, 1)
	id := g.ids[0]
	var gateOpen = make(chan (<-chan struct{}), 1)
	if gate := g.session.Touch(session.AllKeys); gate != nil {
		t.Fatal("a Session with nothing unpublished has a gate")
	}
	if drained := g.session.Drained(); drained != nil {
		t.Fatal("a Session with nothing unpublished is not drained")
	}
	held := g.storage.hold()
	writing := g.commitAsync(appendChange(id, "x"))
	<-held.Entered()
	if gate := g.session.Touch(session.AppendKey(id)); gate == nil {
		t.Fatal("the append key of a conversation with an unpublished append has no gate")
	} else {
		gateOpen <- gate
	}
	if gate := g.session.Touch(session.AppendKey(durable.ConversationId(12_345))); gate != nil {
		t.Fatal("an unrelated key has a gate")
	}
	if gate := g.session.Touch(session.AllKeys); gate == nil {
		t.Fatal("the line is not drained while a commit is in flight")
	}
	if sizes := g.session.LineSizes(); sizes.Unpublished == 0 {
		t.Fatalf("line sizes %+v, want unpublished keys", sizes)
	}
	waiting := spawn(func(ctx ctxT) (any, error) { return nil, g.session.AwaitTouch(ctx, session.AppendKey(id)) })
	if waiting.settled() {
		t.Fatal("AwaitTouch returned while the writer was unpublished")
	}
	held.Release()
	if _, err := writing.wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := waiting.wait(); err != nil {
		t.Fatal(err)
	}
	<-<-gateOpen
	if gate := g.session.Touch(session.AppendKey(id)); gate != nil {
		t.Fatal("a published key still has a gate")
	}
	g.close()
}

func TestAWakeADeferredPublicationRunsIsEnteredBeforeTheNextPartIsPublished(t *testing.T) {
	g := openGC(t, 3)
	a, b, c := g.ids[0], g.ids[1], g.ids[2]
	var (
		mu     sync.Mutex
		events []string
	)
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	wake := make(chan struct{})
	resumed := make(chan struct{})
	// The waiter's wait is a wait of the Session: the publication that wakes it waits until its continuation, which records and
	// goes on to wait for something else, ran.
	// It is slow to start the continuation: the publication still waits for it.
	start := make(chan struct{})
	waiter := spawnContinuing(func(ctx ctxT, continuation *session.Continuation) (any, error) {
		g.session.Await(ctx, wake)
		<-start
		record("waiter resumed")
		continuation.Entered()
		<-resumed
		return nil, nil
	})
	var published int
	g.session.FollowCommits(func(_ ctxT, _ session.ObservedCommit) {
		published++
		record("published " + string(rune('0'+published)))
		if published == 2 {
			// A wait the second commit resolves: woken after the publication's host dispatch.
			g.session.DeferUntilPublished(func() { close(wake) })
		}
	})
	quiesce(waiter)
	held := g.storage.hold()
	first := g.bump(a)
	<-held.Entered()
	second := g.bump(b)
	third := g.bump(c)
	quiesce(second, third)
	g.awaitQueued(2)
	held.Release()
	g.awaitWakeHop(2)
	close(start)
	for _, started := range []*call{first, second, third} {
		if _, err := started.wait(); err != nil {
			t.Fatal(err)
		}
	}
	close(resumed)
	if _, err := waiter.wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// What the second commit's publication woke ran up to its next wait before the third commit was published.
	expectNames(t, events, "published 1", "published 2", "waiter resumed", "published 3")
	g.close()
}

func TestAReadOnlySessionKnowsNoTailsAndRefusesCommitsWithWrites(t *testing.T) {
	g := openGCHooks(t, newBatchStorage(), 1, func(storage *batchStorage) durable.Storage { return storage }, session.Hooks{})
	g.close()
	readOnly := session.NewSessionImpl(g.storage.ControlledStorage, session.Hooks{ReadOnly: true}, nil, nil)
	g.storage.Reopen()
	if latest, known := readOnly.KnownLatestEntryId(g.ids[0]); known || latest != nil {
		t.Fatalf("a read-only Session knows a newest entry: %v, %v", latest, known)
	}
	_, err := readOnly.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.AppendEntry(g.ids[0], durable.EntryDraft{Kind: "x"}) })
	expectErrorContains(t, err, "Read-only")
	must(t, readOnly.Close(ctx))
}
