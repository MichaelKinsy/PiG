<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Regression inventory handoff

This is the `next-regression-inventory` review snapshot, not an acceptance ledger or a claim that all regressions pass.
The integrator owns changes to `parity/interfaces/test-mapping-v<UpstreamVersion>.json`.
The companion [case table](next-regression-cases.tsv) gives every case one review route.
The `proposal` routes remain owned by `next-regression-inventory` until the coordinator accepts the named follow-up task.
No proposed task is a dispatched worker or permission to edit another active slice's files.

## Denominator and provenance

- Worktree baseline: `65ad9df62dbd10f8375af8adf3fed9a4b86da957`.
- Upstream version: the baseline's `coding/pigversion/pigversion.go` pin.
- Inputs: `parity/interfaces/upstream-tests-v<UpstreamVersion>.json` and `parity/interfaces/test-mapping-v<UpstreamVersion>.json`.
- Source denominator: every file under `packages/coding-agent/test/suite/regressions/`, including the four non-numbered files listed in the task.
- Review routing input: `staging/team/lead/tasks` at `bf5fa3bd92543171cce5766176e281d22eb07cfc`, especially `tasks/next-virtual-modules.md`, `tasks/next-session-runtime.md`, and `tasks/INDEX.md`.
- Existing CLI fix inspected: `staging/mirror/ws/next-div-main-cli` at `2721daa32d0a842a284a4a2bf04fcd3b9734b717`.
- Event-owner changes inspected, not merged or claimed: `staging/mirror/ws/events1-final` at `c81c6a06ac8f55cdfd066e250f1cabd5ee902b60`.

The denominator has 80 files and 175 test declarations.
Seven parameterized declarations expand to 15 cases, so this table has 183 rows.
The table retains the compiler's case ID and source line, then adds a separate parameter column.
The expansions are #6019 (OpenAI and Bedrock), #7027 (two post-login selections), #7269 (two dash-prefixed prompts), #8964 (`stream` and `streamSimple`), #9068 (three RPC results and two interactive inputs), and #9340/#9777 (two non-cancellation errors).
These numbers come from the upstream declarations and their literal tables, not from counting passing Go tests.
Every source hash was compared with the pinned mirror.
`make test-inventory-drift` independently regenerated the declaration denominator and passed.

| Allocation | Expanded cases | Meaning |
|---|---:|---|
| `preserved-ported` | 9 | Existing evidence for seven ported files was re-run; no disposition changes. |
| `new-test-port` | 6 | All #3302 and #3303 cases are ported in this slice. |
| `existing-task-reference` | 35 | Exact regression references already occur in another task brief; route review there, not to a duplicate worker. |
| `existing-branch-fix` | 3 | #7269 already has a parser fix on the CLI owner's branch. |
| `proposal` | 130 | Concrete case obligations grouped below; not a claim that 130 tests fail or that their implementations are absent. |

`evidence_candidate` copies the baseline's file-level navigation evidence except for the new test ports.
A `-` means that the baseline supplied no evidence candidate.
It does not claim that each copied test covers the case beside it.
In particular, the existing #2835 tests supply tools directly rather than through extension registration; #3686 tests only the event bridge; #6647 tests only error classification; and #8261 tests stored trust rather than the subagent confirmation path.
The baseline partial claims remain partial.
The #2781 collision loser, #5217 overflow extension metadata, and #7209 Scoped-tab selection reset remain distinct obligations even though adjacent cases already have evidence.

## Completed small remainder

`internal/codingagent/tools/find_regressions_test.go` ports the four #3302 patterns and the two #3303 ignore-scope fixtures through the real `FindTool.Execute` → `fd` → result-path pipeline.
Each test compares the complete sorted path list.
The fixtures isolate HOME and XDG configuration and use temporary directories.
The tests require the existing fd tool; they neither install nor download it.

Upstream `core/tools/find.ts` supplies the rule: path-containing globs use `--full-path` and a leading `**/` where needed, while fd applies nested `.gitignore` files hierarchically.
The production implementation already follows that rule.
There is no production fix in this slice and no claim that these tests failed on unchanged production code.

Compiling Go overlays demonstrate sensitivity without editing the production file:

1. Replace the `--full-path` argument with `--no-ignore`: all three path-containing #3302 cases fail with `No files found matching pattern`; the basename control remains green.
2. Add `--ignore-file <searchPath>/a/.gitignore`: both #3303 cases fail because `b/ignored.txt` disappears from the result.
3. Remove the overlays: all six cases pass with `-count=3`; the whole tools package and its `-race` run pass.

This proves these regressions, not all `find` behavior.
#6104 remains open: it also requires Windows path semantics and the custom-glob operations path.
No `PORT_MAP.md`, coverage, known-gap, divergence, interface, or delivery-ledger row changes here.

## Observed failure retained with its existing owner

#7269 is a real base-code failure, not merely missing inventory evidence.
`cmd/pig/args.go:parseFlags` treats `--` as an empty extension flag, rejects the single-dash prompt, and parses `--provider` and `-c` after the delimiter.
A scratch-only Go probe reproduces both upstream prompt values and the option/file case on this baseline.
The probe compiles and fails with empty messages and `unknown=map[:true]`.
Overlaying only the CLI owner's `cmd/pig/args.go` makes it pass.
The owner's fix consumes the remaining arguments as messages or `@files`, exactly like upstream `cli/args.ts:parseArgs`.
Do not duplicate or cherry-pick that fix into this inventory slice.
The CLI owner still needs to bind its complete caller-path regression evidence before the integrator promotes #7269.

## Follow-up proposals for the coordinator

Each row is a bounded case-review task, not a request to rewrite a subsystem.
Start by reading its TSV cases and matching existing tests and pending owner changes.
Add a compiling failing regression before changing behavior.
Use deterministic barriers for ordering and cancellation.
Keep complete mode/SDK paths where the case crosses a boundary.
Serialize every overlapping production file with the listed active owner.
Do not turn a test reference into an unreviewed designed-out disposition.

All task names below have the prefix `next-regression-`.
Counts include existing partial-case controls so they cannot disappear during closure.

| Task suffix | Cases | Boundary and exact completion requirement | Coordinate before edits |
|---|---:|---|---|
| `event-order` | 6 | #1717/#2113, #3982, #8537: awaited message-end handlers, tool preflight after persistence, cost replacement, and custom-message order in state/events/storage/provider history. | `events1-final`, `div-agent-extensions`; `coding/session*.go`, event bridge. |
| `settlement` | 3 | #6363: one settlement after retry, after agent-end follow-ups, and command waitForIdle at Session rather than Agent idle. | `events1-final`, `next-before-settle`. |
| `queued-command` | 1 | #2023: extension-origin queued slash text reaches the user transcript without dispatching the command. | `next-ext-input-queue`, `events1-final`. |
| `resources` | 6 | #2781 precedence and exact losing source; #5661 uppercase header literals survive migrations; #7187 malformed fields do not drop valid manifest fields. | `next-div-resources`, `next-piglet-update`; resource loading and `coding/packagecontent`. |
| `settings-reload` | 4 | #3616 initial settings survive direct/resource reload and unrelated setter+flush; #7572 nested provider retry overrides preserve unrelated global settings. | `next-settings-model-thinking`; `internal/codingagent/settings.go`. |
| `theme-failures` | 2 | #2791 watcher errors do not crash; #5596 export uses the active fallback for a missing configured theme. | `next-div-tui-render`; theme watcher and export caller. |
| `tool-exclusions` | 2 | #5109 denylist filters available and active built-in/extension tools and outranks allowlist. | `next-virtual-modules`, `div-agent-extensions`; Session registration. |
| `session-names` | 5 | #3686 direct/extension rename events reach both listeners; #5996 both rename paths remove newlines before storing/emitting. | `events1-final`, `next-session-runtime`; Session and mode commands. |
| `tree-guards` | 4 | #3688 clears branch-summary state after cancellation; #9178 guards manual compaction and a held before-tree hook; non-numbered streaming guard preserves leaf. Reuse `TestNavigateTreeRejectsSecondNavigationDuringSessionBeforeTree` where its exact assertions suffice. | `session-guards`, `next-tree-conformance`, `compaction-robust`. |
| `compaction-retry` | 8 | #5217 manual/threshold/overflow metadata reaches extensions; #6647 success, nonretryable, disabled, exhaustion and cancelled backoff exercise the actual loop. | `compaction-robust`, `events1-final`; Session summarization/recovery. |
| `compaction-cancel` | 8 | #7253 persists aborted response before manual compact; #9340/#9777 checks auth/start-event cancellation and distinguishes errors; pre-prompt overflow does not continue the prior assistant. | `compaction-robust`, `session-guards`. |
| `summary-auth` | 2 | #6324 ambient auth needs no API key; #6768 auth-resolved Copilot URL reaches the actual summary request. Use fake transports, not live credentials. | `compaction-robust`, `next-runtime-credentials`, Copilot owner. |
| `pending-render` | 3 | #4167 unresolved tool components survive transcript rebuild but completed ones are not pending; #8611 partial bash output survives thinking toggle. | `div-modes-interactive`, `next-live-render-verify`; live event path and TUI component state. |
| `signal-exit` | 1 | #5724 second signal during awaited cleanup cannot bypass extension cleanup; review alongside #5080, preserving D51's terminal restoration scope. | `next-virtual-modules` (#5080 reference), interactive signal owner. |
| `bash-process` | 3 | #5303 descendant output is collected until close or grace expiry; #6596 Windows System32 taskkill spawn failure is consumed. Include Windows execution, not cross-compilation alone. | `div-tools`, Windows lane; process lifetime helpers. |
| `bash-operations` | 6 | #5208 ignores late output callbacks; #9068 distinguishes throw/empty/undefined for RPC and both interactive bang forms. | `next-ext-user-bash-ops`, `div-ext-host-s15`; all SDKs where wire behavior matters. |
| `rpc-id` | 1 | #5868 unknown-command request ID survives actual dispatch and serialized error. `TestRPCErrorResponseEchoesID` covers only the builder, not dispatch. | `next-rpc-framing`, `div-modes-headless`; `cmd/pig/rpc_mode.go`. |
| `reload-ui` | 9 | #5943 all seven startup/replacement/reload cases; #7829 diagnostics in transcript; stale startup rebind cannot subscribe twice. | `events1-final`, `next-session-runtime`, `div-modes-interactive`. |
| `preflight-abort` | 2 | #5998 blocked handler terminates the run; #8935 later preflight abort prevents earlier prepared tools from starting. Assert no tool side effects. | `div-agent-extensions`, `next-harness-execution`. |
| `session-retry` | 3 | #3317 and both #6019 providers retry through the Session; assert calls, error text and terminal retry event, not only classifier output. | `next-ai-transport-errors`, `compaction-robust`. |
| `find-paths` | 11 | #6104 root/deeper/sibling/relative paths, POSIX backslashes, Windows slash variants, and custom-glob caller. Do not claim the native fd tests cover custom operations. | `div-tools`, Windows lane; `internal/codingagent/tools/find.go`. |
| `scoped-models` | 11 | #6949 unavailable selections; #6999 hot reload; #7153 cached render/background cancellation; both #7209 tabs; #7443 cached match/deadline after miss. | `next-model-catalog-refresh`, `next-settings-model-thinking`; selector and model command. |
| `login-refresh` | 7 | #7027/#7113 login supersedes stalled refresh; bounded background completion, both selection cases, empty catalog, user selection and deadline. Keep Radius test fixtures local/fake. | `next-interactive-auth-followup`, `next-runtime-credentials`, `next-model-catalog-refresh`. |
| `refresh-replacement` | 3 | #7301 older success/failure/provider-scoped failure cannot overwrite a newer availability snapshot. Assert retained errors as well as models. | `next-model-catalog-refresh`, `next-runtime-credentials`. |
| `eventbus` | 1 | #7193 reload/dispose removes only the extension-owned subscriptions; repeat lifecycle to expose leaks. | `div-ext-host-s15`, `next-virtual-modules`. |
| `json-stream` | 3 | #7290 linear serialized update size; #7911 cumulative usage without snapshots; #7925 tool ID/name on first toolcall update. Test JSON and RPC serialization. | `div-modes-headless`, `next-rpc-framing`, `events1-final`. |
| `session-discovery` | 3 | #7497 directory symlink alias retained; broken links and file links ignored without hiding valid sessions. | `next-session-runtime`; `internal/codingagent/session_resume.go`. |
| `tui-methods` | 2 | #7731 captured method calls current renderer after replacement and previous renderer before replacement. Exercise the Go indirection, not a JavaScript Proxy imitation. | `next-div-tui-render`, `div-modes-interactive`; renderer reference. |
| `subagent-trust` | 2 | #8261 trusted project skips per-call confirmation, untrusted interactive project retains it. Generic trust storage is insufficient. | `next-virtual-modules`, subagent/example owner. |
| `fork-boundaries` | 2 | #8724 active-turn abort cannot append to replacement; #8989 removed boundary label preserves compaction context. | `next-session-runtime`, `next-session-migration`, `session-guards`. |
| `context-system` | 6 | #9789 context slice/replay/in-place edits/inserted system messages; context_with_system order, verbatim output, and leading-system diagnostic. | `events1-final`, `next-harness-context`; production request transformation and every SDK. |

The 35 `existing-task-reference` rows need review by their named owner, not automatic acceptance.
`next-virtual-modules` lists 32 expanded cases across eleven files, including references outside its exclusive loader files (#5080 and #5433).
The coordinator must confirm their behavioral owner or split those references into the signal/UI follow-ups before dispatch; the reference alone does not authorize loader work to claim them.
`next-session-runtime` explicitly lists all three #2860 cases and owns the replacement implementation.
D30 and D61 remain active and cannot be silently erased by an inventory update.

## Exact proposed mapping replacements

Replace only these two entries after integrating the test commit.
Leave the upstream inventory unchanged.

```json
[
{
  "path": "packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts",
  "disposition": "ported",
  "upstreamTestHash": "sha256:d9bb34b773d7865109e41ac09146fa3c2c670cc594bb5a350e2d8e30230939db",
  "rationale": "TestFindPathGlobRegression ports all four upstream patterns through FindTool.Execute and real fd with complete path-list assertions. A compiling overlay without --full-path fails the three path-containing patterns; the basename control and the unmodified implementation pass.",
  "evidence": ["internal/codingagent/tools/find_regressions_test.go#TestFindPathGlobRegression"]
},
{
  "path": "packages/coding-agent/test/suite/regressions/3303-find-nested-gitignore.test.ts",
  "disposition": "ported",
  "upstreamTestHash": "sha256:40748b486bf64c9feaacb3e0f411ffec5fdc145cca3dad0f62c680e0673b959f",
  "rationale": "TestFindNestedGitignoreRegression ports the flat sibling and deeply nested fixtures through FindTool.Execute and real fd. Both assert the complete result list and fail under a compiling overlay that flattens a/.gitignore with --ignore-file; the unmodified implementation passes.",
  "evidence": ["internal/codingagent/tools/find_regressions_test.go#TestFindNestedGitignoreRegression"]
}
]
```

For the remaining rows, the TSV is the exact per-case ownership proposal, not replacement closure evidence.
No known-gap keys are removed.
No new source row is marked complete.
No additive behavior or service contact is introduced.
There are no new STOP-AND-ASK decisions in this slice.

## Verification and retained evidence

The signed report commit contains the full command results, commit IDs, environment, log locations, and dependency check.
The Linux lane gates replace the Mac-only tooling in the original brief.
`check-contracts-fast` runs as its explicit target list without `closure-check`; `-o interface-deps` prevents its installer prerequisite from running against the read-only preinstalled TypeScript tools.
All validation targets still execute.
The first lint run found one goimports alignment issue in the new test fixture; gofmt corrected it and both lint gates then passed with zero issues.

Existing `ported` claims were re-run with the exact named tests for #2753, #3217, #6904, #7048, #7150, #8328, and #8337.
This is a re-run of the denominator owner's evidence, not a new mutation audit of those tests.
The remaining cases are not claimed as freshly executed.
No benchmark or performance improvement is claimed; production code and resource lifetimes are unchanged.

### Recheck this snapshot

First run `make test-inventory-drift` to verify the upstream declaration inventory.
Then run this from the repository root to check case completeness and source identity without installing anything:

```python
import csv, hashlib, json, pathlib, re
root = pathlib.Path('.')
version = re.search(r'UpstreamVersion\s*=\s*"([^"]+)"', (root / 'coding/pigversion/pigversion.go').read_text())[1]
inv = json.loads((root / f'parity/interfaces/upstream-tests-v{version}.json').read_text())
files = [f for f in inv['files'] if '/suite/regressions/' in f['path']]
rows = list(csv.DictReader((root / 'parity/upstream-sync/next-regression-cases.tsv').open(), delimiter='\t'))
expected = {(f['path'], str(c['line']), c['id']) for f in files for c in f['cases']}
assert {(r['upstream_path'], r['line'], r['case_id']) for r in rows} == expected
assert len({(r['upstream_path'], r['line'], r['case_id'], r['parameter']) for r in rows}) == len(rows)
hashes = {f['path']: f['sha256'] for f in files}
for r in rows:
    assert r['owner_or_proposed_task']
    assert r['upstream_sha256'] == hashes[r['upstream_path']]
for path, digest in hashes.items():
    assert 'sha256:' + hashlib.sha256((root / '.upstream/current' / path).read_bytes()).hexdigest() == digest
print('regression snapshot: complete case IDs, unique parameters, current source hashes')
```

Review the seven parameter tables again if their source hashes change.
The check intentionally does not promote a mapping or infer behavioral coverage.
