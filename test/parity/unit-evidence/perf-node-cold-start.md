# Node extension cold-start evidence

Baseline: `4f0b9a685`. Reference: published Pi 0.87.1. Toolchain: Go 1.27.1, Node 24.19.0, Linux amd64, Intel Xeon 6746E. This change removes unnecessary Node startup work. It adds no extension API, wire shape, divergence, cache, timeout, retry, or performance allowance.

## Cause and correction

`runtime.mjs` imported the independent Session cleanup function from a module that re-exported the complete SDK. The editor helper also imported the SDK root for one theme helper. Separately, `jiti-loader.mjs` imported every virtual namespace before loading any extension. All three edges had to be removed: fixing either side alone merely moved the same work to another startup phase.

- Keep connection-owned pending/live Session bookkeeping in `runtime-node/independent-session-owner.mjs`. SDK creation still uses the same owner, pending set, abort/dispose ordering, closed-owner rejection, and failure propagation. Cleanup does not import an unused SDK.
- Import the editor's theme helper from the theme leaf already used by the runtime.
- Give jiti getter-backed virtual modules. Native `require(ESM)` loads the exact module on first access; Node retains the evaluated module. Extension factories still use jiti with `moduleCache: false` and `tryNative: false`. No extension source or dependency is scanned to guess its imports.

Pi contracts retained:

| Contract | Exact Pi source |
|---|---|
| Await module import and factory completion; disable jiti's extension module cache | `packages/coding-agent/src/core/extensions/loader.ts:491-510,560-580` |
| Current/legacy virtual aliases share exported values | `packages/coding-agent/src/core/extensions/virtual-modules.ts:14-38` |
| Independent Session construction, rather than prompting the parent Session | `packages/coding-agent/src/core/sdk.ts:175-439` |
| Theme initialization and selection helper | `packages/coding-agent/src/modes/interactive/theme/theme.ts:774-786,1209-1218` |
| EventBus subscription, dispatch and unsubscribe | `packages/coding-agent/src/core/event-bus.ts:12-32` |

Jiti itself wraps each imported namespace in a distinct interop proxy, including in real Pi. A direct oracle probe returned `namespaceIdentity: false, exportIdentity: true`. The regression checks every exported value across aliases and against the native module, not identity of those jiti proxy objects.

## Red and green

`TestNodeColdStartLoadsSDKOnlyOnImport` fails before the fix with `unused SDK closure loaded before an extension imports it`. Its load hook rejects the SDK bundle while constructing the runtime, importing and invoking a light TypeScript factory, re-importing that factory, and cleaning up a connection that never used the SDK. It then permits SDK loading and checks lazy jiti imports, complete exported-value identity, and native alias identity. It passes after the fix.

The existing EventBus scenario fails before the fix at its unchanged 2.0× ceiling. Twelve alternating-order pairs give PiG median 1,787 ms and Pi median 845 ms: **2.11×**. After the fix the same twelve-pair run gives 1,282 ms and 841 ms: **1.52×**. This run preserves the scenario's launch-order alternation.

A separate distribution run executes twelve fresh-home single-pair tests for each binary. The runner snapshots both homes and the working directory for each pair. These are process/artifact-cold starts, not OS page-cache eviction. No compile cache is enabled. p95 uses the nearest-rank definition; with twelve observations it is the maximum.

| Binary | Before p50 | Before p95 | After p50 | After p95 |
|---|---:|---:|---:|---:|
| PiG | 1,783 ms | 1,844 ms | 1,336.5 ms | 1,372 ms |
| Pi | 874.5 ms | 914 ms | 871 ms | 913 ms |

The p50 ratio improves from **2.04× to 1.53×**, leaving **23.3% margin** under the unchanged 2.0× ceiling. The p95 ratio is 1.50×. Every after pair is below 1.61×. The single-pair distributions are supplementary to the alternating-order twelve-pair gate.

Raw observations, in run order, in milliseconds:

```text
before pig: 1739 1713 1806 1844 1783 1783 1804 1805 1800 1777 1717 1714
before pi:   840  897  914  909  850  899  851  854  903  843  895  837
after pig:  1234 1292 1288 1349 1336 1337 1355 1228 1287 1359 1372 1368
after pi:    848  846  912  853  913  847  847  903  846  908  889  912
```

The EventBus comparator is tightened from normalized output equality to escaped-output equality. Its asserted line has no unstable fields. No crop, timeout, or threshold changes.

## Attribution and rejected candidates

A separate Node probe imports `runtime.mjs`, then calls the production `importExtension` on a light factory. Each row uses twelve new Node processes. Timings exclude Node's pre-main/bootstrap interval, unlike the CLI scenario above.

| Candidate | Runtime import p50 | Extension import p50 | Total p50 / p95 | Decision |
|---|---:|---:|---:|---|
| Baseline | 628.27 ms | 8.71 ms | 637.09 / 668.52 ms | Eager SDK cost |
| Only ownership split and theme leaf | 180.10 ms | 464.49 ms | 644.65 / 685.46 ms | Jiti still loads the unused graph |
| Only lazy jiti getters | 662.75 ms | 4.02 ms | 666.79 / 736.49 ms | Runtime still loads the unused graph |
| Combined fix | 176.57 ms | 3.42 ms | 180.01 / 203.08 ms | Ship; removes about 457 ms from this probe |
| Baseline without Node loader hook | 507.13 ms | 6.92 ms | 513.98 / 537.57 ms | Diagnostic only; removing it would break native/worker aliases |
| Baseline with `NODE_COMPILE_CACHE` | 540.25 ms | 8.72 ms | 549.15 / 672.00 ms | Not shipped; the empty-cache first run is 672 ms, so it does not fix cold startup |

The compile-cache probe uses one initially empty directory across twelve processes. Its warm improvement is smaller than removing the unused graph, and it introduces persistent cache/lifetime work without improving the initial cold run. No compile-cache configuration is added.

The existing esbuild bundle already tree-shakes and emits no source maps. A temporary copy with minification reduces its JavaScript from 1,715,403 to 945,471 bytes. With an extension that actually imports `ModelRuntime`, `SessionManager`, and `createAgentSession`, combined-fix startup measures 653.76 / 711.70 ms p50/p95; the minified copy measures 625.96 / 692.29 ms. This small improvement on the full-SDK path does not address the unused-SDK regression. No generated bundle, vendor source, or manifest is changed.

There is no Go-side startup concurrency change. The measured host handshakes are small relative to module evaluation, and preserving configured factory admission order is more valuable than overlapping unrelated startup work. Existing `52-interleaved-node-admission` and admission-order tests remain green.

## Profiles and resources

Node `--cpu-prof` samples before/after identify the removed work:

| Main-thread self sample | Before | After |
|---|---:|---:|
| `makeSyncRequest` to the loader thread | 264.7 ms | 65.0 ms |
| `compileSourceTextModule` | 132.7 ms | 48.8 ms |
| `wrapSafe` | 41.6 ms | 12.7 ms |
| Garbage collection | 36.2 ms | 6.5 ms |

These are diagnostic sampled profiles, not the uninstrumented latency distribution. `NODE_DEBUG=esm` records 458 translation lines before and 103 after. The source/SDK boundary guard supplies the deterministic regression test; trace counts are observations, not magic-count assertions.

The same light-factory probe's median RSS falls from 126,615,552 to 84,410,368 bytes. Median V8 used heap falls from 38,344,932 to 12,807,848 bytes. The lazy table retains only fixed specifier getters. Node retains imported namespaces for the life of the cell, as before. No new thread, process, timer, socket, file descriptor, or persistent cache is introduced. The ownership WeakMap does not retain dead runtime keys. Existing independent-Session lifetime, reload, provider, and subprocess shutdown tests exercise cleanup.

`BenchmarkNodeAdmissionStartup`, twelve measured iterations with CPU and allocation profiles, covers production host startup and shutdown after materialization:

| Composition | Before ns/op | After ns/op | Before B/op / allocs | After B/op / allocs |
|---|---:|---:|---:|---:|
| One Node factory | 968,719,229 | 480,558,074 | 132,479 / 525 | 132,736 / 528 |
| Three Node factories | 1,039,988,193 | 577,080,760 | 179,392 / 855 | 179,737 / 850 |

Go allocation profiles include host construction and startup preparation; they do not count Node allocations. The gain is subprocess work removed, not a Go allocation optimization.

`strace -f -ttt -T` of that production benchmark captures Go spawn, socket accept, register and ready boundaries. In two instrumented launches per version, real Node `execve` to socket connect falls from 1,054/1,115 ms to 385/411 ms. Connect to register is 2.1/2.4 ms before and 2.1/2.3 ms after. Register to host ready is 1.2/0.9 ms before and 1.0/1.0 ms after. Tracing also exposes the machine's mise wrapper before real Node `execve`; those wrapper and tracing costs are not attributed to the SDK. No production instrumentation is added.

## Verification

- `go test ./coding/extension/host/subprocess -v -count=1 -timeout=20m`: pass. The first shell invocation was stopped at the tool's 600-second budget without a result; the complete package takes about 753 seconds. No test timeout is changed.
- Full `extensions-runtime` family: pass at declared durability, including EventBus's performance assertion, independent Session scenario 53, overlay scenario 54, and provider carrier scenario 55.
- `make parity-fast`: 318 of 319 executed scenarios pass. `interactive-rendering/37-auto-theme-dark-modal-notification` fails because `Pick a color` is absent from **both** hosts' captures. A baseline-binary re-probe reproduces the same two-sided failure. This out-of-scope scenario remains an integration blocker; it is not skipped or weakened. The two existing allowed skips are unchanged.
- `go test ./... -timeout=20m`: all touched packages and cross-SDK conformance pass. The three inherited `test/parity/closure` approval/accounting assertions fail, as recorded by the base lane. Three Piglet Binary tests initially inherit the coding-agent harness's regular-file `trust.json.lock`; running `coding/pigletbuild` with an ephemeral `PIG_HOME` and without ambient `PIG_CODING_AGENT_DIR` passes. No user's lock or test assertion is changed.
- `go vet ./...`, `GOOS=windows go vet ./coding/extension/host/subprocess`, `go tool golangci-lint config verify`, touched-package lint, and `make lint`: pass without suppressions. `make lint-changed` needs `LINT_BASE=4f0b9a685` because this lane has no local `main` merge base; that invocation passes.
- Focused race tests for lazy loading, independent Session ownership/creation, reload, and EventBus: pass. Source hygiene, divergence guard and SDK surface drift: pass.
- `make ci-contracts`: reaches the inherited open hot-path test obligations and fails `test-porting-release`. Generated interface drift and preceding contract checks pass.
- `make ci-drift`: scenario lint, PORT_MAP drift, coverage drift and divergence consistency pass; the inherited unapproved D77/D78 records fail divergence quality. No approval is fabricated.

## Replay

Retained local artifacts are under `tmp/perf-node-cold-start/`: distributions, baseline/green logs, Node CPU profiles and module traces, Go CPU/allocation profiles, syscall traces, and complete gate output. The initial direct test-binary invocation also ran an unrelated fixture test from the wrong working directory; its EventBus measurements are retained, but that command is not represented as an overall passing test. Subsequent distributions use the anchored test name and correct directory.

Build one binary at each revision, then run from the repository root:

```sh
export PATH="$HOME/flakes3-tools:$PATH"
export PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi"
export PIG_PARITY_PIG_BIN="<absolute candidate binary>"
go test -tags=parity ./test/parity/runner -count=12 -v \
  -run '^TestParity$/06-eventbus-pubsub$' \
  -args -pig-parity.serial=true -pig-parity.runs=1
# Alternating-order, twelve-pair gate:
go test -tags=parity ./test/parity/runner -count=1 -v \
  -run '^TestParity$/06-eventbus-pubsub$' \
  -args -pig-parity.serial=true -pig-parity.runs=12

go test ./coding/extension/host/subprocess -run '^$' \
  -bench '^BenchmarkNodeAdmissionStartup/(one-node|three-node)$' \
  -benchtime=12x -benchmem -cpuprofile=cpu.pprof -memprofile=alloc.pprof
make parity-family FAMILY=extensions-runtime
```

Coordination: the carrier lane acknowledges the lazy virtual-module boundary, cleanup split, and theme-leaf import. Its subsequent provider callback work does not introduce eager SDK imports. The only production overlap in `runtime.mjs` is the cleanup import; preserve it when joining the lanes. Full repository release approval remains blocked by the explicitly listed unrelated gates.
