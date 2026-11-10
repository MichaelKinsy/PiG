package store

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/store/host-column.test.ts: `host(kind)` table columns (decision "Documents and table rows
// can belong to a host"): the helper, its definition's stored keys and addresses, and its refusals.
//
// Go forms: `host` is Host, which panics with NewHostColumn's error as Json does (the test of an undeclared kind calls
// both); a lone surrogate is the invalid UTF-8 bytes `\xed\xa0\x80`; JavaScript's `undefined` value is nil.
//
// pi: packages/chord/src/store/host-column.ts

// hostColKind is the host kind name with one `id` param.
func hostColKind(t testing.TB, name string) *contracts.HostKind {
	t.Helper()
	kind, err := contracts.NewHostKind(name, contracts.Prop("id", contracts.Str()))
	if err != nil {
		t.Fatalf("NewHostKind(%s): %v", name, err)
	}
	return kind
}

// hostColAddress is the address of kind with param id.
func hostColAddress(kind *contracts.HostKind, id string) contracts.Address {
	return kind.Address(delta.JsonObjectOf("id", id))
}

func TestHostColumns(t *testing.T) {
	session := hostColKind(t, "session")
	column := func(t *testing.T) *HostColumn {
		t.Helper()
		column, err := NewHostColumn(session)
		must(t, err)
		return column
	}

	t.Run("store an address of their kind as its key and read the key back as the address", func(t *testing.T) {
		column := column(t)
		key, err := column.Key(hostColAddress(session, "S1"), "t: column")
		must(t, err)
		qEqual(t, key, "session|S1")
		address, err := column.Address("session|S1")
		must(t, err)
		qEqual(t, address, hostColAddress(session, "S1"))
	})

	t.Run("store the canonical key: a lone surrogate replaced, NUL refused", func(t *testing.T) {
		column := column(t)
		key, err := column.Key(hostColAddress(session, "a\xed\xa0\x80"), "t: column")
		must(t, err)
		qEqual(t, key, "session|a\uFFFD")
		_, err = column.Key(hostColAddress(session, "a\x00b"), "t: column")
		qFails(t, err, "NUL")
	})

	t.Run("refuse a value that is not an address of their kind", func(t *testing.T) {
		column := column(t)
		other := contracts.Address{Kind: "user", Params: delta.JsonObjectOf("id", "S1"), Key: "user|S1"}
		for _, value := range []any{"session|S1", other, nil} {
			_, err := column.Key(value, "t: column")
			qFails(t, err, "t: column: expected an address of session")
			_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		}
	})

	t.Run("refuse a kind that was not declared", func(t *testing.T) {
		_, err := NewHostColumn(&contracts.HostKind{Kind: "session"})
		qFails(t, err, "session: not a declared host kind")
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatalf("Host of an undeclared kind did not panic")
			}
		}()
		Host(&contracts.HostKind{Kind: "session"})
	})

	t.Run("are str columns to the store, never opt(...)", func(t *testing.T) {
		columns, err := DescribeColumns([]ColumnProp{Col("session", Host(session))}, "t")
		must(t, err)
		if got := columns[0]; got.Name != "session" || got.Type != ColStr || got.Optional || got.Host == nil {
			t.Fatalf("column = %+v, want the non-opt str column session with its host", got)
		}
		if !reflect.DeepEqual(columns[0].Host.Kind, session) {
			t.Fatalf("host kind = %+v, want session", columns[0].Host.Kind)
		}
	})
}

// A stored key of another kind is an error, as Pi's `addressOfKey` throws.
func TestHostColumnAddressOfAMalformedKey(t *testing.T) {
	column, err := NewHostColumn(hostColKind(t, "session"))
	must(t, err)
	_, err = column.Address("user|S1")
	qFails(t, err, `"user|S1": not an address key of session`)
}
