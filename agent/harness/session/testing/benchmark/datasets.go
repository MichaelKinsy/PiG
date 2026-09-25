// Package benchmark holds the deterministic session workloads shared by every
// backend's storage and repository benchmarks.
package benchmark

import "fmt"

// StorageBenchmarkDataset is one synthetic linear branch.
type StorageBenchmarkDataset struct {
	Name         string
	EntryCount   int
	PayloadBytes int
	LookupIDs    []string
	TipID        string
}

// StorageBenchmarkEntryID is the deterministic id shared by dataset and
// transaction generation.
func StorageBenchmarkEntryID(index int) string {
	return fmt.Sprintf("benchmark-entry-%08d", index)
}

func createDataset(scale string, entryCount int) StorageBenchmarkDataset {
	lookupCount := min(100, entryCount)
	lookupIDs := make([]string, lookupCount)
	for index := range lookupIDs {
		lookupIDs[index] = StorageBenchmarkEntryID(index * (entryCount - 1) / max(1, lookupCount-1))
	}
	return StorageBenchmarkDataset{
		Name:         "synthetic linear branch: " + scale + ", 256-byte payloads",
		EntryCount:   entryCount,
		PayloadBytes: 256,
		LookupIDs:    lookupIDs,
		TipID:        StorageBenchmarkEntryID(entryCount - 1),
	}
}

// StorageBenchmarkDatasets are the deterministic synthetic linear branches
// shared by all storage measurements.
var StorageBenchmarkDatasets = []StorageBenchmarkDataset{
	createDataset("1k entries", 1_000),
	createDataset("10k entries", 10_000),
	createDataset("100k entries", 100_000),
}
