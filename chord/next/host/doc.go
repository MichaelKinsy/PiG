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
//
// # Writing a method handler
//
// Pi runs an async method handler synchronously up to its first `await`, inside the section that admits its call. A
// contracts.MethodFunction is blocking code on a goroutine of its own, and the section waits for it until it returns or
// suspends (Loop.Lend): the handler's calls into the host (Reference.Release, the admission of Reference.Call, document
// reads, Ctx.Get's first part) run inline in that section, and everything it does after it suspended runs outside it.
//
// A handler suspends where it first waits for something that is not a section of the host: the host's blocking primitives
// do it themselves when they are given the handler's ctx (or a context derived from it): Ctx.Get, Reference.Call and the
// waits of reference members, Sleep, UntilAborted, transactions and document loads, Job.Abort, Run.Outcome. To wait for
// anything else, use chordctx.Await or chordctx.Receive (Pi's awaitWithContext: they end at the call's abort), or call
// chordctx.Suspend(ctx) and then wait.
//
// A raw channel receive, a lock or a condition before the handler's first host call is a synchronous block of the section,
// as a synchronous loop is in Pi: the host's other work does not run until it ends, so a handler that waits for something
// that needs the host never gets it:
//
//	"wait": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
//		<-gate // gate is opened by another call to this host: the section never ends
//		return nil, nil
//	},
//
// Write chordctx.Suspend(ctx) before the receive, or chordctx.Receive(ctx, gate). A goroutine the handler starts is not
// part of its synchronous part; pass it the handler's ctx. Built with -tags chordstall, a section that waits for its handler
// longer than parking.StallAfter prints the stacks of every goroutine once and goes on waiting.
package host
