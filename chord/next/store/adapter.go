package store

import (
	"context"
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/next/store/adapter.ts
//
// The store for adapters (slice 6b §2.1, D1, D2): what an owner outside Chord's objects (Durable's `StoreStorage`)
// uses to keep its data in the store as declared tables and engine documents: its tables declared and prepared under
// its owner name (claimed, the path `self.table` takes), rows encoded at staging and written in a step with what only
// the step knows, and its reads. Internal for adapters; apps use `OpenStore` and objects.
//
// Go forced differences: a read takes the narrow executor it uses (rowGetter / rowLister); a `DecodedRow` is a Row; an
// absent row is nil; Pi's optional `stamp` is "" for none; a missing JavaScript-level argument check that Pi throws is a
// returned error. Async contract: `Prepare`, `Get` and `Select` block until their database calls settled, in call
// order, and return `(value, error)`; `Put` runs inside the commit step.

// DecodedRow is a stored row as reads decode it.
type DecodedRow = Row

// StoreOwnerTables are the tables of one owner name in a store: each declared once (its SQL name `"<owner>:<table>"`
// claimed for the claimant), then prepared together (created at their newest version or migrated, as `self.table`'s
// are).
type StoreOwnerTables struct {
	Store    *ChordStore
	Owner    string
	claimant Claimant
	tables   []*OwnerTable
}

// NewStoreOwnerTables are the tables of owner in store; owner is a restricted name (lowercase `[a-z0-9@/._~-]`): it
// becomes the first part of SQL names.
func NewStoreOwnerTables(store *ChordStore, owner string, claimant Claimant) (*StoreOwnerTables, error) {
	if err := contracts.CheckName(owner, "An owner name"); err != nil {
		return nil, err
	}
	return &StoreOwnerTables{Store: store, Owner: owner, claimant: claimant}, nil
}

// Declare declares definition under the owner, claiming its SQL name (held by another: `invalid/args`).
func (owned *StoreOwnerTables) Declare(definition *TableDefinition) (*OwnerTable, error) {
	table := NewOwnerTable(definition, TableSqlName(owned.Owner, definition.Name))
	if err := owned.Store.Claims.Table(table.Sql.Table, owned.claimant); err != nil {
		return nil, err
	}
	owned.tables = append(owned.tables, table)
	return table, nil
}

// Prepare brings every declared table to its definition: created fresh, or migrated eagerly in their commits.
func (owned *StoreOwnerTables) Prepare(ctx context.Context) error {
	tables := make([]DeclaredTable, len(owned.tables))
	for index, table := range owned.tables {
		tables[index] = DeclaredTable{Definition: table.Definition, Table: table.Sql.Table}
	}
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
	return PrepareTables(tables, owned.Store, fence, owned.Owner, ctx)
}

// OwnerTable is one declared table of an owner: its rows encoded and written in a step, its reads by key and by query.
type OwnerTable struct {
	Definition *TableDefinition
	Sql        *TableSql
}

// NewOwnerTable is the declared table definition at SQL name table.
func NewOwnerTable(definition *TableDefinition, table string) *OwnerTable {
	named := make([]NamedType, len(definition.Columns))
	for index, column := range definition.Columns {
		named[index] = NamedType{Name: column.Name, Type: column.Type}
	}
	return &OwnerTable{Definition: definition, Sql: NewTableSql(table, named, definition.Rows.Physical, definition.Key)}
}

// Encode is row checked and encoded for a `put` (a trust boundary, rule 8: at staging, before the queue).
func (table *OwnerTable) Encode(row any) (*EncodedRow, error) {
	return table.Definition.Rows.Put(row, table.Definition.Version)
}

// Put writes encoded in step (an upsert) and returns the `put` as recorded; stamp names an `int` column that takes
// the step's seq (Durable's `entries.commit_seq`), in the row and in the record ("" for none).
func (table *OwnerTable) Put(step *Step, encoded *EncodedRow, stamp string) (StoreWrite, error) {
	version := table.Definition.Version
	name := table.Sql.Table
	if stamp == "" {
		if err := step.Run(table.Sql.Put, encoded.Params...); err != nil {
			return nil, err
		}
		return &PutWrite{Table: name, Row: encoded.Row, Version: version, Columns: encoded.Columns}, nil
	}
	params := append([]sqlite.SqliteValue(nil), encoded.Params...)
	for index, column := range table.Definition.Rows.Physical {
		if column == stamp {
			params[index] = step.Seq
			break
		}
	}
	if err := step.Run(table.Sql.Put, params...); err != nil {
		return nil, err
	}
	stamped := encoded.Row.Clone()
	stamped.Set(stamp, float64(step.Seq))
	return &PutWrite{Table: name, Row: stamped, Version: version, Columns: encoded.Columns}, nil
}

// Get is the row with the key params (in key order), decoded; nil when absent.
func (table *OwnerTable) Get(executor rowGetter, params []sqlite.SqliteValue, ctx context.Context) (DecodedRow, error) {
	stored, err := executor.Get(table.Sql.Get, params...)
	if err != nil || stored == nil {
		return nil, err
	}
	return table.Definition.Rows.Decode(stored, ctx)
}

// Prepare is a read compiled once (the store's query vocabulary rendered to SQL): columns of the rows where matches
// (nil: all) in order. The values in where only shape it: each is a `?` the caller binds in the order the conditions
// take them, then the limit. Adapters keep their fixed reads so: no SQL is rendered per call.
func (table *OwnerTable) Prepare(columns []string, where *WhereNode, order Order) string {
	return table.Sql.Select(where, order, nil, 0, columns).SQL
}

// PrepareFirst is Prepare for the first row only: the limit is a literal `1` (a bound one costs SQLite a plan step per
// call).
func (table *OwnerTable) PrepareFirst(columns []string, where *WhereNode, order Order) string {
	sql := table.Sql.Select(where, order, nil, 1, columns).SQL
	if strings.HasSuffix(sql, "LIMIT ?") {
		sql = strings.TrimSuffix(sql, "LIMIT ?") + "LIMIT 1"
	}
	return sql
}

// JsonVersion is the version of `json(...)` column column: at 1 every stored cell is at 1 (none is older, a newer one
// fails the owner when its table is prepared), so a read may leave the version column out.
func (table *OwnerTable) JsonVersion(column string) (int, error) {
	found, err := table.Definition.Column(column, "jsonVersion")
	if err != nil {
		return 0, err
	}
	if found.Type != ColJson {
		return 0, table.notJson(column)
	}
	return found.Json.Version, nil
}

// PrepareGet is a point lookup compiled once: columns of the row whose key the caller binds, in key order.
func (table *OwnerTable) PrepareGet(columns []string) string { return table.Sql.GetColumns(columns) }

// Reader is the read of `json(...)` column column's stored cells (`text` written at `version`, migrated on access),
// made once: an adapter selecting the cell itself reads it so.
func (table *OwnerTable) Reader(column string) (func(text string, version int, ctx context.Context) (any, error), error) {
	found, err := table.Definition.Column(column, "reader")
	if err != nil {
		return nil, err
	}
	if found.Type != ColJson {
		return nil, table.notJson(column)
	}
	// At version 1 a cell is the JSON it was written as: no version to compare, nothing to migrate.
	if found.Json.Version == 1 {
		return func(text string, _ int, _ context.Context) (any, error) { return delta.DecodeJson([]byte(text)) }, nil
	}
	what := table.Sql.Table + "." + column
	return func(text string, version int, ctx context.Context) (any, error) {
		return found.Json.Read(text, version, what, ctx)
	}, nil
}

func (table *OwnerTable) notJson(column string) error {
	return errors.New(table.Definition.Name + `: column "` + column + `" is no json(...) column`)
}

// Select is up to limit rows matching where (nil: all) in order, decoded.
func (table *OwnerTable) Select(executor rowLister, where *WhereNode, order Order, limit int64, ctx context.Context) ([]DecodedRow, error) {
	statement := table.Sql.Select(where, order, nil, limit, nil)
	stored, err := executor.All(statement.SQL, statement.Params...)
	if err != nil {
		return nil, err
	}
	rows := make([]DecodedRow, len(stored))
	for index, row := range stored {
		if rows[index], err = table.Definition.Rows.Decode(row, ctx); err != nil {
			return nil, err
		}
	}
	return rows, nil
}
