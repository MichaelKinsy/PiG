// Package storeseam is the test seam of the store's commit queue: what Pi's tests get by replacing `store.commit`
// (test/host/named-query.test.ts "a provider's restart under its readers" 11a, test/host/query-reader.test.ts "a read
// opened while a commit waits"), which Go cannot do to a method, and which the host's tests cannot reach in package store.
// It is unimportable outside chord/next; the store assigns the function in a non-test file; no production code calls it.
package storeseam

// QueueTail is the commit queue's tail of a `*store.ChordStore` (given as any: the seam imports nothing): the channel
// changes whenever a commit, replay or take takes its place in the queue, so a test that starts a store call on a goroutine
// waits until the tail changed to know the call is queued behind a held step.
var QueueTail func(store any) <-chan struct{}
