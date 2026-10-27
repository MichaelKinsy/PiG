package chord

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/watch.ts (WatchEnd, WatchChange, NativeWatchChange, WatchHandle, Watchable, NativeWatchable).

// WatchEndReason is why a watch ended.
//
//   - WatchStopped: Stop was called.
//   - WatchCancelled: the context the watch was acquired with was cancelled.
//   - WatchEnded: the source closed (its owner ended or closed).
//   - WatchListenerError: the listener failed with Error.
//   - WatchFailed: the source failed with Error.
type WatchEndReason string

const (
	WatchStopped       WatchEndReason = "stopped"
	WatchCancelled     WatchEndReason = "cancelled"
	WatchEnded         WatchEndReason = "ended"
	WatchListenerError WatchEndReason = "listener_error"
	WatchFailed        WatchEndReason = "failed"
)

// WatchEnd is the terminal result of one watch; Error is set only for listener_error and failed.
type WatchEnd struct {
	Reason WatchEndReason
	Error  error
}

// WatchChange is one call of a JSON watch's listener, and one change a source hands its Watch: the newest value, the ops
// that turn the previous value into it, and whether the document the watch follows was retired since the previous call. A
// retirement does not end the watch: Value is then what the address shows after it (the source decides; a Durable document
// keeps the last value the watch was given, with no ops), and a document created there later arrives as [["r", value]],
// possibly in the same call. It is plain data: the frame's context is the listener's first argument, never part of the
// change.
type WatchChange[T any] struct {
	Value   T
	Ops     []delta.Op
	Retired bool
}

// NativeWatchChange is one call of a native watch's listener: the new value.
type NativeWatchChange[T any] struct {
	Value T
}

// WatchHandle is a serialized exact-frame observation of an immutable value with bounded pending delivery.
type WatchHandle[T any] interface {
	// Value is the acquisition revision before Start and the latest delivered immutable revision afterward.
	Value() T
	// Start installs the sole asynchronous listener. It never invokes it inline.
	Start(listener func(ctx context.Context, change WatchChange[T]) error)
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
	// Watch returns a watch holding the current value, then one frame per change; cancelling ctx ends it.
	Watch(ctx context.Context) (WatchHandle[T], error)
}

// NativeWatchable is an in-process value that changes and can be watched (it may hold anything, errors and functions
// included): a change is a new value by identity, delivered without ops. It is the in-process counterpart of Watchable.
type NativeWatchable[T any] interface {
	// Value is the current value.
	Value() T
	// Watch returns a watch holding the current value, then one frame per new value; cancelling ctx ends it. It returns an
	// error where Pi's watch rejects.
	Watch(ctx context.Context) (NativeWatchHandle[T], error)
}
