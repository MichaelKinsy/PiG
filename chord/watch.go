package chord

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/watch.ts (WatchEnd, WatchCall, NativeWatchCall, WatchHandle, Watchable, NativeWatchable).

// WatchEndKind is why a watch ended. A watch ends only for good; a failure its source recovers from is a failed WatchCall
// and the watch stays open.
//
//   - WatchStopped: Stop was called.
//   - WatchAborted: the context the watch was acquired with was aborted.
//   - WatchEnded: the source closed (its owner ended or closed).
//   - WatchListenerError: the listener failed with Error.
//   - WatchFailed: the source failed for good with Error.
type WatchEndKind string

const (
	WatchStopped       WatchEndKind = "stopped"
	WatchAborted       WatchEndKind = "aborted"
	WatchEnded         WatchEndKind = "ended"
	WatchListenerError WatchEndKind = "listener_error"
	WatchFailed        WatchEndKind = "failed"
)

// WatchEnd is the terminal result of one watch, tagged by Kind; Error is set only for listener_error and failed.
type WatchEnd struct {
	Kind  WatchEndKind
	Error error
}

// WatchCallKind tags a WatchCall and a NativeWatchCall.
type WatchCallKind string

const (
	// WatchCallValue is a call with the newest value.
	WatchCallValue WatchCallKind = "value"
	// WatchCallFailed is a call with the error of a failure its source may recover from.
	WatchCallFailed WatchCallKind = "failed"
)

// WatchCall is one call of a JSON watch's listener, and one frame a source hands its Watch, tagged by Kind:
//
//   - WatchCallValue: the newest Value, the Ops that turn the previous value into it, and whether the document the watch
//     follows was retired since the previous value call. A retirement does not end the watch: Value is then what the
//     address shows after it (the source decides; a Durable document keeps the last value the watch was given, with no
//     ops), and a document created there later arrives as [["r", value]], possibly in the same call.
//   - WatchCallFailed: the source cannot compute its value now, with Error; the watch stays open, and the next value
//     arrives as [["r", value]]. WatchHandle.Value keeps the last value.
//
// Plain data: the frame's context is the listener's first argument, never part of the call. Value, Ops and Retired are
// zero in a failed call; Error is nil in a value call.
type WatchCall[T any] struct {
	Kind    WatchCallKind
	Value   T
	Ops     []delta.Op
	Retired bool
	Error   error
}

// NativeWatchCall is one call of a native watch's listener: the new value, or a failure its source may recover from, as in
// WatchCall. Value is zero in a failed call; Error is nil in a value call.
type NativeWatchCall[T any] struct {
	Kind  WatchCallKind
	Value T
	Error error
}

// WatchHandle is a serialized exact-frame observation of an immutable value with bounded pending delivery.
type WatchHandle[T any] interface {
	// Value is the acquisition revision before Start and the latest delivered immutable revision afterward (a failed call keeps it).
	Value() T
	// Start installs the sole asynchronous listener. It never invokes it inline.
	Start(listener func(ctx context.Context, call WatchCall[T]) error)
	// Stop idempotently stops future callbacks and returns this watch's terminal result.
	Stop() (WatchEnd, error)
	// Closed returns a channel closed when the watch terminates; an already-running callback remains caller-owned.
	Closed() <-chan struct{}
	// End returns the terminal result once Closed is closed.
	End() WatchEnd
}

// Watchable is a value that changes and can be watched: documents, derives, members of references. Values are immutable by
// convention (JSON for state); each watch starts at the current value and then gets one frame per change. Watch returns an
// error where Pi's watch rejects.
type Watchable[T any] interface {
	// Value is the current value.
	Value() T
	// Watch returns a watch holding the current value, then one frame per change; aborting ctx ends it.
	Watch(ctx context.Context) (WatchHandle[T], error)
}

// NativeWatchable is an in-process value that changes and can be watched (it may hold anything, errors and functions
// included): a change is a new value by identity, delivered without ops. It is the in-process counterpart of Watchable.
type NativeWatchable[T any] interface {
	// Value is the current value.
	Value() T
	// Watch returns a watch holding the current value, then one frame per new value; aborting ctx ends it. It returns an
	// error where Pi's watch rejects.
	Watch(ctx context.Context) (NativeWatchHandle[T], error)
}
