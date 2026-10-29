package store

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/rows.ts
//
// Rows (slice 6a §5 "Values at write", §9): one compiled codec per table version between a plugin's rows and keys
// (a trust boundary, checked closed) and what the store binds and reads; `json(...)` cells serialized at staging
// (rule 8), never walked (R9), and migrated on access when read.
//
// Go forced differences:
//   - A plugin row is a `Row` (map[string]any) or an ordered *delta.JsonObject; a key present with a nil value is
//     JavaScript's `null` (refused for a scalar, the JSON `null` for a json column) and an absent key is
//     `undefined` (missing / NULL). Ints are accepted as any Go integer or an integral float64 within the safe range.
//   - A StoredRow (what the outbox records) is an ordered *delta.JsonObject in declaration order whose numbers are
//     float64 (strict JSON), so it serializes in Pi's key order and number format; EncodedRow.Columns likewise.
//   - JSON.stringify's cycle/BigInt failures: the cell is first checked with chordjson.Validate (cycles and values
//     JSON cannot carry fail "not JSON"; a non-finite number, which JSON.stringify writes as null, fails too).
//   - `json.Marshal` panics on a cycle that Validate would reject, so the check cannot be skipped.

// StoredRow is a row as the outbox records it: the stored values in declaration order (scalars, json values), `opt`
// columns absent when NULL.
type StoredRow = *delta.JsonObject

// EncodedRow is a `put` ready for the step: its parameters in Physical order and its record.
type EncodedRow struct {
	Params []sqlite.SqliteValue
	Row    StoredRow
	// Columns is each written `json(...)` column's version, in column order.
	Columns *delta.JsonObject
	// ID is the row's key identity, as EncodedKey.ID.
	ID string
}

// EncodedKey is a key ready for the store: its parameters in key order, its record and its identity within one table.
type EncodedKey struct {
	Params []sqlite.SqliteValue
	// Key is the key record in key order.
	Key *delta.JsonObject
	// ID is `JSON.stringify` of the parameters: equal keys, equal ids.
	ID string
}

// VersionColumn is the physical name of a `json(...)` column's version column.
func VersionColumn(column string) string { return column + "$v" }

// RowVersion is the row's eager-step version column (W11): set by `put` and eager migration steps only.
const RowVersion = "$v"

// NamedType is the name and type a physical column order is computed from.
type NamedType struct {
	Name string
	Type ColumnType
}

// PhysicalColumns is the physical column order of a stored row: the declared columns, each `json(...)` column's
// version, then `$v`.
func PhysicalColumns(columns []NamedType) []string {
	physical := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		physical = append(physical, column.Name)
	}
	for _, column := range columns {
		if column.Type == ColJson {
			physical = append(physical, VersionColumn(column.Name))
		}
	}
	return append(physical, RowVersion)
}

// ToSqlite is a scalar as SQLite binds it: booleans as 0 and 1.
func ToSqlite(scalar Scalar) sqlite.SqliteValue {
	if flag, ok := scalar.(bool); ok {
		if flag {
			return int64(1)
		}
		return int64(0)
	}
	return scalar
}

// RowCodec is a table version's codec: `put` rows and keys checked and encoded, stored rows decoded. Physical is the
// column order of an encoded row (PhysicalColumns).
type RowCodec struct {
	Physical     []string
	columns      []Column
	key          []Column
	json         []Column
	names        map[string]bool
	keyNames     map[string]bool
	keyPositions []int
	table        string
}

// NewRowCodec is the codec of columns and key (names of columns) of table.
func NewRowCodec(columns []Column, key []string, table string) *RowCodec {
	codec := &RowCodec{columns: columns, names: map[string]bool{}, keyNames: map[string]bool{}, table: table}
	named := make([]NamedType, len(columns))
	for index, column := range columns {
		named[index] = NamedType{Name: column.Name, Type: column.Type}
		codec.names[column.Name] = true
		if column.Type == ColJson {
			codec.json = append(codec.json, column)
		}
	}
	for _, name := range key {
		position := slices.IndexFunc(columns, func(column Column) bool { return column.Name == name })
		codec.key = append(codec.key, columns[position])
		codec.keyPositions = append(codec.keyPositions, position)
		codec.keyNames[name] = true
	}
	codec.Physical = PhysicalColumns(named)
	return codec
}

// Put checks and encodes a `put` row of table version version (a trust boundary): an object with every non-`opt`
// column (a defaulted one filled in) and no other field; `invalid/args` otherwise.
func (codec *RowCodec) Put(row any, version int) (*EncodedRow, error) {
	what := codec.table + ": put"
	fields, err := closedObject(row, codec.names, what)
	if err != nil {
		return nil, err
	}
	params := make([]sqlite.SqliteValue, 0, len(codec.Physical))
	stored := delta.NewJsonObject(len(codec.columns))
	for _, column := range codec.columns {
		value, present := fields(column.Name)
		if !present && column.Type != ColJson {
			value, present = column.Default, column.Default != nil
		}
		if !present {
			if !column.Optional {
				return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: column \"%s\" is missing", what, column.Name))
			}
			params = append(params, nil)
			continue
		}
		cell, err := EncodeCell(column, value, fmt.Sprintf("%s: column \"%s\"", what, column.Name))
		if err != nil {
			return nil, err
		}
		params = append(params, cell.Param)
		stored.Set(column.Name, cell.Stored)
	}
	versions := delta.NewJsonObject(len(codec.json))
	for _, column := range codec.json {
		if stored.Has(column.Name) {
			versions.Set(column.Name, float64(column.Json.Version))
			params = append(params, int64(column.Json.Version))
		} else {
			params = append(params, nil)
		}
	}
	params = append(params, int64(version))
	keyParams := make([]sqlite.SqliteValue, len(codec.keyPositions))
	for index, position := range codec.keyPositions {
		keyParams[index] = params[position]
	}
	id, err := jsstring.MarshalJSON(keyParams)
	if err != nil {
		return nil, err
	}
	return &EncodedRow{Params: params, Row: stored, Columns: versions, ID: string(id)}, nil
}

// Key checks and encodes a key (a trust boundary): an object with exactly the key columns; `invalid/args` otherwise.
func (codec *RowCodec) Key(key any, what string) (*EncodedKey, error) {
	fields, err := closedObject(key, codec.keyNames, fmt.Sprintf("%s: %s: the key", codec.table, what))
	if err != nil {
		return nil, err
	}
	params := make([]sqlite.SqliteValue, 0, len(codec.key))
	stored := delta.NewJsonObject(len(codec.key))
	for _, column := range codec.key {
		value, present := fields(column.Name)
		where := fmt.Sprintf("%s: %s: key column \"%s\"", codec.table, what, column.Name)
		if !present {
			return nil, data.Invalid(data.ReasonArgs, where+" is missing")
		}
		scalar, err := encodeScalar(column, value, where)
		if err != nil {
			return nil, err
		}
		params = append(params, ToSqlite(scalar))
		stored.Set(column.Name, jsonScalar(scalar))
	}
	id, err := jsstring.MarshalJSON(params)
	if err != nil {
		return nil, err
	}
	return &EncodedKey{Params: params, Key: stored, ID: string(id)}, nil
}

// Decode is a stored row as reads return it: booleans from 0 and 1, `opt(...)` columns absent when NULL,
// `json(...)` cells parsed and migrated on access (a cell the code cannot read fails, see JsonColumn.Read).
func (codec *RowCodec) Decode(stored sqlite.SqliteRow, ctx context.Context) (Row, error) {
	row := Row{}
	for _, column := range codec.columns {
		value := stored[column.Name]
		if value == nil {
			continue
		}
		switch column.Type {
		case ColBool:
			row[column.Name] = value == int64(1)
		case ColJson:
			version, err := sqlite.RowInt(stored, VersionColumn(column.Name))
			if err != nil {
				return nil, err
			}
			text, _ := value.(string)
			cell, err := column.Json.Read(text, int(version), codec.table+"."+column.Name, ctx)
			if err != nil {
				return nil, err
			}
			row[column.Name] = cell
		case ColNum:
			if integer, ok := value.(int64); ok {
				value = float64(integer)
			}
			row[column.Name] = value
		default:
			row[column.Name] = value
		}
	}
	return row, nil
}

// closedObject is the field lookup of value when it is an object with only fields of names; `invalid/args`
// otherwise. A field present with a nil value is JavaScript's `null`; an absent one is `undefined`.
func closedObject(value any, names map[string]bool, what string) (func(string) (any, bool), error) {
	var keys []string
	var lookup func(string) (any, bool)
	switch typed := value.(type) {
	case map[string]any:
		if typed == nil {
			return nil, data.Invalid(data.ReasonArgs, what+": expected an object")
		}
		for key := range typed {
			keys = append(keys, key)
		}
		// Go maps have no order: report the first unknown column by name.
		slices.Sort(keys)
		lookup = func(name string) (any, bool) { item, ok := typed[name]; return item, ok }
	case *delta.JsonObject:
		if typed == nil {
			return nil, data.Invalid(data.ReasonArgs, what+": expected an object")
		}
		keys = typed.Keys()
		lookup = typed.Get
	default:
		return nil, data.Invalid(data.ReasonArgs, what+": expected an object")
	}
	for _, field := range keys {
		if !names[field] {
			return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: unknown column \"%s\"", what, field))
		}
	}
	return lookup, nil
}

// Cell is a checked cell: what is bound and what is recorded.
type Cell struct {
	Param  sqlite.SqliteValue
	Stored any
}

// EncodeCell checks and encodes a value of column; a `json(...)` value is serialized, not walked (R9). Stored is a
// strict JSON value: a string, a float64, a bool or the json value itself.
func EncodeCell(column Column, value any, what string) (Cell, error) {
	if column.Type != ColJson {
		stored, err := CheckScalar(column.Type, value, what)
		if err != nil {
			return Cell{}, err
		}
		return Cell{Param: ToSqlite(stored), Stored: jsonScalar(stored)}, nil
	}
	if err := chordjson.Validate(value); err != nil {
		return Cell{}, data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: not JSON (%s)", what, err.Error()))
	}
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		return Cell{}, data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: not JSON (%s)", what, err.Error()))
	}
	// Marshal accepted it; the recorded value is the one serialized. It is kept, not copied or parsed back:
	// same-host values are immutable by convention.
	return Cell{Param: string(text), Stored: value}, nil
}

// jsonScalar is a scalar as the strict JSON of a record: ints as float64.
func jsonScalar(scalar Scalar) any {
	if integer, ok := scalar.(int64); ok {
		return float64(integer)
	}
	return scalar
}

// encodeScalar checks a scalar value of column and returns it (a json column has no scalar value).
func encodeScalar(column Column, value any, what string) (Scalar, error) {
	if column.Type == ColJson {
		return nil, data.Invalid(data.ReasonArgs, what+": a json(...) column has no scalar value")
	}
	return CheckScalar(column.Type, value, what)
}

// safeInteger is number as an int64 when it is an integer within ±(2^53 - 1): Number.isSafeInteger.
func safeInteger(number float64) (int64, bool) {
	if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || math.Abs(number) > float64(MaxSafeInteger) {
		return 0, false
	}
	return int64(number), true
}

func isFinite(number float64) bool { return !math.IsNaN(number) && !math.IsInf(number, 0) }

// CheckScalar is a value of a scalar column type as the store keeps it (a trust boundary): `str` well-formed without
// NUL (string), `int` a safe integer (any Go integer or integral float64; int64), `num` finite (float64), `bool` a
// boolean; `invalid/args` otherwise.
func CheckScalar(typ ColumnType, value any, what string) (Scalar, error) {
	switch typ {
	case ColStr:
		if text, ok := value.(string); ok {
			return StoredText(text, what)
		}
	case ColInt:
		if integer, ok := integerOf(value); ok {
			return integer, nil
		}
	case ColNum:
		if number, ok := numberOf(value); ok && isFinite(number) {
			return number, nil
		}
	case ColBool:
		if flag, ok := value.(bool); ok {
			return flag, nil
		}
	}
	shown, err := jsstring.MarshalJSON(value)
	if err != nil || value == nil {
		shown = []byte(fmt.Sprint(value))
		if value == nil {
			shown = []byte("null")
		}
	}
	return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: %s is not a %s value", what, shown, typ))
}

// numberOf is value as a float64 when it is a Go number.
func numberOf(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	}
	if integer, ok := integerKind(value); ok {
		return float64(integer), true
	}
	return 0, false
}

// integerOf is value as a safe integer when it is a Go integer or an integral float64 within the safe range.
func integerOf(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return safeInteger(typed)
	case float32:
		return safeInteger(float64(typed))
	}
	integer, ok := integerKind(value)
	if !ok || integer > MaxSafeInteger || integer < -MaxSafeInteger {
		return 0, false
	}
	return integer, true
}

// integerKind is a Go integer value as int64 (a uint64 above int64 is out of the safe range: MaxInt64).
func integerKind(value any) (int64, bool) {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if unsigned := reflected.Uint(); unsigned <= math.MaxInt64 {
			return int64(unsigned), true
		}
		return math.MaxInt64, true
	}
	return 0, false
}
