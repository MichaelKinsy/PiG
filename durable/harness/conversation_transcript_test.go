// Ports packages/durable/test/conversation-transcript.test.ts: ConversationTranscript, the client-side layer that merges view
// windows into one transcript (Pi #11327).
//
// Go mappings forced by language mechanics. A returned Promise is a blocking call; "the same items" is the same slice (a pointer to
// its first element), and "the same view" is the same *TranscriptView. Two calls that must share one load run on two goroutines
// that wait on a gate the test releases once both are inside the call.

package harness

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

var transcriptConversation = durable.ConversationRecord{Id: 1}

// transcriptEntries are entries with IDs that have holes: every third ID is missing.
func transcriptEntries(count int) []durable.EntryRecord {
	var entries []durable.EntryRecord
	for id := durable.EntryId(1); len(entries) < count; id++ {
		if id%3 != 0 {
			entries = append(entries, durable.EntryRecord{Id: id, ConversationId: transcriptConversation.Id, Kind: "note"})
		}
	}
	return entries
}

// countingLoader is a loader over entries with Conversation.Entries semantics, counting its calls.
type countingLoader struct {
	entries []durable.EntryRecord
	calls   atomic.Int64
}

func (loader *countingLoader) load(_ context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	loader.calls.Add(1)
	var matching []durable.EntryRecord
	for _, entry := range loader.entries {
		if (query.MinEntryId == nil || entry.Id >= *query.MinEntryId) && (query.MaxEntryId == nil || entry.Id <= *query.MaxEntryId) {
			matching = append(matching, entry)
		}
	}
	if query.Order == nil || *query.Order != durable.ScanAscending {
		slices.Reverse(matching)
	}
	page := durable.Page[durable.EntryRecord, durable.Cursor]{Items: matching[:min(limit, len(matching))]}
	if len(matching) > limit {
		page.Next = &durable.Cursor{"more": true}
	}
	return page, nil
}

// gatedLoader is a loader that holds each call until its gate is closed, counting the calls that began.
type gatedLoader struct {
	inner   *countingLoader
	gate    chan struct{}
	entered atomic.Int64
}

func newGatedLoader(entries []durable.EntryRecord) *gatedLoader {
	return &gatedLoader{inner: &countingLoader{entries: entries}, gate: make(chan struct{})}
}

func (loader *gatedLoader) load(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	loader.entered.Add(1)
	<-loader.gate
	return loader.inner.load(ctx, query, limit)
}

// pendingView is a call of the transcript that runs on its own goroutine.
type pendingView struct {
	done chan struct{}
	view *TranscriptView
	err  error
}

func startPending(call func() (*TranscriptView, error)) *pendingView {
	pending := &pendingView{done: make(chan struct{})}
	go func() {
		defer close(pending.done)
		pending.view, pending.err = call()
	}()
	return pending
}

// wait returns the call's view once it ends.
func (pending *pendingView) wait(t *testing.T) *TranscriptView {
	t.Helper()
	<-pending.done
	if pending.err != nil {
		t.Fatal(pending.err)
	}
	return pending.view
}

func newTranscript(t *testing.T, load EntryLoader, pageSize ...int) *ConversationTranscript {
	t.Helper()
	var options *ConversationTranscriptOptions
	if len(pageSize) > 0 {
		options = &ConversationTranscriptOptions{PageSize: &pageSize[0]}
	}
	return must(NewConversationTranscript(load, options))
}

// windowOf is the view of a window: entries first..last of all, with the entry before them.
func windowOf(all []durable.EntryRecord, first, last int, docs ...ViewDocs) ConversationView {
	view := ConversationView{Conversation: transcriptConversation, Entries: all[first : last+1]}
	if first > 0 {
		view.Before = new(all[first-1].Id)
	}
	if len(docs) > 0 {
		view.Docs = docs[0]
	}
	return view
}

// transcriptShape is the items as a compact list: entry IDs, "gap after..upTo", and "earlier".
func transcriptShape(view *TranscriptView) []string {
	shape := make([]string, len(view.Items))
	for index, item := range view.Items {
		switch item := item.(type) {
		case TranscriptEntry:
			shape[index] = fmt.Sprint(item.Entry.Id)
		case TranscriptGap:
			shape[index] = fmt.Sprintf("gap %d..%d", item.After, item.UpTo)
		case TranscriptEarlier:
			shape[index] = "earlier"
		}
	}
	return shape
}

func transcriptIds(entries []durable.EntryRecord) []string {
	ids := make([]string, len(entries))
	for index, entry := range entries {
		ids[index] = fmt.Sprint(entry.Id)
	}
	return ids
}

func gapsOf(view *TranscriptView) []TranscriptGap {
	var gaps []TranscriptGap
	for _, item := range view.Items {
		if gap, ok := item.(TranscriptGap); ok {
			gaps = append(gaps, gap)
		}
	}
	return gaps
}

// firstGap is the first gap item of view; it fails the test when there is none.
func firstGap(t *testing.T, view *TranscriptView) TranscriptGap {
	t.Helper()
	gaps := gapsOf(view)
	if len(gaps) == 0 {
		t.Fatalf("no gap in %v", transcriptShape(view))
	}
	return gaps[0]
}

func sameItems(a, b *TranscriptView) bool {
	return len(a.Items) == len(b.Items) && (len(a.Items) == 0 || &a.Items[0] == &b.Items[0])
}

func expectShape(t *testing.T, view *TranscriptView, want ...[]string) {
	t.Helper()
	if got, wantList := transcriptShape(view), slices.Concat(want...); !slices.Equal(got, wantList) {
		t.Fatalf("items %v, want %v", got, wantList)
	}
}

func earlierThen(entries []durable.EntryRecord) []string {
	return slices.Concat([]string{"earlier"}, transcriptIds(entries))
}

func TestConversationTranscript(t *testing.T) {
	t.Run("shows a first window under an `earlier` marker, or alone when it starts the transcript", func(t *testing.T) {
		all := transcriptEntries(20)
		later := newTranscript(t, (&countingLoader{entries: all}).load)
		expectShape(t, later.Merge(windowOf(all, 15, 19)), earlierThen(all[15:]))
		if newest, present := later.Newest(); !present || newest != all[19].Id {
			t.Fatalf("newest %d %v, want %d", newest, present, all[19].Id)
		}
		whole := newTranscript(t, (&countingLoader{entries: all}).load)
		expectShape(t, whole.Merge(windowOf(all, 0, 19)), transcriptIds(all))
	})

	t.Run("appends, keeps entries the window dropped, and returns the same items when nothing is added", func(t *testing.T) {
		all := transcriptEntries(30)
		helper := newTranscript(t, (&countingLoader{entries: all}).load)
		docsA := ViewDocsOf(ViewDocs{delta.NewJsonObject(0)}, "a", durable.JsonObject(delta.JsonObjectOf("n", 1.0)))
		first := helper.Merge(windowOf(all, 10, 19, docsA))
		grown := helper.Merge(windowOf(all, 10, 21, docsA))
		expectShape(t, grown, earlierThen(all[10:22]))
		// The window dropped its oldest; the transcript keeps them.
		spliced := helper.Merge(windowOf(all, 18, 21, docsA))
		if !sameItems(spliced, grown) || spliced != grown {
			t.Fatal("a window that dropped its oldest entries returned another view")
		}
		// A docs-only change: a new view, the same items.
		docs := ViewDocsOf(ViewDocs{delta.NewJsonObject(0)}, "a", durable.JsonObject(delta.JsonObjectOf("n", 2.0)))
		streamed := helper.Merge(windowOf(all, 18, 21, docs))
		if streamed == grown {
			t.Fatal("a docs-only change returned the same view")
		}
		if !sameItems(streamed, grown) {
			t.Fatal("a docs-only change returned other items")
		}
		if streamed.Docs != docs {
			t.Fatal("the view does not carry the new docs")
		}
		if sameItems(first, grown) {
			t.Fatal("a merge that added entries returned the same items")
		}
	})

	t.Run("opens a gap when a window does not continue the transcript, and fills it page by page", func(t *testing.T) {
		all := transcriptEntries(200)
		load := &countingLoader{entries: all}
		helper := newTranscript(t, load.load, 50)
		helper.Merge(windowOf(all, 150, 159))
		// A stall: the next window starts well past the newest entry held.
		stalled := helper.Merge(windowOf(all, 190, 199))
		gap := firstGap(t, stalled)
		if gap != (TranscriptGap{After: all[159].Id, UpTo: all[190].Id}) {
			t.Fatalf("gap %+v", gap)
		}
		expectShape(t, stalled, []string{"earlier"}, transcriptIds(all[150:160]), []string{fmt.Sprintf("gap %d..%d", all[159].Id, all[190].Id)}, transcriptIds(all[190:]))
		// 30 entries are missing; one page loads them all and closes the gap.
		filled := must(helper.FillGap(testContext, gap))
		expectShape(t, filled, earlierThen(all[150:]))
		if load.calls.Load() != 1 {
			t.Fatalf("%d loads, want 1", load.calls.Load())
		}
	})

	t.Run("shrinks a gap from its start while more than a page is missing", func(t *testing.T) {
		all := transcriptEntries(200)
		helper := newTranscript(t, (&countingLoader{entries: all}).load, 10)
		helper.Merge(windowOf(all, 100, 109))
		view := helper.Merge(windowOf(all, 190, 199))
		view = must(helper.FillGap(testContext, firstGap(t, view)))
		if gap := firstGap(t, view); gap != (TranscriptGap{After: all[119].Id, UpTo: all[190].Id}) {
			t.Fatalf("gap %+v", gap)
		}
		for len(gapsOf(view)) > 0 {
			view = must(helper.FillGap(testContext, firstGap(t, view)))
		}
		expectShape(t, view, earlierThen(all[100:]))
	})

	t.Run("loads earlier pages until the start, then drops the marker", func(t *testing.T) {
		all := transcriptEntries(45)
		load := &countingLoader{entries: all}
		helper := newTranscript(t, load.load, 20)
		helper.Merge(windowOf(all, 40, 44))
		view := must(helper.LoadEarlier(testContext))
		expectShape(t, view, earlierThen(all[20:]))
		// A page without a next cursor reaches the start.
		view = must(helper.LoadEarlier(testContext))
		expectShape(t, view, transcriptIds(all))
		calls := load.calls.Load()
		if again := must(helper.LoadEarlier(testContext)); again != view {
			t.Fatal("loadEarlier at the start returned another view")
		}
		if load.calls.Load() != calls {
			t.Fatal("loadEarlier at the start loaded")
		}
	})

	t.Run("shares one load between concurrent calls of loadEarlier, and of fillGap for one gap", func(t *testing.T) {
		all := transcriptEntries(100)
		gated := newGatedLoader(all)
		helper := newTranscript(t, gated.load)
		helper.Merge(windowOf(all, 40, 49))
		stalled := helper.Merge(windowOf(all, 90, 99))
		gap := firstGap(t, stalled)
		earlier := []*pendingView{
			startPending(func() (*TranscriptView, error) { return helper.LoadEarlier(testContext) }),
			startPending(func() (*TranscriptView, error) { return helper.LoadEarlier(testContext) }),
		}
		fills := []*pendingView{
			startPending(func() (*TranscriptView, error) { return helper.FillGap(testContext, gap) }),
			startPending(func() (*TranscriptView, error) { return helper.FillGap(testContext, gap) }),
		}
		// One load of each kind is inside the loader; the other call of each joined it.
		eventually(t, func() bool { return gated.entered.Load() == 2 })
		flush()
		flush()
		if gated.entered.Load() != 2 {
			t.Fatalf("%d loads began, want 2", gated.entered.Load())
		}
		close(gated.gate)
		if earlier[0].wait(t) != earlier[1].wait(t) || fills[0].wait(t) != fills[1].wait(t) {
			t.Fatal("concurrent calls did not share one load")
		}
		if gated.inner.calls.Load() != 2 {
			t.Fatalf("%d loads, want 2", gated.inner.calls.Load())
		}
		expectShape(t, must(helper.LoadEarlier(testContext)), transcriptIds(all))
	})

	t.Run("resumes after `newest`: an empty window right after it adds nothing; one past it opens a gap", func(t *testing.T) {
		all := transcriptEntries(30)
		helper := newTranscript(t, (&countingLoader{entries: all}).load)
		view := helper.Merge(windowOf(all, 10, 19))
		newest, _ := helper.Newest()
		resumed := ConversationView{Conversation: transcriptConversation, Entries: []durable.EntryRecord{}, Before: &newest}
		if !sameItems(helper.Merge(resumed), view) {
			t.Fatal("an empty window right after newest changed the items")
		}
		// A window that is empty but starts past `newest`: the entries up to its `before` are missing.
		resumed.Before = new(all[25].Id)
		past := helper.Merge(resumed)
		shape := transcriptShape(past)
		if want := fmt.Sprintf("gap %d..%d", all[19].Id, all[25].Id+1); shape[len(shape)-1] != want {
			t.Fatalf("last item %s, want %s", shape[len(shape)-1], want)
		}
		if newest, _ := helper.Newest(); newest != all[25].Id {
			t.Fatalf("newest %d, want %d", newest, all[25].Id)
		}
		// The next window continues it.
		shape = transcriptShape(helper.Merge(windowOf(all, 26, 27)))
		if want := []string{fmt.Sprintf("gap %d..%d", all[19].Id, all[25].Id+1), fmt.Sprint(all[26].Id), fmt.Sprint(all[27].Id)}; !slices.Equal(shape[len(shape)-3:], want) {
			t.Fatalf("last items %v, want %v", shape[len(shape)-3:], want)
		}
	})

	t.Run("fills a gap from a window that covers it", func(t *testing.T) {
		all := transcriptEntries(100)
		helper := newTranscript(t, (&countingLoader{entries: all}).load)
		helper.Merge(windowOf(all, 40, 49))
		helper.Merge(windowOf(all, 90, 99))
		// A wider window, such as a new watch with a larger `max`, covers the second half of the gap.
		view := helper.Merge(windowOf(all, 70, 99))
		expectShape(t, view, []string{"earlier"}, transcriptIds(all[40:50]), []string{fmt.Sprintf("gap %d..%d", all[49].Id, all[70].Id)}, transcriptIds(all[70:]))
	})

	t.Run("rejects loadEarlier before the first merge and returns the view for a gap that is gone", func(t *testing.T) {
		all := transcriptEntries(10)
		helper := newTranscript(t, (&countingLoader{entries: all}).load)
		if _, err := helper.LoadEarlier(testContext); err == nil || err.Error() != "ConversationTranscript: merge a view first" {
			t.Fatalf("loadEarlier before a merge: %v", err)
		}
		view := helper.Merge(windowOf(all, 0, 9))
		if got := must(helper.FillGap(testContext, TranscriptGap{After: 1, UpTo: 2})); got != view {
			t.Fatal("a gap that is gone returned another view")
		}
	})

	t.Run("rejects a page size below 1", func(t *testing.T) {
		for _, size := range []int{0, -1} {
			if _, err := NewConversationTranscript((&countingLoader{}).load, &ConversationTranscriptOptions{PageSize: &size}); err == nil {
				t.Fatalf("page size %d was accepted", size)
			}
		}
	})
}

func TestConversationTranscriptLoads(t *testing.T) {
	t.Run("retries a load whose loader failed at once, for loadEarlier and fillGap", func(t *testing.T) {
		all := transcriptEntries(100)
		inner := &countingLoader{entries: all}
		var offline atomic.Bool
		offline.Store(true)
		var calls atomic.Int64
		load := func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
			calls.Add(1)
			// An adapter that checks its connection before it starts the request.
			if offline.Load() {
				return durable.Page[durable.EntryRecord, durable.Cursor]{}, errors.New("temporarily offline")
			}
			return inner.load(ctx, query, limit)
		}
		helper := newTranscript(t, load)
		helper.Merge(windowOf(all, 40, 49))
		stalled := helper.Merge(windowOf(all, 90, 99))
		if _, err := helper.LoadEarlier(testContext); err == nil || err.Error() != "temporarily offline" {
			t.Fatalf("loadEarlier: %v", err)
		}
		if _, err := helper.FillGap(testContext, firstGap(t, stalled)); err == nil || err.Error() != "temporarily offline" {
			t.Fatalf("fillGap: %v", err)
		}
		offline.Store(false)
		expectShape(t, must(helper.LoadEarlier(testContext)), transcriptIds(all[:50]), []string{fmt.Sprintf("gap %d..%d", all[49].Id, all[90].Id)}, transcriptIds(all[90:]))
		expectShape(t, must(helper.FillGap(testContext, firstGap(t, stalled))), transcriptIds(all))
		if calls.Load() != 4 {
			t.Fatalf("%d loads, want 4", calls.Load())
		}
	})

	t.Run("retries a load that rejected", func(t *testing.T) {
		all := transcriptEntries(30)
		inner := &countingLoader{entries: all}
		var fail atomic.Bool
		fail.Store(true)
		load := func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
			if fail.Load() {
				return durable.Page[durable.EntryRecord, durable.Cursor]{}, errors.New("timeout")
			}
			return inner.load(ctx, query, limit)
		}
		helper := newTranscript(t, load)
		helper.Merge(windowOf(all, 20, 29))
		if _, err := helper.LoadEarlier(testContext); err == nil || err.Error() != "timeout" {
			t.Fatalf("loadEarlier: %v", err)
		}
		fail.Store(false)
		expectShape(t, must(helper.LoadEarlier(testContext)), transcriptIds(all))
	})

	t.Run("keeps one gap for an empty window whose `before` lies inside it", func(t *testing.T) {
		all := transcriptEntries(100)
		helper := newTranscript(t, (&countingLoader{entries: all}).load)
		helper.Merge(windowOf(all, 40, 49))
		before := helper.Merge(windowOf(all, 90, 99))
		view := helper.Merge(ConversationView{Conversation: transcriptConversation, Entries: []durable.EntryRecord{}, Before: new(all[65].Id), Docs: before.Docs})
		if !sameItems(view, before) {
			t.Fatal("an empty window inside the gap changed the items")
		}
		if len(gapsOf(view)) != 1 {
			t.Fatalf("%d gaps, want 1", len(gapsOf(view)))
		}
	})

	t.Run("shares one load between a stale gap descriptor and the gap it shrank into", func(t *testing.T) {
		all := transcriptEntries(100)
		gated := newGatedLoader(all)
		close(gated.gate)
		helper := newTranscript(t, gated.load, 10)
		helper.Merge(windowOf(all, 10, 19))
		stale := firstGap(t, helper.Merge(windowOf(all, 90, 99)))
		current := firstGap(t, must(helper.FillGap(testContext, stale)))
		if current == stale {
			t.Fatal("the gap did not shrink")
		}
		loads := gated.inner.calls.Load()
		entered := gated.entered.Load()
		gated.gate = make(chan struct{})
		a := startPending(func() (*TranscriptView, error) { return helper.FillGap(testContext, stale) })
		b := startPending(func() (*TranscriptView, error) { return helper.FillGap(testContext, current) })
		eventually(t, func() bool { return gated.entered.Load() == entered+1 })
		flush()
		flush()
		if gated.entered.Load() != entered+1 {
			t.Fatalf("%d loads began, want 1", gated.entered.Load()-entered)
		}
		close(gated.gate)
		if a.wait(t) != b.wait(t) {
			t.Fatal("the stale descriptor and the current gap did not share one load")
		}
		if gated.inner.calls.Load() != loads+1 {
			t.Fatalf("%d loads, want %d", gated.inner.calls.Load(), loads+1)
		}
	})

	t.Run("treats an empty page with a next cursor as the end of the range", func(t *testing.T) {
		all := transcriptEntries(60)
		// A remote loader that always returns a cursor.
		inner := &countingLoader{entries: all}
		load := func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
			page, err := inner.load(ctx, query, limit)
			page.Next = &durable.Cursor{"more": true}
			return page, err
		}
		helper := newTranscript(t, load, 100)
		helper.Merge(windowOf(all, 10, 19))
		view := helper.Merge(windowOf(all, 40, 49))
		view = must(helper.FillGap(testContext, firstGap(t, view)))
		// The first page held every missing entry; the next one is empty and closes the gap.
		view = must(helper.FillGap(testContext, firstGap(t, view)))
		if len(gapsOf(view)) != 0 {
			t.Fatalf("a gap remains: %v", transcriptShape(view))
		}
		must(helper.LoadEarlier(testContext))
		view = must(helper.LoadEarlier(testContext))
		expectShape(t, view, transcriptIds(all[:50]))
	})

	t.Run("resumes with `resume`: nil before a merge, an empty window for an empty transcript, `from` after the newest", func(t *testing.T) {
		all := transcriptEntries(30)
		helper := newTranscript(t, (&countingLoader{entries: all}).load)
		if helper.Resume() != nil {
			t.Fatal("resume before a merge is not nil")
		}
		helper.Merge(ConversationView{Conversation: transcriptConversation, Entries: []durable.EntryRecord{}})
		if got := helper.Resume(); !reflect.DeepEqual(got, &ConversationWindow{}) {
			t.Fatalf("resume of an empty transcript %+v", got)
		}
		helper.Merge(windowOf(all, 0, 9))
		if got := helper.Resume(); !reflect.DeepEqual(got, &ConversationWindow{From: new(all[9].Id + 1)}) {
			t.Fatalf("resume %+v", got)
		}
	})
}

func TestConversationTranscriptOverAHarness(t *testing.T) {
	t.Run("follows the walkthrough: live appends, a stall, fillGap, loadEarlier, and a resume with `from`", func(t *testing.T) {
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		note := func() {
			commitValue(t, root, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
			})
		}
		for range 120 {
			note()
		}
		helper := newTranscript(t, func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
			return root.Entries(ctx, query, limit, nil)
		})
		window := ConversationWindow{Min: new(10), Max: new(20)}
		watch := must(root.Watch(testContext, &window))
		var mu sync.Mutex
		view := helper.Merge(watch.Value())
		watch.Start(func(_ context.Context, change durable.WatchChange[ConversationView]) error {
			merged := helper.Merge(change.Value)
			mu.Lock()
			defer mu.Unlock()
			view = merged
			return nil
		})
		current := func() *TranscriptView {
			mu.Lock()
			defer mu.Unlock()
			return view
		}
		for range 15 {
			note()
		}
		eventually(t, func() bool { return len(transcriptEntriesIn(current())) == 25 })
		if len(gapsOf(current())) != 0 {
			t.Fatalf("a gap in %v", transcriptShape(current()))
		}
		// A drop: the client misses 40 entries, more than `max`, and resumes after the newest it has.
		must(watch.Stop())
		for range 40 {
			note()
		}
		resumeWindow := window
		resumeWindow.From = helper.Resume().From
		resumed := must(root.Watch(testContext, &resumeWindow))
		merged := helper.Merge(resumed.Value())
		if len(gapsOf(merged)) != 1 {
			t.Fatalf("%d gaps, want 1", len(gapsOf(merged)))
		}
		for len(gapsOf(merged)) > 0 {
			merged = must(helper.FillGap(testContext, firstGap(t, merged)))
		}
		for len(merged.Items) > 0 {
			if _, earlier := merged.Items[0].(TranscriptEarlier); !earlier {
				break
			}
			merged = must(helper.LoadEarlier(testContext))
		}
		if got, want := transcriptIds(transcriptEntriesIn(merged)), transcriptIds(allEntries(t, root)); !slices.Equal(got, want) {
			t.Fatalf("transcript %v, want every entry of the conversation %v", got, want)
		}
		// A short drop resumes without a gap.
		for range 3 {
			note()
		}
		againWindow := window
		againWindow.From = helper.Resume().From
		again := must(root.Watch(testContext, &againWindow))
		if len(again.Value().Entries) != 3 {
			t.Fatalf("%d entries, want 3", len(again.Value().Entries))
		}
		merged = helper.Merge(again.Value())
		for _, item := range merged.Items {
			if _, entry := item.(TranscriptEntry); !entry {
				t.Fatalf("a row that is not an entry after a short drop: %v", transcriptShape(merged))
			}
		}
		must(resumed.Stop())
		must(again.Stop())
		closeHarness(t, harness)
	})
}

// transcriptEntriesIn is the entries among the items of view, in order.
func transcriptEntriesIn(view *TranscriptView) []durable.EntryRecord {
	var entries []durable.EntryRecord
	for _, item := range view.Items {
		if entry, ok := item.(TranscriptEntry); ok {
			entries = append(entries, entry.Entry)
		}
	}
	return entries
}
