<!--
SPDX-License-Identifier: MIT
-->

# Release module-layout codemod

The owner selects B+: public libraries plus the root cleanup. Apply it only to a disposable checkout or a reviewed migration checkout. Do not transform an integration branch during rehearsal. The layout follows [Organizing a Go module](https://go.dev/doc/modules/layout).

```sh
bash automation/layout/run.sh --keep-public-libraries /absolute/path/to/checkout
```

This flag selects the complete release layout, not the earlier library-only B layout. It keeps `agent/`, `ai/`, `coding/`, and `tui/` public, including `coding/doc.go`'s public-SDK wording. It moves the three private helpers, groups parity and tests under `test/`, moves the parity authorities to `docs/parity/`, and moves media to `docs/media/`. See [RELEASE-REHEARSAL.md](RELEASE-REHEARSAL.md) for the complete move list and current evidence. The original no-flag all-internal transform remains a historical comparison tool, not the release command.

The entry point requires every section before changing files. On an original tree it calls `go.sh`, `build.sh`, and `docs.sh` with the selected flag and checkout root, then runs `release.py`. Those three sections are building blocks, not complete release-layout commands. On a completed B+ tree it validates the public-library placement and reruns the cleanup and generators. It rejects a partially moved root. With no root argument, the entry point uses its own checkout root. After the moves it reads the current upstream pin and regenerates the Go inventory, recommendations, coverage, the custom-factory ledger, and the normalization inventory:

```sh
go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json
go run ./test/parity/cmd/interfacerecommend -inventory test/parity/interfaces/upstream-v<VERSION>.json -observable test/parity/interfaces/cli-v<VERSION>.json -go-inventory test/parity/interfaces/pig-go.json -out test/parity/interfaces/recommendations-v<VERSION>.json
make coverage RESULTS=
make custom-factory-ledger
make normalization-inventory
```

Empty `RESULTS` makes coverage independent of local parity-run artifacts. Regeneration recomputes derived hashes; replacing path text in generated inventories would leave invalid shape hashes.

The tools require Git, Bash, Python 3.11 or newer, and the repository's Go toolchain. Run only on a disposable checkout or an intentionally prepared migration branch. `git mv` stages moved files with their contents at move time. Other rewrites and later formatting can remain unstaged. Review both the index and the worktree. Do not run against a checkout with unrelated changes. Do not copy node_modules or the upstream mirror into tracked paths.

The [build section](README-build.md) and [docs section](README-docs.md) describe their path tables, scope, and verification.

## layout-go

The following table describes the historical no-flag transform, not the selected release layout. The public-library package audit remains in [PUBLIC-VARIANT.md](PUBLIC-VARIANT.md#package-boundary-audit). The release root cleanup is documented in [RELEASE-REHEARSAL.md](RELEASE-REHEARSAL.md).

| Existing path | Destination |
| --- | --- |
| `agent/` | `internal/agent/` |
| `ai/` | `internal/ai/` |
| `coding/` | `internal/coding/` |
| `tui/` | `internal/tui/` |
| `cmd/`, existing `internal/` | unchanged |
| `extensions/sdk/` | unchanged separate public module |
| `parity/`, `tests/`, `evals/`, `examples/`, `automation/` | unchanged tooling roots |
| `go.mod`, `go.work` | unchanged module identities and workspace members |
| `embed.go`, `CHANGELOG.md` | root resource-only package; exception awaiting owner decision |

Keep parity and tests at the root because their fixtures, ledgers, and generator commands already form explicit tool boundaries. Keep evals and examples separate from production packages. Keep automation under its existing root. Moving these directories adds reference churn without improving Go's implementation boundary.

`go.sh` validates the complete move plan before editing. Missing roots, source/destination collisions, mixed old/new layouts, missing tracked anchors, malformed Go, and unclassified root Go files fail loudly. It enumerates Git-tracked files only. The Go parser reads every Go source regardless of build constraints, including generated files and fixture modules. Rewrites use parsed literal byte offsets, preserve import aliases, and format only changed Go files. Package-relative embeds move with their assets; their patterns remain unchanged. The `ai` go-generate directive and SDK fixture module's local replace path gain the additional parent traversal. Workspace members do not move.

Repository-path rewrites in Go tools are separate from package imports. The explicit scope table in `go.py` avoids changing upstream `packages/ai` paths, Pi package names, keybinding identifiers, and user configuration directories such as `~/.pig/agent`. Unqualified path tokens must name tracked source files/directories or synthetic Go source fixtures; runtime storage such as `agent/models-store.json` remains unchanged. Relative references to tracked files rebase from both the caller and target location. Runtime directory prefixes such as `./` and `../` do not qualify as tracked files. Root-bound traversals in relocated test packages gain exactly one parent component, including collapsed `filepath.Join("../../../..", ...)` arguments. Module-local `replace` paths are rebased without changing module identities, including quoted paths and fixture modules added later. `extensions/sdk` changes are rejected.

The strict source table also updates the repository-layout test's old public-directory policy to assert the approved internal paths, the unchanged SDK, and absence of all four retired root directories. The key-release call-site guards retain their assertions with relocated file paths.

The root resource package embeds `CHANGELOG.md`. Go forbids an internal package from embedding a parent file. Retaining that one file avoids a duplicate changelog or a new build-time generation dependency; it contains no implementation logic. Resolve this exception with the owner before applying the final layout.

Run the codemod regression suite:

```sh
python3 automation/layout/test_go.py
go test ./automation/layout/scan
```

The tests exercise real Git moves, build-constrained imports, parsed comment imports, embed assets, nested module replacements, unchanged public SDK files, repository and governance fixture paths, malformed source and symlink preflight failures, and a second application with identical output. A second application performs no moves or content changes.

## Verification

The current-candidate rehearsal, regression evidence, remaining baseline failures, and application commands are recorded in [REHEARSAL.md](REHEARSAL.md). The older application below remains historical evidence, not the current release gate result.

Record the integration commit, section commits, tool versions, moved/rewritten counts, pre/post parity outcomes, and gate results with the delivery. Run the full entry point twice and compare tracked file contents and the index after each run. Run build, vet, Windows vet, `make ci-contracts ci-drift`, and affected tests on the transformed scratch tree. `layout-go` also runs `go test ./...` and the same real-Pi parity family before and after. A pre-existing failure is not a passing gate.

### Reviewed application

The combined codemod is verified against integration commit `9ef2715ed1f45a82ce62eaaed92dfd54151f298a` with Go 1.27.1, Node 24.19.0, Python 3.12.3, Rust/Cargo 1.97.1, and Pi 0.87.1. The build section is `e62c235a95`; the docs section is `89ecceed75`, with its private branch-name reference removed from the delivery README. Only codemod files are delivered; none of the scratch-tree moves are committed or pushed.

The application moves 2,081 implementation files and seven governance files. It changes the contents of 1,244 existing tracked files, including 1,079 Go files, one nested fixture `go.mod`, and regenerated ledgers. These are measured input-specific counts, not fixed test expectations. There are no missing moved files and no changes under `extensions/sdk`. Module identities and workspace members remain unchanged.

The complete second application reports zero moves and zero rewrites in every section. Tracked contents, file modes, index entries, and raw index bytes remain identical. All 303 scenario TOML files preserve their `assert`, `covers`, and `diverge` values. Upstream declaration, test, behavior-input, and version-delta denominators remain unchanged.

The 32 combined codemod tests pass. Meaningful failures caught during development include an invalid variadic `filepath.Abs`, rewritten upstream `pkg:` IDs, stale subprocess/Piglet fixture build targets, and a port-map fixture outside the relocated scanner roots. The final assertions retain those guards rather than relaxing the production checks. Disabling the import rewrite or removing the `pkg:` boundary makes the corresponding Python regression fail.

| Final transformed-tree check | Result |
| --- | --- |
| `make build` | Pass |
| `go vet ./...` and `GOOS=windows go vet ./...` | Pass; cross-vet is not native Windows execution |
| `make lint` with integration/live/parity build tags | Pass, zero issues |
| `make ci-contracts ci-drift` | Contracts pass; drift stops at inherited D77/D78 missing approval |
| `make docs-drift source-hygiene divergence-guard` | Pass after removing the private branch reference from the delivered README |
| Codemod, Go-module, docs/CI-image/upstream-parity, inventory, and resource tests | Pass |
| Subprocess fixture builders and fused multi-package overlay regression | Pass after translating their build targets |
| `go test ./...` | Fails in three packages that also fail on the unmoved base; details below |
| `json` parity family before/after | Both scenarios pass against real Pi; outcome manifests agree apart from checkout paths and timing measurements |

The JSON proof retains the existing comparators. Pi's event/header behavior is at `packages/coding-agent/src/modes/print-mode.ts:107-126`; extension-command dispatch is at `packages/coding-agent/src/core/agent-session.ts:1619` and `:1766`. No new Pi runtime behavior, comparator weakening, divergence approval, or lint suppression is introduced.

The final `go test ./...` run fails in `cmd/pig` (RPC extension-error shutdown and RPC queue scanning), `internal/coding/extension/host/subprocess` (timeout), and `parity/closure` (pending-divergence dashboard accounting). The unmoved integration tree reproduces all three package failures. The baseline and an intermediate transformed run also expose a large-session extension transport failure. The intermediate run found the codemod's port-map fixture issue; the final source fixes it and the complete upstream-parity test package passes in the final full suite. The remaining integration failures are owner blockers, not layout exceptions or passing gates.
