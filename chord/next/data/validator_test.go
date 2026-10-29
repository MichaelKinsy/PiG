package data_test

import (
	"context"
	"runtime"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/data/validator.test.ts.
//
// Designed out: "leave nothing in TypeBox's build accumulators" asserts that typebox's module-level GetExternal and
// GetFunctions accumulators are empty after a build. The Go validator compiles through a per-call jsonschema compiler
// and keeps no module-level build state, so there is nothing to reset; "are collected with their schema owner" below
// covers the observable half (nothing retains the owner or its validator).

func argumentsSnapshot(t testing.TB, schema contracts.TSchema, path string) *contracts.Snapshot {
	t.Helper()
	snapshot, err := contracts.SnapshotArguments(schema, path)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

var validatorArgs = func() *contracts.Snapshot {
	schema := contracts.Obj(
		contracts.Prop("text", contracts.Str()),
		contracts.Prop("items", contracts.Arr(contracts.Obj(contracts.Prop("n", contracts.Int())))),
		contracts.Prop("tags", contracts.Map(contracts.Str())),
		contracts.Prop("blob", contracts.Bytes(contracts.Max(2))),
	)
	snapshot, err := contracts.SnapshotArguments(schema, "m.args")
	if err != nil {
		panic(err)
	}
	return snapshot
}()

func validValue() *delta.JsonObject {
	return delta.JsonObjectOf("text", "t", "items", []any{delta.JsonObjectOf("n", 1.0)}, "tags", delta.NewJsonObject(0), "blob", make([]byte, 1))
}

// validWith is `{ ...valid, key: value }`.
func validWith(key string, value any) *delta.JsonObject {
	object := validValue()
	object.Set(key, value)
	return object
}

// validWithout is `valid` without key.
func validWithout(key string) *delta.JsonObject {
	object := validValue()
	object.Delete(key)
	return object
}

type checkFailure struct {
	Reason  data.ChordErrorReason
	Message string
	Details any
}

// failure is the `{ reason, message, details }` of the `invalid/args` error a failed check returns, or nil for a pass.
func failure(t *testing.T, value any) *checkFailure {
	t.Helper()
	validator, err := data.ValidatorFor(validatorArgs, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = data.Check(validator, value, data.ReasonArgs, chordctx.Background())
	if err == nil {
		return nil
	}
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid); ok {
		return &checkFailure{Reason: chordErr.Reason, Message: chordErr.Message, Details: chordErr.Details}
	}
	t.Fatalf("unexpected error %v", err)
	return nil
}

func detailsPath(failed *checkFailure) any {
	if object, ok := failed.Details.(*delta.JsonObject); ok {
		return object.Value("path")
	}
	return nil
}

func requirePath(t *testing.T, value any, path string) {
	t.Helper()
	failed := failure(t, value)
	if failed == nil || detailsPath(failed) != path {
		t.Fatalf("failure = %+v, want path %q", failed, path)
	}
}

func requireMessage(t *testing.T, value any, message string) {
	t.Helper()
	failed := failure(t, value)
	if failed == nil || failed.Message != message {
		t.Fatalf("failure = %+v, want message %q", failed, message)
	}
}

func TestFailedChecks(t *testing.T) {
	t.Run("pass a valid value", func(t *testing.T) {
		if failed := failure(t, validValue()); failed != nil {
			t.Fatalf("failure = %+v", failed)
		}
	})

	t.Run("report TypeBox's first error with its JSON Pointer path", func(t *testing.T) {
		failed := failure(t, validWith("text", 1.0))
		if failed == nil || failed.Reason != data.ReasonArgs || failed.Message != "args/text: must be string" {
			t.Fatalf("failure = %+v", failed)
		}
		nexttest.RequireJSONEqual(t, failed.Details, delta.JsonObjectOf("path", "/text"))
		requirePath(t, validWith("items", []any{delta.JsonObjectOf("n", "x")}), "/items/0/n")
		failed = failure(t, nil)
		if failed == nil || failed.Reason != data.ReasonArgs || failed.Message != "args: must be object" {
			t.Fatalf("failure(null) = %+v", failed)
		}
		nexttest.RequireJSONEqual(t, failed.Details, delta.JsonObjectOf("path", "/"))
	})

	t.Run("report a missing field at its own path (TypeBox reports it at the parent)", func(t *testing.T) {
		requireMessage(t, validWithout("text"), "args/text: missing")
		requirePath(t, validWithout("text"), "/text")
		requirePath(t, validWith("items", []any{delta.NewJsonObject(0)}), "/items/0/n")
	})

	t.Run("report an unknown field at its own path, nested too", func(t *testing.T) {
		failed := failure(t, validWith("extra", 1.0))
		if failed == nil || failed.Reason != data.ReasonArgs || failed.Message != "args/extra: unknown field" {
			t.Fatalf("failure = %+v", failed)
		}
		nexttest.RequireJSONEqual(t, failed.Details, delta.JsonObjectOf("path", "/extra"))
		requirePath(t, validWith("items", []any{delta.JsonObjectOf("n", 1.0, "m", 2.0)}), "/items/0/m")
	})

	t.Run("check bytes by type and length", func(t *testing.T) {
		requireMessage(t, validWith("blob", []any{1.0}), "args/blob: expected bytes of the declared length")
		requirePath(t, validWith("blob", make([]byte, 3)), "/blob")
		requireMessage(t, validWith("blob", "text"), "args/blob: expected bytes of the declared length")
		requireMessage(t, validWith("blob", delta.NewJsonObject(0)), "args/blob: expected bytes of the declared length")
	})

	t.Run("a data object cannot pass as bytes", func(t *testing.T) {
		forged := delta.JsonObjectOf("\x00chord:bytes", 1.0)
		requireMessage(t, validWith("blob", forged), "args/blob: expected bytes of the declared length")
	})

	t.Run("leave keys containing `/` or `~` unescaped (TypeBox's instance path, ambiguous)", func(t *testing.T) {
		requirePath(t, validWith("tags", delta.JsonObjectOf("a/b", 1.0)), "/tags/a/b")
	})
}

func TestValidators(t *testing.T) {
	t.Run("compile once per schema owner (counted), then validate (counted); a failure adds one walk", func(t *testing.T) {
		owner := argumentsSnapshot(t, contracts.Obj(contracts.Prop("a", contracts.Str())), "m")
		check := func(ctx context.Context, value any) error {
			validator, err := data.ValidatorFor(owner, ctx)
			if err != nil {
				t.Fatal(err)
			}
			return data.Check(validator, value, data.ReasonArgs, ctx)
		}
		first := nexttest.Counted(t, func(ctx context.Context) {
			if err := check(ctx, delta.JsonObjectOf("a", "x")); err != nil {
				t.Error(err)
			}
		})
		second := nexttest.Counted(t, func(ctx context.Context) {
			if err := check(ctx, delta.JsonObjectOf("a", "y")); err != nil {
				t.Error(err)
			}
		})
		failed := nexttest.Counted(t, func(ctx context.Context) {
			if err := check(ctx, delta.NewJsonObject(0)); err == nil {
				t.Error("expected a failure")
			}
		})
		want := []nexttest.Counts{{Walks: 0, Validations: 1, Compilations: 1}, {Walks: 0, Validations: 1, Compilations: 0}, {Walks: 1, Validations: 1, Compilations: 0}}
		if got := []nexttest.Counts{first, second, failed}; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("counts = %+v, want %+v", got, want)
		}
	})

	t.Run("are collected with their schema owner", func(t *testing.T) {
		ownerRef, validatorRef := func() (weak.Pointer[contracts.Snapshot], weak.Pointer[data.Validator]) {
			owner := argumentsSnapshot(t, contracts.Obj(contracts.Prop("blob", contracts.Bytes())), "m")
			validator, err := data.ValidatorFor(owner, chordctx.Background())
			if err != nil {
				t.Fatal(err)
			}
			return weak.Make(owner), weak.Make(validator)
		}()
		nexttest.ExpectCollected(t, ownerRef)
		runtime.GC()
		nexttest.ExpectCollected(t, validatorRef)
	})
}
