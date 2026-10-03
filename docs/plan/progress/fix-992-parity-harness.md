# fix-992-parity-harness: parity harness robustness from AGG r1 (item 7)

Base: staging porter/pi-0.99.1. Oracle: upstream 0.99.2 (`extensions/sdk-ts/node_modules`, `.upstream/v0.99.2`).

## Part 1: measured wall clock (done)

Root cause: bash's `structuredContent.wall_time_seconds` is a measured duration rounded to 0.1 s (`.upstream/v0.99.2/packages/coding-agent/src/core/tools/bash.ts:392`). On a loaded host the two sides report 0 and 0.1. Two comparator paths pinned or compared the value:

| Scenario | Comparator | Before | After |
|---|---|---|---|
| tools/17-tool-rpc-error-and-bash-wire, rpc/41-rpc-tools-allowlist-overrides-no-tools | `json_output_equal` (JSON comparison never applies text normalization) | `0` vs `0.1` differs | `[[assert.json_aliases]] kind = "duration"` on `/**/wall_time_seconds` |
| json/05, json/06, rpc/42 | `both_contain` with the literal `"wall_time_seconds":0` on the raw output (their `normalize_replace` only reaches the equality comparators) | missing when a run reports 0.1 | `both_match_regex` with `wall_time_seconds":[0-9]+(\.[0-9])?\}` |

The task named "06-json-failing-bash" (json/06, with json/05 and rpc/42 sharing the same line). The AGG file `docs/plan/progress/agg-992.md` is not present in any worktree, so the four named scenarios were located from the pinned literal and the probe output.

Mechanism: the harness's existing JSON alias table (`kind = id|timestamp|path|literal|session_file`) is the timing alias. `timestamp` accepts only integer numbers (`validTimestamp` uses `Int64`), so a fractional elapsed time needs its own kind inside the same table: `duration` accepts two finite non-negative JSON numbers, and keeps presence, JSON type and position (`null`, string, boolean, negative and absent still differ). No field is removed. README documents the kind.

### Red-green record

| Commit | Kind |
|---|---|
| 16f0d3770 test(parity): a measured duration is aliased, not pinned (red) | red: `unknown JSON alias kind "duration"`; scenario 17 and 41 `wall_time_seconds: pig=0 pi=0.1`; json/05, json/06, rpc/42 pin the literal |
| 0ada9750d fix(parity): alias bash's measured wall clock instead of pinning it (green) | tests pass; the three guards in `duration_alias_test.go` |

Regression tests: `TestJSONDurationAliasRetainsPresenceAndType` (7 cases + absent, different exit code, no-reason), `TestJSONScenariosAliasMeasuredDurations` (loads the real 17 and 41 aliases), `TestScenarioAssertionsDoNotPinMeasuredDurations` (every scenario). Mutation: removing the 17 alias or restoring a literal fails the matching guard (shown by the red run).

Parity: 17, 41, 42, json/05, json/06 each pass with `-pig-parity.runs=6` under 64 CPU burners (all 6 pairs). Under that load no pair reported 0.1 (bash completes in <50 ms), so the live 0-vs-0.1 case is proved by the unit tests, not by the burner run.

Upstream tests: this lane changes harness and probe expectations only; no Pi test is ported.

## Part 2: tmux server loss under `make test` plus the whole hermetic suite (in progress)

See the evidence section below once complete.
