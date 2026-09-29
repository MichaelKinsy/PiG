<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Releasing PiG

Only a maintainer with release authority can start a PiG release. Complete every applicable legal, security, provenance, trademark, and publication approval before making a repository, tag, package, image, or binary public.

## Release prerequisites

1. Select the release commit from a protected branch. Keep unpublished nested-module requirements off `main`.
2. Confirm that the source tree is clean.
3. Confirm that `reuse lint` passes.
4. Confirm that required checks pass on the release commit. A candidate with unpublished nested modules must pass the publication gate after the approved nested tags are published and before it reaches `main` or receives the root release tag.
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

Change the version only with `make set-version VERSION=x.y.z`.

`internal/coding/pigversion/pigversion.go` is the version authority. The command updates its PiG pin, the Standard development version, and the newest changelog heading. An ordinary version bump preserves every `go.mod` and `go.sum`. Dependency versions describe published modules, not the next PiG release. Workspace builds use the developing SDK through `go.work`.

```bash
make set-version VERSION=0.3.0 SET_VERSION_ARGS=--dry-run
make set-version VERSION=0.3.0
make set-version VERSION=0.3.0 SET_VERSION_ARGS=--move-unreleased
```

The default adds a dated release heading below the Unreleased entries without moving them. `--move-unreleased` moves those entries into the selected release. The date is today in UTC. Repeating the same command preserves the date and produces no diff. Lower versions and non-`x.y.z` inputs are rejected.

Use `SET_VERSION_ARGS=--release-modules` only to prepare an unpublished release on a protected release branch. This option also advances requirements on local modules in every tracked `go.mod` and computes nested-module entries in every existing `go.sum` from tracked files with working-tree contents. Stage new module files before running it. The hash implementation is `automation/release/modulehash`. Repeat this option after SDK edits only while that module version remains unpublished. Never replace a published version's checksums with hashes of developing source. Do not merge this candidate to `main` until its nested tags are published and `make module-publication` passes.

Use `SET_VERSION_ARGS=--rename-current` only to rename an unpublished candidate. It replaces the newest heading that matches the current pin instead of retaining that heading as a past release. It does not rewrite release prose or historical evidence. Combine options in one quoted value, for example `SET_VERSION_ARGS='--rename-current --dry-run'`. Never rename a published release.

Dry-run prints the planned diff without writing or running consistency tests. An apply runs `go test ./test/gomodule -count=1` and `go test ./internal/codingagent -run '^TestParseChangelog_RealFile$' -count=1`. A failed check returns a nonzero status and retains the changes for inspection. The command does not stage files, commit, tag, or publish.

## Go module publication

PiG publishes two Go modules from one release commit:

- `github.com/MichaelKinsy/PiG`, tagged `vMAJOR.MINOR.PATCH`;
- `github.com/MichaelKinsy/PiG/extensions/sdk`, tagged
  `extensions/sdk/vMAJOR.MINOR.PATCH`.

`go install github.com/MichaelKinsy/PiG/cmd/pig@vMAJOR.MINOR.PATCH` refuses a module whose `go.mod` has a `replace` or `exclude` directive. The root `go.mod` therefore has neither. On `main`, keep nested requirements and checksums at published versions while the PiG version advances. A checkout resolves the SDK to `./extensions/sdk` through `go.work`; this does not prove that an external Go install can resolve the dependency.

`make module-publication` runs in the Linux build CI shard and in `make check`. It reads the root manifest with `GOWORK=off`, checks each required nested tag with `git ls-remote` against the public repository, and downloads only those modules into a fresh cache to verify the committed checksums. It rejects missing tags, unavailable downloads, incorrect checksums, and root replacement or exclusion directives. It does not modify the checkout. It does not prove API compatibility with a developing SDK. When main starts using a new SDK capability, publish the required SDK before merging the consumer.

Prepare release dependency pins with `make set-version VERSION=x.y.z SET_VERSION_ARGS=--release-modules` on the release branch. The release workflow retains the same-commit rule: both tags name the final candidate commit. `automation/release/module-tags.sh VERSION` prints the full tag set (`vVERSION`, then `<dir>/vVERSION` for every nested PiG module the root requires). It rejects replacement or exclusion directives and nested requirements at another version. The `source` job validates this plan and requires an empty `set-version --release-modules --dry-run` diff, so stale SDK hashes fail before an immutable tag is created. After candidate validation and release approval, the `publish` job creates each nested tag as an annotated tag on `$GITHUB_SHA` before creating the draft root release. An existing tag must already name that commit. Run `make module-publication` after nested publication, before merging the candidate to `main` or publishing the draft root release.

For `0.3.0`, publish `extensions/sdk/v0.3.0` first on the final release commit, then publish `v0.3.0` on that same commit. Do not downgrade the current dependency to `v0.2.0`: that published SDK lacks `extensions/sdk/json`, which the current host imports. After release, ordinary version bumps retain this published SDK pin.

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
3. writes `update.json`, the self-update manifest naming each macOS and Linux archive with its SHA-256 from `SHA256SUMS` (`automation/release/gen-update-manifest.py --sha256sums`), signs it with each Ed25519 key in the `release` environment secret `PIG_UPDATE_SIGNING_KEY` (`automation/release/sign-update-manifest.sh`), checks every signature against the keys in `automation/release/update-trust.pem`, and attaches both `update.json` and `update.json.sig` (the signatures as comma-separated base64). The job fails if the secret is missing or holds a key whose public key is not in `update-trust.pem`;
4. creates each nested Go module tag (`extensions/sdk/v<version>`) as an annotated tag on the release commit `$GITHUB_SHA`, after `automation/release/module-tags.sh` validates the release dependency pins; an existing nested tag must name that same commit. It then runs `automation/ci/check-module-publication.py` to verify the published tags and downloaded checksums before creating the draft root release. A nested module tag alone installs no PiG executable: `go install .../cmd/pig@v<version>` resolves only once the root tag exists;
5. creates a **draft** GitHub Release on tag `v<version>` (the tag pattern from "Version and tag" above) with every archive and the combined `SHA256SUMS` attached. A draft never becomes visible, and its tag is never created, until a maintainer reviews the evidence and presses Publish; this is the explicit approval "Publication controls" requires.

### Self-update

Release binaries are built with `-X internal/codingagent.DefaultUpdateURL=https://github.com/<repository>/releases/latest/download/update.json` and with `DefaultUpdateTrustRoot` set to `automation/release/update-trust.pem` (base64-encoded, because `-X` takes one line). `install.sh` records an owner-only install receipt naming that update source, so `pig update` on a script installation fetches the latest published `update.json`, verifies `update.json.sig` against the built-in key, downloads the archive for its platform, checks the archive's SHA-256, and replaces the executable. A standalone `pig.exe` on Windows is not replaced in place (D39); Windows users run the PowerShell installer again.

To set up the signing key: generate an Ed25519 key (`openssl genpkey -algorithm ed25519 -out update-signing.pem`), store its PEM as the `PIG_UPDATE_SIGNING_KEY` secret of the `release` environment, and commit its public key (`openssl pkey -in update-signing.pem -pubout`) to `automation/release/update-trust.pem`.

`pig update` accepts a manifest when any signature in `update.json.sig` verifies against any key the binary trusts, and each binary trusts the keys `update-trust.pem` held when it was built. To rotate the signing key without stranding installed binaries:

1. Generate the incoming key and append its public key to `update-trust.pem` after the outgoing one.
2. Set `PIG_UPDATE_SIGNING_KEY` to both private keys, outgoing then incoming, concatenated as one PEM secret. Every release from here on carries two signatures: binaries built before the rotation verify the outgoing key's signature, binaries built after it verify either one.
3. Keep signing with both keys for as long as binaries that trust only the outgoing key should keep updating in place. A binary that skips every dual-signed release still verifies the latest one, because the latest one still carries the outgoing signature.
4. To retire the outgoing key, remove its public key from `update-trust.pem` and its private key from the secret. Binaries built before the rotation then no longer verify the latest manifest and `pig update` refuses it; those installations update by running `install.sh` again. If the outgoing key was compromised, skip the dual-signing period and retire it at once: a binary that trusts it accepts any manifest it signs, so those installations should be reinstalled with `install.sh` rather than updated.

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
