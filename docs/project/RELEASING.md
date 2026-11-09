<!--
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

`internal/coding/pigversion/pigversion.go` is the version authority. The command updates its PiG pin, the Standard development version, the newest changelog heading, the PiG half of the composite version example in `README.md`, and the versions of the Python SDK (`extensions/sdk-py/pyproject.toml`) and the TypeScript declarations (`extensions/sdk-ts/package.json` and `package-lock.json`). It leaves the Rust `pig-sdk` crate on 0.1.x, because Pig resolves an extension's un-pathed `pig-sdk` requirement through a Cargo patch that applies only within semver. An ordinary version bump preserves every `go.mod` and `go.sum`. Dependency versions describe published modules, not the next PiG release. Workspace builds use the developing SDK through `go.work`.

```bash
make set-version VERSION=0.3.0 SET_VERSION_ARGS=--dry-run
make set-version VERSION=0.3.0
make set-version VERSION=0.3.0 SET_VERSION_ARGS=--move-unreleased
```

The default adds a dated release heading below the Unreleased entries without moving them. `--move-unreleased` moves those entries into the selected release. The date is today in UTC. Repeating the same command preserves the date and produces no diff. Lower versions and non-`x.y.z` inputs are rejected.

Use `SET_VERSION_ARGS=--release-modules` only to prepare an unpublished release on a protected release branch. This option also advances requirements on local modules in every tracked `go.mod` and computes nested-module entries in every existing `go.sum` from tracked files with working-tree contents. Stage new module files before running it. The hash implementation is `automation/release/modulehash`. Repeat this option after SDK edits only while that module version remains unpublished. Never replace a published version's checksums with hashes of developing source. Do not merge this candidate to `main` until its nested tags are published and `make module-publication` passes.

Use `SET_VERSION_ARGS=--rename-current` only to rename an unpublished candidate. It replaces the newest heading that matches the current pin instead of retaining that heading as a past release. It does not rewrite release prose or historical evidence. Combine options in one quoted value, for example `SET_VERSION_ARGS='--rename-current --dry-run'`. Never rename a published release.

Dry-run prints the planned diff without writing or running consistency tests. An apply runs `go test ./test/gomodule -count=1` and `go test ./internal/codingagent -run '^TestParseChangelog_RealFile$' -count=1`, then `make sdk-bundles`. A failed check returns a nonzero status, skips the bundles, and retains the changes for inspection. The command does not stage files, commit, tag, or publish.

`make set-version` re-encodes `CHANGELOG.md.zst` with zstd 1.5.5 and never looks on `PATH`. Run `automation/ci/install-zstd.sh` once. It prints the path of the zstd it built, and `make set-version` finds that build in the default prefix (`$PIG_CACHE_HOME`, else `$XDG_CACHE_HOME`, else `~/.cache`) although the recipe runs in an isolated home. Set `PIG_ZSTD` to the path of another zstd 1.5.5 to use it instead. Without either, `make set-version` stops with an error that names `PIG_ZSTD` and the provisioner, and writes nothing.

`make sdk-bundles` follows every apply because `extensions/sdk-py/sdk.zst` and `bundle_pack.go` embed `extensions/sdk-py/pyproject.toml`, which the version change rewrites. Commit the regenerated bundles with the version change. `make sdk-bundles-drift` fails the pull request otherwise. A dry run does not regenerate them.

### Prepare the release candidate

Run these three commands in this order on the release branch, then commit the result:

```bash
make set-version VERSION=x.y.z SET_VERSION_ARGS='--release-modules --move-unreleased'
make changelog-fold VERSION=x.y.z
make bug-ledger-refresh
```

1. `make set-version` comes first because `make changelog-fold` appends to the `## [x.y.z]` heading and stops when that heading does not exist. `--move-unreleased` moves the entries under `## [Unreleased]` (a Security bullet written directly in `CHANGELOG.md`, for example) into the release. Without it they stay under `[Unreleased]` and the release carries no record of them.
2. `make changelog-fold` comes second because it appends the bullets of the fragments to the sections of the release and deletes the fragments. It moves fragments only, so it stops, changing nothing, while `## [Unreleased]` still holds an entry; run step 1 with `--move-unreleased`.
3. `make bug-ledger-refresh` comes last because the ledger reads the `### Fixed` bullets that the fold wrote. Day to day `make bug-ledger` keeps every committed entry whose fix commits are recorded, so a pull request changes only its own entries; the refresh recomputes every entry from the history, which also settles the fixed-in release and the commit lists of the release's entries.

After these commands `make set-version VERSION=x.y.z SET_VERSION_ARGS='--release-modules --dry-run'` prints nothing. The release workflow's `source` job requires that empty diff.

Commit the release notes draft with the candidate (`make release-notes-draft NOTES_VERSION=x.y.z` writes `docs/release/x.y.z-release-notes-draft.md`, never over an existing file). The `publish` job creates the draft GitHub Release with that file as its body (`--notes-file`) and falls back to GitHub's generated notes (`--generate-notes`) only when the file is absent.

The release workflow refuses to start from a private repository (its first step, in the `source` job): attestation is skipped there, and `assemble-evidence.py` requires a Sigstore bundle for every archive. Each packaged smoke step also compares the `pig:` line of `pig version` with the release version (`pig --version` prints Pi's version by design); the pig-acp smoke compares pig-acp's own version with `automation/release/pig-acp.lock`.

A commit that moves or renames a test a fix commit added to another file keeps that fix's guard in the ledger: `automation/ci/bugledger/moved-tests.txt` maps a moved or renamed test (`old-file::OldName new-file::NewName`, one line per test) and both `make bug-ledger` and the refresh read it; the refresh also follows a moved test that kept its name to the one file that declares it. `make bug-ledger-check` fails on a line whose new test does not exist or whose old test still does. Add the line in the commit that renames the test.

## Changelog fragments and the bug ledger

Release notes are written as fragments in `changelog.d/<lane>-<sha>.md`; `make changelog-fold VERSION=x.y.z` appends them to the release and deletes them, and `make bug-ledger` builds `docs/bug-ledger.json` from the `### Fixed` bullets, the commit history and the divergence records. Both read the history of the release commit, so both need a full clone (`fetch-depth: 0` in CI). A fold that empties `changelog.d/` also removes the `changelog.d/` entries from `automation/ci/version-records.toml`: they excuse a fragment's statement about an earlier Pi release, the folded bullets sit in a released `CHANGELOG.md` section that the public-claims check does not read for such statements, and an entry that excuses nothing fails that check.

`make changelog-check` fails a release candidate when:

- a `### Fixed` bullet lacks a `<!-- fix: <sha> -->` marker. Without one the ledger guesses the fix commits from the history; when the only commit it finds is a squash, it credits the squash's tests to the bullet. A release candidate once grew `docs/bug-ledger.json` from about 2.1 MB to 9.8 MB this way, because five bullets were attributed to the 0.5.0 squash (about 11,000 tests), and the root module passed its size budget;
- a marker, or the `<sha>` in the fragment's name, names a commit that `HEAD` does not reach. A rebase, squash or amend replaces SHAs, so after one of them rename each fragment after the commit that replaced the one it names (`git mv changelog.d/<lane>-<old>.md changelog.d/<lane>-<new>.md`) and update its markers.

`make bug-ledger` refuses, with an error that names the entry, to attribute the tests of a commit that touches more than 500 files. The history it separates: the largest fix commit is 246 files, and every release or content squash above 500 is 799 files or more (the 0.5.0 squash is 13,725). If it names an entry, write or correct the marker of that entry's bullet so it names the commit that fixed the bug; if the commit fixes no product bug, give it a `Bug-Ledger: exempt <category>: <reason>` trailer.

`automation/ci/changelogfrag/legacy.txt` lists the fragments whose commits the 0.5.0 squash replaced; no commit reachable from `HEAD` can name them, so they are exempt from the name rule and from the reachability of their markers. Their `### Fixed` bullets still need a `<!-- fix: -->` marker, which may name a commit that no longer exists, and the fold refuses a bullet without one: the ledger refuses an unmarked bullet that the fold copied into `CHANGELOG.md`. The list only shrinks: the fold deletes a folded fragment and its line.

## Go module publication

PiG publishes two Go modules from one release commit:

- `github.com/MichaelKinsy/PiG`, tagged `vMAJOR.MINOR.PATCH`;
- `github.com/MichaelKinsy/PiG/extensions/sdk`, tagged
  `extensions/sdk/vMAJOR.MINOR.PATCH`.

`go install github.com/MichaelKinsy/PiG/cmd/pig@vMAJOR.MINOR.PATCH` refuses a module whose `go.mod` has a `replace` or `exclude` directive. The root `go.mod` therefore has neither. On `main`, keep nested requirements and checksums at published versions while the PiG version advances. When a change on `main` makes the root use nested-module API newer than the last tag, require the nested module at the pseudo-version of the `main` commit that holds that API (`GOWORK=off GOFLAGS=-mod=mod go get github.com/MichaelKinsy/PiG/extensions/sdk@<commit>`, which also writes the checksums), and require the same version in the modules that are fused with it (`piglets/standard`, `piglets/pig-bolt`), because a fused member cannot require a newer SDK than the Pig source pins. A pseudo-version sorts below the tags created on release branches, which are not ancestors of `main`; the release replaces it with the tag, as `make set-version --release-modules` does for every requirement. `go install ...@main` then builds, and `make module-size` and `make module-build-report` stay clean. A checkout resolves the SDK to `./extensions/sdk` through `go.work`; this does not prove that an external Go install can resolve the dependency.

`make module-publication` runs in the Linux build CI shard and in `make check`. It reads the root manifest with `GOWORK=off`, checks each required nested tag with `git ls-remote` against the public repository (a pseudo-version has no tag; its download proves the commit is public), and downloads only those modules into a fresh cache to verify the committed checksums. It rejects missing tags, unavailable downloads, incorrect checksums, and root replacement or exclusion directives. It does not modify the checkout. It does not prove API compatibility with a developing SDK. When main starts using a new SDK capability, publish the required SDK before merging the consumer.

Go refuses to download a module zip above 500 MiB (524288000 bytes), and `go install` downloads the whole root module. `make module-size` runs in the drift CI shard and in `make check`. It lists the tracked root-module files with Go's module zip rules, adds up their uncompressed bytes, and fails with the largest directories and files when the total exceeds the byte budget in `automation/release/module-size-budget.txt`. It then builds the module zip, serves it through a `file://` GOPROXY under a unique version, runs `go install` of `cmd/pig` at that version outside any workspace, and runs the installed binary with `--version`. A missing `go:embed` target, a `replace` directive, or an oversized tree fails here and not after the tag is published. The budget only moves down: never raise it. Bring the zip under budget by moving bulky data that `go install` users never need into a data-only directory with its own `go.mod` (Go leaves a nested module out of the parent zip; the files stay in the repository and tests keep reading them by path), or by deleting it. `go:embed` cannot cross a module boundary, so embedded files stay in the root module, and a test that wants a data-only file reads it by path instead of embedding it. The data-only modules are `agent/testdata`, `ai/providers/bedrock/testdata`, `ai/testdata`, `automation/pin/frozen`, `bench/durable`, `bench/sessionhost`, `chord/delta/testdata`, `chord/next/host/testdata`, `chord/next/link/testdata`, `chord/next/surfaces/testdata`, `chord/nextnode/plugins/testdata`, `chord/nextnode/surfaces/client/pi`, `chord/output/testdata`, `cmd/gen-models/testdata`, `codemode/testdata`, `coding/cli/testdata`, `coding/extension/builtin/toolsearch/testdata`, `coding/extension/host/subprocess/runtime-node` (the source tree of `runtime-node.pack`, which stays in the root module because the host embeds it), `coding/testdata`, `docs`, `docs/media`, `docs/parity`, `docs/plan`, `durable/codemode/testdata`, `durable/contract`, `durable/core/history/testdata`, `durable/core/host`, `durable/core/internal/delta/testdata`, `durable/core/jv/testdata`, `durable/core/session/testdata`, `durable/core/turn/testdata`, `durable/images/testdata`, `durable/internal/search/testdata`, `durable/toolsearch/testdata`, `env/testdata`, `internal/codingagent/testdata`, `internal/codingagent/tools/testdata`, `internal/evals/testdata`, `internal/mermaid/testdata`, `internal/nodepath/testdata`, `internal/photonimage/testdata`, `internal/wordsegmenter/testdata`, `internal/yaml12/testdata`, `mcp/testdata`, `schema/formats/testdata`, `schema/internal/ecmaregex/testdata`, `schema/testdata`, `test/extension-conformance/testdata`, `test/parity/corpus`, `test/parity/dependency-sources`, `test/parity/interface-closure/gaps`, `test/parity/interface-closure/labels`, `test/parity/interface-closure/specs`, `test/parity/interface-extractor`, `test/parity/interfaces`, `test/parity/pin-ledgers`, `test/parity/scenarios/extensions-runtime/testdata`, `test/parity/scenarios/model-resolver-selector/testdata`, `test/parity/sdk-bulk`, `test/parity/sdk-vectors`, `test/parity/unit-evidence`, `test/parity/upstream-sync`, `third_party/jsonschema/testdata`, `tui/internal/hljs/testdata`, `tui/testdata`, and `tui/widthx/testdata`.

Prepare release dependency pins with `make set-version VERSION=x.y.z SET_VERSION_ARGS=--release-modules` on the release branch. The release workflow retains the same-commit rule: both tags name the final candidate commit. `automation/release/module-tags.sh VERSION` prints the full tag set (`vVERSION`, then `<dir>/vVERSION` for every nested PiG module the root requires). It rejects replacement or exclusion directives and nested requirements at another version. The `source` job validates this plan and requires an empty `set-version --release-modules --dry-run` diff, so stale SDK hashes fail before an immutable tag is created. After candidate validation and release approval, the `publish` job creates each nested tag as an annotated tag on `$GITHUB_SHA` before creating the draft root release. An existing tag must already name that commit. Run `make module-publication` after nested publication, before merging the candidate to `main` or publishing the draft root release.

Until `extensions/sdk/vX.Y.Z` exists, `make module-publication` fails with `unpublished nested-module tag(s)` and `make module-size` fails with `unknown revision extensions/sdk/vX.Y.Z`. That is expected on the release pull request. The `publish` job creates the nested tag first and the root tag second, and both gates pass once the module is served.

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
- `darwin/arm64`;
- `windows/amd64`; and
- `android/arm64`.

`android/arm64` is the Termux binary. It is built with cgo by the Android NDK (`automation/release/android-binary.sh`) and its ELF headers are checked (PIE, `/system/bin/linker64`, bionic `libc.so`); Termux never runs `linux/arm64`. No hosted runner executes it on Android, so a device run is its native verification: an Android 14 emulator with real Termux and `TERMUX_EXEC__SYSTEM_LINKER_EXEC__MODE=force`, and a physical Google Play phone.

Do not list a target as supported until its native or approved equivalent verification passes for the release candidate.

## pig-acp archives

`pig-acp` is the Agent Client Protocol adapter an ACP client (Zed, JetBrains, the ACP registry) starts. Its source lives in the Pigpen Package `@pi-in-go/pigpen-acp`, not in this repository, and has its own version (0.2.x). Every release also publishes one `pig-acp-<version>-<goos>-<goarch>.tar.gz` (`.zip` on Windows) for `linux`, `darwin` and `windows` on `amd64` and `arm64`; Termux has no ACP client and gets none. `<version>` is PiG's version.

Each archive is `pig-acp-<version>-<goos>-<goarch>/` holding `pig-acp`, `pig` (both `.exe` on Windows), the notices of the pig archive, and `PIG-ACP/` with pig-acp's license, its credits and the license of the pi-acp port it derives from. The `pig` is the one from the same release's `pig-<version>-<goos>-<goarch>` archive: the `acp` job takes it from that candidate after `sha256sum -c`, so no second pig build exists and the bytes equal the pig archive's.

`automation/release/pig-acp.lock` pins the Package source by version, tarball URL, exact size and npm sha512 integrity. `automation/release/pig-acp-source.py` refuses a tarball of any other size or digest, a package.json of another name or version, and any tar member that is not a regular file or directory inside `package/`. `automation/release/build-pig-acp.sh` builds pig-acp with `CGO_ENABLED=0 -trimpath` and the lock's version as `main.version`, so `pig-acp --version` reports the pinned Package version, not PiG's. Change the pin by editing the lock: fetch the new tarball, record its `size` and `integrity` (`npm view <package>@<version> dist.integrity`), and run `python3 -m unittest discover -s automation/release -p 'test_pig_acp_*.py'`.

The `acp` job gives each archive the evidence a pig archive has: SPDX and CycloneDX SBOMs, inventory validation, a grype scan that fails the build at high severity, per-archive checksums, a build-provenance attestation and a `.sigstore.json` bundle. `acp-native-smoke` extracts the archive on linux/amd64, darwin/arm64 and windows/amd64 and runs `automation/release/pig-acp-smoke.py`, which starts the archive's `pig-acp` from a directory outside the archive, speaks ACP over stdio, and requires `initialize` and a `session/new` answered through the bundled `pig --mode rpc` (a session id, or, without credentials, pig-acp's terminal-login authentication error). `publish` waits for both jobs. `assemble-evidence.py` keeps each archive, bundle and evidence file; `combine-checksums.py` lists the archives in the one release `SHA256SUMS`, which the ACP registry entry's `sha256` values come from.

pig-acp 0.2.1 resolves a relative `--pig` against the session's working directory (Go's `exec.Command` with `Dir`), and without `--pig` it looks `pig` up on `PATH`, not beside itself. The archives still work when a client passes an absolute `--pig`, which is what the smoke test does. A registry entry that starts `./pig-acp` needs a pig-acp that defaults to the `pig` beside its own executable; that change belongs to Pigpen, and the lock moves to the release that carries it.

## Required artifacts

A release includes:

- one archive for each supported target;
- one `pig-acp` archive for each hosted ACP-client target (see "pig-acp archives");
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
3. writes `update.json`, the self-update manifest naming each macOS, Linux and Android archive with its SHA-256 from `SHA256SUMS` (`automation/release/gen-update-manifest.py --sha256sums`), signs it with each Ed25519 key in the `release` environment secret `PIG_UPDATE_SIGNING_KEY` (`automation/release/sign-update-manifest.sh`), checks every signature against the keys in `automation/release/update-trust.pem`, and attaches both `update.json` and `update.json.sig` (the signatures as comma-separated base64). The job fails if the secret is missing or holds a key whose public key is not in `update-trust.pem`;
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
version (`0.3.0`); npm semver cannot carry the `+0.99.1` build metadata usefully,
so the Pi base version appears in each package's description and README
instead. The layout follows the esbuild/biome pattern and runs no install
script and no download at install time:

- `@pi-in-go/pig`, a small Node.js (>= 18) launcher with `bin: pig`. It maps
  `process.platform`/`process.arch` to a platform package, then runs that
  package's native binary with inherited stdio, forwarding arguments, the exit
  status and signals. Node on Termux reports `process.platform` `android`, which
  the launcher maps to the `android-arm64` package. When the platform package is missing (for example after
  `--omit=optional`), it prints the other install methods.
- `@pi-in-go/pig-{darwin,linux,win32}-{x64,arm64}` and `@pi-in-go/pig-android-arm64`, one per release target,
  each with `os`/`cpu` fields, the single `pig`/`pig.exe` binary, and
  `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md`. The launcher lists all seven
  as exact-version `optionalDependencies`, so npm installs only the matching
  one.

`automation/release/npm/pack_npm.py` builds all eight packages from the release
archives only: it verifies each archive against `SHA256SUMS` before it reads
the binary and notices out of it, writes the `package.json` files, runs
`npm pack`, and writes `publish-order.txt` (seven platform packages, then the
launcher).

### One-time owner setup (trusted publishing, no token)

npm publishes through trusted publishing (OIDC): the workflow's GitHub OIDC
token is exchanged for a short-lived publish credential, and npm attaches
provenance automatically. No npm token or GitHub secret is stored.

1. Create the npm organization `pi-in-go` (free plan; public packages).
2. Bootstrap the eight packages once, because npm can require a package to
   exist before a trusted publisher can be configured for it. After the
   GitHub Release `v0.2.0` is published, on a maintainer machine with `gh`,
   `npm` and Python 3:

   ```bash
   npm login                                          # normal 2FA
   automation/release/npm/bootstrap-publish.sh v0.2.0 # npm asks for your OTP
   ```

   The script downloads the published release archives with `gh`, verifies
   them against `SHA256SUMS`, generates the packages with the same
   `pack_npm.py` CI uses, runs `npm publish --access public` for the seven
   platform packages and then the launcher (skipping any version already on
   npm), and prints the trusted-publisher settings. `DRY_RUN=1` packs without
   publishing.
3. On each of the eight packages (`https://www.npmjs.com/package/<name>/access`),
   add a Trusted Publisher: GitHub Actions, organization or user
   `MichaelKinsy`, repository `PiG`, workflow filename `npm-publish.yml`,
   environment empty. Optionally set publishing access to require 2FA and
   disallow tokens. A package that first ships in a later release, such as
   `@pi-in-go/pig-android-arm64`, needs the same bootstrap and trusted-publisher
   setup before `npm-publish.yml` can publish it.

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
