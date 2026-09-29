<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

## layout-build

This section runs before the B+ root cleanup. The complete release command is `run.sh --keep-public-libraries`; `release.py` subsequently moves the test tree, parity authorities, and media, and updates the remaining build/configuration references. See [RELEASE-REHEARSAL.md](RELEASE-REHEARSAL.md).

Run `bash automation/layout/build.sh /absolute/path/to/checkout` through the shared `run.sh` entry point. The section requires Bash, Git, and Python 3. It rewrites tracked files and does not move files or alter the Git index. It validates required files, all selected input files, and structural replacements before writing. Missing files, symlinks, or unrecognized scanner structures fail the section. A second application makes no content or mode changes.

The default package path table maps `agent/`, `ai/`, `coding/`, and `tui/` to their `internal/` destinations. With `--keep-public-libraries`, the shared `plan.py` table maps only `coding/pigversion`, `tui/parity`, and `tui/termsim`; public-library scanner roots stay unchanged. The governance table matches the docs section: community policies move to `.github/`; governance, maintainers, and quickstart move to `docs/project/`. Root attribution, REUSE, citation, parity ledgers, and agent instructions stay at the root.

The section owns these path consumers:

- `Makefile` and `automation/make/` recipes, including benchmarks, module tests, fixture builds, and upstream-version lookup.
- `.github/` non-Markdown metadata, workflows, issue templates, CODEOWNERS, Dependabot, Security Insights, and CodeQL roots.
- Automation scripts, Python path operands, image build inputs, model generators, vendoring destinations, release/npm version lookup, and automation documentation.
- `.gitattributes`, `.gitignore`, `.gitleaks.toml`, `.golangci.yml`, `REUSE.toml`, `.dockerignore`, `.coderabbit.yaml`, `.devcontainer/`, and `docs/site/public/install.sh` when they contain affected paths.
- Divergence-guard baseline file names and the `divguard:path` headers in its snippet fixtures. Snippet bodies and upstream provenance comments remain unchanged.

Go source belongs to the Go section. Non-automation Markdown, parity evidence, and parity source fixtures belong to the docs section. The automation directory stays at the root: it already separates build tooling from implementation packages. The installer, npm launcher, image locks, and lint configuration currently need no content changes; their paths and formats are unchanged.

Replacements distinguish repository paths from upstream paths and user state. They preserve `packages/ai`, `agent/src`, vendored `pi-ai` names, `$PIG_HOME/agent`, `$home/agent`, and `$agent/dist`. Only the listed checkout-root variables qualify a shell path for rewriting. Python scanner inventories use explicit structural replacements instead of treating every bare `"agent"` or `"ai"` string as a directory. The public-claims scanner retains its previous source coverage without adding the moved agent/ai/tui trees or losing the moved community policies. The port-map scanner visits each implementation file once.

### Tests

```sh
python3 -m unittest discover -s automation/layout -p test_build.py -v
```

The tests exercise transformation behavior and real temporary Git indexes, not workflow-YAML snapshots. They cover path boundaries, attribution/security selectors, Python Path operands, scanner coverage, section ownership, missing inputs, executable modes, untracked files, and rerun identity. `test_divguard_fixture_moves_only_its_repository_path_header` was red before the fixture rule: the transformed production guard rejected its old-path fixtures. The same guard suite passes after applying the rule. `test_public_claims_enumerates_only_existing_prose_in_partial_trees` was red when moved policies became mandatory scan inputs. The original root glob enumerates existing prose; compliance separately checks required files. The corrected rule passes that regression and the unchanged `tests/docs-drift` caller tests.

### Integration evidence

The reviewed integration base is `32086333e4fdddd30f8967ef2e66f97b296c0c5d`. On that base this section moves zero files and rewrites 47 tracked files. This count is a report from the script, not a fixed test expectation. Generated inventories remain the responsibility of their owning regeneration commands.

The section changes repository tooling, not Pi runtime behavior. No new runtime parity scenario or divergence is claimed. Real-Pi before/after family verification belongs to the shared Go-layout application. Keep root `CHANGELOG.md` unchanged in this branch; the docs section inserts the shared layout bullet under `Unreleased` when the codemod runs.

A second scratch application uses integration commit `9ef2715ed1f45a82ce62eaaed92dfd54151f298a` and all three available sections. The build section still rewrites 47 files. The second full application changes no tracked content, file mode, or index bytes. The final build-section rerun reports zero rewrites.

| Verification on the transformed tree | Result |
| --- | --- |
| `make build` | Pass |
| `go vet ./...` | Pass |
| `GOOS=windows go vet ./automation/ci/... ./tests/ci-images ./tests/docs-drift` | Pass |
| `go test ./automation/ci/... ./tests/ci-images ./tests/docs-drift` | Pass |
| `go tool golangci-lint config verify`; focused `go tool golangci-lint run`; `make lint` | Pass |
| Python build-codemod, CI, release, and npm tests; Node npm launcher tests | Pass |
| Compliance file, citation, and pin checks | Pass |
| `make docs-drift source-hygiene divergence-guard` | Pass |
| `make ci-contracts ci-drift` | Blocked at the docs-owned extractor tests: their relative `../../../coding/pigversion/pigversion.go` URL remains unchanged |
| `make ci-drift` independently | Blocked by D77/D78 missing `SCRUTINIZED:approved`; also reproduced on the unmoved base |
| `make lint-changed` | Cannot find a merge base with `main` in the disposable clone; full lint passes |

The extractor failures name `parity/interface-extractor/test/correspondence-inventory.test.mjs:9` and `parity/interface-extractor/test/inventory.test.mjs:8`. They are not build-section skips or passing gates.

The generator branch `a21906ec31` is not part of either integration snapshot. Its `automation/gen/extension-surface/manifest.json` `vendorDestination`, `main.mjs` runtime destination, and `model.mjs` pin path all match the build-section rewrite table. The transformation was checked against those exact branch files. Regenerate its output with its owning `extension-sync` command after integrating the generator; do not hand-edit declaration-relative paths or ownership hashes.

Run the complete entry point and affected tests on a disposable clone of the final integration commit. Keep that clone under `tmp/` when it lives inside a worktree, so source-inventory gates do not discover a second repository. Record build, vet, Windows vet, lint, Python CI/release tests, `go test ./automation/ci/... ./tests/ci-images`, and `make ci-contracts ci-drift` outcomes. Do not interpret an early gate failure as a pass for later prerequisites.
