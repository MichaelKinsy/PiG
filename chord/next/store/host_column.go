package store

import (
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/store/host-column.ts
//
// `host(kind)` table columns (decision "Documents and table rows can belong to a host"): a column holding the address
// of a host of one kind, stored as the address key in a `TEXT` cell (a `str` column to the store), so the store finds
// the rows belonging to one host (`host_data.go`: export, import, delete). At most one per table, never `Opt(...)`,
// indexed.
//
// Go forced differences: HostColumnSchema<K> is designed out (the typed schema of the kind's addresses); Host returns a
// ColumnSchema carrying the HostColumn, and panics with NewHostColumn's error as Json does. A cell's value is a
// contracts.Address.

// HostColumnSchema is a `host(...)` column as `columns` takes it: a ColumnSchema carrying its HostColumn.
type HostColumnSchema = ColumnSchema

// Host is a column holding an address of host kind kind per row: the row belongs to that host. It panics with
// NewHostColumn's error (an undeclared kind).
func Host(kind *contracts.HostKind) HostColumnSchema {
	column, err := NewHostColumn(kind)
	if err != nil {
		panic(err)
	}
	return ColumnSchema{Host: column}
}

// HostColumn is a `host(...)` column's definition: its kind, the stored key of an address and the address of a stored
// key.
type HostColumn struct {
	Kind *contracts.HostKind
}

// NewHostColumn is the definition of a column of kind kind. Declaration policy: only a declared kind decodes keys
// (an error otherwise).
func NewHostColumn(kind *contracts.HostKind) (*HostColumn, error) {
	if _, err := contracts.KindParams(kind); err != nil {
		return nil, err
	}
	return &HostColumn{Kind: kind}, nil
}

// Key is the canonical stored key of value, an address of the column's kind (a trust boundary): its key under the
// `str()` text rules (a lone surrogate replaced, NUL refused), as document keys and host data selections have it;
// `invalid/args` otherwise.
func (column *HostColumn) Key(value any, what string) (string, error) {
	key, ok := contracts.KeyOfAddress(column.Kind, value)
	if !ok {
		return "", invalidArgs("%s: expected an address of %s", what, column.Kind.Kind)
	}
	return StoredText(key, what)
}

// Address is the address a stored key holds; a key of another kind is an error.
func (column *HostColumn) Address(key string) (contracts.Address, error) {
	return contracts.AddressOfKey(column.Kind, key)
}
