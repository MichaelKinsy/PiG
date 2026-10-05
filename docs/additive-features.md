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

Evidence: `TestPublishGitHubNamedNamespace` and `TestNamedGitHubReferenceEscapesTag` fail on unprefixed-only publishing/pull; `TestAddMonorepoPinnedClosure` fails on the prior local-origin refusal; `TestUpdateCommandRoutesBeforeSession` fails on the absent update command. `TestMonorepoPublishPullUpdateIsolation` publishes two names at the same versions through fake `gh`, serves their exact uploaded bytes through a local HTTPS GitHub transport, and checks named updates, mismatched identities, tampering, signer rotation, revocation, rollback, and cancellation. `TestPigletAddPinnedMonorepoThroughCoreMaterializer` uses a real local bare Git repository and removes the materialized checkout before verifying both registered closures. upstream 0.99.1 has no Piglet release counterpart: `packages/coding-agent/src/package-manager-cli.ts:375-385` recognizes only install/remove/uninstall/update/list as Package verbs, and `packages/coding-agent/src/cli/args.ts:253-254` puts other positional words into prompt messages. Piglet evidence is additive, not a paired Pi coverage claim. A compiling mutation that clears the discovery prefix makes `TestDiscoverGitHubVersionPagesNamespaceAndStableVersions` select unprefixed `7.0.0` instead of namespaced `2.0.0` and fails the publisher-to-update test. The restored code passes both. `TestUpdateUnprefixedRepositoryCannotInstallAnotherPiglet` reproduces an unprefixed update installing `beta` when `alpha` was requested; update now binds the installed name independently of the tag namespace before asset download. `TestPullCancellationAfterDownloadDoesNotPublish` also fails on a completed download that previously published after cancellation; pull now checks cancellation before verification and again under the publication lock. The cross-family `cli-utils/04-package-install-local` comparator is `output_equal` at three runs after probing Pi, then pinned at 0.87.1.

Resource disposition: release HTTP requests own response bodies, use bounded reads, and inherit caller cancellation; staged Binary files and failed publication directories are removed. Source closure collection closes every file and directory, reads directories in bounded batches, caps retained file bytes, and installs no source file until validation completes. These operations run in explicit pre-session CLI commands, not on the TUI loop. `BenchmarkPigletClosure` covers sixteen 64-KiB files. A Linux/amd64 Go 1.27.1 sample reports 1.78 ms/op, 2.24 MB/op, and 624 allocations/op; CPU and allocation profiles identify runtime GC/syscalls and `io.ReadAll` respectively. This is a resource baseline, not a speedup claim. Reproduce with `go test ./coding/piglet -run '^$' -bench '^BenchmarkPigletClosure$' -benchmem -cpuprofile /tmp/piglet-closure-cpu.pprof -memprofile /tmp/piglet-closure-mem.pprof -o /tmp/piglet-closure.test`.

Installed PiG Packages distribute Resources and never activate Piglets. An npm package used as a Piglet source is materialized directly and is not added to Package settings. Optional catalog and product transports must use public resolver contracts and must not be imported or registered by Stock Pig.

Build diagnostics are product-neutral substrate. Explicit `pig build` and `pig piglet build --format binary` invocations report real phases and elapsed time on stderr. A terminal receives Pi's default loader frames and dim step text; redirected output receives plain phase lines. `--verbose` streams toolchain diagnostics with member labels. Packed members share a compiler invocation and label; fused members compile with the final Go binary. The build does not invent separate fetch or link phases inside a single toolchain invocation. Failure reports name the current phase, retain a bounded diagnostic tail, and suggest remediation. Success reports the artifact path, byte size, and elapsed time only after verification and record publication. The progress observer is absent from ordinary extension startup and does not change cache identity, compiler inputs, or extension APIs.

The native builder compiles the PiG checkout that contains the working directory or `PIG_SOURCE_ROOT`. Without one, a release binary fetches exactly its own version, `github.com/MichaelKinsy/PiG@v<PigVersion>`, with `go mod download -json`. The running version is a release when its build info names that module version (`go install …@v<version>`) or when the release workflow stamped `coding/pigletbuild.releaseSourceVersion`, because release archives are built from source without VCS metadata. A development build keeps the `source-unavailable` result and its checkout remedy. `GOPROXY`/`GOSUMDB` verify the download and `GOMODCACHE` caches it; no Git clone or temporary checkout is made. The Go command refuses a build overlay beneath `GOMODCACHE`, so the module tree is staged once, by hard link or copy, under `~/.pig/cache/pig-source/<version>-<sum-key>` and published by rename. The build runs with `GOWORK=off` because the module zip omits the workspace's nested modules. Probe stays side-effect free: it reports ready with code `source-fetchable`, and Build performs the download and prints `fetching PiG <v> source (cached after first build)`. The Binary record's source revision is the proxy-reported commit, or the module version when the proxy reports none, and its source digest hashes the staged tree. Evidence: `TestNativeProbeReleaseWithoutCheckoutIsFetchableWithoutDownloading`, `TestNativeProbeDevBuildWithoutCheckoutIsSourceUnavailable`, `TestSelectBuilderAutoSelectsFetchableNative`, `TestResolveSourceFetchesExactlyTheRunningRelease`, `TestResolveSourceDevBuildKeepsSourceUnavailableRemedy`, `TestResolveSourceReportsDownloadFailureWithRemedy`, `TestResolveSourceRejectsAnotherModule`, and the network-gated `TestGoModDownloadFetchesPublishedPigSource`.

The built-in `container` builder follows the native builder in auto selection and is selected explicitly with `--builder container`; a configured builder cannot take that name. `PIG_CONTAINER_ENGINE` selects `docker` or `podman`; otherwise Podman is preferred over Docker. It runs the digest-pinned public Go base image of `automation/images/ci-go/Dockerfile` with `--pull=missing`, mounts a host Go module/build cache from `~/.pig/cache/container-go`, installs exactly the running release with `go install github.com/MichaelKinsy/PiG/cmd/pig@v<PigVersion>`, and runs that release's native builder, which fetches its own source. The host needs neither Go nor PiG source, and any `linux/<arch>` the engine runs can be targeted. The image has only the Go toolchain, so Rust extension cells need a configured builder image with Cargo. Probe runs only `<engine> info`; it does not pull. A development build reports `source-unavailable` because no matching release can be installed. Like configured container builders, it refuses `--sign-key`. Nested input mountpoints are created in the host input directory because an engine cannot create them inside the read-only `/input` mount. Evidence: `TestDefaultContainerEngineSelection`, `TestDefaultContainerProbeIsReadyWithoutPulling`, `TestDefaultContainerProbeNotReady`, `TestSelectBuilderAutoUsesContainerWhenNativeIsNotReady`, `TestDefaultContainerBuildInstallsRunningRelease`, `TestDefaultContainerImageIsTheCIGoImageBase`, and `TestCreateContainerMountpointsUnderReadOnlyInput`.

`pig piglet prune [--keep <n>] [--max-size <size>] [--dry-run]` removes built Piglet Binaries from the managed artifact store. Each build is kept forever otherwise, and a Go binary is tens of megabytes. The newest `n` builds of each Piglet and target (default 2, by record time) are never removed, because one build for several targets records one Binary per target. Without `--max-size` every other build goes, oldest first; with it they go oldest first only until the store is within the size, and the command reports that the limit is missed when protected builds alone exceed it. A removal takes the artifact, its Binary record, and its resolution record unless a remaining Binary uses it, together. Installs pulled from a signed release, which a `current` pointer names, are never touched. Nothing prunes this store automatically.

Remove when: upstream Pi provides equivalent composition and immutable build
artifacts, or Pig drops Piglet support.

Call-site markers:
- `cmd/pig/main.go`
- `cmd/pig/build_command.go`
- `cmd/pig/package_commands.go`
- `coding/piglet/`
- `coding/pigletbuild/`
- `internal/buildprogress/`
- `coding/extension/host/runtimecell/go_packed.go`
- `coding/extension/host/runtimecell/rust_packed.go`
- `coding/extension/host/subprocess/builder.go`
- `coding/extension/installresolver/registry.go`

Tests:
- `coding/piglet/` (`prune_test.go` for `pig piglet prune`)
- `coding/pigletbuild/`
- `cmd/pig/agent_environment_test.go`
- `cmd/pig/build_command_test.go`
- `cmd/pig/build_progress_test.go`
- `internal/buildprogress/reporter_test.go`
- `cmd/pig/package_commands_test.go`

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
- `cmd/pig/extensions_cache_command.go`
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
- `internal/toolchain/resolve_test.go`, `coding/extension/host/runtimecell/build_failure_report_test.go`, `coding/extension/host/subprocess/{build_line,build_failure_group}_test.go`, `cmd/pig/extension_build_diagnostics_test.go`, and `cmd/pig/extensions_cache_command_test.go` (`TestAutomaticExtensionCacheGCEnforcesTheSizeLimitOldestFirst`)
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
- `cmd/pig/main.go`
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
- `cmd/pig/extension_validate_command.go` (`extensionValidationReport`,
  `toolDetailsFromExtension`, `commandDetailsFromExtension`)
- `cmd/pig/package_commands.go` (`--validate-only`/`--check` dispatch,
  `validateInstallSources`)

Tests:
- `cmd/pig/extension_validate_command_test.go`
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

Fused code shares Pig's process-global state. Before a Piglet Binary build links a factory, `coding/pigletbuild` inspects the factory package and its local module dependency closure. The build fails with `PIGLET_FUSED_PROCESS_HAZARD` when fused code calls `os.Exit`, `os.Chdir`, `log.Fatal*`, `fmt.Print*`, or accesses `os.Stdout`. Authors must return errors and use extension host/UI APIs instead of terminating the process, changing its working directory, or writing into terminal output.

Call sites:
- `coding/extension/host/subprocess/host.go`: `LoadInProcess`, `connectExt`
  (fuse branch), `adoptConn` (shared handshake).
- `extensions/sdk/extension.go`: `RunWithConn` (serve over an established conn).
- Consumer: Piglet Binary fused registration (dormant in stock Pig).

Remove when: the Piglet Binary component plan no longer supports fused realization.

Locked by: the shared `fused-go` rows in `test/extension-conformance/`; `TestBuildNativeArtifactRejectsFusedProcessHazards` and `TestVetFusedPackagesInspectsLocalDependencyClosure` in `coding/pigletbuild`; and `TestHost_LoadInProcess`, `TestAC59FusedFocusedComponentUsesProtocolV1`, `TestFusedFocusedTimerInvalidatesAndCleansUp`, and `TestHost_LoadInProcess_NilServe` in `coding/extension/host/subprocess/host_inprocess_test.go`.

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
- `coding/model.go`, `cmd/pig/model.go` thread `ModelEntry.Insecure` into the
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
- `cmd/pig/auth_commands.go`: target listing and credential-store dispatch.
- `cmd/pig/package_commands.go`: pre-session settings use saved/default project trust without invoking extension handlers.
- `cmd/pig/auth_contributions.go`: registration-only inspection and owner-only
  login startup.
- `coding/extension/host/subprocess/host.go`: runtime provider ownership.
- `coding/extension/host/cellpack/` and fused host paths: embedded and fused
  registration.

Locked by: `cmd/pig/auth_commands_test.go` runtime inspection, projection-hit,
duplicate-owner, no-handler, login-owner, and embedded tests;
`cmd/pig/auth_embedded_packed_test.go`
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
- `cmd/pig/package_inventory.go`: additive Package JSON inventory.
- `cmd/pig/status_command.go`: typed aggregate inventory, health, and paths.
- `coding/piglet/resolve.go`: source/Piglet Binary inventory
  and record/artifact validation.

Locked by: `cmd/pig/package_inventory_test.go`, `cmd/pig/status_command_test.go`,
`cmd/pig/configured_resources_test.go`
(`TestProjectConfigPackageOverrideIsDeltaOverInheritedGlobalPackage` and
canonical cross-scope source identity cases), and
`coding/piglet/cli_test.go` /
`coding/piglet/resolve_test.go`. Upstream Package-list behavior
is locked by `cmd/pig/package_commands_test.go`
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
- `cmd/pig/extension_login_preview_command.go`

Tests:
- `coding/extension/login_test.go`
- `internal/codingagent/login_header_test.go`
- `coding/extension/host/subprocess/ui_bridge_test.go`
- `test/extension-conformance/conformance_test.go`
- `coding/extension/host/subprocess/runtime_node_login_test.go`
- `cmd/pig/extension_login_preview_command_test.go`

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

