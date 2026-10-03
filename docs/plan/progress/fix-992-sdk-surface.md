# fix-992-sdk-surface: close the 27 unexcepted sdk-surface missing cells

Base: `staging/porter/pi-0.99.1` @ 8d1d6c896 (upstream pin 0.99.2). Source list: `docs/plan/progress/fix-992-hygiene.md` on `fix-992-hygiene`. `go run ./test/parity/cmd/sdksurface -check` reported 27 unexcepted missing cells.

## Classification of the 27 cells

| cells | cause | closure |
|---|---|---|
| `tool execute result.structuredContent` (Node) | Node already reads and sends `structuredContent` (`normalizeToolResult`, runtime.mjs:3436); the map row says `node = "-"` | map row only |
| `provider.images`, `provider.classifiers` (Go, Rust, Python) | implemented (`image_apis`/`classifier_apis` on the register frame, `provider_operation` requests); the map rows say `-` | map rows name the symbols |
| `provider.images`, `provider.classifiers` (Node) | not implemented: the callbacks were dropped | Node runtime registers and serves them |
| `ctx.modelRegistry.{findOfType,getModelsOfType,getAvailableOfType,getModelOfType,classify,registerVirtualModel,unregisterVirtualModel}` (Node) | `RuntimeModelRegistry` lacks them; the host calls exist (Go, Rust, Python use them) | Node runtime |
| `ctx.ui.theme.{appearance,colors}` (all four), `ctx.ui.theme.style` (Go, Python, Rust; Node's `style` is an internal helper with another signature) | the host palette carries neither the appearance nor the concrete colors, and no SDK has `Theme.style` | host palette + four SDKs |

## Red (tests with signature stubs)

Commit `test(sdk-surface): port upstream 0.99.2 SDK-surface tests with signature stubs (red)`.

Upstream tests in scope: `packages/coding-agent/test/theme-style.test.ts` (token styling, unknown/wrong-slot tokens, OKLCH, appearance, terminal-default colors), whose inputs the SDK tests reuse. The SDK-language behavior has no upstream test (Pi's TypeScript is the reference implementation), so the typed-model rows (`model-registry.ts:71-79,135-177,211-217`, `types.ts:1896-1903`) are conformance rows with new inputs.

Stubs: `extensions/sdk/ui_theme.go` (`Color`, `ThemeStyle`, `UITheme.Appearance/Colors/Style` return zero values), `extensions/sdk-rs/src/theme.rs` (`Color`, `ThemeStyle`, `Theme::appearance/colors/style`; the private `style` helper became `modifier` so the public name is free), `extensions/sdk-py/pig_sdk/__init__.py` (`Theme.appearance/colors/style`; private `_style` became `_modifier`), `internal/codingagent/ext_ui_context.go` (`extensionThemePalette` exported as `ExtensionThemePalette` for the conformance test; no behavior change).

Red run (all failures are behavior failures, none a compile error):

- `extensions/sdk`: `go test . -run UITheme`: 4 tests fail (appearance `""`, every `Style` returns `""`).
- `extensions/sdk-rs`: `cargo test --lib theme`: 3 of 4 fail (`appearance` None, `style` returns `Ok("")`).
- `extensions/sdk-py`: `pytest tests/test_theme_style.py`: every case fails (`style` returns `""`, appearance `None`).
- `coding/extension/host/subprocess`: `TestNodeModelRegistryTypedOperations`, `TestNodeModelRegistryVirtualModels` (`registry.getModelsOfType is not a function`), `TestNodeProviderConfigImagesAndClassifiers` (`image_apis` undefined), `TestNodeThemeAppearanceColorsAndStyle` (`theme.colors` undefined).
- `internal/codingagent`: `TestExtensionThemePaletteCarriesAppearanceColorsAndMode` (no `appearance`, no `colors`).
- `test/extension-conformance`: `TestThemeAcrossSDKs` fails for Go (fused, strict, packed), Rust, Python and Node; `TestModelTypes*AcrossSDKs` pass for Go, Rust and Python and fail for `node-isolated` and `node-packed` (Node is a new placement of the same table).

## Scope change (lead)

The lead moved the `ctx.ui.theme.appearance`, `colors` and `style` cluster (all four SDKs, the host palette and the Node `ThemeShim`) to lane `fix-992-sdk-theme`, based on the red commit above. This lane keeps the Node `ctx.modelRegistry` cells, `provider.images`/`provider.classifiers`, `structuredContent` and the map rows. The theme tests, stubs and fixtures of the red commit stay as they are and stay red; no theme production code is in this branch. A working implementation of the cluster (Go, Rust, Python SDKs, Node `ThemeShim`, host palette, benchmark) was written before the split and is saved, unreviewed and unmerged, in the lane workspace for the new lane to reuse or discard.

Because the red stubs compile, the matrix reports the Python and Rust theme cells as implemented. Only the Go and Node cells are reported missing, so `sdk-surface-drift` is red on 5 theme problems until the theme lane lands.

## Green

Closed cells (matrix rows in `test/parity/sdk-surface.toml`, implementation in `runtime-node/runtime.mjs`):

- `tool execute result.structuredContent` (Node): the runtime already sent it (`normalizeToolResult`); the map row said `-`. Now `read:result.structuredContent`.
- `provider.images`, `provider.classifiers`: Go, Rust and Python already implemented them (`image_apis`/`classifier_apis`, `provider_operation`); the rows now name `ProviderImagesFunc`/`ProviderClassifyFunc`, `ProviderOperations.images`/`classifiers` and `Extension.register_provider`, with the wire `subprocess.ProviderDecl.image_apis`/`classifier_apis`. Node implements them: `registerProvider` strips `images`/`classifiers` from the serialized config and keeps them per provider (`providerOperations`, merged over the previous root as Pi merges defined values, `model-runtime.ts:753-766`); the register frame names the APIs; the `provider_operation` request runs the callback with the request's signal and answers its result or error.
- `ctx.modelRegistry.findOfType`, `getModelsOfType`, `getModelOfType` (synchronous reads of `models` and `typedModels`, `model-registry.ts:145-161`), `getAvailableOfType`, `classify` (never rejects; an aborted signal returns an `aborted` result; failures use Pi's own `classifierErrorResult`), `registerVirtualModel`, `unregisterVirtualModel` (the runtime's). `classify` and `getAvailableOfType` join `WINDOW_CLOSING_CALLS`, as the other I/O host calls are.
- Related gap closed on the way: a Node Provider object's `generateImages`/`classify` (`native-provider.mjs`, `provider-object.mjs`) so `TestModelTypesProviderObjectAcrossSDKs` covers Node. It is not a matrix cell.
- `knownCapabilityGaps` entries `classify` and `getAvailableOfType` (Node) deleted: the gate requires it once the Node runtime calls them.

Edits to red-commit tests, all to my own tests and none to a Pi port: the Node provider-operation test gives its request contexts the request signal the dispatcher always installs (`runtime.mjs` request loop); the conformance rig's `GetModels` returns the catalog its registry state publishes (the Node runtime installs the ready frame's catalog, which was empty in the rig and replaced the typed reads' chat models in the packed placement); the conformance rig test file otherwise only gains the Node placements.

Mutation checks (each fails the named test, then restored): `classifierErrorResult(..., false)` fails `TestNodeModelRegistryTypedOperations`; dropping `providerOperations.delete` in `unregisterProvider` fails `TestNodeProviderConfigImagesAndClassifiers`.

Load: `GOMAXPROCS=4 go test -race -count=24` of the Node model-registry and provider-operation tests, `taskset -c 0-3 -count=12`, and `-race -count=3` of the Node placements of `TestModelTypes*` on four cores with four CPU burners all pass.

Not done and why: a provider registered after the factory finished has no `images`, `classifiers` or `streamSimple` implementation in any SDK, because the host's `registerProvider` call carries no operation declaration. This pre-dates the lane and affects `streamSimple` the same way; recorded as a QUESTION.
