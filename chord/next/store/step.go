package store

import "github.com/MichaelKinsy/PiG/chord/sqlite"

// Ports packages/chord/src/next/store/step.ts
//
// The commit step's working state (slice 6a §3, slice 6b §1.3) and the shape every write takes into it: a write
// staged before the queue (serialized, rule 8), applied inside the step, returning the writes it recorded.
//
// Async contract: Pi's Step methods return Promises that settle in call order within the step's one backend
// transaction; here they block and return `(value, error)` in the same order. The step runs under the store's commit
// queue on one goroutine, so no lock guards it.

// WriteOrder is where a staged write goes in its commit: retirements, creations, then everything else, each phase in
// staging order. A retirement frees a live address before a creation takes it (the partial unique index); an
// adapter's composite write orders its own parts inside Apply.
type WriteOrder string

// The write orders.
const (
	OrderRetire WriteOrder = "retire"
	OrderCreate WriteOrder = "create"
	OrderWrite  WriteOrder = "write"
)

// StagedWrite is a write staged for the commit step: its order and what it does there, returning the writes as
// recorded (Pi's `{ order, apply }` object is this interface).
type StagedWrite interface {
	// StageOrder is the write's phase.
	StageOrder() WriteOrder
	// Apply applies the write inside the step's backend transaction; returns the writes as the outbox records them,
	// in the order applied, with what only the step knows resolved (its seq, a record's revision).
	Apply(step *Step) ([]StoreWrite, error)
}

// Step is one commit step: its backend transaction, its seq, the store's id allocator (D3: ids are taken from it as
// the writes apply, never copied), the ids of the documents it created, the table records it changed (the store's
// catalog takes them after `COMMIT`) and the statements it ran (`chord.store.statements`). Its reads are the step
// reader of adapters: they see the step's own writes.
type Step struct {
	Executor sqlite.SqliteExecutor
	Seq      int64
	IDs      *IdAllocator
	// Created are the ids of the commit's created documents, in the order of their writes.
	Created []int64
	// Tables are the table records this step changed, by SQL name.
	Tables  map[string]*TableRecord
	catalog map[string]*TableRecord
	// Statements counts the statements the step ran.
	Statements int
}

// NewStep is a step over executor at seq; catalog is the store's catalog as the step starts (read only here).
func NewStep(executor sqlite.SqliteExecutor, seq int64, ids *IdAllocator, catalog map[string]*TableRecord) *Step {
	return &Step{Executor: executor, Seq: seq, IDs: ids, Tables: map[string]*TableRecord{}, catalog: catalog}
}

// Table is the record of table name as this step leaves it so far; nil when there is none.
func (step *Step) Table(name string) *TableRecord {
	if record, ok := step.Tables[name]; ok {
		return record
	}
	return step.catalog[name]
}

// Run runs one statement.
func (step *Step) Run(sql string, params ...sqlite.SqliteValue) error {
	step.Statements++
	return step.Executor.Run(sql, params...)
}

// RunCounted runs one statement and returns the rows it changed.
func (step *Step) RunCounted(sql string, params ...sqlite.SqliteValue) (int64, error) {
	step.Statements++
	return step.Executor.RunCounted(sql, params...)
}

// Get runs one query for its first row; nil when there is none.
func (step *Step) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	step.Statements++
	return step.Executor.Get(sql, params...)
}

// All runs one query.
func (step *Step) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	step.Statements++
	return step.Executor.All(sql, params...)
}
