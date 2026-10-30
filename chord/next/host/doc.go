// Package host ports packages/chord/src/next/host, jobs and remote: the host of Chord v2, its objects and references, the
// jobs they run and the links they hold. Pi's three directories import one another, so they are one Go package (see
// chord/next/DESIGN-go-layout.md); the files carry the directory in their names (host_*.go, jobs_*.go, remote_*.go) and
// Pi's `internal.ts` barrels are the package's unexported identifiers.
//
// # Execution model
//
// Pi runs the host on one event loop. Loop is its Go form: Run is one synchronous section (inline when the caller is
// already on the loop), Queue is queueMicrotask, AfterFunc is setTimeout and Go starts an owned background task. The
// helpers take the narrow eventLoop interface, so tests drive virtual time through a fake loop as Pi's tests use fake
// timers. An awaited Promise of Pi is a blocking call returning (value, error); an AbortSignal is a context.Context.
package host
