package storage_test

// pi: packages/durable/src/testing/storage-benchmark.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// Every read scenario of the benchmark suite runs through the public Storage contract (Conversation, ScanConversations,
// Entry, FindLatestHeadMarker, ScanEntries, ScanTasks, Submission, SubmissionByRequest, FindDocument, ScanDocuments)
// against deterministic seeded data and must return its expected count, on every backend.
func TestStorageReadContractAgainstSeededData(t *testing.T) {
	for _, backend := range storageBenchmarkBackends {
		t.Run(backend, func(t *testing.T) {
			createReadFixture(t, backend)
		})
	}
}

// The seeded record count is the sum of the dataset parts, and the memory scales are the 1k and 10k datasets.
func TestStorageBenchmarkScalesAndPrimaryRecordCount(t *testing.T) {
	if len(durabletest.STORAGE_MEMORY_SCALES) != 2 || durabletest.STORAGE_MEMORY_SCALES[0].Name != "1k" || durabletest.STORAGE_MEMORY_SCALES[1].Name != "10k" {
		t.Fatalf("scales = %+v", durabletest.STORAGE_MEMORY_SCALES)
	}
	small := durabletest.STORAGE_MEMORY_SCALES[0]
	large := durabletest.STORAGE_MEMORY_SCALES[1]
	base := durabletest.StorageBenchmarkPrimaryRecordCount(durabletest.StorageBenchmarkScale{})
	if base <= 0 {
		t.Fatalf("an empty scale still seeds the root conversation and fixed documents, got %d", base)
	}
	for _, scale := range []durabletest.StorageBenchmarkScale{small, large} {
		if got, want := durabletest.StorageBenchmarkPrimaryRecordCount(scale), base+scale.EntryCount+scale.TaskCount+scale.DocumentCount; got != want {
			t.Errorf("%s: count = %d, want fixed part + entries + tasks + documents = %d", scale.Name, got, want)
		}
	}
	// The count is the number of primary records the seed commits, observed through the storage it seeds.
	storage := createFixture(t, "memory")
	dataset, err := durabletest.SeedStorageBenchmark(benchmarkContext, storage, durabletest.TIMING_SCALE)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.FilteredTaskCount <= 0 || dataset.FilteredTaskCount > durabletest.TIMING_SCALE.TaskCount {
		t.Errorf("filtered task count %d outside 1..%d", dataset.FilteredTaskCount, durabletest.TIMING_SCALE.TaskCount)
	}
}
