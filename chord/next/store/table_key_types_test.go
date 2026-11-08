package store

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// upstream: packages/chord/test/next/store/table.test.ts "keys: type an int() key column as a number and a str() one as a string (tutorial, 2026-11-03)". Upstream pins KeyOf at the type level; a Go key is a Row (map[string]any) checked at the trust boundary, so the same property runs: an int() key column takes a number and refuses a string, a str() one takes a string and refuses a number, for a key of int alone and for a mixed key.
func TestKeyColumnsTakeTheirColumnTypeAnIntANumberAndAStrAString(t *testing.T) {
	byNumber := qDefine(t, "by_number", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Int()), Col("text", Str())}, Key: []string{"id"}})
	mixed := qDefine(t, "mixed", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Str()), Col("at", Int()), Col("text", Str())}, Key: []string{"id", "at"}})

	key, err := byNumber.Rows.Key(Row{"id": float64(7)}, "get")
	if err != nil || !reflect.DeepEqual(key.Params, []sqlite.SqliteValue{int64(7)}) {
		t.Fatalf("an int key 7 = %+v, %v; want params [7]", key, err)
	}
	key, err = mixed.Rows.Key(Row{"id": "a", "at": float64(1)}, "get")
	if err != nil || !reflect.DeepEqual(key.Params, []sqlite.SqliteValue{"a", int64(1)}) {
		t.Fatalf("a mixed key = %+v, %v; want params [a 1]", key, err)
	}
	for _, refused := range []struct {
		name  string
		codec *RowCodec
		key   Row
	}{
		{"a string for an int() key column", byNumber.Rows, Row{"id": "7"}},
		{"a number for a str() key column", mixed.Rows, Row{"id": float64(1), "at": float64(1)}},
		{"a string for the int() column of a mixed key", mixed.Rows, Row{"id": "a", "at": "1"}},
	} {
		_, err := refused.codec.Key(refused.key, "get")
		_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		if err == nil {
			t.Errorf("%s was accepted", refused.name)
		}
	}
}
