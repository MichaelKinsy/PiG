package store

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/next/store/table-evolve.ts
//
// `EvolveTable(previous, (t) => t…)` (slice 6a §9; decision "Second spec review: the majority"): the next version of
// a table from the edits of one migration. At runtime one editor records the edits, checks each as it is made, and
// materializes the new version at the end.
//
// Go forced differences: the editor's types (TableTailEditor, TableEditor, AddedColumn, PatchOf, Renamed, With,
// Without, WithJson, Next) order the edits and track columns, key and version in the type system; they are designed
// out, and TableEditor is one concrete type whose runtime checks (an edit after the data step, a second data step,
// unknown columns, ...) are all kept. Every edit returns its editor so edits chain, and the first failing edit is
// kept: later edits of the chain are skipped and EvolveTable returns that error (upstream: the first failing edit
// throws out of `edit`), as chord/next/contracts' editors do.

// UpdateStep is the data step of `Update`: `where` (optional) and `set` over the rows not migrated yet.
type UpdateStep struct {
	// Where is compiled against the columns at this edit; nil for every row.
	Where func(row ColumnRefs) Condition
	// Set is a SetOf (or a map of column values), or a func(ColumnRefs) SetOf / func(ColumnRefs) map[string]any.
	Set any
}

// EvolveTable appends the next version (`previous.Version + 1`) with the edits edit makes; edit must return the
// editor it was given. Every version is materialized and fingerprinted here; the object serves the newest, and its
// activation migrates a store at an older version through each one.
func EvolveTable(previous *TableDefinition, edit func(t *TableEditor) *TableEditor) (*TableDefinition, error) {
	editor := newEditor(previous)
	editErr := contracts.EditWith(editor, edit, previous.Name+": evolveTable")
	if editor.err != nil {
		return nil, editor.err
	}
	if editErr != nil {
		return nil, editErr
	}
	return editor.finish()
}

const (
	phaseBefore = iota
	phaseAfter
)

// TableEditor is the editor at runtime: the columns, key and indexes as the edits leave them, the edits before and
// after the data step, and the step. Each edit is checked as it is made (declaration policy: JavaScript callers
// reach the checks).
type TableEditor struct {
	previous *TableDefinition
	what     string
	schemas  []ColumnProp
	key      []string
	indexes  IndexRecords
	// phase is before until the data step or the first `Drop`, `Index` or `DropIndex`.
	phase  int
	before []TableEdit
	step   *DataStep
	after  []TableEdit
	err    error
}

func newEditor(previous *TableDefinition) *TableEditor {
	return &TableEditor{
		previous: previous,
		what:     fmt.Sprintf("%s version %d", previous.Name, previous.Version+1),
		schemas:  append([]ColumnProp(nil), previous.Schemas...),
		key:      append([]string(nil), previous.Key...),
		indexes:  cloneIndexes(previous.Indexes),
	}
}

func cloneIndexes(indexes IndexRecords) IndexRecords {
	clone := make(IndexRecords, len(indexes))
	for i, index := range indexes {
		clone[i] = IndexRecord{Name: index.Name, Columns: append([]string(nil), index.Columns...)}
	}
	return clone
}

// fail keeps the first error of the chain.
func (e *TableEditor) fail(err error) *TableEditor {
	if e.err == nil {
		e.err = err
	}
	return e
}

func (e *TableEditor) errorf(format string, args ...any) *TableEditor {
	return e.fail(fmt.Errorf(format, args...))
}

// Rename renames a column; the key, the indexes and a `json(...)` column's version column follow.
func (e *TableEditor) Rename(from, to string) *TableEditor {
	if e.err != nil {
		return e
	}
	if err := e.assertBefore("rename"); err != nil {
		return e.fail(err)
	}
	if _, err := e.assertColumn(from, "rename"); err != nil {
		return e.fail(err)
	}
	// Checked here, not by the final definition: a later edit may rename it again, but this one runs as DDL.
	if err := CheckColumnName(to, e.what+": rename"); err != nil {
		return e.fail(err)
	}
	if err := e.assertNewName(to, "rename", from, true); err != nil {
		return e.fail(err)
	}
	rename := func(column string) string {
		if column == from {
			return to
		}
		return column
	}
	for i := range e.schemas {
		e.schemas[i].Name = rename(e.schemas[i].Name)
	}
	for i := range e.key {
		e.key[i] = rename(e.key[i])
	}
	for i := range e.indexes {
		for j := range e.indexes[i].Columns {
			e.indexes[i].Columns[j] = rename(e.indexes[i].Columns[j])
		}
	}
	e.before = append(e.before, TableEdit{Kind: EditRename, From: from, To: to})
	return e
}

// Add adds a column: `Opt(...)`, or a scalar one with a `default` (existing rows get it).
func (e *TableEditor) Add(name string, column ColumnSchema) *TableEditor {
	if e.err != nil {
		return e
	}
	if err := e.assertBefore("add"); err != nil {
		return e.fail(err)
	}
	if err := e.assertNewName(name, "add", "", false); err != nil {
		return e.fail(err)
	}
	described, err := DescribeColumns([]ColumnProp{Col(name, column)}, e.what+": add")
	if err != nil {
		return e.fail(err)
	}
	if !described[0].Optional && (described[0].Type == ColJson || described[0].Default == nil) {
		return e.errorf("%s: add: column %s needs opt(...) or a default (existing rows get it)", e.what, quote(name))
	}
	e.schemas = append(e.schemas, Col(name, column))
	e.before = append(e.before, TableEdit{Kind: EditAdd, Name: name, Column: ColumnRecordOf(described[0])})
	return e
}

// Json is a `json(...)` column's new version (its nullability kept): no DDL, no data step, cells migrate on access.
func (e *TableEditor) Json(name string, column ColumnSchema) *TableEditor {
	if e.err != nil {
		return e
	}
	if err := e.assertBefore("json"); err != nil {
		return e.fail(err)
	}
	current, err := e.assertColumn(name, "json")
	if err != nil {
		return e.fail(err)
	}
	described, err := DescribeColumns([]ColumnProp{Col(name, current)}, e.what)
	if err != nil {
		return e.fail(err)
	}
	existing := described[0]
	if existing.Type != ColJson {
		return e.errorf("%s: json: column %s is not a json(...) column", e.what, quote(name))
	}
	if column.Optional {
		return e.errorf("%s: json: pass json(...) itself; the column keeps its nullability", e.what)
	}
	schema := column
	if existing.Optional {
		schema = Opt(column)
	}
	next, err := DescribeColumns([]ColumnProp{Col(name, schema)}, e.what+": json")
	if err != nil {
		return e.fail(err)
	}
	if next[0].Type != ColJson {
		return e.errorf("%s: json: expected a json(...) column", e.what)
	}
	before, after := existing.Json, next[0].Json
	// D9 across table versions: a cell's version says which schema it was written with.
	if after.Version < before.Version {
		return e.errorf("%s: json: column %s goes back from version %d", e.what, quote(name), before.Version)
	}
	if after.Version == before.Version && after.Fingerprint != before.Fingerprint {
		return e.errorf("%s: json: column %s changes its schema at version %d (raise its version)", e.what, quote(name), before.Version)
	}
	e.setSchema(name, schema)
	e.before = append(e.before, TableEdit{Kind: EditJSON, Name: name, Column: ColumnRecordOf(next[0])})
	return e
}

// Rows is the data step: patch gets each row as the columns stand here and returns new values of non-key columns
// (an empty patch stamps the row only); synchronous and pure, run in chunks of 100 in key order.
func (e *TableEditor) Rows(patch func(row Row) (any, error)) *TableEditor {
	if e.err != nil {
		return e
	}
	if err := e.assertBefore("rows"); err != nil {
		return e.fail(err)
	}
	if patch == nil {
		return e.errorf("%s: rows: expected a function", e.what)
	}
	table, err := e.current()
	if err != nil {
		return e.fail(err)
	}
	e.step = &DataStep{Kind: StepRows, Table: table, Patch: patch}
	e.phase = phaseAfter
	return e
}

// Update is the data step as one `updateWhere` over the rows not migrated yet.
func (e *TableEditor) Update(step UpdateStep) *TableEditor {
	if e.err != nil {
		return e
	}
	if err := e.assertBefore("update"); err != nil {
		return e.fail(err)
	}
	table, err := e.current()
	if err != nil {
		return e.fail(err)
	}
	what := fmt.Sprintf("version %d update", table.Version)
	var where *WhereNode
	if step.Where != nil {
		compiled, err := CompileWhere(table, step.Where(table.Refs), what)
		if err != nil {
			return e.fail(err)
		}
		where = &compiled
	}
	set := step.Set
	switch typed := set.(type) {
	case func(ColumnRefs) SetOf:
		set = typed(table.Refs)
	case func(ColumnRefs) map[string]any:
		set = typed(table.Refs)
	}
	compiled, err := CompileSet(table, set, what)
	if err != nil {
		return e.fail(err)
	}
	e.step = &DataStep{Kind: StepUpdate, Table: table, Where: where, Set: compiled}
	e.phase = phaseAfter
	return e
}

// Drop drops a non-key column; its indexes are dropped first, in the same commit.
func (e *TableEditor) Drop(name string) *TableEditor {
	if e.err != nil {
		return e
	}
	e.phase = phaseAfter
	if _, err := e.assertColumn(name, "drop"); err != nil {
		return e.fail(err)
	}
	if slices.Contains(e.key, name) {
		return e.errorf("%s: drop: %s is a key column", e.what, quote(name))
	}
	kept := e.indexes[:0:0]
	for _, index := range e.indexes {
		if !slices.Contains(index.Columns, name) {
			kept = append(kept, index)
			continue
		}
		e.after = append(e.after, TableEdit{Kind: EditDropIndex, Name: index.Name})
	}
	e.indexes = kept
	schemas := make([]ColumnProp, 0, len(e.schemas))
	for _, schema := range e.schemas {
		if schema.Name != name {
			schemas = append(schemas, schema)
		}
	}
	e.schemas = schemas
	e.after = append(e.after, TableEdit{Kind: EditDrop, Name: name})
	return e
}

// Index adds an index (`[a-z0-9_]+`) over non-`opt` `str` or `int` columns; it ends with the key.
func (e *TableEditor) Index(name string, columns []string) *TableEditor {
	if e.err != nil {
		return e
	}
	e.phase = phaseAfter
	what := fmt.Sprintf("%s: index %s", e.what, quote(name))
	if !IndexName.MatchString(name) {
		return e.errorf("%s: expected [a-z0-9_]+", what)
	}
	if _, found := e.indexes.Get(name); found {
		return e.errorf("%s exists", what)
	}
	// Checked against the columns at this edit: the migration creates the index here, whatever later edits do.
	described, err := DescribeColumns(e.schemas, e.what)
	if err != nil {
		return e.fail(err)
	}
	byName := make(map[string]Column, len(described))
	for _, column := range described {
		byName[column.Name] = column
	}
	checked, err := OrderColumns(byName, columns, what)
	if err != nil {
		return e.fail(err)
	}
	e.indexes = append(e.indexes, IndexRecord{Name: name, Columns: checked})
	e.after = append(e.after, TableEdit{Kind: EditIndex, Name: name, Columns: checked})
	return e
}

// DropIndex drops an index.
func (e *TableEditor) DropIndex(name string) *TableEditor {
	if e.err != nil {
		return e
	}
	e.phase = phaseAfter
	position := -1
	for i, index := range e.indexes {
		if index.Name == name {
			position = i
		}
	}
	if position < 0 {
		return e.errorf("%s: dropIndex: no index %s", e.what, quote(name))
	}
	e.indexes = append(e.indexes[:position:position], e.indexes[position+1:]...)
	e.after = append(e.after, TableEdit{Kind: EditDropIndex, Name: name})
	return e
}

// finish is the new version, materialized and fingerprinted, linked to the previous one and the migration from it.
func (e *TableEditor) finish() (*TableDefinition, error) {
	migration := Migration{Before: e.before, Step: e.step, After: e.after}
	return NewTableDefinition(e.previous.Name, e.options(), &Evolution{Previous: e.previous, Migration: migration})
}

// current is the table as the edits so far leave it, at the new version: what a data step reads and writes.
func (e *TableEditor) current() (*TableDefinition, error) {
	return NewTableDefinition(e.previous.Name, e.options(), nil)
}

func (e *TableEditor) options() TableOptions {
	return TableOptions{
		Version: e.previous.Version + 1,
		Columns: append([]ColumnProp(nil), e.schemas...),
		Key:     append([]string(nil), e.key...),
		Indexes: cloneIndexes(e.indexes),
	}
}

func (e *TableEditor) setSchema(name string, schema ColumnSchema) {
	for i := range e.schemas {
		if e.schemas[i].Name == name {
			e.schemas[i].Schema = schema
		}
	}
}

func (e *TableEditor) assertBefore(edit string) error {
	if e.phase == phaseAfter {
		return fmt.Errorf("%s: %s comes before the data step and before drop, index and dropIndex", e.what, edit)
	}
	return nil
}

// assertNewName checks that name names no column yet, also ignoring ASCII case (SQLite's column names do): checked
// per edit, since a later `Drop` cannot take back an `Add` the migration runs first. A rename may change only the
// case of its own column (from; hasFrom).
func (e *TableEditor) assertNewName(name, edit, from string, hasFrom bool) error {
	folded := foldAscii(name)
	for _, existing := range e.schemas {
		if (!hasFrom || existing.Name != from) && foldAscii(existing.Name) == folded {
			return fmt.Errorf("%s: %s: column %s exists (names differing only in case collide)", e.what, edit, quote(existing.Name))
		}
	}
	return nil
}

func (e *TableEditor) assertColumn(name, edit string) (ColumnSchema, error) {
	for _, schema := range e.schemas {
		if schema.Name == name {
			return schema.Schema, nil
		}
	}
	return ColumnSchema{}, fmt.Errorf("%s: %s: no column %s", e.what, edit, quote(name))
}

// foldAscii is name with ASCII letters lowercased and nothing else changed, as SQLite folds identifiers.
func foldAscii(name string) string {
	var folded strings.Builder
	folded.Grow(len(name))
	for i := range len(name) {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		folded.WriteByte(c)
	}
	return folded.String()
}
