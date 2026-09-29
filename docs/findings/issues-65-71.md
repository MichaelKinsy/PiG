# Issues #65 and #71: Node loader and published-package verification

## Scope and identities

Reference: [Pi](https://github.com/earendil-works/pi) 0.87.1, commit `f07218c4d4bbc12bef056a7058c3dd49dfe41abe`. The oracle is the locked published CLI in `extensions/sdk-ts/node_modules/.bin/pi`, not a substitute loader. The issue descriptions supplied in the task are the input; this worker's GitHub CLI could not retrieve the public issues.

- Candidate base: `5fe7bdcea` (the integration candidate).
- Public/main baseline: `b3beb2a8c364389f0d3c5f8c9a02a542806d42c6`.
- Host: Linux amd64. Go 1.27.1, npm 11.17.0, Python 3.12.3, Rust 1.97.1. The maintained npm qualification is 12.1.0; this experiment records the installed npm instead of claiming that qualification.
- Node tarballs: 22.13.0, 22.23.3, 24.21.0, 26.10.0 and unsupported 20.19.0. Each Linux x64 tarball passed its upstream `SHASUMS256.txt` check. No system toolchain was replaced.
- PiG's declared minimum is Node 22.13. Pi declares `>=22.19.0` in `packages/coding-agent/package.json:105`; the observations of Pi running under Node 22.13.0 below are probes, not an upstream support claim.

Every npm install and CLI execution uses private `HOME`, `PIG_CODING_AGENT_DIR` and `PI_CODING_AGENT_DIR` paths. Environment variables whose names contain `API_KEY`, `TOKEN` or `PROXY` are removed. Each CLI gets its own writable dependency copy and working directory. Settings select the installed Package, rather than bypassing its manifest with an exact extension-file path. Pi uses the repository's deterministic faux provider; PiG uses the matching test provider. No live model credentials are needed.

## Load/print matrix

Each cell is **Pi / public-main / candidate**. `OK` means print exits zero with exactly `42\n` and empty stderr. Candidate and Pi additionally produce identical sorted extension-command and complete tool-name inventories. The candidate base and the hook-fixed candidate have the same results in every cell. This is load/registration/print evidence, not a claim that every package command, TUI, provider or background workflow is compatible.

Public/main's extension-side inventory getters return empty values. Its three `OK` package shapes were additionally checked through RPC `get_commands`, which confirms the minimal, `todos`, and `fff-*` registrations. The supplemental public RPC runs also expose existing shutdown/context-cancel diagnostics; those are not hidden or represented as full RPC parity.

| Package (latest published version resolved for this experiment) | Node 22.13.0 | Node 22.23.3 | Node 24.21.0 | Node 26.10.0 |
|---|---|---|---|---|
| Minimal default-export TS fixture, including an enum | OK / OK / OK | OK / OK / OK | OK / OK / OK | OK / N / OK |
| `@juicesharp/rpiv-todo@2.11.0` | OK / OK / OK | OK / OK / OK | OK / OK / OK | OK / N / OK |
| `@ff-labs/pi-fff@0.11.0` | OK / OK / OK | OK / OK / OK | OK / OK / OK | OK / N / OK |
| `@benvargas/pi-claude-code-use@2.2.1` | OK / X / OK | OK / X / OK | OK / X / OK | OK / N / OK |
| `pi-secret-guard@1.2.15` | OK / X / OK | OK / X / OK | OK / X / OK | OK / N / OK |
| `pi-session-switch@0.4.0` | OK / X / OK | OK / X / OK | OK / X / OK | OK / N / OK |
| `pi-observational-memory@3.1.4` | OK / X / OK | OK / X / OK | OK / X / OK | OK / N / OK |
| `@tmustier/pi-usage-extension@0.9.5` | OK / X / OK | OK / X / OK | OK / X / OK | OK / N / OK |
| `@observal/pi-insights@1.2.3` | OK / D / OK | OK / D / OK | OK / D / OK | OK / D+N / OK |
| `@specode/pi-subscription-usage@1.1.1` | OK / F / OK | OK / F / OK | OK / F / OK | OK / F+N / OK |
| `pi-open-agents@0.1.22` | OK / F / OK | OK / F / OK | OK / F / OK | OK / F+N / OK |
| `pi-lens@4.3.0` | OK / S / OK | OK / S / OK | OK / S / OK | OK / S / OK |

Public/main failure keys:

- **N:** `stripTypeScriptTypes` rejects `mode: "transform"`; Node reports only `"strip"` as valid. The child log also contains DEP0205 for `module.register`. The CLI reports a registration transport EOF. The supplemental observer and the selected TS extension both fail. The reported blanket “Node 24+” removal is not reproduced: Node 24.21.0 accepts that mode; Node 26.10.0 rejects it.
- **X:** missing export. The first failures with these exact latest packages are `getCapabilities` (Claude Code use), `isToolCallEventType` (secret guard), `SessionSelectorComponent` (session switch), `EventStream` (observational memory), and `CancellableLoader` (usage extension). The reduced regression separately checks `calculateCost`, including its actual result, and `CancellableLoader`.
- **D:** a `pi.extensions: ["./"]` member reaches a file reader as a directory: `read …: is a directory`.
- **F:** static source-text factory detection rejects a valid evaluated default export: `has no default extension export`.
- **S:** Package validation incorrectly requires `skills/SKILL.md` for pi-lens's `skills` directory. This happens before its declared `./dist/index.js` extension is launched. The candidate resolves both manifest fields and loads the extension.

All 11 named packages are pinned with their complete npm dependency locks and top-level integrity values under [`test/parity/corpus/issues-65-71`](../../test/parity/corpus/issues-65-71). The final replay performs another Pi/candidate comparison for each package on each of the four Node versions. Every pair matches stdout, stderr, exit status and the inventory trace without normalization.

## Upstream rules and candidate disposition

| Rule | Exact Pi source | Candidate disposition |
|---|---|---|
| Import through jiti, disable module cache, use virtual modules, and accept the evaluated default only when it is a function | `packages/coding-agent/src/core/extensions/loader.ts:479-509,557-580` | Already implemented by the base's `jiti-loader.mjs` and Node runtime. No second resolver or package-specific exception is added. |
| Prefer existing `pi.extensions` members, then `index.ts`, then `index.js`; otherwise discover eligible immediate directory entries | `packages/coding-agent/src/core/package-manager.ts:557-635`; `packages/coding-agent/src/core/extensions/loader.ts:670-750` | Already implemented by `coding/extension/source/node_entries.go`, Package discovery and the Node builder. |
| Expand manifest paths through the resource-specific collector; a directory is not necessarily a resource file | `packages/coding-agent/src/core/package-manager.ts:2317-2326,2518-2536` | Already implemented; the pi-insights directory and pi-lens resource directory both pass. |
| Serve the complete Pi virtual module namespaces, including pi-ai's compat root and legacy aliases | `packages/coding-agent/src/core/extensions/virtual-modules.ts:20-36` | Already implemented by the candidate's vendored modules. `calculateCost` is defined at `packages/ai/src/models.ts:900-911`; `CancellableLoader` is exported at `packages/tui/src/index.ts:14`. |
| Discard process warnings in the CLI | `packages/coding-agent/src/cli/setup.ts:4-8` | Already implemented, so the base does not visibly emit DEP0205. This patch removes use of the deprecated API where synchronous hooks exist instead of relying only on warning suppression. |

The only remaining loader change is at `coding/extension/host/subprocess/runtime-node/register-loader.mjs`: use `module.registerHooks({ resolve })` when available, otherwise retain the asynchronous registration API required by Node 22.13. The shared resolve hook is synchronous and serves both APIs. Native imports and native `require(ESM)` preserve virtual-module function identity. Jiti still owns TS transformation and factory import. No package API, wire shape, SDK contract or divergence changes.

Unsupported-runtime handling is already at the host boundary. The actual cold CLI path on Node 20.19.0 rejects startup with `TypeScript extensions need Node.js 22.13 or newer; found v20.19.0`. The real CLI result contains no transport EOF. Existing warm-launcher fake-node regressions also verify the requirement and confirm that only `--version` runs, not the extension process.

## Regression and mutation evidence

- **Red → green:** `TestNodeLoaderUsesSynchronousHooksWhenAvailable` replaces the deprecated registration entry with a throwing function on Nodes that expose `registerHooks`, then exercises actual native SDK imports. The base fails with `deprecated module.register called despite registerHooks support`. The patch passes on all four selected Nodes; the minimum also exercises the real fallback API. On synchronous-hook Nodes the test additionally checks native require/import identity.
- **Reduced production path:** `TestNodeIssuePackageForms` loads a declared directory with a decoy root index, an enum and bundled-style `export { factory as default }`, in both ordinary packed and isolated placements. It checks the exact Pi-observed cost JSON and loader export. An initial hand-written expected `0.2` was wrong: Pi evaluates that cost as `0.19999999999999998`. The test keeps Pi's actual number; no rounding or normalization is applied.
- **Canonical scenario:** `extensions-runtime/55-node-package-entry-exports` invokes the package command through print mode and requires exact output equality for three pairs. It passes on the base because that behavior was already fixed. A compiling runtime mutation that imports the module but returns `undefined` instead of its factory makes the scenario fail with exit 1 and `Extension does not export a valid factory function`. The mutation is removed.
- **Existing coverage:** `TestNodeExtensionModulesLoadLikePinnedPi` compares the broader module-resolution fixture against the pinned Pi jiti. `TestNodeRuntimePreflightRejectsUnsupportedNodeBeforeLaunchingWarmExtension` and its missing-runtime sibling guard the diagnostic boundary. The Node matrix runs these tests as well.
- **Test isolation:** the full test run red-proved `TestFindSDKRoot_InstalledSourceRoot`, `TestFindSDKRoot_NoSourceRootErrors`, and `TestNodeTypeScriptSourceResolutionAndShims` under the required private environment. The SDK fallback tests changed HOME but inherited an unrelated staged PIG_HOME; the Node fixture assumed an `agent` basename while inheriting the caller's custom directory. The tests now declare those inputs locally. Their original assertions remain intact, and all three pass in the same outer environment.
- **CI:** `.github/workflows/ci.yml` runs the hook, package-form and preflight regressions with Node 22.13.0 and the current Node 26 major on Linux, Windows and macOS. The final required CI result includes this job. Native Windows/macOS execution is pending CI; this report does not claim those hosts were locally tested.

## Reproduction and retained evidence

Run the corpus with the desired Node executable directory first on PATH. The script verifies the Pi CLI version, uses `npm ci` for each committed lock, and fails on any install, timeout, load, output or inventory mismatch:

```bash
go build -o /absolute/temp/pig ./cmd/pig
PATH=/absolute/node/bin:$PATH python3 test/parity/corpus/issues-65-71/replay.py \
  --pig /absolute/temp/pig --output /absolute/temp/new-corpus-run
```

The output directory must not already exist. `--installed <directory>` reuses an existing install only after its lock matches the committed lock. `--public <binary>` additionally records the historical binary without treating it as the oracle. Top-level package versions and integrity values are in `packages.json`; transitive versions and integrities are in each `locks/*/package-lock.json`. The script copies dependencies into private per-process directories and never edits the pinned inputs.

Local raw artifacts are retained under `/tmp/verify-issues-65-71/`: `runs/` and `results.json` hold the four-binary initial matrix; `final-<node>/report.json` holds the final locked Pi/candidate replay; `public-rpc/` confirms historical registration; `hooks-red.log`, `hooks-green.log`, `scenario-mutant.log`, `mutation-build.log`, `unsupported.log`, gate logs and CPU/allocation profiles retain the other probes. [`issues-65-71-results.json`](issues-65-71-results.json) records a compact path-independent summary and artifact hashes.

Binary SHA-256 identities:

- Public/main: `9047cb5021a114679be0b796361c7afa9d1bed33178f7f3daa4ad8bb70e335d4`.
- Candidate base: `da8c93bf005b33c19c7e0a03f9e4c334ab78e54d6b01ec62c7d755f9c122f33f`.
- Final locked-corpus binary: `6bd98aca823863efb9a804011aca193ced91ee3bbea64f7f2fbd5af2416d5d0b`. A later source-comment cleanup changes no loader behavior.

## Resources and gates

`BenchmarkNodeAdmissionStartup` with three measured repetitions reports about 275 ms / 152 KB / 548 Go allocations for one Node factory, and 375 ms / 209 KB / 873 allocations for three packed factories on this host. CPU and allocation profiles are retained. These are startup measurements under concurrent verification load, not a speedup claim or whole-child-process memory measurements. The Host shuts down each benchmark runtime. Synchronous hooks add no worker, handler, timer or Go UI-loop work; Node owns the registered hook until child-process exit. Older Node retains the existing hook thread and Host-owned process lifetime.

Passed: four-Node loader/module/preflight tests; full `coding/extension/host/subprocess` tests; SDK fallback and Node source-resolution isolation regressions; Linux `go vet ./...`; Windows vet for all three touched Go packages; touched-package golangci-lint, `make lint-changed` against the integration base, and full `make lint`; workflow CI contract tests; scenario lint; port-map and coverage drift; divergence guard; source hygiene; docs drift; format inventory; custom-factory ledger; SDK surface drift. The focused directory, module, error-message and new package scenario each pass three Pi/PiG pairs. The complete `extensions-runtime` family passes all 59 scenarios at declared durability; the final family run uses tmux 3.7b. Generated coverage adds the scenario without promoting any PORT_MAP file.

Release blockers remain explicit:

- `make ci-contracts ci-drift` stops at `test-porting-release`: the base's reviewed upstream hot-path mapping has pending and partial rows. No row is weakened or relabelled here.
- Running `make ci-drift` separately stops at D78's missing `SCRUTINIZED:approved`. This patch neither approves D78 nor changes that record.
- `go test ./test/parity/...` fails three existing closure assertions: provisional denominator count, divergence scrutiny count, and the foundation divergence row. They expect the unchanged D78 ledger to be approved. The remaining parity packages pass.

Full `go test ./...` completes with only the same three `test/parity/closure` failures. All other packages pass, including the complete runtime-cell, subprocess-host and cross-SDK conformance packages after the test-isolation fixes. The release ledger is unchanged, and these blockers mean this lane cannot certify release readiness.

## Two-line user reply

The candidate loads all 11 reported packages like Pi 0.87.1 in Linux print-mode tests on Node 22, 24 and 26; it also rejects unsupported Node versions with a clear requirement.
The deprecated Node hook call is replaced where supported, with minimum/current-Node regression coverage; native Windows/macOS CI and unrelated release-gate blockers still need to clear before release.
