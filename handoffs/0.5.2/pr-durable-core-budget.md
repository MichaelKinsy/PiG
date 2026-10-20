# `make durable-core-budget` red at the allocation-site check

Branch `fix/durable-core-budget-main`, from `main` 94e6796e8d (the same fix is on `fix/durable-core-budget`, from staging `next`
d9173cb277; the `durable/core` Go sources and the budget files are identical on the two bases).

## Result

`node allocs.mjs --check` fails with 10 packages over `durable/core/budget/allocs.json`. It is red on `main` (94e6796e8d; the
same counts at e84d2349ae and at the 0.5.0 release commit 6af0806f7a) and on staging `next`, with identical counts: `next` did
not cause it. The size check (`measure.mjs`) passes and is unrelated.

`allocs.json` was written once, on 12 Oct (95e205c358, "measured on integrate-042 6449cd87c2"). It was correct for that tree.
The Pi ports that reached the Durable core afterwards (nested tool calls, the scheduler rewrite, document retention, sleeping
tasks, crash-loop guard, task paging, watch ops) merged with no re-measurement. Nothing ran the gate: no workflow, `.mk` file or
script other than the `durable-core-budget` target called `allocs.mjs`, so the drift went unseen from the first merge on. This
branch adds the gate to CI (below).

The gate counts TinyGo's escape-analysis report for `cmd/corewasm` over the `PiG/durable/core` packages
(`CORE_PRINT_ALLOCS=PiG/durable/core`). It depends only on the Go sources and the TinyGo pin (0.42.0). Linking the pinned Pi
packages into the Cloudflare host bundle (9f877fbf74) changes the host bundle, which `measure.mjs` measures, and cannot change
this count.

## Bisect

Counts are TinyGo allocation sites per package, measured on imladris in `tern-ci:v2` with the TinyGo from
`tinygo-install.sh`. "red" means at least one package is above the 95e205c358 baseline.

### `next` since main e84d2349ae: no change

| commit | what | `.` | session | turn | other |
|---|---|---:|---:|---:|---|
| e84d2349ae | main | 684 | 522 | 709 | abi 16, docs 6, history 109, inbox 21, internal/delta 101, jv 43, rec 9, stdlib 17, store 61 |
| 71b4b1e3b6 | checkpointWhen (Pi 5c1736f8) | 684 | 522 | 709 | same |
| 333229c85a | HTTP 413 overflow | 684 | 522 | 709 | same |
| 9be819918e | overflow patterns | 684 | 522 | 709 | same |
| 933b14991e | merge main into next | 684 | 522 | 709 | same |
| 9f877fbf74 | Cloudflare host against the pinned Pi durable build | 684 | 522 | 709 | same |
| ecfe66518d | Harness.wake, retryTask, telemetry option | 684 | 522 | 709 | same |
| d9173cb277 | `next` tip | 684 | 522 | 709 | same |

The only non-test `durable/core` Go commits in the range are checkpointWhen (71b4b1e3b6), 413 (333229c85a) and the overflow
citations (9be819918e); all three are measured above, and so are the merge, the pinned-build commit and the tip.

### When it went red on the line `main` was built from

`main`'s history is release squashes (the first Durable core on `main` is 9080668e4a, "PiG 0.5.0"). The commit-level history
is the staging line `6449cd87c2 .. 42116f895b` (its `durable/core` tree is identical to 9080668e4a) and, after it, `main`'s own
commits (a5489d61a2 has the same `durable/core` tree as 6af0806f7a). `git bisect run` over `95e205c358 .. 2352005a7c`:

| commit | `.` | session | turn | other |
|---|---:|---:|---:|---|
| 95e205c358 baseline written | 617 | 390 | 636 | abi 16, docs 4, history 107, inbox 19, internal/delta 96, jv 32, rec 9, stdlib 15, store 59 |
| d3f0fce358 | 617 | 390 | 636 | same as baseline (green) |
| **8a1578a5bb** merge lg-help-1 into integrate-042 | **618** | 390 | 636 | **first red**: +1 site |
| 4f20ac1743 int-120 (Pi 1.2.0 flip) | 626 | 470 | 685 | docs 6, inbox 21, store 60 |
| 2352005a7c / 42116f895b | 631 | 487 | 696 | docs 6, inbox 21, store 60, history 107 / 109 |
| 9080668e4a PiG 0.5.0 on main | 631 | 487 | 696 | history 109 |
| 936c1f2d0f | 634 | 487 | 698 | |
| 3bf9c35b64 sleeping tasks | 637 | 483 | 697 | store 61 |
| 3cc6f3fa25 | 655 | 522 | 709 | |
| 8883aa8151 | 684 | 522 | 709 | internal/delta 101, jv 43, stdlib 17 |

The first red is the merge 8a1578a5bb: `checkArguments` takes `args any` so a non-object `prepareArguments` result reaches
validation as it is (19e834a869, 4f6992d663; Pi tool.ts:138-154), plus a closure to narrow it back; +1 site in `turnhost.go`. Its
first parent d3f0fce358 and the merged branch 009d02672b are each green or unmeasurable: the bisect runs on 009d02672b,
d989ff1234 and 73cfd740d4 printed no sites, because those trees predate `CORE_PRINT_ALLOCS` (a9580673c4) and count as
"good" only vacuously. The +1 is therefore a property of the merge result against `d3f0fce358`, which does print counts.
The remaining 10 steps of the bisect only cross merges of the same line. Everything after it is feature growth.

## Cause per package

Method: for each site at the tip I took the sites present at the tip and not at 95e205c358 (matching file, source line text and
reason, ignoring line numbers), and `git blame`d them on the commit-level line (a5489d61a2 for the post-squash commits,
42116f895b for the squash content). "Gross" counts new sites; the net change also loses the sites a rewritten function no longer
has (132 sites were removed or moved by the same commits), so gross exceeds net.

| package | was | now | net | gross new | what brought them |
|---|---:|---:|---:|---:|---|
| `.` | 617 | 679 | +62 | 100 | Storage-override tx reads, 9 (501b262aa7, Pi 42a3497d); published notices written in `abi.Chunks`, 9 (546246310f); observer ticks 4, chain-load reads 4, host-driven commit duration 4; non-object `prepareArguments` 3 (4f6992d663); `ownedTasks` 2, `checkStructuredOutput` 2; crash-loop guard, report info, failed-run inbox and blocked-task graph ops, 20 (da0fae4095, Pi a30c2a81..482c1381); `ScanTasks`/`ScanConversations` paging closures, 17 (d34376d0fc); watch ops publish 5 (dbd405f7f6); sleeping tasks and timer classes 4 (3bf9c35b64); `compaction.modelOverrides` 3 (2eac74eed8); AsyncLocalStorage continuation context 2 (b7ab092396). Net includes -5 from this branch's tx.go fix. |
| `abi` | 16 | 16 | 0 | 0 | |
| `docs` | 4 | 6 | +2 | 2 | deferred tools and `tool_search` (35372ce6b3, Pi 77332f7a): the add/remove lists of `addTools` |
| `history` | 107 | 109 | +2 | 4 | a compaction that keeps assistant messages leads with the system baseline (42116f895b, Pi 5713c950): the ordered/settled lists are sized per call |
| `inbox` | 19 | 21 | +2 | 2 | inputs placed as application entries (d0006cbfb7, Pi e6b9a6af) and the harness types port (75ffdce204) |
| `internal/delta` | 96 | 101 | +5 | 2 | the tracker's `Members` walk for an index-retiring delete (f29cbeb92f); the rest are moved sites |
| `jv` | 32 | 43 | +11 | 13 | an indexed object's delete retires the slot and ordered keys sort in O(n log n) (f29cbeb92f): `Clone`/`Delete` copy the slot arrays, `Equal`/`Members` walk with a closure |
| `rec` | 9 | 9 | 0 | 0 | |
| `session` | 390 | 522 | +132 | 180 | scheduler reconcile visits the tasks it concerns, 20 (293e707b08, Pi cb437549); task scheduler 13 (b1dad58765); restart marks 12 (7e566ee4a6, Pi eba84973); wait-cycle rejection 10 (9fbdb006c0, Pi eb2fce43); abort/seal 9 (6e6e23b193); nested calls 7 (642be97925) and `ownedTasks` 5 (a1b56a53fd, Pi 4ac0bd8c); failed commits fail the Session 6 (411d820c3c); document pin/retention (a8d9efde84, 27dacca24a, Pi f44707e7); crash-loop guard, failed-run inbox, report info 42 (da0fae4095); sleeping tasks and wake 13 (3bf9c35b64) |
| `stdlib` | 15 | 17 | +2 | 2 | `slices.Replace` over `[]viewEntry` in view.go (dbd405f7f6), the only new `slices.Insert/Grow/Replace` use in the range; a generic instantiation holds the sites [INFERENCE: not separated by an experiment] |
| `store` | 59 | 61 | +2 | 5 | request errors for a bad storage read (089ee53b5a), schema version 2 admits the sleeping task status (76bd2d518c) |
| `turn` | 636 | 709 | +73 | 103 | nested tool calls 28 (642be97925, Pi 42a3497d `executeTool`), one document per nested call 9 (e4a06b16e0), the `pi.tool` task phases 6 (bb27b3add7), tool output field 5, blocking-compaction failures 4 (32ae04cc76, Pi 89e5c2c0), settle hops 4+3, fused host crossings 25 (9e60a423f4), sleeping tasks 3, `modelOverrides` 1, HTTP 413 1 (333229c85a) |

Every group is the closure for an asynchronous continuation (`then func(...)` handed to the host or to the line), a result or
error record the ported behaviour returns, or a local a closure captures. TinyGo puts those on the heap by construction. The sites
the ports replaced (the old `Sleep` closures, the old `Abort` closures) are already netted out. Reading the new-site list, I found
no site avoidable without redesigning the continuation-passing seam, except the one below.

## Source fix

`publish` (tx.go) formatted the sequence number and every record id with `jv.Append(item, float64)`, which boxes the float into
an interface: one heap object per published record per commit. `jv.Append`'s `float64` case is `AppendNumber` behind a NaN/Inf
check; the values are record ids and `pub.Seq`, which are finite. The five call sites now call `jv.AppendNumber` directly:
`.` goes 684 -> 679, and a commit with N published records allocates N+1 fewer objects.

## CI

`ci.yml` job `durable-cf-host` already installs the pinned TinyGo (`tinygo-install.sh`) and the locked npm tree, so it now runs
`make durable-core-budget` as a last step. `test/ci-images/durable_core_budget_test.go` fails when that job has no `run:` step
of the target, when the step comes before the TinyGo install, or when the Makefile recipe stops running `measure.mjs --check`
and `allocs.mjs --check`; its checker has a negative control (missing step, misordered step, a comment naming the target).
The job's result already feeds the `verify` aggregate (`DURABLE_CF_HOST`), so a red budget blocks the merge.

## Budget

`allocs.json` is updated to the measured tip (`allocs.mjs --update` output, rewritten by hand with identical formatting):

| package | old | new |
|---|---:|---:|
| `.` | 617 | 679 |
| `abi` | 16 | 16 |
| `docs` | 4 | 6 |
| `history` | 107 | 109 |
| `inbox` | 19 | 21 |
| `internal/delta` | 96 | 101 |
| `jv` | 32 | 43 |
| `rec` | 9 | 9 |
| `session` | 390 | 522 |
| `stdlib` | 15 | 17 |
| `store` | 59 | 61 |
| `turn` | 636 | 709 |

The commit message of the baseline commit carries the same per-package justification. The gate keeps its meaning: a count may
only fall from here, and a package that rises is a new site to remove or justify.

## Verification (imladris, `tern-ci:v2`, cores 96-151, nice 15)

- `allocs.mjs` on `next` before the baseline update: the table above (tip `.` 679 after the fix).
- `go test ./durable/core/` passes with the tx.go change (17 s, on the `next` base; the file is identical on `main`).
- On this branch (`main` 94e6796e8d + 4 commits), with TinyGo on the environment (`TINYGO=$(sh durable/core/budget/tinygo-install.sh)`):
  `make durable-core-budget` exits 0: `measure.mjs` passes, `allocs.mjs` is equal to `allocs.json`, and `growheap.test.mjs` /
  `allocs.test.mjs` pass (16/16). Without `TINYGO` the target fails at `build-wasm.sh: tinygo not found`; that is the environment,
  which is why the CI step follows the TinyGo install.
- `go vet ./test/ci-images/` is clean and the whole `go test ./test/ci-images/` passes (87 s), including the two new tests.
- Not run: the rest of the repository's gates, and the new CI step itself on GitHub (it needs a pushed branch).
