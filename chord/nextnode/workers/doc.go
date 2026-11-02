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
