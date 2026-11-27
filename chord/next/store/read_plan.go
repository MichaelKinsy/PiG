package store

import (
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/store/read-plan.ts (slice 14 §3.2, §3.3, §5): a query compiled for the tables it reads
// right now. The plan resolves each column set to the table bound to it, checks every column and match the query
// uses against that table (a trust boundary: conditions arrive as data, and a publisher's reload changes its table),
// lists the tables the read depends on with the columns that matter, and renders its statements: the rows of a range,
// a count, and the probe.
//
// Go forms: Pi throws from the constructor, `NewReadPlan` returns the error. Pi's `undefined` projection lists are
// nil slices (a non-nil empty slice is "none"); Pi's `Set`s of column names are `map[string]bool`; a position or a
// bound that is absent is a nil Position.

// Bound is the table a column set is bound to: its SQL name and the definition its owner declared.
type Bound struct {
	Table      string
	Definition *TableDefinition
}

// Bindings are the store's bindings of column sets, by set name.
type Bindings interface {
	Get(set string) (Bound, bool)
}

// Projection is what a read hands its mapping: columns of the own table, and of the joined set. A nil list is every
// column of its side.
type Projection struct {
	Reads        []string
	PartnerReads []string
}

// UseRole is how a read uses a table.
type UseRole string

// The roles of a Use.
const (
	UseOwn    UseRole = "own"
	UseJoined UseRole = "joined"
	UseExists UseRole = "exists"
)

// Use is one use of a table by a read (a table may be used more than once): its role, the columns of it the read
// depends on, and the own columns a written row of it pairs with (what the probe is limited by).
type Use struct {
	Role UseRole
	// Table is the table's SQL name.
	Table string
	// Depends are the columns of the table a change of which may change the read's result.
	Depends map[string]bool
	// Enters are the columns a change of which may bring a row into the result that was not in it (not the mapped ones).
	Enters map[string]bool
	// PairedBy are the table's columns a written row is identified by: the key, or an `exists`' matched columns.
	PairedBy []string
	// Pairs are the own columns equal to PairedBy (nil: none, the pairing goes through another side).
	Pairs []string
	// Keyed is whether an equality on Pairs is keyed in the own table: only then the probe is used.
	Keyed bool
	// Stable is whether PairedBy are key columns of the table: a row then pairs with the same own rows before and after.
	Stable bool
}

// Position is a position in a read's order: the values of its first order columns (all of them, or a leading part).
type Position []sqlite.SqliteValue

// Range is the rows a statement asks for: after After, through Through (inclusive), at most Limit. A nil bound is none.
type Range struct {
	After   Position
	Through Position
	Limit   int
}

// StoredRead is a stored row as the backend returns it: own columns by name, the partner's as `<set>:<column>`.
type StoredRead = sqlite.SqliteRow

// ReadShape is what a plan is read as.
type ReadShape string

// The shapes of a ReadPlan.
const (
	ReadRows  ReadShape = "rows"
	ReadCount ReadShape = "count"
)

const (
	planOwn     = "t"
	planPartner = "p"
)

var unbounded = Range{}

// EqualityTerm is one own column fixed by an `eq` or `inArray` term in the condition's top-level `and`.
type EqualityTerm struct {
	Column string
	Values []sqlite.SqliteValue
}

// ReadPlan is a query compiled against the store's bindings. Its statements select the columns the mapping gets, the
// own key, the matched own columns and the order columns, so a run can keep the keys the skip test needs.
type ReadPlan struct {
	Query *Query
	// Table is the own table's SQL name.
	Table string
	Order Order
	Uses  []Use
	// Equalities are the own columns fixed in the condition's top-level `and` terms, in order, as SQLite binds them.
	Equalities []EqualityTerm
	bindings   Bindings
	sides      sides
	selected   string
	from       string
	// where is the condition rendered once, with its parameters; no text without a condition.
	where struct {
		sql    string
		has    bool
		params []sqlite.SqliteValue
	}
	// texts are its statements' texts by shape (`text`); a plan is shared by the reads of a store.
	mu    sync.Mutex
	texts map[string]string
}

// NewReadPlan is query compiled for table (the own table's SQL name). shape is what the plan is read as: a count
// depends on its condition and match columns only (slice 14 §3.3).
func NewReadPlan(query *Query, table string, projection Projection, shape ReadShape, bindings Bindings) (*ReadPlan, error) {
	plan := &ReadPlan{Query: query, Table: table, Order: query.Order(), bindings: bindings, texts: map[string]string{}}
	joined := query.Joined
	plan.sides = sidesOf(joined)
	var partner Bound
	if joined != nil {
		var err error
		if partner, err = boundPartner(joined, bindings); err != nil {
			return nil, err
		}
	}
	if err := checkExpected(query.Expected, bindings); err != nil {
		return nil, err
	}
	// A count reads no rows: nothing is mapped and nothing ordered.
	counted := shape == ReadCount
	var reads, partnerReads, orderColumns []string
	if !counted {
		reads = projection.Reads
		if reads == nil {
			reads = namesOf(query.Table.Columns)
		}
		partnerReads = projection.PartnerReads
		if partnerReads == nil && joined != nil {
			partnerReads = namesOf(joined.Set.Columns)
		}
		orderColumns = plan.Order.Columns
	}
	var used []string
	if query.Condition != nil {
		used = ColumnsOf(*query.Condition)
	}
	used = append(used, orderColumns...)
	fixed := map[string]bool{}
	var tested []Use
	if query.Condition != nil {
		plan.Equalities = equalitiesOf(*query.Condition, plan.sides)
		for _, term := range plan.Equalities {
			fixed[term.Column] = true
		}
		what := query.Table.Name + ": exists"
		var err error
		tested, err = existsUses(*query.Condition, query.Column, bindings, what)
		if err != nil {
			return nil, err
		}
	}
	var ownUsed []string
	for _, name := range used {
		if !plan.sides.isPartner(name) {
			ownUsed = append(ownUsed, name)
		}
	}
	plan.Uses = []Use{ownUse(query, table, ownUsed, reads)}
	if joined != nil {
		use, err := joinedUse(query, partner, used, partnerReads, fixed, plan.sides)
		if err != nil {
			return nil, err
		}
		plan.Uses = append(plan.Uses, use)
	}
	for _, use := range tested {
		plan.Uses = append(plan.Uses, pairedWithOwn(use, query, fixed, plan.sides))
	}
	plan.selected = selectList(query, plan.Uses, reads, partnerReads, orderColumns, plan.sides)
	plan.from = fromClause(table, joined, partner)
	if query.Condition != nil {
		plan.where.has = true
		plan.where.sql = WhereSql(*query.Condition, &plan.where.params, plan.render())
	}
	return plan, nil
}

// Select is the rows of rng in the plan's order.
func (plan *ReadPlan) Select(rng Range) Statement {
	sql := plan.text("select", rng, nil, func(filter string) string {
		direction := "DESC"
		if plan.Order.Direction == "asc" {
			direction = "ASC"
		}
		sorting := make([]string, len(plan.Order.Columns))
		for i, name := range plan.Order.Columns {
			sorting[i] = plan.sides.sql(name) + " " + direction
		}
		return "SELECT " + plan.selected + " FROM " + plan.from + filter + " ORDER BY " + strings.Join(sorting, ", ") + " LIMIT ?"
	})
	return Statement{SQL: sql, Params: append(plan.bound(rng), int64(rng.Limit))}
}

// Count is `count(*)` of the rows matching the condition.
func (plan *ReadPlan) Count() Statement {
	sql := plan.text("count", unbounded, nil, func(filter string) string {
		return "SELECT count(*) AS count FROM " + plan.from + filter
	})
	return Statement{SQL: sql, Params: plan.bound(unbounded)}
}

// Probe is the probe (slice 14 §3.3): whether a row of rng has values in the own columns columns. One row or none.
func (plan *ReadPlan) Probe(rng Range, columns []string, values []sqlite.SqliteValue) Statement {
	sql := plan.text("probe", rng, columns, func(filter string) string {
		limited := make([]string, len(columns))
		for i, name := range columns {
			limited[i] = planOwn + "." + Quote(name) + " = ?"
		}
		joiner := " AND "
		if filter == "" {
			joiner = " WHERE "
		}
		return "SELECT 1 AS found FROM " + plan.from + filter + joiner + strings.Join(limited, " AND ") + " LIMIT 1"
	})
	return Statement{SQL: sql, Params: append(plan.bound(rng), values...)}
}

// text is the text of one of its statements, rendered once per shape (which bounds the range has and how long, which
// columns a probe is limited by): a probe runs per written key and read, so only its parameters are built per call.
func (plan *ReadPlan) text(kind string, rng Range, columns []string, render func(filter string) string) string {
	key := kind + "|" + lengthOf(rng.After) + "|" + lengthOf(rng.Through) + "|" + strings.Join(columns, ",")
	plan.mu.Lock()
	defer plan.mu.Unlock()
	text, found := plan.texts[key]
	if !found {
		text = render(plan.filter(rng))
		plan.texts[key] = text
	}
	return text
}

func lengthOf(position Position) string {
	if position == nil {
		return "-"
	}
	return strconv.Itoa(len(position))
}

// bound is the parameters of a statement's filter, in the order `filter` renders them: the condition's, then the
// bounds'.
func (plan *ReadPlan) bound(rng Range) []sqlite.SqliteValue {
	params := slices.Clone(plan.where.params)
	params = append(params, rng.After...)
	return append(params, rng.Through...)
}

// filter is ` WHERE …` of the condition and the range's bounds (row-value comparisons over the leading order columns).
func (plan *ReadPlan) filter(rng Range) string {
	var conditions []string
	if plan.where.has {
		conditions = append(conditions, plan.where.sql)
	}
	asc := plan.Order.Direction == "asc"
	bound := func(position Position, operator string) {
		columns := make([]string, len(position))
		marks := make([]string, len(position))
		for i := range position {
			columns[i] = plan.sides.sql(plan.Order.Columns[i])
			marks[i] = "?"
		}
		conditions = append(conditions, "("+strings.Join(columns, ", ")+") "+operator+" ("+strings.Join(marks, ", ")+")")
	}
	if rng.After != nil {
		bound(rng.After, map[bool]string{true: ">", false: "<"}[asc])
	}
	if rng.Through != nil {
		bound(rng.Through, map[bool]string{true: "<=", false: ">="}[asc])
	}
	if len(conditions) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conditions, " AND ")
}

// render is how the condition's columns and `exists` render: own and partner columns by alias, an `exists` as a
// subquery.
func (plan *ReadPlan) render() ConditionSql {
	column := plan.sides.sql
	return ConditionSql{
		Column: column,
		Exists: func(node ExistsNode, params *[]sqlite.SqliteValue) string {
			return plan.existsSql(node, params, column, 0)
		},
	}
}

func (plan *ReadPlan) existsSql(node ExistsNode, params *[]sqlite.SqliteValue, outer func(string) string, depth int) string {
	alias := "e" + strconv.Itoa(depth)
	column := func(name string) string { return alias + "." + Quote(name) }
	matched := make([]string, 0, len(node.Pairs)+1)
	for _, pair := range node.Pairs {
		matched = append(matched, column(pair.SetColumn)+" = "+outer(pair.Column))
	}
	render := ConditionSql{
		Column: column,
		Exists: func(nested ExistsNode, into *[]sqlite.SqliteValue) string {
			return plan.existsSql(nested, into, column, depth+1)
		},
	}
	if node.Inner != nil {
		matched = append(matched, WhereSql(*node.Inner, params, render))
	}
	// served cannot fail here: NewReadPlan resolved every set of the condition.
	bound, _ := plan.bindings.Get(node.Set)
	return "EXISTS (SELECT 1 FROM " + Quote(bound.Table) + " AS " + alias + " WHERE " + strings.Join(matched, " AND ") + ")"
}

func namesOf(columns []Column) []string {
	names := make([]string, len(columns))
	for i, column := range columns {
		names[i] = column.Name
	}
	return names
}

// sides are the two sides of a query's column names: own columns as they are, a joined set's as `<set>:<column>`.
type sides struct {
	prefix string
	joined bool
}

func sidesOf(joined *Joined) sides {
	if joined == nil {
		return sides{}
	}
	return sides{prefix: PartnerName(joined.Set.Name, ""), joined: true}
}

func (s sides) isPartner(name string) bool { return s.joined && strings.HasPrefix(name, s.prefix) }

// strip is a partner column's name within its set.
func (s sides) strip(name string) string { return name[len(s.prefix):] }

// sql is the column in the plan's SQL, by its side's alias.
func (s sides) sql(name string) string {
	if s.isPartner(name) {
		return planPartner + "." + Quote(s.strip(name))
	}
	return planOwn + "." + Quote(name)
}

// boundPartner is the table bound to the joined set, checked: the read matches the set's whole key as the bound table
// has it now (a key that gained a column would pair one row with several), or it is not what the read was written for.
func boundPartner(joined *Joined, bindings Bindings) (Bound, error) {
	name := joined.Set.Name
	bound, err := served(bindings, name)
	if err != nil {
		return Bound{}, err
	}
	set, err := exposed(bound, name)
	if err != nil {
		return Bound{}, err
	}
	matched := make([]string, len(joined.Pairs))
	for i, pair := range joined.Pairs {
		matched[i] = pair.SetColumn
	}
	if len(set.Key) != len(matched) || !isEvery(set.Key, func(column string) bool { return slices.Contains(matched, column) }) {
		return Bound{}, invalidArgs("column set %q is joined by (%s), its key is (%s): not what this read was written for",
			name, strings.Join(matched, ", "), strings.Join(set.Key, ", "))
	}
	return bound, nil
}

func isEvery(names []string, test func(string) bool) bool {
	for _, name := range names {
		if !test(name) {
			return false
		}
	}
	return true
}

// checkExpected checks the set columns a query's `exists` were written for against the tables bound to their sets.
func checkExpected(expected Expected, bindings Bindings) error {
	for _, each := range expected {
		bound, err := served(bindings, each.Set)
		if err != nil {
			return err
		}
		set, err := exposed(bound, each.Set)
		if err != nil {
			return err
		}
		for _, column := range each.Columns {
			found, has := set.ByName[column.Name]
			if err := checkSameColumn(&column, optional(found, has), each.Set, column.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func optional(column Column, found bool) *Column {
	if !found {
		return nil
	}
	return &column
}

func nameSet(names ...[]string) map[string]bool {
	set := map[string]bool{}
	for _, list := range names {
		for _, name := range list {
			set[name] = true
		}
	}
	return set
}

// ownUse is the use of the query's own table: used are the own columns of its condition and order.
func ownUse(query *Query, table string, used, reads []string) Use {
	var matched []string
	if query.Joined != nil {
		for _, pair := range query.Joined.Pairs {
			matched = append(matched, pair.Column)
		}
	}
	enters := nameSet(used, matched)
	return Use{
		Role: UseOwn, Table: table, Depends: nameSet(keys(enters), reads), Enters: enters,
		PairedBy: query.Table.Key, Pairs: query.Table.Key, Keyed: true, Stable: true,
	}
}

func keys(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	return names
}

// joinedUse is the use of the joined set's table, checked: every partner column the read uses is, in the bound table,
// what the read's set declares.
func joinedUse(query *Query, partner Bound, used, partnerReads []string, fixed map[string]bool, s sides) (Use, error) {
	set, pairs := query.Joined.Set, query.Joined.Pairs
	enters := map[string]bool{}
	for _, name := range used {
		if s.isPartner(name) {
			enters[s.strip(name)] = true
		}
	}
	depends := nameSet(keys(enters), partnerReads, set.Key)
	bound, err := exposed(partner, set.Name)
	if err != nil {
		return Use{}, err
	}
	// Pi's Set iterates in insertion order, which decides the first error: the entered columns, the reads, the key.
	for _, name := range orderedUnion(orderedKeys(used, s), partnerReads, set.Key) {
		expected, hasExpected := set.ByName[name]
		found, hasFound := bound.ByName[name]
		if err := checkSameColumn(optional(expected, hasExpected), optional(found, hasFound), set.Name, name); err != nil {
			return Use{}, err
		}
	}
	matched := make([]string, len(pairs))
	for i, pair := range pairs {
		matched[i] = pair.Column
	}
	return Use{
		Role: UseJoined, Table: partner.Table, Depends: depends, Enters: enters,
		PairedBy: set.Key, Pairs: matched, Keyed: keyedBy(query.Table, fixed, matched), Stable: true,
	}, nil
}

// orderedKeys are the partner columns among used, stripped, in order.
func orderedKeys(used []string, s sides) []string {
	var names []string
	for _, name := range used {
		if s.isPartner(name) {
			names = append(names, s.strip(name))
		}
	}
	return names
}

// orderedUnion is the names of the lists once each, in the order they first occur (a Set's iteration).
func orderedUnion(lists ...[]string) []string {
	var names []string
	for _, list := range lists {
		for _, name := range list {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// existsUses are the uses of the `exists` in condition (a condition of one side, whose columns columnOf knows), each
// checked against the table bound to its set: the match rule, the matched columns' types, its inner condition's
// columns. An outermost one's Pairs are the enclosing columns it is matched to; one nested in another pairs with no
// own row.
func existsUses(condition WhereNode, columnOf func(string) (Column, bool), bindings Bindings, what string) ([]Use, error) {
	var uses []Use
	for _, node := range ExistsIn(condition) {
		bound, err := served(bindings, node.Set)
		if err != nil {
			return nil, err
		}
		set, err := exposed(bound, node.Set)
		if err != nil {
			return nil, err
		}
		pairedBy := make([]string, len(node.Pairs))
		for i, pair := range node.Pairs {
			pairedBy[i] = pair.SetColumn
		}
		label := what + "(" + node.Set + ")"
		if err := CheckMatch(set.Matched(), pairedBy, label); err != nil {
			return nil, err
		}
		for _, pair := range node.Pairs {
			column, hasColumn := set.ByName[pair.SetColumn]
			outer, hasOuter := columnOf(pair.Column)
			if !hasColumn || !hasOuter {
				missing := pair.Column
				if !hasColumn {
					missing = pair.SetColumn
				}
				return nil, invalidArgs("%s: no column %q", label, missing)
			}
			if err := CheckPair(node.Set, column, outer, label); err != nil {
				return nil, err
			}
		}
		if node.Inner != nil {
			side := set.Side()
			if err := CheckNode(nodeData(*node.Inner), &side, label); err != nil {
				return nil, err
			}
		}
		enclosing := make([]string, len(node.Pairs))
		for i, pair := range node.Pairs {
			enclosing[i] = pair.Column
		}
		innerColumns := []string(nil)
		if node.Inner != nil {
			innerColumns = ColumnsOf(*node.Inner)
		}
		depends := nameSet(pairedBy, innerColumns)
		uses = append(uses, Use{
			Role: UseExists, Table: bound.Table, Depends: depends, Enters: depends, PairedBy: pairedBy, Pairs: enclosing,
			Stable: isEvery(pairedBy, func(name string) bool { return slices.Contains(bound.Definition.Key, name) }),
		})
		// One nested in this one is matched to this set's columns: it pairs with no own row.
		if node.Inner != nil {
			within, err := existsUses(*node.Inner, func(name string) (Column, bool) { column, found := set.ByName[name]; return column, found }, bindings, what)
			if err != nil {
				return nil, err
			}
			for _, nested := range within {
				nested.Pairs = nil
				uses = append(uses, nested)
			}
		}
	}
	return uses, nil
}

// pairedWithOwn is an `exists`' use with the own columns it pairs by: its enclosing columns when they are own ones;
// the join's own columns when it is matched on the partner's key; none otherwise (the read runs then).
func pairedWithOwn(use Use, query *Query, fixed map[string]bool, s sides) Use {
	if use.Pairs == nil {
		use.Keyed = false
		return use
	}
	pairs := make([]string, len(use.Pairs))
	for i, name := range use.Pairs {
		if !s.isPartner(name) {
			pairs[i] = name
			continue
		}
		index := slices.IndexFunc(query.Joined.Pairs, func(pair MatchPair) bool { return pair.SetColumn == s.strip(name) })
		if index < 0 {
			use.Pairs, use.Keyed = nil, false
			return use
		}
		pairs[i] = query.Joined.Pairs[index].Column
	}
	use.Pairs = pairs
	use.Keyed = keyedBy(query.Table, fixed, pairs)
	return use
}

// selectList is the select list: what the mapping gets (reads, partnerReads, each `json(...)` column with its
// version), and what the store keeps of a row: the own key, the own columns paired with other tables, the order
// columns.
func selectList(query *Query, uses []Use, reads, partnerReads, order []string, s sides) string {
	own := query.Table
	var ownOrder, partnerOrder []string
	for _, name := range order {
		if s.isPartner(name) {
			partnerOrder = append(partnerOrder, s.strip(name))
		} else {
			ownOrder = append(ownOrder, name)
		}
	}
	chosen := orderedUnion(reads, own.Key, ownOrder)
	for _, use := range uses {
		chosen = orderedUnion(chosen, use.Pairs)
	}
	var versions []string
	for _, name := range reads {
		if column, found := own.ByName[name]; found && column.Type == ColJson {
			versions = append(versions, VersionColumn(name))
		}
	}
	partner := orderedUnion(partnerReads, partnerOrder)
	set := ""
	if query.Joined != nil {
		set = query.Joined.Set.Name
	}
	parts := make([]string, 0, len(chosen)+len(versions)+len(partner))
	for _, name := range append(chosen, versions...) {
		parts = append(parts, planOwn+"."+Quote(name)+" AS "+Quote(name))
	}
	for _, name := range partner {
		parts = append(parts, planPartner+"."+Quote(name)+" AS "+Quote(PartnerName(set, name)))
	}
	return strings.Join(parts, ", ")
}

// fromClause is the tables a statement reads from. `CROSS JOIN` keeps SQLite from reordering: a read is driven from
// its own table.
func fromClause(table string, joined *Joined, partner Bound) string {
	if joined == nil {
		return Quote(table) + " AS " + planOwn
	}
	on := make([]string, len(joined.Pairs))
	for i, pair := range joined.Pairs {
		on[i] = planPartner + "." + Quote(pair.SetColumn) + " = " + planOwn + "." + Quote(pair.Column)
	}
	return Quote(table) + " AS " + planOwn + " CROSS JOIN " + Quote(partner.Table) + " AS " + planPartner + " ON " + strings.Join(on, " AND ")
}

// served is the binding of set; a read over a set nobody serves fails `unavailable` and runs again when one appears.
func served(bindings Bindings, set string) (Bound, error) {
	if bound, found := bindings.Get(set); found {
		return bound, nil
	}
	return Bound{}, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "column set \"" + set + "\" is not served"}, nil)
}

// exposed is the column set bound's table exposes: the one named set (a binding is made from `exposes`).
func exposed(bound Bound, set string) (*ColumnSet, error) {
	if found := bound.Definition.Exposes; found != nil && found.Name == set {
		return found, nil
	}
	return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "column set \"" + set + "\" is not served"}, nil)
}

// checkSameColumn refuses a column of a set the read was written for that the bound table has not, or has differently.
func checkSameColumn(expected, found *Column, set, name string) error {
	if expected != nil && found != nil && SameColumn(*expected, *found) {
		return nil
	}
	return invalidArgs("column %q of column set %q is not what this read was written for", name, set)
}

// equalitiesOf is the own `eq` and `inArray` terms of condition's top-level `and`, in their order (the first term of a
// column counts), with values converted as SQLite binds them.
func equalitiesOf(node WhereNode, s sides) []EqualityTerm {
	seen := map[string]bool{}
	var out []EqualityTerm
	var collect func(n WhereNode)
	collect = func(n WhereNode) {
		switch n.Op {
		case WhereAnd:
			for _, each := range n.Nodes {
				collect(each)
			}
		case WhereEq:
			if s.isPartner(n.Column) || seen[n.Column] {
				return
			}
			seen[n.Column] = true
			out = append(out, EqualityTerm{Column: n.Column, Values: []sqlite.SqliteValue{ToSqlite(n.Value)}})
		case WhereInArray:
			if s.isPartner(n.Column) || seen[n.Column] {
				return
			}
			seen[n.Column] = true
			vals := make([]sqlite.SqliteValue, len(n.Values))
			for i, v := range n.Values {
				vals[i] = ToSqlite(v)
			}
			out = append(out, EqualityTerm{Column: n.Column, Values: vals})
		}
	}
	collect(node)
	return out
}

// keyedBy is whether an equality on columns is keyed in table: together with the fixed columns before them they are
// a leading prefix of its key or of one of its indexes.
func keyedBy(table *TableDefinition, fixed map[string]bool, columns []string) bool {
	paths := [][]string{table.Key}
	for _, index := range table.Indexes {
		paths = append(paths, IndexPath(index.Columns, table.Key))
	}
	for _, path := range paths {
		covered := 0
		for _, name := range path {
			if slices.Contains(columns, name) {
				covered++
			} else if !fixed[name] {
				break
			}
			if covered == len(columns) {
				return true
			}
		}
	}
	return false
}
