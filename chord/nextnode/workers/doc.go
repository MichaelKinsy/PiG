// Package workers ports packages/chord/src/next-node/workers: Chord v2's workers component (slice 10 §3, §4), a worker
// process per address of a host kind, started on demand, stopped when idle, never restarted by itself, its plugins
// shipped from the server; `WorkerMain` is the worker's side. Pi's barrel `index.ts` is designed out: Go imports the
// package, and what the barrel exported is exported here (`CrossedError`, `ProcessLock`, `WorkerMain` and its types,
// `CreateWorkers` and its types).
//
// Go form (DESIGN in handoffs rows nwork.md): a worker is a child process, never a goroutine. The observable contract
// is process isolation: a crash, a kill, a blocked loop and an exit code are contained in the worker, its whole process
// tree is killed with it, its locks are the kernel's. A worker is this program's own executable (or any program that
// calls `WorkerMain`), started with its IPC channel (a socketpair on Unix, a named pipe on Windows) beside its stdout and
// stderr pipes; `Entry` is the executable and `ExecArgv` its arguments.
package workers
