# Engine spike for codemode without Node (owner-approved revision of D-B, 2026-09-29)

Question: which pure-Go WebAssembly host runs the model-written JavaScript of `builtin:codemode`?

- **A**: Pi's exact `quickjs-wasi` 3.6.2 `quickjs.wasm` (sha256 `d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9`, MIT, Copyright 2026 Vercel, Inc.), embedded, with our own host on `github.com/tetratelabs/wazero` v1.12.0 (no cgo).
- **B**: `github.com/fastschema/qjs` v0.0.6 (MIT, wazero v1.9.0 and its own QuickJS-NG wasm build).

Both ran on Linux amd64, Go 1.27.1, 196 cores, Node v24.19.0 for the reference. Sources: `engine_a.go`, `run_a.go`, `main.go` (A), `b/main.go` (B), `node-ref.mjs` (Pi's own `CodemodeSandbox` on Node). Raw output: `a1.out` (cold cache), `a2.out` (warm cache), `b.out`, `node-ref.out`. `run.sh` reproduces them.

## What A executes

A is a Go port of `packages/codemode/src/runtime/worker.ts`: it creates one wasm instance per execution, evaluates the upstream prelude source (`prelude-source.ts` `PRELUDE_SOURCE`, unchanged), passes the `bridge` host function, and drives `settle`, `run`, `stalled` and the job queue exactly as `worker.ts:73-158` does. Limits use the same calls as `quickjs-wasi` `applyLimits` (`qjs_set_memory_limit`, `qjs_set_max_stack_size(512 KiB)`, `qjs_set_interrupt_handler`), with the interrupt flag set by a Go timer, replacing `Atomics.store` plus `worker.terminate()`.

Scenarios run through the real prelude: return value, nested tool call, `text` and `console`, syntax error, thrown TypeError (name, message and stack), a script waiting on a promise that never settles (`stalled`), stack overflow caught as `RangeError`, out-of-memory as a catchable result, and an infinite loop ended by the interrupt handler. Results match Pi's messages (for example the `done` JSON with `name`, `message`, `stack`). This spike does not run the 41 sandbox and 12 declarations cases. The declarations module is pure TypeScript with no engine, and the 41 sandbox cases are the acceptance test of the Go port itself; both are ported in the green commit.

## Numbers

| metric | A (wazero, Pi's wasm) | B (fastschema/qjs) | Node path (Pi's own code) |
|---|---:|---:|---:|
| first compile (empty cache) | 405-432 ms | 696 ms (default cache) | 90 ms first execute (V8 compile) |
| start with warm compilation cache | 22-24 ms | not measured | n/a |
| per execution, `return 1` (full prelude and bridge for A) | 3.8 ms | 4.0 ms (bare eval, no prelude) | 51 ms (worker spawn) |
| CPU-heavy: 5e7 loop iterations | 10.4 s | 15.3 s | 3.25 s |
| infinite loop, 300 ms budget | interrupted at 302 ms | not stopped after 120 s | worker terminated |
| memory limit 16 MiB | catchable `InternalError: out of memory` in 0.24 s | same error after 50.6 s | same class of error |
| deep recursion | catchable `RangeError` in 24-28 ms | wasm trap; panic while freeing the runtime | `RangeError` |

## Decision: A

1. Faithfulness: A is the byte-identical engine Pi ships, with Pi's own limit calls and prelude. B is a different QuickJS-NG build behind its own C wrapper. B offers no host interrupt callback, no job-queue control and no per-call handle API, so the `worker.ts` protocol cannot be reproduced on it without patching its wasm.
2. Robustness: B cannot stop a spinning script (`MaxExecutionTime` had no effect in 120 s), takes 50 s to report out of memory at 16 MiB, and panics after a stack overflow.
3. Speed: A is faster than B on the CPU test and starts a VM in under 4 ms. A is about 3.2 times slower than V8 on a pure-arithmetic loop; model-written orchestration scripts spend their time in tool calls, not arithmetic.
4. Maintenance: A needs about 300 lines of host code over a small, stable export list (`qjs_*`) and one pinned wasm. B ties us to a young third-party wrapper (v0.0.6).

Numbers depend on this host; use them as ratios.
