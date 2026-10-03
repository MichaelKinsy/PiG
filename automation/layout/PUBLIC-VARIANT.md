<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Public-library layout variant

This report records the earlier B rehearsal. The owner now selects B+, which adds the root cleanup to the same `--keep-public-libraries` flag. Use [RELEASE-REHEARSAL.md](RELEASE-REHEARSAL.md) for the current release command and results. The package-boundary audit below remains applicable; the earlier counts and commands record historical evidence.

This is a selectable rehearsal, not a release-layout decision. The input is `2b8b13b52` on the layout-rehearsal branch, whose production candidate is `e04490990bc7248e314aa83873dd895362dc4bbe`. Only the codemod, tests, documentation, and changelog fragment are delivered. The relocated trees are disposable checkouts. No integration branch is transformed.

```sh
bash automation/layout/run.sh --keep-public-libraries /absolute/path/to/disposable-checkout
```

Without the flag, the original all-internal layout remains available. `go.sh`, `build.sh`, and `docs.sh` accept the same flag. Use one variant consistently for all sections. Start a fresh checkout to compare variants; changing the flag does not reverse an existing transformation.

## Package boundary audit

Go's [Organizing a Go module](https://go.dev/doc/modules/layout) guide puts importable libraries in top-level directories and private implementation under `internal/`. Pi publishes four libraries: `packages/agent/package.json:2-10`, `packages/ai/package.json:2-17`, `packages/coding-agent/package.json:2-18`, and `packages/tui/package.json:2-6,53`. Pi exports session construction in `packages/coding-agent/src/index.ts:235-253` and implements `createAgentSession` at `packages/coding-agent/src/core/sdk.ts:175`.

PiG's corresponding public roots remain `agent/`, `ai/`, `coding/`, and `tui/`. `examples/sdk/main.go`, the website and embedded `sdk.md`, the message-type documentation, and the pkg.go.dev links all use those paths. `coding/doc.go` retains its public-SDK wording and all other bytes. This is an import-boundary choice, not a change to Session, provider, agent-loop, or terminal behavior.

The selected private moves are exhaustive for this variant:

| Package | Destination | Evidence and reason |
| --- | --- | --- |
| `coding/pigversion` | `internal/coding/pigversion` | `coding/pigversion/pigversion.go:1-2` describes a cycle-breaking pin provider for packages that cannot import `coding`. `coding/upstream.go:11-24` already re-exports all four constants at the documented public boundary. Examples, external SDK imports, and pkg.go.dev-facing embedding docs do not import the helper. Source-location references in the knowledge graph and generators move with it. |
| `tui/parity` | `internal/tui/parity` | `tui/parity/parity.go:1-15` identifies the repository's parity-test harness. It imports `testing` and the simulator; no library-root API, embedding example, extension SDK, or public user guide exposes its types. Pi's TUI export list does not export this harness (`packages/tui/src/index.ts:1-156`). |
| `tui/termsim` | `internal/tui/termsim` | `tui/termsim/termsim.go:1-9` identifies the headless parity-test simulator. Its callers are the repository's test harnesses and rendering tests, not the public component API or extension SDK. It moves with `tui/parity`; Pi's TUI export list has no corresponding simulator (`packages/tui/src/index.ts:1-156`). |

The Go API inventory already excludes both TUI test-support packages (`parity/cmd/gointerfaces/main.go:107`, guarded by `main_test.go:13-14`). This is additional evidence that they are not library API packages, rather than an inference from the absence of an example.

Do not infer that a subpackage is private merely because `examples/sdk` does not import it. The audit deliberately retains these surfaces:

- `tui/widthx` remains public. Pi exports `getOsc8LinkAtColumn`, `sliceByColumn`, `stripTerminalSequences`, `truncateToWidth`, `visibleWidth`, and `wrapTextWithAnsi` at `packages/tui/src/index.ts:147-156`. PiG implements those public utilities in this package; moving it would remove library capabilities even though the root package still compiled.
- `coding/extension` and `coding/extension/host/inproc` remain public. `coding.RuntimeOptions.NewExtensions`, `Runtime.NewExtensionRunner`, and `coding.SessionOptions.Runner` expose their types (`coding/runtime.go:35-42,151`, `coding/session.go:231`). Hiding either package would change the usable embedding API.
- `coding/rpcclient` remains public. Both `docs/site/docs/rpc.md` and `docs/site/docs/cli-integration.md` explicitly tell external Go programs to import it.
- The other extension, source, Package, Piglet, and builder packages remain at their current paths. This variant does not establish a safe private boundary for their generated factory/build consumers or exported type dependencies. The extension SDK's `bundle.go` identifies the host-side staging relationship; generated/fused consumers require separate contract review before any additional privatization. There is no blanket `coding/*` move.
- `ai` has no child Go package to classify. `extensions/sdk` retains its separate module and every tracked byte.

## Shared behavior and implementation

`plan.py` owns both move tables. The three sections select that table for parsed imports, repository references, Python Path operands, documentation links, and ledgers. Public mode does not apply the original variant's internal-SDK prose replacements or collapse public scanner roots. It still applies governance moves and documentation discovery changes. Nested destinations are created before `git mv`.

All root legal and discovery files stay at the root: `LICENSE`, `LICENSES/`, `REUSE.toml`, `NOTICE`, `README.md`, `CHANGELOG.md`, and `CITATION.cff`. The command remains `cmd/pig`. The same seven governance files move in both variants. The resource-only root `embed.go` exception is unchanged.

The layout test retains its public-root assertions. Its retired `internal/tui` assertion becomes `internal/tui/tui.go`: private helper directories may now exist beneath `internal/tui`, but the root implementation must remain public. No scenario comparator or assertion changes.

## Regression and external-module proof

```sh
python3 -B -m unittest discover -s automation/layout -p 'test_*.py' -v
python3 -B automation/layout/external_imports.py /path/to/public-variant-checkout
```

The Python regressions fail before the selectable implementation: `test_public_libraries_and_private_helpers` cannot select the public layout, and `test_selected_paths_and_public_docs` exposes the old root-only path assumptions. The real external-module build also fails against the original all-internal result because the four public imports disappear. It passes against the unmoved candidate and the public variant.

`external_imports.py` creates a module named `example.org/layout-consumer` outside PiG's tree, disables workspace and proxy fallback, and uses local module replacements. It builds the actual embedding example plus named imports from all four libraries, the extension and inproc types exposed by Session options, the documented RPC client, and TUI width utilities. It uses fresh HOME and agent directories, and preserves only compiler/module cache locations from the caller.

A second red regression, `test_private_package_boundaries`, catches a nested-package reference ending without a slash: the old root-only matcher leaves `coding/pigversion` unchanged in npm packager help. The corrected matcher recognizes complete selected paths while preserving `coding/pigversionish`, already-internal paths, upstream paths, and user configuration paths. The complete Python suite has 42 passing tests. `test_private_package_comment_references` also fails on stale version-pin source comments; those comments now follow the selected path table. The strengthened unchanged-SDK fixture catches an intermediate attempt to rewrite a host-source reference inside `extensions/sdk`; that separate module remains excluded from repository-comment edits. No lint suppression or divergence is added.

The four public packages retain their 5,395 entries in the owning generated Go inventory exactly; the number is a measurement of that inventory, not a fixed acceptance threshold. Raw `go doc -all` comparisons are identical for `agent`, `ai`, and `tui`. For `coding`, the only difference is the package comment's relocated version-pin source path and Go's resulting line wrapping. `coding/doc.go` and all public API declarations remain unchanged.

## Rehearsal evidence

The rehearsal uses the same commands and isolation as [REHEARSAL.md](REHEARSAL.md): physical Go 1.27.1, Node 24.19.0, Python 3.12.3, Rust/Cargo 1.97.1, tmux 3.7b, and real Pi 0.87.1. npm is 11.17.0 on this host rather than the documented 12.1.0. No tool is installed or upgraded. HOME, PiG agent, and Pi agent directories are fresh temporary directories. Installed dependencies and the pinned mirror are read-only links; compiler caches are shared. Windows checks are cross-build and cross-vet evidence, not native execution.

| Property | Original all-internal variant | Public-library variant |
| --- | --- | --- |
| Tracked files moved | 3,354: 3,347 implementation + 7 governance | 26: 19 private-helper + 7 governance |
| Existing files rewritten | 1,559, including 1,341 Go files | 102, including 23 Go files |
| Public Go libraries | Only the separate extension SDK; core libraries become internal | Four root libraries and retained subpackages, plus the separate extension SDK |
| `coding/doc.go` | Rewritten as an in-tree API | Unchanged public-SDK documentation |
| External embedding module | Fails: public imports removed | Pass |
| Linux and Windows `go build ./...` | Pass | Pass |
| Linux and Windows `go vet ./...` | Pass | Pass |
| Lint configuration and full `make lint` | Pass | Pass |
| `make ci-contracts ci-drift` | Stops at pending/partial hot-path test ports | Same blocker |
| Separate `make ci-drift` | D78 lacks `SCRUTINIZED:approved` | Same blocker |
| Remaining format, custom-factory, SDK-surface, divergence-guard, source-hygiene, and docs gates | Pass | Pass |
| Full `go test -json -count=1 ./...` | Three inherited `parity/closure` failures only | Identical failures; no new failures |
| Complete real-Pi JSON family | 01–03 pass; 04 fails | Identical outcomes and failure text |
| Six strict process comparisons with the unmoved binary | Exit status, stdout, and stderr match | Exit status, stdout, and stderr match |

The rewritten-file totals exceed the earlier [REHEARSAL.md](REHEARSAL.md) totals because the shared Go section now updates repository source paths in comments as well as import literals. The selected move sets do not change. The second complete application of each final variant performs zero moves and rewrites; tracked contents, modes, and raw index bytes compare equal.

The unmoved candidate reproduces both release-gate blockers. They remain owner blockers, not passing gates. Source/test/behavior-input/version-delta denominators retain their bytes. All 422 scenario TOMLs retain their `assert`, `covers`, and `diverge` values. Neither the codemod nor this variant changes a runtime comparator, grants divergence approval, or promotes coverage.

The baseline, initial two variants, and final two variants all fail only these full-suite tests:

- `TestImportCurrentDenominatorsAccountsEveryLedgerRow`;
- `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`;
- `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`.

They reject the inherited divergence accounting. The extension-conformance failure recorded in the earlier rehearsal does not reproduce in any of these five runs; no fix is claimed for it.

JSON scenario `04-json-real-provider-error` fails at `/7/message/api`: PiG emits an empty string and Pi emits `openai-completions`. Pi initializes the assistant's API/content at `packages/ai/src/api/openai-completions.ts:308-325` and formats the provider error at `:701-713`. This runtime mismatch is outside the layout change and remains an owner blocker. The outcome comparison removes only checkout prefixes and timing fields from outcome metadata; it does not normalize product output or alter scenario comparators. The passing scenarios exercise Pi's header/event output at `packages/coding-agent/src/modes/print-mode.ts:107-126` and extension-command dispatch before prompting at `packages/coding-agent/src/core/agent-session.ts:1616-1625`.

The strict binary comparisons cover version, help, empty model-list diagnostics, a faux-provider print turn, RPC command enumeration, and an invalid flag. Both final variants match the unmoved binary's raw stdout, stderr, and exit status in every case. These checks do not turn the unrelated failed gates into release approval.

The final paired gate run lasts 713 seconds (2026-09-27 16:48:34–17:00:27 PDT). A separate branch run passes the Python regressions that exercise the Go scanner, scanner-package compilation, full vet, Windows scanner vet, focused scanner lint, full lint, and staged docs/source-hygiene checks. `make lint-changed` cannot find a merge base with `main`; full lint passes instead. Branch contracts/drift reproduce the same blockers. Generated branch inventories and coverage are restored before committing. No integration branch is merged or transformed by this rehearsal.

## Retained evidence

`tmp/layout-public-variant/retained-evidence.tar.gz` has SHA-256 `a066ee0a71bb1b9651e4b4687d5a62cdfb145db08abdd0b31458f530ba917be1`. It contains all five gate runs, raw JSONL test events and parity artifacts, external-import results, raw process comparisons, API comparisons, move/contract audits, idempotence snapshots, red regressions, and the local verification drivers. `comparison.json` records identical test failures and JSON outcomes for every variant. `audit-final-public-post-tests.json` and `audit-final-internal-post-tests.json` retain the exact moved and rewritten file lists. The complete second-application snapshots compare tracked bytes, modes, and raw index hashes; the post-test audits also match the pre-test audits.
