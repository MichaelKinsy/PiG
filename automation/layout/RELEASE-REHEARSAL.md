<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# B+ release-layout rehearsal

The owner selects B+: the public-library boundary plus a root cleanup. `--keep-public-libraries` selects this complete layout. It no longer selects the earlier library-only B layout. This delivery changes the codemod, its tests, documentation, and release-note fragment. No integration branch or committed production tree is transformed.

The rehearsal input is `1cd6ad69fb12c55963510991e0326ce99678b913`. Its production candidate is `e04490990bc7248e314aa83873dd895362dc4bbe`, with the 422 scenarios used in the earlier rehearsals. The upstream oracle remains real Pi 0.87.1.

```sh
bash automation/layout/run.sh --keep-public-libraries /absolute/path/to/disposable-checkout
```

## Selected layout

| Input | Release location |
| --- | --- |
| `agent/`, `ai/`, `coding/`, `tui/` | Keep public at the module root |
| `coding/pigversion`, `tui/parity`, `tui/termsim` | Move under `internal/` |
| `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `SECURITY.md`, `SUPPORT.md` | `.github/` |
| `GOVERNANCE.md`, `MAINTAINERS.md`, `QUICKSTART.md` | `docs/project/` |
| `DIVERGENCES.md`, `PORT_MAP.md`, `DIVERGENCE-IDS.txt` | `docs/parity/` |
| `parity/` | `test/parity/` |
| `tests/` | `test/`, including `test/integration/` |
| `evals/` | `test/evals/` |
| `media/` | `docs/media/` |
| `cmd/`, existing `internal/`, `extensions/`, `piglets/`, `examples/`, `docs/`, `automation/` | Keep at the root |
| `changelog.d/` | Preserve nonempty release notes; remove the directory when empty |

Keep root licensing and discovery files, the Go module/workspace manifests, `Makefile`, `AGENTS.md`, `embed.go`, and dotfiles. The public `extensions/sdk` module retains every tracked byte. `coding/doc.go` retains its public-SDK wording and bytes. The root `embed.go` resource-only exception remains unchanged.

The [public-library audit](PUBLIC-VARIANT.md#package-boundary-audit) still applies. Pi publishes the four reference libraries in `packages/agent/package.json:2-10`, `packages/ai/package.json:2-17`, `packages/coding-agent/package.json:2-18`, and `packages/tui/package.json:2-6,53`. Pi exports session construction at `packages/coding-agent/src/index.ts:235-253` and TUI width helpers at `packages/tui/src/index.ts:147-156`. The cleanup does not hide those capabilities.

## Root listings

These counts come from Git-tracked source entries, not build outputs, `.git`, or the read-only upstream link. The frozen production candidate has 51 entries. The rehearsal input has 52 because the preceding lanes added `changelog.d/`. The B+ result has 39 while those release notes remain. It has 38 after the release owner folds the notes and the empty directory is removed. The codemod does not discard unpublished notes to meet a count.

Before, 52 entries:

```text
.agents              .coderabbit.yaml       .devcontainer
.dockerignore        .editorconfig         .gitattributes
.github              .gitignore            .gitleaks.toml
.golangci.yml        .node-version         AGENTS.md
CHANGELOG.md         CITATION.cff          CODE_OF_CONDUCT.md
CONTRIBUTING.md      DIVERGENCE-IDS.txt    DIVERGENCES.md
GOVERNANCE.md        LICENSE               LICENSES/
MAINTAINERS.md       Makefile              NOTICE
PORT_MAP.md          QUICKSTART.md         README.md
REUSE.toml           SECURITY.md           SUPPORT.md
THIRD_PARTY_NOTICES.md
agent/               ai/                   automation/
changelog.d/         cmd/                  coding/
docs/                embed.go              evals/
examples/            extensions/           go.mod
go.sum               go.work               go.work.sum
internal/            media/                parity/
piglets/             tests/                tui/
```

After, 39 entries:

```text
.agents              .coderabbit.yaml       .devcontainer
.dockerignore        .editorconfig         .gitattributes
.github              .gitignore            .gitleaks.toml
.golangci.yml        .node-version         AGENTS.md
CHANGELOG.md         CITATION.cff          LICENSE
LICENSES/            Makefile              NOTICE
README.md            REUSE.toml            THIRD_PARTY_NOTICES.md
agent/               ai/                   automation/
changelog.d/         cmd/                  coding/
docs/                embed.go              examples/
extensions/          go.mod                go.sum
go.work              go.work.sum           internal/
piglets/             test/                 tui/
```

## Implementation and regression evidence

The existing sections apply the public-library and governance changes. `release.py` then applies one root-cleanup table. It preflights tracked paths, destination collisions, partial layouts, source rewrite anchors, and unexpected root entries. It rejects any rewrite that changes a scenario's `assert`, `covers`, or `diverge` fields. It does not rewrite pinned upstream denominators, vendored sources, historical captured logs/diffs, or the independent SDK module.

The shared Go scanner identifies path-root arguments in `filepath.Join` and `path.Join`. Import rewrites use parsed literal offsets. A segment classifier such as `"/parity"` or `"/tests/"` is not a repository-root path. Paths after an ordinary first component, such as `filepath.Join(root, "tmp", "parity", run)`, remain unchanged. Relative repository references rebase from both the caller and destination. Python `Path(__file__).parents`, Make recipes that change directory, fixture parent creation, and ledger-root derivation have explicit owning rules.

The source fixes preserve these contracts:

- The first release tests fail before the cleanup implementation. The fixture then proves nested moves, Go imports and joins, oracle-relative paths, Markdown links, public SDK preservation, generated-denominator preservation, collision rejection, scenario-contract rejection, and rerun identity.
- Missing `.mk` and `.cjs` ownership leaves coverage and a pinned radius oracle at retired paths. Those source types now participate in the rewrite.
- Quoted root-relative commands such as `"./parity/cmd/coverage"` are repository commands, not unresolved caller-relative files. The coverage drift check now uses `./test/parity/cmd/coverage`.
- Moving a ledger does not move the repository root to the ledger's parent. Coverage, source reconciliation, and the additive-ledger lookup now derive the correct root. The pre-fix coverage drop from 414 to 304 behavioral entries exposes the wrong lookup; the corrected generator returns the unchanged 414 without promoting a record.
- Fixture writers create `docs/parity/` before writing relocated ledgers. Their rejection assertions remain intact.
- Segment classification remains independent of relocation. The pre-fix async checker compares identical expressions, and `TestCandidatePackageExcludesTestAndParityHelpers` wrongly admits the private TUI harness. Preserving directory classifiers fixes both at the rewriter.
- A pinned Git baseline is historical evidence. The verifier resolves its unique mapping filename within the pinned commit and rejects a missing or ambiguous candidate. It still validates the complete committed version and ported-file set. No baseline commit, ported count, hot-path tag, or policy schema changes. The existing committed-baseline regression gains a current-file rename case; fabricated and missing baselines still fail.
- `test/evals/pigeval/registry.py` derives the repository root above both `test/` and `evals/`. Its pre-fix mutation corpus is empty. The unchanged seeded-generation test detects that error.

The Python codemod suite has 47 passing tests. The count is a measurement, not an acceptance threshold. No runtime comparator, lint suppression, or divergence approval is weakened.

## Reproduction

Use physical Go 1.27.2 on PATH, not a shim that selects another toolchain inside fixture modules. Use the isolation procedure from [REHEARSAL.md](REHEARSAL.md): temporary HOME, PiG agent, and Pi agent directories; a system temporary directory; shared compiler caches; and read-only links to the pinned mirror and installed Node dependencies.

Run the complete entry point twice and compare tracked contents, modes, and the raw index. Then run the same gates as before, using the relocated parity command:

```sh
python3 -B -m unittest discover -s automation/layout -p 'test_*.py' -v
python3 -B automation/layout/external_imports.py "$PWD"
go build ./...
GOOS=windows go build ./...
go vet ./...
GOOS=windows go vet ./...
go tool golangci-lint config verify
make lint
make ci-contracts ci-drift
make ci-drift
make -k format-version-inventory custom-factory-ledger-drift sdk-surface-drift divergence-guard source-hygiene docs-drift
go test -json -count=1 ./... > "$isolation/go-tests.jsonl"
make parity-bin
PIG_PARITY_PIG_BIN="$PWD/bin/pig-parity" \
PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi" \
go test -tags=parity -count=1 -timeout 20m ./test/parity/runner -args \
  -pig-parity.dir="$PWD/test/parity/scenarios/json" \
  -pig-parity.tags=hermetic \
  -pig-parity.results="$isolation/json-parity.json"
make evals-test
```

A contracts failure prevents its following target from running; run drift and the remaining prerequisites separately. Do not interpret the inherited failures as passing release evidence.

## Results

The final application moves 1,667 tracked files: 26 from the public-library/governance selection and 1,641 from the root cleanup. It rewrites 624 existing files, including 219 Go files. These totals are measured against the input Git tree, not fixed acceptance thresholds. All 422 scenario contract fields and the pinned upstream/source/test/behavior-input/version-delta denominator bytes remain unchanged. The four public packages retain all 5,395 compared Go inventory entries. The external embedding module builds successfully.

The second complete application reports zero moves and rewrites. Tracked contents, modes, and raw index hashes compare equal. The post-test move/contract audit also matches the pre-test audit.

| Gate | Unmoved input | B+ result |
| --- | --- | --- |
| Linux and Windows `go build ./...` | Pass | Pass |
| Linux and Windows `go vet ./...` | Pass | Pass |
| Lint configuration and full `make lint` | Pass | Pass |
| `make ci-contracts ci-drift` | Stops at pending/partial hot-path test ports | Same blocker; committed baseline preserved |
| Separate `make ci-drift` | D78 lacks `SCRUTINIZED:approved` | Same blocker |
| Remaining format, custom-factory, SDK-surface, divergence-guard, source-hygiene, and docs gates | Pass | Pass |
| Full `go test -json -count=1 ./...` | Three `parity/closure` failures | Same closure failures plus the known packed-provider failure |
| Complete JSON family against real Pi 0.87.1 | 01–03 pass; 04 fails | Identical outcomes and failure text |
| Six strict process comparisons | Reference | Exit status, stdout, and stderr match |
| `make evals-test` | One stale registry-version expectation fails | Same failure; seeded mutation generation now passes |

The three closure failures are `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`. They reject the inherited divergence accounting.

`TestProviderObjectsAcrossSDKs/go-reader-packed` fails with `Provider stream ended without a terminal event`. This failure was already recorded in the earlier all-internal rehearsal. A new diagnostic probe of the unmoved input reproduces it in 2 of 10 invocations. The B+ full run also fails it. All failures are retained; no later pass, retry, longer timeout, or skip is used to claim a fix. It remains an owner blocker outside the layout change.

The extra eval check exposes an inherited failure in `test_repository_registry_reads_pig_own_version`: it expects `own_version_args == ["version"]`, while the current registry returns `[]`. The unmoved and corrected B+ trees fail that same assertion. The ResourceWarnings from the existing eval suite also remain. No eval assertion or budget is changed.

JSON scenario `04-json-real-provider-error` still fails at `/7/message/api`: PiG emits an empty string and Pi emits `openai-completions`. Pi initializes that field at `packages/ai/src/api/openai-completions.ts:308-325` and formats the error at `:701-713`. The passing event/command behavior follows `packages/coding-agent/src/modes/print-mode.ts:107-126` and `packages/coding-agent/src/core/agent-session.ts:1616-1625`. Outcome comparison removes only checkout prefixes and measured runtimes from report metadata; no product output or scenario comparator is changed.

The strict process comparisons cover version, help, empty model-list diagnostics, a faux-provider print turn, RPC command enumeration, and an invalid flag. They compare raw output and exit status against the unmoved binary, not a normalized transcript.

The qualified toolchain is physical Go 1.27.1, Node 24.19.0, Python 3.12.3, Rust/Cargo 1.97.1, tmux 3.7b, and installed Pi 0.87.1. npm is 11.17.0, as in the earlier rehearsal; no tool is installed or upgraded. Windows evidence is cross-build/vet, not native execution. The final B+ gate run takes 655 seconds, from 2026-09-27 18:03:53 to 18:14:48 PDT. The initial pilot lacked the extractor's Node dependencies before running direct Go tests; the documented `make interface-deps` setup resolves that setup error. The full qualified gate runs install those repository dependencies through the existing prerequisite.

Branch checks also run the 47 Python regressions, scanner-package compilation, full vet, Windows scanner vet, focused scanner lint, full lint, contracts/drift, and docs/source checks. `make lint-changed` cannot find a merge base with `main`; full lint passes instead. Generated branch inventories and coverage are restored before committing. No integration branch is merged or transformed.

## Retained evidence

The archive is `tmp/layout-release/retained-evidence.tar.gz`, SHA-256 `7d095e1e2f854c1a0e47b5ca1f9359b15be8a2344d490cf7a80a8a5b3dc3d101`. It retains the baseline and each rehearsal run, raw JSONL test events and parity artifacts, failed source-path probes, external-import proof, raw binary comparisons, public-API comparison, source/contract audits, root listings, idempotence snapshots, eval comparisons, and the unmoved packed-provider diagnostic probe.

`audit-final-2-post-tests.json` records every moved and rewritten file. `root-listings.json` records the before/after source entries. `comparison.json` records unchanged JSON outcomes and separates the inherited closure and packed-provider failures. The exact local drivers are retained beside their logs. No failed run is replaced by a later success.
