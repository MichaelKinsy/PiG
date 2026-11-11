package data

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/data/validator.ts: compiled checks (pi-schema validators of Chord-owned schemas) and the
// `invalid` error of a failed check.
//
// Go forced differences:
//   - Upstream's WeakMap keyed by the owner is a cache keyed by weak pointer, evicted when the owner is collected.
//   - Upstream's `validator.check(value)` is Validator.Check, which returns a bool: pi-schema's Check returns an error
//     only for a `$dynamicRef` target that is malformed or names nothing, and Chord's snapshot vocabulary has no
//     `$dynamicRef`, so the error cannot occur and a snapshot's schema never reaches it.

// SchemaOwner is a Chord-owned record holding a schema: a snapshot, or a contract's params.
type SchemaOwner interface {
	*contracts.Snapshot | *contracts.ParamsData
}

// Validator is the compiled check of one schema: pi-schema's validator.
type Validator struct {
	compiled *schema.Validator
}

// Check reports whether value matches the schema. The error is the one pi-schema raises where its `check` throws: a
// part of the schema compiled on first use that is malformed or names nothing (`$dynamicRef`).
func (validator *Validator) Check(value any) (bool, error) {
	return validator.compiled.Check(value)
}

var validators struct {
	sync.Mutex
	byOwner map[any]*Validator
}

// ValidatorFor is the validator of an owner's schema, compiled on first use (counted) and cached weakly by the owner:
// the owner's collection evicts it. A schema pi-schema cannot compile is an error.
func ValidatorFor[O SchemaOwner](owner O, ctx context.Context) (*Validator, error) {
	switch typed := any(owner).(type) {
	case *contracts.Snapshot:
		return validatorFor(typed, typed.Schema, ctx)
	case *contracts.ParamsData:
		return validatorFor(typed, typed.Schema, ctx)
	}
	panic("unreachable: SchemaOwner has two types")
}

func validatorFor[T any](owner *T, definition contracts.Schema, ctx context.Context) (*Validator, error) {
	key := weak.Make(owner)
	validators.Lock()
	defer validators.Unlock()
	if validator, ok := validators.byOwner[key]; ok {
		return validator, nil
	}
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.compilations", telemetry.InstrumentOptions{}).Add(1, nil)
	// `format` is an annotation (decided for the schema switch): no format checks are compiled in.
	compiled, err := schema.Compile(definition.Node, schema.CompileOptions{})
	if err != nil {
		return nil, err
	}
	validator := &Validator{compiled: compiled}
	if validators.byOwner == nil {
		validators.byOwner = map[any]*Validator{}
	}
	validators.byOwner[key] = validator
	runtime.AddCleanup(owner, func(key weak.Pointer[T]) {
		validators.Lock()
		delete(validators.byOwner, key)
		validators.Unlock()
	}, key)
	return validator, nil
}

// Check checks value (counted); a failure returns `invalid/<reason>` with the first error's JSON Pointer path, as
// upstream throws it. reason is ReasonArgs, ReasonParams or ReasonHostAddress.
func Check(validator *Validator, value any, reason ChordErrorReason, ctx context.Context) error {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.validations", telemetry.InstrumentOptions{}).Add(1, nil)
	ok, err := validator.Check(value)
	if err != nil {
		return err
	}
	if !ok {
		return failedCheck(validator, value, reason, ctx)
	}
	return nil
}

// failedCheck is the error of a failed check: the first error of the validator's explanation (counted: it walks the
// schema). A missing field is reported at the field ("missing"), an unknown one at its own path ("unknown field"); the
// value itself at `/`. A path is a JSON Pointer, `~` and `/` in keys escaped as `~0` and `~1`.
func failedCheck(validator *Validator, value any, reason ChordErrorReason, ctx context.Context) error {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.walks", telemetry.InstrumentOptions{}).Add(1, nil)
	explained, err := validator.compiled.Errors(value)
	if err != nil {
		// The explanation raises where pi-schema's `errors` throws: a subschema the check never compiled.
		return err
	}
	if len(explained) == 0 {
		// A value that failed the check has at least one error.
		return fmt.Errorf("data: a failed check explained nothing")
	}
	path := explained[0].Path
	if path == "" {
		path = "/"
	}
	suffix := path
	if path == "/" {
		suffix = ""
	}
	return Invalid(reason, fmt.Sprintf("%s%s: %s", reason, suffix, explained[0].Message), delta.JsonObjectOf("path", path))
}
