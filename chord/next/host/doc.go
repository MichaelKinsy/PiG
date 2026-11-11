// Package host ports packages/chord/src/host, jobs and remote: the host of Chord v2, its objects and references, the
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
// # Plugins, load and reload
//
// A plugin is a HostFacet: its name (an npm package name, IsPluginName) and its Facet. Host.Load stages every facet's
// setup per plugin: a plugin whose setup fails or is refused at declaration (an invalid or repeated name, a type or registry
// an earlier plugin serves, a remote type on a host without principal or address, a broken accepts table) is left out and
// reported in LoadReport.Failed; the others publish in one batch, and Load never closes the host.
//
// Host.Reload takes the complete new plugin set. A facet that is the very value (DefineFacet's identity) the plugin was
// loaded with is unchanged and untouched; another one for a loaded name is a changed plugin, a loaded name missing from the set
// a removed one, a new name an added one. Staging runs synchronously: every new setup, and the new factory of every object
// of a changed or removed plugin against a recording `self` (every declaration check an attempt makes, no effect: no native
// opens, no reference binds, no document is made). A plugin whose new code fails there keeps its old code (an added one is left
// out) and staging runs again without it; then a changed plugin whose recorded requests would not bind because of such a
// plugin keeps its old code too (the closure rule), until nothing more fails. ReloadOptions.Failed names plugins whose new
// code failed before staging (the caller passes their loaded facet): they count for the closure rule and the report. The
// commit is one batch (types, registries, contributions; a publication wakes the requests waiting for the names it serves);
// then each object takes its fate (swap in place, restart after its drain, retry, or end), and Reload returns the
// ReloadReport once every fate started. Reloads run one after another. A context aborted before the commit fails `aborted`
// and changes nothing; after it, it only ends the wait (`aborted`, details.committed: CommittedReload).
//
// # Writing a method handler
//
// Pi runs an async method handler synchronously up to its first `await`, inside the section that admits its call. A
// contracts.MethodFunction is blocking code that runs as a coroutine of that section (Loop.Lend), which waits for it until
// it returns or suspends: the handler's calls into the host (Reference.Release, the admission of Reference.Call, document
// reads, Ctx.Get's first part) run inline in that section, and everything it does after it suspended runs after the section
// is over.
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
// part of its synchronous part; pass it the handler's ctx. What such a goroutine waits for (the host's waits, Await,
// Receive, Suspend) does not end the handler's synchronous part: only the handler's own next wait does, as an `await` inside
// work that Pi's handler started and did not await does not end the handler's.
//
// The package's own tests install parking.Watch in TestMain: a section that waits for its handler longer than 2 s reports
// the stacks of every goroutine and fails the test binary, so a raw block before the first host call is visible there;
// production builds start no timer.
//
// A handler that ends its goroutine (runtime.Goexit: testing.T.FailNow, t.Fatal) instead of returning is a handler that
// throws: its call is rejected and the host goes on, as in Pi; the Goexit ends the handler's goroutine, never the caller's.
//
// # Lock order
//
// Pi has one event loop, so no code waits for another. The host's loop is a lock in Go: every section holds it, so a
// caller that holds another loop (a component's loop.Run, any mutex) while it enters a host section, and code inside a
// host section that enters that other loop, are an AB/BA deadlock. Nothing in Pi's code forbids it, so the rule is stated
// here. RouteHandle.Retry and RouteHandle.Suspend enter the host's loop and run the app's RouteOptions.Resolve inline, on
// the caller's stack, until it returns or suspends: do not call them while holding a loop or lock that Resolve (before
// chordctx.Suspend) enters, nor one that a Link's Close enters, because the host closes links inside its sections (a
// heartbeat timeout, a closed host's links, the drop that resolves again). Call Retry and Suspend from outside the holder,
// or have Resolve suspend before it takes the lock.
package host
