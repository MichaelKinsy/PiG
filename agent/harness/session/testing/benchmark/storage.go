package benchmark

import (
	"context"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

const (
	messageTimestamp = 1_650_000_000_000
	seedBatchSize    = 250
)

var writeBaselineDataset = StorageBenchmarkDatasets[0]

func createEntry(index, payloadBytes int) session.Entry {
	id := StorageBenchmarkEntryID(index)
	prefix := id + ":"
	var parentID *string
	if index > 0 {
		parentID = new(StorageBenchmarkEntryID(index - 1))
	}
	text := prefix + strings.Repeat("x", max(0, payloadBytes-len(prefix)))
	return session.Entry{ID: id, ParentID: parentID, Type: session.EntryTypeMessage, Message: agent.AgentMessage{
		User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: text}}, Timestamp: messageTimestamp},
	}}
}

func createStorageBenchmarkTransaction(startIndex, entryCount, payloadBytes int) []session.Write {
	writes := make([]session.Write, entryCount)
	for offset := range writes {
		writes[offset] = session.InsertEntry(createEntry(startIndex+offset, payloadBytes))
	}
	return writes
}

// GenerateStorageBenchmarkSeedTransactions returns the deterministic seed
// transactions of a dataset; it claims no production data distribution.
func GenerateStorageBenchmarkSeedTransactions(dataset StorageBenchmarkDataset) [][]session.Write {
	var transactions [][]session.Write
	for startIndex := 0; startIndex < dataset.EntryCount; startIndex += seedBatchSize {
		transactions = append(transactions, createStorageBenchmarkTransaction(startIndex, min(seedBatchSize, dataset.EntryCount-startIndex), dataset.PayloadBytes))
	}
	return transactions
}

// SeedStorageBenchmark seeds one deterministic synthetic linear branch.
func SeedStorageBenchmark(ctx context.Context, storage session.Storage, dataset StorageBenchmarkDataset) error {
	for _, transaction := range GenerateStorageBenchmarkSeedTransactions(dataset) {
		if _, err := storage.Commit(ctx, transaction); err != nil {
			return err
		}
	}
	return nil
}

// StorageReadBenchmarkScenario is a steady-state read against a seeded
// fixture; Run returns a count so each result is consumed.
type StorageReadBenchmarkScenario struct {
	Name           string
	ExpectedResult func(dataset StorageBenchmarkDataset) int
	Run            func(ctx context.Context, storage session.Storage, dataset StorageBenchmarkDataset) (int, error)
}

// StorageWriteBenchmarkScenario is a write run once against each
// independently prepared fixture.
type StorageWriteBenchmarkScenario struct {
	Name       string
	WriteCount int
	// Prepare seeds the fixture before timing; nil means no preparation.
	Prepare func(ctx context.Context, storage session.Storage) error
	Run     func(ctx context.Context, storage session.Storage) (int, error)
}

var appendedEntryID = StorageBenchmarkEntryID(writeBaselineDataset.EntryCount)

func mixedAppendTransaction() []session.Write {
	writes := createStorageBenchmarkTransaction(writeBaselineDataset.EntryCount, 1, writeBaselineDataset.PayloadBytes)
	return append(writes,
		session.SetValue(session.BranchTip("main"), new(appendedEntryID)),
		session.InsertUsage(session.UsageRow{ID: "benchmark-usage", EntryID: new(appendedEntryID), Usage: ai.Usage{
			Input: 1_000, Output: 250, CacheRead: 500, TotalTokens: 1_250,
			Cost: ai.UsageCost{Input: 0.001, Output: 0.001, CacheRead: 0.0001, Total: 0.0021},
		}}),
	)
}

// StorageReadBenchmarkScenarios are the shared read scenarios.
var StorageReadBenchmarkScenarios = []StorageReadBenchmarkScenario{
	{
		Name:           "get 100 distributed entries",
		ExpectedResult: func(dataset StorageBenchmarkDataset) int { return len(dataset.LookupIDs) },
		Run: func(ctx context.Context, storage session.Storage, dataset StorageBenchmarkDataset) (int, error) {
			entries, err := storage.GetEntries(ctx, dataset.LookupIDs)
			return len(entries), err
		},
	},
	{
		Name:           "scan latest 50 entries",
		ExpectedResult: func(dataset StorageBenchmarkDataset) int { return min(50, dataset.EntryCount) },
		Run: func(ctx context.Context, storage session.Storage, _ StorageBenchmarkDataset) (int, error) {
			entries, err := storage.ScanEntries(ctx, session.EntryScan{Order: session.OrderDesc, Limit: new(50)})
			return len(entries), err
		},
	},
	{
		Name:           "scan full branch structure",
		ExpectedResult: func(dataset StorageBenchmarkDataset) int { return dataset.EntryCount },
		Run: func(ctx context.Context, storage session.Storage, dataset StorageBenchmarkDataset) (int, error) {
			entries, err := storage.ScanBranchStructure(ctx, session.StorageBranchScan{Start: dataset.TipID, Order: session.OrderNewestFirst})
			return len(entries), err
		},
	},
}

func commitCount(ctx context.Context, storage session.Storage, writes []session.Write) (int, error) {
	result, err := storage.Commit(ctx, writes)
	return len(result.Seqs), err
}

// StorageWriteBenchmarkScenarios are the shared writes; every invocation
// receives equivalent pre-benchmark state.
var StorageWriteBenchmarkScenarios = []StorageWriteBenchmarkScenario{
	{
		Name: "commit one message entry", WriteCount: 1,
		Run: func(ctx context.Context, storage session.Storage) (int, error) {
			return commitCount(ctx, storage, createStorageBenchmarkTransaction(0, 1, 256))
		},
	},
	{
		Name: "commit 100 message entries", WriteCount: 100,
		Run: func(ctx context.Context, storage session.Storage) (int, error) {
			return commitCount(ctx, storage, createStorageBenchmarkTransaction(0, 100, 256))
		},
	},
	{
		Name: "commit mixed append (" + writeBaselineDataset.Name + ")", WriteCount: 3,
		Prepare: func(ctx context.Context, storage session.Storage) error {
			return SeedStorageBenchmark(ctx, storage, writeBaselineDataset)
		},
		Run: func(ctx context.Context, storage session.Storage) (int, error) {
			return commitCount(ctx, storage, mixedAppendTransaction())
		},
	},
}
