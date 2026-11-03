// Ports packages/durable/src/harness/transcript.ts EntryLoader and ConversationTranscript as the contract their doc comments
// state: the loader is asked for exactly the missing range, concurrent callers share one load and give up independently
// (a waiter's ctx ends only its own wait), a failed load is reported to every waiter of it, and the transcript is safe for
// concurrent use. Upstream's transcript.ts awaits `load(...)` inside a shared promise (`#earlier`, `#filling`); a waiter that
// gives up is a Go ctx cancelled, which Pi has no counterpart of, so the contract is asserted on the Go form.

package harness

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// waitingContext reports, through waiting, the moment a caller starts waiting on it: LoadEarlier and FillGap select on Done
// only to wait for a load that another caller runs, so the signal says the caller joined that load.
type waitingContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func newWaitingContext(parent context.Context) *waitingContext {
	return &waitingContext{Context: parent, waiting: make(chan struct{})}
}

func (ctx *waitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

// A loader receives the exclusive bounds of the missing range as the inclusive bounds of EntryQuery, the order the page
// is read in, the transcript's page size as the limit, and never a conversation ID (EntryLoader: query.ConversationId is
// ignored).
func TestEntryLoaderIsAskedForExactlyTheMissingRange(t *testing.T) {
	all := transcriptEntries(100)
	inner := &countingLoader{entries: all}
	type asked struct {
		query durable.EntryQuery
		limit int
	}
	var mu sync.Mutex
	var queries []asked
	var load EntryLoader = func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		mu.Lock()
		queries = append(queries, asked{query, limit})
		mu.Unlock()
		return inner.load(ctx, query, limit)
	}
	open := func() *ConversationTranscript { return newTranscript(t, load, 7) }
	transcript := open()
	transcript.Merge(windowOf(all, 40, 49))
	stalled := transcript.Merge(windowOf(all, 90, 99))
	gap := firstGap(t, stalled)

	must(transcript.LoadEarlier(testContext))
	must(transcript.FillGap(testContext, gap))

	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 2 {
		t.Fatalf("%d loads, want 2: %+v", len(queries), queries)
	}
	earlier, filling := queries[0], queries[1]
	if earlier.limit != 7 || filling.limit != 7 {
		t.Fatalf("limits %d and %d, want the page size 7", earlier.limit, filling.limit)
	}
	if earlier.query.ConversationId != 0 || filling.query.ConversationId != 0 {
		t.Fatalf("a load carried conversation %d and %d, want none", earlier.query.ConversationId, filling.query.ConversationId)
	}
	// LoadEarlier reads the page below the oldest held entry, newest first.
	if earlier.query.MinEntryId != nil || earlier.query.MaxEntryId == nil || *earlier.query.MaxEntryId != all[39].Id ||
		earlier.query.Order == nil || *earlier.query.Order != durable.ScanDescending {
		t.Fatalf("loadEarlier query %+v, want max %d, descending", earlier.query, all[39].Id)
	}
	// FillGap reads the entries strictly between After and UpTo, oldest first.
	if filling.query.MinEntryId == nil || *filling.query.MinEntryId != gap.After+1 || filling.query.MaxEntryId == nil || *filling.query.MaxEntryId != gap.UpTo-1 ||
		filling.query.Order == nil || *filling.query.Order != durable.ScanAscending {
		t.Fatalf("fillGap query %+v for %+v, want %d..%d, ascending", filling.query, gap, gap.After+1, gap.UpTo-1)
	}
}

// Callers of LoadEarlier share one load; a caller whose ctx ends stops waiting with the cause and leaves the load, and the
// other callers, alone.
func TestConversationTranscriptWaitersGiveUpIndependentlyOfTheSharedLoad(t *testing.T) {
	all := transcriptEntries(30)
	gated := newGatedLoader(all)
	var load EntryLoader = gated.load
	open := func() *ConversationTranscript { return newTranscript(t, load) }
	transcript := open()
	transcript.Merge(windowOf(all, 20, 29))

	leader := startPending(func() (*TranscriptView, error) { return transcript.LoadEarlier(testContext) })
	for gated.entered.Load() != 1 {
		runtime.Gosched() // until the leader is inside the loader, holding the shared load
	}

	reason := errors.New("the client navigated away")
	giveUp, cancel := context.WithCancelCause(testContext)
	joining := newWaitingContext(giveUp)
	follower := startPending(func() (*TranscriptView, error) { return transcript.LoadEarlier(joining) })
	<-joining.waiting
	cancel(reason)
	<-follower.done
	if !errors.Is(follower.err, reason) || follower.view != nil {
		t.Fatalf("follower = %v, %v, want the cancellation cause", follower.view, follower.err)
	}
	select {
	case <-leader.done:
		t.Fatal("a follower giving up ended the shared load")
	default:
	}

	// A caller arriving after the cancellation still joins the same load.
	lateContext := newWaitingContext(testContext)
	late := startPending(func() (*TranscriptView, error) { return transcript.LoadEarlier(lateContext) })
	<-lateContext.waiting
	close(gated.gate)
	first, second := leader.wait(t), late.wait(t)
	if first != second {
		t.Fatal("the callers of one load got different views")
	}
	expectShape(t, first, transcriptIds(all))
	if got := gated.entered.Load(); got != 1 {
		t.Fatalf("%d loads for three callers, want 1", got)
	}
}

// A failed shared load is the error of every caller waiting for it; the next call loads again (nothing remembers the failure).
func TestConversationTranscriptSharedLoadFailureReachesEveryWaiter(t *testing.T) {
	all := transcriptEntries(30)
	inner := &countingLoader{entries: all}
	release := make(chan struct{})
	var entered atomic.Int64
	failure := errors.New("connection reset")
	var failing atomic.Bool
	failing.Store(true)
	var load EntryLoader = func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		entered.Add(1)
		<-release
		if failing.Load() {
			return durable.Page[durable.EntryRecord, durable.Cursor]{}, failure
		}
		return inner.load(ctx, query, limit)
	}
	open := func() *ConversationTranscript { return newTranscript(t, load) }
	transcript := open()
	transcript.Merge(windowOf(all, 20, 29))

	waiters := make([]*pendingView, 4)
	waiters[0] = startPending(func() (*TranscriptView, error) { return transcript.LoadEarlier(testContext) })
	for entered.Load() != 1 {
		runtime.Gosched() // until the first caller is inside the loader
	}
	joined := make([]*waitingContext, len(waiters))
	for i := 1; i < len(waiters); i++ {
		joined[i] = newWaitingContext(testContext)
		waiters[i] = startPending(func() (*TranscriptView, error) { return transcript.LoadEarlier(joined[i]) })
	}
	for _, each := range joined[1:] {
		<-each.waiting
	}
	close(release)
	for i, waiter := range waiters {
		<-waiter.done
		if !errors.Is(waiter.err, failure) || waiter.view != nil {
			t.Fatalf("waiter %d = %v, %v, want %v", i, waiter.view, waiter.err, failure)
		}
	}
	if got := entered.Load(); got != 1 {
		t.Fatalf("%d loads for four callers, want 1", got)
	}
	failing.Store(false)
	expectShape(t, must(transcript.LoadEarlier(testContext)), transcriptIds(all))
	if got := entered.Load(); got != 2 {
		t.Fatalf("%d loads after the retry, want 2", got)
	}
}

// Merge, LoadEarlier, FillGap, Newest and Resume run on any goroutine; the entries all of them add end up once each, in ID order.
func TestConversationTranscriptIsSafeForConcurrentUse(t *testing.T) {
	all := transcriptEntries(120)
	inner := &countingLoader{entries: all}
	var load EntryLoader = inner.load
	open := func() *ConversationTranscript { return newTranscript(t, load, 10) }
	transcript := open()
	transcript.Merge(windowOf(all, 100, 119))

	var group sync.WaitGroup
	for worker := range 6 {
		group.Go(func() {
			for range 20 {
				switch worker % 3 {
				case 0:
					_, _ = transcript.LoadEarlier(testContext)
				case 1:
					transcript.Merge(windowOf(all, 100, 119))
					transcript.Resume()
				default:
					if newest, ok := transcript.Newest(); !ok || newest != all[119].Id {
						t.Errorf("newest = %d, %v, want %d", newest, ok, all[119].Id)
						return
					}
				}
			}
		})
	}
	group.Wait()
	view := must(transcript.LoadEarlier(testContext))
	for len(view.Items) > 0 && view.Items[0].Kind() == "earlier" {
		view = must(transcript.LoadEarlier(testContext))
	}
	expectShape(t, view, transcriptIds(all))
}
