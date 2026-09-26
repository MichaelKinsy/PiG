<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Releasing PiG

Only a maintainer with release authority can start a PiG release. Complete every applicable legal, security, provenance, trademark, and publication approval before making a repository, tag, package, image, or binary public.

## Release prerequisites

1. Select the release commit from the protected default branch.
2. Confirm that the source tree is clean.
3. Confirm that `reuse lint` passes.
4. Confirm that required checks pass on the release commit.
5. Confirm each supported operating system and architecture through its approved native verification path.
6. Build release artifacts only in the protected release workflow.
7. Generate an inventory and artifact-specific SPDX SBOMs.
8. Validate the inventory and SBOMs against manifests, locks, embedded assets, and archive contents.
9. Review vulnerability, license, provenance, secret, and static-analysis findings.
10. Fix each applicable finding or record an approved disposition.
11. Complete the required external review and publication approvals.

See [`docs/supply-chain.md`](../supply-chain.md) for the evidence contract.

## Version and tag

PiG uses semantic versions and annotated tags in the form `vMAJOR.MINOR.PATCH`. Do not reuse a version or replace artifacts attached to a released tag.

The release metadata identifies both the PiG version and the Pi version that defines the parity target.

## Go module publication

PiG publishes two Go modules from one release commit:

- `github.com/MichaelKinsy/PiG`, tagged `vMAJOR.MINOR.PATCH`;
- `github.com/MichaelKinsy/PiG/extensions/sdk`, tagged
  `extensions/sdk/vMAJOR.MINOR.PATCH`.

`go install github.com/MichaelKinsy/PiG/cmd/pig@vMAJOR.MINOR.PATCH` refuses a
module whose `go.mod` has a `replace` or `exclude` directive. The root
`go.mod` therefore has neither: it requires the SDK at the release version
(`github.com/MichaelKinsy/PiG/extensions/sdk vMAJOR.MINOR.PATCH`), and a
checkout resolves that requirement to `./extensions/sdk` through `go.work`.
The root `go.sum` pins the SDK zip hash the checksum database will record for
that tag; `tests/gomodule` recomputes it from the tracked SDK files and prints
the replacement lines whenever the SDK changes. The first change to the SDK
after a release's tag requires the SDK at the next patch version instead, with
that version's `go.sum` lines, so the hash recorded for the published tag never
changes; `go.sum` keeps the published version's lines as they were. The next
release commit moves `PigVersion` and the CHANGELOG to that version. When the
PiG version changes otherwise, bump the SDK requirement and its `go.sum` lines
with it.

Both tags name the same commit. `automation/release/module-tags.sh VERSION`
prints the full tag set (`vVERSION`, then `<dir>/vVERSION` for every nested
PiG module the root requires) and fails closed on a `replace` or `exclude`
directive or a nested requirement at any other version. The release workflow
runs it twice: the `source` job refuses a release commit that fails it, and
the `publish` job creates each nested module tag as an annotated tag on
`$GITHUB_SHA` before it creates the draft release whose publication creates
`vVERSION` on that same commit. Tag the nested modules by hand only with the
same rule: `git tag -a extensions/sdk/vVERSION <release commit>`.

After the draft release is published:

1. Prove an external install with empty module and build caches:

   ```bash
   GOBIN="$(mktemp -d)" GOMODCACHE="$(mktemp -d)" GOFLAGS= GOWORK=off \
     go install github.com/MichaelKinsy/PiG/cmd/pig@vMAJOR.MINOR.PATCH
   ```

2. Inspect the installed identity with `go version -m` and `pig version`.
3. Confirm both module versions on `proxy.golang.org` (for example
   `go list -m github.com/MichaelKinsy/PiG@vMAJOR.MINOR.PATCH` and
   `go list -m github.com/MichaelKinsy/PiG/extensions/sdk@vMAJOR.MINOR.PATCH`)
   and request their pkg.go.dev pages.

Never move or delete a published module tag: the Go checksum database records
each module version's hash permanently.

A Go-installed executable has no standalone-download receipt. Update it with
`go install` again. Do not represent it as a standalone self-update installation.

## Build targets

The planned targets are:

- `linux/amd64`;
- `linux/arm64`;
- `darwin/amd64`;
- `darwin/arm64`; and
- `windows/amd64`.

Do not list a target as supported until its native or approved equivalent verification passes for the release candidate.

## Required artifacts

A release includes:

- one archive for each supported target;
- `SHA256SUMS`;
- `LICENSE`, `LICENSES/`, `NOTICE`, and `THIRD_PARTY_NOTICES.md`;
- an SPDX 2.3 JSON SBOM for each artifact;
- a CycloneDX JSON SBOM for each artifact when the receiving process requires it;
- reviewed license and vulnerability reports;
- release notes;
- provenance attestations; and
- approved signatures.

Each evidence file records the source commit, workflow identity, tool version, and vulnerability database identity where applicable.

## Publication controls

The release workflow creates immutable artifacts and checksums. Untrusted pull-request code must not receive publication credentials. A publication job must use an approved protected environment.

GitHub Releases owns the public release identity. Installers and self-update metadata use only approved, signed release artifacts. A downstream product can mirror a verified artifact. It must not rebuild or resign a PiG release under the same identity.

Do not publish a release, tag, installer, package, image, or binary until every release gate is complete.

## Publishing the candidate

`.github/workflows/release-candidate.yml`'s `publish` job runs after every `binary` matrix target, its `native-smoke` test, and `source` finish. It is the only job in the workflow with a write permission (`permissions: contents: write`), scoped to that job alone, and the only job gated by the `release` GitHub environment. Configure that environment with required reviewers before the first release.

The job:

1. downloads every platform's uploaded archive (skipping the source candidate, which is not a platform archive and which GitHub Releases attaches automatically);
2. combines their digests into one `SHA256SUMS` with `automation/release/combine-checksums.py` and cross-checks it with `sha256sum -c`, so `install.sh` and pi-in-go.dev's installer API (`/api/installer/releases`) see one combined manifest instead of the five separate per-platform ones each matrix job writes as its own evidence;
3. creates each nested Go module tag (`extensions/sdk/v<version>`) as an annotated tag on the release commit `$GITHUB_SHA`, after `automation/release/module-tags.sh` confirms the root `go.mod` is installable with `go install` (see "Go module publication"); a nested tag that already exists must name that same commit. A nested module tag alone installs nothing: `go install .../cmd/pig@v<version>` resolves only once the root tag exists;
4. creates a **draft** GitHub Release on tag `v<version>` (the tag pattern from "Version and tag" above) with every archive and the combined `SHA256SUMS` attached. A draft never becomes visible, and its tag is never created, until a maintainer reviews the evidence and presses Publish; this is the explicit approval "Publication controls" requires.

This repository holds no hosting credentials. The optional mirror of a published release to `dl.pi-in-go.dev` runs from the private hosting repository after publication; `install.sh` downloads from GitHub Releases unless `PIG_DOWNLOAD_BASE` names the mirror.

Publishing the draft release itself (pressing the GitHub UI's Publish
button, or `gh release edit v<version> --draft=false`) remains a manual,
off-workflow action by a maintainer with release authority.

## npm distribution

PiG also ships on npm as `@pi-in-go/pig`. The npm version is the PiG release
version (`0.2.0`); npm semver cannot carry the `+0.87.1` build metadata usefully,
so the Pi base version appears in each package's description and README
instead. The layout follows the esbuild/biome pattern and runs no install
script and no download at install time:

- `@pi-in-go/pig`, a small Node.js (>= 18) launcher with `bin: pig`. It maps
  `process.platform`/`process.arch` to a platform package, then runs that
  package's native binary with inherited stdio, forwarding arguments, the exit
  status and signals. When the platform package is missing (for example after
  `--omit=optional`), it prints the other install methods.
- `@pi-in-go/pig-{darwin,linux,win32}-{x64,arm64}`, one per release target,
  each with `os`/`cpu` fields, the single `pig`/`pig.exe` binary, and
  `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md`. The launcher lists all six
  as exact-version `optionalDependencies`, so npm installs only the matching
  one.

`automation/release/npm/pack_npm.py` builds all seven packages from the release
archives only: it verifies each archive against `SHA256SUMS` before it reads
the binary and notices out of it, writes the `package.json` files, runs
`npm pack`, and writes `publish-order.txt` (six platform packages, then the
launcher).

### One-time owner setup (trusted publishing, no token)

npm publishes through trusted publishing (OIDC): the workflow's GitHub OIDC
token is exchanged for a short-lived publish credential, and npm attaches
provenance automatically. No npm token or GitHub secret is stored.

1. Create the npm organization `pi-in-go` (free plan; public packages).
2. Bootstrap the seven packages once, because npm can require a package to
   exist before a trusted publisher can be configured for it. After the
   GitHub Release `v0.2.0` is published, on a maintainer machine with `gh`,
   `npm` and Python 3:

   ```bash
   npm login                                          # normal 2FA
   automation/release/npm/bootstrap-publish.sh v0.2.0 # npm asks for your OTP
   ```

   The script downloads the published release archives with `gh`, verifies
   them against `SHA256SUMS`, generates the packages with the same
   `pack_npm.py` CI uses, runs `npm publish --access public` for the six
   platform packages and then the launcher (skipping any version already on
   npm), and prints the trusted-publisher settings. `DRY_RUN=1` packs without
   publishing.
3. On each of the seven packages (`https://www.npmjs.com/package/<name>/access`),
   add a Trusted Publisher: GitHub Actions, organization or user
   `MichaelKinsy`, repository `PiG`, workflow filename `npm-publish.yml`,
   environment empty. Optionally set publishing access to require 2FA and
   disallow tokens.

### How the npm job runs

`.github/workflows/npm-publish.yml` runs on the GitHub Release `published`
event, so npm receives nothing until a maintainer publishes the draft release
that `release-candidate.yml` created. `workflow_dispatch` with a `tag` input
re-runs it for an already published release. The single job has
`permissions: contents: read, id-token: write` and:

1. resolves the tag, refuses a draft, and uses the npm dist-tag `next` for a
   GitHub prerelease and `latest` otherwise;
2. downloads `SHA256SUMS` and the six archives from the published release and
   checks them with `sha256sum -c`;
3. runs `pack_npm.py`, which verifies each archive again before extracting;
4. upgrades to npm `^11.5.1` (the minimum for trusted publishing) and runs
   `npm publish <tarball> --access public` for the six platform packages first
   and the launcher last. A version already on npm (`npm view`) is skipped, so
   a partial failure (or the bootstrap release) is finished by re-running the
   workflow.

npm versions are immutable: a published version cannot be replaced, only
deprecated. Fix a bad npm release with a new PiG release.

Local checks, without registry access:

```bash
python3 -m unittest automation/release/npm/test_pack_npm.py
node --test automation/release/npm/launcher.test.js
automation/release/npm/e2e-local.sh   # cross-builds six targets, packs, installs, runs pig --version
```
