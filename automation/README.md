# Automation

Every script PiG runs outside of Go code lives here. The root `Makefile` is the
command interface: run `make help` for the everyday targets and
`make help-parity` for the parity machinery. Call a script directly only when
no target covers what you need, and add a target when you find yourself doing
that twice.

| Folder | Holds | Run from |
|---|---|---|
| `dev/` | machine setup and local developer tools | `make setup`, `make doctor`, by hand |
| `ci/` | gates and test runners that CI also calls | `make check`, `make test`, workflows |
| `gen/` | generators, vendoring, and the upstream mirror | `make upstream-mirror`, `make model-catalogs`, `make knowledge-graph` |
| `release/` | release manifest and SBOM checks | release workflow |
| `porter/` | Pig Porter runners | `make porter*` |
| `images/` | CI container images | `ci-images` workflow |
| `make/` | make fragments the root `Makefile` includes | `make help-parity` |

Every script finds the repository root from its own location, so it works from
any directory.

## Sandboxes, devcontainers, and hosted agents

The automation writes only where the environment allows. Set these when the defaults are not writable or not reachable:

| Variable | Default | Used for |
|---|---|---|
| `TMPDIR` | `/tmp` | every temporary file and directory, and the default `RESULTS` path. Scripts pass an explicit `mktemp` template because macOS ignores `TMPDIR` without one. |
| `XDG_CACHE_HOME` | `~/.cache` | `PIG_DEV_HOME` (the pinned toolchain and `env.mk`) and the upstream tarball cache |
| `PIG_DEV_HOME` | `$XDG_CACHE_HOME/pig-dev` | toolchains and machine settings from `make setup` |
| `SSL_CERT_FILE` | system trust | Go TLS behind an intercepting proxy or in a macOS sandbox; `make doctor` prints this fix when Go cannot verify the module proxy |
| `GOVULNDB` | `https://vuln.go.dev` | a local copy of `vulndb.zip` for `make compliance` offline |

`make doctor` also reports a `GOROOT` or `GOBIN` exported by another installer (mise, asdf, a shell profile); `env.mk` and `env.sh` clear both so the pinned Go is used. `make setup` always re-detects the module proxy from your own Go configuration, so a rerun repairs an earlier fallback. `make compliance` checks that the devcontainer, CI images, and workflows use the same Go, Node, Rust, and Pi versions as their single sources.

## dev

| Script | Purpose |
|---|---|
| `setup.sh` | Installs the Go toolchain pinned by `go.mod`, starts the module-proxy fallback when proxy.golang.org is unreachable, and builds `bin/pig`. `--check` reports without changing anything, including a leaked `GOROOT`/`GOBIN` and Go TLS failures. Behind `make setup` and `make doctor`. |
| `goproxy-github.py` | Serves the Go module proxy protocol from GitHub source archives and verifies every module against `go.sum`. Used only where proxy.golang.org is blocked. |
| `toolchains.lock` | Pinned toolchain archives and SHA-256 digests for `setup.sh`. |
| `login-copilot.sh` | Interactive helper for signing pig in to GitHub Copilot. |
| `parity-probe` | Launches upstream Pi or pig in tmux for side-by-side behavior checks. |
| `stage-clipboard-png.sh` | Puts a PNG on the clipboard for manual paste tests. |

## ci

| Script | Purpose |
|---|---|
| `test-grouped.sh` | The `make test` scheduler: runs Go packages in groups sized for the machine. |
| `test-fixtures.sh` | Builds the Go and Rust extension fixtures tests load. |
| `test-race.sh` | Race and goroutine-leak gate for the TUI concurrency model. |
| `integration-tests.sh` | The tmux integration tier, split by category. |
| `qc-smoke.sh` | Smoke checks against a freshly built pig. Behind `make qc`. |
| `parity-resource-limits.sh` | Chooses parity concurrency from CPU and memory. Probed only by targets that schedule parity. |
| `report-schedule.sh` | Prints the Go-test and parity scheduler buckets. |
| `check-divergence-consistency.sh` | Every ledger entry has a source marker and every marker has an entry. |
| `check-divergence-quality.py` | Divergence and additive records are current, enforceable contracts. |
| `divguard/` | Divergence guard (`make divergence-guard`): syntactic checks for invented limits and timeouts, dropped events, swallowed errors and success on unknown stop reasons, ratcheted by `divguard/baseline.toml`. |
| `check-divergence-delta.sh` | A release adds no divergence without its per-entry justification. |
| `check-coverage-delta.sh`, `check-coverage-drift.py` | Parity coverage did not regress, and `parity/coverage.md` and the badge match `PORT_MAP.md`. |
| `check-public-claims.py` | Public prose makes no claim the evidence contradicts: overclaim phrases, unrecorded governance claims, Pi versions other than the pin, and porting or verification figures that differ from the AGENTS.md coverage block. Runs in `make docs-drift`; pass delivery or blog drafts as extra arguments to audit them too. |
| `check-port-map-drift.py` | `PORT_MAP.md` accounts for every upstream source file. |
| `check-dco.sh` | Every commit in a pull request carries a DCO sign-off. |
| `check-npm-lock-integrity.py` | npm lockfiles pin integrity hashes. |
| `npm-locked.py` | Installs locked npm dependencies when manifest content changes; refuses replacement through a shared symlink. |
| `build-ci-image.sh`, `seed-ci-image-bases.sh` | Build and seed the CI container images in `images/`. |

## gen

| Script | Purpose |
|---|---|
| `mirror-upstream.sh` | Materializes the pinned Pi source under `.upstream/current` (read-only reference). |
| `generate-model-catalogs.sh` | Regenerates model catalogs from the pinned published Pi package. |
| `gen-help.sh` | Renders Pi's own `--help` with PiG's identity. |
| `gen-knowledge-graph.py` | Renders `docs/knowledge-graph/pig.graph.json` into its published forms. |
| `vendor-pi-dist.sh`, `vendor-typebox.sh` | Vendor the pinned Pi release's pure modules, the third-party packages they import, and TypeBox for the Node extension runtime. |

## release

| Script | Purpose |
|---|---|
| `gen-update-manifest.py` | Writes the self-update manifest for a release. |
| `validate-sbom.py` | Validates SBOM structure and prints a review inventory. |

## porter

| Script | Purpose |
|---|---|
| `run-pig-porter.sh` | Runs one bounded Pig Porter task. |
| `run-pig-porter-campaign.sh` | Runs read-only Porter workers across parity families. |
| `verify-pig-porter-local.sh` | Verifies the Porter Piglet in TUI and headless modes. |

## Evals and profiling

The eval harness is not here: it lives in `evals/` (`pigeval`, stdlib Python)
with its own README, because it is a measurement tool with tests, not glue.
`make evals`, `make evals-requests`, `make profile`, and `make pgo` drive it.
