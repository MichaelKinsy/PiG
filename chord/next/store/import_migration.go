package store

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/store/import-migration.ts
//
// Rows of an imported table brought to the version the store records (decision "Documents and table rows can belong
// to a host": imported content migrates as all data does; spec `docs/host-keyed/spec.md` §4): each row taken through
// the versions after the archive's as the eager migration takes stored rows (`table_migration.go`): the edits before
// each data step, the step for a row below its version, the edits after it. Recorded versions stay exact: `$v` as the
// steps stamp it, `json(...)` cells at the version they were written with (a cell a step sets at the column's version
// then).
//
// Go forced differences:
//   - Pi's `Put` rows are plain objects whose key order is insertion order; here a row is the *delta.JsonObject of
//     the `put` (ordered) and the data steps read an EvaluatedRow built from it (integers as int64).
//   - Async contract: none (the function is synchronous in Pi too); a step that throws returns its error.

// migrating is a row in migration: its stored cells, its `json(...)` cells' versions and its `$v`.
type migrating struct {
	row     *delta.JsonObject
	cells   *delta.JsonObject
	version int
}

// migrateRows is puts of the archive's table write at the version recorded has, through history (the owner's newest
// definition of the table): both versions must be in the history (a `migrating` archive's with a data step to
// resume). It fails `invalid/args` otherwise; a data step that fails or returns a patch the version cannot take fails
// with its error. The archive's metadata is not compared: the importer checks every row against the recorded host
// column.
func migrateRows(write *TableDefinitionWrite, puts []*PutWrite, history *TableDefinition, recorded *TableRecord, ctx context.Context) ([]*PutWrite, error) {
	what := "importHostData: table " + write.Table
	versions := HistoryOf(history)
	from := versionIn(versions, write.Version)
	to := versionIn(versions, recorded.Version)
	if from == nil || to == nil {
		return nil, invalidArgs("%s: version %d or %d is not in its owner's history", what, write.Version, recorded.Version)
	}
	if write.State == StateMigrating && (from.Evolution == nil || from.Evolution.Migration.Step == nil) {
		return nil, invalidArgs("%s: version %d exported migrating has no data step to resume", what, write.Version)
	}
	var later []*TableDefinition
	for _, each := range versions {
		if each.Version > from.Version && each.Version <= to.Version {
			later = append(later, each)
		}
	}
	var resumed *TableDefinition
	if write.State == StateMigrating {
		resumed = from
	}
	migrated := make([]*PutWrite, len(puts))
	for index, put := range puts {
		var err error
		if migrated[index], err = migrateRow(put, resumed, later, ctx); err != nil {
			return nil, err
		}
	}
	return migrated, nil
}

// versionIn is the definition of version among versions; nil when there is none.
func versionIn(versions []*TableDefinition, version int) *TableDefinition {
	for _, each := range versions {
		if each.Version == version {
			return each
		}
	}
	return nil
}

// migrateRow is one row through resumed's unfinished data step (an archive exported `migrating`), then each of later.
func migrateRow(put *PutWrite, resumed *TableDefinition, later []*TableDefinition, ctx context.Context) (*PutWrite, error) {
	state := &migrating{row: put.Row.Clone(), cells: put.Columns.Clone(), version: put.Version}
	if resumed != nil {
		migration := resumed.Evolution.Migration
		if err := runStep(state, resumed.Version, migration.Step, ctx); err != nil {
			return nil, err
		}
		for _, edit := range migration.After {
			applyEdit(state, edit)
		}
	}
	for _, definition := range later {
		// A later version always has its migration: it came from `EvolveTable`.
		migration := definition.Evolution.Migration
		for _, edit := range migration.Before {
			applyEdit(state, edit)
		}
		if migration.Step != nil {
			if err := runStep(state, definition.Version, migration.Step, ctx); err != nil {
				return nil, err
			}
		}
		for _, edit := range migration.After {
			applyEdit(state, edit)
		}
	}
	return &PutWrite{Table: put.Table, Row: state.row, Version: state.version, Columns: state.cells}, nil
}

// applyEdit is one column edit on a row, as its DDL changes a stored row.
func applyEdit(state *migrating, edit TableEdit) {
	switch edit.Kind {
	case EditRename:
		if value, present := state.row.Get(edit.From); present {
			state.row.Set(edit.To, value)
		}
		state.row.Delete(edit.From)
		if value, present := state.cells.Get(edit.From); present {
			state.cells.Set(edit.To, value)
		}
		state.cells.Delete(edit.From)
	case EditAdd:
		// A non-`opt` added column has a default, which existing rows get; an `opt(...)` one stays `NULL`.
		if edit.Column.Default != nil {
			state.row.Set(edit.Name, edit.Column.Default)
		}
	case EditDrop:
		state.row.Delete(edit.Name)
		state.cells.Delete(edit.Name)
	}
	// `json` changes no cell (cells migrate on access); `index` and `dropIndex` none either.
}

// runStep is version v's data step on a row below it, stamping v: a `rows` patch on the row decoded with the step's
// table, an `update` where its condition holds.
func runStep(state *migrating, v int, step *DataStep, ctx context.Context) error {
	if state.version >= v {
		return nil
	}
	table := step.Table
	evaluated := evaluatedRow(state.row, table)
	var set SetNode
	switch {
	case step.Kind == StepRows:
		decoded, err := table.Rows.Decode(ctx, sqliteRow(state, table))
		if err != nil {
			return err
		}
		if set, err = PatchSet(step, decoded, v); err != nil {
			return err
		}
	case step.Where == nil || Matches(*step.Where, evaluated):
		set = step.Set
	default:
		return nil
	}
	next, err := ApplySet(set, evaluated, table.Name)
	if err != nil {
		return err
	}
	for _, entry := range set {
		if value, present := next[entry.Column]; present {
			state.row.Set(entry.Column, value)
		} else {
			state.row.Delete(entry.Column)
		}
		column := table.ByName[entry.Column]
		if column.Type != ColJson {
			continue
		}
		if entry.Node.Op == ExprNull {
			state.cells.Delete(entry.Column)
		} else {
			state.cells.Set(entry.Column, float64(column.Json.Version))
		}
	}
	state.version = v
	return nil
}

// evaluatedRow is row as the evaluator reads it (integers of table's `int` columns as int64).
func evaluatedRow(row *delta.JsonObject, table *TableDefinition) EvaluatedRow {
	evaluated := make(EvaluatedRow, row.Len())
	for name, value := range row.All() {
		if number, isNumber := value.(float64); isNumber && table.ByName[name].Type == ColInt {
			if integer, safe := safeInteger(number); safe {
				value = integer
			}
		}
		evaluated[name] = value
	}
	return evaluated
}

// sqliteRow is the row as SQLite stores it in table's layout (what `RowCodec.Decode` reads).
func sqliteRow(state *migrating, table *TableDefinition) sqlite.SqliteRow {
	stored := sqlite.SqliteRow{RowVersion: int64(state.version)}
	for _, column := range table.Columns {
		value, present := state.row.Get(column.Name)
		switch {
		case !present:
			stored[column.Name] = nil
		case column.Type == ColBool:
			stored[column.Name] = ToSqlite(value == true)
		case column.Type == ColJson:
			text, _ := jsstring.MarshalJSON(value)
			stored[column.Name] = string(text)
			stored[VersionColumn(column.Name)] = cellVersion(state.cells, column.Name)
		case column.Type == ColInt:
			integer, _ := integerOf(value)
			stored[column.Name] = integer
		default:
			stored[column.Name] = value
		}
	}
	return stored
}

// cellVersion is the version of a `json(...)` cell; NULL when it has none.
func cellVersion(cells *delta.JsonObject, name string) sqlite.SqliteValue {
	if version, readable := integerOf(cells.Value(name)); readable {
		return version
	}
	return nil
}
