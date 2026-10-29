package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/applier.ts
//
// The applier (slice 6a §13, slice 6b §1.6): recorded writes applied as they were recorded, inside a backend
// transaction, checked against the store's own records (a trust boundary: archives, a replica's batches). One code
// path for import, bootstrap and batch apply, for every write kind; a recorded count the store does not reproduce
// stops applying (`Diverged`, S17).
//
// Async contract: Pi's `applyWrite` awaits its step's statements in order; here they block in the same order and the
// step's error is the returned one (a refusal is `invalid/args`, a divergence a *Diverged). No context is taken (Pi
// takes none).
//
// Go forced differences:
//   - Pi's `write.kind` switch is a type switch over the typed writes (engine.go, table_writes.go); a type outside
//     them is refused (Pi's switch would apply nothing: its closed schema never lets one through).
//   - The recorded nodes are typed (WhereNode, ExprNode, SetNode) and well-formed by construction of their decoders
//     (DecodeWhereNode...): the checks that remain are the ones of the records (columns, scalars, operations), with
//     Pi's messages. A `json(...)` column's recorded fingerprint is a string, so "has a fingerprint" is non-empty.

// Diverged is a recorded write the store does not reproduce (a count, a revision): applying stops, the store
// diverged (S17).
type Diverged struct{ message string }

// Error is the message.
func (diverged *Diverged) Error() string { return diverged.message }

func divergedf(format string, args ...any) error {
	return &Diverged{message: fmt.Sprintf(format, args...)}
}

// IsDiverged reports whether err is (or wraps) a Diverged: Pi's `error instanceof Diverged`.
func IsDiverged(err error) bool {
	var diverged *Diverged
	return errors.As(err, &diverged)
}

// jsString is JSON.stringify of a string (the form names take in messages).
func jsString(text string) string {
	quoted, err := jsstring.MarshalJSON(text)
	if err != nil {
		return `"` + text + `"`
	}
	return string(quoted)
}

// keysOf is the keys of a recorded object; none for an absent one.
func keysOf(object *delta.JsonObject) []string {
	if object == nil {
		return nil
	}
	return object.Keys()
}

// applyWrite applies write in step; a write the records refuse fails `invalid/args`, one they do not reproduce
// *Diverged.
func applyWrite(step *Step, write StoreWrite, what string) error {
	switch write := write.(type) {
	case *TableDefinitionWrite:
		if err := checkTable(write, what); err != nil {
			return err
		}
		_, err := ApplyTable(step, write)
		return err
	case *IndexWrite:
		record, err := recordedTable(step, write.Table, what)
		if err != nil {
			return err
		}
		if err := checkIndex(record, write, what); err != nil {
			return err
		}
		_, err = ApplyIndex(step, write)
		return err
	case *DropIndexWrite:
		record, err := recordedTable(step, write.Table, what)
		if err != nil {
			return err
		}
		if _, has := record.Indexes.Get(write.Name); !has {
			return invalidArgs("%s: no index %s", what, jsString(write.Name))
		}
		_, err = ApplyDropIndex(step, write)
		return err
	case *AlterWrite:
		record, err := recordedTable(step, write.Table, what)
		if err != nil {
			return err
		}
		if err := checkAlter(record, write, what); err != nil {
			return err
		}
		_, err = ApplyAlter(step, write)
		return err
	case *PutWrite:
		record, err := recordedTable(step, write.Table, what)
		if err != nil {
			return err
		}
		physical, sql := RecordedPut(write.Table, record.Columns)
		params, err := putParams(record, write, physical, what)
		if err != nil {
			return err
		}
		return step.Run(sql, params...)
	case *UpdateWrite, *DeleteWrite, *UpdateWhereWrite, *DeleteWhereWrite:
		return applyCounted(step, write, what)
	default:
		return applyDocument(step, write, what)
	}
}

// applyDocument applies a document write: a creation, a change or a retirement on its recorded revision, an
// archive's revision.
func applyDocument(step *Step, write StoreWrite, what string) error {
	switch write := write.(type) {
	case *DocumentCreate:
		var text *string
		if write.Base != nil {
			base, err := jsonOf(write.Base.Value, what)
			if err != nil {
				return err
			}
			text = &base
		}
		return InsertDocument(step, write.Record, text)
	case *DocumentRevisionWrite:
		content := write.Content
		text, err := contentText(content.Kind, content.Value, content.Ops, what)
		if err != nil {
			return err
		}
		return InsertRevision(step, write.ID, write.Seq, content.Kind, content.Version, text)
	case *DocumentChange:
		record, err := liveAt(step, write.ID, write.Revision, what)
		if err != nil {
			return err
		}
		content := write.Content
		text, err := contentText(content.Kind, content.Value, content.Ops, what)
		if err != nil {
			return err
		}
		return WriteContent(step, write.ID, record.History, write.Revision, content, text)
	case *DocumentRetire:
		record, err := liveAt(step, write.ID, write.Revision, what)
		if err != nil {
			return err
		}
		return RetireRecord(step, *record)
	}
	return invalidArgs("%s: not a store write", what)
}

// contentText is a content's stored text: a base's value or a delta's ops, checked as JSON.
func contentText(kind string, value delta.JsonValue, ops []delta.Op, what string) (string, error) {
	if kind == ContentBase {
		return jsonOf(value, what)
	}
	// Ops are typed values the encoder accepts or refuses as JSON.stringify would; no strict-JSON walk applies.
	text, err := jsstring.MarshalJSON(opsOrEmpty(ops))
	if err != nil {
		return "", invalidArgs("%s: not JSON (%s)", what, err.Error())
	}
	return string(text), nil
}

// liveAt is the live record id at revision; another revision, or none, is a divergence.
func liveAt(step *Step, id, revision int64, what string) (*DocumentRecord, error) {
	record, err := ReadDocument(step, id)
	if err != nil {
		return nil, err
	}
	if record == nil || record.RetiredAt != nil || record.Revision != revision {
		return nil, divergedf("%s: document %d is not live at revision %d", what, id, revision)
	}
	return record, nil
}

// jsonOf is value serialized; one JSON.stringify cannot encode is refused.
func jsonOf(value any, what string) (string, error) {
	if err := chordjson.Validate(value); err != nil {
		return "", invalidArgs("%s: not JSON (%s)", what, err.Error())
	}
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		return "", invalidArgs("%s: not JSON (%s)", what, err.Error())
	}
	return string(text), nil
}

// applyCounted applies a counted table write: its statement rendered from the record's columns and the recorded key,
// where and set (checked: only recorded columns, well-formed nodes), and its count compared with the recorded one.
func applyCounted(step *Step, write StoreWrite, what string) error {
	var table string
	var count int64
	switch write := write.(type) {
	case *UpdateWrite:
		table, count = write.Table, write.Count
	case *DeleteWrite:
		table, count = write.Table, write.Count
	case *UpdateWhereWrite:
		table, count = write.Table, write.Count
	case *DeleteWhereWrite:
		table, count = write.Table, write.Count
	}
	record, err := recordedTable(step, table, what)
	if err != nil {
		return err
	}
	columns := make([]NamedType, len(record.Columns))
	for index, entry := range record.Columns {
		columns[index] = NamedType{Name: entry.Name, Type: entry.Record.Type}
	}
	sql := NewTableSql(table, columns, nil, record.Key)
	var statement Statement
	switch write := write.(type) {
	case *UpdateWrite:
		set, err := setOf(record, write.Set, write.Version, what)
		if err != nil {
			return err
		}
		key, err := keyParams(record, write.Key, what)
		if err != nil {
			return err
		}
		versions, err := versionsOf(record, write.Set, write.Columns, what)
		if err != nil {
			return err
		}
		statement = sql.Update(set, key, versions)
	case *DeleteWrite:
		key, err := keyParams(record, write.Key, what)
		if err != nil {
			return err
		}
		statement = Statement{SQL: sql.Delete, Params: key}
	case *UpdateWhereWrite:
		if err := checkWhere(record, write.Where, what); err != nil {
			return err
		}
		set, err := setOf(record, write.Set, write.Version, what)
		if err != nil {
			return err
		}
		versions, err := versionsOf(record, write.Set, write.Columns, what)
		if err != nil {
			return err
		}
		statement = sql.UpdateWhere(write.Where, set, versions)
	case *DeleteWhereWrite:
		if err := checkWhere(record, write.Where, what); err != nil {
			return err
		}
		statement = sql.DeleteWhere(write.Where)
	}
	changed, err := step.RunCounted(statement.SQL, statement.Params...)
	if err != nil {
		return err
	}
	if changed != count {
		return divergedf("%s: changed %d rows, recorded %d", what, changed, count)
	}
	return nil
}

// keyParams is the recorded key as parameters in key order: exactly the key columns, each of its type.
func keyParams(record *TableRecord, key StoredKey, what string) ([]sqlite.SqliteValue, error) {
	if key.Len() != len(record.Key) {
		return nil, invalidArgs("%s: not a key of %s", what, record.Table)
	}
	params := make([]sqlite.SqliteValue, len(record.Key))
	for index, name := range record.Key {
		if !key.Has(name) {
			return nil, invalidArgs("%s: key column %s is missing", what, jsString(name))
		}
		column, recorded := record.Columns.Get(name)
		if !recorded {
			return nil, invalidArgs("%s: key column %s is not a column of %s", what, jsString(name), record.Table)
		}
		scalar, err := CheckScalar(scalarType(column), key.Value(name), what+": key")
		if err != nil {
			return nil, err
		}
		params[index] = ToSqlite(scalar)
	}
	return params, nil
}

// setOf is a recorded set with the migration stamp `$v` when it carries one; key columns never change (rule 13).
func setOf(record *TableRecord, set SetNode, version *int, what string) (SetNode, error) {
	if len(set) == 0 {
		return nil, invalidArgs("%s: an empty or malformed set", what)
	}
	for _, entry := range set {
		if _, recorded := record.Columns.Get(entry.Column); !recorded || slices.Contains(record.Key, entry.Column) {
			return nil, invalidArgs("%s: set writes %s, no non-key column", what, jsString(entry.Column))
		}
		if err := checkExpr(record, entry.Node, what); err != nil {
			return nil, err
		}
	}
	if version == nil {
		return set, nil
	}
	return set.With(RowVersion, ExprNode{Op: ExprLiteral, Value: int64(*version)}), nil
}

// versionsOf is the `json(...)` versions a set stamps: the recorded ones, of `json(...)` columns it sets, up to
// theirs.
func versionsOf(record *TableRecord, set SetNode, columns ColumnVersions, what string) (map[string]int, error) {
	versions := map[string]int{}
	for _, entry := range record.Columns {
		if entry.Record.Type != ColJson {
			continue
		}
		if _, sets := set.Get(entry.Name); !sets {
			continue
		}
		// A clear stamps `NULL` whatever the version; a value stamps its recorded version.
		var version int64
		if recordedVersion := columns.Value(entry.Name); recordedVersion != nil {
			integer, ok := integerOf(recordedVersion)
			if !ok {
				return nil, invalidArgs("%s: no readable version for %s", what, jsString(entry.Name))
			}
			version = integer
		} else {
			version = int64(*entry.Record.Version)
		}
		if version < 1 || version > int64(*entry.Record.Version) {
			return nil, invalidArgs("%s: no readable version for %s", what, jsString(entry.Name))
		}
		versions[entry.Name] = int(version)
	}
	return versions, nil
}

// scalarType is a scalar column's type; `json(...)` columns are not scalars.
func scalarType(column ColumnRecord) ColumnType {
	if column.Type == ColJson {
		return ColStr
	}
	return column.Type
}

// checkWhere is a recorded condition: well-formed nodes over recorded scalar columns (and the row's `$v`).
func checkWhere(record *TableRecord, node WhereNode, what string) error {
	refuse := func() error { return invalidArgs("%s: a malformed condition", what) }
	column := func(name string) error {
		if name == RowVersion {
			return nil
		}
		recorded, ok := record.Columns.Get(name)
		if !ok || recorded.Type == ColJson {
			return refuse()
		}
		return nil
	}
	switch {
	case node.Op == WhereAnd || node.Op == WhereOr:
		for _, each := range node.Nodes {
			if err := checkWhere(record, each, what); err != nil {
				return err
			}
		}
		return nil
	case node.Op == WhereNot:
		if len(node.Nodes) != 1 {
			return refuse()
		}
		return checkWhere(record, node.Nodes[0], what)
	case node.Op == WhereIsNull:
		return column(node.Column)
	case node.Op == WhereInArray || node.Op == WherePrefix || node.Op.isComparison():
		if err := column(node.Column); err != nil {
			return err
		}
		values := []Scalar{node.Value}
		if node.Op == WhereInArray {
			values = node.Values
		}
		for _, value := range values {
			if !isScalar(value) {
				return refuse()
			}
			if _, text := value.(string); node.Op == WherePrefix && !text {
				return refuse()
			}
		}
		return nil
	}
	return refuse()
}

// checkExpr is a recorded expression: scalars, `null`, JSON literals, recorded columns and the vocabulary's
// operations.
func checkExpr(record *TableRecord, node ExprNode, what string) error {
	refuse := func() error { return invalidArgs("%s: a malformed expression", what) }
	switch node.Op {
	case ExprNull:
		return nil
	case ExprLiteral:
		if !isScalar(node.Value) {
			return refuse()
		}
		return nil
	case ExprCol:
		if _, recorded := record.Columns.Get(node.Column); !recorded {
			return refuse()
		}
		return nil
	case ExprJSON:
		return nil
	case ExprAdd, ExprSub, ExprMax, ExprMin, ExprConcat, ExprCoalesce:
		if len(node.Args) != 2 {
			return refuse()
		}
		for _, each := range node.Args {
			if err := checkExpr(record, each, what); err != nil {
				return err
			}
		}
		return nil
	case ExprReplacePrefix:
		if len(node.Args) != 1 {
			return refuse()
		}
		return checkExpr(record, node.Args[0], what)
	case ExprWhen:
		if node.Cond == nil || len(node.Args) != 2 {
			return refuse()
		}
		if err := checkWhere(record, *node.Cond, what); err != nil {
			return err
		}
		for _, each := range node.Args {
			if err := checkExpr(record, each, what); err != nil {
				return err
			}
		}
		return nil
	}
	return refuse()
}

// isScalar is a string, a boolean or a finite number.
func isScalar(value any) bool {
	switch typed := value.(type) {
	case string, bool:
		return true
	case nil:
		return false
	default:
		number, ok := numberOf(typed)
		return ok && isFinite(number)
	}
}

// checkTable is a recorded table's SQL name (two names joined by `:`, R2), columns, key and json versions, by the
// name rules.
func checkTable(write *TableDefinitionWrite, what string) error {
	parts := strings.Split(write.Table, ":")
	if len(parts) != 2 {
		return invalidArgs("%s: table %s: not owner:table", what, jsString(write.Table))
	}
	for _, part := range parts {
		// CheckName fails with a plain error.
		if err := contracts.CheckName(part, what+": table name part"); err != nil {
			return invalidArgs("%s", err.Error())
		}
	}
	folded := map[string]bool{}
	for _, entry := range write.Columns {
		where := fmt.Sprintf("%s: column %s", what, jsString(entry.Name))
		lower := strings.ToLower(entry.Name)
		if !ColumnNamePattern.MatchString(entry.Name) || folded[lower] {
			return invalidArgs("%s: not a column name", where)
		}
		folded[lower] = true
		if err := checkColumn(entry.Record, where); err != nil {
			return err
		}
	}
	if len(write.Key) == 0 {
		return invalidArgs("%s: the key has no column", what)
	}
	return checkOrderColumns(write.Columns, write.Key, what+": the key")
}

func checkColumn(column ColumnRecord, what string) error {
	json := column.Type == ColJson
	if json != (column.Version != nil) || json != (column.Fingerprint != "") {
		return invalidArgs("%s: a json(...) column has a version and a fingerprint, no other column has", what)
	}
	if column.Default == nil {
		return nil
	}
	if json || column.Optional {
		return invalidArgs("%s: only a non-opt scalar column has a default", what)
	}
	_, err := CheckScalar(column.Type, column.Default, what+": the default")
	return err
}

// checkAlter is a recorded column edit of record's next version or its own (the edits after a data step).
func checkAlter(record *TableRecord, write *AlterWrite, what string) error {
	if write.Version != record.Version && write.Version != record.Version+1 {
		return invalidArgs("%s: an edit of version %d on version %d", what, write.Version, record.Version)
	}
	edit := write.Edit
	has := func(name string) bool {
		_, recorded := record.Columns.Get(name)
		return recorded
	}
	fresh := func(name string) bool {
		if !ColumnNamePattern.MatchString(name) {
			return false
		}
		lower := strings.ToLower(name)
		return !slices.ContainsFunc(record.Columns.Names(), func(each string) bool { return strings.ToLower(each) == lower })
	}
	indexed := func(name string) bool {
		if slices.Contains(record.Key, name) {
			return true
		}
		return slices.ContainsFunc(record.Indexes, func(index IndexRecord) bool { return slices.Contains(index.Columns, name) })
	}
	var valid bool
	switch edit.Kind {
	case EditRename:
		valid = has(edit.From) && fresh(edit.To)
	case EditAdd:
		valid = fresh(edit.Name) && (edit.Column.Optional || edit.Column.Default != nil)
	case EditDrop:
		valid = has(edit.Name) && !indexed(edit.Name)
	default:
		current, recorded := record.Columns.Get(edit.Name)
		valid = recorded && current.Type == ColJson && edit.Column.Type == ColJson
	}
	if !valid {
		return invalidArgs("%s: an %s edit the table cannot take", what, edit.Kind)
	}
	if edit.Kind == EditAdd || edit.Kind == EditJSON {
		return checkColumn(edit.Column, fmt.Sprintf("%s: column %s", what, jsString(edit.Name)))
	}
	return nil
}

// checkOrderColumns: names are distinct, recorded, non-`opt` `str` or `int` columns.
func checkOrderColumns(columns ColumnRecords, names []string, what string) error {
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if len(seen) != len(names) {
		return invalidArgs("%s: a column occurs twice", what)
	}
	for _, name := range names {
		column, recorded := columns.Get(name)
		if !recorded || column.Optional || (column.Type != ColStr && column.Type != ColInt) {
			return invalidArgs("%s: %s is not a non-opt str or int column", what, jsString(name))
		}
	}
	return nil
}

func checkIndex(record *TableRecord, write *IndexWrite, what string) error {
	_, exists := record.Indexes.Get(write.Name)
	if !IndexName.MatchString(write.Name) || exists {
		return invalidArgs("%s: index %s: not a new index name", what, jsString(write.Name))
	}
	return checkOrderColumns(record.Columns, write.Columns, fmt.Sprintf("%s: index %s", what, write.Name))
}

// recordedTable is the record of table in step; a write for a table not recorded is refused.
func recordedTable(step *Step, table, what string) (*TableRecord, error) {
	record := step.Table(table)
	if record == nil {
		return nil, invalidArgs("%s: table %s is not recorded", what, jsString(table))
	}
	return record, nil
}

// putParams is the parameters of a `put` in physical order, checked against the recorded columns: every non-`opt`
// column, no other field, values of their types, `json(...)` cells as JSON (no plugin code runs) with a version up to
// the column's, and version up to the table's.
func putParams(record *TableRecord, write *PutWrite, physical []string, what string) ([]sqlite.SqliteValue, error) {
	row, versions, version := write.Row, write.Columns, write.Version
	for _, field := range keysOf(row) {
		if _, recorded := record.Columns.Get(field); !recorded {
			return nil, invalidArgs("%s: unknown column %s", what, jsString(field))
		}
	}
	for _, field := range keysOf(versions) {
		column, recorded := record.Columns.Get(field)
		if !recorded || column.Type != ColJson || !row.Has(field) {
			return nil, invalidArgs("%s: a version for %s, which is no json(...) cell of the row", what, jsString(field))
		}
	}
	if version > record.Version {
		return nil, invalidArgs("%s: version %d is newer than the table's version %d", what, version, record.Version)
	}
	cells := make(map[string]sqlite.SqliteValue, len(record.Columns))
	for _, entry := range record.Columns {
		name, column := entry.Name, entry.Record
		where := fmt.Sprintf("%s: column %s", what, jsString(name))
		value, present := row.Get(name)
		// A row has no explicit `null` for a scalar: an absent field is `NULL`; a `json(...)` cell's `null` is the
		// JSON value.
		if !present {
			if !column.Optional {
				return nil, invalidArgs("%s is missing", where)
			}
			cells[name] = nil
			continue
		}
		if column.Type != ColJson {
			scalar, err := CheckScalar(column.Type, value, where)
			if err != nil {
				return nil, err
			}
			cells[name] = ToSqlite(scalar)
			continue
		}
		recordedVersion, readable := integerOf(versions.Value(name))
		if !readable || recordedVersion > int64(*column.Version) {
			return nil, invalidArgs("%s: no readable version", where)
		}
		text, err := jsonOf(value, where)
		if err != nil {
			return nil, err
		}
		cells[name] = text
		cells[VersionColumn(name)] = recordedVersion
	}
	params := make([]sqlite.SqliteValue, len(physical))
	for index, name := range physical {
		if name == RowVersion {
			params[index] = int64(version)
		} else {
			params[index] = cells[name]
		}
	}
	return params, nil
}
