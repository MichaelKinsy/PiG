package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/store/json-column.test.ts: `json(...)` columns (rule 8): their declaration, and a cell
// read and migrated on access.

var (
	metaV1 = contracts.Obj(contracts.Prop("tag", contracts.Str()))
	metaV2 = contracts.Obj(contracts.Prop("tags", contracts.Str()), contracts.Prop("n", contracts.Int()))
)

func jsonColumnOf(t *testing.T, schema contracts.TSchema, version int) *JsonColumn {
	t.Helper()
	column := Json(schema, JsonColumnOptions{Version: version}).Json
	if column == nil {
		t.Fatal("Json(...) carries no column")
	}
	return column
}

func TestJsonColumnDeclaration(t *testing.T) {
	t.Run("is a schema carrying its column: version and the fingerprint of the schema's normal form", func(t *testing.T) {
		column := jsonColumnOf(t, metaV1, 1)
		if column.Version != 1 {
			t.Fatalf("Version = %d, want 1", column.Version)
		}
		again := jsonColumnOf(t, contracts.Obj(contracts.Prop("tag", contracts.Str())), 1)
		other := jsonColumnOf(t, metaV2, 1)
		if again.Fingerprint != column.Fingerprint {
			t.Fatalf("fingerprints differ for equal schemas: %s, %s", again.Fingerprint, column.Fingerprint)
		}
		if other.Fingerprint == column.Fingerprint {
			t.Fatal("fingerprints equal for different schemas")
		}
	})

	t.Run("refuses a version that is not a positive integer", func(t *testing.T) {
		defer func() {
			recovered, _ := recover().(error)
			if recovered == nil || !strings.Contains(recovered.Error(), "version 0") {
				t.Fatalf("Json(version 0) panic = %v, want an error naming version 0", recovered)
			}
		}()
		Json(metaV1, JsonColumnOptions{Version: 0})
		t.Fatal("Json(version 0) did not fail")
	})
}

func TestReadingACell(t *testing.T) {
	migrateV2 := func(old delta.JsonValue, from int) (any, error) {
		tag, _ := old.(*delta.JsonObject).Value("tag").(string)
		return delta.JsonObjectOf("tags", tag, "n", float64(from)), nil
	}
	v2 := func() *JsonColumn {
		column, err := NewJsonColumn(metaV2, 2, migrateV2)
		if err != nil {
			t.Fatalf("NewJsonColumn: %v", err)
		}
		return column
	}()

	t.Run("parses a cell of the current version with no check", func(t *testing.T) {
		counts := nexttest.Counted(t, func(ctx context.Context) {
			value, err := v2.Read(`{"tags":"a","n":1}`, 2, "t.meta", ctx)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			nexttest.RequireJSONEqual(t, value, delta.JsonObjectOf("tags", "a", "n", float64(1)))
		})
		if counts.Validations != 0 {
			t.Fatalf("validations = %d, want 0", counts.Validations)
		}
	})

	t.Run("migrates an older cell and checks the result once (R8)", func(t *testing.T) {
		counts := nexttest.Counted(t, func(ctx context.Context) {
			value, err := v2.Read(`{"tag":"x"}`, 1, "t.meta", ctx)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			nexttest.RequireJSONEqual(t, value, delta.JsonObjectOf("tags", "x", "n", float64(1)))
		})
		if counts.Validations != 1 {
			t.Fatalf("validations = %d, want 1", counts.Validations)
		}
	})

	t.Run("refuses a newer cell, a missing or throwing migrate and an invalid result", func(t *testing.T) {
		nexttest.Counted(t, func(ctx context.Context) {
			fails := func(err error, want string) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			}
			_, err := v2.Read("{}", 3, "t.meta", ctx)
			fails(err, "newer than 2")
			without, _ := NewJsonColumn(metaV2, 2, nil)
			_, err = without.Read("{}", 1, "t.meta", ctx)
			fails(err, "no migrate")
			throwing, _ := NewJsonColumn(metaV2, 2, func(delta.JsonValue, int) (any, error) { return nil, errors.New("no") })
			_, err = throwing.Read("{}", 1, "t.meta", ctx)
			fails(err, "threw")
			invalid, _ := NewJsonColumn(metaV2, 2, func(delta.JsonValue, int) (any, error) {
				return delta.JsonObjectOf("tags", float64(1), "n", float64(1)), nil
			})
			_, err = invalid.Read("{}", 1, "t.meta", ctx)
			rejectedUnavailable(t, err)
		})
	})
}

// rejectedUnavailable fails the test unless err is an `unavailable` ChordError.
func rejectedUnavailable(t *testing.T, err error) {
	t.Helper()
	rejected(t, err, data.CodeUnavailable, "")
}
