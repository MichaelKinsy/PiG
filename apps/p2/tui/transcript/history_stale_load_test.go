package transcript

// The store's loads (`LoadEarlier`, `FillGap`) end on a goroutine of their own while the view's watch goes on merging on another. Pi's store is one event loop, where
// a load's resumption is not interleaved with a later merge; here a merge that comes between the end of the load and its result reaching the store must not be
// undone by the older view the load produced.

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

func recordOf(id int) durable.EntryRecord {
	return durable.EntryRecord{Id: durable.EntryId(id), ConversationId: 1, Kind: "p2.input", Data: delta.JsonObjectOf("n", float64(id))}
}

// viewJSON is the server's JSON of a window of ids that starts after before (0: the conversation's first entry).
func viewJSON(t *testing.T, before int, ids ...int) delta.JsonValue {
	t.Helper()
	value := harness.ConversationView{Conversation: durable.ConversationRecord{Id: 1}, Entries: []durable.EntryRecord{}}
	for _, id := range ids {
		value.Entries = append(value.Entries, recordOf(id))
	}
	if before > 0 {
		value.Before = new(durable.EntryId(before))
	}
	encoded, err := durable.ToJsonValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func shownIDs(s *TranscriptStore) []int {
	var ids []int
	for _, item := range s.Value().View.Items {
		if entry, ok := item.(harness.TranscriptEntry); ok {
			ids = append(ids, int(entry.Entry.Id))
		}
	}
	return ids
}

// blockedOnTheStore runs fn on a goroutine and returns, once it waits for the store's lock (which the caller holds), a channel that closes when fn returns.
func blockedOnTheStore(t *testing.T, fn func()) (done <-chan struct{}) {
	t.Helper()
	ids := make(chan uint64, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ids <- goroutineid.Current()
		fn()
	}()
	if err := entered.AwaitBlocked(context.Background(), <-ids, finished); err != nil {
		t.Fatal(err)
	}
	return finished
}

func TestALoadThatEndsAfterANewerViewWasMergedKeepsTheNewerView(t *testing.T) {
	page := func(ids ...int) durable.Page[durable.EntryRecord, durable.Cursor] {
		var records []durable.EntryRecord
		for _, id := range ids {
			records = append(records, recordOf(id))
		}
		// No next page: the load covers all it was asked for.
		return durable.Page[durable.EntryRecord, durable.Cursor]{Items: records}
	}
	cases := []struct {
		name string
		// setup merges the views the load starts from; run is the load; loaded is its page; newer is the view merged while the load ends; want is what shows.
		setup  func(t *testing.T, s *TranscriptStore)
		run    func(s *TranscriptStore) error
		loaded durable.Page[durable.EntryRecord, durable.Cursor]
		newer  func(t *testing.T) delta.JsonValue
		want   []int
	}{
		{
			// The window 3-4 has 1-2 before it; the load fetches them while the window moves on to 4-5.
			name:   "LoadEarlier",
			setup:  func(t *testing.T, s *TranscriptStore) { s.apply(context.Background(), viewJSON(t, 2, 3, 4), false) },
			run:    func(s *TranscriptStore) error { return s.LoadEarlier() },
			loaded: page(2, 1),
			newer:  func(t *testing.T) delta.JsonValue { return viewJSON(t, 3, 4, 5) },
			want:   []int{1, 2, 3, 4, 5},
		},
		{
			// The store holds 1-2 and the window 5-6, with 3-4 missing between; the fill fetches them while the window moves on to 6-7.
			name: "FillGap",
			setup: func(t *testing.T, s *TranscriptStore) {
				s.apply(context.Background(), viewJSON(t, 0, 1, 2), false)
				s.apply(context.Background(), viewJSON(t, 4, 5, 6), true)
			},
			run:    func(s *TranscriptStore) error { return s.FillGap(harness.TranscriptGap{After: 2, UpTo: 5}) },
			loaded: page(3, 4),
			newer:  func(t *testing.T) delta.JsonValue { return viewJSON(t, 5, 6, 7) },
			want:   []int{1, 2, 3, 4, 5, 6, 7},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			release := make(chan struct{})
			load := func(context.Context, durable.EntryQuery, int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
				<-release
				return c.loaded, nil
			}
			s := NewTranscriptStore(TranscriptSource{Entries: load})
			c.setup(t, s)
			result := make(chan error, 1)
			loaderID := make(chan uint64, 1)
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				loaderID <- goroutineid.Current()
				result <- c.run(s)
			}()
			id := <-loaderID
			// The load waits in its loader, off the store's lock.
			entered.Await(id, ended)
			s.mu.Lock()
			newer := c.newer(t)
			merged := blockedOnTheStore(t, func() { s.apply(context.Background(), newer, true) })
			close(release)
			// The load ends and queues for the lock behind the merge, which therefore runs first.
			if err := entered.AwaitBlocked(context.Background(), id, ended); err != nil {
				t.Fatal(err)
			}
			s.mu.Unlock()
			<-merged
			<-ended
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if got := shownIDs(s); !slices.Equal(got, c.want) {
				t.Fatalf("shown %v, want %v", got, c.want)
			}
		})
	}
}
