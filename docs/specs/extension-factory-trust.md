# Extension factory trust split

Status: approved with fixes by the Opus spec review of 2026-10-08 (`Review record` below). Step 2 may start under this text. The embedder entry point (`Embedder entry point`) needs the owner's confirmation before it is implemented.

Owner ruling B (2026-10-08): compiled-in extensions (first-party, built into the pig binary) may use Pi's in-process `ExtensionFactory` form `func(api ExtensionAPI)`, and `createCodemodeExtension` and `createToolSearchExtension` are built on it as Pi builds them. Third-party extensions stay subprocess-only. The factory form must be unreachable from the subprocess loader, enforced in code and by a test. UI component factories: compiled-in extensions get Pi's `(tui, theme) => Component`; subprocess extensions get a declarative component description over RPC in phase 2.

Upstream reference (Pi 1.1.0, `.upstream/current/packages/coding-agent/src/`):

- `core/extensions/types.ts:2017` (`ExtensionFactory`), `:2019-2045` (`InlineExtension`), `:1559` (`ExtensionHandler`), `:1564-1890` (`ExtensionAPI`), `:143` (`EditorFactory`), `:149-300` (`ExtensionUIContext`).
- `core/extensions/loader.ts:157-238` (`createExtensionRuntime`), `:244-548` (`createExtensionAPI` with `commit` and `discard`), `:613-632` (`initializeExtension`), `:662-671` (`loadExtensionFromFactory`).
- `core/resource-loader.ts:281,376-378` (`DefaultResourceLoaderOptions.extensionFactories`), `:714-745` (`loadExtensionPaths`, `Unknown built-in extension`), `:1131-1153` (`loadExtensionFactories`, `<inline:N>` naming).
- `core/package-manager.ts:970-1000` (built-in selection by settings and `-e builtin:<name>`).
- `extensions/index.ts` (`builtInExtensions`: llama.cpp, codemode, tool-search, mcp), `extensions/codemode/index.ts:31-48`, `extensions/tool-search/index.ts:12-18`, `extensions/mcp/index.ts`.
- `main.ts:569-575` (`MainOptions.extensionFactories`), `package-manager-cli.ts:746-748,770,788,855-866` (`PackageCommandRuntimeOptions.extensionFactories`), `index.ts:103,115,407-413` (public exports).
- Tests: `test/suite/regressions/8423-extension-factory-failure.test.ts`, `test/suite/regressions/6260-inline-extension-naming.test.ts`, `test/suite/agent-session-codemode.test.ts`, `test/tool-search.test.ts`, `test/llama-extension.test.ts`, `test/mcp-extension.test.ts`, the `extensionFactories` cases of `test/resource-loader.test.ts`.

## Decision

PiG has two classes of extension code with different trust. They share one API (`extension.API`, Pi's `ExtensionAPI`) and one registration result (`extension.Extension`), and they never share a loader.

| | Compiled-in extension | Subprocess-form extension |
|---|---|---|
| Code | Go code that the author of the binary's `main` package passes to the loader by value | A file or Package loaded at run time, or a Piglet member fused at build time (D31) |
| Trust | The author of `main` reviewed it and chose to link it. It runs with the host's authority | Untrusted by default. Isolated by process, socket, project trust, and the wire protocol. A fused member shares the process (D31) but keeps the wire contract |
| Form | Pi's `ExtensionFactory`: `func(api API) error`, called with a live `ExtensionAPI` | The registration handshake of `coding/extension/host/subprocess/protocol.go`, through an SDK (`sdk.Factory` for Go) |
| Loader | `LoadExtensionFromFactory` (in-process, no wire) | the subprocess host (`connectExt`, `adoptConn`, `LoadInProcess`) |
| UI components | Pi's typed factories: `WidgetFactory`, `HeaderFactory`, `FooterFactory`, `EditorFactory`, `CustomFactory` | Declarative component description over RPC (phase 2), and the existing frame push |

Everything upstream does with an in-process `ExtensionFactory` is available to compiled-in code in Pi's shape: `CreateCodemodeExtension()`, `CreateToolSearchExtension()`, `CreateMcpExtension()`, the llama.cpp built-in, and the `InlineExtension` list. Subprocess-form extensions keep their current shape and gain the declarative component form in phase 2.

### Who is first party

First party is defined by linkage, not by authorship of the extension code. A factory is compiled-in only when the source of the binary's `main` package passes it to the loader as a value:

- Stock `pig`: `cmd/pig` passes the rows of `coding/extension/builtin` (codemode, tool-search, mcp, pig-login) and the llama.cpp row. These rows are PiG source, reviewed in this repository.
- An embedding Go program that imports PiG as a library and writes its own `main` (Pi's `main(args, { extensionFactories })`). See `Embedder entry point`.

A Piglet is never first party for this purpose, including PiG Standard and any Piglet the owner writes. A Piglet Binary's `main` is `cmd/pig`; the Piglet builder (`coding/pigletbuild`) adds code only through two generated registries, `coding/extension/host/fusepack/registry_generated.go` (members typed `sdk.Factory`, D31) and `internal/frontendpack/registry_generated.go` (one `func() frontend.Frontend`, D91). Neither registry reaches the factory loader or the built-in table. A first-party extension that needs the factory form lands as a built-in row in PiG source; a Piglet may then select it by name (`builtin:<name>`), which selects reviewed code and supplies none.

## Why the split is safe

The rule that stays absolute: **no input that reaches the subprocess loader can produce a compiled-in factory, and no run-time or Piglet input can add compiled-in code.** Five properties enforce it.

1. **Linkage-only membership.** A compiled-in factory exists only as a Go value that `main` passes down. The factory list is built by value in `main` (`builtin.All(options)` returns a fresh slice from fixed source; the llama.cpp and embedder rows are literals in `main`) and threaded explicitly to the loader. There is no package-level mutable registry of factories, no exported `Register` function, and no `init`-time registration, so a linked package (including a fused member's `init`) cannot add a row. Run-time inputs may only select or disable a registered built-in by name, exactly as Pi does: the `extensions` setting (`-builtin:<name>`, project overrides) and `-e builtin:<name>` (`package-manager.ts:970-1000`), and `--no-extensions`. An unknown name fails with `Unknown built-in extension: builtin:<name>` (`resource-loader.ts:723-727`). No Package manifest entry, file path, wire message, or Piglet field selects a built-in or supplies code. Build tags only remove rows (`pig_strip_<name>`, D92); no build tag or runtime flag adds the factory form.
2. **No wire form.** The protocol has no message that registers, serializes, or requests a factory. An SDK registration describes tools, commands, flags, and handlers as data. The host never calls back into a function value it received from a peer. A UI factory the host builds for a subprocess extension (`FrameHeader`, `FrameFooter`, `RemoteEditorFactory`) is host code that renders wire data.
3. **No import or symbol path.** The loader (`LoadExtensionFromFactory`, the registering `API`, `InlineExtension` handling) lives in a package that the subprocess side does not import transitively. It must not live in `coding/extension` or `coding/extension/host/inproc`, because `coding/extension/host/subprocess` imports both. The test runs `go list -deps` on `coding/extension/host/subprocess`, `.../runtimecell`, `.../cellpack`, `.../fusepack`, `.../invocation`, `coding/extension/source`, `internal/frontendpack`, `extensions/sdk/...` and fails when the set contains the loader package, `coding/extension/builtin` or a built-in implementation package (`builtin/codemode`, `builtin/toolsearch`, `coding/mcpext`, the llama extension). A second check type-checks the same packages and fails when any non-test file references `extension.ExtensionFactory` or `extension.InlineExtension`, because the type itself lives in `coding/extension` beside `API`, as Pi exports it beside `ExtensionAPI`.
4. **Piglet builds cannot reach it.** The Piglet builder's Go-source writes are exactly the two generated registries named above, plus `go.mod`, `go.sum`, `go.work.sum` and baked data. `fused_vet` (`coding/pigletbuild/fused_vet.go`, which already walks a member's local dependency closure) rejects a fused or frontend member that imports the loader package or `coding/extension/builtin`, or references `extension.ExtensionFactory` or `extension.InlineExtension`. This is a contract boundary, not memory isolation: a fused member already shares the host's address space by D31's own terms. It keeps every Piglet extension on the one wire contract, so its isolated, packed, and fused realizations stay observationally identical.
5. **Negative test.** See `Required tests`, test 5.

D31 (fused in-process runtime for Piglet Binaries, `docs/additive-features.md`) stays what it is: a Go SDK extension (`sdk.Factory`, `func() *sdk.Extension`) serving the wire protocol over an in-memory pipe. It is a subprocess-form extension with a different transport, and it does not use the factory form. D91 frontend members are not extensions. This specification neither widens nor replaces D31 or D91.

## Compiled-in factory form

```go
// ExtensionFactory is Pi's ExtensionFactory (types.ts:2017): it receives the ExtensionAPI and registers
// tools, commands, flags, shortcuts, providers, and event handlers on it.
type ExtensionFactory func(api API) error

// InlineExtension is Pi's InlineExtension (types.ts:2019-2045). An empty Name is Pi's bare-factory form.
type InlineExtension struct {
	Name        string
	Factory     ExtensionFactory
	Hidden      bool
	Replaceable bool
	Builtin     bool
}
```

Both types live in `coding/extension`, beside `API`. The loader does not.

- **API.** `API` is the existing `coding/extension.API`, the port of `ExtensionAPI` (types.ts:1564-1890). No production implementation of `extension.API` exists today; only `extensiontest.Fake` implements it. Step 2 ports Pi's `createExtensionAPI` (loader.ts:244-548) as that implementation, on the existing `extension.ExtensionRuntime` (`CreateExtensionRuntime`, `coding/extension/provider_runtime.go`). A compiled-in extension observes Pi's API semantics. The Go SDK (`extensions/sdk`) has its own API type and does not implement `extension.API`; the conformance suite, not a shared Go interface, keeps the two equal.
- **Load transaction.** Registration methods write to the extension record. During loading, flag defaults and runtime changes are pending. On success the loader commits them; on a factory error it discards them, unsubscribes the event-bus subscriptions made during loading, and marks the API failed, so a later call on a captured API fails with `Extension "<path>" failed to load and its API is no longer active.` (loader.ts:532-548, 8423 test 1).
- **Action methods.** `SendMessage`, `AppendEntry`, `GetAllTools`, `SetActiveTools`, `GetSettings`, and the other runtime actions fail with Pi's `Extension runtime not initialized. Action methods cannot be called during extension loading.` until the runner binds its core (loader.ts:157-190). Provider and virtual-model registrations queue before bind, as `pendingProviderRegistrations` does.
- **Lifetime.** The API a factory receives stays live after loading: handlers and tools call it later. On `/reload` and every Session replacement the factory runs again against a fresh runtime, and a captured API from the old runtime fails with Pi's stale-context message (`runtime.invalidate`, loader.ts:193-203). PiG keeps one runtime across a `/reload` when a Host or the previous runner holds it, so loading a compiled-in extension again on that runtime retires the API of its earlier load (`ExtensionRuntime.ReplaceFactoryAPI`): its calls fail with the same stale message and its event-bus subscriptions end. A Session replacement builds a new runtime and invalidates the old one. D30's subprocess exception does not apply to compiled-in factories.
- **Naming.** A built-in loads as `builtin:<name>` and is hidden. A named inline extension loads as `<inline:name>`; a bare factory (empty `Name`) loads as `<inline:N>`, 1-based in list order, and is never hidden or replaceable (resource-loader.ts:1138-1146, 6260 test). The loader stamps source info on the commands and tools a factory registers, as `runExtensionFactory` does today (resource-loader.ts `applyExtensionSourceInfo`).
- **Errors.** A factory error is a load error for the extension path; Pi's message is the error text, or `failed to load extension` when there is none. A panic in a factory is recovered on the loading goroutine into that load error, because Pi catches every exception a factory throws (loader.ts:623-629). The recovery records the panic value and is marked as the upstream catch for the divergence guard.
- **Async contract** (AGENTS.md, TypeScript async/Promise parity). Upstream awaits the factory (`void | Promise<void>`; loader.ts:624 `await factory(load.api)`). The Go factory blocks and returns its error. The resource loader calls factories on the loading goroutine, sequentially, in Pi's order (`loadCurrentExtensionSet` and `loadFinalExtensionSet`, resource-loader.ts:676-789): built-in paths load in the final pass after project trust resolves, after the file paths of that pass (`loadExtensionPaths`, :714-745), and inline factories load in the pre-trust pass when there is one and otherwise in the final pass; `omitReplacedExtensions` runs on the ordered result. The existing order tests in `cmd/pig` (`builtin_extension_order_test.go`) stay green. `LoadExtensionFromFactory` is also safe for concurrent calls on one runtime, and each call commits or discards only its own pending changes (8423 test 2). The factory takes no `context.Context`, because Pi's factory receives no signal; slow work belongs in handlers that receive one.
- **Precursors.** `inlineExtension.Factory func() (extension.Extension, error)` (`coding/cli/extension_set.go`), `builtin.Extension.Factory` (`coding/extension/builtin/builtin.go`), and `packageCommandRuntimeOptions.extensionFactories` (`coding/cli/package_command_trust.go`, Pi `package-manager-cli.ts:746-748`) are the record-returning precursors. Step 2 replaces all three with `ExtensionFactory` and `InlineExtension`. The built-in table keeps `Replaceable` beside the factory and loads every row with `Builtin` semantics.
- **MCP.** `CreateMcpExtension(options) func(api API)` (`coding/mcpext/register.go`) has the factory shape over a narrower `mcpext.API` whose `On*` methods return nothing. It becomes `func(options) extension.ExtensionFactory` over `extension.API`, whose `On*` methods return the unsubscribe function. Re-derive its interface row after the change.

## Built-ins on the factory form (step 2)

Every built-in row moves to the factory form, so there is one mechanism: codemode, tool-search, mcp, llama.cpp, and pig-login (D2).

`CreateCodemodeExtension(options)` and `CreateToolSearchExtension()` are rebuilt as Pi builds them (`extensions/codemode/index.ts:31-48`, `tool-search/index.ts:12-18`): a factory that calls `api.RegisterTool` with the tool definition and an inactive `DefaultActive`, and reads `api.GetSettings()`, `api.AppendEntry`, and `api.GetAllTools()` for the codemode callbacks. Codemode reads the settings through the API on each use, so the `ConfigureCodemode` `GetSettings` callback goes away. The native engine, prelude, and limits stay as `docs/specs/builtin-codemode-tool-search.md` decides. The record builders `toolsearch.Extension()` and `codemode.Extension()` go away once nothing calls them.

## Embedder entry point

Pi exports `main(args, { extensionFactories })` (`index.ts:413`, `main.ts:569-575`), `DefaultResourceLoaderOptions.extensionFactories` (`resource-loader.ts:281`), and `PackageCommandRuntimeOptions.extensionFactories`. In PiG these are the entry points for an embedding program's own `main`.

- `DefaultResourceLoaderOptions` gains `ExtensionFactories []extension.InlineExtension` beside its `LoadExtensions` hook. That is the minimum public entry, and the loader package implements it.
- `Main(args, MainOptions)` needs an importable package, because `cmd/pig` is `package main`. It is a separate change after step 2.
- Classification: inert capability. Stock `pig` passes only its built-in rows. Only a program whose own `main` the embedder writes can pass factories. A Piglet cannot select or supply this entry.
- Owner confirmation needed: ruling B names "the pig binary". An embedding program builds a different binary. This is Pi parity (`index.ts:413`), and the embedder is first party to its own binary, but the owner confirms it before `ExtensionFactories` or `Main` is implemented. The built-ins of step 2 do not depend on it.

## UI component factories

Pi's `ExtensionUIContext` takes component factories where the extension builds a view from the live `tui` and `theme` (types.ts:149-300): `setWidget(key, (tui, theme) => Component & {dispose?()}, options?)`, `setFooter`, `setHeader`, `setEditorComponent(factory | undefined)`, `getEditorComponent()`, `custom(factory, options?)`.

**Compiled-in (step 3).** Commit `e423b16423` already landed the typed members on `extension.UIContext`: `SetWidgetFactory(key, WidgetFactory, opts)` (setWidget's factory overload), `SetHeader(HeaderFactory)`, `SetFooter(FooterFactory)`, `SetEditorComponent(EditorFactory)` and `GetEditorComponent() EditorFactory`, where `EditorFactory` is `func(tui.TUI, tui.EditorTheme, KeybindingsManager) tui.EditorComponent` (Pi's `EditorTheme`, types.ts:143), and `CustomFactory` for `Custom`. Step 3 is therefore: prove that a compiled-in factory reaches each of these members through the production interactive host and its install path, and close the remaining gap that change recorded (an in-process editor component that repaints on its own timer needs a host render hook). `Custom` keeps `factory any`, because it also carries the subprocess description. Factories run on the UI loop, and slow work stays off it (tui/AGENTS.md).

**Subprocess (phase 2).** A function cannot cross the process boundary. The extension describes its component declaratively: a tree of host-owned primitives (text, box/container, spacer, markdown, select, input) with properties and event names, sent over RPC, rendered and laid out by the host with the real `tui` and theme, with events routed back to the extension as messages. The current frame push (`ui.widget.push`, editor surrogates) remains for the SDKs that already use it until a conformance row shows the declarative form reproduces it. Phase 2 needs its own specification (primitive set, event model, invalidate/resize semantics) before code.

## Classification (AGENTS.md product-extension boundary)

- Factory form, loader, built-in table, `InlineExtension`: **required substrate** in Stock PiG. Pi ships them.
- The embedder entry point: an **inert capability**. Stock PiG's `main` passes only its built-in rows. Only an embedding program's own `main` selects it, at build time. A Piglet cannot.
- No Standard behavior is activated. No product UI moves into `internal/` or `cmd/`.

## AGENTS.md amendment

`AGENTS.md` and `docs/extension-authoring.md` forbid a "linked/in-process production extension runtime" and a "general dynamic linked/in-process extension loader". Both prohibitions target extension code from outside the binary's own source. The amendment keeps them and states the exception exactly:

> The compiled-in extension factory form of `docs/specs/extension-factory-trust.md` is permitted: an `extension.ExtensionFactory` that the source of the binary's own `main` package passes to the loader by value (Stock PiG's built-in rows, or an embedding program's own factories). A Piglet member, including a D31 fused extension, never uses it. Settings and `-e builtin:<name>` may select or disable a registered built-in by name, as Pi does. No user, project, Package, file, settings entry, Piglet, or wire peer can supply or add factory code.

`docs/extension-authoring.md` distinguishes the Go SDK factory (`sdk.Factory`, the authoring form for every Package and Piglet extension) from `extension.ExtensionFactory` (compiled-in only). The prohibitions on WASM, embedded JavaScript, `plugin.Open`, and multi-register stay unchanged.

## Divergences and ledger effect

No new divergence: this makes PiG's public API match Pi's `ExtensionFactory` where Pi has it. The one deliberate difference stays what it is today: Pi's file loader imports an extension file into the host process with jiti (loader.ts:559-593), and PiG's file loader is subprocess-only. D19 (`docs/additive-features.md`, multi-language subprocess SDK bridges: "Upstream pi ... loads extensions in-process") records that mechanism, and its observable consequences have their own numbers (D56, D70, D73, D83, D89, D94). No new number is needed.

Interface rows this unblocks (all `pending` in `mapping-v1.1.0.json` today): `ExtensionFactory`, `ExtensionFactory::call:0`, `ExtensionHandler`, `ExtensionHandler::call:0`, `InlineExtension`, `createCodemodeExtension`, `createCodemodeExtension::call:0`, `createToolSearchExtension`, `createToolSearchExtension::call:0`, `MainOptions`, `MainOptions::property:extensionFactories`, `main`, `main::call:0`. `createMcpExtension` is `ported` today on the narrower `mcpext.API` and must be re-derived after the signature change. The `ExtensionUIContext` editor and widget factory rows belong to commit `e423b16423`.

## Required tests (step 2)

1. **Pi's loader tests**, same inputs and expected results: 8423 (`discards runtime changes and disables the failed API`, `does not discard a concurrently loaded factory's provider`), 6260 (inline naming), and the `extensionFactories` and `Unknown built-in extension` cases of `resource-loader.test.ts`.
2. **Pi's built-in tests**: `agent-session-codemode.test.ts`, `tool-search.test.ts`, `llama-extension.test.ts`, `mcp-extension.test.ts`, each through the production loader.
3. **Production path**: a Session test through `cmd/pig`'s extension set proves each built-in registers its tool inactive, that `-builtin:<name>` and `--no-extensions` remove it, and that a third-party tool of the same name replaces it (`replaceable`).
4. **Lifetime**: an API captured by a compiled-in factory fails with Pi's stale message after `/reload` and after `newSession`, and the factory runs again for the new runtime. A factory that panics yields a load error and the Session continues.
5. **Negative (subprocess cannot reach the factory form).** The test binary registers a sentinel compiled-in factory as a built-in row and as a named inline row; each increments its own counter. A positive control (`-e builtin:sentinel` from the host's arguments) must increment the built-in counter, so a sentinel that never runs cannot pass. Then a real subprocess extension in each SDK (Node, Go, Python, Rust, isolated and packed) and a D31 fused Go extension tries every route it has: registering a tool, command, and flag named `builtin:sentinel` and `<inline:sentinel>`; a registration payload with an extra field naming the factory; a tool result and an `appendEntry` naming it; `setActiveTools(["builtin:sentinel"])`; a Package manifest listing `builtin:sentinel`; and `ctx.reload()`. Each route ends with Pi's result for that input (an error, an inert name, or an ignored field), and both sentinel counters keep the value the positive control left. The test fails if either counter changes.
6. **Import graph and symbol reference** (property 3), over the package set named there, transitive.
7. **No package-level registry** (property 1): an AST check over the module's non-test files fails on a package-level variable, or an exported function that stores its argument in package state, whose type contains `extension.ExtensionFactory` or `extension.InlineExtension`.
8. **Piglet builder** (property 4): a test builds an overlay for a Piglet with a fused member and a frontend member and asserts its Go-source writes are exactly the two generated registries. A `fused_vet` test rejects a member that imports the loader package or `coding/extension/builtin`, or references `extension.ExtensionFactory`.

Each test must fail when its property is broken. Record the red evidence in the step-2 change.

## Order of work

1. This specification and the AGENTS.md amendment, reviewed (done, `Review record`).
2. `extension.ExtensionFactory` and `InlineExtension`, the loader package with Pi's `createExtensionAPI`, every built-in row on the factory form, Pi's tests ported, and tests 1-8.
3. Compiled-in UI component factories through the production host (largely landed by `e423b16423`).
4. Embedder entry point after the owner confirms it.
5. Phase 2 specification and implementation of the declarative component form.

## Review record

Opus spec review, 2026-10-08, of commit `34b021eaaa`. Verdict: ACCEPT-WITH-FIXES. The fixes are applied in this text.

Findings:

1. **Piglet members against the ruling.** The original classification said "A Piglet or an embedding program selects it at build time", which would let a Piglet link third-party code through the factory form. Fixed: first party is defined by linkage (`Who is first party`), Piglets never reach the form, and property 4 and test 8 enforce it through the builder and `fused_vet`.
2. **Mutable registry.** The original did not forbid a package-level factory registry. A global registry with an exported or `init`-time `Register` would let any linked package, including a fused member, add a factory. `subprocess.SetFusedResolver` shows the pattern exists in the tree. Fixed: property 1 and test 7.
3. **Loader location.** `coding/extension/host/subprocess` transitively imports `coding/extension` and `coding/extension/host/inproc`. A loader placed in either package fails property 3 on day one, and an import-graph test cannot see a reference to a type in a package the subprocess side legitimately imports. Fixed: the loader's package is constrained, the test is transitive, and a symbol-reference check is added.
4. **Selection routes.** The original listed "a package manifest ... or a wire message" as ways to select a built-in. Pi selects built-ins only through settings and `-e` (`package-manager.ts:970-1000`). Fixed.
5. **Loader semantics.** The original omitted Pi's commit/discard transaction, the failed-API message, concurrent loads on a shared runtime, re-running factories on reload and replacement with stale invalidation, bare-factory `<inline:N>` naming, and exception capture. It also did not say that no production `extension.API` implementation exists yet. Fixed in `Compiled-in factory form` and tests 1 and 4.
6. **Scope.** Fixed so that llama.cpp, pig-login, and `PackageCommandRuntimeOptions.extensionFactories` also move to the one factory mechanism.
7. **Step 3 was stale.** Commit `e423b16423` landed the typed UI factory members after `34b021eaaa`. The proposed `EditorFactory` took `Theme`, but Pi's takes `EditorTheme`. Fixed.
8. **Citations.** Fixed: `ExtensionAPI` is types.ts:1564-1890, not 1066-1296. `createCodemodeExtension` is codemode/index.ts:31-48. The type is `builtin.Extension`, not `builtin.Entry`. The ledger rows are `pending`, not designed-out. The cited ruling record did not contain ruling B, so its text is now stated here. `createMcpExtension`'s row is `ported` on a narrower interface.
9. **Negative test.** The original test had no sentinel value and no positive control, so a test that observed nothing could pass. Fixed in test 5.
10. **Divergence question.** D19 records the subprocess-only file loader, and D56, D70, D73, D83, D89 and D94 record its observable consequences. No new number.
11. **AGENTS.md wording.** "Go code the binary was built with, selected only by the binary's source" also describes a D31 fused member and a Piglet's generated registry. "Nothing a ... settings entry ... can reach it" contradicts Pi's `-builtin:<name>` and `-e builtin:<name>`. Fixed in both documents. `docs/extension-authoring.md` now separates `sdk.Factory` from `extension.ExtensionFactory`.

Answers to the author's questions:

1. Use `func(api API) error` with no `context.Context`. Pi's factory receives no signal, and the loader owns lifetime. Map a thrown exception to the error and recover a panic into it.
2. The trust rules for the embedder entry point belong in this specification, and they now say who may use it. `DefaultResourceLoaderOptions.ExtensionFactories` follows step 2, and `Main` is a separate change. Both wait for the owner's confirmation, because ruling B names only the pig binary.
3. An import-graph test alone is not sufficient. Use properties 1, 3 and 4 together: value-only threading from `main` with no global registry, a transitive import test plus a symbol-reference check, and the Piglet builder and `fused_vet` checks. Do not use a build tag. A tag that enables the form is a build-configuration switch that a Piglet build could set. Go's `internal/` rule is not a boundary here either. It is path-based, so a fused member module whose path lies under `github.com/MichaelKinsy/PiG` passes it. The embedder entry also needs a public package.
