// Package loopseam is the test seam of the host's event loop: what Pi's tests get from `delivered()` (test/support.ts:138, a
// `setTimeout(0)`: it resolves after every microtask chain queued before it has run, and not after one still pending on
// something outside the host), which Go cannot get from the loop's own types without exporting a method on them. It is
// unimportable outside chord/next; the host assigns the function in a non-test file; no production code calls it.
package loopseam

// Quiesce blocks until the host loop (given as any: the seam imports nothing; a loop that is not the host's returns at once)
// has nothing to run that Pi would run before a timer: no joined hop in flight (a goroutine the loop started to run a
// microtask queued from outside a section), and no task running (awaitTask: the goroutine of an `await work()` of work that
// blocks, up to the end of the section that resumes its promise). A task that waits for something the loop does not run (a
// gate a test resolves: chordctx.Receive and Await; a native's work still running; a timer) is not waited for, and is
// waited for again as soon as its wait is over, a gate released just before the call included: Pi's `resolve()` queues the
// continuation at once. A task that blocks any other way is a misuse of the host's contract (DESIGN-go-layout.md section 10):
// the join reports it with the task's function and stack after a generous bound, which is only a fault detector.
var Quiesce func(loop any)
