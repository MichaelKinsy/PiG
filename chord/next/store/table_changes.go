package store

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/store/table-changes.ts
//
// A transaction's writes to one table (slice 6a §6, §7; W10): checked and staged synchronously like a draft's ops (no
// I/O, a bad row returns its error at its own call), kept in staging order for the commit step, and overlaid on
// committed rows for `tx.get` until a bulk write makes that impossible (`invalid/read_after_bulk`).
//
// Go forced differences: Pi's `throw` is the returned error; `staged` is a pointer to the transaction's slice (Pi
// appends to a shared array); a `where` or `set` callback is a `func(ColumnRefs) Condition` / `func(ColumnRefs) SetOf`
// (or `func(ColumnRefs) map[string]any`) given where Pi takes the value or the callback. A TableChanges belongs to
// one transaction, which stages from one goroutine (as Pi's single thread does): it is not locked.
// Async contract: `Get` blocks on one guarded committed read (the store's), then replays the overlay in memory.

// keyWrite is one staged write of one key, as the overlay replays it.
type keyWrite struct {
	kind string
	row  EvaluatedRow
	set  SetNode
}

// The kinds of keyWrite.
const (
	keyPut    = "put"
	keyUpdate = "update"
	keyDelete = "delete"
)

// TableChanges are the writes of one transaction to one table: each appended to the transaction's staged list
// (`staged`, one order across tables) and, for the single-key ones, to its key's overlay.
type TableChanges struct {
	Handle *TableHandle
	staged *[]StagedWrite
	byKey  map[string][]keyWrite
	// bulk is set once an `updateWhere` or `deleteWhere` was staged: `Get` is refused from then on (W10).
	bulk bool
}

// NewTableChanges are the changes of one transaction to handle's table, staged into staged.
func NewTableChanges(handle *TableHandle, staged *[]StagedWrite) *TableChanges {
	return &TableChanges{Handle: handle, staged: staged, byKey: map[string][]keyWrite{}}
}

// Put stages a `put` of row (an upsert).
func (changes *TableChanges) Put(row any) error {
	definition, sql := changes.Handle.Definition, changes.Handle.Sql
	encoded, err := definition.Rows.Put(row, definition.Version)
	if err != nil {
		return err
	}
	*changes.staged = append(*changes.staged, StagePut(sql.Put, sql.Table, encoded, definition.Version))
	changes.write(encoded.ID, keyWrite{kind: keyPut, row: evaluatedOf(definition, encoded.Row)})
	return nil
}

// Update stages an `update` of the row with key (set: values, or a callback of column references).
func (changes *TableChanges) Update(key, set any) error {
	definition, sql := changes.Handle.Definition, changes.Handle.Sql
	encoded, err := definition.Rows.Key(key, "update")
	if err != nil {
		return err
	}
	compiled, err := changes.compileSet(set, "update")
	if err != nil {
		return err
	}
	versions := JsonVersions(definition, compiled)
	*changes.staged = append(*changes.staged, StageUpdate(
		sql.Update(compiled, encoded.Params, versions),
		sql.Table,
		encoded.Key,
		compiled,
		RecordedVersions(compiled, versions),
		nil,
	))
	changes.write(encoded.ID, keyWrite{kind: keyUpdate, set: compiled})
	return nil
}

// Delete stages a `delete` of the row with key.
func (changes *TableChanges) Delete(key any) error {
	definition, sql := changes.Handle.Definition, changes.Handle.Sql
	encoded, err := definition.Rows.Key(key, "delete")
	if err != nil {
		return err
	}
	*changes.staged = append(*changes.staged, StageDelete(Statement{SQL: sql.Delete, Params: slices.Clone(encoded.Params)}, sql.Table, encoded.Key))
	changes.write(encoded.ID, keyWrite{kind: keyDelete})
	return nil
}

// UpdateWhere stages an `updateWhere`: every row matching where gets set.
func (changes *TableChanges) UpdateWhere(where, set any) error {
	definition, sql := changes.Handle.Definition, changes.Handle.Sql
	condition, err := CompileWhere(definition, resolveWhere(definition, where), "updateWhere")
	if err != nil {
		return err
	}
	compiled, err := changes.compileSet(set, "updateWhere")
	if err != nil {
		return err
	}
	versions := JsonVersions(definition, compiled)
	statement := sql.UpdateWhere(condition, compiled, versions)
	*changes.staged = append(*changes.staged, StageUpdateWhere(statement, sql.Table, condition, compiled, RecordedVersions(compiled, versions), nil))
	changes.bulk = true
	return nil
}

// DeleteWhere stages a `deleteWhere`: every row matching where goes.
func (changes *TableChanges) DeleteWhere(where any) error {
	definition, sql := changes.Handle.Definition, changes.Handle.Sql
	condition, err := CompileWhere(definition, resolveWhere(definition, where), "deleteWhere")
	if err != nil {
		return err
	}
	*changes.staged = append(*changes.staged, StageDeleteWhere(sql.DeleteWhere(condition), sql.Table, condition))
	changes.bulk = true
	return nil
}

// Get is the row with key as this transaction leaves it: the committed row with this transaction's `put`, `update`
// and `delete` of the key replayed (nil when absent); refused after a bulk write (W10).
func (changes *TableChanges) Get(key any, ctx context.Context) (EvaluatedRow, error) {
	definition := changes.Handle.Definition
	if changes.bulk {
		return nil, data.Invalid(data.ReasonReadAfterBulk, definition.Name+": tx.get after an updateWhere or deleteWhere on the table")
	}
	encoded, err := definition.Rows.Key(key, "get")
	if err != nil {
		return nil, err
	}
	writes := changes.byKey[encoded.ID]
	start := len(writes)
	for start > 0 && writes[start-1].kind == keyUpdate {
		start--
	}
	var row EvaluatedRow
	if start == 0 {
		// A committed row is decoded by the table's columns: scalars and JSON, what the evaluator takes.
		if row, err = changes.Handle.Get(encoded.Key, ctx); err != nil {
			return nil, err
		}
	} else if from := writes[start-1]; from.kind == keyPut {
		row = from.row
	}
	for _, write := range writes[start:] {
		// An `update` of an absent row changes nothing.
		if row != nil && write.kind == keyUpdate {
			if row, err = ApplySet(write.set, row, definition.Name); err != nil {
				return nil, err
			}
		}
	}
	return row, nil
}

func (changes *TableChanges) compileSet(set any, what string) (SetNode, error) {
	definition := changes.Handle.Definition
	switch typed := set.(type) {
	case func(ColumnRefs) SetOf:
		set = typed(definition.Refs)
	case func(ColumnRefs) map[string]any:
		set = typed(definition.Refs)
	}
	return CompileSet(definition, set, what)
}

func (changes *TableChanges) write(id string, write keyWrite) {
	changes.byKey[id] = append(changes.byKey[id], write)
}

// resolveWhere is where's condition: a callback is given the table's column references.
func resolveWhere(definition *TableDefinition, where any) any {
	if callback, ok := where.(func(ColumnRefs) Condition); ok {
		return callback(definition.Refs)
	}
	return where
}

// evaluatedOf is a stored row (strict JSON: numbers float64) as the evaluator reads one: `int` cells int64.
func evaluatedOf(definition *TableDefinition, stored StoredRow) EvaluatedRow {
	row := make(EvaluatedRow, stored.Len())
	for name, value := range stored.All() {
		if number, ok := value.(float64); ok && definition.ByName[name].Type == ColInt {
			value = int64(number) //portlint:allow numbers guarded: an `int` cell is a checked safe integer
		}
		row[name] = value
	}
	return row
}
