package store

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/table-writes.ts
//
// Table writes (slice 6a §5, §7, §9; S17; decisions "Writes carry their versions", "Index definitions are recorded"):
// the recorded form of every table write, the staged writes a transaction or an activation hands the commit step
// (their SQL rendered at staging, rule 8), and the appliers of the schema writes, which render DDL from the record.
//
// Go forced differences:
//   - Pi's `TableWrite` union is one pointer type per kind (each a StoreWrite whose JSON is Pi's, in Pi's key order).
//   - A StoredKey and ColumnVersions are ordered *delta.JsonObject values (key order is part of the stored bytes);
//     their numbers are float64 as strict JSON carries them.
//   - A `version` that Pi leaves undefined (a plugin's `update`) is a nil *int.
//   - The Apply functions return the write they applied as a StoreWrite, and the step's error as a returned one.
//   - Async contract: Pi's `apply` is `async` over the step's calls; each Apply here blocks and returns
//     `(writes, error)`, running its statements in the same order on the commit step's goroutine.

// StoredKey is a key as writes record it: the key columns' stored values, in key order.
type StoredKey = *delta.JsonObject

// ColumnVersions are `json(...)` columns' versions written by a write, by column, in set order.
type ColumnVersions = *delta.JsonObject

// TableDefinitionWrite is the `table` write.
type TableDefinitionWrite struct {
	Table       string
	Version     int
	State       TableState
	Columns     ColumnRecords
	Key         []string
	Fingerprint string
}

// StoreWriteKind is "table".
func (*TableDefinitionWrite) StoreWriteKind() string { return "table" }

type tableWriteJSON struct {
	Kind        string        `json:"kind"`
	Table       string        `json:"table"`
	Version     int           `json:"version"`
	State       TableState    `json:"state"`
	Columns     ColumnRecords `json:"columns"`
	Key         []string      `json:"key"`
	Fingerprint string        `json:"fingerprint"`
}

// MarshalJSON is the write in Pi's key order.
func (write *TableDefinitionWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(tableWriteJSON{"table", write.Table, write.Version, write.State, write.Columns, namesOrEmpty(write.Key), write.Fingerprint})
}

func namesOrEmpty(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}

// IndexWrite is the `index` write.
type IndexWrite struct {
	Table   string
	Name    string
	Columns []string
}

// StoreWriteKind is "index".
func (*IndexWrite) StoreWriteKind() string { return "index" }

type indexWriteJSON struct {
	Kind    string   `json:"kind"`
	Table   string   `json:"table"`
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// MarshalJSON is the write in Pi's key order.
func (write *IndexWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(indexWriteJSON{"index", write.Table, write.Name, namesOrEmpty(write.Columns)})
}

// DropIndexWrite is the `dropIndex` write.
type DropIndexWrite struct {
	Table string
	Name  string
}

// StoreWriteKind is "dropIndex".
func (*DropIndexWrite) StoreWriteKind() string { return "dropIndex" }

type dropIndexWriteJSON struct {
	Kind  string `json:"kind"`
	Table string `json:"table"`
	Name  string `json:"name"`
}

// MarshalJSON is the write in Pi's key order.
func (write *DropIndexWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(dropIndexWriteJSON{"dropIndex", write.Table, write.Name})
}

// AlterWrite is the `alter` write: a column edit of the migration to Version; DDL rendered from it.
type AlterWrite struct {
	Table   string
	Version int
	Edit    AlterEdit
}

// StoreWriteKind is "alter".
func (*AlterWrite) StoreWriteKind() string { return "alter" }

// editJSON is an edit as JSON in Pi's key order.
func editJSON(edit TableEdit) *delta.JsonObject {
	switch edit.Kind {
	case EditRename:
		return delta.JsonObjectOf("kind", edit.Kind, "from", edit.From, "to", edit.To)
	case EditAdd, EditJSON:
		return delta.JsonObjectOf("kind", edit.Kind, "name", edit.Name, "column", edit.Column)
	case EditIndex:
		return delta.JsonObjectOf("kind", edit.Kind, "name", edit.Name, "columns", namesOrEmpty(edit.Columns))
	}
	return delta.JsonObjectOf("kind", edit.Kind, "name", edit.Name)
}

// MarshalJSON is the write in Pi's key order.
func (write *AlterWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(delta.JsonObjectOf("kind", "alter", "table", write.Table, "version", write.Version, "edit", editJSON(write.Edit)))
}

// PutWrite is the `put` write.
type PutWrite struct {
	Table string
	Row   StoredRow
	// Version is the row's eager-step version (the table's version at the `put`).
	Version int
	Columns ColumnVersions
}

// StoreWriteKind is "put".
func (*PutWrite) StoreWriteKind() string { return "put" }

type putWriteJSON struct {
	Kind    string         `json:"kind"`
	Table   string         `json:"table"`
	Row     StoredRow      `json:"row"`
	Version int            `json:"version"`
	Columns ColumnVersions `json:"columns"`
}

// MarshalJSON is the write in Pi's key order.
func (write *PutWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(putWriteJSON{"put", write.Table, write.Row, write.Version, write.Columns})
}

// UpdateWrite is the `update` write of one row by key.
type UpdateWrite struct {
	Table   string
	Key     StoredKey
	Set     SetNode
	Columns ColumnVersions
	Count   int64
	// Version is a migration's data step: the version it stamps as the row's `$v` (a plugin's `update` never does);
	// nil for none.
	Version *int
}

// StoreWriteKind is "update".
func (*UpdateWrite) StoreWriteKind() string { return "update" }

type updateWriteJSON struct {
	Kind    string         `json:"kind"`
	Table   string         `json:"table"`
	Key     StoredKey      `json:"key"`
	Set     SetNode        `json:"set"`
	Columns ColumnVersions `json:"columns"`
	Count   int64          `json:"count"`
	Version *int           `json:"version,omitempty"`
}

// MarshalJSON is the write in Pi's key order.
func (write *UpdateWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(updateWriteJSON{"update", write.Table, write.Key, write.Set, write.Columns, write.Count, write.Version})
}

// DeleteWrite is the `delete` write of one row by key.
type DeleteWrite struct {
	Table string
	Key   StoredKey
	Count int64
}

// StoreWriteKind is "delete".
func (*DeleteWrite) StoreWriteKind() string { return "delete" }

type deleteWriteJSON struct {
	Kind  string    `json:"kind"`
	Table string    `json:"table"`
	Key   StoredKey `json:"key"`
	Count int64     `json:"count"`
}

// MarshalJSON is the write in Pi's key order.
func (write *DeleteWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(deleteWriteJSON{"delete", write.Table, write.Key, write.Count})
}

// UpdateWhereWrite is the `updateWhere` write.
type UpdateWhereWrite struct {
	Table   string
	Where   WhereNode
	Set     SetNode
	Columns ColumnVersions
	Count   int64
	// Version is a migration's `update` step: the version it stamps as the rows' `$v`; nil for none.
	Version *int
}

// StoreWriteKind is "updateWhere".
func (*UpdateWhereWrite) StoreWriteKind() string { return "updateWhere" }

type updateWhereWriteJSON struct {
	Kind    string         `json:"kind"`
	Table   string         `json:"table"`
	Where   WhereNode      `json:"where"`
	Set     SetNode        `json:"set"`
	Columns ColumnVersions `json:"columns"`
	Count   int64          `json:"count"`
	Version *int           `json:"version,omitempty"`
}

// MarshalJSON is the write in Pi's key order.
func (write *UpdateWhereWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(updateWhereWriteJSON{"updateWhere", write.Table, write.Where, write.Set, write.Columns, write.Count, write.Version})
}

// DeleteWhereWrite is the `deleteWhere` write.
type DeleteWhereWrite struct {
	Table string
	Where WhereNode
	Count int64
}

// StoreWriteKind is "deleteWhere".
func (*DeleteWhereWrite) StoreWriteKind() string { return "deleteWhere" }

type deleteWhereWriteJSON struct {
	Kind  string    `json:"kind"`
	Table string    `json:"table"`
	Where WhereNode `json:"where"`
	Count int64     `json:"count"`
}

// MarshalJSON is the write in Pi's key order.
func (write *DeleteWhereWrite) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(deleteWhereWriteJSON{"deleteWhere", write.Table, write.Where, write.Count})
}

// JsonVersions are the versions of the `json(...)` columns of definition that set writes, which its statement
// stamps.
func JsonVersions(definition *TableDefinition, set SetNode) map[string]int {
	versions := map[string]int{}
	for _, entry := range set {
		column := definition.ByName[entry.Column]
		if column.Type == ColJson {
			versions[entry.Column] = column.Json.Version
		}
	}
	return versions
}

// RecordedVersions are the `columns` a write records: the versions of the `json(...)` columns it sets to a value (a
// clear has none), in set order.
func RecordedVersions(set SetNode, versions map[string]int) ColumnVersions {
	columns := delta.NewJsonObject(len(versions))
	for _, entry := range set {
		version, ok := versions[entry.Column]
		if ok && entry.Node.Op != ExprNull {
			columns.Set(entry.Column, float64(version))
		}
	}
	return columns
}

// StageTable stages a `table` write: the table created when the store does not have it yet, its record replaced
// otherwise.
func StageTable(write *TableDefinitionWrite) StagedWrite {
	return stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		return applied(ApplyTable(step, write))
	}}
}

// StageIndex stages an `index` write.
func StageIndex(write *IndexWrite) StagedWrite {
	return stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		return applied(ApplyIndex(step, write))
	}}
}

// applied is one applied write as a step's recorded writes.
func applied(write StoreWrite, err error) ([]StoreWrite, error) {
	if err != nil {
		return nil, err
	}
	return []StoreWrite{write}, nil
}

// StageEdit stages one edit of the migration of table to version as its `alter`, `index` or `dropIndex` write.
func StageEdit(table string, version int, edit TableEdit) StagedWrite {
	switch edit.Kind {
	case EditIndex:
		return StageIndex(&IndexWrite{Table: table, Name: edit.Name, Columns: edit.Columns})
	case EditDropIndex:
		write := &DropIndexWrite{Table: table, Name: edit.Name}
		return stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
			return applied(ApplyDropIndex(step, write))
		}}
	}
	write := &AlterWrite{Table: table, Version: version, Edit: edit}
	return stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		return applied(ApplyAlter(step, write))
	}}
}

// StagePut stages a `put` of an encoded row with its `INSERT OR REPLACE` (an upsert: the whole row replaced).
func StagePut(sql, table string, row *EncodedRow, version int) StagedWrite {
	write := &PutWrite{Table: table, Row: row.Row, Version: version, Columns: row.Columns}
	return stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		if err := step.Run(sql, row.Params...); err != nil {
			return nil, err
		}
		return []StoreWrite{write}, nil
	}}
}

// StageUpdate stages an `update` of one row by key; its count (0 for an absent key) is recorded, and version when the
// statement stamps the row's `$v` (a migration's `rows` step; nil for none).
func StageUpdate(statement Statement, table string, key StoredKey, set SetNode, columns ColumnVersions, version *int) StagedWrite {
	return counted(statement, func(count int64) StoreWrite {
		return &UpdateWrite{Table: table, Key: key, Set: set, Columns: columns, Count: count, Version: version}
	})
}

// StageDelete stages a `delete` of one row by key; its count is recorded.
func StageDelete(statement Statement, table string, key StoredKey) StagedWrite {
	return counted(statement, func(count int64) StoreWrite { return &DeleteWrite{Table: table, Key: key, Count: count} })
}

// StageUpdateWhere stages an `updateWhere`: one statement, its count recorded (a replica affecting another count
// stops, S17), and version when it stamps the rows' `$v` (a migration's `update` step; nil for none).
func StageUpdateWhere(statement Statement, table string, where WhereNode, set SetNode, columns ColumnVersions, version *int) StagedWrite {
	return counted(statement, func(count int64) StoreWrite {
		return &UpdateWhereWrite{Table: table, Where: where, Set: set, Columns: columns, Count: count, Version: version}
	})
}

// StageDeleteWhere stages a `deleteWhere`: one statement, its count recorded.
func StageDeleteWhere(statement Statement, table string, where WhereNode) StagedWrite {
	return counted(statement, func(count int64) StoreWrite { return &DeleteWhereWrite{Table: table, Where: where, Count: count} })
}

func counted(statement Statement, record func(count int64) StoreWrite) StagedWrite {
	return stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		count, err := step.RunCounted(statement.SQL, statement.Params...)
		if err != nil {
			return nil, err
		}
		return []StoreWrite{record(count)}, nil
	}}
}

// ApplyTable applies a `table` write: `CREATE TABLE` when the store does not have the table yet, then its record.
func ApplyTable(step *Step, write *TableDefinitionWrite) (StoreWrite, error) {
	existing := step.Table(write.Table)
	if existing == nil {
		if err := step.Run(CreateTableSql(write.Table, write.Columns, write.Key)); err != nil {
			return nil, err
		}
	}
	indexes := IndexRecords{}
	if existing != nil {
		indexes = existing.Indexes
	}
	step.Tables[write.Table] = &TableRecord{
		Table: write.Table, Version: write.Version, State: write.State, Columns: write.Columns, Key: write.Key,
		Fingerprint: write.Fingerprint, Indexes: indexes,
	}
	return write, nil
}

// ApplyIndex applies an `index` write: `CREATE INDEX` and the record's index.
func ApplyIndex(step *Step, write *IndexWrite) (StoreWrite, error) {
	existing, err := recorded(step, write.Table)
	if err != nil {
		return nil, err
	}
	if err := step.Run(CreateIndexSql(write.Table, write.Name, write.Columns, existing.Key)); err != nil {
		return nil, err
	}
	indexes := slices.Clone(existing.Indexes)
	if at := slices.IndexFunc(indexes, func(index IndexRecord) bool { return index.Name == write.Name }); at >= 0 {
		indexes[at].Columns = write.Columns
	} else {
		indexes = append(indexes, IndexRecord{Name: write.Name, Columns: write.Columns})
	}
	changed := *existing
	changed.Indexes = indexes
	step.Tables[write.Table] = &changed
	return write, nil
}

// ApplyDropIndex applies a `dropIndex` write: `DROP INDEX` and the record without it.
func ApplyDropIndex(step *Step, write *DropIndexWrite) (StoreWrite, error) {
	existing, err := recorded(step, write.Table)
	if err != nil {
		return nil, err
	}
	if err := step.Run(DropIndexSql(write.Table, write.Name)); err != nil {
		return nil, err
	}
	indexes := IndexRecords{}
	for _, index := range existing.Indexes {
		if index.Name != write.Name {
			indexes = append(indexes, index)
		}
	}
	changed := *existing
	changed.Indexes = indexes
	step.Tables[write.Table] = &changed
	return write, nil
}

// ApplyAlter applies an `alter` write: its DDL (a `json(...)` column's version column follows it) and the record's
// change.
func ApplyAlter(step *Step, write *AlterWrite) (StoreWrite, error) {
	existing, err := recorded(step, write.Table)
	if err != nil {
		return nil, err
	}
	edit := write.Edit
	id := Quote(write.Table)
	changed := *existing
	switch edit.Kind {
	case EditRename:
		column, _ := existing.Columns.Get(edit.From)
		if err := step.Run(fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", id, Quote(edit.From), Quote(edit.To))); err != nil {
			return nil, err
		}
		if column.Type == ColJson {
			from, to := VersionColumn(edit.From), VersionColumn(edit.To)
			if err := step.Run(fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", id, Quote(from), Quote(to))); err != nil {
				return nil, err
			}
		}
		rename := func(name string) string {
			if name == edit.From {
				return edit.To
			}
			return name
		}
		columns := make(ColumnRecords, len(existing.Columns))
		for index, entry := range existing.Columns {
			columns[index] = ColumnRecordEntry{Name: rename(entry.Name), Record: entry.Record}
		}
		indexes := make(IndexRecords, len(existing.Indexes))
		for index, entry := range existing.Indexes {
			on := make([]string, len(entry.Columns))
			for at, name := range entry.Columns {
				on[at] = rename(name)
			}
			indexes[index] = IndexRecord{Name: entry.Name, Columns: on}
		}
		key := make([]string, len(existing.Key))
		for index, name := range existing.Key {
			key[index] = rename(name)
		}
		changed.Columns, changed.Key, changed.Indexes = columns, key, indexes
	case EditAdd:
		definitions := ColumnSql(edit.Name, edit.Column)
		for _, definition := range definitions {
			if err := step.Run(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", id, definition)); err != nil {
				return nil, err
			}
		}
		changed.Columns = withColumn(existing.Columns, edit.Name, edit.Column)
	case EditDrop:
		column, _ := existing.Columns.Get(edit.Name)
		if err := step.Run(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", id, Quote(edit.Name))); err != nil {
			return nil, err
		}
		if column.Type == ColJson {
			if err := step.Run(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", id, Quote(VersionColumn(edit.Name)))); err != nil {
				return nil, err
			}
		}
		columns := ColumnRecords{}
		for _, entry := range existing.Columns {
			if entry.Name != edit.Name {
				columns = append(columns, entry)
			}
		}
		changed.Columns = columns
	case EditJSON:
		changed.Columns = withColumn(existing.Columns, edit.Name, edit.Column)
	}
	step.Tables[write.Table] = &changed
	return write, nil
}

// withColumn is columns with column name set: replaced in place, or added last (`{ ...columns, [name]: column }`).
func withColumn(columns ColumnRecords, name string, column ColumnRecord) ColumnRecords {
	next := slices.Clone(columns)
	if at := slices.IndexFunc(next, func(entry ColumnRecordEntry) bool { return entry.Name == name }); at >= 0 {
		next[at].Record = column
		return next
	}
	return append(next, ColumnRecordEntry{Name: name, Record: column})
}

// recorded is the record of table in step; a write for a table the store does not have fails the step (memory and
// disk disagree: the step fails, which stops the store).
func recorded(step *Step, table string) (*TableRecord, error) {
	record := step.Table(table)
	if record == nil {
		return nil, fmt.Errorf("table %s: not in the store", table)
	}
	return record, nil
}
