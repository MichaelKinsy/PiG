# Built-in codemode and tool-search extensions

Status: approved by the owner on 2026-09-29. This is decision D-B of `docs/plan/upgrade-0.99.1.md` (section 13) as revised the same day: the first approved design ran Pi's own extension code in the Node runtime; the owner then withdrew Node for these two extensions. This specification is the written spec that `AGENTS.md` requires before PiG hosts a WebAssembly sandbox.

Upstream reference: `.upstream/v0.99.1/packages/coding-agent/src/extensions/{codemode,tool-search}` and `.upstream/v0.99.1/packages/codemode`.

## Decision

1. Everything upstream's `codemode` and `tool-search` extensions own is native Go: the execution lifecycle, the nested-tool bridge, the tool declarations, the prelude, the limits, the result formatting, the renderer, tool exposure, and tool search. A Go extension registers them; no Node process is involved. `node` need not be on `PATH`.
2. Only the model-written JavaScript runs in a JavaScript engine: QuickJS compiled to WebAssembly, hosted by `github.com/tetratelabs/wazero` (pure Go, no cgo). This is the sole embedded JavaScript execution in PiG. It does not extend to extensions: the `AGENTS.md` ban on a WASM or embedded-JS *extension runtime* still holds, because the sandbox runs one untrusted script per VM and no extension code.
3. The engine is Pi's own: `quickjs-wasi` 3.6.2, the exact `quickjs.wasm` that upstream loads (`packages/codemode/src/wasm.ts`). The engine spike (`docs/plan/evidence/port-99-f8-engine-spike/README.md`) compared it with `github.com/fastschema/qjs` v0.0.6 and chose it on faithfulness (same engine build, same limit calls, upstream's unmodified prelude), then robustness and speed.

Classification under the product-extension boundary in `AGENTS.md`:

| Piece | Class |
|---|---|
| `builtin:<name>` registry, selection, and provenance | required substrate (Stock PiG) |
| `codemode`, `tool-search` behavior and the sandbox that serves it | upstream-parity behavior (Stock PiG); registered inactive, so it changes no request until a user, setting, or extension activates the tool |
| Any Standard preset that activates them | PiG Standard, not this specification |

No new divergence: upstream's observable behavior is preserved, and the engine is upstream's. The choice of host (wazero instead of a Node worker thread) is internal. It has the resource consequences described below, and the performance difference is recorded in the evidence, not hidden.

## Embedded engine: provenance and license

- The wasm is committed as a build asset, `codemode/assets/quickjs.wasm` (637,405 bytes), embedded with `go:embed`. SHA-256: `d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9`. Source: npm `quickjs-wasi@3.6.2`, the exact version pinned by the coding-agent package of upstream 0.99.1. `codemode/assets/PROVENANCE.md` records the version, the hash, the upstream URL, and the refresh procedure. A test compares the embedded bytes with the recorded hash.
- License: `quickjs-wasi` is MIT, Copyright (c) 2026 Vercel, Inc. The license text ships in `LICENSES/` and is listed in `THIRD_PARTY_NOTICES.md`. The wasm contains QuickJS-NG (MIT, carrying Fabrice Bellard and Charlie Gordon's notice) and wasi-libc; `THIRD_PARTY_NOTICES.md` and `codemode/assets/PROVENANCE.md` record them. `wazero` is Apache-2.0 and is pinned by exact version in `go.mod`.
- The prelude (`PRELUDE_SOURCE`, `packages/codemode/src/runtime/prelude-source.ts`) is embedded verbatim as `codemode/assets/prelude.js`. A test compares it with the recorded hash of the upstream string. A pin move regenerates both assets in one change.
- The refresh procedure moves the wasm, the prelude, and the hashes together, because the prelude assumes the engine's behavior.

## Engine host

One execution owns one wasm instance, as upstream's worker owns one QuickJS VM (`runtime/worker.ts`). The Go host is a port of `runtime/{host,worker,protocol}.ts`:

- The compiled module is shared. It compiles once per process (`wasm.ts:15-35`), and wazero's on-disk compilation cache makes later processes start in about 22 ms instead of about 430 ms (spike numbers). The cache directory is `cache/wazero` under `PIG_HOME` when it is set, else under the user cache directory (`os.UserCacheDir()/pig/wazero`). The cache is an optimization: a missing, unwritable, or corrupt cache is ignored and the module compiles in memory.
- Per execution: instantiate, initialize the engine, apply the limits below, evaluate the prelude, then drive `settle`, `run`, and `stalled` and drain pending jobs exactly as `worker.ts` does. Tool arguments and results cross as JSON strings.
- The instance's standard output and error are discarded (`worker.ts`), and its WASI clock, random source, and no file system are the only system access. The guest has no network, no file, no environment, and no host function except the one `bridge`.
- Instance memory is released when the execution ends. wazero grows the linear memory; QuickJS's own memory limit (below) bounds the heap.

## Identity and naming

Upstream lists the built-ins in `src/extensions/index.ts:6-14`. This specification covers two entries:

| Path | Factory | `replaceable` | Registers |
|---|---|---|---|
| `builtin:codemode` | `codemode/index.ts:46` | yes | tool `codemode`, inactive (`defaultActive: false`, `index.ts:41`) |
| `builtin:tool-search` | `tool-search/index.ts:18` | yes | tool `tool_search`, inactive (`index.ts:14`) |

- An extension path `builtin:<name>` names no file. Its source info is `path: "builtin:<name>"`, `source: "builtin"`, `scope: "temporary"`, `origin: "top-level"` (`core/source-info.ts:14-56`). Errors, diagnostics, and RPC source info use `builtin:<name>`.
- `builtin:<name>` loads by default, is hidden from the startup Extensions list, and loads after project trust is resolved (`core/extensions/types.ts:1985-2013`, `core/resource-loader.ts:706-738`).
- `replaceable` follows `core/resource-loader.ts:120-153`: when another extension registers the tool `codemode` or `tool_search`, the built-in is left out and a warning names both paths. An unknown name fails with `Unknown built-in extension: builtin:<name>` (`resource-loader.ts:716-720`).
- The registry is a Go table in `coding/extension/builtin`. Each row carries the name, `Replaceable`, and a Go factory `func() (extension.Extension, error)` that returns the extension with its tool registered inactive (`defaultActive: false`, exposure `model-only`). `cmd/pig` (`nativeBuiltInExtensions`) turns the rows into the loader's `builtin:<name>` entries, passing the settings manager (`codemode.mode`, `codemode.inlineBudget`, read on every use) and the compilation cache directory. Other families (resource loader, CLI, `pi config`) consume the entries; they do not restate the names. `cmd/pig/llama.go` is the precedent for a native Go built-in.
- The factories run in the in-process extension host. They add no process, socket, or Node cell.
- A built-in tool reaches the session through consumer-owned interfaces that it type-asserts from `ToolContext.SessionManager()`: the branch (for `load()`) and `AppendCustomEntry` (for `store()`, which also emits `entry_appended`). The extension context is not widened. Nested calls use `ToolContext.ExecuteTool` and `Tools()`; tool search uses the context's `GetAllTools`, `GetActiveTools` and `SetActiveTools`.

## How a build, a user, or a Piglet disables or strips them

Disable (no code runs):

1. `"extensions": ["-builtin:codemode"]` in settings disables one. A project entry `+builtin:<name>` or `-builtin:<name>` overrides the user entry (`docs/settings.md:158`, `core/package-manager.ts:972`).
2. `--no-extensions` disables every built-in. `-e builtin:<name>` loads one explicitly (`docs/cli.md:195-197`).
3. A disabled built-in registers nothing. An enabled but unused built-in costs one registered tool definition: the sandbox is created on the first script, so no wasm compiles and no VM starts until then (`execute.lazy.ts:2`).

Piglet selection (issue #92):

4. An active Piglet takes no ambient extensions unless `discovery.extensions` allows them (`docs/pig-piglet-spec.md`, Discovery). A built-in is an ambient extension, so an active Piglet loads none. A Piglet enables one with the explicit extension origin `builtin:codemode` or `builtin:tool-search`. No new Piglet field is needed.
5. Selecting a built-in through the Piglet activates the tool only if the Piglet's tool ceiling allows it. Effective tools remain `runtime registration ∩ Piglet allowlists ∩ CLI narrowing ∩ platform authorization`.

Strip (the code is absent from an artifact):

6. The sandbox, the wasm, and the wazero dependency live in their own packages (`codemode`, `coding/extension/builtin/codemode`). A build with the Go build tag `nocodemode` links neither: the registry row for `codemode` is absent, the `tool_search` row remains (it has no engine), and `builtin:codemode` then fails as an unknown built-in. `TestNocodemodeBuildOmitsCodemodeAndItsEngine` proves the tagged `cmd/pig` links neither `codemode` nor wazero. This is the supported way to remove the code from an artifact: about 640 KiB of wasm plus wazero.

## Resource bounds

Upstream defines the bounds. PiG adds none and raises none, and states the Go-side cost of each.

| Resource | Bound | Source |
|---|---|---|
| QuickJS heap per execution | 256 MiB by default (`memoryLimitBytes`); overrun throws `InternalError: out of memory` in the script, which the script can catch | `execute.ts:48,278`, `codemode/src/types.ts:111` |
| VM stack | 512 KiB (`MAX_STACK_SIZE`); deep recursion throws `RangeError` | `runtime/worker.ts:59` |
| Wall time | none by default; `// @options: {"timeout_ms": N}` sets one; a user abort or session shutdown ends the run. The deadline starts once the module is compiled: the one-off compile (about 0.4 s, and 4 s under the race detector) is not the script's time | `execute.ts:277`, `source.ts` |
| Script output | 10,000 tokens (4 characters per token); the excess spills to `os.TempDir()/pi-codemode-<hex>.txt` | `execute.ts:130,159-200` |
| `store()` | 256 Ki characters of JSON per value, 1 Mi in total | `prelude-source.ts:28-29` |
| `models.classify` | 4 in flight per script; the rest queue in call order | `execute.ts:42,380` |
| Concurrent executions | one wasm instance each; no shared cap beyond the per-execution heap | `runtime/host.ts:86-90` |
| Compiled module | one compile per process, retained; one on-disk cache directory | `wasm.ts:15-35` |
| Nested tool result | bounded by the host tool pipeline and by the 256 Ki character `store()`/output bounds above | `execute.ts` |

Worst-case resident memory is the number of concurrent `codemode` calls times the heap limit, plus the wasm instance overhead (the fixed linear memory the engine starts with) per call. Concurrent calls occur when one assistant turn contains several `codemode` tool calls. The load test measures resident memory and open file descriptors at 1, 4, and 16 concurrent executions and records them in `docs/plan/progress/port-99-f8-spec.md` beside the Node figures they replace (peak about 78 MiB and end 13-46 MiB per execution for 64 MiB scripts).

All sandbox work runs on the goroutine of the tool call, never on the TUI input or render loop. A spinning script is stopped by QuickJS's interrupt handler, which polls an atomic flag; the flag is set by the timeout timer, by the abort context, and by close. No Go thread is blocked beyond the interrupt polling interval.

## Execution lifetime and cancellation

Upstream's contract (`runtime/host.ts:80-90`, `finish` at `242-282`), which the Go host preserves:

1. `Sandbox.Execute` creates one execution and one VM after the module is ready. A sandbox holds no VM between executions.
2. The tool call builds a sandbox and calls `Close` in a `defer` (`execute.ts:269-289`).
3. An execution ends exactly once, through `finish`: the script's `done`, a `crash`, a VM trap, the timeout timer, the abort context, or `Close`.
4. `finish` cancels every pending nested call, sets the interrupt flag, closes the VM, and only then returns the result. The result is never returned before the instance is closed: after `Execute` returns, its wasm instance is closed and its goroutines have exited.
5. Nested calls still pending at `finish` end with status `cancelled`. Unawaited promises are discarded.
6. A closed sandbox rejects new executions with `Sandbox is closed`.

Go-side rules:

7. A tool call cancelled by the session (abort, `Escape`, shutdown) reaches `Execute` as `context.Context` cancellation. A cancelled call ends the VM and reports `aborted`. The tests prove the order: the tool result arrives after the instance is closed and the goroutine count has returned to its start.
8. Nested calls go through the host tool pipeline (`executeTool`) with the execution's context. Cancelling the parent cancels every child through the same context. A child that finishes after the parent ended is discarded and does not change the parent's recorded status.
9. A goroutine owned by an execution (the timer, the nested-call workers) has a single owner, joins before `Execute` returns, and reports errors on the same call. No fire-and-forget goroutine remains after `Execute` (`AGENTS.md`, TypeScript async/Promise parity).
10. A host shutdown or reload closes the extension's context; running executions abort as in item 7. Nothing outlives the process, because the VM is memory in the same process.

## Failure modes

| Failure | Detection | Required result |
|---|---|---|
| Script throws or fails to parse | `done` with `ok: false` | tool result `isError: true`, header `Script failed`, partial output kept, then `Script error:` and the stack |
| Script spins (`while (true) {}`, `while (true) await null`) | timeout or abort sets the interrupt flag | VM closed; `Script timed out` or `Script aborted`; the host stays responsive |
| Heap exhausted | QuickJS out-of-memory | script-level `InternalError: out of memory`; PiG and other extensions keep running |
| Deep recursion | stack guard | script-level `RangeError` |
| Wasm trap | wazero error from a call into the instance | `kind: "sandbox"`; `Script sandbox failed:` result; a later call works |
| `quickjs.wasm` missing, corrupt, or fails to compile | compile error at first use | `kind: "sandbox"` result naming the cause; PiG does not crash; the next call retries the compile |
| Compilation cache unusable | cache open or write error | ignored; the module compiles in memory |
| Nested tool fails, is blocked, or has invalid input | nested `isError` outcome | the script's promise rejects with the tool's error text; the call row shows `error` |
| Parent aborted with nested calls pending | context | pending children cancelled, rows `cancelled`, no late write to the result |
| Reload or shutdown mid-run | context cancelled | the run aborts; no goroutine or instance remains; no temp file other than a spilled output file remains |
| `tool_search` finds nothing | empty ranking | `No matching tools found.` with `loaded: []` |
| `tool_search` empty query or bad `limit` | validation in `execute` | error `query must not be empty` or `limit must be a positive integer` |
| Another extension registers `codemode` or `tool_search` | `replaceable` omission | the built-in is omitted and a warning is reported |

## Host prerequisites owned by other families

The two extensions call upstream APIs that the extension-API family (D-C) owns. They have landed and are used as they are: `ToolDefinition.{Exposure,PrepareLoadout,DefaultActive,ConstrainedSampling}`, `ToolInfo.{Exposure,Namespace}`, `ToolContext.{ExecuteTool,Tools}`, `AgentToolResult.{StructuredContent,Usage,IsError}` and `parentToolCallId` on tool events. This slice adds `coding.Session.AppendCustomEntry` (append a custom entry and emit `entry_appended`) and makes the session the `SessionManager` that `BindCore` gives extensions, as `BindTools` already did.

Deferred, with the owner named in `docs/plan/progress/port-99-f8-spec.md`: the `models` namespace of scripts (`models.getModelsOfType`, `getAvailableOfType`, `getModelOfType`, `classify`), which needs the model runtime to expose classifiers to extensions; it is declared off (`Options.Models` false) until then.

## Verification

- Red: upstream's tests are ported to Go with their original inputs and expectations: `packages/codemode/test/{declarations,sandbox,source}.test.ts` (12, 41, 4 cases), `packages/coding-agent/test/{tool-search,codemode-renderer}.test.ts` (9, 3), and `test/suite/agent-session-codemode.test.ts` (19). Where upstream's test has no Go analogue, the progress file names the replacement.
- Green: the engine host, the codemode and tool-search packages, and the registry, with no edit to a ported test.
- No Node: a test runs MCP tools through codemode with `PATH` containing no `node`.
- Load: execution lifetime and cancellation under `-race -count=24`, `GOMAXPROCS=4`, four pinned cores with CPU burners, and 1/4/16 concurrent executions. Each terminal kind (ok, script error, timeout, abort, close, spin, out of memory, trap, missing asset) runs at least 200 times per load run; after every run the goroutine count, open descriptors, and resident memory return to within a fixed margin of the start.
- Mutation: resolving before the instance closes, dropping the cancellation of pending nested calls, making `Close` a no-op, and removing the interrupt flag each turn a named test red.
