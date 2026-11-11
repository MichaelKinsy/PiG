package store

import (
	"context"
	"fmt"
	"maps"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/apply-batch.ts
//
// Batch apply (slice 6b §1.6, S5, S17): a replica takes commits `from..to` as an outbox gave them, in one backend
// transaction through the store's commit queue, each write applied with its commit's seq by the applier (a trust
// boundary), `lastSeq` and the allocator set from the last commit. Internal: the sync layer's entry point.
//
// Async contract: Pi's `applyBatch` checks the commits synchronously (a failure is the returned promise's rejection,
// before the queue is touched), then takes its place in the store's commit queue at the call and resolves once the
// batch applied or was refused. ApplyBatch does the same and blocks until then: its place in the queue is taken at
// the call, claims and divergence are checked at the call and again at the turn, and nothing is cancelled once
// queued; ctx counts the checks only (Pi's context does).

// ApplyBatch applies commits (from an outbox, in seq order) to store: refused while an owner holds a claim on it, once
// it diverged, or when the first commit does not follow its LastSeq or one does not follow the one before; a write
// the store's records refuse rolls the whole batch back (`invalid/args`); a recorded count the store does not
// reproduce rolls it back and marks the store diverged, so every later batch is refused. Commits are written to the
// store's outbox too when it has one.
func ApplyBatch(store *ChordStore, commits []OutboxCommit, ctx context.Context) error {
	checked, err := checkCommits(commits, ctx)
	if err != nil {
		return err
	}
	return store.Replay(batchReplay(store, checked), IsDiverged)
}

// checkCommits is every commit's envelope and writes, checked.
func checkCommits(commits []OutboxCommit, ctx context.Context) ([]OutboxCommit, error) {
	checked := make([]OutboxCommit, len(commits))
	for index, commit := range commits {
		each, err := checkCommit(commit, fmt.Sprintf("batch: commit %d", index), ctx)
		if err != nil {
			return nil, err
		}
		checked[index] = each
	}
	return checked, nil
}

// batchReplay is the batch as the store's turn runs it.
func batchReplay(store *ChordStore, commits []OutboxCommit) ReplayApply {
	return func(executor sqlite.SqliteExecutor, catalog map[string]*TableRecord) (Replayed, error) {
		return replayCommits(store, commits, executor, catalog)
	}
}

// checkCommit is one commit's envelope and writes, checked (a trust boundary).
func checkCommit(commit OutboxCommit, what string, ctx context.Context) (OutboxCommit, error) {
	if commit.Seq < -MaxSafeInteger || commit.Seq > MaxSafeInteger || commit.NextID < -MaxSafeInteger || commit.NextID > MaxSafeInteger {
		return OutboxCommit{}, invalidArgs("%s: not an outbox commit", what)
	}
	writes := make([]StoreWrite, len(commit.Writes))
	for index, write := range commit.Writes {
		checked, err := CheckWrite(write, fmt.Sprintf("%s: write %d", what, index), ctx)
		if err != nil {
			return OutboxCommit{}, err
		}
		writes[index] = checked
	}
	return OutboxCommit{Seq: commit.Seq, NextID: commit.NextID, Writes: writes}, nil
}

// replayCommits is the batch inside the store's turn: seqs continuous from LastSeq, allocator values never falling.
func replayCommits(store *ChordStore, commits []OutboxCommit, executor sqlite.SqliteExecutor, catalog map[string]*TableRecord) (Replayed, error) {
	tables := make(map[string]*TableRecord, len(catalog))
	maps.Copy(tables, catalog)
	changed := map[string]*TableRecord{}
	lastSeq := store.LastSeq()
	nextID := store.NextID()
	for index, commit := range commits {
		what := fmt.Sprintf("batch: commit %d (seq %d)", index, commit.Seq)
		if commit.Seq != lastSeq+1 {
			return Replayed{}, invalidArgs("%s: does not follow seq %d", what, lastSeq)
		}
		if commit.NextID < nextID {
			return Replayed{}, invalidArgs("%s: its next id %d is below %d", what, commit.NextID, nextID)
		}
		step := NewStep(executor, commit.Seq, NewIdAllocator(commit.NextID), tables)
		for position, write := range commit.Writes {
			where := fmt.Sprintf("%s: write %d (%s)", what, position, write.StoreWriteKind())
			if create, isCreate := write.(*DocumentCreate); isCreate {
				if err := checkCreated(create.Record.ID, commit.NextID, where); err != nil {
					return Replayed{}, err
				}
			}
			if err := applyWrite(step, write, where); err != nil {
				return Replayed{}, err
			}
		}
		if err := WriteCatalog(step); err != nil {
			return Replayed{}, err
		}
		for name, record := range step.Tables {
			tables[name] = record
			changed[name] = record
		}
		if store.RecordsOutbox() {
			recorded := commit.Writes
			if recorded == nil {
				recorded = []StoreWrite{}
			}
			text, err := jsstring.MarshalJSON(recorded)
			if err != nil {
				return Replayed{}, err
			}
			err = executor.Run("INSERT INTO "+TableOutbox+" (seq, format, next_id, writes) VALUES (?, ?, ?, ?)", commit.Seq, OutboxFormat, commit.NextID, string(text))
			if err != nil {
				return Replayed{}, err
			}
		}
		lastSeq = commit.Seq
		nextID = commit.NextID
	}
	if err := executor.Run("UPDATE "+TableMeta+" SET last_seq = ?, next_id = ? WHERE singleton = 1", lastSeq, nextID); err != nil {
		return Replayed{}, err
	}
	return Replayed{LastSeq: lastSeq, NextID: nextID, Tables: changed}, nil
}

// checkCreated: a created document's id is below its commit's next id (the allocator never mints it again), or equal
// to a saturated next id (MaxSafeInteger may be written, as import allows).
func checkCreated(id, nextID int64, what string) error {
	if id < nextID || (nextID == MaxSafeInteger && id == nextID) {
		return nil
	}
	return invalidArgs("%s: id %d is not below the commit's next id %d", what, id, nextID)
}
