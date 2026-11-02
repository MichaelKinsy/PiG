# Proof-class ratchet breakdown (for the owner decision)

Measured by `test/parity/interface-closure/breakdown/proofclass.py` (the script `make ledger-check` runs) on the merged head of `fix/follow-pi-next` (D5a + iface-close merged, tree `bfbf78ea36` plus the regenerated mapping), and re-run at four earlier points with the same script. The baseline file is `test/parity/interface-closure/gaps/proof-class-baseline.tsv` (written in the 0.5.0 squash `6af0806f7a`, 2026-10-19; nothing has changed it since). **The ratchet is report-only**: `proofclass.py` exits non-zero only with `--enforce`, and `make ledger-check` does not pass it, so ledger-check is green (exit 0) while every package but `agent` is above its baseline. The baseline is not raised by this document.

## 1. The count over baseline, per package, over time

Unproven closed rows (a closed row is `ported` or `designed-out`; "unproven" is the script's verdict).

| package | baseline | 0.5.0 `6af0806f7a` (same script) | 0.6.0 base `8dd8f4a8e4` | pin bump `0b5ddbd9e1` | next base `2345dc3fcf` | **head** | **over baseline** |
|---|---:|---:|---:|---:|---:|---:|---:|
| agent | 3 | 3 | 2 | 2 | 2 | 2 | -1 |
| ai | 79 | 93 | 94 | 94 | 94 | **94** | **+15** |
| chord | 17 | 225 | 119 | 113 | 185 | **180** | **+163** |
| coding-agent | 3 | 16 | 16 | 16 | 22 | **22** | **+19** |
| durable | 23 | 61 | 103 | 91 | 97 | **114** | **+91** |
| env | 6 | 10 | 10 | 10 | 10 | **10** | **+4** |
| mcp | 24 | 26 | 26 | 26 | 26 | **26** | **+2** |
| telemetry | 13 | 18 | 29 | 29 | 29 | **29** | **+16** |
| tui | 10 | 10 | 14 | 16 | 18 | **18** | **+8** |
| total | | 462 | 413 | 397 | 483 | **495** (of 13,953 closed rows) | |

Reading it:

* The baseline was never true for the tree that carried it. The same script at the 0.5.0 commit that wrote the baseline already reports ai 93>79, chord 225>17, coding-agent 16>3, durable 61>23, env 10>6, mcp 26>24, telemetry 18>13. So most of the "over baseline" is **not a regression of the follow work**: ai +15, coding-agent +13, env +4, mcp +2 are present at 0.5.0, and ai 94, env 10, mcp 26, telemetry 29 have not moved since the 0.6.0 base.
* What the next line moved since 0.6.0 base (`8dd8f4a8e4`): chord 119 → 180 (+61), durable 103 → 114 (+11), coding-agent 16 → 22 (+6), tui 14 → 18 (+4), agent/ai/env/mcp/telemetry 0. Between the next base `2345dc3fcf` and head: chord -5, durable +17 (iface-close), everything else 0. The D5a merge itself changed no unproven count apart from chord (it closed rows by pin-time bulk derivation, see 2).

## 2. Groups: which proof is missing, and the commits that introduced the rows

The "introducing commit" is `git blame` of the row's `disposition` line in `mapping-v1.4.0+e7a0df27.json` (the commit that last set the row ported/designed-out). Nearly every row was bulk-derived, so the commit is a generation commit, not a feature commit.

| group (class / missing proof) | rows at head | by package |
|---|---:|---|
| **F1** a function/class/callable row: tests touch the symbol, none is mutation-checked or a Pi-oracle test | 217 | ai 39, chord 85, coding-agent 14, durable 56, env 3, telemetry 12, tui 8 |
| **F2** a function/class/callable row: no asserting Go test names the symbol | 36 | ai 12, chord 18, durable 5, env 1 (agent 0) |
| **D1** designed-out row, design-out audit columns (parity, maintainability, performance) missing in `docs/parity/designout-audit.tsv` | 142 | mcp 24, chord 33, ai 31, telemetry 16, durable 15, coding-agent 8, tui 8, env 6, agent 1 |
| **D2** designed-out row whose rationale cites no D-number and no probe | 76 | durable 38, chord 21, ai 12, mcp 2, tui 2, agent 1 |
| **R** registration row: no field-survival test | 22 | chord 21 (chord/next 18), telemetry 1 |
| **U** union: no exhaustive-handling test | 2 | chord 2 |

Totals: F1 217 + F2 36 = 253 (class F), D1 142 + D2 76 = 218 (class D), R 22, U 2: 495. The per-id list is `build/ledger-proof-classes/unproven-behaviour.tsv` after `make ledger-check`.

Introducing commits at head (all groups together; counts are rows):

| package | pin-bump generation `0b5ddbd9e1` (2026-10-29, "pin Pi e7a0df27 ... regenerate") | later commits |
|---|---:|---|
| agent | 2 | |
| ai | 94 | |
| chord | 108 | `0fd7dc1d71` generate after C2/D4 26; `a1923d080a` generate after D3 14; `38f883a7f5` generate after C2/A2b 12; `4f6d4cdfd9` 8 (SetOf decision); `6f4b255cc4` this merge's generate 8; `72a2474367` policy entries 3; `7b888bd9b9` iface-close 1 |
| coding-agent | 18 | `640ba4e71d` first merge round 4 |
| durable | 91 | `7b888bd9b9` iface-close (durable/tui/chord root closure) 17; `4f6d4cdfd9` 4; `38f883a7f5` 2 |
| env | 10 | |
| mcp | 26 | |
| telemetry | 29 | |
| tui | 18 | |

Rows that already existed on 0.6.0 and carry the `8b2180d4b` ("generate the b3a29f11 ledgers", 2026-10-27) lineage account for ai 90, chord 118, coding-agent 16, durable 101, env 10, mcp 26, telemetry 29, tui 14, agent 2 of the 0.6.0-base counts: the unproven rows are mostly the 0.6.0 line's bulk-derived rows, not rows the next lane closed by hand.

What the next lanes added by hand (rows blamed to a non-pin-bump commit, 99): chord 72 (the D2/D3/D4/D5a generation commits above; chord/next is 60 rows in total: registration R 18, F 26, D 16), durable 23 (iface-close 17), coding-agent 4.

## 3. What closing each group takes

| group | mechanism | unit of work | estimate |
|---|---|---|---|
| **F1** (217) | one asserting test per symbol already exists. A mutation proof clears **every row of a Go file at once**: a mutant in the symbol's file in `test/parity/portmap-mutants.json` or a `mutation` entry in a `test/parity/unit-evidence/*.json` naming the test. The 217 rows sit in **40 Go files**: ai 6, chord 16, coding-agent 3, durable 7, env 2, telemetry 2, tui 4. | one mutant (break the behaviour, show the named test fails, record it) per file | about 40 mutants, 10-20 min each with the fail-output captured: **1.5-2 days of one agent**, parallel by package (chord 16 is the largest unit). Cheapest group per row. |
| **F2** (36) | no test names the Go symbol: a unit test has to be written (plus its mutation, which then also counts for F1) | one test per symbol | 36 tests, 15-30 min each: **about 1.5 days**; chord 18 (mostly chord/next declared helpers), ai 12, durable 5, env 1 |
| **D1** (142) | add the row to `docs/parity/designout-audit.tsv` with all three audit columns (parity / maintainability / performance). The file has 30 rows today; the prior design-out proposals live in `docs/parity/gap-closure/designout-audit-proposal-lg-c4-*.md`. Needs the owner (or his delegate) to confirm each design-out, which is the real cost | one TSV row with a one-line reason per column | writing: **1 day** for all 142 by an agent that drafts them from the existing rationales; the owner review is the gate (about 40 min per 25 rows) |
| **D2** (76) | the rationale of a designed-out row must cite an existing divergence D-number or say what Pi code was read (`probe` / `no Pi code` / `Pi does not` / `type-level`...). durable 38 are the IndexedDB storage rows (`pkg:durable/storage/indexeddb`: 35): one shared D-number (the IndexedDB design-out) clears them in one edit | rationale rewrite or one new D-number | durable 38 + chord 21 + ai 12: **half a day** (chord/ai need individual rationales); needs the owner to accept a new D-number for IndexedDB |
| **R** (22) | field-survival test named Registration/Survive/RoundTrip that names the member: chord 18 are chord/next registrations (`Registration`-suffixed declarations) | one RoundTrip test per registration type covers its fields | chord/next 6-8 tests: **half a day**, best done by the chord/next interface lane |
| **U** (2) | an Exhaustive/Variants test for 2 chord unions | 2 tests | 1 hour |

Order of cost-effectiveness: F1 (217 rows, ~40 mutants), then D2/IndexedDB (35 rows, one decision), R (22), D1 (needs the owner), F2 (36 tests). Closing F1 + the IndexedDB D2 rows + R + U removes 276 of the 495 rows; the packages would then stand at ai 55, chord 72, coding-agent 8, durable 23, env 7, mcp 26, telemetry 16, tui 10, agent 2. Against the baseline that leaves ai, durable (23 = 23) and tui (10 = 10) at or under it; chord, coding-agent, env, mcp and telemetry still need D1/D2/F2 work (mcp is all D1: 24 rows, an owner audit).

## 4. Decision for the owner

1. The baseline is stale from its first commit (the 0.5.0 squash): accept that, and re-base it only after a deliberate closure round (rows above), or keep it report-only as today. Do **not** make `make ledger-check` pass `--enforce` before that: it would fail every package but `agent` at 0.5.0 too.
2. If the baseline should be re-based to head instead, the new values are agent 2, ai 94, chord 180, coding-agent 22, durable 114, env 10, mcp 26, telemetry 29, tui 18 (`proofclass.py --update-baseline`); this raises the ratchet and needs the owner's explicit decision, so it was not done.
3. Cheapest real reduction: 40 mutants (F1) across 40 Go files and one IndexedDB D-number.
