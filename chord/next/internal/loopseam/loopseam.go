// Package loopseam is the test seam of the host's event loop: what Pi's tests get from `delivered()` (test/support.ts:138, a
// `setTimeout(0)`: it resolves after every microtask chain queued before it has run), which Go cannot get from the loop's own
// types without exporting a method on them. It is unimportable outside chord/next; the host assigns the function in a non-test
// file; no production code calls it.
package loopseam

// Quiesce blocks until the host loop (given as any: the seam imports nothing; a loop that is not the host's returns at once)
// has no joined hop in flight: no goroutine the loop started for a microtask queued from outside a section is still to run it.
// What Pi runs as the next microtask of a chain, Go runs there.
var Quiesce func(loop any)
