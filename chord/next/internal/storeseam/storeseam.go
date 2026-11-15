// Package storeseam is the test seams of the store: of its commit queue, what Pi's tests get by replacing `store.commit`
// (test/host/named-query.test.ts "a provider's restart under its readers" 11a, test/host/query-reader.test.ts "a read
// opened while a commit waits"), which Go cannot do to a method, and which the host's tests cannot reach in package store; and
// of the reloads a changed binding starts, which Pi's tests wait for with `await flushed()`. It is unimportable outside
// chord/next; the store assigns the functions in a non-test file; no production code calls them.
package storeseam

// QueueTail is the commit queue's tail of a `*store.ChordStore` (given as any: the seam imports nothing): the channel
// changes whenever a commit, replay or take takes its place in the queue, so a test that starts a store call on a goroutine
// waits until the tail changed to know the call is queued behind a held step.
var QueueTail func(store any) <-chan struct{}

// ReadTasks is a barrier on the reloads a changed binding of a `*store.ChordStore` started (the store's reads index owns them,
// Pi's unawaited `void open.load()`): the channel (the store given as any) is closed once the tasks running now ended, and
// already closed when none runs. A test that changes a binding joins the read it started through it, as Pi's `await flushed()`
// does, with no polling.
var ReadTasks func(store any) <-chan struct{}
