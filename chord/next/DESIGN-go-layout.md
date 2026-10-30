# Chord v2 in Go: how `host`, `jobs` and `remote` share one package

Status: decided by lane D5b (Pi `e7a0df27`). D5a (host core: `host`, `attempt`, `object`, `reference`), D6 (`jobs`, `remote`) and D4 (`documents`) build on it. Change it only with a note to those lanes.

## 1. The cycle

Pi's directories form a cycle that Go forbids:

- `host/attempt.ts` and `host/object.ts` use `jobs/internal` by value (`Job`, `JobRecords`); `host/host.ts`, `host/route.ts` and `host/destination.ts` use `remote/internal` by value (`Path`, `HostLink`, `HoldRequest`, `Upstream`, `ProviderSide` wiring).
- `jobs/job.ts`, `jobs/records.ts` and the whole of `remote/*` use `host/internal` by value or as the type of the thing they act on: `AnyHost`, `CallContext`, `Caller`, `nextDelay`, `uuidv7`, `ChordObject`, `Holder`, `Serving`, `Binding`, `Target`, `RetryOperation`, `CheckedParams`, `PinnedKey`, `RefState`, `LIVE`, `LOADING`, `UnboundRequest`. `remote/hold.ts` and `remote/remote-object.ts` implement and drive `ChordObject`/`Target`/`Holder` member by member.

## 2. Decision: one package, `chord/next/host`

`host/`, `jobs/` and `remote/` of Pi are one Go package, `github.com/MichaelKinsy/PiG/chord/next/host` (package name `host`). The package has no Go import cycle by construction: it imports `contracts`, `data`, `registries`, `state`, `link`, `store` and `documents` (one way each), and none of them imports it.

- File names carry the Pi directory: `host_<stem>.go`, `jobs_<stem>.go`, `remote_<stem>.go` (`ref-state.ts` -> `host_ref_state.go`, `member-source.ts` -> `host_member_source.go`). Tests: `<same stem>_test.go`; shared test support: `host_support_test.go` (and per-lane `*_support_test.go`). Tests are in-package (`package host`).
- Pi's barrels `host/internal.ts`, `host/index.ts`, `jobs/internal.ts`, `jobs/index.ts`, `remote/internal.ts` are designed out: Go imports the package. What a barrel exported for Chord's own layers only (`internal.ts`) is unexported in Go (lower-case identifier); what `index.ts` exported to apps is exported (PascalCase, same name). The rule reaches members: a class or type that no `index.ts` exports is unexported with its members in the camelCase Pi spells them (`backoff.failed`, `callContext.settle`); a type that `index.ts` exports keeps PascalCase for the members Pi's public type declares (`Ctx.Get`, `Presence`, `Host.Load`) and lower-case for the rest. A name that `index.ts` exports and an `internal.ts` name never clash in Pi; if two Pi names of different directories collide in the one Go package, the later lane suffixes the one that is internal (`remotePath`), never the public one, and records it in its rows.
- Not an interface package. Moving the thirteen host types of section 1 behind interfaces in a `host/internal` package would turn the host into a facade over its own contracts and force every `ChordObject` member `remote` calls through a second declaration. A Go `host/internal` directory would also be importable only below `chord/next/host/`, which a sibling `chord/next/jobs` could not use. One package keeps Pi's structure (the class members stay where Pi has them) and its names.
- `documents` stays its own package (Pi's `documents/` is one directory, imported by `host` and by `jobs`, never importing them). The one type it takes from `jobs` (`JobHandle`, `Outcome`, `Run`, `StartOptions`, Pi's type-only import via `transaction.ts`) is a consumer-owned interface in `documents` (lane D4: `StartOptions{Key *string}`, `Run{ID() string; Outcome(ctx) (any, error)}`, `JobHandle{Stage(tx, args, ctx, StartOptions) (Run, error)}`) that `jobs_*.go` satisfy structurally. `documents.DocumentsOwner` is implemented by the host's object (D5a); its `Do(work)` is `Loop.Run(work)`, its `Now()` is `Loop.Now()` and its `AfterFunc` adapts `Loop.AfterFunc` (a `func()` cancel for `Timer.Stop`).
- Test helpers that must not import `host` live in `chord/next/internal/nexttest` (as in D1-D3); helpers that build hosts live in `host_support_test.go`.

## 3. Execution model: `Loop` (Go form of Pi's event loop)

Pi runs host, objects, references, links and routines on one JavaScript event loop: a synchronous section is atomic, `queueMicrotask` jobs run after it in order, `setTimeout` callbacks run as later sections, and `Date.now()` is milliseconds. D1's `state` already assumes this: "the host serializes every call into the Batch and the sources" (`state/doc.go`). The Go form is `Loop` (`host_loop.go`, written by D5b, no Pi file; owner-approved class: Promise/event-loop mechanics where Go callbacks return no value, with the Go test named in `rows/d5b.md`):

```go
type Loop struct{ ... }                           // one per host
func NewLoop() *Loop
func (l *Loop) Run(work func())                   // one synchronous section; inline when the caller is on the loop; drains Queue jobs before it returns
func (l *Loop) Queue(job func())                  // queueMicrotask: runs after the running section, in order
func (l *Loop) AfterFunc(d time.Duration, job func()) Timer // setTimeout: job runs as a section
func (l *Loop) Now() int64                        // Date.now(): unix milliseconds
```

Helpers do not take `*Loop`; each takes the narrow consumer-owned interface `eventLoop` (`Now`, `Queue`, `AfterFunc`) so a test drives virtual time exactly as vitest's fake timers do (`fakeLoop` in `host_support_test.go`: `AdvanceTimersByTime`, `TimerCount`, `Date.now()` under the fake clock). JS timer mechanics are not designed out: each timer-driven upstream test is ported as a deterministic Go test on `fakeLoop` with the same delays and expectations.

A section that touches host state runs under `Loop.Run`. Code that runs inside a section (every method of Pi's host classes) assumes it. Entry points that start outside one (an API call from app code, a timer, a link read, a store completion) enter once with `Run`; D5a decides which of its exported methods do. `Queue` from any goroutine is safe.

## 4. Promises and cancellation

AGENTS.md "TypeScript async/Promise parity" governs. In this package:

- An awaited Promise is a blocking Go call returning `(value, error)`; a rejected Promise is the returned error. A section never blocks on the loop's own lock while waiting: a blocking call from inside a section releases nothing (Pi's `await` yields the loop; the Go call is made off the loop by the code that Pi writes after the `await`, i.e. the caller splits at the `await` as Pi's microtask does). Where Pi's method is `async` and awaits a host-internal step, the Go method is blocking and enters the loop (`Run`) at each resumption, as `documents` does (`Do`).
- A `Promise` stored and awaited later by several parties (`keyed-members.ts` `ready`, `routines.ts` `settled`, `parked.ts` call promises) is a small struct with a `done` channel closed once, a result field written before the close, and waits that select on the caller's context (`context.Context`, see below). No `chan` is closed twice (settle is once-only under the loop).
- `AbortSignal` is the `context.Context` of `chord/context` (`WithAbortSignal`, `WithoutAbortSignal`, `WithCancel(parent) (ctx, cancel(cause))`). `signal.reason` is `context.Cause(ctx)`. `throwIfAborted` is `ctx.Err() != nil` returning `context.Cause`.
- Intentionally unawaited Promises (`void this.#resolve(...)` in `route.ts`, the handler calls and the `watch.closed` waiter of `routines.ts`) are owned background goroutines started with `Loop.Go(fn)`, joined by `Loop.Wait()` in the host's close (shutdown draining), never naked `go`. Where the unawaited call is only a state machine step (`void new OnRoutine(...).start()`, `void live(...)`), it is an in-section callback queued with `Loop.Queue` instead, so Pi's ordering is kept (`d5b-async.md`).
- `Promise.withResolvers` + `Promise.race` between a work promise and a close promise (`root.ts`) is a `select` over the work's result channel, the close channel and the caller's `ctx.Done()`.

## 5. Seams between D5b files and the files of later lanes

D5b ports its 25 files before D5a/D6 exist, so a helper never names a concrete type of `host.ts`, `attempt.ts`, `object.ts`, `reference.ts`, `jobs/*`, `remote/*` or `documents`. It names a small consumer-owned interface with the Pi member names of what it uses (AGENTS.md: "a small consumer-owned Go interface only for a real behavioral contract"). D5a/D6 add a compile-time assertion (`var _ routeHost = (*chordHost)(nil)`) next to each concrete type and may replace a seam by the concrete type only if every D5b test still passes against the fakes (fakes live in the D5b tests and stay as the unit tests of the helper).

| helper file | seam (D5b declares, Pi member names) | satisfied by |
|---|---|---|
| `host_ctx.go` | `callHost`: `Incarnation() string`; `Acquire(Type *contracts.Contract, params any, caller Caller, ctx context.Context, options GetOptions) (acquired, error)`; `acquired`: `Release()`, `Reference() any` | `*chordHost`, `*Binding` (D5a). `Ctx.Get` returns the reference as `any` (pass-through: `CallContext` never inspects it); D5a's typed `*Reference` is that value |
| `host_root.go`, `host_routines.go`, `host_route.go`, `host_parked.go` | `callHost` plus `Report(HostError)`, `Closing() bool`, `RouteLink(link, options, ctx) pathLink` (route), `Timeouts().Ready` (parked_long) | `*chordHost` |
| `host_dependencies.go` | `dependencyTarget`: `BlocksFirstPass()`, `HeldOnLink()`, `Label()`; binding view `refState()`, `label()`, `target()` | `*Binding`, `Target` |
| `host_presence.go` | `presenceObject`: `Params()`, `ServedKind()`; `presenceHolder`: `Caller() Caller` | `*ChordObject`, `Holder` |
| `host_members.go`, `host_keyed_members.go` | `keyedSlot`: `Pin(key, ctx) (release func(), err error)`, `Ensure(key, ctx) error`, `Source(key) state.DeriveSource` (= `documents.KeyedSlot`); `keyedProvider`: `PinKey(member, key, ctx) (PinnedKey, error)` (= `Binding`/`Target.pinKey`) | `documents.KeyedSlot`, `*Binding` |
| `host_route.go`, `host_destination.go` | `routePath`: `Link()`, `Held()`, `Holders()`, `Connected(link)`, `Disconnected(link)`, constructor injected as `newPath func(check func()) routePath`; `routeLink`: `Open()`, `Greeted()`, `Role()`, `Close()` | D6 `Path`, `HostLink` |
| `host_native.go`, `host_routines.go` | `HostError` and `Position` (data of `host.ts`, hoisted: see below) | n/a |
| `host_facet.go` | `FacetHost.Serve(Type any, implementation any, options ServeOptions)`: Pi's implementation signature is untyped; the typed overloads are compile-time only (owner class a) | D5a `chordHost.load` |

Data types of `host.ts` that D5b files name are declared by D5b in `host_error.go` and are D5b's rows: `HostError` (sealed interface; one struct per `kind`, `BatchError` and `RegistryError` embedded as D1 declared them), `Position`, `GetOptions`. D5a's `host.ts` port does not redeclare them.

D5a/D6 finish what the seams leave: the upstream tests of the helper files that build a whole host (`createHost`) are listed as `pending` in D5b's rows with the exact case names, for D5a to close; D5b ports every other case.

## 6. Naming, errors, JSON

- Names follow Pi 1:1 (camelCase -> PascalCase when exported). A TypeScript `interface` that is plain data is a struct. A discriminated union is a sealed interface or a struct with `Kind`, as `state` and `registries` did.
- `throw` is a returned `error`; a plugin callback that may throw is called through the `try`-style helper that recovers a panic into an error where Pi catches (`registries/internal.go: try`).
- `Date.now()` is `Loop.Now()` (milliseconds, `int64`).
- Order of Maps and Sets is insertion order: use the ordered collections `state` and `registries` already provide, or a slice plus index; never a Go `map` iteration where Pi iterates a `Map`/`Set`.

## 7. D5b files

Ported: `activity`, `backoff`, `deadline`, `uuid`, `sleep`, `revocation`, `unbound`, `retry-on`, `ref-state`, `follow`, `member-source`, `keyed-members`, `members`, `native`, `parked`, `dependencies`, `presence`, `ctx`, `facet`, `root`, `route`, `destination`, `routines`. Designed out (barrels, Go imports the package): `index`, `internal`. `host_loop.go` and `host_error.go` are D5b's Go-only/hoisted files.

## 8. Amendments made while porting

- `sectionLoop` (`host_ctx.go`): `eventLoop` plus `Run` and `Go`; what every helper that resumes after a blocking wait takes (`*Loop` satisfies it). `callHost`, `routineHost`, `rootHost`, `routeHost` also carry `loop() sectionLoop`.
- Seams beyond the table in section 5 are listed in `handoffs/next/rows/d5b.md` ("What D5a must satisfy"): `dependencyBinding`, `parkedLongObject`, `unboundRequest`, `nativeOwner`, `routedHolder`, `keyedProvider`, `keyedBinding`, `watchableResolver`, `keyedResolver`.
- The batch's `*chord.DeliveryScheduler` is a constructor argument (`referenceState`, `memberSource`, `keyedMembers`): `state.Batch.deliveries` is unexported and the host owns both.
- Name collisions between helper files are resolved by suffixing the internal name (`routineFailed`, `retrySourceType`, `settledRefusal`); the list is in the rows file.
