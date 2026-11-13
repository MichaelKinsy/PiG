package storage_test

// pi: packages/durable/src/testing/storage-benchmark.ts

import (
	"context"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// mintRecorder records every id the seed mints, in call order.
type mintRecorder struct {
	durable.Storage
	mu     sync.Mutex
	minted []int64
}

func (r *mintRecorder) MintId() (int64, error) {
	id, err := r.Storage.MintId()
	r.mu.Lock()
	r.minted = append(r.minted, id)
	r.mu.Unlock()
	return id, err
}

// storage-benchmark.ts seedStorageBenchmark (:89-270) awaits its seeding commits in a fixed order and mints ids in call order
// (its two Promise.all calls join only mintId calls), so the ids are consecutive allocation order: entries, tasks, documents,
// the replay documents in tail order, the history document, then each fork's conversation followed by its entries; the
// dataset names those ids.
func TestSeedStorageBenchmarkMintsConsecutiveIdsInSeedingOrder(t *testing.T) {
	recorder := &mintRecorder{Storage: createFixture(t, "memory")}
	scale := durabletest.STORAGE_MEMORY_SCALES[0]
	dataset, err := durabletest.SeedStorageBenchmark(context.Background(), recorder, scale)
	if err != nil {
		t.Fatal(err)
	}
	minted := recorder.minted
	// The root conversation has a fixed id; every other primary record mints one.
	if want := durabletest.StorageBenchmarkPrimaryRecordCount(scale) - 1; len(minted) != want {
		t.Fatalf("%d ids minted, want %d", len(minted), want)
	}
	for i := 1; i < len(minted); i++ {
		if minted[i] != minted[i-1]+1 {
			t.Fatalf("mint %d returned %d after %d: the ids are not consecutive", i, minted[i], minted[i-1])
		}
	}
	if got, want := dataset.FirstEntryId, durable.IdFromNumber[durable.EntryId](minted[0]); got != want {
		t.Errorf("FirstEntryId = %v, want the first minted id %v", got, want)
	}
	documents := scale.EntryCount + scale.TaskCount + scale.DocumentCount
	if got, want := dataset.ExactDocumentId, durable.IdFromNumber[durable.DocumentId](minted[documents-1]); got != want {
		t.Errorf("ExactDocumentId = %v, want the last of the seeded documents %v", got, want)
	}
	tails := slices.Sorted(maps.Keys(dataset.ReplayDocumentIds))
	for i, tail := range tails {
		if got, want := dataset.ReplayDocumentIds[tail], durable.IdFromNumber[durable.DocumentId](minted[documents+i]); got != want {
			t.Errorf("replay document of tail %d = %v, want %v: replay documents are minted in tail order right after the documents", tail, got, want)
		}
	}
	if got, want := dataset.HistoricalDocumentId, durable.IdFromNumber[durable.DocumentId](minted[documents+len(tails)]); got != want {
		t.Errorf("HistoricalDocumentId = %v, want the id minted right after the replay documents, %v", got, want)
	}
	// The last fork's conversation is minted before its 32 entries.
	if got, want := dataset.DeepestConversationId, durable.IdFromNumber[durable.ConversationId](minted[len(minted)-33]); got != want {
		t.Errorf("DeepestConversationId = %v, want the conversation minted before the last fork's 32 entries, %v", got, want)
	}
	if dataset.AncientAt >= dataset.RecentAt {
		t.Errorf("AncientAt %d is not before RecentAt %d: the history commits were not made in order", dataset.AncientAt, dataset.RecentAt)
	}
}
