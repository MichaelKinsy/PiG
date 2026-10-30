package store

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sort"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/next/store/table-migration.ts
//
// Declared tables brought to their version at activation (slice 6a §9): a table the store does not have is created
// at the newest version with its indexes (no replay); an older one is migrated eagerly, one version at a time, in
// its commits. Per version N: (A) the edits before the data step and `table { version: N, state: "migrating" }`; the
// data step's chunks; (Z) the last chunk (or the `update`) with the edits after the step and `state: "ready"`.
// Without a data step (A) and (Z) are one commit. A version recorded `migrating` resumes after (A), skipping the
// rows already stamped `$v >= N`. Its record holds the columns during the step but version N's fingerprint, not the
// fingerprint of those columns: the fingerprint names the version being reached, which resume, export and import
// check.
//
// Async contract (Pi's `async` functions are blocking here, errors returned as Pi rejects): every await of Pi is a
// blocking call in the same order: reads and commits run one after the other, a commit is queued behind earlier
// ones by the store's FIFO. ctx's abort is checked between chunks (a chunk admitted to the step completes): the
// error is `context.Cause(ctx)`, as `abortSignal.throwIfAborted()` throws the signal's reason. The commits run
// without the activation's abort (a commit admitted to the step completes; the fence refuses one). The fallback
// yield between chunks (Pi's `macrotask`: `setImmediate` or a MessageChannel message, so that other commits and
// timers run between chunks) is `runtime.Gosched()` (the target's yield): Go has no timer clamp nor channel fallback
// to choose between, so those mechanics are designed out; the rule they serve is proved by a yield between every two
// chunks that a test replaces with one running its competing task (evidence_storeB_test.go).

// DeclaredTable is a declared table: its newest definition and its SQL name.
type DeclaredTable struct {
	Definition *TableDefinition
	Table      string
}

// migrationTarget is where a migration runs: the store, the table's SQL name and the commits' fence.
type migrationTarget struct {
	store *ChordStore
	table string
	fence CommitFence
	// yield gives other tasks their turn between two chunks.
	yield func()
}

// PrepareTables brings tables to their definitions: the ones the store does not have created in one commit, then
// each older one migrated. A table the code cannot use (see PlanTable) is an error before anything is written; what
// names the owner.
func PrepareTables(tables []DeclaredTable, store *ChordStore, fence CommitFence, what string, ctx context.Context) error {
	return prepareTables(tables, store, fence, what, ctx, runtime.Gosched)
}

// prepareTables is PrepareTables with the yield between the chunks of a `rows` step.
func prepareTables(tables []DeclaredTable, store *ChordStore, fence CommitFence, what string, ctx context.Context, yield func()) error {
	var created []StagedWrite
	type pending struct {
		table string
		plan  TablePlan
	}
	var migrations []pending
	for _, declared := range tables {
		definition, table := declared.Definition, declared.Table
		plan, err := PlanTable(definition, store.Table(table), fmt.Sprintf("%s: table %s", what, quote(definition.Name)))
		if err != nil {
			return err
		}
		switch plan.Kind {
		case PlanCreate:
			created = append(created, creation(definition, table)...)
		case PlanMigrate:
			migrations = append(migrations, pending{table: table, plan: plan})
		}
	}
	if len(created) > 0 {
		if err := commitMigration(created, store, fence, ctx); err != nil {
			return err
		}
	}
	for _, migration := range migrations {
		if err := migrateTable(migration.plan, migrationTarget{store: store, table: migration.table, fence: fence, yield: yield}, ctx); err != nil {
			return err
		}
	}
	return nil
}

// creation is the writes creating definition fresh as table: at its version, `ready`, with its indexes.
func creation(definition *TableDefinition, table string) []StagedWrite {
	writes := []StagedWrite{StageTable(&TableDefinitionWrite{
		Table: table, Version: definition.Version, State: StateReady, Columns: definition.Record,
		Key: definition.Key, Fingerprint: definition.Fingerprint,
	})}
	for _, index := range definition.Indexes {
		writes = append(writes, StageIndex(&IndexWrite{Table: table, Name: index.Name, Columns: index.Columns}))
	}
	return writes
}

// migrateTable migrates the table through the plan's versions (oldest first, each with its migration), the first
// resumed after its (A) commit when the plan says so. ctx's abort is checked between chunks (a chunk admitted to the
// step completes); a data step that fails, or returns a patch the version cannot take, fails: the table stays
// `migrating` and the committed chunks stay.
func migrateTable(plan TablePlan, target migrationTarget, ctx context.Context) error {
	for index, version := range plan.Versions {
		from := FirstBegin
		if index == 0 {
			from = plan.First
		}
		if err := migrateVersion(version, from, target, ctx); err != nil {
			return err
		}
	}
	return nil
}

// MigrationChunk is the size of a `rows` chunk: one commit each.
const MigrationChunk = 100

// migrateVersion migrates to definition's version from its (A) commit (FirstBegin) or after it (FirstResume).
func migrateVersion(definition *TableDefinition, from string, target migrationTarget, ctx context.Context) error {
	migration := definition.Evolution.Migration
	step, table := migration.Step, target.table
	edits := func(list []TableEdit) []StagedWrite {
		writes := make([]StagedWrite, len(list))
		for i, edit := range list {
			writes[i] = StageEdit(table, definition.Version, edit)
		}
		return writes
	}
	ready := append(edits(migration.After), migrationRecord(definition, definition, StateReady, table))
	if step == nil {
		return commitMigration(append(edits(migration.Before), ready...), target.store, target.fence, ctx)
	}
	if from == FirstBegin {
		writes := append(edits(migration.Before), migrationRecord(step.Table, definition, StateMigrating, table))
		if err := commitMigration(writes, target.store, target.fence, ctx); err != nil {
			return err
		}
	}
	if step.Kind == StepUpdate {
		return commitMigration(append([]StagedWrite{updateStep(step, definition.Version, table)}, ready...), target.store, target.fence, ctx)
	}
	return rowsStep(step, definition.Version, ready, target, ctx)
}

// migrationRecord is the `table` write of columns' columns and key, with definition's version and fingerprint, in state.
func migrationRecord(columns, definition *TableDefinition, state TableState, table string) StagedWrite {
	return StageTable(&TableDefinitionWrite{
		Table: table, Version: definition.Version, State: state, Columns: columns.Record, Key: columns.Key,
		Fingerprint: definition.Fingerprint,
	})
}

// updateStep is the `update` step: one `updateWhere` over the rows below version v, stamping it, so it never applies
// twice.
func updateStep(step *DataStep, v int, table string) StagedWrite {
	sql := sqlOf(step.Table, table)
	below := WhereNode{Op: WhereLt, Column: RowVersion, Value: int64(v)}
	where := below
	if step.Where != nil {
		where = WhereNode{Op: WhereAnd, Nodes: []WhereNode{*step.Where, below}}
	}
	versions := JsonVersions(step.Table, step.Set)
	statement := sql.UpdateWhere(where, step.Set.With(RowVersion, ExprNode{Op: ExprLiteral, Value: int64(v)}), versions)
	return StageUpdateWhere(statement, table, where, step.Set, RecordedVersions(step.Set, versions), &v)
}

// rowsStep is the `rows` step: chunks of MigrationChunk rows below version v in key order, paging forward by the
// last key; each patched row stamped v; the last chunk commits with `ready`.
func rowsStep(step *DataStep, v int, ready []StagedWrite, target migrationTarget, ctx context.Context) error {
	columns := step.Table
	sql := sqlOf(columns, target.table)
	order := Order{Columns: columns.Key, Direction: "asc"}
	below := WhereNode{Op: WhereLt, Column: RowVersion, Value: int64(v)}
	var after []sqlite.SqliteValue
	for {
		statement := sql.Select(&below, order, after, MigrationChunk+1, nil)
		stored, err := Read(target.store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
			return executor.All(statement.SQL, statement.Params...)
		}, ctx)
		if err != nil {
			return err
		}
		last := len(stored) <= MigrationChunk
		if !last {
			stored = stored[:MigrationChunk]
		}
		writes := make([]StagedWrite, 0, len(stored)+len(ready))
		for _, row := range stored {
			write, err := patchRow(step, row, v, sql, target.table, ctx)
			if err != nil {
				return err
			}
			writes = append(writes, write)
		}
		if last {
			return commitMigration(append(writes, ready...), target.store, target.fence, ctx)
		}
		if err := commitMigration(writes, target.store, target.fence, ctx); err != nil {
			return err
		}
		after = make([]sqlite.SqliteValue, len(columns.Key))
		for i, name := range columns.Key {
			after[i] = stored[MigrationChunk-1][name]
		}
		// Other commits queued behind this one run between chunks.
		target.yield()
		// An abandoned activation stops here.
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
	}
}

// patchRow is the write of one row's patch: Patch run on the decoded row, its result checked (an object of non-key
// columns of the version, values of their types) and stamped v through the internal form of `update`.
func patchRow(step *DataStep, stored sqlite.SqliteRow, v int, sql *TableSql, table string, ctx context.Context) (StagedWrite, error) {
	definition := step.Table
	decoded, err := definition.Rows.Decode(stored, ctx)
	if err != nil {
		return nil, err
	}
	patch, err := step.Patch(decoded)
	if err != nil {
		return nil, err
	}
	what := fmt.Sprintf("%s version %d: rows", definition.Name, v)
	var props SetOf
	switch typed := patch.(type) {
	case SetOf:
		props = typed
	case []SetProp:
		props = typed
	case Row:
		if typed == nil {
			return nil, fmt.Errorf("%s: expected an object of column values", what)
		}
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			props = append(props, SetProp{Name: name, Value: typed[name]})
		}
	default:
		return nil, fmt.Errorf("%s: expected an object of column values", what)
	}
	var set SetNode
	for _, prop := range props {
		column, found := definition.ByName[prop.Name]
		if !found {
			return nil, fmt.Errorf("%s: no column %s", what, quote(prop.Name))
		}
		if slices.Contains(definition.Key, prop.Name) {
			return nil, fmt.Errorf("%s: key column %s cannot change", what, quote(prop.Name))
		}
		node, err := cellOf(column, prop.Value, fmt.Sprintf("%s: column %s", what, quote(prop.Name)))
		if err != nil {
			return nil, err
		}
		set = set.With(prop.Name, node)
	}
	key := delta.NewJsonObject(len(definition.Key))
	params := make([]sqlite.SqliteValue, len(definition.Key))
	for i, name := range definition.Key {
		// Key columns are non-opt `str` or `int`: stored scalars.
		params[i] = stored[name]
		key.Set(name, jsonScalar(stored[name]))
	}
	versions := JsonVersions(definition, set)
	statement := sql.Update(set.With(RowVersion, ExprNode{Op: ExprLiteral, Value: int64(v)}), params, versions)
	return StageUpdate(statement, table, key, set, RecordedVersions(set, versions), &v), nil
}

// cellOf is a patch value as a `set` node: `Undefined` clears an `opt(...)` column, `json(...)` cells as JSON
// literals (a JSON `null` too); a scalar `null` is refused by EncodeCell.
func cellOf(column Column, value any, what string) (ExprNode, error) {
	if value == Undefined {
		if !column.Optional {
			return ExprNode{}, fmt.Errorf("%s is not opt(...) and cannot be cleared", what)
		}
		return ExprNode{Op: ExprNull}, nil
	}
	cell, err := EncodeCell(column, value, what)
	if err != nil {
		return ExprNode{}, err
	}
	if column.Type == ColJson {
		return ExprNode{Op: ExprJSON, Json: cell.Stored}, nil
	}
	// EncodeCell returns a scalar for a scalar column (ints as float64 JSON): the node holds the checked scalar.
	scalar, err := CheckScalar(column.Type, value, what)
	if err != nil {
		return ExprNode{}, err
	}
	return ExprNode{Op: ExprLiteral, Value: scalar}, nil
}

func sqlOf(definition *TableDefinition, table string) *TableSql {
	return NewTableSql(table, definition.Named(), definition.Rows.Physical, definition.Key)
}

// commitMigration commits writes without the activation's abort: a commit admitted to the step completes (the fence
// refuses one).
func commitMigration(writes []StagedWrite, store *ChordStore, fence CommitFence, ctx context.Context) error {
	_, err := store.Commit(writes, fence, chordctx.WithoutAbortSignal(ctx))
	return err
}
