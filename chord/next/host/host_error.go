package host

import (
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/host.ts (Position, HostError, GetOptions): the data types of the host that its helper
// files name. D5b declares them ahead of the host core (DESIGN-go-layout.md section 5); the host port does not redeclare
// them. Pi's `HostOptions`, `LinkOptions`, `Host` and `createHost` are the host core's.

// PositionKind is the kind of part a Position names.
type PositionKind string

const (
	// PositionOn is a routine declared with `on`.
	PositionOn PositionKind = "on"
	// PositionOnLive is a routine declared with `onLive`.
	PositionOnLive PositionKind = "onLive"
	// PositionNative is a native opened with `open`.
	PositionNative PositionKind = "native"
)

// Position is where a reported part sits on its object: a routine or a native, by kind, from 1 in declaration order.
type Position struct {
	Kind     PositionKind
	Position int
}

// HostErrorKind tags a HostError. The kinds of state.BatchError and registries.RegistryError are the ones those packages
// declare.
type HostErrorKind string

const (
	// ErrActivationFailed: an object's activation failed (Object, Failure).
	ErrActivationFailed HostErrorKind = "activation_failed"
	// ErrObjectFailed: a live object failed at runtime (Object, Failure): a `migrate` failing on access, a stored version
	// newer than the code.
	ErrObjectFailed HostErrorKind = "object_failed"
	// ErrStorageFailed: the host's store stopped after a backend error (Error): the host closes itself, once.
	ErrStorageFailed HostErrorKind = "storage_failed"
	// ErrCloseFailed: a native's close threw (Object, Native, Error).
	ErrCloseFailed HostErrorKind = "close_failed"
	// ErrCloseTimeout: a native's close took longer than its closeTimeout (Object, Native).
	ErrCloseTimeout HostErrorKind = "close_timeout"
	// ErrDrainTimeout: an attempt's drain took longer than its drainTimeout (Object).
	ErrDrainTimeout HostErrorKind = "drain_timeout"
	// ErrProtocolError: a link's peer broke the protocol (Error).
	ErrProtocolError HostErrorKind = "protocol_error"
	// ErrResolveFailed: a route's `resolve` rejected (Address, Error).
	ErrResolveFailed HostErrorKind = "resolve_failed"
	// ErrDeriveFailed: a derive's compute threw (Object, Member when it is an exposed state member, Error), once per
	// failure episode.
	ErrDeriveFailed HostErrorKind = "derive_failed"
	// ErrRoutineFailed: a routine's call threw, or its source failed for good (Object, Routine, Error).
	ErrRoutineFailed HostErrorKind = "routine_failed"
	// ErrJobFailed: a job's `run` threw, not after an abort (Object, Job, ID, Error): its outcome is `failed { threw }`.
	ErrJobFailed HostErrorKind = "job_failed"
	// ErrJobFinishFailed: a job's `finish` threw (Object, Job, ID, Error): retried with backoff, blocked after five
	// failures in a row.
	ErrJobFinishFailed HostErrorKind = "job_finish_failed"
	// ErrJobInterrupted: a persisted run was cut off five times in a row by crashes (Object, Job, ID, Restarts).
	ErrJobInterrupted HostErrorKind = "job_interrupted"
	// ErrJobDropped: a blocked run was dropped without `finish`: aborted, or its object ended (Object, Job, ID).
	ErrJobDropped HostErrorKind = "job_dropped"
	// ErrParkedLong: an attempt parked `readyTimeout` (since Since, Date.now() time) while WaitingFor, a remote attempt, is
	// only loading: once per parking episode (Object, WaitingFor, Since).
	ErrParkedLong HostErrorKind = "parked_long"
)

// HostError is what `onError` gets, by Kind (Pi's tagged union, one struct as registries.RegistryError is). Object is the
// object's type, with its params key when it has params. Only the fields the Kind lists are set; Batch and Registry are
// set for their kinds, which Kind then repeats.
type HostError struct {
	Kind       HostErrorKind
	Object     string
	Failure    data.FailureCause
	Error      error
	Native     Position
	Routine    Position
	Address    string
	Member     string
	Job        string
	ID         string
	Restarts   int
	WaitingFor string
	Since      int64
	Batch      *state.BatchError
	Registry   *registries.RegistryError
}

// BatchHostError is a batch's report as a HostError.
func BatchHostError(err state.BatchError) HostError {
	return HostError{Kind: HostErrorKind(err.Kind), Error: err.Error, Batch: &err}
}

// RegistryHostError is a registry's report as a HostError.
func RegistryHostError(err registries.RegistryError) HostError {
	return HostError{Kind: HostErrorKind(err.Kind), Error: err.Error, Registry: &err}
}

// GetOptions is what `host.get` and `host.hold` take besides the type, the params and the context.
type GetOptions struct {
	// ReadyTimeout is a getter's deadline for its whole wait, and how long a call made while the reference is loading waits
	// (the park deadline), in milliseconds; nil is the host's `readyTimeout`.
	ReadyTimeout *float64
}
