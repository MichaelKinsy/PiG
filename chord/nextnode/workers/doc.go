// Package workers ports packages/chord/src/next-node/workers: Chord v2's workers component (slice 10 §3, §4), a worker
// process per address of a host kind, started on demand, stopped when idle, never restarted by itself, its plugins
// shipped from the server; `WorkerMain` is the worker's side. Pi's barrel `index.ts` is designed out: Go imports the
// package, and what the barrel exported is exported here (`CrossedError`, `ProcessLock`, `WorkerMain` and its types,
// `CreateWorkers` and its types).
//
// # A worker is a child process, not a goroutine
//
// The observable contract of a worker is isolation, and a goroutine has none of it:
//
//   - crash containment: a worker's crash (`os.Exit`, a signal, a panic, running out of memory) ends that worker only; the
//     server sees its exit code or signal (`worker exited (code 7)`, `worker exited (SIGKILL)`) and fails the address. A
//     panic in a goroutine ends the whole Go process.
//   - kill: a worker that answers nothing (Pi's blocked event loop) is killed with everything it spawned (its process group,
//     a job object on Windows). A goroutine cannot be killed.
//   - lock semantics: one live worker per address across processes, held by the kernel for the holder's lifetime
//     (workers_lock.go), released by its death, however it dies.
//   - output: its stdout and stderr are its own, piped to the server, the stderr tail kept for the failure report.
//
// A worker is therefore this program's own executable (or any program that calls `WorkerMain`), started with its IPC
// channel (a socketpair on Unix, a named pipe on Windows; workers_channel.go) beside its stdout and stderr pipes. `Entry`
// is the executable and `ExecArgv` its arguments (Node's `execArgv` and module). A test starts its own test binary again.
//
// # Lost links, stale lock files, the worker base
//
//   - A running worker whose link was lost (the heartbeat, a protocol error, its own close of its link) is stopped, and its
//     holders' route starts a new worker (`worker_link_lost`); a second lost link within a minute of the last recovery fails
//     the address ("worker lost its link") until `Retry` or a rollout (decision "A lost worker link is recovered from").
//   - A worker killed without its exit hooks leaves its lock's socket file on macOS and the BSDs. The component is the one
//     clearer of its addresses' locks: before each start and after the exit of a child a signal ended, in the address's queue
//     (decision "Stale lock files are cleared by the starter"); a taker still fails closed on one it finds.
//   - Pi bundles the worker's Node entry once with the base code it imports (`base.ts`, `child.ts`: esbuild), in memory, writes it
//     at the first fork into a private directory its component owns, and `close` removes it after every worker exited, so an edit
//     after the build runs in no worker. A Go worker is this program's own compiled executable: `Build` (run by the kit right
//     after the server listens, and by a start when nothing did) copies the `Entry` executable into `WorkersOptions.BaseDir` (the
//     kit's `cache/workers/<kind>/`, emptied first; a new temporary directory without one), records its bytes (`WorkersBuilt.Inputs`:
//     an edit of the entry marks the app's `base`), every worker forks that copy, and `Close` removes the directory once every
//     worker exited. A program cannot be held in memory, so the copy is made at the build and not at the first fork; a component
//     that closed copies nothing and leaves a named directory as it is. It fails when `Entry` cannot be started or copied, as
//     Pi's rejects when the base does not build: a start fails while it did (`worker failed to start`). There is no esbuild
//     bundle, `cache/node` or `NODE_COMPILE_CACHE` (docs/parity/DIVERGENCES.md D600).
//
// # Async contract (AGENTS.md "TypeScript async/Promise parity")
//
//   - Ordering: the control messages and the link's frames share one ordered stream, told to listeners in order by one
//     goroutine; a write never blocks its sender. Per address, starts and stops run one after another (a chain of done
//     channels); an event of a child is recorded on that child as it happens, in the order its source tells it, and a late
//     event of an old child never changes a new one. A process's exit handler runs before anyone waiting on the exit resumes.
//   - Cancellation: `context.Context` is `AbortSignal`. `Close` cancels starts waiting in the app's `Start`, for `started`
//     or for the plugins; the caller's context ends only its own wait. A worker's context aborts at shutdown.
//   - Shutdown draining: `Close` stops every child (`shutdown`, a 5 s grace, then the tree is killed), clears the timers and
//     joins every goroutine the component owns. A worker runs its closers in reverse order, then closes its NodeHost, forcing its own exit after 3 s.
//   - Unawaited Promises are owned tasks (the component's task group, the worker's reload loop), never naked goroutines.
package workers
