# Pig Additive Features

This file documents Pig-only **additive** features that have no upstream Pi
equivalent. These are not behavioral divergences from upstream: upstream simply
doesn't have these capabilities. `docs/parity/DIVERGENCES.md` is reserved for actual
user-visible behavioral deltas where upstream and pig do the same thing
differently (e.g. rendering, command output, prompts).

Each record uses a global `D<N>` ID. Put one short
`pig additive (D<N>)` marker at each implementation point. Keep the full
rationale in this file.

If an upstream behavior changes such that one of these additive features starts
*conflicting* with upstream, promote it to `docs/parity/DIVERGENCES.md` with a numbered D
entry and a SCRUTINIZED tag.

## Repository release maintenance

`make set-version VERSION=x.y.z` is repository-only release tooling, not an additive Stock PiG runtime API. It synchronizes the PiG pin, Standard development version, and changelog heading while preserving Go dependency pins and checksums. The explicit `--release-modules` option prepares local module requirements and publication checksums on an unpublished release branch. The nested tags must be published before that candidate reaches `main`; `make module-publication` verifies required public tags and downloaded checksums without workspace or SDK-cache assistance. The command preserves Unreleased entries unless `--move-unreleased` is selected, supports a read-only `--dry-run`, and permits explicit renaming of an unpublished candidate with `--rename-current`. See `docs/project/RELEASING.md`. This required release substrate does not select product behavior and adds no numbered runtime divergence. Pi's corresponding repository automation is `scripts/sync-versions.js` and `scripts/release.mjs`; the Go module publication mechanism is Go-specific.

---

## D18 Piglet management and build dispatch

Stock disposition: required substrate. A Piglet cannot supply the parser, resolver, verifier, or builder needed to select and load itself.

What: Pig adds product-neutral Piglet source management, validation, execution,
and build commands. Upstream Pi has no Piglet composition or Piglet Binary
concept.

Current behavior: `pig piglet list|show|validate|schema|add|remove|pull|update|publish|build` operates on explicit Piglet source or a signed Piglet Binary release. `pig piglet add` accepts local paths, product-contributed catalog refs, npm source packages, and Git refs. Remote sources are materialized without changing Package settings, must pass portable Piglet validation, and write an adjacent origin record containing the exact npm version/integrity or Git commit. Git refs include local `git:file://localhost/<path>` URLs, which upstream parseGitUrl rejects. On Windows a file URL's drive becomes a plain segment of the checkout path (`git/localhost/C/...`), any other path segment containing `:` is refused with upstream's "Refusing to use path outside package install root", and git clones the equivalent `file:///C:/...` because Git for Windows reads the localhost form as a UNC path. JSON inventory includes that origin. A Piglet selects Resources by typed origin, controls ambient discovery, scopes capabilities, and may declare a required agent environment. `piglet build --format binary` resolves exact component closure and can fuse compatible Go factories while preserving the ordinary extension registration contract. A Piglet can set `build.extensionRealization: fused` to reject any selected extension that would otherwise use a subprocess realization. A built Piglet Binary verifies its embedded Piglet, resolution record, executable component plan, and optional Ed25519 DSSE signature before command dispatch. `pig piglet keygen`, `build --sign-key`, `pull`, `verify`, and `trust` provide offline signing, signer continuity, revocation, and a local required-signature policy. `pull` downloads a signed release index and one target asset from HTTPS or GitHub Releases, pins the first signer locally, and publishes no managed file until the index checksum and both signatures verify. `pig piglet publish <name|path> --to github` publishes one GitHub Release named `v<release.version>` with a signed Binary per target, `SHA256SUMS`, and a signed `piglet-release.json` index. It builds each target with a ready signing builder or collects prebuilt signed Binaries with `--artifacts`, lists every target it cannot produce and uploads nothing, refuses an existing release, and checks every staged asset as `pull` would. Publication dry-runs by default and runs `gh release create` only with `--yes`; Pig never handles a GitHub token. `pig verify --provenance` can separately delegate an explicit Sigstore keyless provenance check to the GitHub CLI; startup never makes that network request.
Stock Pig contains the generic command and runtime machinery but selects no
Piglet and activates no product Resources by default.

`publish --tag-prefix <name>/` selects an explicit per-Piglet Git tag namespace while preserving unprefixed tags by default. The prefix must match the manifest name. Repository-layout inference is deliberately absent: the same publication inputs keep the same release identity when sibling Piglets are added or a publisher uses prebuilt artifacts outside a checkout. Signed indexes carry `github.repository` and `github.tagPrefix`; receipts retain that envelope. `github:owner/repo/<piglet>@version` binds name, version, repository, and namespace. `update <name>` enumerates the signed repository's public GitHub releases API and selects the highest stable SemVer only in that namespace, never repository-wide latest. Explicit versions can select prereleases. Discovery is bounded to 100 pages of 100 releases and fails on an incomplete inventory. Pull and update reject rollback and repository/namespace changes and revalidate signer and release continuity under the managed-store lock.

`add git:<repository>@<full-commit>#subdirectory=<escaped-path>` reads only the selected subdirectory's Piglet. It requires a matching clean commit, copies declared relative local Resources and prompt files into `<name>.source/<commit>/`, and rewrites only the corresponding registered YAML paths. It preserves explicit empty scopes and executable permissions. The origin binds original and registered Piglet digests and each copied file digest. Closure paths cannot escape the Piglet file's directory, traverse symlinks, include Git metadata, or copy special files. The in-memory closure is bounded to 4,096 visited entries and 32 MiB. Source removal owns only its recorded closure. Bundled source requires explicit YAML fields rather than aliases or merge keys. Other remote source forms retain their refusal of local Resource origins. These are required, inert distribution mechanisms; no optional product is selected.

`pig piglet publish <name|path> --to npm` publishes Piglet source to npm, and `pig package publish [<dir>] --to npm` is optional sugar over `npm publish` for a Package. Plain `npm publish` of a Package with the `pig-package` keyword stays the supported Pi-style path, and Pi has no Piglet. Both commands run the author's own `npm` (or the global `npmCommand`), so npm does the authentication, including a one-time password, a passkey, and npm trusted publishing; PiG never reads or stores a token and adds `--provenance` when `ACTIONS_ID_TOKEN_REQUEST_URL` is set. Both are dry runs until `--yes`, and both check `npm view` and refuse an existing `name@version`. The Piglet command validates the Piglet, writes a temporary package from it (`piglet.yaml`, `package.json` with the keyword `pig-piglet` and `pig.piglet`, README, LICENSE, the system prompt file), replaces each `local:` Package by `npm:<name>@^<version>` read from that Package's own `package.json` (or `--package-map`), and refuses a Piglet whose `npm:` Packages are not on npm or that `piglet add` would refuse (`extends`, `agentEnv.devContainer`, file-based secrets, local Resource origins). It records a signed Binary release in `package.json` under `pig.binaries` (`ref`, `signer`): `--binaries github:<owner/repo>` reads and verifies the signed release index, and without it a release recorded by `publish --to github --yes` under `receipts/piglet-publications/` is used. The Package command validates the Package and the fields the pi-in-go.dev catalog reads, runs `npm publish` in the Package directory, and never edits `package.json`. `pig piglet add npm:<name>` accepts exactly what publication produces. These are inert distribution mechanisms; catalogs find packages by npm keyword and no product is selected.

Evidence: `TestPublishGitHubNamedNamespace` and `TestNamedGitHubReferenceEscapesTag` fail on unprefixed-only publishing/pull; `TestAddMonorepoPinnedClosure` fails on the prior local-origin refusal; `TestUpdateCommandRoutesBeforeSession` fails on the absent update command. `TestMonorepoPublishPullUpdateIsolation` publishes two names at the same versions through fake `gh`, serves their exact uploaded bytes through a local HTTPS GitHub transport, and checks named updates, mismatched identities, tampering, signer rotation, revocation, rollback, and cancellation. `TestPigletAddPinnedMonorepoThroughCoreMaterializer` uses a real local bare Git repository and removes the materialized checkout before verifying both registered closures. upstream 0.99.1 has no Piglet release counterpart: `packages/coding-agent/src/package-manager-cli.ts:375-385` recognizes only install/remove/uninstall/update/list as Package verbs, and `packages/coding-agent/src/cli/args.ts:253-254` puts other positional words into prompt messages. Piglet evidence is additive, not a paired Pi coverage claim. A compiling mutation that clears the discovery prefix makes `TestDiscoverGitHubVersionPagesNamespaceAndStableVersions` select unprefixed `7.0.0` instead of namespaced `2.0.0` and fails the publisher-to-update test. The restored code passes both. `TestUpdateUnprefixedRepositoryCannotInstallAnotherPiglet` reproduces an unprefixed update installing `beta` when `alpha` was requested; update now binds the installed name independently of the tag namespace before asset download. `TestPullCancellationAfterDownloadDoesNotPublish` also fails on a completed download that previously published after cancellation; pull now checks cancellation before verification and again under the publication lock. The cross-family `cli-utils/04-package-install-local` comparator is `output_equal` at three runs after probing Pi, then pinned at 0.87.1.

Release test hook: `PIG_PIGLET_GITHUB_URL` names one loopback origin that replaces `https://github.com` (release downloads for `github:` refs) and `https://api.github.com` (`update` discovery) with the same paths, so the real `pig piglet pull` and `pig piglet update` commands can run against a local release server. It reuses the `PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP` opt-in rather than adding a second gate: without that opt-in, or with a non-loopback host, a path, query, fragment or credentials, the command refuses. Unset, both commands use GitHub exactly as before. A boolean opt-in cannot carry a server address, so the base is the one new variable, named after `PIG_UPDATE_URL`. `TestPigletUpdateEndToEndThroughLoopbackGitHub` (cmd/pig) pulls v1.0.0, updates to v1.1.0 and checks the output, the current pointer and the installed bytes; a rerun requests only discovery and rewrites nothing; offline, rollback, wrong checksum, oversized asset, altered index, different signer, a non-loopback override and an override without the opt-in are each refused with the installed state byte-identical. A test transport refuses and records any request for another host. It fails on the prior code because `github:` refs and discovery could only reach GitHub. `TestGitHubURLOverrideAcceptsOnlyAnOptedInLoopbackOrigin` checks each refusal by its own message, including HTTPS overrides that no later URL check would refuse, and `TestGitHubURLOverrideRoutesIndexAndDiscovery` checks that both URLs take the override and that a refused override sends no request.

Resource disposition: npm publication runs only in explicit pre-session CLI commands. `npm` is a child process that inherits the command's context and is cancelled with it, the generated package directory is removed when the command ends, and a signed release index read for `--binaries` is bounded to 1 MiB with the response body closed. Release HTTP requests own response bodies, use bounded reads, and inherit caller cancellation; staged Binary files and failed publication directories are removed. Source closure collection closes every file and directory, reads directories in bounded batches, caps retained file bytes, and installs no source file until validation completes. These operations run in explicit pre-session CLI commands, not on the TUI loop. `BenchmarkPigletClosure` covers sixteen 64-KiB files. A Linux/amd64 Go 1.27.1 sample reports 1.78 ms/op, 2.24 MB/op, and 624 allocations/op; CPU and allocation profiles identify runtime GC/syscalls and `io.ReadAll` respectively. This is a resource baseline, not a speedup claim. Reproduce with `go test ./coding/piglet -run '^$' -bench '^BenchmarkPigletClosure$' -benchmem -cpuprofile /tmp/piglet-closure-cpu.pprof -memprofile /tmp/piglet-closure-mem.pprof -o /tmp/piglet-closure.test`.

Installed PiG Packages distribute Resources and never activate Piglets. An npm package used as a Piglet source is materialized directly and is not added to Package settings. Optional catalog and product transports must use public resolver contracts and must not be imported or registered by Stock Pig.

Build diagnostics are product-neutral substrate. Explicit `pig build` and `pig piglet build --format binary` invocations report real phases and elapsed time on stderr. A terminal receives Pi's default loader frames and dim step text; redirected output receives plain phase lines. `--verbose` streams toolchain diagnostics with member labels. Packed members share a compiler invocation and label; fused members compile with the final Go binary. The build does not invent separate fetch or link phases inside a single toolchain invocation. Failure reports name the current phase, retain a bounded diagnostic tail, and suggest remediation. Success reports the artifact path, byte size, and elapsed time only after verification and record publication. The progress observer is absent from ordinary extension startup and does not change cache identity, compiler inputs, or extension APIs.

The native builder compiles the PiG checkout that contains the working directory or `PIG_SOURCE_ROOT`. Without one, a release binary fetches exactly its own version, `github.com/MichaelKinsy/PiG@v<PigVersion>`, with `go mod download -json`. The running version is a release when its build info names that module version (`go install …@v<version>`) or when the release workflow stamped `coding/pigletbuild.releaseSourceVersion`, because release archives are built from source without VCS metadata. A development build keeps the `source-unavailable` result and its checkout remedy. `GOPROXY`/`GOSUMDB` verify the download and `GOMODCACHE` caches it; no Git clone or temporary checkout is made. The Go command refuses a build overlay beneath `GOMODCACHE`, so the module tree is staged once, by hard link or copy, under `~/.pig/cache/pig-source/<version>-<sum-key>` and published by rename. The build runs with `GOWORK=off` because the module zip omits the workspace's nested modules. Probe stays side-effect free: it reports ready with code `source-fetchable`, and Build performs the download and prints `fetching PiG <v> source (cached after first build)`. The Binary record's source revision is the proxy-reported commit, or the module version when the proxy reports none, and its source digest hashes the staged tree. Evidence: `TestNativeProbeReleaseWithoutCheckoutIsFetchableWithoutDownloading`, `TestNativeProbeDevBuildWithoutCheckoutIsSourceUnavailable`, `TestSelectBuilderAutoSelectsFetchableNative`, `TestResolveSourceFetchesExactlyTheRunningRelease`, `TestResolveSourceDevBuildKeepsSourceUnavailableRemedy`, `TestResolveSourceReportsDownloadFailureWithRemedy`, `TestResolveSourceRejectsAnotherModule`, and the network-gated `TestGoModDownloadFetchesPublishedPigSource`.

A checkout reached through a symbolic link, such as one under `/tmp` or `/var` on macOS, builds as reached: every go command the builders start with an explicit environment (`pig build`, the Piglet Binary compile, the release source download, and Go cell and source-extension builds) carries `PWD` naming its working directory (`toolchain.WorkDirEnv`), as os/exec sets it only for a nil environment. Otherwise the go command takes the physical directory, which a workspace's `use .` does not name. `TestSourceGoCommandBuildsThroughSymlinkedCheckout` and `TestWorkDirEnvReplacesInheritedPWD` lock it.

The built-in `container` builder follows the native builder in auto selection and is selected explicitly with `--builder container`; a configured builder cannot take that name. `PIG_CONTAINER_ENGINE` selects `docker` or `podman`; otherwise Podman is preferred over Docker. It runs the digest-pinned public Go base image of `automation/images/ci-go/Dockerfile` with `--pull=missing`, mounts a host Go module/build cache from `~/.pig/cache/container-go`, installs exactly the running release with `go install github.com/MichaelKinsy/PiG/cmd/pig@v<PigVersion>`, and runs that release's native builder, which fetches its own source. The host needs neither Go nor PiG source, and any `linux/<arch>` the engine runs can be targeted. The image has only the Go toolchain, so Rust extension cells need a configured builder image with Cargo. Probe runs only `<engine> info`; it does not pull. A development build reports `source-unavailable` because no matching release can be installed. Like configured container builders, it refuses `--sign-key`. Nested input mountpoints are created in the host input directory because an engine cannot create them inside the read-only `/input` mount. Evidence: `TestDefaultContainerEngineSelection`, `TestDefaultContainerProbeIsReadyWithoutPulling`, `TestDefaultContainerProbeNotReady`, `TestSelectBuilderAutoUsesContainerWhenNativeIsNotReady`, `TestDefaultContainerBuildInstallsRunningRelease`, `TestDefaultContainerImageIsTheCIGoImageBase`, and `TestCreateContainerMountpointsUnderReadOnlyInput`.

`pig piglet prune [--keep <n>] [--max-size <size>] [--dry-run]` removes built Piglet Binaries from the managed artifact store. Each build is kept forever otherwise, and a Go binary is tens of megabytes. The newest `n` builds of each Piglet and target (default 2, by record time) are never removed, because one build for several targets records one Binary per target. Without `--max-size` every other build goes, oldest first; with it they go oldest first only until the store is within the size, and the command reports that the limit is missed when protected builds alone exceed it. A removal takes the artifact, its Binary record, and its resolution record unless a remaining Binary uses it, together. Installs pulled from a signed release, which a `current` pointer names, are never touched. Nothing prunes this store automatically.

Startup verification cache: a signed Piglet Binary hashes its executable bytes at startup only when its file changed since its last passing start. After the closure, signature, signer, and signed-manifest checks all pass, `signature.CheckCached` records the Binary's resolved path, its file identity (device, inode, size, modification and status-change times; on Windows the volume serial number, file index, size, last write time, and change time), the SHA-256 of its signature block, and a digest of the trust policy (`require on`, trusted, revoked, and embedded keys) in `state/piglet-verify/verified.json` under the PiG config root. The file has one current unversioned shape: the verification rules ship inside the Binary an entry names, so a Binary with other rules is another file identity and never matches an older entry, and a file that does not parse as the current shape is treated as missing. The file is owner-only, replaced atomically, and keeps one entry per path, at most 8, dropping the oldest first. A later start that matches every field still checks the envelope signature, the signed size, the signer, and the manifest, and skips only the hash. Any difference, or a missing, corrupt, unreadable, or not owner-only cache, causes a full check, and an entry never vouches for another path. A failed check is never recorded. `pig verify` and `pig piglet verify` call `signature.Check`, which never reads the cache. When the cache cannot be written, startup prints a `pig: warning:` line on stderr and continues. Threat note: the cache keeps detection of every change made through a file system that records a status-change time (on Windows, the NTFS change time), including an in-place write that restores the modification time. It gives up start-time detection of in-place corruption that keeps the size and the recorded times, of an in-place write that restores the modification time on a file system without a status-change time (such as FAT), and on Windows of a writer that also sets the change time back; `pig piglet verify` still detects all of these. The startup check is a self-check and does not stop an attacker who can rewrite the Binary. On an Apple M4 Pro (median of 10, `-p`, faux provider, 63 MB signed Binary), Stock PiG took 31.1 ms, a full check at every start 56.1 ms, a first start that writes the cache 59.3 ms, and a start with a matching entry 33.3 ms. Evidence: `coding/piglet/signature/verifycache_test.go` counts executable hashes through the `hashExecutable` seam. A second check skips the hash. A touched modification time, an in-place write that restores the modification time, a recorded size that differs, a replaced file (new inode), a changed trust policy or embedded signer, and another path to the same file each cause a re-hash. A tampered byte with a changed identity fails. Corrupt, unknown-field, unreadable, and not owner-only caches cause a full check. Failed checks are never written, `Check` always hashes, and the cache keeps at most 8 entries. `coding/pigletbuild/binarypiglet/signature_test.go` shows that a manifest mismatch is never remembered, that a cache write failure is reported and not fatal, and that `require on` still refuses a remembered Binary signed by an untrusted key.

A Piglet Binary needs no extension. `pig piglet build --format binary` and `pig piglet publish` build a Piglet whose effective form (after `extends` and `extends.remove`) lists no extensions and has no frontend member: a base that only strips built-ins (D92) or sets defaults such as `model` is Stock PiG's own parts with that Piglet baked in. `pigletbuild.Validate` takes the effective Piglet's listed extension count beside the resolved count, so "lists none" is decided from the manifest, not from resolution results: a Piglet that lists extensions that resolve to none still fails with `piglet resolves to no extensions` beside each `unresolved extension` blocker, and a Piglet whose extension cells all stay outside the Binary still fails with `nothing to embed or fuse`. Locked by `TestValidateRejectsUnresolvedPigletsAndAcceptsEmptyBases`, `TestRunBuildRejectsListedExtensionThatResolvesToNothing`, `TestEmptyBasePigletBuildsASignedBinary` (signed build, `pig piglet verify`, `-p` answered by the baked default model, `--help` without the stripped tools; red before: `piglet resolves to no extensions`), `TestEmptyBaseChildWithAnExtensionBuilds`, `TestRunBuildRejectsPigletWhoseCellsAllStayOutsideTheBinary` (a Python-only Piglet, `nothing to embed or fuse`) and `TestPublishVerdictCountsTheListedExtensions` (the `pig piglet publish` verdict) in `coding/pigletbuild`. Approval: owner and the 0.4.2 lead, 2026-10-08 (MINIMAL-BASE gap B1).

Every Piglet Binary record keeps the strip table of the core that built it (`stripTable`, one ID list per kind) and, for a keep-mode Piglet (D92), its keep-mode lists (`stripKeep`). Both are fields of the one current Binary record shape, canonical and in the record digest, with no version field or reader branch. `pig piglet build` reads the newest Binary record of the same Piglet from the record store (`coding/piglet/strip_delta.go`, `StripDeltaReport`) and prints the strip delta report on stderr before the build runs, and in `--json` output as `stripDelta`; `pig piglet show` prints it too. The report names the built-ins the running core's table adds since that record: the ones a keep-mode list leaves out and the ones a deny-mode list now includes. A Piglet without a Binary record prints none. Locked by `TestRunBuildPrintsTheStripDeltaReport` (a previous record whose table lacks `/trust` and `codemode`; the report prints although the build then fails at builder selection), `TestShowPrintsKeepModeAndTheStripDelta` and `TestKeepModePigletBuildsASignedBinary`. Approval: owner and 0.4.2 lead, 2026-10-08.

TypeScript and JavaScript extensions in a Piglet Binary: the native builder packs each Node cell's member sources into one deterministic tar archive (a directory member whole except `.git`, a single-file member as that file; a symbolic link that names the member directory or a path inside it is kept, and one leaving the member directory fails the build) and embeds it as a cellpack cell with language `node`, the archive's SHA-256 digest, and each member's source path. The component plan records the member as a subprocess component with binary materialization and a `node` runtime requirement, so `VerifyRegisteredClosure` and the signed manifest's embedded files include it. At startup `cellpack.Register` refuses an archive whose bytes differ from the manifest digest and publishes it once per digest under `<config root>/piglet-binary-cells/node/<digest>/sources` through `runtimecell.PublishArtifact`, the cache publication Stock PiG uses for its own Node runtime. A later start reuses a published entry by its `ready.json` alone and does not compare the extracted files with the archive, unlike an embedded Go or Rust cell, which is rewritten at every start; a changed or deleted extracted file persists until the digest directory is removed. The host resolves each member from its extracted source with `ResolveExtConfigWithIdentity` and starts and reloads it through the ordinary cell planner, so packing, admission order, event bus, the Node preflight (22.13 or newer on `PATH`), and load failures are Stock PiG's: a missing or old Node reports `Failed to load extension "<source>": ... TypeScript extensions need Node.js 22.13 or newer; ...` with the `-ne` hint and startup exits 1, as upstream main.ts does for any extension load error. The Binary embeds no Node. Evidence: `TestComponentPlanRecordsTheNodeRuntime`, `TestBuildNodeCellEmbedsMemberSources`, `TestExtractNodeCellPublishesMemberSources`, `TestHost_LoadEmbeddedCellsStartsNodeCellFromExtractedSources`, `TestHost_LoadEmbeddedNodeCellWithoutNodeReportsStockError`, `TestNodeExtensionStripConflictNamesTheStripEntry`, and `TestPigletBinaryRunsItsNodeExtension`, which builds a real Binary with a TypeScript extension and runs its tool and command with the faux provider.

Remove when: upstream Pi provides equivalent composition and immutable build
artifacts, or Pig drops Piglet support.

Call-site markers:
- `coding/cli/main.go`
- `coding/cli/build_command.go`
- `coding/cli/package_commands.go`
- `coding/piglet/`
- `coding/pigletbuild/`
- `coding/piglet/npmsource.go`
- `coding/piglet/release/fetch.go`
- `coding/source/npmname.go`
- `internal/npmpublish/`
- `coding/cli/package_publish.go`
- `internal/buildprogress/`
- `internal/fspublish/`
- `coding/extension/host/runtimecell/go_packed.go`
- `coding/extension/host/runtimecell/rust_packed.go`
- `coding/extension/host/subprocess/builder.go`
- `coding/extension/installresolver/registry.go`
- `coding/extension/host/cellpack/node_cell.go`
- `coding/extension/host/subprocess/embedded_cells.go`
- `coding/extension/host/subprocess/host.go`
- `coding/pigletbuild/node_cell.go`

Tests:
- `coding/piglet/` (`prune_test.go` for `pig piglet prune`)
- `coding/pigletbuild/` (`publish_npm_test.go` for `pig piglet publish --to npm`)
- `coding/piglet/npmsource_test.go`
- `coding/piglet/release/fetch_test.go`
- `internal/npmpublish/npmpublish_test.go`
- `coding/cli/package_publish_test.go`
- `coding/cli/piglet_publish_npm_test.go` (publish a Package and a Piglet to a local registry stub, add the Piglet back, validate it)
- `coding/cli/agent_environment_test.go`
- `coding/cli/build_command_test.go`
- `coding/cli/build_progress_test.go`
- `internal/buildprogress/reporter_test.go`
- `internal/fspublish/` (`publish_linux_test.go` for the no-replace rename where Android refuses a Piglet commit's hard link)
- `coding/pigletbuild/publish_linkrefused_linux_test.go` and `coding/piglet/add_linkrefused_linux_test.go` (Piglet scripts, records, managed artifacts, the staged PiG module source and `pig piglet add` publish while the kernel refuses links through a seccomp filter)
- `coding/cli/package_commands_test.go`
- `coding/piglet/signature/verifycache_test.go` (startup verification cache)
- `coding/pigletbuild/binarypiglet/signature_test.go` (startup signature check and its cache)
- `coding/pigletbuild/node_cell_test.go`
- `coding/pigletbuild/node_binary_test.go` (Piglet Binary with a TypeScript extension)
- `coding/extension/host/cellpack/node_cell_test.go`
- `coding/extension/host/subprocess/embedded_node_cell_test.go`
- `coding/cli/piglet_verify_cache_startup_test.go` (a signed Binary whose verification cache cannot be written runs and warns at each start)

PORT_MAP path: n/a.
SCRUTINIZED:approved
## D19 Multi-language subprocess SDK bridges

Stock disposition: required substrate. PiG Standard selects extensions but does not own the cross-language SDK or wire contract.

What: pig ships SDKs in three languages: Go (`extensions/sdk`), Rust
(`extensions/sdk-rs`), and Python (`extensions/sdk-py`): that all author
into the **same** pig subprocess extension API. Upstream pi ships a single
TypeScript SDK and loads extensions in-process.

Current behavior: every SDK speaks the one current wire contract declared in
`coding/extension/host/subprocess/protocol.go` (
`register/ready/request/response/call/call_result/notify/cancel/shutdown/
widget_push`). Registration shape (`tools/commands/handlers/...`),
host-call methods (`ui.notify`, `ui.setStatus`, `sendMessage`,
`sendUserMessage`, `appendEntry`, `setSessionName`), tool-result shape
(`content/details/is_error`), and cancellation semantics are identical
across SDKs and validated by `test/extension-conformance/conformance_test.go`,
which compares the in-process Go reference against Go, Rust, and Python
subprocess SDKs. Focused components use one lifecycle-owned worker per active
overlay. Go, Rust, Python, and Node coalesce timer invalidation, preserve ordered
input, reject late generations, and detach invalidation before disposal. The
host serializes terminal focus across extensions without blocking the TUI loop.
A subprocess extension's `onTerminalInput` listener registers through the
host-side `UIContext.OnRemoteTerminalInput`. The interactive host asks for its
verdict on an owned background task and resumes the listener pass on the TUI
loop, so rendering continues while a verdict is pending, and input typed after
the chunk waits so verdicts, rewrites, and handling keep upstream's order.
Footers and headers follow Pi's component contract across the process
boundary. Pi renders the component at the current width every frame
(`interactive-mode.ts:2418-2480`). Each SDK therefore offers a renderer form
(`SetFooterRenderer`/`SetHeaderRenderer`, `set_footer_renderer`/`set_header_renderer`)
that renders at the host's width and again after every `width_change`, and every
static `SetFooter`/`SetHeader` call tags its rows with the width the SDK holds.
The host paints rows only at the width they carry, so a frame laid out for a
wider pane never reaches the renderer's overflow check after a resize (public
issue #104). A string-list widget is sent with no width, as a `widget_push`
that needs no reply, and the host lays a width-less list out with
`Text(line, 1, 0)` as Pi does (`interactive-mode.ts:2321-2336`).
The wire has no independent version or compatibility
negotiation; it changes atomically with the running Pig binary and staged SDKs.

Transport: each extension process connects to one host socket named by `PIG_EXT_SOCKET` (a packed cell gets one variable per member). The socket is AF_UNIX. On Windows, a Node extension instead gets a named pipe with an unguessable name that only the current user can open, because Node's `net` module treats every Windows path as a named pipe. The Python SDK reaches AF_UNIX on Windows through Winsock, because CPython does not expose `socket.AF_UNIX` there; the Rust SDK does the same. Framing and handshake are identical on both carriers, and extension code never names the transport.

Model registry replication: Pi's `ctx.modelRegistry` reads (`getAll`, `find`, `getAvailable` and the typed reads) are synchronous. The Go, Rust and Python SDKs answer them with a blocking `getModelRegistryState` host call at the moment of the read. The Node runtime cannot block, so it declares `wants_model_registry` in its register frame and answers from a replicated snapshot: the ready payload's `models` and each `model_registry_update` notify. The host builds and sends that snapshot only to a connection that declared it (`UIBridge.RegisterExtConn`, `PublishModelCatalog`). The snapshot encodes the whole model catalog, more than 1 MB of JSON for the built-in catalog, and the host publishes it three times while it wires model operations. A Piglet Binary with one fused Go extension spent that work on its startup path when every connection received it, for an extension that discarded every copy: on an Apple M4 Pro (median of 20, faux provider), `-p` took 72.1 ms against 31.7 ms for Stock PiG built with the same flags, and takes 33.0 ms with the rule. Each SDK's answers are unchanged. `internal/codingagent/extension_catalog_binding_test.go` locks the rule on the real model-operation wiring (`WireModelOperations` over a `ModelRegistry`): `TestGoSDKExtensionStartsWithoutModelRegistrySnapshot` (a fused Go extension costs no catalog build in its ready payload or any publication, and its read still returns the host's registry) and `TestModelCatalogReachesOnlyRegistryReplicas`. A Node cell member receives the `model_registry_update` notify before its deferred ready payload, and that payload replaces the replica, so it must carry the catalog: `TestNodeModelRegistryReplicaSurvivesDeferredReady` fails when it does not or when the Node runtime does not declare `wants_model_registry`.

Staging: upstream pi loads TypeScript extensions in process, so it has no SDK
module to resolve and no staging step. Pig compiles extensions, so a clean host
needs a buildable copy of the SDK on disk. Pig embeds each SDK in the binary and
stages it to `<configRoot>/state/pigsdk/sdk[-py|-rs]`, keyed by a content hash
recorded in `.pig-sdk-version`, and re-stages whenever the binary's embedded SDK
differs. Product hosts pass that same resolved config root into source and packed
extension builders, so HOME, XDG, and PIG_HOME isolation cannot select a stale
SDK from another user root. Re-staging replaces the owned language directory so source files removed
from a newer SDK cannot survive and break extension builds. The extension build
cache key folds the staged SDK in, so an SDK change
invalidates every build compiled against the previous one; builds record the
fingerprint of the SDK they used and a re-stage prunes the ones the current SDK
can no longer select. `pig reload` exposes this: it stages and then prunes in one
command, because a re-stage is what makes a build stale and dropping them
separately leaves a window where the SDK is current but the selected builds are
not. `--dry-run` previews, `--all` also drops builds predating fingerprinting,
and `--sdk-path`/`--sdk-version` are accessors for build scripts that stage
before printing so a script never wires an extension to a directory pig is about
to restage. Staged-versus-embedded status is reported by `pig diagnose`, which
already covers it alongside paths, auth, models, and environment.

The Go bridge keeps nullable usage counts and optional booleans as pointers. `sdk.Bool`, `ContextUsage.TokensOr`, and `PercentOr` provide explicit construction/fallback ergonomics without changing field presence or wire behavior. Legacy Go factories and exact standalones build against a private current SDK alias with recursively copied subpackages and rewritten self-imports, including internal packages. Build-local aliases and modfiles are removed after success, failure, or cancellation; authored files remain unchanged. Interactive native cold builds select a product-neutral `Building extensions...` notice through the build observer. Node loads and print/JSON/RPC modes do not select it, regardless of terminal stderr.

`pig reload` is deliberately the same word as the interactive `/reload`: both
mean make what is loaded match what is on disk, and it is the command reached
for after rebuilding pig itself. `/reload` stages the SDKs for the same reason
before it recompiles extensions. None of this exists upstream, because upstream
extensions are in-process TypeScript and there is no SDK to stage.

Why: pig is a Go-native port. We do not embed a JS runtime, do not ship
WASM, and do not load dynamic Go plugins. To still let authors write
extensions in non-Go languages, pig adds out-of-process language bridges
that target the upstream-equivalent extension API rather than replacing it.
The SDKs are bridges into the existing API surface, not separate APIs.

Node extensions continue to author against Pi's canonical TypeScript API.
`extensions/sdk-ts` is a declaration-only adapter that pins those upstream types
and adds declarations for PiG-only calls. PiG's Node subprocess compatibility
modules remain the runtime implementation.

Skip conditions:
- if you only need a Go extension, use `extensions/sdk`
- all shipped SDKs expose the full core declaration surface (tools,
  commands, events, shortcuts, flags, providers, message renderers, widgets,
  and focused custom components). If a future upstream surface lands in one SDK,
  it must land in all SDKs or be recorded in `docs/extension-api-parity.md`
  in the same change.

Remove when: upstream pi gains equivalent first-class non-TS extension
SDKs, or pig consolidates onto a single SDK language.

Call-site markers:
- `extensions/sdk/...`
- `extensions/sdk-rs/...`
- `extensions/sdk-py/...`
- `extensions/sdk-ts/...` (TypeScript declarations only)
- `coding/extension/host/subprocess/protocol.go`
- `coding/extension/host/subprocess/load_error_cause.go`
- `coding/extension/host/subprocess/ui_bridge.go` (`PublishModelCatalog`: model registry replication)
- `coding/extension/ui.go` and `coding/extension/opaque_types.go`
  (`OnRemoteTerminalInput`, `RemoteTerminalInputHandler`)

Tests:
- `test/extension-conformance/conformance_test.go` covers Go/Rust/Python
  isolated lifecycle and timer redraw.
- `coding/extension/host/subprocess/integration_test.go` covers the Node runtime,
  including timer redraw, burst coalescing, and cleanup.
- `extensions/sdk-ts` verifies its exact upstream version pin and compiles a
  representative Pi-compatible extension with the PiG login augmentation.
- `coding/extension/host/subprocess/packed_go_test.go` covers Go/Rust/Python
  packed cells.
- `coding/extension/host/subprocess/host_inprocess_test.go` covers D31 fused
  focused components.
- `coding/extension/host/subprocess/ui_bridge_test.go` covers exclusive focus,
  cancellation, generation barriers, and cleanup.
- `internal/codingagent/terminal_input_queue_test.go` and
  `coding/extension/host/subprocess/terminal_input_test.go` cover ordered,
  non-blocking terminal-input verdicts, cancellation at shutdown, and a real
  Node listener through the production input loop.

PORT_MAP path: `coding/extension/host/subprocess/` (host) and the SDK roots above.
SCRUTINIZED:approved

## D20 Packed runtime-cell substrate for factory extensions

Generated Go cells read requirements, SDK pins, local replacements, and checksums from both extension roots and their workspace modules. They retain the highest declared requirement for a workspace module, even when its source uses a local replacement. A local source path does not remove Go's minimum-version constraint. `TestBuildGoPackedCellKeepsRequiredWorkspaceVersions` checks the generated module and compiles two consumers with different version requirements; `make porter-check` exercises the released root and SDK pins through the real loader.

Stock disposition: required substrate. Packing is a transparent host optimization and must remain behaviorally identical to isolated execution.

Extension builds use one Go toolchain and report failures briefly. `internal/toolchain.ResolveGo` selects the go command once per process: the command on PATH, else the PiG-managed toolchain. A command inside its own installation is used as found. A version-manager shim or a distribution binary outside its GOROOT is asked for its root with the inherited GOROOT removed, and the installation's own binary is then pinned, because a shim chooses its version from the directory it runs in and a build runs in a generated directory. Every Go build passes `GoToolchain.Environ`, which replaces an inherited GOROOT with that installation's root. An inherited GOROOT of another release is what made `go` run one release's compiler under another's go command (`compile: version "go1.26.7" does not match go tool version "go1.26.1"`, repeated for every standard-library package). A mismatch that remains, a broken installation, is one line naming both releases and the fix. A cell's cache key includes the toolchain root and the compiler binary's identity, so repairing the installation is an input change.

While cells compile in interactive mode on a terminal, one status line shows how many cells are finished and which extensions are compiling. It rewrites itself and is cleared when the load pass ends; a warm start prints nothing. A failed build is one line: `BuildFailure` holds the summary, a cause that failures of one kind share, and the path of a log under `<config-root>/cache/logs/` that holds the complete compiler output and the generated go.mod (newest 64 logs, each bounded). Extensions that fail for one cause are one line (`13 extensions failed to build: <cause> (names; details: <log>)`) in startup diagnostics and in `/reload` issues. At startup that line, like each member's `Failed to load extension` error in Pi, is followed by the `pig -ne` hint. A compiler diagnostic, and a mismatched toolchain, are recorded against the cell's inputs (sources, SDK, toolchain release, root and compiler identity), so an unchanged start reports the recorded failure without compiling; network, module and disk failures are never recorded. A recorded failure says how to retry: `pig extensions cache prune --failures`. Within a process a mismatched toolchain is met once, not once per cell.

`pig extensions cache prune --failures` removes recorded failures, including those of current extensions. The automatic daily prune also enforces a 5 GiB size limit, evicting the entries used longest ago; entries the current extensions use are never evicted for it, so the cache can exceed the limit. Retention stays 30 days.

The automatic prune never delays startup. Pi has no cache collection at startup; the packed-runtime cache is PiG's, so this is PiG behavior (D20). Loading extensions only records which entries the current extensions use. Interactive, print, JSON and RPC modes start the collection in the background once they are running, and a process starts at most one. It runs at most once a day, measured from the `.last-auto-gc` marker in the cache root, and only one process collects at a time. A process that exits cancels it and waits up to two seconds for it to stop; an entry it had already renamed to a tombstone is removed by the next collection. An entry a running extension leases, or builds, is skipped and counted, and so is an entry whose files the file system reports busy: EBUSY or ETXTBSY, a Windows sharing violation, or an NFS `.nfs*` file, which NFS creates in place of deleting a file that is open elsewhere. A directory that the collection could not remove because it still holds only `.nfs*` files, which happens when the collection itself deletes an open file, is busy too. A skipped entry is not an error and does not stop the collection from writing its marker. `pig extensions cache prune` reports the count as `Skipped (in use)` (`skipped` in `--json`). A failure that is not a busy file leaves the marker unwritten, so the next start collects again. The background collection never prints, because a terminal UI owns the screen: it records a failure in `cache/.auto-gc.error`, which `pig extensions cache stats` shows and the next successful collection removes.

What: pig adds an internal "runtime cell" planner and a set of generated
packed runners that can host multiple subprocess extensions inside one
shared OS process. Upstream pi has no equivalent because pi loads
extensions in-process.

Current behavior: `coding/extension/source/resolve.go` resolves one exact
conventional factory or standalone source. The subprocess source adapter derives
the runtime, language, factory, and placement fields. The planner in
`coding/extension/host/subprocess/cell_plan.go` groups those configs into
`CellSpec` values with strategies `isolated`, `packed-go`, `packed-rust`,
`packed-python`, or `packed-node`. Conventional Go, Rust, Python, and Node
factories are packable; every standalone (an exact executable, including a
shebang Node script) is isolated. Generated runners live under
`coding/extension/host/runtimecell/` and are cached by deterministic cell hash,
except `packed-node`: nothing is compiled for a Node cell, so its cache
(`coding/extension/host/subprocess/packed_node.go`) holds only one copy of the
embedded Node runtime plus a manifest naming each member's resolved entry; the
same jiti loader an isolated Node extension uses reads each member's
TS/JS source fresh at process start (N8; matches upstream pi hosting every
extension in one process, `packages/coding-agent/src/core/extensions/loader.ts`).
Each contained extension still gets its own Unix socket and runs the
**same** `register` handshake: there is no multi-register payload. A disambiguated host key retains the selected extension's registration identity in every language. Go factories from distinct roots with the same module path occupy separate cells because a Go build can select only one root per module path. `Host.Reload` loads each extension on its own as upstream does: a failed
extension is reported and not loaded. Native packed-cell failures retain their isolated fallback. Node factories share one process across interleaved native cells: a private host admission channel starts each member's factory only at its configured turn, and every member retains its own registration socket.

Pi re-invokes an extension's factory inside the process that already holds its module: `/reload` clears the factory cache and re-invokes every factory, and a Session replacement builds a new resource loader whose first load keeps the cache. PiG mirrors that with the same admission channel. A cell process's stdin stays open, and each control line (`admit`, member name, socket, entry, cwd, reload pass; or `park`) starts one more generation of a member's factory on a new socket beside the old one. The runners are `runtime-node/generations.mjs` for Node and the generated Go, Rust and Python runners in `coding/extension/host/runtimecell/`. The Node runtime applies Pi's `extensionCache` rules to each admission: a reload re-imports the module (jiti re-evaluates `.ts`; Node's ESM cache keeps an unedited `.mjs`, and an edited module and its local importers are evaluated again, D93), and a same-cwd replacement reuses the cached factory. Compiled and Python factories keep package or module state because the process is retained. On the first admission of a reload pass, the Python runner re-imports each extension that uses a module changed since its import, so `/reload` runs edited Python source as Pi runs an edited `.ts` extension. An extension uses each module its factory call imports, also one that another extension imported first, each module that a used module imports, the packages of a used submodule, and each module that no factory call imported whose deepest extension root is its own. An edit to a module that several extensions use reloads all of them. The runner records the imports of an extension's factory call and of its own modules; an import made by other code after the factory returned, an `__import__` call without globals, or a loader that bypasses `__import__` and `importlib.import_module` is seen only as a first import, so an extension that uses a module only that way can keep its old code until it reloads for another reason. A re-import drops each module whose loaded users all reload, so an unedited extension that uses no edited module keeps its module state beside an edited one in the same or a nested directory. A package installed under an extension's directory outside `site-packages` or `dist-packages`, for example with `pip install --target ./deps`, counts as its own source. The runner records each factory call's first imports with a finder first in `sys.meta_path` that finds no module itself, and every import, also of a module already imported, by wrapping `builtins.__import__` and `importlib.import_module` (`TestReloadRunsTheCurrentSourceOfACommandLineExtension`, `TestReloadKeepsTheModuleStateOfAnUneditedPythonExtensionBesideAnEditedOne`, `TestPythonRunnerReimportsOnlyAnEditedExtensionsOwnSource`). The old generation keeps serving until the swap; closing its connection retires it, including its event-bus subscriptions. The host counts the states that share one process (`processShare`) and kills the process with the last one, so a reload whose plan does not claim a process ends it. A compiled cell is retained by artifact, a Node cell by plan group, and an isolated Node extension by name and entry. Module state lives in the process that holds the module, and `isolation` is a PiG setting Pi lacks: an extension whose plan moves between an isolated process and a shared group, or that `Host.Load` started outside the plan, gets a new process and a new module on the next reload, and its old process ends (`TestReloadAcrossAPlacementChangeStartsAFreshProcess`). The `name`, `socket`, `entry` and `cwd` fields of an admission are percent-encoded (`%`, tab, line feed, carriage return) because a source path, the name derived from it and the runtime directory a user chooses may hold them; the Node, Go, Python and Rust runners decode the same four escapes. A crashed process is never retained: crash recovery and the supervisor start a fresh one. A process with a connection that closed without the host closing it, or that stopped answering its heartbeat, is never claimed again either, because a killed process still accepts control lines until it is reaped (`TestReloadAfterNodeCellCrashNoticeRefusesUnreapedProcess`, `TestUnexpectedConnectionCloseWithdrawsItsProcessFromRetention`). `RuntimeRetention` (`retention.go`) lets the Host of a replacement Session claim the processes its predecessor parked with `Host.Retain`, which returns once each runner has taken a `park` hold and connected to an acknowledgement socket, so retiring the last generation cannot end the process. A process exits when its last generation ends unless it is parked; the Node runtime keeps its stdin channel from holding the event loop open except while it awaits an admission or is parked, so RPC quit handling still observes an event-loop drain; an unclaimed parked process ends with the replacement's load. A Host that retires after its successor loaded parks nothing. Stock `pig` has no caller for `RuntimeRetention`: its Session replacement is in place and keeps one Host for the process (D30, D61), so its extension processes and module state already survive `/new`, `/resume`, and `/fork`. An embedder that builds a Host per replacement Session calls `SetRuntimeRetention` on each Host and `Retain` before `Shutdown`.

Node process recovery uses the admitted factory, stack evidence, or last dispatched owner to quarantine only the culprit. The remaining Node members restart together. An unattributable crash restarts the whole group once; repeated unattributable crashes split diagnostic groups in halves until the failing member is identified, then rejoin all healthy members. Diagnostic groups and culprit quarantine are runtime fault containment, not changes to authored isolation. Recovery does not replay an interrupted tool or callback. Generation checks reject obsolete crash reports and connection-owned UI state; new named capability calls reach the replacement, while already-admitted callbacks fail on the dead connection. Recovery inherits the original owner cancellation and drains on Host shutdown.

Node transform caching remains Jiti's implementation: the loader reads source contents on each load and disables evaluated-module caching between loads. PiG places the disposable filesystem cache under `<config-root>/cache/jiti` and separates loader content, Jiti and Node versions, and compiler environment options. A changed temporary directory does not discard valid transforms. Cache-disable options retain Jiti's behavior. The transform cache is not part of a published cell's immutable artifact or its pruning commands. Internal imports avoid loading unused TUI, highlighting, and schema dependencies; extension imports still receive the complete shared Pi namespaces. `TestNodeJitiCachePersistsWithoutStaleSource`, `TestNodeStartupDefersPresentationDependencies`, and `extensions-runtime/56-node-lazy-imports` guard these boundaries.

PiG bundles the pinned AI and TUI implementation graphs while retaining canonical shared modules, complete exports, and original source-relative asset URLs. Private emoji-regex compilation and native segmenter construction happen when first used; public segmenter getters still return the same native instances. Node's source-validated bytecode cache activates on virtual-library import after the first Jiti transform and flushes when the runtime processes the host's ready message. Its default directory is `<config-root>/cache/node-compile`; existing Node cache configuration and `NODE_DISABLE_COMPILE_CACHE` remain authoritative. This cache stores no evaluated factories and is outside cell artifact pruning. `TestNodeLibraryBundlesKeepSharedIdentitiesWithoutRawCatalogLoads`, `TestNodeCompileCacheStartsAtLibraryImportAndChecksSourceBytes`, `TestNodeReadyPublishesNativeCompileCache`, the private Unicode guards, bundle regeneration, and pinned-source comparison cover these implementation boundaries. No worker, notification, callback, or factory ordering is removed.

A Node cell's added member costs only its own state, as an extension added to Pi's one process does. Jiti's disabled module cache does not copy the Pi library: virtual SDK imports resolve through Node's module cache, so a second member that imports the SDK loads no further module. The members share the process's one IO worker (D19), and the host's model registry publications, which reach every member's socket, are read, validated and decoded once per process; each member copies what its extension reads from the shared snapshot, so members never share a model object and packed matches isolated. With 10 synthetic TypeScript extensions that import the SDK, the cell's peak RSS fell from about 385 to 140 MiB and `-p` startup from about 464 to 259 ms. `TestNodeCellMembersShareOneIOWorker`, `TestNodeCellAddedMembersStayWithinTheirOwnState`, `TestNodeCellMembersShareRegistryPublications` and the conformance row `TestNodeModelCatalogReadsAreEachExtensionsOwn` cover it.

Why: pi's in-process model gives one runtime per extension for free. Pig
must spawn a process per extension by default, which is expensive when
authors register many small factory-style SDK extensions; for Node this was
especially costly (a 150-210 MB Node process per TS/JS extension). The
packed-cell substrate is a transparent optimization that preserves the same
wire per extension, preserves the upstream-equivalent extension API, and
uses fault containment under crash quarantine. It is not a new authoring
model - factory extensions are still ordinary subprocess extensions; the host
just shares the OS process when it is safe.

Skip conditions / non-pack triggers:
- the selected source is a standalone (including a shebang Node script);
- source resolution does not prove one exact standard factory;
- the extension's isolation is set to anything other than empty/`shared-ok`
  (the `--isolated`/`isolation: strict` escape hatch);
- a native cell is quarantined, which fissions its members on reload;
- Node recovery identifies a culprit, which receives its own process while healthy members remain shared. Temporary bisection groups diagnose an unknown repeated crash.

A Go module or workspace can contain several factory packages. Selecting the
module or workspace root is ambiguous. Selecting one exact package keeps the
containing module or workspace as its compiler closure and remains packable.

Remove when: upstream pi gains a comparable shared-process substrate or Pig no
longer needs shared-process packing. Multi-register remains outside the approved
extension model.

Call-site markers:
- `coding/cli/extensions_cache_command.go`
- `coding/extension/host/subprocess/cell_plan.go`
- `coding/extension/host/subprocess/reload_cells.go`
- `coding/extension/host/subprocess/packed_go.go`
- `coding/extension/host/subprocess/packed_rust.go`
- `coding/extension/host/subprocess/packed_python.go`
- `coding/extension/host/subprocess/packed_node.go`
- `coding/extension/host/subprocess/node_recovery.go`
- `coding/extension/host/subprocess/cell_start.go`
- `coding/extension/host/subprocess/packed_quarantine.go`
- `coding/extension/host/subprocess/source_resolver.go` (node factory defaults
  to `shared-ok`)
- `coding/extension/host/runtimecell/go_packed.go`
- `coding/extension/host/runtimecell/rust_packed.go`
- `coding/extension/host/runtimecell/python_packed.go`
- `coding/extension/host/subprocess/runtime-node/jiti-loader.mjs` (persistent transform cache location)
- `coding/extension/host/subprocess/runtime-node/compile-cache.mjs` (native bytecode cache activation)
- `coding/extension/host/subprocess/runtime-node/cell.mjs`
- `coding/extension/host/subprocess/runtime-node/state.mjs` (AsyncLocalStorage
  scoping so a Node cell's shim calls resolve to the right extension)

Tests:
- `coding/extension/host/runtimecell/{go,rust,python}_packed_test.go`
- `internal/toolchain/resolve_test.go`, `coding/extension/host/runtimecell/build_failure_report_test.go`, `coding/extension/host/subprocess/{build_line,build_failure_group}_test.go`, `coding/cli/extension_build_diagnostics_test.go`, and `coding/cli/extensions_cache_command_test.go` (`TestAutomaticExtensionCacheGCEnforcesTheSizeLimitOldestFirst`)
- `coding/extension/host/subprocess/cell_plan_test.go`
- `coding/extension/host/subprocess/packed_go_test.go`
  (`TestHost_ReloadPlansPacked{Go,Rust,Python}AndFissionsQuarantinedCell`)
- `coding/extension/host/subprocess/node_cell_test.go`
  (`TestNodeCellHostsFiveExtensionsInOneProcess`,
  `TestNodeCellContinueOnErrorLoadsHealthyExtensions`,
  `TestNodeCellRegistrationOrderMatchesConfigOrder`,
  `TestNodeCellReloadKeepsOneProcessForAllExtensions`,
  `TestNodeCellProcessDeathStopsExtensionsAndReloadRecovers`,
  `TestNodeCellIsolatedEscapeHatchGetsOwnProcess`)
- `coding/extension/host/subprocess/retained_factory_test.go` and `retained_native_factory_test.go`: module state across reload and Session replacement for Node (packed and isolated), Go, Python and Rust, Pi's loader rules for `.mjs` and `.ts`, old-generation bus retirement, crash recovery after a retained reload, and release of unclaimed parked processes.
- `coding/extension/host/subprocess/node_admission_test.go`, `node_recovery_test.go`, and `node_recovery_lifetime_test.go`: interleaved admission/bus, culprit-only quarantine, whole-group retry, bisection/rejoin, no replay, stale generations, original-owner cancellation and shutdown draining.
- `coding/extension/host/subprocess/node_runtime_cache_linkrefused_linux_test.go` (`TestNodeRuntimeMaterializesWhereLinksAreRefused`: a launcher's Node runtime tree is copied while the kernel refuses links through a seccomp filter)
- `coding/extension/host/subprocess/node_cell_memory_probe_test.go`
  (`TestNodeCellMemoryProbe`, gated by `PIG_NODE_CELL_MEMORY_PROBE=1`)

PORT_MAP path: n/a (downstream-only host substrate; does not map to any
upstream pi file).
SCRUTINIZED:approved

## D21 Reload placement report

Stock disposition: product-neutral diagnostics. Keep this in Stock PiG because it explains the generic host's placement decisions without activating a product workflow.

What: Pig records the latest subprocess reload decision for `/reload --explain`.
The report contains placement, cache, build, replacement, and quarantine facts.
`LastReloadReport` returns a copy for the interactive command.

Why: Pi loads extensions in process and has no runtime-cell placement report.

Call-site marker: `coding/extension/host/subprocess/reload_report.go`.

Locked by: `coding/extension/host/subprocess/reload_report_test.go`,
`coding/extension/host/subprocess/reload_failure_test.go`, and
`internal/codingagent/slash_session_handlers_test.go`.

Remove when: upstream provides an equivalent reload placement report, or Pig
removes `/reload --explain`.

PORT_MAP path: n/a.
SCRUTINIZED:approved

## D22 Stock documentation materialization

Stock disposition: required substrate. The materialized bundle documents Stock PiG contracts and activates no optional Product Resource.

What: Stock Pig embeds its public documentation and materializes it under
`<ConfigRoot>/docs`. The `pig docs` command reads and refreshes that bundle.
Upstream Pi can rely on documentation installed with its npm package, while a
standalone Pig binary cannot assume a source checkout exists.

Current behavior: startup performs a best-effort content-addressed sync. The
stock system prompt points at the materialized bundle so the agent can inspect
the exact extension, Package, and Piglet contracts implemented by its binary.
The `codemode` description and the argument errors of its `models` globals name
the bundle's `codemode.md` where Pi names `CODEMODE_DOCS_PATH` in its package.
This is distribution infrastructure, not an extension, and it activates no
optional Product Resource.

Remove when: every supported Stock Pig distribution installs the matching docs
beside the binary through another verified mechanism.

Call-site markers:
- `internal/pigdocs/pigdocs.go`
- `coding/cli/main.go`
- `internal/codingagent/prompts/coding.go`
- `coding/extension/builtin/codemode/description.go`
- `internal/evals/harness.go`

Tests:
- `internal/pigdocs/pigdocs_test.go`
- `internal/codingagent/auth_guidance_test.go`
- `internal/codingagent/prompts/coding_test.go`
- `coding/extension/builtin/codemode/docs_path_test.go`

PORT_MAP path: n/a (standalone distribution support).
SCRUTINIZED:approved
## D23 Per-tool source metadata and piglet-scoped active-tool Context API

Stock disposition: inert capability. The API changes no active tools unless a selected Piglet applies a scope.

What: pig adds four methods to `extension.Context` (`GetAllTools`,
`GetActiveTools`, `SetActiveTools`, `GetFlagValue`) and a per-tool `Source`
field on the subprocess wire protocol's `ToolDecl` / SDK `toolDef`. Together
these enable the piglet extension (D18) to:

- Identify which source each tool came from (builtin, a named extension, or a
  specific MCP server via the `"mcp:<server>"` convention).
- Apply per-source tool scoping rules from the piglet YAML.
- Filter the active tool set via `SetActiveTools` so the agent only sees
  piglet-allowed tools.
- Read CLI flag values at runtime via `GetFlagValue` (e.g. `--piglet`).

MCP tool scoping (two-axis, so piglets and the MCP adapter coexist without
surprises):
- An explicit `mcpServers:` section is authoritative and per-server: a listed
  server is scoped by its `tools` entry; an unlisted server is excluded.
- With no `mcpServers:` section, MCP tools follow the same positive-only gate
  as extensions, keyed on the `pig-mcp-adapter` extension entry
  (`scope.MCPAdapterExtensionName`). Listing the adapter enables its MCP tools
  (optionally filtered by that entry's `tools` scope); omitting it from a
  positive-only piglet hides them. A piglet with no `extensions:` section at
  all keeps the permissive default (all MCP tools visible). This closes a prior
  leak where a positive-only piglet that omitted the adapter still exposed all
  MCP tools, and makes `extensions: [pig-mcp-adapter]` meaningful rather than a
  no-op.

Upstream pi stamps `sourceInfo` automatically in the in-process extension loader
(`loader.ts:221 sourceInfo: extension.sourceInfo`). Pig's subprocess host must
receive the per-tool source through the wire protocol because extensions run
out-of-process. The `Source` field on `ToolDecl` / `toolDef` is optional and
backward compatible: when omitted, the host falls back to the extension name.
The per-tool source reaches Piglet scoping through the in-process
`extension.Context`. Node extensions see exactly upstream's `ToolInfo`, whose
`sourceInfo` is the registering extension's, as `pi.getAllTools()` does. The
`getAllTools` host call the Go, Rust and Python SDKs use also carries the
per-tool source as `source`, which the Go SDK exposes as the deprecated
`ToolInfo.Source`.

Upstream has no `GetAllTools`, `GetActiveTools`, `SetActiveTools`, or
`GetFlagValue` on `ExtensionContext` because upstream loads extensions
in-process and does not have piglet-driven tool scoping.

Skip conditions:
- Extensions that do not wrap external tool sources do not need to set `Source`.
- Extensions that do not read flags do not call `GetFlagValue`.
- Only the piglet extension calls `SetActiveTools`; other extensions should
  not modify the active tool set directly.

Remove when: upstream pi adds equivalent tool-scoping and flag-value Context
APIs, or pig's piglet extension is replaced by an upstream mechanism.

Call-site markers:
- `coding/extension/context.go`: `GetAllTools`, `GetActiveTools`,
  `SetActiveTools`, `GetFlagValue`
- `coding/extension/context_actions.go`: `ContextActions` struct
- `coding/extension/host/subprocess/protocol.go`: `ToolDecl.Source`
- `extensions/sdk/protocol.go`: `toolDef.Source`
- `extensions/sdk/extension.go`: `ToolWithSource()`
- `extensions/sdk-rs/src/protocol.rs`: `ToolDef.source`
- `extensions/sdk-rs/src/extension.rs`: `tool_with_source()`
- `extensions/sdk-py/pig_sdk/__init__.py`: `tool(..., source=)`
- `coding/extension/host/subprocess/host.go`: `buildExtension()` reads
  `ToolDecl.Source` into `RegisteredTool.SourceInfo`
- `internal/codingagent/interactive_extensions.go`: wires `ContextActions`
- `coding/piglet/scope.go`: `ScopeTools()` MCP branch
- `coding/piglet/main.go`: `BuildExtension()` calls
  `SetActiveTools`

Tests:
- `coding/piglet/piglet_test.go`: MCP scoping tests
- `coding/extension/host/inproc/context_actions_test.go`
- `test/extension-conformance/conformance_test.go`: cross-SDK source
  round-trip
- `test/upstream-parity/context_parity_test.go`: `pigOnlyContextMembers`

PORT_MAP path: n/a (downstream piglet-scoping and per-tool source plumbing).
SCRUTINIZED:approved
## D28 Extension validation report surface (`pig install --validate-only --json`)

Stock disposition: required substrate. Package publishers and Piglets may consume the report, but PiG Standard does not own validation.

Pig adds `pig install --validate-only [--json]` (alias `--check`): it loads,
builds, and starts one or more extension package refs without installing them,
then emits a structured `extensionValidationReport`. Upstream pi has no
validate-only install mode and no machine-readable extension validation output;
its extension runtime is in-process JavaScript loaded at session start, so there
is no separate "register without installing" step to report on.

This is a downstream-only interoperability surface. External deployment and
publication systems can run `pig install <root> --validate-only --json` in an
isolated `PIG_HOME` and parse the JSON report. Nothing in Pig core depends on a
specific deployment controller.

The report carries, per package: validity, registration state, the flat name
lists of each registered surface (tools, commands, handlers, flags, shortcuts,
providers) for counts and duplicate detection, and: under `toolDetails` /
`commandDetails`: the model- and user-facing text of each registered tool and
command (label, description, prompt snippet, prompt guidelines). The detail
arrays exist so a downstream validator can scan the text a connected extension
injects into the model context for prompt injection; the flat name lists stay
authoritative for counts.

Constraints:
- `--validate-only` is install-only (`packageInstall`); it never mutates
  settings or installs a package.
- Detail projections are emitted in deterministic tool/command name order.
- `toolDetails`/`commandDetails` are `omitempty`: a clean extension with no
  description text emits no detail entries, not empty ones.
- The flat name lists remain the authority for registration counts and
  duplicate detection; the detail arrays are additive scannable text.

Call-site markers:
- `coding/cli/extension_validate_command.go` (`extensionValidationReport`,
  `toolDetailsFromExtension`, `commandDetailsFromExtension`)
- `coding/cli/package_commands.go` (`--validate-only`/`--check` dispatch,
  `validateInstallSources`)

Tests:
- `coding/cli/extension_validate_command_test.go`
  (`TestInstallValidateOnlyLoadsAndRegistersExtension`,
  `TestInstallValidateOnlyEmitsToolAndCommandDetails`)

Remove when: upstream pi adds an equivalent machine-readable extension
validation report, or Pig drops `pig install --validate-only --json`.

PORT_MAP path: n/a (downstream-only validation/interop surface; does not map to
any upstream pi file).
SCRUTINIZED:approved

## D31 Fused in-process extension runtime for Piglet Binaries

Stock disposition: inert capability. Stock PiG never selects the fused path; an explicitly built Piglet Binary activates it through its verified component plan.

What: an additive extension load path, `subprocess.Host.LoadInProcess`
(`coding/extension/host/subprocess/host.go`), that runs an extension's factory
inside the host process over an in-memory `net.Pipe` instead of spawning a
subprocess and connecting over a Unix socket. It reuses the full wire protocol
and register/ready handshake (`adoptConn`); only the transport differs: the
extension serves via the SDK's `RunWithConn` (a goroutine in the host) rather
than `RunWithSocket` in a separate process. This is the "linked in-process
runtime" gated by the pig extension-boundary rules: extension code is compiled
into the pig binary and runs in-process (fused), with no subprocess, no socket,
and no runtime toolchain.

Why: a Piglet Binary may fuse compatible Go extensions to reduce process and
memory overhead while retaining exact component-plan identity. The
`coding/pigletbuild` builder registers their
`factory().RunWithConn` serve funcs; non-fused components use subprocess or
external realization.

Observational identity: stock pig never calls `LoadInProcess`. The
`managedExt.inProcServe` field is nil for every extension a normal pig loads, so
`connectExt` takes the unchanged listen/spawn/accept path, so its behavior is
the subprocess path's. A Piglet Binary supplies a non-nil serve func.
The subprocess and fused paths converge at `adoptConn`, so a fused extension
observes the same handshake, ready payload, provider registration, and event
dispatch as a subprocess one.

Conformance gate: every shared cross-SDK behavioral harness includes `fused-go` beside subprocess Go. The canonical Go fixture factory drives both realizations, so tool, command, event, UI, model, terminal-input, geometry, source-metadata, OAuth, user-content, and liveness recordings fail on fused-only drift. `TestConformance_TransportsMatch`, `TestLivenessConformanceSDKsMatch`, and the focused conformance rows in `test/extension-conformance/` enforce the shared protocol behavior.

Startup cost: on an Apple M4 Pro (median of 20, faux provider, Binary and Stock PiG both built with `-trimpath -ldflags '-s -w'`), a Binary with one fused Go extension takes 22.6 ms for `--version` against 21.5 ms for Stock PiG, 33.0 ms for `-p` against 31.6 ms, and shows the first typed key after 33.4 ms against 33.6 ms. The fused extension's register handshake and session events cross an in-memory pipe and the SDK decodes them; it receives no model registry snapshot (D19). A signed Binary also hashes its whole executable with SHA-256 before command dispatch, because the signature binds those bytes. For a 62 MB darwin/arm64 Binary that adds about 26 ms to every start (`--version` 48.5 ms). The hash stays on the startup path: a digest cached by file metadata cannot prove the bytes it skips.

Fused code shares Pig's process-global state. Before a Piglet Binary build links a factory, `coding/pigletbuild` inspects the factory package and its local module dependency closure. The build fails with `PIGLET_FUSED_PROCESS_HAZARD` when fused code calls `os.Exit`, `os.Chdir`, `log.Fatal*`, `fmt.Print*`, or accesses `os.Stdout`. Authors must return errors and use extension host/UI APIs instead of terminating the process, changing its working directory, or writing into terminal output. The vet reads only the members' own and workspace module source, so after it passes the build prints one `note:` naming every other module the members and workspace modules require, as sorted `path@version` at the version the merged `go.mod` selects, with the replacement target when a `replace` supplies the source (`fusedModuleGraph.unvetted`, `fusedVetScopeNote`). No note prints when there are none.

Fused members bring their module graph. A Go dependency module's requirements and replacements do not reach the main module, so `coding/pigletbuild` (`mergeFusedModules`, `overlayFuse`) adds the fused extensions' and frontend member's requirements to the build overlay's view of Pig's `go.mod` by minimum version selection (highest version wins; a raised Pig requirement prints a `note:`), promotes their non-SDK replacements, and overlays `go.sum` (and `go.work.sum` for a workspace checkout) with Pig's checksums plus the members'. The build therefore runs under `-mod=readonly`, offline from a warm module cache, from a read-only checkout, and never writes a checksum or `go.mod` into the source tree. A member requiring a newer SDK than the Pig source pins, or two different replacements of one module, fail with an error naming both sides. The member's `go.mod`, `go.sum`, and local replacement sources are recorded build inputs (`buildInputs`).

Call sites:
- `coding/extension/host/subprocess/host.go`: `LoadInProcess`, `connectExt`
  (fuse branch), `adoptConn` (shared handshake).
- `extensions/sdk/extension.go`: `RunWithConn` (serve over an established conn).
- `coding/pigletbuild/native_build.go`: `overlayFuse` (fused members' module
  graph and checksums in the build overlay, via `mergeFusedModules`).
- Consumer: Piglet Binary fused registration (dormant in stock Pig).

Remove when: the Piglet Binary component plan no longer supports fused realization.

Locked by: the shared `fused-go` rows in `test/extension-conformance/`; `TestBuildNativeArtifactRejectsFusedProcessHazards`, `TestVetFusedPackagesInspectsLocalDependencyClosure`, `TestOverlayFuseBuildsExternalModuleFromReadOnlyWorkspaceCheckout`, `TestOverlayFuseBuildsExternalModuleWithoutWorkspaceReadonlyOffline`, `TestOverlayFuseRejectsMemberRequiringNewerSDK`, `TestOverlayFuseRejectsConflictingMemberReplacements`, `TestMergeFusedModulesKeepsHigherPigVersion`, `TestBuildNativeArtifactNamesModulesTheFusedVetSkipped`, `TestBuildNativeArtifactPrintsNoVetScopeNoteWithoutExternalModules`, `TestMergeFusedModulesNamesUnvettedModulesAtTheSelectedVersion`, `TestOverlayFuseNamesTheFrontendMembersUnvettedModules`, and `TestBuildInputsFrontendDigestCoversItsModuleGraph` in `coding/pigletbuild`; and `TestHost_LoadInProcess`, `TestAC59FusedFocusedComponentUsesProtocolV1`, `TestFusedFocusedTimerInvalidatesAndCleansUp`, and `TestHost_LoadInProcess_NilServe` in `coding/extension/host/subprocess/host_inprocess_test.go`.

SCRUTINIZED:approved
## D36 Insecure TLS opt-in for OpenAI-compatible providers

Stock disposition: explicit unsafe opt-in. It remains off by default and must not be enabled by PiG Standard. Prefer a trusted CA whenever one is available.

What: pig adds an `Insecure bool` field to `ai.OpenAIConfig`,
`extension.ProviderConfig`, the model registry's `providerConfig`, and
`ModelEntry`. When set, the OpenAI provider's HTTP client skips server TLS
certificate verification. This is a general, opt-in knob for any
OpenAI-compatible endpoint behind a self-signed or internal-CA certificate
(on-prem gateways), not tied to any product. Upstream pi relies on Node's global
`NODE_TLS_REJECT_UNAUTHORIZED` / custom agents; pig has no equivalent, so this
additive optional field is the pig mechanism. It is never the default and has no
effect when unset.

Product OAuth login and model providers are not core divergences. OAuth is
contributed through the generic OAuth extension bridge (a parity mechanism; see
`docs/extension-api-parity.md`). Model providers use the generic
`RegisterProvider` extension API. Product implementations remain outside Stock
PiG.

Parity impact: upstream pi has no `Insecure` field. The upstream fast/hermetic
parity gate stays green: `Insecure` is an additive optional field (omitempty,
defaults false, no behavior change when unset).

Call-site markers:
- `ai/openai.go`: `Insecure` field on `OpenAIConfig`.
- `ai/http_transport.go`: `InsecureSkipVerify` applied when a provider opts in.
- `internal/codingagent/model_registry.go`, `coding/extension/provider.go` -
  `Insecure` on the provider config and resolved model entry.
- `coding/model.go`, `coding/cli/model.go` thread `ModelEntry.Insecure` into the
  OpenAI-compatible provider construction (plumbing of the marked field).

Tests:
- `internal/codingagent/model_registry_test.go`
  (`TestModelRegistry_RegisterProvider_InsecureThreadsToEntry`).

Remove when: upstream pi gains a first-class per-provider TLS-skip option, or
pig adopts a global TLS-reject equivalent that supersedes this field.

SCRUTINIZED:approved

## D40 Extension-contributed authentication targets

Stock disposition: inert capability. Stock PiG owns product-neutral discovery and dispatch; selected extensions and Piglets own provider identity and authentication policy.

What: Pig keeps upstream-compatible built-in OAuth login/logout behavior and
adds `pig login --list [--json]`. Before a session, Pig resolves enabled
extensions. For each source extension without a valid projection for the exact
source configuration and immutable artifact digest, Pig starts it for
registration inspection, collects runtime OAuth provider declarations, and
stops it without dispatching lifecycle or capability handlers. A valid
projection avoids startup. Factory construction during a projection miss still
executes extension code; it is not a side-effect-free metadata read. Embedded
packed members are inspected independently through the complete compiled
artifact, so one broken sibling produces its own diagnostic and does not erase
healthy targets. Without a valid provider-to-extension projection or known owner
mapping, targeted discovery may inspect unrelated members in artifact order;
each inspection remains member-isolated. After discovery identifies ownership,
login starts only the owning extension and verifies its live registration before
use. Duplicate provider IDs fail deterministically and name both owners. Embedded and fused extensions use the same registration
contract. The JSON inventory contains provider ID, display name, and
callback-server requirement. `--no-input` requires an explicit provider.

Why: raw Pig must remain product-neutral while distributions can contribute
authentication targets used by CLI and TUI flows. Upstream Pi has the provider
registry and interactive login, but no generic pre-session target inventory.

Remove when: upstream provides equivalent contributed pre-session registration
and machine-readable discovery.

Call-site markers:
- `coding/cli/auth_commands.go`: target listing and credential-store dispatch.
- `coding/cli/package_commands.go`: pre-session settings use saved/default project trust without invoking extension handlers.
- `coding/cli/auth_contributions.go`: registration-only inspection and owner-only
  login startup.
- `coding/extension/host/subprocess/host.go`: runtime provider ownership.
- `coding/extension/host/cellpack/` and fused host paths: embedded and fused
  registration.

Locked by: `coding/cli/auth_commands_test.go` runtime inspection, projection-hit,
duplicate-owner, no-handler, login-owner, and embedded tests;
`coding/cli/auth_embedded_packed_test.go`
(`TestEmbeddedPackedAuthDiscoveryAndLoginByLanguage`) for Go/Rust/Python
independent discovery, owner-only login, and provider cleanup; subprocess fused
OAuth registration tests; existing OAuth conformance and TUI external-store
coverage.
PORT_MAP path: n/a.
Ratification: explicitly approved by the user for section SHA-256 `73a5ebf45b3f9b35b2f02fe24dec8c9d843b019a7907fccd9621815ef30f8139`.
SCRUTINIZED:approved
## D41 Additive typed status and Piglet inventory

Stock disposition: product-neutral diagnostics. Stock PiG reports installed state; PiG Standard may consume the inventory but does not own its schema or collection.

What: Pig retains upstream-compatible `pig list` Package output and adds
machine-readable Package details through `pig package list --json`, typed
Package/Resource/Piglet health through `pig status --json`, and independent
Piglet inventory through `pig piglet list --json`. `pig package list --json`
preserves independently configured user and project entries. `pig status
--json` resolves the effective Package set: equivalent source spellings are
matched by canonical identity across settings scopes, and a matching project
filter entry is applied as a delta over the inherited user Package. The JSON
envelopes use one strict current unversioned shape. `--no-input` never prompts.

Why: upstream Pi has no independent Piglet entity, Piglet Binary/Image records,
or machine-readable aggregate health and provenance. Resource filtering remains
owned by the Pi-compatible `pig config` TUI. Pig adds no separate Resource
mutation namespace.

Remove when: upstream provides equivalent typed status and Piglet inventory, or
Pig removes its Piglet and Package-health additions.

Call-site markers:
- `coding/cli/package_inventory.go`: additive Package JSON inventory.
- `coding/cli/status_command.go`: typed aggregate inventory, health, and paths.
- `coding/piglet/resolve.go`: source/Piglet Binary inventory
  and record/artifact validation.

Locked by: `coding/cli/package_inventory_test.go`, `coding/cli/status_command_test.go`,
`coding/cli/configured_resources_test.go`
(`TestProjectConfigPackageOverrideIsDeltaOverInheritedGlobalPackage` and
canonical cross-scope source identity cases), and
`coding/piglet/cli_test.go` /
`coding/piglet/resolve_test.go`. Upstream Package-list behavior
is locked by `coding/cli/package_commands_test.go`
(`TestRunPackageCommandBareListMatchesUpstreamPackageList`).
PORT_MAP path: `packages/coding-agent/src/package-manager-cli.ts` for Package
list parity; additive status and Piglet record inventory have no upstream path.
Ratification: explicitly approved by the user for section SHA-256 `84552a728172209b60df6a68f7d120207cd2e29e0346ef8afb4782eac6b56485`.
SCRUTINIZED:approved
## D52 Extension terminal-geometry context (`ctx.width` / `ctx.height`)

Stock disposition: inert capability. Geometry changes no behavior until a selected extension reads it.

What: pig exposes the current terminal width and height to every extension
SDK through the runtime context, and notifies extensions when either
changes. Upstream pi has no height context: its `ctx.width` predates this
and, while width reaches the host, neither width nor height is delivered
to the SDK context in the upstream source.

Current behavior: terminal geometry travels as `width_change` /
`height_change` notifications to all connected extension processes, and
each SDK exposes live getters: Go `Context.Width()`/`Height()`, Python
`Context.width`/`_height`, Rust `Context::width()`/`height()`, Node
`RuntimeContext.width`/`height`. The ready payload carries the terminal
geometry (width with a 120 fallback for the pre-TUI window, height as 0
until the wiring layer reports it), and resize is broadcast so a widget
can re-layout. Every ready path (isolated, packed, and the deferred packed
Node ready) reads the current geometry. The host deduplicates
notifications per extension, so one extension's handshake never suppresses
a resize for the others. After a load or reload commits, the host sends
the current geometry to each new extension that saw an older value, so a
resize during `/reload` reaches every reloaded extension. A connection receives geometry in the order the host records it, so that resynchronization and a concurrent resize cannot leave an extension on the older value.
`TestConformance_Geometry` asserts all four SDKs report the height
delivered by a notification; the parity matrix records the surface as
complete (pig-additive).

Why: extension authors needed layout logic that depends on the terminal
height (widgets that change their content or line count with the pane
size). Reaching the height was impossible while the ready payload always
reported 0, so the wiring layer registers a height callback beside the
existing width callback and pushes the real height at TUI startup and on
every resize.

Skip conditions:
- if an extension only needs width, the existing behavior already covers
  it and this entry adds nothing
- the geometry surface is pig-additive; it is not a replacement for a
  value upstream provides, so it does not change parity or make an
  upstream implementation harder to port.

Call sites: `coding/extension/host/subprocess/host.go`
(`SetHeightFunc`, `readyGeometry`, `readyGeometryFor`, `syncGeometry`,
`NotifyHeight`), `coding/extension/host/subprocess/packed_go.go`
(`acceptPackedExt`), `coding/extension/host/subprocess/cell_start.go`
(`activateNode`),
`internal/codingagent/interactive.go` (width/height callback registration
and the startup kick), and the per-SDK context getters under
`extensions/sdk*`.

Observationally identical, no upstream divergence to track: the timeline
of the height notification relative to other ready-time events is the
pig-additive contract; see `docs/extension-api-parity.md` geometry row.
SCRUTINIZED:approved

Remove when: upstream delivers terminal height (or width height) to the
extension SDK context, obsoleting the pig-additive geometry surface.

Locked by: `TestConformance_Geometry`, which asserts all four SDKs report
the height delivered by a notification;
`TestPackedGoExtensionReceivesHeightThroughReload`,
`TestReloadResizeDuringHandshakeReachesEveryExtension`, and
`TestCrashRestartHandshakeDoesNotSuppressResizeForOthers` lock the ready
geometry and per-extension delivery across reload and crash restart;
`TestGeometryResyncAndResizeReachAConnectionInRecordedOrder` locks the
delivery order; the
parity matrix records the surface as complete (pig-additive).

## D60 Typed native login extension API

Stock disposition: inert capability. Stock PiG supplies validation and rendering; only a selected extension supplies identity and activates the surface. PiG Standard owns its selected login identity.

What: Pig adds typed `UIContext.SetLogin` and the direct `ui.setLogin` wire call.
The Go, Rust, Python, and Node SDKs expose the same semantic operation. Pig
validates one strict current unversioned `LoginDefinition` and renders it with
the fixed native template.

Current behavior: `SetLogin` and `SetHeader` share one header slot. The last
successful call wins. Clearing the header with `SetHeader(nil)` or the SDK clear
operation restores Stock Pig's text header. An invalid definition returns a
field error and keeps the current header. A quiet startup accepts the call but
keeps the slot hidden. The subprocess bridge applies one host call when UI
binding is available and otherwise retains only the latest pending header
operation. The TUI render path reads a local validated copy and performs no
per-render IPC. `pig extension preview-login <path>` starts exactly one
extension, emits its `session_start`, and renders the login without starting a
model session. `pig extension init <path> --login` scaffolds a neutral Go login
extension. Source and packed builds receive the running process's resolved
config root and therefore compile against its matching staged SDK. `/reload`
reloads the selected extension and its login. Product identities are ordinary
extension Resources selected by a Piglet or an explicit extension path.

Constraints: grids are exactly 41 by 5 for `brand`, 32 by 14 for `hero`, and 16
by 14 for `mascot`. Each grid uses ASCII symbols. `.` means transparent. A fully transparent `brand` omits the brand band, and the hero and mascot start at the top of the header. Each
other symbol needs one palette entry. Palette keys are one printable non-space
ASCII character and colors use `#RRGGBB`. The palette has at most 32 used
colors. Unknown or duplicate JSON fields fail. Text must be non-empty valid
UTF-8 without control or line-separator characters. Name, description, and
tagline widths are limited to 24, 48, and 76 columns. Name plus description and
spacing are limited to 80 columns.

Why: subprocess extensions cannot pass a live TUI component factory. The typed
definition gives all SDKs one validated identity format while Pig owns layout,
terminal capability handling, and operational status.

Remove when: upstream Pi provides an equivalent typed native login contract, or
Pig removes extension-defined login identity.

Call-site markers:
- `coding/extension/login.go`
- `coding/extension/ui.go`
- `coding/extension/host/subprocess/protocol.go`
- `coding/extension/host/subprocess/ui_bridge.go`
- `coding/extension/host/subprocess/runtime-node/runtime.mjs`
- `extensions/sdk/login.go`
- `extensions/sdk-rs/src/login.rs`
- `extensions/sdk-py/pig_sdk/__init__.py`
- `coding/cli/extension_login_preview_command.go`

Tests:
- `coding/extension/login_test.go`
- `internal/codingagent/login_header_test.go`
- `coding/extension/host/subprocess/ui_bridge_test.go`
- `test/extension-conformance/conformance_test.go`
- `coding/extension/host/subprocess/runtime_node_login_test.go`
- `coding/cli/extension_login_preview_command_test.go`

PORT_MAP path: n/a, because this is a downstream extension API.
SCRUTINIZED:approved

## D67 Windows container builds use the image default user

Stock disposition: required substrate. Native identity mapping belongs to the generic Piglet builder, not a selected product.

What: Linux and macOS container builds pass `--user <uid>:<gid>` for the invoking user. Windows has SIDs rather than numeric host uid/gid values, so its Docker or Podman invocation omits `--user` and uses the Linux builder image's default user. The VM maps bind-mounted output to the Windows host user. The builder does not attempt a Windows numeric account lookup.

Why: this is platform policy for Piglet builds, which have no Pi counterpart. The owner selected the image-default-user policy on 2026-09-25 (WIN-NOTES Q5). Reclassification changes no command, permission policy or test.

Call-site marker: `coding/pigletbuild/container_builder.go`, `containerUserArgsFor`.

Locked by: `coding/pigletbuild` `TestContainerUserArgsOnWindowsLookUpNoIdentity`, `TestContainerUserArgsPassTheInvokingUser`, and `TestContainerBuilderBuildProducesHostArtifactAndContainerIdentity`. These retain the Windows no-lookup requirement and Unix invoking-user requirement.

Remove when: PiG maps a Windows identity into the build container, or container builds stop bind-mounting host output.

SCRUTINIZED:approved

## D69 Windows Piglet scripts are cmd.exe launchers

Stock disposition: required substrate. The generic builder emits a launcher for the selected host platform without activating product behavior.

What: `pig piglet build --format script` emits a POSIX launcher on Unix and a two-line CRLF cmd.exe launcher on Windows: `@setlocal DisableDelayedExpansion`, then `@pig --piglet "<source>" %*`. Double quoting and doubled percent signs preserve spaces, ampersands, carets, apostrophes and percent signs; disabling delayed expansion preserves exclamation marks. Windows output uses the requested `--out` name, which the caller names `.cmd`; `--out -` prints the same bytes. Unix retains mode 0755 and `exec pig --piglet '<source>' "$@"`.

Why: Pi has no Piglet launchers. This is an additive platform format, not a different implementation of Pi behavior. The owner selected native cmd.exe launchers on 2026-09-25 (WIN-NOTES Q7). Reclassification changes no launcher bytes or quoting tests.

Call-site marker: `coding/pigletbuild/main.go`, `sourceScript`.

Locked by: `coding/pigletbuild` `TestSourceScriptPerPlatform`, `TestAC2AC7SourceScriptFileAndStdoutCreateNoPigState`, and `TestWindowsScriptKeepsBangsUnderDelayedExpansion`. These retain exact platform bytes and actual argument/quoting execution checks.

Remove when: never, unless Windows runs POSIX shell scripts natively.

SCRUTINIZED:approved

## D81 Same-manager helper download coalescing

Stock disposition: required substrate. Coordination protects the generic helper installer from competing writes without selecting product behavior.

What: Pi starts one download per overlapping caller. PiG shares one in-flight download for concurrent requests for the same missing helper within a `ToolsManager`. The visible improvement is coalesced download reporting, with a single `Downloading...` status and one archive network fetch for the shared flight. The installed tool path and binary contents remain unchanged. Different helpers, managers and processes do not share a flight.

A caller rechecks the installed path while claiming a new flight, so completion between its initial lookup and its claim does not trigger another download. Only the owner reports download status. Completion removes the flight on success or failure.

Pi source: `packages/coding-agent/src/utils/tools-manager.ts:353-356` returns an installed path without reporting status. Lines 377-382 start a download for every missing-tool call without a shared Promise or in-flight map. A direct probe of the installed `dist/utils/tools-manager.js` (byte-identical in upstream 0.87.1 and 0.99.1, as is `src/utils/tools-manager.ts`), with two calls held at an asset-response barrier, produces two version lookups, two asset requests and two status sequences.

Why: the owner classifies coalescing identical concurrent downloads as an additive robustness improvement. Shared work prevents competing writes to the same archive and binary. This adds no command, setting or product workflow.

Call-site marker: `internal/codingagent/tools/tools_manager.go`: `EnsureTool`.

Locked by: `TestDownloadTool_LateCallerReusesFinishedDownload` in `internal/codingagent/tools/tools_manager_test.go` pauses a caller after its initial miss, lets another caller install and clear its flight, then releases the late caller. The fix and test match public commits `80feeefa8` and `178798664`; the production comment additionally cites D81. Removing only the locked recheck compiles and deterministically fails with `expected 1 download, got 2`. Both this test and `TestDownloadTool_ConcurrentDedup` pass 500 race-detector runs normally and under two-CPU stress. `test/parity/scenarios/tools/12-managed-tool-status.toml` compares the shared installed-tool reuse and status contract against pinned Pi; it does not claim Pi coalescing parity.

Remove when: Pi implements equivalent same-tool download sharing, or PiG no longer coalesces helper downloads.

Approval: the owner explicitly classifies this capability as additive robustness in the fix-download-dedup lane.
SCRUTINIZED:approved


---

## D91 Piglet frontend members draw the interactive mode

Stock disposition: required substrate. A Piglet cannot supply the seam that lets its own frontend member take the interactive screen; the seam selects nothing on its own.

What: a Piglet Binary can fuse one Go frontend member (`slots.frontend.member`, a directory of a Go module whose root package exports `func Frontend() frontend.Frontend` from `github.com/MichaelKinsy/PiG/extensions/sdk/frontend`). A Piglet that extends another inherits the member, which resolves against the directory of the Piglet file that names it; the child replaces it by naming its own `slots.frontend` or removes it with `extends.remove.slots: [frontend]`, and removing an absent member is an error. `frontend` is the only slot. The member's `Open` runs once per interactive run, after raw mode and before the first paint. A member that returns a session draws the run: PiG keeps building Pi's component tree, and the `tui.TuiSurface` renderer reports each frame as retained-tree ops keyed by stable component ids, transcript entries in the main region and the input dock in the dock region. Tool cards arrive as semantic `ToolCard` nodes (name, decoded arguments, plain call header, status, output, a registered definition's result rendering, and for a finished call whose result details carry a unified `patch`, as Pi's edit details do, that patch as a `Diff`); a call that ended because the user aborted its turn reports status `cancelled` instead of `error`, while its card still draws the abort as Pi does. User and assistant messages arrive as `MarkdownText` nodes (role, Markdown source, and whether the text is still streaming, which the surface learns from interactive mode through `tui.SurfaceHooks` like the diff), and an assistant's thinking runs as `Thinking` nodes (source, whether the user hid thinking, streaming); an assistant message is a run of nodes, one per text block, thinking run and error line, under ids that extend the message's id, so a streamed block keeps its node while its source grows. Every other component arrives as the ANSI lines it rendered, at the region width the session reports. Terminal input that the session claims, during startup and in the input loop, never reaches key handling. A session that asks for a fallback, or fails to draw a frame, is closed and the run continues with the configured ANSI renderer, repainting the whole tree with a warning. While a session draws, a TUI mode switch is refused. Teardown closes the session before input pauses, so the drain discards its in-flight answers. The member is not part of the extension API: it has no wire protocol and no subprocess realization, and only the native builder fuses it.

The input editor arrives as an `Editor` node (text, cursor in UTF-16 code units, placeholder, and whether it is sendable). The dock around it is, in order: a `Lines` node for the components above the editor, a `Working` node while a status indicator shows, the editor, a `Lines` node for the completion list and the components below, and a `Footer` node while PiG's footer shows. PiG draws neither of the editor's ANSI borders there, since the frontend frames the editor and shows its whole text; the borders stay wherever the editor is drawn as lines. `Working` carries the indicator's kind (`working`, `compaction`, `branchSummary` or `retry`), its message as PiG draws it (the retry countdown included), an extension's spinner frames from `setWorkingIndicator` (nil for the default Braille spinner, empty for none) and the frame interval, but not the current frame: spinner ticks send nothing, and the frontend animates the spinner. `Footer` carries the data of Pi's `FooterComponent` (`footer.ts`) through the helpers `renderFooter` draws with: the cwd with home abbreviated, the git branch, the session name, the session's usage totals (token counts and cost in US dollars), the latest cache-hit rate in percent, subscription billing, the context usage (tokens, percent from 0 to 100, unknown after a compaction, and the window in tokens), auto-compaction, experimental features, the model id, the provider only while more than one is available, the thinking level only for a model that reasons, the routed model of a virtual selection, and the extension statuses in key order. A footer an extension sets replaces the `Footer` node and draws as lines below the editor; pending messages, widgets and every other unmodeled component stay `Lines`. While a selector replaces the editor, an overlay draws, or an extension's editor component stands in, the dock is one `Lines` node as before. The editor is sendable once the input loop runs and while the editor has the keys, with no selector, overlay, extension dialog or external editor holding them and no exit requested; streaming and compaction keep it sendable because Pi accepts prompts then. A session acts on the editor through `Env.Edit` (replace a UTF-16 range and set the cursor, one undo step; ignored when its `Len` is not the editor's current text length, so an edit computed before keys in flight is dropped), `Env.Undo` (the editor's own undo history) and `Env.Send` (submit text through the path Enter takes, so slash commands, multi-line prompts, steering while the agent works and queueing during compaction behave as typed; the editor's draft stays unless that path itself sets the editor). They are safe from any goroutine and never block: PiG runs them on its owner loop in call order through the queue the extension editor bridge uses, an action taken while the session handles input runs before the next key, and none runs after the session closes.

A selector or dialog that takes the editor's place arrives in the editor's place as a `Selector` node (title, description, searchable query, the shown items in key order with id, label, detail, checked and tree depth, the selected id, tabs and the active tab, status, loading, and a confirm question) or, for a settings list, a `Settings` node (query, the shown settings with id, label, description, value, the values the confirm key cycles and whether it opens a submenu, and the selected id); its id `selector.<n>` is new for each selector, and a settings list keeps its id while it reports an open submenu as a `Selector`. Producers implement `tui.NativeComponent`: `FilterableList`, `SelectSubmenuComponent`, `ExtensionSelectorComponent` (extension select and confirm, login methods, built-in confirmations), `SettingsList`, `ModelSelector`, `ScopedModelsList`, `UserMessageSelector`, `OAuthSelector`, `TreeSelect`, the session selector, the thinking selector, the trust selector, the automatic theme menu, `SteppedSubmenu` and the theme submenu; the session selector's rename field and the tree's label editor stay lines, as do extension input and editor dialogs, the login dialog, custom components and loaders. The frame borders around a native component are not drawn. Overlays arrive in a third region, `overlay`, as `Overlay` nodes (the component's ANSI lines at Pi's overlay width, the width in cells, Pi's anchor and whether the overlay leaves the keys); while one that takes the keys shows, the dock is one `Lines` node. `Env.Act` performs an `Action` (`highlight`, `choose`, `dismiss`, `filter`, `tab`, `set`) on a selector node as the keys it stands for: a background task computes each step's keys on the owner loop against the node as it is then and routes them through the terminal input path (`routeInputSequence`) to whatever has the keys, so selectors keep Pi's results, side effects and previews, and an action stops once its node is gone. Keys typed in the terminal keep reaching the selector. Built-in modal selector loops now offer each input sequence to the frontend session first (`consumeModalHostInput`), so a session's replies and events no longer reach the selector as keys. `tui.SettingItem.Opens` marks the Theme and Warnings rows, which open selectors as Pi's submenus do.

A frame also carries `Frame.SessionFile`, the file the session persists to as Pi's `sessionManager.getSessionFile` reports it, or `""` for a session that is not persisted (`--no-session`). The first frame carries it, and a later frame only after the file changed, as `/new`, `/resume`, `/fork`, `/clone` or an extension's session replacement change it; a frame may then carry it and no ops. `TuiSurface` reads it through `SurfaceHooks.SessionFile` on each frame and compares it with the last file it reported, so a frontend learns of a switch without polling. A frontend can then name the session file to its host, for example so a terminal's assistant can read the conversation.

PiG lends the terminal to another program without ending the session: the external editor (Ctrl+G, an extension's `ctx.ui.editor` and the session selectors' external edit, all through `openExternalEditorBuffer`) and a job-control stop (Ctrl+Z). `Session.Suspend` arrives while the terminal is still raw and PiG still reads it, before the other program starts; until `Session.Resume`, PiG calls only `Close`. `Session.Resume` arrives once PiG has the terminal back in raw mode. `TuiSurface.Suspend` stops drawing but keeps the retained tree and the components' invalidations, so the frame after `TuiSurface.Resume` carries only what changed meanwhile, such as the edited text as one update of the editor node, and no frame when nothing changed. `Stop` and `Start` still replace every node. The ANSI renderers keep Pi's stop, start and full repaint.

A frame also carries `Frame.Theme`, the palette of the theme PiG draws with: its name, whether it is designed for a dark background, and each color token it sets as `#rrggbb` under Pi's token names, with the export colors `pageBg`, `cardBg` and `infoBg`. A token PiG draws faint is mixed toward the terminal's background as `Theme.ColorValues` resolves it, and a token at the terminal's default color or at one of its sixteen ANSI colors is absent, since it follows the terminal's own palette; a token at a color of the 256-color cube or ramp keeps its xterm value. The first frame carries it, and a later frame only when the palette changed, as a theme switch in `/settings`, a `/theme` preview, an extension's `setTheme` or terminal colors that change the theme do; a frame may then carry it and no ops. `TuiSurface` rebuilds the palette only after the active theme or the terminal's colors changed, so other frames do not pay for it.

PiG reads the terminal's colors and its light/dark appearance under a frontend session exactly as it does under its ANSI renderer, through the same theme controller: `TuiSurface` paints nothing on the terminal, but `TuiSurface.QueryTerminalColors` writes the terminal color query (OSC 10, OSC 11, OSC 4 for ANSI colors 0-15, then DA1) to the terminal the session draws on (`TuiSurface.SetTerminalOut`, which `openFrontend` sets to `Env.Out`), and the replies reach `ConsumeTerminalColorResponse` through the usual input path, as do the CSI ? 997 light/dark reports that CSI ? 2031 h asks for. So a light/dark theme pair and the system theme pick the variant the terminal's background calls for, and follow the terminal's appearance switches, each switch reaching the session as a frame with the new `Frame.Theme`. A session reports those replies and reports as not its own (`Session.HandleInput`). `Env.Out` is a `frontendTerminal` that takes one Write at a time: the session's, from any of its goroutines, and PiG's own, the query and, through `themeOutput` while the session draws, CSI ? 2031 h and l. A write larger than the terminal's output buffer can otherwise take turns with another one in the kernel, and a query inside a frame would break both. Before this, the query went to the surface's discarded output, so PiG assumed a dark terminal until the first appearance report, generated the system theme without the terminal's colors, and waited out the color query's 100 ms timeout before the first frame.

The frontend's Terminal receives Pi's program status: `TuiSurface.Terminal` forwards every OSC 7501 report, clear included and with no support query, to a session that implements `frontend.ProgramStatusSession` (clearing it before `Suspend` and `Close`, reporting it again after `Resume`), and `tui.SetProgramStatusElsewhere` keeps the process terminal from writing the query or a report to stdout while a session may draw. When the frontend declines the run, fails to open, or its session falls back, the process terminal sends the query it held back and the reporter sends it the current status. `TuiSurface.Terminal` takes no lock, because the editor asks for it while the surface renders (locked by `TestSurfaceTerminalReportsProgramStatusToTheSession`, `TestSurfaceTerminalWritesNoProgramStatusToTheProcessTerminal`, `TestProcessTerminalQueriesProgramStatusOnceNoSessionShowsIt`, `TestSurfaceRendersAnEditorBeforeAnythingAsksForItsTerminal`, `TestFrontendSessionShowsTheProgramStatus`, `TestProcessTerminalShowsTheProgramStatusOnceNoSessionDraws` and `TestFrontendFallbackHandsTheProgramStatusBack`).

A frame also carries `Frame.Mark`, the application's mark for a frontend that shows a brand of its own: PiG's is the pig head of the active sprite (`piglogin.Active`), the 16-by-14 pixels the startup header draws, as a `Mark` of its width, its height and its opaque pixels in row-major order (`MarkPixel`: column, row and `#rrggbb`). Each color is the one the header's half block takes in the active theme's color mode: the sprite's own color in truecolor, and in 256-color mode the xterm color of the palette index `ForegroundAnsi` selects. The first frame carries it, and a later frame only when it changed, as `/sprite` choosing another sprite, an extension registering or unregistering the sprite PiG shows, or a switch to a theme in another color mode do; a frame may then carry it and no ops. A zero `Mark` is no mark. `TuiSurface` reads it through `SurfaceHooks.Mark` on each frame and compares it with the last mark it reported; the interactive mode keeps the mark it built with the sprite and the color mode it was built from (`frontendMarkCache`) and rebuilds it only after `piglogin.SameHead` or the color mode says the head changed, so an unchanged frame allocates nothing for it.

Pi source: Pi 1.1.0 paints the interactive mode only through `TuiMainScreen` and `TuiAltScreen`, which `createInteractiveTui` (`packages/coding-agent/src/modes/interactive/tui-renderer.ts`) constructs. It has no Piglet and no frontend member.

Why: the owner approved a renderer seam for Tern-native rendering through Piglets (issue #92; Piglet slots design, D91), with the first slice limited to the seam and tool cards (2026-10-04). The P0 spike showed that D89 tool renderers alone cannot place native cards: a rendered line cannot leave the alternate screen, address a card after another surface opens, or account for the grid rows a surface anchors.

Stock behavior: Stock PiG compiles `internal/frontendpack` with no member, so `InteractiveModeOptions.Frontend` is nil and every frontend path returns before it acts. Stock output is unchanged.

Call-site markers: `extensions/sdk/frontend/frontend.go` (contract), `tui/tui_surface.go` (`TuiSurface`, `SurfaceHooks`, `appendFrontendNodes`, `dockEntries`, `overlayEntries`, `Editor.frontendParts`, `nodesEqual`, `Editor.ApplyEdit`, `Editor.Undo`), `tui/tui_surface_native.go` (`NativeComponent`, `NativeAction`, `TuiSurface.NativeKeys`, the shared producers), the `NativeNode` methods in `tui/model_select.go`, `tui/scoped_models_list.go`, `tui/user_message_selector.go`, `tui/oauth_selector.go`, `tui/tree_select.go` and `internal/codingagent/native_selectors.go`, `tui/select_submenu.go` (`SelectSubmenuComponent.description`), `internal/codingagent/interactive_theme_modal.go` (`consumeModalHostInput`), `tui/tool_execution.go` (`MarkAborted`, `FinalizeAborted`, `SetResult`), `internal/codingagent/interactive_frontend.go` (`openFrontend`, `frontendSurfaceHooks`, `frontendWorking`, `FooterComponent.frontendFooter`, `footerNode`, `frontendEditorSendable`, `postFrontendAction`, `postFrontendAct`, `feedFrontendActs`, `applyFrontendEdit`, `sendFrontendPrompt`, `toolResultDiff`, `leaveFrontend`, `frontendInputReady`, `frontendInput`, `closeFrontend`), `internal/codingagent/interactive_events.go` (message end abort), `internal/codingagent/interactive_transcript.go` (aborted call rebuild), `internal/codingagent/interactive_input.go` (`dispatchInputChunk`, `submit`), `internal/codingagent/startup_input.go` (`handleStartupInput`), `internal/codingagent/interactive_tui.go` (`mountInteractiveTui`, `switchTuiMode`, `stopInteractiveTui`), `internal/codingagent/interactive.go` (`InteractiveModeOptions.Frontend`), `coding/cli/main.go`, `internal/frontendpack/frontendpack.go`, `coding/piglet/types.go` (`Slots`, `SlotMember`, `RemoveSpec.Slots`, `validateSlots`), `coding/piglet/extends.go` (`mergePiglets`, `applyRemovals`, `anchorPigletLocalPaths`, `clonePiglet`), `coding/piglet/origins.go` (`HasFrontend`, `FrontendDir`), `coding/piglet/main.go` (`cmdShow`), `coding/piglet/piglet.schema.json` (`slots`, `removeSpec.slots`), `coding/pigletbuild/native_build.go` (`overlayFuse`, `resolveFrontendMember`), `coding/pigletbuild/records.go` (frontend build input), `coding/pigletbuild/main.go` (`bakePiglet`), `coding/pigletbuild/container_builder.go`.

Handoff and theme call-site markers: `tui/tui_surface.go` (`TuiSurface.Suspend`, `TuiSurface.Resume`, `framePalette`, `surfacePalette`, `TuiSurface.SetTerminalOut`, `TuiSurface.TerminalOut`, `TuiSurface.QueryTerminalColors`), `tui/terminal_color_query.go` (`queryTerminalColors`, the query written to a given writer), `internal/codingagent/interactive_frontend.go` (`openFrontend`, `frontendTerminal`), `internal/codingagent/interactive_theme.go` (`themeOutput`), `internal/codingagent/interactive_editor.go` (`openExternalEditorBuffer`) and `internal/codingagent/interactive_signals_unix.go` (`suspendOperations`).

Mark call-site markers: `extensions/sdk/frontend/frontend.go` (`Frame.Mark`, `Mark`, `MarkPixel`), `tui/tui_surface.go` (`SurfaceHooks.Mark`, `frameMark`), `internal/codingagent/interactive_frontend.go` (`frontendMarkCache`, `headMark`, `markPixelColor`) and `coding/piglogin/head.go` (`SameHead`).

Locked by: `TestDiffSurfaceRegionReplaysToNext` (2000 random region changes replay to the next tree), `TestTuiSurfaceParallelToolsUpdateTheirOwnNodes`, `TestTuiSurfaceToolStatusFollowsTheCardLifecycle`, `TestTuiSurfaceReportsAnAbortedCallAsCancelled` (the card's terminal lines are unchanged), `TestTuiSurfaceSendsMessagesAsMarkdown` (a streamed delta is one update of the block's own node), `TestTuiSurfaceSendsThinkingRuns` (hidden runs still draw Pi's label), `TestTuiSurfaceSendsAToolResultsDiff` (only a finished call), `TestTuiSurfaceReportsTheInputEditorAsAnEditorNode` (UTF-16 cursor; a sendable flip is one editor update), `TestTuiSurfaceReportsTheDockAsLinesWithoutAMountedEditor` (selector and overlay), `TestTuiSurfaceDropsTheEditorBordersAroundANativeEditor` (no border or scroll count around a native editor; the borders under an overlay), `TestTuiSurfaceReportsTheWorkingIndicatorAboveTheEditor` (one insert, update and remove; spinner ticks send nothing; a mounted indicator leaves the lines only while the hook reports it), `TestTuiSurfaceReportsTheFooterAsTheLastDockNode` (each data change is one footer update; an extension's footer replaces it), `TestNodesEqualComparesWorkingAndFooterByValue`, `TestEditorApplyEditIsOneUndoStep`, `TestEditorApplyEditNormalizesAndClamps`, `TestTuiSurfaceRendersRegionsAtSessionWidths`, `TestTuiSurfaceSendsADefinitionsResultWithoutItsCall` and the other `TestTuiSurface*` tests in `tui/tui_surface_test.go`, each new node test also checking that the replayed tree equals a fresh surface's first frame; `TestNodeKindsAreClosed` in `extensions/sdk/frontend`; `TestFrontendSessionDrawsTheRunInsteadOfTheTerminal`, `TestFrontendMarksOnlyTheGeneratingMessageAsStreaming`, `TestToolResultDiffReadsTheEditPatch` (live and reloaded edit details), `TestFrontendThatDeclinesLeavesTheANSIRenderer`, `TestFrontendFallbackRepaintsWithTheConfiguredRenderer` (regular and fullscreen), `TestFrontendInputNeverReachesTheEditor` (startup and loop input), `TestFrontendKeepsTheScreenAgainstATuiModeSwitch`, `TestFrontendSessionClosesOnceAtTeardown`, `TestFrontendEditorActionsFromInputRunBeforeTheNextKey` (edit, key and undo order), `TestFrontendEditIgnoresAStaleLength`, `TestFrontendSendSubmitsLikeEnter` (slash command, multi-line prompt, compaction queue, draft kept), `TestFrontendSendWhileWorkingSteers`, `TestFrontendEditorSendableTransitions` (an extension dialog, which takes the keys without the focus, included) and `TestFrontendEditorActionsAfterCloseDoNothing` in `internal/codingagent/interactive_frontend_test.go`; `TestFooterNodeCarriesThePiFooterData` (each field against `renderFooter`'s inputs), `TestFrontendDockReportsTheWorkingIndicator` (agent start and end, working message, extension frames, compaction) and `TestFrontendDockReportsTheFooterUntilAnExtensionReplacesIt` in `internal/codingagent/interactive_frontend_dock_test.go`; the abort marks in `TestInteractiveMode_AbortPushesErrorIntoPendingTools`, `TestInteractiveMode_FinalizeRunningTools_FreezesElapsed` and `TestResumeAbortedToolIgnoresOrphanedResult`; `TestOverlayFuseRegistersFrontendMember` (the generated registry compiles), `TestValidateRejectsUnresolvedAndEmptyPiglets`, `TestFrontendDirResolvesInsideThePigletDirectory`, `TestFrontendSlotInheritsReplacesAndRemoves` (inherit across directories and through a middle Piglet, replace, remove, remove then name, remove an absent member, clone independence), `TestParseSlotsRejectsInvalidMembers`, the slot rows of `TestAC1SchemaParserAgreement` and the `slots` check in `TestBakePigletInlinesAgentShapeAndStripsBuildOrigins`. Mutation checks: dropping the moved-entry removal in `diffSurfaceRegion`, mapping started cards to pending, and removing the startup input route each fail their test; so do dropping the action drain after `HandleInput`, the `Len` guard, each sendable condition, the closed-session guard, the undo snapshot in `ApplyEdit`, and the overlay and selector fallbacks; so do keeping the bottom border, dropping the working node, skipping a mounted indicator the hook does not report or drawing one it does, comparing working or footer nodes without the message, the extension statuses or the cache-hit rate, naming the provider with one provider, reporting the default spinner's frames, ignoring an extension's footer, and leaving a reasoning model's thinking level unset; so do skipping the child's slot in `mergePiglets`, skipping the anchor, ignoring `remove.slots`, accepting the removal of an absent member, a shallow slot clone, accepting an unknown removed slot, and keeping `slots` in the baked Piglet.

The selector and overlay slice is locked by the `TestNative*` tests in `tui/tui_surface_native_test.go` (a selector in the editor's place, one update per selection move, a new id per selector; frame borders dropped; filter, submenu and settings nodes; Highlight never wraps from the first or last item and does nothing for the current item, an unknown item, a singleton list or an empty filter result; Choose, Dismiss and stale ids; Filter types and erases a key at a time; SetValue cycles through the host loop and never for a row that opens a selector; SwitchTab; overlays in their region and a capturing overlay stopping actions; resend; node equality), `tui/native_selectors_test.go` and `internal/codingagent/native_selectors_test.go` (each producer's node, its key transitions and a `NativeKeys` round trip), and `internal/codingagent/interactive_frontend_selectors_test.go` (an extension select answered by `Act` choose and dismiss and by keys; a session's input during a modal settings loop not reaching the list, and `Act` set and dismiss there).

The session file slice is locked by `TestTuiSurfaceReportsTheSessionFileWhenItChanges` (first frame, no repeat, a frame with only the file, `""`) and `TestFrontendFramesReportTheSessionFileAcrossSwitches` (`/clone`, `/new`, `/resume` and `/fork` each report the new file, a leaf move reports nothing, an in-memory session reports `""`). Reporting the file in every frame, reporting it only in frames with ops, and reading the session captured at open each fail one of them.

The handoff and theme slice is locked by `TestTuiSurfaceSuspendHoldsFramesAndResumeSendsOnlyTheChanges` (no frame while suspended; Suspend, Resume, then one frame of two updates), `TestTuiSurfaceResumeWithoutChangesSendsNoFrame`, `TestTuiSurfaceStopAndStartStillResendAndSuspendWaitsForStart`, `TestTuiSurfaceFramesCarryTheThemeWhenItChanges` (the tokens of `theme_dark.json` with spot-checked values, no repeat, a theme switch as a frame without ops, switching back, no frame for terminal colors that leave the palette), `TestTuiSurfaceThemePaletteMixesFaintTokensAndOmitsDefaultOnes`, `TestTuiSurfaceThemePaletteOmitsTheTerminalsAnsiColors` (the system theme generated without the terminal's colors sends no color; index 123 keeps `#87ffff`), `TestFrontendFramesCarryTheThemeAcrossSwitches` (an extension's `setTheme` and a preview each reach the session as one frame) and `TestFrontendSessionLendsTheTerminal` (in a pty child, Ctrl+G and Ctrl+Z: Suspend while raw and reading, Resume once raw again after the editor ran, then one editor update). Resending every node on Resume, skipping `Session.Resume`, drawing while suspended, suspending before Start, resending an equal palette, dropping a frame that carries only the theme, sending unmixed faint colors, dropping the export colors, sending or dropping every indexed color, pausing input before Suspend, forcing a full render after the continuation, and the editor's former stop, start and repaint each fail one of them.

The appearance slice is locked by `TestTuiSurfaceQueriesTheTerminalColorsOnItsTerminal` (a frame and `HideCursor` write nothing to the terminal; the query is written there byte for byte and Tern's light replies complete it), `TestFrontendThemeFollowsTheTerminalAppearance` (for `light/dark` and the system theme: the exact terminal wire of CSI ? 2031 h and each query, Tern 0.4.5's light and dark replies and reports picking `light`/`dark` or the light and dark system theme in the next frame, nothing reaching the editor or the session) and `TestFrontendTerminalWritesTakeTurns` (while a session's Write is in progress, PiG's query and CSI ? 2031 h wait for it). Leaving the surface's terminal unset in `openFrontend` fails the second, writing the query to the surface's paint output fails the first two, painting on the terminal fails the first, and dropping the `frontendTerminal` lock or writing CSI ? 2031 h past it fails the third.

The mark slice is locked by `TestTuiSurfaceReportsTheMarkWhenItChanges` (first frame, no repeat, a frame with only the mark, a change of size alone, the zero mark, each frame's own copy, no mark without the hook), `TestFrontendFramesCarryTheActiveSpritesMark` (the first frame's mark is the default sprite's head as the header's truecolor half blocks draw it; an edit, a second registration of the same sprite and a light theme carry none; an extension registering the saved sprite carries its head, unregistering it the default's, `/sprite` another sprite's, and a dark theme in 256-color mode the head in the xterm colors the header's 256-color half blocks select), `TestAnsiRunBuildsNoMark` (a run without a frontend builds no mark) and `TestSameHeadMatchesHeadPixels` (a sprite and its copy, or its copy with another wordmark, are the same head; no two heads whose pixels differ are, for every pair of sprites and for copies with one input of the head changed). Sending the mark in every frame, never again after the first, without a frame of its own or sharing the hook's pixels; caching it across a sprite or color-mode change; drawing it in truecolor or through `RGBTo256` in 256-color mode; transposing the grid; building it for an ANSI run; and `SameHead` ignoring any input of the head, or comparing the wordmark, each fail one of them.

The selector and overlay slice (the `Selector`, `Settings` and `Overlay` nodes, the overlay region and `Env.Act`) was assigned by the owner, Michael Kinsy, on 2026-10-05 for PiG 0.5.0.

Frame cost: a `TuiSurface` frame costs what changed, not the length of the transcript (owner direction, 2026-10-05). A component that embeds `invalidatable` or `BaseComponent` reports each `Invalidate` to the surface that draws it, and a `Container` reports each change of its children with the children it kept at the start and the end. The surface rebuilds only the reported components, walks only a container's changed children, and places them in the main region by each container's cumulative node counts. Every frame it also rebuilds the components whose changes do not all invalidate them, the ones the main-screen renderer renders every frame: a component without `invalidatable`, a `Box`, a component another surface draws, a running `BashExecutionComponent`, an `Editor` that draws an embedded status, a `Markdown` with a transform, a custom message or custom entry whose renderer component changes on its own, an open session or settings selector, and a tool card whose definition's result component is one of these. It walks the whole document when the width, the theme or the hooks change, on a resend, when two containers change in one frame, and while a component is keyed by its position. Each frame sends the ops of the whole region's diff from the last frame's nodes. `SurfaceHooks.Streaming` returns the message that streams, so a message that stops streaming is rebuilt without a walk. The frontend contract is unchanged. Stock PiG adds one atomic load to `Invalidate` and records the kept children of a changed `Container`; its output is unchanged.

Frame cost markers: `tui/tui_surface_main.go` (`surfaceMain`, `updateMain`, `applyChanges`, `walkMain`), `tui/tui.go` (`invalidatable.notifySurface`, `Container.markChildrenLocked`, `Container.takeChildrenLocked`), and `SurfaceLive` (reported by every component whose `IsDirty` covers changes that do not invalidate it) in `tui/editor_status.go`, `tui/bash_execution.go`, `tui/markdown.go`, `tui/custom_message.go`, `internal/codingagent/custom_entry_component.go`, `internal/codingagent/session_selector.go` and `internal/codingagent/settings_selector.go`. Locked by `TestTuiSurfaceStreamingFrameWorkDoesNotGrowWithTheTranscript` and `TestTuiSurfaceMessageFrameWorkDoesNotGrowWithTheTranscript` (a token, a new message part, and a message mounted or unmounted under 10 and 1000 messages rebuild and walk only what changed), `TestTuiSurfaceRandomEditsReplayToAFreshRender` (random node, structure, streaming and width changes, with components keyed by position or not, send the whole-region diff's ops, keep ids, keep the retained index exact and replay to a fresh render), `TestTuiSurfaceSplicesAnInsertBefore` (an insert before a child, alone or with a later removal or replacement in the same frame), `TestEveryContainerChildMutatorMarksTheKeptChildren`, `TestDiffSurfaceMainMatchesTheWholeRegionDiff`, `TestTuiSurfaceRebuildsAMessageThatStopsStreaming`, `TestTuiSurfacePollsComponentsThatChangeWithoutInvalidating`, `TestTuiSurfaceStopReleasesTheComponents` and `TestSurfacePollsEveryIsDirtyOverride` in `tui/tui_surface_incremental_test.go`, and measured by `BenchmarkStreamingFrameLongTranscript`, `BenchmarkDockFrameLongTranscript` and `BenchmarkMessageFrameLongTranscript` in `tui/tui_surface_bench_test.go`.

The session file slice (`Frame.SessionFile`, `SurfaceHooks.SessionFile`) was added on 2026-10-06 under the owner's direction of 2026-10-05 to integrate PiG with Tern, so that Tern's assistant can read a PiG agent's conversation from its session file.

The handoff and theme slice (`Session.Suspend`, `Session.Resume`, `Frame.Theme`, `TuiSurface.Suspend`, `TuiSurface.Resume`) was added on 2026-10-06 under the owner's direction to integrate PiG with Tern: an external program's terminal handoff and the theme's palette.

Clicks and fullscreen overlays: a frontend that cannot report where the pointer is clicks the part of a node that takes clicks. A component implementing `tui.Clickable` reports that part of its lines as `Lines.Click` (an `Area` of rows and cells); the built-in header reports its logo (the pig head, or the text mark where it stands in for Pi's logo). `Env.Click` hands a `Click` on a node's area to the component that drew it, on the owner loop, as a terminal click at that cell, with the screen cell it shows at when the session shows a screen; a click outside the area, on a node that no longer shows, or after Close does nothing. A session implementing `frontend.ScreenSession` can cover its view with a fullscreen overlay: `Screen` reports the screen's size (zero or less counts from the terminal's), where main's first line and the dock's lines show on it, whether the session shows the dock itself (`ShowsDock`), whether the view is hidden and whether the user asked for reduced motion. PiG then plays the animations its fullscreen mode plays over the alternate screen (the logo easter egg, and the 3D pig of `/arminsayshi` and `/pigsayhi`, D87) on that screen: the animation engine is unchanged, it opens as an overlay with `OverlayOptions.Fullscreen`, which `TuiSurface` reports as `Overlay{Fullscreen: true}` at the screen's size with a `Click` area covering it, so a click closes it as in fullscreen mode, and keys reach it as before. The dissolve starts from the screen as the session shows it (`TuiSurface.ScreenLines`): the document's end, or from the clicked component when the end would leave it out of view, at main's cell, over the dock's lines unless the session shows the dock itself. A session that shows the dock itself keeps the dock's editor and footer nodes under a fullscreen overlay (the editor reports Sendable false, since the overlay takes the keys), so under Tern the dissolve and the pig play above Tern's own editor and status strip, which is what Pi's dissolve starts from: what the screen shows. The animation's clock stands still while the view is hidden and it requests no frame then; under reduced motion, or for a session that shows no screen, the eggs do what they do outside fullscreen mode (the inline pig head; the logo click does nothing). The input loop paints nothing for a sequence a frontend session took whole, such as a protocol acknowledgement: painting after each one rendered once per acknowledged frame, so an animation ran at the frontend's frame rate (60 to 77 frames a second in Tern) instead of its own 30.

Click and screen call-site markers: `extensions/sdk/frontend/frontend.go` (`Lines.Click`, `Area`, `Env.Click`, `Click`, `Overlay.Fullscreen`, `Overlay.Click`, `ScreenSession`, `Screen`), `tui/tui_surface_screen.go` (`Clickable`, `TuiSurface.Screen`, `TuiSurface.ScreenLines`, `TuiSurface.Click`), `tui/tui_surface.go` (`linesNode`, `overlayEntries`), `tui/tui.go` (`OverlayOptions.Fullscreen`), `tui/tui_surface_native.go` (`overlaysEqual`), `internal/codingagent/pig_logo_animation_play.go` (`playEgg3d`, `showPigLogoAnimation`, `eggSurface`, `eggClock`), `internal/codingagent/startup_header.go` (`builtInHeaderClickArea`), `internal/codingagent/ext_ui_context.go` (`specialLinesComponent.ClickArea`), `internal/codingagent/interactive_frontend.go` (`Env.Click`, `frontendInput`) and `internal/codingagent/interactive_input.go` (`dispatchRead`).

The click and screen slice is locked by `TestTuiSurfaceReportsAClickAreaOnLines` (an area changing alone updates the node; replay is fresh), `TestNodesEqualComparesClickAndFullscreen`, `TestTuiSurfaceClickNeedsTheAreaAndTheNode` (outside the area, an unknown node, an overlay that is not fullscreen, after Stop), `TestTuiSurfaceScreenCountsFromTheTerminal`, `TestTuiSurfaceScreenLinesFollowTheEndOrTheClickedComponent` (MainRow, MainColumn, DockColumn, the clicked component in view, the click's screen cell), `TestTuiSurfaceScreenLinesLeaveOutADockTheSessionShows`, `TestTuiSurfaceReportsAFullscreenOverlay` (the screen's size and a Click area with a screen session, an ordinary overlay without one), `TestTuiSurfaceKeepsTheDockUnderAFullscreenOverlayWhenTheSessionShowsIt`, `TestFrontendLogoClickPlaysTheAnimationOnTheScreen` (the logo's area, its start cell from the composed screen, the fullscreen overlay, click and escape), `TestFrontendSlashEggsPlayOnTheScreenOrInline` (no screen, a screen not shown now, reduced motion), `TestEggClockStandsStillWhileHidden`, `TestFrontendClickAfterCloseDoesNothing` and `TestFrontendTakenReadPaintsNothing` (a frontend read paints nothing, a key read paints at once). The D87 oracle and click tests are unchanged and pass; a session without a frontend never sets `frontendTookInput`, so its input loop paints as before.

The click and screen slice (`Lines.Click`, `Env.Click`, `ScreenSession`, `Overlay.Fullscreen`) was added on 2026-10-06 under the owner's direction to play PiG's easter eggs natively in Tern; keeping Tern's own dock under the animation was the owner's choice of 2026-10-06.

The appearance slice (`TuiSurface.SetTerminalOut`, `TuiSurface.TerminalOut`, `TuiSurface.QueryTerminalColors`, `frontendTerminal`, the `Session.HandleInput` rule for PiG's own terminal replies and the `Env.Out` rule that each Write, from any of the session's goroutines, reaches the terminal whole) was added on 2026-10-06 under the owner's direction to integrate PiG with Tern, so that PiG picks the theme variant for Tern's appearance and follows its light/dark switches.

The mark slice (`Frame.Mark`, `Mark`, `MarkPixel`, `SurfaceHooks.Mark`, `piglogin.SameHead`) was added on 2026-10-06 under the owner's direction to integrate PiG with Tern, so that Tern's settings show PiG's active sprite as their brand.

An extension surface may also carry its component structure (D107): `Lines.View`, `Overlay.View` and `ToolCard.ResultView` describe the lines as Pi's tui components (container, box, text, truncated text, markdown, spacer, dynamic border, select list, settings list, image, loader, stacks and opaque line ranges) and Pi's conversation components (user and assistant messages with thinking, tool executions, bash executions and diffs), with the rows each covers, the extension's per-surface theme overrides and the focused list. A conversation node carries the transcript nodes the main transcript reports for it (`ViewNode.Transcript`, `ViewNode.Diff`). The lines stay authoritative; a frontend that does not draw a view, or one of its kinds, draws the lines. `Env.Act` reaches a view's focused list through `Action.ViewNode`. `StatePayload.frontend` tells extensions a frontend draws, so Node derives views only then. The component-kit amendment (D107) was approved by the owner, Michael Kinsy, on 2026-10-06, and its conversation kinds on 2026-10-07.

Clicks on extension views (D91): `Click.ViewPath` names a node of the clicked node's `View` by the index of each child on the path, so `Row` and `Column` count from that view node's first cell, and `Click.Item` names a select-list item by its value or a settings-list row by its id. PiG turns a native click on an element of an extension's overlay (`overlay.N`) or dock view into the gesture a terminal sends at the cell that element occupies in the fullscreen layout: `press`, then `release` and `click` to the component that took the press, or, when none took it, `release` to the component under the pointer and `click` when that leaves the release too, to the same component with the same local `x`/`y` (Pi's `TuiMouseEvent`), so the extension cannot tell Tern from a terminal. PiG counts these clicks as Pi counts a terminal's: a click on the cell and component of the one before it, within Pi's 500 ms double-click interval, has `clickCount` 2, then 3. `Click.Count` is a session's own count: Tern's `activate`, which follows a double click's second `select` on a list item, is that click again with `Count` 2, delivered with `clickCount` 2 only when PiG had not counted it 2 already (the system's double-click interval may be longer than Pi's). A list item scrolled out of the window is wheeled into view first, as a user would. This happens only while the run's `tuiMode` is fullscreen; in regular mode a terminal sends no mouse, so a click does nothing. Reachable: select-list items, settings-list rows (not inside an open submenu), annotated `lines` list rows, and the first cell of text, box and other kit nodes. Not reachable: interior cells of custom-drawn `ansi`/`lines` blocks, for which the frontend has no cell coordinates, and transcript views. Limit: when the component leaves the press, a terminal's text selection decides whether a double click's release is a click, from the word under the pointer on the screen row; PiG reads that word from the component's own line, because a frontend does not draw Pi's screen behind an overlay, so a word that continues from the screen beside an overlay into its first cell is not seen. Call-site markers: `extensions/sdk/frontend/frontend.go` (`Click.ViewPath`, `Click.Item`, `Click.Count`), `tui/tui_surface_mouse.go` (`TuiSurface.clickExtension`, `sessionClickCount`), `tui/tui_surface_screen.go` (`TuiSurface.Click`). Locked by `TestConformance_ExtensionMouseClicksKitList` and the terminal-parity tests in `tui/tui_surface_mouse_test.go` (`TestTuiSurfaceClickMatchesTheTerminalOnOverlays`, `…ThroughViewPaths`, `…OnDockRows`, `TestTuiSurfaceClickWheelsASelectListItemIntoView`, `TestTuiSurfaceClickReachesASettingsRowUnderItsSearch`, `TestTuiSurfaceClickDoesNothingOutsideFullscreen`, `TestTuiSurfaceMultiClicksMatchTheTerminal`), which make the same clicks once through `Click.ViewPath` and once as SGR reports at the element's cell on the alternate screen and require the same events, and `TestTuiSurfaceActivateCompletesADoubleClick`. Added on 2026-10-06 under the owner's direction (seamless mouse for PiG extensions in Tern).

Tool result images: `ToolCard.Images` carries the images Pi's `ToolExecutionComponent` shows after a tool's output (`tool-execution.ts:338-368`): each image block with data and a MIME type, in order, while `terminal.showImages` is on, decoded and identified by the SHA-256 of its bytes (`ViewImage.Ref`), with `MaxWidthCells` the width PiG's terminal renderer draws it at (`terminal.imageWidthCells`, or 60 when it is unset, at most the card's width less 2 and at least 1, as Pi's `Image` computes it in `image.ts` `render`). Pi also requires a terminal image protocol; a frontend draws the images itself, so a member that cannot draw one shows Pi's fallback text, `[Image: [<mime>] <w>x<h>]`, in its place. The images are decoded and hashed once per block and the same slice is reported while the blocks and the width stay, so an unchanged card costs no new allocation and sends no update. The ANSI renderer is untouched. Call-site marker: `tui/tui_surface.go` (`ToolExecutionComponent.frontendTool`, `frontendImagesAt`). Locked by `TestTuiSurfaceSendsAToolResultsImages` (data and MIME required, undecodable data keeps no bytes, width setting and card bound, no rebuild or update for an unchanged card, none while images are hidden) and `TestFrontendToolCardCarriesTheResultsImages` (tool events with `showImages` on and off, a result without images). Added on 2026-10-07 under the owner's approval of Tern native images, which draw them as Tern `image` nodes that open Tern's viewer on a click.

User message images: `MarkdownText.Images` carries the images a user message holds, such as a pasted screenshot (each `ImageContent` with data and a MIME type, in order), live and when a session's messages are drawn again; it is nil for assistant text, which carries no images in Pi or PiG. Pi's `UserMessageComponent` draws only the text, and so does PiG's terminal renderer: the field is inert there, and a frontend may show the images with the message (Tern draws them as thumbnails that open its viewer). `UserMessageComponent` keeps the blocks and decodes them on a frontend's first use, so the terminal renderer never decodes them, and an unchanged message reports the same slice; `nodesEqual` compares the images by `Ref` and size without decoding. Call-site markers: `tui/tui_surface.go` (`UserMessageComponent.frontendImages`), `tui/user_message_block.go` (`SetImages`), `internal/codingagent/interactive_transcript.go` (`userMessageImages`). Locked by `TestTuiSurfaceSendsAUserMessagesImages` (images present, terminal lines unchanged, one decode, no update for an unchanged message) and `TestFrontendUserMessageCarriesItsImages` (a live message and a rebuilt session). Added on 2026-10-07 under the same approval.

Parity allowance: no Pi scenario selects a frontend member; Stock PiG runs none.

Remove when: Pi gains an equivalent frontend seam, or PiG stops supporting Piglet frontend members.

Approval: owner Michael Kinsy, 2026-10-04 (seam and tool cards as the first D91 pull request; the cancelled status, message, thinking and diff nodes approved the same day as the Tern-native drawing direction); the editor dock (the `Editor` node and `Env.Edit`, `Env.Undo` and `Env.Send`) and the footer and status slice (the `Working` and `Footer` nodes and the borderless native editor) assigned by the owner on 2026-10-05 for PiG 0.5.0.
SCRUTINIZED:approved

---

## D92 Piglet strip lists disable built-ins and compile them out of a Piglet Binary

Stock disposition: required substrate. A Piglet cannot remove a built-in from the binary that loads it, so the resolver, the ID table, the registration shims and the runtime gates live in Stock PiG; Stock PiG builds no strip tag and runs no strip list, so every built-in stays linked and enabled.

What: a Piglet's `strip` key lists stable IDs in five lists: `tools` (built-in tools), `commands` (built-in slash commands with the leading slash, and `/piglet`, the active Piglet's inspection command), `extensions` (built-in extensions), `apis` (model API implementations, `ai.StrippableAPIs`) and `features`. The IDs come from `internal/pigstrip/ids_generated.go` (`pigstrip.Lists`, `pigstrip.Known`), which `go run ./coding/piglet/internal/genstripids` (`make piglet-strip-ids`, part of `make generate`) builds from the built-in tool registry, `BuiltinSlashCommands` with `piglet.InspectCommand`, the built-in extension list (`codingagent.LlamaExtensionPath` and `builtin.All`), `ai.StrippableAPIs` and `pigstrip.Features` (the runtime features plus `pigstrip.BinaryFeatures`). An unknown ID is a resolve-time error that names it; `TestCommittedStripIDsMatchRegistrations` and `make piglet-strip-ids-drift` fail when a registration changes without the table. Strip lists union down `extends`; `extends.remove.strip` re-enables an entry and requires `allowWiden`. Strip and D91's `slots` are one Piglet model (keep, replace, strip): `RemoveSpec` carries both `slots` and `strip`, both merge down `extends`, and `pig piglet show` lists the replaced frontend and every stripped ID in one `Slots` section named by manifest path (`frontend  replaced(<dir>)`, `extensions.mcp  stripped(binary)`, `tools.grep  stripped(runtime)`).

One strip state. `internal/pigstrip` is the only source of truth for what a process strips: it holds the generated ID table and the process records (`pigstrip.Strip`, `Has`, `IDs`). A Piglet Binary's OFF shims record what the Binary compiled out from `init`. `StripSpec.Record` records every entry of every list of the active Piglet's strip list: `applyPigletStrip` at session startup, and `Main` for a Piglet Binary's baked Piglet before command dispatch. The experimental entry reads the selected Piglet's `StripSpec.HasFeature(experimental-server)` and records nothing, because the experimental server applies no Piglet. A compiled-out built-in and a runtime-stripped one are therefore the same record. A stripped built-in is removed where it is registered, so no surface derived from that registry can show it:

The experimental entry (`pig_experimental` builds, `runExperimentalCommand`) selects the Piglet from `--piglet <name|path>` or `--piglet=<name|path>` as the stable CLI does, ahead of `PIG_PIGLET_PATH` and `PIG_PIGLET_NAME`, and removes the flag before the experimental parser, which rejects existing CLI options (`takePigletFlag`). Locked by `TestExperimentalEntryHonorsPigletFlag`.

| Registry | Where a stripped entry is removed | Surfaces derived from it |
|---|---|---|
| built-in tools | Pi's `--exclude-tools` denylist, set from `pigstrip.IDs(tools)` | tool registry, active tools, system prompt tool list, `getAllTools`, RPC tool metadata; `--help` tool name rows, title and `--tools` examples |
| user bash (`!`, `!!`) | owned by the `bash` tool | editor input, `/hotkeys` rows, startup hints |
| slash commands | `BuiltinSlashCommands` | command registry and dispatcher, autocomplete, extension command conflict diagnostics |
| `/piglet` | the Piglet extension registers no `/piglet` command (`buildExtensionWithOwner` in `coding/piglet/main.go`); its `--piglet` flag, tool scoping and system prompt stay | extension command registry and dispatcher, autocomplete, extension API `getCommands`, RPC `get_commands`; typed `/piglet` goes to the model as an unknown slash command does in Pi |
| keybinding actions | an action whose command is stripped resolves to no key (`app.model.select`, `app.model.cycleForward`, `app.model.cycleBackward`: `/model`; `app.thinking.cycle`: `/thinking`; `app.message.copy`: `/copy`; `app.session.new`, `tree`, `fork`, `resume`: their commands); double-escape opens neither a stripped `/tree` nor `/fork` | key dispatch, `/hotkeys` rows, startup hints |
| built-in extensions | the ON shims (`builtin.All`, `llamaInlineExtensions`), `cliBuiltinExtensionNames` | loaded extensions with their commands, tools and providers, llama.cpp login, `pig config` `builtin:<name>` rows, `--help` (`pig mcp`), docs section topics, the first-run `/sprite` step (`pig-login`) |
| model APIs and providers | `ai.LookupBuiltInProvider`, `ai.OfferedModels`; a built-in provider whose catalog models are all on stripped APIs is no login provider (`ai.ProviderStripped`) | `--list-models`, `/model`, cycling, `--models`, startup default, RPC `get_available_models`, the extension model registry, `/login` and `/logout` provider lists, `--help` environment rows |
| settings rows | `settingItem.gated` with `unstripped` (skills: `Skill commands`; mermaid: `Mermaid diagrams`; changelog: `Collapse changelog`, `Install telemetry`; `/tree`: `Tree filter mode`; `/tree` and `/fork`: `Double-escape action` values) | `/settings` |
| help entries | `printHelp` passes its text through `stripHelp`, which drops or rewrites the entries a strip owns: tool name rows, the title's tool list and a `--tools` example naming a stripped tool; `pig mcp` and the `mcp` help topic; environment rows of a provider that offers no model; the `--skill`, `--prompt-template`, `--theme` and `--export` rows and examples; `update self`, `pig docs`, the `extension init` languages and `build` in the `pig piglet` row. `pig piglet` usage leaves out `build` and `publish` without `piglet-builder`. Stock PiG prints Pi's text unchanged | `--help`, `pig` subcommand listing, `pig piglet` usage |
| config listings | `cliBuiltinExtensionNames` leaves out a stripped built-in extension; `collectConfigResourceItems` leaves out the resources of a stripped `skills`, `prompt-templates` or `themes` | `pig config`, `pig status` resources |
| docs citations | `codingagent.PigDocsFile` names no file a `docs` strip never writes; the docs section names no stripped topic | system prompt, codemode description and errors, provider login help, startup onboarding line |

Predicates this replaces: `CLIFlags.pigletStrip`, `StripSpec.HasTool`, `HasCommand`, `HasAPI` and `CommandNames`, `InteractiveOptions.StrippedCommands` with `InteractiveMode.builtinSlashCommands` and `SlashRegistry.unregisterBuiltins` (now `pigstrip.Has(commands, "/<name>")` inside `BuiltinSlashCommands`); `ai.APIStripped` (now `pigstrip.Has(apis, <api>)`); `codingagent.StripFeature*` and `codingagent.StripFeatures` (now `pigstrip.Themes`, `Skills`, `PromptTemplates`, `ExperimentalServer` and `pigstrip.Features`); `experimentalServerStripped(p)` (now `StripSpec.HasFeature(experimental-server)` on the Piglet the entry resolves); and the per-list tables in `coding/piglet` (now `pigstrip.Known`). Pi's `--exclude-tools`, `--no-skills`, `--no-prompt-templates` and `--no-themes` stay the removal mechanism of their Pi registries; `applyPigletStrip` sets them from `pigstrip` only. `StripSpec.HasFeature` stays for the builder and manifest validation, which read a Piglet that is not running. Unchanged by decision: RPC commands that correspond to a stripped slash command (`new_session`, `set_model`, `compact` and others) keep the RPC wire unchanged, and skills, prompt templates and themes an extension provides still load under a stripped feature, as under Pi's `--no-*` switches. A strip list may remove every built-in tool and `/quit`: a tool-less Piglet is allowed by design, and any tightening is an owner decision.

Leak gate: `TestStripLeakGate` takes its cases from `pigstrip.Lists` and `pigstrip.Known` and the user-visible names of each ID from `internal/pigstrip/leakgate`, strips each ID in turn, and fails when a surface still names it. The interactive half (`internal/codingagent`) covers `/hotkeys`, the startup hints, slash autocomplete, argument completion, `/settings`, the `/login` providers, first-run setup, the system prompt docs section and the provider login help, and fails too when a key of a stripped command still runs its action. The CLI half (`cmd/pig`) covers `--help`, `pig piglet` usage, `pig config` and `--list-models` in process; the full system prompt with codemode active, the extension API (`getCommands`, `getAllTools`, the model registry) and RPC `get_commands` and `get_available_models` from `pig --mode rpc --piglet` with a probe extension; and `--help`, `pig piglet` and `--list-models` of cmd/pig built with every strip build tag. Built-in slash commands appear on no CLI surface, since Pi's `getCommands` and `get_commands` list none. Each half fails when a strip list or feature ID has no surface mapping.

Help and config call sites: `coding/cli/help_strip.go` (`stripHelp`, called from `printHelp`), `coding/cli/config_command.go` (`configResourceTypeStripped`), `coding/cli/extension_set.go` (`cliBuiltinExtensionNames`), `coding/piglet/main.go` (`printHelp`). Locked by both `TestStripLeakGate` halves. Stock PiG records no strip, so `stripHelp` returns Pi's text untouched (`pigstrip.Active` is false) and the other filters remove nothing.

Every ID has a disposition. `binary`: a Piglet Binary does not link the built-in; the native builder passes `-tags` with the effective list's tags (`StripSpec.BuildTags`), and each built-in's registration shim pair switches on its tag. `runtime`: the built-in stays linked and the Binary disables it at startup. Stock PiG running a Piglet applies every entry at runtime through the one strip state, and each ON shim then acts like its OFF shim, so a compiled-out feature and a runtime-stripped one report the same `<what> is stripped from this Piglet (strip.<list>: <id>)` message (`pigstrip.Error`) or take the same Pi fallback. A runtime-stripped `mcp` therefore skips the Radius MCP offer after a Radius `/login`, and a runtime-stripped `llama.cpp` has no inline entry and starts no llama host, exactly as in a Binary without them. Exception: in Stock PiG the CLI subcommands (`pig docs`, `pig piglet build`, `pig update`, `pig config`, `--export`) dispatch before the Piglet is applied, so their runtime strip takes effect only in-session; a Piglet Binary records its baked strip before dispatch, and compiles the shimmed ones out. The builder refuses to compile out an extension runtime that one of the Piglet's own cells needs (`stripRuntimeConflict`: a Node or Python cell, or a Go or Rust cell built at runtime).

Build tags (`pigstrip.Tag`: `pig_strip_` plus the ID with non-alphanumerics as `_`; `coding/piglet/strip_tags_test.go` pins that each tag has an ON (`!tag`) and OFF (`tag`) production file and that no other `pig_strip_` tag exists):

| ID | Tag | Leaves the Binary | Absent behavior |
|---|---|---|---|
| `extensions: mcp` | `pig_strip_mcp` | `mcp`, `mcp/oauth`, `coding/mcpext` | not loaded (settings or `-e builtin:mcp` dropped silently); `pig mcp` reports the strip, exit 1; no Radius MCP offer; an MCP server another extension registers is reported as unconnected with the strip message in place of Pi's "another extension may have replaced the built-in MCP support" |
| `extensions: codemode` | `pig_strip_codemode` (was `nocodemode`) | `codemode`, `builtin/codemode`, wazero | not loaded |
| `extensions: tool-search` | `pig_strip_tool_search` | its registration (`builtin/toolsearch` with codemode) | not loaded |
| `extensions: pig-login` | `pig_strip_pig_login` | its registration (`/sprite`) | not loaded |
| `extensions: llama.cpp` | `pig_strip_llama_cpp` | `internal/codingagent/llama` | no llama host, `/llama` or llama.cpp login |
| `apis: bedrock-converse-stream` | `pig_strip_bedrock_converse_stream` | the AWS SDK and smithy (89 packages) | its models are offered nowhere (see below); naming or building one fails with the strip message; a `models.json` custom model under its provider still takes its base URL from the full generated catalog, so the strip adds no `"baseUrl" is required` error |
| `apis: google-vertex` | `pig_strip_google_vertex` | `golang.org/x/oauth2`, `compute/metadata` | same |
| `apis: mistral-conversations` | `pig_strip_mistral_conversations` | the Mistral provider files | same |
| `features: node-extensions` | `pig_strip_node_extensions` | the embedded Node runtime (10.5 MB), zstd | a TypeScript or JavaScript extension fails to load with the message |
| `features: extension-sdk-go`, `-rust`, `-python` | `pig_strip_extension_sdk_go` etc. | the staged SDK sources (`extensions/sdk` `Source`, `sdk-rs`, `sdk-py`) | SDK staging, source builds, packed-cell SDK lookup, `pig reload` and `pig extension init` report the message |
| `features: syntax-highlight` | `pig_strip_syntax_highlight` | `tui/internal/hljs`, jsarray (regexp2 stays: the Markdown component evaluates marked's rules with it) | Pi's unknown-language code block path |
| `features: word-dictionaries` | `pig_strip_word_dictionaries` | the CJK, Thai, Lao, Khmer and Burmese dictionaries (6.3 MB) | each run is one word, as ICU without dictionary data |
| `features: mermaid` | `pig_strip_mermaid` | `internal/mermaid`, goldmark | mermaid fences stay raw (mermaid off) |
| `features: export-html` | `pig_strip_export_html` | `internal/codingagent/export` and its assets | `/export` to HTML, `--export` and RPC `export_html` report the message |
| `features: self-update` | `pig_strip_self_update` | `coding/cli/self_update.go` | `pig update self` reports the message; no new-version notice |
| `features: changelog` | `pig_strip_changelog` | the root package (`CHANGELOG.md`) | no `/changelog` (it leaves `BuiltinSlashCommands`, `featureCommands`); no What's New, so no update telemetry ping |
| `features: docs` | `pig_strip_docs` | `internal/pigdocs` | `pig docs` reports the message; no docs prompt section |
| `features: piglet-builder` | `pig_strip_piglet_builder` | `coding/pigletbuild` | `pig piglet build` and `publish` report the message |
| `features: experimental-server` | none | already absent from stock `cmd/pig` | `server` and `client` go to the stable CLI |

A stripped API's models are offered nowhere: not by `--list-models`, `/model` and the model selector, model cycling, the `--models` scope, startup's saved-default and provider-default fallback, RPC `get_available_models`, or the extension model registry (`getAll`, `getAvailable`, the replicated catalog). The filter is one predicate, `ai.OfferedModels`, applied where the catalog leaves its sources: the generated catalog listing (`ai.ListModels`), every read of an `ai.Models` collection (`GetModels`, `GetAllModels`, `GetAvailable`, `GetAllAvailable`), and the composed per-provider catalog (`ModelRegistry.GetProviderModelData`, which also covers `models.json` and extension definitions on a stripped API, and `GetAll`). `/model <provider>/<id>` naming one opens the model selector with no match, as for any model the selector does not offer. An exact catalog lookup (`ai.LookupModel`, `ai.LookupModelExact`) still finds such a model, so building one reports the strip. `--model` and `--provider` name the strip too: when resolution finds no offered model, `ai.StrippedModelError` reports the strip of the named catalog model, or of a provider whose catalog models are all on stripped APIs, instead of `Model ... not found` or `Unknown provider`. A `models.json` model on a stripped API under a provider of its own is not a catalog model, so `--model` naming it reports `Model ... not found`.

Runtime only, with the reason: built-in `tools` (one static registry indexed by prompts, renderers and RPC metadata), `commands` (handlers share `InteractiveMode` state; `/piglet` is one handler in the Piglet extension, which every Piglet process links for `--piglet`, scoping and the system prompt, so compiling it out would save nothing), `themes`, `skills`, `prompt-templates` (loaders inside the resource loader and settings UI; the dark and light themes are required). Not stripped: Anthropic, OpenAI (completions, responses, Codex), Copilot and pi-messages APIs (shared helpers, no external SDK; Codex's websocket is woven into OpenAI Responses), Google Generative AI and Azure (small gain; shared helpers would move), OAuth login flows (a few hundred lines each, called directly by core login), image processing and clipboard (core: read tool, prompt images), `golang.org/x/text/collate` (Pi's localeCompare order), the earendil announcement image (539 KB, Pi easter egg; a later candidate), and the subprocess host, which fused members and embedded cells still need.

Functional floor: a Piglet can't strip every tool and `/quit`. `Piglet.Validate` (`validateStripFloor` in `coding/piglet/strip.go`) refuses a strip list that names every built-in tool in the generated table together with `/quit`: `strip.tools and strip.commands: a Piglet can't strip every tool and /quit (strip.tools: bash, edit, find, grep, ls, powershell, read, write; strip.commands: /quit); keep at least one tool or /quit`. Either alone stays allowed: without `/quit`, Ctrl+D and a double Ctrl+C still exit, and a Piglet without tools is a chat-only agent. The check sits beside the `skills` conflict in `Piglet.Validate`, which runs on the authored Piglet and on the effective Piglet after `extends` unions the strip lists, so one rule covers `pig piglet validate`, `build`, `publish` and startup: Stock PiG reports it as any invalid requested Piglet (`warning: piglet <path>: ...`, then `pig: the requested Piglet could not be loaded`, exit 1), and a Binary can't carry such a Piglet because its build refuses it. Locked by `TestStripFloorRefusesEveryToolAndQuit` (`coding/piglet`), `TestRunBuildRejectsPigletBelowTheStripFloor` (`coding/pigletbuild`) and `TestPigletStripFloorAtStartup` (`cmd/pig`), each red before (the Piglet loaded and answered). Approval: owner and the 0.4.2 lead, 2026-10-08 (MAX-STRIP finding 2).

Keep mode: `strip.keep.<list>` (`StripSpec.Keep`, a `StripKeep` of five `*[]string` lists, so `keep.<list>: []` keeps nothing and survives marshalling) names the IDs a list keeps; every other ID of that list in the running core's table is stripped, so a minimal base stays minimal as PiG and Pi add built-ins. `ParseBytes` refuses a null `strip.keep` or keep list, which the decoder would read as deny mode stripping nothing (`validateStripKeepNulls`), and a list in deny and keep mode in one file (`validateStripModes`), then expands keep mode in place (`StripSpec.expandKeep`): the deny field becomes the table's IDs of the list minus the kept ones. That is the one place strip resolves, so `applyPigletStrip`, `Record`, `BuildTags`, `StrippedIDs`, the leak gates, records and `validateStripFloor` read the expanded deny lists unchanged, and `Validate` refuses a keep-mode list whose deny field is not its expansion. Keep IDs use the same table and checks as deny IDs; `keep.keep` is an unknown field. `mergeStrip` applies the extends algebra list by list (piglet-derivatives design table 2.3): a deny child narrows a keep base's keep list, a child keep list must lie inside the base's (keep base) or outside its deny list (deny base) or fails pointing to `extends.remove.strip`, and keep mode is inherited per list down the chain. `extends.remove.strip.<list>` re-enables IDs in both modes (in keep mode the ID joins the keep list) and still requires `allowWiden`; `extends.remove.strip.keep.<list>` is refused and names the list, because returning to deny mode would admit every later built-in (owner answer Q4, 2026-10-08). `StripSpec.MarshalYAML` writes the keep lists and not their expansion, so the baked Piglet re-expands at startup against the Binary's own table. The Binary record gains `stripKeep` (keep-mode lists and their kept IDs) and `stripTable` (the building core's table), both canonical and in the digest; `pig piglet show` prints `extensions  keep([])` above the expanded rows. Strip delta report (`coding/piglet/strip_delta.go`): `pig piglet build` (before the build runs, in `--json` as `stripDelta`) and `pig piglet show` compare the running table with the newest Binary record's `stripTable` for the Piglet and print `Strip table: PiG <old> → <new> adds <n> built-in IDs`, the new IDs a keep-mode list leaves out and the ones a deny-mode list now includes; with no record nothing prints. Locked by `TestStripKeepModeParses`, `TestStripKeepModeStripsNewTableIDs`, `TestStripKeepModeExtendsAlgebra` (every row of table 2.3 and the keep-to-deny refusal), `TestStripKeepModeMeetsTheFloor`, `TestStripDeltaReportNamesNewTableIDs` (synthetic new IDs through the test-only `knownStripIDs` table), `TestShowPrintsKeepModeAndTheStripDelta`, `TestBinaryRecordCarriesKeepModeAndTheStripTable`, `TestRunBuildPrintsTheStripDeltaReport`, `TestKeepModePigletBuildsASignedBinary` (real signed build) and `TestStripLeakGate/session_keep_mode`. Approval: owner and 0.4.2 lead, 2026-10-08, under D92 and D18 with no new number.

Pi source: Pi 1.1.0 has no Piglet and no strip list. The runtime gates reuse Pi's switches: `excludeTools` (`sdk.ts`), `noSkills`, `noPromptTemplates`, `noThemes` (`resource-loader.ts`). A stripped built-in extension is dropped from the built-in extension paths silently (`builtinExtensionStripped` in `coding/cli/extension_set.go`), as Pi drops a `disabledBuiltinExtensions` entry for `--no-mcp` (`main.ts:785`, `resource-loader.ts:578`): no MCP servers, tools, commands or OAuth, and the `mcp` subcommand still works for a runtime strip.

`--no-mcp` stays Pi's own switch and is not recorded in `pigstrip`. `enabledBuiltinExtensionPaths` drops `builtin:mcp` for it as Pi's resource loader does, and Pi's other `--no-mcp` output stays unchanged: `--help` keeps its MCP rows, `pig mcp` works, and an MCP server another extension registers is reported with Pi's "another extension may have replaced the built-in MCP support" (`runner.ts:760`). Routing `--no-mcp` through `pigstrip` would change that Pi output, so only a Piglet strip list records `strip.extensions`. `strip.tools` stays exact built-in tool IDs validated against the generated table and feeds `--exclude-tools`; it accepts no `*` patterns, because MCP tool names (`mcp__<server>__<tool>`) are not known at resolve time and could not be validated. Pi's `createToolNameMatcher` (`mcp-servers.ts`, ported as `extension.ToolNameMatcher`) matches a stripped tool's exact name; a Piglet that drops MCP tools strips `extensions: [mcp]`.

Why: the owner approved Piglet strip (issue #92; Piglet slots and strip design, sections 4a, 5 and 9 phase P3) on 2026-10-04, and on 2026-10-05 directed the Piglet to drop as many features at build time as realistic, aligned with upstream's new tool options, with binary size and startup time as goals.

Stock behavior: without an active Piglet, or with a Piglet without `strip`, every gate returns before it acts: `applyPigletStrip` leaves the flags and `pigstrip` untouched, so every `pigstrip.Has` is false (`builtinExtensionStripped`, `BuiltinSlashCommands`, every ON shim), and a Binary record has no `strip` field. Stock builds no tag, so every ON shim is linked and every moved reference is the same code. Stock output is unchanged.

Call-site markers: `internal/pigstrip/pigstrip.go`; `coding/piglet/strip.go`, `strip_delta.go`, `types.go`, `extends.go`, `main.go` (`renderSlots`, `cmdShow`, `InspectCommand`, `buildExtensionWithOwner`); `coding/piglet/artifact/record.go`; `coding/pigletbuild/records.go`, `native_build.go` (`BuildTags`, `stripRuntimeConflict`); `coding/cli/piglet_strip.go`, `extension_set.go` (`builtinExtensionStripped`), `builtin_extensions.go`, `cli_runtime_build.go`, `rpc_mode.go`, `main.go`, `startup_settings.go`, `main_experimental.go`, and the shim pairs `mcp_command{,_stripped}.go`, `llama{,_stripped}.go`, `export_flag{,_stripped}.go`, `self_update{,_stripped}.go`, `docs_command{,_stripped}.go`, `piglet_builder_command{,_stripped}.go`; `coding/extension/builtin/{codemode,tool_search,mcp,pig_login}_{on,off}.go`; `coding/extension/host/inproc/mcp_servers.go` (`unhandledMcpServersReason`); `ai/provider_strip.go` (`OfferedModels`, `StrippedModelError`), `register_builtins.go` and the `bedrock`, `google_vertex`, `mistral` ON files with their `_stripped.go` files; `internal/codingagent/model_resolver.go` (`strippedCliModelError`), `radius_models.go` (`dropUncomposableProviders`); `coding/model.go`, `ai/direct_simple.go`; `coding/extension/host/subprocess/node_runtime_{on,off}.go`, `builder.go`, `node_runtime_cache.go`; `coding/extension/pigsdk/sdk_{go,rust,python}_{on,off}.go`; `coding/extension/host/runtimecell/sdk_strip.go` and the packed SDK finders; `tui/highlight_hljs_{on,off}.go`, `tui/highlight.go`; `internal/wordsegmenter/cjk.go`, `sea.go`, `dictionaries_off.go`; `internal/codingagent/mermaid_transform.go`, `mermaid_off.go`, `llama_{on,off}.go`, `interactive_radius_mcp{,_stripped}.go`, `session_export_html{,_stripped}.go`, `changelog_bundle{,_stripped}.go`, `model_registry.go`, `slash_commands.go` (`BuiltinSlashCommands`), `slash_session_handlers.go`, `prompts/docs_section{,_stripped}.go`; the generated table `internal/pigstrip/ids_generated.go`.

Locked by: `TestStripRejectsUnknownIDsByName`, `TestStripAcceptsKnownIDs`, `TestStripUnionsDownTheExtendsChain` (with keep, replace and strip in one remove), `TestShowListsSlotsAsOneModel`, `TestStripBuildTagsHaveRegistrationShims` and `TestStrippedPigletCommandIsNotRegistered` in `coding/piglet`; `TestPigletStripRemovesPigletCommand` (`cmd/pig`: RPC `get_commands`, `getCommands`, typed `/piglet` reaching the model); `TestCommittedStripIDsMatchRegistrations`; `TestBinaryRecordStripIsPartOfIdentityAndDigest`; `TestBinaryStripEntriesRecordTheEffectiveStrip`, `TestPigletBinaryBuildArgsPassStripTags` and `TestStripRuntimeConflictRefusesARuntimeTheBinaryNeeds` in `coding/pigletbuild`; one link test per tag that runs `go list -tags <tag> -deps ./cmd/pig` (or the package's embed or file list) in a normal `go test` and was red-proven by breaking the shim (`TestStripCodemodeBuildOmitsCodemodeAndItsEngine`, `TestStripMcpBuildOmitsMcpClientAndExtension`, `TestStripToolSearchBuildOmitsToolSearch`, `TestStripPigLoginBuildOmitsSpriteRegistration`, `TestStripLlamaBuildOmitsLlamaHost`, the provider, Node, SDK, highlight, dictionary, mermaid, export, self-update, changelog, docs and builder link tests); runtime-strip tests per feature, among them `TestApplyPigletStripRecordsEveryList` (`cmd/pig`, every runtime strip list lands in `pigstrip`), the `Record` case of `TestStripAcceptsKnownIDs` (`coding/piglet`), `TestKnownCoversEveryList` and `TestFeatureIDsAreStable` (`internal/pigstrip`), the `a stripped mcp offers nothing` case of `TestRadiusLoginOffersTheRadiusMCPServerUpstream` (`internal/codingagent`) and `TestRunnerNamesTheMcpStripForUnhandledMcpServers` (`coding/extension/host/inproc`); the offered-model tests `TestStrippedAPIModelsAreNotOffered` and `TestStrippedModelErrorNamesTheStrip` (`ai`), `TestModelRegistryOmitsStrippedAPIModels` and `TestModelsJSONCustomModelUnderAStrippedProviderIsNoBaseURLError` (`internal/codingagent`), `TestPrintModelListOmitsStrippedAPIModels`, `TestStrippedProviderCustomModelIsNeitherOfferedNorALoadError`, `TestSelectStartupModelSkipsStrippedAPIModels` and `TestRPCGetAvailableModelsOmitsStrippedAPIModels` (`cmd/pig`); and stripped-Binary tests in `cmd/pig/strip_*_binary_test.go` that build `cmd/pig` with every tag (`runStrippedPig`), among them `TestStrippedBinaryReportsStrippedAPIs`, which also pins that `--list-models` lists no stripped API's model. `make piglet-strip-build` (part of `ci-contracts`) vets every package and builds `cmd/pig` with every tag.

Parity allowance: no Pi scenario selects a Piglet strip list; Stock PiG runs none and builds no tag. A stripped profile is outside the parity families.

Remove when: Pi gains an equivalent built-in removal mechanism, or PiG stops supporting Piglet strip lists.

Approval: owner Michael Kinsy, 2026-10-04 (runtime strip as the first D92 pull request); 2026-10-05 (compile-out of every realistic candidate, aligned with upstream's tool options).
SCRUTINIZED:approved

---

## D95 Install-change restart warning for a running native executable

Stock disposition: inert capability. It detects a changed install and shows one warning. It selects no product behavior and runs no extension code.

What: Pi 1.1.0 (since 1.0.4, #10439) warns when an update or removal replaced the install a running process loads code from. Pi's `detectInstallChange` (`packages/coding-agent/src/config.ts`) re-reads the `package.json` the process started from, and `InteractiveMode.maybeShowInstallChangeWarning` shows the warning in place of the bug report hint. Pi returns no change for its Bun executable, because that code is embedded. PiG is one native executable, so the package-based check cannot apply. PiG builds the same behavior from what can change under a running `pig`:

- At startup `installchange.Record` stores the executable's identity: the `os.Executable` path with symbolic links resolved, the device and inode on Unix or the volume serial number and file index on Windows (`GetFileInformationByHandle`), the size and the modification time. It also keeps the path the process was launched from, so a package manager that repoints a link is noticed.
- The extension host records each extension cell it starts from the governed cache (a Go, Rust or Python packed cell, a Node launcher or an isolated binary), and only a cache artifact that holds a usage lease.
- Nothing polls. The check runs when an error is shown: on an assistant error before the bug report hint, on a failed tool call, in `showError` and when queued extension errors are shown. It stats the recorded paths and never searches for another install, as Pi reads the `package.json` it started from, so a removed install is reported even when another `pig` sits further up the tree. Only a missing file counts; a permission error says nothing about the install. The first detected change sets a flag, so the warning shows once and the bug report hint no longer shows, as in Pi.
- A replaced or removed executable, or a cell or runtime file that another `pig` pruned, shows Pi's warning for a removed install with pig's app name: `Warning: The pig installation this session runs from was removed or replaced. Features that load code on demand can fail until restart. Restart with `pig --session <id>` to continue this session.` A pruned cell or runtime file names the extension files instead: `The pig extension files this session runs from were removed or replaced.` Without a persisted session or a terminal, the last sentence is `Restart pig.`

Single-binary difference: Pi detects a different version string in a package file and names it (`pi was updated to X while this session was running (Y)`). PiG reads no version from the replaced file and cannot name it, because it cannot run or parse a binary it did not start, so it always uses Pi's sentence for a removed or replaced install. Pi's check never fires for the Bun executable, which PiG's check replaces. PiG's check does not cover a change to a file that the process embeds, because an embedded file cannot change.

Pi source: `packages/coding-agent/src/config.ts` `detectInstallChange`; `packages/coding-agent/src/modes/interactive/interactive-mode.ts` `maybeSuggestBugReport`, `maybeShowInstallChangeWarning` and the `tool_execution_end` case; `packages/coding-agent/test/config.test.ts`, `test/interactive-mode-bug-report-hint.test.ts` and `test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts`.

Why: another `pig` can update the executable or prune the extension cache while this process runs, and a cell started later then fails with a missing file. The warning turns that failure into a restart instruction.

Call-site markers: `coding/cli/main.go` (`Main`), `internal/installchange/installchange.go`, `internal/codingagent/install_change.go` (`maybeShowInstallChangeWarning`), `internal/codingagent/interactive_chat.go` (`showError`), `internal/codingagent/interactive_extension_errors.go`, `coding/extension/host/subprocess/host.go` and `packed_go.go`.

Locked by: `internal/installchange/installchange_test.go` replaces and removes a real executable that a built probe records (`TestDetectInstallChangeReportsARemovedInstallInsteadOfReadingAFileFurtherUp`, `TestDetectInstallChangeReportsAReplacedExecutable`, `TestDetectInstallChangeReportsPrunedCellFiles`, `TestTrackerFollowsASymlinkedExecutable`); `internal/codingagent/install_change_test.go` drives the warning through the interactive error paths; `coding/extension/host/subprocess/install_change_tracking_test.go` shows that a started cell is tracked and that pruning its cache is detected.

Remove when: Pi stops detecting an install change, or PiG no longer starts code from files that an update or another process can replace or remove.

Approval: the owner approved building this PiG version in place of designing it out on 2026-10-05.
SCRUTINIZED:approved

## D97 Packaged remote execution daemons live next to the pig executable

Stock disposition: inert capability. It only locates a program that the caller asks to deploy; it selects no product behavior.

What: Pi resolves the daemon of `@earendil-works/pi-env` from `bin/pi-env-<platform>-<arch>/pi-env[.exe]` inside its npm package (`ssh.ts`). A PiG binary cannot embed eight daemons, so `env.PackagedDaemon` resolves `<directory of the pig executable>/pi-env/pi-env-<platform>-<arch>/pi-env[.exe]`. `PI_ENV_DAEMON_DIR` replaces that directory, and `SshConnectOptions.Binary` names one file. `make build-env-daemons` cross-builds all eight into a directory. Without a daemon in place, `SshConnection` fails with `No pi-env daemon for <platform> <arch>`, as Pi does when its package lacks the binary.

Why: one Go binary per release target is how PiG ships. The release archive has to carry the daemons next to `pig` (one archive per host system holding all eight, about 35 MB); that release-engineering step is not part of this change, so a stock `pig` finds no daemon until it is.

Call-site markers: `env/ssh.go` (`packagedDaemonDirectory`, `PackagedDaemon`), `Makefile` (`build-env-daemons`).

Locked by: `env/packaged_daemon_test.go` (`TestPackagedDaemonLocation`) fails when the directory override, the per-system subdirectory or the Windows `.exe` name changes.

Remove when: never; the location follows the distribution form.

Approval: approved by owner Michael Kinsy 2026-10-06.
SCRUTINIZED:approved

## D100 Crash attribution reads Go panic traces

Stock disposition: required substrate. Pi attributes a crash to an extension from a JavaScript stack. PiG's own crashes carry a Go trace, and without this form no crash in the PiG process could be attributed.

What: `internal/codingagent/crash_log.go` (`isStackFrame`) accepts, in a stack that holds a Go goroutine header (`goroutine N [...]:`), a tab-indented `<file>:<line>[ +0x<offset>]` line as a stack frame. Pi's `at` frames match exactly as in Pi, and a JavaScript stack that contains such a line attributes nothing, as in Pi.

Why: a Go trace puts the source location on its own line below the function name and has no `at` frames.

Call-site markers: `internal/codingagent/crash_log.go` (`isStackFrame`).

Locked by: `internal/codingagent/crash_log_test.go` (`TestStackFrameFormsAreJavaScriptAtFramesAndGoPanicLocations`) fails when a Go panic location stops counting as a stack frame.

Remove when: never; the trace format follows the implementation language.

Approval: required substrate recorded by the 0.5.0 integration; approved by owner Michael Kinsy 2026-10-06.
SCRUTINIZED:approved

---

## D101 The get_extensions RPC command lists the loaded extensions

Stock disposition: inert capability. It reports the extensions the session already loaded and selects no behavior.

What: Pi's RPC protocol has no command that lists loaded extensions. Pi's in-process callers read the list with `session.extensionRunner.getExtensionPaths()` (`packages/coding-agent/src/core/extensions/runner.ts:625`), which its evaluation harness uses to reject unexpected extensions (`packages/evals/src/harness.ts:357-363`). PiG's evaluation harness drives a `pig` process over RPC, so `get_extensions` returns `{"paths": [...]}`: the resolved path of each loaded extension in load order, hidden extensions included, with PiG's built-in extensions as `builtin:<name>`. The result is empty when the session has no extension runner.

Pi source: `packages/coding-agent/src/core/extensions/runner.ts` `getExtensionPaths`; `packages/evals/src/harness.ts` `createPiCodingAgentHarness`.

Why: the harness cannot call the runner across a process boundary, and a regenerated Pi client has nothing to learn from it because every other RPC command is unchanged.

Call-site markers: `coding/cli/rpc_mode.go` (`get_extensions` case).

Locked by: `coding/cli/rpc_mode_test.go` (`TestRPCGetExtensionsResponseShape`) and `internal/evals/harness_run_test.go` (`TestRunPiCodingAgentRejectsUnexpectedExtensions` fails when a configured extension is not the bridge, and fails if the check is removed).

Remove when: Pi adds an RPC command that lists loaded extensions (adopt its name and shape), or PiG's evaluation harness runs an in-process Session.

Approval: the lead requested the command under the owner's instruction of 2026-10-05 to port and account for packages/telemetry and packages/evals; approved by owner Michael Kinsy 2026-10-06.
SCRUTINIZED:approved

---

## D102 Exit log, crash-output markers and uncaught-goroutine handling for interactive sessions

Stock disposition: product-neutral diagnostics. They record why an interactive session ended and select no product behavior.

What: Pi 1.1.0 leaves a trace for one unrequested ending, an uncaught JavaScript exception (`crashes.json`). A termination signal (`registerSignalHandlers` in `packages/coding-agent/src/modes/interactive/interactive-mode.ts`), a dead terminal (`emergencyTerminalExit`, exit 129) and an external SIGINT end the process with no record. Go adds two endings Pi cannot have. A panic or fatal error in a goroutine that does not recover ends the process with a runtime dump on stderr and no crash record, where Pi routes an unawaited rejection to its `uncaughtException` handler. The OS can also kill the process (SIGKILL, memory pressure on macOS, power loss), which no code in the process observes. Issue #165 reported a session that exited by itself a few minutes after a prompt round trip and left nothing to diagnose.

Current behavior:

- `<agent dir>/exit.log` receives one line per ending that no user action asked for: `received SIGHUP`, `received SIGTERM` (every mode), `received SIGINT (exit 130)`, `terminal gone: <error> (exit 129)`, `extension requested shutdown (ctx.shutdown())`, `interactive mode ended with an error: <error> (exit 1)`. Each line carries the time, the pid and the parent pid. The parent pid is 1 when the parent died. `PIG_DEBUG` also copies the reason to the debug log. The file keeps the newest 100 lines once it passes 64 KiB. A user quit (`/quit`, Ctrl+D, Ctrl+C twice) writes nothing.
- An interactive session writes `<agent dir>/crash-output/<pid>.log` at startup. It holds one JSON header line (pid, parent pid, version, working directory, start time). `runtime/debug.SetCrashOutput` makes the Go runtime copy any unrecovered panic or fatal error into it, in addition to stderr. Every clean or already-recorded end removes the file. At the next interactive start, `ReconcileSessionMarkers` handles each file whose process is gone. A file with a runtime report becomes a `crashes.json` record (`uncaught_exception` for a panic, `fatal_error` for a runtime fatal error) stamped with the file's modification time, which is when the process died. The startup notice announces it when it is less than seven days old, as for any crash record, and `/bug` attaches it. An `exit.log` line goes with it. A file with only the header becomes an `exit.log` line saying that the session ended without recording an exit, which means the OS killed it or it died where Go could not report.
- A panic in a goroutine of the interactive session reaches `uncaughtOffLoop` when the goroutine was started through the session's background group, or when it happens in the cache-warm refresh outside the parts upstream's `refresh` wraps in `try`. It stops the extension processes, restores the terminal modes, prints `pig exiting due to uncaughtException:` with the stack, records the crash and exits 1, as Pi's `uncaughtCrash` does. A dead-terminal error there takes the emergency exit instead. Any other goroutine still ends the process as Go does, and its report reaches the marker file.
- `pig diagnose` prints the crash log, exit log and debug log paths and the newest five `exit.log` lines.

Parity fix, not part of this feature: when the terminal's input ends while its output lives, Pi 1.1.0 registers no `end` listener on standard input and keeps running, and exits 0 only when nothing else holds its event loop open. PiG used to exit 1 after printing `EOF`, which was one way an idle session could vanish. It now stops reading input and keeps running until a signal, as Pi does. `TestTerminalInputEOFDoesNotEndTheSession` and `TestInteractiveKeepsRunningWhenInputEndsOnALiveTerminal` lock it.

Why: a session that vanishes while idle must leave the reason. The cache-warm timer fires minutes after a round trip on a timer goroutine. Upstream's refresh catches failures of the extension decision and of the refresh request, and PiG recovers a panic in those parts the same way. A panic elsewhere in the refresh would have ended the process with no record, where Pi's floating refresh promise reaches `uncaughtCrash`.

Call-site markers: `internal/codingagent/exit_record.go`, `internal/codingagent/uncaught_goroutine.go`, `internal/codingagent/interactive.go` (`Run`), `internal/codingagent/interactive_input.go` (`emergencyTerminalExit`), `internal/codingagent/interactive_crash.go` (`uncaughtOffLoop`), `internal/codingagent/interactive_tui.go` (`handleInterruptSignal`, `requestExtensionShutdown`, which both extension hosts call), `internal/codingagent/cache_warmer.go`, `coding/cli/main.go`, `coding/cli/diagnose.go` and `coding/cli/profiling.go`.

Locked by: `internal/codingagent/exit_record_test.go` ends a real child process with a goroutine panic, an unrecovered background-task panic, a runtime fatal error (concurrent map writes) and SIGKILL, and checks the next start's crash record and `exit.log` line. `TestReconciledCrashKeepsTheCrashTime` checks the crash time and the seven-day announcement window. `TestBackgroundTaskPanicEndsTheSessionWithACrashRecord` drives the production handler, and `TestDeadTerminalPanicOffTheOwnerLoopKillsTheExtensionProcesses` its dead-terminal branch. `TestCacheWarmerRefreshRequestPanicIsCaughtAndReschedules`, `TestCacheWarmerDecisionPanicKeepsPisDecision` and `TestCacheWarmerRefreshPanicOutsideTheCaughtRequestReachesTheUncaughtHandler` pin upstream's catch boundaries in the refresh timer. `TestSubprocessShutdownHostActionRequestsExitViaOwnerLoop` and `TestInprocExtensionShutdownLeavesALine` check the extension-requested line. `coding/cli/interactive_exit_record_linux_test.go` runs the binary on a pseudo-terminal and checks SIGHUP, SIGTERM, SIGINT, a closed controlling terminal, a closed input with a live output (the session keeps running) and a SIGKILL reported by the next start.

Remove when: Pi records these endings itself, or PiG can no longer lose a session without an observable trace.

Approval: the owner assigned lane idle-exit-165 on 2026-10-06 to make every exit that no user action asked for leave a record.
SCRUTINIZED:approved

---

## D104 Durable read path

Stock disposition: inert capability. Each part is a cache or a read accessor behind an existing contract; no extension or Piglet selects it, and a caller that does not use the new members sees PiG's earlier behavior.

What: Pi Durable re-reads and re-decodes the stored transcript for every context read (pi-durable 1.0.4 reads about 212,000 rows per eight-tool turn at 3,500 turns). PiG's Go Durable keeps that contract and adds:
- `sqlite.Options.WorkingSetBytes` (also `NodeSqliteStorageOptions.WorkingSetBytes`): a bounded, per-conversation working set of decoded entries in `SqliteStorage`. A Commit writes through it, a read fills it, and a change another connection makes to the database file (`PRAGMA data_version`) resets it. The default is 64 MiB of estimated decoded size; 0 retains nothing. `Storage.Entry` and `Storage.ScanEntries` still return detached copies. The Harness reads a context range through the internal `durable/internal/entryscan` seam, and every view that leaves the Harness (`Conversation.Context`, `runtime.Context` for task handlers, replicated view entries, the exported `ReadContext`, `DeriveContext` and `ActiveEntries`) is a deep copy. Only the generation handlers read shared, read-only views.
- `sqlite.TextReader`: an optional database capability that hands each result row's first column to a callback without building a row map. `NodeSqliteDatabase` implements it; a database without it loads through `All`.
- `ai.Context.Frozen`: the caller declares that nothing modifies `Messages`, or anything they reference, while a request made from the context is live. Normalization then shares the messages, as pi-ai's `normalizeContext` does (`packages/ai/src/utils/transcript.ts:30-34`, which copies nothing); without it PiG deep-copies and validates them. Validation still runs. Durable's generation sets it unless a `beforeRequest` hook receives a copy.
- `ai.TranscriptContext.Len` and `At`: read a normalized transcript without the deep copy `Messages` makes, as reading `transcript.messages[i]` does in pi-ai. The returned message shares memory with the transcript and is read-only.
- `ai.NewDecodedToolCall`: builds the tool call that decoding the stored JSON object yields, including the member order of its arguments, so the single-pass entry decoder in `durable` need not re-run the reflection decoder for tool calls.

Pi source: `packages/durable/src/harness/context.ts` (the context scan per read); `packages/ai/src/utils/transcript.ts` `normalizeContext`.

Why: on the durable-bench workload a warm turn at 3,500 turns went from about 9.6 s to about 0.37 s with the default GOMAXPROCS, and to about 0.2 s with GOMAXPROCS=1, in one host's measurements (`bench/durableperf/README.md`), with the store format and every ported Durable test unchanged.

Call-site markers: `durable/storage/sqlite/working_set.go` (`workingSet`), `durable/storage/sqlite/node/node.go` (`WorkingSetBytes`, `EachText`), `durable/storage/sqlite/database.go` (`TextReader`), `durable/harness/context_cache.go` (`contextCache`), `ai/transcript.go` (`Frozen`, `Len`, `At`), `ai/types.go` (`NewDecodedToolCall`).

Locked by: `durable/storage/sqlite/working_set_test.go` (range equals `ScanEntries` through forks, eviction, out-of-order commits, failed commits and another connection's writes; results are never written again), `durable/harness/context_cache_test.go` (the cached context equals `DeriveContext` through head markers, edits, forks and out-of-order commits), `durable/harness/context_detach_test.go` and `durable/harness/context_shared_test.go` (changes a caller, a task handler, a `beforeRequest` hook, a view observer or a caller of `DeriveContext` or `ActiveEntries` makes to what it receives do not reach later reads), `ai/transcript_frozen_test.go`, `ai/json_plain_test.go`, `ai/faux_usage_estimate_test.go`, and `durable/entry_fast_test.go` (the fast decoder equals the reflection codec on encoded records and on 16,000 byte-level mutations).

Remove when: Pi Durable keeps the scanned range across invocations and decodes entries once per read (upstream's unreleased change) and PiG's per-read cost matches it without the working set, or Pi's `normalizeContext` starts copying.

Approval: the lead assigned D104 on 2026-10-06 under the owner's durable-perf instruction; owner sign-off pending.
SCRUTINIZED:approved

---

## D107 Extension component kit

Stock disposition: required substrate. Go, Rust and Python extension authors get Pi's tui component library for Pi parity of their extension UI, which a subprocess could not pass before. The frontend structure is inert without a frontend member.

What: an extension surface (`ctx.ui.custom`, widgets, header, footer, tool, message and entry renderers) may carry a view: a tree of Pi's tui components (container, box, text, truncated text, markdown, spacer, dynamic border, select list, settings list, image, loader, stacks) with opaque `lines` ranges for custom `render(width)` components. Go, Rust and Python send an authoritative view, which the host renders with PiG's tui ports. The host owns the focused list's state, keeps the keys it binds, and reports its callbacks as `ui.view.event`. The Node runtime reads its real pi-tui components into the same view while a frontend draws, and the host keeps such a view only when rendering it reproduces the frame's lines byte for byte. Image bytes cross once per connection, under bounds. A surface may override a closed set of theme tokens for itself. A D91 frontend receives the view as `Lines.View`, `Overlay.View` and `ToolCard.ResultView`. The full contract is `docs/plan/extension-component-kit.md`.

Conversation kinds (Pi parity; the wire is `pig additive (D107)`): Pi exports its conversation components to extensions (`packages/coding-agent/src/index.ts:435-468`), so a Node extension draws a conversation at main-transcript quality. The kit carries them as `user-message`, `assistant-message` (thinking is its content block), `tool-execution`, `bash-execution` and `diff` (`renderDiff` in a `Text`). The host renders each with the Go port PiG's main transcript uses (`tui.UserMessageComponent`, `tui.AssistantMessageComponent`, a tool card from `codingagent.NewToolRendererCard` with the built-in tool definitions' renderers, `tui.BashExecutionComponent`, `tui.RenderDiff`), so the terminal shows Pi's bytes, and a node with an `id` keeps its component across frames as a Pi author keeps one and calls its update methods. Every SDK builds them (Go `kit.NewUserMessage`, `NewAssistantMessage`, `NewToolExecution`, `NewBashExecution`, `NewDiff`; Rust `kit::UserMessage` and its siblings; Python `kit.UserMessage` and its siblings), and the Node runtime derives them from Pi's own components. A frontend gets the main transcript's nodes for them (`ViewNode.Transcript`: `MarkdownText`, `Thinking`, `ToolCard`; `ViewNode.Diff`). Closures (`markdownTransformers`, a custom markdown theme, an extension's own tool renderers, Pi's `undefined` tool definition) are not expressible; a Node component that uses one stays a `lines` range. See `docs/plan/extension-component-kit.md` §2.1.

Pi source: `packages/tui/src/components/{box,text,truncated-text,markdown,spacer,select-list,settings-list,image,loader,stack,h-stack,v-stack}.ts`, `packages/tui/src/tui.ts` `Container`; `packages/coding-agent/src/modes/interactive/components/dynamic-border.ts`; `packages/coding-agent/src/modes/interactive/theme/theme.ts` `getSelectListTheme`, `getSettingsListTheme`, `getMarkdownTheme`. Conversation kinds: `packages/coding-agent/src/modes/interactive/components/{user-message,assistant-message,tool-execution,bash-execution,diff}.ts`, `packages/coding-agent/src/core/tools/renderers/index.ts` and the built-in tool definitions (`core/tools/{read,bash,edit,write,grep,find,ls}.ts`).

Why: the owner approved closing the "Custom TUI components" gap with a shared component kit rendered by PiG on 2026-10-06, so that every SDK has Pi's components and a frontend such as Tern draws extension UI natively.

Stock behavior: no frontend, so Node derives no view and the host builds no frontend structure. Terminal bytes of every existing extension are unchanged. An authoritative view renders byte-identically to the tui ports (`TestViewKitRendersTheSameBytesAsTheTuiPorts`), and a frame drawn as lines, as lines annotated with their view, or as the authoritative view writes the same terminal bytes (`TestViewsLeaveTheTerminalBytesUnchanged`). Cost on the lines path, before the kit (7c98fbb9f) and after (2dfc8ce0c), 10 runs each under the perf lock on an M4 Pro: `BenchmarkWidgetPushLinesFrame` (a `widget_push` frame decoded, applied and drawn) 1.066 µs and 1.037 µs (no significant change), 7 allocations in both, 336 and 368 B/op; `BenchmarkCustomOverlayLinesFrame` (a `ui.custom.render` frame decoded, applied and drawn) 762 ns and 778 ns (no significant change), 7 allocations in both, 320 and 336 B/op. The added bytes are the wire payloads' new `view` field (a `json.RawMessage` header) in the decoded struct; no allocation was added.

Call-site markers: `coding/extension/host/subprocess/protocol.go` (`ViewPayload`, `ViewNode`, `ViewList`, `NotifyUIViewEvent`, `NotifyUIViewEvicted`, `StatePayload.Frontend`), `coding/extension/host/subprocess/view_kit.go`, `view_kit_conversation.go` (`ConversationRenderers`, the conversation kinds), `internal/codingagent/kit_conversation.go` (the tool cards), `tui/tui_surface.go` (`TranscriptNodes`), `view_images.go`, `view_wire.go` (`acceptView`, `reportViewRejection`), `ui_bridge.go` (`UpdateViewFrame`, `updateWidgetView`, `slotView`, `SetFrontend`), `push_proxy.go`, `render_proxy.go`, `tool_render_proxy.go` (`toolRenderProxy.view`, `FrontendView`), `event_bus.go` (`observeXref`), `host.go` (`SetInboundObserver`, a diagnostic observer of extension frames), `coding/extension/provider_runtime.go` (`SetErrorReporter`, `ReportError`), `coding/extension/host/inproc/runner.go` (`BindCore`), `coding/extension/view_surface.go`, `internal/codingagent/custom_overlay.go`, `internal/codingagent/ext_ui_context.go`, `internal/codingagent/interactive_frontend.go` (`reportFrontend`), `internal/codingagent/interactive_runtime.go` (`attachSubprocess`), `tui/tui_surface_view.go` (`modalOverlay.FrontendView`), `tui/overlay_compositor.go` (`modalOverlay`), `tui/select_list.go`, `tui/theme_overrides.go`, `tui/component_themes.go`, `extensions/sdk/kit`, `extensions/sdk/frontend/view.go`.

Locked by: `TestViewKitRendersTheSameBytesAsTheTuiPorts`, `TestViewKitDrawsConversationKindsWithTheTranscriptPorts`, `TestViewKitReportsConversationTranscriptNodes`, `TestViewKitKeepsConversationComponentsAcrossFrames`, `TestViewKitDisposesToolCardsItNoLongerDraws`, `TestViewKitRejectsInvalidConversationNodes`, `TestViewNodeMessageRoundTrips`, `TestEncodeConversationKinds`, Rust `conversation_kinds_encode_as_the_go_sdk`, Python `test_conversation_kinds_encode_as_the_go_sdk`, `TestNodeViewConversationKinds`, `TestNodeViewPinnedPrivateFields`, `TestConformance_ComponentKitConversationKinds` (every SDK cell, isolated and packed), `TestAssistantMessageHiddenThinkingLabelMatchesPi`, `TestViewKitRoutesBoundKeysToTheFocusedListAndReportsEvents`, `TestViewKitDropsAViewThatDoesNotReproduceItsLines`, `TestViewKitRejectsInvalidViews` (including a `list` whose items differ from its rows or whose selection is out of range), `TestViewKitReportsALinesListToTheFrontend`, `TestViewPayloadRoundTripsTheSpecExample`, `TestUIBridge_SetFrontendPushesTheFlagOncePerChange`, `TestFrontendSessionIsReportedToExtensions` (open and fall back each push the flag; a declined frontend never sets it), `TestFrontendOnlyAnnotationsFollowTheFrontendFlag`, `TestEncodeOmitsFrontendOnlyAnnotationsWithoutAFrontend`, `TestTuiSurfaceTitledOverlayCarriesItsComponentsView`, `TestToolRenderProxyDrawsAndReportsTheRenderersView`, `TestRejectedViewsAreReportedOncePerSurface`, `TestRejectedViewsReachTheRunnersErrorListeners`, `TestHost_InboundObserverSeesEveryExtensionFrameInWireOrder`, the conformance rows `TestConformance_ComponentKitRendersPiComponents`, `TestConformance_ComponentKitWidgetHeaderFooterAndRenderers` and `TestConformance_ComponentKitImagesSendOnce` (inproc-go reference, subprocess-go, fused-go, packed-go), `TestEverySDKReceivesEveryHostNotifyCapability`, the tui SelectList and theme override tests, and the Go SDK kit tests.

Parity allowance: no Pi scenario sends a view; the terminal draws what Pi's components draw.

Remove when: Pi gains a cross-process component protocol.

Approval: owner Michael Kinsy, 2026-10-06 (build now); conversation kinds 2026-10-07.
SCRUTINIZED:approved

---

## D109 Extension SDK upgrade

Stock disposition: required substrate. It classifies a build failure, rewrites source only when the user asks (or turns on `extensionsAutoUpgrade`), and selects no product behavior.

What: PiG compiles a Go extension against the SDK of the running pig, so an SDK release can break an extension written for an earlier one. Pi loads extension source in process and has no SDK to drift from. PiG adds (1) a build-failure classification: a `go build` whose every compile error is an SDK change reports `written for an older SDK` with each changed API's old and new shape (`BuildFailure.Drift`); (2) `pig extension upgrade [<name|path>...] [--all] [--dry-run] [--summary]`, which applies the rules of `extensions/sdk/upgrade` (typed analysis with `go/types`, source edits formatted with `go/format`), backs up the originals under `<config root>/state/extension-upgrade/`, prints a diff, rebuilds and reports per extension; (3) an interactive startup question `Run pig extension upgrade <path>... now? [y/N]` before Pi's exit on an extension load error, which on yes reloads the resources in the same process, with print, JSON and RPC modes printing the command and exiting 1 with unchanged standard output; (4) the global setting `extensionsAutoUpgrade` (default false) that runs the rules at startup and after `pig update`, one line per extension, rules only; (5) a stale-copy warning when two loaded extensions register the same tool and one directory is a copy of the other; (6) `make sdk-apidiff`, which fails an incompatible change to any package of the SDK module since the pinned release that has no rule and no release note.

Pi source: `packages/coding-agent/src/main.ts:919-923` (an extension load error prints and exits; PiG keeps this), `packages/coding-agent/src/core/extensions/runner.ts` (tool conflicts; PiG keeps Pi's error).

Why: the SDK is PiG's own contract and changes between releases. A raw compiler log tells the user nothing about what changed, and most breaks (a getter that returns an error, a field that became a pointer) have one mechanical rewrite. Pi's `extensions` setting is an array, so the flag is the flat `extensionsAutoUpgrade`.

Call-site markers: `coding/cli/extension_upgrade.go`, `coding/cli/extension_upgrade_command.go`, `coding/cli/extension_upgrade_startup.go`, `coding/cli/extension_stale_copy.go`, `coding/cli/main.go` (startup upgrade), `coding/cli/self_update.go` (upgrade after `pig update`), `coding/extension/host/runtimecell/go_failure.go`, `coding/extension/host/runtimecell/go_module.go`, `coding/extension/host/subprocess/builder_listing.go`, `internal/codingagent/settings.go` (`ExtensionsAutoUpgrade`, `GetExtensionsAutoUpgrade`), `extensions/sdk/upgrade/rules.go`.

Locked by: `extensions/sdk/upgrade` fixture tests (one old-extension fixture per past SDK shape: each input upgrades to its golden output, which builds), `TestClassify*` in the same package, `build_failure_sdk_drift_test.go` in `coding/extension/host/runtimecell`, `coding/cli/extension_upgrade_test.go` and `coding/cli/extension_upgrade_startup_test.go` (print mode names the command and writes nothing; auto-upgrade loads the extension; a project file cannot enable it; the interactive question answers yes and no), `coding/cli/extension_stale_copy_test.go`, `internal/codingagent/settings_extensions_auto_upgrade_test.go` and `automation/ci/sdkapidiff/main_test.go`.

Remove when: Pi gains a compiled extension SDK with its own upgrade path.

Approval: owner Michael Kinsy, 2026-10-08 (task ext-upgrade); the setting name `extensionsAutoUpgrade` ruled by the lead for 0.4.2.
SCRUTINIZED:approved
