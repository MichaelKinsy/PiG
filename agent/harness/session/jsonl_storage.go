package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

var errJsonlStorageClosed = errors.New("JsonlStorage is closed")

// JsonlStorage serializes append transactions and materializes their committed state.
type JsonlStorage struct {
	Header       JsonlStorageHeader
	fs           harness.FileSystem
	path         string
	now          func() int64
	queue        MutationLine
	mu           sync.RWMutex
	storageState *InMemoryStorageState
	legacy       *LegacyV3Source
	closed       bool
	closeOnce    sync.Once
}

func newJsonlStorage(options JsonlStorageOptions, header JsonlStorageHeader) *JsonlStorage {
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	return &JsonlStorage{Header: header, fs: options.FileSystem, path: options.Path, now: now, storageState: NewInMemoryStorageState()}
}

// CreateJsonlStorage atomically writes a header and optional initial transaction.
func CreateJsonlStorage(ctx context.Context, options JsonlStorageOptions, header JsonlStorageHeader, initial []Write) (*JsonlStorage, error) {
	storage := newJsonlStorage(options, header)
	prepared, err := storage.storageState.PrepareCommit(initial, storage.now())
	if err != nil {
		return nil, err
	}
	err = publishJsonl(ctx, storage.fs, storage.path, header, func(appendWrites func([]CommittedWrite) error) error {
		if len(prepared.Writes) > 0 {
			return appendWrites(prepared.Writes)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	storage.storageState.ApplyValidated(prepared.Writes)
	return storage, nil
}

// OpenJsonlStorage replays complete transactions and repairs only a torn format-4 tail.
func OpenJsonlStorage(ctx context.Context, options JsonlStorageOptions) (*JsonlStorage, error) {
	reader, err := options.FileSystem.OpenTextLineReader(ctx, options.Path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read JSONL storage %s: %w", options.Path, err)
	}
	parsed, err := readJsonlHeader(ctx, reader, options.Path)
	reader.Close(ctx)
	if err != nil {
		return nil, err
	}
	if parsed.Legacy != nil {
		return openLegacyJsonlStorage(ctx, options)
	}
	return openV4JsonlStorage(ctx, options, *parsed.Header)
}

func openV4JsonlStorage(ctx context.Context, options JsonlStorageOptions, header JsonlStorageHeader) (*JsonlStorage, error) {
	content, err := options.FileSystem.ReadTextFile(ctx, options.Path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read JSONL storage %s: %w", options.Path, err)
	}
	if header.StorageVersion != JSONLStorageVersion {
		return nil, fmt.Errorf("Session %s uses unsupported storage version %d", header.ID, header.StorageVersion)
	}
	complete := strings.LastIndexByte(content, '\n')
	lines := []string{}
	if complete >= 0 {
		lines = strings.Split(content[:complete], "\n")
	}
	storage := newJsonlStorage(options, header)
	for index := 1; index < len(lines); index++ {
		writes, err := ParseJsonlTransaction(lines[index])
		if err == nil {
			err = storage.replayCommitted(writes)
		}
		if err != nil {
			return nil, fmt.Errorf("Invalid JSONL storage %s: line %d: %w", options.Path, index+1, err)
		}
	}
	if header.NextSeq != nil {
		if err := storage.storageState.AdvanceNextSeq(*header.NextSeq); err != nil {
			return nil, err
		}
	}
	if !strings.HasSuffix(content, "\n") {
		if err := PublishFileAtomically(ctx, storage.fs, storage.path, func(appendText func(string) error) error { return appendText(strings.Join(lines, "\n") + "\n") }); err != nil {
			return nil, err
		}
	}
	return storage, nil
}

func openLegacyJsonlStorage(ctx context.Context, options JsonlStorageOptions) (*JsonlStorage, error) {
	source, err := ReadLegacyV3Source(ctx, options.FileSystem, options.Path)
	if err != nil {
		return nil, err
	}
	header := source.Header
	header.NextSeq = new(source.NextSeq)
	storage := newJsonlStorage(options, header)
	storage.legacy = source
	if err := source.Writes(ctx, nil, func(write CommittedWrite) error { return storage.replayCommitted([]CommittedWrite{write}) }); err != nil {
		return nil, err
	}
	return storage, nil
}

func (storage *JsonlStorage) replayCommitted(writes []CommittedWrite) error {
	if err := storage.storageState.ValidateCommitted(writes); err != nil {
		return err
	}
	storage.storageState.ApplyValidated(writes)
	return nil
}

func (storage *JsonlStorage) admit(operation func() (any, error)) (*LineJob, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if storage.closed {
		return nil, errJsonlStorageClosed
	}
	return storage.queue.Enqueue(operation), nil
}

// Commit appends one complete transaction before exposing its in-memory effects.
func (storage *JsonlStorage) Commit(ctx context.Context, writes []Write) (CommitResult, error) {
	job, err := storage.admit(func() (any, error) {
		storage.mu.Lock()
		defer storage.mu.Unlock()
		return storage.applyCommit(ctx, writes)
	})
	if err != nil {
		return CommitResult{}, err
	}
	value, err := job.Wait()
	if err != nil {
		return CommitResult{}, err
	}
	return value.(CommitResult), nil
}

func (storage *JsonlStorage) applyCommit(ctx context.Context, writes []Write) (CommitResult, error) {
	if storage.legacy != nil && len(writes) > 0 {
		return storage.upgradeLegacy(ctx, writes)
	}
	prepared, err := storage.storageState.PrepareCommit(writes, storage.now())
	if err != nil {
		return CommitResult{}, err
	}
	if len(prepared.Writes) > 0 {
		line, err := SerializeJsonlTransaction(prepared.Writes)
		if err != nil {
			return CommitResult{}, err
		}
		if err := storage.fs.AppendFile(ctx, storage.path, []byte(line+"\n")); err != nil {
			return CommitResult{}, fmt.Errorf("Failed to append JSONL storage %s: %w", storage.path, err)
		}
	}
	prepared.Result.Stats = storage.withImportedUsage(storage.storageState.ApplyValidated(prepared.Writes))
	return prepared.Result, nil
}

func (storage *JsonlStorage) upgradeLegacy(ctx context.Context, writes []Write) (CommitResult, error) {
	source := storage.legacy
	timestamp := storage.now()
	id, err := UUIDv7(&timestamp)
	if err != nil {
		return CommitResult{}, err
	}
	details := JsonValue(map[string]any{"source": "v3-import"})
	all := append([]Write{InsertUsage(UsageRow{ID: id, Usage: source.ImportedUsage, Adjustment: true, Details: &details})}, writes...)
	prepared, err := storage.storageState.PrepareCommit(all, timestamp)
	if err != nil {
		return CommitResult{}, err
	}
	header := storage.Header
	header.NextSeq = new(prepared.Result.FirstSeq + int64(len(prepared.Writes)))
	err = publishJsonl(ctx, storage.fs, storage.path, header, func(appendWrites func([]CommittedWrite) error) error {
		if err := source.Writes(ctx, nil, func(write CommittedWrite) error { return appendWrites([]CommittedWrite{write}) }); err != nil {
			return err
		}
		return appendWrites(prepared.Writes)
	})
	if err != nil {
		return CommitResult{}, err
	}
	prepared.Result.Stats = storage.storageState.ApplyValidated(prepared.Writes)
	storage.legacy = nil
	prepared.Result.FirstSeq++
	prepared.Result.Seqs = prepared.Result.Seqs[1:]
	return prepared.Result, nil
}

func (storage *JsonlStorage) withImportedUsage(stats SessionStats) SessionStats {
	if storage.legacy != nil {
		stats.Usage = storage.legacy.ImportedUsage
	}
	return stats
}

func jsonlRead[T any](storage *JsonlStorage, read func(*InMemoryStorageState) (T, error)) (T, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if storage.closed {
		var zero T
		return zero, errJsonlStorageClosed
	}
	return read(storage.storageState)
}

func (storage *JsonlStorage) GetEntries(_ context.Context, ids []string) (map[string]Entry, error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) (map[string]Entry, error) { return s.GetEntries(ids), nil })
}
func (storage *JsonlStorage) GetValue(_ context.Context, address StoredAddressBase) (*StoredValue[any], error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) (*StoredValue[any], error) { return s.GetValue(address), nil })
}
func (storage *JsonlStorage) ScanValues(_ context.Context, address StoredAddressBase) ([]StoredValue[any], error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) ([]StoredValue[any], error) { return s.ScanValues(address), nil })
}
func (storage *JsonlStorage) ReadList(_ context.Context, address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) ([]ListElement[any], error) { return s.ReadList(address, options) })
}
func (storage *JsonlStorage) ScanBranch(_ context.Context, query StorageBranchScan) ([]Entry, error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) ([]Entry, error) { return s.ScanBranch(query) })
}
func (storage *JsonlStorage) ScanBranchStructure(_ context.Context, query StorageBranchScan) ([]EntryStructure, error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) ([]EntryStructure, error) { return s.ScanBranchStructure(query) })
}
func (storage *JsonlStorage) ScanEntries(_ context.Context, query EntryScan) ([]Entry, error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) ([]Entry, error) { return s.ScanEntries(query), nil })
}
func (storage *JsonlStorage) ScanUsage(_ context.Context, query UsageScan) ([]UsageRow, error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) ([]UsageRow, error) { return s.ScanUsage(query), nil })
}
func (storage *JsonlStorage) GetStats(context.Context) (SessionStats, error) {
	return jsonlRead(storage, func(s *InMemoryStorageState) (SessionStats, error) {
		return storage.withImportedUsage(s.GetStats()), nil
	})
}

// IsLegacyV3 reports whether the backing file still requires first-write upgrade.
func (storage *JsonlStorage) IsLegacyV3() bool {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	return storage.legacy != nil
}

// CaptureForkNextSeq snapshots the next sequence at a serialized commit boundary.
func (storage *JsonlStorage) CaptureForkNextSeq(context.Context) (int64, error) {
	job, err := storage.admit(func() (any, error) {
		storage.mu.RLock()
		defer storage.mu.RUnlock()
		return storage.storageState.GetNextSeq(), nil
	})
	if err != nil {
		return 0, err
	}
	value, err := job.Wait()
	if err != nil {
		return 0, err
	}
	return value.(int64), nil
}

// Close rejects later calls and waits for admitted commits.
func (storage *JsonlStorage) Close(context.Context) error {
	storage.closeOnce.Do(func() {
		storage.mu.Lock()
		storage.closed = true
		storage.mu.Unlock()
		_, _ = storage.queue.Run(func() (any, error) { return nil, nil })
	})
	return nil
}
