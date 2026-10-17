package host

// Ports packages/chord/src/types.ts (Pi 1.2.0): the caller identity, the call context of one root, the readable state
// source and its delta events, and a reference's status, shape and base members. Exposed is object.go's Exposed (the
// value self.Expose returns); StateSource is type-level only and has no Go form.

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Principal is the app-defined, JSON-serializable identity of a caller, mapped to an object key by
// HostOptions.PrincipalKey. Pi's principal type is a parameter (`createHost<P>`, default `unknown`); the app supplies
// every principal and Chord makes none up.
type Principal = any

// GetOptions are the options of a get (types.ts:7-12).
type GetOptions struct {
	// Principal the object is created for. HasPrincipal false is Pi's undefined (the host's own principal); true with a
	// nil Principal is an explicit null, which host.ts `??` also maps to the host's own principal but acquireRemote
	// sends on.
	Principal    Principal
	HasPrincipal bool
	// State is false for a method-only reference (types.ts:11-15): it receives no state unless another reference to
	// the same object wants it, and is ready once its object is live. Nil is true.
	State *bool
	// Signal aborts an awaited Host.Get (Pi's `options.signal`; Ctx.Get ignores it).
	Signal context.Context
}

// Ctx is the explicit call context of one root, a short Chord call tree (types.ts:14-24). Calls made with it carry
// its root id and code version; Get holds are released when the call that received this ctx settles.
type Ctx interface {
	// Signal is the root's AbortSignal: its Done channel closes when the root is aborted.
	Signal() context.Context
	RootID() string
	CodeVersion() int
	Principal() Principal
	// Get holds the object of contract typ for input; options nil is Pi's undefined. It returns once the reference is ready:
	// live, with every state member of its variant (unless State is false). It fails with the activation error, aborted
	// when the ctx is aborted or the call settles first, or unavailable/ready_timeout after the host's ReadyTimeout; the
	// reference is released then.
	Get(typ *Contract, input any, options *GetOptions) (Ref, error)
}

// StateEventType is the discriminator of a StateEvent (types.ts:26-29): the closed union "snap" | "ops" | "reset".
type StateEventType string

// StateEvent types (types.ts:26-29).
const (
	StateEventSnap  StateEventType = "snap"
	StateEventOps   StateEventType = "ops"
	StateEventReset StateEventType = "reset"
)

// StateEvent is one event of a delta stream (types.ts:26-29): a snapshot, ops with the value after them, or a reset
// after a migration or rebind.
type StateEvent[T any] struct {
	// Type is StateEventSnap, StateEventOps or StateEventReset.
	Type  StateEventType
	Value T
	// Ops are set for StateEventOps only.
	Ops []delta.Op
}

// Readable is a readable state source: a cell, a derivation, or a reference's state member (types.ts:31-38). Values are
// immutable by contract.
type Readable[T any] interface {
	Value() T
	// Subscribe calls listener with the current value immediately, then with every new value.
	Subscribe(listener func(value T)) func()
	// Events is the delta stream: a snapshot immediately, then ops, or a reset after a migration or rebind.
	Events(listener func(event StateEvent[T])) func()
}

// RefStatus is a reference's status (types.ts:53).
type RefStatus string

// RefStatus values (types.ts:53).
const (
	RefLoading  RefStatus = "loading"
	RefLive     RefStatus = "live"
	RefReleased RefStatus = "released"
	RefGone     RefStatus = "gone"
)

// Shape is the provider's shape for a reference (types.ts:55-61).
type Shape struct {
	// Variant is the variant; nil is Pi's undefined.
	Variant *string
	Members []string
	// Contract is the fingerprint of the provider contract.
	Contract string
}

// RefBase is the members every reference has (types.ts:63-79). Ref (contract.ts) adds the contract's members.
type RefBase interface {
	// Contract is the consumer contract token this reference was bound with.
	Contract() *Contract
	Status() RefStatus
	// Ready blocks until Pi's ready Promise settles; its rejection is the error.
	Ready(ctx context.Context) error
	// Variant is the variant; nil is Pi's undefined.
	Variant() *string
	// Shape is nil (Pi's undefined) until the provider's shape is known.
	Shape() *Shape
	// Err is why the reference is gone, if it is: Pi's error member, which Go's error interface keeps from being Error.
	Err() error
	OnShape(listener func(shape Shape)) func()
	// OnStatus is called after every status change (loading → live, live → loading while a link reconnects, released,
	// gone with err). A UI watches for gone to get again.
	OnStatus(listener func(status RefStatus, err error)) func()
	Release()
}
