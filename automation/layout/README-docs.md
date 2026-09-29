<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

## layout-docs

This section runs before the B+ root cleanup. Use `run.sh --keep-public-libraries` for the complete owner-selected release layout. `release.py` subsequently moves the parity authorities to `docs/parity/`, the parity tooling to `test/parity/`, and media to `docs/media/`, and rebases their links. The historical sections below describe this section's earlier standalone scope. See [RELEASE-REHEARSAL.md](RELEASE-REHEARSAL.md).

This section implements documentation and evidence path changes for the layout in [Go's official module guide](https://go.dev/doc/modules/layout). Run it through `automation/layout/run.sh` after merging all sections. For a focused scratch test, run `bash automation/layout/docs.sh /absolute/path/to/checkout` after the Go section. With no argument, the wrapper uses its own checkout root.

The optional `--keep-public-libraries` flag selects the shared private-helper path table. It preserves the public SDK, RPC-client, and message-type prose, including the pkg.go.dev links. The governance and automation-table prose edits still apply. The remaining descriptions of internal Session construction below apply only to the original default variant.

### Governance and repository map

| Existing root file | Destination | Discovery requirement |
| --- | --- | --- |
| `CODE_OF_CONDUCT.md` | `.github/CODE_OF_CONDUCT.md` | GitHub community-health discovery |
| `CONTRIBUTING.md` | `.github/CONTRIBUTING.md` | GitHub contribution guidance |
| `SECURITY.md` | `.github/SECURITY.md` | GitHub security policy and OpenSSF Scorecard |
| `SUPPORT.md` | `.github/SUPPORT.md` | GitHub support guidance |
| `GOVERNANCE.md` | `docs/project/GOVERNANCE.md` | Linked from the root README and other policies |
| `MAINTAINERS.md` | `docs/project/MAINTAINERS.md` | Linked from the root README and governance |
| `QUICKSTART.md` | `docs/project/QUICKSTART.md` | Repository rehearsal guide, distinct from the website quickstart |

GitHub accepts the four community-health files in `.github/`, the root, or `docs/`: see [default community-health files](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/creating-a-default-community-health-file). Scorecard's [`isSecurityFile`](https://github.com/ossf/scorecard/blob/main/checks/raw/security_policy.go) explicitly recognizes `.github/security.md` case-insensitively. Neither requires those files at the root.

Keep `README.md`, `LICENSE`, `NOTICE`, `CHANGELOG.md`, module/workspace manifests, `LICENSES/`, `THIRD_PARTY_NOTICES.md`, `CITATION.cff`, and `REUSE.toml` at the root. [REUSE](https://reuse.software/spec-3.3/#license-files) requires `LICENSES/` there. Keeping the single root REUSE manifest preserves its whole-tree annotation scope. GitHub's [citation discovery](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-citation-files) requires a root citation file. Keep the root license for GitHub license discovery and notices for distribution. No notice or license text changes except repository path references.

Keep `AGENTS.md`, `PORT_MAP.md`, and `DIVERGENCES.md` at the root. They are maintenance authorities read by existing gates and agents, not community-health files. Keep `parity/`, `tests/`, `evals/`, `examples/`, and `automation/` as separate tooling roots. Their existing boundaries distinguish evidence, tests, evaluations, examples, and maintainer commands; moving them would add fixture and ledger churn without improving the internal Go boundary. No `pkg/` directory is introduced. The public `extensions/sdk` module stays in place.

### Rewrite ownership

`docs.py` owns tracked Markdown outside `automation/` and vendored trees, the knowledge-graph source, and the explicit non-Go parity suffix set. It does not follow `node_modules` or the upstream mirror. The build section owns automation documentation, GitHub configuration, workflows, compliance path consumers, and release scripts. The Go section owns Go import literals, source markers in Go comments, and Go fixture path literals.

The path tables update:

- PiG target paths in `PORT_MAP.md`, divergence call-site references, additive records, and module `AGENTS.md` files;
- repository-relative Markdown links, including outgoing links from moved documents, fragment identifiers, reference definitions, and HTML `src`/`href` destinations;
- public and embedded documentation references, current-branch PiG source URLs, and Go import examples;
- reviewed parity mapping targets, unit evidence, scenario path references, and oracle-helper version-pin reads;
- `parity/interface-extractor/test/inventory.test.mjs` and `parity/interface-extractor/test/correspondence-inventory.test.mjs`, whose relative pin becomes `../../../internal/coding/pigversion/pigversion.go` through `RELATIVE_PIN_PATHS`;
- field IDs and paths in `parity/format-versions.toml`, retaining each complete reviewed record and sorting by its rewritten ID; and
- `docs/knowledge-graph/pig.graph.json`, followed by its existing generator to refresh the site, embedded reference, JSON-LD, Mermaid, and troubleshooting mirror.

Upstream `packages/...` paths, abbreviated TypeScript source references, canonical `pkg:ai/...` interface IDs, external URLs, commit-pinned source links, and user configuration paths such as `~/.pig/agent` remain unchanged. The script does not edit source/published denominators, historical text captures, or generated candidate/coverage inventories. The entry point regenerates Go candidates, recommendations, coverage, the custom-factory ledger, and the normalization inventory through their owning commands. No comparator, coverage status, divergence approval, or interface disposition is promoted.

The script preflights required tracked files, source/destination conflicts, symlinks, and reviewed prose replacements before moving governance files with `git mv`. It enumerates only Git-tracked edit targets. Missing inputs and unknown prose shapes fail rather than silently skipping work. A completed or partially completed governance move can resume; a second complete application reports no moves or rewrites. Relative instruction references use an explicit `./` when rebasing would otherwise make them indistinguishable from root paths.

`docs_prose.py` contains the reviewed changes that are not path substitutions. The SDK pages and their referring pages distinguish the unchanged public extension SDK from internal Session construction and the internal RPC client. External applications use RPC rather than importing internal packages. The repository-quality policy and README describe the approved layout instead of preserving the contradictory public-package/root-governance policy. The script inserts one idempotent bullet under `CHANGELOG.md`'s `Unreleased` Fixed section; it does not add release notes to a published version. The changelog itself is not changed in the codemod delivery commit.

### Relocated oracle scripts

`MOVED_ORACLE_ROOTS` rebases the repository root in the tool-validation, terminal-color, fork-snapshot, and telemetry-schema scripts under the moved implementation trees. Each script must contain exactly one old or new quoted root. Telemetry's generated Go destination and regeneration commands also follow the package move. The installed Pi version checks and upstream module paths remain unchanged. The caller color test and the three fixture-generation commands verify these paths against the pinned Pi implementation.

### Upstream generator integration seam

The separate upstream-sync generator at `a21906ec31` is not part of the tested integration base. Its generator configuration needs the following changes when that branch is integrated. These are source/configuration changes, not permission to edit generated declarations or their hashes by hand.

| Generator input | Required layout change |
| --- | --- |
| `automation/gen/extension-surface/manifest.json` | Prefix `vendorDestination` with `internal/`. Keep Pi package roots, canonical interface IDs, upstream paths, and release integrities unchanged. |
| `automation/gen/extension-surface/model.mjs:loadInputs` | Read the pin at `internal/coding/pigversion/pigversion.go`. |
| `automation/gen/extension-surface/main.mjs` | Set the runtime output root to `internal/coding/extension/host/subprocess/runtime-node/generated`. |
| `automation/gen/extension-surface/pi.test.mjs` | Point its generated-module URL at the relocated runtime. |
| `extension-sync-outputs.json` | Translate owned `coding/...` paths with the same move table before regeneration. Preserve their existing content hashes at the move boundary. Otherwise the publisher sees the moved files as unowned and attempts to unlink missing old paths. |
| `parity/extension-sync-bindings.toml` and `parity/extension-sync-exceptions.toml` | Translate local target/evidence paths if present; retain member IDs and dispositions. |

The build section owns the automation source/configuration edits. The docs section's parity path rules handle the bindings/exception ledgers when present. Regenerate with `make extension-sync`, then run `make extension-sync-test extension-sync-drift`. `main.mjs` calculates declaration-import traversal with `path.relative`, so regeneration adds the extra parent traversal and refreshes manifest/provenance hashes. Run `make extension-sync-check` separately and report unimplemented members; do not treat generator drift passing as behavioral closure. The generator's own README records unresolved implementation obligations.

### Verification

Run the focused codemod tests with:

```sh
python3 -B -m unittest discover -s automation/layout -p test_docs.py -v
bash -n automation/layout/docs.sh
```

Scratch verification uses integration commit `9ef2715ed1`, Go section `ef672f8525`, build section `e62c235a95`, Go 1.27.1, Python 3.12.3, and the real Pi 0.87.1 comparator. The docs section moves seven files and rewrites 113 files, including generated knowledge-graph mirrors. These counts describe that scratch input, not acceptance thresholds.

The extractor-pin follow-up rechecks the delivered `a24ef379db` scripts from a fresh checkout. Both extractor tests are explicit required inputs in `RELATIVE_PIN_PATHS`. Each accepts exactly one old or new relative pin path; a missing file or unexpected path fails preflight. The applied diff changes only each test's pin URL to `../../../internal/coding/pigversion/pigversion.go`. All 14 codemod tests and all 50 extractor tests pass on this rerun.

Red-to-green regressions cover upstream-ID corruption, relative instruction rerun drift, symlink-parent redirection, relative extractor pin reads, and sorted format records. The remaining tests cover missing inputs, collisions, partial moves, untracked-file isolation, Markdown rebasing, generated mirrors, and the reviewed SDK prose boundary. No Pi runtime behavior is added or changed by this section, so no new runtime parity scenario is appropriate.

Verification results:

- Full entry point: successful application; second application preserves tracked contents and index entries.
- Build and vet: `go build ./...` and `go vet ./...` pass.
- Windows cross-vet: passes for `internal/pigdocs`, `tests/docs-drift`, and the coverage, scenario lint, test inventory, interface inventory, and behavior-check command packages. This is not native Windows execution proof.
- Selected-package golangci-lint: configuration verifies; zero issues.
- Selected-package Go tests: all seven packages above pass.
- Knowledge-graph generator `--check`: passes.
- `make ci-contracts`: passes, including all 50 extractor tests and the SDK surface drift gate.
- `make ci-drift`: reaches the inherited D77/D78 missing-approval failures. Those same pending records block the pre-layout base; no approval or gate is weakened. The remaining `divergence-guard`, `source-hygiene`, and `docs-drift` targets pass when run separately.
- `make parity-family FAMILY=ai-sdk`: passes against real Pi 0.87.1. Its unchanged Cloudflare helper oracle includes `packages/ai/src/providers/cloudflare-stream.ts:6-14`; no helper semantics are translated by this codemod.
- All 303 tracked scenario TOML files retain their `covers`, `assert`, and `diverge` values. All six retained source/test/behavior-input inventory files retain their bytes.

The generator integration seam above and the root resource-only `embed.go` exception recorded by the Go section require integrator/owner review. They are not silently resolved by documentation rewrites.
