// Package admitseam is the test seam of an attempt's admission hop: what Pi's tests get from the order of microtasks (a
// `set` made in the turn of a reload lands before the microtask that runs the new attempt's admission, test/host/
// admission.test.ts "a denial between staging and activation"), which Go has no way to say: a transaction of a source
// takes its place on a line and lands on a later hop than the host's own microtask. It is unimportable outside chord/next;
// the host assigns the function in a non-test file; no production code calls it.
package admitseam

// Hold is set by the host package. Hold(host, hold) makes the host (a Host of chord/next/host, given as any: the seam imports
// nothing) hand the hop that starts each attempt's admission (Pi's `await undefined` before `#runAdmission`) to hold instead
// of queueing it: hold is called in the section that starts the attempt, must not block, and keeps start; the admission
// begins once start is called, from any goroutine (it queues the hop as the host would have). Hold(host, nil) ends the
// seam: later attempts queue their hop as before; a start kept earlier still works.
var Hold func(host any, hold func(start func()))
