package store

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/columns.ts
//
// Table columns (decisions "Flat columns", "Nullable columns and the `set` form", "Restricted names"): the
// restricted subset of L1's helpers a table's `columns` take (`Str Int Num Bool`, `Opt(...)` of them, `Json(...)`,
// defaults), and the column descriptors and records the store keeps.
//
// Go forced differences: the type-level machinery (Columns' mapped types, ColumnTypeOf, ValueOf, RowOf, PutRowOf,
// OrderColumn) is designed out; a row is `Row = map[string]any` (str string, int int64, num float64, bool bool, json
// delta.JsonValue; an absent key is NULL). A table's columns are an ordered slice of ColumnProp (Go maps have no
// order), built with Col. Pi's `describeColumns` reads each schema as unknown data, so that every runtime check is
// kept: the ColumnSchema of any contracts.TSchema is accepted and refused when it is not a column schema.

// Row is a row as reads return it and `put` takes it: str string, int int64, num float64, bool bool, json
// delta.JsonValue; an `opt(...)` column that is NULL is absent.
type Row = map[string]any

// ColumnSchema is a schema a table column takes: a scalar one (Str Int Num Bool), a Json column, or either wrapped in
// Opt. Schema is a scalar column's contracts schema (its type and default); Json is set for a `json(...)` column.
type ColumnSchema struct {
	Schema   contracts.TSchema
	Optional bool
	Json     *JsonColumn
}

// ColumnProp is one column of a table's `columns`, in declaration order.
type ColumnProp struct {
	Name   string
	Schema ColumnSchema
}

// Col is `{ [name]: schema }`: Go has no ordered literal.
func Col(name string, schema ColumnSchema) ColumnProp { return ColumnProp{Name: name, Schema: schema} }

// Str is a `str()` column. Options as contracts.Str; a table column supports only Default (describeColumns refuses
// the rest, as Pi does).
func Str(opts ...contracts.Option) ColumnSchema { return ColumnSchema{Schema: contracts.Str(opts...)} }

// Int is an `int()` column.
func Int(opts ...contracts.Option) ColumnSchema { return ColumnSchema{Schema: contracts.Int(opts...)} }

// Num is a `num()` column.
func Num(opts ...contracts.Option) ColumnSchema { return ColumnSchema{Schema: contracts.Num(opts...)} }

// Bool is a `bool()` column.
func Bool(opts ...contracts.Option) ColumnSchema {
	return ColumnSchema{Schema: contracts.Bool(opts...)}
}

// Opt makes a column nullable.
func Opt(schema ColumnSchema) ColumnSchema {
	schema.Optional = true
	return schema
}

// ColumnType is a column's type in the store.
type ColumnType string

// The column types.
const (
	ColStr  ColumnType = "str"
	ColInt  ColumnType = "int"
	ColNum  ColumnType = "num"
	ColBool ColumnType = "bool"
	ColJson ColumnType = "json"
)

// Scalar is a scalar cell as the store keeps it in rows, writes and operands: a well-formed string (string), an
// integer (int64), a number (float64) or a boolean (bool).
type Scalar = any

// Column is a column of the store: its name, type, nullability and default; a `json(...)` one with its definition.
type Column struct {
	Name     string
	Type     ColumnType
	Optional bool
	// Default is the declared default of a scalar column; nil when there is none (a json column has none).
	Default Scalar
	// Json is a `json(...)` column's definition.
	Json *JsonColumn
}

// ColumnRecord is a column as the `table` write records it (slice 6a §5): what a replica needs without the plugin's
// code. The fields serialize in Pi's key order.
type ColumnRecord struct {
	Type     ColumnType `json:"type"`
	Optional bool       `json:"optional,omitempty"`
	Default  Scalar     `json:"default,omitempty"`
	// Version and Fingerprint are a `json(...)` column's version and schema fingerprint.
	Version     *int   `json:"version,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ColumnRecordEntry is a column's name and record.
type ColumnRecordEntry struct {
	Name   string
	Record ColumnRecord
}

// ColumnRecords are the columns of a table as recorded, in declaration order (a JSON object's key order; column
// names never look like integers, so JavaScript keeps their order).
type ColumnRecords []ColumnRecordEntry

// Get is the record of column name.
func (records ColumnRecords) Get(name string) (ColumnRecord, bool) {
	for _, entry := range records {
		if entry.Name == name {
			return entry.Record, true
		}
	}
	return ColumnRecord{}, false
}

// Names are the column names in order.
func (records ColumnRecords) Names() []string {
	names := make([]string, len(records))
	for index, entry := range records {
		names[index] = entry.Name
	}
	return names
}

// MarshalJSON is the JSON object of the records, in order (what JSON.stringify gives).
func (records ColumnRecords) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for index, entry := range records {
		if index > 0 {
			out = append(out, ',')
		}
		out = jsstring.AppendString(out, entry.Name)
		out = append(out, ':')
		text, err := jsstring.MarshalJSON(entry.Record)
		if err != nil {
			return nil, err
		}
		out = append(out, text...)
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads a JSON object of records in key order.
func (records *ColumnRecords) UnmarshalJSON(text []byte) error {
	value, err := delta.DecodeJson(text)
	if err != nil {
		return err
	}
	parsed, err := ColumnRecordsFromJSON(value)
	if err != nil {
		return err
	}
	*records = parsed
	return nil
}

// ColumnRecordsFromJSON reads the records of a decoded JSON object (an *delta.JsonObject, keys in order) of the
// shape `ColumnRecord` serializes to; any other shape is an error.
func ColumnRecordsFromJSON(value any) (ColumnRecords, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, errors.New("store: column records: expected an object")
	}
	records := make(ColumnRecords, 0, object.Len())
	for name, raw := range object.All() {
		record, err := ColumnRecordFromJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("store: column \"%s\": %w", name, err)
		}
		records = append(records, ColumnRecordEntry{Name: name, Record: record})
	}
	return records, nil
}

// ColumnRecordFromJSON reads a decoded JSON column record. An int column's default is an int64, a num's a float64.
func ColumnRecordFromJSON(value any) (ColumnRecord, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return ColumnRecord{}, errors.New("expected an object")
	}
	var record ColumnRecord
	typ, _ := object.Value("type").(string)
	switch ColumnType(typ) {
	case ColStr, ColInt, ColNum, ColBool, ColJson:
		record.Type = ColumnType(typ)
	default:
		return ColumnRecord{}, fmt.Errorf("unknown type \"%s\"", typ)
	}
	if optional, ok := object.Value("optional").(bool); ok {
		record.Optional = optional
	}
	if fallback, present := object.Get("default"); present {
		switch typed := fallback.(type) {
		case float64:
			if record.Type == ColInt {
				record.Default = int64(typed)
			} else {
				record.Default = typed
			}
		default:
			record.Default = typed
		}
	}
	if version, ok := object.Value("version").(float64); ok {
		number := int(version)
		record.Version = &number
	}
	if fingerprint, ok := object.Value("fingerprint").(string); ok {
		record.Fingerprint = fingerprint
	}
	return record, nil
}

// ColumnNamePattern is the column name rule (decision "Restricted names").
var ColumnNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DescribeColumns are the columns of columns in declaration order, checked (declaration policy: JavaScript callers
// reach it): names `[A-Za-z_][A-Za-z0-9_]*` without two equal ignoring ASCII case, only the helpers' types, no
// bounds or patterns (a bulk `set` could not keep them), a default of the column's type and not on an `Opt(...)`
// column.
func DescribeColumns(columns []ColumnProp, table string) ([]Column, error) {
	seen := map[string]bool{}
	described := make([]Column, 0, len(columns))
	for _, prop := range columns {
		if err := CheckColumnName(prop.Name, table); err != nil {
			return nil, err
		}
		folded := asciiLower(prop.Name)
		if seen[folded] {
			return nil, fmt.Errorf("%s: column \"%s\" differs from another only in case", table, prop.Name)
		}
		seen[folded] = true
		column, err := describeColumn(prop.Name, prop.Schema, fmt.Sprintf("%s: column \"%s\"", table, prop.Name))
		if err != nil {
			return nil, err
		}
		described = append(described, column)
	}
	return described, nil
}

// asciiLower is toLowerCase for names that passed the name rule (ASCII only).
func asciiLower(name string) string { return strings.ToLower(name) }

// CheckColumnName fails unless name follows the column name rule (declaration policy); what names the table or the
// edit.
func CheckColumnName(name, what string) error {
	if !ColumnNamePattern.MatchString(name) {
		return fmt.Errorf("%s: column \"%s\": expected [A-Za-z_][A-Za-z0-9_]*", what, name)
	}
	return nil
}

var scalarTypes = map[string]ColumnType{"string": ColStr, "integer": ColInt, "number": ColNum, "boolean": ColBool}

func describeColumn(name string, schema ColumnSchema, what string) (Column, error) {
	if schema.Json != nil {
		return Column{Name: name, Type: ColJson, Optional: schema.Optional, Json: schema.Json}, nil
	}
	// Declaration policy: a JavaScript caller may pass any schema, so its fields are read as unknown data.
	object, _ := schema.Schema.Node.(*delta.JsonObject)
	var typ ColumnType
	if object != nil {
		if text, ok := object.Value("type").(string); ok {
			typ = scalarTypes[text]
		}
	}
	if typ == "" {
		return Column{}, fmt.Errorf("%s: expected str(), int(), num(), bool(), opt(...) or json(...)", what)
	}
	for _, key := range object.Keys() {
		if key != "type" && key != "default" {
			return Column{}, fmt.Errorf("%s: \"%s\" is not supported in a table column", what, key)
		}
	}
	fallback, hasDefault := object.Get("default")
	if hasDefault {
		if schema.Optional {
			return Column{}, fmt.Errorf("%s: an opt(...) column has no default", what)
		}
		normalized, err := checkDefault(typ, fallback, what)
		if err != nil {
			return Column{}, err
		}
		fallback = normalized
	} else {
		fallback = nil
	}
	return Column{Name: name, Type: typ, Optional: schema.Optional, Default: fallback}, nil
}

// checkDefault checks a declared default and returns it as the column's Scalar (int: int64).
func checkDefault(typ ColumnType, value any, what string) (Scalar, error) {
	var valid bool
	var normalized Scalar = value
	switch typ {
	case ColStr:
		text, ok := value.(string)
		if ok {
			stored, err := StoredText(text, what)
			valid = err == nil && stored == text
			if err != nil {
				return nil, err
			}
		}
	case ColInt:
		if number, ok := value.(float64); ok {
			if integer, safe := safeInteger(number); safe {
				valid, normalized = true, integer
			}
		}
	case ColNum:
		number, ok := value.(float64)
		valid = ok && isFinite(number)
	case ColBool:
		_, valid = value.(bool)
	}
	if !valid {
		text, err := jsstring.MarshalJSON(value)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s: the default %s is not a %s value", what, text, typ)
	}
	return normalized, nil
}

// ColumnRecordOf is the record of column in the `table` write.
func ColumnRecordOf(column Column) ColumnRecord {
	if column.Type == ColJson {
		version := column.Json.Version
		return ColumnRecord{Type: ColJson, Optional: column.Optional, Version: &version, Fingerprint: column.Json.Fingerprint}
	}
	return ColumnRecord{Type: column.Type, Optional: column.Optional, Default: column.Default}
}
