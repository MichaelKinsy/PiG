package chord

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/watch.ts (WatchEnd, WatchHandle, Watchable, NativeWatchable).

// WatchEndReason is why a watch ended.
//
//   - WatchStopped: Stop was called.
//   - WatchCancelled: the context the watch was acquired with was cancelled.
//   - WatchRetired: the source was retired; its last frame was [["r", null]].
//   - WatchEnded: the source closed (its owner ended or closed).
//   - WatchListenerError: the listener failed with Error.
//   - WatchFailed: the source failed with Error.
type WatchEndReason string

const (
	WatchStopped       WatchEndReason = "stopped"
	WatchCancelled     WatchEndReason = "cancelled"
	WatchRetired       WatchEndReason = "retired"
	WatchEnded         WatchEndReason = "ended"
	WatchListenerError WatchEndReason = "listener_error"
	WatchFailed        WatchEndReason = "failed"
)

// WatchEnd is the terminal result of one watch; Error is set only for listener_error and failed.
type WatchEnd struct {
	Reason WatchEndReason
	Error  error
}

// WatchHandle is a serialized exact-frame observation of an immutable value with bounded pending delivery.
type WatchHandle[T any] interface {
	// Value is the acquisition revision before Start and the latest delivered immutable revision afterward.
	Value() T
	// Start installs the sole asynchronous listener. It never invokes it inline.
	Start(listener func(ctx context.Context, value T, ops []delta.Op) error)
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
