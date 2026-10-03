# Lane fix-992-codemode-mem: the code mode memory-limit script took ~13-24 s in the Go sandbox

Branch `fix-992-codemode-mem` from `staging/porter/pi-0.99.1`. Upstream: upstream v0.99.2 (`.upstream/v0.99.2`).

Finding (aggregator A): `coding` `TestUpstreamCodemodeOptionsAndStore/limits_script_memory_so_runaway_allocations_fail_inside_the_script` takes ~24 s and exceeds 30 s under `-race`. Pi's test (`.upstream/v0.99.2/packages/coding-agent/test/suite/agent-session-codemode.test.ts:412`) is unchanged from 0.99.1 and already ported in `coding/codemode_session_upstream_test.go:378` with the original script and expectations (`error` contains `out of memory`, `n < 512`). No upstream test is new or changed for this scope.

## Cause

Not the heap limit, allocation granularity, memory growth or a polling interval: linear memory is already reserved once at limit + 64 MiB (`newEngine`), and the profile shows 95 % of the time inside compiled guest code. The engine was built with `WithCloseOnContextDone(true)` (`codemode/engine.go`), which makes wazero's compiler emit, at every `loop` header, an indirect call into a Go trampoline that exits the compiled code, calls `FailIfClosed` and re-enters (`wazero@v1.12.0/internal/engine/wazevo/frontend/lower.go:1355`, `call_engine.go:473`). QuickJS's interpreter dispatch loop and the byte loops of `String.prototype.repeat` are wasm loops, so every bytecode and every copied character paid a Go round trip.

Measured on the shared (load 40-100) 196-core host, same script, one execution after warm-up:

| | time |
|---|---|
| upstream 0.99.2 (`quickjs-wasi@3.6.2` in node, with and without an interrupt handler; `/tmp/qjsmem.mjs`) | 1.95-2.05 s |
| Pig before (`BenchmarkMemoryLimitTrip`, `-benchtime 3x`) | 13.3 s |
| Pig with `WithCloseOnContextDone(false)` (experiment, no hard stop) | 3.4 s |
| Pig after | 3.0 s |

Other scripts: 100 x `"x".repeat(1<<20)`: 5.1 s -> 1.2 s; a 10M-iteration integer loop: 8.5 s -> 2.3 s (node 0.77 s). Every code mode script ran 4-6x slower than it needed to.

## Fix

The hard stop (a script that never reaches QuickJS's interrupt poll, `execution.go` `terminationGrace`) is a real requirement: the sandbox shares PiG's process and Go cannot preempt compiled guest code, where Pi calls `worker.terminate()`. So the stop is kept and made cheap:

- `codemode/haltcheck.go` `instrumentHalt` rewrites the module once at engine creation: it appends a mutable i32 global exported as `pig_halt` and inserts `global.get $pig_halt; if; unreachable; end` after every `loop` header (a load and a branch in compiled code). The rewriter decodes the instruction stream (MVP, reference types, bulk memory, tail calls) and fails on anything else (SIMD, atomics, exceptions); the embedded module needs none of those. A custom `SandboxOptions.Wasm` module it cannot rewrite keeps the previous `WithCloseOnContextDone` hard stop (review fix, `TestHaltFallsBackToClosingAModuleItCannotInstrument`). The embedded `assets/quickjs.wasm` is not modified.
- `newEngine` no longer sets `WithCloseOnContextDone`; `vm.halt` sets the global; the grace timer in `execution.finish` calls it (the VM-start / grace-timer ordering is closed by `execution.machine` and `execution.halting`, whichever is set second halts). The halted guest traps with `unreachable`, which surfaces as the existing `vmTrap`; the verdict was already recorded, `hardStops` and the lifetime guarantees are unchanged.
- No observable change: error text, RangeError/`InternalError: out of memory` and the timeout/abort messages are the engine's and the existing tests'.

## Red

Commit `08c311ee6` `test(codemode): ... (red)`: `codemode/haltcheck_test.go` plus signature stubs `instrumentHalt` (returns an error) and `vm.halt` (does nothing), and `BenchmarkMemoryLimitTrip`.

`go test ./codemode -run 'InstrumentHalt|TestHaltStops'`: `TestInstrumentHaltTrapsALoopWhenTheGlobalIsSet` and `TestInstrumentHaltLeavesALoopFreeModuleAlone` fail with `instrumentHalt: not implemented`; `TestHaltStopsAGuestThatNeverReachesTheInterruptCheck` fails with `the script still runs 10s after halt`. `TestInstrumentHaltRejectsWhatItCannotRewrite` passed on the stub (it always errors); it is meaningful against the implementation (the truncated and SIMD cases reach the decoder). The Pi test itself needed no new red: it already existed; the symptom is its duration (13 s alone, 24+ s under `-race` on a loaded host), measured by the benchmark.

Test fix in the green commit: the red commit's hand-assembled module had wrong section sizes (type 0x09 -> 0x08, code 0x1d -> 0x20, `count` body 0x14 -> 0x16). That is a mistake of my own tests, not an upstream mis-port.

## Green

Commit `perf(codemode): halt a stuck QuickJS instance with a loop-head check ... (green)`.

Mutation checks (each compiled, each caught):
- no check inserted (`op == 0x03` -> never): `TestInstrumentHaltTrapsALoopWhenTheGlobalIsSet` and `TestHaltStopsAGuest...` fail after 10 s; `TestAScriptThatNeverReaches...` never returns (the grace halt has nothing to trap).
- `vm.halt` does nothing: `TestHaltStopsAGuest...` fails.
- the grace timer returns before halting: `TestAScriptThatNeverReaches...` never returns.

## Verification

- `go test -race ./codemode/... ./coding/extension/builtin/... -count=1`: ok. The Pi test `TestUpstreamCodemodeOptionsAndStore/limits_script_memory...`: 3.1 s under `-race` (was 24 s); `-race -count=6` of `TestUpstreamCodemode*` with `GOMAXPROCS=4 taskset -c 100-103`: ok.
- Load: `GOMAXPROCS=4 taskset -c 100-103`, `-race -count=24`, lifetime/execute/halt/timeout/abort tests (14 tests, 336 runs): all pass. With four busy-loop burners pinned on the same four cores, `TestExecuteReturnsOnlyAfterTheVMIsClosedForEveryTerminalKind` failed twice ("N executions needed the instance closed under them"): the starved VM thread took more than the 500 ms `terminationGrace` to see the interrupt flag, so the hard stop fired. That assertion and the grace are unchanged by this lane; the starvation case is not made worse by it (the halt is the same escalation at the same time), and I did not change either.
- `make parity-family`: no parity scenario covers codemode (`grep -ril codemode test/parity/scenarios` is empty), so there is none to run.
- `go fix -diff ./...` is not empty in `coding/extension/host/runtimecell/build_failure.go` (not touched here); `./codemode` is clean.
