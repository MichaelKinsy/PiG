# 0.3.0 startup budget: root cause and fix

The advisory startup job reported the no-extension workload at 1.142 (warm) and 1.240 (cold) of v0.2.0 against a 1.05 budget. The budget stays at 1.05. This note records what the profile found and what changed.

## Method

- `automation/perf/startup_timing.py` measures the time to the first assistant `message_start` from a JSON-mode print run, interleaving builds. The host has 128 CPUs and a 4-CPU CI runner behaves differently: a 128-thread Go runtime pays for every garbage-collection cycle. All numbers below pin the harness to four CPUs with `taskset`.
- CPU profiles came from `PIG_PROFILE=cpu` over 100 cold runs per build. `GODEBUG=inittrace=1` gave package initialization time, bytes, and allocations. `PIG_STARTUP_TRACE=1` gave the phase timeline.
- The host is shared, so single runs vary by 5 to 10 percent. The tables use medians of 60 interleaved runs and repeat on separate CPU sets.

## Root cause

There was no single bug. Four costs added up, three of them specific to 0.3.0:

| Cost | Where | Per run |
|---|---|---|
| 177 package-level `regexp.MustCompile` calls in linked packages | package init | 2.7 ms, 1.1 MB |
| Catalog materialization: a throwaway `Model`, level list, and map copy per entry to pick `MaxThinking`, plus a reflection index slice per field in every compat clone | `NewServices` | about 1.8 ms of 3.7 ms |
| First-start SDK staging: a temp file, `chmod`, rename, and failed remove per file (71 files, three languages), then hashing every staged tree to prune an empty cache | `EnsureSyncedContext` | 8 to 10 ms cold, about 5 ms after |
| First-start docs staging: the same per-file dance | `pigdocs.Sync` | 2.0 ms cold, 1.6 ms after |

The ci-rc2 estimate of 4 ms for `ToModel`/`cloneCompat` was right about the size, but 0.2.0 does comparable work in `RuntimeModels` (8 ms of CPU there against 4.5 ms here), so the catalog does not explain the ratio by itself. The cold SDK staging and the init work do.

## Changes

- `internal/lazyregexp` compiles a pattern on first use with `regexp.MustCompile` semantics. Every package-level pattern in the linked packages uses it. Package initializers of this module went from 2.0 MB in 18140 allocations to 0.83 MB in 9701.
- `ToCapabilities` selects `MaxThinking` without allocating; `scalarjson.CloneFields` reads fields with `Field(i)`. Allocations per catalog model fell from 12.1 to 9.7.
- SDK and docs staging build a complete tree beside the target and rename it into place; the marker is the last file. `Prune` skips fingerprinting when the cache has no entries, and the embedded-SDK marker hash streams files instead of copying them.

Behavior is unchanged. Pi's `create()` still awaits the refresh; per-provider auth checks, their ordering, and their errors are untouched.

## Results

Job harness, no-extension workload, four CPUs, medians of 25 runs per cell:

| Revision | Warm ratio | Cold ratio |
|---|---:|---:|
| ci-rc2 (`3044f3e3d`) | 1.121 | 1.234 |
| this change, run 1 | 0.970 | 0.969 |
| this change, run 2 | 1.022 | 1.044 |

Interleaved medians of 60 runs, three CPU sets, this change: warm 1.018, 1.009, 1.022; cold 1.019, 1.007, 1.033. ci-rc2 measured 1.133, 1.058, 1.112 warm and 1.287, 1.161, 1.193 cold in the same runs.

## Not done: third-party package initializers

Two dependencies initialize eagerly and account for most of what remains of package init. Both are shared with v0.2.0, so they do not cause the ratio, but they are the largest remaining startup cost.

| Package | Init time | Reason |
|---|---:|---|
| `github.com/alecthomas/chroma/v2/lexers` | 10 to 11 ms | Parses about 250 embedded XML lexers in a package variable initializer. |
| `github.com/santhosh-tekuri/jsonschema/v6` | 5 to 6 ms | Compiles every draft's metaschema in `init`. |

Building without the chroma lexers takes `--version` from 26 ms to 19 ms. Deferring either package needs an in-tree fork with a lazy registry: the module cannot be replaced (`go install` rejects `replace`), a nested module needs its own release, and the fork trips `go vet` composites on chroma's unkeyed `Rule` literals. That is a dependency-policy decision, so it is left open.

## Guards

`make startup-proxies` runs, by name and without wall-clock thresholds:

- `TestPackageInitWorkBudget`: bytes and allocations of this module's package initializers, from the runtime's init accounting.
- `TestLinkedPackagesCompileNoRegexpAtInit`: no package-level `regexp.MustCompile` in any package linked into `pig`.
- `TestLazyRegexpPatternsAllCompile`: every lazy pattern compiles, since the init-time panic moved to first use.
- `TestToModelCatalogAllocationBudget` and `TestToCapabilitiesMatchesSupportedThinkingLevelSelection`.
- The SDK staging tests in `coding/extension/pigsdk`.
