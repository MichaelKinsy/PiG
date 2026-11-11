package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
)

// BenchmarkStoreStoragePublishing is the cost of one commit's publication: plainly (no host), and in the attached host's batch
// whose section runs its work inline (the host's own is a loop turn: this measures what Publishing itself adds).
func BenchmarkStoreStoragePublishing(b *testing.B) {
	for _, attached := range []bool{false, true} {
		name := "plain"
		if attached {
			name = "batch"
		}
		b.Run(name, func(b *testing.B) {
			store := memoryStore(b).(*chordstore.ChordStore)
			storage := value(Open(background, store, "harness"))
			if attached {
				batch := state.NewBatch(func(state.BatchError) {}, nil).UseSection(func(work func()) { work() }, nil)
				must(b, store.Attach(chordstore.Attachment{Batch: batch}))
				b.Cleanup(store.Detach)
			}
			published := 0
			b.ReportAllocs()
			for b.Loop() {
				storage.Publishing(background, func() { published++ })
			}
			if published == 0 {
				b.Fatal("nothing published")
			}
		})
	}
}
