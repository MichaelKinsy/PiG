package store

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/store/column-set.ts (slice 14 §5): the columns of a table that other plugins' conditions
// and joins may read, declared once.
//
// Go forced differences: a set's columns are an ordered slice of ColumnProp (Schemas, what a table spreads into its
// own) with the described Columns and ByName, and its indexes IndexRecords, as a TableDefinition's are (Go maps have no
// order); the type-only `~key` and the mapped `refs` type have no Go form; Pi's structural `ExposingTable` and the
// `{ name, exposes }` of `checkExposedOnce` are the structs ExposingTable and Exposer.

// ColumnSetOptions is what DefineColumns takes.
type ColumnSetOptions struct {
	// Columns are the public columns: scalar ones (`str int num bool`, `opt(...)` of them, `host(...)`).
	Columns []ColumnProp
	// Key is the key of the table that exposes the set.
	Key []string
	// Indexes are the indexes others may match by, each a leading prefix of one of the table's.
	Indexes IndexRecords
}

// ColumnSet is a declared column set: its columns, key and indexes, checked once in DefineColumns. Refs are its column
// references, for conditions over the set wherever it appears (a query's own table, a joined set, an `exists`).
type ColumnSet struct {
	Name string
	// Schemas are the column schemas as declared: what a table spreads into its own columns.
	Schemas []ColumnProp
	Columns []Column
	ByName  map[string]Column
	Key     []string
	Indexes IndexRecords
	Refs    ColumnRefs
}

// DefineColumns declares the columns of a table that other plugins' conditions and joins may read.
func DefineColumns(name string, options ColumnSetOptions) (*ColumnSet, error) {
	if err := contracts.CheckName(name, "column set"); err != nil {
		return nil, err
	}
	described, err := DescribeColumns(options.Columns, name)
	if err != nil {
		return nil, err
	}
	for _, column := range described {
		// Declaration policy: a `json(...)` cell has no scalar value a condition could compare.
		if column.Type == ColJson {
			return nil, fmt.Errorf("%s: column %s: json(...) is not in a column set", name, quote(column.Name))
		}
	}
	byName := make(map[string]Column, len(described))
	for _, column := range described {
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
		checked, err := OrderColumns(byName, index.Columns, fmt.Sprintf("%s: index %s", name, quote(index.Name)))
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, IndexRecord{Name: index.Name, Columns: checked})
	}
	// One reference per column, tagged with the set: a condition names the side it means by it.
	refs := make(ColumnRefs, len(described))
	for _, column := range described {
		refs[column.Name] = NewSetColumnRef(column.Name, name)
	}
	return &ColumnSet{
		Name:    name,
		Schemas: append([]ColumnProp(nil), options.Columns...),
		Columns: described,
		ByName:  byName,
		Key:     key,
		Indexes: indexes,
		Refs:    refs,
	}, nil
}

// Side is the set as a condition is checked or compiled against it: its name and columns.
func (set *ColumnSet) Side() Side { return Side{Name: set.Name, ByName: set.ByName} }

// Matched is the set as a match is checked against it: its name, columns, key and declared indexes.
func (set *ColumnSet) Matched() MatchedSet {
	return MatchedSet{Side: set.Side(), Key: set.Key, Indexes: set.Indexes}
}

// ExposingTable is the parts of a table definition CheckExposes reads.
type ExposingTable struct {
	Name    string
	ByName  map[string]Column
	Key     []string
	Indexes IndexRecords
}

// CheckExposes checks that table can expose set (declaration policy): every column of the set is a column of the table
// of the same type and nullability, the keys are equal, and each index of the set is a leading prefix of the key or of
// one of the table's indexes (which end with the key).
func CheckExposes(table ExposingTable, set *ColumnSet) error {
	what := fmt.Sprintf("%s: exposes %s", table.Name, quote(set.Name))
	for _, column := range set.Columns {
		own, found := table.ByName[column.Name]
		if !found {
			return fmt.Errorf("%s: the table has no column %s", what, quote(column.Name))
		}
		if !SameColumn(own, column) {
			return fmt.Errorf("%s: column %s is declared differently in the table", what, quote(column.Name))
		}
	}
	if !slices.Equal(table.Key, set.Key) {
		return fmt.Errorf("%s: the keys differ", what)
	}
	paths := [][]string{table.Key}
	for _, index := range table.Indexes {
		paths = append(paths, IndexPath(index.Columns, table.Key))
	}
	for _, index := range set.Indexes {
		leading := slices.ContainsFunc(paths, func(path []string) bool {
			return len(path) >= len(index.Columns) && slices.Equal(path[:len(index.Columns)], index.Columns)
		})
		if !leading {
			return fmt.Errorf("%s: index %s is no leading prefix of the key or of an index of the table", what, quote(index.Name))
		}
	}
	return nil
}

// Exposer is a table's name and the column set it exposes, for CheckExposedOnce.
type Exposer struct {
	Name    string
	Exposes *ColumnSet
}

// CheckExposedOnce refuses a table exposing a set another table declared by the same object exposes (declaration
// policy: a set name is bound to one table), naming both.
func CheckExposedOnce(definition Exposer, declared []Exposer, label string) error {
	if definition.Exposes == nil {
		return nil
	}
	set := definition.Exposes.Name
	for _, other := range declared {
		if other.Exposes != nil && other.Exposes.Name == set {
			return fmt.Errorf("%s: column set %s is exposed by tables %s and %s", label, quote(set), quote(other.Name), quote(definition.Name))
		}
	}
	return nil
}

// SameColumn reports whether two columns are the same to a reader: the type, whether `opt(...)`, a `host(...)`
// column's kind. A default is the writer's business.
func SameColumn(a, b Column) bool {
	x, y := ColumnRecordOf(a), ColumnRecordOf(b)
	return x.Type == y.Type && x.Optional == y.Optional && (x.Host == nil) == (y.Host == nil) && (x.Host == nil || *x.Host == *y.Host)
}

// IndexPath is the columns an index is stored by: its own, then the key columns not among them.
func IndexPath(columns, key []string) []string {
	path := slices.Clone(columns)
	for _, column := range key {
		if !slices.Contains(columns, column) {
			path = append(path, column)
		}
	}
	return path
}
