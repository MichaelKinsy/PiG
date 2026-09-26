<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Compliance evidence

This page maps every README badge, every OpenSSF Scorecard check, and the OpenSSF Best Practices "passing" criteria to the file, workflow, or command that satisfies it.

Run `make compliance` to check the locally verifiable rows. It fails on any miss: a missing required file, an invalid `CITATION.cff`, a workflow action not pinned to a full commit SHA, a workflow whose top-level token is not read-only, a stale copy of the Go, Node, Rust, or Pi pin, a `reuse lint` failure, or a `govulncheck` finding. The Security workflow runs the same target.

Status values:

- **Verifiable now**: a local command or a file in this repository proves it today.
- **Activates when public**: the evidence exists, but the badge or check reads it from the public repository or a public service.
- **Owner action**: a repository setting, registration, or release step that only the owner can perform.

## Badges

| Badge | Evidence | Status |
|---|---|---|
| CI | `.github/workflows/ci.yml`: DCO, Linux `make check`, Windows native | Activates when public |
| CodeQL | `.github/workflows/security.yml` job `codeql` (Go) | Activates when public |
| OpenSSF Scorecard | `.github/workflows/scorecard.yml` with `publish_results: true`; the checks are listed below | Activates when public |
| OpenSSF Best Practices | Criteria mapped below; the badge is an HTML comment in `README.md` until the project id exists | Owner action: register at bestpractices.dev |
| REUSE | `REUSE.toml`, `LICENSES/`, SPDX headers; `make compliance` runs `reuse --no-multiprocessing lint` | Verifiable now; badge after registering at api.reuse.software |
| Go Reference | Package doc comments; `go.mod` module `github.com/MichaelKinsy/PiG` | Activates when public; owner requests the pkg.go.dev fetch |
| Go version | `go.mod` `go` directive (language floor) | Activates when public |
| License | `LICENSE` (MIT); `CITATION.cff` `license: MIT` | Verifiable now |
| Latest release | GitHub Releases | Owner action: publish the first release, tagged `v` plus `coding.PigVersion` |
| Pi pin | Static badge naming the pinned Pi release, not a parity result; `make compliance` fails when it differs from `coding.UpstreamVersion` | Verifiable now |
| Parity coverage | `.github/badges/parity-coverage.svg` from `make coverage`; the badge is the share of ported files with a behavioral parity scenario, not a pass rate; `make coverage-drift` rejects a stale badge | Verifiable now |

## OpenSSF Scorecard checks

| Check | Evidence | Status |
|---|---|---|
| Branch-Protection | Branch ruleset on `main`: pull request, one approving review, required status checks | Owner action |
| Code-Review | Same ruleset; `.github/CODEOWNERS`; `.github/pull_request_template.md` | Owner action; the score grows with reviewed merges |
| Signed-Releases | `release-candidate.yml` writes `<archive>.sigstore.json` beside each archive and runs `actions/attest-build-provenance` | Owner action: attach archives, `SHA256SUMS`, and `.sigstore.json` files to the first GitHub Release |
| Pinned-Dependencies | Every action pinned to a commit SHA (`make compliance` check `actions`); image bases pinned by digest; `go.sum`; npm lock files | Verifiable now |
| Token-Permissions | Every workflow's top-level token is read-only (`make compliance` check `permissions`). Write scopes are job-level only: `ci-images.yml` `packages: write` for the gated publish step, and the job-level scopes CodeQL, Scorecard, and attestation require | Verifiable now |
| SAST | CodeQL (`security.yml`), Staticcheck, `go vet`, golangci-lint (`make lint`), Ruff, Bandit, ShellCheck | Activates when public; Scorecard reads the pull-request check history |
| Vulnerabilities | `go tool govulncheck ./...` (`make compliance`); Grype over the source SBOM in `security.yml`; `npm audit` of every lock file | Verifiable now |
| Dependency-Update-Tool | `.github/dependabot.yml`: gomod, npm, pip, cargo, github-actions | Verifiable now |
| Security-Policy | `SECURITY.md`; `.github/security-insights.yml` | Verifiable now |
| License | `LICENSE`; `LICENSES/` | Verifiable now |
| Maintained | Commit and issue activity within 90 days | Activates when public; Scorecard scores a repository younger than 90 days as not maintained |
| Fuzzing | Native Go fuzz targets: `tui/render_state_test.go` `FuzzTUIRender_StateMachineInvariants`, `internal/mermaid/robust_test.go` `FuzzRender` | Activates when public |
| Packaging | `release-candidate.yml` builds, attests, and uploads release archives | Activates at the first published release |
| CII-Best-Practices | OpenSSF Best Practices badge | Owner action: register, then complete the criteria below |

## OpenSSF Best Practices "passing" criteria

| Criterion area | Evidence | Status |
|---|---|---|
| Project website and description | `README.md`; https://pi-in-go.dev, rendered from `docs/site/docs/` | Activates when public |
| How to contribute | `CONTRIBUTING.md`; `CODE_OF_CONDUCT.md`; `GOVERNANCE.md`; `MAINTAINERS.md` | Verifiable now |
| FLOSS license | `LICENSE` (MIT, OSI-approved); REUSE-compliant headers | Verifiable now |
| Basic documentation | `README.md`; `docs/site/docs/`; the embedded agent docs under `internal/pigdocs/content/` | Verifiable now |
| Public version-controlled source | GitHub repository with history | Activates when public |
| Unique version numbering | `coding.PigVersion`; tags `vX.Y.Z` (`docs/project/RELEASING.md`) | Owner action: tag the first release |
| Release notes | `CHANGELOG.md` | Verifiable now |
| Bug-reporting process | `.github/ISSUE_TEMPLATE/`; `/bug` writes a report archive and links to the bug form (D62) | Activates when public |
| Vulnerability reporting process | `SECURITY.md` (private vulnerability reporting) | Owner action: enable private vulnerability reporting |
| Working build system | `make build`; `make setup` | Verifiable now |
| Automated test suite | `make test`, `make check`; CI runs them | Verifiable now |
| Tests for new functionality | `CONTRIBUTING.md` requires tests derived from upstream behavior; `AGENTS.md` requires a regression guard for every bug fix; `.github/pull_request_template.md` | Verifiable now |
| Compiler warnings and linters | `go vet`, golangci-lint (`make lint`), Staticcheck | Verifiable now |
| Secure development knowledge | `SECURITY.md` threat model; `AGENTS.md` security-suppression rules | Verifiable now |
| Good cryptographic practice | Go standard library crypto; no custom cryptography; checksum-verified downloads (`internal/toolchain`, `automation/dev/setup.sh`) | Verifiable now |
| Delivery secured against MITM | HTTPS-only downloads with SHA-256 verification; Sigstore bundles and build provenance for releases | Verifiable now for toolchain downloads; release bundles activate with the first release |
| Publicly known vulnerabilities fixed | `govulncheck` and Grype in CI; Dependabot | Verifiable now |
| Static analysis | CodeQL, Staticcheck, golangci-lint, Bandit, Ruff | Verifiable now |
| Dynamic analysis | Race detector (`make test-race`), fuzz targets, parity scenarios against the real Pi binary | Verifiable now |

## Supply chain and provenance

| Item | Evidence | Status |
|---|---|---|
| SLSA build provenance | `actions/attest-build-provenance` in `release-candidate.yml` gives SLSA Build Level 2. PiG does not claim Level 3: the build does not run on an isolated, non-falsifiable builder. | Activates at release |
| SBOMs | SPDX and CycloneDX SBOMs from `anchore/sbom-action`, validated by `automation/release/validate-sbom.py`; source SBOMs in `security.yml` | Activates at release (release SBOMs); verifiable in CI (source SBOMs) |
| govulncheck | `make compliance` | Verifiable now |
| Secret scanning | Gitleaks and TruffleHog (`security.yml`); GitHub secret scanning with push protection | Verifiable in CI; owner action to enable GitHub secret scanning and push protection |
| DCO | `automation/ci/check-dco.sh` in `ci.yml`; `CONTRIBUTING.md` requires `Signed-off-by` | Verifiable now |
| CITATION.cff | `CITATION.cff`; `make compliance` checks CFF 1.2.0 fields, the repository URL from `go.mod`, and the license | Verifiable now |

## One source for each version pin

| Pin | Source | Copies that `make compliance` checks |
|---|---|---|
| Go toolchain | `go.mod` `toolchain` | Workflows read `go.mod` (`go-version-file`); devcontainer; CI image Dockerfiles and tags |
| Node | `.node-version` | Workflows read it (`node-version-file`); devcontainer; CI parity image tag |
| Rust | `ci.yml` `RUST_VERSION` | Devcontainer; CI parity image tag |
| Pi | `coding/pigversion/pigversion.go` `UpstreamVersion` (re-exported as `coding.UpstreamVersion`) | `extensions/sdk-ts` dependencies; CI parity oracle image; `parity/known-gaps.toml`; `parity/behavior-contracts.toml`; README badge. `ci.yml` reads the source directly. |
| Reviewed Pi baseline | `coding/upstream.go` `UpstreamReviewedVersion` | The Makefile derives the leap ledgers from it |
