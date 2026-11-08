package store

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
)

// A commit's writer calls from a goroutine of its own (the Session's), while the host's loop runs its own commits on the
// batch (its sources, deliveries, derives): the batch has no locks, the host serializes every call to it. So the
// publication must run as a section of the host (`Batch.Section`), never on the writer's goroutine, as Pi's continuation
// after the commit's `await` runs on its one event loop. Found by `-race` in p2's reload case of m1.test.ts: the writer's
// publication and the loop's commit touched the batch's fields at once.
func TestStoreStoragePublishesInASectionOfTheHost(t *testing.T) {
	store := memoryStore(t).(*chordstore.ChordStore)
	storage := value(Open(background, store, "harness"))
	// The host's way to run a section: one at a time, as its loop does.
	var loop sync.Mutex
	var inSection atomic.Int32
	var sections atomic.Int32
	batch := state.NewBatch(func(state.BatchError) {}, nil).UseSection(func(work func()) {
		loop.Lock()
		defer loop.Unlock()
		sections.Add(1)
		inSection.Add(1)
		defer inSection.Add(-1)
		work()
	}, func() bool { return inSection.Load() > 0 })
	must(t, store.Attach(chordstore.Attachment{Batch: batch}))
	t.Cleanup(store.Detach)

	const writers = 8
	var group sync.WaitGroup
	published := make(chan bool, writers)
	for range writers {
		group.Go(func() {
			storage.Publishing(background, func() { published <- inSection.Load() == 1 })
		})
	}
	group.Wait()
	close(published)
	count := 0
	for inside := range published {
		count++
		if !inside {
			t.Error("a publication ran outside a section of the host")
		}
	}
	if count != writers || sections.Load() != writers {
		t.Fatalf("%d publications in %d sections, want %d of each", count, sections.Load(), writers)
	}
}
