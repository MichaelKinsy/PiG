// Package state ports packages/chord/src/next/state: the reactive state of Chord v2. A host owns one Batch, which runs
// each commit's adoptions, then delivers one frame for every watched value that changed; Sources (JSON) and
// NativeSources (in-process values, FollowedSource and WritableSource) are what watches and derives read; Derives are
// values computed from them, recomputed on read when a commit marked them dirty.
//
// # Execution model
//
// Pi runs the whole of this package on one event loop: a Batch, a Source and a Derive are plain objects whose methods
// never run concurrently, and an `await` or `queueMicrotask` boundary is the only place another task interleaves. The
// Go port keeps that contract instead of locking every field: the host that owns a Batch serializes every call into
// the Batch and the sources, derives and writables built on it (the Go form of the event loop), and gives the Batch the
// queue Soon needs (Batch.UseMicrotasks). Only what a watch's own goroutines reach is safe for concurrent use: a watch's
// Stop, abort or failing listener ends it from a delivery goroutine, which removes the watch from its source (the source
// guards its watch, subscriber and follower collections) and DependentSet is safe too.
//
// # Promises
//
// `watch(context): Promise<Watch>` is an awaited call that never suspends in Pi (it resolves with openWatch's result,
// and a throw is a rejection): the Go form is a blocking Watch(ctx) (handle, error). Watch deliveries run on the
// chord.DeliveryScheduler the Batch was given (Batch.UseScheduler), the Go form of the host's microtask queue.
//
// # Subclassing
//
// Pi's Source, NativeSource and FollowedSource are abstract classes with virtual members. Go embeds the base struct by
// value and Init gives it the embedder as its delegate; a member the embedder defines itself (Start, Current, Watched,
// Unfollowed, OpenWatch) is the override, and the embedder reaches the base member through the embedded field.
// Type-level only items of Pi (the `Writable` and `Watchable` conditional signatures, `const T` inference) are carried
// by Go generics.
//
// # Forced differences
//
//   - `Constant` is both Pi's class and the `constant()` function: the class is Constant (NewConstant) and the typed
//     function of the app-facing index is ConstantOf.
//   - A throwing `value` getter is a (value, error) pair: Source.Value, Derive.Value and Constant.Value return it, so
//     they are not chord.Watchable (whose Value is total); a typed handle layer adapts them.
//   - `undefined` JSON values (WatchStart.Last, NativeStart.Last) are a HasLast presence boolean.
//   - An `adopt`, `flush` or `Deliver` that throws in Pi returns an error here.
//   - `handedOut` makes a V8 stack text before an app keeps the error; Go errors keep no receivers, so HandedOut is the
//     identity.
package state
