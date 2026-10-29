package store

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/table.ts
//
// Table definitions (slice 6a §5, §9; decisions "Flat columns", "Restricted names", "Index definitions are
// recorded"): `DefineTable(name, TableOptions{Version, Columns, Key, Indexes})`, checked and materialized once at
// declaration: its columns, its key, its indexes, its fingerprint and the compiled row codec its writes and reads
// use. A version `EvolveTable` appends links to the one before it and to the migration between them.
//
// Go forced differences: the type parameters (columns C, key K, version V) and TableOptions' typed key/index columns
// are designed out (the runtime checks stay: Pi's declaration policy for JavaScript callers); the columns are an
// ordered []ColumnProp, the indexes an ordered IndexRecords; DefineTable returns the declaration error where Pi
// throws it.

// TableOptions is what DefineTable takes.
type TableOptions struct {
	// Version is the table's version: a positive integer, raised by every change (`EvolveTable`).
	Version int
	Columns []ColumnProp
	// Key is the primary key's columns, non-`opt` `str` or `int` ones.
	Key []string
	// Indexes are the secondary indexes by name (`[a-z0-9_]+`); each ends with the primary key.
	Indexes IndexRecords
}

// IndexDef is an index declaration: its name and columns.
type IndexDef = IndexRecord

// DefineTable declares a table: a base version written in full, the first of its history. Later versions come from
// EvolveTable; a new base starts a new history.
func DefineTable(name string, options TableOptions) (*TableDefinition, error) {
	return NewTableDefinition(name, options, nil)
}

// TableFormVersion is the table form's version: the fingerprints of table definitions are persisted, so their input
// is versioned.
const TableFormVersion = 1

// IndexName is the index name rule.
var IndexName = regexp.MustCompile(`^[a-z0-9_]+$`)

// Evolution is how a version came about: the version before it and the migration from there, for one `EvolveTable`.
type Evolution struct {
	Previous  *TableDefinition
	Migration Migration
}

// TableDefinition is a declared table at one version: its columns in declaration order, key, indexes (declared
// columns; the key is appended when they are rendered), the fingerprint of all of it and the row codec.
type TableDefinition struct {
	Name    string
	Version int
	// Schemas are the column schemas as declared, which `EvolveTable` edits into the next version's.
	Schemas []ColumnProp
	Columns []Column
	ByName  map[string]Column
	Key     []string
	Indexes IndexRecords
	// Fingerprint is `Hash64` of the columns, key and indexes in TableFormVersion's form.
	Fingerprint string
	// Record is the columns as the `table` write records them.
	Record ColumnRecords
	Rows   *RowCodec
	// Refs is one reference per column, for the `(t) => …` callbacks of queries and writes.
	Refs ColumnRefs
	// Evolution is the version before this one and the migration from it; nil for a base.
	Evolution *Evolution
}

// NewTableDefinition is the checked and materialized definition of name (declaration policy: JavaScript callers
// reach these checks).
func NewTableDefinition(name string, options TableOptions, evolution *Evolution) (*TableDefinition, error) {
	if err := contracts.CheckName(name, "table"); err != nil {
		return nil, err
	}
	if options.Version < 1 || int64(options.Version) > MaxSafeInteger {
		return nil, fmt.Errorf("%s: version %d is not a positive integer", name, options.Version)
	}
	columns, err := DescribeColumns(options.Columns, name)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Column, len(columns))
	for _, column := range columns {
		byName[column.Name] = column
	}
	key, err := OrderColumns(byName, options.Key, name+": the key")
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("%s: the key has no column", name)
	}
	indexes := make(IndexRecords, 0, len(options.Indexes))
	for _, index := range options.Indexes {
		if !IndexName.MatchString(index.Name) {
			return nil, fmt.Errorf("%s: index %s: expected [a-z0-9_]+", name, quote(index.Name))
		}
		for _, existing := range indexes {
			if existing.Name == index.Name {
				return nil, fmt.Errorf("%s: index %s declared twice", name, quote(index.Name))
			}
		}
		checked, err := OrderColumns(byName, index.Columns, fmt.Sprintf("%s: index %s", name, quote(index.Name)))
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, IndexRecord{Name: index.Name, Columns: checked})
	}
	record := make(ColumnRecords, len(columns))
	for i, column := range columns {
		record[i] = ColumnRecordEntry{Name: column.Name, Record: ColumnRecordOf(column)}
	}
	fingerprint, err := TableFingerprint(record, key, indexes)
	if err != nil {
		return nil, err
	}
	return &TableDefinition{
		Name:        name,
		Version:     options.Version,
		Schemas:     append([]ColumnProp(nil), options.Columns...),
		Columns:     columns,
		ByName:      byName,
		Key:         key,
		Indexes:     indexes,
		Fingerprint: fingerprint,
		Record:      record,
		Rows:        NewRowCodec(columns, key, name),
		Refs:        columnRefs(columns),
		Evolution:   evolution,
	}, nil
}

// Column is the column name; an unknown one is an error (what names the use).
func (definition *TableDefinition) Column(name, what string) (Column, error) {
	column, found := definition.ByName[name]
	if !found {
		return Column{}, fmt.Errorf("%s: %s: no column %s", definition.Name, what, quote(name))
	}
	return column, nil
}

// Named is the columns as the SQL renderer takes them (`NewTableSql`'s columns).
func (definition *TableDefinition) Named() []NamedType {
	named := make([]NamedType, len(definition.Columns))
	for i, column := range definition.Columns {
		named[i] = NamedType{Name: column.Name, Type: column.Type}
	}
	return named
}

// Index is the declared columns of index name.
func (definition *TableDefinition) Index(name string) ([]string, bool) {
	return definition.Indexes.Get(name)
}

// OrderColumns checks names as key, index or order columns of a table whose columns are byName: existing, non-`opt`
// `str` or `int`, no column twice (declaration policy: JavaScript callers reach it); what names the use in errors.
func OrderColumns(byName map[string]Column, names []string, what string) ([]string, error) {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			return nil, fmt.Errorf("%s: a column occurs twice", what)
		}
		seen[name] = true
	}
	for _, name := range names {
		column, found := byName[name]
		if !found {
			return nil, fmt.Errorf("%s: no column %s", what, quote(name))
		}
		if column.Optional || (column.Type != ColStr && column.Type != ColInt) {
			return nil, fmt.Errorf("%s: column %s is not a non-opt str() or int() column", what, quote(name))
		}
	}
	return append([]string{}, names...), nil
}

// fingerprintValue and fingerprintInput serialize in the key order of Pi's `{ tableFormVersion, value }`.
type fingerprintValue struct {
	Columns ColumnRecords `json:"columns"`
	Key     []string      `json:"key"`
	Indexes IndexRecords  `json:"indexes"`
}

type fingerprintInput struct {
	TableFormVersion int              `json:"tableFormVersion"`
	Value            fingerprintValue `json:"value"`
}

// TableFingerprint is the fingerprint of a table's columns, key and indexes; column and index names sorted (their
// order is no change).
func TableFingerprint(columns ColumnRecords, key []string, indexes IndexRecords) (string, error) {
	sortedColumns := append(make(ColumnRecords, 0, len(columns)), columns...)
	sort.SliceStable(sortedColumns, func(i, j int) bool { return sortedColumns[i].Name < sortedColumns[j].Name })
	// Object.fromEntries: array-index names come first (JavaScript's own key order).
	jsstring.SortOwnKeys(sortedColumns, func(entry ColumnRecordEntry) string { return entry.Name })
	sortedIndexes := append(make(IndexRecords, 0, len(indexes)), indexes...)
	sort.SliceStable(sortedIndexes, func(i, j int) bool { return sortedIndexes[i].Name < sortedIndexes[j].Name })
	jsstring.SortOwnKeys(sortedIndexes, func(entry IndexRecord) string { return entry.Name })
	if key == nil {
		key = []string{}
	}
	text, err := jsstring.MarshalJSON(fingerprintInput{
		TableFormVersion: TableFormVersion,
		Value:            fingerprintValue{Columns: sortedColumns, Key: key, Indexes: sortedIndexes},
	})
	if err != nil {
		return "", err
	}
	return contracts.Hash64(string(text)), nil
}
