# Go practices for PiG performance work

This document states how PiG uses Go and TinyGo for performance work. It covers the toolchain, language and library practice in each area, the cost differences between TinyGo and standard Go in Durable, the gates that hold performance, and the order in which to take performance work. Pi's observable behavior is the limit: a performance change that alters any output Pi produces is a parity defect, not an optimization.

Read [TypeScript-to-Go porting](../typescript-to-go-porting.md) first for the semantic rules and for the performance-proof requirement. This document does not repeat them.

## 1. Toolchain

### Version pins

| file | role |
|---|---|
| `.go-version` | The Go release that CI, the release workflow and the CI images use. Every `actions/setup-go` step reads it, and CI checks that `go env GOVERSION` equals it. |
| `go.mod` `go` line | The language floor (Go 1.26). Do not raise it to match the build toolchain. |
| `go.mod` and `go.work` `toolchain` line | Equal to `.go-version`. A Go older than the pin with `GOTOOLCHAIN=auto` requests the pinned toolchain; where Go does not publish one (Termux), build with `GOTOOLCHAIN=local`. |
| `automation/dev/toolchains.lock` | The archive and digest that `automation/dev/setup.sh` uses when go.dev is unreachable. |
| `automation/images/*` | CI images, pinned by digest. |
| `coding/pigletbuild/container_default.go` | The container image that builds Piglet Binaries, the CI Go image base. |

Rules:

- Build and test with the `.go-version` release. A local toolchain older than `.go-version` can disagree with CI, because the standard library's JSON engine and its security fixes change in patch releases.
- Scripts that pin a toolchain with `GOTOOLCHAIN` read `.go-version`, as the Durable scripts (`durable/core/budget/build-wasm.sh`, `durable/core/contracttest/gate.sh`, `durable/core/host/cf/check.sh`, `durable/core/probe/build.sh`) do. `make compliance` rejects any other Go release in a build, CI, image or setup file. A count baseline that depends on the toolchain (for example exact Wasm instruction counts) is regenerated in the same change as the version bump.
- Nested modules (`extensions/sdk`, `piglets/standard`, the Porter extension, test fixtures) declare the same Go 1.26 floor. `third_party/` holds vendored upstream source as packages of the root module; lint, `go fix` and the JSON ratchet skip it.

### Experiments and build flags

- PiG sets no `GOEXPERIMENT`. The toolchain's default experiments apply: the Green Tea collector, size-specialized allocation and the JSON v2 engine behind the `encoding/json` API.
- Release binaries build with `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X …"` from an archive source without VCS metadata. The Android binary uses cgo with the NDK, because Termux requires it. Piglet Binaries build with `-buildvcs=false -trimpath`.
- No build uses PGO. `make pgo` writes a merged CPU profile to `tmp/pgo/default.pgo`. Adopt PGO only with a controlled measurement on dedicated hardware.
- Architecture levels stay at the defaults (`GOAMD64=v1`, the base arm64 level). Do not raise `GOAMD64`: `v3` allows the compiler to fuse multiply-add on amd64.

### Fused multiply-add

The Go specification allows a compiler to compute `x*y + z` with one rounding. The Go compiler does this on arm64 at every level, on ppc64, ppc64le, s390x, riscv64 and loong64, and on amd64 at `GOAMD64=v3`. JavaScript always rounds the product first. A Pi-derived float that feeds a score, a sort order, a color or any printed number can therefore differ on arm64.

- Write an explicit conversion around the product when the result is observable: `total += float64(float64(n) * 0.1)`. The specification forbids fusion across an explicit conversion.
- Products of integers that are exact in float64 cannot change and need no conversion.
- `make test-fma-arm64` runs the `Fuzzy` oracle tests of the packages in `FMA_ARM64_PACKAGES` (default `./tui`) as a linux/arm64 binary under qemu. Other fusable Pi-derived sites have no oracle run on arm64.
- To list fused operations without qemu, cross-compile with the compiler's hash debugging: `GOOS=linux GOARCH=arm64 go build -gcflags='github.com/MichaelKinsy/PiG/...=-d=fmahash=y' -o /dev/null ./cmd/pig`. Each `fmahash triggered` line is one fused operation. The count is deterministic and suits a shrink-only gate. Do not ship `-d=fmahash=n`; it is a compiler debugging flag.

## 2. Language and library practice by area

### JSON

- With the Go 1.27 toolchain, `encoding/json` runs the JSON v2 engine behind the v1 API. A build at the Go 1.26 language floor (`GOTOOLCHAIN=local` with Go 1.26) runs the v1 engine, and no CI job tests that build. `extensions/sdk/json` is a copy of the v1 engine with WTF-8 handling of unmatched UTF-16 surrogates. It serves the extension wire, session entries, Durable storage and the Go SDK, and it keeps the SDK independent of the user's Go release. Two engines therefore run in one process, with different allocation profiles.
- Decode into typed structs. Keep members whose shape is open as `json.RawMessage` or a scanner, not as `map[string]any`. A decode into `any`, `map[string]any`, `[]any` or a type parameter builds a tree of boxed values.
- `make any-trap` counts those decodes against `automation/ci/anytrap/baseline.toml`. The counts may only shrink. The gate matches calls of `encoding/json` and `encoding/json/v2`; decodes through `extensions/sdk/json` need the same scrutiny in review.
- A faster encoder or decoder must keep Pi's JavaScript output rules where Pi's output depends on them: insertion key order, `-0` printed as `0`, non-finite numbers, invalid UTF-8 and last-wins duplicate names. Prove each rule with a test against Pi before replacing a codec.

### Provider streaming (`ai`)

- Accumulate streamed text in builders, never with `string +=` per delta.
- Resume partial tool-argument JSON from the last parsed offset (`ai/streaming_json_resume.go`); do not re-parse from byte zero.
- Publish partial messages through copy-on-write cells (`ai/assistant_publication.go`), not a deep clone per event.
- The SSE decoder (`ai/sse.go`) builds one string per line and copies the raw lines per event. Reuse the line bytes, and build the raw text only on the error path that prints it.
- `BenchmarkProviderStreamFrames` reports allocations and bytes per frame for each provider through the real HTTP client, decoder and builder. Output digests (`TestLongStreamOutputUnchanged` and its siblings) pin the event sequence, so an allocation change can prove it kept the output.

### Terminal UI (`tui`)

- Key matching caches parsed key identities; `tui/keystroke_allocs_norace_test.go` holds per-keystroke allocation ceilings for matching.
- Do not use `iter.Pull` on a per-keystroke path: each call starts a coroutine. A push loop with `break` keeps the same order.
- Render into reused byte buffers. Width functions must use Pi's Unicode tables.

### Iterators and generics

- Use `iter.Seq` where Pi hands out an iterator, and `iter.Pull` where Pi's control flow is a coroutine: `ai/js_promise.go` and `ai/tslib_async.go` run JavaScript async bodies this way.
- A method called through a type parameter goes through the generic dictionary and does not inline. Instantiate hot generic code with concrete types, not interfaces, and measure before relying on it.

### Standard library packages

- Prefer `slices`, `maps`, `cmp`, `min`, `max`, `for i := range n`, `strings.Cut` and `errors.AsType`. `make go-fix-clean` fails when `go fix` would change a file.
- Use `weak` and `runtime.AddCleanup` where Pi relies on object identity without retaining the object.
- `unique.Handle` interning has no measured caller; add it only with a profile.
- `sync.Pool` suits buffers shared across goroutines with a bounded size class, as the extension frame buffers do. Bounds that Pi does not have are recorded in `docs/additive-features.md` (D112). Do not use the `arena` experiment.

### Concurrency and lifetime

- An awaited Promise maps to a blocking call. An unawaited Promise maps to an owned goroutine with cancellation, error reporting and shutdown draining. Prefer `sync.WaitGroup.Go` for joined work.
- Use `context.WithCancelCause`, `context.AfterFunc` and `context.WithoutCancel` to carry Pi's abort semantics.
- Tests use `testing/synctest` for time and ordering. `make test-race` runs the TUI loop race tests and checks the `goroutineleak` profile. The `agent`, `ai` and `coding/extension/host/subprocess` test binaries fail when any test leaks a goroutine (`testenv.RunWithoutGoroutineLeaks`); the profile finds leaks only when it is written, so use `testenv.LeakedGoroutines`, never `Count` after `runtime.GC`. Run `-race` on every package you change.
- `make divergence-guard` rejects dropped sends, swallowed errors and invented limits without a recorded reason.

### Errors and diagnostics

- Use `errors.Is`, `errors.AsType` and `errors.Join`; do not compare errors with `==` or classify them by message text.
- `PIG_PROFILE` (`internal/profiling`) writes CPU, heap, allocation, block, mutex and goroutine profiles and an execution trace. `internal/codingagent/exit_record.go` sets `debug.SetCrashOutput`. A `runtime/trace.FlightRecorder` dump next to the crash record would add the last seconds before a crash; it must stay opt-in.
- PiG prints what Pi prints. It does not add structured logging to user output.

### Processes and HTTP

- Start a child that can have children in its own process group on Unix, and kill the group: the bash tool, extension subprocesses, MCP stdio servers and Durable environment processes do this. On Windows, kill the tree as Pi does (`taskkill /T /F`, `packages/coding-agent/src/utils/shell.ts` `killProcessTree`). The bash tool and extension subprocesses also hold their children in a job object; MCP stdio servers and Durable environment processes use `taskkill` only.
- `exec.Command` without a context is acceptable only where the caller owns cancellation, as `coding/extension/exec.go` does to match Node's spawn.
- Provider clients share one transport recipe (`ai/http_transport.go`): HTTP/1.1, Undici's connect budget and idle deadlines. Each provider value owns its transport and connection pool. Pi installs one global Undici dispatcher (`packages/coding-agent/src/core/http-dispatcher.ts` `configureHttpDispatcher`), so PiG does not reuse connections across a provider change, and the idle connections of a replaced provider close only at the idle timeout.

## 3. TinyGo and standard Go in Durable

The Durable core compiles from one source three ways: native Go, Go for `wasip1`, and TinyGo for `wasip1` with `-scheduler=none` (`durable/core/budget/build-wasm.sh`). `durable/core/purity_test.go` keeps the core free of reflection, `encoding/json`, `fmt`, OS, clock, `sync`, `runtime` and `unsafe` packages, goroutines, channels and package-level mutable state. `make durable-contract` runs the conformance corpus against every build. The TinyGo build uses its precise collector and the default `-opt=z`.

Costs that differ:

| pattern | standard Go | TinyGo (wasm32) |
|---|---|---|
| boxing a value into `any` | allocates unless the value is pointer-shaped, zero-sized, a single byte, a small integer or a constant; escape analysis can keep the box on the stack | allocates for any value wider than a pointer (4 bytes): `float64`, `string`, slices |
| `string(b) == lit`, `bytes.CutPrefix(b, []byte(lit))` | no allocation | allocates |
| `slices.Grow` | allocates only the new backing array when capacity is short; the compiler removes the `append(s, make(...)...)` temporary | also allocates a zeroed temporary of the added length |
| slice growth | size-class rounding | rounds to the next power of two |
| maps | Swiss tables, scanned precisely | bucket arrays without pointer layout, scanned conservatively |
| interior pointers | constant-time span lookup | finding the object head walks block metadata in proportion to object size |
| collection trigger | paced by `GOGC` | collects when the heap is full |
| closures | an escaping closure allocates | an escaping closure allocates, and captured variables move to the heap more often |
| compute | the Go Wasm backend runs many more instructions | fewer instructions; LLVM optimization |
| module size | several MiB | about one MiB |

Rules for code that both builds compile:

- Keep retained state as bytes, offsets and integers, not `any` trees.
- Compare bytes in place; do not convert to compare.
- Size slices exactly with `make` and `copy`; avoid `slices.Grow` and large `append` growth.
- Avoid maps in retained state and in the per-step path.
- Avoid closures and captured variables in the per-hop path; return integers and write into a step arena.
- Never build strings with `+=` in a loop.
- `tinygo build -print-allocs=<regexp>` lists every heap-allocating site at compile time. Its count for the core packages is deterministic and suits a shrink-only gate. TinyGo's `runtime.MemStats.NumGC` gives collections per turn without instrumentation.

## 4. Gates

Hold performance with counts. A count does not depend on machine load, so it can gate every merge. Time measurements belong on dedicated hardware with a variance check.

| area | gate | command or file |
|---|---|---|
| package initialization | bytes and allocations of this module's initializers and of every linked initializer | `coding/cli/startup_init_budget_test.go` |
| startup work | skill-ignore work ratio, warm standard startup with the SDK lock | `coding/cli/startup_budget_test.go` |
| startup time | 25-trial comparison with the previous release and Pi (advisory) | `.github/workflows/startup-performance.yml` |
| eval budgets | wall time, memory and request-size ceilings | `make perf-check`, `test/evals/budgets.toml` |
| JSON decodes into `any` | shrink-only baseline | `make any-trap` |
| keystroke | allocations per key match | `tui/keystroke_allocs_norace_test.go` |
| auth and catalogs | allocations of unchanged reads and cached projections | `ai/auth_read_allocs_norace_test.go`, `ai/registry_allocation_test.go`, `internal/codingagent/extension_catalog_*_test.go` |
| invented limits, dropped events | shrink-only baseline | `make divergence-guard` |
| idioms | `go fix -diff` is empty | `make go-fix-clean` |
| benchmarks | median comparison with a base run | `make bench`, `make bench-compare` |
| races and leaks | TUI loop race tests, `goroutineleak` profile | `make test-race` |
| arm64 float parity | Fuzzy oracle tests under qemu | `make test-fma-arm64` |
| Durable core | conformance on native, Go Wasm and TinyGo Wasm; size and instantiation budgets | `make durable-contract`, `durable/core/budget/budget.json` |

Gaps in the gates:

- Provider streaming has allocation benchmarks but no ceiling on allocations per frame.
- The keystroke ceiling covers key matching, not input through the editor to a rendered frame.
- No gate counts session append allocations per entry or session open allocations per megabyte.
- No gate counts fused multiply-add operations on arm64 or TinyGo allocation sites.
- Most benchmarks report allocations without a committed ceiling.
- `make any-trap`, `make bench-compare`, `make perf-check`, `make test-fma-arm64` and `make durable-contract` are not part of a CI shard.

## 5. Order of work

Rank performance work by expected gain times safety. Parity is the limit for every item: name the test that proves the output did not change.

1. Put an existing count gate into CI before writing a new optimization.
2. Widen a gate to the code it misses, such as decodes through `extensions/sdk/json`.
3. Fix float expressions that can fuse where Pi rounds the product, and gate the fused-operation count.
4. Add a ceiling at today's value, then lower it. A new ceiling starts at the measured count and only shrinks.
5. Remove per-frame and per-keystroke allocation: typed decode of provider frames, reused SSE line bytes, render buffers.
6. In Durable, gate collections per turn and TinyGo allocation sites, then remove closures and boxing from the hop path.
7. Decide PGO, GC policy (`GOGC`) and toolchain experiments only with measurements on dedicated hardware.

Do not adopt: the `arena` experiment, `GOAMD64=v3`, a default `GOMEMLIMIT`, JSON code generators that change output rules, or `-d=fmahash=n` in release builds.
