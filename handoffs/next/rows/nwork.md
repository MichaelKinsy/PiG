# Rows of lane N-WORK (Chord v2 slice 10 workers, Pi `6d4b7443`) for the integrator

Branch `fix/follow-nwork` on `fix/follow-pi-next` `65411f70dd`, with KFold's route commit (`6902afe773`) and plugin seams (`c01defa69c`) merged. Delete this file (and `nwork-tests.json`) after transcribing. One Go package `chord/nextnode/workers` (files `workers_<stem>.go`; `index.ts` is the package's exported names). Red commits: `d5a696b90a` (control, lock, process tests: `undefined: crossed, CrossedError, ...`), `479ecc3ebc` (main, workers tests, fixture worker, benchmarks: `undefined: processPort ...`).

**Status: complete: all 31 `workers.test.ts` cases are ported and pass (the eight rollout cases included, with KFold's `d03cd05f3e`).** Review `handoffs/next/reviews/nwork.md` (RevWork): B1-B5 and S1-S8 fixed, nits N1-N8 fixed or recorded below; each blocker and should-fix has a regression test (red commit message records its failure). Windows code is compile- and vet-checked only (no Windows host).

## Design record: goroutine or child process (owner review)

A worker is a **child process**. The observable contract (`workers.ts` header, slice 10 §3) is isolation: a crash is contained and reported by its exit code or signal (`worker exited (code 7)`, `(SIGKILL)`), a worker that answers nothing is killed with everything it spawned (process group; job object on Windows), one live worker per address is a kernel lock across processes, its stdout and stderr are its own. A goroutine has none of these (a panic ends the whole Go process; it cannot be killed; no exit code). So the Go form keeps Pi's: `fork` becomes `os/exec` of the app's own executable (`Entry`, `ExecArgv`, `Env`; a test starts its own binary again, `TestMain` + `CHORD_WORKERS_FIXTURE`), detached in its own process group, stdout/stderr piped.

| Pi (Node) | Go | why |
|---|---|---|
| `fork` with V8 `serialization: "advanced"` IPC | a socketpair on fd 3 (Unix) / a named pipe named by `CHORD_WORKERS_CHANNEL` (Windows); records `kind byte, uint32 length, payload` (frame or JSON control message) | no Node channel in Go; one ordered stream keeps frame/control ordering; sends never block (unbounded queue, one writer goroutine) |
| `process.on("message"/"disconnect")`, `process.send` | `link.IpcPort` implemented by `channel`; the worker's `process` is `openProcessChannel()` | Pi's `ipcLink` is reused unchanged |
| `workerMain` returns, the event loop keeps the process | `WorkerMain` blocks (`select{}`), ends through `exitProcess` | a Go program ends when `main` returns |
| `process.once("exit", removeFile)` | `atExit` hooks run by `exitProcess` | Go has no exit event; a SIGKILLed holder leaves a stale socket file and the lock fails closed, as in Pi |
| `uncaughtException`/`unhandledRejection` -> `fatal` | a panic in `fn` is recovered into `fatal`; a panic in a reload is recovered into a `reloaded` error (as Pi's try/catch around `runtime.reload`); a panic in an app goroutine ends the process (exit 2), which the server reports as a crash | a Go panic in a goroutine cannot be caught elsewhere; containment is the process boundary |
| `new Promise` queue per address, `queue.then(op)` | chain of `done` channels, one owned goroutine per operation | order and the bookkeeping-before-awaiter order are kept (`Workers.enqueue`) |
| the JS thread | a `host.Loop` of the component; every synchronous part of a Pi function is `loop.Run` | same as D5b (`DESIGN-go-layout.md` §3) |
| a blocked event loop (busy loop) | the whole worker process stopped (`SIGSTOP`, resumed by a helper process) in the fixture | a Go worker has no single thread to block; stopping the process leaves nothing running (no heartbeat, no shutdown handler, no timer), the observable of Pi's busy loop |
| `entry` module + `execArgv` | `Entry` executable + `ExecArgv` | a worker is a program |
| esbuild, `vm.Script`, `createPluginRuntime` | the plugins component's Node subprocess (docs/plan/chord-plugins-node-evaluator.md): `WorkerMainOptions.Node` (`plugins.NodeOptions`) and `HostPackages` (module files); `WorkerPlugins.Load` starts the worker's `NodeHost`, routes the worker's address kind to its link on the worker's Go host (a relay without an address) and loads it; the NodeHost's exit is the worker's exit and its keep-alive counts the worker's idle state | ported on the real stack (real esbuild build, real Node host); the worker's core facets are JavaScript modules (plugnode-differences.md, 2) |

Windows: the channel, process tree (job object), `lock` (named pipe) compile (`GOOS=windows go vet`); they have no runtime test here (no Windows host). The tests that stop a worker process (`freeze`, SIGTERM) are Unix-only.

## (a) Upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/chord/src/next-node/workers/control.ts | ported | chord/nextnode/workers/workers_control.go | test:chord/nextnode/workers/workers_control_test.go#TestErrorsCrossAsMessageCodeAndReasonNeverAStackOrCauseComeBackAsChordErrorsWhenTheyHadACode | `ToWorker`/`FromWorker` unions are one struct each tagged by `Kind`, each writing and reading its own JSON in Pi's key order; `controlOf[M]` is generic and takes what the channel delivers (`[]byte` frame, `json.RawMessage` control); `crossed(err any)`; the report's `ReloadReport` is `host.ReloadReport` (`ReloadedObject.Cause` is a value there) |
| packages/chord/src/next-node/workers/index.ts | designed-out | none | none | re-export barrel; the exported set is the package's (`CrossedError`, `ProcessLock`, `WorkerMain`, `WorkerStart`, `WorkerPlugins`, `WorkerResult`, `WorkerMainOptions`, `CreateWorkers`, `WorkerState`, `Workers`, `WorkersError`, `WorkersOptions`) |
| packages/chord/src/next-node/workers/lock.ts | ported | chord/nextnode/workers/workers_lock.go, workers_lock_unix.go, workers_lock_windows.go | test:chord/nextnode/workers/workers_lock_test.go#TestProcessLockMakesASecondTakerWaitUntilTheHolderReleasesThenTakesIt | `processLock` is `ProcessLock(name, ctx)` returning `ProcessLockHandle` (Go cannot give the function and the type one name); waiters are connections the holder keeps open and never answers, joined by `Release`; ctx abort returns `context.Cause`; abstract socket (`@chord-lock-`), socket file, named pipe |
| packages/chord/src/next-node/workers/main.ts | ported | chord/nextnode/workers/workers_main.go | test:chord/nextnode/workers/workers_main_test.go#TestTheWorkersSideShutsDownDuringItsStartupAbortsItRunsTheClosersRegisteredSoFarInReverseOrderExits0 | see the design record; the worker's state is a mutex (every message, signal and watch listener enters through it; sends never block); `fn` and closers return errors; reloads run on an owned task |
| packages/chord/src/next-node/workers/process.ts | ported | chord/nextnode/workers/workers_process.go, workers_process_unix.go, workers_process_windows.go, workers_channel*.go | test:chord/nextnode/workers/workers_process_test.go#TestAWorkerProcessPipesItsOutputKeepsItsStderrTailQueuesAnEarlyFrameOnItsLinkTellsControlMessages | `exited` is a closed channel + `Exit()`; the `Exited` handler runs before waiters resume; a fork that fails to start is a process of pid -1 whose exit {null, null} is told, as Node's `error` event; stderr tail is the last 4096 UTF-16 units |
| packages/chord/src/next-node/workers/workers.ts | ported | chord/nextnode/workers/workers_workers.go, workers_outcome.go | test:chord/nextnode/workers/workers_workers_test.go#TestFailuresStayACrashAfterReadyStaysFailedWorkerExitedWorkerFailedNothingRestarts | `Workers.State()` is a `state.SourceHandle`; `WorkersError` is a sealed interface (`BatchWorkersError` wraps `state.BatchError`); `Workers<K>` kind parameter type-level; `IdleMs` is a `time.Duration`; a `clock` seam (unexported option) drives the timers with vitest-style fake timers; the component joins its own goroutines in `Close`; the call into the app's `Start` is not joined (it may never return) |
| packages/chord/test/next/bench/workers.ts | ported | chord/nextnode/workers/workers_bench_test.go | test:chord/nextnode/workers/workers_bench_test.go#BenchmarkWorkerColdStart | Go benchmarks run by hand (`go test -run '^$' -bench . ./chord/nextnode/workers`): cold start to `started` and `ready` (median), server heap per idle worker (20) |

Go-only files (no Pi file): `workers_channel.go` (+`_unix`, `_windows`: the IPC channel), `workers_exit.go` (exit hooks), `workers_outcome.go` (once-settled outcome, task group).

## (b) PORT_MAP rows to add

| source | PiG | status |
|---|---|---|
| `packages/chord/src/next-node/workers/{control,lock,main,process,workers}.ts` | `chord/nextnode/workers` (`workers_<stem>.go`; `index.ts` is the package's exported names) | ✅ |

## (c) Tests: see `nwork-tests.json` (per upstream test file: ported / missing / designedOut)

`control.test.ts`, `lock.test.ts`, `process.test.ts`, `main.test.ts` fully ported (`main.test.ts`'s SIGTERM+disconnect `it` is two Go tests; the SIGTERM one is Unix-only). `workers.test.ts`: 23 of 31 ported with Pi's inputs and expectations; "a link lost while its child lives" and "reaps a worker that closed its own IPC channel" are Unix-only (`freeze`). Adaptations of the fixture worker (`workers_fixture_worker_test.go`): `Control.fatal` is not ported (an uncaught throw from a timer is a panic in an app goroutine, a process crash in Go; no test calls it); the plugin `@test/notes` is a Go facet (`fixtureEvaluator`) whose label is its script's text; `GORACE=atexit_sleep_ms=0` is set in the fixture workers' environment (a `-race` binary sleeps a second at exit, which Node does not, and the keep-alive test is about when a worker stops). A bug the tests found: the child's channel left descriptor 3 open after `Disconnect` (`net.FileConn` duplicates it), so the server never saw a child-side close (`TestFixRound1Reaps...`).

## (d) Async contracts: see `nwork-async.md`

## (e) New public Go API (interface IDs for the integrator)

`chord/nextnode/workers`: `CrossedError`, `CrossedReport`, `CrossedFailure`, `CrossedCause`, `CrossedObject`, `ReloadedPlugins`, `ToWorker`, `ToWorkerKind` (`ToWorkerStart`, `ToWorkerShutdown`, `ToWorkerReload`), `FromWorker`, `FromWorkerKind` (`FromWorkerStarted`, `FromWorkerReady`, `FromWorkerIdle`, `FromWorkerFatal`, `FromWorkerReloaded`); `ProcessLock`, `ProcessLockHandle`; `ForkOptions`, `Exit`, `ProcessEvents`, `WorkerProcess` (`NewWorkerProcess`, `Exited`, `Exit`, `StderrTail`, `Send`, `Kill`, `Pid`, `StartedAt`, `Link`); `WorkerMain`, `WorkerStart`, `WorkerLoad`, `WorkerPlugins`, `BusyWatchable`, `WorkerResult`, `WorkerMainOptions`; `CreateWorkers`, `WorkersOptions`, `WorkerStatus` (`WorkerStarting`, `WorkerRunning`, `WorkerStopping`, `WorkerFailed`), `WorkerState`, `WorkersError`, `WorkerFailedError`, `AuthorizeFailedError`, `WorkerExitedError`, `BatchWorkersError`, `Workers`. No SDK or CLI surface.

## Open points for the integrator

- **Owner review of the mapping** (design record above): process, not goroutine; the decision is conservative (it preserves every observable of Pi's slice 10 §3).
- `make generate` stops at `interface-proposals` on a clone without `.upstream/current` (the pin mirror is the integrator's); no other generate step ran.
- Windows has no runtime test (compile and vet only).

## Review follow-up (NWork3)

| item | fix | test |
|---|---|---|
| B1 deadlock (component loop held across `route.Retry`) | `d57c1ae9b6` (retry outside the loop) | `TestARolloutCallsTheRouteOutsideTheComponentLoopSoAServerSectionClosingAWorkersLinkCannotDeadlockIt` (deadlocks on the old ordering) |
| B2 activity read off the host loop | worker reads the activity watch's value | `TestTheWorkersIdleStateReadsTheHostsActivityOnlyThroughItsWatch` (DATA RACE under `-race` before) |
| B3 `Close(ctx)` ignores ctx | `taskGroup.wait(ctx)` | `TestCloseStopsWaitingWhenItsContextEndsAWorkerThatIgnoresShutdownKeepsRunning`, `...AnAppHookThatIgnoresItsContextKeepsRunning` |
| B4 GC test | root cause: the test clock's raw `time.AfterFunc`; a stopped runtime timer keeps its callback (the start-timer job, which captures the address) in the P's timer heap until the heap is cleaned. `host.Loop` timers drop their job on Stop; the fleet's real clock is a `host.Loop` now (product code was never affected: its default clock is the component's `host.Loop`) | `TestGC100AddressesStartedAndStoppedLeaveNoStateNoRecordNoSuspension` |
| B5 | all eight rollout tests pass | |
| S1 job handle race | mutex + Kill is a no-op once the process ended | Windows only (vet) |
| S2 pipe/goroutine leak before connect | `ended` finishes the channel (`82253c81c3`), `finish` cancels the pending accept | Unix: `...ExitsBeforeConnecting` tests; Windows vet |
| S3 lock waiters | one reader per waiter, forgotten on close, joined by `Release` | `TestProcessLockForgetsAWaiterWhoseConnectionClosedBeforeItsOwnRelease` |
| S4 start context registrations | cancelable child of the route's context, cancelled when `start` returns | `TestAStartsContextEndsWithTheStartSoItsRegistrationsDoNotAccumulate` |
| S5 shutdown ordering | `stopping` and abort synchronously, closers on a goroutine | the existing shutdown tests (no new one: `exitProcess` ends the test process) |
| S6 interfaces | `*Workers`, `*ProcessLockHandle`, `*WorkerPlugins` | compile |
| S7 promise emulation | `outcome[T]` | the workers tests |
| S8 unjoined WaitGroup | deleted | |
| N1, N2, N4, N5, N6, N7, N8 | fixed (N7: the design record above) | N6: `TestAChannelRefusesARecordOverTheLimitAndStaysUsable` |
| N3 | a `treeOf` failure kills the worker and puts the error in its stderr tail; the job is still assigned after `Start` (`os/exec` cannot resume a suspended child), so a grandchild spawned before it escapes the job | |
