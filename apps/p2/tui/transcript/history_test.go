package transcript_test

// test/p2/history.test.ts over the TUI's transcript store (tui/transcript/history.go; Pi's is src/web/history.ts, shared by the web and the TUI chat view): the
// window `Chat.view` holds, earlier pages and gaps through `Chat.entries`. What Go's store does not have: `seed`/`keptWindow` (the transcript kept on the device
// belongs to the web client; the TUI keeps nothing between runs), `loadThrough` (the web's jump to an entry) and the gap row of the web page (a browser test).
// What those tests protect (a view that continues what is held merges in, one that does not starts over) is tested through the views a reconnect sends. Pi's
// kept transcript that a live view starts past starts over; a live store's reconnect past what it holds leaves a gap instead (the second test), so that case
// has no Go counterpart.

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

func entry(id int) durable.EntryRecord {
	return durable.EntryRecord{Id: durable.EntryId(id), ConversationId: 1, Kind: "p2.input", Data: delta.JsonObjectOf("n", float64(id))}
}

// view is the server's JSON of a conversation view of ids; before is the entry just before the window, 0 for none.
func view(t *testing.T, before int, ids ...int) delta.JsonValue {
	t.Helper()
	value := harness.ConversationView{Conversation: durable.ConversationRecord{Id: 1}, Entries: []durable.EntryRecord{}}
	for _, id := range ids {
		value.Entries = append(value.Entries, entry(id))
	}
	if before > 0 {
		id := durable.EntryId(before)
		value.Before = &id
	}
	encoded, err := durable.ToJsonValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func rangeOf(from, to int) []int {
	ids := make([]int, 0, to-from+1)
	for id := from; id <= to; id++ {
		ids = append(ids, id)
	}
	return ids
}

// shown is the store's items as the test reads them: an entry's ID, `gap after-upTo`, or `earlier`.
func shown(store *transcript.TranscriptStore) []any {
	pages := store.Value()
	if pages == nil {
		return nil
	}
	var items []any
	for _, item := range pages.View.Items {
		switch item := item.(type) {
		case harness.TranscriptEntry:
			items = append(items, int(item.Entry.Id))
		case harness.TranscriptGap:
			items = append(items, "gap "+strconv.Itoa(int(item.After))+"-"+strconv.Itoa(int(item.UpTo)))
		case harness.TranscriptEarlier:
			items = append(items, "earlier")
		}
	}
	return items
}

func ids(from, to int, prefix ...any) []any {
	items := append([]any(nil), prefix...)
	for _, id := range rangeOf(from, to) {
		items = append(items, id)
	}
	return items
}

// liveView is a view watchable whose values the test sends, as Chord delivers them: snapshot as a whole value (a reconnect's `r`), ops as a change of the last
// one. A send returns once the store applied it, once the store's watch has started.
type liveView struct {
	mu       sync.Mutex
	value    delta.JsonValue
	listener func(context.Context, chord.WatchCall[delta.JsonValue]) error
	started  chan struct{}
	once     sync.Once
}

func newLiveView() *liveView { return &liveView{started: make(chan struct{})} }

func (l *liveView) Value() delta.JsonValue { return nil }

func (l *liveView) Watch(context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	return &liveWatch{live: l, closed: make(chan struct{})}, nil
}

func (l *liveView) send(value delta.JsonValue, ops []delta.Op) {
	<-l.started
	l.mu.Lock()
	listener := l.listener
	l.mu.Unlock()
	if err := listener(context.Background(), chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: value, Ops: ops}); err != nil {
		panic(err)
	}
}

func (l *liveView) snapshot(value delta.JsonValue) { l.send(value, []delta.Op{{"r", value}}) }

func (l *liveView) ops(value delta.JsonValue) { l.send(value, nil) }

type liveWatch struct {
	live   *liveView
	closed chan struct{}
	once   sync.Once
}

func (w *liveWatch) Value() delta.JsonValue { return nil }
func (w *liveWatch) Start(listener func(context.Context, chord.WatchCall[delta.JsonValue]) error) {
	w.live.mu.Lock()
	w.live.listener = listener
	w.live.mu.Unlock()
	w.live.once.Do(func() { close(w.live.started) })
}
func (w *liveWatch) Stop() (chord.WatchEnd, error) {
	w.once.Do(func() { close(w.closed) })
	return chord.WatchEnd{Kind: chord.WatchStopped}, nil
}
func (w *liveWatch) Closed() <-chan struct{} { return w.closed }
func (w *liveWatch) End() chord.WatchEnd     { return chord.WatchEnd{Kind: chord.WatchStopped} }

// query is a `Chat.entries` query as the store sent it.
type query struct {
	max   *durable.EntryId
	min   *durable.EntryId
	order durable.ScanOrder
	limit int
}

// entriesOf is `Chat.entries` over the entries all, recording each query.
func entriesOf(all ...int) (transcript.TranscriptSource, *[]query) {
	var mu sync.Mutex
	queries := &[]query{}
	load := func(_ context.Context, q durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		order := durable.ScanDescending
		if q.Order != nil {
			order = *q.Order
		}
		mu.Lock()
		*queries = append(*queries, query{max: q.MaxEntryId, min: q.MinEntryId, order: order, limit: limit})
		mu.Unlock()
		var matching []int
		for _, id := range all {
			if (q.MinEntryId == nil || durable.EntryId(id) >= *q.MinEntryId) && (q.MaxEntryId == nil || durable.EntryId(id) <= *q.MaxEntryId) {
				matching = append(matching, id)
			}
		}
		if order == durable.ScanDescending {
			slices.Reverse(matching)
		}
		page := durable.Page[durable.EntryRecord, durable.Cursor]{}
		for _, id := range matching[:min(limit, len(matching))] {
			page.Items = append(page.Items, entry(id))
		}
		if len(matching) > limit {
			page.Next = &durable.Cursor{}
		}
		return page, nil
	}
	return transcript.TranscriptSource{Entries: load}, queries
}

func store(live *liveView, source transcript.TranscriptSource) (*transcript.TranscriptStore, func()) {
	source.View = live
	s := transcript.NewTranscriptStore(source)
	return s, s.Subscribe(func() {})
}

func equal(t *testing.T, got, want []any) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("shown = %v, want %v", got, want)
	}
}

func TestTheWindowsEntriesStayAsItMovesEarlierPagesLoadUntilTheStart(t *testing.T) {
	live := newLiveView()
	source, queries := entriesOf(rangeOf(1, 120)...)
	s, stop := store(live, source)
	defer stop()
	live.snapshot(view(t, 70, rangeOf(71, 100)...))
	equal(t, shown(s), ids(71, 100, "earlier"))
	// The window drops its oldest: they stay.
	live.ops(view(t, 100, rangeOf(101, 120)...))
	equal(t, shown(s), ids(71, 120, "earlier"))
	if err := s.LoadEarlier(); err != nil {
		t.Fatal(err)
	}
	if len(*queries) != 1 || (*queries)[0].max == nil || *(*queries)[0].max != 70 || (*queries)[0].order != durable.ScanDescending || (*queries)[0].limit != 100 || (*queries)[0].min != nil {
		t.Fatalf("queries = %+v", *queries)
	}
	equal(t, shown(s), ids(1, 120))
	// At the start nothing loads.
	if err := s.LoadEarlier(); err != nil {
		t.Fatal(err)
	}
	if len(*queries) != 1 {
		t.Fatalf("queries = %+v", *queries)
	}
}

func TestAReconnectPastWhatIsHeldLeavesAGapThatFillsPageByPage(t *testing.T) {
	live := newLiveView()
	source, _ := entriesOf(rangeOf(1, 300)...)
	s, stop := store(live, source)
	defer stop()
	live.snapshot(view(t, 0, rangeOf(1, 50)...))
	// Much happened while disconnected: the new window starts past entry 50.
	live.snapshot(view(t, 250, rangeOf(251, 300)...))
	equal(t, shown(s), append(append(ids(1, 50), "gap 50-251"), ids(251, 300)...))
	gapOf := func() (harness.TranscriptGap, bool) {
		for _, item := range s.Value().View.Items {
			if gap, ok := item.(harness.TranscriptGap); ok {
				return gap, true
			}
		}
		return harness.TranscriptGap{}, false
	}
	// Each fill takes one page off the gap, until it is gone.
	fills := 0
	for gap, ok := gapOf(); ok; gap, ok = gapOf() {
		if err := s.FillGap(gap); err != nil {
			t.Fatal(err)
		}
		if fills++; fills > 10 {
			t.Fatal("the gap never filled")
		}
	}
	if fills < 2 {
		t.Fatalf("the gap of 200 entries filled in %d page(s)", fills)
	}
	equal(t, shown(s), ids(1, 300))
}

func TestALiveViewContinuingWhatIsHeldIsMergedIn(t *testing.T) {
	live := newLiveView()
	source, _ := entriesOf()
	s, stop := store(live, source)
	defer stop()
	live.snapshot(view(t, 0, rangeOf(1, 5)...))
	equal(t, shown(s), ids(1, 5))
	live.snapshot(view(t, 4, rangeOf(5, 7)...))
	equal(t, shown(s), ids(1, 7))
}

func TestAKeptTranscriptTheLiveViewDoesNotContinueOrOneNewerThanTheServersStartsOver(t *testing.T) {
	for name, c := range map[string]struct {
		held   []int
		before int
		window []int
	}{
		// The server was restored from an older backup and ends at 6.
		"newer than the server's": {rangeOf(1, 8), 4, []int{5, 6}},
		// An empty conversation on the server.
		"an empty conversation": {rangeOf(1, 3), 0, nil},
	} {
		t.Run(name, func(t *testing.T) {
			live := newLiveView()
			source, _ := entriesOf()
			s, stop := store(live, source)
			defer stop()
			live.snapshot(view(t, 0, c.held...))
			live.snapshot(view(t, c.before, c.window...))
			var want []any
			if c.before > 0 {
				want = []any{"earlier"}
			}
			for _, id := range c.window {
				want = append(want, id)
			}
			equal(t, shown(s), want)
		})
	}
}
