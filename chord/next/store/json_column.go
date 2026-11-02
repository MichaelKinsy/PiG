package store

import (
	"context"
	"fmt"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/store/json-column.ts
//
// `Json(schema, JsonColumnOptions{Version, Migrate})` table columns (decision "The `json(...)` column helper", W11):
// one JSON `TEXT` cell with its own version, typed when read, migrated on access (the result checked once, R8),
// never walked on write (R9).
//
// Go forced differences: `json(...)` returns a ColumnSchema carrying the column (Pi's TypeBox `Unsafe` carrying a
// `json` field); the type machinery (JsonColumnSchema<T>, Static) is designed out. Json panics where Pi throws
// (an invalid version, a schema outside Chord's vocabulary): a declaration error of the plugin's code, as the
// constructors of chord/next/contracts do; NewJsonColumn returns the error.

// JsonColumnOptions is what Json takes besides the schema.
type JsonColumnOptions struct {
	// Version is the version of the cells' shape; a cell stores the version it was written at.
	Version int
	// Migrate maps a cell of an older version `from` to this version's shape; pure. Without it older cells are
	// refused. A returned error is `migrate threw`.
	Migrate func(old delta.JsonValue, from int) (any, error)
}

// Json is a column holding one JSON value of schema per row, at options.Version; wrap it in Opt to make it
// nullable. It panics with NewJsonColumn's error.
func Json(schema contracts.TSchema, options JsonColumnOptions) ColumnSchema {
	column, err := NewJsonColumn(schema, options.Version, options.Migrate)
	if err != nil {
		panic(err)
	}
	return ColumnSchema{Json: column}
}

// JsonColumn is a `json(...)` column's definition: its schema snapshot, version and fingerprint, and the read of a
// stored cell.
type JsonColumn struct {
	Schema  contracts.TSchema
	Version int
	// Fingerprint is the fingerprint of the schema's normal form, recorded with the table: the same version with
	// another is `broken`.
	Fingerprint string
	snapshot    *contracts.Snapshot
	migrate     func(old delta.JsonValue, from int) (any, error)
}

// NewJsonColumn is the column of schema at version. Declaration policy: JavaScript callers reach the version check.
func NewJsonColumn(schema contracts.TSchema, version int, migrate func(old delta.JsonValue, from int) (any, error)) (*JsonColumn, error) {
	if version < 1 || int64(version) > MaxSafeInteger {
		return nil, fmt.Errorf("json(...): version %d is not a positive integer", version)
	}
	snapshot, err := contracts.SnapshotSchema(schema, "json(...)")
	if err != nil {
		return nil, err
	}
	fingerprint, err := contracts.FingerprintOf(snapshot.Normal)
	if err != nil {
		return nil, err
	}
	return &JsonColumn{Schema: snapshot.Schema, Version: version, Fingerprint: fingerprint, snapshot: snapshot, migrate: migrate}, nil
}

// Read is the value of a stored cell written at version: parsed; an older one migrated and checked once (R8,
// counted). A newer version, a missing or failing Migrate and an invalid result return `unavailable` (what names the
// cell); the caller fails the owner (slice 6a §1.4), the data stays.
func (column *JsonColumn) Read(text string, version int, what string, ctx context.Context) (any, error) {
	value, err := delta.DecodeJson([]byte(text))
	if err != nil {
		return nil, err
	}
	if version == column.Version {
		return value, nil
	}
	if version > column.Version {
		return nil, refused(fmt.Sprintf("%s: stored at version %d, newer than %d", what, version, column.Version), nil)
	}
	if column.migrate == nil {
		return nil, refused(fmt.Sprintf("%s: no migrate from version %d to %d", what, version, column.Version), nil)
	}
	migrated, err := column.migrate(value, version)
	if err != nil {
		return nil, refused(fmt.Sprintf("%s: migrate from version %d threw", what, version), err)
	}
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.validations", telemetry.InstrumentOptions{}).Add(1, nil)
	validator, err := data.ValidatorFor(column.snapshot, ctx)
	if err != nil {
		return nil, err
	}
	if !validator.Check(migrated) {
		return nil, refused(fmt.Sprintf("%s: migrate from version %d does not match the schema", what, version), nil)
	}
	return migrated, nil
}

// refused is the error of a cell the code cannot read: the owner fails `broken` with it.
func refused(message string, cause error) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: message}, cause)
}
