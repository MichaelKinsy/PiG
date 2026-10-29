package store

import (
	"maps"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/next/store/rows.test.ts: rows (slice 6a §5, §9): `put` rows and keys checked and encoded,
// stored rows decoded.
//
// Go test conventions: Pi's `{ ...row, id: undefined }` is the row without the key (an absent key is `undefined`; a
// key present with nil is JavaScript's `null`, see rows.go). Designed out: the BigInt case of "serializes json cells
// at staging" (Go has no BigInt; the cycle case is kept).

func rowsTable(t *testing.T) *TableDefinition {
	t.Helper()
	definition, err := DefineTable("t", TableOptions{
		Version: 3,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("n", Int(contracts.Default(7))),
			Col("x", Num()),
			Col("b", Bool()),
			Col("note", Opt(Str())),
			Col("meta", Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 2})),
		},
		Key: []string{"id"},
	})
	if err != nil {
		t.Fatalf("DefineTable: %v", err)
	}
	return definition
}

func rowsPutFails(t *testing.T, codec *RowCodec, row any, want string) {
	t.Helper()
	_, err := codec.Put(row, 3)
	if err == nil {
		t.Fatalf("Put(%v) succeeded, want %q", row, want)
	}
	if want == "" {
		_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	} else if !strings.Contains(err.Error(), want) {
		t.Fatalf("Put error = %v, want it to contain %q", err, want)
	}
}

func rowsWith(row Row, key string, value any) Row {
	next := Row{}
	maps.Copy(next, row)
	if value == nil {
		delete(next, key)
	} else {
		next[key] = value
	}
	return next
}

func TestPutRows(t *testing.T) {
	codec := rowsTable(t).Rows

	t.Run("encodes a row in physical order, defaults filled, booleans as 0 and 1, json serialized with its version", func(t *testing.T) {
		encoded, err := codec.Put(Row{"id": "a", "x": 1.5, "b": true, "meta": delta.JsonObjectOf("a", float64(1))}, 3)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		wantParams := []sqlite.SqliteValue{"a", int64(7), 1.5, int64(1), nil, `{"a":1}`, int64(2), int64(3)}
		if !reflect.DeepEqual(encoded.Params, wantParams) {
			t.Fatalf("params = %#v, want %#v", encoded.Params, wantParams)
		}
		nexttest.RequireJSONEqual(t, encoded.Row, delta.JsonObjectOf("id", "a", "n", float64(7), "x", 1.5, "b", true, "meta", delta.JsonObjectOf("a", float64(1))))
		nexttest.RequireJSONEqual(t, encoded.Columns, delta.JsonObjectOf("meta", float64(2)))
		key, err := codec.Key(Row{"id": "a"}, "get")
		if err != nil || encoded.ID != key.ID {
			t.Fatalf("row id = %q, key id = %v, %v", encoded.ID, key, err)
		}
	})

	t.Run("is a closed check: missing and unknown columns, wrong types, unsafe integers, non-finite numbers, NUL", func(t *testing.T) {
		row := Row{"id": "a", "x": float64(1), "b": false, "meta": delta.NewJsonObject(0)}
		rowsPutFails(t, codec, rowsWith(row, "id", nil), "")
		rowsPutFails(t, codec, rowsWith(row, "extra", 1), `unknown column "extra"`)
		rowsPutFails(t, codec, rowsWith(row, "n", float64(1<<53)), "")
		rowsPutFails(t, codec, rowsWith(row, "n", 1.5), "")
		rowsPutFails(t, codec, rowsWith(row, "x", jsstringInfinity()), "")
		rowsPutFails(t, codec, rowsWith(row, "b", int64(1)), "")
		rowsPutFails(t, codec, rowsWith(row, "id", "a\x00"), "NUL")
		rowsPutFails(t, codec, rowsWith(row, "note", struct{}{}), "")
		// A key present with nil is JavaScript's `null`: no str value.
		withNull := rowsWith(row, "note", "x")
		withNull["note"] = nil
		rowsPutFails(t, codec, withNull, "")
		rowsPutFails(t, codec, []any{}, "expected an object")
	})

	t.Run("serializes json cells at staging: a cycle fails there (rule 8)", func(t *testing.T) {
		cycle := map[string]any{}
		cycle["self"] = cycle
		rowsPutFails(t, codec, Row{"id": "a", "x": float64(1), "b": false, "meta": cycle}, "")
	})

	t.Run("replaces lone surrogates in text", func(t *testing.T) {
		lone := jsstring.FromUTF16([]uint16{'a', 0xD800})
		encoded, err := codec.Put(Row{"id": lone, "x": float64(1), "b": false, "meta": delta.NewJsonObject(0)}, 3)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if id := encoded.Row.Value("id"); id != "a\uFFFD" {
			t.Fatalf("id = %q, want a U+FFFD", id)
		}
	})
}

func TestKeys(t *testing.T) {
	codec := rowsTable(t).Rows

	t.Run("takes exactly the key columns", func(t *testing.T) {
		key, err := codec.Key(Row{"id": "a"}, "get")
		if err != nil || !reflect.DeepEqual(key.Params, []sqlite.SqliteValue{"a"}) {
			t.Fatalf("Key = %+v, %v; want params [a]", key, err)
		}
		_, err = codec.Key(Row{}, "get")
		if err == nil || !strings.Contains(err.Error(), "is missing") {
			t.Fatalf("Key({}) error = %v, want is missing", err)
		}
		_, err = codec.Key(Row{"id": "a", "n": 1}, "get")
		if err == nil || !strings.Contains(err.Error(), `unknown column "n"`) {
			t.Fatalf("Key(extra) error = %v, want unknown column", err)
		}
		_, err = codec.Key(Row{"id": 1}, "get")
		_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	})
}

func TestDecoding(t *testing.T) {
	codec := rowsTable(t).Rows

	t.Run("reads booleans, leaves NULL opt columns absent and parses json cells", func(t *testing.T) {
		row, err := codec.Decode(sqlite.SqliteRow{"id": "a", "n": int64(1), "x": float64(2), "b": int64(0), "note": nil, "meta": `{"a":1}`, "meta$v": int64(2)}, background)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(row) != 5 || row["id"] != "a" || row["n"] != int64(1) || row["x"] != float64(2) || row["b"] != false {
			t.Fatalf("row = %#v", row)
		}
		nexttest.RequireJSONEqual(t, row["meta"], delta.JsonObjectOf("a", float64(1)))
	})

	t.Run("refuses a json cell of a newer version", func(t *testing.T) {
		_, err := codec.Decode(sqlite.SqliteRow{"id": "a", "n": int64(1), "x": float64(2), "b": int64(0), "note": nil, "meta": "{}", "meta$v": int64(3)}, background)
		if err == nil || !strings.Contains(err.Error(), "newer than 2") {
			t.Fatalf("Decode error = %v, want newer than 2", err)
		}
	})
}

// jsstringInfinity is Number.POSITIVE_INFINITY.
func jsstringInfinity() float64 { return math.Inf(1) }
