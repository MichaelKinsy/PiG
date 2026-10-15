package durabletest

// Ports packages/durable/src/testing/storage-benchmark.ts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// StorageBenchmarkScale is the size of a seeded benchmark dataset.
type StorageBenchmarkScale struct {
	Name          string
	EntryCount    int
	TaskCount     int
	DocumentCount int
}

// STORAGE_MEMORY_SCALES are the dataset sizes used for memory measurements.
var STORAGE_MEMORY_SCALES = []StorageBenchmarkScale{
	{Name: "1k", EntryCount: 1_000, TaskCount: 200, DocumentCount: 200},
	{Name: "10k", EntryCount: 10_000, TaskCount: 2_000, DocumentCount: 2_000},
}

// TIMING_SCALE is the dataset size used for timing measurements.
var TIMING_SCALE = StorageBenchmarkScale{Name: "timing", EntryCount: 1_000, TaskCount: 300, DocumentCount: 300}

var replayTails = []int{0, 16, 128, 1_024}

const (
	historySegmentLength = 128
	forkDepth            = 8
	entriesPerFork       = 32
	batchSize            = 100
)

// StorageBenchmarkPrimaryRecordCount is the number of primary records that SeedStorageBenchmark creates at scale.
func StorageBenchmarkPrimaryRecordCount(scale StorageBenchmarkScale) int {
	return 1 + scale.EntryCount + scale.TaskCount + scale.DocumentCount + len(replayTails) + 1 + forkDepth*(1+entriesPerFork)
}

// StorageBenchmarkDataset identifies the seeded records that read benchmarks address.
type StorageBenchmarkDataset struct {
	FirstEntryId          durable.EntryId
	FilteredTaskCount     int
	ExactDocumentId       durable.DocumentId
	ExactDocumentKey      string
	ReplayDocumentIds     map[int]durable.DocumentId
	HistoricalDocumentId  durable.DocumentId
	AncientAt             durable.Seq
	RecentAt              durable.Seq
	DeepestConversationId durable.ConversationId
	AncestorHeadEntryId   durable.EntryId
}

func benchmarkTask(id durable.TaskId, index int) storedTask {
	statuses := []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskTerminal}
	status := statuses[index%len(statuses)]
	kind := "benchmark.other"
	if index%4 == 0 {
		kind = "benchmark.filtered"
	}
	record := storedTask{
		Id:             id,
		ConversationId: durable.ROOT_CONVERSATION_ID,
		Kind:           kind,
		Version:        1,
		Input:          map[string]any{"index": index},
		Background:     index%5 == 0,
		AbortRequested: index%7 == 0,
	}
	if status == durable.TaskTerminal {
		var result durable.JsonValue = map[string]any{"index": index}
		record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status:  status,
			Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result},
		}
		return record
	}
	var checkpoint durable.JsonValue = map[string]any{"index": index, "payload": strings.Repeat("x", 64)}
	record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: status, Checkpoint: &checkpoint}
	return record
}

func mintAs[I durable.Id](storage durable.Storage) (I, error) {
	id, err := storage.MintId()
	return durable.IdFromNumber[I](id), err
}

func deltaSet(count int) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentDelta, Version: 1, Ops: []durable.Op{{"s", []any{"count"}, count}}}
}

var rootScope = durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID}

// SeedStorageBenchmark seeds deterministic representative data through only the public Storage contract.
func SeedStorageBenchmark(ctx context.Context, storage durable.Storage, scale StorageBenchmarkScale) (StorageBenchmarkDataset, error) {
	var dataset StorageBenchmarkDataset
	commit := func(writes ...durable.StorageWrite) (durable.Seq, error) { return storage.Commit(ctx, writes) }
	if _, err := commit(durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}); err != nil {
		return dataset, err
	}

	var firstEntryId *durable.EntryId
	for start := 0; start < scale.EntryCount; start += batchSize {
		var writes []durable.StorageWrite
		for index := start; index < min(start+batchSize, scale.EntryCount); index++ {
			id, err := mintAs[durable.EntryId](storage)
			if err != nil {
				return dataset, err
			}
			entry := durable.EntryRecord{
				Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "benchmark.entry",
				Data: map[string]any{"index": index, "text": fmt.Sprintf("entry-%d-%s", index, strings.Repeat("x", 96))},
			}
			if index == 0 {
				firstEntryId = &id
				entry.Head = &id
			}
			writes = append(writes, durable.EntryWrite{Value: entry})
		}
		if _, err := commit(writes...); err != nil {
			return dataset, err
		}
	}

	for start := 0; start < scale.TaskCount; start += batchSize {
		var writes []durable.StorageWrite
		for index := start; index < min(start+batchSize, scale.TaskCount); index++ {
			id, err := mintAs[durable.TaskId](storage)
			if err != nil {
				return dataset, err
			}
			writes = append(writes, durable.TaskWrite{Value: benchmarkTask(id, index)})
		}
		if _, err := commit(writes...); err != nil {
			return dataset, err
		}
	}

	var exactDocumentId *durable.DocumentId
	for start := 0; start < scale.DocumentCount; start += batchSize {
		var writes []durable.StorageWrite
		for index := start; index < min(start+batchSize, scale.DocumentCount); index++ {
			id, err := mintAs[durable.DocumentId](storage)
			if err != nil {
				return dataset, err
			}
			exactDocumentId = &id
			key := fmt.Sprintf("key-%d", index)
			writes = append(writes, durable.DocumentCreateWrite{
				Record:  durable.DocumentCreate{Id: id, Kind: "benchmark.family", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
				Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("index", index, "text", strings.Repeat("x", 128))},
			})
		}
		if _, err := commit(writes...); err != nil {
			return dataset, err
		}
	}

	replayDocumentIds := map[int]durable.DocumentId{}
	var replayCreates []durable.StorageWrite
	for _, tail := range replayTails {
		id, err := mintAs[durable.DocumentId](storage)
		if err != nil {
			return dataset, err
		}
		replayDocumentIds[tail] = id
		key := strconv.Itoa(tail)
		replayCreates = append(replayCreates, durable.DocumentCreateWrite{
			Record: durable.DocumentCreate{
				Id: id, Kind: "benchmark.replay", Key: &key, Scope: rootScope,
				History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
			},
			Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("count", 0, "text", strings.Repeat("x", 64))},
		})
	}
	if _, err := commit(replayCreates...); err != nil {
		return dataset, err
	}
	for count := 1; count <= replayTails[len(replayTails)-1]; count++ {
		var writes []durable.StorageWrite
		for _, tail := range replayTails {
			if count <= tail {
				writes = append(writes, durable.DocumentChangeWrite{Id: replayDocumentIds[tail], Content: deltaSet(count)})
			}
		}
		if _, err := commit(writes...); err != nil {
			return dataset, err
		}
	}

	historicalDocumentId, err := mintAs[durable.DocumentId](storage)
	if err != nil {
		return dataset, err
	}
	if _, err := commit(durable.DocumentCreateWrite{
		Record: durable.DocumentCreate{
			Id: historicalDocumentId, Kind: "benchmark.history", Scope: rootScope,
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		},
		Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("count", 0)},
	}); err != nil {
		return dataset, err
	}
	var ancientAt durable.Seq
	for count := 1; count <= historySegmentLength; count++ {
		if ancientAt, err = commit(durable.DocumentChangeWrite{Id: historicalDocumentId, Content: deltaSet(count)}); err != nil {
			return dataset, err
		}
	}
	if _, err := commit(durable.DocumentChangeWrite{Id: historicalDocumentId, Content: durable.DocumentContent{
		Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("count", historySegmentLength),
	}}); err != nil {
		return dataset, err
	}
	if ancientAt == 0 {
		return dataset, errors.New("Benchmark history seed produced no commits")
	}
	recentAt := ancientAt
	for count := historySegmentLength + 1; count <= historySegmentLength*2; count++ {
		if recentAt, err = commit(durable.DocumentChangeWrite{Id: historicalDocumentId, Content: deltaSet(count)}); err != nil {
			return dataset, err
		}
	}

	if firstEntryId == nil {
		return dataset, errors.New("Benchmark scale must create entries")
	}
	parentConversationId := durable.ROOT_CONVERSATION_ID
	parentAt := *firstEntryId
	deepestConversationId := durable.ROOT_CONVERSATION_ID
	for depth := range forkDepth {
		conversationId, err := mintAs[durable.ConversationId](storage)
		if err != nil {
			return dataset, err
		}
		if _, err := commit(durable.ConversationWrite{Value: durable.ConversationRecord{
			Id: conversationId, Parent: &durable.ConversationParent{ConversationId: parentConversationId, At: parentAt},
		}}); err != nil {
			return dataset, err
		}
		writes := make([]durable.StorageWrite, 0, entriesPerFork)
		var lastId durable.EntryId
		for index := range entriesPerFork {
			id, err := mintAs[durable.EntryId](storage)
			if err != nil {
				return dataset, err
			}
			lastId = id
			writes = append(writes, durable.EntryWrite{Value: durable.EntryRecord{
				Id: id, ConversationId: conversationId, Kind: "benchmark.fork", Data: map[string]any{"depth": depth, "index": index},
			}})
		}
		if _, err := commit(writes...); err != nil {
			return dataset, err
		}
		parentConversationId = conversationId
		parentAt = lastId
		deepestConversationId = conversationId
	}

	if exactDocumentId == nil {
		return dataset, errors.New("Benchmark scale must create documents")
	}
	return StorageBenchmarkDataset{
		FirstEntryId:          *firstEntryId,
		FilteredTaskCount:     min(50, (scale.TaskCount+59)/60),
		ExactDocumentId:       *exactDocumentId,
		ExactDocumentKey:      fmt.Sprintf("key-%d", scale.DocumentCount-1),
		ReplayDocumentIds:     replayDocumentIds,
		HistoricalDocumentId:  historicalDocumentId,
		AncientAt:             ancientAt,
		RecentAt:              recentAt,
		DeepestConversationId: deepestConversationId,
		AncestorHeadEntryId:   *firstEntryId,
	}, nil
}

// StorageReadBenchmark is one read measurement and the value it must return for a seeded dataset.
type StorageReadBenchmark struct {
	Name     string
	Run      func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error)
	Expected func(dataset StorageBenchmarkDataset) int64
}

func documentCount(document *durable.StoredDocument, err error) (int64, error) {
	if err != nil || document == nil {
		return -1, err
	}
	switch count := document.Value.Value("count").(type) {
	case int:
		return int64(count), nil
	case int64:
		return count, nil
	case float64:
		return int64(count), nil
	default:
		return -1, nil
	}
}

// STORAGE_READ_BENCHMARKS are the read measurements run against every backend.
var STORAGE_READ_BENCHMARKS = storageReadBenchmarks()

func storageReadBenchmarks() []StorageReadBenchmark {
	benchmarks := []StorageReadBenchmark{
		{
			Name: "exact entry lookup",
			Run: func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				found, err := storage.Entry(ctx, dataset.FirstEntryId)
				if err != nil || found == nil {
					return -1, err
				}
				return int64(found.Entry.Id), nil
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.FirstEntryId) },
		},
		{
			Name: "entry page scan (100)",
			Run: func(ctx context.Context, storage durable.Storage, _ StorageBenchmarkDataset) (int64, error) {
				page, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 100, nil)
				return int64(len(page.Items)), err
			},
			Expected: func(StorageBenchmarkDataset) int64 { return 100 },
		},
		{
			Name: "filtered task scan (50)",
			Run: func(ctx context.Context, storage durable.Storage, _ StorageBenchmarkDataset) (int64, error) {
				kind, status, background := "benchmark.filtered", durable.TaskPending, true
				page, err := storage.ScanTasks(ctx, durable.TaskQuery{Kind: &kind, Status: &status, Background: &background}, 50, nil)
				return int64(len(page.Items)), err
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.FilteredTaskCount) },
		},
		{
			Name: "exact document address among many",
			Run: func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				key := dataset.ExactDocumentKey
				found, err := storage.FindDocument(ctx, durable.DocumentAddress{
					Kind: "benchmark.family", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession},
				}, durable.CurrentPoint)
				if err != nil || found == nil {
					return -1, err
				}
				return int64(found.Id), nil
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.ExactDocumentId) },
		},
	}
	for _, tail := range replayTails {
		benchmarks = append(benchmarks, StorageReadBenchmark{
			Name: fmt.Sprintf("document replay tail (%d)", tail),
			Run: func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				return documentCount(storage.Document(ctx, dataset.ReplayDocumentIds[tail], durable.CurrentPoint))
			},
			Expected: func(StorageBenchmarkDataset) int64 { return int64(tail) },
		})
	}
	return append(benchmarks,
		StorageReadBenchmark{
			Name: "ancient historical read before newer base",
			Run: func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				return documentCount(storage.Document(ctx, dataset.HistoricalDocumentId, durable.AtSeq(dataset.AncientAt)))
			},
			Expected: func(StorageBenchmarkDataset) int64 { return historySegmentLength },
		},
		StorageReadBenchmark{
			Name: "recent historical read after newer base",
			Run: func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				return documentCount(storage.Document(ctx, dataset.HistoricalDocumentId, durable.AtSeq(dataset.RecentAt)))
			},
			Expected: func(StorageBenchmarkDataset) int64 { return historySegmentLength * 2 },
		},
		StorageReadBenchmark{
			Name: "fork-depth history scan (100)",
			Run: func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				page, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: dataset.DeepestConversationId}, 100, nil)
				return int64(len(page.Items)), err
			},
			Expected: func(StorageBenchmarkDataset) int64 { return 100 },
		},
		StorageReadBenchmark{
			Name: "fork-depth head lookup",
			Run: func(ctx context.Context, storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				found, err := storage.FindLatestHeadMarker(ctx, dataset.DeepestConversationId, nil)
				if err != nil || found == nil {
					return -1, err
				}
				return int64(found.Id), nil
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.AncestorHeadEntryId) },
		},
	)
}

// StorageWriteBenchmark is one write measurement and the count it must return.
type StorageWriteBenchmark struct {
	Name     string
	Expected int64
	Run      func(ctx context.Context, storage durable.Storage) (int64, error)
}

// SeedStorageWriteBenchmark seeds the common state expected by every write benchmark sample.
func SeedStorageWriteBenchmark(ctx context.Context, storage durable.Storage) error {
	if _, err := storage.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
	}); err != nil {
		return err
	}
	writes := make([]durable.StorageWrite, 0, 100)
	for index := range 100 {
		id, err := mintAs[durable.EntryId](storage)
		if err != nil {
			return err
		}
		writes = append(writes, durable.EntryWrite{Value: durable.EntryRecord{
			Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "benchmark.baseline", Data: map[string]any{"index": index},
		}})
	}
	_, err := storage.Commit(ctx, writes)
	return err
}

// STORAGE_WRITE_BENCHMARKS are the write measurements run against every backend.
var STORAGE_WRITE_BENCHMARKS = []StorageWriteBenchmark{
	{
		Name:     "commit one entry",
		Expected: 1,
		Run: func(ctx context.Context, storage durable.Storage) (int64, error) {
			id, err := mintAs[durable.EntryId](storage)
			if err != nil {
				return 0, err
			}
			_, err = storage.Commit(ctx, []durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{
				Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "benchmark.write",
				Data: map[string]any{"text": strings.Repeat("x", 128)},
			}}})
			return 1, err
		},
	},
	{
		Name:     "commit 100 entries",
		Expected: 100,
		Run: func(ctx context.Context, storage durable.Storage) (int64, error) {
			writes := make([]durable.StorageWrite, 0, 100)
			for index := range 100 {
				id, err := mintAs[durable.EntryId](storage)
				if err != nil {
					return 0, err
				}
				writes = append(writes, durable.EntryWrite{Value: durable.EntryRecord{
					Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "benchmark.write",
					Data: map[string]any{"index": index, "text": strings.Repeat("x", 128)},
				}})
			}
			_, err := storage.Commit(ctx, writes)
			return int64(len(writes)), err
		},
	},
	{
		Name:     "commit mixed entry/task/submission/document",
		Expected: 4,
		Run: func(ctx context.Context, storage durable.Storage) (int64, error) {
			entryId, err := mintAs[durable.EntryId](storage)
			if err != nil {
				return 0, err
			}
			taskId, err := mintAs[durable.TaskId](storage)
			if err != nil {
				return 0, err
			}
			submissionId, err := mintAs[durable.SubmissionId](storage)
			if err != nil {
				return 0, err
			}
			documentId, err := mintAs[durable.DocumentId](storage)
			if err != nil {
				return 0, err
			}
			requestId := fmt.Sprintf("benchmark-%d", submissionId)
			key := strconv.FormatInt(int64(documentId), 10)
			writes := []durable.StorageWrite{
				durable.EntryWrite{Value: durable.EntryRecord{Id: entryId, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "benchmark.mixed"}},
				durable.TaskWrite{Value: benchmarkTask(taskId, int(taskId))},
				durable.SubmissionWrite{Value: durable.SubmissionRecord{
					Id: submissionId, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &requestId,
					Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &entryId,
				}},
				durable.DocumentCreateWrite{
					Record:  durable.DocumentCreate{Id: documentId, Kind: "benchmark.mixed", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
					Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("entryId", entryId, "taskId", taskId)},
				},
			}
			_, err = storage.Commit(ctx, writes)
			return int64(len(writes)), err
		},
	},
}
