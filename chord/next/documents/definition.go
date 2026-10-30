package documents

import (
	"context"
	"fmt"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/documents/definition.ts
//
// Document definitions: `defineState(name, ...)`, its fingerprint, the one check of a definition's `initial()` (R8),
// the checked `migrate` of an older value and, for a persisted document, its checkpoint rule (slice 6a §10).
//
// Go forced differences: the TypeScript overloads that require `checkpointWhen` with `persist: true` and forbid it
// otherwise are type-level (designed out; the Go StateOptions is one struct and the declaring attempt checks the
// store, P3); `DocumentOf<S>` is *JsonObject; the validator is the one of the definition's schema snapshot.

// JsonObject is a document root: a JSON object.
type JsonObject = delta.JsonObject

// CommitKind is when a commit reaches the store.
type CommitKind string

// The commit kinds.
const (
	// CommitImmediate stores before the commit resolves (the default).
	CommitImmediate CommitKind = "immediate"
	// CommitDeferred adopts and publishes at once and stores with the next immediate change, the run's outcome, the
	// object's close, or about WithinMs later.
	CommitDeferred CommitKind = "deferred"
)

// CommitPersistence is when a commit reaches the store (Durable's `CommitPersistence`): `immediate`, the default,
// before the commit resolves; `deferred` (a persisted run's state change, D8): adopted and published at once, stored
// with the next immediate change, the run's outcome, the object's close, or about `WithinMs` (default 1,000 ms) later.
type CommitPersistence struct {
	Kind CommitKind
	// WithinMs is the deferral's bound; nil is the default.
	WithinMs *float64
}

// Immediate is the default CommitPersistence.
var Immediate = CommitPersistence{Kind: CommitImmediate}

// CheckpointInfo is what a persisted document's `checkpointWhen` learns of its stored revisions.
type CheckpointInfo struct {
	// DeltasSinceBase is the deltas stored after its newest base, without the change being decided.
	DeltasSinceBase int
}

// CheckpointWhen reports whether a commit's change of a persisted document is stored as a whole new base (true) or as
// a delta.
type CheckpointWhen func(value *JsonObject, ops []delta.Op, info CheckpointInfo) bool

// StateOptions is what DefineState takes.
type StateOptions struct {
	Schema  contracts.TSchema
	Version int
	// Initial is checked against the schema once, when the declaring definition is first used.
	Initial func() *JsonObject
	// Migrate maps a value of an older version `from` to this version's shape; pure. Required to read older values.
	Migrate func(old *JsonObject, from int) (*JsonObject, error)
	Keyed   bool
	// Persist stores the document in the host's store; only in `local` types with empty params (P3).
	Persist bool
	// CheckpointWhen is called once per commit per changed document; creations and version changes always store a base.
	CheckpointWhen CheckpointWhen
}

// DefineState declares a document: a singleton per object, or with Keyed many per object, each by a string key; with
// Persist stored in the host's store. Its name is a restricted name (lowercase `[a-z0-9@/._~-]`).
func DefineState(name string, options StateOptions) (*StateDefinition, error) {
	if err := contracts.CheckName(name, "A state name"); err != nil {
		return nil, err
	}
	return newStateDefinition(name, options)
}

func neverCheckpoint(*JsonObject, []delta.Op, CheckpointInfo) bool { return false }

// StateDefinition is a declared document.
type StateDefinition struct {
	Name    string
	Schema  contracts.TSchema
	Version int
	Keyed   bool
	Persist bool
	// Fingerprint is `hash64` of the schema's normal form (descriptions and titles ignored), computed once: the same
	// version with another fingerprint is a schema changed without a version bump.
	Fingerprint string
	snapshot    *contracts.Snapshot
	initial     func() *JsonObject
	migrate     func(old *JsonObject, from int) (*JsonObject, error)
	checkpoint  CheckpointWhen
	checked     bool
}

func newStateDefinition(name string, options StateOptions) (*StateDefinition, error) {
	snapshot, err := contracts.SnapshotSchema(options.Schema, name)
	if err != nil {
		return nil, err
	}
	fingerprint, err := contracts.FingerprintOf(snapshot.Normal)
	if err != nil {
		return nil, err
	}
	checkpoint := options.CheckpointWhen
	if checkpoint == nil {
		checkpoint = neverCheckpoint
	}
	return &StateDefinition{
		Name: name, Schema: options.Schema, Version: options.Version, Keyed: options.Keyed, Persist: options.Persist,
		Fingerprint: fingerprint, snapshot: snapshot, initial: options.Initial, migrate: options.Migrate,
		checkpoint: checkpoint,
	}, nil
}

// CheckpointWhen reports whether a persisted document's change is stored as a new base (its `checkpointWhen`); never
// asked in memory.
func (definition *StateDefinition) CheckpointWhen(value *JsonObject, ops []delta.Op, info CheckpointInfo) bool {
	return definition.checkpoint(value, ops, info)
}

// Initial is a fresh `initial()` value; the first is checked against the schema (counted), later ones are not (R8).
func (definition *StateDefinition) Initial(ctx context.Context) (*JsonObject, error) {
	value := definition.initial()
	if definition.checked {
		return value, nil
	}
	if err := definition.check(value, "initial()", ctx); err != nil {
		return nil, err
	}
	definition.checked = true
	return value, nil
}

// Migrate is old, a value of version `from`, migrated to this version and checked against the schema once, as it is
// produced (R8, counted). It fails without a `migrate` (the access is refused, the data kept) and when the result does
// not match.
func (definition *StateDefinition) Migrate(old *JsonObject, from int, ctx context.Context) (*JsonObject, error) {
	if definition.migrate == nil {
		return nil, fmt.Errorf("%s: no migrate from version %d to %d", definition.Name, from, definition.Version)
	}
	value, err := definition.migrate(old, from)
	if err != nil {
		return nil, err
	}
	if err := definition.check(value, fmt.Sprintf("migrate() from version %d", from), ctx); err != nil {
		return nil, err
	}
	return value, nil
}

func (definition *StateDefinition) check(value *JsonObject, what string, ctx context.Context) error {
	if metrics := chordctx.Telemetry(ctx).Metrics(); metrics != nil {
		metrics.Counter("chord.data.validations", telemetry.InstrumentOptions{}).Add(1, nil)
	}
	validator, err := data.ValidatorFor(definition.snapshot, ctx)
	if err != nil {
		return err
	}
	if !validator.Check(value) {
		return fmt.Errorf("%s: %s does not match the schema", definition.Name, what)
	}
	return nil
}
