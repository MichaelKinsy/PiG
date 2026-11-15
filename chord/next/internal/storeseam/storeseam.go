// Package storeseam is the test seam of the store's commit queue: what Pi's tests get by replacing `store.commit`
// (test/host/named-query.test.ts "a provider's restart under its readers" 11a, test/host/query-reader.test.ts "a read
// opened while a commit waits"), which Go cannot do to a method, and which the host's tests cannot reach in package store.
// It is unimportable outside chord/next; the store assigns the function in a non-test file; no production code calls it.
package storeseam

// QueueTail is the commit queue's tail of a `*store.ChordStore` (given as any: the seam imports nothing): the channel
// changes whenever a commit, replay or take takes its place in the queue, so a test that starts a store call on a goroutine
// waits until the tail changed to know the call is queued behind a held step.
var QueueTail func(store any) <-chan struct{}

// Adopt is a commit's adoption as its fence gives it: the seq the commit stored, the ids of its created documents in
// the order of their writes, the table rows its writes changed.
type Adopt = func(seq int64, ids []int64, rows int) error

// WrapAdopt makes a `*store.ChordStore` (given as any) pass the adoption of every commit that takes its place in its commit
// queue through wrap from then on, and returns the function wrap returns in its place: what Pi's tests get by replacing
// `store.commit` and wrapping `fence.adopt` (test/host/tiling.test.ts "the invariant, with two owners and commit groups").
// wrap runs once per commit, in the order the commits take their place (a commit that joins a group not yet started
// included), and may tell a test so; the function it returns runs where the fence's adoption did, between the adoptions of one
// group's commits. A nil wrap restores the store's own.
var WrapAdopt func(store any, wrap func(adopt Adopt) Adopt)

// FailRuns makes the runs of a `*store.RegisteredRead` (given as any) fail from then on: each time the read runs in a commit's
// step, fail is called, and a non-nil error is the run's failure (a failed run, as a read that cannot run gives); a nil error
// lets the read run for real. A nil fail restores the read's own runs. What Pi's tests get by assigning `read.run`
// (test/host/tiling.test.ts, "a failed page ..."): a page of a list's query fails while another page of it runs well, which
// no builder or decoder can make happen.
var FailRuns func(read any, fail func() error)

