package store

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/sql.ts
//
// SQL of declared tables (slice 6a §5, §6, §7): the DDL a table definition renders, and the statements of its reads
// and writes, generated from the compiled vocabulary with every value bound and every identifier quoted. Names were
// checked at declaration (and at import), so no `"` occurs in them and quoting is always safe.
//
// Go forced differences: Pi's WhereNode/ExprNode/SetNode unions (expression.ts) are the node types of
// expression.go, dispatched by a type switch; a SetNode is ordered (an []SetEntry). Pi renders a statement as
// `{ sql, params }`: Statement.

// Statement is a statement and its parameters.
type Statement struct {
	SQL    string
	Params []sqlite.SqliteValue
}

// Order is an order of rows: the order columns (the key appended as the tie-break) and one direction ("asc" or
// "desc").
type Order struct {
	Columns   []string
	Direction string
}

// Quote is a quoted SQL identifier.
func Quote(name string) string { return `"` + name + `"` }

func quoteAll(names []string) []string {
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = Quote(name)
	}
	return quoted
}

// TableSqlName is the SQL name of an owner's table: `<owner>:<table>` (R2), injective since neither part contains `:`.
func TableSqlName(owner, table string) string { return owner + ":" + table }

// CreateTableSql is `CREATE TABLE` for columns (as recorded) and key: `STRICT`, the key as primary key (`WITHOUT
// ROWID`, except for a single `int` key, which becomes the rowid: the fastest point lookups, and rows of any size), an
// `int` column checked within ±(2^53 - 1) (rule 10), a `bool` one within 0 and 1, a `json(...)` one as `TEXT` with
// its version column, and the row's `$v`.
func CreateTableSql(table string, columns ColumnRecords, key []string) string {
	var definitions []string
	for _, entry := range columns {
		definitions = append(definitions, ColumnSql(entry.Name, entry.Record)...)
	}
	definitions = append(definitions, Quote(RowVersion)+" INTEGER NOT NULL", "PRIMARY KEY ("+strings.Join(quoteAll(key), ", ")+")")
	rowid := false
	if len(key) == 1 {
		first, _ := columns.Get(key[0])
		rowid = first.Type == ColInt
	}
	suffix := ", WITHOUT ROWID"
	if rowid {
		suffix = ""
	}
	return "CREATE TABLE " + Quote(table) + " (" + strings.Join(definitions, ", ") + ") STRICT" + suffix
}

var safeText = strconv.FormatInt(MaxSafeInteger, 10)

// ColumnSql is the definitions of one column: itself, and a `json(...)` column's version column.
func ColumnSql(name string, column ColumnRecord) []string {
	nullability := " NOT NULL"
	if column.Optional {
		nullability = ""
	}
	defaulted := ""
	if column.Default != nil {
		defaulted = " DEFAULT " + literal(column.Default)
	}
	id := Quote(name)
	switch column.Type {
	case ColStr:
		return []string{id + " TEXT" + nullability + defaulted}
	case ColInt:
		return []string{id + " INTEGER" + nullability + defaulted + " CHECK (" + id + " BETWEEN -" + safeText + " AND " + safeText + ")"}
	case ColNum:
		return []string{id + " REAL" + nullability + defaulted}
	case ColBool:
		return []string{id + " INTEGER" + nullability + defaulted + " CHECK (" + id + " IN (0, 1))"}
	}
	return []string{id + " TEXT" + nullability, Quote(VersionColumn(name)) + " INTEGER" + nullability}
}

// literal is a default as a SQL literal: declaration data (checked), which DDL cannot bind.
func literal(value Scalar) string {
	switch typed := value.(type) {
	case string:
		return "'" + strings.ReplaceAll(typed, "'", "''") + "'"
	case bool:
		if typed {
			return "1"
		}
		return "0"
	case float64:
		return string(jsparity.AppendNumber(nil, typed))
	}
	return fmt.Sprint(value)
}

// RecordedPut is the physical columns of a recorded table in the order RowCodec.Physical uses (PhysicalColumns), and
// its whole-row `INSERT OR REPLACE` over them.
func RecordedPut(table string, columns ColumnRecords) (physical []string, sql string) {
	named := make([]NamedType, len(columns))
	for index, entry := range columns {
		named[index] = NamedType{Name: entry.Name, Type: entry.Record.Type}
	}
	physical = PhysicalColumns(named)
	return physical, putSql(table, physical)
}

// putSql is `INSERT OR REPLACE` of a whole row over physical (`put` is an upsert).
func putSql(table string, physical []string) string {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(physical)), ", ")
	return "INSERT OR REPLACE INTO " + Quote(table) + " (" + strings.Join(quoteAll(physical), ", ") + ") VALUES (" + marks + ")"
}

// CreateIndexSql is `CREATE INDEX` named `<table>:<index>` over columns, the key appended (every index ends with it).
func CreateIndexSql(table, index string, columns, key []string) string {
	all := append([]string(nil), columns...)
	for _, column := range key {
		if !containsString(columns, column) {
			all = append(all, column)
		}
	}
	return "CREATE INDEX " + Quote(table+":"+index) + " ON " + Quote(table) + " (" + strings.Join(quoteAll(all), ", ") + ")"
}

func containsString(list []string, item string) bool {
	return slices.Contains(list, item)
}

// DropIndexSql is `DROP INDEX` of `<table>:<index>`.
func DropIndexSql(table, index string) string { return "DROP INDEX " + Quote(table+":"+index) }

// TableSql is the statements of one table at one version, rendered once per table handle: the fixed ones as texts,
// the ones over compiled conditions and `set`s on demand.
type TableSql struct {
	Table string
	// selected are the columns a read selects: the declared ones and each `json(...)` column's version.
	selected string
	byKey    string
	// Put is `INSERT OR REPLACE` of a whole row, in RowCodec.Physical's order (`put` is an upsert).
	Put    string
	Get    string
	Delete string
}

// NewTableSql renders the statements of table over columns, physical (RowCodec.Physical) and key.
func NewTableSql(table string, columns []NamedType, physical, key []string) *TableSql {
	id := Quote(table)
	var selected []string
	for _, column := range columns {
		selected = append(selected, Quote(column.Name))
	}
	for _, column := range columns {
		if column.Type == ColJson {
			selected = append(selected, Quote(VersionColumn(column.Name)))
		}
	}
	conditions := make([]string, len(key))
	for index, column := range key {
		conditions[index] = Quote(column) + " = ?"
	}
	sql := &TableSql{Table: table, selected: strings.Join(selected, ", "), byKey: strings.Join(conditions, " AND ")}
	sql.Put = putSql(table, physical)
	sql.Get = "SELECT " + sql.selected + " FROM " + id + " WHERE " + sql.byKey
	sql.Delete = "DELETE FROM " + id + " WHERE " + sql.byKey
	return sql
}

// GetColumns is `SELECT` of columns of one row by key: a point lookup, without the `ORDER BY` and `LIMIT` of a page.
func (sql *TableSql) GetColumns(columns []string) string {
	return "SELECT " + strings.Join(quoteAll(columns), ", ") + " FROM " + Quote(sql.Table) + " WHERE " + sql.byKey
}

// Update is `UPDATE` of one row by key; versions gives each `json(...)` column's version to stamp.
func (sql *TableSql) Update(set SetNode, key []sqlite.SqliteValue, versions map[string]int) Statement {
	var params []sqlite.SqliteValue
	assignments := setSql(set, versions, &params)
	params = append(params, key...)
	return Statement{SQL: "UPDATE " + Quote(sql.Table) + " SET " + assignments + " WHERE " + sql.byKey, Params: params}
}

// UpdateWhere is `UPDATE` of every row matching where.
func (sql *TableSql) UpdateWhere(where WhereNode, set SetNode, versions map[string]int) Statement {
	var params []sqlite.SqliteValue
	assignments := setSql(set, versions, &params)
	condition := WhereSql(where, &params)
	return Statement{SQL: "UPDATE " + Quote(sql.Table) + " SET " + assignments + " WHERE " + condition, Params: params}
}

// DeleteWhere is `DELETE` of every row matching where.
func (sql *TableSql) DeleteWhere(where WhereNode) Statement {
	var params []sqlite.SqliteValue
	return Statement{SQL: "DELETE FROM " + Quote(sql.Table) + " WHERE " + WhereSql(where, &params), Params: params}
}

// Select is a page: rows matching where (nil: all) in order, after the order values after (nil: from the start;
// row-value comparison; the key in the order makes it total), at most limit (bound); every column, or only columns
// (nil: every column).
func (sql *TableSql) Select(where *WhereNode, order Order, after []sqlite.SqliteValue, limit int64, columns []string) Statement {
	var params []sqlite.SqliteValue
	var conditions []string
	if where != nil {
		conditions = append(conditions, WhereSql(*where, &params))
	}
	if after != nil {
		orderColumns := strings.Join(quoteAll(order.Columns), ", ")
		operator := "<"
		if order.Direction == "asc" {
			operator = ">"
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(after)), ", ")
		conditions = append(conditions, "("+orderColumns+") "+operator+" ("+marks+")")
		params = append(params, after...)
	}
	filter := ""
	if len(conditions) > 0 {
		filter = " WHERE " + strings.Join(conditions, " AND ")
	}
	direction := "DESC"
	if order.Direction == "asc" {
		direction = "ASC"
	}
	sorting := make([]string, len(order.Columns))
	for index, column := range order.Columns {
		sorting[index] = Quote(column) + " " + direction
	}
	params = append(params, limit)
	selected := sql.selected
	if columns != nil {
		selected = strings.Join(quoteAll(columns), ", ")
	}
	return Statement{SQL: "SELECT " + selected + " FROM " + Quote(sql.Table) + filter + " ORDER BY " + strings.Join(sorting, ", ") + " LIMIT ?", Params: params}
}

// Count is `count(*)` of the rows matching where (nil: all).
func (sql *TableSql) Count(where *WhereNode) Statement {
	var params []sqlite.SqliteValue
	filter := ""
	if where != nil {
		filter = " WHERE " + WhereSql(*where, &params)
	}
	return Statement{SQL: "SELECT count(*) AS count FROM " + Quote(sql.Table) + filter, Params: params}
}

// setSql is the assignments of set; a written `json(...)` column also stamps its version (a clear stamps `NULL`).
func setSql(set SetNode, versions map[string]int, params *[]sqlite.SqliteValue) string {
	assignments := make([]string, 0, len(set))
	for _, entry := range set {
		assignments = append(assignments, Quote(entry.Column)+" = "+exprSql(entry.Node, params))
		if version, ok := versions[entry.Column]; ok {
			assignments = append(assignments, Quote(VersionColumn(entry.Column))+" = ?")
			if entry.Node.Op == ExprNull {
				*params = append(*params, nil)
			} else {
				*params = append(*params, int64(version))
			}
		}
	}
	return strings.Join(assignments, ", ")
}

func exprSql(node ExprNode, params *[]sqlite.SqliteValue) string {
	switch node.Op {
	case ExprNull:
		return "NULL"
	case ExprLiteral:
		*params = append(*params, ToSqlite(node.Value))
		return "?"
	case ExprCol:
		return Quote(node.Column)
	case ExprJSON:
		// Serialized once at staging already (rule 8): this cannot fail.
		text, _ := jsstring.MarshalJSON(node.Json)
		*params = append(*params, string(text))
		return "?"
	case ExprWhen:
		condition := WhereSql(*node.Cond, params)
		a := exprSql(node.Args[0], params)
		b := exprSql(node.Args[1], params)
		return "(CASE WHEN " + condition + " THEN " + a + " ELSE " + b + " END)"
	case ExprReplacePrefix:
		length := int64(utf8.RuneCountInString(node.From))
		// SQLite's `substr` counts characters of UTF-8 text: code points, as the prefix's length does.
		// Parameters in the order their `?` occur in the text.
		value := exprSql(node.Args[0], params)
		*params = append(*params, length, node.From, node.To)
		rest := exprSql(node.Args[0], params)
		*params = append(*params, length+1)
		unchanged := exprSql(node.Args[0], params)
		return "(CASE WHEN substr(" + value + ", 1, ?) = ? THEN ? || substr(" + rest + ", ?) ELSE " + unchanged + " END)"
	case ExprCoalesce:
		return "coalesce(" + exprSql(node.Args[0], params) + ", " + exprSql(node.Args[1], params) + ")"
	case ExprAdd:
		return "(" + exprSql(node.Args[0], params) + " + " + exprSql(node.Args[1], params) + ")"
	case ExprSub:
		return "(" + exprSql(node.Args[0], params) + " - " + exprSql(node.Args[1], params) + ")"
	case ExprMax:
		return "max(" + exprSql(node.Args[0], params) + ", " + exprSql(node.Args[1], params) + ")"
	case ExprMin:
		return "min(" + exprSql(node.Args[0], params) + ", " + exprSql(node.Args[1], params) + ")"
	case ExprConcat:
		return "(" + exprSql(node.Args[0], params) + " || " + exprSql(node.Args[1], params) + ")"
	}
	panic(fmt.Sprintf("store: unknown expression operator %q", node.Op))
}

var comparisons = map[WhereOp]string{WhereEq: "=", WhereNe: "<>", WhereLt: "<", WhereLte: "<=", WhereGt: ">", WhereGte: ">="}

// WhereSql is a condition with its operands bound; SQL's `NULL` rules are the vocabulary's.
func WhereSql(node WhereNode, params *[]sqlite.SqliteValue) string {
	switch node.Op {
	case WhereAnd:
		if len(node.Nodes) == 0 {
			return "1"
		}
		return "(" + joinWhere(node.Nodes, " AND ", params) + ")"
	case WhereOr:
		if len(node.Nodes) == 0 {
			return "0"
		}
		return "(" + joinWhere(node.Nodes, " OR ", params) + ")"
	case WhereNot:
		return "(NOT " + WhereSql(node.Nodes[0], params) + ")"
	case WhereIsNull:
		return Quote(node.Column) + " IS NULL"
	case WhereInArray:
		values := make([]any, len(node.Values))
		for index, value := range node.Values {
			values[index] = ToSqlite(value)
		}
		text, _ := jsstring.MarshalJSON(values)
		*params = append(*params, string(text))
		return Quote(node.Column) + " IN (SELECT value FROM json_each(?))"
	case WherePrefix:
		prefix, _ := node.Value.(string)
		upper, bounded := PrefixUpperBound(prefix)
		*params = append(*params, prefix)
		if !bounded {
			return Quote(node.Column) + " >= ?"
		}
		*params = append(*params, upper)
		return "(" + Quote(node.Column) + " >= ? AND " + Quote(node.Column) + " < ?)"
	}
	operator, ok := comparisons[node.Op]
	if !ok {
		panic(fmt.Sprintf("store: unknown condition operator %q", node.Op))
	}
	*params = append(*params, ToSqlite(node.Value))
	return Quote(node.Column) + " " + operator + " ?"
}

func joinWhere(nodes []WhereNode, separator string, params *[]sqlite.SqliteValue) string {
	parts := make([]string, len(nodes))
	for index, node := range nodes {
		parts[index] = WhereSql(node, params)
	}
	return strings.Join(parts, separator)
}
