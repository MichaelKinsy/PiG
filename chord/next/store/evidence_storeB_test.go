package store

import (
	"testing"
)

// Ports packages/chord/test/next/store/table-migration.test.ts describe("the chunk yield"): a migration over many
// chunks yields between chunks, so that a competing task runs between them ("yields between chunks without Node's
// setImmediate" on 3 * MIGRATION_CHUNK rows; "yields between chunks without a timer ... while timers due run in
// between (a latency guard)" on 10 * MIGRATION_CHUNK rows with a `setTimeout(..., 0)` due during the migration). Pi
// watches JavaScript's timer mechanics (`setTimeout` spied, `setImmediate` removed); Go has neither (the yield is
// `runtime.Gosched()`, after which the Go scheduler, not the migration, decides which goroutine runs, so a
// goroutine-based assertion is not deterministic), so the rule they guard is asserted on the yield seam of prepareTables: the
// test's yield is the event loop's macrotask turn between the chunks, running the callback queued before the migration
// started (Pi's `setTimeout(..., 0)`), and records the commits made when each turn happens.

// storeBMigrationRun is what a migration with a one-callback event loop saw.
type storeBMigrationRun struct {
	// base is the last seq before the migration.
	base int64
	// turns are the last seqs at which the yield turns happened, in order.
	turns []int64
	// firedAt are the last seqs at which the queued callback ran.
	firedAt []int64
	// last is the last seq after the migration.
	last int64
}

// storeBMigrateTo2 migrates count rows of version 1 to version 2 with a callback due at the start (it runs at the first
// turn of the event loop, the yield between two chunks) and returns what the loop saw.
func storeBMigrateTo2(t *testing.T, count int) storeBMigrationRun {
	t.Helper()
	versions := migrationEdits(t)
	store := filledEdits(t, count, versions.v1)
	run := storeBMigrationRun{base: store.LastSeq()}
	queue := []func(){func() { run.firedAt = append(run.firedAt, store.LastSeq()) }}
	yield := func() {
		run.turns = append(run.turns, store.LastSeq())
		tasks := queue
		queue = nil
		for _, task := range tasks {
			task()
		}
	}
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
	must(t, prepareTables([]DeclaredTable{{Definition: versions.v2, Table: migrationTable}}, store, fence, "app.store", background, yield))
	record := store.Table(migrationTable)
	qEqual(t, []any{record.Version, record.State}, []any{2, StateReady})
	run.last = store.LastSeq()
	return run
}

// 3 * MIGRATION_CHUNK rows: (A) = base+1, chunks 1 and 2 = base+2 and base+3, (Z) = base+4; a turn after each chunk
// commit, none after (Z), and the callback ran at the first one: between the first and the second chunk.
func TestMigrationYieldsBetweenChunksOfAThreeChunkMigration(t *testing.T) {
	run := storeBMigrateTo2(t, 3*MigrationChunk)
	qEqual(t, run.turns, []int64{run.base + 2, run.base + 3})
	qEqual(t, run.firedAt, []int64{run.base + 2})
	qEqual(t, run.last, run.base+4)
}

// 10 * MIGRATION_CHUNK rows: (A) = base+1, chunks 1..9 = base+2..base+10, (Z) = base+11; a turn after each of the nine
// chunk commits, so that the callback due at the start runs after the first chunk, not after the migration.
func TestMigrationRunsADueCallbackBetweenChunksOfATenChunkMigration(t *testing.T) {
	run := storeBMigrateTo2(t, 10*MigrationChunk)
	turns := make([]int64, 9)
	for i := range turns {
		turns[i] = run.base + int64(i) + 2
	}
	qEqual(t, run.turns, turns)
	qEqual(t, run.firedAt, []int64{run.base + 2})
	qEqual(t, run.last, run.base+11)
}
