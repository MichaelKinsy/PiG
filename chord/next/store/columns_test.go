package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/store/columns.test.ts: table columns (decisions "Flat columns", "Nullable columns
// and the `set` form"): descriptors.
//
// Designed out: "row types" (the `RowOf` / `PutRowOf` type-level Equal checks): Go has no type-level machinery over
// column declarations; a row is `Row`.

func describeFails(t *testing.T, columns []ColumnProp, want string) {
	t.Helper()
	_, err := DescribeColumns(columns, "t")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("DescribeColumns error = %v, want it to contain %q", err, want)
	}
}

func TestColumnDescriptors(t *testing.T) {
	t.Run("describes the helpers in declaration order", func(t *testing.T) {
		columns, err := DescribeColumns([]ColumnProp{
			Col("id", Str()),
			Col("n", Int(contracts.Default(0))),
			Col("x", Num()),
			Col("b", Bool()),
			Col("note", Opt(Str())),
			Col("meta", Opt(Json(contracts.Obj(), JsonColumnOptions{Version: 2}))),
		}, "t")
		if err != nil {
			t.Fatalf("DescribeColumns: %v", err)
		}
		type described struct {
			name     string
			typ      ColumnType
			optional bool
		}
		var got []described
		for _, column := range columns {
			got = append(got, described{column.Name, column.Type, column.Optional})
		}
		want := []described{{"id", ColStr, false}, {"n", ColInt, false}, {"x", ColNum, false}, {"b", ColBool, false}, {"note", ColStr, true}, {"meta", ColJson, true}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("columns = %v, want %v", got, want)
		}
		records := make([]ColumnRecord, len(columns))
		for index, column := range columns {
			records[index] = ColumnRecordOf(column)
		}
		meta := records[5]
		if meta.Fingerprint == "" || meta.Version == nil || *meta.Version != 2 {
			t.Fatalf("json record = %+v, want version 2 and a fingerprint", meta)
		}
		records[5] = ColumnRecord{Type: ColJson, Optional: true, Version: meta.Version}
		wantRecords := []ColumnRecord{
			{Type: ColStr},
			{Type: ColInt, Default: int64(0)},
			{Type: ColNum},
			{Type: ColBool},
			{Type: ColStr, Optional: true},
			{Type: ColJson, Optional: true, Version: meta.Version},
		}
		if !reflect.DeepEqual(records, wantRecords) {
			t.Fatalf("records = %+v, want %+v", records, wantRecords)
		}
	})

	t.Run("checks names: the pattern, no two equal ignoring case, no $", func(t *testing.T) {
		describeFails(t, []ColumnProp{Col("a-b", Str())}, "[A-Za-z_][A-Za-z0-9_]*")
		describeFails(t, []ColumnProp{Col("a$v", Str())}, `column "a$v"`)
		describeFails(t, []ColumnProp{Col("path", Str()), Col("Path", Str())}, "only in case")
		columns, err := DescribeColumns([]ColumnProp{Col("camelCase", Str()), Col("_x1", Int())}, "t")
		if err != nil || len(columns) != 2 {
			t.Fatalf("DescribeColumns = %v, %v; want 2 columns", columns, err)
		}
	})

	t.Run("refuses other schemas, bounds, patterns, opt defaults and defaults of another type", func(t *testing.T) {
		column := func(schema ColumnSchema) []ColumnProp { return []ColumnProp{Col("c", schema)} }
		describeFails(t, column(ColumnSchema{Schema: contracts.Arr(contracts.Str())}), "expected str()")
		describeFails(t, column(ColumnSchema{Schema: contracts.Obj()}), "expected str()")
		describeFails(t, column(Str(contracts.Max(3))), `"maxLength" is not supported`)
		describeFails(t, column(Str(contracts.Pattern("a"))), `"pattern" is not supported`)
		describeFails(t, column(Opt(Int(contracts.Default(1)))), "no default")
		describeFails(t, column(Int(contracts.Default(1.5))), "not a int value")
		describeFails(t, column(Str(contracts.Default("a\x00"))), "NUL")
	})
}
