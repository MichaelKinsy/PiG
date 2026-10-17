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
	// Pi storage-benchmark.ts:42-51: 1 root conversation + entryCount + taskCount + documentCount + REPLAY_TAILS.length (4)
	// + 1 + FORK_DEPTH (8) * (1 + ENTRIES_PER_FORK (32)); the fixed part of an empty scale is 1 + 4 + 1 + 8*33 = 270.
	if base != 270 {
		t.Fatalf("fixed part of the count = %d, want 270 from Pi's formula", base)
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
