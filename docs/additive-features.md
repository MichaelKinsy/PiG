# Pig Additive Features

This file documents Pig-only **additive** features that have no upstream Pi
equivalent. These are not behavioral divergences from upstream: upstream simply
doesn't have these capabilities. `DIVERGENCES.md` is reserved for actual
user-visible behavioral deltas where upstream and pig do the same thing
differently (e.g. rendering, command output, prompts).

Each record uses a global `D<N>` ID. Put one short
`pig additive (D<N>)` marker at each implementation point. Keep the full
rationale in this file.

If an upstream behavior changes such that one of these additive features starts
*conflicting* with upstream, promote it to `DIVERGENCES.md` with a numbered D
entry and a SCRUTINIZED tag.

---

## D18 Piglet management and build dispatch

Stock disposition: required substrate. A Piglet cannot supply the parser, resolver, verifier, or builder needed to select and load itself.

What: Pig adds product-neutral Piglet source management, validation, execution,
and build commands. Upstream Pi has no Piglet composition or Piglet Binary
concept.

Current behavior: `pig piglet list|show|validate|schema|add|remove|pull|publish|build` operates on explicit Piglet source or a signed Piglet Binary release. `pig piglet add` accepts local paths, product-contributed catalog refs, npm source packages, and Git refs. Remote sources are materialized without changing Package settings, must pass portable Piglet validation, and write an adjacent origin record containing the exact npm version/integrity or Git commit. Git refs include local `git:file://localhost/<path>` URLs, which upstream parseGitUrl rejects. On Windows a file URL's drive becomes a plain segment of the checkout path (`git/localhost/C/...`), any other path segment containing `:` is refused with upstream's "Refusing to use path outside package install root", and git clones the equivalent `file:///C:/...` because Git for Windows reads the localhost form as a UNC path. JSON inventory includes that origin. A Piglet selects Resources by typed origin, controls ambient discovery, scopes capabilities, and may declare a required agent environment. `piglet build --format binary` resolves exact component closure and can fuse compatible Go factories while preserving the ordinary extension registration contract. A Piglet can set `build.extensionRealization: fused` to reject any selected extension that would otherwise use a subprocess realization. A built Piglet Binary verifies its embedded Piglet, resolution record, executable component plan, and optional Ed25519 DSSE signature before command dispatch. `pig piglet keygen`, `build --sign-key`, `pull`, `verify`, and `trust` provide offline signing, signer continuity, revocation, and a local required-signature policy. `pull` downloads a signed release index and one target asset from HTTPS or GitHub Releases, pins the first signer locally, and publishes no managed file until the index checksum and both signatures verify. `pig piglet publish <name|path> --to github` publishes one GitHub Release named `v<release.version>` with a signed Binary per target, `SHA256SUMS`, and a signed `piglet-release.json` index. It builds each target with a ready signing builder or collects prebuilt signed Binaries with `--artifacts`, lists every target it cannot produce and uploads nothing, refuses an existing release, and checks every staged asset as `pull` would. Publication dry-runs by default and runs `gh release create` only with `--yes`; Pig never handles a GitHub token. `pig verify --provenance` can separately delegate an explicit Sigstore keyless provenance check to the GitHub CLI; startup never makes that network request.
Stock Pig contains the generic command and runtime machinery but selects no
Piglet and activates no product Resources by default.

Installed PiG Packages distribute Resources and never activate Piglets. An npm package used as a Piglet source is materialized directly and is not added to Package settings. Optional catalog and product transports must use public resolver contracts and must not be imported or registered by Stock Pig.

Build diagnostics are product-neutral substrate. Explicit `pig build` and `pig piglet build --format binary` invocations report real phases and elapsed time on stderr. A terminal receives Pi's default loader frames and dim step text; redirected output receives plain phase lines. `--verbose` streams toolchain diagnostics with member labels. Packed members share a compiler invocation and label; fused members compile with the final Go binary. The build does not invent separate fetch or link phases inside a single toolchain invocation. Failure reports name the current phase, retain a bounded diagnostic tail, and suggest remediation. Success reports the artifact path, byte size, and elapsed time only after verification and record publication. The progress observer is absent from ordinary extension startup and does not change cache identity, compiler inputs, or extension APIs.

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
- `coding/piglet/`
- `coding/pigletbuild/`
- `cmd/pig/agent_environment_test.go`
- `cmd/pig/build_command_test.go`
- `cmd/pig/build_progress_test.go`
- `internal/buildprogress/reporter_test.go`
- `media/demo/tests/test_demo.py`
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
across SDKs and validated by `tests/extension-conformance/conformance_test.go`,
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
- `tests/extension-conformance/conformance_test.go` covers Go/Rust/Python
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

Stock disposition: required substrate. Packing is a transparent host optimization and must remain behaviorally identical to isolated execution.

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
same type-stripping loader an isolated Node extension uses reads each member's
TS/JS source fresh at process start (N8; matches upstream pi hosting every
extension in one process, `packages/coding-agent/src/core/extensions/loader.ts`).
Each contained extension still gets its own Unix socket and runs the
**same** `register` handshake: there is no multi-register payload. `Host.Reload` loads each extension on its own as upstream does: a failed
extension is reported and not loaded, a failed packed cell restages its members
in isolated cells, and a quarantined packed cell fissions
its members back into generated isolated cells on the next reload.

Why: pi's in-process model gives one runtime per extension for free. Pig
must spawn a process per extension by default, which is expensive when
authors register many small factory-style SDK extensions; for Node this was
especially costly (a 150-210 MB Node process per TS/JS extension). The
packed-cell substrate is a transparent optimization that preserves the same
wire per extension, preserves the upstream-equivalent extension API, and
degrades to isolated cells under crash quarantine. It is not a new authoring
model - factory extensions are still ordinary subprocess extensions; the host
just shares the OS process when it is safe.

Skip conditions / non-pack triggers:
- the selected source is a standalone (including a shebang Node script);
- source resolution does not prove one exact standard factory;
- the extension's isolation is set to anything other than empty/`shared-ok`
  (the `--isolated`/`isolation: strict` escape hatch);
- the cell is quarantined, which fissions its members on reload.

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
- `coding/extension/host/subprocess/packed_quarantine.go`
- `coding/extension/host/subprocess/source_resolver.go` (node factory defaults
  to `shared-ok`)
- `coding/extension/host/runtimecell/go_packed.go`
- `coding/extension/host/runtimecell/rust_packed.go`
- `coding/extension/host/runtimecell/python_packed.go`
- `coding/extension/host/subprocess/runtime-node/cell.mjs`
- `coding/extension/host/subprocess/runtime-node/state.mjs` (AsyncLocalStorage
  scoping so a Node cell's shim calls resolve to the right extension)

Tests:
- `coding/extension/host/runtimecell/{go,rust,python}_packed_test.go`
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
This is distribution infrastructure, not an extension, and it activates no
optional Product Resource.

Remove when: every supported Stock Pig distribution installs the matching docs
beside the binary through another verified mechanism.

Call-site markers:
- `internal/pigdocs/pigdocs.go`
- `cmd/pig/main.go`
- `internal/codingagent/prompts/coding.go`

Tests:
- `internal/pigdocs/pigdocs_test.go`
- `internal/codingagent/auth_guidance_test.go`
- `internal/codingagent/prompts/coding_test.go`

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
`extension.Context`. It does not change what extensions see: the wire's
`getAllTools` answers with upstream's `ToolInfo`, whose `sourceInfo` is the
registering extension's, as `pi.getAllTools()` does.

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
- `tests/extension-conformance/conformance_test.go`: cross-SDK source
  round-trip
- `tests/upstream-parity/context_parity_test.go`: `pigOnlyContextMembers`

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

Conformance gate: every shared cross-SDK behavioral harness includes `fused-go` beside subprocess Go. The canonical Go fixture factory drives both realizations, so tool, command, event, UI, model, terminal-input, geometry, source-metadata, OAuth, user-content, and liveness recordings fail on fused-only drift. `TestConformance_TransportsMatch`, `TestLivenessConformanceSDKsMatch`, and the focused conformance rows in `tests/extension-conformance/` enforce the shared protocol behavior.

Fused code shares Pig's process-global state. Before a Piglet Binary build links a factory, `coding/pigletbuild` inspects the factory package and its local module dependency closure. The build fails with `PIGLET_FUSED_PROCESS_HAZARD` when fused code calls `os.Exit`, `os.Chdir`, `log.Fatal*`, `fmt.Print*`, or accesses `os.Stdout`. Authors must return errors and use extension host/UI APIs instead of terminating the process, changing its working directory, or writing into terminal output.

Call sites:
- `coding/extension/host/subprocess/host.go`: `LoadInProcess`, `connectExt`
  (fuse branch), `adoptConn` (shared handshake).
- `extensions/sdk/extension.go`: `RunWithConn` (serve over an established conn).
- Consumer: Piglet Binary fused registration (dormant in stock Pig).

Remove when: the Piglet Binary component plan no longer supports fused realization.

Locked by: the shared `fused-go` rows in `tests/extension-conformance/`; `TestBuildNativeArtifactRejectsFusedProcessHazards` and `TestVetFusedPackagesInspectsLocalDependencyClosure` in `coding/pigletbuild`; and `TestHost_LoadInProcess`, `TestAC59FusedFocusedComponentUsesProtocolV1`, `TestFusedFocusedTimerInvalidatesAndCleansUp`, and `TestHost_LoadInProcess_NilServe` in `coding/extension/host/subprocess/host_inprocess_test.go`.

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
can re-layout. `TestConformance_Geometry` asserts all four SDKs report the
height delivered by a notification; the parity matrix records the surface
as complete (pig-additive).

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
(`SetHeightFunc`, `readyGeometry`, `NotifyHeight`),
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
the height delivered by a notification; the parity matrix records the
surface as complete (pig-additive).

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
- `tests/extension-conformance/conformance_test.go`
- `coding/extension/host/subprocess/runtime_node_login_test.go`
- `cmd/pig/extension_login_preview_command_test.go`

PORT_MAP path: n/a, because this is a downstream extension API.
SCRUTINIZED:approved

