# Lane port-99-f8-spec: codemode and tool-search (Phase 2 step 8, D-B)

Branch port-99-f8-spec, forked from the 0.99.1 porter branch. Spec: `docs/specs/builtin-codemode-tool-search.md`.

## Environment notes

- The owner lifted the npm release-age cooldown for the upstream 0.99.1 pin only. the coding-agent package of upstream 0.99.1 (and its exact-pinned dependencies, among them `pi-codemode` and `quickjs-wasi` 3.6.2) was installed in a scratch directory (`/tmp/pi99-f8-spec`) with no global npm config changed and no override used on this host. `typescript@5.9.3` was installed beside it for the vendor closure walker.
- `extensions/sdk-ts` still pins 0.87.1 and the tracked vendored tree (`runtime-node/shims/pi-dist`) is 0.87.1. The pin move is the lead's Phase 1. Every result below that needs the vendored 0.99.1 modules was produced in a scratch copy of this branch (`/tmp/f8s-scratch`) whose vendored tree was regenerated from the 0.99.1 install with this branch's `automation/gen/vendor-pi-dist.sh`. In this branch alone, and before the pin move, those tests fail because the modules are absent.

## Red

Commit: see the log (subject: `test(codemode): port codemode and tool-search tests for upstream 0.99.1 with signature stubs (red)`). Run recorded in `docs/plan/evidence/port-99-f8-spec-red.txt`.

| upstream test file | cases | ported as |
|---|---:|---|
| `packages/codemode/test/declarations.test.ts` | 12 | `TestUpstreamCodemodeDeclarations`, verbatim copy under `coding/extension/host/subprocess/testdata/upstream-0.99.1` |
| `packages/codemode/test/sandbox.test.ts` | 41 | `TestUpstreamCodemodeSandbox`, verbatim copy |
| `packages/codemode/test/source.test.ts` | 4 | `TestUpstreamCodemodeSource`, verbatim copy |
| `packages/coding-agent/test/tool-search.test.ts` | 9 | `TestUpstreamToolSearch`, verbatim copy |
| `packages/coding-agent/test/codemode-renderer.test.ts` | 3 | `TestUpstreamCodemodeRenderer`, verbatim copy |
| `packages/coding-agent/test/suite/agent-session-codemode.test.ts` | 19 | 16 as Go Session tests in `coding/codemode_session_upstream_test.go`; 3 that call Pi's functions without a Session as `TestUpstreamAgentSessionCodemodeWithoutASession` (the second half of "declares models only for the session's own codemode tool" is its own case there) |

The ported TypeScript files keep upstream's bodies. Only the runtime import specifiers change (vitest becomes `vitest-shim.mjs` on `node:test`, `src/...` becomes the vendored copy). Node 22.19 and later strips the types. Each Go test runs one file and reports every upstream case as a subtest; a case that does not run fails the test (`wantCases`).

New tests with no upstream analog (spec, "Verification"): `coding/extension/builtin` registry and strip-closure tests, `builtin_extension_test.go` (host loads `builtin:<name>` through the Node cell), `codemode_lifetime_test.go` (worker lifetime through every terminal kind, and 16 concurrent executions).

Stubs and blocked cases (other families own the behavior):

- `coding/extension/builtin.All` and `Resolve` are signature-only stubs in the red commit.
- `codemodeHarness.setCodemodeMode` (`Settings.codemode`, `ExtensionAPI.getSettings`), `codemodeHarness.registerScorerProvider` (classifier providers, `Models.classify`) are stubs in `coding/codemode_session_harness_test.go`.
- The nested-call, tool-exposure and structured-result cases need `ctx.tools`, `ctx.executeTool`, `ToolDefinition.{exposure,prepareLoadout,defaultActive}`, `outputSchema`, `structuredContent`, tool-result `usage` and `parentToolCallId` from the extension-API family. The Session-level cases and `TestBuiltinCodemodeRunsAScriptThroughTheNodeCell` stay red until that family lands.

## Redirect: native Go with QuickJS on wazero (owner-approved 2026-09-29)

The Node design above is superseded (the Node green `5d30b1a6f` is reverted by `01fceb7cb`). The new design is `docs/specs/builtin-codemode-tool-search.md`; the engine choice and numbers are in `docs/plan/evidence/port-99-f8-engine-spike/README.md`. The verbatim upstream TypeScript copies under `coding/extension/host/subprocess/testdata/upstream-0.99.1` stay as the oracle for the Go ports; their Go wrappers need the vendored 0.99.1 Node tree and are not the acceptance test any more.

### Red 2: package `codemode` (port of `packages/codemode`)

Go ports with upstream's inputs and expectations, plus signature-only stubs (`codemode/{types,identifier,source,declarations,sandbox,assets}.go` return zero values):

| upstream file | Go file | cases |
|---|---|---:|
| `test/declarations.test.ts` | `codemode/declarations_test.go` | 12 |
| `test/source.test.ts` | `codemode/source_test.go` | 4 |
| `test/sandbox.test.ts` | `codemode/sandbox_test.go` | 41 |

Sandbox mapping notes: "embedded sources parse as JavaScript" becomes the hash check of the embedded prelude and wasm plus a first run; "reports a missing worker file as a sandbox error" has no analogue (no worker file) and is replaced by `TestCorruptWasmModuleIsASandboxError`; the abort signal is a `context.Context`; the never-settling tool promise is a tool that waits for its context, because every goroutine of an execution joins before `Execute` returns.

Red run (`docs/plan/evidence/port-99-f8-codemode-red.txt`): 57 of 57 cases fail on the stubs; none passes before the implementation.

### Red 3a: extension side, pure functions (tool-search and the codemode description catalog)

`coding/extension/builtin/toolsearch/toolsearch_upstream_test.go` ports `packages/coding-agent/test/tool-search.test.ts` (tokenize 1, Bm25Ranker 3, description 1) and `coding/extension/builtin/codemode/description_upstream_test.go` ports its four codemode description catalog cases. `coding/extension/builtin/builtin_test.go` keeps the two registry cases and replaces the Node-era asset-closure cases (designed out by the redirect: there are no runtime assets) with `TestFactoriesRegisterTheirToolInactive` (`index.ts:14,41`). Stubs return zero values. Red run: 11 of 12 fail; `TestBm25RankerReturnsNothingForUnknownOrEmptyQueries` passes on the stub because the expected result is empty (it is meaningful only after the ranker is real; it is mutation-checked in the green commit). Evidence: `docs/plan/evidence/port-99-f8-extension-red.txt`.

## Final state (native Go)

### Commits, red to green (this lane, after the base `40001cd42`)

| commit | what |
|---|---|
| `09460cd4e`, `5a42b0983`, `5d30b1a6f` | the Node design: spec, red, green. Superseded by the owner's 2026-09-29 redirect; the green is reverted by `01fceb7cb` |
| `f2a58cd95` | engine spike evidence (Pi's quickjs-wasi wasm on wazero vs fastschema/qjs) |
| `8fb6fbc54` | the approved spec: native Go, QuickJS on wazero (revised D-B) |
| `797f3298c` | red 2: package `codemode` tests ported to Go (57, all red on stubs) |
| `5440dc3a2`, `ca252db25` | green 1: package `codemode` (sandbox, declarations, source); fix: compile time out of the deadline |
| merge of `porter/pi-0.99.1` | family 6 landed (ToolContext, exposure, loadout) |
| red 3a `test(codemode): port upstream tool-search and codemode description tests` | 12 tests, 11 red |
| `5309a659f` | green 2a: tool-search ranking and the codemode description catalog |
| `3d97d80b3` | green 2b: the native `builtin:codemode` and `builtin:tool-search` extensions, `cmd/pig` wiring, `nocodemode` tag |
| `fef0c2547` | green 3: the codemode renderer |

### Test counts

- Package `codemode`: 57 ported upstream cases (12 declarations, 4 source, 41 sandbox; "missing worker file" is replaced by `TestCorruptWasmModuleIsASandboxError`, "embedded sources parse" by the hash and first-run test) plus own lifetime tests (every terminal kind x25, Close, hard stop, late nested call, deadline excludes compile) and the resource test.
- tool-search 5 + codemode description catalog 4 + registry 3 upstream-derived cases, with added BM25 score, schema-walk, tie-order and catalog-order tests pinned to measurements of upstream's own ranker.
- Renderer: 3 upstream cases plus 2.
- Session: `agent-session-codemode.test.ts` (19 cases): the 3 session-free ones are ported to `coding/extension/builtin/codemode/session_free_upstream_test.go` (their Node oracle copies are deleted with the Node path); of the 16 Session cases 11 pass. The 5 that fail are named below.
- `TestCodemodeRunsMCPStyleToolsWithoutNodeOnPath` (PATH holds no `node`) and `TestCodemodeTimeoutCancelsARunningNestedCall`.

### Numbers (Linux amd64, this host)

| | Go sandbox (wazero) | Node path (Pi's own code) |
|---|---:|---:|
| first compile, empty cache | 405-432 ms | 90 ms |
| start with a warm compilation cache | 22-24 ms | n/a |
| per execution, `return 1` | 3.8 ms | 51 ms |
| 5e7-iteration loop | 10.4 s | 3.25 s |
| 256 MiB allocation loop to out-of-memory | 13.6 s | 2.1 s |
| resident memory, one 64 MiB buffer per execution, 1/4/16 concurrent: peak | +66/+262/+1049 MiB | +150/+381/+1309 MiB (a 64 MiB string) |
| the same, after they finish | +0/+2/+8 MiB | +83/+122/+252 MiB |
| descriptors after | unchanged | +1 |

A 64 MiB `'x'.repeat` takes 3.4 s (6.7 s under the race detector) because the wasm string builder copies per character; a `Uint8Array` fill of the same size takes 59 ms. Scripts that build huge strings are the slow case. wazero is 3-6 times slower than V8's WebAssembly on arithmetic- and memory-bound scripts; a script that spends its time in tool calls does not notice.

### Mutation checks (each turns a named test red)

Interrupt handler off; interrupt flag not raised; pending nested calls not cancelled; `Close` not waiting; VM not closed; `calls.Wait` dropped; deadline started before the compile; nested `Signal` replaced by `Background` (hangs until the test watchdog); `on` vs `only` listing; the structured-content path; output truncation keeping images; BM25 idf constant, tie order (`sort.Slice`), schema keys, cheapest-first, namespace order; renderer total, sub-cent precision, call/argument/output previews and full-output path.

### Load test

`-race -count=24`, `GOMAXPROCS=4`, `taskset -c 0-3`, 8 CPU burners on the same cores. The first run exposed three defects that are fixed (`ca252db25`): the one-off module compile counted against the 10 s deadline (4 s under the race detector, over 10 s under load), a hard-stop test whose script the interrupt flag could stop, and a resident-memory margin that the race detector's shadow memory breaks. The reruns on the final code pass: package `codemode` `-race -count=24` (`GOMAXPROCS=4`, `taskset -c 0-3`, 8 burners on the same cores) in 689 s with no failure; the every-terminal-kind test (9 kinds x 25 runs) and the resource test `-count=4` pass; `TestCodemodeTimeoutCancelsARunningNestedCall` and `TestCodemodeRunsMCPStyleToolsWithoutNodeOnPath` in package `coding` pass `-race -count=24` under the same load. Under load: descriptors 7 -> 7, live VMs 0 after every run, resident memory returns to within +8 MiB of the baseline at 1, 4 and 16 concurrent executions (peak +64/+260/+1040 MiB with the race detector). Two more findings fixed in tests only: the resource test held its 64 MiB with `repeat` (3.4 s each unloaded, so 16 of them exceeded its 60 s deadline on a third of four cores) and the out-of-memory case of the terminal-kind test grew its heap with `repeat` (over 10 s under load); both now allocate typed arrays, which the engine handles natively. No sandbox behavior changed.

### Deferred, and why

- `models.*` for scripts (3 Session cases, `TestUpstreamCodemodeModels`): `ModelRuntime` has no `Classify` and `registerScorerProvider` needs the classifier provider registration; `Options.Models` stays false so the model API is not declared. Owner: the model runtime / classifier family.
- 2 Session cases (`runs nested calls in parallel`, `keeps structured content that tool_result handlers replace`) use Node fixture tools: the Node extension runtime in this base does not send `outputSchema` or `structured_content` over the wire (the wire has them, `protocol.go:804`). Owner: the Node SDK path (pin move / family 6F-ts).
- 1 Session case (`resolves bash calls to structured results`): the built-in `bash` tool declares no `outputSchema` in this base. Owner: the tools family.
- `PORT_MAP` rows for the added upstream files (`packages/codemode/src/**`, `extensions/codemode/**`, `extensions/tool-search/**`) and the interface-inventory mapping: the lead's Phase 1 / generated files.
- `-builtin:codemode` selection, `--no-extensions`, and the Piglet ambient-extension rule already exist in the loader (`cmd/pig/extension_set.go`); no Piglet field was needed.
- No changelog fragment: the tools are registered inactive and are reachable only through `--tools`, the `defaultTools` setting or another extension; the MCP extension activates codemode in its lane.

### Deviations from upstream (Go mechanics or unreachable edges; none is an observable Pi behavior)

- `$ref` segments with invalid percent-encoding resolve to `unknown` (JavaScript throws `URIError`); invalid schema JSON renders `unknown`.
- Unpaired surrogates in script output become U+FFFD (Go strings cannot hold them).
- `@options` JSON errors keep upstream's prefix; the detail is Go's JSON error text, not V8's.
- `TimeoutMs` 0 means "the default"; `+Inf` is upstream's `Infinity`.
- Test-file edits with reasons are in the commit messages (`3d97d80b3`): the harness loads the native extension; `SetLeafID` for upstream's `sessionManager.branch()`; the config selector lists only hosted built-ins because codemode and tool-search now sort before llama.cpp. The Node-path tests and vendored oracle copies (`5a42b0983`) are deleted; the history keeps them.

### Review fixes (rev-port-99-f8-spec)

- The VM had wazero's sandbox defaults: a fake wall clock fixed at 2022-01-01, a fake monotonic clock, a deterministic random source and a zero timezone offset, so `Date.now()` was wrong and `Math.random()` repeated in every execution. It now gets the host clock in milliseconds, `crypto/rand` and the host timezone, as quickjs-wasi's WASI shim gives Pi's worker.
- The codemode renderer was unreachable (keyed by tool name, which the renderer gate does not list). The tool definition now carries `codingagent.CodemodeRenderers`, as upstream's definition spreads `codemodeRenderers`. The renderer now cuts arguments in UTF-16 units (the rune slice panicked), sanitizes the output and places image fallbacks as `getTextOutput` does, resolves `app.tools.expand`, and flags `[invalid arg]` only for a non-string `code`.
- Search limits beyond the int range, a rejected nested call's row status, U+FFFD in rendered enum values, and the trimming of script stacks now match upstream. The terminal-kind lifetime test counts hard stops per run.
